package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// deadBaseRig is a stream of two merging cards, a then b, a naming BASE friend-health and
// b naming BASE main, both of the rig's origin; friend-health was on origin when a's work
// was cut from it and is deleted since, as rowan/friend-health was on 2026-10-04.
func deadBaseRig(t *testing.T) *landRig {
	t.Helper()
	r := newLandRig(t)
	r.git(r.worker, "push", "-q", "origin", "main:refs/heads/friend-health")
	r.git(r.worker, "fetch", "-q", "origin")
	briefs := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}
	r.ok("add --stream s1 --brief-file " + brief("a", "friend-health") + " --brief-file " + brief("b", "main"))
	heads := map[string]string{"a": r.head("a", "friend-health", "a.txt", "a\n"), "b": r.head("b", "main", "b.txt", "b\n")}
	r.queued(heads, "a", "b")
	r.git(r.worker, "push", "-q", "origin", ":refs/heads/friend-health")
	return r
}

// deadBaseJudgments is the open judgments of a dead base in stream s1.
func (r *landRig) deadBaseJudgments() []sprint.Group {
	r.t.Helper()
	var out []sprint.Group
	for _, g := range r.inboxGroups() {
		if g.Kind == sprint.Judgment && g.Type == sprint.NDeadBase && g.Stream == "s1" {
			out = append(out, g)
		}
	}
	return out
}

// sn-friends-cost-tokensb named BASE rowan/friend-health, deleted from origin, and the
// lander tried it 203 times, every 6 s, its stream held behind it for half an hour. A
// dead base is one fact: the first pass refuses the card once with one judgment naming
// the card and the base and lands the card behind it; the passes after it do not try the
// card again; `card base` to a branch on origin re-points it, keeping its work and reads,
// and the next pass lands it (docs/SPEC-SPRINT.md section 7, a dead base).
func TestLanderRefusesADeadBaseOnceAndTheStreamMovesOn(t *testing.T) {
	t.Parallel()
	r := deadBaseRig(t)
	refusal := "card a names BASE friend-health, which is not on origin"

	var said strings.Builder
	for pass := 1; pass <= 3; pass++ {
		code, out, errs := r.do("land")
		said.WriteString(out + errs)
		if pass == 1 {
			assert.Equal(t, 1, code, "the pass that meets the dead base refuses it: %s%s", out, errs)
			assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=friend-health")
			assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main", "the card behind it lands in the same pass")
			continue
		}
		assert.Equal(t, 0, code, "pass %d: %s%s", pass, out, errs)
		assert.NotContains(t, out+errs, "friend-health", "pass %d tried the dead base again", pass)
	}
	assert.Equal(t, 1, strings.Count(said.String(), refusal), "one refusal over three passes:\n%s", said.String())
	assert.Equal(t, map[string]string{"a": "merging/queued", "b": "landed/merged"}, r.places("a", "b"))
	assert.Equal(t, "merging", r.streamState("s1"), "the stream is not stopped: its other cards land")
	js := r.deadBaseJudgments()
	require.Len(t, js, 1, "one judgment: %+v", js)
	assert.Equal(t, []string{"a"}, js[0].Primaries)
	assert.Len(t, js[0].Notes, 1)

	// re-pointed to a branch not on origin: refused, nothing changed
	code, _, errs := r.do("card base a gone-too")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "gone-too is not a branch on origin")
	assert.Len(t, r.deadBaseJudgments(), 1)

	var before cardView
	r.json("card a", &before)
	out := r.ok("card base a main")
	assert.Contains(t, out, "a BASE friend-health -> main")
	var after cardView
	r.json("card a", &after)
	assert.Equal(t, before.Primary.F("head"), after.Primary.F("head"), "the work is kept")
	assert.Equal(t, before.Primary.F("attempt"), after.Primary.F("attempt"), "the work is kept")
	assert.Equal(t, len(before.Reads), len(after.Reads), "the reads are kept")
	assert.Contains(t, after.Primary.F("brief"), "BASE: main\n")
	assert.NotContains(t, after.Primary.F("brief"), "friend-health")
	assert.Empty(t, after.Primary.F(sprint.FieldDeadBase))
	assert.Empty(t, r.deadBaseJudgments(), "the re-point answers the judgment")
	assert.Equal(t, 1, strings.Count(r.ok("log --card a"), "BASE friend-health -> main"), "one log line")

	out = r.ok("land")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.Equal(t, map[string]string{"a": "landed/merged", "b": "landed/merged"}, r.places("a", "b"))
	assert.Equal(t, []string{"land a (sprint stream s1)", "land b (sprint stream s1)", "base"}, r.mainLog())
}

// An answered judgment arms the card again: the next pass tries it once, and a base still
// not on origin is refused once more with a new judgment.
func TestLanderTriesADeadBaseAgainOnceItsJudgmentIsAnswered(t *testing.T) {
	t.Parallel()
	r := deadBaseRig(t)
	code, _, _ := r.do("land")
	require.Equal(t, 1, code)
	js := r.deadBaseJudgments()
	require.Len(t, js, 1)
	r.ok("ack " + js[0].Notes[0] + " --reason 'try it again'")
	require.Empty(t, r.deadBaseJudgments())
	code, _, errs := r.do("land")
	assert.Equal(t, 1, code, errs)
	assert.Equal(t, 1, strings.Count(errs, "card a names BASE friend-health, which is not on origin"))
	assert.Len(t, r.deadBaseJudgments(), 1)
	code, out, errs := r.do("land")
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.NotContains(t, out+errs, "friend-health")
}
