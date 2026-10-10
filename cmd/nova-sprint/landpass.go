package main

// landpass.go is the land pass in two phases (tla/LandPass.tla; the owner, 2026-10-07:
// "We can do merges across work streams in parallel. The only thing that needs to be
// serial is the merge after.").
//
// PHASE 1, THE MERGES, IN PARALLEL ACROSS STREAMS. Each stream with a batch gets its own
// worktree of the repository's clone (worktree: <clone>@<stream> under the land root, made
// on first use, kept across passes, removed when the stream has no batch), and in it, beside
// the other streams, cuts land/<stream> from the base's tip on origin and merges its cards'
// heads one by one as before (mergeHead, remap, resolveLedgers, checkCard), then gates the
// batch's tree ONCE (build, vet and the tree tests) instead of once per head; only when that
// one gate is red is each head gated alone again from the base (build with gateEach), so the
// red head is blamed with the finding the lander always gave. A pass of eight batches of two
// cards ran sixteen gates one after another (20 minutes, 2026-10-07); it now runs eight, up
// to --land-parallel at a time. One base commit's gate and cure stay serial (baseGates), the
// fetches and every write of the clone's shared refs too (fetchMu), and each stream's lander
// (fork) has its own scratch and output, so the log keeps its lines and their order.
//
// PHASE 2, THE LANDING, SERIAL. The built batches land onto the base one at a time, as
// they are built: in priority order among the built ones, a batch waiting for a
// higher-priority stream still merging for landGrace at most (2026-10-10, fault item 4:
// before, each landing waited for every stream before it, up to its LandDeadline, and one
// 99-card batch held every later stream's landing). Each job's LandDeadline runs on its own
// clock from when it holds a width slot (launch): a gate past it is abandoned for this pass,
// nothing blamed; a job waiting for a slot, the chain or another stream's gate of its base
// commit runs no clock, the holder's bound freeing it. A stream's next batch (at most
// landBatchMax cards) starts as soon as its last is done. A red batch gate blames its head
// by bisection (bisect), and a bench that could not run the gate (benchFault) blames
// nothing. A batch cut from the tip the base still has is pushed as before,
// with no new gate. When the base moved (a batch before it in this pass landed, or a push
// from outside), the batch is merged again onto the new tip in its worktree, the same merges
// and checks and no gate per head: when the files it changes and the files landed since
// are disjoint and the same cards merged, it is pushed with no new gate (a clean merge of
// disjoint files); else the combined tree is gated once and pushed. A red combined gate
// refuses the batch naming the batches it collided with and leaves its cards queued for
// the next pass; no stream stops for it, and no other stream's outcome changes. A tip whose
// whole tree a gate saw is recorded in baseGateCache (a clean merge of disjoint files is
// not), so the next batch on a gated tip gates no base and a disjoint push is regated. The report is
// the merge step as before (landed), and movedExactly reads the step's receipt for this
// batch's landings alone: the step also releases the waiting cards the landing unblocks and
// marks sentinels reached, lines that are not this batch's and never made it FAILED again.

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// landParallelDefault is how many streams merge at once (--land-parallel).
const landParallelDefault = 4

var errLandDeadline = errors.New("landing gate deadline")

func gateWaitWhy(ctx context.Context) string {
	if ctx.Err() == nil {
		return ""
	}
	if errors.Is(context.Cause(ctx), errLandDeadline) {
		return "the gate of this batch exceeded " + LandDeadline.String() + "; no card was blamed or pushed; run land again"
	}
	return "the gate of this batch was canceled; no card was blamed or pushed; run land again"
}

// landShared is what one pass's streams share while they merge beside each other.
type landShared struct {
	// fetchMu serializes every fetch and every other write of the clone's shared refs and
	// metadata (a worktree added or removed): two fetches of one ref at once fail on its lock.
	fetchMu sync.Mutex
	// gateMu guards the baseGateCache, baseGateFails and cureTried maps and baseGates.
	gateMu sync.Mutex
	// baseGates keeps a gate and its cure single-writer for each base commit: a one-slot
	// channel per commit, so the wait for it watches the job's context (acquireGate). A
	// different base can be checked while one gate waits on a bench.
	baseGates map[string]chan struct{}
	// checkMu serializes a red --check's gate decision, which appends to one record.
	checkMu sync.Mutex
	// treesMu guards pruned and used.
	treesMu sync.Mutex
	// pruned is each clone whose stale worktrees this pass has pruned; used is each clone's
	// streams that worked in a worktree of it this pass, for the removal of the rest.
	pruned map[string]bool
	used   map[string]map[string]bool
}

// locks is the pass's shared locks, made once.
func (l *lander) locks() *landShared {
	if l.shared == nil {
		l.shared = &landShared{pruned: map[string]bool{}, used: map[string]map[string]bool{}}
	}
	return l.shared
}

func (s *landShared) baseGate(sha string) chan struct{} {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if s.baseGates == nil {
		s.baseGates = map[string]chan struct{}{}
	}
	if s.baseGates[sha] == nil {
		s.baseGates[sha] = make(chan struct{}, 1)
	}
	return s.baseGates[sha]
}

// acquireGate takes the base commit's gate for the caller, or returns false when ctx ends
// first (the pass abandoned the job at LandDeadline): a job waiting behind another
// stream's gate of the same commit is never held past its own bound. release puts it back.
func (s *landShared) acquireGate(ctx context.Context, sha string) (release func(), ok bool) {
	gate := s.baseGate(sha)
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, true
	case <-ctx.Done():
		return nil, false
	}
}

// fetched runs one git fetch (or another write of the clone's shared refs) under the pass's
// fetch lock.
func (l *lander) fetched(ctx context.Context, dir string, args ...string) (string, error) {
	s := l.locks()
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	return l.git(ctx, dir, args...)
}

// landJob is one stream's batch through the pass's two phases.
type landJob struct {
	stream string
	cards  []landCard
	ids    []string
	f      *lander // the stream's lander: its own scratch and output (fork)
	b      landBatch
	clone  string // the repository's clone: the fetch and push hub, the land lock's home
	dir    string // the stream's worktree of it, where the batch is built
	// done says phase 1 ended the batch (refused, or reported with a fact): nothing to land;
	// past, on and ok are the batch's outcome as batch reported it before (land.go until
	// 2026-10-07): the stream goes on past its first past cards when on (every card of the
	// batch landed, or the cards before a card whose own refusal was recorded and that card,
	// reworked at the tip or returned to review; build puts a base's cure first, so they are
	// the first past of cards as it leaves them), and ok is false when a push landed and its
	// report did not.
	done, on, ok bool
	past         int
	cut          landCut // the batch branch as prepare cut it: the base's tip, a cure merged first
	gated        bool    // the tip's whole tree passed a gate with the tree tests (phase 1's one gate, or the combined gate)
	merged       []string
	failed       conflictCard
	baseSha, tip string   // the base tip the batch was cut from, and its gated tip
	files        []string // what the tip changes against that base
	// grace is the built batch's landGrace, started when it left phase 1 (pass)
	grace <-chan time.Time
}

// fork is the lander one stream's batch runs in: the pass's settings, store, caches and
// locks, with its own scratch (the merges' notes, the conflict's paths, the gates' bench,
// the stream as its key on the gate bench's hash ring and in its name on a bench's Go lane,
// lander/<stream>) and its own output, merged into the pass's in stream order (pass).
func (l *lander) fork(stream string) *lander {
	f := *l
	f.out, f.toScore = nil, nil
	f.ledgerLog, f.recLog, f.recNote, f.baseFix = nil, nil, "", ""
	f.conflictKind, f.conflictPaths, f.cardTips = "", nil, nil
	f.baseStop, f.baseCount, f.baseWhy = false, false, ""
	f.gateHost, f.gateWall = "", 0
	f.gateKey, f.gateRing, f.gateSlot = stream, 0, 0
	f.laneAs = landLaneWho + "/" + stream
	f.baseNotes = nil
	f.diffs, f.scope = map[string]string{}, map[string][]string{}
	return &f
}

// take folds a job's lander back into the pass's: its lines in order, its scores' places
// moved past the lines already kept, its diffs and scope.
func (l *lander) take(f *lander) {
	for _, j := range f.toScore {
		j.at += len(l.out)
		l.toScore = append(l.toScore, j)
	}
	l.out = append(l.out, f.out...)
	maps.Copy(l.diffs, f.diffs)
	maps.Copy(l.scope, f.scope)
	f.out, f.toScore = nil, nil
}

// openStream reads one stream's queue as the pass lands it: the cards with where each
// lands, or false with the refusal kept (no such stream, stopped, nothing queued).
func (l *lander) openStream(s *sprint.Snapshot, stream string) ([]landCard, bool) {
	refused := func(why string) ([]landCard, bool) {
		l.keep(landBatch{Stream: stream, Status: "refused", IDs: []string{}, Reason: why})
		return nil, false
	}
	ctl := s.StreamCtl(stream)
	switch {
	case ctl == nil:
		return refused("no such stream; run: nova-sprint where")
	case ctl.F("state") == sprint.StreamStopped:
		return refused("stopped (" + ctl.F("cause") + "); run: nova-sprint resume --stream " + stream)
	}
	if l.prose == nil {
		l.prose = map[string][]string{}
	}
	l.prose[stream] = sprint.StreamProse(s, stream)
	queue := landQueue(s, stream)
	if len(queue) == 0 {
		return refused("nothing queued to merge in stream " + stream + "; run: nova-sprint queue --stream " + stream)
	}
	var cards []landCard
	// a card held on a dead base is skipped, and a card that needs one, until its judgment
	// is answered or its base is re-pointed (sprint.DeadBaseHeld): the refusal was said once
	held := map[string]bool{}
	for _, c := range queue {
		lc := landCard{id: c.ID, base: l.base}
		pr := s.Work.Placed(c.ID)
		if pr != nil {
			lc.head, lc.attempt, lc.primary = pr.F("head"), pr.F("attempt"), pr
			cb := swarm.ReadCardBase([]byte(pr.F("brief")))
			lc.repo, lc.paths, lc.brief = cb.Repo, swarm.CardPaths([]byte(pr.F("brief"))), pr.F("brief")
			if cb.Ref != "" {
				lc.base = cb.Ref
			}
			if sprint.DeadBaseHeld(s, pr, lc.base) || slices.ContainsFunc(sprint.Split(pr.F("needs")), func(n string) bool { return held[n] }) {
				held[c.ID] = true
				continue
			}
			if s.Fleet != nil {
				lc.result = claimText(s.Fleet.Card(sprint.WorkCardID(pr.ID, pr.Int("attempt"))))
				if pr.Int("attempt") > 1 {
					var earlier []*sprint.Card
					for k := 1; k < pr.Int("attempt"); k++ {
						if w := s.Fleet.Card(sprint.WorkCardID(pr.ID, k)); w != nil {
							earlier = append(earlier, w)
						}
					}
					if b := sprint.BaseOf(earlier); b.Head != "" {
						lc.start = b.Head
					}
				}
			}
		}
		if lc.protected = sprint.ProtectedLandWhy(s, stream, lc.repo, lc.base, c.ID); lc.protected != "" {
			// the refusal names the promotion flag beside the mark (docs/SPEC-SPRINT.md
			// section 7, protected-bases-pb-b.w2)
			lc.protected += "; or mark it the promotion stream, for every repository: nova-sprint stream set " + stream + " --promotion"
		}
		cards = append(cards, lc)
	}
	return cards, true
}

// landBatchMax is the most cards one batch merges. v1-4's batch of 99 cards spent 708 s
// merging and gating (2026-10-10), past LandDeadline: abandoned, then cut again the same way
// the next pass, the stream in merging for hours. A stream's next batch starts as soon as
// its last one lands (pass), so a long queue lands in batches that each fit the bound.
const landBatchMax = 16

// batchOf is the next batch of a stream's cards: the run of consecutive cards from the
// first naming its repository and base, at most landBatchMax.
func batchOf(cards []landCard) int {
	n := 1
	for n < len(cards) && n < landBatchMax && cards[n].repo == cards[0].repo && cards[n].base == cards[0].base {
		n++
	}
	return n
}

// pass lands every stream of order: round after round, each stream's next batch merged
// beside the others' (phase 1, merges), then the green ones landed one at a time in order
// (phase 2, land), a stream going on past the cards its batch landed and past a card whose
// own refusal was recorded (reworked at the tip, or returned to review: the stream's other
// cards land in the same pass), and ending at the lander's own failure, as stream did.
// failed says a push landed and its report did not.
func (l *lander) pass(ctx context.Context, s *sprint.Snapshot, order []string) (failed bool) {
	queues := map[string][]landCard{}
	var streams []string
	for _, name := range order {
		if cards, ok := l.openStream(s, name); ok {
			queues[name], streams = cards, append(streams, name)
		}
	}
	var pushed []*landJob // the batches landed in this pass, in order: what a later one may collide with
	next := func(name string) *landJob {
		cards := queues[name]
		if len(cards) == 0 {
			return nil
		}
		n := batchOf(cards)
		ids := make([]string, n)
		for i, c := range cards[:n] {
			ids[i] = c.id
		}
		return &landJob{stream: name, cards: cards[:n], ids: ids, f: l.fork(name),
			b: landBatch{Stream: name, Status: "refused", Cards: n, IDs: ids, Repo: cards[0].repo, Base: cards[0].base, DryRun: l.dry}}
	}
	prio := map[string]int{}
	for i, name := range streams {
		prio[name] = i
	}
	m := &mergeRun{finished: make(chan *landJob), sem: make(chan struct{}, max(l.parallel, 1)), prior: map[string]chan struct{}{}}
	running := map[*landJob]bool{}
	start := func(jobs []*landJob) {
		// prepare is serial, in priority order (the clone, its lock, the worktree)
		for _, j := range jobs {
			l.prepare(ctx, s, j)
		}
		for _, j := range jobs {
			running[j] = true
			l.launch(ctx, m, j)
		}
	}
	var first []*landJob
	for _, name := range streams {
		if j := next(name); j != nil {
			first = append(first, j)
		}
	}
	start(first)
	// THE LANDINGS, SERIAL, AS EACH BATCH IS BUILT (2026-10-10, fault item 4): this loop
	// alone pushes, one batch at a time. A built batch lands in priority order among the
	// built ones, and waits for a higher-priority stream still in phase 1 for landGrace at
	// most, never for that stream's whole gate: before, every landing waited in priority
	// order for each stream before it, up to its LandDeadline, so v1-4's 99-card batch
	// (708 s) held every later stream's landing. A stream's next batch starts as soon as its
	// last one is done.
	var ready []*landJob
	for len(running) > 0 || len(ready) > 0 {
		if len(ready) == 0 {
			j := <-m.finished
			delete(running, j)
			j.grace = l.graceClock(j.stream)
			ready = append(ready, j)
			continue
		}
		slices.SortStableFunc(ready, func(a, b *landJob) int { return prio[a.stream] - prio[b.stream] })
		j := ready[0]
		ahead := false
		for r := range running {
			ahead = ahead || prio[r.stream] < prio[j.stream]
		}
		if ahead && !j.done && j.grace != nil {
			select {
			case f := <-m.finished:
				delete(running, f)
				f.grace = l.graceClock(f.stream)
				ready = append(ready, f)
				continue
			case <-j.grace:
				j.grace = nil // its grace spent: it lands now
			}
		}
		ready = ready[1:]
		if !j.done {
			l.land(ctx, j, pushed)
		}
		l.take(j.f)
		if j.b.Tip != "" && !l.dry {
			// what this pass put on the base, a whole batch or the heads before a conflict:
			// the files a later batch may collide with
			pushed = append(pushed, j)
		}
		switch {
		case !j.ok:
			failed = true
			queues[j.stream] = nil
		case j.on:
			queues[j.stream] = queues[j.stream][j.past:]
		default:
			queues[j.stream] = nil
		}
		if nj := next(j.stream); nj != nil {
			start([]*landJob{nj})
		}
	}
	m.wg.Wait()
	return failed
}

// refuse ends the job in phase 1 with a refusal: nothing pushed or reported for it.
func (j *landJob) refuse(why string) {
	j.b.Reason = why
	j.f.keep(j.b)
	j.done, j.past, j.on, j.ok = true, 0, false, true
}

// ended ends the job with the outcome a report gave it: the cards the stream goes past, whether
// it goes on, and whether a push landed was reported (batch's done, on, ok).
func (j *landJob) ended(past int, on, ok bool) { j.done, j.past, j.on, j.ok = true, past, on, ok }

// ownRefusal reports the card that ended its batch with its own refusal, as batch did: an
// empty commit whose result claims changes returns the card to review (returnCard); any other
// refusal is the conflict fact (conflict), which reworks the card at the tip and stops
// nothing, or stops the stream for the lander's own failure (a ledger it could not resolve, a
// head origin does not hold). true when the stream goes on.
func (l *lander) ownRefusal(stream string, f conflictCard) bool {
	if f.emptyCommit {
		return l.returnCard(stream, f)
	}
	return l.conflict(stream, f)
}

// prepare is the batch before any cut or merge, serial across the streams in their order: the
// refusals before git (placeWhy, pause), the clone and its lock, the clone restored when a
// pass cut short left it dirty (restoreClone, LAND CLEANED), and the stream's worktree made
// or cleaned. The cut and base gate run with the stream's merge in prepareCut. A dry run
// ends here (dryBatch).
func (l *lander) prepare(ctx context.Context, s *sprint.Snapshot, j *landJob) {
	b := &j.b
	if why, also := l.placeWhy(j.stream, j.cards); why != "" {
		b.Also = also
		j.refuse(why)
		return
	}
	// a merge window open, or the base's merge queue holding a group, pauses the batch before
	// any git; the queue is asked again just before the push (queueHead)
	if why := l.pause(ctx, s, b.Repo, b.Base); why != "" {
		j.refuse(why)
		return
	}
	clone, why := l.clone(ctx, b.Repo)
	if why != "" {
		j.refuse(why)
		return
	}
	j.clone, b.Dir = clone, clone
	if l.dry {
		j.ended(j.f.dryBatch(*b, j.cards))
		return
	}
	if why := l.hold(ctx, clone); why != "" {
		j.refuse(why)
		return
	}
	if out, err := l.git(ctx, clone, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" || l.repoDir == "" && l.merging(ctx, clone) {
		if err != nil || l.repoDir != "" {
			j.refuse("the clone " + clone + " is not clean (" + firstLine(out, err) + "); commit or discard its changes, then run land again")
			return
		}
		files, why := l.restoreClone(ctx, clone, b.Base)
		if why != "" {
			j.refuse(why)
			return
		}
		b.Cleaned = files
	}
	dir, why := l.worktree(ctx, clone, j.stream, b.Base)
	if why != "" {
		j.refuse(why)
		return
	}
	j.dir = dir
	b.Times = &landTimes{}
	// The cut includes the base gate. Run it with this stream's merge so a
	// stalled gate cannot hold the next stream's cut in serial preparation.
}

// prepareCut cuts the branch and gates its base in this stream's bounded worker.
func (l *lander) prepareCut(ctx context.Context, j *landJob) {
	b := &j.b
	c, why := l.cut(ctx, j.dir, j.stream, j.cards, b.Times)
	if why := gateWaitWhy(ctx); why != "" {
		j.refuse(why)
		return
	}
	if why != "" {
		l.buildFailed(ctx, j, why)
		return
	}
	j.cut = c
}

// buildNotes moves a build's notes onto the batch: the ledgers it resolved, the base fix it
// landed first, and the cards' order after a cure.
func (l *lander) buildNotes(j *landJob) {
	j.b.Also, l.ledgerLog = append(j.b.Also, l.ledgerLog...), nil
	if l.baseFix != "" {
		j.b.Also, l.baseFix = append(j.b.Also, l.baseFix), ""
	}
	for i, c := range j.cards {
		j.ids[i] = c.id // a base fix lands first (cureBase)
	}
}

// buildFailed ends the job on a build that refused the whole batch and blamed no card:
// counted under the base-gate rule when the base's gate ran red (baseRefused), the one
// judgment of a base branch that is gone (missingBase), else the refusal as it is.
func (l *lander) buildFailed(ctx context.Context, j *landJob, why string) {
	l.buildNotes(j)
	if l.baseAbsent {
		// the base is not on origin: one fact, recorded once, and the stream goes on
		past, on, ok := l.deadBaseRefused(j.b, j.stream, j.cards)
		j.ended(past, on, ok)
		return
	}
	if l.baseCount {
		// the base-gate rule: the refusal counted per stream and base in the store, its third
		// (or this process's third failure) stopping the stream with the coordinator's judgment
		l.baseRefused(j.b, j.stream, why)
		j.ended(0, false, true)
		return
	}
	if strings.Contains(why, "couldn't find remote ref") && l.baseGone(ctx, j.dir, j.b.Base) {
		// the base branch is gone (merged and deleted): one judgment names every
		// card on it and the rebase line that fixes them (rebase.go)
		l.missingBase(j.b, why)
		j.ended(0, false, true)
		return
	}
	j.refuse(why)
}

// worktree is the stream's working directory in the repository's clone: a git worktree
// under the land root named <clone's directory name>@<stream>, made on first use (detached,
// at the clone's tip; build cuts the batch branch in it) and kept across passes, restored to
// the base when a pass cut short left it dirty. The clone itself holds no branch while its
// worktrees work (switch --detach): a branch checked out there could not be cut in a
// worktree. Every write of the clone's worktree list is under the fetch lock. The stale
// worktrees the clone lists (directories gone) are pruned once a pass.
func (l *lander) worktree(ctx context.Context, clone, stream, base string) (dir, why string) {
	root := l.root
	if root == "" {
		var err error
		if root, err = l.a.landRoot(); err != nil {
			return "", "no directory to keep the stream's worktree in (" + oneline.Err(err) + ")"
		}
		l.root = root
	}
	dir = worktreeDir(root, clone, stream)
	s := l.locks()
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	s.treesMu.Lock()
	if s.used[clone] == nil {
		s.used[clone] = map[string]bool{}
	}
	s.used[clone][stream] = true
	prune := !s.pruned[clone]
	s.pruned[clone] = true
	s.treesMu.Unlock()
	if prune {
		if _, err := l.git(ctx, clone, "worktree", "prune"); err != nil {
			return "", "the worktrees of the clone " + clone + " could not be pruned: " + firstLine("", err)
		}
	}
	if branch, err := l.git(ctx, clone, "branch", "--show-current"); err == nil && branch != "" {
		if _, err := l.git(ctx, clone, "switch", "--detach"); err != nil {
			return "", "the clone " + clone + " could not be detached from " + branch + ": " + firstLine("", err)
		}
	}
	listed, err := l.worktrees(ctx, clone)
	if err != nil {
		return "", "the worktrees of the clone " + clone + " could not be listed: " + firstLine("", err)
	}
	if !slices.ContainsFunc(listed, func(p string) bool { return samePath(p, dir) }) {
		if _, err := os.Stat(dir); err == nil {
			// a directory git does not know under the lander's own root: a worktree whose
			// record was pruned, the lander's to remove (strictly under its root, safepath)
			if err := safepath.RemoveUnder(root, dir); err != nil {
				return "", "the stale worktree " + dir + " could not be removed: " + oneline.Err(err)
			}
		}
		if _, err := l.git(ctx, clone, "worktree", "add", "--detach", dir, "HEAD"); err != nil {
			return "", "the worktree " + dir + " of the clone " + clone + " could not be made: " + firstLine("", err)
		}
		return dir, ""
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" || l.merging(ctx, dir) {
		if _, why := l.restoreClone(ctx, dir, base); why != "" {
			return "", strings.Replace(why, "the clone "+dir, "the worktree "+dir, 1)
		}
	}
	return dir, ""
}

// worktreeName is the start of a clone's worktree directory names under the land root:
// the clone's own directory name when the lander keeps it there, else the clone's path as
// a directory name (repoDirName: readable, and a hash so two paths never share one).
func worktreeName(root, clone string) string {
	if samePath(filepath.Dir(clone), root) {
		return filepath.Base(clone)
	}
	return repoDirName(clone)
}

// worktreeDir is where a stream's batch of the repository at clone is built: under the land
// root, <worktreeName>@<stream>.
func worktreeDir(root, clone, stream string) string {
	return filepath.Join(root, worktreeName(root, clone)+"@"+stream)
}

// worktrees is the clone's worktrees other than itself, as git lists them (git prints a
// path with its links resolved: compare with samePath).
func (l *lander) worktrees(ctx context.Context, clone string) ([]string, error) {
	out, err := l.git(ctx, clone, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && !samePath(p, clone) {
			dirs = append(dirs, p)
		}
	}
	return dirs, nil
}

// samePath says two paths name one directory, their links resolved where they exist (a
// temporary directory under a linked root is listed by git under its real root).
func samePath(a, b string) bool {
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return real(a) == real(b)
}

// pruneWorktrees removes, as the pass ends, each clone's worktrees for streams that had
// no batch in it (git worktree remove --force): the next pass makes them again on demand.
// A removal that fails is a NOTE, never an exit.
func (l *lander) pruneWorktrees(ctx context.Context) {
	s := l.locks()
	s.treesMu.Lock()
	used := maps.Clone(s.used)
	s.treesMu.Unlock()
	for _, clone := range slices.Sorted(maps.Keys(used)) {
		name := worktreeName(l.root, clone)
		dirs, err := l.worktrees(ctx, clone)
		if err != nil {
			continue
		}
		for _, dir := range dirs {
			if !samePath(filepath.Dir(dir), l.root) {
				continue
			}
			stream, ok := strings.CutPrefix(filepath.Base(dir), name+"@")
			if !ok || used[clone][stream] {
				continue
			}
			if _, err := l.fetched(ctx, clone, "worktree", "remove", "--force", dir); err != nil {
				l.baseNotes = append(l.baseNotes, "the worktree "+dir+" of a stream with no batch was not removed: "+firstLine("", err))
			}
		}
	}
}

// beforeWait runs the test seam, when set, just before a job waits for the chain, a slot or
// another stream's gate of its base commit (what: chain, slot, gate).
func (l *lander) beforeWait(stream, what string) {
	if l.a != nil && l.a.beforeWait != nil {
		l.a.beforeWait(stream, what)
	}
}

// mergeRun is one pass's phase 1: the jobs' exits (finished, read by the pass alone), the
// width slots, and each repository/base's last cut, which the next job of that pair waits
// for (the same repository/base cuts in priority order).
type mergeRun struct {
	finished chan *landJob
	sem      chan struct{}
	prior    map[string]chan struct{}
	wg       sync.WaitGroup
}

// landGrace is how long a built batch waits for a higher-priority stream still in phase 1
// before it lands ahead of it: priority order kept when it costs little, and no slow gate
// holding another stream's landing for more than this (pass).
const landGrace = 30 * time.Second

// graceClock is the built batch's landGrace: the test seam's when set.
func (l *lander) graceClock(stream string) <-chan time.Time {
	if l.a != nil && l.a.landGrace != nil {
		return l.a.landGrace(stream)
	}
	return time.After(landGrace)
}

// landClock is the job's LandDeadline: the test seam's when set, per stream or for all.
func (l *lander) landClock(stream string) <-chan time.Time {
	if l.a != nil && l.a.landDeadline != nil {
		return l.a.landDeadline(stream)
	}
	if l.a != nil && l.a.after != nil {
		return l.a.after(LandDeadline)
	}
	return time.After(LandDeadline)
}

// launch is one job's phase 1 in its own goroutine, worktree and lander: it waits for the
// last cut of its repository/base, then a width slot, then cuts, merges and gates. The job's
// LandDeadline runs on its own clock from the moment it holds a slot (2026-10-10): its gate
// past the bound is cancelled and the batch refused for this pass with its cards still
// queued, nothing blamed. A job waiting for the chain, a slot or another stream's gate of its
// base commit runs no clock: the holder's own bound frees the wait, so no stream waits on
// another for more than one bound at a time and no clock is spent standing in line. The
// slot is given back before the job's exit is sent, so a job built while the pass lands
// another holds nothing the others need. A job ended in prepare is sent at once.
func (l *lander) launch(pass context.Context, m *mergeRun, j *landJob) {
	m.wg.Add(1)
	if j.done {
		go func() {
			defer m.wg.Done()
			m.finished <- j
		}()
		return
	}
	key := normRepo(j.b.Repo) + "\x00" + j.b.Base
	wait := m.prior[key]
	cutDone := make(chan struct{})
	m.prior[key] = cutDone
	go func() {
		defer m.wg.Done()
		var cutOnce sync.Once
		cutIsDone := func() { cutOnce.Do(func() { close(cutDone) }) }
		ctx, cancel := context.WithCancelCause(pass)
		defer func() {
			cancel(nil)
			cutIsDone()
			m.finished <- j
		}()
		if wait != nil {
			l.beforeWait(j.stream, "chain")
			select {
			case <-wait: // same repository/base cuts in priority order
			case <-ctx.Done():
				j.refuse(gateWaitWhy(ctx))
				return
			}
		}
		l.beforeWait(j.stream, "slot")
		select {
		case m.sem <- struct{}{}:
		case <-ctx.Done():
			j.refuse(gateWaitWhy(ctx))
			return
		}
		held := make(chan struct{})
		go func() {
			select {
			case <-l.landClock(j.stream):
				cancel(errLandDeadline)
			case <-held:
			}
		}()
		defer func() { close(held); <-m.sem }()
		j.f.prepareCut(ctx, j)
		cutIsDone()
		if !j.done {
			j.f.merge(ctx, j)
		}
	}()
}

// merge is one job's phase 1, in the stream's lander and worktree, beside the other
// streams': the heads merged and checked onto the branch prepare cut (mergeCards), the
// batch's tree gated once as a whole, its heads gated one by one only when that gate is
// red (build with gateEach, to blame the head), then --check run. It ends the job on a
// refusal or a fact; a built batch waits for phase 2 (land).
func (l *lander) merge(ctx context.Context, j *landJob) {
	b, stream, dir := &j.b, j.stream, j.dir
	baseSha := j.cut.baseSha
	merged, failed, why := l.mergeCards(ctx, dir, stream, j.cards, j.cut, b.Times, false)
	if why := gateWaitWhy(ctx); why != "" {
		j.refuse(why)
		return
	}
	if why == "" && j.cut.first == 0 && len(merged) > 0 {
		// one gate on the batch's tree, the tree tests included (a tip pushed as gated is
		// cached, phase 2); red, each head is gated alone from the base again, as before, and
		// the red one ends the batch with the finding (those gates run the tree tests only
		// where a head changed what they read, so that tip is not recorded as gated)
		start := time.Now()
		l.stage("gate", "the batch's tree")
		red := l.treeGate(ctx, dir, true)
		since(&b.Times.Merge, start)
		if why := gateWaitWhy(ctx); why != "" {
			j.refuse(why)
			return
		}
		if red == benchGateUnavailableWhy {
			j.refuse(red + "; no card is blamed and nothing was pushed or reported")
			return
		}
		if red == "" {
			j.gated = true
		} else {
			// red: the head that turned the tree red is found by bisection over the tips the
			// merges left (bisect, tla/LandBisect.tla), at most ceil(log2 n) gates where gating
			// each head again from the base took n (99 heads ran past LandDeadline, 2026-10-10);
			// it is returned with its finding, and the prefix before it, which passed a gate,
			// goes on to land
			start := time.Now()
			k, finding, gated, env := l.bisect(ctx, dir, l.cardTips, len(merged), red)
			since(&b.Times.Merge, start)
			if why := gateWaitWhy(ctx); why != "" {
				j.refuse(why)
				return
			}
			if env != "" {
				j.refuse(env + "; no card is blamed and nothing was pushed or reported")
				return
			}
			if k < len(merged) {
				c := j.cards[k]
				merged, failed = merged[:k], conflictCard{landCard: c, why: "the head " + c.head + " of " + c.id + " fails the tree gate: " + finding}
			}
			j.gated = gated
		}
	}
	if why != "" {
		l.buildFailed(ctx, j, why)
		return
	}
	l.buildNotes(j)
	if len(merged) == 0 {
		j.ended(1, l.ownRefusal(stream, failed), true)
		return
	}
	b.Scope = l.scopeOf(merged)
	// the batch as built: this commit is what is pushed and reported, whatever the worktree's
	// checkout becomes after
	tip, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		j.refuse("the batch branch has no tip: " + firstLine("", err))
		return
	}
	l.stage("check", "check")
	start := time.Now()
	why, out := l.runCheck(ctx, dir)
	if why := gateWaitWhy(ctx); why != "" {
		j.refuse(why)
		return
	}
	if why != "" {
		why = l.checkDecided(ctx, dir, stream, b.Base, tip, j.cards[:len(merged)], why, out)
	}
	since(&b.Times.Check, start)
	if why != "" {
		b.Cards, b.IDs = len(merged), j.ids[:len(merged)]
		l.fact(*b, sprint.MergeReq{Stream: stream, Batch: len(merged), Red: true, Note: why}, j.cards[:len(merged)], "red", why)
		j.ended(0, false, true)
		return
	}
	files, err := l.git(ctx, dir, "diff", "--name-only", "-M", baseSha, tip)
	if err != nil {
		j.refuse("the files the batch changes could not be listed: " + firstLine("", err))
		return
	}
	j.merged, j.failed, j.baseSha, j.tip, j.files = merged, failed, baseSha, tip, lines(files)
}

// bisect finds the head that turned a red batch's tree red (tla/LandBisect.tla). tips[i] is
// the batch branch's tip before head i merged (tips[0] the base, whose tree passed its gate)
// and tips[n] the tip after the last of the n heads merged, whose tree's gate with the tree
// tests is red. A tip is gated with the tree tests only when the heads up to it changed a
// file they read (treeTested), as a head was gated alone before 2026-10-10: a batch that
// changes none of them is gated once more without them, and green there it lands whole
// (k = n). The search keeps the tip before head lo passing and the tip after head hi red,
// gating the middle tip: at most ceil(log2 n) gates. k is the head blamed, finding the
// gate's finding on the tip it turned red, and the batch branch is left at the tip before
// it (the prefix that passed); gated says that prefix passed a gate with the tree tests
// (its tip may be recorded as gated). env is a refusal that blames no head: a bench that did
// not run the gate, a cancelled gate, or a branch git could not move.
func (l *lander) bisect(ctx context.Context, dir string, tips []string, n int, red string) (k int, finding string, gated bool, env string) {
	if n < 1 || len(tips) < n+1 {
		return 0, "", false, "the tips of the batch's merges were not recorded, so its red gate blames no head"
	}
	tested := func(i int) (bool, string) {
		changed, err := l.git(ctx, dir, "diff", "--name-only", "-M", tips[0], tips[i])
		if err != nil {
			return false, "the files the batch changes up to head " + strconv.Itoa(i) + " could not be listed: " + firstLine("", err)
		}
		return slices.ContainsFunc(strings.Split(changed, "\n"), treeTested), ""
	}
	gate := func(i int) (string, bool, string) {
		if _, err := l.git(ctx, dir, "reset", "-q", "--hard", tips[i]); err != nil {
			return "", false, "the batch branch could not be moved to the tip after head " + strconv.Itoa(i) + " for the bisection of its red gate: " + firstLine("", err)
		}
		tests, why := tested(i)
		if why != "" {
			return "", false, why
		}
		l.stage("gate", "bisect: the tip after head "+strconv.Itoa(i)+" of "+strconv.Itoa(n))
		red := l.treeGate(ctx, dir, tests)
		if wait := gateWaitWhy(ctx); wait != "" {
			return "", false, wait
		}
		if red == benchGateUnavailableWhy {
			return "", false, red
		}
		return red, tests, ""
	}
	if tests, why := tested(n); why != "" {
		return 0, "", false, why
	} else if !tests {
		// the batch's red came from tree tests that read none of its files
		r, _, env := gate(n)
		if env != "" {
			return 0, "", false, env
		}
		if r == "" {
			return n, "", false, ""
		}
		red = r
	}
	lo, hi, finding := 0, n-1, red
	gated = true // the base passed its gate with the tree tests
	for lo < hi {
		mid := (lo + hi) / 2
		why, tests, env := gate(mid + 1)
		if env != "" {
			return 0, "", false, env
		}
		if why != "" {
			hi, finding = mid, why
		} else {
			lo, gated = mid+1, tests
		}
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", tips[lo]); err != nil {
		return 0, "", false, "the batch branch could not be reset to the tip before " + strconv.Itoa(lo+1) + " after the bisection: " + firstLine("", err)
	}
	return lo, finding, gated && lo > 0, ""
}

// checkDecided is a red --check's gate decision (gateRerun), one stream at a time: the
// decision appends to one record under the land root, and the streams merge beside each
// other.
func (l *lander) checkDecided(ctx context.Context, dir, stream, base, tip string, cards []landCard, why, out string) string {
	s := l.locks()
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	return l.gateRerun(ctx, dir, stream, base, tip, cards, why, out)
}

// lines is out split into its non-empty lines.
func lines(out string) []string {
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ls = append(ls, l)
		}
	}
	return ls
}

// land is one job's phase 2, serial: when a batch landed before it in this pass moved its
// base, the base's tip is fetched and the batch merged again onto it (again); then the
// queue read again, the push, and the report. A push rejected from outside is met once
// more (the base fetched and the batch merged again), then reported with the rejected
// fact, as before. A batch whose base nothing moved is pushed with the one fetch its build
// made.
func (l *lander) land(ctx context.Context, j *landJob, pushed []*landJob) {
	f, b, stream := j.f, &j.b, j.stream
	moved := slices.ContainsFunc(pushed, func(p *landJob) bool { return p.clone == j.clone && p.b.Base == b.Base })
	for attempt := 1; ; attempt++ {
		if why := gateWaitWhy(ctx); why != "" {
			j.refuse(why)
			return
		}
		if moved || attempt > 1 {
			start := time.Now()
			newBase, why := f.baseNow(ctx, j.dir, b.Base)
			since(&b.Times.Fetch, start)
			if why != "" {
				j.refuse(why)
				return
			}
			if newBase != j.baseSha {
				if why := f.again(ctx, j, newBase, pushed); why != "" {
					j.refuse(why)
					return
				}
				if len(j.merged) == 0 {
					// no head merges onto the new tip: the first met a conflict there, which
					// ends the batch as it did before (the card's own refusal, reworked at the tip
					// and the stream going on; or the lander's own failure, the stream stopped);
					// nothing is pushed, and nothing is reported as a batch of none (the merge step
					// reads an empty batch as the whole queue)
					if j.failed.id == "" {
						j.refuse("no head of the batch merges onto the moved base " + b.Base + " and no card was blamed; run land again")
						return
					}
					j.ended(1, f.ownRefusal(stream, j.failed), true)
					return
				}
			}
		}
		f.stage("queue", "queue read")
		start := time.Now()
		why := f.queueHead(ctx, stream, j.cards[:len(j.merged)])
		since(&b.Times.Queue, start)
		if why != "" {
			j.refuse(why)
			return
		}
		if l.a.beforePush != nil {
			l.a.beforePush(attempt)
		}
		f.stage("push", "git push")
		start = time.Now()
		_, err := f.git(ctx, j.dir, "push", "--porcelain", "origin", j.tip+":refs/heads/"+b.Base)
		since(&b.Times.Push, start)
		if err == nil {
			b.Tip = j.tip
			// the tip pushed is the base's next tip: when its whole tree passed a gate with
			// the tree tests (the batch's own, or the combined gate), it is recorded as gated
			// and no batch gates it again; a clean merge of disjoint files was never gated as
			// a tree and is not recorded, so the next pass's base gate runs on it (tla/
			// LandPass.tla, CachedIsGreen: two heads green alone can be red together)
			if j.gated {
				l.locks().gateMu.Lock()
				l.baseGateCache[j.tip] = ""
				delete(l.baseGateFails, j.tip)
				l.locks().gateMu.Unlock()
			}
			if !f.landed(*b, stream, j.cards[:len(j.merged)]) {
				j.ended(0, false, false)
				return
			}
			if j.failed.id != "" {
				j.ended(len(j.merged)+1, f.ownRefusal(stream, j.failed), true)
				return
			}
			j.ended(len(j.cards), true, true)
			return
		}
		if !rejected(err) {
			j.refuse("the push to " + b.Base + " failed: " + firstLine("", err) + "; nothing was reported")
			return
		}
		if attempt == 2 {
			b.Cards, b.IDs = len(j.merged), j.ids[:len(j.merged)]
			f.fact(*b, sprint.MergeReq{Stream: stream, Batch: len(j.merged), Rejected: true, Note: firstLine("", err)}, j.cards[:len(j.merged)], "rejected", "the push to "+b.Base+" was rejected again after a rebuild on the moved base: "+firstLine("", err))
			j.ended(0, false, true)
			return
		}
	}
}

// baseNow is the base's tip on origin now, fetched under the pass's fetch lock.
func (l *lander) baseNow(ctx context.Context, dir, base string) (sha, why string) {
	if _, err := l.fetched(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
		return "", "the fetch of " + base + " in " + dir + " failed: " + firstLine("", err)
	}
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "refs/remotes/origin/"+base+"^{commit}")
	if err != nil {
		return "", "the base " + base + " has no tip in " + dir + ": " + firstLine("", err)
	}
	return sha, ""
}

// again merges the batch onto the base's new tip, in the stream's worktree: the same
// merges, resolutions and checks as the build, no gate per head. The combined tree is
// pushed with no new gate when the files the batch changes and the files landed on the
// base since it was cut are disjoint and the same cards merged (needsGate); else it is
// gated once (the tree gate with the tree tests, then --check), and a red gate is the
// refusal, naming the batches of this pass it collided with; nothing is reported, no
// stream stops, and the cards are landed on the next pass. A head that no longer merges
// ends the batch before it as the build does (merged a prefix, failed the head): the
// prefix is gated, pushed and the conflict reported after it by land; none merged, land
// reports the conflict and pushes nothing.
func (l *lander) again(ctx context.Context, j *landJob, newBase string, pushed []*landJob) string {
	if why := gateWaitWhy(ctx); why != "" {
		return why
	}
	b, dir := &j.b, j.dir
	l.ledgerLog = nil
	// a batch that ended at a blamed head (a conflict, or its red gate's bisection) merges
	// again only the heads before it: the blamed head would turn the combined tree red and
	// refuse the prefix that passed, every pass
	cards := j.cards
	if j.failed.id != "" {
		cards = j.cards[:len(j.merged)]
	}
	merged, failed, baseSha, _, why := l.build(ctx, dir, j.stream, cards, b.Times, false)
	if failed.id == "" && len(merged) == len(cards) {
		failed = j.failed // the head blamed before keeps its finding, reported after the push
	}
	b.Also, l.ledgerLog = append(b.Also, l.ledgerLog...), nil
	if wait := gateWaitWhy(ctx); wait != "" {
		return wait
	}
	if why != "" {
		return why
	}
	if baseSha != newBase {
		return "the base " + b.Base + " moved again while the batch was merged onto its new tip; run land again"
	}
	tip, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "the batch branch has no tip after its merge onto the moved base: " + firstLine("", err)
	}
	files, err := l.git(ctx, dir, "diff", "--name-only", "-M", newBase, tip)
	if err != nil {
		return "the files the batch changes could not be listed: " + firstLine("", err)
	}
	landedFiles, err := l.git(ctx, dir, "diff", "--name-only", "-M", j.baseSha, newBase)
	if err != nil {
		return "the files landed on " + b.Base + " since the batch was cut could not be listed: " + firstLine("", err)
	}
	batchFiles := lines(files)
	j.gated = false
	if collide, gate := needsGate(j.merged, merged, batchFiles, lines(landedFiles)); gate {
		start := time.Now()
		l.stage("gate", "the combined tree")
		red := l.treeGate(ctx, dir, true)
		if wait := gateWaitWhy(ctx); wait != "" {
			return wait
		}
		if red == benchGateUnavailableWhy {
			return red + "; no card is blamed and nothing was pushed or reported"
		}
		if red == "" {
			var out string
			red, out = l.runCheck(ctx, dir)
			if wait := gateWaitWhy(ctx); wait != "" {
				return wait
			}
			if red != "" {
				red = l.checkDecided(ctx, dir, j.stream, b.Base, tip, j.cards[:len(merged)], red, out)
			}
		}
		if wait := gateWaitWhy(ctx); wait != "" {
			return wait
		}
		since(&b.Times.Check, start)
		if red != "" {
			return "the batch passes the gate alone and fails it merged onto " + b.Base + " as this pass moved it (" + collidedWith(pushed, collide, len(merged) != len(j.merged)) + "): " + red + "; nothing was pushed or reported, its cards stay queued, and the next pass merges it onto the new tip"
		}
		j.gated = true
	}
	j.baseSha, j.tip, j.merged, j.failed, j.files = newBase, tip, merged, failed, batchFiles
	b.Scope = l.scopeOf(merged)
	return ""
}

// needsGate is the pass's decision for a batch merged again onto a base that moved: no gate
// when the same cards merged as before and the files the batch changes (batchFiles) are
// disjoint from the files landed on the base since the batch was cut (landedFiles); else a
// gate of the combined tree, with the files both changed (collide), sorted.
func needsGate(was, merged, batchFiles, landedFiles []string) (collide []string, gate bool) {
	landed := map[string]bool{}
	for _, f := range landedFiles {
		landed[f] = true
	}
	for _, f := range batchFiles {
		if landed[f] && !slices.Contains(collide, f) {
			collide = append(collide, f)
		}
	}
	slices.Sort(collide)
	return collide, len(collide) > 0 || !slices.Equal(was, merged)
}

// collidedWith names what the batch collided with: each batch landed earlier in this pass
// that changed one of the files, else a push from outside the pass; and the cards that no
// longer merge, when the batch shrank.
func collidedWith(pushed []*landJob, collide []string, shrank bool) string {
	var parts []string
	for _, p := range pushed {
		var shared []string
		for _, f := range p.files {
			if slices.Contains(collide, f) {
				shared = append(shared, f)
			}
		}
		if len(shared) > 0 {
			parts = append(parts, "stream "+p.stream+", "+idSpan(p.merged)+", on "+strings.Join(shared, ","))
		}
	}
	if len(parts) == 0 && len(collide) > 0 {
		parts = append(parts, "a push from outside this pass, on "+strings.Join(collide, ","))
	}
	if shrank {
		parts = append(parts, "fewer of its heads merge onto the new tip")
	}
	return strings.Join(parts, "; ")
}
