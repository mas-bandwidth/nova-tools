package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// recut <id> --widen (docs/SPEC-SPRINT.md section 2, "recut-widen-r.w1: a HOLD's
// PATHS-PROPOSED line widens the twin"): a card whose attempt came back held for PATHS too
// narrow, its report carrying PATHS-PROPOSED, is re-cut as its twin with the union of the
// old PATHS and the proposed ones, and the twin's first attempt starts from the held
// attempt's pushed head; a report with no line, a glob that climbs out with .., and a glob
// that names no file at the base or the head are refused, nothing written.
func TestRecutWidenAppliesPathsProposed(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	brief := writeBrief(t, "fix the empty case, tier: pro\nREPO: "+r.remote+"\nBASE: main\nPATHS: a.go")
	// held runs one card of stream s to a held finish: its attempt pushed b.go, a file the
	// base has not, and the report says report
	held := func(s, report string) (id, head string) {
		id = s + "-1"
		r.ok("add --stream " + s + " --count 1 --one --brief-file " + brief)
		r.deal(1)
		r.ok("take --as m1 " + id + ".w1@1")
		head = r.head(id, "main", "b.go", "package b\n")
		r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/sprint/"+id)
		r.ok("finish --as m1 " + id + ".w1@1 --failed --head " + head + " --branch sprint/" + id + " --report '" + report + "'")
		return id, head
	}

	// the union, and the next attempt starts from the held head
	{
		id, head := held("s1", "HOLD: the fix needs b.go; PATHS-PROPOSED: b.go, README,b.go")
		out := r.ok("recut " + id + " --widen --repo-dir " + r.clone)
		assert.Contains(t, out, id+"b")
		assert.Contains(t, out, "NEXT "+id+"b starts from "+id+" attempt 1 head="+head)
		got := r.ok("card " + id + "b --brief")
		assert.Contains(t, got, "\nPATHS: a.go,b.go,README\n", "the old PATHS first, then each proposed glob once")
		assert.Contains(t, got, "\nCARRY: "+id+" attempt 1 head="+head+"\n")
		assert.NotContains(t, got, "PATHS: a.go\n")
		assert.Contains(t, r.ok("card "+id), "replaced by "+id+"b")

		// the member stages the twin's first attempt at the held head, as a rework's
		r.deal(1)
		var take struct {
			Packets []member.Packet `json:"packets"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.ok("take --as m1 "+id+"b.w1@1 --json")), &take))
		require.Len(t, take.Packets, 1)
		p := member.Carried(take.Packets[0])
		assert.Equal(t, head, p.BaseHead)
		assert.Equal(t, 1, p.BaseFrom)
		// and a friend dealt it is told to carry that head
		assert.Contains(t, friendBrief("f", sprint.Packet{Card: id + "b.w1", Attempt: 1, Brief: p.Brief, Branch: "sprint/x"}), "its head, "+head)
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
			code, out, errs := r.do("recut " + id + " --widen --repo-dir " + r.clone)
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
