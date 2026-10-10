package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/yield"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/cardtree"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/harness"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// THE NATIVE OPENCODE EXECUTION PATH. A frozen run
// configuration is a set of fields the caller hands over complete; nothing in it
// is derived on this side, and the run starts exactly one child bound to exactly
// those fields. This is the path a native `opencode` binary executes on, not the
// runner that passes a prompt FILE to a harness selected by a worker
// description. Here the model, the card text, the auth copy, the slot and the
// deadline are all in the configuration, and the child is
// `<binary> run --model <provider/model> --title <label> -- <card text>`, the shape the
// providers table declares and swarm.LaunchArgvFor builds (nativeLaunchArgv).

// nativeRunConfig is the frozen configuration of one native run.
type nativeRunConfig struct {
	binary   string        // the harness binary path, resolved once
	model    string        // provider/model, one slash, both sides nonempty
	label    string        // the --title this run is labelled with
	card     []byte        // the card text, passed as the message, byte-for-byte
	slotDir  string        // the slot directory; HOME is a data dir beneath it
	root     string        // the configured root the slot directory must sit under
	authFile string        // optional: an auth file to copy one entry out of
	deadline time.Duration // the wall bound that kills the child
	idle     time.Duration // the idle bound: neither log nor process tree moving this long ends the card (0 = no watch)
	repos    []string      // repositories a card may clone (owner/name): network to
	// github.com only, expressed as a wall host rule
	recipients []string // bus lanes a card may address; default none, and a bus
	// send is denied inside the wall regardless
	sandbox        string // the nova-sandbox binary naming the wall; "" = resolve on PATH
	noWall         bool   // the caller typed --no-wall: run with no containment, named by its OK line
	noSharedCaches bool   // the caller typed --no-shared-caches: the Go caches stay under HOME as today
	configFile     string // optional: an opencode.json provider config copied beside the auth copy
	// benchHome is the BENCH's home -- this process's own, never the child's -- and it is
	// where the provisioning standard puts the toolchain (swarm.ToolchainRoots). Empty is
	// the ordinary case and means "ask the OS"; a test names a home of its own, because the
	// argv has to be assertable without the machine's real toolchain under it.
	benchHome string
	// benchOS is the operating system whose toolchain list the wall is built from -- this
	// bench's own, because the wall contains a card on THIS machine. Empty is the ordinary
	// case and means runtime.GOOS; a test names one, so the linux list is assertable from a
	// Mac and the darwin list from a linux runner.
	benchOS string
	// WORKER: the worker description `--worker <file>` names, when one is
	// given. It is the source of the model -- a key is authorized for one model only, and
	// the description pins it -- and when it carries "secret": "<NAME>" it is the source of
	// the key, taken from the environment and passed through by name, with no auth file
	// ever written. nil means native keeps --model and --auth as today.
	worker *swarm.Worker
	// identity is the pool identity the loop's argv names (--identity, a nova-config
	// loop record), the name and email every commit carries; nil reads the pool's
	// <root>/identity.tsv (swarm.LoadPoolIdentity).
	identity *swarm.StagingIdentity
	// usd is the dollar budget (--usd, a route's usd): the harness's reported cost
	// at which the card is stopped beside the token budget; nil for none.
	usd *big.Rat
	// netAllow is the provider's loopback host:port, passed to the wall as --net-allow.
	netAllow string
	// bodySilence is the gap, after response headers, with no body bytes, that
	// ends the attempt UNKNOWN. Zero means ProviderBodySilence (45s). Production
	// leaves it zero. A test may set a shorter gap so the suite does not wait 45s.
	bodySilence time.Duration
	// bodyAfter arms that gap. Nil means the timer inside readWithinSilence.
	// A test passes a clock it can fire so the 45s gap is an event.
	bodyAfter func(time.Duration) <-chan time.Time
	// deadlineFn, when set, is this one run's deadline event, in place of the
	// package seam nativeDeadline. A test hands the wait a deadline that fires
	// when the child it means to kill is there -- never a wall clock -- and it
	// stays a field on this configuration rather than a package var, so the
	// test runs in parallel with the others instead of swapping the seam under
	// them. Nil is production's timer.
	deadlineFn func(time.Duration) (<-chan time.Time, func() bool)
	// headerWait is how long the proxy waits for response headers after the
	// request is written; expiry ends the attempt UNKNOWN. Zero means
	// ProviderHeaderTimeout (45s). Production leaves it zero.
	headerWait time.Duration
	// onProxy receives the proxy once it is listening, before the child starts.
	// Nil in production.
	onProxy func(*swarm.ProviderProxy)
	// startSleep is the wait before a failed start is launched again (harnessStartWaits);
	// nil is the real wait (startSleep). A test records the schedule through it.
	startSleep func(time.Duration)
	// onPhase is called at the entry of each named phase function (prepare,
	// wall, start, watch, collect, report). Nil in production.
	onPhase func(phase string)
	// resultsRoot is where RESULT.md, usage.tsv and the report are published,
	// outside the job directory a sweep deletes. Empty means the
	// caller did not ask: the direct tests keep the files in the job directory.
	// The verb itself always names one, derived from --root when --results-root
	// is absent, because that root was already given.
	resultsRoot string
	// runID is this invocation's directory under <results-root>/<label>/. It is
	// claimed once, shared by every attempt of this run, and never reused: a
	// later invocation of the same label gets its own id, so attempt numbers
	// that restart at 1 cannot overwrite the previous run.
	runID string
	// THE BUDGET (docs/SPEC-SWARM.md, the rule that the swarm's own tokens are
	// budgeted per job). Every native launch carries the word:
	// `tokens` is the number the caller named and `unmetered` is the caller's statement
	// that this provider has no live accounting and the deadline is the only stop. There
	// is no default and no third state -- cmdNative refuses a launch that named neither,
	// before any directory is made -- so a zero `tokens` beside a false `unmetered` cannot
	// reach this struct from the command line.
	//
	// THE TOOL INFERS NEITHER FROM THE PROVIDER. No test can tell a provider that
	// costs money from one that does not: a provider's name is only what a config
	// file calls it, and a `baseURL` can point a local-looking name at a metered
	// endpoint. Nor from usage: a reported `0` is
	// a measurement that adds nothing to the sum and is never a reason to print
	// `unmetered`.
	tokens    int
	unmetered bool
	// usageInterval is how often the live sampler (startLiveSampler) reads the harness's
	// database while the launch runs (the budget rule). cmdNative has already refused one under a
	// second and one not shorter than the deadline, so what reaches here is a usable interval.
	usageInterval time.Duration
	// benchName is the name of this bench; "" means resolve via os.Hostname.
	benchName string
	// stageTimeout is the hard timeout for staging (default 120s).
	stageTimeout time.Duration
	// borrowed is the object directory the staged checkout borrows (the bench mirror's,
	// swarm.MirrorCloneArgs), a read of the wall; "" when its objects are its own.
	borrowed string
	// frame, when set, is the member's frame of this launch (docs/SPEC-CARD-CONTRACT.md):
	// staging stages its commit on its branch, and its profile writes JOB.md and the shims.
	frame *cardcontract.Frame
	// decider, when set, is the decide read's backend and clock (nativedecide.go): a
	// test's; nil is Jev over its real transport with the key JEV_API_KEY holds, and the
	// wall clock.
	decider *decider
	// gateRun, when set, is the gate decision's test runner (nativegate.go): a test's; nil
	// runs go test in the child's wall with the child's environment.
	gateRun gateRunner
}

// headless is the headless harness this run's binary is (pkg/harness: claude, codex
// or grok, by the program's name), and "" for an opencode launch through the providers
// table (docs/SPEC-SWARM.md, the headless harnesses).
func (cfg nativeRunConfig) headless() string {
	if k := harness.KindOf(cfg.binary); harness.IsHeadless(k) {
		return k
	}
	return ""
}

// nativeRunResult is what one run records when the child has gone.
type nativeRunResult struct {
	rc           int               // the child's exit code; -1 when the deadline killed it
	wallSeconds  float64           // the wall the run took
	wall         string            // the wall's own name from its SANDBOX OK line, or "none"
	cardSHA256   string            // sha256 of the card text, lowercase hex
	binarySHA256 string            // sha256 of the harness binary, lowercase hex
	job          string            // the job directory <slot>/jobs/<label> the child ran in
	root         string            // the resolved swarm root; the sweep removes the job only under it
	resultsDir   string            // <results-root>/<label>/<attempt> when the publish landed, else ""
	usageState   string            // the store path the NATIVE OK line names when no store answered, "" otherwise
	usageReason  string            // no-rows | no-store | no-sqlite3 | query-failed, "" when the store answered
	configSHA    string            // sha8 of the carried provider config, "" when --config named none
	tmp          string            // the TMPDIR the child was handed, <slot>/tmp/<label>, never a repo
	harness      string            // ok | silent: silent when the capture holds no words of the child's and no result was found
	fence        string            // the first path the harness's own fence auto-rejected, "" when it rejected nothing
	wallReport   string            // the WALL report line when the fence stopped the card and it published nothing
	reason       string            // harness-silent when the child exited 0 but wrote no report, "" otherwise
	wallRefusal  swarm.WallRefusal // the path and step a wall refused, zero when it refused nothing
	shellDenial  swarm.ShellDenial // a denial the card's own shell reported, zero when it reported none
	end          string            // the end the usage row records: done, failed, wall, or unknown
	lost         bool              // the provider read died after the request may have been accepted
	unrecorded   bool              // the unknown could not be written anywhere the next reader looks
	terminated   bool              // a TERM from outside ended the run mid-flight, not the deadline
	deadlined    bool              // the deadline timer ended the run: the child was still working at its wall
	survivors    string            // what the harness left in its group when it exited on its own: "", <pgid>:reaped or <pgid>:alive
	starts       int               // the harness starts tried when every one failed (harnessStartFailed), else 0
	// THE JOB'S OWN FIGURE (the budget rule keeps two numbers apart: the row is the launch's and
	// the line is the job's). These three are the JOB's -- the sum over every launch of
	// this one invocation of `native` -- and they are what the NATIVE OK line's `budget=`
	// renders. The per-launch usage ROW is written elsewhere (writeNativeUsage) and carries
	// that launch's own figures, never these: a job's rows are disjoint, so that adding
	// them counts each launch once, and two launches reported at 40 and 70 under
	// `--tokens 100` keep 40 and 70 in their rows while the line prints 110/100.
	spent    int  // the observed sum over the whole job at the final read
	observed bool // any budget column was a number at all
	partial  bool // some budget column was a dash: the plus on the line
	// spend is the job's `spend=` word (cardcost.SpendWord): its tokens by class, requests,
	// largest prompt, the harness's own cost and its model, folded over every launch's final
	// read, so a card's cost record carries what the run spent; "" when no read answered.
	spend string
	// stopped is the `stopped=<tokens|max_turns|max_cache_read|unverifiable>` field of rule
	// 13d, and "" for a card the machinery did not stop under that rule. It is a KEY OF ITS
	// OWN (decision 17): `reason=terminated` stays what a TERM from outside prints, and the
	// `reason=` inside the `usage=none` group stays the usage read's.
	stopped string
	// stoppedWhy is the last failed read's own reason when stopped is `unverifiable`
	// (liveSampler.Why), and "" for every other stop: it is the only record of WHAT the
	// three reads said, so the NATIVE BUDGET line carries it.
	stoppedWhy string
	// defect is the PROMPT-DEFECT line a card budget's stop owes. The budget rule prints it on
	// native's own stdout AFTER the NATIVE OK line and writes it into NO file.
	defect      string
	idleEnd     swarm.IdleEnd // the watch ended this card: how long it had been still, the step, and any refusal it never moved past
	idled       bool          // the idle watch ended the run, not the deadline and not the child
	blockedPath string        // the report the run wrote FOR a card that published none, "" when it wrote none
	// usage is the LAST attempt's usage row, exactly as it was appended to usage.tsv. It
	// is carried out of the run so the card-end event carries the numbers the row carries
	// -- tokens_in, tokens_out, usd, provider, model -- rather than a second reading of
	// the provider store that could disagree with the file.
	usage swarm.UsageRow
}

// THE ONE SEAM IN THE IDLE PATH, AND WHY IT HAD TO EXIST.
//
// The wait below ends a launch in one of four ways and three of them are events the run
// can be TOLD about: the watch declared the card idle, the deadline timer fired, a TERM
// arrived. A test that wanted to assert what the run DOES with an idle end had no way to
// deliver one, so the first idle tests delivered it by arranging real time -- `--idle 2s`
// against a `FAKE-SLEEP 60` child -- and then asserted on what the run had printed by
// then. That is a wall-clock assertion wearing an event's clothes, and under the gate's
// whole-suite load (GOMAXPROCS=8 -p 2 -parallel 4 across the repo) the arrangement stopped
// holding: `TestNativeIdleEndsAStillCardLongBeforeItsDeadline` went red in landing batch
// 16an while its own ci-ok was green, because ci.yml's CL tier shards only the
// touched packages and never puts the machine under that load.
//
// These vars are the whole fix on the production side. They are the real functions,
// byte for byte, and nothing about the run's behaviour is decided by them being variables:
// no call site changed except the name it is reached through, and no test sets them in a
// run that is not testing the wait itself. A test may now hand the wait an idle end
// directly and assert the ORDER of what follows -- idle declared, then the group reaped,
// then the card ended with the idle reason -- and assert that the deadline branch never
// ran, because the deadline branch is the only one that calls nativeKillGroup.
var (
	nativeWatchIdle = swarm.WatchIdle
	nativeReap      = swarm.Reap
	nativeKillGroup = swarm.KillGroup
	// nativeDeadline is the fourth event the wait can be told about. The
	// deadline test arranged it with real time -- `--deadline 3s` and a 5 s bound on the
	// WHOLE run, setup and teardown included -- and on hosted macOS that run took 6.08 s
	// and 6.13 s at 2a43d771 (3.04 s on a local macOS bench) with the kill unchanged. The binary
	// gets the real timer, byte for byte; a test hands the wait a deadline that fires when
	// the tree it means to kill is actually there.
	nativeDeadline = func(d time.Duration) (fire <-chan time.Time, stop func() bool) {
		t := time.NewTimer(d)
		return t.C, t.Stop
	}
)

// nativeEndLeftovers ends what a harness that exited on its own left in its process
// group (docs/SPEC-CARD-CONTRACT.md, the finish; docs/SPEC-SWARM.md, `native`): a
// grandchild that kept running (a language server, a watcher, a shell's `&`) holds the
// harness's pipes and outlives the card. The harness leads its own group from its start
// (ownChildGroup), so the group is signalled whole: a terminate, swarm.TerminateGrace, then
// a kill. It returns "" when the group was already empty, else the group and how it ended,
// <pgid>:reaped or <pgid>:alive (something outlived the kill), for the NATIVE line.
func nativeEndLeftovers(pgid int, started string) string {
	if !swarm.GroupAlive(pgid, started) {
		return ""
	}
	if nativeReap(pgid, started, swarm.TerminateGrace) {
		return strconv.Itoa(pgid) + ":alive"
	}
	return strconv.Itoa(pgid) + ":reaped"
}

type nativePrepared struct {
	cfg          nativeRunConfig
	startTime    time.Time
	bin          string
	provider     string
	launch       []string
	jobDir       string
	releaseLease func()
	releaseSlot  func()
	dataHome     string
	tmpDir       string
	cacheDir     string
	shimDir      string
	shimShell    string
	goBin        string
	toolPath     []string
	configSHA    string
	proxy        *swarm.ProviderProxy
	binaryHash   string
	cardHash     [32]byte
	stageRes     swarm.StageResult
	decided      string
	workStart    string
	stepArgv     []string
}

type nativeWalled struct {
	runPath string
	runArgv []string
	wall    string
	niced   bool
}

type nativeStarted struct {
	cmd            *exec.Cmd
	pgid           int
	started        string
	done           chan error
	deadlineC      <-chan time.Time
	stopDeadline   func() bool
	idleC          <-chan swarm.IdleEnd
	stopWatch      chan struct{}
	attemptStart   time.Time
	before         int64
	providerBefore int64
}

type nativeWatched struct{}

type nativeCollected struct {
	retry bool
}

type nativeRunState struct {
	prep          *nativePrepared
	wal           *nativeWalled
	childEnv      []string
	devNull       *os.File
	log           *os.File
	harnessOut    *os.File
	outLog        string
	providerMark  int64
	captureMark   int64
	runStart      time.Time
	wallOut       bytes.Buffer
	timeline      *swarm.Timeline
	reader        *swarm.WallReader
	denials       *swarm.ShellDenialReader
	capture       io.Writer
	res           nativeRunResult
	grace         time.Duration
	termCh        chan os.Signal
	bodyStall     <-chan struct{}
	sampler       *liveSampler
	jobSpent      int
	jobObserved   bool
	jobPartial    bool
	launchSpends  []cardcost.Usage
	prevLaunchEnd time.Time
	startRetries  int
	lastAttempt   int
	runCtx        context.Context
	stopRun       context.CancelFunc
}

// prepare validates configurations, resolves absolute paths, establishes leases,
// prepares data and cache directories, shell shims, provider configuration, staging,
// and frame setup.
func prepare(cfg nativeRunConfig, errOut io.Writer) (*nativePrepared, nativeRunResult, int) {
	if cfg.onPhase != nil {
		cfg.onPhase("prepare")
	}
	startTime := time.Now()
	// (0) ABSOLUTE PATHS. The slot and the root are turned absolute AND symlink-resolved at
	// admission so a relative spelling cannot reach the wall (which refuses `--read ./x` and
	// `--write x/...`), and so the run's own paths cannot disagree with each other: on darwin
	// `/var` is a symlink to `/private/var`, so an absolute spelling and a relative one of one
	// directory came out as two different names.
	abslot, err := swarm.AbsResolved(cfg.slotDir)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the slot directory %s could not be made absolute: %s", oneline.Field(cfg.slotDir), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	cfg.slotDir = abslot
	absroot, err := swarm.AbsResolved(cfg.root)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the configured root %s could not be made absolute: %s", oneline.Field(cfg.root), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	cfg.root = absroot
	if strings.TrimSpace(cfg.resultsRoot) != "" {
		absResults, err := swarm.AbsResolved(cfg.resultsRoot)
		if err != nil {
			refuseNative(errOut, fmt.Sprintf("the results root %s could not be made absolute: %s", oneline.Field(cfg.resultsRoot), oneline.Escape(err.Error())))
			return nil, nativeRunResult{}, 2
		}
		cfg.resultsRoot = absResults
	}

	// THE PUBLIC-CLASS GATE (CARD-8390): a public-class worker never sees a
	// card that clones an unlisted repo. The card is refused with CARD REFUSED
	// before any directory is made and before any child starts.
	if cfg.worker != nil && cfg.worker.IsPublic() {
		if repo, refused := swarm.CheckPublicCard(*cfg.worker, string(cfg.card), cfg.root); refused {
			fmt.Fprintln(errOut, swarm.PublicRefusalLine(repo, cfg.worker.Name))
			return nil, nativeRunResult{}, 1
		}
	}

	// (1) THE BINARY. Resolved once, on PATH when the name has no separator, then
	// checked for existence and the execute bit. A missing binary and an
	// unexecutable one are the same refusal class, one line each.
	bin := cfg.binary
	if !strings.ContainsRune(bin, filepath.Separator) {
		found, err := exec.LookPath(cfg.binary)
		if err != nil {
			refuseNative(errOut, fmt.Sprintf("the harness binary %s is missing", oneline.Field(cfg.binary)))
			return nil, nativeRunResult{}, 2
		}
		bin = found
	}
	if _, err := os.Stat(bin); err != nil {
		refuseNative(errOut, fmt.Sprintf("the harness binary %s is missing", oneline.Field(bin)))
		return nil, nativeRunResult{}, 2
	}
	// The execute question is asked by the platform's own rule, never by the unix bit
	// alone: windows carries no such bit and reports 0666 for every file, so reading it
	// there refused every harness that existed. See isExecutable in executable.go.
	if !isExecutable(bin) {
		refuseNative(errOut, fmt.Sprintf("the harness binary %s is not executable", oneline.Field(bin)))
		return nil, nativeRunResult{}, 2
	}
	// A headless harness is launched by its resolved path: the wall reads its install, and
	// never the bench's login directory the symlink chain to it may run through
	// (swarm.HeadlessProgramRoot).
	if cfg.headless() != "" {
		if real, err := filepath.EvalSymlinks(bin); err == nil {
			bin = real
		}
	}

	// (2) THE MODEL. Native routes are named provider/model, and a model id with no
	// provider prefix cannot be given to any harness. providerOf splits on the one
	// slash and refuses a missing side or a slash inside the provider.
	provider, ok := providerOf(cfg.model)
	if !ok {
		refuseNative(errOut, fmt.Sprintf("the model %q has no provider prefix (a native model is provider/model, one slash, both sides nonempty)", cfg.model))
		return nil, nativeRunResult{}, 2
	}

	// (2a) THE ONE LAUNCHER. The harness argv comes from the providers
	// table, never a literal here: the route's provider row (or the table's default row)
	// gives the shape, and this run's binary, model, label and card fill it. A table that
	// cannot be read is refused before any directory is made.
	launch, err := nativeLaunchArgv(bin, cfg, provider)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the providers table gave no argv for %s: %s", oneline.Field(cfg.model), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}

	// (2b) THE WORKER DESCRIPTION. When `--worker <file>` names a
	// description, the description is the source of the model: a key is authorized for ONE
	// model only, and the description pins that one. The gate compares provider/model as
	// ONE NAME: a description's `model` without a slash takes the description's
	// `provider` as its prefix, so `deepseek-v4-flash` under provider `opencode` is
	// `opencode/deepseek-v4-flash`, the same name `--model` carries. A mismatch is
	// refused, naming BOTH models on one line, before any directory is made and before any
	// child starts. Without --worker, native keeps --model as today.
	if cfg.worker != nil {
		pinned := cfg.worker.Model
		if !strings.Contains(pinned, "/") {
			pinned = cfg.worker.Provider + "/" + pinned
		}
		if pinned != cfg.model {
			refuseNative(errOut, fmt.Sprintf("--model %s differs from the worker description's model %s; a key is authorized for one model only, and the description pins the model this run launches",
				oneline.Field(cfg.model), oneline.Field(cfg.worker.Model)))
			return nil, nativeRunResult{}, 2
		}
		// A description naming "secret": "<NAME>" takes the key from THIS process's own
		// environment -- `nova-secrets exec` set it around the run -- and the value is
		// never written to a file, never printed, and never in a REFUSED or OK line. An
		// absent or empty variable is refused HERE, before anything runs, the way run and
		// supervise refuse it.
		if cfg.worker.Secret != "" {
			if _, err := swarm.SecretFromEnv(cfg.worker.Secret); err != nil {
				refuseNative(errOut, oneline.Escape(err.Error()))
				return nil, nativeRunResult{}, 2
			}
		}
	}

	// (3) THE SLOT IS UNDER THE ROOT. A slot outside the configured root is a write
	// this run has no business making, and it refuses before any directory is made.
	if !within(cfg.root, cfg.slotDir) {
		refuseNative(errOut, fmt.Sprintf("the slot directory %s is outside the configured root %s",
			oneline.Field(cfg.slotDir), oneline.Field(cfg.root)))
		return nil, nativeRunResult{}, 2
	}

	// (3a) THE LABEL IS A NAME, NOT A PATH. The slot is checked against the
	// root above; that check is not the whole of it, and the directories this run
	// then makes, leases and hands the wall as write roots are <slot>/jobs/<label> and
	// <slot>/tmp/<label>, and a label is a string a card's own TSV row can spell. A label of
	// `../../../OUTSIDE` makes both of those joins a path OUTSIDE the swarm root: MkdirAll
	// makes it, StartJobLease publishes and then os.Remove's `.lease` inside it, and
	// nativeSandboxArgv passes it to the wall as --write. within(root, slotDir) is true of
	// that launch; nobody asked within(root, jobDir). So the name is judged as a NAME, by
	// the same safepath.NameOK the hygiene verbs already use on a job name, before it is
	// joined into anything.
	// An empty label is the callers' own shape, not a walk: cmdNative fills it from the
	// card's basename and a test that leaves it empty joins nothing. It is judged by the
	// two within checks below like any other derivation.
	if cfg.label != "" && !safepath.NameOK(cfg.label) {
		refuseNative(errOut, fmt.Sprintf("the label %s is not a job name: use letters, digits, dot, dash or underscore, with no path separator, no leading dash and no %s -- the label is joined into the job directory, the temp directory and the wall's write set, so a label that walks names a directory outside the root",
			oneline.Field(cfg.label), oneline.Field("..")))
		return nil, nativeRunResult{}, 2
	}

	// The job directory is where the child runs and writes: <slot>/jobs/<label>, made here
	// before the child starts, so the card's cwd exists and the card is told its place by
	// that cwd (docs/SPEC-SWARM.md). HOME is a data directory under the slot directory; the
	// child is pointed at it and nothing above it.
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	// THE RESULTS ROOT IS NOT THE JOB. A sweep deletes the job
	// directory. Publishing into it, or into a directory inside it, would make
	// the copy the sweep removes.
	if cfg.resultsRoot != "" && within(jobDir, cfg.resultsRoot) {
		refuseNative(errOut, fmt.Sprintf("the results root %s is inside the job directory %s; pass a --results-root outside the directory a sweep deletes",
			oneline.Field(cfg.resultsRoot), oneline.Field(jobDir)))
		return nil, nativeRunResult{}, 2
	}
	// The join is checked as well as the name: NameOK above makes this true by
	// construction, and a derivation that decides where a card writes is checked anyway,
	// because the cost of the two being out of step once is a MkdirAll and a --write
	// outside the swarm root.
	if !within(cfg.root, jobDir) || !strictlyWithin(cfg.slotDir, jobDir) {
		refuseNative(errOut, fmt.Sprintf("the job directory %s is not strictly below the slot %s and the root %s",
			oneline.Field(jobDir), oneline.Field(cfg.slotDir), oneline.Field(cfg.root)))
		return nil, nativeRunResult{}, 2
	}
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		refuseNative(errOut, fmt.Sprintf("the job directory %s could not be made: %s", oneline.Field(jobDir), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	// THE LEASE. The bench's hygiene pass reaps job directories, and it reads the capture
	// alone: a capture quiet for fifteen minutes is what one long model call looks like,
	// and a live card reaped on that quiet is what the lease prevents. <job>/.lease
	// carries this process's pid and a heartbeat for as long as the child runs, and the
	// reaper never touches a leased job or the slot's data/ and tmp/ around it. It is
	// released, and the file removed, when this run returns by any path.
	//
	// AND IT IS THE JOB DIRECTORY'S OWNERSHIP. Two `native` runs on one physical
	// <slot>/jobs/<label> hold one set of paths: the bench store gives each its own seat,
	// but the job directory, the data home, the temp directory and the logs under it are
	// ONE set, and the first run to exit removes the other's lease. docs/SPEC-SWARM.md
	// gives a worker its own data home and its own job directory and a slot to exactly one
	// worker, and its failure table closes two workers on one data home as the
	// `database is locked` race. So the second run is REFUSED rather than made safe, and
	// it is refused HERE -- the take is the first thing this verb does to the job directory
	// that was not already there, and nothing of the holder's is touched on the way out.
	releaseLease, err := swarm.StartJobLease(jobDir, cfg.label)
	if err != nil {
		if held, ok := swarm.HeldJobLease(err); ok {
			refuseNative(errOut, fmt.Sprintf("the job directory %s is held by a live run: pid=%d host=%s label=%s started=%s; two runs in one job directory share one data home, one temp directory and one set of logs, and the first of them to end removes the other's lease -- give the second run a job directory of its own",
				oneline.Field(jobDir), held.PID, oneline.Field(held.Host),
				oneline.Field(held.Label), oneline.Field(held.Started)))
			return nil, nativeRunResult{}, 2
		}
		// A take that establishes nothing would hand
		// back a do-nothing release and the launch would go on -- with `.lease` an owned
		// directory, BOTH of two runs would be told they hold the place. A run that cannot
		// prove it owns its job directory does not start.
		refuseNative(errOut, fmt.Sprintf("the job lease on %s could not be taken, so this run cannot prove it owns its job directory and will not start: %s; clear or repair %s and run it again",
			oneline.Field(jobDir), oneline.Escape(err.Error()), oneline.Field(filepath.Join(jobDir, swarm.JobLeaseName))))
		return nil, nativeRunResult{}, 2
	}
	// THE SLOT IS HELD BY EXACTLY ONE WORKER. The job lease above refuses a
	// second run in the same <slot>/jobs/<label>. It cannot refuse a second run in the same
	// SLOT under a different label, and the data home below is per SLOT, not per job: two
	// labels in one slot is one HOME, one cache and one opencode.db, which is the
	// `database is locked` race docs/SPEC-SWARM.md closes. The bench store
	// cannot answer this -- its lease is a count and names no directory -- so the slot says
	// it itself, with the same lease machinery and the same four rules, and it is taken
	// HERE, after the job lease, so that same-slot-same-label keeps saying what slot ownership requires.
	releaseSlot, err := swarm.StartSlotLease(cfg.slotDir, cfg.label)
	if err != nil {
		releaseLease()
		if held, ok := swarm.HeldJobLease(err); ok {
			refuseNative(errOut, fmt.Sprintf("the slot %s is held by a live run: pid=%d host=%s label=%s started=%s; two runs in one slot share one data home, one cache and one opencode.db -- give the second run a slot of its own",
				oneline.Field(cfg.slotDir), held.PID, oneline.Field(held.Host),
				oneline.Field(held.Label), oneline.Field(held.Started)))
			return nil, nativeRunResult{}, 2
		}
		refuseNative(errOut, fmt.Sprintf("the slot lease on %s could not be taken, so this run cannot prove it holds the slot alone and will not start: %s; clear or repair %s and run it again",
			oneline.Field(cfg.slotDir), oneline.Escape(err.Error()), oneline.Field(filepath.Join(cfg.slotDir, swarm.SlotLeaseName))))
		return nil, nativeRunResult{}, 2
	}
	dataHome := filepath.Join(cfg.slotDir, "data")
	if err := os.MkdirAll(dataHome, 0o755); err != nil {
		releaseSlot()
		releaseLease()
		refuseNative(errOut, fmt.Sprintf("the data directory %s could not be made: %s", oneline.Field(dataHome), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	// TMPDIR is the slot's own tmp/<label>, never the job directory (which admission git-inits
	// into a repo): a card's temp dir inside a repo makes tests checking for a non-bus directory
	// fail for a reason the card did not cause. The slot
	// directory is never a repo, so a temp file made here sits outside every repository the
	// card's work could touch. It is made here so the child's TMPDIR exists before it starts.
	tmpDir := filepath.Join(cfg.slotDir, "tmp", cfg.label)
	if !within(cfg.root, tmpDir) || !strictlyWithin(cfg.slotDir, tmpDir) {
		releaseSlot()
		releaseLease()
		refuseNative(errOut, fmt.Sprintf("the temp directory %s is not strictly below the slot %s and the root %s",
			oneline.Field(tmpDir), oneline.Field(cfg.slotDir), oneline.Field(cfg.root)))
		return nil, nativeRunResult{}, 2
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		releaseSlot()
		releaseLease()
		refuseNative(errOut, fmt.Sprintf("the temp directory %s could not be made: %s", oneline.Field(tmpDir), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	// THE SHARED PER-BENCH CACHE. The Go toolchain and every module are the
	// same for every card under one root, but each card left to itself downloads them
	// into its own data home -- up to 5 GB per slot, and 120 cards fill two benches to
	// 100%. The cache lives once under <root>/cache (a permitted write root beside the
	// job directory) and the child is pointed at it by GOMODCACHE, GOCACHE and NPM_CONFIG_CACHE.
	if !cfg.noSharedCaches && cfg.root != "" {
		if err := swarm.EnsureCacheDirs(cfg.root); err != nil {
			releaseSlot()
			releaseLease()
			refuseNative(errOut, fmt.Sprintf("the shared cache directories under %s could not be made: %s", oneline.Field(swarm.CacheRoot(cfg.root)), oneline.Escape(err.Error())))
			return nil, nativeRunResult{}, 2
		}
	}

	// (3b) THE BENCH-SHARED GO CACHES. Go derives GOMODCACHE and GOCACHE from
	// HOME, and a native run makes HOME the slot's data home, so every card downloads
	// its own copy of the module cache -- and a toolchain -- and grows a slot to five to seven
	// gigabytes. Instead the two caches live once per bench under <root>/cache, made here at
	// mode 0755 BEFORE the child can derive them and handed to the child as GOMODCACHE and
	// GOCACHE. GOTOOLCHAIN=local keeps a card from fetching a toolchain behind the bench's
	// back. The sharing is safe because Go's caches are concurrency-safe by design and the
	// module cache is read-mostly. --no-shared-caches keeps today's behaviour exactly: no
	// names set, the caches under HOME. swarm.EnsureCacheDirs above made both
	// (swarm.GoModCacheDir, swarm.GoBuildCacheDir), under the same condition.
	cacheDir := nativeCacheDir(cfg)

	// (3c) THE SHELL SHIM. The harness spawns its bash tool's shell by NAME,
	// resolving it through the child's PATH and SHELL, and hands it the harness's own
	// environment -- which carries the provider key. A `bash` and an `sh` wrapper are
	// written into <slot>/shim, which is inside the wall's read set and outside its write
	// set, and each unsets every KEY/TOKEN/SECRET name before exec'ing the real shell
	// (shellshim.go). A run whose shim cannot be written REFUSES: a card's shell carrying
	// the seat's key is the defect this closes, not a mode to fall back to.
	shimDir, shimShell, shimErr := writeNativeShellShims(cfg.slotDir)
	if shimErr != nil {
		releaseSlot()
		releaseLease()
		refuseNative(errOut, fmt.Sprintf("%s the card's shell cannot be scrubbed of the provider key: %s",
			oneline.Field(cfg.label), oneline.Err(shimErr)))
		return nil, nativeRunResult{}, 2
	}
	// (3d) THE GO SHIM (nova-tools#5174, cost rule 5): with the shared caches on, the go the
	// child runs by name adds -trimpath, so the machine's warm GOCACHE serves this checkout
	// and the card's gate does not compile the repository again (shellshim.go).
	goBin := swarm.BenchGoBin(benchOS(cfg), benchHome(cfg), os.Getenv("PATH"))
	toolPath := swarm.BenchPath(benchOS(cfg), benchHome(cfg), os.Getenv("PATH"))
	if cacheDir != "" {
		if err := writeNativeGoShim(shimDir, goBin); err != nil {
			releaseSlot()
			releaseLease()
			refuseNative(errOut, fmt.Sprintf("%s %s", oneline.Field(cfg.label), oneline.Err(err)))
			return nil, nativeRunResult{}, 2
		}
	}

	// (4) THE AUTH COPY. One entry, the model's provider's, moved to the data home so
	// the child's account resolves, and left mode 0600. A source that is looser than
	// 0600 is refused: its copy would spread a secret further than its owner.
	// --auth stays ONLY the legacy shape's: a description that names
	// "secret": "<NAME>" takes the key from the environment and writes no auth file, so
	// this step is skipped entirely for one. When a description IS given and --auth is
	// used, the auth copy is made and one note line says so.
	//
	// AND THE COPY DIES WITH THE CARD. The child reads the copy for as long as it runs
	// -- every launch of a retried card included -- and when the run returns by any path
	// the copy is removed: a plaintext key file that outlives its card is the bench
	// standard's plaintext-key drift (docs/SPEC-SECRETS.md, the dogfooding ten), left
	// in a data home the hygiene reap may not visit for days. The secret shape still
	// writes nothing at all.
	if cfg.authFile != "" {
		if reason := copyAuth(cfg.authFile, provider, dataHome); reason != "" {
			releaseSlot()
			releaseLease()
			refuseNative(errOut, reason)
			return nil, nativeRunResult{}, 2
		}
		if cfg.worker != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: --auth %s copies the provider secret into the job's data home on disk, mode 0600, and the copy is removed when the run ends; the legacy shape -- a description naming \"secret\": \"<NAME>\" would keep the key in the environment and write no auth file\n",
				oneline.Field(cfg.authFile))
		}
	}

	// (4b) THE PROVIDER CONFIG. The clean env carries the provider's auth entry
	// into the job's own XDG data home but no opencode.json, so every configured provider --
	// ollama, inception, zen -- is unknown to the harness and the run dies rc=1 in under a
	// second. --config copies an opencode.json beside the carried auth file, mode 0600, so
	// the harness resolves the provider exactly as it does when a person adds it to
	// ~/.config/opencode. Only THE MODEL'S OWN provider is checked: a config whose entry for
	// it has no key in --auth is refused before anything runs, naming the provider and never
	// the key; a provider whose options carry a baseURL and no apiKey field has no key to be
	// absent (ollama on localhost) and is admitted without one. Every other provider in the
	// file is carried verbatim and not checked -- this run never calls them, and checking
	// them refused local-model cards for an absent inception key on every adoption pass
	// for a card that does not use those providers.
	//
	// (4c) AND THE JOB'S OWN FENCE. The harness's `permission` block is written
	// into the SAME file, whether or not --config named one, because a run with no config at
	// all still runs under the harness's default fence -- which auto-rejects the job's own
	// `../scratch` and every read-only path a card names -- and that fence is what killed 8
	// cards. The block names this job's directories; the carried
	// provider config keeps its own bytes and its own rules beside them (pkg/swarm/fence.go).
	// On a walled bench the wall owns what the child may read, so only a --no-wall run takes
	// the card's `READ:` paths: with no OS wall there is nothing else to open them.
	//
	// (4d) AND THE DESCRIPTION'S OWN READ ROOTS, ON A WALLED RUN AS MUCH AS AN UNWALLED ONE
	// `read_roots` is the worker description's declaration of what every job
	// of this worker may READ -- a bench-local mirror, a corpus, a toolchain under a user
	// directory. `worker check` accepted it, `LoadWorker` validated it, and NEITHER fence
	// was ever told: the wall's read set was built without it (nativeSandboxArgv) and the
	// block above took paths on a --no-wall run only. So the harness auto-rejected every
	// read of a staged path the desk had granted, the card came back with the whole row
	// owed, and the run still printed `NATIVE OK ... rc=0 harness=ok`.
	//
	// THE WALL IS STILL THE REAL BOUNDARY. The harness's fence is a
	// second, weaker one, and a second fence that denies what the first one grants can only
	// cost cards. What it is handed here is EXACTLY what the wall is handed below and never
	// more: the DESCRIPTION's roots, which a person wrote at the desk.
	//
	// THE CARD'S OWN `READ:` PATHS ARE NOT IN THIS. A card is the MODEL's text, and a fence
	// rule a card can widen for itself is no fence; they stay the --no-wall affair they were.
	var reads []string
	if cfg.noWall {
		reads = swarm.CardReadPaths(cfg.card)
	}
	reads = append(reads, nativeReadRoots(cfg)...)
	configSHA, reason, proxy := writeJobConfig(cfg, provider, dataHome, jobDir, reads, errOut)
	cleanup := func() {
		releaseSlot()
		releaseLease()
		if proxy != nil {
			// ignored: a close on the refusal path; the reason printed below is the one reported
			_ = proxy.Close()
		}
		if cfg.authFile != "" {
			// ignored: clean up auth copy on early refusal path
			_ = removeAuthCopy(dataHome, errOut)
		}
	}
	if reason != "" {
		cleanup()
		refuseNative(errOut, reason)
		return nil, nativeRunResult{}, 2
	}
	if proxy != nil && cfg.onProxy != nil {
		cfg.onProxy(proxy)
	}
	// The keyless provider's loopback host:port travels to the wall as --net-allow
	// because `(allow network-outbound (remote ip))` does not reach 127.0.0.1: a
	// local-model card runs and dies silently without the named grant.
	cfg.netAllow = nativeNetAllow(cfg, provider)

	// The two hashes are recorded from the same bytes the run is about to use, so a
	// caller can prove later that neither the card nor the binary changed under it.
	binaryHash, _ := fileSHA256(bin)
	cardHash := sha256.Sum256(cfg.card)
	// (4e) STAGING FROM BENCH MIRROR.
	// Staging clones from the bench's local mirror (--reference or clone --shared)
	// with a hard timeout (120 s) that ends the card RESULT: BLOCKED stage-timeout <bench> <secs>
	// and writes the end record like any other card.
	bench := cfg.benchName
	if bench == "" {
		if h, err := os.Hostname(); err == nil {
			bench, _, _ = strings.Cut(h, ".")
		}
	}
	if bench == "" {
		bench = "bench"
	}
	stageOpts := swarm.StageOptions{
		Card:      cfg.card,
		TargetDir: filepath.Join(jobDir, "repo"),
		JobDir:    jobDir,
		BenchHome: cfg.benchHome,
		BenchName: bench,
		Timeout:   cfg.stageTimeout,
	}
	if fr := cfg.frame; fr != nil && fr.Repo != "" {
		url := swarm.CardRepoURL(fr.Repo)
		if url == "" {
			url = fr.Repo
		}
		stageOpts.Base = &swarm.CardBase{Repo: url, Sha: fr.StageSha, Ref: fr.BaseRef, Named: fr.Repo}
		stageOpts.Branch = fr.Branch
		if fr.Kind == "work" && fr.Attempt > 1 {
			// a rework is staged at its base branch's tip, the work before it carried on top
			// where it applies (docs/SPEC-CARD-CONTRACT.md, where a rework starts)
			stageOpts.Rework = &swarm.Rework{Prev: fr.PrevHead, From: fr.PrevFrom}
		}
	}
	stageRes, stageErr := swarm.StageCard(stageOpts)
	if stageErr != nil {
		cleanup()
		if stageRes.TimedOut {
			secs := int(cfg.stageTimeout.Seconds())
			if secs <= 0 {
				if cfg.stageTimeout > 0 {
					secs = 1
				} else {
					secs = int(swarm.DefaultStageTimeout.Seconds())
				}
			}
			swarm.WriteStageTimeoutResult(jobDir, bench, secs)
			fmt.Fprintf(os.Stdout, "STAGE FAIL bench=%s repo=%s base=%s secs=%d reason=stage-timeout\n",
				oneline.Field(bench), oneline.Field(stageRes.BaseRepo), oneline.Field(swarm.Version8(stageRes.BaseSha)), secs)
			writeNativeUsage(cfg, dataHome, provider, cfg.model[len(provider)+1:], startTime, time.Now(), time.Time{}, -1, 1, "stage-timeout", nil, errOut)
			res := nativeRunResult{
				rc:           -1,
				cardSHA256:   hex.EncodeToString(cardHash[:]),
				binarySHA256: binaryHash,
				job:          jobDir,
				wall:         "none",
				configSHA:    configSHA,
				tmp:          tmpDir,
				end:          "stage-timeout",
				wallSeconds:  time.Since(startTime).Seconds(),
			}
			return nil, res, 1
		}
		fmt.Fprintf(os.Stdout, "STAGE FAIL bench=%s repo=%s base=%s reason=%s\n",
			oneline.Field(bench), oneline.Field(stageRes.BaseRepo), oneline.Field(stageFailBase(stageRes)), oneline.Escape(stageErr.Error()))
		refuseNative(errOut, stageErr.Error())
		return nil, nativeRunResult{}, 2
	}
	if !stageRes.Staged && swarm.CardNamesRepo(cfg.card) {
		cleanup()
		named := swarm.ReadCardBase(cfg.card)
		fmt.Fprintf(os.Stdout, "STAGE FAIL bench=%s repo=%s base=%s reason=no-repo-staged\n",
			oneline.Field(bench), oneline.Field(named.Named), oneline.Field(swarm.Version8(named.Sha)))
		refuseNative(errOut, fmt.Sprintf("%s names repo %s but nothing was staged into %s: read the card's REPO:/base-repo: line (owner/name or a clone URL)",
			oneline.Field(cfg.label), oneline.Field(named.Named), oneline.Field(stageOpts.TargetDir)))
		return nil, nativeRunResult{}, 2
	}
	writeStageOK(os.Stdout, bench, stageRes)
	if stageRes.Carry != nil {
		fmt.Fprintln(os.Stdout, oneline.Escape(stageRes.Carry.Line()))
	}
	if stageRes.Shared {
		cfg.borrowed = filepath.Join(stageRes.Mirror, "objects")
	}

	// (4f) THE FRAME (docs/SPEC-CARD-CONTRACT.md layers 2 and 3). A framed launch whose
	// checkout is staged gets JOB.md in the job directory and its family's shims first on
	// its PATH (git push recorded, the clone of its own repository a link to the checkout,
	// gh pr create its finish), so the child meets the frame through the commands it knows.
	// A frame that cannot be installed refuses the launch: a child outside its frame is the
	// defect the frame closes.
	decided := ""
	workStart := ""
	if cfg.frame != nil && stageRes.Staged {
		start, err := installFrameTimed(cfg, jobDir, stageRes.BaseSha, stageRes.Carry, os.Stdout)
		if err != nil {
			cleanup()
			if errors.Is(err, errReadStart) {
				fmt.Fprintf(os.Stdout, "STAGE FAIL bench=%s repo=%s base=%s reason=%s\n",
					oneline.Field(bench), oneline.Field(stageRes.BaseRepo), oneline.Field(stageFailBase(stageRes)), oneline.Escape(err.Error()))
				refuseNative(errOut, err.Error())
				return nil, nativeRunResult{}, 2
			}
			refuseNative(errOut, fmt.Sprintf("%s the card's frame could not be installed: %s", oneline.Field(cfg.label), oneline.Err(err)))
			return nil, nativeRunResult{}, 2
		}
		workStart = start
		// THE DECIDE READ (nativedecide.go; docs/SPEC-SPRINT.md section 6): a flash card's
		// first read is decided here, before any child, when its p(defect) is past a bar
		switch route, op := nativeDecide(cfg, jobDir, start, stageRes.BaseSha, os.Stdout, errOut); route {
		case decide.RouteStrings:
			decided = op
		case decide.RouteBounce, decide.RouteLand:
			cleanup()
			res := nativeRunResult{rc: 0, harness: "ok", wall: "none", cardSHA256: hex.EncodeToString(cardHash[:]), binarySHA256: binaryHash,
				job: jobDir, root: cfg.root, configSHA: configSHA, tmp: tmpDir, end: "done", wallSeconds: time.Since(startTime).Seconds()}
			res.resultsDir = publishDecided(cfg, jobDir, errOut)
			return nil, res, 0
		}
	}

	// (4g) A SCRIPT CARD (docs/SPEC-SPRINT.md, a card is a tree of steps). A work card whose
	// every work step is a script step runs the executor in place of the harness, and no model
	// is launched at all. The executor is not the child: it runs outside the child's wall (a
	// wall does not nest) with no credential in its environment, and puts each program, POST
	// command and git in the step's own wall, tighter than a child's (below, at the wall).
	var stepArgv []string
	if cfg.frame != nil && stageRes.Staged && cfg.frame.Kind == "work" {
		all, err := installTreeSteps(cfg.card, cfg.slotDir, jobDir)
		if err != nil {
			cleanup()
			refuseNative(errOut, fmt.Sprintf("%s the card's script steps could not be installed: %s", oneline.Field(cfg.label), oneline.Err(err)))
			return nil, nativeRunResult{}, 2
		}
		if all != nil {
			stepArgv = all
			fmt.Fprintf(os.Stdout, "NATIVE NOTE label=%s every work step is a script step: the executor runs in place of the harness, no model, each step in its own wall\n", oneline.Field(cfg.label))
		}
	}

	return &nativePrepared{
		cfg:          cfg,
		startTime:    startTime,
		bin:          bin,
		provider:     provider,
		launch:       launch,
		jobDir:       jobDir,
		releaseLease: releaseLease,
		releaseSlot:  releaseSlot,
		dataHome:     dataHome,
		tmpDir:       tmpDir,
		cacheDir:     cacheDir,
		shimDir:      shimDir,
		shimShell:    shimShell,
		goBin:        goBin,
		toolPath:     toolPath,
		configSHA:    configSHA,
		proxy:        proxy,
		binaryHash:   binaryHash,
		cardHash:     cardHash,
		stageRes:     stageRes,
		decided:      decided,
		workStart:    workStart,
		stepArgv:     stepArgv,
	}, nativeRunResult{}, 0
}

// wall resolves the sandbox wall binary, configures argv with sandbox containment
// if enabled, applies process priority lowering (nice), and configures step launches.
func wall(p *nativePrepared, errOut io.Writer) (*nativeWalled, nativeRunResult, int) {
	if p.cfg.onPhase != nil {
		p.cfg.onPhase("wall")
	}
	// (5) THE WALL (slice 11). Every native run is walled unless the caller typed --no-wall:
	// the wall is never implied away. A --sandbox name is used as typed;
	// otherwise the tool's own name is resolved on PATH. A wall that cannot express a repo
	// allow rule is still a wall -- a card naming no repos runs inside it without the rule,
	// and one naming repos is refused, never unwalled. A machine with no wall binary at all
	// refuses unless --no-wall owns every read and write the child makes.
	runPath := p.launch[0]
	runArgv := p.launch[1:]
	wall := p.cfg.sandbox
	if wall == "" && !p.cfg.noWall {
		found, err := exec.LookPath(swarm.SandboxBinary)
		if err != nil {
			refuseNative(errOut, fmt.Sprintf("%s no wall: %s is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes",
				oneline.Field(p.cfg.label), oneline.Field(swarm.SandboxBinary)))
			return nil, nativeRunResult{}, 2
		}
		wall = found
	}
	if wall != "" {
		if len(p.cfg.repos) > 0 && !sandboxHostRules(wall) {
			refuseNative(errOut, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(p.cfg.label)))
			return nil, nativeRunResult{}, 2
		}
		runPath = wall
		runArgv = nativeSandboxArgv(p.launch, p.cfg, p.dataHome, p.jobDir, p.tmpDir)
	} else if len(p.cfg.repos) > 0 {
		refuseNative(errOut, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(p.cfg.label)))
		return nil, nativeRunResult{}, 2
	}
	// THE CHILD'S PRIORITY, where the wall forbids the child to lower its own (the darwin
	// wall denies setpriority): native lowers the group after the start, and the card's own
	// `nice -n 19` resolves to the shim's, which runs the command without the wall's warning.
	niced := nativeNicesChild(runtime.GOOS, wall != "")
	if p.stepArgv != nil {
		var err error
		if runPath, runArgv, niced, err = nativeStepLaunch(runtime.GOOS, p.stepArgv, wall); err != nil {
			refuseNative(errOut, fmt.Sprintf("%s the wall %s could not be made absolute: %s", oneline.Field(p.cfg.label), oneline.Field(wall), oneline.Err(err)))
			return nil, nativeRunResult{}, 2
		}
		wall = ""
	}
	if niced && p.shimDir != "" {
		if err := writeNativeNiceShim(p.shimDir); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: %s; a card's nice inside the wall warns setpriority and runs its command anyway\n", oneline.Err(err))
		}
	}
	return &nativeWalled{
		runPath: runPath,
		runArgv: runArgv,
		wall:    wall,
		niced:   niced,
	}, nativeRunResult{}, 0
}

func initRunState(p *nativePrepared, w *nativeWalled, errOut io.Writer) (*nativeRunState, nativeRunResult, int) {
	secretEnv := ""
	if p.cfg.worker != nil {
		secretEnv = p.cfg.worker.Secret
	}
	jobRepo := filepath.Join(p.jobDir, swarm.JobRepo)
	if p.cacheDir != "" {
		if _, err := os.Stat(jobRepo); err == nil {
			if err := swarm.PrepareLispJobCache(filepath.Dir(p.cacheDir), jobRepo); err != nil {
				refuseNative(errOut, fmt.Sprintf("the private Lisp cache could not be prepared: %s", oneline.Escape(err.Error())))
				return nil, nativeRunResult{}, 2
			}
		}
	}
	childEnv := nativeChildEnv(p.dataHome, p.jobDir, p.tmpDir, p.cacheDir, secretEnv, p.shimDir, p.shimShell, p.toolPath)
	// A headless harness runs from a private home under the data home, seeded with its credential
	// file alone (swarm.HeadlessHomeOf), and is pointed at it by name where it reads one. claude
	// copies no file: its login is the token --pass hands it by name, and a run without one says so.
	if k := p.cfg.headless(); k != "" {
		h := swarm.HeadlessHomeOf(k, benchHome(p.cfg), p.dataHome)
		for _, kv := range h.Env {
			name, _, _ := strings.Cut(kv, "=")
			childEnv = append(environWithoutName(childEnv, name), kv)
		}
		if err := seedHeadlessHome(h); err != nil {
			refuseNative(errOut, fmt.Sprintf("%s the harness's private home could not be made under the data home: %s", oneline.Field(p.cfg.label), oneline.Err(err)))
			return nil, nativeRunResult{}, 2
		}
		if note := swarm.HeadlessTokenNote(h, childEnv); note != "" {
			fmt.Fprintf(errOut, "NATIVE NOTE login: %s %s\n", oneline.Field(p.cfg.label), oneline.Escape(note))
		}
	}
	if p.cfg.root != "" {
		var id swarm.StagingIdentity
		if p.cfg.identity != nil {
			id = *p.cfg.identity
		} else {
			var err error
			if id, err = swarm.LoadPoolIdentity(p.cfg.root); err != nil {
				refuseNative(errOut, err.Error()+"; or give the loop --identity <owner>,<name>,<email> in its nova-config argv")
				return nil, nativeRunResult{}, 2
			}
		}
		for _, kv := range swarm.StagingGitEnv(id) {
			name, _, _ := strings.Cut(kv, "=")
			childEnv = append(environWithoutName(childEnv, name), kv)
		}
		seeded, note := seedCatalog(p.cfg.root, p.dataHome)
		if note != "" {
			fmt.Fprintf(errOut, "NATIVE NOTE catalog: %s\n", oneline.Escape(note))
		}
		for _, kv := range seeded {
			name, _, _ := strings.Cut(kv, "=")
			childEnv = append(environWithoutName(childEnv, name), kv)
		}
	} else {
		refuseNative(errOut, "missing configured root for pool identity; refusing to launch under nobody's name")
		return nil, nativeRunResult{}, 2
	}
	if err := writeNativeArgvLog(p.cfg.slotDir, w.runPath, w.runArgv, childEnv); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE argv log: %s; the run goes on without its record of the argv and environment the child is handed\n", oneline.Err(err))
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the child's stdin %s could not be opened: %s", oneline.Field(os.DevNull), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	log, err := os.OpenFile(filepath.Join(p.cfg.slotDir, "native.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		devNull.Close() // ignored: /dev/null, opened read-only on an error path that refuses the run
		refuseNative(errOut, fmt.Sprintf("the run log %s could not be opened: %s", oneline.Field(filepath.Join(p.cfg.slotDir, "native.log")), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	outLog := filepath.Join(p.jobDir, "harness-output.log")
	providerMark := fileSize(filepath.Join(p.dataHome, filepath.FromSlash(harnessLogFile)))
	captureMark := fileSize(outLog)
	runStart := time.Now()
	harnessOut, err := os.OpenFile(outLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND|swarm.ONoFollow, 0o644)
	if err != nil {
		devNull.Close() // ignored: /dev/null, opened read-only on an error path that refuses the run
		log.Close()
		refuseNative(errOut, fmt.Sprintf("the harness output log %s could not be opened: %s", oneline.Field(outLog), oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	timeline := swarm.NewTimeline()
	reader := swarm.NewWallReader(p.cfg.label, func(line string) { fmt.Fprintln(errOut, line) })
	denials := swarm.NewShellDenialReader()
	capture := io.MultiWriter(log, harnessOut, timeline, reader, denials)

	res := nativeRunResult{
		rc:           -1,
		cardSHA256:   hex.EncodeToString(p.cardHash[:]),
		binarySHA256: p.binaryHash,
		job:          p.jobDir,
		root:         p.cfg.root,
		wall:         "none",
		configSHA:    p.configSHA,
		tmp:          p.tmpDir,
	}
	if p.cfg.noWall {
		res.wall = swarm.SandboxNoneByFlag
	}
	grace := swarm.DefaultLaunchGrace
	if p.cfg.worker != nil {
		grace = swarm.LaunchGrace(*p.cfg.worker)
	}
	termCh := nativeTermCh()
	var bodyStall <-chan struct{}
	if p.proxy != nil {
		bodyStall = p.proxy.Stalled()
	}
	if p.cfg.resultsRoot != "" && safepath.NameOK(p.cfg.label) {
		id, err := claimNativeResultsRun(p.cfg)
		if err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: a results run directory could not be claimed under %s: %s\n", oneline.Field(p.cfg.resultsRoot), oneline.Escape(err.Error()))
		} else {
			p.cfg.runID = id
		}
	}
	sampler := startLiveSampler("", 0, p.cfg, outLog)
	// A headless child has no database to sample: it prints its usage once, when it ends,
	// so its budgets are asked of the final read and the deadline is its live stop.
	if (!p.cfg.unmetered && p.cfg.tokens > 0 || p.cfg.usd != nil || (p.cfg.worker != nil && p.cfg.worker.HasCardBudget())) && p.cfg.headless() == "" {
		sampler = startLiveSampler(p.dataHome, p.cfg.usageInterval, p.cfg, outLog)
	}
	if p.stepArgv != nil {
		childEnv = append(cardtree.ScrubEnv(childEnv), "HOME="+p.dataHome)
	}
	runCtx, stopRun := context.WithCancel(context.Background())

	return &nativeRunState{
		prep:         p,
		wal:          w,
		childEnv:     childEnv,
		devNull:      devNull,
		log:          log,
		harnessOut:   harnessOut,
		outLog:       outLog,
		providerMark: providerMark,
		captureMark:  captureMark,
		runStart:     runStart,
		timeline:     timeline,
		reader:       reader,
		denials:      denials,
		capture:      capture,
		res:          res,
		grace:        grace,
		termCh:       termCh,
		bodyStall:    bodyStall,
		sampler:      sampler,
		runCtx:       runCtx,
		stopRun:      stopRun,
	}, nativeRunResult{}, 0
}

// start launches a child process attempt, establishing process groups and wait channels.
func start(s *nativeRunState, attempt int, errOut io.Writer) (*nativeStarted, nativeRunResult, int) {
	if s.prep.cfg.onPhase != nil {
		s.prep.cfg.onPhase("start")
	}
	before := fileSize(s.outLog)
	providerBefore := fileSize(filepath.Join(s.prep.dataHome, filepath.FromSlash(harnessLogFile)))
	cmd := subproc.Long(s.runCtx, s.wal.runPath, s.wal.runArgv...)
	ownChildGroup(cmd)
	cmd.Env = s.childEnv
	cmd.Dir = s.prep.jobDir
	cmd.Stdin = s.devNull
	cmd.Stdout = s.capture
	cmd.Stderr = io.MultiWriter(s.capture, &s.wallOut)
	attemptStart := time.Now()
	if err := cmd.Start(); err != nil {
		s.log.Close()
		s.harnessOut.Close()
		refuseNative(errOut, fmt.Sprintf("the child could not be started: %s", oneline.Escape(err.Error())))
		return nil, nativeRunResult{}, 2
	}
	pgid := cmd.Process.Pid
	if s.wal.niced {
		if err := lowerChildPriority(pgid); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the child's priority could not be lowered: %s\n", oneline.Err(err))
		}
	}
	started := swarm.StartStamp(pgid)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := nativeDeadline
	if s.prep.cfg.deadlineFn != nil {
		deadline = s.prep.cfg.deadlineFn
	}
	deadlineC, stopDeadline := deadline(s.prep.cfg.deadline)
	stopWatch := make(chan struct{})
	idleC := nativeWatchIdle(swarm.IdleWatch{
		Log: s.outLog, Job: s.prep.jobDir, Pid: pgid, Idle: s.prep.cfg.idle, Reader: s.reader,
	}, stopWatch)

	return &nativeStarted{
		cmd:            cmd,
		pgid:           pgid,
		started:        started,
		done:           done,
		deadlineC:      deadlineC,
		stopDeadline:   stopDeadline,
		idleC:          idleC,
		stopWatch:      stopWatch,
		attemptStart:   attemptStart,
		before:         before,
		providerBefore: providerBefore,
	}, nativeRunResult{}, 0
}

// watch monitors the running child for completion, deadline expiry, idle timeout,
// proxy body stall, external SIGTERM, or budget exhaustion.
func watch(s *nativeRunState, st *nativeStarted) *nativeWatched {
	if s.prep.cfg.onPhase != nil {
		s.prep.cfg.onPhase("watch")
	}
	select {
	case runErr := <-st.done:
		st.stopDeadline()
		switch ee := runErr.(type) {
		case nil:
			s.res.rc = 0
		case *exec.ExitError:
			s.res.rc = ee.ExitCode()
		default:
			s.res.rc = -1
			if errors.Is(runErr, exec.ErrWaitDelay) {
				s.res.rc = 0
			}
		}
		s.res.survivors = nativeEndLeftovers(st.pgid, st.started)
	case <-st.deadlineC:
		nativeKillGroup(st.pgid, st.started)
		<-st.done
		s.res.rc = -1
		s.res.deadlined = true
	case end := <-st.idleC:
		st.stopDeadline()
		nativeReap(st.pgid, st.started, swarm.TerminateGrace)
		<-st.done
		s.res.rc = -1
		s.res.idled, s.res.idleEnd = true, end
	case <-s.bodyStall:
		st.stopDeadline()
		nativeReap(st.pgid, st.started, swarm.TerminateGrace)
		<-st.done
		s.res.rc = -1
		s.res.lost = true
	case <-s.termCh:
		st.stopDeadline()
		nativeReap(st.pgid, st.started, swarm.TerminateGrace)
		<-st.done
		s.res.rc = -1
		s.res.terminated = true
	case word := <-s.sampler.Fired():
		st.stopDeadline()
		swarm.Reap(st.pgid, st.started, swarm.TerminateGrace)
		<-st.done
		s.res.rc = -1
		s.res.stopped = word
		if word == stoppedUnverifiable {
			s.res.stoppedWhy = s.sampler.Why()
		}
	}
	close(st.stopWatch)
	return &nativeWatched{}
}

// collect handles post-attempt accounting: measuring elapsed duration, checking for lost
// responses, recording provider usage rows, accumulating budget, and deciding whether to retry.
func collect(s *nativeRunState, st *nativeStarted, wat *nativeWatched, attempt int, errOut io.Writer) *nativeCollected {
	if s.prep.cfg.onPhase != nil {
		s.prep.cfg.onPhase("collect")
	}
	elapsed := time.Since(st.attemptStart)
	s.res.wallSeconds += elapsed.Seconds()
	tail := readSince(s.outLog, st.before)
	lost := swarm.LostResponse(tail) || s.res.lost
	if s.prep.proxy != nil && s.prep.proxy.Lost() {
		lost = true
	}
	s.res.end = swarm.EndDone
	if lost {
		s.res.end = swarm.EndUnknown
		s.res.lost = true
	} else if s.res.rc != 0 {
		s.res.end = swarm.EndFailed
	}
	if s.res.stopped != "" {
		s.res.end = swarm.EndBudget
		if s.res.stopped == stoppedUnverifiable {
			s.res.end = swarm.EndUnverifiable
		}
	}
	var launchUsage swarm.ProviderUsage
	launchEnd := time.Now()
	launchUsage, s.res.usageReason, s.res.usageState, s.res.usage = writeNativeUsage(
		s.prep.cfg, s.prep.dataHome, s.prep.provider, s.prep.cfg.model[len(s.prep.provider)+1:],
		st.attemptStart, launchEnd, s.prevLaunchEnd, s.res.rc, attempt, s.res.end, tail, errOut)
	s.prevLaunchEnd = launchEnd

	if sum, seen, part := launchUsage.Budget(); seen > 0 {
		s.jobSpent += sum
		s.jobObserved = true
		s.jobPartial = s.jobPartial || part
	}
	if launchUsage.Observed {
		s.launchSpends = append(s.launchSpends, launchSpend(launchUsage.Values))
	}
	if s.res.terminated {
		return &nativeCollected{retry: false}
	}
	if lost {
		if err := persistUnknownFn(s.prep.jobDir); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the acceptance could not be recorded: %s\n", oneline.Escape(err.Error()))
			s.res.unrecorded = true
		}
		return &nativeCollected{retry: false}
	}
	if s.res.stopped != "" {
		return &nativeCollected{retry: false}
	}
	launchTokens := 0
	if sum, seen, _ := launchUsage.Budget(); seen > 0 {
		launchTokens = sum
	}
	_, published := swarm.FindCardResult(s.prep.jobDir)
	cause, failed := harnessStartFailed(tail, elapsed, launchTokens, published)
	_, launchFailure := swarm.ProviderLaunchFailure(tail)
	if !published && (failed || launchFailure) {
		if refusal, ok := providerEnd(s.prep.cfg.headless(), s.prep.dataHome, st.providerBefore, st.attemptStart, s.res.rc, tail); ok &&
			(refusal.Class == swarm.CauseCredit || refusal.Class == swarm.CauseAuth) {
			return &nativeCollected{retry: false}
		}
	}
	if failed {
		if s.startRetries < len(harnessStartWaits) {
			if word := s.sampler.StopWordAtFinal(s.jobSpent, s.jobObserved, spendCost(s.launchSpends)); word != "" {
				s.res.stopped = word
				return &nativeCollected{retry: false}
			}
			wait := harnessStartWaits[s.startRetries]
			s.startRetries++
			fmt.Fprintf(errOut, "NATIVE RETRY start=%d wait=%ds cause=%s\n", attempt+1, int(wait/time.Second), oneline.Field(cause))
			sleep := s.prep.cfg.startSleep
			if sleep == nil {
				sleep = startSleep
			}
			sleep(wait)
			return &nativeCollected{retry: true}
		}
		s.res.starts = attempt
		return &nativeCollected{retry: false}
	}
	if launchFailure && elapsed < s.grace && attempt < swarm.MaxProviderAttempts {
		if word := s.sampler.StopWordAtFinal(s.jobSpent, s.jobObserved, spendCost(s.launchSpends)); word != "" {
			s.res.stopped = word
			return &nativeCollected{retry: false}
		}
		time.Sleep(swarm.ProviderRetryDelay(attempt))
		return &nativeCollected{retry: true}
	}
	return &nativeCollected{retry: false}
}

// report finalizes the run: folding budget numbers, closing logs, diagnosing wall or
// harness failures, recording timeline, running gate checks, and publishing results.
func report(s *nativeRunState, errOut io.Writer) (nativeRunResult, int) {
	if s.prep.cfg.onPhase != nil {
		s.prep.cfg.onPhase("report")
	}
	s.sampler.Stop()
	s.res.spent, s.res.observed, s.res.partial = s.jobSpent, s.jobObserved, s.jobPartial
	s.res.spend = spendWord(s.launchSpends)
	if !s.res.observed {
		if spent, observed, _, _, _ := s.sampler.Observed(); observed {
			s.res.spent, s.res.observed, s.res.partial = spent, true, true
		}
	}
	s.res.defect = s.sampler.Defect()
	s.log.Close()
	s.harnessOut.Close()
	s.res.harness = harnessState(s.prep.jobDir)
	s.res.fence = fenceRejected(s.prep.jobDir)
	if rows := s.timeline.Rows(); len(rows) > 0 {
		if err := swarm.WriteTimeline(filepath.Join(s.prep.jobDir, swarm.TimelineFileName), rows); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the timeline.tsv could not be written: %s\n", oneline.Escape(err.Error()))
		}
	}
	if report, ok := swarm.WallDeath(s.prep.jobDir, s.prep.cfg.label); ok {
		s.res.wallReport = report
	}
	if _, published := swarm.FindCardResult(s.prep.jobDir); !published {
		if raw, err := os.ReadFile(filepath.Join(s.prep.jobDir, "harness-output.log")); err == nil {
			if wr, ok := swarm.WallStopped(raw); ok {
				s.res.wallRefusal = wr
			}
		}
	}
	if sd, ok := s.denials.Denied(); ok {
		s.res.shellDenial = sd
	}
	if s.res.lost {
		s.res.end = swarm.EndUnknown
	} else {
		s.res.end = swarm.EndDone
		if s.res.rc != 0 {
			s.res.end = swarm.EndFailed
		}
	}
	if !s.res.lost && ((s.res.wallRefusal != swarm.WallRefusal{}) || (s.res.shellDenial != swarm.ShellDenial{})) {
		s.res.end = swarm.EndWall
	}
	if !s.res.lost && s.res.idled {
		s.res.end = swarm.EndWall
		reason := fmt.Sprintf("the card's log and its process tree were both still for %.0fs; the run ended it rather than holding the slot to its deadline", s.res.idleEnd.Idle.Seconds())
		if s.res.idleEnd.Refused {
			s.res.wallRefusal = swarm.WallRefusal{Path: s.res.idleEnd.Path, Step: s.res.idleEnd.Step}
			reason = fmt.Sprintf("the wall refused %s %s and the card wrote nothing for %.0fs after it",
				oneline.Field(s.res.idleEnd.Kind), oneline.Field(s.res.idleEnd.Path), s.res.idleEnd.Idle.Seconds())
		}
		if path, wrote, err := swarm.WriteBlockedResult(s.prep.jobDir, s.prep.cfg.label, s.res.idleEnd.Kind, s.res.idleEnd.Path, s.res.idleEnd.Step, reason); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the blocked report could not be written: %s\n", oneline.Escape(err.Error()))
		} else if wrote {
			s.res.blockedPath = path
		}
	}
	if !s.res.lost && s.res.deadlined && !s.res.idled && !s.res.terminated && s.res.stopped == "" && s.res.wallReport == "" && (s.res.wallRefusal == swarm.WallRefusal{}) {
		// the card worked to its wall: a commit in ./repo and no report is work to name, not a silent no-result (fault 9)
		if path, wrote, err := swarm.WriteDeadlineResult(s.prep.jobDir, s.prep.cfg.label, true); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the deadline report could not be written: %s\n", oneline.Escape(err.Error()))
		} else if wrote {
			s.res.blockedPath = path
		}
	}
	handedBack := false
	if !s.res.lost && !s.res.idled && !s.res.terminated && s.res.wallReport == "" && (s.res.wallRefusal == swarm.WallRefusal{}) {
		if raw, err := os.ReadFile(s.outLog); err == nil {
			if h, ok := swarm.ProviderHandback(swarm.ProviderExit{Tail: raw, Job: s.prep.jobDir, RC: s.res.rc, Wall: time.Duration(s.res.wallSeconds * float64(time.Second)), Route: s.prep.cfg.model, Routes: swarm.ParseRouteList(os.Getenv(swarm.RoutesEnv))}); ok {
				printed := printedHarnessError(raw)
				if strings.Contains(printed, "ProviderModelNotFoundError") {
					h.Cause = swarm.CauseFromText(printed)
				} else if c, ok := sessionProviderError(s.prep.dataHome, s.runStart); ok {
					h.Cause = c
				} else if line := providerLogError(s.prep.dataHome, s.providerMark); line != "" {
					h.Cause = swarm.CauseFromText(line)
				} else if printed != "" {
					h.Cause = swarm.CauseFromText(printed)
				}
				if s.res.starts > 0 {
					h.Cause.Message = strings.TrimSpace(h.Cause.Message + " (harness starts tried: " + strconv.Itoa(s.res.starts) + ")")
				}
				fmt.Fprintln(errOut, oneline.Escape(h.Line(s.prep.cfg.label)))
				handedBack = true
			}
		}
	}
	if !s.res.idled && !s.res.terminated && s.res.rc == 0 && s.res.wallReport == "" && (s.res.wallRefusal == swarm.WallRefusal{}) {
		if question, asked := swarm.AskedEnd(s.prep.jobDir, s.res.rc); asked {
			if path, wrote, err := swarm.WriteAskedResult(s.prep.jobDir, s.prep.cfg.label, question); err != nil {
				fmt.Fprintf(errOut, "NATIVE NOTE: the asked report could not be written: %s\n", oneline.Escape(err.Error()))
			} else if wrote {
				fmt.Fprintf(errOut, "NATIVE NOTE: the card ended its last turn with a question and published no report of its own; one naming the question was written to %s\n", oneline.Field(path))
			}
		}
	}
	if !s.res.lost && !s.res.idled && !s.res.terminated && !handedBack && s.res.stopped == "" && s.res.wallReport == "" &&
		(s.res.wallRefusal == swarm.WallRefusal{}) && (s.res.shellDenial == swarm.ShellDenial{}) {
		if _, published := swarm.FindCardResult(s.prep.jobDir); !published {
			if cause, ok := providerEnd(s.prep.cfg.headless(), s.prep.dataHome, s.providerMark, s.runStart, s.res.rc, tailSince(s.outLog, s.captureMark)); ok {
				fmt.Fprintln(errOut, oneline.Escape(providerLine(s.prep.cfg.label, s.res.wallSeconds, s.prep.cfg.model, cause)))
			}
		}
	}
	if s.prep.decided != "" {
		settleDecided(s.prep.cfg, s.prep.jobDir, s.prep.decided, errOut)
	}
	if !s.res.lost && s.prep.stepArgv == nil {
		run := s.prep.cfg.gateRun
		if run == nil {
			run = nativeGateRunner(s.wal.wall, func(argv []string) []string {
				return nativeSandboxArgv(argv, s.prep.cfg, s.prep.dataHome, s.prep.jobDir, s.prep.tmpDir)
			}, s.childEnv, s.prep.jobDir, s.prep.goBin)
		}
		nativeGate(s.prep.cfg, s.prep.jobDir, s.prep.tmpDir, s.prep.workStart, run, os.Stdout, errOut)
	}
	s.res.resultsDir = publishNativeResults(s.prep.cfg, s.prep.jobDir, s.lastAttempt, errOut)

	if s.wal.wall != "" {
		backend, cwd, reason := wallNamed(s.wallOut.String())
		if reason != "" {
			refuseNative(errOut, fmt.Sprintf("%s wall %s; the run is refused rather than silently unwalled", oneline.Field(s.prep.cfg.label), oneline.Escape(reason)))
			return nativeRunResult{}, 2
		}
		s.res.wall = backend
		if !sameDir(cwd, s.prep.jobDir) {
			refuseNative(errOut, fmt.Sprintf("%s wall ran the child in %s, not the job directory %s; --cwd was not applied", oneline.Field(s.prep.cfg.label), oneline.Field(cwd), oneline.Field(s.prep.jobDir)))
			return nativeRunResult{}, 2
		}
	}
	if s.res.unrecorded {
		return s.res, 2
	}
	if s.res.rc == 0 {
		if _, published := swarm.FindCardResult(s.prep.jobDir); !published {
			s.res.reason = "harness-silent"
		}
	}
	return s.res, 0
}

// nativeRun executes one frozen configuration and returns the recorded result and
// the command's exit code: 0 the child ran, 2 a refusal (one REFUSED line on
// errOut). A refusal is a defect in the configuration the run can see before it
// spends anything, and it names one reason.
func nativeRun(cfg nativeRunConfig, errOut io.Writer) (_ nativeRunResult, code int) {
	// (1) prepare phase
	p, earlyRes, earlyCode := prepare(cfg, errOut)
	if earlyCode != 0 || p == nil {
		return earlyRes, earlyCode
	}
	defer p.releaseLease()
	defer p.releaseSlot()
	if p.proxy != nil {
		defer p.proxy.Close()
	}
	if p.cfg.authFile != "" {
		defer func() {
			if left := removeAuthCopy(p.dataHome, errOut); len(left) > 0 {
				refuseNative(errOut, fmt.Sprintf("the auth copy outlived the card: %s is still on disk after the run's cleanup", oneline.Field(strings.Join(left, ","))))
				if code == 0 {
					code = 2
				}
			}
		}()
	}

	// (2) wall phase
	w, earlyRes, earlyCode := wall(p, errOut)
	if earlyCode != 0 {
		return earlyRes, earlyCode
	}

	// execution state setup
	state, earlyRes, earlyCode := initRunState(p, w, errOut)
	if earlyCode != 0 {
		return earlyRes, earlyCode
	}
	defer state.devNull.Close()
	defer stopNativeTerm(state.termCh)
	defer state.stopRun()

	// (3)-(5) execution loop: start, watch, collect
	for attempt := 1; ; attempt++ {
		state.lastAttempt = attempt

		st, earlyRes, earlyCode := start(state, attempt, errOut)
		if earlyCode != 0 {
			return earlyRes, earlyCode
		}

		wat := watch(state, st)

		col := collect(state, st, wat, attempt, errOut)
		if col.retry {
			continue
		}
		break
	}

	// (6) report phase
	return report(state, errOut)
}

// persistUnknownFn is the handoff writer. A test of a failed disk sets it
// and puts it back. The production function is persistUnknown.
var persistUnknownFn = persistUnknown

// persistUnknown writes the handoff the next reader holds on. The marker is
// first. The two logs are the fallback. A short write or a failed close is
// not a successful record. If none of them can be written, the caller still
// prints the unknown verdict and then refuses the run.
func persistUnknown(jobDir string) error {
	acc := filepath.Join(jobDir, "provider-acceptance")
	if err := writeUnknownFile(acc, "unknown\n"); err == nil {
		return nil
	} else {
		var failed []string
		failed = append(failed, err.Error())
		line := "why=unknown-acceptance\n"
		for _, name := range []string{"harness-output.log", "harness.log"} {
			if werr := writeUnknownFile(filepath.Join(jobDir, name), line); werr != nil {
				failed = append(failed, werr.Error())
				continue
			}
			return nil
		}
		return fmt.Errorf("%s", strings.Join(failed, "; "))
	}
}

func writeUnknownFile(path, body string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	n, werr := fmt.Fprintf(f, "%s", oneline.Escape(body))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if n != len(oneline.Escape(body)) {
		return fmt.Errorf("%s: short write", path)
	}
	return cerr
}

// fileSize is a path's size, or 0 when it cannot be measured: the mark the retry loop reads
// before a launch so the provider tail it inspects is THIS attempt's output.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// readSince reads what one launch appended to the capture after offset, bounded so a chatty
// harness does not read a whole log to answer a yes/no.
func readSince(path string, offset int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil
	}
	return raw
}

// fenceRejected is the first path the harness's own fence auto-rejected in this job's
// capture, or "" when it rejected nothing. It is asked OF THE RUN'S OWN CAPTURE,
// `<job>/harness-output.log` (issue #608), the file with one writer -- never `harness.log`,
// which carries the runner's stdout and this very line.
func fenceRejected(jobDir string) string {
	raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	if err != nil {
		return ""
	}
	path, ok := swarm.FenceRejection(raw)
	if !ok {
		return ""
	}
	return path
}

// harnessState is the `harness=<ok|silent>` token the NATIVE OK line always carries. ONE
// DEFINITION, and this is it (issues #591, #594, #608 folded): a run is `silent` when the
// capture above holds nothing the child said AND no `RESULT.md` is found anywhere the gather
// looks for one. Anything else is `ok`.
//
// A SILENT HARNESS IS NOT A QUIET MODEL. The run this closes was a local model whose tool
// calls the harness never parsed: the child emitted them as raw text, no tool ran, nothing
// was written, and the process exited 0, so the one line a coordinator reads said OK and the
// card scored `no-result` -- the token for a model that chose to publish nothing.
// The two are different faults with different remedies (a harness that cannot drive this
// model; a model that had nothing to say), and the line now tells them apart. A harness that
// SPOKE and published nothing is `ok` and scores `no-result`: there is evidence to read.
//
// THE FILE IS THE RUN'S OWN CAPTURE, `<job>/harness-output.log` (issue #608) -- never
// `harness.log`, which a runner pin may own. Reading the capture rather than a file this
// process does not write is what keeps the token honest on a bench.
//
// THE WALL'S OWN LINES ARE NOT THE HARNESS SPEAKING. The wall prints `SANDBOX ...` on the
// child's stderr, which this capture also holds, and counting those bytes would make a WALLED
// run -- the very run that wrote issue #591 -- impossible to call silent. They are skipped
// here.
//
// THE RESULT IS LOOKED FOR by the one lookup (swarm.FindCardResult): the job root, then
// `repo/` and one directory below it (issue #594). A card's STEP 1 makes `repo/` the model's
// cwd, so a working run publishes there; a shallower lookup here would print `harness=silent` about a run that
// worked, which is the same class of fault this token exists to end.
func harnessState(jobDir string) string {
	if harnessSpoke(filepath.Join(jobDir, "harness-output.log")) {
		return "ok"
	}
	if result, ok := swarm.FindCardResult(jobDir); ok && wroteBytes(result) {
		return "ok"
	}
	return "silent"
}

// captureHeadBytes bounds what harnessSpoke reads of a capture: a wall's own header is a
// handful of lines, so a capture larger than this holds words of the child's whatever its
// head says, and a run's capture can be megabytes that nobody needs read to answer a yes/no.
const captureHeadBytes = 64 << 10

// harnessSpoke says whether the capture holds a line the CHILD wrote: any non-blank line that
// is not one of the wall's own `SANDBOX ` lines. An absent or empty file is a harness that
// said nothing, and so is one holding the wall's header alone.
func harnessSpoke(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return false
	}
	if fi.Size() > captureHeadBytes {
		return true
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "SANDBOX ") {
			continue
		}
		return true
	}
	return false
}

// wroteBytes says whether a path is a regular file holding at least one byte: the test
// harnessState applies to a result, so an empty RESULT.md is nothing published. Lstat, not
// Stat: a planted symlink is not a published result (issue #233).
func wroteBytes(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}

// stageFailBase is the base a STAGE FAIL line names: the card's sha, else the ref it names
// (a refusal stages nothing, so the result's sha is empty for a card staged at its ref).
func stageFailBase(r swarm.StageResult) string {
	if r.BaseSha != "" {
		return swarm.Version8(r.BaseSha)
	}
	return r.Ref
}

// nativeToCI is the step behind CI cmdNative takes: yield.ToCI, and never anything else
// in production (internal/ci TestCopiesRunNiced). The one other value is the test
// binary's: its TestMain makes it a no-op, because that binary is CI's own process and
// runs cmdNative in-process, and stepping it would put a CI leg behind the very
// children it must beat. The real step is read through the built binary
// (TestACardsLaunchRunsBehindCI).
var nativeToCI = yield.ToCI

// yieldNative steps this native run behind CI through toCI (nativeToCI in production)
// before it starts anything, so the wall, the harness and every process the card's
// child runs inherit yield.Nice. A run that cannot step down is refused, never run at
// CI's priority: the same answer nova-ci local gives, on every OS (one with no
// setpriority included; a member says so once at its start). The NATIVE REFUSED line
// is in the launch's log, where the member's finish report reads it (nativeRefusedWhy).
func yieldNative(toCI func() error, stderr io.Writer) bool {
	if err := toCI(); err != nil {
		refuseNative(stderr, "yield to CI: "+oneline.Err(err)+"; a card never runs at the priority of the CI legs beside it")
		return false
	}
	return true
}

// refuseNative writes the one REFUSED line the run owes its caller.
func refuseNative(w io.Writer, reason string) {
	fmt.Fprintf(w, "NATIVE REFUSED: %s\n", oneline.Escape(oneline.WithRemedy(reason, "nova-swarm native -h")))
}

// sandboxHostRules asks the wall, once, whether it can express a repo allow rule: network
// to github.com for the named repositories is a HOST rule, and the wall's `check` verb says
// so with `hosts=enforceable`. Anything else -- a check that will not run, or a line without
// that token -- is a wall that cannot express the rule, and the run refuses rather than run
// the card unwalled (SPEC-SANDBOX rule 1 and rule 11).
func sandboxHostRules(sandbox string) bool {
	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, sandbox, "check")
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "hosts=enforceable")
}

// nativeSandboxArgv is the wrap for a native run: the wall's flags, then --, then the
// harness verbatim (SPEC-SANDBOX rule 12) -- launch, the one launcher's argv, binary first. The job directory is the first --write and the
// --cwd (rule 13); the data home is the second --write and the child's HOME (rule 9); the
// temp directory is a --write so the child's TMPDIR is usable inside the wall; the
// slot directory is the read set. Each repo the card named is a --repo allow rule, and a
// recipient never appears: a bus send is denied by the wall itself, not granted by the
// caller, so no allow rule is ever built for one.
func nativeSandboxArgv(launch []string, cfg nativeRunConfig, dataHome, jobDir, tmpDir string) []string {
	bin := launch[0]
	argv := []string{
		"--read", cfg.slotDir,
		"--write", jobDir,
		"--write", dataHome,
		"--write", tmpDir,
	}
	// The shared per-bench cache root is a write for the same reason the data home is: a
	// card extracts a module it downloads, and the wall denies a write it was not handed
	// (card 8963, issue #1048, docs/SPEC-SANDBOX.md). It is one directory for the whole
	// bench, so the write is shared, not per-card, and it is named once.
	if cacheDir := nativeCacheDir(cfg); cacheDir != "" {
		argv = append(argv, "--write", cacheDir)
	}
	// --tmp names the slot's temp directory as the child's TMPDIR. Without it the wall made
	// its default, <first --write>/.nova-sandbox-tmp, inside the job directory's repo, and
	// set TMPDIR there over the one nativeChildEnv hands (fault 1, 2026-10-10).
	argv = append(argv, "--tmp", tmpDir, "--cwd", jobDir)
	// The keyless provider's loopback address is opened back up by name, never by widening
	// the wall's network promise (issue #591).
	if cfg.netAllow != "" {
		argv = append(argv, "--net-allow", cfg.netAllow)
	}
	// The shell launcher read the harness's own directory and /opt/homebrew so git and the
	// harness's libraries resolve inside the wall; the native path does the same (run 7).
	// Without the harness directory the wall denies even the resolver's own files, and
	// without /opt/homebrew the common toolchain roots are invisible.
	argv = append(argv, "--read", filepath.Dir(bin))
	if cfg.headless() != "" {
		if root := swarm.HeadlessProgramRoot(bin); root != filepath.Dir(bin) {
			argv = append(argv, "--read", root)
		}
	}
	if fi, err := os.Stat("/opt/homebrew"); err == nil && fi.IsDir() {
		argv = append(argv, "--read", "/opt/homebrew")
	}
	// THE BENCH TOOLCHAIN (pkg/swarm/toolchain.go is the one source of these names).
	// This is the implicit worker description's `read_roots`: the provisioning standard puts
	// Go and sbcl in a user directory, and without the roots the wall denied EXECUTION of
	// the bench's own `go` and left the card the distribution's 1.22.2, which `go.mod`
	// refuses under GOTOOLCHAIN=local. Read-only, skipped if absent, and nothing else under
	// HOME is named.
	//
	// ONE LIST, TWO KINDS, and the kind comes from the list rather than from here: `--read`
	// for the sdk tree, whose `go` the card must RUN, and `--read-noexec` for the module
	// cache, which the card only reads. A `--read` root carries EXECUTE on both wall bodies,
	// so the cache under that flag would put every dependency's own files one exec away from
	// running inside the wall (security review of #1364).
	//
	// AND THE LIST IS PER GOOS, because a Mac bench's toolchains are INSTALLED rather than
	// unpacked into a home and each one resolves its runtime from the directory of the
	// launcher that ran it -- `/opt/homebrew/bin/go` is a symlink into the Cellar, and
	// without the Cellar tree the wall left the M2 Air `go: cannot find GOROOT directory:
	// 'go' binary is trimmed`, `java: Unable to locate a Java Runtime` and `dotnet: Failed to
	// resolve full path of the current executable []` (measured 2026-09-18).
	for _, root := range swarm.ToolchainRoots(benchOS(cfg), benchHome(cfg)) {
		flag := "--read-noexec"
		if root.Exec {
			flag = "--read"
		}
		argv = append(argv, flag, root.Path)
	}
	// The worker description's own read roots (issue #1463): the directories a person at the
	// desk declared every job of this worker may read. They are --read and never --write and
	// never --read-noexec -- a root the desk names is a reference, not a workspace, and the
	// execute question is the toolchain list's above, which decides its own kinds.
	for _, r := range nativeReadRoots(cfg) {
		argv = append(argv, "--read", r)
	}
	// The checkout borrows the bench mirror's objects (swarm.MirrorCloneArgs): git inside the
	// wall reads them through the checkout's alternates, and writes only its own. A read,
	// never a write, never an exec.
	if cfg.borrowed != "" {
		argv = append(argv, "--read-noexec", cfg.borrowed)
	}
	for _, r := range cfg.repos {
		argv = append(argv, "--repo", r)
	}
	argv = append(argv, "--")
	argv = append(argv, launch...)
	return argv
}

// benchHome is the home the toolchain roots are found under: the one a caller named, and
// otherwise this process's own. An OS that will not say is the empty string, which names no
// root at all -- a wall with no toolchain root is the behaviour of every run before the
// roots existed, and it is never a guess at a directory.
func benchHome(cfg nativeRunConfig) string {
	if cfg.benchHome != "" {
		return cfg.benchHome
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// benchOS is the operating system whose toolchain list the roots come from: the one a caller
// named, and otherwise this process's own. The list is per GOOS because a linux bench's
// toolchain is unpacked under HOME and a Mac bench's is installed on the machine.
func benchOS(cfg nativeRunConfig) string {
	if cfg.benchOS != "" {
		return cfg.benchOS
	}
	return swarm.ThisOS()
}

// nativeChildEnv is the child's whole environment, built rather than inherited: a short
// allowlist survives the caller's own environment (PATH, LANG, TERM, the XDG_ and
// NOVA_SWARM_ families, and any provider credential whose name carries KEY, TOKEN or
// SECRET), and the names this run owns are then set exactly once. HOME and XDG_DATA_HOME
// point at the data home, NOVA_SWARM_JOB names the job directory, and TMPDIR is the slot's
// own tmp/<label> (never the job directory, which admission git-inits into a repo) instead
// of the caller's own, which the wall denies.
// XDG_CONFIG_HOME and XDG_CACHE_HOME are dropped, never inherited, so the harness defaults
// them under HOME and never follows them outside the wall.
//
// cacheDir, when nonempty, points GOMODCACHE, GOCACHE and ASDF's compiled Lisp output at
// the bench-shared caches under <root>/cache and pins GOTOOLCHAIN=local; empty is
// --no-shared-caches, and the four names are then absent.
//
// secretEnv is the NAME a worker description's `secret` carries (issue #881): the value is
// passed through to the child BY NAME, exactly once -- stripped from the inherited set even
// when its name already carries KEY/TOKEN/SECRET -- so a name that does not itself carry one
// still reaches the harness. The value is never written to a file and never printed; the
// argv log redacts any name that carries a secret.
//
// shimDir and shimShell are the shell wrappers this run wrote under <slot>/shim
// (shellshim.go, issue #1814). shimDir goes FIRST on the child's PATH and shimShell is
// pinned as SHELL, which are the two names the harness resolves its bash tool's shell
// through. The harness process keeps the key -- it is the process that makes the API call
// -- and every shell under it is handed an environment with the secret names unset. Both
// are empty on windows and in the unit tests of the argv builder, and the environment is
// then exactly what it was.
//
// toolPath is the bench's toolchain directories (swarm.BenchPath: the `bin` of every home
// root the wall executes, then GOROOT/bin where `go` and `gofmt` live), put on the child's
// PATH in that order right after the wrappers, so a card's bare `go`, `gofmt`, `dotnet` or
// `cargo` resolves to the toolchain the wall grants whatever PATH the loop unit started the
// member with. Empty names nothing and leaves PATH as it was.
func nativeChildEnv(dataHome, jobDir, tmpDir, cacheDir, secretEnv, shimDir, shimShell string, toolPath []string) []string {
	return nativeChildEnvFrom(os.Environ(), dataHome, jobDir, tmpDir, cacheDir, secretEnv, shimDir, shimShell, toolPath)
}

// nativeChildEnvFrom is nativeChildEnv over environ, native's own environment.
func nativeChildEnvFrom(environ []string, dataHome, jobDir, tmpDir, cacheDir, secretEnv, shimDir, shimShell string, toolPath []string) []string {
	var kept []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if keepNativeEnv(name) {
			kept = append(kept, kv)
		}
	}
	remove := []string{
		"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "NOVA_SWARM_JOB", "TMPDIR",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "ASDF_OUTPUT_TRANSLATIONS",
	}
	if shimShell != "" {
		remove = append(remove, "SHELL")
	}
	if secretEnv != "" {
		remove = append(remove, secretEnv)
	}
	// the decide read's key is native's own (nativedecide.go), never the child's
	remove = append(remove, decide.JevSecret)
	for _, name := range remove {
		kept = environWithoutName(kept, name)
	}
	// Git config isolation is unconditionally enforced so the bench's own config
	// cannot leak into what the worker commits.
	kept = append(kept, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out := append(kept,
		"HOME="+dataHome,
		"XDG_DATA_HOME="+dataHome,
		"NOVA_SWARM_JOB="+jobDir,
		"TMPDIR="+tmpDir,
		// the harness prints its ERROR lines into the capture, so a start it refuses names
		// why (`error="ProviderModelNotFoundError: ..."`), not only its UnknownError envelope
		"OPENCODE_PRINT_LOGS=1",
		"OPENCODE_LOG_LEVEL=ERROR",
	)
	if cacheDir != "" {
		out = append(out,
			"GOMODCACHE="+swarm.GoModCacheDir(filepath.Dir(cacheDir)),
			"GOCACHE="+swarm.GoBuildCacheDir(filepath.Dir(cacheDir)),
			"GOTOOLCHAIN=local",
			"ASDF_OUTPUT_TRANSLATIONS="+swarm.JobASDFOutputTranslations(filepath.Join(jobDir, swarm.JobRepo)),
		)
	}
	// The wrappers go on before the secret is re-added, so the ONE process that keeps the
	// key is the harness itself and every shell it spawns by name is scrubbed (#1814).
	out = pathWithDirsFirst(out, append([]string{shimDir}, toolPath...)...)
	if shimShell != "" {
		out = append(out, "SHELL="+shimShell)
	}
	// a worker whose description names the decide read's key is handed nothing by it: the
	// key is native's alone (nativedecide.go)
	if secretEnv != "" && secretEnv != decide.JevSecret {
		for _, kv := range environ {
			if v, ok := strings.CutPrefix(kv, secretEnv+"="); ok {
				out = append(out, secretEnv+"="+v)
			}
		}
	}
	return out
}

// nativeCacheDir is the bench-shared Go cache root: <root>/cache, the one directory every
// slot of a bench shares so a card's data home holds harness state only (card 8963). The
// module and build caches are its two children. It is empty when the caller typed
// --no-shared-caches, which restores the old per-card caches under HOME, and while the root
// is unset (a unit test of the argv builder), when there is nothing to share.
func nativeCacheDir(cfg nativeRunConfig) string {
	if cfg.noSharedCaches || cfg.root == "" {
		return ""
	}
	return swarm.CacheRoot(cfg.root)
}

// nativeReadRoots is what the worker description declared every job of this worker may READ
// absolute directories the description names -- a bench-local mirror, a
// corpus, a toolchain under a user directory. `swarm.LoadWorker` has already refused a
// relative entry, an empty one, one that does not exist, one that is not a directory and one
// that would hold the key file (pkg/swarm/worker.go:227-268), so nothing is re-checked
// here and nothing is invented: a run with no description names none.
func nativeReadRoots(cfg nativeRunConfig) []string {
	if cfg.worker == nil {
		return nil
	}
	return cfg.worker.ReadRoots
}

// keepNativeEnv says whether one inherited name survives into the native child: the names a
// program needs (PATH, LANG, TERM), explicit Git pool identity variables (GIT_AUTHOR_NAME,
// GIT_AUTHOR_EMAIL, GIT_COMMITTER_NAME, GIT_COMMITTER_EMAIL), the XDG_ and NOVA_SWARM_ families,
// any harness configuration whose name starts with OPENCODE_, and any provider credential whose
// name carries KEY, TOKEN or SECRET. Everything else is the caller's own noise and is dropped,
// so no path the caller happened to export reaches the child.
func keepNativeEnv(name string) bool {
	switch name {
	case "PATH", "LANG", "TERM":
		return true
	case "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL":
		return true
	}
	if strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "NOVA_SWARM_") || strings.HasPrefix(name, "OPENCODE_") {
		return true
	}
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}

// environWithoutName is the environment minus one name, so a name this run sets itself is
// certain to appear exactly once.
func environWithoutName(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); n == name {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// writeNativeArgvLog records the exact argv and environment the child is about to be handed
// into <slot>/native-argv.log, so a later reader can prove what the wall was asked to run.
// A value whose name carries KEY, TOKEN or SECRET is written as <redacted>, never the secret
// itself. A log that cannot be written is the error returned, which the run names on a NOTE
// line and goes on without.
func writeNativeArgvLog(slotDir, runPath string, runArgv, env []string) error {
	var f strings.Builder
	fmt.Fprintf(&f, "argv: %s\n", oneline.Escape(strings.Join(append([]string{runPath}, runArgv...), " ")))
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if keepNativeSecretName(name) {
			val = "<redacted>"
		}
		fmt.Fprintf(&f, "env: %s=%s\n", oneline.Escape(name), oneline.Escape(val))
	}
	return os.WriteFile(filepath.Join(slotDir, "native-argv.log"), []byte(f.String()), 0o644)
}

// keepNativeSecretName says whether a name carries a secret, which the argv log redacts.
func keepNativeSecretName(name string) bool {
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}

// wallNamed reads the SANDBOX OK line out of the wall's captured stderr and returns the
// backend it named and the cwd it applied. The cwd is read from the cwdb64=<base64url>
// field WHEN THE WALL PRINTS IT -- the machine-readable receipt, a strict base64url encoding
// of the raw path bytes the wall applied, so a path holding U+0020, a literal backslash, or
// a non-ASCII name survives the line exactly. When no receipt is present the readable
// cwd=<dir> field beside it is decoded instead (decodeField): THE cwd TOKEN IS A
// PRODUCER'S ONE-LINE FIELD, and a job directory whose path holds U+0020 -- a configured
// root under `work 2` -- reaches this side as `work\x202`, one token with the whitespace
// escaped. Taking that token literally made sameDir compare the escaped spelling with the
// real path: the two differed, the refusal formatter escaped both spellings again so they
// DISPLAYED identically, and a job that had already completed and written its RESULT and its
// usage row was refused as a pre-launch failure. Decoding the field back to the path the
// producer held restores the comparison without weakening it: a cwd that is not the job
// directory still refuses. A wall that printed no SANDBOX OK line, or one whose receipt is
// present but not valid base64url, or one that names no cwd at all, is a run this tool
// cannot trust to name its own containment, and the reason is returned (SPEC-SANDBOX rules
// 1 and 11: never silently degraded, and a wall that cannot say what it is is no wall).
func wallNamed(out string) (backend, cwd, reason string) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "SANDBOX OK ") {
			continue
		}
		receipt := false
		readable := ""
		for _, tok := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(tok, "backend="):
				backend = strings.TrimPrefix(tok, "backend=")
			case strings.HasPrefix(tok, "cwdb64="):
				raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(tok, "cwdb64="))
				if err != nil {
					return backend, "", "printed a SANDBOX OK line whose cwdb64 receipt is not valid base64url"
				}
				cwd = string(raw)
				receipt = true
			case strings.HasPrefix(tok, "cwd="):
				readable = decodeField(strings.TrimPrefix(tok, "cwd="))
			}
		}
		if backend != "" {
			if !receipt {
				if readable == "" {
					return backend, "", "printed a SANDBOX OK line with no cwdb64 receipt"
				}
				cwd = readable
			}
			return backend, cwd, ""
		}
	}
	return "", "", "ran without a SANDBOX OK line naming its backend"
}

// decodeField inverts the one-line field encoding of pkg/oneline for a token read
// back out of a producer's record. `\xNN` decodes to the byte it spells and
// `\uNNNN` to the code point; every other byte is copied through. The encoding is NOT
// injective (a literal backslash is not escaped), so a path that literally spells an
// escape sequence cannot be told from the character it encodes; that limit is SPEC.md's
// and this inverse does not change it. It exists only to put a producer's own field back
// the way the producer held it before the value is compared or printed.
func decodeField(s string) string {
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'x':
				if i+3 < len(s) {
					if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
						buf = append(buf, byte(v))
						i += 4
						continue
					}
				}
			case 'u':
				if i+5 < len(s) {
					if v, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
						buf = append(buf, string(rune(v))...)
						i += 6
						continue
					}
				}
			}
		}
		buf = append(buf, s[i])
		i++
	}
	return string(buf)
}

// sameDir asks whether two paths name the same directory once symlinks are resolved, so a
// wall that reports the job directory spelled through a symlinked parent still matches the
// path the caller built it from. When either path will not resolve, the raw strings are
// compared.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return a == b
}

// writeNativeUsage records one card's usage row next to its RESULT.md, once the child is
// gone and its store is complete. The token numbers come from the store; the data home is
// passed here explicitly (the run chose it), and the reader looks in its standard locations.
// When sqlite3 is missing the columns are dashes and the note is carried to the caller, and
// the run still finishes rather than failing on a number nobody can see. When no store exists
// the row keeps its dashes and the returned reason and path name what the NATIVE OK line says.
// ONE ROW PER LAUNCH: a native run that retries a launch appends a row for each
// attempt, so a retried card's usage.tsv carries attempt=1,2,3 for its one job and each
// attempt is summed once. A fast failure whose provider reported nothing keeps its dashes,
// and `usd` stays a dash rather than becoming a zero. The `end` column names how the attempt
// ended -- done, failed, or wall.
// THE ROW IS THE LAUNCH'S (rule 13d, "Two numbers, kept apart"). It returns the usage it
// finally read as well, because the JOB's figure on the NATIVE OK line is the sum of these
// launches' own final reads -- "a job's rows are disjoint, so that adding them counts each
// launch once", and two launches reported at 40 and 70 keep 40 and 70 here while the line
// prints 110. The caller folds; this function never sees the job's running sum.
// A HEADLESS CHILD'S ROW IS READ FROM ITS CAPTURE (swarm.HeadlessUsage): what the launch
// appended to `<job>/harness-output.log`, handed here as capture. The reason is `no-usage`
// when it printed no result and `unreadable` when the result would not parse; the path is
// the capture's.
func writeNativeUsage(cfg nativeRunConfig, dataHome, provider, model string, start, end, notBefore time.Time, rc, attempt int, endWord string, capture []byte, errOut io.Writer) (usage swarm.ProviderUsage, reason, path string, written swarm.UsageRow) {
	// notBefore is the EARLIER launch's end, and it is the floor that keeps this row
	// disjoint from that one: without it the window's five-second widening reaches back over
	// the previous launch's rows and counts them twice (usagecard.go says what that cost).
	var note, storePath, rr string
	if k := cfg.headless(); k != "" {
		usage, note, rr = headlessLaunchUsage(k, capture)
		storePath = filepath.Join(cfg.slotDir, "jobs", cfg.label, "harness-output.log")
	} else {
		usage, note, storePath, rr = swarm.ReadCardUsageAfter(dataHome, start, end, notBefore)
	}
	rcCol := "-"
	if rc >= 0 {
		rcCol = strconv.Itoa(rc)
	}
	row := swarm.UsageRow{
		"job": cfg.label, "attempt": strconv.Itoa(attempt),
		"started": start.UTC().Format(time.RFC3339),
		"ended":   end.UTC().Format(time.RFC3339),
		"end":     dash(endWord),
		"rc":      rcCol, "provider": provider, "model": model,
	}
	for _, c := range swarm.TokenColumns {
		row[c] = dash(usage.Values[c])
	}
	row["usd"] = dash(usage.Values["usd"])
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	if err := swarm.AppendCardUsage(filepath.Join(jobDir, "usage.tsv"), row); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the usage.tsv could not be written: %s\n", oneline.Escape(err.Error()))
	}
	// THE DURABLE COPY. The job file above is the working copy the
	// batch still reads. The same row is also appended under this run's own
	// attempt directory, so a later invocation cannot mix its usage into this
	// one. An empty results root is the direct-test shape and writes nothing else.
	if dir := nativeResultsAttemptDir(cfg, attempt); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the results directory %s could not be made: %s\n", oneline.Field(dir), oneline.Escape(err.Error()))
		} else if err := swarm.AppendCardUsage(filepath.Join(dir, "usage.tsv"), row); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the usage.tsv could not be written: %s\n", oneline.Escape(err.Error()))
		}
	}
	if err := swarm.AppendCardUsage(filepath.Join(cfg.slotDir, "usage.tsv"), row); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the slot's usage.tsv could not be written: %s\n", oneline.Escape(err.Error()))
	}
	if note != "" {
		fmt.Fprintf(errOut, "NATIVE NOTE: %s\n", oneline.Escape(note))
	}
	if rr == "" {
		return usage, "", "", row
	}
	if storePath == "" {
		storePath = filepath.Join(dataHome, filepath.FromSlash(swarm.OpenCodeDB))
	}
	return usage, rr, storePath, row
}

// headlessLaunchUsage is one headless launch's usage read from its capture: the usage, a
// note for errOut, and the usage=none reason ("" when the harness reported).
func headlessLaunchUsage(kind string, capture []byte) (usage swarm.ProviderUsage, note, reason string) {
	u, err := swarm.HeadlessUsage(kind, capture)
	switch {
	case err != nil:
		return swarm.ProviderUsage{Values: map[string]string{}}, "the harness's usage could not be read from its output: " + err.Error(), "unreadable"
	case !u.Observed:
		return u, "", "no-usage"
	}
	return u, "", ""
}

// seedHeadlessHome makes the harness's private home (swarm.HeadlessHome): emptied of what
// an earlier card of this slot left, then given a copy of the credential files the harness
// needs, 0600, and nothing else of the bench's own login (claude needs none: its login is
// the token the run hands it, h.Token). A credential the bench has not got is skipped: the
// harness then answers logged out, which the run classes as a provider failure of class
// auth (swarm.HeadlessFailure). The bench's own files are only read, so the card can write
// its copy and never the login.
func seedHeadlessHome(h swarm.HeadlessHome) error {
	if err := safepath.RemoveUnder(filepath.Dir(h.Dir), h.Dir); err != nil {
		return err
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	for _, name := range h.Login {
		b, err := os.ReadFile(filepath.Join(h.Source, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(h.Dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// launchSpend is one launch's final read as a cost record (pkg/cardcost): the five
// token classes (a dash stays unreported, never 0), its requests and largest prompt, the
// harness's own cost and the provider/model it reported.
func launchSpend(v map[string]string) cardcost.Usage {
	line := []string{}
	for col, key := range map[string]string{"tokens_in": "input", "cache_read": "cache_read", "cache_write": "cache_write",
		"tokens_out": "output", "reasoning": "reasoning", "requests": "requests", "max_prompt": "max_prompt"} {
		if v[col] != "" && v[col] != swarm.Dash {
			line = append(line, key+"="+v[col])
		}
	}
	u := cardcost.ParseUsage(strings.Join(line, " "))
	if v["cost"] != "" {
		u.Actual, u.ActualBy = v["cost"], cardcost.ActualByHarness
	}
	if v["provider"] != "" && v["provider"] != swarm.Dash && v["model"] != "" && v["model"] != swarm.Dash {
		u.Model = v["provider"] + "/" + v["model"]
	}
	return u
}

// spendCost is the harness's cost of the launches so far, "" unless every launch that
// reported tokens reported one (the cost spendWord carries).
func spendCost(launches []cardcost.Usage) string {
	return cardcost.ParseSpend(spendWord(launches)).Actual
}

// spendWord is the job's spend= word over its launches' records: their sum, the model the
// last one reported, and the harness's cost only when every launch that reported tokens
// reported one (a cost for part of the job is not the job's).
func spendWord(launches []cardcost.Usage) string {
	if len(launches) == 0 {
		return ""
	}
	total := cardcost.SumUsage(launches)
	model, priced := "", 0
	for _, u := range launches {
		if u.Model != "" {
			model = u.Model
		}
		if u.Actual != "" || !u.Tokens.Reported() {
			priced++
		}
	}
	cost := total.Actual
	if priced < len(launches) {
		cost = ""
	}
	return cardcost.SpendWord(total.Tokens, cost, model)
}

// nativeRunSeq distinguishes invocations that share a process, which is what a
// test does when it runs the same label twice. The stamp and the pid
// distinguish every other pair. Together they are one path element.
var nativeRunSeq uint64

func newNativeRunID() string {
	n := atomic.AddUint64(&nativeRunSeq, 1)
	return fmt.Sprintf("%d-%d-%d", time.Now().UTC().UnixNano(), os.Getpid(), n)
}

// claimNativeResultsRun creates <results-root>/<label>/<runID> exclusively.
// Mkdir fails if the name is taken, and the next id is a different sequence
// value, so two invocations cannot publish into one directory.
func claimNativeResultsRun(cfg nativeRunConfig) (string, error) {
	labelDir := filepath.Join(cfg.resultsRoot, cfg.label)
	if err := os.MkdirAll(labelDir, 0o755); err != nil {
		return "", err
	}
	var last error
	for i := 0; i < 8; i++ {
		id := newNativeRunID()
		if !safepath.NameOK(id) {
			last = fmt.Errorf("run id %s is not a name", id)
			continue
		}
		err := os.Mkdir(filepath.Join(labelDir, id), 0o755)
		if err == nil {
			return id, nil
		}
		last = err
		if os.IsExist(err) {
			continue
		}
		return "", err
	}
	if last == nil {
		last = fmt.Errorf("no free run directory")
	}
	return "", last
}

// nativeResultsAttemptDir is <results-root>/<label>/<runID>/<attempt>, or ""
// when this run is not publishing outside the job. The run id is claimed once
// per invocation; the attempt is the decimal the launch loop counted, and it
// restarts at 1 every invocation, which is why it is not the identity.
func nativeResultsAttemptDir(cfg nativeRunConfig, attempt int) string {
	if strings.TrimSpace(cfg.resultsRoot) == "" || !safepath.NameOK(cfg.label) || !safepath.NameOK(cfg.runID) || attempt < 1 {
		return ""
	}
	attemptName := strconv.Itoa(attempt)
	if !safepath.NameOK(attemptName) {
		return ""
	}
	return filepath.Join(cfg.resultsRoot, cfg.label, cfg.runID, attemptName)
}

// publishNativeResults copies RESULT.md and the harness report into the attempt
// directory usage.tsv was already appended to. It returns that directory only
// when the copy landed, so a sweep that sees "" leaves the job in place rather
// than deleting the only copy. A card that published nothing still publishes
// usage.tsv and the report: the spend and the capture are what a sweep removes
// along with the working directory.
func publishNativeResults(cfg nativeRunConfig, jobDir string, attempt int, errOut io.Writer) string {
	dir := nativeResultsAttemptDir(cfg, attempt)
	if dir == "" {
		return ""
	}
	if within(jobDir, dir) {
		fmt.Fprintf(errOut, "NATIVE NOTE: the results directory %s is inside the job directory; not publishing there\n", oneline.Field(dir))
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the results directory %s could not be made: %s\n", oneline.Field(dir), oneline.Escape(err.Error()))
		return ""
	}
	// THE CAPTURE IS REQUIRED. A missing or unreadable harness-output.log is not
	// an absent optional file: copying nothing and still reporting success is
	// how --sweep-now deleted the only copy. Any error keeps the job.
	reportFrom := filepath.Join(jobDir, "harness-output.log")
	if err := copyRegularFile(reportFrom, filepath.Join(dir, "report")); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the report could not be published to %s: %s\n", oneline.Field(dir), oneline.Escape(err.Error()))
		return ""
	}
	if from, ok := swarm.FindCardResult(jobDir); ok {
		if err := copyRegularFile(from, filepath.Join(dir, "RESULT.md")); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: RESULT.md could not be published to %s: %s\n", oneline.Field(dir), oneline.Escape(err.Error()))
			return ""
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.tsv")); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: usage.tsv was not published under %s\n", oneline.Field(dir))
		return ""
	}
	return dir
}

// copyRegularFile copies one regular file by bytes and rename. A symlink or a
// pipe is not a result: the same rule the gather uses, so a planted link cannot
// be what gets published outside the job.
func copyRegularFile(from, to string) error {
	fi, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", from)
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Clean(to), raw, 0o644)
}

// sweepNativeJob removes the job directory after its results have been
// published. The path has to sit strictly below the swarm root; safepath is
// the only removal.
func sweepNativeJob(root, job string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(job) == "" {
		return fmt.Errorf("the job directory is not known")
	}
	return safepath.RemoveUnder(root, job)
}

// launchArgvFor is the one launcher, swarm.LaunchArgvFor: the providers table decides the
// argv every native launch hands its harness. It is a variable only so a test can prove
// that a launch asks it; nothing else ever assigns it.
var launchArgvFor = swarm.LaunchArgvFor

// nativeLaunchArgv is the harness argv of one native run, built by the one launcher from
// the providers table. The table's row for the route's provider gives the
// shape -- the row swarm.DefaultLaunchRow when the table names no row of its own -- and
// this run fills it: the binary resolved from --harness, the provider/model the run was
// routed to, its label as the title, and the card text as the prompt -- followed, for a
// typed card, by the RESULT-FORMAT paragraph (swarm.CardPrompt). The
// card's sha256 stays the sha of the card text alone.
func nativeLaunchArgv(bin string, cfg nativeRunConfig, provider string) ([]string, error) {
	// a headless harness has one argv shape of its own (swarm.HeadlessArgv), the model
	// the part of the route after its provider
	if k := cfg.headless(); k != "" {
		return swarm.HeadlessArgv(k, bin, cfg.model[len(provider)+1:], nativePrompt(cfg))
	}
	return launchArgvFor(swarm.LaunchRow(provider), benchOS(cfg), swarm.LaunchRequest{
		Harness: bin, Model: cfg.model, Title: cfg.label, Prompt: nativePrompt(cfg),
	})
}

// nativePrompt is the card as the harness is handed it: a framed card's prompt begins with
// its JOB.md (docs/SPEC-CARD-CONTRACT.md section 2), any other is the card as written.
func nativePrompt(cfg nativeRunConfig) string {
	if cfg.frame == nil {
		return swarm.CardPrompt(cfg.card)
	}
	return cardcontract.Prompt(filepath.Join(cfg.slotDir, "jobs", cfg.label), swarm.CardPrompt(cfg.card))
}

// writeStageOK names the command phases of staging (docs/SPEC-CARD-CONTRACT.md,
// staging), while preserving the readiness line before the frame is installed.
func writeStageOK(w io.Writer, bench string, st swarm.StageResult) {
	fmt.Fprintf(w, "STAGE OK bench=%s repo=%s base=%s secs=%.0f clone=%.1f fetch=%.1f checkout=%.1f\n",
		oneline.Field(bench), oneline.Field(st.BaseRepo), oneline.Field(swarm.Version8(st.BaseSha)), st.Wall.Seconds(), st.Clone.Seconds(), st.Fetch.Seconds(), st.Checkout.Seconds())
}

// installFrameTimed reports the whole successful frame installation, including
// recipes and shims, separately from staging (docs/SPEC-CARD-CONTRACT.md, staging).
func installFrameTimed(cfg nativeRunConfig, jobDir, head string, carry *cardcontract.Carry, w io.Writer) (start string, err error) {
	started := time.Now()
	if start, err = installFrame(cfg, jobDir, head, carry); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "FRAME OK secs=%.1f\n", time.Since(started).Seconds())
	return start, nil
}

// installFrame records the staged commit in the slot and writes a framed launch's JOB.md
// and its family's shims into <slot>/shim, the shims handing through to the real git; a
// rework's carry (where it was staged, and whether the work before it came) is JOB.md's. It
// returns a read's start: the commit the work it reads started from (workStart), "" for
// work or when none is known.
func installFrame(cfg nativeRunConfig, jobDir, head string, carry *cardcontract.Carry) (string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("no git on PATH for the shims to hand through to: %w", err)
	}
	if git, err = filepath.Abs(git); err != nil {
		return "", err
	}
	st := cardcontract.Staged{Job: jobDir, Repo: filepath.Join(jobDir, swarm.JobRepo), Head: head, Git: git, Carry: carry}
	if nativeCacheDir(cfg) != "" {
		st.GoCache = swarm.GoBuildCacheDir(cfg.root) // the GOCACHE nativeChildEnv hands the child
	}
	if cfg.frame.Kind == "read" {
		// first, so a read whose start cannot be known leaves nothing of its frame behind
		if st.Start, err = workStart(git, st.Repo, cfg.frame.ReviewBase); err != nil {
			return "", err
		}
		if st.Start != "" {
			if st.Gate, err = readGate(git, st.Repo, st.Start); err != nil {
				return "", err
			}
		}
	}
	// the commit staged, recorded in the slot (outside the job the child writes): the
	// member counts the child's commits from it, never from the checkout's own refs
	if err := atomicfile.Write(filepath.Join(cfg.slotDir, cardcontract.StagedName), []byte(head+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("recording the staged commit: %w", err)
	}
	if err := cardcontract.StageRecipes(*cfg.frame, jobDir); err != nil {
		return "", err
	}
	return st.Start, cardcontract.Install(cardcontract.For(cardcontract.FamilyOf(cfg.model)), *cfg.frame, st, nativeShellShimDir(cfg.slotDir))
}

// errReadStart marks a read refused at staging because the commit its work started from
// cannot be known: native prints the STAGE FAIL line for it, as for a stage that failed, so
// no child runs and the sprint deals the read again.
var errReadStart = errors.New("staging refused: the read's start")

// workStart is the commit the work a read reviews started from, found in the read's staged
// checkout before the child runs: the merge base of its HEAD (the work's head) and the
// review base, a full sha; "" when they have none. The packet carries the base's name, never
// the commit the work was staged on, and the base branch moves while the work is read
// (docs/SPEC-CARD-CONTRACT.md, JOB.md).
//
// The base is one of three, told apart in this order: a full sha (it never moves); a branch,
// when the checkout holds refs/remotes/origin/<base>; a tag, when it holds refs/tags/<base>
// and no such branch (a tag never moves); and anything else is taken for a branch. A sha or
// a tag is used as it is. A branch is fetched from origin into refs/remotes/origin/<base>
// first, and a fetch that fails is an errReadStart, never the clone's own ref: the checkout
// is cloned from the bench mirror, whose branch can be older than the commit the work
// started from, the merge base against it is that older tip, and a diff from it shows every
// card landed in between as the work's own, so the diff names many cards' files and not the
// one the read reviews (measured by a 1000-card load test: the diff had 22 files, not
// exactly one). origin's branch holds the work's start (the work was cut from
// it) and not the work (a read comes before the land), so the merge base against it is
// exactly the start, however far the branch has moved since.
func workStart(git, checkout, base string) (string, error) {
	if base == "" {
		return "", nil
	}
	o := gitrun.Options{Bin: git, C: checkout, OwnRepo: true}
	has := func(ref string) bool {
		_, err := gitrun.Output(context.Background(), o, "rev-parse", "-q", "--verify", "--end-of-options", ref+"^{commit}")
		return err == nil
	}
	ref := "refs/remotes/origin/" + base
	switch {
	case typedrec.IsFullSha(base):
		ref = base
	case !has(ref) && has("refs/tags/"+base):
		ref = "refs/tags/" + base
	default:
		if _, err := gitrun.Output(context.Background(), o, "fetch", "-q", "--no-tags", "--", "origin", "+refs/heads/"+base+":"+ref); err != nil {
			return "", fmt.Errorf("%w: the base branch %s could not be fetched from origin, and the checkout's own %s may be older than the work's start: %w", errReadStart, base, ref, err)
		}
	}
	sha, err := gitrun.Output(context.Background(), o, "merge-base", "--end-of-options", "HEAD", ref)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			// Unrelated histories have no merge base: JOB.md names no start.
			return "", nil
		}
		return "", fmt.Errorf("%w: the merge base against %s could not be read: %w", errReadStart, base, err)
	}
	if !typedrec.IsFullSha(sha) {
		return "", fmt.Errorf("%w: the merge base against %s returned no full commit sha: %q", errReadStart, base, sha)
	}
	return sha, nil
}

// readGate is a read's gate (cardcontract.ReadGate): the tests of what the work changed,
// start..HEAD in the checkout.
func readGate(git, checkout, start string) (*cardcontract.Gate, error) {
	out, err := gitrun.Output(context.Background(), gitrun.Options{Bin: git, C: checkout, OwnRepo: true},
		"diff", "--name-only", "--no-renames", "-z", "--end-of-options", start, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("the read's gate: the files the work changed could not be read: %w", err)
	}
	var changed []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			changed = append(changed, f)
		}
	}
	return cardcontract.ReadGate(checkout, changed)
}

// providerOf splits a native model id on its single slash and reports whether it
// has a believable provider prefix: both sides nonempty, no slash inside the
// provider.
func providerOf(model string) (string, bool) {
	provider, rest, ok := strings.Cut(model, "/")
	if !ok || provider == "" || rest == "" {
		return "", false
	}
	return provider, true
}

// strictlyWithin is within with the root itself excluded: a label of ".." makes
// Join(slot, "jobs", "..") the slot, which is inside the root and is still not a job
// directory of this run's own.
func strictlyWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// within reports whether path sits at or under root, lexically, without touching the
// filesystem, so a missing slot can still be judged.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyAuth moves exactly the provider's entry from the auth file into
// dataHome/auth.json, mode 0600, and returns the refusal reason when the source is
// looser than 0600 or the copy cannot end 0600. Both mode questions are asked of the
// platform (authmode.go): windows reports 0666 for every readable file, so neither rule
// refuses there. A refusal raised after the first write unlinks every copy it left --
// dataHome/auth.json, and dataHome/opencode/auth.json once that one is written -- before
// returning, so a refused copy leaves no plaintext key on the bench
// (docs/SPEC-SECRETS.md, the dogfooding ten), and a failed unlink is named in the reason.
func copyAuth(src, provider, dataHome string) string {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Sprintf("the auth file %s could not be read: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	if authModeWiderThanOwner(runtime.GOOS, st.Mode()) {
		return fmt.Sprintf("the auth copy would not be 0600: the auth file %s is mode %04o, so copying it spreads a secret beyond its owner; chmod 600 it first",
			oneline.Field(src), st.Mode().Perm())
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Sprintf("the auth file %s could not be read: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	var entries map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Sprintf("the auth file %s is not one JSON object of provider entries: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	one := map[string]any{}
	if v, ok := entries[provider]; ok {
		one[provider] = v
	}
	body, _ := json.Marshal(one)
	dst := filepath.Join(dataHome, "auth.json")
	ocCopy := filepath.Join(dataHome, "opencode", "auth.json")
	if err := os.WriteFile(dst, body, 0o600); err != nil {
		return fmt.Sprintf("the auth copy %s could not be written: %s", oneline.Field(dst), oneline.Escape(err.Error()))
	}
	// The first write has landed, so every refusal below leaves no copy: drop unlinks the
	// copies that exist and names one it cannot remove in the reason, never swallowing it.
	drop := func(reason string, copies ...string) string {
		for _, p := range copies {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				reason = fmt.Sprintf("%s; the refused copy %s could not be removed: %s", oneline.Escape(reason), oneline.Field(p), oneline.Escape(err.Error()))
			}
		}
		return reason
	}
	if dstSt, err := os.Stat(dst); err == nil && authModeNotOwnerOnly(runtime.GOOS, dstSt.Mode()) {
		return drop(fmt.Sprintf("the auth copy would not be 0600: %s ended mode %04o", oneline.Field(dst), dstSt.Mode().Perm()), dst)
	}
	ocDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		return drop(fmt.Sprintf("the auth directory %s could not be made: %s", oneline.Field(ocDir), oneline.Escape(err.Error())), dst)
	}
	if err := os.WriteFile(ocCopy, body, 0o600); err != nil {
		return drop(fmt.Sprintf("the auth copy %s could not be written: %s", oneline.Field(ocCopy), oneline.Escape(err.Error())), ocCopy, dst)
	}
	return ""
}

// removeAuthCopy deletes the carried auth copy when the run ends and returns every copy
// still on disk afterwards (nil when none is). It is deferred the moment copyAuth
// succeeds, so every return path after it -- done, failed, wall, idle, terminated, or a
// refusal between the copy and the child's start -- leaves no auth.json on the bench. The
// two paths are exactly the two copyAuth writes, named rather than walked, and a file that
// is already gone is not an error.
//
// THE CARD OWNS THE DATA HOME WHILE IT RUNS, so it can take the write bit off dataHome or
// dataHome/opencode (chmod 0555) and an unlink there fails with permission denied. The
// cleanup therefore gives each parent directory -- only a real directory, never through a
// symlink the card planted -- its owner rwx back before the unlink. A copy that is still
// there afterwards is returned: the caller fails the run on it, because a plaintext key
// that outlives its card is the drift this cleanup exists to stop.
func removeAuthCopy(dataHome string, errOut io.Writer) []string {
	var left []string
	for _, p := range []string{
		filepath.Join(dataHome, "auth.json"),
		filepath.Join(dataHome, "opencode", "auth.json"),
	} {
		dir := filepath.Dir(p)
		if st, err := os.Lstat(dir); err == nil && st.IsDir() && st.Mode().Perm()&0o700 != 0o700 {
			if err := os.Chmod(dir, st.Mode().Perm()|0o700); err != nil {
				fmt.Fprintf(errOut, "NATIVE NOTE: the directory %s holding the auth copy could not be made writable again: %s\n", oneline.Field(dir), oneline.Escape(err.Error()))
			}
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(errOut, "NATIVE NOTE: the auth copy %s could not be removed at the run's end: %s\n", oneline.Field(p), oneline.Escape(err.Error()))
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			left = append(left, p)
		}
	}
	return left
}

// writeJobConfig writes the ONE opencode.json the job's harness reads, beside the carried
// auth copy in the job's own data home, mode 0600, and returns the sha8 the NATIVE OK line
// names -- the sha8 OF THE BYTES THE CHILD SEES, which is the only config any later reader
// can check the run against.
//
// It carries two things. The provider config a caller named with --config,
// whose entry for THE MODEL's provider is refused when its key is absent from the auth file:
// that provider is exactly the one the harness is about to call, and the refusal names the
// provider, never the key. And this job's own fence block, which is written
// WHETHER OR NOT a config was named, because the harness's default fence auto-rejects the
// card's own `../scratch` and every path it names on a `READ:` line.
//
// A WORKER DESCRIPTION THAT NAMES A SECRET IS THE CONFIG: its own provider
// declaration carries `{env:NAME}` -- the variable's NAME, never its value, the exact rule
// the run path writes by -- and there is no auth file for a key to be absent from,
// so the missing-auth check does not apply. --config is refused with such a description at
// the verb, because the description's declaration is the one this run means.
//
// A config file this side cannot parse is still carried verbatim -- the refusal check is
// best-effort and the copy is not -- and then the fence cannot be merged into it, which is
// said once on stderr as a NATIVE NOTE rather than refused: a run with an unparseable config
// is a run the caller has already chosen, and it is better fenced-by-default than not run.
func writeJobConfig(cfg nativeRunConfig, provider, dataHome, jobDir string, reads []string, notes io.Writer) (string, string, *swarm.ProviderProxy) {
	var raw []byte
	configPath := cfg.configFile
	switch {
	case cfg.worker != nil && cfg.worker.Secret != "":
		raw = cfg.worker.HarnessConfig()
		configPath = ""
	case configPath != "":
		body, err := os.ReadFile(configPath)
		if err != nil {
			return "", fmt.Sprintf("the config file %s could not be read: %s", oneline.Field(configPath), oneline.Escape(err.Error())), nil
		}
		if modelProviderMissingAuth(body, cfg.authFile, provider) {
			return "", fmt.Sprintf("the config file %s names provider %s, whose key is absent from the auth file %s; add it to --auth or drop the provider from --config",
				oneline.Field(configPath), oneline.Field(provider), oneline.Field(dash(cfg.authFile))), nil
		}
		raw = body
	}
	body, merged := swarm.MergeFencePermission(raw, jobDir, reads)
	// the route's model is declared in the config, so the harness knows it whatever
	// catalog it starts with (swarm.DeclareRouteModel: the fresh-home catalog race)
	if merged && strings.HasPrefix(cfg.model, provider+"/") {
		if declared, ok := swarm.DeclareRouteModel(body, provider, cfg.model[len(provider)+1:]); ok {
			body = declared
		} else if notes != nil {
			fmt.Fprintf(notes, "NATIVE NOTE: the model %s could not be declared in the config %s (its provider entry is not an object); the launch falls back to the harness's own catalog\n", oneline.Field(cfg.model), oneline.Field(dash(configPath)))
		}
	}
	var proxy *swarm.ProviderProxy
	if merged {
		// headerTimeout and chunkTimeout are written for a harness that honors
		// them. They are not the deadline: the measured OpenCode did not end a
		// stall on them. The deadline is the proxy below, and only when the
		// provider has an http baseURL the proxy can stand in front of.
		body = swarm.ApplyProviderReadDeadline(body, provider)
		upstream := swarm.ProviderBaseURL(body, provider)
		if swarm.ProviderProxyEligible(upstream) {
			silence := cfg.bodySilence
			if silence <= 0 {
				silence = swarm.ProviderBodySilence
			}
			opened, err := swarm.ListenProviderProxy(swarm.ProviderProxyConfig{
				Upstream: upstream, Silence: silence, After: cfg.bodyAfter,
				HeaderWait: cfg.headerWait,
			})
			if err != nil {
				return "", fmt.Sprintf("the provider read proxy could not listen: %s", oneline.Escape(err.Error())), nil
			}
			pointed, ok := swarm.PointProviderAtProxy(body, provider, opened.HarnessURL())
			if !ok {
				// ignored: a close on the refusal path; the reason returned below is the one reported
				_ = opened.Close()
				return "", fmt.Sprintf("the provider %s could not be pointed at the read-deadline proxy", oneline.Field(provider)), nil
			}
			body = pointed
			proxy = opened
		}
	}
	if !merged && notes != nil {
		fmt.Fprintf(notes, "NATIVE NOTE: the config %s is not a JSON object this tool can read, so the job's fence rules were not written into it and the provider request was not pointed at the read-deadline proxy; the harness runs on its own defaults and a rejection is reported as fence=rejected\n", oneline.Field(dash(configPath)))
	}
	sum := sha256.Sum256(body)
	dst := filepath.Join(dataHome, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		if proxy != nil {
			// ignored: a close on the refusal path; the reason returned below is the one reported
			_ = proxy.Close()
		}
		return "", fmt.Sprintf("the config directory %s could not be made: %s", oneline.Field(filepath.Dir(dst)), oneline.Escape(err.Error())), nil
	}
	if err := os.WriteFile(dst, body, 0o600); err != nil {
		if proxy != nil {
			// ignored: a close on the refusal path; the reason returned below is the one reported
			_ = proxy.Close()
		}
		return "", fmt.Sprintf("the config copy %s could not be written: %s", oneline.Field(dst), oneline.Escape(err.Error())), nil
	}
	return hex.EncodeToString(sum[:])[:8], "", proxy
}

// modelProviderMissingAuth reports whether the one provider this run will call -- the
// --model's -- is named by the config and has no entry in the auth file. Only that provider
// is asked about. A config is the whole of a person's ~/.config/opencode and names every
// provider they keep; the ones this model does not use are never reached by the child, so
// their keys are not this run's business, and refusing on them would refuse good cards.
//
// It answers false when the config does not parse into a "provider" object (the copy is
// still performed verbatim), when the config does not name this provider at all, and when
// the entry's options carry a baseURL and no apiKey field -- ollama on localhost has no key
// to be absent.
func modelProviderMissingAuth(raw []byte, authPath, provider string) bool {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false
	}
	providers, ok := cfg["provider"].(map[string]any)
	if !ok {
		return false
	}
	entry, named := providers[provider]
	if !named || keylessProvider(entry) {
		return false
	}
	if authPath == "" {
		return true
	}
	authRaw, err := os.ReadFile(authPath)
	if err != nil {
		return true
	}
	var entries map[string]any
	if json.Unmarshal(authRaw, &entries) != nil {
		return true
	}
	_, has := entries[provider]
	return !has
}

// keylessProvider reports whether a provider entry needs no key: its options carry a baseURL
// and no apiKey field, so the harness reaches it (for example ollama on localhost) with no
// credential to be absent.
func keylessProvider(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	opts, ok := m["options"].(map[string]any)
	if !ok {
		return false
	}
	baseURL, _ := opts["baseURL"].(string)
	if baseURL == "" {
		return false
	}
	_, hasKey := opts["apiKey"]
	return !hasKey
}

// providerLoopback reads the carried config and returns the loopback host:port the model's
// provider's baseURL names, or "" when the provider carries no baseURL, names no loopback,
// or the config cannot be read. The wall's --net-allow opens exactly that address back up
// after a keyless provider on localhost.
func providerLoopback(cfgPath, provider string) string {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	providers, _ := cfg["provider"].(map[string]any)
	entry, ok := providers[provider]
	if !ok {
		return ""
	}
	m, _ := entry.(map[string]any)
	opts, _ := m["options"].(map[string]any)
	base, _ := opts["baseURL"].(string)
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	if !loopbackHost(host) {
		return ""
	}
	port := u.Port()
	if port == "" {
		return ""
	}
	return net.JoinHostPort(host, port)
}

// loopbackHost reports whether a host names the machine's own loopback: localhost, an
// IPv4 loopback (127/8) or IPv6's ::1. A keyless provider on such an address is not reached
// by the wall's (allow network-outbound (remote ip)) and needs its own --net-allow grant.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// fileSHA256 returns the lowercase hex sha256 of a file's bytes.
func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// nativeNetAllow is the loopback host:port the wall opens back up for this run: the keyless
// provider's the carried config names, for an opencode child. A headless harness reaches its
// vendor by remote ip and carries no providers config, so it is granted no address at all:
// its wall opens nothing an opencode child's does not (docs/SPEC-SWARM.md, what a card can
// reach).
func nativeNetAllow(cfg nativeRunConfig, provider string) string {
	if cfg.configFile == "" || cfg.headless() != "" {
		return ""
	}
	return providerLoopback(cfg.configFile, provider)
}
