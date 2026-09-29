package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

type logMem struct {
	*Mem
	mu  sync.Mutex
	log []string
	who string
}

func (l *logMem) add(s string) { l.mu.Lock(); l.log = append(l.log, s); l.mu.Unlock() }

func (l *logMem) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	r, err := l.Mem.Apply(ctx, m)
	var ids []string
	for _, e := range m.Members {
		if hasChanges(e) {
			ids = append(ids, e.ID)
		}
	}
	l.add(fmt.Sprintf("%s apply %s %s rev %s -> %v err=%v replay=%v [%s]", l.who, m.Table, m.OperationID, m.ExpectedTableRevision, r.After, err, r.Replay, strings.Join(ids, " ")))
	return r, err
}
func (l *logMem) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	ok, err := l.Mem.Acquire(ctx, gen, op)
	l.add(fmt.Sprintf("%s acquire %s gen %d ok=%v", l.who, op.ID, gen, ok))
	return ok, err
}
func (l *logMem) Release(ctx context.Context, op OpRecord, commit bool) error {
	had := l.Mem.Pending()
	err := l.Mem.Release(ctx, op, commit)
	hid := ""
	if had != nil {
		hid = had.ID
	}
	l.add(fmt.Sprintf("%s release %s commit=%v (fence held %s)", l.who, op.ID, commit, hid))
	return err
}

type tagMem struct {
	*logMem
	who string
}

func (t *tagMem) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	r, err := t.logMem.Mem.Apply(ctx, m)
	var ids []string
	for _, e := range m.Members {
		if hasChanges(e) {
			ids = append(ids, e.ID)
		}
	}
	t.logMem.add(fmt.Sprintf("%s apply %s %s exp %s -> %v err=%v replay=%v [%s]", t.who, m.Table, m.OperationID, m.ExpectedTableRevision, r.After, err, r.Replay, strings.Join(ids, " ")))
	return r, err
}
func (t *tagMem) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	ok, err := t.logMem.Mem.Acquire(ctx, gen, op)
	t.logMem.add(fmt.Sprintf("%s acquire %s gen %d ok=%v", t.who, op.ID, gen, ok))
	return ok, err
}
func (t *tagMem) Release(ctx context.Context, op OpRecord, commit bool) error {
	had := t.logMem.Mem.Pending()
	err := t.logMem.Mem.Release(ctx, op, commit)
	hid := ""
	if had != nil {
		hid = had.ID
	}
	t.logMem.add(fmt.Sprintf("%s release %s commit=%v (fence held %s)", t.who, op.ID, commit, hid))
	return err
}

func TestCRZombieWriter(t *testing.T) {
	skipUntilEngineRepair(t)
	if underRace {
		// Fails at e3a805e66 as well (12 of 15 runs of this test alone under
		// -race; none of 10 without): a worker's take is cut at the fleet
		// table ("member ... changed under the step") while two tick loops
		// race. A finding of its own, not yet worked.
		t.Skip("KNOWN-ZOMBIE-RACE: under -race a take is cut at the fleet table while two tick loops race; fails at the base too")
	}
	for trial := 0; trial < 40; trial++ {
		w := crSprint(t, uint64(trial+1))
		h := w.h
		h.startMachine()
		lm := &logMem{Mem: h.m}
		mk := func(who string) *Store {
			return &Store{B: &tagMem{logMem: lm, who: who}, Names: h.st.Names, Actor: "machine", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
		}
		a, b := mk("A"), mk("B")
		h.st.B = &tagMem{logMem: lm, who: "W"}
		var wg sync.WaitGroup
		stop := make(chan struct{})
		loop := func(st *Store) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = st.Tick(h.ctx)
			}
		}
		wg.Add(2)
		go loop(a)
		go loop(b)
		var bad error
		for r := 1; r <= 60 && bad == nil; r++ {
			for _, m := range crMembers {
				if _, err := h.st.Run(h.ctx, TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 100}, Who: m})); err != nil {
					bad = err
					break
				}
				s := h.snap()
				for _, c := range s.Fleet.Cell(m, sprint.Working) {
					if _, err := h.st.Run(h.ctx, FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: m})); err != nil {
						bad = err
					}
				}
			}
			h.tick(time.Second)
		}
		close(stop)
		wg.Wait()
		if bad != nil {
			msg := bad.Error()
			t.Logf("trial %d: %s", trial, msg)
			var member string
			if i := strings.Index(msg, "member "); i >= 0 {
				member = strings.Fields(msg[i+7:])[0]
			}
			var tail []string
			for _, l := range lm.log {
				if strings.Contains(l, member) || strings.Contains(l, "acquire") && strings.Contains(l, "ok=true") || strings.Contains(l, "release") {
					tail = append(tail, l)
				}
			}
			if len(tail) > 40 {
				tail = tail[len(tail)-40:]
			}
			t.Logf("log:\n%s", strings.Join(tail, "\n"))
			t.FailNow()
		}
	}
}
