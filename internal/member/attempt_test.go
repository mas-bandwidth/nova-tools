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

// Every work take of a member with a decider is decided in its end's long work, over the
// child's result as RESULT.md says it and the finish's own report, and the finish carries the
// decision (--decision); whether it routes the finish is the card's bar, read by the server.
// A member with no decider asks nothing and carries no flag. A decision that cannot be made
// is said on one NOTE line and the finish goes by its reason line alone.
func TestAnEndedTakeIsDecidedAndTheFinishCarriesTheDecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		decide bool
		err    error
		flag   bool
		asks   int
	}{
		{"a decider asks", true, nil, true, 1},
		{"no decider asks nothing", false, nil, false, 0},
		{"a failed ask is a note", true, errors.New("the backend answered 402"), false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &attemptAsks{err: tc.err}
			cfg := Config{As: "m", Width: 2}
			if tc.decide {
				cfg.Attempt = a.decide
			}
			g := newRig(cfg)
			p := pk("c1")
			p.Gen = 2
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
