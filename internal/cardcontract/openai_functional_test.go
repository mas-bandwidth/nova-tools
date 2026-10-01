//go:build functional

package cardcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIProfileExplainsItsWorkAndReadFrames(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"work", "read"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "openai", kind)
			job, err := os.ReadFile(filepath.Join(r.job, JobName))
			require.NoError(t, err)
			text := string(job)
			assert.Contains(t, text, "RESULT.md")
			assert.Contains(t, text, "attempt 1")
			assert.Contains(t, text, r.base)
			assert.Contains(t, text, "The staged checkout")

			shims := For("openai").Shims(r.frame, r.staged)
			require.Len(t, shims, 2)
			assert.Equal(t, "git", shims[0].Name)
			assert.Equal(t, "gh", shims[1].Name)
			assert.NotContains(t, shims[0].Script, "clone)")
			assert.Contains(t, shims[1].Script, "nova_openai_gh_guard")
			if kind == "read" {
				assert.Contains(t, text, "gh pr review --request-changes --body-file")
				assert.Contains(t, text, "file:line")
			} else {
				assert.Contains(t, text, "linked worktree")
				assert.Contains(t, text, "git -C")
				assert.Contains(t, text, "gh pr create --title")
				assert.Contains(t, text, "--body-file")
			}
		})
	}
}

func assertOpenAINoFinish(t *testing.T, job string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(job, FinishName))
	assert.True(t, os.IsNotExist(err), "no finish record is created: %v", err)
}

func TestOpenAIWorktreePushRecordsItsSourceWithoutChangingOrigin(t *testing.T) {
	t.Parallel()
	r := newRig(t, "openai", "work")
	worktree := filepath.Join(r.job, "worktree")
	code, _, errb := r.sh(r.repo, `git worktree add -b openai/change "`+worktree+`" HEAD`)
	require.Equal(t, 0, code, "%s", errb)
	head := r.commit(worktree, "linked worktree change")

	code, _, errb = r.sh(r.job, `git -C "`+worktree+`" push -u origin HEAD`)
	require.Equal(t, 0, code, "%s", errb)
	code, _, errb = r.sh(r.job, `git -C "`+worktree+`" push origin openai/change`)
	require.Equal(t, 0, code, "%s", errb)
	branch, pushed := LastPushed(r.job)
	assert.Equal(t, "openai/change", branch)
	assert.Equal(t, head, pushed)

	gotRecord, err := os.ReadFile(filepath.Join(r.job, PushedName))
	require.NoError(t, err)
	top, err := filepath.EvalSymlinks(worktree)
	require.NoError(t, err)
	assert.Equal(t, "openai/change\t"+head+"\t"+top+"\nopenai/change\t"+head+"\t"+top+"\n", string(gotRecord))
	assert.Equal(t, r.base, git(t, r.origin, "rev-parse", "main"), "the shim records the push without contacting origin")
	assert.Empty(t, git(t, r.origin, "for-each-ref", "refs/heads/openai"), "the linked-worktree branch stays local until the member pushes")
}

func TestOpenAICreateFinishesWithTheExactFileBody(t *testing.T) {
	t.Parallel()
	r := newRig(t, "openai", "work")
	worktree := filepath.Join(r.job, "worktree")
	code, _, errb := r.sh(r.repo, `git worktree add -b openai/change "`+worktree+`" HEAD`)
	require.Equal(t, 0, code, "%s", errb)
	head := r.commit(worktree, "linked worktree change")
	body := "## Summary\n\nAdded the change.\n\n## Gate\n\n`go test ./internal/cardcontract`"
	bodyPath := filepath.Join(r.job, "pr-body.md")
	require.NoError(t, os.WriteFile(bodyPath, []byte(body), 0o644))

	code, _, errb = r.sh(worktree, `gh pr create --title="Linked worktree change" --body-file="`+bodyPath+`"`)
	require.Equal(t, 0, code, "%s", errb)
	finish, ok := ReadFinish(r.job)
	require.True(t, ok, "the shim writes the finish record")
	assert.True(t, finish.Shaped)
	assert.Equal(t, head, finish.Head)
	assert.Equal(t, "openai/change", finish.Branch)
	assert.Equal(t, "ok", finish.Verdict)
	assert.Equal(t, "Linked worktree change", finish.Title)
	assert.Equal(t, "Linked worktree change", finish.Report)
	assert.Equal(t, body, finish.Body)
}

func TestOpenAIReadCanRequestChangesWithMultilineFindings(t *testing.T) {
	t.Parallel()
	r := newRig(t, "openai", "read")
	head := r.commit(r.repo, "under review")
	code, out, errb := r.sh(r.repo, "gh pr diff")
	require.Equal(t, 0, code, "%s", errb)
	assert.Contains(t, out, "+under review")
	code, out, errb = r.sh(r.repo, "gh pr diff --name-only")
	require.Equal(t, 0, code, "%s", errb)
	assert.Equal(t, "f", strings.TrimSpace(out))
	code, _, errb = r.sh(r.job, "gh pr view")
	require.Equal(t, 0, code, "%s", errb)
	code, _, errb = r.sh(r.job, "gh pr checks")
	require.Equal(t, 0, code, "%s", errb)

	body := "f:2: keep the staged change\n\nREADME.md:8: name the exact command"
	bodyPath := filepath.Join(r.job, "findings.md")
	require.NoError(t, os.WriteFile(bodyPath, []byte(body), 0o644))
	code, _, errb = r.sh(r.repo, `gh pr review --request-changes --body-file "`+bodyPath+`"`)
	require.Equal(t, 0, code, "%s", errb)
	finish, ok := ReadFinish(r.job)
	require.True(t, ok, "the review shim writes the finish record")
	assert.True(t, finish.Shaped)
	assert.Equal(t, head, finish.Head)
	assert.Equal(t, "sprint/c1", finish.Branch)
	assert.Equal(t, "broken", finish.Verdict)
	assert.Equal(t, "f:2: keep the staged change", finish.Report)
	assert.Equal(t, body, finish.Body)
	require.NoError(t, os.Remove(filepath.Join(r.job, FinishName)))
	approved := "Approved after reading the staged diff."
	approvedPath := filepath.Join(r.job, "approval.md")
	require.NoError(t, os.WriteFile(approvedPath, []byte(approved), 0o644))
	code, _, errb = r.sh(r.repo, `gh pr review -a -F "`+approvedPath+`"`)
	require.Equal(t, 0, code, "%s", errb)
	finish, ok = ReadFinish(r.job)
	require.True(t, ok, "the approval shim writes the finish record")
	assert.True(t, finish.Shaped)
	assert.Equal(t, "ok", finish.Verdict)
	assert.Equal(t, approved, finish.Report)
	assert.Equal(t, approved, finish.Body)
}

func TestOpenAIRefusesWrongKindGhCommands(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, line string
	}{
		{name: "create on read", kind: "read", line: `gh pr create --title "wrong kind" --body-file "BODY"`},
		{name: "review on work", kind: "work", line: `gh pr review --approve --body-file "BODY"`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "openai", tc.kind)
			bodyPath := filepath.Join(r.job, "body.md")
			require.NoError(t, os.WriteFile(bodyPath, []byte("body"), 0o644))
			line := strings.ReplaceAll(tc.line, "BODY", bodyPath)
			code, _, errb := r.sh(r.repo, line)
			assert.Equal(t, 2, code, "%s: %s", line, errb)
			assert.Contains(t, errb, "gh: REFUSED", line)
			assertOpenAINoFinish(t, r.job)
		})
	}
}

// A read-only global option is not a push: the guard passes it to the real Git, as the
// claude profile does (docs/SPEC-CARD-CONTRACT.md section 5).
func TestOpenAIGitVersionIsNotRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, "openai", "work")
	code, out, errb := r.sh(r.repo, "git --version")
	require.Equal(t, 0, code, errb)
	assert.Contains(t, out, "git version")
	assert.NotContains(t, errb, "REFUSED")
}

func TestOpenAIGitPagerOptionsAreNotRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, "openai", "work")
	r.commit(r.repo, "work")
	for _, line := range []string{"git -P log -1", "git --no-pager log -1", "git --paginate -P log -1"} {
		code, out, errb := r.sh(r.repo, line)
		require.Equal(t, 0, code, "%s: %s", line, errb)
		assert.Contains(t, out, "commit ", line)
		assert.NotContains(t, errb, "REFUSED", line)
	}
}

func TestOpenAIRefusesUnrepresentablePushes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
	}{
		{name: "force", line: "git push --force origin HEAD"},
		{name: "delete", line: "git push origin :main"},
		{name: "other remote", line: "git push mirror HEAD"},
		{name: "unknown option", line: "git push -q origin HEAD"},
		{name: "extra ref", line: "git push origin HEAD main"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "openai", "work")
			code, _, errb := r.sh(r.repo, tc.line)
			assert.Equal(t, 2, code, "%s: %s", tc.line, errb)
			assert.Contains(t, errb, "git: REFUSED push", tc.line)
			branch, head := LastPushed(r.job)
			assert.Empty(t, branch, "the refusal records no push")
			assert.Empty(t, head, "the refusal records no push")
			assertOpenAINoFinish(t, r.job)
		})
	}
}

func TestOpenAIRefusesUnsupportedTargetsAndOptionsBeforeFinishing(t *testing.T) {
	t.Parallel()
	createOptions := []struct {
		name   string
		option string
	}{
		{name: "base", option: "--base main"},
		{name: "head", option: "--head openai/change"},
		{name: "repository", option: "--repo other/repo"},
		{name: "draft", option: "--draft"},
		{name: "unknown", option: "--mystery"},
	}
	for _, tc := range createOptions {
		tc := tc
		t.Run("create/"+tc.name, func(t *testing.T) {
			t.Parallel()
			work := newRig(t, "openai", "work")
			bodyPath := filepath.Join(work.job, "body.md")
			require.NoError(t, os.WriteFile(bodyPath, []byte("body"), 0o644))
			line := `gh pr create --title "wrong target" --body-file "` + bodyPath + `" ` + tc.option
			code, _, errb := work.sh(work.repo, line)
			assert.Equal(t, 2, code, "%s: %s", line, errb)
			assert.Contains(t, errb, "gh: REFUSED", line)
			assertOpenAINoFinish(t, work.job)
		})
	}

	readOptions := []struct {
		name string
		line func(string) string
	}{
		{name: "target URL", line: func(file string) string {
			return `gh pr review --approve --body-file "` + file + `" https://example.invalid/owner/repo/pull/7`
		}},
		{name: "equals body file", line: func(file string) string { return `gh pr review --request-changes --body-file="` + file + `"` }},
		{name: "unknown option", line: func(file string) string { return `gh pr review --approve --body-file "` + file + `" --mystery` }},
		{name: "unsupported diff option", line: func(string) string { return "gh pr diff --patch" }},
	}
	for _, tc := range readOptions {
		tc := tc
		t.Run("read/"+tc.name, func(t *testing.T) {
			t.Parallel()
			read := newRig(t, "openai", "read")
			findingsPath := filepath.Join(read.job, "findings.md")
			require.NoError(t, os.WriteFile(findingsPath, []byte("f:1: finding"), 0o644))
			line := tc.line(findingsPath)
			code, _, errb := read.sh(read.repo, line)
			assert.Equal(t, 2, code, "%s: %s", line, errb)
			assert.Contains(t, errb, "gh: REFUSED", line)
			assertOpenAINoFinish(t, read.job)
		})
	}
}
