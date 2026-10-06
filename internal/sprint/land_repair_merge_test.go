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
	t.Run("a base fault leaves a new balanced span unchanged", func(t *testing.T) {
		t.Parallel()
		base := "# T\n\nOld `stray line.\nNew x here.\n"
		head := "# T\n\nOld `stray line.\nNew `x` here.\n"
		r := newMergeRig(t, map[string]string{"a.md": base}, map[string]string{"a.md": head})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Empty(t, note)
		assert.Equal(t, head, r.read("a.md"))
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"))
	})
	t.Run("an escaped backquote remains literal", func(t *testing.T) {
		t.Parallel()
		head := "Use \\` to quote.\n"
		r := newMergeRig(t, map[string]string{"a.md": "Use quotes.\n"}, map[string]string{"a.md": head})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Empty(t, note)
		assert.Equal(t, head, r.read("a.md"))
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"))
	})
	t.Run("indented code keeps its backquote", func(t *testing.T) {
		t.Parallel()
		head := "# T\n\n    echo `date\n"
		r := newMergeRig(t, map[string]string{"a.md": "# T\n"}, map[string]string{"a.md": head})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
		assert.Empty(t, note)
		assert.Equal(t, head, r.read("a.md"))
	})
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
