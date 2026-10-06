package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lander's document repairs (docs/SPEC-SPRINT.md section 7, the document repairs),
// through land: a head whose only fault is one stray backquote lands with the file
// repaired on its merge commit and the repair named in the batch's NOTE, the merge
// commit's body and the card's record; a head whose stray backquote could be either of
// two is refused naming the line; a file under the stream's prose globs is not read for
// backquotes at all.
func TestLandRepairsAStrayBackquoteOnItsMergeAndSaysSo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, text, prose string
		landed, why, note       string
	}{
		{name: "a stray backquote", file: "doc.md", text: "# Bus\n\nThe `push` verb takes `--proof first.\n", landed: "# Bus\n\nThe `push` verb takes --proof first.",
			note: "the documents were repaired at the merge: doc.md:3 a stray backquote dropped at column 23"},
		{name: "an ambiguous span", file: "doc.md", text: "# Bus\n\nSee `a` b `c` ` here.\n",
			why: "fails the lander's checks: doc.md:3 leaves a code span unmatched and the repair is ambiguous: 2 backquotes could be the stray one: See `a` b `c` ` here. (E4)"},
		{name: "a prose path", file: "security/audit.md", text: "# Audit\n\nThe call `f(x) returns ``` and `` here `.\n", prose: "security/**,ratings/**",
			landed: "# Audit\n\nThe call `f(x) returns ``` and `` here `."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			r.commit("doc.md", "# Bus\n\nThe `push` verb.\n", "the doc")
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --one --brief-file " + writeNeedsBrief(t, t.TempDir(), "c1", "Fix c1.\nPATHS: doc.md, security/**", ""))
			require.NoError(t, os.MkdirAll(filepath.Join(r.worker, "security"), 0o755))
			heads := map[string]string{"c1": r.head("c1", "main", tc.file, tc.text)}
			r.queued(heads, "c1")
			if tc.prose != "" {
				r.ok("stream set s1 --prose " + tc.prose)
			}
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=c1 fact=conflict reason=the head "+heads["c1"]+" of c1 "+tc.why)
				assert.Equal(t, []string{"the doc", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"c1": "merging/stuck"}, r.places("c1"))
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
			assert.Equal(t, []string{"land c1 (sprint stream s1)", "the doc", "base"}, r.mainLog())
			got := r.git(r.remote, "show", "main:"+tc.file)
			assert.Equal(t, tc.landed, got)
			body := r.git(r.remote, "log", "-1", "--format=%b", "main")
			if tc.note == "" {
				assert.NotContains(t, out, "repaired", "a prose path is not read")
				assert.NotContains(t, body, "repaired")
				r.clean()
				return
			}
			assert.Zero(t, strings.Count(got, "`")%2, "the landed file has an even count")
			assert.Contains(t, out, "NOTE c1: "+tc.note+"\n")
			assert.Contains(t, body, strings.ToUpper(tc.note[:1])+tc.note[1:]+".")
			assert.Equal(t, heads["c1"], r.git(r.remote, "rev-parse", "main^2"), "the merge keeps the head as its second parent")
			assert.Equal(t, map[string]string{"c1": "landed/merged"}, r.places("c1"))
			assert.Contains(t, r.ok("card c1"), tc.note)
			r.clean()
		})
	}
}
