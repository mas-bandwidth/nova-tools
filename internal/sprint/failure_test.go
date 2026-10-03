package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Rule 2 (nova-tools#5174, the owner, 2026-10-02: "Escalate on the second identical failure,
// not the third."): identical is one function, SameFailure over FailureClass. A take whose
// child left no result is one class whatever its line; any other end is the member's reason,
// its first line before the "; " the child's report follows; the provider's failures, a
// staging refusal and an end with no line are never the card's and never identical.
func TestSameFailureIsTheOneDefinitionOfAnIdenticalFailure(t *testing.T) {
	t.Parallel()
	long := "push refused: " + strings.Repeat("x", 300)
	for _, tc := range []struct {
		name, a, b string
		class      string // FailureClass(a)
		same       bool
	}{
		{"no result twice, whatever the lines", "no result: no RESULT.md shape; quack", "no result: no RESULT.md shape; other", "no result", true},
		{"a verdict and the child's first words", "verdict not-done; tests red in x", "verdict not-done; tests red in y", "verdict not-done; tests red in", true},
		{"a verdict, another child's reason", "verdict not-done; tests red in x", "verdict not-done; ran out of ideas", "verdict not-done; tests red in", false},
		{"a verdict after the member's push", "verdict not-done; pushed=abc to b: tests red in x", "verdict not-done; pushed=def to c pr=4: tests red in y", "verdict not-done; tests red in", true},
		{"a verdict with no child's line", "verdict not-done", "verdict not-done; ", "verdict not-done", true},
		{"a launch refused is the member's", "launch refused: no model", "launch refused: no model", "", false},
		{"the same gate end", "budget: no RESULT.md shape; r1", "budget: no RESULT.md shape; r2", "budget: no RESULT.md shape", true},
		{"the same push refusal", "push refused: rejected (non-fast-forward); r", "push refused: rejected (non-fast-forward); s", "push refused: rejected (non-fast-forward)", true},
		{"the first line only", "no commit: nothing staged\nmore", "no commit: nothing staged\nother", "no commit: nothing staged", true},
		{"spaces around are not a difference", "  push refused: rejected ; r", "push refused: rejected; s", "push refused: rejected", true},
		{"another reason", "verdict not-done; r", "budget: no RESULT.md shape; r", "verdict not-done; r", false},
		{"no result against a failure", "no result: no RESULT.md shape", "no RESULT.md shape; r", "no result", false},
		{"another why", "nothing to do: done in #12; r", "nothing to do: the file is gone; r", "nothing to do: done in #12", false},
		{"a provider failure is never the card's", "provider failure: provider: class=5xx status=502 msg=bad gateway", "provider failure: provider: class=5xx status=502 msg=bad gateway", "", false},
		{"a staging refusal is the member's", "staging refused: no bench mirror", "staging refused: no bench mirror", "", false},
		{"an end with no line has no class", "", "", "", false},
		{"a long reason is cut, and cut alike", long + "; a", long + "; b", cutText(long, MaxProviderErrorBytes), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.class, FailureClass(tc.a))
			assert.Equal(t, tc.same, SameFailure(tc.a, tc.b))
			assert.Equal(t, tc.same, SameFailure(tc.b, tc.a), "the same both ways")
		})
	}
}

// The takes of one attempt's work card: the last two ended takes ended the same way
// (their records, the provider's line given its kind back) or not; a take with no record
// (its member went down) is never the same as another.
func TestIdenticalEndsReadsTheLastTwoTakesOfTheCard(t *testing.T) {
	t.Parallel()
	take := func(line string) string { return ProviderTake{Route: "r", Error: line}.String() }
	nr, prov := "no result: no RESULT.md shape; q", "stream error: server_error"
	for _, tc := range []struct {
		name    string
		redeals int
		takes   map[int]string
		class   string
	}{
		{"one take", 0, map[int]string{1: take(nr)}, ""},
		{"two takes with no result", 1, map[int]string{1: take(nr), 2: take(nr)}, "no result"},
		{"the provider twice is not the card's", 1, map[int]string{1: take(prov), 2: take(prov)}, ""},
		{"no result after the provider", 1, map[int]string{1: take(prov), 2: take(nr)}, ""},
		{"a member down between", 2, map[int]string{1: take(nr), 3: take(nr)}, ""},
		{"the last two of three", 2, map[int]string{1: take(prov), 2: take(nr), 3: take(nr)}, "no result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := map[string]string{"redeals": itoa(tc.redeals), FieldTakeEnded: "t"}
			for n, v := range tc.takes {
				f[FieldProviderTake+itoa(n)] = v
			}
			wc := &Card{ID: "p.w1", Fields: f}
			assert.Equal(t, tc.class, identicalEnds(wc))
			assert.Equal(t, tc.class != "" || tc.redeals >= MaxRedeals, redealBound(wc))
		})
	}
}
