package member

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attemptAsks is a fake attempt decider: it records what it was attemptAsks over and answers with a
// card line and a record line, or with err.
type attemptAsks struct {
	mu      sync.Mutex
	results []string
	reasons []string
	err     error
}

func (a *attemptAsks) decide(p Packet, result, reason string) (string, []byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.results, a.reasons = append(a.results, result), append(a.reasons, reason)
	if a.err != nil {
		return "", nil, a.err
	}
	return "needs-pro p=0.840 op=" + p.Primary + "@1.0123456789ab", []byte(`{"id":"` + p.Primary + `@1.0123456789ab"}`), nil
}

// A work take whose packet carries the attempt bar is decided in its end's long work, over
// the child's result as RESULT.md says it and the finish's own report, and the finish
// carries the decision (--decision). No bar, no decider: no ask and no flag. A decision that
// cannot be made is said on one NOTE line and the finish goes by its reason line alone.
func TestAnEndedTakeIsDecidedAndTheFinishCarriesTheDecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, bar string
		err       error
		flag      bool
		asks      int
	}{
		{"the bar asks", "0.7", nil, true, 1},
		{"no bar asks nothing", "", nil, false, 0},
		{"a failed ask is a note", "0.7", errors.New("the backend answered 402"), false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &attemptAsks{err: tc.err}
			g := newRig(Config{As: "m", Width: 2, Attempt: a.decide})
			p := pk("c1")
			p.Gen, p.DecideAttempt = 2, tc.bar
			g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
			_, err := g.tick(t) // restart: the child is ours now
			require.NoError(t, err)
			g.r.child("c1").end(Result{Ran: true, Shaped: true, Verdict: "not-done", Head: "abc", Report: "tests red in x", Body: "## Gate\nFAIL TestX"})
			g.s.reset()
			_, err = g.tick(t)
			require.NoError(t, err)
			lines := g.s.lines("finish")
			require.Len(t, lines, 1)
			assert.True(t, strings.HasPrefix(lines[0], "finish --as m c1@2 --report verdict not-done; tests red in x --failed"), lines[0])
			assert.Equal(t, tc.flag, strings.Contains(lines[0], ` --decision {"id":"p-c1@1.0123456789ab"}`), lines[0])
			require.Len(t, a.reasons, tc.asks)
			if tc.asks > 0 {
				assert.Equal(t, "verdict not-done; tests red in x", a.reasons[0], "attemptAsks over the finish's report")
				assert.Equal(t, "head: abc\nverdict: not-done\nreport: tests red in x\n\n## Gate\nFAIL TestX\n", a.results[0], "and the result as RESULT.md says it")
			}
			assert.Equal(t, tc.err != nil, strings.Contains(g.out.String(), "NOTE attempt c1 not decided: the backend answered 402; the reason line routes the finish"), g.out.String())
		})
	}
	assert.Empty(t, ResultText(Result{}), "a child that wrote no result has no RESULT.md")
}
