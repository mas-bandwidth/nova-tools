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

	"github.com/mas-bandwidth/nova-tools/internal/log"
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
	Files    int    // the --files budget every card in the batch carries; 0 takes the default
	Tokens   string // the --tokens budget; empty takes the default
	QueueDir string // the queue directory STOP lives in; empty falls back to Root (admission.go)
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
	// Log is where the structured JSON event line goes, beside the stdout line and never
	// instead of it. nil writes no JSON line, which is how the tests that predate the
	// slice keep their exact stdout and stderr; cmd/nova-pulse passes stderr, which on a
	// bench is the systemd journal.
	Log io.Writer
	// GUID is the source of this run's guid. nil reads the real /proc in production; a
	// test injects a fixed one so it reads no /proc.
	GUID func() string
}

// sliceTimeout is rule 8's quiet window: a slot whose native.log was written within this
// window still holds a live native and is not free.
const sliceTimeout = 120 * time.Second

// nativeRunner is the command nova-swarm batch starts once per card, given label, slot,
// model, card path and root. It is the runner the deployment keeps on PATH -- the same one
// the run-batch.sh shim execs -- and launch has no flag of its own for one, so this name is
// the whole answer to the batch admission's runner.
const nativeRunner = "nova-native-runner.sh"

// Launch allocates free slots, admits the cards that fit as one nova-swarm batch in its
// CARD form (--id --cards --deadline --runner --root --files, the only form that runs a
// card; the POOL form wants --tokens as well, which no launch flag supplies, issue #630),
// and queues the rest when --queue is set. The card form still carries the file budget
// `nova-swarm batch` refuses an admission without (issue #869). It returns the process
// exit code: 0 when the pulse was admitted, 2 when it could not be.
func Launch(in LaunchInput) int {
	cards, err := readCards(in.Cards)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if len(cards) == 0 {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s holds no card; a pulse of no cards is a typo; name a cards file with at least one row\n", oneline.Field(in.Cards))
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
		in.event("refuse", fmt.Sprintf("launch: %d cards over %d free slots", n, free), 0, nil)
		return 2
	}

	id := swarm.NewID(in.Now(), "pulse")
	started := in.Now()
	in.event("start", "launch: pulse "+id, 0, nil)
	goCards := cards
	queued := 0
	if n > free {
		goCards = cards[:free]
		queued = n - free
	}

	batches := 0
	if len(goCards) > 0 {
		if err := os.MkdirAll(filepath.Join(in.Root, "cards", id), 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		// The admitted cards become their own cards.tsv, so the batch runs exactly the
		// cards that fit the free slots and never the queued remainder.
		admitted := filepath.Join(in.Root, "cards", id, "cards.tsv")
		if err := writeCardsTSV(admitted, goCards); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		if err := os.MkdirAll(filepath.Join(in.Root, "pulses"), 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		if !runBatch(in, id, admitted) {
			return 2
		}
		record(in.Root, id, len(goCards))
		batches = 1
	}

	if queued > 0 {
		if err := queueRemainder(in.Root, cards[free:]); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
	}

	fmt.Fprintf(in.Stdout, "PULSE OK id=%s n=%d free-before=%d queued=%d batches=%d deadline=%s\n",
		oneline.Field(id), n, free, queued, batches, oneline.Field(in.Deadline))
	in.event("done", fmt.Sprintf("launch: pulse %s cards=%d", id, n), in.Now().Sub(started), nil)
	return 0
}

// event writes the structured JSON line BESIDE the stdout event line, through internal/log.
// It writes nothing when the caller configured no Log writer, which is how every test that
// predates the slice keeps its exact stdout and stderr. The stdout line is written by the
// caller and is never touched here: the JSON line is additive, and a reader that only knows
// the stdout event still works.
func (in LaunchInput) event(event, msg string, dur time.Duration, err error) {
	if in.Log == nil {
		return
	}
	clock := in.Now
	if clock == nil {
		clock = time.Now
	}
	guid := in.GUID
	if guid == nil {
		guid = log.ProcessGUID
	}
	l := log.New(clock, guid, "nova-pulse")
	l.Verb = "launch"
	l.Event = event
	l.Msg = msg
	l.DurMS = dur.Milliseconds()
	if err != nil {
		l.Err = err.Error()
	}
	_ = l.Write(in.Log)
}

// runBatch admits one batch as the swarm's CARD form of nova-swarm batch and relays a swarm
// refusal as a PULSE REFUSED, queueing nothing.
func runBatch(in LaunchInput, id, cardsPath string) bool {
	then := fmt.Sprintf("nova-pulse harvest --id %s --root %s", id, in.Root)
	// The file budget, because `nova-swarm batch` refuses an admission that names none
	// ("--files is required and is at least 1, got 0") and a launch with no configured
	// budget would otherwise send the zero the swarm reads as "refusing to guess". A
	// LaunchInput carrying none still names the documented default (issue #869).
	files := in.Files
	if files < 1 {
		files = DefaultLaunchFiles
	}
	var out, errb bytes.Buffer
	cmd := exec.Command("nova-swarm", "batch",
		"--id", id,
		"--cards", cardsPath,
		"--deadline", in.Deadline,
		"--runner", nativeRunner,
		"--root", in.Root,
		"--files", strconv.Itoa(files),
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

// record appends one row to <root>/pulses/<id>.tsv naming the batch: its id and card count.
func record(root, id string, n int) {
	f, err := os.OpenFile(filepath.Join(root, "pulses", id+".tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pulse-%s\t%d\n", id, n)
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
