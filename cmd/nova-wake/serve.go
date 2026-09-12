package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// serve is the OTHER shape (rule 10). `watch` runs inside a tool call and
// returns to a session that is already awake; `serve` is a process outside any
// session that starts a turn only when a note has landed. A named friend should
// wake efficiently when a note for them lands, and be efficient while there is
// NO work, never polling once a second inside a turn that costs tokens.
//
// So: it fetches the bus every --interval -- a git fetch costs no tokens -- and
// for each new note whose To: names <name> it runs <command> <id> [<id>...],
// ONCE, and a second time only on a person's word or under a declared
// idempotent receiver. It starts the command for a note and for nothing else,
// never on an interval, never to receipt, never to look, so an empty minute
// costs one fetch and zero tokens.
//
// The command receives note IDS AND NOTHING ELSE: never inbox's output, never
// the INBOX OPEN carrying=<n> line or the carried list, never a body. The
// line's model opens the note itself.

// serveKillPoint is the injected kill. A test cannot SIGKILL a function it is
// calling, and the thing that has to be proved is what the state file holds at
// each of three instants -- after `dispatching` is written and before the spawn,
// after the spawn and before `delivered`, and after `delivered`. So the loop
// asks this variable at each of those points and returns as if the process had
// died there. It is never set outside a test, and it is a var in this package
// rather than an environment variable because reading the environment TO DECIDE
// BEHAVIOUR is the thing this repo's review reads twice.
var serveKillPoint string

// DeadlineFloor is the shortest --hours this verb will take. --hours is a
// FLOAT, and a float can name a deadline no run can reach: `--hours 1e-12` is
// `time.Duration(3.6e-3 ns)` == 0, so the deadline equals the start, the first
// `now.Before(end)` is already false, and the process exits 0 having polled
// nothing -- `WAKE SERVE fired=0 notes=0 ... idle=0s`, a green that did nothing.
// A deadline shorter than a second is that case or too close to it to tell
// apart from it, so it is refused by name instead (SPEC.md Conventions:
// refusals over silence). A deadline SHORTER THAN ONE --interval is not
// refused: it polls once and ends, which is what a first run and this package's
// own tests ask for.
const DeadlineFloor = time.Second

// DefaultBatchMax is the listing law's 20: every note queued at the moment the
// command is not running is handed to one invocation, in bus order, at most this
// many ids. One turn reads k notes rather than k turns reading one.
const DefaultBatchMax = 20

func cmdServe(args []string, stdout, stderr io.Writer, clock wake.Clock) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var (
		busDir     = fs.String("bus", "", "")
		as         = fs.String("as", "", "")
		onNote     = fs.String("on-note", "", "")
		interval   = fs.String("interval", "", "")
		state      = fs.String("state", "", "")
		hours      = fs.Float64("hours", 0, "")
		receipt    = fs.Bool("receipt", false, "")
		remote     = fs.String("remote", "", "")
		branch     = fs.String("branch", "", "")
		idempotent = fs.Bool("on-note-idempotent", false, "")
		batchMax   = fs.Int("batch-max", DefaultBatchMax, "")
		gitTimeout = fs.Int("git-timeout", DefaultGHTimeout, "")
		words      = fs.Int("receipt-max-words", 0, "")
		redeliver  = fs.String("redeliver", "", "")
	)
	if !parseFlags(fs, args, stderr) {
		return 2
	}

	var p problems
	if *busDir == "" {
		p.missing("bus")
	}
	if *as == "" {
		p.missing("as")
	}
	if *state == "" {
		p.missing("state")
	}
	if *onNote == "" {
		// --redeliver names its handler because the state stores no command.
		p.missing("on-note")
		if *redeliver != "" {
			p.add("--redeliver names no handler", "  "+redeliverHint+"\n")
		}
	}
	if *redeliver == "" {
		if *interval == "" {
			p.missing("interval")
		}
		if *hours <= 0 {
			p.missing("hours")
		}
		if d := time.Duration(*hours * float64(time.Hour)); *hours > 0 && d < DeadlineFloor {
			p.add("--hours "+strconv.FormatFloat(*hours, 'g', -1, 64)+" is a deadline of "+wake.Dur(d)+", below the "+wake.Dur(DeadlineFloor)+" floor",
				"  a deadline this short can round to zero and exit 0 having polled nothing; 0.02 is about a minute\n")
		}
	}
	var every time.Duration
	if *interval != "" {
		every = parseDur(&p, "interval", *interval)
		if every > 0 && every < IntervalFloor {
			p.add("--interval "+*interval+" is below the "+wake.Dur(IntervalFloor)+" floor", "  the interval matches the latency a person will accept, never the second\n")
		}
	}
	if *redeliver == "" && (*remote == "" || *branch == "") {
		// Rule 10: serve FETCHES THE BUS EVERY --interval. A plain `inbox`
		// reads the checkout and never the remote, so a serve with no remote
		// sits on a standing checkout forever -- the false-quiet failure this
		// verb exists to end. The fetch is not an option --receipt turns on.
		p.add("serve fetches the bus every --interval and needs --remote and --branch",
			"  a fetch is how mail on the remote reaches the checkout; nova-bus wait takes the checkout lock, fetches and fast-forwards\n")
	}
	if *receipt && (*remote == "" || *branch == "") {
		p.add("--receipt needs --remote and --branch", "  a receipt is pushed, the same way nova-bus pushes one\n")
	}
	if *gitTimeout <= 0 {
		p.add("--git-timeout must be a positive number of seconds", "  a budget of zero or less is not 'unlimited'; it is a call that can never finish\n")
	}
	if *redeliver == "" && *words <= 0 {
		// nova-bus has no default for it and neither has this: it is a fact
		// about how much of a receipt the caller wants printed.
		p.missing("receipt-max-words")
	}
	if *batchMax < 0 {
		p.add("--batch-max is negative", "  0 already means all\n")
	}
	if p.any() {
		return p.print(stderr, "serve")
	}

	release, holder, err := wake.LockState(*state)
	if err != nil {
		return refused(stderr, oneline.Err(err))
	}
	if release == nil {
		return refused(stderr, "another nova-wake holds "+wake.LockName(*state)+" (pid "+holder+"); one writer per state file")
	}
	defer release()

	st, err := wake.Load(*state)
	if err != nil {
		return refused(stderr, "the state file "+*state+" could not be read: "+oneline.Err(err)+
			"; repair it, or pass a new --state path and accept a cold start on purpose")
	}

	// The pin is checked here for the same reason watch checks it: serve's
	// whole promise is that mail on the remote reaches the checkout through
	// nova-bus, and a nova-bus that stopped fetching would leave a serve that
	// looks perfectly healthy and is blind.
	switch found, verr := wake.BusVersion(context.Background(), time.Duration(*gitTimeout)*time.Second); {
	case verr != nil:
		// A program missing from PATH is a failed poll and not a refusal.
	case !wake.AcceptBus(Version(), found):
		return refused(stderr, wake.BusRefusal(Version(), found))
	}

	s := &server{
		stdout: stdout, stderr: stderr, clock: clock, st: st, statePath: *state,
		bus: *busDir, as: *as, onNote: *onNote, batchMax: *batchMax,
		idempotent: *idempotent, receipt: *receipt, remote: *remote, branch: *branch,
		words: *words, timeout: time.Duration(*gitTimeout) * time.Second,
		inOrder: map[string]bool{},
	}
	s.busSrc = &wake.Bus{
		Dir: *busDir, As: *as, ReceiptMaxWords: *words, Timeout: s.timeout,
		Seen: func(line string) bool { _, ok := st.Get("bus:line:" + line); return ok },
		// The one suppression serve's own state decides: a note this receiver
		// already holds a delivery record for has been handed to the mind.
		Printed: func(id string) bool { _, ok := st.Get(serveKey(id)); return ok },
	}
	ctx := context.Background()
	if *redeliver != "" {
		return s.redeliverOne(ctx, *redeliver)
	}
	return s.loop(ctx, every, time.Duration(*hours*float64(time.Hour)))
}

type server struct {
	stdout, stderr io.Writer
	clock          wake.Clock
	st             *wake.State
	statePath      string
	bus, as        string
	onNote         string
	batchMax       int
	idempotent     bool
	receipt        bool
	remote, branch string
	words          int
	timeout        time.Duration

	fired, notes, redelivered, uncertain, cc int
	failed                                   int
	maxWait                                  time.Duration
	saidBlocked                              bool

	// busSrc is the WATCH verb's classifier, reused rather than re-spelled:
	// rule 7 is about the bus source and serve reads the same bus with the
	// same program. Its counts are this run's WAKE SOURCE line.
	busSrc *wake.Bus
	// order is bus order -- the order the ids were listed in, which is not the
	// order they sort in. Every poll re-lists what this receiver still
	// carries, so a restart recovers it.
	order   []string
	inOrder map[string]bool
}

// serveKey is one note's delivery record.
func serveKey(id string) string { return "serve:" + id }

// loop is the process outside a session.
func (s *server) loop(ctx context.Context, every, hours time.Duration) int {
	start := s.clock.Now()
	end := start.Add(hours)
	stop := s.statePath + ".stop"

	// A RESTART FIRST ESTABLISHES THAT THE RECEIVER IS IDLE, and only then runs
	// what it finds queued. Killing serve does not prove that the command it
	// started died: the child may be alive and mid-turn, and the harness is
	// one.
	s.recover(ctx)

	for {
		now := s.clock.Now()
		if !now.Before(end) {
			break
		}
		if _, err := os.Stat(stop); err == nil {
			break
		}
		due := now.Add(every)
		s.poll(ctx, now, every)
		s.dispatch(ctx, now)
		s.save()
		// The cadence is the interval, and `wait` has already spent part of it
		// blocking: sleeping the whole interval on top of a blocking fetch
		// would fetch every two.
		s.clock.Sleep(due.Sub(s.clock.Now()))
	}
	// queued= and uncertain= are read off the STATE at the exit rather than
	// counted as they happened: what the reader needs at the end is what is
	// still waiting and what is still unresolved, which is not the same number
	// as how many became so during this run.
	queued, uncertain := 0, 0
	for _, id := range s.ids() {
		switch s.stateOf(id) {
		case "queued":
			queued++
		case "uncertain":
			uncertain++
		}
	}
	s.sourceLine(ctx)
	// The count line prints on FAILURE too: a dispatch whose command exited 7
	// is not a fire that worked, and a reader of this line must not have to
	// infer it from the absence of anything else.
	fmt.Fprintf(s.stdout, "WAKE SERVE fired=%d notes=%d redelivered=%d uncertain=%d queued=%d cc=%d failed=%d max_wait=%s idle=%s\n",
		s.fired, s.notes, s.redelivered, uncertain, queued, s.cc, s.failed,
		oneline.Field(wake.Dur(s.maxWait)), oneline.Field(wake.Dur(s.clock.Now().Sub(start))))
	return 0
}

// recover turns every dispatch interrupted by a kill into an UNCERTAIN one. A
// command with no idempotency protocol may have acted, and a tool that ran it
// again would be choosing a duplicate action on the mind's behalf.
func (s *server) recover(ctx context.Context) {
	for _, id := range s.ids() {
		state, stamp, attempt, _ := s.record(id)
		if state != "dispatching" {
			continue
		}
		_ = stamp
		s.st.Set(serveKey(id), wake.Compose("uncertain", wake.Stamp(s.clock.Now()), "attempt="+strconv.Itoa(attempt)))
		s.uncertain++
		fmt.Fprintf(s.stdout, "WAKE UNCERTAIN id=%s attempt=%d: dispatch interrupted; %s\n",
			oneline.Field(id), attempt, oneline.Escape(s.remedy(id)))
		if s.idempotent && attempt == 1 {
			// The caller's declaration of a STRONGER RECEIVER CONTRACT: the
			// command de-duplicates by id, durably, tolerates a second
			// invocation beside a live first one, and its exit 0 for an id is a
			// terminal acknowledgement that the work for that id has completed.
			// So the interrupted attempt is run once more, before anything
			// queued, and the queue waits for THIS retry's delivered rc=0.
			//
			// ONLY attempt=1. "An interrupted attempt=2 is uncertain and
			// blocks all the same, so nothing fires forever and nothing goes
			// quiet" -- never a third automatic run.
			s.runBatch(ctx, []string{id}, attempt+1, true)
		}
	}
	s.save()
}

// blocked reports the first unresolved uncertain id. An unresolved uncertain
// entry BLOCKS THIS RECEIVER'S QUEUE: no queued note is dispatched and nothing
// is redelivered while one exists.
func (s *server) blocked() string {
	for _, id := range s.ids() {
		if s.stateOf(id) == "uncertain" {
			return id
		}
	}
	return ""
}

// poll reads the bus once and records what it found. It never starts the
// command to look.
//
// RULE 7 IS NOT THE WATCH VERB'S ALONE. Every line the bus source reads is
// classified as suppressed, relayed or standing, and every line is counted; a
// line this tool cannot classify is a line this tool PRINTS. A serve that kept
// only `INBOX NOTE ` would be the grep of 2026-09-10 rebuilt in the other verb:
// an hour over a bus refusing every read, ending `fired=0` and exit 0.
func (s *server) poll(ctx context.Context, now time.Time, budget time.Duration) {
	// The process budget must COVER the fetch it asked for: the wait is told to
	// block for one interval, so killing the process at --git-timeout would
	// kill serve's own fetch every poll (a serve --interval 60s at the 45s
	// default fetched the first 45s of every minute and was blind for the rest).
	out, code, err := s.runBusWithin(ctx, budget+s.timeout, s.busArgs(budget))
	// The lines are classified whether or not the poll ended badly: a run that
	// dropped an INBOX REFUSED line because nova-bus exited 2 is the false
	// quiet arriving by the other road.
	// BUS ORDER, for every note the poll listed -- including the ones the
	// classifier suppresses because this receiver already holds a record for
	// them. On a restart that is ALL of them, and a dispatch that read its
	// order off the state file would read sorted order and call it bus order.
	for _, id := range wake.BusNoteIDs(out) {
		s.remember(id)
	}
	res := s.busSrc.Classify(out)
	list := bounded.Capped(s.stdout, bounded.Default, "WAKE", "bus",
		"the bus said more than a poll prints; nova-bus inbox --bus <dir> --as <name> lists the whole of it")
	for _, it := range res.Items {
		switch it.Kind {
		case wake.KindBus:
			s.recordNote(it, now)
		case wake.KindBusLine:
			// The first sighting of an unrecognised line is news, and it is
			// remembered so the next hour of the same sentence stands rather
			// than waking the line again.
			s.st.Sight(it.Key)
			list.Line(wake.Render(wake.KindBusLine, it.Key, it.Value, now))
		}
	}
	// Shown every time, woken on once -- and touched, because a line that
	// stands is the most recently seen thing there is.
	for _, key := range res.StandingKeys {
		s.st.Sight(key)
	}
	for _, line := range res.Standing {
		list.Line(line)
	}
	list.More()
	// The sighting memory grows with THINGS THAT HAPPEN rather than with things
	// being watched, so it is an LRU of 300 like the watch verb's -- a serve
	// runs for hours over a bus that may emit a distinct line every poll, and
	// an unbounded map in a file rewritten every poll is the state growing
	// without a reason anybody chose.
	s.st.Evict()
	switch {
	case err != nil:
		fmt.Fprintf(s.stderr, "WAKE POLL bus: %s\n", oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)))
	case code != 0:
		// nova-bus said NO, which is an answer: its lines were read above, and
		// the exit is said out loud rather than turned into a quiet nil.
		fmt.Fprintf(s.stderr, "WAKE POLL bus: nova-bus exit=%d\n", code)
	}
	s.save()
}

// recordNote turns one classified INBOX NOTE into this receiver's delivery
// record. To: WAKES; Cc: DOES NOT. To means must act, cc means should know, and
// a broadcast to five is five turns.
func (s *server) recordNote(it wake.Item, now time.Time) {
	id := it.ID
	if id == "" {
		return
	}
	s.remember(id)
	addr := ""
	if p := wake.Decompose(it.Value); len(p) > 1 {
		addr = p[1]
	}
	switch addr {
	case "to":
		// To means must act.
		s.notes++
		s.st.Set(serveKey(id), wake.Compose("queued", wake.Stamp(now)))
	case "cc":
		// Cc means should know: recorded, counted, never a turn, and read by
		// the line at its next natural turn. A broadcast to five is five turns.
		s.notes++
		s.cc++
		s.st.Set(serveKey(id), wake.Compose("cc", wake.Stamp(now)))
	default:
		// A note addressed to ANOTHER NAME, which a bus does not normally list
		// for this reader at all. It is not this receiver's note: not
		// dispatched, not counted as cc, not counted as one of this receiver's
		// notes. The record is kept so it is not re-examined every poll, and
		// the allow-list decides what is DISPATCHED exactly as the suppress
		// list decides what is hidden -- the unsafe direction here is not
		// silence, it is starting somebody's command over a note nobody
		// addressed to them.
		s.st.Set(serveKey(id), wake.Compose("cc", wake.Stamp(now)))
	}
}

// remember keeps BUS ORDER: the order the bus listed the ids in, which is not
// the order they sort in.
func (s *server) remember(id string) {
	if s.inOrder[id] {
		return
	}
	s.inOrder[id] = true
	s.order = append(s.order, id)
}

// sourceLine is rule 7's four counts, once per run, before the exit line. They
// add up: read equals the sum of the other three. A bus that printed nothing
// and a bus that printed sixty REFUSED lines must not look the same.
func (s *server) sourceLine(ctx context.Context) {
	read, suppress, relay, standing := s.busSrc.Counts()
	head, headAt := wake.Head(ctx, s.bus, s.timeout)
	fmt.Fprintf(s.stdout, "WAKE SOURCE bus read=%d suppressed=%d relayed=%d standing=%d head=%s head-at=%s\n",
		read, suppress, relay, standing, oneline.Field(dash(head)), oneline.Field(dash(headAt)))
}

// busArgs is rule 10's fetch: serve FETCHES THE BUS EVERY --interval, because a
// git fetch costs no tokens and mail that landed on the remote reaches the
// checkout no other way. It is `wait` WITHOUT --advance -- wait takes the
// checkout lock, fetches, fast-forwards and returns the moment the inbox would
// list something new or at its timeout -- so no cursor moves and nothing is
// consumed. The timeout is DERIVED FROM THE INTERVAL this run was given and is
// never a guessed second.
func (s *server) busArgs(budget time.Duration) []string {
	return []string{"wait", "--bus", s.bus, "--as", s.as,
		"--receipt-max-words", strconv.Itoa(s.words),
		"--timeout", wake.Dur(budget), "--interval", wake.Dur(budget),
		"--remote", s.remote, "--branch", s.branch}
}

// dispatch hands every queued id to ONE invocation, in bus order, at most
// --batch-max: one turn reads k notes rather than k turns reading one.
func (s *server) dispatch(ctx context.Context, now time.Time) {
	if id := s.blocked(); id != "" {
		queued := 0
		for _, other := range s.ids() {
			if s.stateOf(other) == "queued" {
				queued++
			}
		}
		s.blockedOnce(id, queued)
		return
	}
	var batch []string
	for _, id := range s.ids() {
		if s.stateOf(id) != "queued" {
			continue
		}
		if _, stamp, _, _ := s.record(id); stamp != "" {
			if at, err := time.Parse(time.RFC3339, stamp); err == nil && now.Sub(at) > s.maxWait {
				s.maxWait = now.Sub(at)
			}
		}
		batch = append(batch, id)
		if s.batchMax > 0 && len(batch) >= s.batchMax {
			break
		}
	}
	if len(batch) == 0 {
		return
	}
	s.runBatch(ctx, batch, 1, false)
}

// blockedOnce prints the WAKE BLOCKED line once per call, with its remedy.
func (s *server) blockedOnce(id string, queued int) {
	if s.saidBlocked {
		return
	}
	s.saidBlocked = true
	fmt.Fprintf(s.stdout, "WAKE BLOCKED as=%s uncertain=%s queued=%d: a dispatch may still own this receiver; end it, then %s\n",
		oneline.Field(s.as), oneline.Field(id), queued, oneline.Escape(s.remedy(id)))
}

// runBatch is the one place a command is started, and the order of the three
// writes around it is the whole of rule 10's durability: `dispatching` BEFORE
// the spawn, for every id in the batch; the spawn; `delivered` with the exit
// code AFTER the command returns, for every id in the batch.
func (s *server) runBatch(ctx context.Context, ids []string, attempt int, redelivered bool) {
	stamp := wake.Stamp(s.clock.Now())
	for _, id := range ids {
		s.st.Set(serveKey(id), wake.Compose("dispatching", stamp, "attempt="+strconv.Itoa(attempt)))
	}
	s.save()
	if serveKillPoint == "before-spawn" {
		return
	}
	rc := s.spawn(ctx, ids)
	if serveKillPoint == "before-delivered" {
		return
	}
	mark := "0"
	if redelivered {
		mark = "1"
		s.redelivered++
	}
	done := wake.Stamp(s.clock.Now())
	for _, id := range ids {
		if rc != 0 {
			// A command that did not exit 0 has NOT accepted the note, and
			// writing `delivered rc=7` for it loses the note twice over: it is
			// never dispatched again, and --redeliver refuses it as delivered.
			// A harness out of credits, a typo in the command, a crashed child
			// -- every one of them silently consumed a note. Exit 0 is the one
			// acceptance boundary an arbitrary command offers, so anything else
			// is uncertain and a person's; "never a silent duplicate, and never
			// a silent loss".
			s.st.Set(serveKey(id), wake.Compose("uncertain", done, "attempt="+strconv.Itoa(attempt), "rc="+strconv.Itoa(rc)))
			s.uncertain++
			s.failed++
			why := "dispatch did not accept"
			if redelivered {
				why = "retry not terminal"
			}
			fmt.Fprintf(s.stdout, "WAKE UNCERTAIN id=%s attempt=%d rc=%d: %s; %s\n",
				oneline.Field(id), attempt, rc, oneline.Escape(why), oneline.Escape(s.remedy(id)))
			continue
		}
		s.st.Set(serveKey(id), wake.Compose("delivered", done, "rc="+strconv.Itoa(rc), "redelivered="+mark))
	}
	s.save()
	s.fired++
	fmt.Fprintf(s.stdout, "WAKE FIRED ids=%d first=%s rc=%d redelivered=%s\n",
		len(ids), oneline.Field(ids[0]), rc, oneline.Field(mark))
	if serveKillPoint == "after-delivered" {
		return
	}
	// The receipt is the MACHINERY's claim that the note was handed to the mind
	// and the mind returned, so it goes after `delivered rc=0` and never at fire
	// time: a receipt sent before the command returned would claim a handoff a
	// kill could still lose. A note whose command failed stays on the open list
	// unreceipted, which is where a note nobody has dealt with belongs.
	if s.receipt && rc == 0 {
		s.sendReceipts(ctx, ids)
	}
}

// spawn runs the command with the ids and NOTHING else on its command line.
func (s *server) spawn(ctx context.Context, ids []string) int {
	fields := strings.Fields(s.onNote)
	cmd := exec.CommandContext(ctx, fields[0], append(append([]string{}, fields[1:]...), ids...)...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if asExitErr(err, &ee) {
			return ee.ExitCode()
		}
		// A command that could not be started at all is a non-zero exit like
		// any other: the tool never reads its output and never retries it.
		fmt.Fprintf(s.stderr, "WAKE POLL on-note: %s\n", oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)))
		return 127
	}
	return 0
}

func (s *server) sendReceipts(ctx context.Context, ids []string) {
	args := []string{"receipt", "--bus", s.bus, "--as", s.as}
	for _, id := range ids {
		args = append(args, "--note", id)
	}
	args = append(args, "--remote", s.remote, "--branch", s.branch)
	if _, _, err := s.runBus(ctx, args); err != nil {
		fmt.Fprintf(s.stderr, "WAKE POLL receipt: %s\n", oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)))
	}
}

// redeliverOne is A PERSON'S ACT: the person has ended the earlier command or
// watched it return, and the state stores no command, so the redelivery names
// its handler. It is refused unless the state is uncertain.
func (s *server) redeliverOne(ctx context.Context, id string) int {
	state, _, attempt, _ := s.record(id)
	if state != "uncertain" {
		return refused(s.stderr, "--redeliver "+id+" is "+dash(state)+", not uncertain; a redelivery is for a dispatch this tool could not prove had finished, and nothing else")
	}
	s.runBatch(ctx, []string{id}, attempt+1, true)
	s.save()
	return 0
}

// remedy is the sentence that ends every uncertain and blocked line: the exact
// command a person runs.
func (s *server) remedy(id string) string {
	return "nova-wake serve --bus " + s.bus + " --as " + s.as + " --state " + s.statePath +
		" --redeliver " + id + " --on-note <command> runs it again"
}

// ids returns every note this receiver has a record for, in bus order -- which
// is the order the ids were first seen, and is what the queue numbers keep.
func (s *server) ids() []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range s.order {
		if _, ok := s.st.Get(serveKey(id)); ok {
			out = append(out, id)
			seen[id] = true
		}
	}
	// A restart has records before it has a listing. State order is SORTED
	// order, which is not bus order -- the first poll re-lists what this
	// receiver still carries and puts them right.
	for _, k := range s.st.Keys() {
		if id, ok := strings.CutPrefix(k, "serve:"); ok && !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

func (s *server) record(id string) (state, stamp string, attempt, rc int) {
	raw, ok := s.st.Get(serveKey(id))
	if !ok {
		return "", "", 0, 0
	}
	p := wake.Decompose(raw)
	state = p[0]
	if len(p) > 1 {
		stamp = p[1]
	}
	for _, f := range p[2:] {
		if v, ok := strings.CutPrefix(f, "attempt="); ok {
			attempt, _ = strconv.Atoi(v)
		}
		if v, ok := strings.CutPrefix(f, "rc="); ok {
			rc, _ = strconv.Atoi(v)
		}
	}
	if attempt == 0 {
		attempt = 1
	}
	return state, stamp, attempt, rc
}

func (s *server) stateOf(id string) string {
	state, _, _, _ := s.record(id)
	return state
}

func (s *server) save() {
	if err := s.st.Save(s.statePath); err != nil {
		fmt.Fprintf(s.stderr, "WAKE POLL state: %s\n", oneline.Err(err))
	}
}

// runBus starts nova-bus under the timeout. It is the same one program the
// watch verb starts, with the same read of stdout and stderr together.
func (s *server) runBus(ctx context.Context, args []string) (string, int, error) {
	return s.runBusWithin(ctx, s.timeout, args)
}

func (s *server) runBusWithin(ctx context.Context, budget time.Duration, args []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-bus", args...)
	raw, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(raw), 0, fmt.Errorf("nova-bus timed out after %s", wake.Dur(budget))
	}
	if err != nil {
		var ee *exec.ExitError
		if asExitErr(err, &ee) {
			// nova-bus said NO, which is an answer: its lines are read AND
			// classified, and the exit code is handed back rather than
			// swallowed into a nil the caller reads as success.
			return string(raw), ee.ExitCode(), nil
		}
		return string(raw), 0, fmt.Errorf("nova-bus could not be run: %s", oneLine(err.Error()))
	}
	return string(raw), 0, nil
}

func asExitErr(err error, out **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*out = ee
	}
	return ok
}
