package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The sequence of a card that was right (docs/SPEC-SPRINT.md section 6, a rework that
// finds nothing to do): one reader passes its head, the other finds it broken, the
// coordinator reworks it, and the next attempt's worker finds nothing to do and commits
// nothing. That is no failed work: the head the earlier attempt pushed has an ok read, so
// the card goes back to review at that head, where the machine asks two readers again;
// the coordinator is not asked to judge a failure.
func TestNothingToDoAtAHeadAReaderPassedIsBackInReview(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct{ name, report string }{
		{"no commit", "no commit: the child committed nothing: head " + head + " is the staged commit or behind it; nothing left to change"},
		{"nothing to do", "nothing to do: the renames are already right; nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
			ta.ok("add --stream s1 --count 1 --brief-file " + proBriefFile(t))
			ta.deal(1)
			ta.ok("take --as m1 s1-1.w1@1")
			ta.ok("finish --as m1 s1-1.w1@1 --head " + head + " --branch sprint/s1-1.w1")
			ta.ok("ask")
			ta.ok("read --as reader-a --ok s1-1.r1.reader-a --finding 'the renames are right'")
			ta.ok("read --as reader-b --broken s1-1.r1.reader-b --finding 'internal/ci/testdata/deleted-tests.txt:12 names the old file'")
			ta.ok("rework s1-1")
			ta.deal(1)
			ta.ok("take --as m1 s1-1.w2@1")
			ta.ok("finish --as m1 s1-1.w2@1 --failed --report '" + tc.report + "'")
			pr := ta.primary("s1-1")
			assert.Equal(t, sprint.Review, pr.Col)
			assert.Equal(t, head, pr.F("head"), "back in review at the head a reader passed")
			assert.NotEqual(t, "failed", pr.F("result"), "nothing to do at a passed head is no failed work")
			for _, g := range ta.inboxGroups() {
				assert.NotEqual(t, sprint.NWorkFailed, g.Type, "the coordinator is not asked to judge a failure: %+v", g)
			}
			ta.ok("ask")
			var asked []string
			for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
				asked = append(asked, ta.askedOf(rd)...)
			}
			assert.Len(t, asked, 2, "two readers asked at the new attempt: %v", asked)
			for _, id := range asked {
				assert.Contains(t, id, "s1-1.r2.", "read at attempt 2")
			}
		})
	}
}

// With no reader's pass at the head, a rework that finds nothing to do is failed work,
// as before: the coordinator judges it.
func TestNothingToDoWithNoPassedHeadIsFailedWork(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --brief-file " + proBriefFile(t))
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head " + head + " --branch sprint/s1-1.w1")
	ta.ok("ask")
	ta.ok("read --as reader-a --broken s1-1.r1.reader-a --finding 'main.go:3 is wrong'")
	ta.ok("read --as reader-b --broken s1-1.r1.reader-b --finding 'main.go:4 is wrong'")
	ta.ok("rework s1-1")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w2@1")
	ta.ok("finish --as m1 s1-1.w2@1 --failed --report 'nothing to do: nothing'")
	assert.Equal(t, "failed", ta.primary("s1-1").F("result"))
	assert.Equal(t, sprint.NWorkFailed, ta.group(sprint.NWorkFailed, "s1").Type)
}
