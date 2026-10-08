package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A daemon starting up over a lane's run that is still alive adopts it: the lane holds the
// card and waits on the process, no FAIL report is written over the working run, and the
// card ends as any run's does when the process exits with its report written (the stopgap
// runners' `adopt`; until now the daemon finished every started card failed at once,
// internal/friend/tla/LaneEnd.tla AdoptedNotFailed). A run whose process is gone is ended
// as before.
func TestARestartedDaemonAdoptsALiveLaneRunAndFinishesItsCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}, block: make(chan struct{})}
		r, state := laneRig(t, h, 2)
		c1 := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		*state = LaneState{Sessions: map[int]string{1: "ses_1", 2: "ses_2"}, Started: map[string]Started{"c1~15": {Lane: 1, Card: c1, At: t0.Add(-time.Minute), Pid: 4242}}}
		var mu sync.Mutex
		alive := true
		r.d.ProcessAlive = func(pid int) bool { mu.Lock(); defer mu.Unlock(); return pid == 4242 && alive }
		ended := make(chan struct{})
		r.d.WaitProcess = func(ctx context.Context, pid int) bool {
			require.Equal(t, 4242, pid)
			select {
			case <-ended:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var holding Status
		r.at[4] = func() { holding = r.last() }
		r.at[6] = func() { // the live run finishes its card and exits
			require.NoError(t, os.MkdirAll(c1.Outbox, 0o755))
			require.NoError(t, os.WriteFile(c1.Report(), []byte("Verdict: LAND\nHead: abc\n\nfine\n"), 0o644))
			require.NoError(t, os.WriteFile(c1.Result(), []byte("RESULT: c1\n"), 0o644))
			mu.Lock()
			alive = false
			mu.Unlock()
			close(ended)
		}
		r.at[8] = func() { close(h.block) } // lane 2's own card ends after
		r.run(t, 14)
		turns, _, seeds := h.got()
		assert.Empty(t, seeds, "both lanes keep their sessions")
		assert.Equal(t, []string{"ses_2: c2"}, turns, "lane 1 waits on the adopted run and pushes nothing; lane 2 works on")
		assert.Equal(t, "1:ses_1:c1/1 2:ses_2:c2/1", holding.Lanes, "the adopted card is in lane 1's hand")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "card c1 was begun at "+t0.Add(-time.Minute).UTC().Format(time.RFC3339)+" by the daemon before, and its run (pid 4242) is alive: adopted")
		assert.Contains(t, records, "lane 1: card c1 adopted (pid 4242): the lane waits on the run")
		assert.NotContains(t, records, "its run is gone", "no FAIL over a working run")
		assert.Contains(t, records, `subject="card c1 (adopted)" messages=0`)
		assert.Contains(t, records, "card=done finish=report")
		raw, err := os.ReadFile(c1.Report())
		require.NoError(t, err)
		assert.Equal(t, "Verdict: LAND\nHead: abc\n\nfine\n", string(raw), "the friend's own report stands")
		assert.Empty(t, state.GivenUp, "a card finished by its adopted run is not set aside")
		assert.Empty(t, state.Started)
	})
}

// A started card whose process is gone is ended as before: the lane's FAIL report, the
// card set aside; and a run the exec started has its process id on the started mark, so the
// next daemon can tell the two apart.
func TestAStartedCardWhoseRunIsGoneIsEndedAndARunsPidIsRecorded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}, block: make(chan struct{})}
		h.pid = 777
		r, state := laneRig(t, h, 1)
		c1 := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{"c1~15": {Lane: 1, Card: c1, At: t0.Add(-time.Minute), Pid: 4242}}}
		r.d.ProcessAlive = func(pid int) bool { return false }
		midPid, midStarted := 0, false
		r.at[6] = func() {
			st, ok := state.Started["c2~15"]
			midPid, midStarted = st.Pid, ok
		}
		r.at[8] = func() { close(h.block) }
		r.run(t, 12)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "card c1 was begun at "+t0.Add(-time.Minute).UTC().Format(time.RFC3339)+" and its run is gone: finish=failed")
		assert.Equal(t, []string{"c1~15"}, state.GivenUp)
		require.True(t, midStarted, "the running card is marked started")
		assert.Equal(t, 777, midPid, "with the process the exec handed")
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: c2"}, turns)
	})
}
