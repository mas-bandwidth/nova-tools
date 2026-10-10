package friend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pacingHarness is a lanes harness standing in for a Claude Code headless
// run: each card turn spends Spend of the 5-hour window and its output ends
// with the rate_limit_event the harness prints, which the turn's report
// carries read by ReadRateLimitEvents. The window resets at resets; after
// it, a fresh window is spent at a tenth of the rate.
type pacingHarness struct {
	*lanesHarness
	mu      sync.Mutex
	now     func() time.Time
	spend   float64
	used    float64
	resets  time.Time
	maxUsed float64
	starts  []time.Time
}

func (h *pacingHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	h.mu.Lock()
	now := h.now()
	h.starts = append(h.starts, now)
	h.mu.Unlock()
	lt, err := h.lanesHarness.DeliverTo(ctx, session, text)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !now.Before(h.resets) {
		h.used, h.resets, h.spend = 0, h.resets.Add(5*time.Hour), h.spend/10
	}
	h.used += h.spend
	h.maxUsed = max(h.maxUsed, h.used)
	status := "allowed"
	if h.used >= 1 {
		status = "rejected"
	} else if h.used >= 0.5 {
		status = "allowed_warning"
	}
	out := fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q}
{"type":"rate_limit_event","rate_limit_info":{"status":%q,"resetsAt":%d,"rateLimitType":"five_hour","utilization":%.4f,"isUsingOverage":false}}
{"type":"result","subtype":"success","is_error":false}
`, session, status, h.resets.Unix(), h.used)
	lt.Windows = ReadRateLimitEvents(out, now)
	return lt, err
}

func (h *pacingHarness) seen() (starts []time.Time, maxUsed float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Time(nil), h.starts...), h.maxUsed
}

// A subscription friend's lanes are paced by the windows her harness
// reports: as the 5-hour window fills the effective width falls from her
// row's 4 (3, 2, 1, then none at the default pacing of 80 percent), so no
// run meets the hard limit; the coordinator is told once, as a judgment,
// when she is paced below half; the status carries the window's use beside
// the width; and when the window resets her lanes are back at the row's
// width with no hand on the wheel.
func TestPacingLowersWidthAsTheWindowFills(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var tasks [][2]string
		var ids []string
		finish := map[string]bool{}
		for i := 1; i <= 40; i++ {
			id := fmt.Sprintf("c%d", i)
			tasks, ids, finish[id] = append(tasks, [2]string{id, "queued"}), append(ids, id), true
		}
		dir := cardDirFixture(t, tasks, ids, nil)
		lh := &lanesHarness{dir: dir, finish: finish, active: map[string]int{}}
		r, _ := laneRig(t, lh, 4)
		resets := t0.Add(2 * time.Minute)
		h := &pacingHarness{lanesHarness: lh, spend: 0.05, resets: resets}
		h.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
		r.d.Deliver = h
		r.run(t, 200)

		records := strings.Join(r.records, "\n")
		var paced []string
		for _, l := range r.records {
			if i := strings.Index(l, "pacing: width "); i >= 0 {
				paced = append(paced, l[i:])
			}
		}
		require.NotEmpty(t, paced, "the pacer said nothing:\n%s", records)
		var widths []string
		for _, l := range paced {
			widths = append(widths, strings.Fields(l)[2]+"->"+strings.Fields(l)[4])
		}
		assert.Equal(t, []string{"4->3", "3->2", "2->1", "1->0", "0->4"}, widths, "the width falls step by step as the window fills, and is the row's again at the reset:\n%s", strings.Join(paced, "\n"))
		assert.Contains(t, paced[0], "five_hour at ")
		assert.Contains(t, paced[0], "of 80%", "the default pacing")
		assert.Contains(t, paced[len(paced)-1], "no window over the pacing")

		starts, maxUsed := h.seen()
		assert.Less(t, maxUsed, 1.0, "no run met the hard limit")
		assert.LessOrEqual(t, maxUsed, DefaultPacing+0.05*4, "at most the turns in flight spent past the pacing")
		var at0 time.Time
		for _, l := range r.records {
			if strings.Contains(l, "pacing: width 1 -> 0") {
				at0, _ = time.Parse(time.RFC3339, strings.Fields(l)[0])
			}
		}
		require.False(t, at0.IsZero(), records)
		var during, after int
		for _, s := range starts {
			switch {
			case s.After(at0) && s.Before(resets):
				during++
			case !s.Before(resets):
				after++
			}
		}
		assert.Zero(t, during, "nothing starts while the window is at the pacing")
		assert.Positive(t, after, "the lanes run again once the window resets")

		var judged []string
		for _, m := range r.adaMessages(t) {
			if strings.Contains(m.Subject, "paced to") {
				judged = append(judged, m.Subject)
				assert.Equal(t, "blocker", m.Kind, "a judgment")
			}
		}
		require.Len(t, judged, 1, "told once, however low and however long:\n%s", records)
		assert.Contains(t, judged[0], "friend bob: paced to 1 of 4 lanes")
		assert.Contains(t, judged[0], "5h ")

		r.mu.Lock()
		statuses := append([]Status(nil), r.status...)
		r.mu.Unlock()
		var sawZero bool
		for _, s := range statuses {
			if s.Paced != nil && *s.Paced == 0 {
				sawZero = true
				assert.Contains(t, s.Window, "5h ", "the window's use beside the width")
				assert.Contains(t, s.Lanes, ":paced", "a lane beyond the paced width says so")
				assert.Equal(t, 4, s.Width, "the row's width is unchanged")
			}
		}
		assert.True(t, sawZero, "a status said the lanes paced to none")
		last := statuses[len(statuses)-1]
		require.NotNil(t, last.Paced)
		assert.Equal(t, 4, *last.Paced)
		assert.Equal(t, "80%", last.Pacing)
	})
}

// The report a Claude Code headless run prints is read for each window's
// last word; a line with no utilization is under the warning threshold.
func TestReadRateLimitEventsTakesEachWindowsLastWord(t *testing.T) {
	t.Parallel()
	at := t0
	out := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1788100000,"rateLimitType":"five_hour"}}
not json
{"type":"assistant","message":{"content":"rate_limit_event"}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1788505200,"rateLimitType":"seven_day","utilization":0.62,"surpassedThreshold":0.5}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1788100000,"rateLimitType":"five_hour","utilization":0.91}}`
	got := ReadRateLimitEvents(out, at)
	require.Len(t, got, 2)
	assert.Equal(t, WindowUse{Window: WindowFiveHour, Used: 0.91, Status: "allowed_warning", Resets: time.Unix(1788100000, 0).UTC(), At: at}, got[0])
	assert.Equal(t, WindowSevenDay, got[1].Window)
	assert.InDelta(t, 0.62, got[1].Used, 1e-9)
	assert.Empty(t, ReadRateLimitEvents("plain text\n", at))
}

// The effective width is the row's scaled by the share of the paced budget
// left, rounded up; none at the pacing or when rejected; the row's again
// once the window resets; and a report with no utilization keeps the use
// last read for the same reset.
func TestPacerWidthFollowsTheTightestWindow(t *testing.T) {
	t.Parallel()
	now := t0
	reset5, reset7 := now.Add(time.Hour), now.Add(72*time.Hour)
	var p Pacer
	n, why := p.Width(8, 0.8, now)
	assert.Equal(t, 8, n)
	assert.Empty(t, why)

	p.Observe([]WindowUse{{Window: WindowFiveHour, Used: 0.2, Resets: reset5, At: now}, {Window: WindowSevenDay, Used: 0.5, Resets: reset7, At: now}})
	n, why = p.Width(8, 0.8, now)
	assert.Equal(t, 3, n, "7d: 8 * 0.3/0.8 = 3")
	assert.Equal(t, WindowSevenDay, why)
	assert.Equal(t, "5h 20% 7d 50%", p.Use(now))

	p.Observe([]WindowUse{{Window: WindowFiveHour, Used: 0, Status: "allowed", Resets: reset5, At: now}})
	assert.Equal(t, "5h 20% 7d 50%", p.Use(now), "no utilization keeps the use read for the same reset")

	p.Observe([]WindowUse{{Window: WindowFiveHour, Used: 0.8, Resets: reset5, At: now}})
	n, why = p.Width(8, 0.8, now)
	assert.Equal(t, 0, n, "at the pacing: none")
	assert.Equal(t, WindowFiveHour, why)
	n, _ = p.Width(8, 0.9, now)
	assert.Equal(t, 1, n, "a looser pacing on the row: 8 * 0.1/0.9 rounds up to one")

	n, why = p.Width(8, 0.8, reset5)
	assert.Equal(t, 3, n, "the 5h window reset; the 7d one still holds")
	assert.Equal(t, WindowSevenDay, why)
	n, _ = p.Width(8, 0.8, reset7)
	assert.Equal(t, 8, n, "both reset: the row's width")

	p.Observe([]WindowUse{{Window: WindowFiveHour, Used: 0.1, Status: "rejected", Resets: reset7.Add(time.Hour), At: reset7}})
	n, _ = p.Width(8, 0.8, reset7)
	assert.Equal(t, 0, n, "rejected by the harness: none until the reset")
}

// The row's pacing is a fraction of each window; none, or one out of range,
// is the default.
func TestPacingIsTheRowsSetting(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, DefaultPacing, PacingOf(0), 1e-9)
	assert.InDelta(t, DefaultPacing, PacingOf(1.2), 1e-9)
	assert.InDelta(t, 0.5, PacingOf(0.5), 1e-9)
	assert.Equal(t, "80%", PacingText(DefaultPacing))
}

// The newer shape names every window in one event; a rejection marks the
// window it spent.
func TestReadRateLimitEventsReadsTheUnifiedWindows(t *testing.T) {
	t.Parallel()
	five, seven := t0.Add(time.Hour), t0.Add(50*time.Hour)
	got := ReadRateLimitEvents(claudeEvent("rejected", false, 0.6, 1.0, five, seven), t0)
	require.Len(t, got, 2)
	assert.Equal(t, WindowUse{Window: WindowFiveHour, Used: 0.6, Status: "allowed", Resets: five.UTC().Truncate(time.Second), At: t0}, got[0])
	assert.Equal(t, WindowUse{Window: WindowSevenDay, Used: 1.0, Status: "rejected", Resets: seven.UTC().Truncate(time.Second), At: t0}, got[1])
}

// The batch turn is paced too: the gate reads every command's output for the
// harness's rate_limit_event (Watch), and a batch delivery while a window is
// at or past the row's pacing is Deferred without running, the message kept
// in hand, until the window resets; the friend is not sent down, since the
// harness has not refused. A higher pacing lets the same use through.
func TestTheGatePacesTheBatchTurnByTheWindows(t *testing.T) {
	t.Parallel()
	clock := &limitClock{t: time.Date(2026, 10, 5, 22, 0, 0, 0, edt)}
	fiveReset, sevenReset := time.Date(2026, 10, 5, 23, 0, 0, 0, edt), time.Date(2026, 10, 9, 12, 0, 0, 0, edt)
	var downs []string
	pacing := 0.0
	l := &Limits{Now: clock.now, Pacing: func() float64 { return pacing }, Down: func(until time.Time, reason string) { downs = append(downs, reason) }}
	se := &scriptExec{
		outs: []string{
			"turn one\n" + claudeEvent("allowed_warning", false, 0.5, 0.2, fiveReset, sevenReset) + "\n",
			"turn two\n" + claudeEvent("allowed_warning", false, 0.82, 0.21, fiveReset, sevenReset) + "\n",
			"turn three\n" + claudeEvent("allowed_warning", false, 0.86, 0.22, fiveReset, sevenReset) + "\n",
			"turn four\n" + claudeEvent("allowed", false, 0.05, 0.23, fiveReset.Add(5*time.Hour), sevenReset) + "\n",
		},
		exits: []int{0, 0, 0, 0},
	}
	d := l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(se.run)})

	_, err := d.Deliver(context.Background(), "one")
	require.NoError(t, err)
	_, err = d.Deliver(context.Background(), "two")
	require.NoError(t, err, "at 50% of the 5-hour window the batch turn runs")

	_, err = d.Deliver(context.Background(), "three")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "at 82%, past the default pacing of 80%, the batch turn is held")
	assert.Contains(t, deferred.Reason, "five_hour at 82% of 80%")
	assert.Contains(t, deferred.Reason, "2026-10-05T23:00:00-04:00")
	assert.Len(t, se.texts, 2, "the held turn runs nothing")
	_, _, limited := l.Limited()
	assert.False(t, limited, "pacing is not a limit: the friend is not sent down")
	assert.Empty(t, downs)
	assert.Equal(t, "5h 82% 7d 21%", l.WindowUse(), "the windows as the harness last reported them")

	pacing = 0.9
	_, err = d.Deliver(context.Background(), "three")
	require.NoError(t, err, "a pacing of 90% lets 82% through")
	assert.Equal(t, []string{"one", "two", "three"}, se.texts)

	pacing = 0
	_, err = d.Deliver(context.Background(), "four")
	require.ErrorAs(t, err, &deferred, "back at the default, 86% is held")
	assert.Len(t, se.texts, 3)

	clock.t = fiveReset
	_, err = d.Deliver(context.Background(), "four")
	require.NoError(t, err, "once the window resets the batch turn runs again")
	assert.Equal(t, []string{"one", "two", "three", "four"}, se.texts)
	assert.Equal(t, "5h 5% 7d 23%", l.WindowUse())
	assert.Empty(t, downs, "no run met the hard limit")
}
