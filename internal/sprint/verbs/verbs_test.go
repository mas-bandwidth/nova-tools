package verbs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The tests drive the composed twin (sprintfn.Twin over tset.Mem, with X, the
// derivation, J, the parts and the queries of the stack) through Env, as the
// store's binding will be driven after G0.

const testPrefix = "t:"

var testNames = sprint.Names{Prefix: testPrefix}

// testColumns are the set columns of the four tables, as the write path's own
// tests define them (sprintfn's helpers_test.go).
var testColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// world is one sprint on the twin: the four tables defined (Layer 1's
// lifecycle, before the first step), a clock the test moves, and an Env over a
// counting client.
type world struct {
	t   *testing.T
	tw  *sprintfn.Twin
	log *sprintfn.LogStub
	cc  *Counting
	env *Env
	mu  sync.Mutex
	now time.Time
	cfg *config.Mem
}

func newWorld(t *testing.T) *world {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(testPrefix, table, tset.TableDefinition{Columns: testColumns[table],
			MemberPrefix: testNames.TSetMemberPrefix(table), EpochKey: testNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	w := &world{t: t, log: sprintfn.NewLogStub(), now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	w.tw = sprintfn.NewTwin(m, w.log, testNames)
	w.tw.UseQueries()
	w.tw.UseIntents()
	w.tw.SetClock(func() time.Time {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.now
	})
	w.cc = &Counting{C: w.tw}
	w.env = &Env{C: w.cc, Names: testNames, Actor: "coord", noWait: true}
	w.cfg = config.NewMem()
	w.configure("coord")
	return w
}

// tick moves the store's clock on.
func (w *world) tick(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// configure makes friend f nova-config's coordinator.
func (w *world) configure(f string) {
	w.t.Helper()
	ctx := context.Background()
	if _, found, _ := w.cfg.Get(ctx, config.KindFriend, f); !found {
		if _, err := w.cfg.Insert(ctx, config.KindFriend, config.Row{Name: f, Fields: map[string]string{"slots": "1", "tiers": "pro"}}, "test"); err != nil {
			w.t.Fatalf("config friend %s: %v", f, err)
		}
	}
	if _, _, err := w.cfg.Update(ctx, config.KindSprint, config.KindSprint, map[string]string{"coordinator": f}, "test"); err != nil {
		w.t.Fatalf("config coordinator %s: %v", f, err)
	}
}

// initSprint runs init on the world's Env.
func (w *world) initSprint() {
	w.t.Helper()
	if _, err := Init(context.Background(), w.env, InitReq{Config: w.cfg}); err != nil {
		w.t.Fatalf("init: %v", err)
	}
	w.tick(time.Second)
}

// step sends one raw request to the twin, and fails the test on a refusal.
func (w *world) step(req *sprintfn.Request) *sprintfn.StepReply {
	w.t.Helper()
	res, err := sprintfn.Step(context.Background(), w.tw, req)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("step %s: err %v, refusal %v, result err %v", req.Meta.Verb, err, res.Refusal, res.Err)
	}
	return res.Step
}

// read is one atomic read on the twin.
func (w *world) read(rr *sprintfn.ReadRequest) *sprintfn.ReadReply {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.tw, rr)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("read: err %v, refusal %v, result err %v", err, res.Refusal, res.Err)
	}
	return res.Read
}

// clock is the clock as a read at the epoch sees it.
func (w *world) clock(epoch uint64) (sprintfn.ClockResult, bool) {
	w.t.Helper()
	c, has, err := clockOf(w.read(clockRead(dec(epoch))), 0)
	if err != nil {
		w.t.Fatal(err)
	}
	return c, has
}

// killMode is where a faulty client loses a step.
type killMode int

const (
	killNone   killMode = iota
	killBefore          // the step is never sent; the caller cannot know
	killAfter           // the step applies and its reply is lost
)

// faulty stands between a verb and the twin: it refuses a step as the store
// would on a race or a bug, loses a step before or after it applies, or runs
// another writer just before a step, by the step's number (from 1).
type faulty struct {
	c      sprintfn.Client
	mu     sync.Mutex
	steps  int
	refuse func(n int, req *sprintfn.Request) *sprintfn.Refusal
	kill   func(n int) killMode
	before func(n int)
	sent   []*sprintfn.Request // every step that reached the twin
}

func (f *faulty) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	out := make([]sprintfn.Result, 0, len(items))
	for _, it := range items {
		if it.Step == nil {
			r, err := f.c.Pipeline(ctx, []sprintfn.Item{it})
			if err != nil {
				return nil, err
			}
			out = append(out, r...)
			continue
		}
		f.mu.Lock()
		f.steps++
		n := f.steps
		f.mu.Unlock()
		mode := killNone
		if f.kill != nil {
			mode = f.kill(n)
		}
		if mode == killBefore {
			return nil, &sprintfn.OutcomeUnknownError{Cause: errors.New("the connection dropped before the step was sent")}
		}
		if f.refuse != nil {
			if ref := f.refuse(n, it.Step); ref != nil {
				out = append(out, sprintfn.Result{Refusal: ref})
				continue
			}
		}
		if f.before != nil {
			f.before(n)
		}
		r, err := f.c.Pipeline(ctx, []sprintfn.Item{it})
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.sent = append(f.sent, it.Step)
		f.mu.Unlock()
		if mode == killAfter {
			return nil, &sprintfn.OutcomeUnknownError{Cause: errors.New("the connection dropped after the step was sent")}
		}
		out = append(out, r...)
	}
	return out, nil
}

func refusalOf(code string) *sprintfn.Refusal {
	return &sprintfn.Refusal{Code: code, Message: code + "; nothing was changed"}
}

// TestDoRetriesRaceFiveTimes: a step refused on a race is planned again on a
// fresh read, at most five times (1.5.3), with the epoch reloaded after STALE
// or EPOCHAHEAD; each try is two round trips.
func TestDoRetriesRaceFiveTimes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, code := range []string{"REVISION", "PLACE", sprintfn.CodeXGuard, sprintfn.CodeCounter, "ROWSET"} {
		t.Run("five races then applied: "+code, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.initSprint()
			f := &faulty{c: w.tw, refuse: func(n int, _ *sprintfn.Request) *sprintfn.Refusal {
				if n <= Retries {
					return refusalOf(code)
				}
				return nil
			}}
			cc := &Counting{C: f}
			w.env.C = cc
			res, err := Start(ctx, w.env, ClockReq{})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if res.Retries != Retries || f.steps != Retries+1 || len(f.sent) != 1 {
				t.Fatalf("retries %d, steps tried %d, applied %d; want %d, %d, 1", res.Retries, f.steps, len(f.sent), Retries, Retries+1)
			}
			if got, want := cc.Trips(), 2*(Retries+1); got != want || res.Trips != want {
				t.Fatalf("round trips %d (result %d), want %d: a read and a step a try", got, res.Trips, want)
			}
			if c, _ := w.clock(0); !running(c) {
				t.Fatal("the machine does not run after the start applied")
			}
		})
	}
	t.Run("a sixth race is refused", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		f := &faulty{c: w.tw, refuse: func(int, *sprintfn.Request) *sprintfn.Refusal { return refusalOf("REVISION") }}
		w.env.C = f
		_, err := Start(ctx, w.env, ClockReq{})
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != "REVISION" || rf.Retries != Retries || rf.Local {
			t.Fatalf("err %v, want REVISION after %d retries", err, Retries)
		}
		if f.steps != Retries+1 || len(f.sent) != 0 {
			t.Fatalf("steps tried %d, applied %d; want %d, 0", f.steps, len(f.sent), Retries+1)
		}
		if c, _ := w.clock(0); running(c) {
			t.Fatal("a refused start moved the clock")
		}
	})
	t.Run("STALE reloads the epoch and plans at it", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
			t.Fatalf("clear: %v", err)
		}
		stale := &Env{C: w.tw, Names: testNames, Actor: "coord", Epoch: 0, noWait: true}
		f := &faulty{c: w.tw, refuse: func(n int, req *sprintfn.Request) *sprintfn.Refusal {
			if n == 1 {
				r := refusalOf(sprintfn.CodeStale)
				r.Detail.ActiveEpoch = "1"
				return r
			}
			return nil
		}}
		stale.C = f
		// The read at epoch 0 names the active epoch 1 (AL2): the verb plans at 1.
		res, err := Start(ctx, stale, ClockReq{})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if stale.Epoch != 1 || res.Epoch != 1 || f.steps != 2 || res.Retries != 2 {
			t.Fatalf("epoch %d (result %d), steps %d, retries %d; want 1, 1, 2 (a STALE step), 2 (the read's reload and the STALE)", stale.Epoch, res.Epoch, f.steps, res.Retries)
		}
		if f.sent[0].Epoch != "1" {
			t.Fatalf("the applied step was planned at epoch %s, want 1", f.sent[0].Epoch)
		}
	})
	t.Run("EPOCHAHEAD on the read reloads", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		w.env.Epoch = 7
		if _, err := Start(ctx, w.env, ClockReq{}); err != nil {
			t.Fatalf("start: %v", err)
		}
		if w.env.Epoch != 0 {
			t.Fatalf("epoch %d, want the active 0", w.env.Epoch)
		}
	})
}

// TestDoBugNotRetried: a refusal that is not a race is returned at once, with
// no second read and no second step (1.3.5); a plan that refuses from its read
// sends nothing.
func TestDoBugNotRetried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, code := range []string{sprintfn.CodeWrongType, "DRIFT", sprintfn.CodeConfig, sprintfn.CodeRequest, sprintfn.CodeMachineState, sprintfn.CodeNotCoord, "OPCONFLICT"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.initSprint()
			f := &faulty{c: w.tw, refuse: func(int, *sprintfn.Request) *sprintfn.Refusal { return refusalOf(code) }}
			cc := &Counting{C: f}
			w.env.C = cc
			_, err := Start(ctx, w.env, ClockReq{})
			var rf *Refused
			if !errors.As(err, &rf) || rf.Code() != code || rf.Retries != 0 || rf.Local {
				t.Fatalf("err %v, want %s with no retry", err, code)
			}
			if f.steps != 1 || cc.Trips() != 2 {
				t.Fatalf("steps %d, trips %d; want 1 and 2", f.steps, cc.Trips())
			}
		})
	}
	t.Run("a plan's own refusal sends no step", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		w.cc.Reset()
		_, err := Stop(ctx, w.env, ClockReq{}) // init leaves it STOPPED
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeMachineState || !rf.Local {
			t.Fatalf("err %v, want a local MACHINESTATE", err)
		}
		if w.cc.Steps() != 0 || w.cc.Trips() != 1 {
			t.Fatalf("steps %d, trips %d; want 0 and 1 (the read)", w.cc.Steps(), w.cc.Trips())
		}
	})
	t.Run("a lost reply is unknown, never retried", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		f := &faulty{c: w.tw, kill: func(int) killMode { return killAfter }}
		w.env.C = f
		_, err := Start(ctx, w.env, ClockReq{Op: "start-1"})
		var u *Unknown
		if !errors.As(err, &u) || !errors.Is(err, tset.ErrOutcomeUnknown) || f.steps != 1 {
			t.Fatalf("err %v, steps %d; want an unknown outcome after one step", err, f.steps)
		}
		// The same op settles it: its receipt answers, and nothing is written.
		w.env.C = w.tw
		res, err := Start(ctx, w.env, ClockReq{Op: "start-1"})
		if err != nil || !res.Replay {
			t.Fatalf("resume: %+v, %v; want the recorded result", res, err)
		}
	})
}

// ---- verbs in parts

// makeCards is a verb in parts for the tests: it creates n reader cards
// c001.. in r1:asked, chunk at a time, the row with part 1. Its continuation
// is the index of the next card; its read is the chunk's ids (their absence).
func makeCards(n, chunk int) PartsPlan {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%03d", i+1)
	}
	at := func(cont string) int {
		if cont == "" {
			return 0
		}
		i, _ := strconv.Atoi(cont)
		return i
	}
	return PartsPlan{Verb: "make", Args: map[string]any{"n": n, "ids": IDsDigest(ids)}, Chunk: chunk,
		Read: func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest {
			i := at(cont)
			return &sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "ids", Table: sprint.Readers, IDs: ids[i:min(i+chunk, n)]}}}
		},
		Plan: func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
			i := at(cont)
			j := min(i+chunk, n)
			var fresh []string
			for _, r := range rd.Tset[0].Records {
				if r.Exists {
					return Part{}, fmt.Errorf("card %s exists before its part", r.ID)
				}
				fresh = append(fresh, r.ID)
			}
			if len(fresh) != j-i {
				return Part{}, fmt.Errorf("read %d ids, planned %d", len(fresh), j-i)
			}
			scores := make([]string, len(fresh))
			for k := range scores {
				scores[k] = strconv.Itoa(i + k + 1)
			}
			var entries []tset.Entry
			if i == 0 {
				entries = append(entries, tset.Entry{Kind: "rows", Table: sprint.Readers, Add: []string{"r1"}})
			}
			entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Readers, To: "r1:asked", IDs: fresh, About: fresh, Scores: scores})
			return Part{Req: &sprintfn.Request{Body: sprintfn.Body{Entries: entries}}, Next: strconv.Itoa(j), Last: j == n}, nil
		}}
}

// created is the ids a step creates.
func created(req *sprintfn.Request) int {
	n := 0
	for _, en := range req.Body.Entries {
		if en.Kind == "create" {
			n += len(en.IDs)
		}
	}
	return n
}

// checkCards holds the world to the n cards of makeCards: each in r1:asked,
// each with exactly one line of history, so none was created twice.
func checkCards(t *testing.T, w *world, n int) {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%03d", i+1)
	}
	rd := w.read(&sprintfn.ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{
		{Kind: "ids", Table: sprint.Readers, IDs: ids},
		{Kind: "count", Table: sprint.Readers, Cells: []string{"r1:asked"}}}})
	for i, r := range rd.Tset[0].Records {
		if !r.Exists || r.Place == nil || r.Place.Row != "r1" || r.Place.Col != "asked" {
			t.Fatalf("card %s: %+v, want it in r1:asked", ids[i], r)
		}
		if h := w.log.History(testPrefix, "0", ids[i]); len(h) != 1 {
			t.Fatalf("card %s has %d lines of history, want 1: made once", ids[i], len(h))
		}
	}
	if got := rd.Tset[1].Counts; len(got) != 1 || got[0] != uint64(n) {
		t.Fatalf("r1:asked holds %v, want %d", got, n)
	}
}

// TestPartsResumeNeitherSkipsNorRepeats: a verb in parts killed after every
// part, before or after its step reached the store, resumes by done from the
// last applied part's continuation: every card made once, none skipped, and a
// resume of a finished op writes nothing (the finished mark, errata 3 H4).
// Clean, n parts cost n + 1 round trips.
func TestPartsResumeNeitherSkipsNorRepeats(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const n, chunk = 10, 3 // four parts
	t.Run("clean run, n + 1 round trips", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		res, err := w.env.Parts(ctx, "", makeCards(n, chunk))
		if err != nil {
			t.Fatal(err)
		}
		if res.Parts != 4 || w.cc.Trips() != 5 || res.Trips != 5 {
			t.Fatalf("parts %d, trips %d (result %d); want 4 and 5", res.Parts, w.cc.Trips(), res.Trips)
		}
		if res.Op == "" || !validOp(res.Op) {
			t.Fatalf("the verb made no op to print: %q", res.Op)
		}
		checkCards(t, w, n)
	})
	for _, mode := range []killMode{killBefore, killAfter} {
		t.Run(fmt.Sprintf("killed at every part, mode %d", mode), func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			const op = "op-make"
			// killAfter: each run's first step applies and its reply is lost.
			// killBefore: each run's first step applies, and its second is lost
			// before it reaches the store. Either way a run applies one part.
			at := 1
			if mode == killBefore {
				at = 2
			}
			applied := 0
			for run := 0; run < 10; run++ {
				f := &faulty{c: w.tw, kill: func(k int) killMode {
					if k == at {
						return mode
					}
					return killNone
				}}
				w.env.C = f
				res, err := w.env.Parts(ctx, op, makeCards(n, chunk))
				applied += len(f.sent)
				if res.Resumed != run {
					t.Fatalf("run %d resumed after %d parts, want %d", run, res.Resumed, run)
				}
				if err == nil {
					if applied != 4 {
						t.Fatalf("%d parts reached the store, want 4", applied)
					}
					checkCards(t, w, n)
					w.env.C = w.tw
					again, err := w.env.Parts(ctx, op, makeCards(n, chunk))
					if err != nil || !again.Replay || again.Resumed != 4 || again.Parts != 0 {
						t.Fatalf("resume of a finished op: %+v, %v; want its finished mark found and nothing sent", again, err)
					}
					checkCards(t, w, n)
					return
				}
				var u *Unknown
				if !errors.As(err, &u) || u.Op != op {
					t.Fatalf("run %d: %v, want an unknown outcome naming op %s", run, err, op)
				}
			}
			t.Fatal("the op never finished")
		})
	}
	t.Run("other arguments under the op are refused", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		f := &faulty{c: w.tw, kill: func(k int) killMode {
			if k == 2 {
				return killBefore
			}
			return killNone
		}}
		w.env.C = f
		if _, err := w.env.Parts(ctx, "op-x", makeCards(n, chunk)); err == nil {
			t.Fatal("the kill did not stop the op")
		}
		w.env.C = w.tw
		_, err := w.env.Parts(ctx, "op-x", makeCards(n+1, chunk))
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != "OPCONFLICT" {
			t.Fatalf("err %v, want OPCONFLICT", err)
		}
	})
}

// TestPartsLimitResumesAtHalfChunk: a part refused LIMIT is planned again at
// half the chunk, and every part after it keeps the half, across a resume too
// (the chunk rides the continuation) (1.5.4).
func TestPartsLimitResumesAtHalfChunk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const n = 10
	w := newWorld(t)
	limits := 0
	var mu sync.Mutex
	f := &faulty{c: w.tw,
		refuse: func(_ int, req *sprintfn.Request) *sprintfn.Refusal {
			if created(req) > 2 {
				mu.Lock()
				limits++
				mu.Unlock()
				r := refusalOf(sprintfn.CodeLimit)
				r.Detail.Budget = "candidates"
				return r
			}
			return nil
		},
		kill: func(k int) killMode {
			if k == 4 { // part 3 at the half chunk: applied, reply lost
				return killAfter
			}
			return killNone
		}}
	w.env.C = f
	_, err := w.env.Parts(ctx, "op-half", makeCards(n, 4))
	var u *Unknown
	if !errors.As(err, &u) {
		t.Fatalf("err %v, want the kill's unknown outcome", err)
	}
	f.kill = nil
	res, err := w.env.Parts(ctx, "op-half", makeCards(n, 4))
	if err != nil {
		t.Fatal(err)
	}
	if limits != 1 {
		t.Fatalf("%d parts were refused LIMIT, want 1: the chunk stays halved, the resume too", limits)
	}
	for i, req := range f.sent {
		if c := created(req); c > 2 {
			t.Fatalf("step %d made %d cards after the LIMIT, want at most 2", i+1, c)
		}
	}
	if res.Chunk != 2 || res.Resumed != 3 || res.Parts != 2 {
		t.Fatalf("chunk %d, resumed %d, parts %d; want 2, 3, 2 (five parts of two)", res.Chunk, res.Resumed, res.Parts)
	}
	checkCards(t, w, n)
}
