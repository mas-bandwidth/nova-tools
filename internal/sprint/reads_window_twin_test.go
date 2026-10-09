package sprint_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seat is told when broken reads outrun ok reads (docs/SPEC-SPRINT.md section 8; the
// owner, 2026-10-06 8:30 PM ET: "We have to catch broken reads faster than this. You should
// get some notification."), on the twin with the rig's clock: the verdicts are the readers'
// own, recorded by the read verb, and the tick reads the ledger they leave.

// The two finding classes the broken reads give.
const (
	missingFinding = "RULE: the branch this read names is not on origin; there is no commit to judge"
	lineFinding    = "internal/sprint/x.go:3 breaks STEP 2: return the error"
)

// readsRig is the hold rig with the friends held, so every read is a reader's, and verdict
// the verdict each reader gives.
type readsRig struct {
	*holdRig
	verdict map[string]string
	gave    map[string]map[string]int // reader -> verdict -> count
	n       int
}

func newReadsRig(t *testing.T, cards int, verdict map[string]string) *readsRig {
	t.Helper()
	r := &readsRig{holdRig: newHoldRig(t, cards, 0), verdict: verdict, gave: map[string]map[string]int{}}
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "the readers table alone"})
	return r
}

// round is one round of the sprint: a tick, every ready card taken and finished, a tick,
// and every read asked given its reader's verdict (a reader with none is left alone).
func (r *readsRig) round() {
	r.t.Helper()
	r.tick()
	for _, m := range []string{"m1", "m2"} {
		for _, c := range r.snap().Fleet.Cell(m, sprint.Ready) {
			r.must(store.TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: m}))
			wc := r.snap().Fleet.Card(c.ID)
			r.must(store.FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: wc.Int("gen")}, Head: pushedSha, Who: m}))
		}
	}
	r.tick()
	for _, c := range r.snap().Readers.Column(sprint.Asked, sprint.Reading) {
		v := r.verdict[c.Row]
		if v == "" {
			continue
		}
		req := sprint.ReadReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Verdict: v, Who: c.Row}
		if v == "broken" {
			req.Finding = missingFinding
			if r.n%3 == 2 {
				req.Finding = lineFinding
			}
			r.n++
		}
		r.must(store.ReadStep(req))
		if r.gave[c.Row] == nil {
			r.gave[c.Row] = map[string]int{}
		}
		r.gave[c.Row][v]++
	}
}

// until runs rounds until done says so, at most fifty.
func (r *readsRig) until(done func() bool) {
	r.t.Helper()
	for range 50 {
		if done() {
			return
		}
		r.round()
	}
	r.t.Fatalf("not done after fifty rounds: %v", r.gave)
}

// judged is the ids of every judgment of the type the log holds, and the open ones.
func (r *readsRig) judged(typ string) (written map[string]string, open []sprint.Open) {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	written = map[string]string{}
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == typ && l.Note.Kind == sprint.Judgment {
			written[l.Note.ID] = l.Note.What
		}
	}
	for _, o := range r.snap().Open {
		if o.Note.Type == typ {
			open = append(open, o)
		}
	}
	return written, open
}

func (r *readsRig) total(v string) int {
	n := 0
	for _, g := range r.gave {
		n += g[v]
	}
	return n
}

func TestBrokenReadsOutrunningOkRaiseOneNotice(t *testing.T) {
	t.Parallel()
	r := newReadsRig(t, 60, map[string]string{"reader-a": "broken", "reader-b": "ok", "reader-c": "broken"})
	r.until(func() bool { return r.total("ok")+r.total("broken") >= sprint.ReadsWindowMin })
	r.tick()
	written, open := r.judged(sprint.NBrokenReadsOutrun)
	require.Len(t, open, 1, "one notice while broken outrun ok: %v", r.gave)
	what := open[0].Note.What
	assert.Contains(t, what, "broken reads outrun ok reads")
	for rd, g := range r.gave {
		assert.Contains(t, what, fmt.Sprintf("%s %d ok, %d broken", strings.TrimPrefix(rd, sprint.ReaderPrefix), g["ok"], g["broken"]), "it names each reader's counts, by machine")
	}
	assert.Contains(t, what, "top findings: ")
	assert.Contains(t, what, fmt.Sprintf("%q", missingFinding[:sprint.FindingClassLen]), "the finding classes, by their first 60 characters")
	assert.Equal(t, []string{"look at the readers", "raise the read tier", "act"}, open[0].Note.Decisions)
	assert.Len(t, written, 1)

	// more broken reads in the same episode: still the one notice, its counts brought up to date
	r.round()
	r.round()
	r.tick()
	written, open = r.judged(sprint.NBrokenReadsOutrun)
	require.Len(t, open, 1)
	assert.Len(t, written, 1, "once an episode, never once a tick")

	// thirty minutes of running time on, the window is empty: the episode closes
	r.mu.Lock()
	r.now = r.now.Add(sprint.ReadsWindowSpan + time.Minute)
	r.mu.Unlock()
	r.tick()
	_, open = r.judged(sprint.NBrokenReadsOutrun)
	assert.Empty(t, open, "the window fell below the bar: the notice closes")

	// the where record carries the window's counts
	w := sprint.ReadsWindowOf(r.snap(), nil)
	assert.Zero(t, w.OK+w.Broken)
	assert.Equal(t, r.st.Now().Add(-sprint.ReadsWindowSpan), w.Since)

	// a new episode raises a new notice
	r.until(func() bool { _, o := r.judged(sprint.NBrokenReadsOutrun); return len(o) == 1 })
	written, _ = r.judged(sprint.NBrokenReadsOutrun)
	assert.Len(t, written, 2, "the next episode is told again")
}

func TestOkReadsOutrunningBrokenRaiseNoNotice(t *testing.T) {
	t.Parallel()
	r := newReadsRig(t, 30, map[string]string{"reader-a": "broken", "reader-b": "ok", "reader-c": "ok"})
	r.until(func() bool { return r.total("ok")+r.total("broken") >= 2*sprint.ReadsWindowMin })
	r.tick()
	written, _ := r.judged(sprint.NBrokenReadsOutrun)
	assert.Empty(t, written, "%v", r.gave)
	w := sprint.ReadsWindowOf(r.snap(), nil)
	assert.Equal(t, r.total("ok"), w.OK)
	assert.Equal(t, r.total("broken"), w.Broken)
}

func TestAReaderBreakingNearlyEverythingIsNamedOnce(t *testing.T) {
	t.Parallel()
	r := newReadsRig(t, 60, map[string]string{"reader-a": "broken", "reader-b": "ok"}) // reader-c reads nothing
	r.hold(sprint.HoldReq{Names: []string{"reader-c"}, Reason: "two readers"})
	r.until(func() bool {
		return r.gave["reader-a"]["broken"] >= sprint.ReaderWindowLen && r.gave["reader-b"]["ok"] >= sprint.ReaderOtherMin
	})
	r.tick()
	written, open := r.judged(sprint.NReaderBreaks)
	require.Len(t, open, 1, "%v", r.gave)
	assert.Equal(t, sprint.StreamSubject(sprint.ReaderSubject("a")), open[0].Subject(), "a reader is its machine")
	assert.Contains(t, open[0].Note.What, "reader a breaks nearly everything: 20 of its last 20 verdicts broken,")
	assert.Contains(t, open[0].Note.What, "while b broke 0 of its last")
	assert.Equal(t, "hold reader-a", open[0].Note.Decisions[0], "held by the row it reads on")
	assert.NotContains(t, open[0].Note.What, "tiers", "compared reader against reader, never by tier")
	assert.Len(t, written, 1)

	r.round()
	r.tick()
	written, open = r.judged(sprint.NReaderBreaks)
	assert.Len(t, open, 1)
	assert.Len(t, written, 1, "named once an episode")
	for _, o := range open {
		assert.NotContains(t, o.Note.What, "reader b ", "the reader that reads well is never named")
	}

	// reader-a reads well again: its last twenty fall under the bar and the episode closes
	r.verdict["reader-a"] = "ok"
	r.until(func() bool { return r.gave["reader-a"]["ok"] >= 5 })
	r.tick()
	_, open = r.judged(sprint.NReaderBreaks)
	assert.Empty(t, open, "%v", r.gave)
	assert.False(t, strings.Contains(fmt.Sprint(open), "reader-a"))
}
