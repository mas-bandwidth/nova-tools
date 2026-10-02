package store

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The Mem's wait on its log (waitlog.go): a line after the cursor returns at
// once, woken, with the log's last id; a quiet log waits the time its LogWait
// is handed and is not woken; a commit during a wait wakes it.
func TestTheMemsWaitOnTheLogWakesOnALine(t *testing.T) {
	t.Parallel()
	m := NewMem()
	var waited []time.Duration
	m.LogWait = func(d time.Duration) { waited = append(waited, d) }
	st := &Store{B: m, Names: sprint.Names{}, Now: func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }}
	ctx := context.Background()
	wait := time.Minute // handed to the fake: nothing waits on it
	note := func() {
		t.Helper()
		if _, err := st.Run(ctx, Step{Verb: "note", Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: sprint.NMachineStarted, Who: "tester", At: s.Now, What: "a line"}}}
		}}); err != nil {
			t.Fatal(err)
		}
	}
	tail, woke, err := st.WaitLog(ctx, 0, "", wait)
	if err != nil || woke || tail != "" || len(waited) != 1 || waited[0] != wait {
		t.Fatalf("an empty log: tail %q woke %v err %v waited %v", tail, woke, err, waited)
	}
	note()
	cur, err := st.LogTail(ctx)
	require.NoError(t, err, "the tail after a line: %q %v", cur, err)
	require.NotEmpty(t, cur, "the tail after a line: %q %v", cur, err)
	tail, woke, err = st.WaitLog(ctx, 0, "", wait)
	if err != nil || !woke || tail != cur || len(waited) != 1 {
		t.Fatalf("a line after the cursor: tail %q woke %v err %v waited %v", tail, woke, err, waited)
	}
	tail, woke, err = st.WaitLog(ctx, 0, cur, wait)
	if err != nil || woke || tail != cur || len(waited) != 2 {
		t.Fatalf("nothing after the cursor: tail %q woke %v err %v waited %v", tail, woke, err, waited)
	}
	// a commit during the wait wakes it
	m.LogWait = func(time.Duration) { note() }
	tail, woke, err = st.WaitLog(ctx, 0, cur, wait)
	if err != nil || !woke || memSeq(tail) <= memSeq(cur) {
		t.Fatalf("a line during the wait: tail %q woke %v err %v", tail, woke, err)
	}
	require.Equal(t, 4, m.Calls["waitlog"], "waits: %d exchanges, want one a wait (4)", m.Calls["waitlog"])
}

// With no LogWait the Mem waits for a commit: a wait with an hour to go
// returns as soon as another writer's line lands. The wait is bounded by a
// deadline, so a wait that is never woken fails the test at the deadline and not
// at the package's timeout, and the line lands after the wait began.
func TestTheMemsWaitReturnsOnACommit(t *testing.T) {
	t.Parallel()
	m := NewMem()
	st := &Store{B: m, Names: sprint.Names{}, Now: func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }}
	// the bound of a wait that is never woken: never waited out while it is
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type outcome struct {
		woke bool
		err  error
	}
	got := make(chan outcome, 1)
	go func() {
		_, woke, err := st.WaitLog(ctx, 0, "", time.Hour)
		got <- outcome{woke, err}
	}()
	// the wait has begun (its exchange is counted as it takes its wake)
	for ctx.Err() == nil {
		m.mu.Lock()
		began := m.Calls["waitlog"] > 0
		m.mu.Unlock()
		if began {
			break
		}
		runtime.Gosched()
	}
	_, err := st.Run(context.Background(), Step{Verb: "note", Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: sprint.NMachineStarted, Who: "tester", At: s.Now, What: "a line"}}}
	}})
	require.NoError(t, err)
	if o := <-got; !o.woke || o.err != nil {
		t.Fatalf("the wait returned woke %v, err %v: a commit during it wakes it at once", o.woke, o.err)
	}
}
