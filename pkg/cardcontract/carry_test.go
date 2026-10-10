package cardcontract

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A rework's JOB.md says where it was staged: the tip of its base branch, and whether the work
// of the attempt before came with it; work that did not apply is asked to be redone first,
// before "Do that first". A rework whose base never moves keeps the sentence it had.
func TestJobTextSaysWhereAReworkWasStaged(t *testing.T) {
	t.Parallel()
	const tip, prev, carried = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333"
	f := Frame{Kind: "work", Card: "c1.w3", Attempt: 3, Repo: cardURL, BaseRef: "main", Branch: "sprint/c1.w3", PrevHead: prev, PrevFrom: 2, Fix: "assert the bound"}
	at := "Attempt 3 of this card. This checkout is staged at the tip of main as origin held it when it was staged, " + tip
	for _, tc := range []struct {
		name   string
		carry  Carry
		want   []string
		absent []string
	}{
		{"carried", Carry{Base: "main", Tip: tip, Prev: prev, From: 2, Staged: carried, State: CarryOK},
			[]string{at + ", with the work of attempt 2 (its head " + prev + ") carried on top as one commit, " + carried + ", and the checkout starts from that commit.\n"},
			[]string{"must be redone", "continues attempt"}},
		{"on the tip already", Carry{Base: "main", Tip: tip, Prev: prev, From: 2, Staged: prev, State: CarryOK},
			[]string{at + ": attempt 2's head, " + prev + ", descends from it, and the checkout starts from that head.\n"}, []string{"must be redone"}},
		{"held", Carry{Base: "main", Tip: tip, Prev: prev, From: 2, Staged: tip, State: CarryHeld},
			[]string{at + ", which already holds the work of attempt 2 (its head " + prev + ").\n"}, []string{"must be redone"}},
		{"conflict", Carry{Base: "main", Tip: tip, Prev: prev, From: 2, Staged: tip, State: CarryConflict},
			[]string{at + ". The work of attempt 2 (its head " + prev + ") did not apply cleanly at the tip and is not in this checkout.\n",
				"The coordinator asks: assert the bound\nThe previous work: the work of attempt 2 must be redone from this tip: `git diff " + tip + "..." + prev + "` shows it, and the commit you make holds it again with this attempt's fix.\nDo that first; a finish with no new commit is refused.\n"},
			nil},
		{"none pushed", Carry{Base: "main", Tip: tip, Staged: tip, State: CarryNone},
			[]string{at + "; no attempt before this one pushed work to carry.\n"}, []string{"must be redone"}},
	} {
		c := tc.carry
		for _, family := range []string{"claude", "plain"} {
			text := For(family).JobText(f, Staged{Job: "/j", Repo: "/j/repo", Head: c.Staged, Carry: &c})
			for _, w := range tc.want {
				assert.Contains(t, text, w, "%s %s", tc.name, family)
			}
			for _, a := range tc.absent {
				assert.NotContains(t, text, a, "%s %s", tc.name, family)
			}
		}
	}
	assert.Contains(t, For("claude").JobText(f, Staged{Job: "/j", Repo: "/j/repo", Head: prev}),
		"This checkout continues attempt 2: its head, "+prev+", is the last pushed by any attempt before this one", "no carry: a base that never moves")
}

// Native's carry line is the carry's words behind its prefix, and the member reads the last
// one from native's log; a log with none reads "".
func TestTheCarryLineIsReadBackFromNativesLog(t *testing.T) {
	t.Parallel()
	c := Carry{Base: "sprint/s1", Tip: "1111111111111111111111111111111111111111", Prev: "2222222222222222222222222222222222222222", From: 2,
		Staged: "3333333333333333333333333333333333333333", State: CarryOK}
	assert.Equal(t, "STAGE CARRY staged=333333333333 tip=111111111111 of sprint/s1 carry=carried attempt=2 prev=222222222222", c.Line())
	log := []byte("STAGE OK bench=b repo=r base=33333333 secs=1\n" + c.Line() + "\nFRAME OK secs=0.1\n")
	assert.Equal(t, c.Words(), ParseCarryLine(log))
	assert.Empty(t, ParseCarryLine([]byte("STAGE OK bench=b\nFRAME OK secs=0.1\n")))
	none := Carry{Base: "main", Tip: c.Tip, Staged: c.Tip, State: CarryNone}
	assert.Equal(t, "staged=111111111111 tip=111111111111 of main carry=none", none.Words())
}
