package pulse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// LaunchInput is everything the launch verb needs, held apart from the command-line parsing
// so a test can drive it with fake directories and a fake nova-swarm on PATH.
type LaunchInput struct {
	Cards    string // path to cards.tsv: label<TAB>slot<TAB>model<TAB>card
	Root     string // the pulse root; the swarm pool lives under <root>/pool
	Slots    int    // the ceiling on free slots launch considers
	Deadline string // the whole pulse's deadline, whole seconds
	Queue    bool   // true keeps the overflow in queue.tsv instead of refusing
	QueueDir string // the queue directory STOP lives in; empty falls back to Root (admission.go)
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
}

// sliceTimeout is rule 8's quiet window: a slot whose native.log was written within this
// window still holds a live native and is not free.
const sliceTimeout = 120 * time.Second

// Launch allocates free slots, admits the cards that fit as nova-swarm batch (one per model
// route, with --then nova-pulse harvest), and queues the rest when --queue is set. It
// returns the process exit code: 0 when the pulse was admitted, 2 when it could not be.
func Launch(in LaunchInput) int {
	cards, err := readCards(in.Cards)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if len(cards) == 0 {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s holds no card; a pulse of no cards is a typo\n", oneline.Field(in.Cards))
		return 2
	}
	// STOP admission (SPEC-PULSE class C): while the bench is red, only the cards whose
	// line 1 names the red launch. No STOP is no filtering and no line -- the launch path
	// before this card, unchanged.
	if stop := readStopFor(in); len(stop) > 0 {
		admitted, refused := admitCards(cards, stop, in.Stderr)
		if refused > 0 && len(admitted) == 0 {
			fmt.Fprintf(in.Stdout, "PULSE STOP admitted=0 refused=%d admit=%s (the gate's STOP admits only the red's own card)\n",
				refused, oneline.Field(dash(AdmissionName(stop))))
			return 0
		}
		cards = admitted
	}

	free := freeSlots(in.Root, in.Slots, in.Now())
	n := len(cards)
	if n > free && !in.Queue {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED UNDER-SLOTS cards=%d free=%d (pass --queue, or wait)\n", n, free)
		return 2
	}

	id := swarm.NewID(in.Now(), "pulse")
	goCards := cards
	queued := 0
	if n > free {
		goCards = cards[:free]
		queued = n - free
	}

	benches := groupByModel(goCards)
	if err := os.MkdirAll(filepath.Join(in.Root, "pulses"), 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}

	for _, b := range benches {
		tasksDir := filepath.Join(in.Root, "cards", id, swarm.Slug(b.Model))
		if err := os.MkdirAll(tasksDir, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		for i, c := range b.Cards {
			raw, err := os.ReadFile(c.Card)
			if err != nil {
				fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
				return 2
			}
			if err := os.WriteFile(filepath.Join(tasksDir, strconv.Itoa(i)), raw, 0o644); err != nil {
				fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
				return 2
			}
		}
		if !runBatch(in, id, b.Model, tasksDir) {
			return 2
		}
		record(in.Root, id, b.Model, len(b.Cards))
	}

	if queued > 0 {
		if err := queueRemainder(in.Root, cards[free:]); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
	}

	fmt.Fprintf(in.Stdout, "PULSE OK id=%s n=%d free-before=%d queued=%d batches=%d deadline=%s\n",
		oneline.Field(id), n, free, queued, len(benches), oneline.Field(in.Deadline))
	return 0
}

// runBatch admits one model route as one nova-swarm batch and relays a swarm refusal as a
// PULSE REFUSED, queueing nothing.
func runBatch(in LaunchInput, id, model, tasksDir string) bool {
	then := fmt.Sprintf("nova-pulse harvest --id %s --root %s", id, in.Root)
	pool := filepath.Join(in.Root, "pool")
	var out, errb bytes.Buffer
	cmd := exec.Command("nova-swarm", "batch",
		"--pool", pool,
		"--tasks", tasksDir,
		"--label", "pulse-"+id,
		"--deadline", in.Deadline,
		"--then", then)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(errb.String())
		if reason == "" {
			reason = oneline.Err(err)
		}
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Cap(reason, oneline.TailBytes))
		return false
	}
	return true
}

// record appends one row to <root>/pulses/<id>.tsv naming the batch: its label, model and
// card count.
func record(root, id, model string, n int) {
	f, err := os.OpenFile(filepath.Join(root, "pulses", id+".tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pulse-%s\t%s\t%d\n", id, model, n)
}

// queueRemainder appends each overflow card to <root>/queue.tsv, one row per card.
func queueRemainder(root string, cards []CardRow) error {
	f, err := os.OpenFile(filepath.Join(root, "queue.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, c := range cards {
		if _, err := fmt.Fprintf(f, "%s\t%s\t%s\n", c.Label, c.Model, c.Card); err != nil {
			return err
		}
	}
	return nil
}

// groupByModel folds the cards into benches in cards.tsv order, one bench per distinct model
// in the order the models first appear.
func groupByModel(cards []CardRow) []BenchRow {
	var benches []BenchRow
	seen := map[string]int{}
	for _, c := range cards {
		idx, ok := seen[c.Model]
		if !ok {
			idx = len(benches)
			seen[c.Model] = idx
			benches = append(benches, BenchRow{Model: c.Model})
		}
		benches[idx].Cards = append(benches[idx].Cards, c)
	}
	return benches
}

// freeSlots counts the free slots among the first `slots`, a slot free only when it holds no
// lock (its slot file is absent or state=free) and its native.log has been quiet.
func freeSlots(root string, slots int, now time.Time) int {
	pool := filepath.Join(root, "pool")
	n := 0
	for i := 1; i <= slots; i++ {
		if slotFree(pool, i, now) {
			n++
		}
	}
	return n
}

func slotFree(pool string, n int, now time.Time) bool {
	if raw, err := os.ReadFile(filepath.Join(pool, "slots", strconv.Itoa(n)+".json")); err == nil {
		var sf struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(raw, &sf)
		if sf.State != "free" {
			return false
		}
	}
	if fi, err := os.Stat(filepath.Join(pool, strconv.Itoa(n), "native.log")); err == nil {
		if now.Sub(fi.ModTime()) < sliceTimeout {
			return false
		}
	}
	return true
}
