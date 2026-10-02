package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A budget that ends a card says which budget it was and at what count, with what the
// run cost to the cent, rounded up (nova-tools #5094: two Mercury cards ended
// `budget: no RESULT.md shape` at two cents each, and nothing said it was the token
// count that ended them).
func TestTheBudgetLineNamesWhichBudgetAndTheCount(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		stopped       string
		tokens, spent int
		partial       bool
		cost          string
		want          string
	}{
		"the token budget, with the cost":   {stoppedTokens, 400000, 509940, false, "0.0211", "tokens 509,940 of 400,000, $0.03"},
		"a partial count says so":           {stoppedTokens, 400000, 419088, true, "0.0178", "tokens 419,088+ of 400,000, $0.02"},
		"no cost reported":                  {stoppedTokens, 1000, 1200, false, "", "tokens 1,200 of 1,000, cost unreported"},
		"a card budget names its own field": {stoppedMaxTurns, 0, 50000, false, "0.5", "max_turns, tokens 50,000, $0.50"},
		"a source that stopped answering":   {stoppedUnverifiable, 400000, 12, false, "", "unverifiable: the usage source stopped answering, tokens 12 of 400,000, cost unreported"},
	} {
		assert.Equal(t, c.want, nativeBudgetWords(c.stopped, c.tokens, c.spent, c.partial, c.cost), name)
	}
}

// From native's log to the member's finish: the NATIVE BUDGET line rides into the failed
// finish's reason after the end, so the coordinator's judgment reads which budget ended the
// run instead of `budget: no RESULT.md shape`.
func TestTheFinishOfABudgetEndedRunCarriesTheBudgetLine(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	write(t, logPath, "NATIVE INCOMPLETE label=c1 job=j tmp=t rc=-1 wall=none harness=ok budget=509940/400000 stopped=tokens spend=input:509940,output:710,cost:0.0211 why=no-result\n"+
		"NATIVE BUDGET label=c1 budget: tokens 509,940 of 400,000, $0.03\n")
	c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	fin, why := member.Judge(c.Result(), member.Push{None: "the child committed nothing"})
	assert.Equal(t, member.FinishFailed, fin)
	assert.Equal(t, "budget: tokens 509,940 of 400,000, $0.03: no RESULT.md shape", why)
}
