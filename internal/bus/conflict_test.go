package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The union, both ways: it keeps every line of both sides in a stable order, and it does
// not double a line both sides already have. An INDEX and a RECEIPTS are sets of lines
// whose order is history, so "ours then whatever of theirs is new" is the whole of it.
func TestUnionLinesKeepsBothSidesAndDoublesNothing(t *testing.T) {
	t.Parallel()
	ours := "a\nb\n"
	theirs := "a\nc\n"
	got, want := UnionLines(ours, theirs), "a\nb\nc\n"
	require.Equal(t, want, got, "UnionLines = %q, want %q", got, want)
	// Identical sides are one side.
	got, want = UnionLines(ours, ours), "a\nb\n"
	require.Equal(t, want, got, "UnionLines over one content twice = %q, want %q", got, want)
	// A side that is missing entirely contributes nothing and loses nothing.
	got, want = UnionLines("", theirs), "a\nc\n"
	require.Equal(t, want, got, "UnionLines with an empty side = %q, want %q", got, want)
	got = UnionLines("", "")
	require.Empty(t, got, "UnionLines over nothing = %q, want empty", got)
	// Blank lines and CRLF are folded, because every reader of these files already skips
	// the first and this tool never writes the second.
	got, want = UnionLines("a\n\nb\n", "a\r\nc\r\n"), "a\nb\nc\n"
	require.Equal(t, want, got, "UnionLines = %q, want %q", got, want)
}

// The attribute file, both ways: it is written when the rules are not there, and writing it
// again changes nothing. It is APPENDED to a file the bus already had rather than
// replacing it, because that file belongs to the bus and this tool owns two lines of it.
func TestEnsureMergeAttributes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	wrote, err := EnsureMergeAttributes(root)
	require.NoError(t, err)
	require.True(t, wrote, "the first call wrote nothing; a bus with no .gitattributes has no union rule")
	first, err := os.ReadFile(filepath.Join(root, AttributesName))
	require.NoError(t, err)
	for _, want := range []string{"from-*/INDEX merge=union", "from-*/RECEIPTS merge=union"} {
		require.Contains(t, string(first), want, "%s does not carry %q:\n%s", AttributesName, want, first)
	}
	// The second call is a no-op: no write, and not a second copy of the rules.
	wrote, err = EnsureMergeAttributes(root)
	require.NoError(t, err)
	require.False(t, wrote, "the rules were written twice; every send after the first would touch a shared file for nothing")
	again, err := os.ReadFile(filepath.Join(root, AttributesName))
	require.NoError(t, err)
	require.False(t, string(again) != string(first), "the second call changed the file:\n%s\n---\n%s", first, again)

	// A bus that already has a .gitattributes of its own keeps it, and gains the rules.
	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, AttributesName), []byte("*.md text\n"), 0o644))
	{
		wrote, err := EnsureMergeAttributes(other)
		require.False(t, err != nil || !wrote, "EnsureMergeAttributes over an existing file = %v %v", wrote, err)
	}
	got, err := os.ReadFile(filepath.Join(other, AttributesName))
	require.NoError(t, err)
	for _, want := range []string{"*.md text", "from-*/INDEX merge=union", "from-*/RECEIPTS merge=union"} {
		require.Contains(t, string(got), want, "the bus's own rule or the tool's is missing:\n%s", got)
	}
}

// THE ABORT THAT FAILS, which used to return as though it had worked. `git rebase --abort`
// can fail -- here because the rebase state directory cannot be removed -- and the run then
// returned with the checkout STILL in a rebase, so every later verb refused for a reason
// that was true and unhelpful and nothing said the real one. The state is checked after the
// attempt, and the refusal carries the recovery that works on the state it found.
func TestAnAbortThatFailsIsRefusedWithTheRecovery(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, where := conflictedRebase(t)

	// Make the abort fail: git cannot remove the entries of a directory it cannot write.
	require.NoError(t, os.Chmod(where, 0o500))
	t.Cleanup(func() { os.Chmod(where, 0o700) })

	err := abortRebase(dir)
	require.Error(t, err, "an abort that failed reported success; the checkout is still in a rebase and every later verb will refuse for the wrong reason")
	for _, want := range []string{"could not be aborted", "STILL in a rebase", where, "git rebase --abort", "git reset --hard ORIG_HEAD"} {
		require.Contains(t, err.Error(), want, "the refusal does not say %q: %v", want, err)
	}
	if strings.Contains(err.Error(), "\n") {
		require.NotContains(t, err.Error(), "\n", "the refusal is more than one line: %q", err.Error())
	}

	// The other way, and on a fixture of its own: an abort with nothing in its way works
	// and says nothing.
	//
	// A FRESH ONE rather than this checkout with the obstacle taken back off, because
	// taking it off does not undo the abort that failed under it, and what that abort left
	// behind is not the same on every platform. A directory a process cannot write is one
	// whose entries git could not delete on unix -- the rebase state is untouched and the
	// second abort has something to abort -- while on Windows a read-only directory is one
	// git empties and then cannot REMOVE, so the failed abort has already thrown the state
	// away and left the bare directory, which no chmod puts back and no `git rebase
	// --abort` will touch. Making the second half depend on the wreckage of the first tests
	// the wreckage; a conflicted rebase of its own tests the abort.
	clean, _ := conflictedRebase(t)
	{
		err := abortRebase(clean)
		require.NoError(t, err, "an abort that worked was reported as a failure: %v", err)
	}
	{
		_, still := inRebase(clean)
		require.False(t, still, "the checkout is still in a rebase after a successful abort")
	}
	if _, err := CurrentBranch(clean); err != nil {
		require.NoError(t, err, "the checkout is not on a branch after the abort: %v", err)
	}
}

// conflictedRebase builds a checkout that is genuinely mid-rebase on a conflict, and
// returns it together with the rebase state directory git left behind.
func conflictedRebase(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	{
		out, err := git(dir, "init", "--quiet", "-b", "main")
		require.NoError(t, err, "init: %v %s", err, out)
	}
	commit := func(content, message string) {
		t.Helper()
		write(t, dir, "x.txt", content)
		{
			out, err := git(dir, "add", "-A")
			require.NoError(t, err, "add: %v %s", err, out)
		}
		{
			out, err := git(dir, append(identityArgs(testIdentity["Ada"]), "commit", "-q", "-m", message)...)
			require.NoError(t, err, "commit: %v %s", err, out)
		}
	}
	commit("base\n", "base")
	{
		out, err := git(dir, "checkout", "-q", "-b", "other")
		require.NoError(t, err, "checkout: %v %s", err, out)
	}
	commit("theirs\n", "theirs")
	{
		out, err := git(dir, "checkout", "-q", "main")
		require.NoError(t, err, "checkout: %v %s", err, out)
	}
	commit("mine\n", "mine")
	// A rebase that conflicts, so the checkout is genuinely mid-rebase.
	{
		_, err := git(dir, append(identityArgs(testIdentity["Ada"]), "rebase", "other")...)
		require.Error(t, err, "the fixture did not conflict")
	}
	where, still := inRebase(dir)
	require.True(t, still, "the fixture is not in a rebase")
	return dir, where
}

// The recovery a refusal offers is a sentence a person types, so the sentence is read back
// out of the code rather than trusted: `git push` alone cannot land a branch that is ahead
// of a remote which has moved, and that is exactly the state these refusals are about.
func TestNoRefusalRecommendsABarePush(t *testing.T) {
	t.Parallel()
	require.False(t, !strings.Contains(pullRebaseAdvice, "git pull --rebase && git push"), "the shared advice is %q, which is not the two commands that land a branch that is behind", pullRebaseAdvice)
	for _, bad := range []string{"push or drop them first", "run git push"} {
		require.NotContains(t, pullRebaseAdvice, bad, "the advice still says %q, which does not work against a remote that has moved", bad)
	}
}
