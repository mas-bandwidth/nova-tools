package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// brief <id> --widen (docs/SPEC-SPRINT.md section 2, "recut-widen-r.w1: a HOLD's
// PATHS-PROPOSED line widens the card in place"): a card whose attempt came back held for
// PATHS too narrow, its report carrying PATHS-PROPOSED, has its brief edited in place, the
// same id and no twin (the owner, 2026-10-06: "We gotta stop doing this twin shit. it's
// waste."), with the union of the old PATHS and the proposed ones (the paths before any
// prose on the line), and its next attempt starts from the held attempt's pushed head; a
// report with no line, a glob that climbs out with .., and a glob that names no file at the
// base or the head are refused, nothing written; recut --widen is retired and says what to
// run instead.
func TestBriefWidenKeepsTheId(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	// add reads the brief at its base (the brief checks): the file PATHS names is there
	r.commit("a.go", "package a\n", "a.go at the base")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	brief := writeBrief(t, "fix the empty case, tier: pro\nREPO: "+r.remote+"\nBASE: main\nPATHS: a.go\nTEST: none a fixture of brief --widen")
	// held runs one card of stream s to a held finish: its attempt pushed b.go, a file the
	// base has not, and the report says report
	held := func(s, report string) (id, head string) {
		id = s + "-1"
		r.promotionStream(s) // the card is cut on main, which the promotion stream alone takes
		r.ok("add --stream " + s + " --count 1 --one --brief-file " + brief)
		r.deal(1)
		r.ok("take --as m1 " + id + ".w1@1")
		head = r.head(id, "main", "b.go", "package b\n")
		r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/sprint/"+id)
		r.ok("finish --as m1 " + id + ".w1@1 --failed --head " + head + " --branch sprint/" + id + " --report '" + report + "'")
		return id, head
	}

	// the union, in place, and the next attempt starts from the held head
	{
		id, head := held("s1", "HOLD: the fix needs b.go; PATHS-PROPOSED: b.go, README,b.go")

		// recut --widen is retired: it names the verb to run, and writes nothing
		applies := r.applies()
		code, _, errs := r.do("recut " + id + " --widen --repo-dir " + r.clone)
		assert.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "--widen is retired: a PATHS widening edits the card in place, the same id and no twin; nothing was changed; run: nova-sprint brief "+id+" --widen")
		assert.Equal(t, applies, r.applies())

		out := r.ok("brief " + id + " --widen --repo-dir " + r.clone)
		assert.Contains(t, out, id+" brief edited in place by coordinator at attempt 1: - PATHS: a.go | + CARRY: "+id+" attempt 1 head="+head+" | + PATHS: a.go,b.go,README; review -> ready, attempt 2 next")
		assert.Contains(t, out, "NEXT "+id+" attempt 2 starts from attempt 1 head="+head)
		got := r.ok("card " + id + " --brief")
		assert.Contains(t, got, "\nPATHS: a.go,b.go,README\n", "the old PATHS first, then each proposed glob once")
		assert.Contains(t, got, "\nCARRY: "+id+" attempt 1 head="+head+"\n")
		assert.NotContains(t, got, "PATHS: a.go\n")
		assert.NotContains(t, r.ok("card "+id), "replaced by", "no twin")
		assert.Equal(t, sprint.Ready, r.primary(id).Col, "the same card, its next attempt")

		// the member stages the next attempt at the held head, as a rework's
		r.deal(1)
		var take struct {
			Packets []member.Packet `json:"packets"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.ok("take --as m1 "+id+".w2@1 --json")), &take))
		require.Len(t, take.Packets, 1)
		p := member.Carried(take.Packets[0])
		assert.Equal(t, head, p.BaseHead)
		assert.Equal(t, 1, p.BaseFrom)
		// and a friend dealt it is told to carry that head
		assert.Contains(t, friendBrief("f", sprint.Packet{Card: id + ".w2", Attempt: 2, Brief: p.Brief, Branch: "sprint/x"}), "its head, "+head)
	}

	// refused, nothing written
	{
		for _, c := range []struct {
			name, stream, report string
			want                 []string
		}{
			{"no line", "s2", "HOLD: PATHS too narrow", []string{"no PATHS-PROPOSED line", "attempt 1"}},
			{"climbs out, and names no file", "s3", "HOLD: PATHS-PROPOSED: ../x.go,c.go,b.go",
				[]string{"../x.go climbs out", "c.go names no file at main or at the head"}},
		} {
			id, _ := held(c.stream, c.report)
			before := r.applies()
			code, out, errs := r.do("brief " + id + " --widen --repo-dir " + r.clone)
			assert.Equal(t, 1, code, "%s: %s%s", c.name, out, errs)
			for _, w := range c.want {
				assert.Contains(t, errs, w, c.name)
			}
			assert.Equal(t, before, r.applies(), "%s: nothing was written", c.name)
		}
	}

	t.Run("the member carries the line into the finish report", func(t *testing.T) {
		long := strings.Repeat("x", 600)
		got := member.CarryProposed(long, member.Result{Report: "held\nPATHS-PROPOSED: b.go", Body: "## Body\n"})
		assert.LessOrEqual(t, len(got), 500)
		assert.True(t, strings.HasSuffix(got, "; PATHS-PROPOSED: b.go"), got)
		assert.Equal(t, "done", member.CarryProposed("done", member.Result{Report: "done"}))
		globs, ok := member.PathsProposed("pushed=x: HOLD; PATHS-PROPOSED: b.go, c/*.go")
		assert.True(t, ok)
		assert.Equal(t, []string{"b.go", "c/*.go"}, globs)
		// the paths alone, read before any prose on the line
		globs, ok = member.PathsProposed("HOLD\nPATHS-PROPOSED: `b.go`, c/d.go because the test needs both, and e.go\n")
		assert.True(t, ok)
		assert.Equal(t, []string{"b.go", "c/d.go"}, globs)
		globs, _ = member.PathsProposed("PATHS-PROPOSED: b.go (the fixture).")
		assert.Equal(t, []string{"b.go"}, globs)
		globs, _ = member.PathsProposed("PATHS-PROPOSED: b.go.")
		assert.Equal(t, []string{"b.go"}, globs)
	})

	t.Run("a friend's HOLD keeps the line and origin's head", func(t *testing.T) {
		const sha = "0123456789abcdef0123456789abcdef01234567"
		p := sprint.Packet{Card: "c.w1", Primary: "c", Attempt: 1, Branch: "sprint/c", Brief: "x\nREPO: mas-bandwidth/nova-tools\n"}
		tip := func(context.Context, string, string) (string, error) { return sha, nil }
		fr, err := friendFinish(context.Background(), "f", p, "Verdict: HOLD\nHead: "+sha+"\n\nPATHS too narrow.\n\nPATHS-PROPOSED: b.go\n", tip)
		require.NoError(t, err)
		assert.True(t, fr.Failed)
		assert.Equal(t, sha, fr.Head)
		assert.Contains(t, fr.Report, "; PATHS-PROPOSED: b.go")
	})
}

// A widen reads the path before the prose (docs/SPEC-CARD-CONTRACT.md section 4): each
// PATHS-PROPOSED item is its path up to the first whitespace, dash or semicolon, and what
// follows is the writer's reason, read and ignored; an item with no path before its prose is
// refused, the item printed. The path a reason follows is what every worker writes.
func TestAWidenReadsThePathBeforeTheProse(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.commit("a.go", "package a\n", "a.go at the base")
	require.NoError(t, os.MkdirAll(filepath.Join(r.worker, "cmd/nova-sprint"), 0o755))
	r.commit("cmd/nova-sprint/pushproof.go", "package main\n", "a hyphenated path at the base")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	brief := writeBrief(t, "fix the empty case, tier: pro\nREPO: "+r.remote+"\nBASE: main\nPATHS: a.go\nTEST: none a fixture of brief --widen")
	held := func(s, report string) (id, head string) {
		id = s + "-1"
		r.promotionStream(s)
		r.ok("add --stream " + s + " --count 1 --one --brief-file " + brief)
		r.deal(1)
		r.ok("take --as m1 " + id + ".w1@1")
		head = r.head(id, "main", "b.go", "package b\n")
		r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/sprint/"+id)
		r.ok("finish --as m1 " + id + ".w1@1 --failed --head " + head + " --branch sprint/" + id + " --report '" + report + "'")
		return id, head
	}

	// every item's path is read before its prose: the hyphen stays in the path, while the em
	// dash, the semicolon and the parenthetical are the writers' reasons, and the paths after
	// the first prose are read too
	{
		id, head := held("s1", "HOLD: more PATHS; PATHS-PROPOSED: cmd/nova-sprint/pushproof.go — pushJudgments calls a.seatInbox at line 498, b.go; the fix needs the new file, README (the map)")
		out := r.ok("brief " + id + " --widen --repo-dir " + r.clone)
		assert.Contains(t, out, "NEXT "+id+" attempt 2 starts from attempt 1 head="+head)
		got := r.ok("card " + id + " --brief")
		assert.Contains(t, got, "\nPATHS: a.go,cmd/nova-sprint/pushproof.go,b.go,README\n", "the old PATHS first, then each proposed path once")
		assert.Contains(t, got, "\nCARRY: "+id+" attempt 1 head="+head+"\n")
	}

	// an item with no path before its prose is refused, the item printed, nothing written
	{
		id, _ := held("s2", "HOLD: more PATHS; PATHS-PROPOSED: — because the path was forgotten, b.go")
		before := r.applies()
		code, out, errs := r.do("brief " + id + " --widen --repo-dir " + r.clone)
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, errs, "— because the path was forgotten: no path before its prose")
		assert.Equal(t, before, r.applies(), "nothing was written")
	}
}
