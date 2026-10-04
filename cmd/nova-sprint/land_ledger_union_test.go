package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// shrinkLedger is a shrink-only ledger by its path (allowlist.ShrinkOnly), one no
// generated-ledger family owns: land resolves a conflict in it as the union of both
// sides' removals, with no update run.
const shrinkLedger = "internal/ci/testdata/sharedtemp_allowlist.txt"

// Two cards that each remove a row of a shrink-only ledger, the rows adjacent, conflict
// line by line; land resolves the ledger as the base less both removals, commits the
// merge naming the ledger, lands both without a stop, and logs one line per ledger. A
// card whose side adds a row to the ledger is refused as a conflict as before, the
// merge aborted and the card stuck, the reason naming the line.
func TestLandResolvesAConflictInAShrinkOnlyLedgerAsTheUnionOfRemovals(t *testing.T) {
	t.Parallel()
	const base = "# the shared temp ledger\n# ceiling: 3\ninternal/a\tTestA\ninternal/b\tTestB\ninternal/c\tTestC\n"
	for _, tc := range []struct {
		name          string
		first, second map[string]string
		why           string // "" lands both
	}{
		{"adjacent removals land", map[string]string{"debt/a": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{"debt/b": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/a\tTestA\ninternal/c\tTestC\n"}, ""},
		{"an added row is refused", map[string]string{"debt/a": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{"debt/b": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 3\ninternal/a\tTestA\ninternal/c\tTestC\ninternal/d\tTestD\n"},
			`its shrink-only ledger ` + shrinkLedger + ` conflicts and is not a union of removals: the right side adds a line, which is no removal: "internal/d\tTestD"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			r.files("the debt", map[string]string{"debt/a": "a\n", "debt/b": "b\n", shrinkLedger: base, "notes.tsv": "one\n"})
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", tc.first), "s1-2": r.card("s1-2", tc.second)}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 does not merge")
				assert.Contains(t, errs, tc.why)
				assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the refused merge is aborted")
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.Contains(t, out, "NOTE ledger "+shrinkLedger+": resolved as the union of removals (-1 left, -1 right)")
			assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
			assert.Equal(t, "# the shared temp ledger\n# ceiling: 2\ninternal/c\tTestC", r.git(r.remote, "show", "main:"+shrinkLedger), "the ledger is the base less both removals")
			assert.Equal(t, "one", r.git(r.remote, "show", "main:notes.tsv"))
			assert.Equal(t, "", r.git(r.remote, "ls-tree", "main", "debt/a"), "both cards' work landed")
			assert.Equal(t, "", r.git(r.remote, "ls-tree", "main", "debt/b"))
			body := r.git(r.remote, "log", "-1", "--format=%b", "main")
			assert.Contains(t, body, "The shrink-only ledgers conflicted and were resolved as the union of both sides' removals: ledger "+shrinkLedger+": resolved as the union of removals (-1 left, -1 right).")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			assert.Contains(t, r.ok("card s1-2"), "the shrink-only ledgers "+shrinkLedger+" conflicted and were resolved at the merge as the union of both sides' removals")
			assert.NotContains(t, r.ok("card s1-1"), "resolved", "a card that merged plainly says nothing more")
			r.clean()
		})
	}
}
