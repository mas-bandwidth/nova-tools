//go:build functional

package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChangeBaseListAtCommitAndAddedListRowsAgreeInAGitHistory is the git half of
// TestAddedUnexecutedRowFails (rowan hold 3 at d3f2ddf5, item 1): in a
// real history, ChangeBase finds the change's base both ways, ListAtCommit reads
// the list as the base had it, and the row the change appended is the one
// AddedListRows reports. It runs git a dozen times: exec of a whole program is
// the functional tier's (Glenn 2026-09-26, nova-tools#4328).
func TestChangeBaseListAtCommitAndAddedListRowsAgreeInAGitHistory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "dev")
	writeGo(t, root, "docs/CLI.md", "```\n$ nova-foo run\n```\n")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "pr")
	writeGo(t, root, "docs/CLI.md", "```\n$ nova-foo run\n$ nova-foo newverb --x 1\n```\n")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n$ nova-foo newverb --x 1\n")
	git("commit", "-q", "-am", "adds an unexecuted example")

	event := filepath.Join(t.TempDir(), "event.json")
	require.NoError(t, os.WriteFile(event, []byte(`{"pull_request":{"base":{"sha":"`+base+`"}}}`), 0o600))
	for name, env := range map[string]map[string]string{
		"pull_request event": {"GITHUB_EVENT_PATH": event},
		"local merge base":   {"GITHUB_BASE_REF": "dev"},
		"the default base":   {},
	} {
		got, err := ChangeBase(root, func(k string) string { return env[k] })
		if err != nil || got != base {
			assert.Fail(t, fmt.Sprintf("%s: ChangeBase = %q, %v; want %s", name, got, err, base))
			continue
		}
		baseList, present, err := ListAtCommit(root, got, UnexecutedListPath)
		require.NoError(t, err, "%s: ListAtCommit = present %v, %v; want the base's list", name, present, err)
		require.True(t, present, "%s: ListAtCommit = present %v, %v; want the base's list", name, present, err)
		head := loadAllowlist(t, filepath.Join(root, UnexecutedListPath), unexecutedListOptions)
		added := AddedListRows(baseList, head.Text())
		assert.Equal(t, []string{"$ nova-foo newverb --x 1"}, added, "%s: added rows = %q; want the appended row, which fails the class test", name, added)
	}
	_, present, err := ListAtCommit(root, base, "internal/ci/testdata/absent.txt")
	assert.NoError(t, err, "ListAtCommit of a file the base lacks = present %v, %v; want introduced (false, nil)", present, err)
	assert.False(t, present, "ListAtCommit of a file the base lacks = present %v, %v; want introduced (false, nil)", present, err)
}

// TestChangeBaseUsesTheFirstParentWhenAReleaseMergeCheckoutHasNoDevRef proves
// a release merge checkout compares against the actual target parent when the
// default dev ref is unavailable; the added row remains visible to the shrink
// check. See docs/SPEC-CI.md, unit-rule burndown.
func TestChangeBaseUsesTheFirstParentWhenAReleaseCheckoutHasNoDevRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "release")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n")
	git("add", "-A")
	git("commit", "-q", "-m", "release baseline")
	git("checkout", "-q", "-b", "topic")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n$ nova-foo newverb --x 1\n")
	git("commit", "-q", "-am", "adds an unexecuted example")
	git("checkout", "-q", "release")
	writeGo(t, root, "release-note.txt", "release commit\n")
	git("add", "-A")
	git("commit", "-q", "-m", "release target moves")
	parents := strings.Fields(git("rev-list", "--parents", "-n", "1", "HEAD"))
	require.Len(t, parents, 2)
	parent := parents[1]
	git("merge", "--no-ff", "-q", "topic", "-m", "release merge")

	base, err := ChangeBase(root, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, parent, base, "the base is the actual first parent, even without a dev ref")
	baseList, present, err := ListAtCommit(root, base, UnexecutedListPath)
	require.NoError(t, err)
	require.True(t, present)
	head := loadAllowlist(t, filepath.Join(root, UnexecutedListPath), unexecutedListOptions)
	assert.Equal(t, []string{"$ nova-foo newverb --x 1"}, AddedListRows(baseList, head.Text()), "the shrink-only invariant still sees the increase")

	// A present but unrelated dev ref is different from an absent ref: it is an
	// intended base whose history is unavailable, so the first parent must not
	// hide that failure.
	unrelated := git("commit-tree", git("rev-parse", "HEAD^{tree}"), "-m", "unrelated dev")
	git("update-ref", "refs/heads/dev", unrelated)
	_, err = ChangeBase(root, func(string) string { return "" })
	require.ErrorContains(t, err, "no base commit")
}

// TestChangeBaseRefusesToGuessForANonMergeCheckoutWithoutDevRefs proves an
// ordinary local checkout cannot substitute its previous commit for an
// unavailable intended base. See docs/SPEC-CI.md, unit-rule burndown.
func TestChangeBaseRefusesToGuessForANonMergeCheckoutWithoutDevRefs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "release")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n")
	git("add", "-A")
	git("commit", "-q", "-m", "release baseline")
	writeGo(t, root, "release-note.txt", "release commit\n")
	git("add", "-A")
	git("commit", "-q", "-m", "ordinary release commit")

	_, err := ChangeBase(root, func(string) string { return "" })
	require.ErrorContains(t, err, "HEAD is not a merge commit with an actual first parent")
}
