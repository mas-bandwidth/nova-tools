package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// restoreSprint is a sprint with something of every kind a restore must keep: a
// primary finished with its head, branch, report and usage under a caller's
// operation id and read ok (heads, attempts, cost, the read and its verdict), and
// another finished failed (an open judgment), on the harness's twin store.
func restoreSprint(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	fillRestoreSprint(h)
	return h
}

// fillRestoreSprint is restoreSprint's steps on the harness's store.
func fillRestoreSprint(h *harness) {
	h.t.Helper()
	h.setup(2)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	s := h.snap()
	for i, id := range []string{"s1-1", "s1-2"} {
		c := s.Fleet.Card(s.Work.Card(id).F("work"))
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
		step := FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: i == 1,
			Head: "0123456789abcdef0123456789abcdef01234567", Branch: "work/" + id, Report: "pushed", Usage: "input=1000 output=100"})
		step.CallerOp = "finish-" + id
		h.must(step)
	}
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.readAllOK("s1-1")
}

// dropFromDump is the twin's document with the primary's head taken off its work
// card and every caller's recorded result taken out of the log: the cards and
// the tables are all there, so the counts are the store's.
func dropFromDump(t *testing.T, doc []byte, primary string) []byte {
	t.Helper()
	var d map[string]any
	require.NoError(t, json.Unmarshal(doc, &d))
	members := d["tables"].(map[string]any)["t-work"].(map[string]any)["members"].(map[string]any)
	fields := members[primary].(map[string]any)["fields"].(map[string]any)
	require.Contains(t, fields, "head", "the primary carries its head before the drop")
	delete(fields, "head")
	for _, l := range d["logs"].(map[string]any) {
		delete(l.(map[string]any), "done")
	}
	out, err := json.MarshalIndent(d, "", " ")
	require.NoError(t, err)
	return out
}

// incompleteSource is the store's source whose dump lost what drop takes, with
// the store's own counts: a dump whose checksum is its own and whose sprint is not
// the store's.
type incompleteSource struct {
	MemSource
	drop func([]byte) []byte
}

func (s incompleteSource) Save(ctx context.Context) ([]byte, SnapshotCounts, error) {
	doc, c, err := s.MemSource.Save(ctx)
	return s.drop(doc), c, err
}

// rdbSource hands out one RDB whose CRC-64 is its own, as a Redis's BGSAVE does,
// with the counts a Redis source reports (unknown).
type rdbSource struct{}

func (rdbSource) Save(context.Context) ([]byte, SnapshotCounts, error) {
	body := []byte("REDIS0011\xfa\x09redis-ver\x057.2.0\xff")
	c := redisCRC64(body)
	for i := 0; i < 8; i++ {
		body = append(body, byte(c>>(8*i)))
	}
	return body, SnapshotCounts{Keys: -1, Cards: -1}, nil
}

// A SEMANTICALLY INCOMPLETE DUMP FAILS THE RESTORE (Stella's nova-sprint review,
// item 4). A dump whose checksum is its own and whose cards and tables are all
// there, but which lost a primary's head and the callers' recorded results, is
// a dump the sprint cannot be restored from: a restore of it loses the head the
// read was of, and a worker retrying its finish would be run again as new work.
// Counting tables and cards cannot see it; the restore compares the sprint's
// state, names the parts, and the snapshot is removed. The complete dump
// restores at the semantic level, and an RDB checked by its header and checksum
// alone is verified at the integrity level, never reported as semantic.
func TestASemanticallyIncompleteDumpFailsRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := restoreSprint(t)
	names := h.st.Names
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { at = at.Add(time.Second); return at }
	src := incompleteSource{MemSource: MemSource{M: h.m, Names: names}, drop: func(doc []byte) []byte { return dropFromDump(t, doc, "s1-1") }}
	sn := &Snapshotter{Dir: t.TempDir(), Keep: 2, Source: src, Twin: MemTwin{Names: names}, Now: clock}
	got, err := sn.Take(ctx)
	require.Error(t, err, "a dump that lost the primary's head and the callers' results loaded with a valid checksum and the store's counts (%+v) and was kept as a verified snapshot", got.Counts)
	require.ErrorContains(t, err, "does not restore the sprint the store held at the save")
	require.ErrorContains(t, err, "record work s1-1 field head")
	require.ErrorContains(t, err, "done finish-s1-1")
	files, _ := SnapshotFiles(sn.Dir)
	require.Empty(t, files, "the incomplete snapshot is removed")

	// the counts alone pass it: they are the store's
	full, live, err := MemSource{M: h.m, Names: names}.Save(ctx)
	require.NoError(t, err)
	counted, err := MemTwin{Names: names}.Load(dropFromDump(t, full, "s1-1"))
	require.NoError(t, err)
	require.Equal(t, live, counted, "the incomplete dump counts as the store does")
	require.Equal(t, 2, live.Cards, "the work table's cards are counted under the store's names")

	// the complete dump is restored at the semantic level
	sn = &Snapshotter{Dir: t.TempDir(), Keep: 2, Source: MemSource{M: h.m, Names: names}, Twin: MemTwin{Names: names}, Now: clock}
	got, err = sn.Take(ctx)
	require.NoError(t, err)
	require.Equal(t, RestoreSemantic, got.Restore)
	want, err := ReadState(ctx, h.m, names)
	require.NoError(t, err)
	for _, part := range []string{"epoch", "record work s1-1 field head", "record work s1-1 field attempt", "done finish-s1-1", "fence"} {
		require.Contains(t, want.Parts, part, "the sprint's state holds %s", part)
	}

	// an RDB checked by its header and checksum is integrity, never semantic
	sn = &Snapshotter{Dir: t.TempDir(), Keep: 2, Source: rdbSource{}, Twin: RDBTwin{}, Now: clock}
	got, err = sn.Take(ctx)
	require.NoError(t, err)
	require.Equal(t, RestoreIntegrity, got.Restore, "a header-and-checksum pass is reported as %q", got.Restore)
	require.Equal(t, RestoreIntegrity, RestoreLevel(MemSource{M: h.m, Names: names}, RDBTwin{}))
}

// cutter is the store a process dies on: at its cut it dumps the store, as a
// snapshot taken at that instant, and the process hears nothing after. before
// is the step's acquisition (nothing of it written), pending its commit (every
// manifest applied, the operation in the fence), committed just after its
// commit (the reply lost).
type cutter struct {
	*Mem
	at   string
	dump []byte
}

func (c *cutter) cut() error {
	if c.dump == nil {
		doc, err := c.Mem.Snapshot()
		if err != nil {
			return err
		}
		c.dump = doc
	}
	return errors.New("the process died here")
}

func (c *cutter) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if c.at == "before" && !op.Lock {
		return false, c.cut()
	}
	return c.Mem.Acquire(ctx, gen, op)
}

func (c *cutter) Release(ctx context.Context, op OpRecord, commit bool) error {
	if c.at == "pending" && commit {
		return c.cut()
	}
	if err := c.Mem.Release(ctx, op, commit); err != nil {
		return err
	}
	if c.at == "committed" && commit {
		return c.cut()
	}
	return nil
}

// effects is the world outside the store, stubbed: the pushes origin took (a
// push of the head a branch already holds is no new push, as git's), and what
// the caller did on hearing each committed result, once per operation it heard
// (a caller's operation id answers the same operation on every retry).
type effects struct {
	origin map[string]string
	pushes int
	heard  map[string]int
}

func (e *effects) push(branch, head string) {
	if e.origin[branch] != head {
		e.origin[branch] = head
		e.pushes++
	}
}

// A RESTART AT THE FINISH, READ AND PUSH-REPORT BOUNDARIES (Stella's nova-sprint
// review, item 4). The store is dumped at each boundary of each step (before
// its acquisition, with its operation pending in the fence, just after its
// commit with the reply lost), the process dies there, and a new one restores
// the dump into a fresh store and retries what it was doing under the same
// caller operation id, the outside world stubbed. The transition happens, once:
// the restore keeps the fence and the callers' recorded results, so a pending
// operation is finished by the retry and a committed one is answered, never run
// again as new work; the caller hears one operation and origin takes one push.
func TestARestartAtAStepBoundaryRepeatsNoEffectAndLosesNoTransition(t *testing.T) {
	t.Parallel()
	const head = "89abcdef0123456789abcdef0123456789abcdef"
	type boundary struct {
		name string
		// ready brings a sprint to the step; step is the step, retried as it was;
		// push is the outside effect before it (push-report); landed says the
		// transition is in the store, once
		ready  func(h *harness)
		step   func(h *harness) Step
		push   bool
		landed func(t *testing.T, h *harness)
	}
	workCard := func(h *harness) *sprint.Card { s := h.snap(); return s.Fleet.Card(s.Work.Card("s1-1").F("work")) }
	taken := func(h *harness) {
		h.setup(1)
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		c := workCard(h)
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	}
	finishStep := func(failed bool) func(h *harness) Step {
		return func(h *harness) Step {
			c := workCard(h)
			r := sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: failed, Report: "the gate is red"}
			if !failed {
				r.Report, r.Head, r.Branch = "pushed="+head+" to work/s1-1: landed the thing", head, "work/s1-1"
			}
			step := FinishStep(r)
			step.CallerOp = "m1-finish-s1-1"
			return step
		}
	}
	for _, b := range []boundary{
		{name: "finish", ready: taken, step: finishStep(true), landed: func(t *testing.T, h *harness) {
			require.Equal(t, sprint.Review, h.state("s1-1"))
			require.Equal(t, 1, h.written(sprint.NWorkFailed), "work came back failed is written once")
			require.Len(t, h.openOf(sprint.NWorkFailed), 1)
		}},
		{name: "push-report", ready: taken, step: finishStep(false), push: true, landed: func(t *testing.T, h *harness) {
			require.Equal(t, sprint.Review, h.state("s1-1"))
			require.Equal(t, head, h.snap().Work.Card("s1-1").F("head"), "the primary carries the pushed head")
			require.Equal(t, 1, h.written(sprint.NWorkOK)+h.written(sprint.NWorkFailed), "the work's return is written once")
		}},
		{name: "read", ready: func(h *harness) {
			taken(h)
			h.must(finishStep(false)(h))
			h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		}, step: func(h *harness) Step {
			rc := h.snap().Readers.Of("s1-1")[0]
			step := ReadStep(sprint.ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}})
			step.CallerOp = "reader-read-s1-1"
			return step
		}, landed: func(t *testing.T, h *harness) {
			// the ask places every read the card needs at once; the step is one
			// of them, and a retry of it does not land a second
			var ok int
			for _, rc := range h.snap().Readers.Of("s1-1") {
				if rc.Col == sprint.OK {
					ok++
				}
			}
			require.Equal(t, 1, ok, "the read step landed once")
		}},
	} {
		for _, at := range []string{"before", "pending", "committed"} {
			t.Run(b.name+" "+at, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				b.ready(h)
				step := b.step(h)
				fx := &effects{origin: map[string]string{}, heard: map[string]int{}}
				if b.push {
					fx.push("work/s1-1", head)
				}
				c := &cutter{Mem: h.m, at: at}
				h.st.B = c
				_, _ = h.st.Run(h.ctx, step) // ignored: the process died at the cut and heard nothing
				require.NotNil(t, c.dump, "the step reached its %s boundary", at)

				// a new process: the dump restored into a fresh store, the clock past
				// the grace of an operation left in the fence
				m := NewMem()
				require.NoError(t, m.Restore(c.dump))
				fence, err := m.ReadFence(h.ctx)
				require.NoError(t, err)
				_, done, err := m.Done(h.ctx, step.CallerOp)
				require.NoError(t, err)
				require.Equal(t, at == "pending", fence.Pending != nil, "the dump's fence at %s: %+v", at, fence.Pending)
				require.Equal(t, at == "committed", done, "the dump records the caller's result at %s: %v", at, done)
				r := &harness{t: t, m: m, ctx: h.ctx, now: h.now.Add(10 * time.Minute), live: h.live}
				n := 0
				r.st = &Store{B: m, Names: h.st.Names, Actor: h.st.Actor, CheckTwin: checkTwin, Sleep: func(time.Duration) {},
					Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
					NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprintf("restarted-%d", n) }}
				for range 2 { // the retry, and a retry of the retry
					if b.push {
						fx.push("work/s1-1", head)
					}
					res := r.must(step)
					fx.heard[res.Op]++
				}
				require.Len(t, fx.heard, 1, "the caller heard one operation: %v", fx.heard)
				if b.push {
					require.Equal(t, 1, fx.pushes, "origin took one push")
				}
				b.landed(t, r)
				r.clean("restarted at " + at)
			})
		}
	}
}

// The reversed witness of the restart: a dump that lost the callers' recorded
// results (a semantically incomplete one, which the restore check refuses) does
// not answer the retry of a committed step. The retry plans afresh on a store
// where the transition already happened: it is refused, or heard as another
// operation, and either way the caller cannot tell its step happened once.
func TestARestartOnADumpWithoutTheResultsCannotAnswerTheRetry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	c := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	step := FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: true, Report: "red"})
	step.CallerOp = "m1-finish-s1-1"
	cut := &cutter{Mem: h.m, at: "committed"}
	h.st.B = cut
	_, _ = h.st.Run(h.ctx, step) // ignored: the process died at the cut and heard nothing
	require.NotNil(t, cut.dump)
	var d map[string]any
	require.NoError(t, json.Unmarshal(cut.dump, &d))
	for _, l := range d["logs"].(map[string]any) {
		delete(l.(map[string]any), "done")
	}
	lost, err := json.Marshal(d)
	require.NoError(t, err)
	m := NewMem()
	require.NoError(t, m.Restore(lost))
	st := &Store{B: m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: func() string { return "restarted" }, Sleep: func(time.Duration) {}}
	res, err := st.Run(h.ctx, step)
	require.NoError(t, err)
	require.False(t, res.Replay, "the retry is answered as a replay without the recorded result")
	require.NotEmpty(t, res.Refused, "the retry of a step that happened ran again as new work: %+v", res)
}

// The pending replay payload and the table definition are logical state too:
// identical IDs, checksums and member counts cannot prove either survived.
func TestASemanticRestoreKeepsThePendingOperationAndTableShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, part string
		pending    bool
		mutate     func(map[string]any)
	}{
		{name: "table definition", part: "table work", mutate: func(d map[string]any) {
			def := d["tables"].(map[string]any)["t-work"].(map[string]any)["def"].(map[string]any)
			def["Columns"].([]any)[0].(map[string]any)["Fold"] = "changed-fold"
		}},
		{name: "pending result", part: "fence", pending: true, mutate: func(d map[string]any) {
			for _, l := range d["logs"].(map[string]any) {
				if pending, ok := l.(map[string]any)["fence"].(map[string]any); ok {
					pending["result"] = "changed replay result"
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := restoreSprint(t)
			if tc.pending {
				h = newHarness(t)
				h.setup(1)
				h.must(DealStep(sprint.DealReq{}))
				s := h.snap()
				c := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
				h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
				cut := &cutter{Mem: h.m, at: "pending"}
				h.st.B = cut
				step := FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: true, Report: "red"})
				step.CallerOp = "pending-finish"
				_, _ = h.st.Run(h.ctx, step) // ignored: the cut simulates the process dying before commit
				require.NotNil(t, cut.dump)
			}
			doc, err := h.m.Snapshot()
			require.NoError(t, err)
			want, err := ReadState(h.ctx, h.m, h.st.Names)
			require.NoError(t, err)
			var d map[string]any
			require.NoError(t, json.Unmarshal(doc, &d))
			tc.mutate(d)
			incomplete, err := json.Marshal(d)
			require.NoError(t, err)
			require.ErrorContains(t, SemanticRestore(h.ctx, want, MemTwin{Names: h.st.Names}, incomplete), tc.part)
		})
	}
}
