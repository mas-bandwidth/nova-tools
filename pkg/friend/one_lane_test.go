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

// One live lane per card (docs/SPEC-FRIEND.md, one lane per card): the night of
// 2026-10-05 the same card ran in two lanes at once, and the finish of one left the other
// running to no purpose.

// TestOneLaneRunsPerCard starts two lanes for one card and sees the second refused, and
// finishes a card and sees its other lane ended.
func TestOneLaneRunsPerCard(t *testing.T) {
	t.Parallel()

	t.Run("two daemons on one directory start one lane for a card; the second is refused", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			// a friend's duplicate runners: two daemons on her one working directory
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}, block: make(chan struct{})}
			// each daemon's pause waits for the bubble to settle, one daemon at a time (Wait is
			// never called from two goroutines at once; a send on the channel is a durable block)
			settle := make(chan struct{}, 1)
			var rigs [2]*rig
			for i := range rigs {
				rigs[i], _ = laneRig(t, h, 1)
				rigs[i].d.Pause = func(context.Context, time.Duration) {
					settle <- struct{}{}
					synctest.Wait()
					<-settle
				}
			}
			rigs[0].at[10] = func() { close(h.block) }
			var wg sync.WaitGroup
			errs := make([]error, len(rigs))
			for i, r := range rigs {
				ctx, cancel := context.WithCancel(context.Background())
				r.cancel, r.stopAfter = cancel, 20
				wg.Go(func() { errs[i] = r.d.Run(ctx) })
			}
			wg.Wait()
			require.NoError(t, errs[0])
			require.NoError(t, errs[1])

			turns, _, _ := h.got()
			assert.Equal(t, []string{"ses_1: c1"}, turns, "one lane ran the card")
			assert.Equal(t, 1, h.maxBusy)
			records := strings.Join(append(append([]string{}, rigs[0].records...), rigs[1].records...), "\n")
			assert.Equal(t, 1, strings.Count(records, "lane 1: card c1 refused: bob lane 1 (daemon "), records)
			assert.Contains(t, records, "runs it (one live lane per card)")
			assert.Equal(t, 1, strings.Count(records, " card=done "), records)
			m, ok := ReadLaneMark(dir, "c1~15")
			require.True(t, ok)
			assert.True(t, m.Ended, "the lane that ran it ended it")
			assert.True(t, strings.HasPrefix(m.Who, "bob lane 1 (daemon "), m.Who)
		})
	})

	t.Run("a card her row's running list names another friend running is refused", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
			h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
			r, _ := laneRig(t, h, 1)
			r.d.Running = func() map[string]string { return map[string]string{"c1~15": "zhi", "c2": "bob"} }
			r.run(t, 10)
			turns, _, _ := h.got()
			assert.Equal(t, []string{"ses_1: c2"}, turns, "c1 is zhi's; c2 is named running by bob herself")
			records := strings.Join(r.records, "\n")
			assert.Equal(t, 1, strings.Count(records, "lane 1: card c1 refused: zhi runs it (one live lane per card)"), "said once while it stands: %s", records)
			_, ok := ReadLaneMark(dir, "c1~15")
			assert.False(t, ok, "a refused card is never claimed")
		})
	})

	// finished is the rig of a lane running c1, its card on her row, its turn held until the
	// run's end; at step 8 another lane finishes the card by what finish does.
	finished := func(t *testing.T, finish func(dir string, r *rig, gone *bool)) (string, *rig, *LaneState, *lanesHarness) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, block: make(chan struct{})}
		r, state := laneRig(t, h, 1)
		var mu sync.Mutex
		gone := false
		r.d.Held = func(context.Context) (Row, error) {
			mu.Lock()
			defer mu.Unlock()
			if gone {
				return Row{From: FromCards}, nil
			}
			return Row{From: FromCards, Cards: []HeldCard{{Card: "c1", Job: "c1~15", Col: "working", Brief: "RESULT: c1\n"}}}, nil
		}
		r.at[8] = func() { mu.Lock(); finish(dir, r, &gone); mu.Unlock() }
		r.run(t, 16)
		return dir, r, state, h
	}

	t.Run("a card that leaves her row for another friend's lane ends this lane", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir, r, state, h := finished(t, func(_ string, r *rig, gone *bool) {
				*gone = true
				r.d.Running = func() map[string]string { return map[string]string{"c1": "zhi"} }
			})
			turns, _, _ := h.got()
			assert.Equal(t, []string{"ses_1: c1"}, turns)
			records := strings.Join(r.records, "\n")
			assert.Contains(t, records, "lane 1: card c1 ended: card finished by zhi; its run is stopped and nothing is finished by this lane")
			assert.Contains(t, records, `card=ended reason="card finished by zhi"`, "the run came back stopped before the daemon's end")
			raw, err := os.ReadFile(filepath.Join(dir, "jobs", "c1~15", LaneMarkFile))
			require.NoError(t, err)
			assert.Equal(t, "ended: card finished by zhi\n", string(raw))
			assert.NoFileExists(t, filepath.Join(dir, "outbox", "c1~15", "REPORT.md"), "the finish is the other lane's: this lane writes none")
			assert.Empty(t, state.Started)
			assert.Contains(t, state.GivenUp, "c1~15", "never handed again")
			assert.Equal(t, "1:ses_1:-", r.last().Lanes, "the lane is free")
		})
	})

	t.Run("another lane's finish of the same job ends this lane", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir, r, state, _ := finished(t, func(dir string, _ *rig, _ *bool) {
				// the other lane's end, as endCard writes it: her report, then the mark
				out := filepath.Join(dir, "outbox", "c1~15")
				require.NoError(t, os.MkdirAll(out, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(out, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644))
				require.NoError(t, endLaneMark(dir, "c1~15", "bob lane 2 (daemon 7.1)"))
			})
			records := strings.Join(r.records, "\n")
			assert.Contains(t, records, "lane 1: card c1 ended: card finished by bob lane 2 (daemon 7.1)")
			assert.Contains(t, records, `card=ended reason="card finished by bob lane 2 (daemon 7.1)"`)
			assert.NotContains(t, records, "card=done", "this lane finishes nothing")
			m, _ := ReadLaneMark(dir, "c1~15")
			assert.Equal(t, LaneMark{Who: "bob lane 2 (daemon 7.1)", Ended: true}, m, "the other lane's word stands")
			raw, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
			require.NoError(t, err)
			assert.Equal(t, "Verdict: LAND\n", string(raw), "her report stands")
			assert.Empty(t, state.Started)
		})
	})
}

// A lane mark is claimed by one lane: a second claim names the holder, a stale one is taken
// over, an ended one is never claimed again.
func TestALaneMarkIsClaimedByOneLane(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	holder, err := ClaimLane(dir, "c1~15", "bob lane 1", at)
	require.NoError(t, err)
	assert.Empty(t, holder)
	holder, err = ClaimLane(dir, "c1~15", "bob lane 2", at.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "bob lane 1", holder)
	holder, err = ClaimLane(dir, "c1~15", "bob lane 1", at.Add(time.Minute))
	require.NoError(t, err)
	assert.Empty(t, holder, "its own mark is kept")
	holder, err = ClaimLane(dir, "c1~15", "bob lane 2", at.Add(time.Minute+LaneMarkStale))
	require.NoError(t, err)
	assert.Empty(t, holder, "a mark unrefreshed for LaneMarkStale is a gone lane's")
	m, _ := ReadLaneMark(dir, "c1~15")
	assert.Equal(t, LaneMark{Who: "bob lane 2", At: at.Add(time.Minute + LaneMarkStale)}, m)
	require.NoError(t, endLaneMark(dir, "c1~15", "bob lane 2"))
	holder, err = ClaimLane(dir, "c1~15", "bob lane 3", at.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, "bob lane 2", holder, "an ended card is never claimed again")
}
