package main

// land is the coordinator's landing step as one command (docs/SPEC-SPRINT.md
// section 7; tla/SprintEvents.tla, the verb "land": only the head of a
// stream's merge queue lands, QHead). For each stream, in priority order, the
// merge queue is read in work order up to its first stuck card (the merge
// step's barrier) and cut into batches: each run of consecutive cards naming
// one repository and one base (the brief's REPO: and BASE: lines, read by
// swarm.ReadCardBase) is one batch. A batch is merged head by head (--no-ff)
// onto a branch cut from the base's tip on origin, gated once, pushed (never
// forced), and reported through the merge step (store.MergeStep), the step
// `merge --stream s --batch n` runs, so the store changes exactly as that verb
// changes it. The streams' batches merge beside each other and land one at a
// time (landpass.go, the pass in two phases; tla/LandPass.tla). A head that is missing or conflicts ends the batch before it and
// is reported with the merge step's conflict fact; a check that fails, with
// its red fact; a push rejected again after one rebuild on the moved base,
// with its rejected fact. The verb keeps no state of its own: the store
// changes only through those merge steps, and git and the check run as
// programs in the caller's environment.
//
// The push and the report are two operations on two systems, so land fences
// them: the caller's --epoch is checked before any git; the queue and the epoch
// are read again just before each push; the report is one store step that lands
// the batch, its cards by name, only while the queue still holds each of them at
// the head and attempt land built, at the epoch land read (landStep; the cards
// are looked for wherever they stand, so a card accepted or ranked ahead of them
// since changes nothing);
// a batch pushed and not reported is left in the base, the loop line names the
// store's reason, and the timeline is marked pushed-unreported <sha>. The next
// land records that mark through the merge step before any new merge (Recovers).
// Only a batch pushed and reported has its cards' branches
// tagged for the cleanup, which deletes them from origin outside every batch
// (landprune.go).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/diffcheck"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// landCheckBudget bounds one --check run: a batch's whole test command.
const landCheckBudget = 30 * time.Minute

// landWords is the banner's paragraph on land; its indented lines are what
// `nova-sprint land -h` quotes.
func landWords() string {
	return strings.TrimSpace(`
Landing, the coordinator's: an external delivery (git pushes the base) and a store write (the merge step):
  nova-sprint land --stream s1 --check 'make test'
    merges each queued card's head (--no-ff) in queue order onto a branch cut
    from origin's base, one batch per run of cards naming one REPO: and BASE:
    (--base for a card naming none); runs --check once per batch; pushes, never
    forced, rebuilding once on a moved base; then reports the batch as merge
    --stream s1 --batch <n> does. A head missing or in conflict ends the batch
    before it and is reported as merge --conflict, a red check as --red, a
    second rejected push as --rejected. Each head merged is checked first, by
    script and no model: a head whose diff changes a file outside its brief's
    PATHS, or leaves a stranded sentence fragment or an unmatched backquote in
    prose, ends the batch as a head in conflict does. Tests, testdata, tla/RUNS.tsv and
    tla/CASES.tsv, the docs catalog and AGENTS.md maps are inside every PATHS. A card that adds a directory
    owns its catalog row and the AGENTS.md maps; a conflict only in those maps, or
    those maps and added catalog rows, resolves as the ledgers do. A conflict only
    in the generated ledgers lands: the tip's side, then their tests' update run
    (NOVA_CI_UPDATE=1) to a fixed point, one commit. A refusal of the card's
    own head (a file conflict, the lander's checks, a merged tree failing the
    tree gate its base passes) stops nothing: the card is reworked at the
    base's tip (the refusal its fix, the seat told once), or for files outside
    its PATHS returned for the widen rule, and the stream's other cards land in
    the same pass; a card at its brief's bound goes back to review instead. A
    ledger the lander could not resolve, or a head origin does not hold, stops
    the stream, and after resume land merges the head again. The clone is --repo-dir,
    else the dir= each line names; git uses the caller's environment. A kept
    clone a pass cut short left not clean is restored to the fetched base before
    the batch (LAND CLEANED names the files); a --repo-dir one is refused. After
    the whole pass each landed merge diff is scored (nova-decide's score
    decision, with the key JEV_API_KEY holds, a minute for the pass; recorded in
    decide/score.jsonl under the land root): a batch whose cards' top class meets
    the sprint row's decide_score_bar (empty: none) raises one judgment, landed
    work scored low, listing them (scored=, judged=).
  nova-sprint land --land-parallel 4
    merges the streams' batches beside each other, up to that many at once, each in
    its own worktree of the clone (<clone>@<stream> under the land root), each batch's
    tree gated once as a whole (a red gate then gates its heads one by one, to blame
    the head); then lands the green batches one at a time in priority order: a batch
    cut from the tip the base still has is pushed with no new gate; one whose base moved
    (a batch landed before it) is merged again onto the new tip and pushed with no new
    gate when it changes no file the landings since touched, else gated once combined,
    and a red combined gate refuses it for this pass, naming what it collided with, and
    stops no stream. 1 merges the streams one after another.
  nova-sprint land --stream s1 --dry-run
    reads the store only: no git, no push, no report. The window: land pins
    each card's head and attempt as it reads them; a caller's --epoch is held
    before any git, the queue, the heads and the epoch again just before the
    push, and the report lands the batch, its cards by name, only while the queue
    holds each of them at that head at that epoch (one store step); a card
    accepted or ranked ahead since changes nothing. A clear, a return, a
    rework or a crash after the check leaves the push unreported (LAND
    FAILED, exit 2; run land again, never a bare merge, and its own checks
    decide: an unchanged card is recorded with no new push, a reworked one
    merged at its new head or met in conflict); a clear there pushes for an epoch
    just left: nothing is recorded for it, and the push is not undone.
  nova-sprint land --stream s1
    run again, it recovers once the outside is quiet (tla/Land.tla, Recovers),
    not otherwise: a run cut short between the push and the report on every
    try never reports; and a base that moves twice between the read and the
    push gives up (one rebuild, then the rejected fact, the stream stopped):
    nothing is pushed or lost and the cards stay queued; resume the stream
    (nova-sprint resume --stream s1 --did 'the base moved') and run land again.
  nova-sprint merge-window open --for 10m --reason 'the release merges by hand'
    pauses every landing for 10 minutes, its reason on each batch it pauses;
    land pauses a batch too while the merge queue of the branch it lands onto
    holds a group (a GitHub repository's, asked through gh; a queue that
    cannot be read pauses as a held one). A paused batch is refused before any
    git and again just before its push: nothing is pushed or recorded, no
    stream stops, and the cards land on a run after the pause ends.`) + "\n"
}

// landBatch is one batch's outcome, a line of output and an item of --json.
type landBatch struct {
	Stream string   `json:"stream"`
	Status string   `json:"status"` // ok, refused, failed
	Cards  int      `json:"cards"`
	IDs    []string `json:"ids"`
	Repo   string   `json:"repo,omitempty"`
	Base   string   `json:"base,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	Tip    string   `json:"tip,omitempty"`
	// Fact is the merge fact reported for a refusal (conflict, red,
	// rejected); empty when nothing was reported and the store is unchanged,
	// and always empty in a dry run, which reports nothing.
	Fact string `json:"fact,omitempty"`
	// WouldRecord is, in a dry run, the fact land would report for the refusal,
	// and nothing was: a reader of fact never has to check dry_run to know
	// whether the store holds it.
	WouldRecord string `json:"would_record,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// Also is every cause of the refusal after the first, each with its one next command,
	// and on a twin the verb that stands in for land: one NOTE line each.
	Also []string `json:"also,omitempty"`
	// Scope is each merged card's scope amendments, card:file: the files outside its PATHS
	// the rule allows as the test, fixture or doc of the same change (sprint.ScopeAmended).
	Scope  []string `json:"scope,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	// Times is how long each of the batch's steps took; nil for a batch refused before
	// its git ran, and for a dry run.
	Times *landTimes `json:"times,omitempty"`
	// Prune is the cards' branches on origin a landed batch put on the cleanup queue
	// (landprune.go; a dry run: would put); nil for a batch that did not land, or
	// whose cards recorded no branch.
	Prune *landPrune `json:"prune,omitempty"`
	// Score is the landed batch's scores (landscore.go): nil for a batch that did not
	// land, and for a dry run.
	Score *landScore `json:"score,omitempty"`
	// Cleaned is every file the lander's restore of its own clone discarded before the
	// batch (restore), one LAND CLEANED line; nil when the clone was clean.
	Cleaned []string `json:"cleaned,omitempty"`
	// Bench is the fleet member whose Go lane ran the batch's tree gate, empty when
	// the gate ran in this process or did not run. Wall is that gate's seconds.
	Bench string  `json:"bench,omitempty"`
	Wall  float64 `json:"wall,omitempty"`
	// Ring is how many benches the gate's hash ring held and Slot the one the batch's
	// stream hashed to (landring.go), zero when no bench ran a gate.
	Ring int `json:"ring,omitempty"`
	Slot int `json:"slot,omitempty"`
	// stopped says the fact recorded stopped the stream (the conflict fact of a card's own
	// head does not).
	stopped bool
}

// landTimes is a batch's steps, in seconds: the fetch, the merges (with any head
// fetched at its merge), the check, the queue read again before the push, the push and
// the report. A rebuild on a moved base adds its fetch, merges, check and push.
type landTimes struct {
	Fetch  float64 `json:"fetch"`
	Merge  float64 `json:"merge"`
	Check  float64 `json:"check"`
	Queue  float64 `json:"queue"`
	Push   float64 `json:"push"`
	Report float64 `json:"report"`
}

// since adds the seconds from start to now to *to.
func since(to *float64, start time.Time) { *to += time.Since(start).Seconds() }

// line is the batch's output line.
func (b landBatch) line() string {
	tip := b.Tip
	if tip == "" {
		tip = "-"
	}
	l := fmt.Sprintf("LAND %s stream=%s cards=%d base=%s tip=%s ids=%s", strings.ToUpper(b.Status), oneline.Field(b.Stream), b.Cards,
		oneline.Field(dashed(b.Base)), tip, oneline.Field(idSpan(b.IDs)))
	if b.Repo != "" {
		l += " repo=" + oneline.Field(b.Repo)
	}
	if b.Dir != "" {
		l += " dir=" + oneline.Field(b.Dir)
	}
	if t := b.Times; t != nil {
		l += fmt.Sprintf(" fetch=%.1fs merge=%.1fs check=%.1fs queue=%.1fs push=%.1fs report=%.1fs", t.Fetch, t.Merge, t.Check, t.Queue, t.Push, t.Report)
	}
	if b.Bench != "" {
		l += fmt.Sprintf(" bench=%s wall=%.1fs", oneline.Field(b.Bench), b.Wall)
		if b.Ring > 0 {
			l += fmt.Sprintf(" ring=%d slot=%d", b.Ring, b.Slot)
		}
	}
	if p := b.Prune; p != nil {
		switch {
		case p.Why != "":
			l += " branches_queued=-"
		default:
			l += " branches_queued=" + strconv.Itoa(p.Queued)
		}
		if len(p.Kept) > 0 {
			l += " branches_kept=" + strconv.Itoa(len(p.Kept))
		}
	}
	if s := b.Score; s != nil && (s.Scored > 0 || s.Why == "") {
		l += " scored=" + strconv.Itoa(s.Scored)
		if s.Judged {
			l += " judged=yes"
		}
	}
	if len(b.Scope) > 0 {
		l += " scope=" + oneline.Field(strings.Join(b.Scope, ","))
	}
	if b.Fact != "" {
		l += " fact=" + b.Fact
	}
	if b.WouldRecord != "" {
		l += " would_record=" + b.WouldRecord
	}
	if b.DryRun {
		l += " dry_run=yes"
	}
	if b.Reason != "" {
		l += " reason=" + oneline.Escape(b.Reason)
	}
	return l
}

// idSpan is ids as <first>..<last>, one id alone, - for none.
func idSpan(ids []string) string {
	switch len(ids) {
	case 0:
		return "-"
	case 1:
		return ids[0]
	}
	return ids[0] + ".." + ids[len(ids)-1]
}

// landCard is one queued card with where it lands: the head and attempt
// land read and pins (tla/Land.tla, a head is <<card, attempt>>: a rework
// keeps the id and the epoch), and the repository and base its brief names.
type landCard struct {
	id, head, attempt, repo, base string
	paths                         []string     // the brief's PATHS globs, nil when it names none (checkCard)
	brief                         string       // the brief, the card a landed diff is scored against (landscore.go)
	primary                       *sprint.Card // the primary, whose brief decision its landing attaches to (briefdecide.go)
	// resolved is the card's note when its landing did more than merge its head (the
	// generated ledgers regenerated, landledger.go): set by each build, reported with the
	// batch
	resolved string
	// protected is why the lander may not land the card on its base, a protected branch in
	// a stream not marked for its repository (sprint.ProtectedLandWhy), "" when it may
	protected string
	// start is the attempt's start commit when an earlier attempt pushed a head
	// (sprint.BaseOf). Empty on a first attempt: checkCard then uses the merge-base
	// with the base.
	start string
	// result is the finished work card's recorded result (claimText), empty when the
	// sprint holds none. checkEmptyCommit reads it as the attempt's RESULT.md.
	result string
}

// pin is the card as the report's guard and the operation's arguments name
// it: <id>@<attempt>:<head>.
func (c landCard) pin() string { return c.id + "@" + c.attempt + ":" + c.head }

// lander is one run of land.
type lander struct {
	a                          *app
	c                          common
	st                         *store.Store
	repoDir, base, check, root string
	dry, twin                  bool // twin: a mem twin, which has no git
	// conflictKind and conflictPaths are what the last merge that stopped on unmerged paths
	// left (mergeHead): the conflict fact carries them (conflictCard).
	conflictKind  string
	conflictPaths []string
	out           []landBatch
	epoch         uint64              // the epoch land read: every report is fenced to it
	diffs         map[string]string   // each card's merge diff, as checkCard read it, for its score
	scope         map[string][]string // each card's scope amendments, as checkCard allowed them (sprint.ScopeAmended)
	prose         map[string][]string // each stream's prose globs (sprint.StreamProse), whose backquotes checkCard does not read
	toScore       []scoreJob          // the landed batches, scored after the whole pass (landscore.go)
	// ledgerLog is the land log's lines for the shrink-only ledgers the batch's merges
	// resolved (ledgerunion.go), reported with the batch (NOTE) and then cleared.
	ledgerLog []string
	// recLog and recNote are the lines and the note of the records a merge
	// resolved (landappend.go), taken by mergeHead when the merge lands
	recLog  []string
	recNote string
	// gate is the gate decision's backend, clock, bars and record for a red batch gate
	// (landgate.go), nil when none is made; gateNote says why none is, once.
	gate          *landGate
	gateNote      string
	baseGateCache map[string]string // base commit SHA -> finding ("" when green)
	// baseGateFails is the base-gate rule's record of the base commits that failed their
	// tree gate, kept across rounds with the cache (treeGateBase); baseStop says the last
	// build stopped on the base's third failure; now and rulesOff are a test's clock and
	// rules turned off (nil: the app's clock, nova-config's sprint row).
	baseGateFails map[string]*baseGateFail
	baseStop      bool
	// cureTried is the heads tried as a red base's fix and found none, by base commit and
	// head (cureBase): not gated again on that base
	cureTried map[string]bool
	// baseFix is the land log's line for the last build's base fix, reported with the batch
	// (NOTE) and then cleared
	baseFix string
	// baseCount says the last build ran the base's gate under the rule and it was red, baseWhy
	// its finding: the refusal is counted in the store (baseRefused), never only in this
	// process, which a hand land starts empty every run and the server every start
	baseCount bool
	baseWhy   string
	// baseAbsent says the last build's fetch of the base alone failed with origin's
	// words for a ref it does not hold (notOnOrigin). The batch is the dead-base
	// fact (deadBaseRefused), not a fetch to retry.
	baseAbsent bool
	// baseNotes is what the pass's re-check of the bases that stopped streams did (baseRecheck)
	baseNotes []string
	// held is each clone's land lock this land holds (hold), released as it ends
	held     map[string]*filelock.FileLock
	now      func() time.Time
	rulesOff []string
	// gateHost is the bench the batch's tree gate ran on, gateWall how long those
	// runs took on the lander's clock; keep stamps them onto the next batch line.
	gateHost string
	gateWall time.Duration
	// gateKey is the batch's key on the gate bench's hash ring (landring.go): the stream
	// being landed (fork), the base re-checked (baseRecheck); gateRing and gateSlot are the
	// ring's size and the key's slot when a bench ran a gate, for the batch's line.
	gateKey  string
	gateRing int
	gateSlot int
	// laneAs is the holder this lander's gate records on a bench's Go lane: lander/<stream>
	// in a stream's fork, landLaneBase in the base re-check (laneWho).
	laneAs string
	// parallel is how many streams merge at once in the pass's first phase (--land-parallel;
	// landpass.go), and shared the locks and records the pass's streams share.
	parallel int
	shared   *landShared
}

// keep appends a batch, carrying the tree gate's bench and wall when one ran.
func (l *lander) keep(b landBatch) {
	if b.Bench == "" && l.gateHost != "" {
		b.Bench, b.Wall = l.gateHost, l.gateWall.Seconds()
		b.Ring, b.Slot = l.gateRing, l.gateSlot
	}
	l.gateHost, l.gateWall, l.gateRing, l.gateSlot = "", 0, 0, 0
	l.out = append(l.out, b)
}

// ranOnBench adds one tree gate run on host ("here" for this machine) to the batch's
// bench line: every host the batch's gates ran on, joined by +, and their total wall.
func (l *lander) ranOnBench(host string, wall time.Duration) {
	if host == "" {
		return
	}
	if !slices.Contains(strings.Split(l.gateHost, "+"), host) {
		l.gateHost = strings.TrimPrefix(l.gateHost+"+"+host, "+")
	}
	l.gateWall += wall
}

// stage names where the landing is, for the land loop's beat (landloop.go).
func (l *lander) stage(name, proc string) {
	if l == nil || l.a == nil || name == "" {
		return
	}
	if proc == "" {
		proc = "nothing named"
	}
	l.a.landStage(name, proc)
}

func (a *app) cmdLand(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("land")
	var streams listFlag
	fs.Var(&streams, "stream", "a stream to land (again, or comma separated, for more; default: every stream with cards queued to merge and not stopped)")
	repoDir := fs.String("repo-dir", "", "the clone to land from, its origin the remote pushed to; each stream's batch is built in a worktree of it under the land root (default: a clone per repository under the directory each line names)")
	base := fs.String("base", "", "the base branch of a card whose brief names no BASE: line")
	check := fs.String("check", "", "a command run once per batch, by sh -c in the clone on the batch branch, before the push (bounded to 30m); non-zero reports the batch red and pushes nothing")
	dry := fs.Bool("dry-run", false, "print the batches it would land and change nothing: reads the store only (no git, no push, no report)")
	parallel := fs.Int("land-parallel", landParallelDefault, "how many streams merge at once, each in its own worktree of the clone, before the landings go one at a time (landpass.go); 1 merges the streams one after another")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "land", err.Error())
	}
	var bad []string
	if len(pos) > 0 {
		bad = append(bad, "takes no words; a stream is --stream <s>")
	}
	if *parallel < 1 {
		bad = append(bad, "--land-parallel wants a count of one or more, not "+strconv.Itoa(*parallel))
	}
	if strings.HasPrefix(*base, "-") {
		bad = append(bad, "--base wants a branch name, not "+*base)
	}
	if *repoDir != "" {
		if fi, err := os.Stat(*repoDir); err != nil || !fi.IsDir() {
			bad = append(bad, "--repo-dir wants an existing clone; "+*repoDir+" is not a directory")
		}
	}
	if len(bad) > 0 {
		return refuse(stderr, "land", strings.Join(bad, "; "))
	}
	// land's reads and its report are steps of the sprint: each takes the server's one
	// line of control (a.serial) when this process is the server (run --land), so none
	// runs during a tick or a worker's batch; its git runs outside it, for as long as
	// git takes. A land by itself holds a lock nothing else wants.
	a.serial.Lock()
	st, err := a.store(*c)
	a.serial.Unlock()
	if err != nil {
		return refuse(stderr, "land", err.Error())
	}
	// the epoch the caller holds is checked before anything is read or run
	// (tla/Land.tla, Read: a stale caller is refused before any push)
	if c.epoch >= 0 && uint64(c.epoch) != st.PinnedEpoch() {
		fmt.Fprintf(stderr, "%s land: the sprint is at epoch %d, not %d (cleared since): nothing was fetched, pushed or reported; run: nova-sprint where\n", prog, st.PinnedEpoch(), c.epoch)
		return 1
	}
	if a.baseGateCache == nil {
		a.baseGateCache = map[string]string{}
	}
	if a.baseGateFails == nil {
		a.baseGateFails = map[string]*baseGateFail{}
	}
	l := &lander{a: a, c: *c, st: st, repoDir: *repoDir, base: *base, check: *check, dry: *dry, twin: a.twinOpen(c.redis), epoch: st.PinnedEpoch(), diffs: map[string]string{}, scope: map[string][]string{}, prose: map[string][]string{},
		baseGateCache: a.baseGateCache, baseGateFails: a.baseGateFails, cureTried: map[string]bool{}, parallel: *parallel}
	l.locks()
	defer l.release()
	ctx := a.landCtx
	if ctx == nil {
		ctx = context.Background() // a direct land command has no loop caller
	}
	if *check != "" && !*dry {
		a.serial.Lock()
		l.gate, l.gateNote = a.landGate(ctx, st)
		a.serial.Unlock()
	}
	if *repoDir != "" {
		if abs, err := filepath.Abs(*repoDir); err == nil {
			l.repoDir = abs
		}
	}
	a.serial.Lock()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	a.serial.Unlock()
	if err != nil {
		return a.readFailed("land", err, stderr)
	}
	// the streams in stream order: the named ones (a name that is no stream
	// last, to be refused), else every one with cards queued and not stopped
	var order []string
	for _, name := range s.Streams() {
		named := slices.Contains(streams, name)
		if named || len(streams) == 0 && s.StreamCtl(name) != nil && s.StreamCtl(name).F("state") != sprint.StreamStopped && len(landQueue(s, name)) > 0 {
			order = append(order, name)
		}
	}
	for _, name := range streams {
		if !slices.Contains(order, name) {
			order = append(order, name)
		}
	}
	// the bases that stopped streams, re-checked once each before the streams land
	l.baseRecheck(ctx, s)
	// by priority (sprint.LandOrder): the stream whose merging set holds the highest level
	// first, then the most cards behind, then stream order; a named stream that is no stream
	// keeps its place last
	order = sprint.LandOrder(s, order)
	// a card already pushed and not reported is recorded before any new merge
	var recordFailed bool
	s, recordFailed = l.recordPushed(ctx, s, order)
	if s == nil {
		return l.report(true, nil, stdout, stderr) // no pass can run without a canonical snapshot
	}
	order = stillQueued(s, order)
	// the pass: every stream's batch merged beside the others', then the green ones landed
	// one at a time in this order (landpass.go; tla/LandPass.tla)
	failed := l.pass(ctx, s, order) || recordFailed
	if !l.dry {
		l.pruneWorktrees(ctx)
	}
	// the cleanup, after every stream and outside every batch: the land loop's own
	// (landAfter) when the loop runs this land, else once here, as the command ends
	var pruned []pruneResult
	if !a.landLazy && !l.dry {
		pruned = a.flushPrune(ctx, true)
	}
	// the landed diffs' scores, last: under the land loop's context when the loop runs this
	// land, so its shutdown ends the pass, and one deadline for the pass (landscore.go)
	sctx := ctx
	if a.landCtx != nil {
		sctx = a.landCtx
	}
	l.scoreAll(sctx)
	return l.report(failed, pruned, stdout, stderr)
}

// report prints the batches, the cleanup and the summary: exit 0 when every batch
// landed, 1 when one was refused, 2 when a push landed and its report did not. A
// cleanup that failed changes no exit: its batches landed.
func (l *lander) report(failed bool, pruned []pruneResult, stdout, stderr io.Writer) int {
	batches, cards, refused := 0, 0, 0
	for _, b := range l.out {
		switch b.Status {
		case "ok":
			batches++
			cards += b.Cards
		default:
			refused++
		}
	}
	code := 0
	switch {
	case failed:
		code = 2
	case refused > 0:
		code = 1
	}
	if l.c.json {
		status := map[int]string{0: "ok", 1: "refused", 2: "failed"}[code]
		out := l.out
		if out == nil {
			out = []landBatch{}
		}
		if pruned == nil {
			pruned = []pruneResult{}
		}
		// ignored: a map of strings, numbers and plain structs of strings always encodes
		notes := l.baseNotes
		if notes == nil {
			notes = []string{}
		}
		b, _ := json.Marshal(map[string]any{"verb": "land", "status": status, "exit": code, "batches": batches, "cards": cards,
			"refused": refused, "dry_run": l.dry, "items": out, "prune": pruned, "base_checks": notes})
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, b := range l.out {
		if b.Cleaned != nil {
			fmt.Fprintf(stdout, "LAND CLEANED stream=%s dir=%s files=%d paths=%s\n", oneline.Field(b.Stream), oneline.Field(b.Dir), len(b.Cleaned), oneline.Field(strings.Join(b.Cleaned, ",")))
		}
		w := stdout
		if b.Status != "ok" {
			w = stderr
		}
		fmt.Fprintln(w, b.line())
		for _, x := range b.Also {
			fmt.Fprintf(w, "NOTE %s\n", oneline.Escape(x))
		}
		switch {
		case b.WouldRecord != "":
			fmt.Fprintf(w, "NOTE land would report this as merge --%s and stop stream %s; nothing was reported (dry run)\n", b.WouldRecord, oneline.Field(b.Stream))
		case b.Fact == "conflict" && !b.stopped:
			fmt.Fprintf(w, "NOTE the card is reworked at the tip and stream %s goes on; the seat is told\n", oneline.Field(b.Stream))
		case b.Fact == "dead-base":
			fmt.Fprintf(w, "NOTE stream %s is not stopped: these cards are held from landing until their judgment is answered or their base re-pointed, and the rest of the stream lands; run: nova-sprint inbox\n", oneline.Field(b.Stream))
		case b.Fact != "":
			fmt.Fprintf(w, "NOTE the stream is stopped (%s); run: nova-sprint inbox\n", b.Fact)
		case b.Status == "refused" && !l.dry:
			fmt.Fprintf(w, "NOTE nothing was pushed or reported for stream %s; its cards stay queued\n", oneline.Field(b.Stream))
		}
		if s := b.Score; s != nil && s.Why != "" {
			// what was not scored is said where the land loop shows what went wrong
			fmt.Fprintf(stderr, "NOTE %s\n", oneline.Escape(s.Why))
		}
		if p := b.Prune; p != nil {
			if p.Why != "" {
				fmt.Fprintf(w, "NOTE %s\n", oneline.Escape(p.Why))
			}
			for _, k := range p.Kept {
				fmt.Fprintf(w, "NOTE no branch queued for deletion for %s\n", oneline.Escape(k))
			}
		}
	}
	for _, p := range pruned {
		fmt.Fprintln(stdout, p.line(true))
	}
	for _, n := range l.baseNotes {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(n))
	}
	dry := ""
	if l.dry {
		dry = " dry_run=yes"
	}
	w := stdout
	if code != 0 {
		w = stderr // each line above names its remedy
	}
	fmt.Fprintf(w, "LAND DONE batches=%d cards=%d refused=%d%s; run: nova-sprint where\n", batches, cards, refused, dry)
	return code
}

// landQueue is the stream's queued merge cards the merge step can land, in
// work order: those before its first stuck card (steps_merge.go, the barrier).
func landQueue(s *sprint.Snapshot, stream string) []*sprint.Card {
	queued := s.Merge.Cell(stream, sprint.Queued)
	stuck := s.Merge.Cell(stream, sprint.Stuck)
	if len(stuck) == 0 {
		return sprint.MergePriorityOrder(s, queued)
	}
	var before []*sprint.Card
	for _, c := range queued {
		if c.Score < stuck[0].Score || (c.Score == stuck[0].Score && c.ID < stuck[0].ID) {
			before = append(before, c)
		}
	}
	return sprint.MergePriorityOrder(s, before)
}

// shaRE is a commit id as a head names it: hex, abbreviated or whole.
var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// placeWhy is why land cannot place a batch before any git, "" when it can:
// every problem of the batch at once (a base missing, a repository missing with
// no clone given, and with them every head that is not a commit id, which land
// would meet next), so one run names all of them (docs/STANDARD.md, a refusal
// names every problem at once), each cause on its own line with its one next
// command: the first is the refusal's reason, the rest (also) its NOTE lines, and on a
// twin, which has no git, the merge that stands in for land. A base that is not a
// branch name is refused alone.
func (l *lander) placeWhy(stream string, cards []landCard) (string, []string) {
	id, base := cards[0].id, cards[0].base
	if strings.HasPrefix(base, "-") {
		return "card " + id + " names the base " + base + ", which is not a branch name; run: nova-sprint card " + id, nil
	}
	var why, flags []string
	if base == "" {
		why = append(why, "card "+id+" names no BASE: line and no --base was given")
		flags = append(flags, "--stream "+stream+" --base <branch>")
	}
	if cards[0].repo == "" && l.repoDir == "" {
		why = append(why, "the card names no REPO: line and no --repo-dir was given")
		flags = append(flags, "--repo-dir <clone>")
	}
	if len(why) == 0 {
		// a protected base in an unmarked stream (docs/SPEC-SPRINT.md section 7), refused
		// before any git with the mark as its one next command
		return cards[0].protected, nil
	}
	var also []string
	for _, c := range cards {
		if !shaRE.MatchString(c.head) {
			// before any git nothing was recorded and no stream stopped: the one next
			// command is the return; its judgment offers the rework
			also = append(also, "the head "+dashed(c.head)+" of "+c.id+" is not a commit id (a finish without --head records the card's id); run: nova-sprint return "+c.id+" --reason 'its head is not a commit'")
		}
	}
	if l.twin {
		also = append(also, fmt.Sprintf("this twin has no git: nova-sprint merge --stream %s --batch %d records the landing in land's place (land merges and pushes)", stream, len(cards)))
	}
	return strings.Join(why, ", and ") + "; run: nova-sprint land " + strings.Join(flags, " "), also
}

// dryBatch is the dry run's outcome of a batch land can place: it stops where
// land stops before any git, at the first head that is not a commit id
// (headNotCommit, mergeHead's own check and words): the cards before it a
// batch that lands, that card refused as land reports it, with the conflict
// fact land would record and the stream stop; nothing is recorded.
func (l *lander) dryBatch(b landBatch, cards []landCard) (done int, on, ok bool) {
	cut := slices.IndexFunc(cards, func(c landCard) bool { return headNotCommit(b.Stream, c) != "" })
	if cut < 0 {
		b.Status = "ok"
		l.tag(context.Background(), &b, cards)
		l.keep(b)
		return len(cards), true, true
	}
	if cut > 0 {
		before := b
		before.Status, before.Cards, before.IDs = "ok", cut, b.IDs[:cut]
		l.tag(context.Background(), &before, cards[:cut])
		l.keep(before)
	}
	c := cards[cut]
	l.keep(landBatch{Stream: b.Stream, Status: "refused", Cards: 1, IDs: []string{c.id}, WouldRecord: "conflict", Reason: headNotCommit(b.Stream, c), DryRun: true})
	return 0, false, true
}

// headNotCommit is why a card's head cannot be merged whatever origin holds:
// it is not a commit id (a finish without --head records the card's id); ""
// when it is one. land meets it at the card's merge (mergeHead) and its dry
// run before any git (dryBatch), in these words.
//
// A land that meets it records the conflict fact, which stops the stream, so the
// remedy ends with the resume that starts it again: return, rework and resume,
// in that order.
func headNotCommit(stream string, c landCard) string {
	if shaRE.MatchString(c.head) {
		return ""
	}
	return "the head " + dashed(c.head) + " of " + c.id + " is not a commit id (a finish without --head records the card's id); run: nova-sprint return " + c.id +
		" --reason 'its head is not a commit', then nova-sprint rework " + c.id + " --fix 'finish with --head <commit>', then (a land that met it stopped the stream) nova-sprint resume --stream " +
		stream + " --did 'returned " + c.id + " for rework'"
}

// conflictCard is a card that did not merge, with git's words.
type conflictCard struct {
	landCard
	why string
	// kind and paths are the conflict's files, as the merge left them unmerged: "file" when
	// one is a file no generated ledger owns, "ledger" when every one is a ledger whose
	// resolution failed, "" when the card failed for any other cause (sprint.MergeReq,
	// ConflictKind: the conflict rule redoes a file conflict on the tip).
	kind        string
	paths       []string
	emptyCommit bool // the refusal is an empty commit that claims changes: return the card to review, do not stop the stream on a conflict
}

// returnCard returns one card to review when its landing was refused because the
// head's diff from the attempt's start is empty while the result claims changes
// (docs/SPEC-SPRINT.md section 7, the lander's checks). The finding is the return
// reason. A conflict is not recorded and the stream is not stopped: the card goes
// back to review for the coordinator.
func (l *lander) returnCard(stream string, f conflictCard) bool {
	b := landBatch{
		Stream: stream,
		Status: "refused",
		Cards:  1,
		IDs:    []string{f.id},
		Repo:   f.repo,
		Base:   f.base,
		Reason: f.why,
		DryRun: l.dry,
	}
	if l.dry || l.st == nil {
		l.out = append(l.out, b)
		return false
	}
	req := sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{f.id}}, Reason: f.why, Who: l.c.actor}
	step := store.ReturnStep(req)
	plan := step.Plan
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		if why := headWhy(s, stream, []landCard{f.landCard}); why != "" {
			return sprint.Plan{Refused: []sprint.Refusal{{Key: stream, Why: why}}}
		}
		return plan(s)
	}
	named := []string{f.pin()}
	step.Args = store.ArgsOf(struct {
		Req  sprint.ReturnReq
		Pins []string
	}{req, named})
	epoch := l.epoch
	step.Epoch = &epoch
	if l.c.op != "" {
		step.CallerOp = l.c.op + "." + stream + "." + step.Args
	}
	l.a.serial.Lock()
	res, err := l.st.Run(context.Background(), step)
	l.a.serial.Unlock()
	code := stepExit(res, err)
	if code != 0 || len(res.Moved) == 0 {
		b.Reason = f.why + "; the return step did not record it (" + stepWhy(res, err) + "); " + againRemedy(stream)
	}
	l.out = append(l.out, b)
	return code == 0 && len(res.Moved) > 0
}

// conflict reports the card that ended its batch with the conflict fact: the
// step's batch is that one card, by name, at the head that did not merge (a
// replacement attempt is never blamed). A refusal of the card's own head
// stops nothing: the step reworks it at the tip, or returns it for the widen
// rule (sprint's landRefused); the lander's own failure (a ledger it could not
// resolve, a head origin does not hold) stops the stream. true when the step
// recorded it and the stream goes on.
func (l *lander) conflict(stream string, f conflictCard) bool {
	b := landBatch{Stream: stream, Status: "refused", Cards: 1, IDs: []string{f.id}}
	recorded, stopped := l.fact(b, sprint.MergeReq{Stream: stream, Batch: 1, Conflict: f.id, Note: f.why, ConflictKind: f.kind, ConflictPaths: f.paths}, []landCard{f.landCard}, "conflict", f.why)
	return recorded && !stopped
}

// fact reports a fact through the merge step (one that stops the stream, or a
// refusal of the card's own head, which does not), and the batch refused with
// it: whether the step recorded it, and whether it stopped the stream.
func (l *lander) fact(b landBatch, r sprint.MergeReq, pins []landCard, fact, why string) (recorded, stopped bool) {
	b.Status, b.Fact, b.Reason = "refused", fact, why
	r.Who = l.c.actor
	res, err := l.step(r, pins)
	if code := stepExit(res, err); code != 0 {
		b.Fact = ""
		b.Reason = why + "; the merge step did not record it (" + stepWhy(res, err) + "); " + againRemedy(r.Stream)
	}
	b.stopped = slices.ContainsFunc(res.Moved, func(m string) bool { return strings.Contains(m, " stopped: ") })
	l.keep(b)
	return b.Fact != "", b.stopped
}

// baseRefused counts a refusal on the base's gate through the merge step
// (sprint.MergeReq.BaseRefused) and refuses the batch with it: the fact is base when the
// count stopped the stream. The base's one judgment is sprint.LandBaseRefused's: a base whose
// red already stopped another stream refuses this one under that judgment, never stopping it.
func (l *lander) baseRefused(b landBatch, stream, why string) (bool, bool) {
	r := sprint.MergeReq{Stream: stream, Base: b.Base, BaseRefused: l.baseWhy, Who: l.c.actor}
	if l.baseStop {
		r.BaseRed = l.baseWhy
	}
	b.Status, b.Reason = "refused", why
	res, err := l.stepWith(r, nil, sprint.LandBaseRefused)
	switch {
	case stepExit(res, err) != 0:
		b.Reason = why + "; the merge step did not count it (" + stepWhy(res, err) + "); " + againRemedy(stream)
	case slices.ContainsFunc(res.Moved, func(m string) bool { return strings.Contains(m, " stopped: ") }):
		b.Fact = "base"
	}
	l.keep(b)
	return false, true
}

// baseRecheck is each land pass's re-check of the bases that stopped streams
// (docs/SPEC-SPRINT.md section 8, v11-base-red-auto-resume-now; internal/sprint, land_base.go):
// a stream stopped on its base's red gets no pass of its own, so the pass gates the tip of
// each such base, once a base, even when every stream is stopped, and a green tip is recorded
// (sprint.BaseGreen); the tick's base-gate rule then resumes every stream stopped on it. A red tip is gated again no sooner than the base-gate rule's last wait
// (sprint.BaseGateRetries); a dry run, a twin (no git) and the rule turned off re-check
// nothing. What it did is NOTE lines (baseNotes); it changes no exit.
func (l *lander) baseRecheck(ctx context.Context, s *sprint.Snapshot) {
	if l.dry || l.twin || slices.Contains(l.offRules(ctx), sprint.RuleBaseGate) {
		return
	}
	type site struct{ repo, base string }
	var sites []site
	first := map[site]string{}
	for _, st := range sprint.BaseRedStreams(s) {
		ctl := s.StreamCtl(st)
		at := site{base: l.base}
		for _, c := range s.Merge.Cell(st, sprint.Queued) {
			if pr := s.Work.Placed(c.ID); pr != nil {
				cb := swarm.ReadCardBase([]byte(pr.F("brief")))
				at.repo = cb.Repo
				if cb.Ref != "" {
					at.base = cb.Ref
				}
				break
			}
		}
		if b := ctl.F(sprint.FieldBaseGateBase); b != "" {
			at.base = b
		}
		if at.base == "" {
			continue
		}
		if _, ok := first[at]; !ok {
			first[at] = st
			sites = append(sites, at)
		}
	}
	for _, at := range sites {
		if ctx.Err() != nil {
			return
		}
		sha, why := l.baseTip(ctx, at.repo, at.base)
		if why != "" {
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" was not re-checked: "+why)
			continue
		}
		if f := l.baseGateFails[sha]; f != nil && l.clock().Before(f.next) {
			continue
		}
		red, cached := l.baseGateCache[sha]
		if !cached || red != "" {
			dir, _ := l.clone(ctx, at.repo)
			l.gatesBase(at.base)
			red = l.treeGate(ctx, dir, true)
		}
		if ctx.Err() != nil {
			return // an interrupted re-check is no evidence about the base
		}
		if red == benchGateUnavailableWhy {
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" was not re-checked: "+red)
			continue
		}
		if red != "" {
			f := l.baseGateFails[sha]
			if f == nil {
				f = &baseGateFail{}
				l.baseGateFails[sha] = f
			}
			f.n, f.why, f.next = f.n+1, red, l.clock().Add(sprint.BaseGateRetries[len(sprint.BaseGateRetries)-1])
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" still fails its tree gate at "+shortSha(sha)+"; re-checked again at "+f.next.UTC().Format("15:04:05 MST"))
			continue
		}
		l.baseGateCache[sha] = ""
		delete(l.baseGateFails, sha)
		res, err := l.greenStep(sprint.BaseGreenReq{Stream: first[at], Base: at.base, Sha: sha, Who: l.c.actor})
		if code := stepExit(res, err); code != 0 {
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" passes its tree gate again at "+shortSha(sha)+"; the store did not record it ("+stepWhy(res, err)+")")
			continue
		}
		l.baseNotes = append(l.baseNotes, "the base "+at.base+" passes its tree gate again at "+shortSha(sha)+"; its streams resume by rule at the next tick")
	}
}

// baseTip cuts the clone of repo at the tip of base fetched from origin: the tip's commit, or
// why it could not.
func (l *lander) baseTip(ctx context.Context, repo, base string) (sha, why string) {
	dir, why := l.clone(ctx, repo)
	if why == "" {
		why = l.hold(ctx, dir)
	}
	if why != "" {
		return "", why
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" || l.merging(ctx, dir) {
		return "", "the clone " + dir + " is not clean (" + firstLine(out, err) + ")"
	}
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
		return "", "the fetch of " + base + " in " + dir + " failed: " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "switch", "--no-track", "--force-create", "land/base-check", "refs/remotes/origin/"+base); err != nil {
		return "", "the base " + base + " could not be cut in " + dir + ": " + firstLine("", err)
	}
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "the base " + base + " has no tip in " + dir + ": " + firstLine("", err)
	}
	return sha, ""
}

// greenStep records a green base (sprint.BaseGreen) fenced to the epoch land read: its own
// step, as a stopped stream refuses the merge step, and a green base is the fact for it.
func (l *lander) greenStep(r sprint.BaseGreenReq) (store.Result, error) {
	step := store.Step{Verb: "merge base-green", Args: store.ArgsOf(r), Load: []string{sprint.Merge, sprint.Work},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.BaseGreen(s, r) }}
	epoch := l.epoch
	step.Epoch = &epoch
	if l.c.op != "" {
		step.CallerOp = l.c.op + "." + r.Stream + "." + step.Args
	}
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	return l.st.Run(context.Background(), step)
}

// baseGone says the build's failure is the base branch's: origin no longer
// holds refs/heads/<base>. It asks origin once, only after a build failed.
func (l *lander) baseGone(ctx context.Context, dir, base string) bool {
	if base == "" {
		return false
	}
	out, err := l.git(ctx, dir, "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/"+base)
	return err != nil || strings.TrimSpace(out) == ""
}

// missingBase records the one judgment of a land whose base branch is gone
// (sprint.MergeReq.MissingBase): every unlanded card on that base and the
// rebase line that fixes them, through the merge step; the batch is refused
// with git's own words.
func (l *lander) missingBase(b landBatch, why string) (bool, bool) {
	b.Status, b.Reason = "refused", why
	r := sprint.MergeReq{Stream: b.Stream, Base: b.Base, MissingBase: b.Base, Who: l.c.actor}
	res, err := l.step(r, nil)
	if code := stepExit(res, err); code != 0 {
		b.Reason = why + "; the merge step did not record it (" + stepWhy(res, err) + "); " + againRemedy(b.Stream)
	}
	l.out = append(l.out, b)
	return false, true
}

// deadBaseRefused records a batch whose base is not on origin through the merge step
// (sprint.MergeReq.DeadBase): each card marked and one judgment raised for it, the stream not
// stopped, and the batch refused once with the sentence of each card. The land pass skips the
// cards after it (sprint.DeadBaseHeld) and goes on to the rest of the stream; a step that did
// not record it leaves the cards to be tried again by the next pass.
func (l *lander) deadBaseRefused(b landBatch, stream string, cards []landCard) (int, bool, bool) {
	var why []string
	for _, c := range cards {
		why = append(why, sprint.DeadBaseWhy(c.id, b.Base))
	}
	id := "<id>"
	if len(cards) == 1 {
		id = cards[0].id
	}
	b.Status, b.Reason = "refused", strings.Join(why, "; ")+"; run: nova-sprint card base "+id+" <a branch on origin>"
	res, err := l.step(sprint.MergeReq{Stream: stream, DeadBase: b.Base, Who: l.c.actor}, cards)
	if code := stepExit(res, err); code != 0 {
		b.Reason += "; the merge step did not record it (" + stepWhy(res, err) + "); " + againRemedy(stream)
		l.keep(b)
		return 0, false, true
	}
	b.Fact = "dead-base"
	l.keep(b)
	return len(cards), true, true
}

// againRemedy is the one remedy land names when a report did not go through:
// land again, which rereads the current queue and lets its own checks decide.
// A bare merge step is never offered: after a rework the queue starts with the
// same ids at a head the base does not hold, and merge --batch would record it
// past the head guard (tla/Land.tla, idguard).
func againRemedy(stream string) string {
	return "run land again, which rereads the queue and lets its checks decide (where the cards are as they were, their merges and push are no-ops and the report records them; a card reworked since is merged at its new head, or meets a real conflict): nova-sprint land --stream " + stream
}

// landed reports a pushed batch through the merge step; false (and the line
// FAILED, with land again as the remedy) when the store did not take it.
func (l *lander) landed(b landBatch, stream string, pins []landCard) bool {
	ids := make([]string, len(pins))
	for i, c := range pins {
		ids[i] = c.id
	}
	b.Cards, b.IDs = len(ids), ids
	l.stage("report", "merge report")
	start := time.Now()
	res, err := l.step(sprint.MergeReq{Stream: stream, Batch: len(ids), Who: l.c.actor}, pins)
	if b.Times != nil {
		since(&b.Times.Report, start)
	}
	if code := stepExit(res, err); code != 0 || !movedExactly(res.Moved, ids) {
		why := stepWhy(res, err)
		if b.Tip != "" && b.Tip != "-" {
			if err := l.markPushed(stream, b.Tip, pins); err != nil {
				why += "; the timeline was not marked (" + oneline.Err(err) + ")"
			}
		}
		b.Status = "failed"
		b.Reason = sprint.ReportRefusedReason(b.Base, b.Tip, why) + "; " + againRemedy(stream)
		l.keep(b)
		return false
	}
	b.Status = "ok"
	// pushed AND reported: only now are its cards' branches tagged for the cleanup (a
	// batch pushed and not reported keeps them: land is run again and may need the heads)
	l.tag(context.Background(), &b, pins)
	var ends []briefEnd // each card's end, attached to the brief decision it names (briefdecide.go)
	for _, c := range pins {
		label, note := decide.LandLabel(c.attempt)
		ends = append(ends, briefEndOf(c.id, c.primary, decide.End{Label: label, Note: note}))
	}
	b.Also = append(b.Also, l.a.attachBriefs(ends)...)
	l.keep(b)
	// its diffs are scored after the whole pass (landscore.go): a score never holds a landing
	l.toScore = append(l.toScore, scoreJob{at: len(l.out) - 1, stream: stream, pins: pins})
	return true
}

// recordPushed reports cards already marked pushed-unreported, through the merge
// step only, before this pass merges anything. A mark that is still queued after
// that is hidden in this snapshot so the pass does not merge it again; the store
// keeps it for the next land. A dry run hides the same cards and writes nothing.
func (l *lander) recordPushed(ctx context.Context, s *sprint.Snapshot, order []string) (*sprint.Snapshot, bool) {
	if l == nil || s == nil {
		return s, false
	}
	if l.dry {
		l.hidePushed(s, order)
		return s, false
	}
	if l.st == nil || l.a == nil {
		return s, false
	}
	// A returned or reworked card cannot inherit a receipt from its old head.
	// Clear such receipts before choosing batches to recover.
	for _, stream := range order {
		if len(sprint.ClearObsoletePushedUnreported(s, stream).Units) == 0 {
			continue
		}
		epoch := l.epoch
		step := store.Step{Verb: "land", Load: []string{sprint.Work, sprint.Merge}, Epoch: &epoch, Actor: l.c.actor,
			Plan: func(fresh *sprint.Snapshot) sprint.Plan { return sprint.ClearObsoletePushedUnreported(fresh, stream) }}
		l.a.serial.Lock()
		res, runErr := l.st.Run(ctx, step)
		var fresh *sprint.Snapshot
		var loadErr error
		if runErr == nil && len(res.Refused) == 0 {
			fresh, loadErr = l.st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
		}
		l.a.serial.Unlock()
		if runErr != nil || len(res.Refused) != 0 {
			l.keep(landBatch{Stream: stream, Status: "failed", Reason: "obsolete pushed receipt could not be cleared: " + stepWhy(res, runErr)})
			return nil, true
		}
		if loadErr != nil || fresh == nil {
			why := firstLine("", loadErr)
			if why == "" {
				why = "the store returned no snapshot"
			}
			l.keep(landBatch{Stream: stream, Status: "failed", Reason: "obsolete pushed receipt was cleared but its result could not be read: " + why})
			return nil, true
		}
		s = fresh
	}
	type pushed struct {
		stream string
		pins   []landCard
		sha    string
	}
	var batches []pushed
	stuck := false
	for _, stream := range order {
		ids := sprint.PushedUnreportedIDs(s, stream)
		if len(ids) == 0 {
			continue
		}
		pins := l.pinsOf(s, ids)
		if len(pins) == 0 {
			sha := sprint.PushedUnreportedSHA(s, ids[0])
			l.keep(landBatch{Stream: stream, Status: "failed", IDs: ids, Cards: len(ids), Tip: sha,
				Reason: sprint.ReportRefusedReason("-", sha, "the marked card is not merging") + "; " + againRemedy(stream)})
			stuck = true
			continue
		}
		batches = append(batches, pushed{stream: stream, pins: pins, sha: sprint.PushedUnreportedSHA(s, ids[0])})
	}
	if len(batches) == 0 {
		l.hidePushed(s, order)
		return s, stuck
	}
	failed := stuck
	for _, b := range batches {
		ids := make([]string, len(b.pins))
		for i, c := range b.pins {
			ids[i] = c.id
		}
		lb := landBatch{Stream: b.stream, Base: b.pins[0].base, Tip: b.sha, Repo: b.pins[0].repo, Cards: len(ids), IDs: ids}
		res, err := l.step(sprint.MergeReq{Stream: b.stream, Batch: len(ids), Who: l.c.actor}, b.pins)
		if code := stepExit(res, err); code != 0 || !movedExactly(res.Moved, ids) {
			lb.Status = "failed"
			lb.Reason = sprint.ReportRefusedReason(lb.Base, b.sha, stepWhy(res, err)) + "; " + againRemedy(b.stream)
			l.keep(lb)
			failed = true
			continue
		}
		lb.Status = "ok"
		if dir, why := l.clone(ctx, b.pins[0].repo); why == "" {
			lb.Dir = dir
			l.tag(ctx, &lb, b.pins)
		} else {
			lb.Prune = &landPrune{Why: why}
		}
		l.keep(lb)
	}
	l.a.serial.Lock()
	fresh, err := l.st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	l.a.serial.Unlock()
	if err != nil || fresh == nil {
		l.hidePushed(s, order)
		return s, failed
	}
	l.hidePushed(fresh, order)
	return fresh, failed
}

// stillQueued keeps a stream whose queue still holds a card, and a name that is
// no stream (the pass refuses that). A stream just recorded empty is left out,
// so the pass does not say nothing is queued after the record.
func stillQueued(s *sprint.Snapshot, order []string) []string {
	if s == nil {
		return order
	}
	var keep []string
	for _, name := range order {
		if s.StreamCtl(name) == nil || len(landQueue(s, name)) > 0 {
			keep = append(keep, name)
		}
	}
	return keep
}

// pinsOf is the pinned cards of ids that are still merging, read from the
// snapshot, so a record of a pushed batch builds no git.
func (l *lander) pinsOf(s *sprint.Snapshot, ids []string) []landCard {
	if s == nil || s.Work == nil {
		return nil
	}
	var out []landCard
	for _, id := range ids {
		pr := s.Work.Placed(id)
		if pr == nil || pr.Col != sprint.Merging {
			continue
		}
		cb := swarm.ReadCardBase([]byte(pr.F("brief")))
		base := cb.Ref
		if base == "" {
			base = l.base
		}
		out = append(out, landCard{
			id: id, head: pr.F("head"), attempt: pr.F("attempt"),
			repo: cb.Repo, base: base, primary: pr, brief: pr.F("brief"),
		})
	}
	return out
}

// hidePushed takes still-queued marked cards out of this snapshot's merge queue.
// The store is unchanged. The pass then does not merge them again.
func (l *lander) hidePushed(s *sprint.Snapshot, order []string) {
	if s == nil || s.Merge == nil {
		return
	}
	for _, stream := range order {
		for _, id := range sprint.PushedUnreportedIDs(s, stream) {
			m := s.Merge.Placed(id)
			if m == nil {
				continue
			}
			cp := *m
			cp.Col = ""
			if m.Fields != nil {
				cp.Fields = make(map[string]string, len(m.Fields))
				for k, v := range m.Fields {
					cp.Fields[k] = v
				}
			}
			s.Merge.Put(&cp)
		}
	}
}

// markPushed writes pushed-unreported <sha> on the batch's cards and on the timeline.
func (l *lander) markPushed(stream, sha string, pins []landCard) error {
	if l == nil || l.dry || l.st == nil || l.a == nil || sha == "" || len(pins) == 0 {
		return nil
	}
	marked := make([]sprint.PushedPin, len(pins))
	for i, c := range pins {
		marked[i] = sprint.PushedPin{ID: c.id, Head: c.head, Attempt: c.attempt}
	}
	epoch := l.epoch
	step := store.Step{
		Verb:  "land",
		Load:  []string{sprint.Work, sprint.Merge},
		Epoch: &epoch,
		Actor: l.c.actor,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.MarkPushedUnreported(s, stream, sha, marked)
		},
	}
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	res, err := l.st.Run(context.Background(), step)
	if err != nil {
		return err
	}
	if len(res.Refused) > 0 {
		return fmt.Errorf("%s", stepWhy(res, nil))
	}
	return nil
}

// movedExactly says the step's moved lines hold the landings of ids, each exactly once
// (`<id> merging -> landed`) and no other line about one of them, in whatever order: the
// step lands the named cards in the queue's order at the report, which a rank between the
// push and the report can change, and the cards landed are the same cards. Lines about
// other cards are the step's own and allowed: a landing releases every waiting card whose
// needs have all landed (`<id> waiting -> ready (its needs landed)`, steps_work.go) and
// marks the sentinels reached (`sentinel <id> reached`, steps_sentinel.go), in the same
// step; until 2026-10-07 those lines made a committed landing LAND FAILED ("pushed ... and
// NOT reported (moved N+k)"), stopped the stream's next batches, exited 2 and could roll the
// server back. A replayed receipt of another batch names none of ids and is not this one.
func movedExactly(moved, ids []string) bool {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	if len(want) != len(ids) {
		return false // a card named twice is not a batch
	}
	seen := map[string]bool{}
	for _, line := range moved {
		id, rest, _ := strings.Cut(line, " ")
		if !want[id] {
			continue // another card's move, the step's own
		}
		if seen[id] || rest != "merging -> landed" && !strings.HasPrefix(rest, "merging -> landed ") {
			return false
		}
		seen[id] = true
	}
	return len(seen) == len(want)
}

// step runs one merge step, as `merge --stream` runs it, fenced to the epoch
// land read and guarded in the same store step: it plans only while the
// stream's queue holds every pinned card, each at the head and attempt land
// read and pushed, and the batch it records is those cards by name (MergeReq.
// Cards), so a card gone from the queue since the push, or reworked to another
// head, refuses the report and nothing is recorded that was not pushed. The pins are part of
// the step's arguments, and under the caller's --op its op id is the op and
// those arguments, so a replay returns only the receipt of this very batch.
func (l *lander) step(r sprint.MergeReq, pins []landCard) (store.Result, error) {
	return l.stepWith(r, pins, sprint.MergeStep)
}

// stepWith is step with the merge step's plan given: sprint.MergeStep, or
// sprint.LandBaseRefused for a refusal on a red base.
func (l *lander) stepWith(r sprint.MergeReq, pins []landCard, plan func(*sprint.Snapshot, sprint.MergeReq) sprint.Plan) (store.Result, error) {
	// the batch is the pinned cards by name, never the first n of the queue, each with
	// what its landing did past a merge of its head
	r.Cards = make([]string, len(pins))
	for i, c := range pins {
		r.Cards[i] = c.id
		if c.resolved != "" {
			if r.Resolved == nil {
				r.Resolved = map[string]string{}
			}
			r.Resolved[c.id] = c.resolved
		}
	}
	step := store.MergeStep(r)
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		if why := headWhy(s, r.Stream, pins); why != "" {
			return sprint.Plan{Refused: []sprint.Refusal{{Key: r.Stream, Why: why}}}
		}
		return plan(s, r)
	}
	named := make([]string, len(pins))
	for i, c := range pins {
		named[i] = c.pin()
	}
	step.Args = store.ArgsOf(struct {
		Req  sprint.MergeReq
		Pins []string
	}{r, named})
	epoch := l.epoch
	step.Epoch = &epoch
	if l.c.op != "" {
		step.CallerOp = l.c.op + "." + r.Stream + "." + step.Args
	}
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	return l.st.Run(context.Background(), step)
}

// stepWhy is why a step did not move what it was given.
func stepWhy(res store.Result, err error) string {
	if err != nil {
		return oneline.Err(err)
	}
	var why []string
	for _, r := range res.Refused {
		why = append(why, r.Key+": "+r.Why)
	}
	if len(why) == 0 {
		return fmt.Sprintf("moved %d", len(res.Moved))
	}
	return strings.Join(why, "; ")
}

// queueHead is why the stream's merge queue, read again at the epoch land
// read, no longer holds the pinned cards at their heads, or why the push pauses
// (a merge window opened since, or the base's merge queue holding a group:
// docs/SPEC-SPRINT.md section 7, the lander's pause); "" when it does not.
func (l *lander) queueHead(ctx context.Context, stream string, pins []landCard) string {
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Merge, sprint.Work}, nil)
	l.a.serial.Unlock()
	if err != nil {
		return "the merge queue could not be read again at epoch " + strconv.FormatUint(l.epoch, 10) + ": " + oneline.Err(err)
	}
	if why := headWhy(s, stream, pins); why != "" {
		return why
	}
	return l.pause(ctx, s, pins[0].repo, pins[0].base)
}

// headWhy is why the stream's queue no longer holds every pinned card at the
// head and attempt pinned; "" when it does. The cards are looked for by name,
// wherever they stand: the tick's accepts put cards in the queue by their order
// of work while a landing builds, often ahead of the batch, and that is no
// change to the batch: cards are accepted continuously while a landing builds,
// and counting that as a change would refuse almost every landing, or leave a
// pushed batch with nothing to report. A pinned card
// gone from the queue, or reworked to another head, still refuses.
func headWhy(s *sprint.Snapshot, stream string, pins []landCard) string {
	// a stream stopped since land read it (a red recorded while an earlier stream of the
	// same run landed) is not pushed: a stopped stream moves only after resume
	if ctl := s.StreamCtl(stream); ctl != nil && ctl.F("state") == sprint.StreamStopped {
		return "stopped (" + ctl.F("cause") + ") since it was read; run: nova-sprint resume --stream " + stream
	}
	queued := map[string]bool{}
	for _, c := range landQueue(s, stream) {
		queued[c.ID] = true
	}
	for _, c := range pins {
		if !queued[c.id] {
			return fmt.Sprintf("the merge queue of %s no longer holds %s (landed, stuck or returned since it was read); run land again", stream, c.id)
		}
		pr := s.Work.Placed(c.id)
		if pr == nil || pr.F("head") != c.head || pr.F("attempt") != c.attempt {
			return fmt.Sprintf("%s is at attempt %s head %s now, not attempt %s head %s as it was read and built (reworked since); run land again", c.id, dashed(pr.F("attempt")), dashed(pr.F("head")), dashed(c.attempt), dashed(c.head))
		}
	}
	return ""
}

// build cuts the batch branch from origin's base (cut) and merges the cards' heads
// in order (mergeCards), stopping at the first the card itself stops (a head that is
// not a commit on origin, or a merge that left unmerged paths): merged is the cards
// merged, failed the card that ended the batch, why a refusal of the whole batch that
// blames no card (git's own failure: the fetch, the cut, an identity, a hook, the
// disk), nothing to report. baseSha is the base's tip the branch was cut from. With
// gateEach each head's merged tree passes the tree gate before the next is merged
// (gateCard), as every build did before 2026-10-07; without it no head is gated here
// and the caller gates the batch's tree once (landpass.go, merge), except after a base
// cure, which gates each head as before: gated says which the build did. The fetch's
// seconds and the merges' are added to t.
func (l *lander) build(ctx context.Context, dir, stream string, cards []landCard, t *landTimes, gateEach bool) (merged []string, failed conflictCard, baseSha string, gated bool, why string) {
	c, why := l.cut(ctx, dir, stream, cards, t)
	if why != "" {
		return nil, failed, c.baseSha, gateEach, why
	}
	merged, failed, why = l.mergeCards(ctx, dir, stream, cards, c, t, gateEach)
	return merged, failed, c.baseSha, gateEach || c.first > 0, why
}

// landCut is a batch branch cut from the base: the base's tip, and after a base cure the
// cure's id merged first (first = 1), else nothing merged yet.
type landCut struct {
	baseSha string
	merged  []string
	first   int
}

// cut fetches the base and the cards' heads, cuts land/<stream> from the base's tip in dir
// and gates that tip (gateBase, the base's cure included): the cut, or the refusal. It runs
// before the batch's merges, serial across the pass's streams in their order (landpass.go,
// prepare), so the stream that meets a red base first is the first in priority order, as
// before.
func (l *lander) cut(ctx context.Context, dir, stream string, cards []landCard, t *landTimes) (landCut, string) {
	base := cards[0].base
	// THE FETCH BRINGS WHAT THE BATCH NEEDS AND NOTHING ELSE: the base, and the cards'
	// heads by their ids, in one exchange. A fetch of every branch of origin costs a
	// negotiation over all of them, once a stream a round: on a repository with two
	// thousand card branches it was 15 s a fetch and landing fell to a third of the
	// fleet's rate. A head origin
	// does not hold fails the one fetch; then the base alone is fetched and each head at
	// its merge (mergeHead), which names the card.
	baseRef := "+refs/heads/" + base + ":refs/remotes/origin/" + base
	fetch := []string{"fetch", "--no-tags", "origin", baseRef}
	for _, c := range cards {
		// a fetch by id wants the whole id; a short head is found at its merge
		if shaRE.MatchString(c.head) && (len(c.head) == 40 || len(c.head) == 64) {
			fetch = append(fetch, c.head)
		}
	}
	l.stage("fetch", "git fetch")
	start := time.Now()
	l.baseAbsent = false
	_, err := l.fetched(ctx, dir, fetch...)
	if err != nil {
		_, err = l.fetched(ctx, dir, "fetch", "--no-tags", "origin", baseRef)
	}
	since(&t.Fetch, start)
	if err != nil {
		// the base alone fetched and origin holds no such branch: a dead base, the batch's
		// cards' fact, never retried as a fetch that failed (deadBaseRefused)
		l.baseAbsent = containsAny(err.Error(), notOnOrigin)
		return landCut{}, "the fetch of origin in " + dir + " failed: " + firstLine("", err)
	}
	l.stage("merge", "git merge")
	start = time.Now()
	defer since(&t.Merge, start)
	baseSha, why := l.cutBranch(ctx, dir, stream, base)
	if why != "" {
		return landCut{}, why
	}
	merged, first, env, why := l.gateBase(ctx, dir, stream, cards, baseSha)
	switch {
	case env != "":
		return landCut{baseSha: baseSha}, env + "; no card is blamed and nothing was pushed or reported"
	case why != "":
		return landCut{baseSha: baseSha}, why
	}
	return landCut{baseSha: baseSha, merged: merged, first: first}, ""
}

// cutBranch pins the fetched base before changing branches: a push can move the
// remote-tracking ref while another stream cuts. A same-branch -C can also move
// HEAD without replacing the old branch's index and worktree; reset makes the
// branch's three views agree before any card head is merged.
func (l *lander) cutBranch(ctx context.Context, dir, stream, base string) (string, string) {
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "refs/remotes/origin/"+base+"^{commit}")
	if err != nil {
		return "", "the base " + base + " has no tip in " + dir + ": " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "switch", "--no-track", "--force-create", "land/"+stream, sha); err != nil {
		return "", "the base " + base + " could not be cut from origin in " + dir + ": " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "reset", "--hard", sha); err != nil {
		return "", "the branch " + stream + " could not be reset to the base " + base + " in " + dir + ": " + firstLine("", err)
	}
	return sha, ""
}

// mergeCards merges the cards' heads onto the batch branch cut (cut), from the first after
// the cure's: each merged (mergeHead), its maps regenerated where both sides did (remap),
// held to the lander's checks (checkCard), and with gateEach gated (gateCard); after a cure
// each head is gated whatever gateEach says. The results are build's.
func (l *lander) mergeCards(ctx context.Context, dir, stream string, cards []landCard, c landCut, t *landTimes, gateEach bool) (merged []string, failed conflictCard, why string) {
	start := time.Now()
	defer since(&t.Merge, start)
	merged = slices.Clone(c.merged)
	if c.first > 0 {
		gateEach = true // the cure's tree was gated alone; each head after it is, as before
	}
	for i := c.first; i < len(cards); i++ {
		card := &cards[i]
		before, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return nil, failed, "the batch branch has no tip before the merge of " + card.id + ": " + firstLine("", err) + "; no card is blamed and nothing was pushed or reported"
		}
		var refused, env string
		l.conflictKind, l.conflictPaths = "", nil // the merge below says, when it stops on unmerged paths
		refused, env, card.resolved = l.mergeHead(ctx, dir, stream, *card)
		if refused == "" && env == "" && card.resolved == "" {
			// a plain merge of two sides that each regenerated the map (remap)
			var remapped string
			if remapped, env = l.remap(ctx, dir, *card); remapped != "" {
				card.resolved = remapped
				l.ledgerLog = append(l.ledgerLog, card.id+": "+remapped)
			}
		}
		if refused == "" && env == "" {
			var repaired string
			if refused, env, repaired = l.checkCard(ctx, dir, stream, *card, before); repaired != "" {
				card.resolved = strings.TrimPrefix(card.resolved+"; "+repaired, "; ")
				l.ledgerLog = append(l.ledgerLog, card.id+": "+repaired)
			}
		}
		if refused == "" && env == "" && gateEach {
			refused, env = l.gateCard(ctx, dir, *card, before)
		}
		switch {
		case env != "":
			return nil, failed, env + "; no card is blamed and nothing was pushed or reported"
		case refused != "":
			return merged, conflictCard{landCard: *card, why: refused, kind: l.conflictKind, paths: l.conflictPaths, emptyCommit: refused == emptyCommitFinding}, ""
		}
		merged = append(merged, card.id)
	}
	return merged, failed, ""
}

// gateBase is the build's gate of the base's tip, serial for the same commit across the
// pass's streams (the pass's per-commit gate, acquireGate: a commit is gated once, and the
// wait for it ends with the job's context): "" and
// first = 0 when the base is green; else its cure looked for among the batch's cards
// (cureBase), the cure merged and gated alone on the batch branch, merged = its id and
// first = 1 so the build goes on after it; why is the base's refusal with no cure, env a
// failure that is not a card's.
func (l *lander) gateBase(ctx context.Context, dir, stream string, cards []landCard, baseSha string) (merged []string, first int, env, why string) {
	s := l.locks()
	l.beforeWait(stream, "gate")
	release, ok := s.acquireGate(ctx, baseSha)
	if !ok {
		return nil, 0, ctx.Err().Error(), "" // abandoned while waiting for another stream's gate of this commit
	}
	defer release()
	base := cards[0].base
	l.baseStop, l.baseCount, l.baseWhy = false, false, ""
	was := 0
	s.gateMu.Lock()
	if f := l.baseGateFails[baseSha]; f != nil {
		was = f.n
	}
	s.gateMu.Unlock()
	red, stop := l.treeGateBase(ctx, dir, baseSha, base)
	if err := ctx.Err(); err != nil {
		return nil, 0, err.Error(), "" // no cure or base failure for an abandoned gate
	}
	if red == benchGateUnavailableWhy {
		return nil, 0, red, "" // no cure search or red-base retry for a missing bench
	}
	if red == "" {
		return nil, 0, "", ""
	}
	// a queued head whose tree passes the gate the base fails is the base's fix, not a
	// casualty of it: it lands first and the batch goes on after it (sprint.FindBaseCure)
	cured, env := l.cureBase(ctx, dir, stream, cards, baseSha, red)
	if env != "" {
		return nil, 0, env, ""
	}
	if cured < 0 {
		// counted when the gate ran red here (or this process's record stops the stream); a
		// refusal inside a retry's wait, or with the rule off, is not
		s.gateMu.Lock()
		f := l.baseGateFails[baseSha]
		l.baseStop, l.baseCount, l.baseWhy = stop, stop || f != nil && f.n != was, red
		s.gateMu.Unlock()
		return nil, 0, "", "the base " + base + " fails the tree gate at its tip, so no head is merged onto it; fix the base, then run land again: " + red
	}
	cure := cards[cured]
	copy(cards[1:cured+1], cards[:cured])
	cards[0] = cure
	return []string{cure.id}, 1, "", ""
}

// notOnOrigin is how a remote says it holds no object of that id.
var notOnOrigin = []string{"not our ref", "couldn't find remote ref", "no such remote ref", "unadvertised object"}

// mergeHead merges one card's head onto the batch branch. card is why the
// card itself does not merge (its head is not a commit id, origin holds no
// such commit, or the merge stopped on unmerged paths, aborted); env is why
// git failed for any other reason, which is not the card's (an identity, a
// hook, the disk, the network), with any merge in progress aborted; both ""
// when it merged. A head the clone lacks is fetched from origin by its id once.
// A merge stopped only on generated ledgers is resolved (landledger.go,
// resolveLedgers), a shrink-only ledger as the union of both sides' removals
// (ledgerunion.go, unionLedgers), and the agents maps plus a catalog that both
// sides only add rows to as the union of those rows then the map family
// (landledger.go, stageCatalogUnion). note is what the card's timeline says of it.
// mergeHead merges the recorded head of c into the checkout at dir (a plain git
// merge, which takes merge=union for the files .gitattributes marks), deduplicating
// append-only records after the merge and resolving keyed tables, the tables lock,
// and shrink-only ledgers.
func (l *lander) mergeHead(ctx context.Context, dir, stream string, c landCard) (card, env, note string) {
	before, _ := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	l.recLog, l.recNote = nil, ""
	card, env, note = l.mergeHeadLedgers(ctx, dir, stream, c)
	if card != "" || env != "" {
		return card, env, note
	}
	if head, _ := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}"); before == "" || head == before {
		return "", "", note
	}
	lines, dcard, derr := l.dedupeMerge(ctx, dir, before, c)
	if derr != "" {
		return "", derr, ""
	}
	if dcard != "" {
		if _, err := l.git(ctx, dir, "reset", "--hard", before); err != nil {
			return "", "the reset to " + before + " after the dedupe refusal of " + c.id + " failed: " + firstLine("", err), ""
		}
		return dcard, "", ""
	}
	l.ledgerLog = append(l.ledgerLog, append(l.recLog, lines...)...)
	if l.recNote != "" {
		note = strings.TrimPrefix(l.recNote+"; "+note, "; ")
		note = strings.TrimSuffix(note, "; ")
	}
	return "", "", note
}

// mergeHeadLedgers is mergeHead before the append-only records are deduplicated.
func (l *lander) mergeHeadLedgers(ctx context.Context, dir, stream string, c landCard) (card, env, note string) {
	if why := headNotCommit(stream, c); why != "" {
		return why, "", ""
	}
	merge := func() error {
		_, err := l.git(ctx, dir, "merge", "--no-ff", "--no-edit", "-m", "land "+c.id+" (sprint stream "+stream+")", c.head)
		return err
	}
	err := merge()
	if err == nil {
		return "", "", ""
	}
	if _, missing := l.git(ctx, dir, "cat-file", "-e", c.head+"^{commit}"); missing != nil {
		_, ferr := l.fetched(ctx, dir, "fetch", "--no-tags", "origin", c.head)
		switch {
		case ferr != nil && containsAny(ferr.Error(), notOnOrigin):
			// before the card is blamed, every branch is fetched once: a head given
			// short (a fetch by id wants the whole id) or one behind its branch's tip on
			// a remote that serves only advertised refs is on origin all the same
			if _, all := l.fetched(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*"); all != nil {
				return "", "the fetch of origin's branches for the head " + c.head + " of " + c.id + " failed: " + firstLine("", all), ""
			}
			if _, still := l.git(ctx, dir, "cat-file", "-e", c.head+"^{commit}"); still != nil {
				return "the head " + c.head + " of " + c.id + " is missing: origin holds no such commit (" + firstLine("", ferr) + ")", "", ""
			}
		case ferr != nil:
			return "", "the fetch of the head " + c.head + " of " + c.id + " failed: " + firstLine("", ferr), ""
		}
		if err = merge(); err == nil {
			return "", "", ""
		}
	}
	unmerged, uerr := l.git(ctx, dir, "ls-files", "--unmerged")
	_, inMerge := l.git(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	conflict := uerr == nil && unmerged != ""
	why := ""
	if conflict && inMerge == nil {
		paths, ours := unmergedPaths(unmerged)
		l.conflictPaths, l.conflictKind = paths, "file"
		// the append-only records, keyed tables and the tables lock first (landappend.go):
		// resolved and staged; what is left goes on as before, and a merge left with
		// nothing is committed
		var rlines []string
		var rnote, rwhy, renv string
		paths, rlines, rnote, rwhy, renv = l.resolveRecords(ctx, dir, paths)
		switch {
		case renv != "":
			env = renv
			paths = nil
		case rwhy != "":
			why = "; " + rwhy
			paths = nil
		case len(rlines) > 0 && len(paths) == 0:
			msg := []string{"land " + c.id + " (sprint stream " + stream + ")", "The records and the tables lock conflicted and were resolved: " + strings.Join(rlines, "; ") + "."}
			if _, err := l.git(ctx, dir, "commit", "-q", "-m", msg[0], "-m", msg[1]); err != nil {
				env = "the resolved merge of " + c.id + " could not be committed: " + firstLine("", err)
				break
			}
			l.recLog, l.recNote = rlines, rnote
			return "", "", ""
		case len(rlines) > 0:
			l.recLog, l.recNote = rlines, rnote
		}
		if len(paths) == 0 {
			goto abort
		}
		if allLedgers(paths, l.ledgers()) {
			l.conflictKind = "ledger"
		}
		// the shrink-only ledgers first (ledgerunion.go): each resolved as the union of
		// both sides' removals and staged; what is left is the generated ledgers a
		// family regenerates (landledger.go), or a conflict refused as before
		union, rest := unionPaths(paths, l.ledgers())
		lines, uwhy, uenv := []string(nil), "", ""
		if len(union) > 0 {
			lines, uwhy, uenv = l.unionLedgers(ctx, dir, union)
		}
		switch {
		case uenv != "":
			env = uenv
		case uwhy != "":
			why = "; " + uwhy
		case len(union) > 0 && len(rest) == 0:
			msg := unionMessage(c.id, stream, lines)
			if _, err := l.git(ctx, dir, "commit", "-q", "-m", msg[0], "-m", msg[1]); err != nil {
				env = "the resolved merge of " + c.id + " could not be committed: " + firstLine("", err)
				break
			}
			l.ledgerLog = append(l.ledgerLog, lines...)
			return "", "", unionNote(union)
		default:
			var cline, cwhy, cenv string
			rest, cline, cwhy, cenv = l.stageCatalogUnion(ctx, dir, rest)
			switch {
			case cenv != "":
				env = cenv
			case cwhy != "":
				why = "; " + cwhy
			default:
				if owners, outside := ledgerOwners(rest, l.ledgers()); len(outside) == 0 && len(rest) > 0 {
					var renv string
					if note, why, renv = l.resolveLedgers(ctx, dir, stream, c, rest, ours, owners); note != "" {
						if cline != "" {
							lines = append(lines, cline)
							note = catalogUnionNote() + "; " + note
						}
						l.ledgerLog = append(l.ledgerLog, lines...)
						if len(union) > 0 {
							note = unionNote(union) + "; " + note
						}
						return "", "", note
					}
					// the failed resolution ended the merge and restored the clone
					env, why, inMerge = renv, "; "+why, errors.New("no merge in progress")
				}
			}
		}
	}
abort:
	if inMerge == nil {
		if _, aerr := l.git(ctx, dir, "merge", "--abort"); aerr != nil {
			return "", "git merge --abort failed after the merge of " + c.id + " stopped: " + firstLine("", aerr), ""
		}
	}
	switch {
	case env != "":
		return "", env, ""
	case conflict:
		return "the head " + c.head + " of " + c.id + " does not merge: " + firstLine("", err) + why, "", ""
	}
	return "", "the merge of " + c.id + " failed in git, not on its changes: " + firstLine("", err), ""
}

// ledgers is the generated ledgers land regenerates at a merge.
func (l *lander) ledgers() []landLedger {
	if l.a.ledgers != nil {
		return l.a.ledgers
	}
	return landLedgers
}

// emptyCommitFinding is the refusal when a landing's head diff from the attempt's
// start is empty while the result claims changes. It is the return reason, whole.
const emptyCommitFinding = "empty commit: the result claims changes the diff does not show"

var (
	verdictOkRE = regexp.MustCompile(`(?im)^\s*verdict:\s*ok\b`)
	// nothingRE is a result line that says the child found nothing to do: the verdict
	// line, or a line that begins with the finish kind (cardhdr.EndNothing,
	// cardhdr.EndNoCommit), never those words anywhere in prose.
	nothingRE  = regexp.MustCompile(`(?im)^\s*(?:verdict:\s*nothing\b|(?:report:\s*)?(?:nothing[- ]to[- ]do|no commit)\b)`)
	stepShaRE  = regexp.MustCompile(`(?im)^\s*step\b[^\n]*`)
	hexTokenRE = regexp.MustCompile(`\b[0-9a-f]{7,64}\b`)
	diffStatRE = regexp.MustCompile(`(?im)(\b[1-9]\d*\s+files?\s+changed\b|\b[1-9]\d*\s+insertions?\b|\b[1-9]\d*\s+deletions?\b|\b[1-9]\d*\s+lines?\s+added\b|\b[1-9]\d*\s+lines?\s+removed\b|^\s*[\w./-]+\s+\|\s+[1-9]\d*|\bdiff\s*stat:\s*\+?[1-9])`)
	stepDashRE = regexp.MustCompile(`(?im)^\s*step[^\n]*?[-–—]\s*$`)
)

// hasSha says a line holds a token shaped like a commit id: 7 to 64 hex characters
// with a digit in them, so an all-letter word such as "decade" or "defaced" is not one.
func hasSha(line string) bool {
	for _, tok := range hexTokenRE.FindAllString(strings.ToLower(line), -1) {
		if strings.ContainsAny(tok, "0123456789") {
			return true
		}
	}
	return false
}

// claimText is the result the lander holds for a card: what the finished work card
// claimText returns the text checkEmptyCommit reads from the card as finished and
// recorded (the store's head and report fields). It tests the claim against the
// result's own words only, as the card enumerates them (a step line with a commit
// sha, a non-empty diff stat, or a literal `verdict: ok` line), instead of
// synthesizing `verdict: ok` from the finish's ok field.
func claimText(wc *sprint.Card) string {
	if wc == nil {
		return ""
	}
	report := strings.TrimSpace(wc.F("report"))
	return "head: " + wc.F("head") + "\n" + report + "\n"
}

// checkEmptyCommit decides a landing whose head commit's diff from the attempt's start
// commit is empty (docs/SPEC-SPRINT.md section 7, the lander's checks). resultText is
// the attempt's recorded result, the RESULT.md the finish carried (claimText). An empty
// diff with a result that claims changes (a verdict of ok, a step line with a commit sha,
// or a diff stat that is not empty) is refused with emptyCommitFinding. A result that
// claims nothing (a nothing-to-do verdict or report line, every step -) passes. A result
// the lander does not hold cannot be told to claim nothing, so an empty diff with no
// result text is refused too. A non-empty diff passes.
func checkEmptyCommit(emptyDiff bool, resultText string) string {
	if !emptyDiff {
		return ""
	}
	if strings.TrimSpace(resultText) == "" || verdictOkRE.MatchString(resultText) {
		return emptyCommitFinding
	}
	if nothingRE.MatchString(resultText) {
		return ""
	}
	steps := stepShaRE.FindAllString(resultText, -1)
	allDash := len(steps) > 0
	for _, st := range steps {
		if hasSha(st) {
			return emptyCommitFinding
		}
		allDash = allDash && stepDashRE.MatchString(st)
	}
	if allDash {
		return ""
	}
	if diffStatRE.MatchString(resultText) {
		return emptyCommitFinding
	}
	return ""
}

// checkCard is the lander's mechanical checks of one card merged onto the batch branch
// at before (pkg/diffcheck; docs/SPEC-SPRINT.md section 7, the lander's checks): the
// merge's own diff touches no file outside the card's PATHS (E12) and leaves no stranded
// sentence fragment or unmatched backquote (E4), and makes no empty commit whose
// result claims changes (checkEmptyCommit). A card that adds a directory owns its
// catalog row and the AGENTS.md maps (diffcheck.Outside). First the documents the merge
// writes are repaired on the merge commit (sprint.RepairMerge: a stray backquote, an
// open fence, trailing whitespace, a final newline, CRLF), as the ledgers are
// regenerated, and repaired is the landing note that names each repair; a fault with no
// one repair is refused with its line (E4). A Markdown or text file's backquotes are
// judged by the repair, not by E4's count, and a file under the stream's prose globs
// (FieldProse) is not read for them at all. A card that fails is taken off the batch
// branch (reset to before) and ends the batch as a head that does not merge does, with
// what failed; card and env are mergeHead's. A merge that made no commit (the head is in
// the base already) is not checked: it changes nothing the base does not hold.
func (l *lander) checkCard(ctx context.Context, dir, stream string, c landCard, before string) (card, env, repaired string) {
	after, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || after == before {
		return "", "", ""
	}
	var why []string
	prose := l.prose[stream]
	repaired, refused, err := sprint.RepairMerge(dir, func(args ...string) (string, error) { return l.git(ctx, dir, args...) }, before, prose)
	if err != nil {
		if _, rerr := l.git(ctx, dir, "reset", "-q", "--hard", before); rerr != nil {
			return "", "the batch branch could not be reset after the documents of " + c.id + " could not be repaired: " + firstLine("", rerr), ""
		}
		return "", "the documents of the merge of " + c.id + " could not be repaired: " + firstLine("", err), ""
	}
	for _, f := range refused {
		why = append(why, f.String()+" (E4)")
	}
	if after, err = l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return "", "the merge of " + c.id + " has no tip after its repair: " + firstLine("", err), ""
	}
	diff, err := l.git(ctx, dir, "diff", "-M", "--no-color", before, after)
	if err != nil {
		return "", "the diff of the merge of " + c.id + " could not be read: " + firstLine("", err), ""
	}
	tracked, err := l.git(ctx, dir, "ls-tree", "-r", "--name-only", before)
	if err != nil {
		return "", "the files tracked before the merge of " + c.id + " could not be listed: " + firstLine("", err), ""
	}
	beforePaths := []string{}
	if tracked != "" {
		beforePaths = strings.Split(tracked, "\n")
	}
	// SPEC-SPRINT section 7: required validation files are inside every PATHS;
	// ordinary scope amendments remain recorded and unrelated source stays refused.
	amended, out := sprint.LandScope(c.paths, diff, beforePaths)
	if len(out) > 0 {
		why = append(why, "it changes files outside its PATHS (E12): "+strings.Join(out, ", "))
	}
	for _, f := range diffcheck.Fragments(diff) {
		// a document's backquotes are the repair's to judge, a prose file's no one's
		if strings.Contains(f.Why, "code span unmatched") && (sprint.DocFile(f.File) || sprint.DocProse(prose, f.File)) {
			continue
		}
		why = append(why, f.String()+" (E4)")
	}
	// The attempt's start: an earlier attempt's pushed head when the card has one,
	// else where this head left the base. A git error (an unknown sha) leaves the diff
	// unjudged, so this check skips and the other checks stand (docs/SPEC-SPRINT.md
	// section 7, the lander's checks). git diff --quiet exits 0 when no file changed.
	start := c.start
	if start == "" {
		if s, err := l.git(ctx, dir, "merge-base", "refs/remotes/origin/"+c.base, c.head); err == nil && s != "" {
			start = s
		} else if s, err := l.git(ctx, dir, "merge-base", before, c.head); err == nil && s != "" {
			start = s
		} else {
			start = "refs/remotes/origin/" + c.base
		}
	}
	if _, err := l.git(ctx, dir, "diff", "--quiet", start, c.head); err == nil {
		if finding := checkEmptyCommit(true, c.result); finding != "" {
			if _, rerr := l.git(ctx, dir, "reset", "-q", "--hard", before); rerr != nil {
				return "", "the batch branch could not be reset after " + c.id + " failed the lander's checks: " + firstLine("", rerr), ""
			}
			return finding, "", ""
		}
	}
	if len(why) == 0 {
		l.diffs[c.id] = diff
		l.scope[c.id] = amended
		return "", "", repaired
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", before); err != nil {
		return "", "the batch branch could not be reset after " + c.id + " failed the lander's checks: " + firstLine("", err), ""
	}
	return "the head " + c.head + " of " + c.id + " fails the lander's checks: " + strings.Join(why, "; "), "", ""
}

// containsAny says s holds one of words.
func containsAny(s string, words []string) bool {
	return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(s, w) })
}

// runCheck runs --check in the clone: why "" when it passed or there is none, and its
// output.
func (l *lander) runCheck(ctx context.Context, dir string) (why, out string) {
	if l.check == "" {
		return "", ""
	}
	b := subproc.Prepare(ctx, landCheckBudget, "sh", "-c", l.check)
	defer b.Cancel()
	b.Cmd.Dir, b.Cmd.Env = dir, l.a.gitEnv
	raw, err := b.Cmd.CombinedOutput()
	if err = b.Wrap("check "+l.check, err); err != nil {
		return "the check " + l.check + " failed: " + oneline.Err(err) + checkTail(string(raw)), string(raw)
	}
	return "", string(raw)
}

// checkTail is ": <the output's last line>", "" for no output.
func checkTail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return ": " + oneline.Cap(l, 300)
	}
	return ""
}

// clone is the directory a batch of the repository lands in: --repo-dir, else
// a clone kept under the land root, made on first use. Every clone reused,
// given or kept, has its origin read and held to the repository the card
// names before any git touches it, so a batch is never pushed to another
// repository's remote (a card naming none takes --repo-dir's as it is; one
// naming none with no --repo-dir is refused before, by placeWhy). why is a
// refusal.
func (l *lander) clone(ctx context.Context, repo string) (dir, why string) {
	if l.repoDir != "" {
		if l.dry {
			return l.repoDir, ""
		}
		return l.repoDir, l.originIs(ctx, l.repoDir, repo)
	}
	if l.root == "" {
		root, err := l.a.landRoot()
		if err != nil {
			return "", "no directory to keep clones in (" + oneline.Err(err) + "); run: nova-sprint land --repo-dir <clone>"
		}
		l.root = root
	}
	dir = filepath.Join(l.root, repoDirName(repo))
	if l.dry {
		return dir, ""
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return dir, l.originIs(ctx, dir, repo)
	}
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return "", "the land directory " + l.root + " could not be made: " + oneline.Err(err)
	}
	// one branch, never every branch: the landing fetches the base and the heads it needs
	// itself (build), and a clone of a card repository's thousands of branches was 80 s
	if _, err := l.git(ctx, "", "clone", "--no-tags", "--single-branch", "--", repo, dir); err != nil {
		return "", "the clone of " + repo + " into " + dir + " failed: " + firstLine("", err)
	}
	return dir, ""
}

// hold takes the clone's land lock (landLockName in its git directory) for the rest of
// this land, once per clone, before a batch or a base check changes its checkout (add's
// brief lint only fetches into its own refs and reads, and takes none): one land at a time
// works in a clone, so no merge, reset or
// branch of another land's lands in the middle of this one's batch (two landers sharing a
// clone left the server's merge to find the other's MERGE_HEAD, 2026-10-06). A clone
// another land holds is refused before any git changes it, naming the holder; its cards
// stay queued for the next land. release lets every lock go as land ends.
func (l *lander) hold(ctx context.Context, dir string) string {
	if l.held[dir] != nil {
		return ""
	}
	gitDir, err := l.git(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "the clone " + dir + " has no git directory: " + firstLine("", err)
	}
	lock, err := filelock.TryLock(filepath.Join(gitDir, landLockName), "nova-sprint land")
	if h, ok := filelock.AsHeldError(err); ok {
		return "the clone " + dir + " is in use by another land (" + h.Holder.String() + "); nothing was merged, pushed or reported, and its cards stay queued for the next land"
	}
	if err != nil {
		return "the land lock of the clone " + dir + " could not be taken: " + oneline.Err(err)
	}
	if l.held == nil {
		l.held = map[string]*filelock.FileLock{}
	}
	l.held[dir] = lock
	return ""
}

// release lets go of every clone's land lock this land took (hold).
func (l *lander) release() {
	for dir, lock := range l.held {
		// ignored: an unlock that fails leaves nothing held, the kernel lets go as the process ends
		_ = lock.Unlock()
		delete(l.held, dir)
	}
}

// landLockName is the land lock's file in a clone's git directory (holdClone).
const landLockName = "nova-sprint-land.lock"

// merging says a merge is in progress in the clone: a pass cut short between a merge and
// its commit or abort.
func (l *lander) merging(ctx context.Context, dir string) bool {
	_, err := l.git(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil
}

// restoreClone puts the clone the lander keeps under its root back to the fetched base
// before a batch, when a pass cut short (a hand land beside the server's, a crash) left it
// not clean: any merge in progress aborted, the checkout reset to origin's base, untracked
// files removed, inside that clone only. files is every path it discarded, for the LAND CLEANED
// line; why is a refusal when the clone could not be restored. A clone given with
// --repo-dir is the caller's and never comes here (docs/SPEC-SPRINT.md, section 7,
// land-clone-self-heals-r.w1).
func (l *lander) restoreClone(ctx context.Context, dir, base string) (files []string, why string) {
	out, err := l.git(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, "the clone " + dir + " is not clean and its status could not be read: " + firstLine("", err)
	}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		files = append(files, e[3:])
		if e[0] == 'R' || e[0] == 'C' {
			i++ // a rename's or copy's source follows it
		}
	}
	slices.Sort(files)
	files = slices.Compact(files)
	if files == nil {
		files = []string{}
	}
	fail := func(step string, err error) ([]string, string) {
		return nil, "the clone " + dir + " is not clean and could not be restored (" + step + ": " + firstLine("", err) + "); commit or discard its changes, then run land again"
	}
	if l.merging(ctx, dir) {
		if _, err := l.git(ctx, dir, "merge", "--abort"); err != nil {
			return fail("the merge in progress could not be aborted", err)
		}
	}
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
		return fail("the fetch of the base "+base, err)
	}
	if _, err := l.git(ctx, dir, "reset", "--hard", "-q", "refs/remotes/origin/"+base); err != nil {
		return fail("the reset to the base "+base, err)
	}
	if _, err := l.git(ctx, dir, "clean", "-f", "-d", "-q"); err != nil {
		return fail("the removal of untracked files", err)
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return nil, "the clone " + dir + " is not clean after its restore (" + firstLine(out, err) + "); commit or discard its changes, then run land again"
	}
	return files, ""
}

// originIs is why the clone's origin is not where the batch belongs, ""
// when it is: its fetch URL is the repository the card names (any, for a
// card naming none), and its effective push URLs, every one git push would
// write to (remote.origin.pushurl, else the fetch URL), are exactly one, the
// same repository.
func (l *lander) originIs(ctx context.Context, dir, repo string) string {
	run := "; nothing was fetched or pushed; run: nova-sprint land --repo-dir <a clone of " + dashed(repo) + " whose origin fetches and pushes there>"
	fetch, err := l.git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return "the clone " + dir + " has no origin to push to: " + firstLine("", err) + run
	}
	if repo != "" && !sameRepo(fetch, repo) {
		return "the card names the repository " + repo + " and the clone " + dir + " fetches from " + fetch + run
	}
	out, err := l.git(ctx, dir, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return "the push URLs of origin in " + dir + " could not be read: " + firstLine("", err) + run
	}
	pushes := strings.Fields(out)
	if len(pushes) != 1 {
		return fmt.Sprintf("the clone %s pushes origin to %d URLs (%s), and land pushes to one", dir, len(pushes), strings.Join(pushes, ", ")) + run
	}
	if !sameRepo(pushes[0], fetch) {
		return "the clone " + dir + " fetches from " + fetch + " and pushes to " + pushes[0] + run
	}
	return ""
}

// defaultLandRoot is where land keeps its clones: nova-sprint/land in the
// user's cache directory.
func defaultLandRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "nova-sprint", "land"), nil
}

// repoDirName is a repository's clone directory name: its URL or path with
// every character that is not a letter, digit, dot or dash a dash, cut to 48,
// for a person to read, then the first 16 hex digits of the SHA-256 of the
// repository's one spelling (normRepo), so two repositories whose readable
// names agree (a-b/c and a/b-c) never share a clone.
func repoDirName(repo string) string {
	key := normRepo(repo)
	sum := sha256.Sum256([]byte(key))
	name := strings.Trim(notNameRE.ReplaceAllString(key, "-"), "-.")
	if len(name) > 48 {
		name = name[len(name)-48:]
	}
	return strings.TrimLeft(name, "-.") + "-" + hex.EncodeToString(sum[:8])
}

// notNameRE is a run of characters a clone directory's name does not keep.
var notNameRE = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// normRepo is a repository's one spelling, so an https, an ssh and an scp
// spelling of one repository compare equal: the scheme and the user dropped,
// the host lower-cased (a host has no case), the path kept as it is (a path
// may have case: /srv/Repo.git and /srv/repo.git are two repositories), its
// trailing slash and .git cut. A local path is its path.
func normRepo(u string) string {
	u = strings.TrimSpace(u)
	host, path := "", u
	if i := strings.Index(u, "://"); i >= 0 {
		host, path, _ = strings.Cut(u[i+3:], "/")
	} else if colon := strings.Index(u, ":"); colon > 0 && !strings.Contains(u[:colon], "/") {
		host, path = u[:colon], u[colon+1:] // scp-like: [user@]host:path
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	if host == "" {
		return path
	}
	return strings.ToLower(host) + "/" + strings.TrimPrefix(path, "/")
}

// sameRepo says two spellings name one repository.
func sameRepo(a, b string) bool { return normRepo(a) == normRepo(b) }

// rejected says a failed push was refused by the remote (the base moved, or
// a rule on it), not a push that could not reach it.
func rejected(err error) bool {
	return containsAny(err.Error(), []string{"[rejected]", "[remote rejected]", "non-fast-forward", "fetch first"})
}

// git runs one git in dir (none: the current directory, for a clone into a
// path it names), in the caller's environment, and returns its trimmed stdout,
// except -z output whose status columns and paths are byte-exact; an error
// carries git's own words.
func (l *lander) git(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: dir != ""}, args...)
	if err != nil {
		words := strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout))
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, words)
	}
	if slices.Contains(args, "-z") {
		return string(res.Stdout), nil
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// firstLine is a failure's words on one line: the error's, else out's.
func firstLine(out string, err error) string {
	if err != nil {
		var lines []string
		for _, l := range strings.Split(err.Error(), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "hint:") {
				lines = append(lines, l)
			}
		}
		return oneline.Cap(strings.Join(lines, " | "), 400)
	}
	if out == "" {
		return "no output"
	}
	return oneline.Cap(strings.ReplaceAll(out, "\n", " | "), 400)
}

// allLedgers says every path is a ledger: a shrink-only one (ledgerunion.go) or a generated
// one a family regenerates (landledger.go). A conflict on those alone is the lander's own to
// resolve, and one it could not is no conflict the conflict rule redoes.
func allLedgers(paths []string, ledgers []landLedger) bool {
	_, rest := unionPaths(paths, ledgers)
	_, outside := ledgerOwners(rest, ledgers)
	return len(outside) == 0
}

// scopeOf is the merged cards' scope amendments as the batch's line names them,
// card:file, in merge order.
func (l *lander) scopeOf(merged []string) []string {
	var out []string
	for _, id := range merged {
		for _, f := range l.scope[id] {
			out = append(out, id+":"+f)
		}
	}
	return out
}
