package pulse

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		model := modelFor(c.Kind, "", relaunchTiers)
		if c.Kind == "read" || c.Kind == "tone" {
			if m := cheapestReadModel(in.Templates); m != "" {
				model = m
			}
		}
		rows = append(rows, CardRow{Label: label, Slot: "-", Model: model, Card: cardPath})
	}
	if err := writeCardsTSV(filepath.Join(in.Root, "cards.tsv"), rows); err != nil {
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

// cheapestReadModel is the bench cost table's cheapest route that can hold a
// read card (SPEC-PULSE rules 7 and 13): the capable model with the lowest
// average cost per token — zero beats flat beats metered, ties broken by usd
// per Mtok — so the zero-cost local model comes first and reads stay off the
// paid routes. It reads benches.tsv beside the templates directory, or
// routes.tsv when that is what is there, in the cost table's own shape (one
// column per model; a cost row carrying `<class> <usd per Mtok>` and a
// capability row). A read card needs only read capability, which every known
// capability covers. It reports "" when neither file can be read, and the
// caller then keeps the kind-only route it has always written.
func cheapestReadModel(templatesDir string) string {
	raw, err := os.ReadFile(filepath.Join(templatesDir, "benches.tsv"))
	if err != nil {
		raw, err = os.ReadFile(filepath.Join(templatesDir, "routes.tsv"))
		if err != nil {
			return ""
		}
	}
	var header, costs, caps []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		p := strings.Split(line, "\t")
		if len(p) < 2 {
			continue
		}
		switch strings.TrimSpace(p[0]) {
		case "model":
			header = p
		case "cost":
			costs = p
		case "capability":
			caps = p
		}
	}
	if len(header) == 0 || len(costs) != len(header) || len(caps) != len(header) {
		return ""
	}
	best, bestRank, bestUSD := "", 99, 0.0
	for i := 1; i < len(header); i++ {
		name := strings.TrimSpace(header[i])
		if name == "" {
			continue
		}
		switch strings.TrimSpace(caps[i]) {
		case "read", "text", "code", "replay":
		default:
			continue
		}
		rank := 99
		usd := 0.0
		if f := strings.Fields(costs[i]); len(f) > 0 {
			switch f[0] {
			case "zero":
				rank = 0
			case "flat":
				rank = 1
			case "metered":
				rank = 2
			}
			if len(f) > 1 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil {
					usd = v
				}
			}
		}
		if rank > 2 {
			continue
		}
		if best == "" || rank < bestRank || (rank == bestRank && usd < bestUSD) {
			best, bestRank, bestUSD = name, rank, usd
		}
	}
	return best
}

// relaunchTiers names the two tiers a relaunch falls back to when no bench
// cost table sits beside the templates directory: by kind alone through cut's
// modelFor, the same "flash"/"pro" it has always written.
var relaunchTiers = map[string]string{"flash": "flash", "pro": "pro"}

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
