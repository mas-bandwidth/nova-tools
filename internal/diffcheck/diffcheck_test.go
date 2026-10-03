package diffcheck

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is one card's landed diff from the sprint branch sprint/mechanical-2026-10-02,
// as the review of 2026-10-02 labelled it (docsd-07, diaryd-08 and negd-42 wrong; the
// rest fine).
func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".diff"))
	require.NoError(t, err)
	return string(raw)
}

// The two fragments the review found in docsd-07 and diaryd-08 (E4) are found where the
// review put them: docsd-07's code span split so its backquote is left open and the
// sentence before it stranded (docs/SPEC-CAIRN.md:53-55), and diaryd-08's two comments
// whose lead-in lines lost their ends (cmd/nova-fuse/main.go:186 and :455). The third
// change of diaryd-08, an invented reason that reads as a sentence, is no fragment.
func TestFragmentsFindTheReviewsStrandedFragments(t *testing.T) {
	t.Parallel()
	got := func(name string) []string {
		var out []string
		for _, f := range Fragments(fixture(t, name)) {
			out = append(out, f.String())
		}
		return out
	}
	assert.Equal(t, []string{
		"docs/SPEC-CAIRN.md:54 leaves a code span unmatched: the change takes 1 backquotes and puts back 2",
		`docs/SPEC-CAIRN.md:54 leaves a sentence fragment: "this is written from" is followed by a new sentence, "An append into a"`,
	}, got("docsd-07"))
	assert.Equal(t, []string{
		`cmd/nova-fuse/main.go:187 leaves a sentence fragment: "with nothing at all" is followed by a new sentence, "A missing --box does"`,
		`cmd/nova-fuse/main.go:456 leaves a sentence fragment: "own order, and the" is followed by a new sentence, "The MORE line reports"`,
	}, got("diaryd-08"))
}

// Diffs the review called fine leave nothing: a rewritten Go comment (diaryd-75), a
// rewritten specification paragraph (docsd-08), test code (negd-42) and a pure rename
// (names-02). Over the whole reviewed set of 2026-10-02 (234 cards) the rule found only
// cards the review called wrong for a fragment.
func TestFragmentsPassCleanDiffs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"diaryd-75", "docsd-08", "negd-42", "names-02"} {
		assert.Empty(t, Fragments(fixture(t, name)), name)
	}
}

// One line of prose at a time: what opens a sentence, what ends mid-sentence, and the
// lines that are not prose at all.
func TestFragmentsReadOnlyProse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, diff string
		found      bool
	}{
		{"go comment stranded", "diff --git a/x.go b/x.go\n@@ -1,3 +1,2 @@\n // the box holds\n-// three cards.\n+// The box is full.\n", true},
		{"go comment goes on", "diff --git a/x.go b/x.go\n@@ -1,3 +1,2 @@\n // the box holds\n-// three cards.\n+// four cards.\n", false},
		{"before ends a sentence", "diff --git a/x.go b/x.go\n@@ -1,3 +1,2 @@\n // the box holds.\n-// three cards.\n+// The box is full.\n", false},
		{"old went on with a sentence", "diff --git a/x.go b/x.go\n@@ -1,3 +1,2 @@\n // the box holds\n-// The cards.\n+// The box is full.\n", false},
		{"all capitals go on", "diff --git a/x.go b/x.go\n@@ -1,3 +1,2 @@\n // the box holds\n-// a card.\n+// MORE cards.\n", false},
		{"code is no prose", "diff --git a/x.go b/x.go\n@@ -1,3 +1,2 @@\n x := the\n-y\n+Y := 1\n", false},
		{"a deletion strands the line before", "diff --git a/a.md b/a.md\n@@ -1,3 +1,2 @@\n the box holds\n-three cards.\n The next sentence.\n", true},
		{"backquote opened", "diff --git a/a.md b/a.md\n@@ -1,2 +1,2 @@\n Run it.\n-Use `x` here.\n+Use `x here.\n", true},
		{"a fence is no backquote", "diff --git a/a.md b/a.md\n@@ -1,2 +1,2 @@\n Run it.\n-Use x here.\n+Use x here: ```\n", false},
		{"another file type", "diff --git a/a.sh b/a.sh\n@@ -1,2 +1,2 @@\n # the box holds\n-# three\n+# The `box\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.found, len(Fragments(tc.diff)) > 0, "%v", Fragments(tc.diff))
		})
	}
}

// negd-42 edited a test file its PATHS does not name (E12): the land merge's diff names
// it, and only it, against the files the card names. A card's own PATHS hold every file
// of a fine diff, a glob names what it matches, and a card that names no PATHS is held to
// none.
func TestOutsideNamesFilesBeyondPaths(t *testing.T) {
	t.Parallel()
	negd42 := []string{"internal/bus/symlink_discipline_test.go", "internal/bus/timeout_test.go", "internal/bus/unaddressed_test.go"}
	assert.Equal(t, []string{"internal/bus/prepared_delivery_refuses_unrelated_content_test.go"}, Outside(negd42, fixture(t, "negd-42")))
	assert.Empty(t, Outside([]string{"docs/SPEC-CAIRN.md"}, fixture(t, "docsd-07")))
	assert.Empty(t, Outside([]string{"cmd/nova-fuse/*.go"}, fixture(t, "diaryd-08")), "a glob")
	assert.Equal(t, []string{"docs/SPEC-CAIRN.md"}, Outside([]string{"docs/SPEC-CHECK.md"}, fixture(t, "docsd-07")))
	assert.Nil(t, Outside(nil, fixture(t, "negd-42")))
}

// A rename holds both sides: a PATHS file renamed in its own directory is the card's (a
// name card, names-02), renamed anywhere else it is not, and a file from outside PATHS
// moved into the ledgers' directory is not either. Only the class ledgers are every
// card's to update, never the class tests' fixtures beside them.
func TestOutsideHoldsRenamesAndTheLedgersNarrowly(t *testing.T) {
	t.Parallel()
	edit := func(p string) string { return "diff --git a/" + p + " b/" + p + "\n@@ -1 +1 @@\n-a\n+b\n" }
	rename := func(from, to string) string {
		return "diff --git a/" + from + " b/" + to + "\nsimilarity index 100%\nrename from " + from + "\nrename to " + to + "\n"
	}
	docs := []string{"docs/a.md", "cmd/x.go"}
	for _, tc := range []struct {
		name, diff string
		outside    bool
	}{
		{"a rename in place", fixture(t, "names-02"), false},
		{"a PATHS file renamed elsewhere", rename("docs/a.md", "cmd/evil.go"), true},
		{"an outside file moved into the ledgers' directory", rename("cmd/y.go", "internal/ci/testdata/y_allowlist.txt"), true},
		{"a list ledger", edit("internal/ci/testdata/serial-tests_allowlist.txt"), false},
		{"the deleted-tests ledger", edit("internal/ci/testdata/deleted-tests.txt"), false},
		{"a counted ledger's shard", edit("internal/ci/testdata/generality/internal/bus.txt"), false},
		{"a class test's fixture", edit("internal/ci/testdata/net/allowed.go.txt"), true},
		{"a shard that is no .txt", edit("internal/ci/testdata/generality/x.go"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			paths := docs
			if tc.name == "a rename in place" {
				paths = []string{"cmd/nova-self-talk/issue1468_test.go"}
			}
			assert.Equal(t, tc.outside, len(Outside(paths, tc.diff)) > 0, "%v", Outside(paths, tc.diff))
		})
	}
}
