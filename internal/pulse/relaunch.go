package pulse

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// relaunch is harvest's last act (SPEC-PULSE rule 15): pool, cut and launch --queue again,
// with queue.tsv rows first, then next.tsv, then the sources. It never pads: an empty pool
// and queue prints PULSE POOL EMPTY and starts nothing.
func relaunch(in HarvestInput) int {
	ordered := readCandidates(filepath.Join(in.Root, "queue.tsv"))
	ordered = append(ordered, readCandidates(filepath.Join(in.Root, "next.tsv"))...)
	ordered = append(ordered, readCandidates(filepath.Join(in.Root, "pool.tsv"))...)

	if len(ordered) == 0 {
		fmt.Fprintf(in.Stdout, "PULSE POOL EMPTY in-flight=0\n")
		return 0
	}

	cardsDir := filepath.Join(in.Root, "cards", in.ID)
	if err := os.MkdirAll(cardsDir, 0o755); err != nil {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("cannot make cards dir: %s", oneline.Err(err)))
	}
	var rows []CardRow
	for i, c := range ordered {
		label := sanitizeID(c.ID)
		cardPath := filepath.Join(cardsDir, fmt.Sprintf("%03d-%s.md", i, label))
		if err := os.WriteFile(cardPath, []byte(render(c, in.Templates)), 0o644); err != nil {
			return refusal(in.Stderr, "HARVEST", fmt.Errorf("cannot cut %s: %s", label, oneline.Err(err)))
		}
		rows = append(rows, CardRow{Label: label, Slot: "-", Model: modelFor(c.Kind), Card: cardPath})
	}
	if err := writeCardsTSV(in.Root, rows); err != nil {
		return refusal(in.Stderr, "HARVEST", err)
	}

	return runLaunchSubprocess(in, filepath.Join(in.Root, "cards.tsv"))
}

// readCandidates reads a five-field candidate TSV (source, id, kind, title, template).
func readCandidates(path string) []Candidate {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) < 4 {
			continue
		}
		out = append(out, Candidate{Source: p[0], ID: p[1], Kind: p[2], Title: p[3]})
		if len(p) >= 5 {
			out[len(out)-1].Template = p[4]
		}
	}
	return out
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	r := strings.NewReplacer("/", "-", "#", "-", " ", "-", "\t", "-")
	return r.Replace(id)
}

// render produces a card file from a template: the template body plus the title. A missing
// template renders the header the contract needs rather than stalling the pulse.
func render(c Candidate, templatesDir string) string {
	if c.Template != "" && templatesDir != "" {
		if raw, err := os.ReadFile(filepath.Join(templatesDir, c.Template+".md")); err == nil {
			return string(raw) + "\n" + c.Title + "\n"
		}
	}
	return "RESULT " + c.ID + " sha=000000000000\n" + c.Title + "\n"
}

// modelFor routes a card kind to its model: read/text/tone are flash, fix/replay/drift are pro.
func modelFor(kind string) string {
	switch kind {
	case "read", "text", "tone":
		return "flash"
	default:
		return "pro"
	}
}

func writeCardsTSV(root string, rows []CardRow) error {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.Label, r.Slot, r.Model, r.Card)
	}
	return os.WriteFile(filepath.Join(root, "cards.tsv"), []byte(b.String()), 0o644)
}

// runLaunchSubprocess invokes nova-pulse launch --queue as a subprocess and relays its output, so the
// batch admission goes through the launch verb, never re-implemented here.
func runLaunchSubprocess(in HarvestInput, cardsTSV string) int {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-pulse", "launch", "--cards", cardsTSV, "--root", in.Root, "--queue")
	cmd.Stdout = in.Stdout
	cmd.Stderr = in.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE launch failed: %s\n", oneline.Err(err))
		return 1
	}
	return 0
}
