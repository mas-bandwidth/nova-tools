package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// A dogfood run found `nova-merge queue release oops` taking the extra word, releasing the
// hold, and exiting 0. The word was silently dropped, which is the whole of the bug: a
// caller who typed a subverb's argument in the wrong place, or typed a second one, got a
// MUTATION they did not ask for and a zero exit telling them it went as they meant.
//
// The class is `scanQueueArgs` returning positionals that the subverb then ignores, so it
// is checked here for EVERY subverb at once rather than for the one that was reported:
// release, sweep and classify all took extras and all dropped them. The three that use
// their positionals -- hold (the reason), skip and unskip (the numbers), front (the one
// number) -- are held to the same rule from the other side, so that a future subverb
// cannot quietly join the first group.
func TestNoQueueSubverbSilentlyDropsAnExtraPositional(t *testing.T) {
	t.Parallel()

	// The subverbs that take NO positional argument of their own. An extra word is a
	// refusal at exit 2, and NOTHING is written.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"release", []string{"release", "oops"}},
		{"sweep", []string{"sweep", "--window", "1h", "oops"}},
		{"classify", []string{"classify", "--run", "r1", "--verdict", merge.ClassFlaky, "oops"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := newLab(t)
			l.init("main")
			// A hold is standing, so a `release` that ran would leave a mark and a
			// `sweep` that ran would refuse for a reason of its own.
			if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "hold", "the base is frozen", "--who", "rowan"); exit != 0 {
				t.Fatalf("hold: exit %d\n%s\n%s", exit, stdout, stderr)
			}
			// `classify` is taken off the line before the queue's flags are scanned,
			// so it is spelled the way a caller spells it: the subverb first.
			args := append([]string{"queue", "--lane", l.lane}, c.args...)
			if c.name == "classify" {
				args = append([]string{"queue", "classify", "--lane", l.lane}, c.args[1:]...)
			}
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("`queue %s` with an extra positional is exit 2, got %d\nstdout: %s\nstderr: %s", c.name, exit, stdout, stderr)
			}
			contains(t, stderr, "REFUSED")
			contains(t, stderr, "oops")
			// The hold is still standing: a refused invocation changes nothing.
			if _, err := os.Stat(filepath.Join(l.lane, merge.HoldName)); err != nil {
				t.Fatalf("`queue %s` mutated the lane on its way to refusing: the hold is gone (%v)", c.name, err)
			}
		})
	}

	// And the subverbs that DO take positionals still take them: the guard above is about
	// words nobody reads, not about narrowing the verbs that read theirs.
	t.Run("the verbs that use their positionals still do", func(t *testing.T) {
		t.Parallel()
		l := newLab(t)
		l.init("main")
		// `hold` grew a required --who while this branch was out: a hold is a person's,
		// and one nobody owns is one nobody can release. The rule under test here is the
		// positionals -- a multi-word reason is still ONE reason and not three dropped
		// words -- so the flag is given and the assertion is unchanged.
		if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "hold", "several", "words", "of", "reason", "--who", "rowan"); exit != 0 {
			t.Fatalf("a multi-word hold reason: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "release"); exit != 0 {
			t.Fatalf("a bare release: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "skip", "11", "12"); exit != 0 {
			t.Fatalf("skip of two: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		q := l.loadQueue()
		if !eqInts(q.Skipped, []int{11, 12}) {
			t.Fatalf("skip took %v, want [11 12]", q.Skipped)
		}
	})
}
