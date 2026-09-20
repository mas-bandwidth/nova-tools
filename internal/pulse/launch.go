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
	// Benches and Bench are handed straight to nova-swarm batch so one pulse can fill more
	// than one bench (docs/SPEC-SWARM.md, "Benches"); empty passes neither flag and the
	// batch runs on this machine exactly as before (issue #637).
	Benches string // the benches table file
	Bench   string // the benches to fill, comma separated
	// Routes is the routes.tsv the typed decision reads to pick each card's worker. Empty
	// means no routing: the cards group by their own model column, exactly as before.
	Routes  string
	Floor   float64 // answers below the floor keep the card's own model as the default worker
	KeyEnv  string  // the environment variable the decision's key comes from
	BaseURL string  // the decision provider endpoint
	// Runner is the native runner `nova-swarm batch` starts once per card. Empty keeps the
	// bare PATH name this verb has always passed; a path is checked here and passed
	// absolute, which is what lets a deployment whose runner is not on PATH launch without
	// touching PATH at all (issue #1760).
	Runner string
	// Swarm is the nova-swarm binary this launch drives. Empty resolves it on PATH -- and
	// whichever binary answers is asked its version before the batch, so a stale one
	// shadowing the current one is refused by name rather than by a puzzling flag error
	// (issue #1760, launchswarm.go).
	Swarm string
	// Version is this nova-pulse's own build version, the other half of that check. Empty
	// runs no probe at all: cmd/nova-pulse always passes one, so a shipped binary always
	// probes, and the tests that predate the check keep their exact argv.
	Version string
	// Attempts is the bound on start-time provider failures. 0 takes DefaultLaunchAttempts.
	Attempts int
	// Max is the ceiling on the cards this invocation considers, the repo's --max
	// convention: 0 means all (docs/SPEC.md, Conventions). It was on the usage line, was
	// parsed, and was then dropped on the floor -- `--max 1` admitted three cards
	// (issue #1821). The rows beyond it are left in the caller's cards.tsv untouched.
	Max int
	// Sleep is the backoff between those attempts; nil is time.Sleep. A test passes one
	// that returns at once, so a bounded retry costs a test no wall clock.
	Sleep  func(time.Duration)
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
	// Log is where the structured JSON event line goes, beside the stdout line and never
	// instead of it. nil writes no JSON line, which is how the tests that predate the
	// slice keep their exact stdout and stderr; cmd/nova-pulse passes stderr, which on a
	// bench is the systemd journal.
	Log io.Writer
	// GUID is the source of this run's guid. nil reads the real /proc in production; a
	// test injects a fixed one so it reads no /proc.
	GUID func() string
}

// RoutesLogFile is the log rule 8 demands: one ROUTE line per card, beside the
// card's label and the time, under the queue directory.
const RoutesLogFile = "ROUTES.log"

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
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s holds no card; a pulse of no cards is a typo\n", oneline.Field(in.Cards))
		return 2
	}
	// --max, before anything is paid for: routing a card costs a model call, and a card
	// over the ceiling is not this invocation's to route, admit or queue (issue #1821).
	if in.Max > 0 && len(cards) > in.Max {
		fmt.Fprintf(in.Stderr, "PULSE NOTE max=%d: %s holds %d cards; this pulse considers the first %d and leaves the rest where they are\n",
			in.Max, oneline.Field(in.Cards), len(cards), in.Max)
		cards = cards[:in.Max]
	}
	// ROUTE (SPEC-DECIDE rule 8): with --routes, each card's worker is a typed decision, and
	// the ROUTE line is logged beside the card and the time. Below the floor the card keeps
	// its own model as the default worker and the line says so. Every card is routed, queued
	// ones included, so ROUTES.log holds one row per card.
	if strings.TrimSpace(in.Routes) != "" {
		if code := routeCards(in, cards); code != 0 {
			return code
		}
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

	// WHICH runner and WHICH nova-swarm, before a slot is taken and before a card is
	// written: both answers are the caller's to give, and a launch that has the wrong one
	// of either spends a slot to find out (issue #1760). The refusals name the reason and
	// the flag that fixes it.
	runner, err := resolveRunner(in.Runner)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED %s\n", oneline.Err(err))
		in.event("refuse", "launch: "+oneline.Err(err), 0, err)
		return 2
	}
	swarmBin, err := resolveSwarm(in.Swarm, in.Version)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED %s\n", oneline.Err(err))
		in.event("refuse", "launch: "+oneline.Err(err), 0, err)
		return 2
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
		ran, ok := runBatchBounded(in, id, goCards, runner, swarmBin)
		if !ok {
			return 2
		}
		id = ran
		record(in.Root, id, len(goCards), in.Slots, in.Deadline)
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

// runBatchBounded admits the cards as one nova-swarm batch, and retries a batch that failed
// at START time on the provider's side -- bounded, with a backoff, and only on the signature
// the layers underneath wrote down (issue #1761, launchswarm.go). It returns the id of the
// batch that ran, which is not the id it was given when a retry was needed: every attempt is
// its own batch, with its own cards.tsv and its own `--then`, so the harvest that follows
// folds exactly the attempt that produced the work.
//
// The failed attempt's job directories are moved aside, never deleted: a retry that erases
// the first failure leaves a lane unable to tell a flake from a pattern.
func runBatchBounded(in LaunchInput, id string, goCards []CardRow, runner string, swarmBin resolvedSwarm) (string, bool) {
	attempts := in.Attempts
	if attempts < 1 {
		attempts = DefaultLaunchAttempts
	}
	for attempt := 1; ; attempt++ {
		if err := os.MkdirAll(filepath.Join(in.Root, "cards", id), 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return "", false
		}
		// The admitted cards become their own cards.tsv, so the batch runs exactly the
		// cards that fit the free slots and never the queued remainder.
		admitted := filepath.Join(in.Root, "cards", id, "cards.tsv")
		if err := writeCardsTSV(admitted, goCards); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return "", false
		}
		if err := os.MkdirAll(filepath.Join(in.Root, "pulses"), 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return "", false
		}
		reason, ok := runBatch(in, id, admitted, runner, swarmBin)
		if ok {
			return id, true
		}

		// What the layers underneath already recorded, read back before anything is said:
		// `PULSE REFUSED: exit status 3` was the whole of the operator's line while the
		// label, the rc, the wall clock and the provider's own error reference sat one
		// directory away (issue #1761).
		diags := diagnose(in.Root, goCards)
		if attempt < attempts && anyStartFailure(diags) {
			for _, d := range diags {
				parkJob(d.Job, attempt)
			}
			wait := backoff(attempt)
			fmt.Fprintf(in.Stderr, "PULSE RETRY attempt=%d of %d backoff=%s provider-start-failure %s\n",
				attempt, attempts, wait, firstStartFailureLine(diags))
			in.event("retry", fmt.Sprintf("launch: pulse %s attempt %d of %d", id, attempt, attempts), 0, nil)
			in.sleep(wait)
			id = swarm.NewID(in.Now(), "pulse")
			continue
		}

		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s%s\n",
			oneline.Cap(reason, oneline.TailBytes), diagTail(diags, attempt, attempts))
		in.event("refuse", "launch: nova-swarm batch "+oneline.Cap(reason, oneline.TailBytes), 0, nil)
		return "", false
	}
}

// anyStartFailure reports whether any admitted card failed at start time on the provider's
// side, the one shape a retry is for.
func anyStartFailure(diags []cardDiag) bool {
	for _, d := range diags {
		if d.startFailure() {
			return true
		}
	}
	return false
}

func firstStartFailureLine(diags []cardDiag) string {
	for _, d := range diags {
		if d.startFailure() {
			return d.line()
		}
	}
	return ""
}

// diagTail is the rest of the operator's one line: what each card that left a job directory
// actually did, and where to read the whole of it. A batch that refused before any job
// existed adds nothing, and its refusal is the swarm's own line exactly as before.
func diagTail(diags []cardDiag, attempt, attempts int) string {
	if len(diags) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, " | attempts=%d of %d", attempt, attempts)
	shown := 0
	for _, d := range diags {
		if shown >= diagCards {
			fmt.Fprintf(&b, " | +%d more card(s)", len(diags)-shown)
			break
		}
		fmt.Fprintf(&b, " | %s", d.line())
		shown++
	}
	return b.String()
}

// diagCards is how many cards' diagnoses one refusal line carries. A pulse is several cards
// and a refusal is one line: the rest are named by count and read in the job tree.
const diagCards = 3

// sleep is the backoff seam: nil is the real clock, and a test passes one that returns at
// once so a bounded retry costs it no wall time.
func (in LaunchInput) sleep(d time.Duration) {
	if in.Sleep != nil {
		in.Sleep(d)
		return
	}
	time.Sleep(d)
}

// runBatch admits one batch as the swarm's CARD form of nova-swarm batch. It returns the
// swarm's own reason when the batch failed, and whether it succeeded; the refusal line is
// the caller's to write, because only the caller knows whether a retry is left.
func runBatch(in LaunchInput, id, cardsPath, runner string, swarmBin resolvedSwarm) (string, bool) {
	// `nova-swarm batch` runs --then with sh -c (internal/swarm/batch.go), so every field
	// interpolated here is shell text, not an argv slot. A root path holding a space split
	// the harvest's --root in two; one holding a `;`, a backtick or a `$(` would have run
	// something else. Both fields are quoted as shell tokens (issue #1825).
	then := fmt.Sprintf("nova-pulse harvest --id %s --root %s", shellToken(id), shellToken(in.Root))
	// The file budget, because `nova-swarm batch` refuses an admission that names none
	// ("--files is required and is at least 1, got 0") and a launch with no configured
	// budget would otherwise send the zero the swarm reads as "refusing to guess". A
	// LaunchInput carrying none still names the documented default (issue #869).
	files := in.Files
	if files < 1 {
		files = DefaultLaunchFiles
	}
	var out, errb bytes.Buffer
	cmd := exec.Command(swarmBin.Path, "batch",
		"--id", id,
		"--cards", cardsPath,
		"--deadline", in.Deadline,
		"--runner", runner,
		"--root", in.Root,
		"--files", strconv.Itoa(files),
		"--then", then)
	// One pulse can fill more than one bench: both flags reach `nova-swarm batch`
	// untouched, and an empty one is not passed at all (issue #637).
	if in.Benches != "" {
		cmd.Args = append(cmd.Args, "--benches", in.Benches)
	}
	if in.Bench != "" {
		cmd.Args = append(cmd.Args, "--bench", in.Bench)
	}
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(errb.String())
		if reason == "" {
			// `exit status 3` is a Go *exec.ExitError stringified with nothing added.
			// It is still the truth about the child, so it is still said -- but it is
			// said as the exit of a NAMED binary, and the caller adds what the job
			// tree recorded (issue #1761).
			reason = fmt.Sprintf("%s batch: %s (it said nothing on stderr)", oneline.Field(swarmBin.Path), oneline.Err(err))
		}
		return reason, false
	}
	return "", true
}

// record appends one row to <root>/pulses/<id>.tsv naming the batch: its id, its card count,
// and the width and deadline it ran with.
//
// The last two are new (issue #1819). Rule 15's pulse-again is a real `nova-pulse launch`
// subprocess, and launch requires --slots and --deadline; harvest had neither and invoked a
// launch that could only refuse. It does not have to guess: the pulse being harvested ran
// with a width and a deadline, and this is where they are written down. The first two fields
// are unchanged, so a reader of the old two-field row still reads it.
func record(root, id string, n, slots int, deadline string) {
	f, err := os.OpenFile(filepath.Join(root, "pulses", id+".tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pulse-%s\t%d\t%d\t%s\n", id, n, slots, oneline.Field(deadline))
}

// shellToken quotes a string so `sh -c` reads it as exactly one argument, whatever it holds.
// Single quotes are literal in POSIX sh, and the one character they cannot hold is closed,
// escaped and reopened. Used for every field launch interpolates into the --then string that
// `nova-swarm batch` will run as a shell command (issue #1825).
func shellToken(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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

// routeCards picks each card's worker with the shared typed decision and rewrites the card's
// model column to the chosen worker description, so the grouping below makes one batch per
// worker. The default below the floor is the card's own model. Every ROUTE line is appended
// to ROUTES.log beside the card's label and the time (SPEC-DECIDE rule 8). It returns the
// process exit code: 0 when every card was routed, 2 when the decision could not run.
func routeCards(in LaunchInput, cards []CardRow) int {
	rows, err := swarm.ParseRoutes(in.Routes)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	log, err := os.OpenFile(routesLogPath(in), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	defer log.Close()
	for i := range cards {
		c := &cards[i]
		raw, err := os.ReadFile(c.Card)
		if err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		res, err := swarm.RouteText(string(raw), in.BaseURL, in.KeyEnv, rows, in.Floor, c.Model)
		if err != nil {
			fmt.Fprintf(in.Stderr, "PULSE REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		c.Model = res.Worker
		stamp := time.Now().UTC()
		if in.Now != nil {
			stamp = in.Now().UTC()
		}
		fmt.Fprintf(log, "%s\t%s\t%s\n", stamp.Format(time.RFC3339), oneline.Field(c.Label), res.Line(c.Label, in.Floor))
	}
	return 0
}

// routesLogPath is <queue>/ROUTES.log: the queue directory when the caller named one, else
// the pulse root, which is where a standalone launch keeps its state.
func routesLogPath(in LaunchInput) string {
	if d := strings.TrimSpace(in.QueueDir); d != "" {
		return filepath.Join(d, RoutesLogFile)
	}
	return filepath.Join(in.Root, RoutesLogFile)
}

// freeSlots counts the free slots among the first `slots`. A slot is free when it holds no
// lock -- its slot file absent, or state=free -- AND NOTHING ELSE.
//
// It used to also refuse a slot whose <pool>/<n>/native.log had been written in the last 120
// seconds, which meant the only thing standing between a card and a slot could be a file's
// mtime. docs/SPEC-PULSE.md rule 8 and docs/CLI.md both say, in those words, "never a log
// age and never a 120 s rule", and rule 8 demands a test named
// launch-slot-free-from-lock-files. Two documents against one constant: the constant loses
// (issue #1822). A live native holds the swarm's own lock (issue #457), which is the fact
// this reads; a log's mtime is a guess about that fact and was wrong in both directions --
// it took a slot no one held, and it freed a held slot whose worker had simply gone quiet.
func freeSlots(root string, slots int, now time.Time) int {
	pool := filepath.Join(root, "pool")
	n := 0
	for i := 1; i <= slots; i++ {
		if slotFree(pool, i) {
			n++
		}
	}
	return n
}

func slotFree(pool string, n int) bool {
	raw, err := os.ReadFile(filepath.Join(pool, "slots", strconv.Itoa(n)+".json"))
	if err != nil {
		return true
	}
	var sf struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(raw, &sf)
	return sf.State == "free"
}
