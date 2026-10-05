package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "handle the empty case, tier: pro")) // pro: two readers
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head " + sha + " --report 'pushed'")
	assert.Contains(t, ta.ok("card s1-1"), "NEXT starts from attempt 1 head="+sha)
	ta.ok("ask")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a --finding 'fine'")
	ta.ok("ask") // the second read, the first ok
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

// `card base <id> <branch>` re-points a merging card's BASE (docs/SPEC-SPRINT.md,
// lander-dead-base): it refuses a card that is not merging and a branch not on origin, and
// keeps the card's place, writing one log line.
func TestCardBaseReplacesBaseForMergingCardAndRefusesNotOnOrigin(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	briefs := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}
	r.ok("add --stream s1 --one --brief-file " + brief("s1-1", "old-branch"))

	// Unstarted card: not merging
	code, _, errs := r.do("card base s1-1 main")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "card s1-1 is not merging")

	heads := map[string]string{
		"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"),
	}
	r.queued(heads, "s1-1")

	// Merging card, but target branch is not on origin
	code, _, errs = r.do("card base s1-1 branch-does-not-exist")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "the branch branch-does-not-exist is not on origin")

	// A member may not re-point a card: card base is the coordinator's alone
	before := r.applies()
	code, _, errs = r.do("card base s1-1 main --actor m1")
	assert.Equal(t, 2, code, "card base by a member: %q", errs)
	assert.Contains(t, errs, "card base is the coordinator's alone")
	assert.Equal(t, before, r.applies(), "card base by a member wrote")
	assert.NotContains(t, r.ok("log --card s1-1"), "card s1-1 base ->")

	// Merging card, target branch main is on origin
	out := r.ok("card base s1-1 main")
	assert.Contains(t, out, "card s1-1 base -> main")

	// Log records the move
	log := r.ok("log --card s1-1")
	assert.Contains(t, log, "card s1-1 base -> main")

	// The work and reads are kept
	places := r.places("s1-1")
	assert.Equal(t, "merging/queued", places["s1-1"])
}
