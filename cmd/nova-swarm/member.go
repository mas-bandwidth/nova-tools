package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/binstamp"
	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/gocache"
	"github.com/mas-bandwidth/nova-tools/internal/harness"
	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/mas-bandwidth/nova-tools/internal/yield"
)

// cmdMember is `nova-swarm member`: this machine as one member of a sprint's
// fleet (or one of its readers). Every few seconds it beats, reads its queue
// from the sprint, pushes the commit of every work card whose child ended and
// reports it, takes up to its width and starts each card taken as one child
// through `nova-swarm native`. The fleet table is the dispatcher; the loop is
// internal/member. Every sprint verb goes to the sprint's server (--server, `nova-sprint
// run --listen`), the one writer; this machine opens no store. send delivers them: nil is
// the server's client (sprintwire.Client.Do), a test's is the server's own step.
func cmdMember(args []string, stdout, stderr io.Writer, send func(context.Context, ...[]string) ([]sprintwire.Result, error)) int {
	fs := flag.NewFlagSet("member", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &flags{verb: "member", fs: fs}
	as := fs.String("as", "", "required: this machine's `name`, its row in the fleet table (with --reader, its row in the readers table, reader-<machine>)")
	width := fs.Int("width", 0, "an override of the most cards it runs at once, a twin's; a worker runs its fleet row's width, read every tick: a member its own row's, a reader its machine's (reader-<m> runs at m's width)")
	reader := fs.Bool("reader", false, "run as a reader: take and run reads of finished work instead of work cards, at its machine's width; a flash card's first read is a decide read, asked with JEV_API_KEY from this environment (docs/SPEC-SPRINT.md section 6)")
	harness := fs.String("harness", "", "required: the harness binary `path` each card's child runs under (native --harness)")
	model := fs.String("model", "", "the `provider/model` a card with no route runs on; a reader given it runs every read on it")
	root := fs.String("root", "", "required: the `dir` the launches and results sit under")
	slots := fs.String("slots", "", "the `dir` of the launch directories, one per card launch (default <root>/slots)")
	resultsRoot := fs.String("results-root", "", "the `dir` each launch's results are written under (default <root>/results)")
	// the override: a card with no route
	deadline := newSecondsFlag(fs, "deadline", 0, "the wall-clock bound a card with no route runs to, a `duration` or whole seconds; a reader given it runs every read to it")
	tokensWord := fs.String("tokens", "", "the token budget a card with no route runs on, a number of tokens or the word unmetered (`n|unmetered`); a reader given it runs every read on it")
	every := newSecondsFlag(fs, "every", 3*time.Second, "the time between passes and beats, a `duration` or whole seconds, above 0 and at most 5s (default 3s)")
	once := fs.Bool("once", false, "run one pass, wait for its starts and pushes, and stop")
	ticks := fs.Int("ticks", 0, "run this many passes and stop (not with --once; default: run until stopped)")
	auth := fs.String("auth", "", "the harness's auth `file`, handed to each child's native --auth")
	config := fs.String("config", "", "the harness's provider config `file`, handed to each child's native --config")
	workerFile := fs.String("worker", "", "the worker description `file` (JSON), handed to each child's native --worker; the secret it names is handed through too")
	noWall := fs.Bool("no-wall", false, "run each child with no nova-sandbox wall (native --no-wall): the caller owns every read and write it makes")
	ghBin := fs.String("gh", "gh", "the gh `path` the member opens a work card's pull request with, outside the wall (default gh)")
	passFlag := fs.String("pass", "", "the `NAME,...` of secrets in this environment a child is handed (the loop record's nova-secrets keys); a harness that reads its provider key from the environment needs it")
	stageWall := newSecondsFlag(fs, "stage-wall", swarm.DefaultStageTimeout, "the bound on staging each card's checkout, a `duration` or whole seconds, handed to native as --stage-timeout: a slow machine under load names a longer one in its loop row's argv (default 120s)")
	diskFloor := fs.Int("disk-floor", 10, "the free `GiB` the slots' volume keeps: below it no card starts (default 10; 0 checks nothing)")
	maxLoad := fs.Float64("max-load", 0, "the maximum one-minute host `load` at which a local child starts (default 0: no load gate)")
	warnLoad := fs.Float64("warn-load", 0, "the one-minute host `load` at which a local child start warns, at or below --max-load (default 0: no warning)")
	gocacheGiB := fs.Int("gocache-limit", int(gocache.Limit/gib), "the `GiB` the shared Go build cache is held under by the cleaner, oldest unused entries removed down to 80% of it, never one used in the last two hours (default 20; a busy machine holds its working set with more)")
	identity := fs.String("identity", "", "the pool identity every child commits under, `owner,name,email` (default: the pool's identity.tsv)")
	server := fs.String("server", "", "required: the sprint server's `address:port`, which nova-sprint run --listen started on the coordinator's machine; every sprint verb goes there and this machine opens no store")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*as, "as", "the member's name in the fleet table (a reader's in the readers table with --reader)")
	// a worker runs the width its fleet row names (fleet up --width, fleet sync), read with
	// its queue every tick: a member its own row's, a reader its machine's (reader-<m>,
	// sprint.ReaderMachine); --width is a twin's override
	if *width < 0 {
		f.add("--width is an override of the fleet row's width and is at least 1; leave it out to run the row's")
	}
	f.want(*server, "server", "the sprint server's host:port, the run loop started with nova-sprint run --listen on the coordinator's machine: the member sends every sprint verb there and opens no store")
	f.want(*harness, "harness", "the harness binary path a card runs under (nova-swarm native --harness)")
	// the card decides its model: the deal (a read: the ask) writes the route it drew
	// into the packet (provider/model, tokens, deadline); --model, --tokens and
	// --deadline are the override a card with no route runs on (a store with no route: a
	// twin, one machine), and a reader's, given, run its reads over their routes
	if *model != "" {
		if _, ok := providerOf(*model); !ok {
			f.add(fmt.Sprintf("--model %q is not provider/model (one slash, both sides nonempty); every card would be refused by native", *model))
		}
	}
	f.want(*root, "root", "the root directory slots and results sit under")
	if *tokensWord != "" {
		f.tokens(*tokensWord) // the word is read here, once, so a typo is one refusal and not one failed card each
	}
	if deadline.d < 0 {
		f.add("--deadline is the wall bound per card a card with no route runs to, above 0")
	}
	if every.d <= 0 || every.d > 5*time.Second {
		f.add("--every is between 1ms and 5s: the beat it carries has a 15s deadline in the fleet")
	}
	if *as != "" && !safepath.NameOK(*as) {
		f.add(fmt.Sprintf("--as %q is not a name (letters, digits, - _ .)", *as))
	}
	var onceGiven, ticksGiven bool
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "once":
			onceGiven = true
		case "ticks":
			ticksGiven = true
		}
	})
	if onceGiven && ticksGiven {
		f.add("give --once or --ticks <n>, not both")
	}
	if ticksGiven && *ticks <= 0 {
		f.add("give --ticks 1 or more, or leave it out to run until stopped")
	}
	if stageWall.d <= 0 {
		f.add("--stage-wall is the bound on staging a card's checkout, above 0 (default 120s)")
	}
	if *diskFloor < 0 {
		f.add("--disk-floor is the free GiB the slots' volume must keep for the member to start a card: 0 or more (0 checks nothing; default 10)")
	}
	if math.IsNaN(*maxLoad) || math.IsInf(*maxLoad, 0) || *maxLoad < 0 {
		f.add("--max-load is the finite maximum one-minute host load at which a local child starts: 0 or more (0 checks nothing)")
	}
	if math.IsNaN(*warnLoad) || math.IsInf(*warnLoad, 0) || *warnLoad < 0 {
		f.add("--warn-load is the finite one-minute host load at which a local child start warns: 0 or more (0 disables the warning)")
	}
	if *warnLoad > 0 && (*maxLoad == 0 || *warnLoad > *maxLoad) {
		f.add("--warn-load requires --max-load and is at or below it")
	}
	if *gocacheGiB < 1 {
		f.add("--gocache-limit is the GiB the shared Go build cache is held under: 1 or more (default 20)")
	}
	// the pool identity every child commits under, from the loop's argv in nova-config;
	// without it native reads the pool's identity.tsv
	if *identity != "" {
		if _, err := swarm.ParseIdentity(*identity); err != nil {
			f.add(err.Error())
		}
	}
	pass := splitNames(*passFlag)
	for _, n := range pass {
		if !envNameRE.MatchString(n) {
			f.add(fmt.Sprintf("--pass %q is not an environment name (letters, digits, _)", n))
		}
	}
	if *workerFile != "" {
		// the worker description names the secret its harness reads: that one name is
		// handed through too (docs/SPEC-CARD-CONTRACT.md, the child's environment)
		if w, problems := swarm.LoadWorker(*workerFile); len(problems) == 0 && w.Secret != "" {
			pass = append(pass, w.Secret)
		}
	}
	if f.refused(stderr) {
		return 2
	}
	// the beat writes from its own goroutine (memberLoop), so what this verb writes to stderr
	// is one line at a time
	stderr = &lockedWriter{w: stderr}
	// a member only takes a card it can run at CI's priority: native refuses every card on
	// an OS with no setpriority, so a member there would take and fail every card it is dealt
	if why := yieldRefusal(yield.Supported, runtime.GOOS); why != "" {
		return refuse(stderr, " member", why)
	}
	// a name --pass lists that this environment does not hold is read here, in
	// this process, before any directory is made (keys.go)
	held, extra, err := prepareMemberKeys(pass, os.Getenv)
	if err != nil {
		return refuse(stderr, " member", err.Error())
	}
	pass = append(pass, extra...)
	if *slots == "" {
		*slots = filepath.Join(*root, "slots")
	}
	if *resultsRoot == "" {
		*resultsRoot = filepath.Join(*root, "results")
	}
	for _, d := range []string{*slots, *resultsRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return refuse(stderr, " member", err.Error())
		}
	}
	self, err := os.Executable()
	if err != nil {
		return refuse(stderr, " member", "own executable: "+err.Error())
	}
	// the sprint's verbs go to its server, which runs them beside the store (internal/sprintwire)
	if send == nil {
		send = sprintwire.Client{Addr: *server}.Do
	}
	sp := &sprintwire.Worker{Send: send, Failed: sprintFailureOutput}
	// a member hands native the decide key when its environment holds it, or when
	// --pass named it and this process read it (keys.go): a reader's native asks the
	// decide read with it, a worker's the gate decision of a red gate, and neither
	// hands it to the harness (nativedecide.go, nativegate.go, nativeChildEnv)
	nativePass := append(append([]string{}, pass...), decide.JevSecret)
	rn := &nativeRunner{
		self: self, harness: *harness, model: *model, root: *root, slots: *slots,
		resultsRoot: *resultsRoot, deadline: deadline.d, stageWall: stageWall.d, tokens: *tokensWord, auth: *auth, config: *config,
		worker: *workerFile, noWall: *noWall, stderr: stderr, pass: nativePass, held: held, identity: *identity,
		load: hostload.Local(), maxLoad: *maxLoad, warnLoad: *warnLoad,
		cacheLimit: int64(*gocacheGiB) * gib,
	}
	// a work card's commit is pushed by the member, outside the wall, at its
	// finish (memberpush.go); a read pushes nothing
	var pu member.Pusher
	if !*reader {
		gp := newGitPusher(*root, *slots)
		gp.gh = *ghBin
		gp.notes = stdout
		pu = gp
	}
	// the machine's CPU, a sample a second, for the beat's load (hostload.Sampler); a
	// reader beats nothing
	var meter *hostload.Sampler
	if !*reader {
		meter = hostload.NewSampler(hostload.Local())
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		go meter.Run(ctx)
	}
	// a launch the member is done with leaves no checkout behind, and none is started on a
	// volume under the floor (slotclean.go); what a crash or a kill left is swept first
	var room func() (bool, string)
	if *diskFloor > 0 {
		room = diskRoom(*slots, *diskFloor, diskFree)
	}
	cfg := memberConfig(*as, *width, *reader, meter, room, *root, *noWall)
	cfg.Sleep = time.Sleep // harness starts StartGap apart
	// the attempt decision reads the decision key in this process when --pass names
	// it and the environment does not hold it (keys.go)
	cfg.Attempt = workAttempt(*reader, rn.getenv)
	m := member.New(cfg, sp, rn, pu, stdout)
	kind := "member"
	if *reader {
		kind = "reader"
	}
	widthWord := "row" // the fleet row's, read every tick
	if *width > 0 {
		widthWord = "override:" + strconv.Itoa(*width)
	}
	modelWord := "card" // each card's route, from its packet
	if *model != "" {
		modelWord = "card,override:" + *model
	}
	fmt.Fprintf(stdout, "MEMBER %s as=%s width=%s every=%s server=%s harness=%s model=%s stage-wall=%s\n", oneline.Field(kind), oneline.Field(*as), oneline.Field(widthWord), oneline.Field(every.d.String()), oneline.Field(*server), oneline.Field(*harness), oneline.Field(modelWord), oneline.Field(stageWall.d.String()))
	// the machine's one model catalog, refreshed once here and never per launch (catalog.go)
	fmt.Fprintf(stdout, "CATALOG %s\n", oneline.Escape(refreshCatalog(*harness, *root)))
	if note := passNote(*model, pass, *auth); note != "" {
		fmt.Fprintln(stdout, note)
	}
	if removed, kept := rn.prune(time.Now()); removed > 0 {
		fmt.Fprintf(stdout, "NOTE sweep: removed %d ended launch directories under %s, kept the newest %d\n", removed, oneline.Field(*slots), kept)
	}
	rn.cleaner() // from here a launch the member ends is tagged, and removed apart from its pass
	// a unit restart or stop drains the member (memberLoop): SIGTERM only, so a terminal's
	// interrupt, which reaches every child of the process group too, still ends it at once
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)
	keep := mirrorKeeping(os.Getenv(mirrorsEnv), *root, rn.goBuildCache(), stdout, stderr)
	if keep != nil {
		rn.benchHome, _ = os.UserHomeDir()
	}
	n, replaced := memberLoop(m, loopRun{keep: keep, every: every.d, limit: loopTicks(*once, ticksGiven, *ticks), stamp: func() string { return binstamp.Of(self) }, term: term, deadline: deadline.d}, stdout, stderr)
	if replaced {
		return exitReplaced
	}
	fmt.Fprintf(stdout, "MEMBER OK as=%s ticks=%d running=%d\n", oneline.Field(*as), n, m.Running())
	return 0
}

// memberConfig is the member.Config nova-swarm runs a member or a reader with. A reader's
// ScriptVerify reads a script card's head out of the bench mirror and runs its program in
// the member's wall, so a script card whose head is its program's output needs no model
// read (docs/SPEC-SPRINT.md, the script read); a worker's is nil. cmdMember sets Sleep,
// so its pass loop's clock stays where the push table names it.
func memberConfig(as string, width int, reader bool, meter *hostload.Sampler, room func() (bool, string), root string, noWall bool) member.Config {
	cfg := member.Config{As: as, Width: width, Reader: reader, Meter: meter, Room: room, Background: true, Attempt: workAttempt(reader, os.Getenv)}
	if reader {
		cfg.ScriptVerify = scriptVerify(root, "", noWall)
	}
	return cfg
}

// exitReplaced is member's exit when its binary was replaced under it: not 0, so
// a supervisor that restarts only a failed loop restarts it too (the loop units
// in fleet/templates restart on any exit).
const exitReplaced = 3

// loopTicks is the tick count a bounded run stops at: 1 for --once, --ticks' n,
// 0 for a run until stopped.
func loopTicks(once, ticksGiven bool, ticks int) int {
	if once {
		return 1
	}
	if ticksGiven {
		return ticks
	}
	return 0
}

// loopRun is how memberLoop runs: every pass's interval, the passes it stops at (limit > 0),
// the stamp of the binary it runs, the supervisor's stop (term: SIGTERM), the member's own
// --deadline (the deadline of a card whose packet names none), and its clock: now, and after,
// the wait between passes (nil: the wall clock and time.After).
type loopRun struct {
	every    time.Duration
	limit    int
	stamp    func() string
	term     <-chan os.Signal
	deadline time.Duration
	now      func() time.Time
	after    func(time.Duration) <-chan time.Time
	keep     func(time.Time) // refreshes the bench mirrors before a pass (mirrorKeeping); nil: none
}

// memberLoop ticks m every lr.every (lr.limit > 0: at most limit ticks) and returns the
// ticks it ran. It drains, taking no new card (said once) and reporting each child as it
// ends, and stops when the last is reported, on two words:
//
//   - its binary was replaced. A loop runs the code it was started with for as long as it
//     runs: a release installed under it would leave the fleet worked by the code before it
//     (2026-10-01: six members kept the binaries they began with until restarted by hand).
//     So before each tick it reads its binary's stamp, and when that changed since it began
//     it drains; replaced is true when it stopped so (exit 3, its supervisor starts the new).
//   - its supervisor stopped it (SIGTERM: a unit restart or stop, the loops play's restart
//     of a changed unit; nova-tools#5096 item 26). A stop never kills a card: the drain is
//     bounded by member.DrainBound of the longest deadline the running cards name, past which
//     it stops with what is left running (its supervisor ends it; the sprint deals the
//     cards again when the member goes down, and a member started again adopts a child
//     still alive). It exits 0.
//
// With no child running either stops at once.
func memberLoop(m *member.Member, lr loopRun, stdout, stderr io.Writer) (n int, replaced bool) {
	now, after := lr.now, lr.after
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = time.After
	}
	began := ""
	if lr.stamp != nil {
		began = lr.stamp()
	}
	// the beat goes on its own clock, apart from the work pass (internal/member BeatLoop): one
	// now, so the member is up before its first pass, then one every interval while the pass
	// goes on, ending with this loop
	if err := m.Beat(); err != nil {
		fmt.Fprintf(stderr, "nova-swarm member: %s\n", oneline.Escape(err.Error()))
	}
	beatCtx, stopBeats := context.WithCancel(context.Background())
	beatTicker := time.NewTicker(lr.every)
	beatsEnded := make(chan struct{})
	go func() { defer close(beatsEnded); m.BeatLoop(beatCtx, beatTicker.C, stderr) }()
	// the beat ends before the loop does: nothing it writes comes after the member's last line
	defer func() { stopBeats(); beatTicker.Stop(); <-beatsEnded }()
	draining := "" // why the member drains: "" while it takes
	var termed bool
	var bound time.Duration
	var until time.Time // a SIGTERM's drain ends here at the latest
	for {
		select {
		case <-lr.term:
			termed = true
		default:
		}
		if draining == "" && began != "" && lr.stamp() != began {
			if m.Running() == 0 {
				fmt.Fprintf(stdout, "MEMBER STOP the binary this member runs was replaced; its supervisor starts the new one\n")
				return n, true
			}
			draining = "replaced"
			m.Drain()
			fmt.Fprintf(stdout, "MEMBER DRAIN the binary this member runs was replaced: taking no new card, %d running; it stops when the last child is reported\n", m.Running())
		}
		if termed && until.IsZero() {
			if m.Running() == 0 {
				fmt.Fprintf(stdout, "MEMBER STOP SIGTERM: nothing running\n")
				return n, false
			}
			bound = member.DrainBound(m.LongestDeadline(lr.deadline))
			until = now().Add(bound)
			if draining == "" {
				draining = "SIGTERM"
				m.Drain()
			}
			fmt.Fprintf(stdout, "MEMBER DRAIN SIGTERM: taking no new card, %d running; it stops when the last child is reported, at most %s (by %s)\n", m.Running(), oneline.Field(bound.String()), oneline.Field(until.Format("15:04:05")))
		}
		if draining != "" && m.Running() == 0 {
			if draining == "replaced" {
				fmt.Fprintf(stdout, "MEMBER STOP the binary this member runs was replaced; its supervisor starts the new one\n")
				return n, true
			}
			fmt.Fprintf(stdout, "MEMBER STOP SIGTERM: the last child is reported\n")
			return n, false
		}
		if !until.IsZero() && !now().Before(until) {
			fmt.Fprintf(stdout, "MEMBER STOP SIGTERM: the drain's bound %s passed with %d running; its supervisor ends them, and the sprint deals their cards again when this member is down\n", oneline.Field(bound.String()), m.Running())
			return n, draining == "replaced"
		}
		n++
		if lr.keep != nil {
			lr.keep(now())
		}
		acted, err := m.Tick(now())
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm member: tick %d: %s\n", n, oneline.Escape(err.Error()))
		}
		if acted > 0 || err != nil {
			// where the pass's time went, by part: a lane freed during a pass waits for the rest of it
			spent := m.LastPass()
			fmt.Fprintf(stdout, "tick %d acted=%d running=%d %s queue=%.1fs push=%.1fs report=%.1fs fill=%.1fs\n", n, acted, m.Running(), oneline.Field(now().Format("15:04:05")),
				spent.Queue.Seconds(), spent.Push.Seconds(), spent.Report.Seconds(), spent.Fill.Seconds())
		}
		if lr.limit > 0 && n >= lr.limit {
			m.WaitLong() // no start half made, no push cut off, when the process exits
			return n, false
		}

		// the next pass at the interval, or at once when a push ended, a child exited or the
		// supervisor said stop
		select {
		case <-after(lr.every):
		case <-m.Wake():
		case <-lr.term:
			termed = true
		}
	}
}

// sprintFailureOutput is one bounded diagnostic from a failed sprint verb (sprintwire.Worker's Failed).
// stderr leads because it holds the refusal or store error; stdout follows because a
// verb can print a repair or move receipt before a later store operation fails. Each
// half keeps room for the other, and secret-shaped values never reach the member log.
func sprintFailureOutput(stdout, stderr []byte) []byte {
	clean := func(raw []byte, n int) string {
		return oneline.Cap(oneline.Escape(log.Redact(strings.TrimSpace(string(raw)))), n)
	}
	if len(strings.TrimSpace(string(stdout))) == 0 {
		return []byte(clean(stderr, oneline.TailBytes))
	}
	if len(strings.TrimSpace(string(stderr))) == 0 {
		return []byte(clean(stdout, oneline.TailBytes))
	}
	const labels = "stderr: ; stdout: "
	each := (oneline.TailBytes - len(labels)) / 2
	return []byte("stderr: " + clean(stderr, each) + "; stdout: " + clean(stdout, each))
}

// nativeRunner runs one packet as one `nova-swarm native` child in its own
// slot directory, its results under <results-root>/<card>/.
type nativeRunner struct {
	self, harness, model, root, slots, resultsRoot string
	deadline, stageWall                            time.Duration
	tokens, auth, config, worker, identity         string
	noWall                                         bool
	stderr                                         io.Writer
	env                                            []string                     // added to this process's environment: none in production, a test's
	lookPath                                       func(string) (string, error) // resolves a headless harness on PATH (harnessFor); nil is exec.LookPath, a test's its own
	pass                                           []string                     // the secret names handed to native (--pass, the worker's secret)
	held                                           map[string]secrets.Secret    // secrets read in this process; nil when the environment already held every name
	benchHome                                      string                       // the home whose nova-bench/mirror a card's clone step borrows (mirrorKeeping); "": the card keeps $HOME
	load                                           hostload.Source
	maxLoad, warnLoad                              float64

	// launches started and not yet ended; failed ones ended and kept (slotclean.go). mu
	// guards both: the member's pass tags a launch ended while the cleaner prunes. tagged
	// is the cleaner's queue; nil cleans in Ended
	mu         sync.Mutex
	live, kept map[string]bool
	removing   sync.Mutex // held while a launch directory is removed, and while one is claimed

	tagged chan ended

	// the cleaner's lazy work (lazyclean.go): epoch is the sprint's epoch plus one as the
	// member's last pass read it (Epoch; 0: none read yet); oldFailed and cache are the
	// cleaner's own, touched by no other goroutine
	epoch      atomic.Uint64
	oldFailed  map[string]bool
	cache      gocache.Trim
	cacheLimit int64 // --gocache-limit in bytes; 0 is gocache.Limit
}

// stageTimeout is native's --stage-timeout for each launch: the member's --stage-wall, else
// native's own default, said.
func (r *nativeRunner) stageTimeout() time.Duration {
	if r.stageWall > 0 {
		return r.stageWall
	}
	return swarm.DefaultStageTimeout
}

// started marks a launch running, so no prune of the pool touches its directory until the
// member ends it (slotclean.go).
func (r *nativeRunner) started(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		r.live = map[string]bool{}
	}
	r.live[name] = true
}

// Start runs a packet as a child. The launch is claimed (live) before its directory is
// touched and for as long as it runs, so the cleaner, which removes ended launches apart
// from the pass, never removes the directory of a launch of the same name started again.
func (r *nativeRunner) Start(p member.Packet) (child member.Child, err error) {
	if !safepath.NameOK(p.Card) {
		return nil, fmt.Errorf("card %q is not a name", p.Card)
	}
	// one slot and one results root per launch (the card at its generation, or
	// attempt, in its epoch), so a result can only be this launch's; a launch
	// whose pid file names a live process is adopted, never run twice
	name := launchName(p)
	r.removing.Lock() // a removal in flight ends first: it may be this name's old directory
	r.started(name)
	r.removing.Unlock()
	defer func() {
		if err != nil {
			r.mu.Lock()
			delete(r.live, name)
			r.mu.Unlock()
		}
	}()
	slot := filepath.Join(r.slots, name)
	results := filepath.Join(r.resultsRoot, name)
	logPath := filepath.Join(r.slots, name+".native.log")
	pidPath := filepath.Join(r.slots, name+".pid")
	job := filepath.Join(slot, "jobs", p.Card)
	if pid := livePID(pidPath); pid > 0 {
		proc, _ := os.FindProcess(pid) // ignored: a pid alive a moment ago; a nil proc makes Stop a no-op
		c := &nativeChild{card: p.Card, logPath: logPath, results: results, job: job, done: make(chan struct{}), proc: proc}
		go func() {
			for processAlive(pid) {
				time.Sleep(time.Second)
			}
			close(c.done)
		}()
		r.started(name)
		return c, nil
	}
	// SPEC-FRIEND "The local child load gate": the configured raw one-minute load
	// decides admission immediately before a new local child is created.
	if r.maxLoad > 0 && r.load.Load1 != nil {
		if load, ok := r.load.Load1(); ok {
			if load > r.maxLoad {
				return nil, fmt.Errorf("local child refused: one-minute load %.2f is above configured bound %.2f", load, r.maxLoad)
			}
			if r.warnLoad > 0 && load >= r.warnLoad && r.stderr != nil {
				fmt.Fprintf(r.stderr, "nova-swarm member: WARNING local child %s starts at one-minute load %.2f, near configured bound %.2f\n", oneline.Field(p.Card), load, r.maxLoad)
			}
		}
	}
	if err := safepath.RemoveUnder(r.slots, slot); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(slot, 0o755); err != nil {
		return nil, err
	}
	model, tokens, deadline, err := r.route(p)
	if err != nil {
		return nil, err
	}
	// the card tells the child the wall it runs under, the one --deadline below ends it at
	// (member.CardText): a brief's own DEADLINE line is planning text, and a child that
	// planned on 120 minutes was ended at 40 with no RESULT.md (fault 9, 2026-10-10)
	p.Deadline = int(deadline / time.Second)
	card, err := childCard(p)
	if err != nil {
		return nil, err
	}
	cardPath := filepath.Join(r.slots, name+".card.md")
	if err := os.WriteFile(cardPath, []byte(swarm.PointCardAtMirrors(card, r.benchHome)), 0o644); err != nil {
		return nil, err
	}
	bin, err := r.harnessFor(p)
	if err != nil {
		return nil, err
	}
	framePath := filepath.Join(r.slots, name+cardcontract.FrameName)
	if err := cardcontract.WriteFrame(framePath, frameOf(p, model, r.root)); err != nil {
		return nil, err
	}
	args := []string{"native", "--harness", bin, "--model", model, "--card", cardPath, "--frame", framePath, "--slot", slot,
		"--root", r.root, "--deadline", deadline.String(), "--tokens", tokens, "--label", p.Card, "--results-root", results,
		"--stage-timeout", r.stageTimeout().String()}
	if p.USD != "" {
		// the route's dollar budget, beside its token budget (#5094); a reader's override
		// names tokens, not dollars, so an overridden read keeps the route's
		args = append(args, "--usd", p.USD)
	}
	if r.auth != "" {
		args = append(args, "--auth", r.auth)
	}
	if r.config != "" {
		args = append(args, "--config", r.config)
	}
	if r.worker != "" {
		args = append(args, "--worker", r.worker)
	}
	if r.noWall {
		args = append(args, "--no-wall")
	}
	if r.identity != "" {
		args = append(args, "--identity", r.identity)
	}
	// A long-lived child: its own cancellable context and no deadline (its own --deadline
	// ends it), released when the wait returns.
	ctx, release := context.WithCancel(context.Background())
	cmd := subproc.Long(ctx, r.self, args...)
	cmd.Env = r.childEnv(model)
	logf, err := os.Create(logPath)
	if err != nil {
		release()
		return nil, err
	}
	// a file in the slot, never a pipe back to the member: the wait below is on native's
	// process alone, so a process native left cannot hold the finish (docs/SPEC-CARD-CONTRACT.md, the finish)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		release()
		logf.Close()
		return nil, err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil && r.stderr != nil {
		// the child runs; only a restart's way back to it is lost, so the member says so and goes on
		fmt.Fprintf(r.stderr, "nova-swarm member: NOTE card %s runs as pid %d, and its pid file %s could not be written (%s): a member restarted while it runs cannot find it and may launch the card a second time; let it end before restarting this member; run: ls -ld %s\n",
			oneline.Field(p.Card), cmd.Process.Pid, oneline.Field(pidPath), oneline.Err(err), oneline.Field(r.slots))
	}
	c := &nativeChild{card: p.Card, logPath: logPath, results: results, job: job, done: make(chan struct{}), proc: cmd.Process}
	go func() {
		c.err = cmd.Wait()
		release()
		logf.Close()
		// ignored: the pid file of a child that has ended; a leftover names a dead pid, which the next start overwrites
		_ = safepath.RemoveUnder(r.slots, pidPath)
		close(c.done)
	}()
	r.started(name)
	return c, nil
}

// childCard is the card file a child is handed: the packet's brief with the rules file the
// card names injected at stage time (rules by reference, nova-tools#5174 rule 6:
// swarm.StagedBrief), then what the sprint adds (member.CardText). A card that names none
// gets nothing injected: it carries its own rules. A brief that already carries the rules is
// handed as it is; a card naming a rules file this build does not hold is refused, and it is
// not started.
func childCard(p member.Packet) (string, error) {
	if p.Rules != "" {
		rules, err := swarm.HeldRules(p.Rules)
		if err != nil {
			return "", fmt.Errorf("card %s: the rules by reference: %w", p.Card, err)
		}
		p.Brief = swarm.StagedBrief(p.Brief, rules)
	}
	return member.CardText(p), nil
}

// route is what one launch runs on: the packet's route (the card's model, budget and
// deadline, as the deal or the ask drew them), each part it leaves empty from the
// member's override; a launch with no model, budget or deadline from either is
// refused. A read's route is drawn from the reader tier (internal/sprint/route.go,
// readRouteOf), so a reader needs no --model; a reader started with --model,
// --tokens or --deadline runs its reads on what it names over the read's route.
func (r *nativeRunner) route(p member.Packet) (model, tokens string, deadline time.Duration, err error) {
	model, tokens, deadline = p.Model, p.Tokens, time.Duration(p.Deadline)*time.Second
	if p.Kind == "read" {
		if r.model != "" {
			model = r.model
		}
		if r.tokens != "" {
			tokens = r.tokens
		}
		if r.deadline > 0 {
			deadline = r.deadline
		}
	}
	if model == "" {
		model = r.model
	}
	if tokens == "" {
		tokens = r.tokens
	}
	if deadline <= 0 {
		deadline = r.deadline
	}
	switch {
	case model != "" && tokens != "" && deadline > 0:
	case p.Route != "":
		return "", "", 0, fmt.Errorf("card %s's route %s (model %q) names no tokens or deadline and this member has no override for them: give the card's model: pin its tokens: and deadline: lines, or start the member with --tokens and --deadline", p.Card, p.Route, p.Model)
	default:
		return "", "", 0, fmt.Errorf("card %s has no route (model %q tokens %q deadline %s) and this member no override for what is missing: add the tier's route with nova-config route add, or start the member with --model, --tokens and --deadline", p.Card, model, tokens, deadline)
	}
	return model, tokens, deadline, nil
}

// harnessFor is the binary a packet's child runs under: the member's --harness, or for a
// route naming a headless harness (internal/harness; docs/SPEC-SWARM.md, the headless
// harnesses) that program on this member's PATH. A headless program this machine has not
// got refuses the launch, so the sprint deals the card to a member that has.
func (r *nativeRunner) harnessFor(p member.Packet) (string, error) {
	if !harness.IsHeadless(p.Harness) {
		return r.harness, nil
	}
	lookPath := r.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath(p.Harness)
	if err != nil {
		return "", fmt.Errorf("card %s's route %s runs under %s, which is on no PATH entry of this member: install it and log in, or deal the card to a member that has it", p.Card, p.Route, p.Harness)
	}
	return bin, nil
}

// launchName is the name of one launch: the card at its generation (a read:
// its attempt) in its epoch; a card dealt again is another launch.
func launchName(p member.Packet) string {
	// a path element, built by concatenation (the card id passed safepath.NameOK)
	e := ".e" + strconv.FormatUint(p.Epoch, 10)
	if p.Kind == "read" {
		return p.Card + ".a" + strconv.Itoa(p.Attempt) + e
	}
	return p.Card + ".g" + strconv.Itoa(p.Gen) + e
}

// livePID is the pid a pid file names when that process is alive, else 0.
func livePID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || !processAlive(pid) {
		return 0
	}
	return pid
}

// processAlive is whether a signal 0 reaches the process.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

type nativeChild struct {
	card, logPath, results, job string
	done                        chan struct{}
	err                         error
	once                        sync.Once
	result                      member.Result
	// proc is native's process: the machine's stop signals it (Stop), and native reaps its
	// harness's group with its own grace (native_proc_unix.go, the SIGTERM branch of watch).
	proc *os.Process
}

// StopGrace is how long a native child told to stop by the machine's stop has to end by
// itself (native reaps its harness group within swarm.TerminateGrace) before it is killed.
const StopGrace = 60 * time.Second

// Stop is member.Stopper: native is told to stop (SIGTERM; where the system has no such
// signal, killed), its working tree and log left as they are, and killed if it is still
// there after StopGrace. It returns native's pid, the evidence the lane record names.
func (c *nativeChild) Stop() int {
	if c.proc == nil {
		return 0
	}
	if err := c.proc.Signal(syscall.SIGTERM); err != nil {
		_ = c.proc.Kill() // ignored: a process already gone is the end wanted
	}
	time.AfterFunc(StopGrace, func() {
		select {
		case <-c.done:
		default:
			_ = c.proc.Kill() // ignored: the wait on done reads the end
		}
	})
	return c.proc.Pid
}

// noUsageReported is the usage of a launch whose harness reported no token and left no
// receipt: a record with nothing in it, its source named, so the member's --usage is
// never absent (docs/SPEC-SWARM.md, the member's read verdict).
var noUsageReported = func() string {
	u := cardcost.NoUsage()
	u.Extra = []string{"usage_source=none"}
	return u.String()
}()

// receiptSpend is this launch's durable per-attempt usage rows read back
// (member.ReceiptUsage; docs/SPEC-SPRINT.md, "What a card cost"). The job directory is
// unique to one card generation or read attempt, so rows from another attempt cannot enter
// the consumer's cost. Native writes these rows before it prints its final summary; they
// recover accounting when that summary is cut off.
func receiptSpend(job string) (cardcost.Usage, bool, error) { return member.ReceiptUsage(job) }

// mergeReceiptSpend keeps native's final timing and budget words while filling the per-call
// usage fields from the durable receipt written before that summary (docs/SPEC-SPRINT.md,
// "What a card cost").
func mergeReceiptSpend(line string, recovered cardcost.Usage) string {
	u := cardcost.ParseUsage(line)
	mergeCount := func(old, receipt int64) int64 {
		if receipt >= 0 {
			return receipt
		}
		return old
	}
	u.Tokens = cardcost.Tokens{
		Input:      mergeCount(u.Tokens.Input, recovered.Tokens.Input),
		CacheRead:  mergeCount(u.Tokens.CacheRead, recovered.Tokens.CacheRead),
		CacheWrite: mergeCount(u.Tokens.CacheWrite, recovered.Tokens.CacheWrite),
		Output:     mergeCount(u.Tokens.Output, recovered.Tokens.Output),
		Reasoning:  mergeCount(u.Tokens.Reasoning, recovered.Tokens.Reasoning),
		Requests:   mergeCount(u.Tokens.Requests, recovered.Tokens.Requests),
		MaxPrompt:  mergeCount(u.Tokens.MaxPrompt, recovered.Tokens.MaxPrompt),
	}
	if recovered.Model != "" {
		u.Model = recovered.Model
	}
	// The receipt is authoritative when the summary is absent or disagrees. Its
	// empty actual is unknown, and must clear any partial summary price.
	u.Actual, u.ActualBy = recovered.Actual, recovered.ActualBy
	return u.String()
}

// spendMatchesReceipt is whether a native summary carries the per-call totals that the
// durable receipt rows independently report. A disagreement marks a truncated summary.
func spendMatchesReceipt(summary string, recovered cardcost.Usage) bool {
	u := cardcost.ParseSpend(summary)
	counts := [][2]int64{
		{u.Tokens.Input, recovered.Tokens.Input}, {u.Tokens.CacheRead, recovered.Tokens.CacheRead},
		{u.Tokens.CacheWrite, recovered.Tokens.CacheWrite}, {u.Tokens.Output, recovered.Tokens.Output},
		{u.Tokens.Reasoning, recovered.Tokens.Reasoning},
	}
	for _, pair := range counts {
		if pair[1] >= 0 && pair[0] != pair[1] {
			return false
		}
	}
	if recovered.Model != "" && u.Model != recovered.Model {
		return false
	}
	if recovered.Actual != "" {
		a, aok := cardcost.Sum(u.Actual, "0")
		b, bok := cardcost.Sum(recovered.Actual, "0")
		if !aok || !bok || a != b {
			return false
		}
	} else if u.Actual != "" {
		return false
	}
	return true
}

// Wait is closed when the child has ended (member.Waiter).
func (c *nativeChild) Wait() <-chan struct{} { return c.done }

func (c *nativeChild) Done() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Printed is when the child last printed: the modification time of its log, and the zero
// time when the log is not there (member.Printer; the member stamps progress on the card
// while the child prints, and the late rule reads the silence).
func (c *nativeChild) Printed() time.Time {
	fi, err := os.Stat(c.logPath)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

var _ member.Printer = (*nativeChild)(nil)
var _ member.Stopper = (*nativeChild)(nil)

var nativeRC = regexp.MustCompile(`\bNATIVE (\S+) .*\brc=(-?\d+)\b.*\bharness=(\S+)`)

// nativeSpent is the NATIVE line's wall seconds and budget word, the launch's usage.
var nativeSpent = regexp.MustCompile(`\bNATIVE \S+ .*\bwall=([0-9.]+s)\b.*\bbudget=(\S+)`)

// nativeSpend is the NATIVE line's spend= word (spendWord): the job's tokens by class,
// requests, largest prompt, the harness's cost and model.
var nativeSpend = regexp.MustCompile(`\bNATIVE \S+ .*\bspend=(\S+)`)

// nativeEnd is how a launch that did not finish ended, from its log: the provider's
// failure (a NATIVE PROVIDER- line), the budget (stopped=), the deadline (rc=-1 with
// neither, and no TERM from outside), else "".
func nativeEnd(log []byte) string {
	switch {
	case nativeProvider.Match(log):
		return member.EndProvider
	case nativeUnverifiable.Match(log):
		return member.EndUnverifiable
	case nativeStopped.Match(log):
		return member.EndBudget
	case nativeKilled.Match(log) && !nativeTermed.Match(log):
		return member.EndDeadline
	}
	return ""
}

// nativeProviderWhy is the reason of a run the provider failed (nativeprovider.go's
// PROVIDER-FAIL line, and the 5xx hand-back's, swarm.Handback, PROVIDER-5XX): the rest of
// the line after its first reason=, the cause (`provider: class=<c> status=<n|-> msg=<m>`).
// A hand-back line that names no reason (a native before the cause) is its own reason.
var (
	nativeProviderWhy = regexp.MustCompile(`(?m)\bNATIVE PROVIDER-(?:FAIL|5XX) \S.*? reason=(.+)$`)
	nativeHandback    = regexp.MustCompile(`(?m)\bNATIVE (PROVIDER-5XX \S.*)$`)
)

// providerReason is why the provider failed the run, from native's log: the provider line's
// reason, else a 5xx hand-back line with none (`provider: PROVIDER-5XX label=... ref=...`),
// else "".
func providerReason(log []byte) string {
	if m := nativeProviderWhy.FindSubmatch(log); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	if m := nativeHandback.FindSubmatch(log); m != nil {
		return "provider: " + strings.TrimSpace(string(m[1]))
	}
	return ""
}

// nativeGateLine is native's NATIVE GATE line (nativegate.go): where the gate decision sent
// a not-done child's red gate, and the tests it names.
var nativeGateLine = regexp.MustCompile(`(?m)^NATIVE GATE \S.*? route=(\S+) tests=(\S*)`)

// nativeStageFail is native's STAGE FAIL line's reason: the launch refused at staging,
// before any child ran (native.go; tla/CardContract.tla, StageRefused).
var nativeStageFail = regexp.MustCompile(`(?m)^STAGE FAIL .*\breason=(.+)$`)

// nativeRefusedWhy is native's NATIVE REFUSED line's reason (refuseNative): a launch
// native would not run, such as one that could not step behind CI (yieldNative). It is
// the report of a launch that left no result of its own, so the reason reaches the
// card's finish instead of "ended without a result".
var nativeRefusedWhy = regexp.MustCompile(`(?m)^NATIVE REFUSED: (.+)$`)

// nativeYieldRefused is the one NATIVE REFUSED line that is the machine's and not the
// card's: native could not step behind CI (yieldNative). Only this refusal ends a launch
// as a staging refusal; every other one is the card's and stays a failed finish.
var nativeYieldRefused = regexp.MustCompile(`(?m)^NATIVE REFUSED: (yield to CI: .+)$`)

var (
	nativeProvider = regexp.MustCompile(`\bNATIVE PROVIDER-`)
	nativeStopped  = regexp.MustCompile(`\bNATIVE \S+ .*\bstopped=`)
	// nativeUnverifiable is a launch native ended because its usage source stopped
	// answering (docs/SPEC-SWARM.md, native: stopped=unverifiable, end=budget-unverifiable)
	nativeUnverifiable = regexp.MustCompile(`\bNATIVE \S+ .*\bstopped=unverifiable\b`)
	// nativeBudgetWhy is the NATIVE BUDGET line's words: which budget ended the run and at
	// what count (nativeBudgetWords)
	nativeBudgetWhy = regexp.MustCompile(`(?m)^NATIVE BUDGET \S+ budget: (.+)$`)
	nativeKilled    = regexp.MustCompile(`\bNATIVE \S+ .*\brc=-1\b`)
	nativeTermed    = regexp.MustCompile(`\breason=terminated\b`)
)

// Result reads how the child ended: the NATIVE line's rc and harness word, and
// the finish the gh shim recorded in the job, else the newest RESULT.md under
// the card's results, in the contract's shape (docs/SPEC-CARD-CONTRACT.md
// section 3). The head is the result's, else the last push the git shim
// recorded in the job, else an older result's `rev:` line; a read's verdict and
// report fall back to the older shape too. A launch refused at staging (its STAGE FAIL
// line) ran no child: it ends EndStaging with the line's reason, and nothing else is read.
func (c *nativeChild) Result() member.Result {
	c.once.Do(func() {
		ran := false
		var end, usage, provider, refused, budget, gate, gateTests, carry, usageError, nativeSpendLine string
		nativeHasSpend := false
		if b, err := os.ReadFile(c.logPath); err == nil {
			carry = cardcontract.ParseCarryLine(b)
			if m := nativeGateLine.FindSubmatch(b); m != nil {
				gate, gateTests = string(m[1]), strings.ReplaceAll(strings.TrimSpace(string(m[2])), ",", ", ")
			}
			if m := nativeRefusedWhy.FindSubmatch(b); m != nil {
				refused = strings.TrimSpace(string(m[1]))
			}
			if m := nativeStageFail.FindSubmatch(b); m != nil {
				// refused at staging: no child ran, so no result of this launch exists to read
				why := strings.TrimPrefix(strings.TrimSpace(string(m[1])), member.EndStaging+": ")
				c.result = member.Result{End: member.EndStaging, Staging: why, Report: "no child ran (see " + c.logPath + ")"}
				return
			}
			if m := nativeYieldRefused.FindSubmatch(b); m != nil && !nativeRC.Match(b) {
				// refused before any child ran because this machine could not step behind CI:
				// the machine's fault, never the card's, so it ends as a staging refusal does
				// and the sprint deals the card to another member (StageRefused)
				c.result = member.Result{End: member.EndStaging, Staging: strings.TrimSpace(string(m[1])), Report: "no child ran (see " + c.logPath + ")"}
				return
			}
			if m := nativeRC.FindSubmatch(b); m != nil {
				ran = string(m[1]) == "OK" && string(m[2]) == "0" && string(m[3]) == "ok"
			}
			if m := nativeSpent.FindSubmatch(b); m != nil {
				// the launch's cost record (internal/cardcost): the wall and the budget word,
				// and what the job spent by token class with the harness's cost (spend=)
				u := cardcost.NoUsage()
				if s := nativeSpend.FindSubmatch(b); s != nil {
					u = cardcost.ParseSpend(string(s[1]))
				}
				u.Wall, u.Budget = string(m[1]), string(m[2])
				usage = u.String()
			}
			if s := nativeSpend.FindSubmatch(b); s != nil {
				nativeHasSpend = true
				nativeSpendLine = string(s[1])
			}
			end = nativeEnd(b)
			provider = providerReason(b)
			if m := nativeBudgetWhy.FindSubmatch(b); m != nil && (end == member.EndBudget || end == member.EndUnverifiable) {
				budget = strings.TrimSpace(string(m[1]))
			}
		}
		if c.job != "" {
			// collect writes usage.tsv before report prints spend= on the final NATIVE
			// line; a native process stopped between those writes still has a durable
			// per-attempt receipt for this generation. The file is also the fallback when
			// native's final log is absent altogether.
			recovered, found, receiptErr := receiptSpend(c.job)
			if receiptErr != nil {
				usageError = receiptErr.Error()
			} else if found && (usage == "" || !nativeHasSpend || !spendMatchesReceipt(nativeSpendLine, recovered)) {
				if usage == "" {
					usage = recovered.String()
				} else {
					usage = mergeReceiptSpend(usage, recovered)
				}
			}
		}
		path := newestResult(c.results)
		var raw []byte
		if path != "" {
			raw, _ = os.ReadFile(path) // ignored: an unreadable result reads as no result, which the finish judges failed
		}
		// the shim's record of gh pr create or gh pr review is the finish when there is
		// one; a RESULT.md the child wrote as well rides in its body for the readers
		cr, shimmed := cardcontract.ReadFinish(c.job)
		if !shimmed {
			cr = typedrec.ParseCardResult(raw)
		} else if own := strings.TrimSpace(string(raw)); own != "" && !cardcontract.IsFinish(c.job, raw) {
			// native published the child's own RESULT.md beside the finish: it rides in the body
			cr.Body = strings.TrimSpace(cr.Body + "\n\n## RESULT.md\n\n" + own)
		}
		head, verdict, report := cr.Head, cr.Verdict, cr.Report
		if head == "" {
			_, head = cardcontract.LastPushed(c.job)
		}
		if !cr.Shaped {
			lh, lv, lr := readResult(path)
			if head == "" {
				head = lh
			}
			if verdict == "" {
				verdict = lv
			}
			if report == "" {
				report = lr
			}
		}
		if report == "" {
			if ran {
				report = "finished; the child published no one-line report"
			} else if refused != "" {
				report = "native refused: " + refused
			} else {
				report = "the child ended without a result (see " + c.logPath + ")"
			}
		}
		if usageError != "" {
			// Keep the missing receipt visible in the stored cost record and the finish
			// words; never turn an unreadable snapshot into a measured zero.
			u := cardcost.ParseUsage(usage)
			u.Extra = append(u.Extra, "usage_source_error=receipt-unreadable")
			usage = u.String()
			report = oneline.Cap(report+"; usage receipt unreadable: "+oneline.Escape(usageError), 300)
		}
		if usage == "" {
			// the harness reported nothing and left no receipt: the finish or the read
			// still carries --usage, saying so (noUsageReported), so a routed read's
			// verdict is kept and recorded unpriced=no-tokens, never refused for a
			// missing --usage (docs/SPEC-SPRINT.md, "Reads are priced like work")
			usage = noUsageReported
		}
		if verdict == "not-done" && gate == member.GateGreen {
			// the child's gate was red only on failures the gate decision classed flaky, and
			// their rerun passed: the work is done as far as its gate says; the readers read it
			verdict, report = "ok", "gate: "+gateTests+" flaky, green on the rerun; "+report
		}
		c.result = member.Result{Ran: ran, OK: ran, Shaped: cr.Shaped, Verdict: verdict, Head: head, Report: report, Title: cr.Title, Body: cr.Body, End: end, Usage: usage, Provider: provider, Budget: budget, Gate: gate, GateTests: gateTests, Carry: carry}
	})
	return c.result
}

// frameOf is a launch's frame (docs/SPEC-CARD-CONTRACT.md layer 1): the repository and
// base the brief's header names, the packet's branch and attempt, and the commit to stage:
// a read's head under read, a later attempt's last pushed head of any earlier attempt
// (sprint.BaseOf, the packet's base_head and base_attempt), else the base's sha.
func frameOf(p member.Packet, model, root string) cardcontract.Frame {
	cb := swarm.ReadCardBase([]byte(p.Brief))
	first, _, _ := strings.Cut(p.Brief, "\n")
	f := cardcontract.Frame{Kind: p.Kind, Card: p.Card, Attempt: p.Attempt, Model: model,
		Repo: cb.Repo, BaseRef: cb.Ref, StageSha: cb.Sha, Branch: p.Branch, Why: p.Why, Finding: p.Finding, Fix: p.Fix, Stage: cb.Stage}
	if len(cb.Stage) > 0 {
		f.Recipes = filepath.Join(root, cardcontract.RecipesName)
	}
	mh, _ := cardhdr.ReadModel(first) // line 1's tier, by the one parser the deal reads it with
	f.Tier = mh.Tier
	if p.Tier != "" { // the sprint's: a read's read tier, a rework's --tier
		f.Tier = p.Tier
	}
	if p.Kind == "read" {
		f.Branch, f.ReviewBase = p.WorkBranch, cb.Ref
		f.DecideBounce, f.DecideReview = p.DecideBounce, p.DecideReview
		if p.WorkBase != "" {
			f.ReviewBase = p.WorkBase
		}
		if typedrec.IsFullSha(p.Head) {
			f.StageSha = p.Head
		}
		return f
	}
	f.DecideGateFlaky, f.DecideGatePreexisting = p.DecideGateFlaky, p.DecideGatePreexisting
	if p.BaseHead != "" {
		f.StageSha, f.PrevHead, f.PrevFrom = p.BaseHead, p.BaseHead, p.BaseFrom
	}
	return f
}

// newestResult is the newest RESULT.md under dir, "" when none.
func newestResult(dir string) string {
	var best string
	var bestT time.Time
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "RESULT.md" && (best == "" || info.ModTime().After(bestT)) {
			best, bestT = path, info.ModTime()
		}
		return nil
	})
	return best
}

// readResult reads a RESULT.md in the shape before the card contract: the head
// from a `rev: <sha>` line, a read's verdict from a `verdict: ok|broken` line,
// the report from the "## One line" section (else the first prose line with no
// colon). It stays because briefs still say that shape: a read brief tells its
// reader "`## Head` with `verdict: ok` or `verdict: broken`, `## One line`", and
// a card whose brief names no repository is never framed, so its child writes
// what its brief says. A work card's finish never rests on it (member.Judge
// wants the contract's shape); a read's verdict and report do, until the briefs
// that name that shape are gone.
func readResult(path string) (head, verdict, report string) {
	if path == "" {
		return "", "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	inOne := false
	first := "" // the first prose line, the report when there is no One line
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "rev:") && head == "" {
			head = strings.TrimSpace(strings.TrimPrefix(l, "rev:"))
		}
		if strings.HasPrefix(l, "verdict:") && verdict == "" {
			verdict = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "verdict:")))
		}
		if strings.HasPrefix(l, "## ") {
			inOne = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(l, "## ")), "one line")
			continue
		}
		if inOne && l != "" && report == "" {
			report = l
		}
		if !strings.HasPrefix(l, "#") && l != "" && !strings.Contains(l, ":") && first == "" {
			first = l
		}
	}
	if report == "" {
		report = first
	}
	if strings.ContainsAny(head, " \t") || len(head) > 64 {
		head = ""
	}
	return head, verdict, report
}

// envNameRE is an environment variable's name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// splitNames is a comma list of names, blanks dropped.
func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// childKept are the names a card's native child is started with, besides the
// prefixes in childKeptPrefixes and the secrets --pass names: what native, git
// and the harness need, and nothing that carries a credential.
var childKept = map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "LANG": true, "TERM": true, "USER": true, "LOGNAME": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true}

// childKeptPrefixes are the families of names a native child is started with:
// the locale, the Go toolchain's settings, nova-swarm's own, the XDG
// directories and the opencode harness's settings.
var childKeptPrefixes = []string{"LC_", "GO", "NOVA_SWARM_", "NOVA_TEST_", "XDG_", "OPENCODE_"}

// secretNameRE is a name that carries a credential: never handed to a child
// unless --pass names it, whatever the lists above say.
var secretNameRE = regexp.MustCompile(`(?i)TOKEN|SECRET|PASSWORD|PASSWD|KEY|CREDENTIAL|AUTH`)

// childEnviron is the environment a card's native child starts with
// (docs/SPEC-CARD-CONTRACT.md, the child's environment): an allowlist, never a
// denylist. A name is kept when it is one of childKept or of a family in
// childKeptPrefixes and carries no credential, or when it is a secret pass
// names (the loop record's nova-secrets keys); everything else is dropped.
func childEnviron(env, pass []string) []string {
	passed := map[string]bool{}
	for _, n := range pass {
		passed[n] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		kept := childKept[name]
		for _, p := range childKeptPrefixes {
			kept = kept || strings.HasPrefix(name, p)
		}
		if passed[name] || (kept && !secretNameRE.MatchString(name)) {
			out = append(out, kv)
		}
	}
	return out
}

// localProviders are the providers a harness reaches with no key: a model on
// this machine.
var localProviders = map[string]bool{"ollama": true, "lmstudio": true, "llamacpp": true, "local": true}

// passNote is the one NOTE line a member prints at its start when no secret is
// handed to its children (--pass empty, no worker secret, no --auth file) and
// its model is not a local one: the children start with no provider key and
// fail at the provider (docs/SPEC-CARD-CONTRACT.md, the child's environment).
func passNote(model string, pass []string, auth string) string {
	provider, _, _ := strings.Cut(model, "/")
	if len(pass) > 0 || auth != "" || localProviders[strings.ToLower(provider)] {
		return ""
	}
	return "NOTE member --pass names no secret: a child's harness that reads its provider key from the environment starts without it and fails at the provider; run: nova-swarm member ... --pass <KEY> (the loop record's nova-secrets keys)"
}

// yieldRefusal is why a member will not start on an OS with no setpriority
// (yield.Supported false): native refuses every card there rather than run it at
// the priority of the CI legs beside it (nova-ci local refuses the same way), so
// a member would take and fail every card it is dealt (nova-tools#4293). "" where
// a launch can step behind CI.
func yieldRefusal(supported bool, goos string) string {
	if supported {
		return ""
	}
	return "no setpriority on " + goos + ": native would refuse every card this member takes rather than run it at CI's priority; run members on darwin or Linux"
}

// lockedWriter is a writer two goroutines share, one Write at a time: the member's loop and
// its beat (memberLoop).
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return fmt.Fprint(l.w, string(p))
}

// mirrorsEnv names the repositories (a `url,...` list) a member keeps a bare mirror of under
// ~/nova-bench/mirror, fetched at start and after landings, with the shared build cache
// warmed at each one's HEAD branch tip. An environment word and not a flag: a unit's
// environment is where a bench's repositories are named, and the usage line stays as it is.
const mirrorsEnv = "NOVA_SWARM_MIRRORS"

// mirrorEvery is the least time between two refreshes of a member's mirrors: a landing the
// member sees is fetched at the next pass past it.
const mirrorEvery = time.Minute

// mirrorKeeping is the member's warm mirrors (docs/SPEC-SWARM.md, the warm clones and caches
// card): keep, which a pass calls with the time, starts a refresh of every mirror in the
// background when none runs and mirrorEvery has passed (the first call, at start, always),
// A card's clone step reaches the mirrors by the path the card names (PointCardAtMirrors),
// never by an environment word: the harness's environment is rebuilt, and its HOME is the
// slot's. A refresh that fails is a NOTE and never stops a pass: staging reads the mirror
// it finds, and a cold cache costs time only.
func mirrorKeeping(urls, root, gocache string, stdout, stderr io.Writer) (keep func(time.Time)) {
	home, err := os.UserHomeDir()
	if urls == "" || err != nil {
		return nil
	}
	var keepers []swarm.MirrorKeeper
	for _, u := range splitNames(urls) {
		name := strings.TrimSuffix(filepath.Base(strings.TrimSuffix(u, "/")), ".git")
		keepers = append(keepers, swarm.MirrorKeeper{Origin: u, Mirror: swarm.MirrorPath(home, name), WarmDir: filepath.Join(root, "cache", "warm", name), GoCache: gocache})
	}
	var busy atomic.Bool
	var last time.Time
	return func(now time.Time) {
		if !last.IsZero() && now.Sub(last) < mirrorEvery || !busy.CompareAndSwap(false, true) {
			return
		}
		last = now
		go func() {
			defer busy.Store(false)
			for _, k := range keepers {
				res, err := k.Refresh(context.Background())
				switch {
				case err != nil:
					fmt.Fprintf(stderr, "nova-swarm member: NOTE mirror %s: %s\n", oneline.Field(k.Mirror), oneline.Escape(err.Error()))
				case res.Created || res.Moved:
					fmt.Fprintf(stdout, "MIRROR %s created=%t tip=%s\n", oneline.Field(k.Mirror), res.Created, oneline.Field(res.Tip))
				}
			}
		}()
	}
}

// scriptVerify is a reader's member.Config.ScriptVerify (docs/SPEC-SPRINT.md, the script
// read): a read of a script card is asked first of a ScriptVerifier that checks the head
// out of the bench mirror of the card's repository, runs the card's program in the same
// wall the member runs its script steps in, and compares the program's diff with the
// head's. The verifier is built per packet because the mirror is the card's repository's;
// the checkouts sit under root, the reader's own work dir. sandbox names the wall binary
// ("" resolves nova-sandbox on PATH) and noWall runs the program unconfined, as the
// member's own --no-wall does for its children.
func scriptVerify(root, sandbox string, noWall bool) func(member.Packet, cardhdr.Class) (bool, string) {
	base := filepath.Join(root, "script-read")
	return func(p member.Packet, class cardhdr.Class) (ok bool, why string) {
		repo := swarm.ReadCardBase([]byte(p.Brief)).Repo
		home, _ := os.UserHomeDir()
		mirror := swarm.FindBenchMirror(home, repo)
		if mirror == "" {
			return false, "no bench mirror for " + repo
		}
		if err := os.MkdirAll(base, 0o700); err != nil {
			return false, "the script read's work dir " + base + ": " + err.Error()
		}
		v := member.ScriptVerifier{Mirror: mirror, Temp: base, Run: scriptRun(base, sandbox, noWall)}
		return v.Verify(p, class)
	}
}

// scriptRun runs a script card's program in the member's wall (step.go's cardtree.Wall):
// the checkout and a private temp its only writes, the network denied, the toolchain and
// the checkout's borrowed objects readable, all under ctx (the card's deadline). It is the
// one place the script read starts a process, so a test can hand Verify a fake instead.
func scriptRun(base, sandbox string, noWall bool) func(context.Context, string, []string) error {
	return func(ctx context.Context, dir string, argv []string) error {
		wall, why := stepWall(sandbox, noWall)
		if why != "" {
			return errors.New(why)
		}
		work, err := os.MkdirTemp(base, "wall-")
		if err != nil {
			return err
		}
		defer func() { _ = safepath.RemoveUnder(base, work) }() // ignored: the work dir is this read's own, under the reader's work dir the pool sweeps
		bin, tmp := filepath.Join(work, "bin"), filepath.Join(work, "tmp")
		for _, d := range []string{bin, tmp} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		wall.Tmp = tmp
		if wall.Bin != "" {
			wall.Read = stepReads(dir, bin, benchPasswdHome())
		}
		run := argv
		if wall.Bin != "" {
			run = append([]string{wall.Bin}, wall.Argv(dir, argv)...)
		}
		cmd := subproc.Context(ctx, run[0], run[1:]...)
		cmd.Dir, cmd.Env = dir, wall.Env(os.Environ())
		return cmd.Run()
	}
}
