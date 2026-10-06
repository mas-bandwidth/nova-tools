package sprint_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// mergeRig is a repository with a base and a card's head merged onto it --no-ff, as the
// lander merges one (cmd/nova-sprint/land.go, mergeHead): before is the base's tip.
type mergeRig struct {
	t      *testing.T
	dir    string
	before string
}

func (r *mergeRig) git(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=lander", "GIT_AUTHOR_EMAIL=lander@example.invalid", "GIT_COMMITTER_NAME=lander", "GIT_COMMITTER_EMAIL=lander@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *mergeRig) must(args ...string) string {
	r.t.Helper()
	out, err := r.git(args...)
	require.NoError(r.t, err, "git %v", args)
	return out
}

func (r *mergeRig) write(file, text string) {
	r.t.Helper()
	p := filepath.Join(r.dir, file)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(r.t, os.WriteFile(p, []byte(text), 0o600))
	r.must("add", file)
}

// newMergeRig commits base on main, the head's files on a card branch, and merges the
// card onto main as the lander does.
func newMergeRig(t *testing.T, base, head map[string]string) *mergeRig {
	t.Helper()
	r := &mergeRig{t: t, dir: t.TempDir()}
	r.must("init", "-q", "-b", "main")
	for f, s := range base {
		r.write(f, s)
	}
	r.must("commit", "-q", "-m", "base")
	r.before = r.must("rev-parse", "HEAD")
	r.must("switch", "-q", "-c", "card")
	for f, s := range head {
		r.write(f, s)
	}
	r.must("commit", "-q", "-m", "work of c1")
	r.must("switch", "-q", "main")
	r.must("merge", "--no-ff", "--no-edit", "-m", "land c1 (sprint stream s1)", "card")
	return r
}

func (r *mergeRig) read(file string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, file))
	require.NoError(r.t, err)
	return string(b)
}

// The lander's repair on the merge commit (docs/SPEC-SPRINT.md section 7, the lander's
// checks): a head whose only fault is one stray backquote lands with the file repaired
// on its merge commit, the count even and the note naming the line; a head whose stray
// backquote could be either of two is refused naming the line, the merge untouched; a
// file under a prose glob is not read for backquotes at all.
func TestTheLanderRepairsAStrayBackquoteAndSaysSo(t *testing.T) {
	t.Parallel()
	t.Run("a stray backquote is dropped on the merge commit and named", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"docs/SPEC-BUS.md": "# Bus\n\nThe `push` verb.\n"},
			map[string]string{"docs/SPEC-BUS.md": "# Bus\n\nThe `push` verb takes `--proof first.\n"})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Equal(t, "the documents were repaired at the merge: docs/SPEC-BUS.md:3 a stray backquote dropped at column 23", note)
		got := r.must("show", "HEAD:docs/SPEC-BUS.md")
		assert.Equal(t, "# Bus\n\nThe `push` verb takes --proof first.", got)
		assert.Zero(t, strings.Count(got, "`")%2, "the landed file has an even count")
		assert.Empty(t, r.must("status", "--porcelain"), "the repair is committed, nothing is left in the tree")
		assert.NotEqual(t, merged, r.must("rev-parse", "HEAD"), "the merge commit is rewritten")
		assert.Equal(t, r.before+" "+r.must("rev-parse", "card"), r.must("log", "-1", "--format=%P"), "it is still the merge, both parents")
		assert.Equal(t, "land c1 (sprint stream s1)", r.must("log", "-1", "--format=%s"))
		assert.Contains(t, r.must("log", "-1", "--format=%b"), "The documents were repaired at the merge: docs/SPEC-BUS.md:3 a stray backquote dropped at column 23.")
	})
	t.Run("an ambiguous span is refused naming the line", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"a.md": "# T\n"}, map[string]string{"a.md": "# T\n\nSee `a` b `c` ` here.\n"})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		require.Len(t, refused, 1)
		assert.Equal(t, "a.md:3 leaves a code span unmatched and the repair is ambiguous: 2 backquotes could be the stray one: See `a` b `c` ` here.", refused[0].String())
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"), "a refusal writes nothing")
		assert.Equal(t, "# T\n\nSee `a` b `c` ` here.\n", r.read("a.md"))
	})
	t.Run("a prose path is not checked", func(t *testing.T) {
		t.Parallel()
		audit := "# Audit\n\nThe call `f(x) returns ``` and `` here `.\n"
		r := newMergeRig(t, map[string]string{"README.md": "r\n"}, map[string]string{"security/audit.md": audit})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, []string{"security/**", "ratings/**"})
		require.NoError(t, err)
		assert.Empty(t, note)
		assert.Empty(t, refused)
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"))
		assert.Equal(t, audit, r.read("security/audit.md"))

		// the same file off the prose globs is read, and refused or repaired
		note, refused, err = sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.True(t, note != "" || len(refused) > 0, "off the prose globs its odd count is a fault")
	})
	t.Run("the formatter's faults are repaired with the backquote", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"a.md": "one\n"}, map[string]string{"a.md": "one\ntwo `x \r\nthree"})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Equal(t, "the documents were repaired at the merge: a.md:2 a CRLF line ending made LF; a.md:2 trailing whitespace trimmed; a.md:2 a stray backquote dropped at column 5; a.md:3 a final newline added", note)
		assert.Equal(t, "one\ntwo x\nthree\n", r.read("a.md"))
		assert.Empty(t, r.must("status", "--porcelain"))
	})
}

// A cold read of 2026-10-06: a change that only deletes can leave a code span
// unmatched (the line that closed it taken out, or its closing backquote), and the
// repair read only the lines a change adds, so it passed. E4 judges the resulting
// paragraph: a deletion that takes an odd count of backquotes from a paragraph left odd
// is repaired or refused as an addition is; one that balances a span passes.
func TestE4CatchesADeletionThatUnbalancesASpan(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n" +
		"@@ -1,4 +1,3 @@\n # T\n \n The `stream\n-set` verb is one.\n"
	changed := sprint.DocChanged(diff)
	require.Equal(t, map[string]sprint.DocLines{"a.md": {Deleted: []sprint.DocCut{{At: 4, Lines: []string{"set` verb is one."}}}}}, changed,
		"a deletion-only diff carries its deletion")
	fixed, fixes, refused := sprint.RepairDoc("a.md", "# T\n\nThe `stream\n", changed["a.md"], false)
	assert.Empty(t, refused)
	assert.Equal(t, "# T\n\nThe stream\n", fixed)
	assert.Len(t, fixes, 1)

	// the base's own odd count, a deletion taking an even count from it, is left
	base := "Old `fault.\nnew `x` line\n"
	fixed, fixes, refused = sprint.RepairDoc("a.md", base, sprint.DocLines{Deleted: []sprint.DocCut{{At: 2, Lines: []string{"gone `y`"}}}}, false)
	assert.Equal(t, base, fixed)
	assert.Empty(t, fixes)
	assert.Empty(t, refused)

	t.Run("a deleted line that closed a span is caught", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"a.md": "# T\n\nThe `stream\nset` verb is one.\n"},
			map[string]string{"a.md": "# T\n\nThe `stream\n"})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Equal(t, "the documents were repaired at the merge: a.md:3 a stray backquote dropped at column 5", note)
		assert.Equal(t, "# T\n\nThe stream\n", r.read("a.md"))
	})
	t.Run("a closing backquote deleted from a line is caught", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"a.md": "a `b` c `d\ne` f\n"}, map[string]string{"a.md": "a `b` c `d\ne f\n"})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Equal(t, "the documents were repaired at the merge: a.md:1 a stray backquote dropped at column 9", note)
		assert.Equal(t, "a `b` c d\ne f\n", r.read("a.md"))
	})
	t.Run("a deletion with no backquote beside it to drop is refused naming the line", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"a.md": "See `x\nmid\ny` here\nend\n"}, map[string]string{"a.md": "See `x\nmid\nend\n"})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		require.Len(t, refused, 1)
		assert.Equal(t, "a.md:2 leaves a code span unmatched: the lines it deletes take an odd count of backquotes and no line beside them holds one to drop: mid", refused[0].String())
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"), "a refusal writes nothing")
	})
	t.Run("a deletion that balances a span passes", func(t *testing.T) {
		t.Parallel()
		base := "# T\n\nOne `a` here.\nA stray ` tick.\nTwo `b` there.\n\nThe `c`\nand `d` go.\n"
		r := newMergeRig(t, map[string]string{"a.md": base},
			map[string]string{"a.md": "# T\n\nOne `a` here.\nTwo `b` there.\n\nThe `c`\n"})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		assert.Empty(t, refused)
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"), "nothing to repair, nothing written")
	})
}

// The night of 2026-10-05: four heads (1535 and 1639 backquotes) were refused because
// workers wrapped inline code spans across a line break in Markdown they wrote, and the
// coordinator joined the spans by hand. A span wrapped on lines the change adds (a line
// that leaves a span open, the next added line closing it) is joined on the merge commit
// and noted; a fence's lines and a base line's span are left as they are; a join that
// still leaves the span open is refused naming the line.
func TestTheLanderJoinsAWrappedCodeSpanInAddedLines(t *testing.T) {
	t.Parallel()
	t.Run("three wrapped spans are joined and noted", func(t *testing.T) {
		t.Parallel()
		head := "# Study\n\nThe adapter runs `tmux\nsend-keys` on the pane.\n\n" +
			"A packet is `RakNet\nconnected` once `the\nhandshake` is done.\n"
		r := newMergeRig(t, map[string]string{"docs/STUDY.md": "# Study\n"}, map[string]string{"docs/STUDY.md": head})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Equal(t, "E4 repaired: 3 spans joined in docs/STUDY.md", note)
		assert.Equal(t, "# Study\n\nThe adapter runs `tmux send-keys` on the pane.\n\n"+
			"A packet is `RakNet connected` once `the handshake` is done.\n", r.read("docs/STUDY.md"))
		assert.Empty(t, r.must("status", "--porcelain"), "the join is committed")
		assert.Contains(t, r.must("log", "-1", "--format=%b"), "E4 repaired: 3 spans joined in docs/STUDY.md.")
		assert.Equal(t, "land c1 (sprint stream s1)", r.must("log", "-1", "--format=%s"))
	})
	t.Run("a fence is untouched", func(t *testing.T) {
		t.Parallel()
		head := "# T\n\n```sh\necho `date\nuname` done\n```\n\nThe `x\ny` joined.\n"
		r := newMergeRig(t, map[string]string{"a.md": "# T\n"}, map[string]string{"a.md": head})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Equal(t, "E4 repaired: 1 span joined in a.md", note)
		assert.Equal(t, "# T\n\n```sh\necho `date\nuname` done\n```\n\nThe `x y` joined.\n", r.read("a.md"))
	})
	t.Run("a base line's span is never rewritten", func(t *testing.T) {
		t.Parallel()
		base := "# T\n\nThe `old\nspan` stays.\n"
		r := newMergeRig(t, map[string]string{"a.md": base}, map[string]string{"a.md": base + "\nNew `a` line.\n"})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		assert.Empty(t, refused)
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"))

		// an added line closing a base line's span is not joined to it either
		r = newMergeRig(t, map[string]string{"a.md": "The `old\n"}, map[string]string{"a.md": "The `old\nspan` closed.\n"})
		merged = r.must("rev-parse", "HEAD")
		note, refused, err = sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		assert.Empty(t, refused)
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"))
	})
	t.Run("a truly unmatched backquote is refused naming the line", func(t *testing.T) {
		t.Parallel()
		head := "# T\n\nThis `is open\nand `this` too.\n"
		r := newMergeRig(t, map[string]string{"a.md": "# T\n"}, map[string]string{"a.md": head})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		require.NotEmpty(t, refused)
		assert.Equal(t, "a.md:3 leaves a code span unmatched: joining the span wrapped at this line leaves an odd count of backquotes: This `is open and `this` too.", refused[0].String())
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"), "a refusal writes nothing")
		assert.Equal(t, head, r.read("a.md"))
	})
}
