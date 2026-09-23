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
	seen, err := readSeen(in.Root)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: seen.tsv cannot be read, so nothing is admitted: %s\n", oneline.Err(err))
		return 2
	}
	queued, err := dropHeldCards(in.Root, readQueuedCards(queuePath), seen)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}

	ordered := readCandidates(filepath.Join(in.Root, "next.tsv"))
	ordered = append(ordered, readCandidates(filepath.Join(in.Root, "pool.tsv"))...)
	ordered, err = dropHeldCandidates(in.Root, ordered, seen)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}

	if len(queued) == 0 && len(ordered) == 0 {
		fmt.Fprintf(in.Stdout, "PULSE POOL EMPTY in-flight=0\n")
		return 0
	}

	// The width and the deadline the pulse being harvested ran with (issue #1819). This
	// ran `nova-pulse launch` with neither, and launch requires both, so rule 15's
	// pulse-again could only ever print launch's own refusal of it. It is not guessed:
	// launch wrote them down beside the pulse.
	slots, deadline, store, owner, ok := readPulseShape(in.Root, in.ID)
	if !ok {
		fmt.Fprintf(in.Stderr, "PULSE NOTE relaunch skipped: %s does not record this pulse's width, deadline and bench slot lease, and launch requires --slots --deadline --slots-store --owner; run nova-pulse launch yourself for the %d card(s) waiting\n",
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
		body := render(c, in.Templates)
		if matches, merr := identitiesFor(in.Root, c.ID); merr == nil && len(matches) == 1 {
			body += fmt.Sprintf("IDENTITY kind=%s id=%s attempt=%d\n", matches[0].Kind, matches[0].ID, matches[0].Attempt)
		}
		if err := os.WriteFile(cardPath, []byte(body), 0o644); err != nil {
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

	return runLaunchSubprocess(in, filepath.Join(in.Root, "cards.tsv"), slots, deadline, store, owner)
}

func dropHeldCards(root string, cards []CardRow, seen map[string]bool) ([]CardRow, error) {
	var out []CardRow
	for _, c := range cards {
		kind, id, ambiguous, err := heldIdentity(root, c.Label, c.Card)
		if err != nil {
			return nil, err
		}
		if ambiguous || (kind != "" && seen[kind+"\x00"+id]) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func dropHeldCandidates(root string, cands []PoolRow, seen map[string]bool) ([]PoolRow, error) {
	var out []PoolRow
	for _, c := range cands {
		kind, id, ambiguous, err := heldIdentity(root, c.ID, "")
		if err != nil {
			return nil, err
		}
		if ambiguous || (kind != "" && seen[kind+"\x00"+id]) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// heldIdentity is the source kind and id a card or candidate was admitted
// under. The card's own IDENTITY line wins. A bare label that matches two
// sources is not guessed: the card is not admitted.
func heldIdentity(root, label, cardPath string) (kind, id string, ambiguous bool, err error) {
	if a, ok := cardIdentity(cardPath); ok {
		return a.Kind, a.ID, false, nil
	}
	k, i, _, _, err := lookupIdentity(root, label)
	if err != nil {
		if strings.Contains(err.Error(), "ambiguous") {
			return "", "", true, nil
		}
		return "", "", false, err
	}
	return k, i, false, nil
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

// readPulseShape reads back the width, deadline and bench slot lease `launch` recorded
// for this pulse: <root>/pulses/<id>.tsv,
// `pulse-<id><TAB><cards><TAB><slots><TAB><deadline><TAB><store><TAB><owner>`. A row
// written before issue #1819 has neither width nor deadline; a row written before
// #1903 has no lease. Either way the answer is false rather than inventing a number
// or a store.
func readPulseShape(root, id string) (slots int, deadline, store, owner string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(root, "pulses", id+".tsv"))
	if err != nil {
		return 0, "", "", "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		p := strings.Split(strings.TrimSpace(line), "\t")
		if len(p) < 6 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(p[2]))
		if err != nil || n < 1 {
			continue
		}
		d := strings.TrimSpace(p[3])
		s := strings.TrimSpace(p[4])
		o := strings.TrimSpace(p[5])
		if d == "" || s == "" || o == "" {
			continue
		}
		slots, deadline, store, owner, ok = n, d, s, o, true
	}
	return slots, deadline, store, owner, ok
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
func runLaunchSubprocess(in HarvestInput, cardsTSV string, slots int, deadline, store, owner string) int {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-pulse", "launch", "--cards", cardsTSV, "--root", in.Root, "--queue",
		"--slots", strconv.Itoa(slots), "--deadline", deadline,
		"--slots-store", store, "--owner", owner)
	cmd.Stdout = in.Stdout
	cmd.Stderr = in.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE launch failed: %s\n", oneline.Err(err))
		return 1
	}
	return 0
}
