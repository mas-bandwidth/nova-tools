package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// `card <id>` shows each attempt's pushed head and the head the next attempt starts from, and
// the packet the rework is handed carries the same (sprint.BaseOf; docs/SPEC-CARD-CONTRACT.md
// layer 1), on the mem twin: attempt 1 pushes, attempt 2 fails with no commit, attempt 3
// starts from attempt 1's head.
func TestCardShowsTheHeadTheNextAttemptStartsFrom(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "handle the empty case, tier: pro")) // pro: two readers
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head " + sha + " --report 'pushed'")
	assert.Contains(t, ta.ok("card s1-1"), "NEXT starts from attempt 1 head="+sha)
	ta.ok("ask")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a --finding 'fine'")
	ta.ok("read --as reader-b --broken s1-1.r1.reader-b --finding 'line 3: the test is missing'")
	ta.ok("rework s1-1 --fix 'add the test'")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w2@1")
	ta.ok("finish --as m1 s1-1.w2@1 --failed --report 'out of budget'")
	ta.ok("rework s1-1")
	ta.deal(1)
	out := ta.ok("take --as m1 s1-1.w3@1")
	assert.Contains(t, out, "PACKET s1-1.w3 attempt=3")
	card := ta.ok("card s1-1")
	var attempts []string
	for _, l := range strings.Split(card, "\n") {
		if strings.HasPrefix(l, "ATTEMPT ") {
			attempts = append(attempts, l)
		}
	}
	if assert.Len(t, attempts, 3) {
		assert.Contains(t, attempts[0], " head="+sha+" ")
		assert.Contains(t, attempts[1], " head=- ", "a failed attempt pushed no head")
	}
	assert.Contains(t, card, "NEXT starts from attempt 1 head="+sha, "attempt 2 failed with no commit: attempt 3's base is attempt 1's head")
	assert.Contains(t, ta.ok("queue --as m1 --json"), `"base_head":"`+sha+`","base_attempt":1`)
}
