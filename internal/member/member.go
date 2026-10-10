// Package member is a fleet member of a sprint: the loop a fleet machine runs
// against the sprint's fleet table (the machine's queue) and the sprint's
// verbs, with each card run as one child through a runner. The fleet table
// is the dispatcher; the member only replays, for real, the sequence the
// world driver plays in simulation: beat, queue, push and finish what ended, take to
// its width, start each card taken as a child. A reader runs the same loop
// against the readers table: begin what was asked, report what ended.
//
// The member speaks to the sprint only through its command line and JSON
// (Sprint), and runs a card only through a Runner, so the harness underneath
// (a Claude Code child, an OpenCode child, a test double) is replaceable and
// the loop is testable without a store or a process.
//
// A work card ends at a local commit inside the wall, which holds no
// credential; the member, outside the wall, pushes that commit to origin's
// branch the sprint named (Pusher) before it reports the finish, so the merge
// finds the work on origin from any machine.
package member

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/readregular"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// ReadRegular bounds a launch's durable input or capture before a member uses it
// for cost recovery. It refuses special files through the shared regular-file reader.
func ReadRegular(path string, limit int64) ([]byte, error) { return readregular.Read(path, limit) }

// Sprint runs one sprint verb and returns its exit code and stdout.
type Sprint interface {
	Run(args ...string) (code int, out []byte)
}

// Runner starts a card as a child and reports how it ended.
type Runner interface {
	// Start runs the packet as one child and returns a handle for it.
	Start(p Packet) (Child, error)
}

// Ender is a Runner told when the member is done with a launch whose child ended: reported,
// returned or reaped, so nothing reads its working tree again. failed is whether it ended in
// a way a person may want to inspect (a failed finish, a read returned with no verdict); the
// runner removes or keeps what the launch staged (docs/SPEC-SWARM.md, `member`).
type Ender interface {
	Ended(p Packet, failed bool)
}

// Epocher is a Runner told the sprint's epoch each pass, as the queue answered it: its
// cleaner, apart from the pass, removes what launches of epochs long cleared left behind
// (docs/SPEC-SWARM.md, `member`). Epoch only records the number; it never waits.
type Epocher interface {
	Epoch(epoch uint64)
}

// Child is one running card.
type Child interface {
	// Done says whether the child has ended.
	Done() bool
	// Result is how it ended: ok, the head it finished at (a work card;
	// "" when unknown), and the report (one paragraph, the child's own words).
	Result() Result
}

// Printer is a Child that says when it last printed output (zero: never). The member stamps
// progress on its card while it prints (stampProgress); a child that is not a Printer is
// never stamped, and the late rule never returns its card for want of a stamp.
type Printer interface {
	Printed() time.Time
}

// Stopper is a Child the member can end for the machine's stop (docs/SPEC-SPRINT.md section
// 14, stop cancels jobs): Stop signals the child's process to end, keeping its working tree
// and its log, and returns the pid it signalled (0 when none is known). The runner escalates
// to a kill after its own grace; the member reads Done as for any end. A Child that is no
// Stopper is left to end by itself and is stop-returned when it has.
type Stopper interface {
	Stop() (pid int)
}

// ProgressEvery is how often the member stamps progress on a card whose child prints: the
// sprint's own number (internal/sprint ProgressEvery, inside its RuleProgressWindow of ten
// minutes with room for a stamp that is late or lost; docs/SPEC-SPRINT.md section 8, the
// rules table's row late).
const ProgressEvery = 3 * time.Minute

// Pusher puts a work card's commit on origin, outside the wall: the commit
// the child's result names (Result.Head) pushed to the branch the packet
// names (Packet.Branch), never forced. It is asked once per ended launch.
type Pusher interface {
	Push(p Packet, r Result) Push
}

// Push is how a push ended: exactly one of Sha, None and Refused is set. A push that
// landed may also have opened the card's pull request (PR) or said why not (PRNote).
type Push struct {
	Sha     string // pushed: the full sha origin's branch now holds
	None    string // not pushed: why (the child committed nothing); the finish is failed, no commit
	Refused string // the push failed: git's own line; the finish is a failure
	PR      string // the pull request the member opened for the pushed head, "" when none
	PRNote  string // why a pull request the result asked for was not opened, "" when none was asked or it opened
}

// ReadStageRetry is how long a read whose stage failed waits before it is run again once by the
// same reader (docs/SPEC-SPRINT.md, the readers; docs/SPEC-CARD-CONTRACT.md, staging).
const ReadStageRetry = 15 * time.Second

// pushWidth is the most pushes one tick runs at once: each is one git to
// origin, and a tick whose children ended together pushes them together.
const pushWidth = 8

// Result is how a child ended. Ran is whether the child ran to its end (its
// harness answered); OK is its verdict: a work card's, that the work is done,
// a read's, the reader's `verdict: ok` in RESULT.md. A read whose child did
// not run or gave no verdict has no finding to report: the read is left as it
// is for the sprint's lateness rule, never filed as broken against the work.
// Shaped is whether RESULT.md has the contract's shape (docs/SPEC-CARD-CONTRACT.md
// section 3); a work card's finish is ok only with it (Judge).
type Result struct {
	Ran     bool
	OK      bool
	Shaped  bool
	Verdict string // a work card's "ok" or "not-done"; a read's "ok", "broken", or "" when the reader gave none
	Head    string
	Report  string
	Title   string // the pull request the child asked for (gh pr create), "" when none
	Body    string
	// End is how a run that did not finish ended, as the child's harness said it:
	// EndProvider, EndBudget, EndDeadline, or "" (Judge puts it first in a failed
	// finish's reason). Usage is what it spent, one line (its budget and wall),
	// reported with the finish onto the attempt's record.
	End   string
	Usage string
	// Provider is why End is EndProvider, as native read it from the harness's own log
	// or transcript: the cause, `provider: class=<c> status=<n|-> msg=<words>`
	// (docs/SPEC-CARD-CONTRACT.md section 4; tla/CardContract.tla, ProviderFailure).
	// "" when the end was a launch the provider never accepted, which names no reason.
	Provider string
	// Staging is why End is EndStaging: the reason of native's STAGE FAIL line, the launch
	// refused before any child ran (tla/CardContract.tla, StageRefused).
	Staging string
	// Budget is which budget ended a run whose End is EndBudget, and at what count, as
	// native's NATIVE BUDGET line says it ("tokens 509,940 of 400,000, $0.03"); Judge
	// says it after the end (nova-tools #5094). "" when native named none.
	Budget string
	// Step is why a tree card failed at its first work step: `step <n> <verdict>: <words>`
	// (treeFinish; docs/SPEC-SPRINT.md, a card is a tree of steps). Judge names it as the
	// failed finish's reason; "" for every other card.
	Step string
	// Gate is where native's gate decision sent a not-done child's red gate, its NATIVE GATE
	// line's route (docs/SPEC-SPRINT.md section 5, the gate verdict): GatePreExisting, which
	// Judge names as the failed finish's reason with GateTests; GateGreen (its flaky failures
	// passed their rerun), which the reader of the result takes as the verdict ok; "" for none.
	Gate      string
	GateTests string
	// Carry is where a rework was staged, as native's STAGE CARRY line says it
	// (cardcontract.Carry.Words: the staged commit, the tip of its base branch, and whether
	// the work before it carried); the finish's report carries it, so the card's timeline
	// says it. "" for a first attempt and a read.
	Carry string
}

// The gate routes the member acts on (native's NATIVE GATE line): a gate red only on
// pre-existing failures, and a gate whose every failure was flaky and passed its rerun.
const (
	GatePreExisting = cardhdr.EndPreExisting
	GateGreen       = "green"
)

// The ends Judge names first in a failed finish.
const (
	EndProvider = cardhdr.EndProvider // the sprint's route stats count it apart
	EndStaging  = cardhdr.EndStaging  // no child ran: the sprint deals the card to another member
	EndNoResult = cardhdr.EndNoResult // the child left no result: the sprint deals the card again
	EndBudget   = "budget"
	EndDeadline = "deadline"
	// EndUnverifiable is native's end=budget-unverifiable (docs/SPEC-SWARM.md, native): the
	// member's usage source, its read of the harness's usage database, stopped answering and
	// native ended the child for a budget it could no longer see. Judge says it as a budget
	// end when the child left a result; a child it ended with none is the member's failure
	// (the source is this machine's), finished staging refused, and the member rests
	// (UsageRest).
	EndUnverifiable = "budget-unverifiable"
)

// Finish is how a work launch ended, as the member judges it (tla/CardContract.tla).
type Finish string

const (
	FinishOK     Finish = "ok"     // reported: the result's head, pushed
	FinishFailed Finish = "failed" // reported --failed, with the reason
	FinishReaped Finish = "reaped" // never reported: the claim moved, or the card left the queue
)

// Judge is a work card's finish from its result and its push, in one place
// (docs/SPEC-CARD-CONTRACT.md section 4; tla/CardContract.tla, Judge): ok only
// when the result has the shape, its verdict is ok, and the member pushed a
// commit the child made; otherwise failed, with the reason. Every work card
// ends with a commit: a child with nothing to do says `verdict: nothing`, and
// that is a failed finish, nothing to do, for the coordinator to judge. A run the
// provider failed that left no result, its push not refused, is failed with the kind
// `provider failure` and the provider's reason, and only that one: the sprint deals that
// card again and never judges it; a refused push or a result with the shape is failed work
// whatever the run's end (tla/CardContract.tla, JudgeOf and ProviderFailure). A launch
// refused at staging ran no child: failed with the kind `staging refused` and the stage's
// reason, the member's failure and never the card's (StageRefused).
func Judge(r Result, pu Push) (fin Finish, why string) {
	defer func() {
		// a budget or a deadline names how the run ended first; the provider's kind is
		// the provider case's own (below), never a prefix on another reason, and nor is
		// the staging kind (the stage's own case, and the usage source's)
		if fin == FinishFailed && r.End != "" && r.End != EndProvider && r.End != EndStaging && !strings.HasPrefix(why, EndStaging) {
			end := r.End
			if end == EndUnverifiable {
				end = EndBudget // the budget's words say which: "budget: unverifiable: ..."
			}
			if end == EndBudget && r.Budget != "" {
				why = r.Budget + ": " + why // which budget, and at what count (#5094)
			}
			why = end + ": " + why
		}
	}()
	switch {
	case r.End == EndStaging:
		// the member's machine refused the launch before any child ran: the kind and the
		// stage's reason; the sprint deals the card to another member (StageRefused)
		return FinishFailed, EndStaging + ": " + r.Staging
	case r.End == EndUnverifiable && !r.Shaped:
		// this member's usage source stopped answering and native ended the child for it,
		// which left no result: the member's failure and never the card's (the owner,
		// 2026-10-03: a provider problem is never a card problem), on the staging kind, so
		// the sprint deals the card to another member without spending its redeal bound
		// and opens no judgment on it (StageRefused); a result with the shape is judged
		// below as any budget end's is
		return FinishFailed, EndStaging + ": budget " + cmp.Or(r.Budget, "unverifiable: the usage source stopped answering")
	case pu.Refused != "":
		return FinishFailed, "push refused: " + pu.Refused
	case r.End == EndProvider && r.Provider != "" && !r.Shaped:
		// the provider failed the run and it left no result: the kind and the
		// provider's reason, never the shape's; the sprint deals the card again. A refused
		// push above, and a shaped result below (nothing, not done), are the card's own
		return FinishFailed, EndProvider + ": " + r.Provider
	case !r.Shaped && r.End == "":
		// the child ended by itself and left no result: no work came back, and the sprint
		// deals the card again on another route (cardhdr.EndNoResult). A run its budget or
		// its deadline ended is the card's to be judged (below: the end is said first)
		return FinishFailed, EndNoResult + ": no RESULT.md shape"
	case !r.Shaped:
		return FinishFailed, "no RESULT.md shape"
	case r.Step != "":
		return FinishFailed, r.Step
	case r.Verdict == "nothing":
		why := strings.TrimSpace(r.Report)
		if len(why) >= len("nothing:") && strings.EqualFold(why[:len("nothing:")], "nothing:") {
			why = strings.TrimSpace(why[len("nothing:"):])
		}
		return FinishFailed, cardhdr.EndNothing + ": " + why
	case r.Verdict == "not-done" && r.Gate == GatePreExisting:
		// the gate decision classed every failure that stayed red pre-existing: the base's
		// or the member's, never the card's (sprint.FailureClass gives it no class)
		return FinishFailed, cardhdr.EndPreExisting + ": " + r.GateTests
	case r.Verdict != "ok":
		return FinishFailed, "verdict " + r.Verdict
	case pu.Sha == "":
		return FinishFailed, cardhdr.EndNoCommit + ": " + pu.None
	}
	return FinishOK, ""
}

// Packet is what a card hands the member, as `nova-sprint queue --json` and
// `take --json` print it.
type Packet struct {
	Card       string   `json:"card"`
	Kind       string   `json:"kind"` // work or read
	As         string   `json:"as"`
	Primary    string   `json:"primary"`
	Stream     string   `json:"stream"`
	Attempt    int      `json:"attempt"`
	Gen        int      `json:"gen,omitempty"`
	Epoch      uint64   `json:"epoch"`
	Brief      string   `json:"brief,omitempty"`
	Rules      string   `json:"rules,omitempty"` // the held rules file the card names, "" when it carries its own (sprint.Packet)
	Fix        string   `json:"fix,omitempty"`
	Finding    string   `json:"finding,omitempty"` // a rework's: the readers' words that found the attempt before broken
	Why        string   `json:"why,omitempty"`     // a rework's: how the attempt before ended
	Notes      []string `json:"notes"`
	Branch     string   `json:"branch,omitempty"`
	Base       string   `json:"base,omitempty"`
	BaseHead   string   `json:"base_head,omitempty"`
	BaseFrom   int      `json:"base_attempt,omitempty"` // the attempt BaseHead is the head of
	Worker     string   `json:"worker,omitempty"`
	Head       string   `json:"head,omitempty"`
	WorkBranch string   `json:"work_branch,omitempty"`
	WorkBase   string   `json:"work_base,omitempty"`
	Report     string   `json:"report,omitempty"`
	// A work card's route, as the deal drew it (docs/SPEC-SPRINT.md, the deal's
	// route): the model the child runs on, its budget and deadline (seconds);
	// empty when the store has no route, and the member's own run.
	Route    string `json:"route,omitempty"`
	Model    string `json:"model,omitempty"`
	Tokens   string `json:"tokens,omitempty"`
	USD      string `json:"usd,omitempty"`     // the dollar budget per card, a decimal; "" for none (#5094)
	Harness  string `json:"harness,omitempty"` // the harness the route names (internal/harness); "" is the member's --harness
	Deadline int    `json:"deadline,omitempty"`
	// Tier is the tier the route was drawn from when the sprint decided it (a read's
	// read tier, a rework's --tier); empty when the brief's line 1 names it.
	Tier string `json:"tier,omitempty"`
	// A decide read's bars on p(defect), as the ask wrote them on the read card
	// (docs/SPEC-SPRINT.md section 6, the decide read); empty for a strings read.
	DecideBounce string `json:"decide_bounce,omitempty"`
	DecideReview string `json:"decide_review,omitempty"`
	// A work card's gate decision bars, as the deal wrote them on the work card
	// (docs/SPEC-SPRINT.md section 5, the gate verdict); empty for none.
	DecideGateFlaky       string `json:"decide_gate_flaky,omitempty"`
	DecideGatePreexisting string `json:"decide_gate_preexisting,omitempty"`
}

// queueCard is one card of `nova-sprint queue --as <me> --json`. Its claim is the
// answer's epoch, its gen (a work card) and its attempt (a read), which a packet repeats:
// the queue hands a packet only for a card the member asked one for (queueArgs).
type queueCard struct {
	ID      string  `json:"id"`
	Table   string  `json:"table"`
	Row     string  `json:"row"`
	Col     string  `json:"col"`
	Gen     int     `json:"gen"`
	Attempt int     `json:"attempt"`
	Packet  *Packet `json:"packet"`
}

type queueOut struct {
	As    string      `json:"as"`
	Epoch uint64      `json:"epoch"`
	Width int         `json:"width"` // the worker's width, from its fleet row: a member's own, a reader's its machine's (0: no row)
	Cards []queueCard `json:"cards"`
	// Reader is whether the name is a row of the readers table (nil: a server from before
	// the field). A reader with no row beats nothing and is asked nothing until the
	// coordinator declares it (reader add).
	Reader *bool `json:"reader,omitempty"`
	// Machine is the machine's state word as the queue's server read it, RUNNING or
	// STOPPED (cmd/nova-sprint queue --json); "" from a server before the word. STOPPED
	// cancels every lane this worker runs and takes nothing (machineStop).
	Machine string `json:"machine,omitempty"`
}

type takeOut struct {
	Packets []Packet `json:"packets"`
}

// Config is one member's or reader's standing.
type Config struct {
	As string // the member's (reader's) name in the fleet (readers) table
	// Width is an override of the most cards it runs at once, a twin's. A
	// worker with none runs the width its fleet row names, read with its
	// queue every tick (the fleet row is the truth): a member's own row, a
	// reader's the row of the machine it is named for (reader-<m> runs at
	// m's width, sprint.ReaderMachine).
	Width  int
	Reader bool // run the readers-table loop instead of the fleet's
	// Room is asked once a tick before any child is started (a recovered card or a taken
	// one): ok false starts none that tick, with why said once when it begins and once when
	// it ends. nil asks nothing (docs/SPEC-SWARM.md, `member`, the disk floor).
	Room func() (ok bool, why string)
	// Now is the clock the work pass's progress is read on (BeatLoop); nil is time.Now.
	Now func() time.Time
	// Meter is the machine's one-second CPU samples, taken by a goroutine of the caller
	// (hostload.Sampler.Run); nil, or with no sample since the last beat, the beat
	// measures the machine itself.
	Meter *hostload.Sampler
	// Sleep waits between two harness starts (StartGap); nil starts them back to back
	// (the tests' member). Clock is the time it measures the gap by; nil is time.Now.
	Sleep func(time.Duration)
	Clock func() time.Time
	// Background takes the long work out of the pass: a launch's start, its result and its
	// push run apart from the pass that began them and are collected by a later pass, and
	// Wake says when one posted or a child exited. false (the tests' member) does each where
	// the pass asks for it, so a pass is one step.
	Background bool
	// Attempt asks the attempt decision over every work take's end (docs/SPEC-SPRINT.md
	// section 2, the attempt decision; the card's bars decide at the server whether it routes
	// the finish, never whether it is asked): the packet,
	// the child's RESULT.md as text and the finish's reason line in; the decision's card line
	// (decide.Decided) and the decision as one JSON record line out, both carried by the
	// finish. It is long (a backend's answer), so it runs in the end's long work, beside the
	// push, never in the pass. nil asks none.
	Attempt func(p Packet, result, reason string) (decided string, decision []byte, err error)
	// ResolveStep resolves a step line's commit (7 to 40 hex) on the card's branch
	// to one full sha (docs/SPEC-SPRINT.md, the verdict per step). nil resolves in
	// the clone StepClone names, or, when that is nil too, in the launch checkout
	// the member verb's --slots or --root names. A member with neither accepts a
	// 40-hex sha as itself and refuses a shorter prefix as unknown.
	ResolveStep func(branch, prefix string) (sha string, err error)
	// StepClone is the checkout whose branch holds the card's step commits.
	// nil discovers it from the process arguments (the member verb).
	StepClone func(p Packet) string
	// ScriptVerify makes this reader a script reader (docs/SPEC-SPRINT.md, the script read):
	// a read of a script card (CLASS: script) is first asked of it, with the card's program
	// and deadline; ok is an ok read, whose finding is the one line why, counted as all the
	// reads the card needs. Not ok is no verdict: the read goes on to a model child as any
	// read does. nil asks none.
	ScriptVerify func(p Packet, class cardhdr.Class) (ok bool, why string)
}

// launch is one child and the claim it was started for: the card at the
// generation (a read: the attempt) and the epoch of the packet it was handed,
// so its result settles that claim and no other (a card cleared and dealt
// again is a new claim, and an old child's result is reaped, never reported).
type launch struct {
	child   Child // nil until its start has ended (busy)
	gen     int
	attempt int
	epoch   uint64
	branch  string
	packet  Packet
	// busy says long work of this launch is in flight, apart from the pass (long): its
	// start, or its end (the child's result read and, for a work card, its commit pushed).
	// A busy launch is left alone: not reported, not reaped, not forgotten, until the work
	// posts what it found and a pass collects it.
	busy       bool
	busyAt     time.Time // when that long work began
	res        *Result   // how the child ended, once its end has been collected
	push       *Push     // the push at its end, once made (a finish the store did not answer is reported again, never pushed again)
	decided    *decided  // the take's attempt decision, once asked (Config.Attempt): reported with the finish, never asked again
	spent      bool      // a read whose child ended with no verdict: not ours to report, not run again until the sprint moves the card
	retryAt    time.Time // a read whose stage failed: when this reader runs it again; zero before the failure is seen
	retried    bool      // a read run again after a stage failure: a second one is returned
	claimMoved bool      // a prior queue answer showed the claim moved while still running
	dropped    bool      // a prior queue answer showed the card left the queue while still running
	stamped    time.Time // when the member last stamped progress on the card (stampProgress); zero: never
	// stopped says the machine's stop cancelled this launch (machineStop): its child was
	// told to stop (stopPid, 0 when unknown), and once it has ended the card is handed back
	// with stop-return, never finished; stopAt is when.
	stopped bool
	stopPid int
	stopAt  time.Time
}

// Member is the loop's state: the children running, by card id.
type Member struct {
	cfg     Config
	sprint  Sprint
	runner  Runner
	pusher  Pusher
	out     io.Writer
	running map[string]launch // by card id (a work card's id, a read card's id)
	// stopped says the last queue read said the machine is STOPPED (queueOut.Machine):
	// every lane was told to stop and nothing is taken, started or recovered until a
	// queue says it runs again (machineStop; tla/StopCancels.tla).
	stopped bool
	// stopRetry is when each card's refused stop-return is tried again (StopReturnRetry),
	// and stopRefused the refusal last said for it, said once while its text stands.
	stopRetry   map[string]time.Time
	stopRefused map[string]string
	// handedBack is each card the stop handed back and the generation it was handed back
	// at: a queue read before the store moved it still lists it working at that
	// generation, and it is never recovered at it (the store deals it again at the next).
	handedBack map[string]int
	// returnedAt is when this reader returned a read with no verdict, by card id: a read
	// asked of it again is not begun before ReadStageRetry has passed, so a reader that
	// cannot launch does not take and return the same read every pass
	returnedAt map[string]time.Time
	// stageRetried is the reads run again once after a stage failure, by card id: a second
	// stage failure of the card is returned, whichever path launches it
	stageRetried map[string]bool
	epoch        uint64
	width        int  // the width this tick runs to: the override, else the fleet row's
	saidNoWidth  bool // the NOTE that no row names a width has been said
	drain        bool
	noRow        bool   // a reader whose queue said it is no row of the readers table, said once
	beaten       uint64 // the Meter's samples the last written beat has carried
	noRoom       bool   // Room said no on the last tick it was asked
	// usageStopped is when a launch of this member last ended because its usage source
	// stopped answering (EndUnverifiable); zero when none has, or the rest after it ended
	usageStopped time.Time
	// spent is where the last pass's time went, by part (PassTimes)
	spent PassTimes
	// have is the --have of the cards the last pass ended holding (haveWords), for the
	// reader's beat, which asks its queue on its own clock and reads none of the answer
	have atomic.Pointer[string]

	// the beat's own clock (BeatLoop): beatMu guards beaten; progress is when the work pass
	// last advanced, in unix nanoseconds
	beatMu   sync.Mutex
	progress atomic.Int64
	// longSince is when the oldest long work still in flight began, in unix nanoseconds
	// (0: none), set by the pass (longWork) and read by the beat (Stalled); longs counts
	// the long work in flight, for a bounded run's end (WaitLong)
	longSince atomic.Int64
	longs     sync.WaitGroup

	// lastStart is when this member last started a harness, for StartGap.
	lastStart time.Time
	passNow   time.Time // the pass's own time (Tick's), for what a start records

	// The pass has no long step: what is long in a launch's life (its start, staggered;
	// reading its result; pushing its commit) is run apart from the pass, by long, and
	// posts what it found here, by card, for a later pass to collect. postMu guards posted,
	// the findings by card id; startMu puts the starts one after another, StartGap apart;
	// pushGate bounds the pushes running at once (pushWidth); wake holds one word for the
	// loop: something was posted or a child exited, so the next pass is due now (Wake).
	postMu   sync.Mutex
	posted   map[string]post
	startMu  sync.Mutex
	pushGate chan struct{}
	wake     chan struct{}
}

// post is what a launch's long work found: its start (the child, or why there is none),
// or its end (how the child ended and, for a work card, its push).
type post struct {
	note     string // a line for the log: a script read that gave no verdict, its model read begun
	child    Child
	startErr error
	res      *Result
	push     *Push
	decided  *decided
}

// decided is a take's attempt decision as the finish carries it (Config.Attempt): its card
// line and its record line, or why none was made.
type decided struct {
	line     string
	decision []byte
	err      error
}

// New is a member with nothing running. A reader pushes nothing, and its
// pusher may be nil; a work member's pusher pushes every work card's commit.
func New(cfg Config, s Sprint, r Runner, pu Pusher, out io.Writer) *Member {
	m := &Member{cfg: cfg, sprint: s, runner: r, pusher: pu, out: out, running: map[string]launch{}, stageRetried: map[string]bool{}, returnedAt: map[string]time.Time{}, width: cfg.Width,
		posted: map[string]post{}, pushGate: make(chan struct{}, pushWidth), wake: make(chan struct{}, 1), stopRetry: map[string]time.Time{}, stopRefused: map[string]string{}, handedBack: map[string]int{}}
	m.advanced() // a member begins with its pass going on
	return m
}

// Drain stops the member taking new cards: from the next tick it beats, reads
// its queue and reports every child as it ends, and starts nothing (not a taken
// card, not a recovered one). A member whose binary was replaced drains, and
// stops when Running is 0.
func (m *Member) Drain() { m.drain = true }

// DrainMost is the longest a draining member waits for its children: two minutes.
// A member that drains does no new work, so a restart that waits longer costs the
// machine's whole width, while a card killed at the bound is redealt at the cost of
// one card; past the bound the member reports what finished and stops, and the
// cards still running are left for the server to redeal as for any member that
// stops. The stop timeout of the loop units (fleet/templates: TimeoutStopSec,
// ExitTimeOut) is DrainMost and a minute, so a member always stops by itself
// before its supervisor kills what is left.
const DrainMost = 2 * time.Minute

// DrainBound is how long a member stopped by its supervisor (SIGTERM) waits for the children
// it runs: the longest deadline they run to, and LongStall for the push and the report after
// it, at most DrainMost (nova-tools#5096 item 26).
func DrainBound(longest time.Duration) time.Duration {
	return min(longest+LongStall, DrainMost)
}

// LongestDeadline is the longest deadline of the cards the member runs: each packet's
// route deadline, or override (the member's own --deadline) for one that names none, and
// override whenever it is longer (a reader given --deadline runs every read to it).
func (m *Member) LongestDeadline(override time.Duration) time.Duration {
	longest := time.Duration(0)
	for _, l := range m.running {
		if l.spent {
			continue
		}
		longest = max(longest, time.Duration(l.packet.Deadline)*time.Second, override)
	}
	return longest
}

// Running is how many lanes the member holds: one for every launch from its start until
// its card is reported (a spent launch holds none). A child that has exited holds its lane
// until the report, so the cards a member has working never pass its width; the sprint's
// take holds the same bound (internal/sprint, takeOne).
func (m *Member) Running() int {
	n := 0
	for _, l := range m.running {
		if !l.spent {
			n++
		}
	}
	return n
}

// halves is the half slots the member's launches hold: a work card two, a read card one
// (a read costs half a slot of the one width, docs/SPEC-SPRINT.md section 6, "A read is a
// consumer card"). A reader's launches are all reads, each a whole lane of its own loop.
func (m *Member) halves() int {
	n := 0
	for _, l := range m.running {
		switch {
		case l.spent:
		case l.packet.Kind == "read" && !m.cfg.Reader:
			n++
		default:
			n += 2
		}
	}
	return n
}

// full says the packet would pass the member's width: a reader's lanes are whole, a
// member's are counted in half slots (halves), a read card taking one and a work card two.
func (m *Member) full(p Packet) bool {
	if m.cfg.Reader {
		return m.Running() >= m.width
	}
	cost := 2
	if p.Kind == "read" {
		cost = 1
	}
	return m.halves()+cost > 2*m.width
}

// reads says the launch is a read: a reader's every launch, and a read card a member was
// dealt on its fleet row. A read pushes nothing and is reported with the read verb.
func (m *Member) reads(l launch) bool { return m.cfg.Reader || l.packet.Kind == "read" }

// Wake is the word that the next pass is due before the loop's interval: a push ended, or
// a child exited (a Background member). It holds at most one word.
func (m *Member) Wake() <-chan struct{} { return m.wake }

// woken leaves the word for the loop, once.
func (m *Member) woken() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// THE BEAT GOES ON ITS OWN CLOCK. The work pass is serial: the queue, then for each ended
// card a push to the forge and a finish, then the take and the starts; from a machine
// 100 ms from the store a verb takes seconds, and sixteen lanes ending together hold one
// pass longer than MissedBeatsDown beat windows (docs/SPEC-SPRINT.md section 5), so a
// machine alive, pushing and finishing would be marked down. A machine that is busy is
// never down, so the beat is not the pass's: BeatLoop sends it every interval, apart from
// the pass, for as long as the pass is going on. A pass that has not advanced for BeatStall
// (a verb or a push that never returns) stops the beat, so a hung member still goes down
// and its cards are dealt elsewhere.

// BeatStall is how long the work pass may go without advancing before the member stops
// beating. A pass advances at every sprint verb it gets an answer to, every push that ends
// and every pass it begins; each verb is bounded (two minutes, the member's own budget), so
// five minutes without one is a pass that is stuck, not slow.
const BeatStall = 5 * time.Minute

// LongStall is how long a launch's long work (its start, its result read, its push) may be
// in flight before the member stops beating. It is above the sum of that work's own budgets
// (a fetch and a push of up to five minutes each, the push tried again, a pull request of a
// minute) and above a start's wait behind a width of others, so work that is slow and
// bounded never stops the beat; only work that outlives every budget does.
const LongStall = 30 * time.Minute

// PassTimes is where one pass's time went: reading the queue, pushing the ended
// children's commits, reporting them, and filling the lanes (the take and the starts,
// their stagger with them). A pass is the member's one line of control, so a child that
// exits while it runs waits for what is left of it: these four say what it waits on.
type PassTimes struct {
	Queue, Push, Report, Fill time.Duration
}

// LastPass is where the last pass's time went.
func (m *Member) LastPass() PassTimes { return m.spent }

// run is one sprint verb of the work pass: its answer is the pass advancing.
func (m *Member) run(args ...string) (int, []byte) {
	code, out := m.sprint.Run(args...)
	m.advanced()
	return code, out
}

// advanced records that the work pass went on, now.
func (m *Member) advanced() { m.progress.Store(m.clock().UnixNano()) }

func (m *Member) clock() time.Time {
	if m.cfg.Now != nil {
		return m.cfg.Now()
	}
	return time.Now()
}

// Stalled is how long the work pass has gone without advancing, when that is past
// BeatStall; 0 while it is going on.
func (m *Member) Stalled() time.Duration {
	now := m.clock()
	since := now.Sub(time.Unix(0, m.progress.Load()))
	// long work a launch began and has not posted (a push or a start that never
	// returns) stalls the member as a verb that never returns does: the pass itself
	// goes on round it, so its own advance is no evidence the work is moving
	if at := m.longSince.Load(); at != 0 {
		// only past LongStall: a push inside its own budgets, or a start waiting its
		// turn behind others, is slow and not stuck, and must not down a healthy machine
		if d := now.Sub(time.Unix(0, at)); d > LongStall {
			since = max(since, d)
		}
	}
	if since <= BeatStall {
		return 0
	}
	return since
}

// Beat is one beat of this member's presence, apart from the work pass. A member's is
// `fleet beat <member>`, naming the highest one-second load sample since the last beat
// written, so the ten-second highest nova-sprint keeps over its beats (docs/SPEC-SPRINT.md,
// the fleet) is the highest of the last ten seconds; with no sample it names none and
// nova-sprint fleet beat measures the machine itself. A reader's is its queue, the verb that
// is a reader's beat (docs/SPEC-SPRINT.md, the readers), its answer not read. A beat that
// fails is the error: the next goes at the next interval.
func (m *Member) Beat() error {
	m.beatMu.Lock()
	defer m.beatMu.Unlock()
	// a reader's beat wants no packet: none for a card it may start, none for one it runs
	args := []string{"queue", "--as", m.cfg.As, "--json", "--packets", "0"}
	if have := m.have.Load(); have != nil && *have != "" {
		args = append(args, "--have", *have)
	}
	var total uint64
	if !m.cfg.Reader {
		args = []string{"fleet", "beat", m.cfg.As, "--stop-returns", strconv.Itoa(m.OwedStopReturns())}
		if m.cfg.Meter != nil {
			var pct float64
			var ok bool
			if pct, total, ok = m.cfg.Meter.Peak(m.beaten); ok {
				args = append(args, "--load", strconv.FormatFloat(pct, 'f', 1, 64))
			}
		}
	}
	run := m.sprint.Run
	if m.cfg.Reader {
		run = func(args ...string) (int, []byte) { return queueOf(m.sprint.Run, args) }
	}
	code, out := run(args...)
	if code == 0 && !m.cfg.Reader && m.cfg.Meter != nil {
		m.beaten = total
	}
	if code != 0 {
		return fmt.Errorf("beat: exit %d: %s", code, strings.TrimSpace(string(out)))
	}
	return nil
}

// BeatLoop beats at every tick of every until ctx ends, while the work pass is going on:
// once it has gone BeatStall without advancing, the beat stops, said once, and starts again,
// said once, when the pass goes on. A beat that fails is said and the next is sent at the
// next tick.
func (m *Member) BeatLoop(ctx context.Context, every <-chan time.Time, out io.Writer) {
	stopped := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-every:
		}
		if d := m.Stalled(); d > 0 {
			if !stopped {
				stopped = true
				fmt.Fprintf(out, "MEMBER BEAT STOPPED: the work pass has not advanced for %s (the bound is %s); the sprint marks this member down and deals its cards elsewhere\n", d.Round(time.Second), BeatStall)
			}
			continue
		}
		if stopped {
			stopped = false
			fmt.Fprintf(out, "NOTE beat resumed: the work pass advanced\n")
		}
		if err := m.Beat(); err != nil {
			fmt.Fprintf(out, "NOTE %s\n", err)
		}
	}
}

// Tick is one pass of the loop: collect and push what ended, read the queue,
// report every child that ended, recover working cards, and take up to the width.
// When the queue read fails or times out, local completed children are still
// collected, pushed, reported, and cleaned up so an injected failing queue cannot
// starve an already-finished child, while new takes are suppressed (docs/SPEC-SWARM.md, member;
// docs/SPEC-SPRINT.md, the fleet).
func (m *Member) Tick(now time.Time) (acted int, err error) {
	m.advanced()
	m.passNow = now
	m.spent = PassTimes{}
	lap := m.clock()
	// since is the time since the last lap, which it begins anew
	since := func() time.Duration {
		was := lap
		lap = m.clock()
		return lap.Sub(was)
	}
	// unanswered collects the verbs the store did not answer in this pass
	var unanswered []string
	noAnswer := func(what string, out []byte) {
		unanswered = append(unanswered, what)
		fmt.Fprintf(m.out, "NOTE %s: the store did not answer; the pass goes on, and it is tried again next pass: %s\n", what, oneLine(strings.TrimSpace(string(out))))
	}
	defer m.haveWords() // what the pass ended holding, for the beat
	defer func() {
		if err == nil && len(unanswered) > 0 {
			err = fmt.Errorf("the store did not answer %d of this pass's verbs (%s); each is tried again next pass", len(unanswered), strings.Join(unanswered, ", "))
		}
	}()

	wasOurs := map[string]bool{}
	for id := range m.running {
		wasOurs[id] = true
	}
	claimMoved := map[string]bool{}
	acted += m.collect()
	m.endEndedLocal()
	acted += m.collect()
	m.spent.Push = since()

	reportOnQueueFailure := func() int {
		reports := 0
		for _, id := range slices.Sorted(maps.Keys(m.running)) {
			l, ours := m.running[id]
			if !ours || l.busy || l.spent || l.stopped {
				continue // a stopped launch is the stop's: handed back, never reported (machineStop)
			}
			if l.dropped && (l.res != nil || (l.child != nil && l.child.Done())) {
				m.forget(id, false)
				fmt.Fprintf(m.out, "%s %s: no longer in the queue (dropped or returned)\n", FinishReaped, id)
				continue
			}
			if l.claimMoved && (l.res != nil || (l.child != nil && l.child.Done())) {
				m.forget(id, false)
				fmt.Fprintf(m.out, "%s %s: the claim moved\n", FinishReaped, id)
				continue
			}
			if l.res == nil {
				continue
			}
			if m.reportOne(id, l, now, nil, noAnswer) {
				reports++
			}
		}
		m.spent.Report += since()
		return reports
	}

	// the beat is not this pass's: it goes on its own clock (BeatLoop), so a pass held for
	// minutes by its pushes and finishes never lets the machine go down while it works
	code, out := queueOf(m.run, m.queueArgs())
	m.spent.Queue = since()
	if code != 0 {
		acted += m.collect()
		acted += reportOnQueueFailure()
		return acted, fmt.Errorf("queue: exit %d: %s", code, strings.TrimSpace(string(out)))
	}
	var q queueOut
	if err := json.Unmarshal(out, &q); err != nil {
		acted += m.collect()
		acted += reportOnQueueFailure()
		return acted, fmt.Errorf("queue: not JSON: %w", err)
	}
	m.epoch = q.Epoch
	if e, ok := m.runner.(Epocher); ok {
		e.Epoch(q.Epoch)
	}
	if m.cfg.Reader && q.Reader != nil && *q.Reader == m.noRow {
		// the readers table is the coordinator's (init --readers, reader add): a reader
		// with no row is never asked, so the loop says so once, and once when it came
		m.noRow = !*q.Reader
		if m.noRow {
			fmt.Fprintf(m.out, "MEMBER NOT A READER %s: no row of the readers table, so its queue is no beat and it is asked nothing; the coordinator declares it: nova-sprint reader add %s\n", oneLine(m.cfg.As), oneLine(m.cfg.As))
		} else {
			fmt.Fprintf(m.out, "NOTE reader %s: the readers table has its row; it beats and is asked reads\n", oneLine(m.cfg.As))
		}
	}
	if m.cfg.Width == 0 && q.Width != m.width {
		// the fleet row changed (fleet up --width, fleet sync): said once, run from now
		fmt.Fprintf(m.out, "width %d -> %d (the fleet row)\n", m.width, q.Width)
		m.width = q.Width
	}
	if m.cfg.Width == 0 && m.width == 0 && !m.saidNoWidth {
		// no row names a width: a member before its fleet row, a reader named for no
		// machine; it takes nothing until one does, said once
		m.saidNoWidth = true
		fmt.Fprintf(m.out, "NOTE width 0: no fleet row names this worker's width, so it takes nothing; a member runs at its own fleet row's, a reader named reader-<m> runs at machine m's (nova-config machine set <m> --width <n>, then nova-sprint fleet sync)\n")
	}
	// The machine's stop (docs/SPEC-SPRINT.md section 14): every lane is cancelled and its
	// card handed back with stop-return once the child has ended; nothing is reported,
	// recovered or taken while it stands. The word is read every pass, so a stop between two
	// beats still stops this pass's starts (NoLaunchAfterStop).
	held := []string{"--epoch", strconv.FormatUint(q.Epoch, 10)}
	ids := make([]string, 0, len(q.Cards))
	byID := map[string]queueCard{}
	for _, c := range q.Cards {
		ids = append(ids, c.ID)
		byID[c.ID] = c
	}
	sort.Strings(ids)
	if acted += m.machineStop(q, byID, now); m.stopped {
		m.spent.Fill = since()
		return acted, nil
	}

	// 1. Report every child that ended, one verb per card (each report is its
	// own words).
	acted += m.collect()
	m.endEnded(ids, byID)
	acted += m.collect() // a member that is not Background did its ends just now
	m.spent.Push += since()
	for _, id := range ids {
		c := byID[id]
		l, ours := m.running[id]
		if !ours || l.busy || l.stopped {
			// long work of the launch is in flight: it is left alone until that posts; a
			// stopped launch is the stop's: handed back, never reported (machineStop), its
			// end collected before the word was read notwithstanding
			continue
		}
		if m.moved(l, c) {
			// the claim moved under the child (a clear, a redeal): its result
			// is nobody's; it is reaped when it ends and the new claim is run.
			// Whatever the card's column: a redeal or a withdrawn card dealt
			// again lists it in this member's ready (asked) column, and an
			// ended launch kept there holds the width with nothing reported
			// (tla/CardContract.tla, Reap and EveryLaunchEnds)
			if !l.child.Done() {
				l.claimMoved = true
				m.running[id] = l
				continue
			}
			epoch, gen, attempt := m.claim(c)
			fmt.Fprintf(m.out, "%s %s: the claim moved (epoch %d gen %d attempt %d, now epoch %d gen %d attempt %d)\n", FinishReaped, id, l.epoch, l.gen, l.attempt, epoch, gen, attempt)
			m.forget(id, false) // reaped: the result is nobody's
			claimMoved[id] = true
			continue
		}
		if c.Col != "working" && c.Col != "reading" {
			continue
		}
		if l.spent || l.res == nil {
			// a child that has not ended, or whose end (its result read, a work
			// card's commit pushed) has not been collected yet
			continue
		}
		if m.reportOne(id, l, now, c.Packet, noAnswer) {
			acted++
		}
	}
	// A child whose card the queue no longer lists (the sprint was cleared, the
	// card was dealt elsewhere) has nothing left to report to: once it has
	// ended it is forgotten, so it does not hold a place of the width for ever.
	for id, l := range m.running {
		if _, listed := byID[id]; !listed && !l.busy {
			if l.child.Done() {
				m.forget(id, false)
				fmt.Fprintf(m.out, "%s %s: no longer in the queue (dropped or returned)\n", FinishReaped, id)
			} else {
				l.dropped = true
				m.running[id] = l
			}
		}
	}
	if out := m.stampProgress(now); out != nil {
		noAnswer("progress", out)
	}
	m.spent.Report += since()
	if m.drain {
		return acted, nil
	}
	// every card this tick would start is started, or, when Config.Room says no, finished as
	// refused at staging with its reason, so the sprint deals it to another member and says why
	// (refuseStaging); asked after the reports, so a launch whose end rested the member
	// (room) rests it in this pass
	launch := m.start
	if ok, why := m.room(now); !ok {
		launch = func(p Packet) bool {
			returned := m.refuseStaging(p, why)
			if returned && p.Kind == "read" {
				// a read handed back under the floor is a return like any other: the
				// sprint asks it again in place at most twice, then judges it
				// (internal/sprint MaxReadReasks; tla/DirtyTick.tla, ReasksBounded),
				// and this reader waits ReadStageRetry before it begins it again
				m.returnedAt[p.Card] = now
			}
			return returned
		}
	}
	// 4. Recover in-flight (working/reading) cards that have no child of ours,
	// clamped to width. A card in the queue as working (reading) with no child
	// of ours is a card from before this process started (or one whose moved
	// claim just reaped): it is run again from its packet, at the same
	// generation, so a member restart loses nothing. Clamped to width so
	// recovery never overflows capacity; excess cards remain in the queue for
	// subsequent passes.
	acted += m.recoverWorking(ids, byID, wasOurs, claimMoved, launch)
	// 5. Take (begin) for the lanes the reports freed.
	n, out := m.take(q, held, now, launch)
	if out != nil {
		noAnswer(m.takeVerb(), out)
	}
	m.spent.Fill = since()
	return acted + n, nil
}

// reportOne reports one ended launch whose result is ready. It returns true if the card
// was acted on and forgotten, or false if retained for retry or left in flight.
func (m *Member) reportOne(id string, l launch, now time.Time, qPacket *Packet, noAnswer func(string, []byte)) bool {
	r := *l.res
	if r.End == EndUnverifiable {
		m.usageStopped = now // the member's rest (UsageRest), from the last such end
	}
	launched := []string{"--epoch", strconv.FormatUint(l.epoch, 10)}
	var args []string
	ok := r.OK // as reported: a work card whose push was refused is reported failed
	if m.reads(l) {
		if r.End == EndStaging && !l.retried && !m.drain {
			// (a draining reader starts nothing: its stage failure is handed back below)
			// RULE (docs/SPEC-SPRINT.md, the readers): a read's stage failure is never a
			// verdict. The stage is tried once more here after ReadStageRetry; a second
			// failure falls to the return below, which hands the read to another reader.
			if l.retryAt.IsZero() {
				l.retryAt = now.Add(ReadStageRetry)
				m.running[id] = l
				fmt.Fprintf(m.out, "read %s: stage failed (%s); no verdict recorded; run again in %s\n", id, oneLine(r.Staging), ReadStageRetry)
				return false
			}
			if now.Before(l.retryAt) {
				return false
			}
			if qPacket == nil {
				return false // suppress new starts from an unknown queue state
			}
			// the retry is owed once per card: it is remembered across a start that fails and
			// across a launch the recovery path makes (start gives it to the launch)
			delete(m.running, id)
			m.stageRetried[id] = true
			p := l.packet
			if qPacket != nil {
				p = *qPacket
			}
			return m.start(p)
		}
		finding := oneLine(r.Report)
		if r.Verdict == "broken" {
			finding = findingOf(r)
		}
		// a broken verdict whose finding names no file, line or rule is no verdict
		// (docs/SPEC-CARD-CONTRACT.md section 3): handed back as "no finding", so the
		// sprint asks another reader and the coordinator never judges on nothing
		noFinding := r.Ran && r.Verdict == "broken" && !typedrec.NamesADefect(finding)
		if !r.Ran || (r.Verdict != "ok" && r.Verdict != "broken") || noFinding {
			// no verdict is no finding, and not a read: the read is
			// returned, and the sprint's next tick asks it of another
			// reader free at the attempt, or of this reader again, which
			// does not begin it before ReadStageRetry (docs/SPEC-SPRINT.md
			// section 6; tla/DirtyTick.tla, ReadReturn,
			// JudgedOnlyAfterTheBound). A return refused leaves the
			// launch spent: the read stays for the lateness rule.
			why := oneLine(r.Report)
			switch {
			case r.End == EndStaging:
				why = EndStaging + ": " + oneLine(r.Staging) // the stage's reason, to the inbox
			case r.End == EndProvider && r.Provider != "":
				why = EndProvider + ": " + oneLine(r.Provider) // the provider's cause, as Judge names it
			}
			reason := cut(fmt.Sprintf("no verdict (ran=%t verdict=%q): %s", r.Ran, r.Verdict, why))
			if noFinding {
				reason = cut(NoFinding + ": the broken read names no file, line or rule: " + finding)
			}
			args = append(append([]string{"read", "--as", m.cfg.As, "--return", id, "--reason", reason}, usageArgs(r)...), launched...)
			code, out := m.run(args...)
			fmt.Fprintf(m.out, "read %s: returned exit=%d: %s\n", id, code, reason)
			if code == 2 {
				noAnswer("read --return "+id, out)
				return false
			}
			if code != 0 {
				l.spent = true
				m.running[id] = l
				return false
			}
			m.forget(id, true) // returned with no verdict
			m.returnedAt[id] = now
			return true
		}
		word := "--ok"
		if r.Verdict == "broken" {
			word = "--broken"
		}
		args = append(append([]string{"read", "--as", m.cfg.As, word, id, "--finding", finding}, usageArgs(r)...), launched...)
	} else {
		pu := *l.push
		fin, why, report := finishReport(r, pu, l.branch)
		switch {
		case pu.Sha != "":
			fmt.Fprintf(m.out, "push %s pushed=%s branch=%s\n", id, pu.Sha, l.branch)
			if pu.PRNote != "" {
				fmt.Fprintf(m.out, "NOTE pr %s not opened: %s\n", id, oneLine(pu.PRNote))
			}
		case pu.Refused != "":
			fmt.Fprintf(m.out, "NOTE push %s refused: %s\n", id, pu.Refused)
		default:
			fmt.Fprintf(m.out, "push %s: not pushed: %s\n", id, pu.None)
		}
		if fin != FinishOK {
			fmt.Fprintf(m.out, "NOTE finish %s failed: %s\n", id, why)
		}
		args = []string{"finish", "--as", m.cfg.As, id + "@" + strconv.Itoa(l.gen), "--report", report}
		// the head and the branch are the push's: a finish names only what origin holds
		if pu.Sha != "" {
			args = append(args, "--head", pu.Sha)
			if l.branch != "" {
				args = append(args, "--branch", l.branch)
			}
		}
		if fin != FinishOK {
			args = append(args, "--failed")
		}
		args = append(args, usageArgs(r)...)
		// the take's attempt decision, when one was asked (attempt): the finish carries it
		if d := l.decided; d != nil && d.err != nil {
			fmt.Fprintf(m.out, "NOTE attempt %s not decided: %s; the reason line routes the finish\n", id, oneLine(d.err.Error()))
		} else if d != nil {
			fmt.Fprintf(m.out, "decide %s attempt %s\n", id, d.line)
			args = append(args, "--decision", string(d.decision))
		}
		ok = fin == FinishOK
		args = append(args, launched...)
	}
	code, out := m.run(args...)
	fmt.Fprintf(m.out, "%s %s ok=%t exit=%d%s\n", args[0], id, ok, code, routeWords(l.packet))
	if code == 2 {
		noAnswer(args[0]+" "+id, out)
		return false
	}
	m.forget(id, !m.reads(l) && !ok) // refused (1) too: the card is no longer ours to report
	return true
}

// takeVerb is the verb a take is: a reader's is read --begin.
func (m *Member) takeVerb() string {
	if m.cfg.Reader {
		return "read --begin"
	}
	return "take"
}

// take takes (begins) a card for every free lane in one verb and starts each:
// a lane is free when no launch holds it (Running), and a launch holds its lane
// until its card is reported. The cards counted are the queue's ready (asked) cards this member
// runs no child for: a card taken earlier in the pass is listed ready still, and
// is neither counted nor begun again. It returns the starts, and, when the store
// did not answer the verb, what the verb printed (nil otherwise): nothing was
// taken, and the pass goes on.
func (m *Member) take(q queueOut, held []string, now time.Time, launch func(Packet) bool) (acted int, unanswered []byte) {
	room := m.width - m.Running()
	if !m.cfg.Reader {
		// in half slots: a read card holds one, a work card two; the sprint's take cuts the
		// cards it hands to the room they hold (internal/sprint, takeOne), and a server from
		// before read cards cuts them to its width, never past it
		room = 2*m.width - m.halves()
		reads := slices.ContainsFunc(q.Cards, func(c queueCard) bool {
			_, ours := m.running[c.ID]
			return !ours && c.Col == "ready" && IsReadCardID(c.ID)
		})
		if !reads {
			room /= 2 // work cards alone: whole slots
		}
	}
	if room <= 0 {
		return 0, nil
	}
	ready := 0
	for _, c := range q.Cards {
		if _, ours := m.running[c.ID]; !ours && (c.Col == "ready" || c.Col == "asked") {
			ready++
		}
	}
	if ready == 0 {
		return 0, nil
	}
	var packets []Packet
	if m.cfg.Reader {
		// the reads begun are named: the first n asked in queue order, so what
		// is started is exactly what was claimed
		var ids []string
		for _, c := range q.Cards {
			if _, ours := m.running[c.ID]; ours {
				continue // begun earlier in this pass
			}
			if at, ok := m.returnedAt[c.ID]; ok && now.Before(at.Add(ReadStageRetry)) {
				continue // returned by this reader a moment ago: asked of it again later
			}
			if c.Col == "asked" && c.Packet != nil && len(ids) < room {
				ids = append(ids, c.ID)
				packets = append(packets, *c.Packet)
			}
		}
		for id := range m.returnedAt {
			if _, listed := q.card(id); !listed || slices.Contains(ids, id) {
				delete(m.returnedAt, id)
			}
		}
		if len(ids) == 0 {
			return 0, nil
		}
		args := append(append([]string{"read", "--as", m.cfg.As, "--begin"}, ids...), held...)
		code, out := m.run(args...)
		if code == 2 {
			return 0, orWords(out)
		}
		if code != 0 {
			fmt.Fprintf(m.out, "read --begin refused: %s\n", strings.TrimSpace(string(out)))
			return 0, nil
		}
	} else {
		args := append([]string{"take", "--as", m.cfg.As, "--limit", strconv.Itoa(room), "--json"}, held...)
		code, out := m.run(args...)
		if code == 2 {
			return 0, orWords(out)
		}
		if code != 0 {
			fmt.Fprintf(m.out, "take refused: %s\n", strings.TrimSpace(string(out)))
			return 0, nil
		}
		var t takeOut
		if err := json.Unmarshal(out, &t); err != nil {
			// an answer that is not the take's JSON is no answer: nothing is started
			return 0, []byte(fmt.Sprintf("take: not JSON: %v", err))
		}
		packets = t.Packets
	}
	for _, p := range packets {
		if launch(p) {
			acted++
		}
	}
	return acted, nil
}

// orWords is what a verb printed, never nil: the mark of a verb the store did
// not answer (take).
func orWords(out []byte) []byte {
	if out == nil {
		return []byte{}
	}
	return out
}

// card is the queue's card of the id.
func (q queueOut) card(id string) (queueCard, bool) {
	for _, c := range q.Cards {
		if c.ID == id {
			return c, true
		}
	}
	return queueCard{}, false
}

// recoverWorking starts children for in-flight (working/reading) cards that
// have no child of ours, up to member width. Each card it leaves for want of
// room is said, one line, and stays in the queue for a later pass.
func (m *Member) recoverWorking(ids []string, byID map[string]queueCard, wasOurs, claimMoved map[string]bool, launch func(Packet) bool) int {
	acted := 0
	for _, id := range ids {
		c := byID[id]
		inFlight := c.Col == "working" || c.Col == "reading"
		if !inFlight {
			continue
		}
		if _, ours := m.running[id]; ours {
			continue
		}
		if wasOurs[id] && !claimMoved[id] {
			continue
		}
		if g, handed := m.handedBack[id]; handed {
			if _, gen, _ := m.claim(c); gen == g {
				continue // handed back by the stop at this generation: the store deals it again at the next
			}
			delete(m.handedBack, id)
		}
		if c.Packet == nil {
			continue
		}
		if m.full(*c.Packet) {
			fmt.Fprintf(m.out, "recover %s deferred: width %d full\n", id, m.width)
			continue
		}
		if launch(*c.Packet) {
			acted++
		}
	}
	return acted
}

// start runs a packet as a child, unless one is already running for it or width is full.
func (m *Member) start(p Packet) bool {
	p = Carried(p)
	if _, ok := m.running[p.Card]; ok {
		fmt.Fprintf(m.out, "start %s: already running\n", p.Card)
		return false
	}
	if m.full(p) {
		fmt.Fprintf(m.out, "start %s: width %d full\n", p.Card, m.width)
		return false
	}
	// the launch holds its lane from here; its start is long (a stagger, a slot made, a
	// process begun) and is done apart from the pass, one start after another
	m.running[p.Card] = launch{busy: true, busyAt: m.clock(), gen: p.Gen, attempt: p.Attempt, epoch: p.Epoch, branch: p.Branch, packet: p, retried: m.stageRetried[p.Card]}
	m.long(func() {
		m.startMu.Lock()
		m.staggerStart()
		ch, note, err := m.scriptOrStart(p)
		m.startMu.Unlock()
		m.post(p.Card, post{child: ch, note: note, startErr: err})
	})
	m.longWork()
	if m.cfg.Background {
		return true
	}
	m.collect()
	_, started := m.running[p.Card]
	return started
}

// long does a launch's long work: apart from the pass when the member is Background, where
// the pass asks for it when it is not (a test's member: a pass is one step).
func (m *Member) long(work func()) {
	if m.cfg.Background {
		m.longs.Go(work)
		return
	}
	work()
}

// longWork records when the oldest long work still in flight began, for the beat: a push
// or a start that never returns stalls the member (Stalled) though the pass goes on.
func (m *Member) longWork() {
	var oldest int64
	for _, l := range m.running {
		if at := l.busyAt.UnixNano(); l.busy && (oldest == 0 || at < oldest) {
			oldest = at
		}
	}
	m.longSince.Store(oldest)
}

// WaitLong waits for the long work in flight to end: a member run for a bounded number of
// passes does not leave a start half made or a push cut off when its process exits.
func (m *Member) WaitLong() { m.longs.Wait() }

// post leaves what a launch's long work found for the next pass, and wakes the loop.
func (m *Member) post(card string, p post) {
	m.advanced() // long work that ended is the member going on
	m.postMu.Lock()
	m.posted[card] = p
	m.postMu.Unlock()
	m.woken()
}

// collect gives every launch what its long work posted, and returns how many launches it
// ended for a start that failed (each reported at once: a work card finished failed, a read
// returned). A launch whose start ended has its child from here; one whose end was read has
// its result and its push, and the pass reports it.
func (m *Member) collect() (acted int) {
	m.postMu.Lock()
	posted := m.posted
	m.posted = map[string]post{}
	m.postMu.Unlock()
	for _, card := range slices.Sorted(maps.Keys(posted)) {
		po := posted[card]
		l, ours := m.running[card]
		if !ours || !l.busy {
			continue // unreachable while a busy launch is left alone; nothing to give it to
		}
		l.busy = false
		if po.note != "" {
			fmt.Fprintf(m.out, "read %s: %s\n", card, po.note)
		}
		switch {
		case po.startErr != nil:
			p := l.packet
			delete(m.running, card)
			fmt.Fprintf(m.out, "start %s: %v\n", card, po.startErr)
			if p.Kind != "read" {
				m.failLaunch(p, po.startErr)
			} else {
				m.returnUnstarted(p, po.startErr)
			}
			acted++
			continue
		case po.child != nil:
			l.child = po.child
			if w, ok := po.child.(Waiter); ok && m.cfg.Background {
				go func() { <-w.Wait(); m.woken() }()
			}
			m.running[card] = l
			fmt.Fprintf(m.out, "start %s attempt=%d gen=%d running=%d/%d%s\n", card, l.attempt, l.gen, m.Running(), m.width, routeWords(l.packet))
		default:
			l.res, l.push, l.decided = po.res, po.push, po.decided
			m.running[card] = l
		}
	}
	m.longWork()
	return acted
}

// forget drops a launch and what is remembered of its card, and tells a runner that is an
// Ender the launch is done with (failed: a person may want to inspect it).
func (m *Member) forget(id string, failed bool) {
	if l, ok := m.running[id]; ok {
		if e, ok := m.runner.(Ender); ok {
			e.Ended(l.packet, failed)
		}
	}
	delete(m.running, id)
	delete(m.stageRetried, id)
}

// The machine's stop cancels jobs (docs/SPEC-SPRINT.md section 14; the owner, 2026-10-08:
// "official machine stop must cancel every active fleet or friend sprint job, preserve progress,
// return work AND reads to their same owner ready pool automatically"). The queue's answer
// carries the machine's word (queueOut.Machine), read every pass, before any start: STOPPED
// tells every child to stop (Stopper, its working tree and log kept), and once a stopped
// child has ended its card is handed back to this worker's own row with
//
//	nova-sprint stop-return --as <row> <card>@<gen> --epoch <epoch> --reason "owned process stopped"
//
// (the store's verb: it returns the same card to ready, a read to asked, at a new generation,
// idempotent on replay, and refuses a worker that is not the owner or names a stale
// generation). A refusal is said and tried again next pass while the machine stays STOPPED.
// Nothing is taken, started, recovered or finished while STOPPED: a card cancelled by the
// stop is never finished (NoLaunchAfterStop, EveryLaneReturnsOnStop; tla/StopCancels.tla).
// RUNNING again takes the queue's cards as they are, at the generation the queue carries.

// StopReturnReason is the reason every stop-return names.
const StopReturnReason = "owned process stopped"

// StopReturnRetry is how long a refused stop-return waits before it is sent again: the
// store's verb may be absent or the machine already RUNNING, and a verb a pass is a flood.
const StopReturnRetry = time.Minute

// OwedStopReturns is how many of this member's launches the stop cancelled whose card is
// not yet handed back: the beat carries it (fleet beat --stop-returns), and start waits on
// zero. A card the queue holds working under this row with no child of ours is owed too,
// and returned at once (machineStop), so it is never counted here.
func (m *Member) OwedStopReturns() int {
	n := 0
	for _, l := range m.running {
		if l.stopped {
			n++
		}
	}
	return n
}

// machineStop is the pass's stop step. The cancel part runs while the word is STOPPED:
// every child is told to stop, and every queue card working or reading under this row
// with no child of ours (a member restarted mid-stop: its old children are gone, and the
// queue is the record on disk) is handed back at once. The hand-back part runs every pass
// whatever the word: a stopped launch whose child has ended is handed back with
// stop-return, tried again after StopReturnRetry when refused, its refusal said once while
// it stands; while RUNNING a stopped launch whose claim the queue no longer holds working
// (moved to a new generation, or gone) has nothing left to return and is reaped. It
// returns how many cards it handed back.
func (m *Member) machineStop(q queueOut, byID map[string]queueCard, now time.Time) (acted int) {
	switch {
	case q.Machine == "STOPPED":
		if !m.stopped {
			m.stopped = true
			fmt.Fprintf(m.out, "MEMBER STOP machine STOPPED: taking no card; %d running lane(s) are cancelled and handed back with stop-return\n", m.Running())
		}
	case q.Machine != "":
		if m.stopped {
			m.stopped = false
			fmt.Fprintf(m.out, "MEMBER START machine %s: taking cards again\n", q.Machine)
		}
	}
	if m.stopped {
		// the cancel part
		for _, id := range slices.Sorted(maps.Keys(m.running)) {
			l := m.running[id]
			if l.busy || l.child == nil || l.stopped {
				continue // its start is in flight: the next pass stops it
			}
			if s, ok := l.child.(Stopper); ok {
				l.stopPid = s.Stop()
			}
			l.stopped, l.stopAt = true, now
			m.running[id] = l
			fmt.Fprintf(m.out, "LANE CANCELLED BY STOP card=%s gen=%d epoch=%d pid=%d: the child was told to stop; its working tree and log are kept\n", id, l.gen, l.epoch, l.stopPid)
		}
		// a card working under this row with no child of ours: its run is gone with the
		// member that ran it (a restart mid-stop), and the queue is the record
		for _, id := range slices.Sorted(maps.Keys(byID)) {
			c := byID[id]
			if _, ours := m.running[id]; ours || (c.Col != "working" && c.Col != "reading") {
				continue
			}
			epoch, gen, _ := m.claim(c)
			if gen <= 0 {
				continue
			}
			if g, handed := m.handedBack[id]; handed && g == gen {
				continue
			}
			if m.stopReturn(id, gen, epoch, 0, now, "no child of this member runs it: the run is gone with the member that ran it") {
				acted++
			}
		}
	}
	// the hand-back part, every pass
	for _, id := range slices.Sorted(maps.Keys(m.running)) {
		l := m.running[id]
		if !l.stopped || l.busy || l.child == nil || !l.child.Done() {
			continue
		}
		if !m.stopped {
			if c, listed := byID[id]; !listed || (c.Col != "working" && c.Col != "reading") || m.moved(l, c) {
				// RUNNING again and the claim is not ours to return: the store moved it (a
				// new generation, a drop); the result is nobody's
				fmt.Fprintf(m.out, "%s %s: cancelled by stop, and the claim moved under it (gen %d epoch %d); nothing to return\n", FinishReaped, id, l.gen, l.epoch)
				m.forget(id, true)
				continue
			}
		}
		gen, epoch := l.gen, l.epoch
		if c, listed := byID[id]; listed {
			ep, g, _ := m.claim(c)
			if g > 0 {
				gen = g
			}
			if ep > 0 {
				epoch = ep
			}
		}
		if gen <= 0 {
			continue
		}
		if m.stopReturn(id, gen, epoch, l.stopPid, now, "") {
			m.forget(id, true) // kept for inspection: the tree holds whatever the child had done
			acted++
		}
	}
	return acted
}

// stopReturn sends one stop-return for the card at its generation and epoch, unless its
// last refusal is younger than StopReturnRetry. It says the OK, and a refusal once while
// its text stands, and returns whether the store took it.
func (m *Member) stopReturn(id string, gen int, epoch uint64, pid int, now time.Time, why string) bool {
	if gen <= 0 {
		return false
	}
	if at, ok := m.stopRetry[id]; ok && now.Before(at) {
		return false
	}
	args := []string{"stop-return", "--as", m.cfg.As, fmt.Sprintf("%s@%d", id, gen), "--epoch", strconv.FormatUint(epoch, 10), "--reason", StopReturnReason}
	code, out := m.run(args...)
	if code != 0 {
		m.stopRetry[id] = now.Add(StopReturnRetry)
		if text := oneLine(strings.TrimSpace(string(out))); m.stopRefused[id] != text {
			m.stopRefused[id] = text
			fmt.Fprintf(m.out, "STOP-RETURN refused card=%s gen=%d pid=%d exit=%d: %s; tried again in %s\n", id, gen, pid, code, text, StopReturnRetry)
		}
		return false
	}
	delete(m.stopRetry, id)
	delete(m.stopRefused, id)
	m.handedBack[id] = gen
	if why != "" {
		why = " (" + why + ")"
	}
	fmt.Fprintf(m.out, "STOP-RETURN OK card=%s gen=%d epoch=%d pid=%d: handed back to %s ready by the machine's stop%s\n", id, gen, epoch, pid, m.cfg.As, why)
	return true
}

// Waiter is a child that says when it has ended: a Background member's loop is woken then
// (Wake), so the child's push begins at once and not at the loop's next interval.
type Waiter interface {
	Wait() <-chan struct{}
}

// UsageRest is how long a member starts no card after a launch of its ended because its
// usage source stopped answering (EndUnverifiable: native's read of the harness's usage
// database, this machine's own): a launch started while it stays so would end the same way,
// and each such end is the member's failure, reported on the staging kind (Judge). A launch
// that ends so during the rest begins it again from its end; the rest is a clock's, since no
// launch runs to read the source again while it holds (the usage source outage of
// 2026-10-03, 11:46 PM, ended every child on the flash routes together, and each end was
// judged as its card's).
const UsageRest = 10 * time.Minute

// room is whether a child may be started this tick (Config.Room, then the usage source's
// rest) and why, saying so when the answer changes: the refusal once when it begins, and
// once when it ends.
func (m *Member) room(now time.Time) (bool, string) {
	ok, why := true, ""
	if m.cfg.Room != nil {
		ok, why = m.cfg.Room()
	}
	if ok && !m.usageStopped.IsZero() {
		since := m.usageStopped.UTC().Format(time.RFC3339)
		if until := m.usageStopped.Add(UsageRest); now.Before(until) {
			ok, why = false, "the usage source stopped answering since "+since+": no card is started until "+until.UTC().Format(time.RFC3339)
		} else {
			why = "the rest after the usage source stopped answering (" + since + ") ended"
			m.usageStopped = time.Time{}
		}
	}
	switch {
	case !ok && !m.noRoom:
		fmt.Fprintf(m.out, "take REFUSED: %s\n", why)
	case ok && m.noRoom:
		fmt.Fprintf(m.out, "NOTE take resumed: %s\n", why)
	}
	m.noRoom = !ok
	return ok, why
}

// refuseStaging ends a card this member will not start (Config.Room said no) on the staging
// refusal path, with the reason, so the sprint sees it at once: a work card is finished
// failed `staging refused: <why>`, which the sprint withdraws and deals to another member
// without spending its redeal bound and notes in the inbox (docs/SPEC-CARD-CONTRACT.md
// section 4; tla/CardContract.tla, StageRefused); a read is returned with the same reason,
// for another reader. It returns whether the sprint took the word.
func (m *Member) refuseStaging(p Packet, why string) bool {
	reason := cut(EndStaging + ": " + oneLine(why))
	epoch := strconv.FormatUint(p.Epoch, 10)
	args := []string{"finish", "--as", m.cfg.As, p.Card + "@" + strconv.Itoa(p.Gen), "--failed", "--report", reason, "--epoch", epoch}
	if p.Kind == "read" {
		args = []string{"read", "--as", m.cfg.As, "--return", p.Card, "--reason", reason, "--epoch", epoch}
	}
	code, out := m.run(args...)
	fmt.Fprintf(m.out, "%s %s ok=false exit=%d %s%s\n", args[0], p.Card, code, EndStaging, routeWords(p))
	if code != 0 {
		fmt.Fprintf(m.out, "NOTE %s %s refused: %s\n", args[0], p.Card, strings.TrimSpace(string(out)))
	}
	return code == 0
}

// failLaunch reports a taken work card this member cannot launch (no model, a
// slot it cannot make) as a failed finish with the reason, so the store sees it
// at once and opens the failed-work judgment; a card left working would be
// started again every tick, the refusal only in this log, until judged late.
func (m *Member) failLaunch(p Packet, why error) {
	args := []string{"finish", "--as", m.cfg.As, p.Card + "@" + strconv.Itoa(p.Gen), "--failed",
		"--report", cut(cardhdr.EndLaunch + ": " + oneLine(why.Error())), "--epoch", strconv.FormatUint(p.Epoch, 10)}
	code, out := m.run(args...)
	fmt.Fprintf(m.out, "finish %s ok=false exit=%d launch refused%s\n", p.Card, code, routeWords(p))
	if code != 0 {
		fmt.Fprintf(m.out, "NOTE finish %s refused: %s\n", p.Card, strings.TrimSpace(string(out)))
	}
}

// returnUnstarted hands back a read this reader cannot start (no route, a slot it cannot
// make) with the reason, so the sprint asks it of another reader at once, or of this one
// again at most MaxReadReasks times and then judges it; this reader does not begin it again
// before ReadStageRetry. A read left reading with no child would be started again every
// pass, the refusal only in this log, for the read's whole deadline.
func (m *Member) returnUnstarted(p Packet, why error) {
	reason := cut(cardhdr.EndLaunch + ": " + oneLine(why.Error()))
	code, out := m.run("read", "--as", m.cfg.As, "--return", p.Card, "--reason", reason, "--epoch", strconv.FormatUint(p.Epoch, 10))
	fmt.Fprintf(m.out, "read %s: returned exit=%d: %s\n", p.Card, code, reason)
	if code != 0 {
		fmt.Fprintf(m.out, "NOTE read --return %s refused: %s\n", p.Card, strings.TrimSpace(string(out)))
		return
	}
	m.returnedAt[p.Card] = m.passNow
}

// moved says the claim moved under a launch: the queue's card is at another
// epoch, generation (a read: attempt) than the one the child was started for. The
// claim is the card's own (the answer's epoch, its gen and attempt), or its packet's
// when it carries one: the queue hands packets only for the cards asked (queueArgs).
func (m *Member) moved(l launch, c queueCard) bool {
	epoch, gen, attempt := m.claim(c)
	return l.epoch != epoch || (!m.cfg.Reader && l.gen != gen) || (m.cfg.Reader && l.attempt != attempt)
}

// claim is the queue's card's claim: its packet's, when it carries one, else the card's own.
func (m *Member) claim(c queueCard) (epoch uint64, gen, attempt int) {
	if p := c.Packet; p != nil {
		return p.Epoch, p.Gen, p.Attempt
	}
	return m.epoch, c.Gen, c.Attempt
}

// queueArgs is the pass's queue, asking only for the packets it may use (a reader's answer
// otherwise carries every asked read's brief, hundreds of kilobytes, every pass): --packets,
// the reads a reader may begin this pass (its lanes free, and those the pass's reports
// free; a member none: its take hands its packets), and --have, every card it holds a
// launch for and every read it returned a moment ago, which need none. Every other
// in-flight card comes with its packet, so a restarted member recovers its cards from the
// first answer.
func (m *Member) queueArgs() []string {
	n := 0
	if m.cfg.Reader && !m.drain {
		n = m.lanes()
	}
	args := []string{"queue", "--as", m.cfg.As, "--json", "--packets", strconv.Itoa(n)}
	if words := m.haveWords(); words != "" {
		args = append(args, "--have", words)
	}
	return args
}

// haveWords is the cards that need no packet, comma separated, as --have names them, and
// remembered for the beat (Beat): every card this worker holds a launch for, and every
// read it returned a moment ago.
func (m *Member) haveWords() string {
	have := make([]string, 0, len(m.running)+len(m.returnedAt))
	for id := range m.running {
		have = append(have, id)
	}
	for id, at := range m.returnedAt {
		// a read this reader handed back is held to be its own only until it may begin it
		// again (ReadStageRetry): past that it wants the packet, and a card named here for
		// ever would never be sent one and never be begun again
		if _, ours := m.running[id]; !ours && m.passNow.Before(at.Add(ReadStageRetry)) {
			have = append(have, id)
		}
	}
	sort.Strings(have)
	words := strings.Join(have, ",")
	m.have.Store(&words)
	return words
}

// lanes is how many cards this pass may start: the lanes free now, and one for every
// launch the pass will report (its end collected or posted, or its child exited), so a
// read begun in the lane a report frees has its packet in the same pass.
func (m *Member) lanes() int {
	m.postMu.Lock()
	ended := map[string]bool{}
	for id, po := range m.posted {
		ended[id] = po.child == nil // a start posted frees nothing; its failure, or an end, does
	}
	m.postMu.Unlock()
	n := m.width - m.Running()
	for id, l := range m.running {
		if !l.spent && (l.res != nil || ended[id] || (l.child != nil && l.child.Done())) {
			n++
		}
	}
	return max(n, 0)
}

// queueOf runs a queue that asks for its packets (--packets), and, when the server is one
// from before the flag (it refuses it, `unknown flag --packets`), the queue as it was:
// a member installed ahead of its server works on, at one more exchange a pass, until the
// server is new.
func queueOf(run func(...string) (int, []byte), args []string) (int, []byte) {
	code, out := run(args...)
	if code == 0 || !bytes.Contains(out, []byte("unknown flag --packets")) {
		return code, out
	}
	i := slices.Index(args, "--packets")
	return run(args[:i]...)
}

// stampProgress stamps progress on the work cards whose child printed since the card's last
// stamp, each at most every ProgressEvery: `progress --as <member> <card>@<gen>... --epoch
// <n>`, one verb for the cards of each epoch (docs/SPEC-SPRINT.md section 8, the rules
// table's row late; tla/SprintRules.tla, Stamp). A child that prints nothing stamps nothing:
// the silence is what the late rule reads. A refused stamp waits ProgressEvery like any
// other; it returns what the verb printed when the store did not answer, nil otherwise.
func (m *Member) stampProgress(now time.Time) (unanswered []byte) {
	if m.cfg.Reader {
		return nil
	}
	byEpoch := map[uint64][]string{}
	for id, l := range m.running {
		p, ok := l.child.(Printer)
		if !ok || l.busy || l.spent || l.res != nil || l.child.Done() {
			continue
		}
		if at := p.Printed(); at.IsZero() || !at.After(l.stamped) || now.Sub(l.stamped) < ProgressEvery {
			continue
		}
		byEpoch[l.epoch] = append(byEpoch[l.epoch], id)
	}
	for _, epoch := range slices.Sorted(maps.Keys(byEpoch)) {
		ids := slices.Sorted(slices.Values(byEpoch[epoch]))
		args := []string{"progress", "--as", m.cfg.As}
		for _, id := range ids {
			args = append(args, id+"@"+strconv.Itoa(m.running[id].gen))
		}
		args = append(args, "--epoch", strconv.FormatUint(epoch, 10))
		code, out := m.run(args...)
		if code == 2 {
			unanswered = out
			continue
		}
		if code != 0 {
			fmt.Fprintf(m.out, "NOTE progress refused (exit %d): %s\n", code, oneLine(strings.TrimSpace(string(out))))
		}
		for _, id := range ids {
			l := m.running[id]
			l.stamped = now
			m.running[id] = l
		}
	}
	return unanswered
}

// endEnded begins the end of every launch whose child has exited and whose claim the queue
// still holds: the child's result is read and, for a work card, its commit is pushed,
// pushWidth pushes at a time. Both are long (files read; a fetch and one exchange with the
// forge), so they are done apart from the pass (long), which never waits on them; the
// launch is busy until they post, and the pass after that reports it. Each launch keeps
// its push, so a finish the store did not answer is reported again and never pushed again.
// The rule is docs/SPEC-SWARM.md's `member` (the push at a work card's finish) and
// docs/SPEC-SPRINT.md's finish row (the head the merge reads).
func (m *Member) endEnded(ids []string, byID map[string]queueCard) {
	var ends sync.WaitGroup
	for _, id := range ids {
		c := byID[id]
		l, ours := m.running[id]
		if !ours || l.busy || l.stopped || l.res != nil || l.spent || (c.Col != "working" && c.Col != "reading") || m.moved(l, c) || !l.child.Done() {
			continue
		}
		l.busy, l.busyAt = true, m.clock()
		m.running[id] = l
		child, p, branch := l.child, l.packet, l.branch
		ends.Add(1)
		m.longs.Add(1)
		go func() {
			defer m.longs.Done()
			defer ends.Done()
			r := child.Result()
			if m.cfg.Reader || p.Kind == "read" {
				m.post(id, post{res: &r})
				return
			}
			r = treeFinishWith(p, r, m.stepResolve(p))
			var pu Push
			switch {
			case r.Head == "":
				pu.None = "the child's result names no commit (no head: line, no push recorded)"
			case branch == "":
				pu.None = "the packet names no branch to push to"
			case m.pusher == nil:
				pu.None = "this member has no pusher"
			default:
				m.pushGate <- struct{}{}
				pu = m.pusher.Push(p, r)
				<-m.pushGate
				if pu.Sha == "" && pu.Refused == "" && pu.None == "" {
					pu.Refused = "the pusher said nothing"
				}
			}
			m.post(id, post{res: &r, push: &pu, decided: m.attempt(p, r, pu, branch)})
		}()
	}
	m.longWork()
	if !m.cfg.Background {
		ends.Wait() // a pass is one step: the ends it began, side by side, are its own
	}
}

// endEndedLocal begins the end of locally running launches whose child has exited
// when the queue read failed or timed out, so completed children are not starved
// (docs/SPEC-SWARM.md, member; docs/SPEC-SPRINT.md, the fleet).
func (m *Member) endEndedLocal() {
	var ends sync.WaitGroup
	for _, id := range slices.Sorted(maps.Keys(m.running)) {
		l, ours := m.running[id]
		if !ours || l.busy || l.stopped || l.res != nil || l.spent || l.child == nil || !l.child.Done() || l.claimMoved || l.dropped {
			continue
		}
		l.busy, l.busyAt = true, m.clock()
		m.running[id] = l
		child, p, branch := l.child, l.packet, l.branch
		ends.Add(1)
		m.longs.Add(1)
		go func() {
			defer m.longs.Done()
			defer ends.Done()
			r := child.Result()
			if m.cfg.Reader || p.Kind == "read" {
				m.post(id, post{res: &r})
				return
			}
			r = treeFinishWith(p, r, m.stepResolve(p)) // the step resolver of the other finish path (stepsha.go), so a prefix resolves on both
			var pu Push
			switch {
			case r.Head == "":
				pu.None = "the child's result names no commit (no head: line, no push recorded)"
			case branch == "":
				pu.None = "the packet names no branch to push to"
			case m.pusher == nil:
				pu.None = "this member has no pusher"
			default:
				m.pushGate <- struct{}{}
				pu = m.pusher.Push(p, r)
				<-m.pushGate
				if pu.Sha == "" && pu.Refused == "" && pu.None == "" {
					pu.Refused = "the pusher said nothing"
				}
			}
			m.post(id, post{res: &r, push: &pu, decided: m.attempt(p, r, pu, branch)})
		}()
	}
	m.longWork()
	if !m.cfg.Background {
		ends.Wait()
	}
}

// finishReport is a work card's finish as the member reports it, from its result and its
// push (Judge): ok or failed, why when failed, and the report: the push first, so the
// 500-byte cut never takes it, then the child's line, and a failed finish's reason before
// both. The attempt decision is asked over the same report (attempt).
func finishReport(r Result, pu Push, branch string) (fin Finish, why, report string) {
	fin, why = Judge(r, pu)
	report = oneLine(r.Report)
	if r.Carry != "" {
		// where the rework was staged, before the child's words and after the push
		report = cut("stage: " + oneLine(r.Carry) + "; " + report)
	}
	if pu.Sha != "" {
		said := "pushed=" + pu.Sha + " to " + branch
		if pu.PR != "" {
			said += " pr=" + pu.PR
		}
		report = cut(said + ": " + report)
	}
	if fin != FinishOK {
		report = cut(why + "; " + report)
	}
	return fin, why, CarryProposed(report, r)
}

// ProposedKey begins the one line a held report proposes the PATHS its card lacked by
// (docs/SPEC-CARD-CONTRACT.md section 4, "recut-widen-r.w1"): PATHS-PROPOSED: <glob>[,<glob>...].
const ProposedKey = "PATHS-PROPOSED:"

// PathsProposed is the globs of the first PATHS-PROPOSED line in text, read to the end of its
// line or a ";", empty ones left out: the paths alone, read before any prose on that line.
// Each comma-separated item is its first word, trimmed of quotes, emphasis and a closing
// full stop; an item with words after its first ends the paths ("b.go, c.go because the
// test needs it" is b.go and c.go). ok is false when text has no such line.
func PathsProposed(text string) (globs []string, ok bool) {
	_, rest, ok := strings.Cut(text, ProposedKey)
	if !ok {
		return nil, false
	}
	rest, _, _ = strings.Cut(rest, "\n")
	rest, _, _ = strings.Cut(rest, ";")
	for _, item := range strings.Split(rest, ",") {
		words := strings.Fields(item)
		if len(words) == 0 {
			continue
		}
		if g := strings.TrimRight(strings.Trim(words[0], "`*\"'"), "."); g != "" {
			globs = append(globs, g)
		}
		if len(words) > 1 {
			break // prose follows the paths
		}
	}
	return globs, true
}

// CarryProposed is a finish's report with the child's PATHS-PROPOSED line, read from its
// report and else its body, kept at the report's end within the 500-byte cut, so brief
// --widen reads it off the card (docs/SPEC-SPRINT.md section 2, "recut-widen-r.w1"); the
// report as it is when the child proposed nothing or the report holds the line already.
func CarryProposed(report string, r Result) string {
	globs, ok := PathsProposed(r.Report)
	if !ok {
		globs, ok = PathsProposed(r.Body)
	}
	if _, has := PathsProposed(report); !ok || has || len(globs) == 0 {
		return report
	}
	line := cut("; " + ProposedKey + " " + strings.Join(globs, ","))
	return report[:min(len(report), 500-len(line))] + line
}

// Carry is where a card's next attempt starts when brief --widen widens it in place, or a
// rule's twin's first: the held card, its attempt and the head that attempt pushed, written
// into the brief header as its CARRY: line (docs/SPEC-SPRINT.md section 2, "recut-widen-r.w1").
type Carry struct {
	Card    string
	Attempt int
	Head    string
}

// CarryKey begins a brief's CARRY: line.
const CarryKey = "CARRY:"

// CarryLine is c as its brief header line: CARRY: <card> attempt <n> head=<sha>.
func CarryLine(c Carry) string {
	return fmt.Sprintf("%s %s attempt %d head=%s", CarryKey, c.Card, c.Attempt, c.Head)
}

// CarryOf is the CARRY: line of a brief's header (line 1 and the lines after it up to the
// first blank one), ok only with a card, an attempt and a full sha.
func CarryOf(brief string) (c Carry, ok bool) {
	for i, l := range strings.Split(brief, "\n") {
		l = strings.TrimSpace(l)
		if i > 0 && l == "" {
			break
		}
		rest, found := strings.CutPrefix(l, CarryKey)
		if !found {
			continue
		}
		if n, err := fmt.Sscanf(strings.TrimSpace(rest), "%s attempt %d head=%s", &c.Card, &c.Attempt, &c.Head); err != nil || n != 3 {
			return Carry{}, false
		}
		return c, c.Attempt > 0 && typedrec.IsFullSha(c.Head)
	}
	return Carry{}, false
}

// Carried is a work packet as the member stages it: one with no pushed head of its own
// (sprint.BaseOf found none) whose brief carries a CARRY: line starts from that head, as a
// rework starts from its last pushed head (docs/SPEC-CARD-CONTRACT.md, "Where a rework
// starts"), so a card brief --widen widens keeps the held attempt's work; any other as it is.
func Carried(p Packet) Packet {
	if p.Kind == "read" || p.BaseHead != "" {
		return p
	}
	if c, ok := CarryOf(p.Brief); ok {
		p.BaseHead, p.BaseFrom = c.Head, c.Attempt
	}
	return p
}

// attempt is a work take's attempt decision (Config.Attempt), asked in its end's long work
// over the child's result as RESULT.md says it and the finish's report; nil when the member
// has no decider, or no take ran (a launch refused at staging).
func (m *Member) attempt(p Packet, r Result, pu Push, branch string) *decided {
	if m.cfg.Attempt == nil || r.End == EndStaging {
		return nil
	}
	_, _, report := finishReport(r, pu, branch)
	line, decision, err := m.cfg.Attempt(p, ResultText(r), report)
	return &decided{line: line, decision: decision, err: err}
}

// ResultText is a child's result as its RESULT.md says it (docs/SPEC-CARD-CONTRACT.md
// section 3), the fields the member read and its body; "" when it wrote none.
func ResultText(r Result) string {
	if !r.Shaped && r.Head == "" && r.Verdict == "" && r.Report == "" && r.Body == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "head: %s\nverdict: %s\nreport: %s\n", orDash(r.Head), orDash(r.Verdict), orDash(oneLine(r.Report)))
	if body := strings.TrimSpace(r.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cut is a report cut at 500 bytes, the bound oneLine keeps.
func cut(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// NoFinding begins the reason of a read the member hands back because its broken verdict
// names no defect (docs/SPEC-CARD-CONTRACT.md section 3).
const NoFinding = "no finding"

// MaxFindingBytes bounds a broken read's finding as the member reports it: under the
// sprint's bound on a card's text field (sprint.MaxCardTextBytes), so a long review is cut,
// never refused.
const MaxFindingBytes = 6 << 10

// findingOf is a broken read's finding as the member reports it: every line of its report
// and its body in order, the body's repeat of the report and markdown headings left out,
// joined with " / " and cut to MaxFindingBytes, never its first line alone
// (docs/SPEC-SPRINT.md section 6: the coordinator's judgment shows the finding in full).
func findingOf(r Result) string {
	report := oneLine(r.Report)
	lines := []string{}
	if report != "" {
		lines = append(lines, report)
	}
	repeat := report != ""
	for _, l := range strings.Split(r.Body, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || strings.HasPrefix(l, "#"):
		case repeat && l == report:
			repeat = false
		default:
			lines = append(lines, l)
		}
	}
	return oneline.Cap(strings.Join(lines, " / "), MaxFindingBytes)
}

// oneLine is a report as one line for a verb's flag: the first non-empty
// line, cut at 500 bytes; "" when there is none.
func oneLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if len(l) > 500 {
			l = l[:500]
		}
		return l
	}
	return ""
}

// fromTheSprint opens what CardText appends to the brief.
const fromTheSprint = "## From the sprint\n\n"

// BriefOf is the brief a card file (CardText) begins with, the sprint's mechanics after it
// cut off: what the decide read is asked over, as its bars were calibrated (the work card
// alone, never the read's mechanics or the worker's report).
func BriefOf(card string) string {
	if i := strings.Index(card, "\n\n"+fromTheSprint); i >= 0 {
		return card[:i+1]
	}
	if strings.HasPrefix(card, fromTheSprint) {
		return ""
	}
	return card
}

// CardText is the card file a child is given: the brief VERBATIM first (a
// card's brief is a whole child brief in the card grammar `nova-swarm lint
// --card` checks, whose line 1 is the contract line), then, appended, the
// mechanics the sprint adds: the attempt, the branch and base when a base
// names a repository, the fix of this attempt, the notes, and the exact
// command that reports it. Nothing is put in front of the brief.
func CardText(p Packet) string {
	var b strings.Builder
	brief := strings.TrimRight(p.Brief, "\n")
	if brief != "" {
		b.WriteString(brief)
		b.WriteString("\n\n")
	}
	b.WriteString(fromTheSprint)
	if p.Kind == "read" {
		fmt.Fprintf(&b, "This is read %s: attempt %d of %s, worked by %s, at head %s on branch %s", p.Card, p.Attempt, p.Primary, p.Worker, p.Head, p.WorkBranch)
		if p.WorkBase != "" {
			fmt.Fprintf(&b, " (base %s)", p.WorkBase)
		}
		b.WriteString(".\n\n")
		if strings.TrimSpace(p.Report) != "" {
			fmt.Fprintf(&b, "The worker's report:\n\n%s\n\n", strings.TrimSpace(p.Report))
		}
	} else {
		fmt.Fprintf(&b, "This is %s: attempt %d of %s (stream %s).", p.Card, p.Attempt, p.Primary, p.Stream)
		if p.Branch != "" {
			fmt.Fprintf(&b, " The checkout is on branch %s; JOB.md, which the prompt names first, says where it is and how this card ends. When you end, the member pushes your commit to origin's branch %s from outside the wall.", p.Branch, p.Branch)
		}
		b.WriteString("\n\n")
		b.WriteString(cardtree.Guide(cardtree.Parse(p.Brief)))
	}
	if strings.TrimSpace(p.Fix) != "" {
		fmt.Fprintf(&b, "Fix, this attempt:\n\n%s\n\n", strings.TrimSpace(p.Fix))
	}
	for _, n := range p.Notes {
		if strings.TrimSpace(n) != "" {
			fmt.Fprintf(&b, "Note:\n\n%s\n\n", strings.TrimSpace(n))
		}
	}
	if p.Kind == "read" {
		b.WriteString("Your verdict is RESULT.md's `verdict: ok` or `verdict: broken` (broken means the work is wrong for the card; a problem of your own run is not a verdict, leave the line out). ")
		b.WriteString("A broken verdict tells them what to do: at least one finding line names the file (file:line), the line, or the card's STEP or RULE the work breaks, and says what to change. A broken verdict that names none is no verdict: the sprint asks another reader. ")
		b.WriteString("Your RESULT.md's `report:` line and its body are what the sprint records as your finding, in full; the member reports it for you as:\n\n")
		fmt.Fprintf(&b, "    nova-sprint read --as %s (--ok | --broken) %s --epoch %d --finding '<your findings>'\n", p.As, p.Card, p.Epoch)
		return b.String()
	}
	b.WriteString("Your RESULT.md's `report:` line is what the sprint records as your report; the member reports it for you as:\n\n")
	fmt.Fprintf(&b, "    nova-sprint finish --as %s %s@%d --epoch %d --branch %s --head <sha> --report '<one line>' [--failed]\n", p.As, p.Card, p.Gen, p.Epoch, p.Branch)
	return b.String()
}

// routeWords is a work card's route as the member's start and finish lines name
// it: " route=<r> model=<m>", "" when the packet names none (the member's
// override, said on its MEMBER line) and for a read.
func routeWords(p Packet) string {
	switch {
	case p.Kind == "read":
		return ""
	case p.Route == "":
		return "" // the member's override, said once on its MEMBER line
	}
	return " route=" + p.Route + " model=" + p.Model
}

// usageArgs is the --usage a finish or a read reports: what the run spent, the card's
// cost record (internal/cardcost), kept on the card; none when the child reported nothing.
func usageArgs(r Result) []string {
	if r.Usage == "" {
		return nil
	}
	return []string{"--usage", r.Usage}
}

// StartGap is the least time between two harness starts on one member, so the cards a
// pass takes do not all start their harness in the same instant (sixteen at once on one
// machine lost their start).
const StartGap = 300 * time.Millisecond

// staggerStart waits, when the member has a Sleep, until StartGap has passed since its
// last harness start, then marks this one. Work and read loops alike.
func (m *Member) staggerStart() {
	if m.cfg.Sleep == nil {
		return
	}
	now := time.Now
	if m.cfg.Clock != nil {
		now = m.cfg.Clock
	}
	if !m.lastStart.IsZero() {
		if wait := StartGap - now().Sub(m.lastStart); wait > 0 {
			m.cfg.Sleep(wait)
		}
	}
	m.lastStart = now()
}

// ScriptReadPrefix begins the finding of a script read that found the head the program's own
// output. The sprint counts reads by it (sprint.ScriptReadPrefix is this constant), so the
// prefix the member writes and the one the sprint reads are one value.
const ScriptReadPrefix = "script read: "

// scriptChild is a read already ended: the script reader's own verdict, with no process.
type scriptChild struct{ res Result }

func (c scriptChild) Done() bool     { return true }
func (c scriptChild) Result() Result { return c.res }

// scriptOrStart is a launch's start: a read of a script card is asked of this reader's
// ScriptVerify first, and an ok answer is the read, ended with no child and no model
// (docs/SPEC-SPRINT.md, the script read); any other answer is no verdict and the read is
// started as a model child, the note saying why. Every other card is started as it is.
func (m *Member) scriptOrStart(p Packet) (ch Child, note string, err error) {
	if m.cfg.Reader && p.Kind == "read" && m.cfg.ScriptVerify != nil {
		if c, why := cardhdr.ReadClass(p.Brief); why == "" && c.IsScript() {
			ok, why := m.cfg.ScriptVerify(p, c)
			if ok {
				return scriptChild{Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Report: ScriptReadPrefix + oneLine(why)}}, "", nil
			}
			note = "script read gave no verdict (" + oneLine(why) + "); asked of a model"
		}
	}
	ch, err = m.runner.Start(p)
	return ch, note, err
}

// DefaultScriptDeadline bounds a script run whose card names no deadline.
const DefaultScriptDeadline = 30 * time.Minute

// ScriptVerifier is a script reader's means (docs/SPEC-SPRINT.md, the script read). Mirror
// is a repository that holds the attempt's start commit and the head; Temp is a directory
// the checkout is made in (removed after); Run runs the program's argv in a directory
// under the wall, with the card's deadline in ctx, and is the one place a program runs:
// the caller wires the wall, a test a fake. Git is the git program, "" for git on PATH.
type ScriptVerifier struct {
	Mirror string
	Temp   string
	Git    string
	Run    func(ctx context.Context, dir string, argv []string) error
}

// Verify is Config.ScriptVerify: it checks out the attempt's start commit (the packet's
// base head, else the merge base of the head and the work's base branch), runs the
// card's program from the repository root under the card's deadline, and compares the
// resulting diff with the head's diff byte for byte. Identical is ok, with the one line
// that says what was compared; a difference, an empty diff, a missing commit or a failed
// run is not ok, with the reason. The head is never taken on the worker's word.
func (v ScriptVerifier) Verify(p Packet, class cardhdr.Class) (ok bool, why string) {
	argv := strings.Fields(class.Script)
	switch {
	case len(argv) == 0:
		return false, "the card names no program"
	case p.Head == "":
		return false, "the read names no head"
	}
	deadline := cmp.Or(class.Deadline, DefaultScriptDeadline)
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	g := func(dir string, args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{Bin: v.Git, C: dir, OwnRepo: true, Timeout: deadline}, args...)
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(res.Stderr)))
		}
		return string(res.Stdout), nil
	}
	start := p.BaseHead
	if start == "" {
		for _, base := range []string{p.WorkBase, "origin/" + p.WorkBase} {
			if base == "" || base == "origin/" {
				continue
			}
			if out, err := g(v.Mirror, "merge-base", p.Head, base); err == nil {
				start = strings.TrimSpace(out)
				break
			}
		}
	}
	if start == "" {
		return false, "no start commit: the packet has no base head and no base branch to take the merge base with"
	}
	want, err := g(v.Mirror, "diff", "--binary", "--full-index", start, p.Head)
	if err != nil {
		return false, err.Error()
	}
	if want == "" {
		return false, "the head's diff against the start commit is empty"
	}
	dir, err := os.MkdirTemp(v.Temp, "script-read-")
	if err != nil {
		return false, err.Error()
	}
	defer func() {
		// a checkout left behind is the bench's disk: the read gives no verdict for it
		if err := safepath.RemoveUnder(cmp.Or(v.Temp, os.TempDir()), dir); err != nil {
			ok, why = false, "the checkout was not removed: "+err.Error()
		}
	}()
	for _, args := range [][]string{{"clone", "-q", "--no-checkout", v.Mirror, dir}} {
		if _, err := g("", args...); err != nil {
			return false, err.Error()
		}
	}
	if _, err := g(dir, "checkout", "-q", "--detach", start); err != nil {
		return false, err.Error()
	}
	if err := v.Run(ctx, dir, argv); err != nil {
		return false, "the program failed: " + err.Error()
	}
	if _, err := g(dir, "add", "-A"); err != nil {
		return false, err.Error()
	}
	got, err := g(dir, "diff", "--cached", "--binary", "--full-index", start)
	if err != nil {
		return false, err.Error()
	}
	if got != want {
		return false, fmt.Sprintf("the program's diff (%d bytes) is not the head's (%d bytes)", len(got), len(want))
	}
	return true, fmt.Sprintf("ran %q at %s: its diff is %s's, %d bytes, identical", class.Script, short(start), short(p.Head), len(got))
}

// short is the first twelve characters of a sha.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// IsReadCardID says the card id is a read card's, <primary>.r<attempt>.<reader>[.g<n>]
// (internal/sprint ParseReadCard, which this package may not import).
func IsReadCardID(id string) bool {
	parts := strings.Split(id, ".")
	if len(parts) != 3 && len(parts) != 4 {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(parts[1], "r"))
	return strings.HasPrefix(parts[1], "r") && err == nil && n >= 1
}
