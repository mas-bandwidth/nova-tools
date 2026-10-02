//go:build functional

package cardcontract

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// rig is one staged job: an origin, a checkout of it on the card's branch at
// <job>/repo, and a profile's JOB.md and shims installed, the shims first on PATH.
type rig struct {
	t                        *testing.T
	origin, job, repo, shims string
	base                     string
	frame                    Frame
	staged                   Staged
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func newRig(t *testing.T, family, kind string) *rig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-SWARM.md)")
	}
	root := t.TempDir()
	r := &rig{t: t, origin: filepath.Join(root, "origin.git"), job: filepath.Join(root, "slot", "jobs", "c1"), shims: filepath.Join(root, "slot", "shim")}
	r.repo = filepath.Join(r.job, "repo")
	git(t, root, "init", "-q", "--bare", r.origin)
	git(t, root, "clone", "-q", r.origin, r.repo)
	require.NoError(t, os.WriteFile(filepath.Join(r.repo, "f"), []byte("base\n"), 0o644))
	git(t, r.repo, "add", "f")
	git(t, r.repo, "commit", "-q", "-m", "base")
	git(t, r.repo, "push", "-q", "origin", "main")
	git(t, r.repo, "switch", "-q", "-c", "sprint/c1")
	r.base = git(t, r.repo, "rev-parse", "HEAD")
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	r.frame = Frame{Kind: kind, Card: "c1", Attempt: 1, Model: "test/model", Repo: cardURL, BaseRef: "main", Branch: "sprint/c1", ReviewBase: "main"}
	r.staged = Staged{Job: r.job, Repo: r.repo, Head: r.base, Git: realGit}
	require.NoError(t, Install(For(family), r.frame, r.staged, r.shims))
	return r
}

// sh runs a command line in dir with the shims first on PATH; its exit, stdout and stderr.
func (r *rig) sh(dir, line string, stdin ...string) (int, string, string) {
	r.t.Helper()
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+r.shims+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(stdin[0])
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(r.t, err)
	}
	return code, out.String(), errb.String()
}

func (r *rig) commit(dir, text string) string {
	r.t.Helper()
	code, _, errb := r.sh(dir, "echo '"+text+"' > f && git commit -q -am '"+text+"' && git rev-parse HEAD")
	require.Equal(r.t, 0, code, errb)
	return git(r.t, dir, "rev-parse", "HEAD")
}

// Every push form is recorded in the job, answered as a push, and leaves origin as it was.
func TestAGitPushIsRecordedAndNothingLeaves(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"claude", "plain"} {
		r := newRig(t, family, "work")
		head := r.commit(r.repo, "work")
		for _, tc := range []struct{ line, branch, head string }{
			{"git push", "sprint/c1", head},
			{"git push -u origin HEAD", "sprint/c1", head},
			{"git push origin sprint/c1", "sprint/c1", head},
			{"git push origin HEAD:refs/heads/feature", "feature", head},
			{"git push --force-with-lease origin main", "main", r.base},
			{"git -C " + r.repo + " push origin", "sprint/c1", head},
		} {
			code, _, errb := r.sh(r.repo, tc.line)
			require.Equal(t, 0, code, "%s %s: %s", family, tc.line, errb)
			assert.Contains(t, errb, "To "+cardURL, tc.line)
			assert.Contains(t, errb, tc.branch+" -> sprint/c1 (pushed by the sprint", tc.line)
			branch, got := LastPushed(r.job)
			assert.Equal(t, tc.branch, branch, "%s %s", family, tc.line)
			assert.Equal(t, tc.head, got, "%s %s", family, tc.line)
		}
		assert.Equal(t, r.base, git(t, r.origin, "rev-parse", "main"), "origin is untouched")
		assert.Empty(t, git(t, r.origin, "for-each-ref", "refs/heads/sprint"), "no branch reached origin")
		code, _, errb := r.sh(r.repo, "git push origin :main")
		assert.Equal(t, 1, code)
		assert.Contains(t, errb, "REFUSED")
	}
}

// A clone of the card's repository, in any spelling, is the staged checkout; the child's
// commit in it is the checkout's, and a link inside the checkout stays out of its status.
func TestACloneOfTheCardsRepositoryIsTheStagedCheckout(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	for i, url := range []string{
		cardURL,
		"https://example.com/example-owner/example-repo",
		"https://example.com/example-owner/example-repo/",
		"ssh://git@example.com/example-owner/example-repo.git",
		"git@example.com:example-owner/example-repo.git",
		"https://user@example.com/Example-Owner/Example-Repo.git",
		"ssh://git@example.com:22/Example-Owner/example-repo.git",
		"https://example.com:443/example-owner/example-repo",
	} {
		dir := filepath.Join(r.job, "c"+string(rune('a'+i)))
		code, _, errb := r.sh(r.job, "git clone --depth 1 -b main "+url+" "+dir)
		require.Equal(t, 0, code, "%s: %s", url, errb)
		assert.Contains(t, errb, "staged checkout")
		got, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		want, err := filepath.EvalSymlinks(r.repo)
		require.NoError(t, err)
		assert.Equal(t, want, got, url)
	}
	code, _, errb := r.sh(r.job, "git clone "+cardURL)
	require.Equal(t, 0, code, errb)
	head := r.commit(filepath.Join(r.job, "example-repo"), "in the clone")
	assert.Equal(t, head, git(t, r.repo, "rev-parse", "sprint/c1"), "the clone's commit is the checkout's")
	code, _, errb = r.sh(r.repo, "git clone "+cardURL+" inner && git status --porcelain")
	require.Equal(t, 0, code, errb)
	code, out, _ := r.sh(r.repo, "git status --porcelain")
	require.Equal(t, 0, code)
	assert.Empty(t, strings.TrimSpace(out), "a link inside the checkout is excluded from its status")
	code, _, errb = r.sh(r.job, "git clone "+cardURL+" ca")
	assert.Equal(t, 128, code, "an existing destination is refused as git refuses it")
	assert.Contains(t, errb, "already exists")
}

// A clone of any other repository is refused with one line naming the rule.
func TestACloneOfAnotherRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	for _, url := range []string{"https://example.com/other/repo.git", r.origin, "git@example.com:example-owner/other.git"} {
		code, _, errb := r.sh(r.job, "git clone "+url+" x")
		assert.Equal(t, 128, code, url)
		assert.Contains(t, errb, "REFUSED clone of "+url)
		assert.Equal(t, 1, strings.Count(strings.TrimSpace(errb), "\n")+1, "one line")
		_, err := os.Lstat(filepath.Join(r.job, "x"))
		assert.True(t, os.IsNotExist(err), "nothing is made")
	}
}

// Branching, status, fetch and the rest go to the real git; in plain, so does a clone.
func TestEveryOtherGitCommandPassesThrough(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	for _, line := range []string{"git checkout -q -b topic", "git switch -q -c topic2", "git branch topic3", "git fetch -q origin", "git pull -q origin main", "git status --short", "git --no-pager log --oneline -1", "git -c core.pager=cat log -1"} {
		code, _, errb := r.sh(r.repo, line)
		assert.Equal(t, 0, code, "%s: %s", line, errb)
	}
	assert.Equal(t, "topic2", git(t, r.repo, "symbolic-ref", "--short", "HEAD"))
	p := newRig(t, "plain", "work")
	code, _, errb := p.sh(p.job, "git clone -q "+p.origin+" real")
	require.Equal(t, 0, code, errb)
	fi, err := os.Lstat(filepath.Join(p.job, "real"))
	require.NoError(t, err)
	assert.True(t, fi.IsDir(), "plain hands a clone to the real git")
}

// gh pr create, in each form, is the work card's finish: RESULT.md in the shape, verdict
// ok, the title and body kept, and nothing opened.
func TestGhPrCreateWritesTheFinish(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	head := r.commit(r.repo, "done")
	body := filepath.Join(r.job, "body.md")
	require.NoError(t, os.WriteFile(body, []byte("## Summary\n\nthe change\n"), 0o644))
	for _, tc := range []struct{ line, stdin, title, body string }{
		{`gh pr create --title "Add f" --body "the change"`, "", "Add f", "the change"},
		{`gh pr create -t "Add f" -b "x" --base main --head sprint/c1 --draft`, "", "Add f", "x"},
		{`gh pr create --title="Add f" --body-file ` + body, "", "Add f", "## Summary\n\nthe change"},
		{`gh pr create --title "Add f" -F -`, "from stdin\n", "Add f", "from stdin"},
		{`gh pr create --fill`, "", "done", ""},
		{`gh pr create --title "nothing: the check already holds"`, "", "nothing: the check already holds", ""},
	} {
		_ = os.Remove(filepath.Join(r.job, FinishName)) // ignored: the first form finds none to remove
		code, out, errb := r.sh(r.repo, tc.line, tc.stdin)
		require.Equal(t, 0, code, "%s: %s", tc.line, errb)
		assert.Contains(t, out, "the sprint pushes it to sprint/c1 and opens it")
		b, err := os.ReadFile(filepath.Join(r.job, FinishName))
		require.NoError(t, err)
		res := typedrec.ParseCardResult(b)
		assert.True(t, res.Shaped, "%s:\n%s", tc.line, b)
		assert.Equal(t, head, res.Head)
		assert.Equal(t, "sprint/c1", res.Branch)
		wantVerdict := "ok"
		if strings.HasPrefix(tc.title, "nothing:") {
			wantVerdict = "nothing"
		}
		assert.Equal(t, wantVerdict, res.Verdict, tc.line)
		assert.Equal(t, tc.title, res.Report, "the report is the title")
		assert.Equal(t, tc.title, res.Title)
		assert.Equal(t, tc.body, res.Body)
	}
	code, _, errb := r.sh(r.repo, `gh pr create --body x`)
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "REFUSED pr create with no --title")
}

// gh pr review is a read's verdict; diff, view and checks answer from the checkout.
func TestGhPrReviewIsTheRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "read")
	r.commit(r.repo, "under review")
	code, out, errb := r.sh(r.repo, "gh pr diff")
	require.Equal(t, 0, code, errb)
	assert.Contains(t, out, "+under review")
	code, out, _ = r.sh(r.repo, "gh pr diff --name-only")
	require.Equal(t, 0, code)
	assert.Equal(t, "f", strings.TrimSpace(out))
	code, out, _ = r.sh(r.job, "gh pr view")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "under review")
	code, _, _ = r.sh(r.job, "gh pr checks")
	assert.Equal(t, 0, code)
	for _, tc := range []struct{ line, verdict, report string }{
		{`gh pr review --approve --body "gate green, diff matches"`, "ok", "gate green, diff matches"},
		{`gh pr review -a`, "ok", "ok"},
		{`gh pr review --request-changes --body "f:1 wrong word"`, "broken", "f:1 wrong word"},
	} {
		code, _, errb := r.sh(r.job, tc.line)
		require.Equal(t, 0, code, "%s: %s", tc.line, errb)
		b, err := os.ReadFile(filepath.Join(r.job, FinishName))
		require.NoError(t, err)
		res := typedrec.ParseCardResult(b)
		assert.True(t, res.Shaped, string(b))
		assert.Equal(t, tc.verdict, res.Verdict, tc.line)
		assert.Equal(t, tc.report, res.Report, tc.line)
	}
	for _, line := range []string{`gh pr review --comment --body x`, `gh pr review --request-changes`} {
		code, _, errb := r.sh(r.job, line)
		assert.Equal(t, 1, code, line)
		assert.Contains(t, errb, "REFUSED", line)
	}
}

// A read that hands its own RESULT.md to gh pr review as the body (it begins head: <sha>)
// is recorded with the body's report: line, never the head line: the inbox's judgment line
// carries the reader's finding, not a commit id.
func TestGhPrReviewBodyThatIsAResultReportsItsReportLine(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "read")
	head := r.commit(r.repo, "under review")
	body := "head: " + head + "\nbranch: sprint/c1\nverdict: broken\ngate: -\noutput: -\nreport: PR title lacks quack.\n\n## Body\n\nf:1 the title lacks quack\n"
	bodyPath := filepath.Join(r.job, "RESULT.md")
	require.NoError(t, os.WriteFile(bodyPath, []byte(body), 0o644))
	code, _, errb := r.sh(r.repo, `gh pr review --request-changes --body-file "`+bodyPath+`"`)
	require.Equal(t, 0, code, errb)
	finish, ok := ReadFinish(r.job)
	require.True(t, ok)
	assert.Equal(t, "broken", finish.Verdict)
	assert.Equal(t, "PR title lacks quack.", finish.Report)
	assert.NotContains(t, finish.Report, head)
}

// Everything gh does that writes is refused with one line, in every profile.
func TestGhRefusesEveryWrite(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"claude", "plain"} {
		r := newRig(t, family, "work")
		for _, line := range []string{"gh pr merge 1", "gh pr close 1", "gh pr comment 1 -b x", "gh pr edit 1", "gh issue comment 1 -b x", "gh api repos", "gh auth login", "gh pr list", "gh issue view 1", "gh repo view", "gh -R o/r pr merge 1"} {
			code, _, errb := r.sh(r.job, line)
			assert.Equal(t, 2, code, "%s %s", family, line)
			assert.Contains(t, errb, "gh: REFUSED", line)
			assert.Equal(t, 1, strings.Count(strings.TrimSpace(errb), "\n")+1, "one line: %s", line)
		}
	}
}

// Every family has a profile that keeps the contract: a push is recorded, JOB.md names
// the checkout, the branch, the commit and the result shape.
func TestEveryProfileKeepsTheContract(t *testing.T) {
	t.Parallel()
	for _, family := range Families {
		p := For(family)
		assert.Equal(t, family, p.Family())
		r := newRig(t, family, "work")
		head := r.commit(r.repo, "w")
		code, _, errb := r.sh(r.repo, "git push")
		require.Equal(t, 0, code, "%s: %s", family, errb)
		_, got := LastPushed(r.job)
		assert.Equal(t, head, got, family)
		job, err := os.ReadFile(filepath.Join(r.job, JobName))
		require.NoError(t, err)
		for _, want := range []string{r.repo, "sprint/c1", r.base, "RESULT.md", "verdict: ok | not-done | nothing", "-count=1", "-timeout 600s"} {
			assert.Contains(t, string(job), want, family)
		}
		assert.NotContains(t, string(job), r.job+"/gocache", "%s: no job has a build cache of its own", family)
	}
}

// JOB.md carries what the attempt before left and the tier; a read's says to review.

// A push the member cannot carry is refused with one line and recorded as
// nothing: a delete, a prune, a mirror, every branch or tag, and a push option.
// An option's value is never read as the remote or the source.
func TestAPushTheMemberCannotCarryIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	head := r.commit(r.repo, "w")
	git(t, r.repo, "branch", "feature")
	for _, line := range []string{"git push --delete origin feature", "git push -d origin feature", "git push origin --prune", "git push --mirror origin",
		"git push --all origin", "git push --tags origin", "git push -o ci.skip origin HEAD", "git push --push-option=ci.skip origin HEAD", "git push -oci.skip origin HEAD"} {
		code, _, errb := r.sh(r.repo, line)
		assert.Equal(t, 1, code, line)
		assert.Contains(t, errb, "REFUSED", line)
		assert.Equal(t, 1, strings.Count(strings.TrimSpace(errb), "\n")+1, "one line: %s", line)
		_, err := os.Stat(filepath.Join(r.job, PushedName))
		assert.True(t, os.IsNotExist(err), "a refused push records nothing: %s", line)
	}
	code, _, errb := r.sh(r.repo, "git push --repo origin origin HEAD")
	require.Equal(t, 0, code, errb)
	_, got := LastPushed(r.job)
	assert.Equal(t, head, got, "an option's value is consumed, never read as the source")
}

// git accepts an unambiguous prefix of a long flag, so each refused long flag is refused at
// every prefix (docs/SPEC-CARD-CONTRACT.md, the push): the refusal names the flag as typed,
// records nothing, and the lease and ordinary pushes still pass.
func TestAPushRefusesAbbreviatedFlags(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	head := r.commit(r.repo, "w")
	for _, flag := range []string{"--del", "--dele", "--delete", "--mirr", "--mirror", "--push-o=x", "--push-option=x", "--pus=x", "--pru", "--bra", "--tag", "--al"} {
		code, _, errb := r.sh(r.repo, "git push "+flag+" origin HEAD")
		assert.Equal(t, 1, code, flag)
		assert.Contains(t, errb, "REFUSED git push "+flag+":", flag)
		_, err := os.Stat(filepath.Join(r.job, PushedName))
		assert.True(t, os.IsNotExist(err), "a refused push records nothing: %s", flag)
	}
	for _, line := range []string{"git push --force-with-lease origin HEAD", "git push --force-with-lease=main origin HEAD", "git push origin HEAD"} {
		code, _, errb := r.sh(r.repo, line)
		require.Equal(t, 0, code, "%s: %s", line, errb)
		_, got := LastPushed(r.job)
		assert.Equal(t, head, got, line)
	}
}

// gh's finishes are scoped to the card's kind: a read never creates a pull
// request and work never reviews one; neither writes a finish.
func TestGhFinishesAreScopedToTheCardsKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, line string }{
		{"read", `gh pr create --title T --body x`},
		{"work", `gh pr review --approve --body x`},
	} {
		r := newRig(t, "claude", tc.kind)
		code, _, errb := r.sh(r.repo, tc.line)
		assert.Equal(t, 2, code, tc.kind)
		assert.Contains(t, errb, "REFUSED", tc.kind)
		_, err := os.Stat(filepath.Join(r.job, FinishName))
		assert.True(t, os.IsNotExist(err), "%s: no finish", tc.kind)
	}
}
