package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guard_test.go holds commit-guard to its rule on git log output from a fake runner: no git runs here.
// guard_functional_test.go runs it on real repositories and through a real pre-push hook.

const guardTestRules = `# the guarded person and the markers
person alice@example.com
person Alice@Example.ORG
marker Co-Authored-By: Claude
marker Generated with [Claude Code]
marker co-authored-by: gpt
`

// logRecord is one commit as git log -z prints it with guardFormat.
func logRecord(sha, parents, an, ae, cn, ce, body string) string {
	return strings.Join([]string{sha, parents, an, ae, cn, ce, body}, "\x1f") + "\x00"
}

const (
	shaMarked  = "1111111111111111111111111111111111111111"
	shaPlain   = "2222222222222222222222222222222222222222"
	shaSeat    = "3333333333333333333333333333333333333333"
	shaParent  = "9999999999999999999999999999999999999999"
	markedBody = "fleet: add a package\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>\n"
)

// guardGit answers every call with out, stderr and code, and keeps the arguments of each.
type guardGit struct {
	out, err string
	code     int
	calls    [][]string
}

func (f *guardGit) run(bin string, args, _ []string) (string, string, int) {
	f.calls = append(f.calls, append([]string{bin}, args...))
	return f.out, f.err, f.code
}

func guardRun(t *testing.T, git *guardGit, stdin string, args ...string) (int, string, string) {
	t.Helper()
	rules := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, os.WriteFile(rules, []byte(guardTestRules), 0o600))
	var out, errb bytes.Buffer
	code := commitGuard(append([]string{"--rules", rules}, args...), strings.NewReader(stdin), git.run, &out, &errb)
	return code, out.String(), errb.String()
}

// The negative control: a commit under the guarded address with an AI marker is refused, naming the commit, its
// author, the marker line and the rebase that rewrites it; the person's own plain commit and an AI-marked commit
// under the seat's own address pass.
func TestCommitGuardRefusesOnlyTheMarkedGuardedCommit(t *testing.T) {
	t.Parallel()
	git := &guardGit{out: logRecord(shaMarked, shaPlain, "Alice", "alice@example.com", "Alice", "alice@example.com", markedBody) +
		logRecord(shaPlain, shaSeat, "Alice", "alice@example.com", "Alice", "alice@example.com", "docs: a typo\n") +
		logRecord(shaSeat, shaParent, "bench-1", "bench-1@example.com", "bench-1", "bench-1@example.com", markedBody)}
	code, out, errs := guardRun(t, git, "", "--repo", "/r", "origin/main..HEAD")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-fleet commit-guard: commit "+shaMarked+" (author Alice <alice@example.com>, committer Alice <alice@example.com>) "+
		`carries the AI marker "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" under a guarded address; `+
		"amend it as the seat's own identity (git config user.email <the seat's address>), run: git rebase --exec 'git commit --amend --no-edit --reset-author' "+shaMarked+"~1\n", errs)
	assert.Equal(t, [][]string{{"git", "--no-replace-objects", "-C", "/r", "log", "-z", guardFormat, "origin/main..HEAD"}}, git.calls)
}

func TestCommitGuardPassesPlainAndOtherIdentities(t *testing.T) {
	t.Parallel()
	git := &guardGit{out: logRecord(shaPlain, shaSeat, "Alice", "alice@example.com", "Alice", "alice@example.com", "docs: a typo\n\nCo-Authored-By: Bob <bob@example.com>\n") +
		logRecord(shaSeat, shaParent, "bench-1", "bench-1@example.com", "GitHub", "noreply@github.com", markedBody)}
	code, out, errs := guardRun(t, git, "", "HEAD~2..HEAD")
	assert.Equal(t, 0, code, errs)
	assert.Equal(t, "COMMIT-GUARD OK commits=2\n", out)
	assert.Empty(t, errs)
	assert.Equal(t, [][]string{{"git", "--no-replace-objects", "log", "-z", guardFormat, "HEAD~2..HEAD"}}, git.calls, "no --repo: git finds the repository as a hook's would")
}

// The address may be the committer's alone, either address and the marker match without case, a marker may sit
// anywhere on its line, and a root commit's rebase is --root.
func TestCommitGuardCommitterCaseAndRoot(t *testing.T) {
	t.Parallel()
	git := &guardGit{out: logRecord(shaMarked, "", "bench-1", "bench-1@example.com", "Alice", "ALICE@example.org", "init\n\n🤖 generated with [claude code](https://claude.com/claude-code)\n")}
	code, _, errs := guardRun(t, git, "", "HEAD")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "(author bench-1 <bench-1@example.com>, committer Alice <ALICE@example.org>)")
	assert.Contains(t, errs, `carries the AI marker "🤖 generated with [claude code](https://claude.com/claude-code)"`)
	assert.True(t, strings.HasSuffix(errs, "--reset-author' --root\n"), errs)
}

func TestCommitGuardRulesRefused(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"person a@example.com\n": "want at least one person and one marker line, have 1 and 0",
		"marker x\n":             "want at least one person and one marker line, have 0 and 1",
		"person a@example.com\nmarker x\nauthor y\n": `line 3: "author" is neither person nor marker`,
		"person\nmarker x\n":                         "line 1: person has no value",
	} {
		_, err := parseRules(text)
		require.Error(t, err, text)
		assert.Equal(t, want, err.Error(), text)
	}
	r, err := parseRules(guardTestRules)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"alice@example.com": true, "alice@example.org": true}, r.people)
	assert.Equal(t, []string{"co-authored-by: claude", "generated with [claude code]", "co-authored-by: gpt"}, r.markers)
}

// A push sends each local sha, less what the remote had (its old sha) and what any remote-tracking branch reaches. A
// deletion sends nothing.
func TestPrePushRevs(t *testing.T) {
	t.Parallel()
	revs, err := prePushRevs("refs/heads/a " + shaMarked + " refs/heads/a " + shaParent + "\nrefs/heads/b " + shaPlain + " refs/heads/b " + zeroSHA + "\n(delete) " + zeroSHA + " refs/heads/c " + shaSeat + "\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"--ignore-missing", shaMarked, shaPlain, "^" + shaParent, "--not", "--remotes"}, revs)
	revs, err = prePushRevs("(delete) " + zeroSHA + " refs/heads/c " + shaSeat + "\n")
	require.NoError(t, err)
	assert.Nil(t, revs)
	_, err = prePushRevs("refs/heads/a " + shaMarked + "\n")
	assert.EqualError(t, err, `pre-push line "refs/heads/a `+shaMarked+`" has 2 fields, want 4`)
}

// --pre-push reads git's lines from stdin and logs what they send; a push of deletions alone runs no git.
func TestCommitGuardPrePush(t *testing.T) {
	t.Parallel()
	git := &guardGit{out: logRecord(shaMarked, shaParent, "Alice", "alice@example.com", "Alice", "alice@example.com", markedBody)}
	code, _, errs := guardRun(t, git, "refs/heads/a "+shaMarked+" refs/heads/a "+zeroSHA+"\n", "--pre-push", "origin", "git@example.com:o/r.git")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "commit "+shaMarked+" (author Alice <alice@example.com>")
	assert.Equal(t, [][]string{{"git", "--no-replace-objects", "log", "-z", guardFormat, "--ignore-missing", shaMarked, "--not", "--remotes"}}, git.calls)

	git = &guardGit{}
	code, out, errs := guardRun(t, git, "(delete) "+zeroSHA+" refs/heads/c "+shaSeat+"\n", "--pre-push", "origin")
	assert.Equal(t, 0, code, errs)
	assert.Equal(t, "COMMIT-GUARD OK commits=0\n", out)
	assert.Empty(t, git.calls)
}

func TestCommitGuardRefusesItsInvocation(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "nova-fleet commit-guard: want a revision such as origin/main..HEAD, or --pre-push <remote>; run: nova-fleet commit-guard -h\n"},
		{[]string{"--all"}, "nova-fleet commit-guard: unknown flag --all; run: nova-fleet commit-guard -h\n"},
		{[]string{"--pre-push"}, "nova-fleet commit-guard: --pre-push wants a value; run: nova-fleet commit-guard -h\n"},
		{[]string{"--pre-push", "origin", "url", "HEAD"}, "nova-fleet commit-guard: --pre-push takes the hook's <remote> [<url>], not revisions (HEAD); run: nova-fleet commit-guard -h\n"},
	} {
		git := &guardGit{}
		code, out, errs := guardRun(t, git, "", c.args...)
		assert.Equal(t, 2, code, c.args)
		assert.Empty(t, out)
		assert.Equal(t, c.want, errs, c.args)
		assert.Empty(t, git.calls)
	}
	var out, errb bytes.Buffer
	missing := filepath.Join(t.TempDir(), "none")
	assert.Equal(t, 2, commitGuard([]string{"--rules", missing, "HEAD"}, strings.NewReader(""), (&guardGit{}).run, &out, &errb))
	assert.Contains(t, errb.String(), "nova-fleet commit-guard: the rules cannot be read (open "+missing+": ")
	assert.True(t, strings.HasSuffix(errb.String(), "; the push-guard play writes them, run: nova-fleet help push-guard\n"), errb.String())
}

// A git that fails is exit 2 with its own words and the log line to run by hand.
func TestCommitGuardGitFails(t *testing.T) {
	t.Parallel()
	git := &guardGit{err: "fatal: ambiguous argument 'nope': unknown revision\n", code: 128}
	code, _, errs := guardRun(t, git, "", "--repo", "/a b", "nope")
	assert.Equal(t, 2, code)
	assert.Equal(t, "nova-fleet commit-guard: git log exit 128: fatal: ambiguous argument 'nope': unknown revision; run: git --no-replace-objects -C '/a b' log --oneline nope\n", errs)
}
