package sprint

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	acceptanceSmall = "SET OK units=3 ready=1 blocked=1 owned=0\nSET DONE done=1 percent=33\n"
	acceptanceLive  = "SET OK units=42 ready=7 blocked=9 owned=0\nSET DONE done=26 percent=61\n"
)

func TestParseAcceptanceReadsSetOKAndSetDone(t *testing.T) {
	acc, err := ParseAcceptance("SET EVAL unit=a holds=true\n" + acceptanceLive)
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Set || acc.Done != 26 || acc.Units != 42 || acc.Percent != 61 {
		t.Fatalf("acceptance = %+v, want 26/42 percent 61", acc)
	}
}

func TestParseAcceptanceRefusesStatusInsteadOfSetDone(t *testing.T) {
	_, err := ParseAcceptance("SET OK units=42 ready=7 blocked=9 owned=0\n:status \"landed\"\n")
	if err == nil || !strings.Contains(err.Error(), "SET DONE") || !strings.Contains(err.Error(), ":status") {
		t.Fatalf("err = %v, want a refusal to count :status", err)
	}
	_, err = ParseAcceptance("SET OK units=42 ready=0 blocked=0 owned=0\nSET DONE done=26\n")
	if err == nil || !strings.Contains(err.Error(), "percent") {
		t.Fatalf("err = %v, want a refusal to recompute percent", err)
	}
}

func TestMeasureUsesAcceptanceNotClosedTasks(t *testing.T) {
	if Percent(26, 42) != 62 {
		t.Fatalf("Percent(26, 42) = %d, want 62 so a stored 61 can only be SET DONE's", Percent(26, 42))
	}
	tasks := []Task{
		{ID: "a", Kind: KindFix, Owner: "johnny", State: StateClosed},
		{ID: "b", Kind: KindFix, Owner: "johnny", State: StateClosed},
	}
	p, err := Measure(tasks, Acceptance{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Done != 0 || p.Units != 0 || p.Percent != 0 || p.Evaluated {
		t.Fatalf("unevaluated closed tasks counted as %+v", p)
	}
	acc, err := ParseAcceptance(acceptanceLive)
	if err != nil {
		t.Fatal(err)
	}
	p, err = Measure(tasks, acc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Done != 26 || p.Units != 42 || p.Percent != 61 || !p.Evaluated || p.ETAMinutes != 0 {
		t.Fatalf("progress = %+v, want 26/42 percent 61 eta 0", p)
	}
}

// TestADelayedWriterDoesNotMoveProgressBackwards is the race in WriteProgress:
// one writer has already read done=1, then a newer evaluation and a task close
// publish 26/42 percent 61 with a shorter wall, and the first writer must not
// put the older snapshot back.
func TestADelayedWriterDoesNotMoveProgressBackwards(t *testing.T) {
	t.Run("fake", func(t *testing.T) {
		checkDelayedWriter(t, NewFakeStore())
	})
	t.Run("redis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		st := NewRedisStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
		t.Cleanup(func() { _ = st.Close() })
		checkDelayedWriter(t, st)
	})
}

func checkDelayedWriter(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 17, 0, 0, 0, time.UTC)
	if err := st.PutSprint(ctx, Sprint{Name: "fixes", Goal: "the fixes day", OpenedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		task := Task{ID: id, Kind: KindFix, Ref: "o/n#" + id, Owner: "johnny", State: StateOpen, EstMinutes: 60, CreatedAt: now}
		if err := st.PutTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		if err := st.AddTask(ctx, "fixes", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordAcceptance(ctx, st, "fixes", acceptanceSmall); err != nil {
		t.Fatal(err)
	}
	seed, err := st.GetSprint(ctx, "fixes")
	if err != nil {
		t.Fatal(err)
	}
	if seed.Done != 1 || seed.Units != 3 || seed.Percent != 33 || seed.ETAMinutes != 120 {
		t.Fatalf("seed progress = done %d units %d percent %d eta %d, want 1/3 percent 33 eta 120", seed.Done, seed.Units, seed.Percent, seed.ETAMinutes)
	}

	late := &lateWriter{Store: st}
	if err := WriteProgress(ctx, late, "fixes"); err != nil {
		t.Fatal(err)
	}
	if late.innerErr != nil {
		t.Fatal(late.innerErr)
	}
	if !late.saw {
		t.Fatal("the delayed writer never read a snapshot")
	}
	if late.published {
		t.Fatal("the older snapshot was published")
	}
	if late.after.Done != 26 || late.after.Units != 42 || late.after.Percent != 61 || late.after.ETAMinutes != 60 {
		t.Fatalf("after the delayed attempt, progress is done %d units %d percent %d eta %d, want 26/42 percent 61 eta 60",
			late.after.Done, late.after.Units, late.after.Percent, late.after.ETAMinutes)
	}
	final, err := st.GetSprint(ctx, "fixes")
	if err != nil {
		t.Fatal(err)
	}
	if final.Done != 26 || final.Units != 42 || final.Percent != 61 || final.ETAMinutes != 60 {
		t.Fatalf("final progress is done %d units %d percent %d eta %d, want 26/42 percent 61 eta 60",
			final.Done, final.Units, final.Percent, final.ETAMinutes)
	}
	if final.Goal != "the fixes day" {
		t.Fatalf("goal = %q, the progress write rewrote the sprint", final.Goal)
	}
}

// lateWriter stalls the first publish after it has measured, lands a newer
// snapshot, then lets the first publish try to commit what it measured.
type lateWriter struct {
	Store
	once      sync.Once
	saw       bool
	published bool
	after     Sprint
	innerErr  error
}

func (w *lateWriter) PublishProgress(ctx context.Context, name string, measure func(ProgressView) (Progress, error)) (bool, error) {
	stalled := false
	ok, err := w.Store.PublishProgress(ctx, name, func(v ProgressView) (Progress, error) {
		p, merr := measure(v)
		if merr != nil {
			return Progress{}, merr
		}
		w.once.Do(func() {
			stalled = true
			w.innerErr = landNewerAcceptance(ctx, w.Store, name)
		})
		return p, nil
	})
	if stalled {
		w.saw = true
		w.published = ok
		if sp, gerr := w.Store.GetSprint(ctx, name); gerr != nil {
			w.innerErr = gerr
		} else {
			w.after = sp
		}
	}
	return ok, err
}

func landNewerAcceptance(ctx context.Context, st Store, name string) error {
	task, err := st.GetTask(ctx, "a")
	if err != nil {
		return err
	}
	task.State = StateClosed
	task.DoneAt = time.Date(2026, 9, 22, 16, 30, 0, 0, time.UTC)
	if err := st.PutTask(ctx, task); err != nil {
		return err
	}
	return RecordAcceptance(ctx, st, name, acceptanceLive)
}
