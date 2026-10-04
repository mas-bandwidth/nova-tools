package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// shrinkLedger is a shrink-only ledger by its path (shrinkonly.ShrinkOnly), one no
// generated-ledger family owns: land resolves a conflict in it as the union of both
// sides' removals, with no update run. countedLedger is a shrink-only ledger whose rows
// carry counts, lowered in place.
const (
	shrinkLedger  = "internal/ci/testdata/sharedtemp_allowlist.txt"
	countedLedger = "internal/ci/testdata/dead_code_allowlist.txt"
)

// Two cards that each remove a row of a shrink-only ledger, the rows adjacent, conflict
// line by line; land resolves the ledger as the base less both removals, commits the
// merge naming the ledger, lands both without a stop, and logs one line per ledger. Two
// cards that lower the counts of a counted ledger's rows, one key on both sides, land
// with each count lowered by both. A card whose side adds a row to the ledger is refused
// as a conflict as before, the merge aborted and the card stuck, the reason naming the
// line.
func TestLandResolvesAConflictInAShrinkOnlyLedgerAsTheUnionOfRemovals(t *testing.T) {
	t.Parallel()
	const base = "# the shared temp ledger\n# ceiling: 3\ninternal/a\tTestA\ninternal/b\tTestB\ninternal/c\tTestC\n"
	for _, tc := range []struct {
		name, ledger, base string
		first, second      map[string]string
		want               string // the ledger on the base after both land
		why                string // "" lands both
		nLeft, nRight      int
	}{
		{"adjacent removals land", shrinkLedger, base,
			map[string]string{"debt/a": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{"debt/b": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/a\tTestA\ninternal/c\tTestC\n"},
			"# the shared temp ledger\n# ceiling: 2\ninternal/c\tTestC", "", 1, 1},
		{"lowered counts land", countedLedger, "# the dead code ledger\n# ceiling: 2\ncmd/a 5\ncmd/b 3\n",
			map[string]string{"debt/a": "", countedLedger: "# the dead code ledger\n# ceiling: 2\ncmd/a 4\ncmd/b 3\n"},
			map[string]string{"debt/b": "", countedLedger: "# the dead code ledger\n# ceiling: 2\ncmd/a 3\ncmd/b 1\n"},
			"# the dead code ledger\n# ceiling: 2\ncmd/a 2\ncmd/b 1", "", 1, 2},
		{"an added row is refused", shrinkLedger, base,
			map[string]string{"debt/a": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{"debt/b": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 3\ninternal/a\tTestA\ninternal/c\tTestC\ninternal/d\tTestD\n"},
			"", `its shrink-only ledger ` + shrinkLedger + ` conflicts and is not a union of removals: the right side adds a line, which is no removal: "internal/d\tTestD"`, 0, 0},
		{"a numbered row in an uncounted ledger is not renumbered", shrinkLedger, "# the shared temp ledger\n# ceiling: 2\ninternal/a 5\tTestA\ninternal/b 3\tTestB\n",
			map[string]string{"debt/a": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/a 4\tTestA\ninternal/b 3\tTestB\n"},
			map[string]string{"debt/b": "", shrinkLedger: "# the shared temp ledger\n# ceiling: 2\ninternal/a 3\tTestA\ninternal/b 3\tTestB\n"},
			"", `its shrink-only ledger ` + shrinkLedger + ` conflicts and is not a union of removals: the left side adds a line, which is no removal: "internal/a 4\tTestA"`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			r.files("the debt", map[string]string{"debt/a": "a\n", "debt/b": "b\n", tc.ledger: tc.base, "notes.tsv": "one\n"})
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
			line := unionLine(tc.ledger, tc.nLeft, tc.nRight)
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.Contains(t, out, "NOTE "+line)
			assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
			assert.Equal(t, tc.want, r.git(r.remote, "show", "main:"+tc.ledger), "the ledger is the base less both sides' removals")
			assert.Equal(t, "one", r.git(r.remote, "show", "main:notes.tsv"))
			assert.Equal(t, "", r.git(r.remote, "ls-tree", "main", "debt/a"), "both cards' work landed")
			assert.Equal(t, "", r.git(r.remote, "ls-tree", "main", "debt/b"))
			body := r.git(r.remote, "log", "-1", "--format=%b", "main")
			assert.Contains(t, body, "The shrink-only ledgers conflicted and were resolved as the union of both sides' removals: "+line+".")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			assert.Contains(t, r.ok("card s1-2"), "the shrink-only ledgers "+tc.ledger+" conflicted and were resolved at the merge as the union of both sides' removals")
			assert.NotContains(t, r.ok("card s1-1"), "resolved", "a card that merged plainly says nothing more")
			r.clean()
		})
	}
}
