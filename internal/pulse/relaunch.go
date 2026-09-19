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
	// queue.tsv FIRST, and in the grammar launch writes (issue #1820). `launch --queue`
	// writes its overflow as CARD ROWS -- label, model, card path -- and this read them
	// with readCandidates, which wants a five-field candidate table and skips any row
	// with fewer than four fields. Every card launch queued was dropped on the floor and
	// `PULSE POOL EMPTY` printed over the top of it. They are already cut cards: they do
	// not want rendering from a template, they want admitting.
	queuePath := filepath.Join(in.Root, "queue.tsv")
	queued := readQueuedCards(queuePath)

	ordered := readCandidates(filepath.Join(in.Root, "next.tsv"))
	ordered = append(ordered, readCandidates(filepath.Join(in.Root, "pool.tsv"))...)

	if len(queued) == 0 && len(ordered) == 0 {
		fmt.Fprintf(in.Stdout, "PULSE POOL EMPTY in-flight=0\n")
		return 0
	}

	// The width and the deadline the pulse being harvested ran with (issue #1819). This
	// ran `nova-pulse launch` with neither, and launch requires both, so rule 15's
	// pulse-again could only ever print launch's own refusal of it. It is not guessed:
	// launch wrote them down beside the pulse.
	slots, deadline, ok := readPulseShape(in.Root, in.ID)
	if !ok {
		fmt.Fprintf(in.Stderr, "PULSE NOTE relaunch skipped: %s does not record this pulse's width and deadline, and launch requires --slots and --deadline; run nova-pulse launch yourself for the %d card(s) waiting\n",
			oneline.Field(filepath.Join(in.Root, "pulses", in.ID+".tsv")), len(queued)+len(ordered))
		return 0
	}

	cardsDir := filepath.Join(in.Root, "cards", in.ID)
	if err := os.MkdirAll(cardsDir, 0o755); err != nil {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("cannot make cards dir: %s", oneline.Err(err)))
	}
	table := fallbackRoutes
	if t, err := readCostTable(in.Templates); err == nil {
		table = t
	}
	rows := queued
	for i, c := range ordered {
		label := sanitizeID(c.ID)
		cardPath := filepath.Join(cardsDir, fmt.Sprintf("%03d-%s.md", i, label))
		if err := os.WriteFile(cardPath, []byte(render(c, in.Templates)), 0o644); err != nil {
			return refusal(in.Stderr, "HARVEST", fmt.Errorf("cannot cut %s: %s", label, oneline.Err(err)))
		}
		rows = append(rows, CardRow{Label: label, Slot: "-", Model: routeFor(c.Kind, false, table).Name, Card: cardPath})
	}
	if err := writeCardsTSV(filepath.Join(in.Root, "cards.tsv"), rows); err != nil {
		return refusal(in.Stderr, "HARVEST", err)
	}
	// The queue rows are now in the table being launched, so the queue is taken -- moved
	// aside, never deleted, because the relaunch may refuse and a queue that was erased
	// on the way to a refusal is work nobody can find. `launch --queue` writes a fresh
	// queue.tsv for whatever overflows this time.
	if len(queued) > 0 {
		_ = os.Rename(queuePath, queuePath+".taken-"+sanitizeID(in.ID))
	}

	return runLaunchSubprocess(in, filepath.Join(in.Root, "cards.tsv"), slots, deadline)
}

// readQueuedCards reads the table `launch --queue` writes: label<TAB>model<TAB>card, the
// three fields queueRemainder emits, and the four-field card form too so one reader answers
// for both spellings (issue #1820). A row naming a card file that is not there is skipped:
// the queue outlives the cards directory of a root someone cleaned.
func readQueuedCards(path string) []CardRow {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []CardRow
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, "\t")
		var row CardRow
		switch len(p) {
		case 3:
			row = CardRow{Label: p[0], Slot: SlotDash, Model: p[1], Card: p[2]}
		case 4:
			row = CardRow{Label: p[0], Slot: p[1], Model: p[2], Card: p[3]}
		default:
			continue
		}
		if strings.TrimSpace(row.Label) == "" || strings.TrimSpace(row.Card) == "" {
			continue
		}
		if _, err := os.Stat(row.Card); err != nil {
			continue
		}
		out = append(out, row)
	}
	return out
}

// readPulseShape reads back the width and deadline `launch` recorded for this pulse:
// <root>/pulses/<id>.tsv, `pulse-<id><TAB><cards><TAB><slots><TAB><deadline>`. A two-field
// row -- every row written before issue #1819 -- records neither, and says so by answering
// false rather than by inventing a number.
func readPulseShape(root, id string) (slots int, deadline string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(root, "pulses", id+".tsv"))
	if err != nil {
		return 0, "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		p := strings.Split(strings.TrimSpace(line), "\t")
		if len(p) < 4 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(p[2]))
		if err != nil || n < 1 {
			continue
		}
		d := strings.TrimSpace(p[3])
		if d == "" {
			continue
		}
		slots, deadline, ok = n, d, true
	}
	return slots, deadline, ok
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

// runLaunchSubprocess invokes nova-pulse launch --queue as a subprocess and relays its output, so the
// batch admission goes through the launch verb, never re-implemented here.
func runLaunchSubprocess(in HarvestInput, cardsTSV string, slots int, deadline string) int {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-pulse", "launch", "--cards", cardsTSV, "--root", in.Root, "--queue",
		"--slots", strconv.Itoa(slots), "--deadline", deadline)
	cmd.Stdout = in.Stdout
	cmd.Stderr = in.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE launch failed: %s\n", oneline.Err(err))
		return 1
	}
	return 0
}
