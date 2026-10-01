//go:build functional

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// This diagnostic distinguishes the product wall from the PATH-only Git shim.
// Both remotes live under this job's temporary writable directory. No forge,
// network address, credential, or member-side push participates in the probe.
func TestReview4964AbsoluteGitPushBypassesShimInsideProductWall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unsupported product-wall platform")
	}
	checkCmd, checkCancel := subproc.CommandFor(context.Background(), 30*time.Second, builtSandbox, "check")
	defer checkCancel()
	check, err := checkCmd.CombinedOutput()
	require.NoError(t, err, "built product wall check failed: %s", check)
	if strings.Contains(string(check), "backend=none") {
		t.Skipf("unsupported product-wall backend: %s", check)
	}
	if fi, err := os.Stat("/usr/bin/git"); err != nil || !fi.Mode().IsRegular() {
		t.Skipf("unsupported: /usr/bin/git is unavailable: %v", err)
	}

	d, root, _, repoURL, _ := review4964WorkSetup(t, false)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	harness := review4964Harness(t, `
set -eu
cd repo
printf 'absolute git probe\n' >> f
git commit -q -am 'absolute git probe'
git init -q --bare "$NOVA_SWARM_JOB/direct.git"
git init -q --bare "$NOVA_SWARM_JOB/shim.git"
/usr/bin/git push -q "$NOVA_SWARM_JOB/direct.git" HEAD:refs/heads/direct
test ! -e "$NOVA_SWARM_JOB/.sprint/pushed.tsv"
git push -q "$NOVA_SWARM_JOB/shim.git" HEAD:refs/heads/shim
gh pr create --title 'Local wall probe' --body 'No external publication'
printf 'head: %s\nbranch: %s\nverdict: ok\ngate: -\noutput: -\nreport: local wall probe\n' "$(git rev-parse HEAD)" "$(git symbolic-ref --short HEAD)" > "$NOVA_SWARM_JOB/RESULT.md"
`)
	first, rest, _ := strings.Cut(memberCard, "\n")
	p := member.Packet{Card: "a-1", Kind: "work", As: "m1", Primary: "a-1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 1,
		Brief: first + "\nbase-repo: " + repoURL + "\nBASE: main\n" + rest, Branch: "sprint/a-1.w1"}
	rn := &nativeRunner{self: review4964NativeLauncher(t, root), sprintBin: d.bin, harness: harness,
		model: "fake/claude-card-contract", root: root, slots: filepath.Join(root, "slots"),
		resultsRoot: filepath.Join(root, "results"), deadline: time.Minute,
		tokens: "unmetered", noWall: false, stderr: io.Discard,
		env: []string{"PATH=" + filepath.Dir(builtSandbox) + string(os.PathListSeparator) + os.Getenv("PATH")}}
	child, err := rn.Start(p)
	require.NoError(t, err)
	nc, ok := child.(*nativeChild)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for !child.Done() {
		require.NoError(t, ctx.Err(), "native child did not end; log: %s", nc.logPath)
		time.Sleep(100 * time.Millisecond)
	}
	outer, err := os.ReadFile(nc.logPath)
	require.NoError(t, err)
	wallLog, err := os.ReadFile(filepath.Join(rn.slots, launchName(p), "native.log"))
	require.NoError(t, err, "native output:\n%s", outer)
	require.NoError(t, nc.err, "native output:\n%s\nwall output:\n%s", outer, wallLog)
	require.True(t, child.Result().Ran, "native output:\n%s\nwall output:\n%s", outer, wallLog)

	var receipt string
	for _, line := range strings.Split(string(wallLog), "\n") {
		if strings.HasPrefix(line, "SANDBOX OK ") {
			receipt = line
			break
		}
	}
	require.NotEmpty(t, receipt, "a no-wall run does not establish this observation:\n%s", wallLog)
	backend, cwd, reason := wallNamed(receipt)
	require.Empty(t, reason)
	require.Contains(t, []string{"landlock", "sandbox-exec"}, backend)
	assert.Equal(t, nc.job, cwd)
	assert.Contains(t, receipt, "net=nopromise")

	head := strings.TrimSpace(runGit(t, filepath.Join(nc.job, "repo"), "rev-parse", "HEAD"))
	direct := filepath.Join(nc.job, "direct.git")
	shim := filepath.Join(nc.job, "shim.git")
	assert.Equal(t, head, strings.TrimSpace(runGit(t, direct, "rev-parse", "--verify", "refs/heads/direct")),
		"absolute Git published only to a local bare repository inside the wall's write root")
	assert.Empty(t, strings.TrimSpace(runGit(t, shim, "for-each-ref", "--format=%(refname)", "refs/heads/shim")),
		"unqualified git push was intercepted, leaving its local bare repository untouched")
	branch, recordedHead := cardcontract.LastPushed(nc.job)
	assert.Equal(t, "shim", branch)
	assert.Equal(t, head, recordedHead)
	pushes, err := os.ReadFile(filepath.Join(nc.job, cardcontract.PushedName))
	require.NoError(t, err)
	assert.Len(t, strings.Split(strings.TrimSpace(string(pushes)), "\n"), 1,
		"the direct push must have no shim record")
}

// A Claude card's gh shim writes the complete result under .sprint/finish.md.
// The contract says that command finishes the card without a second RESULT.md.
// Native must count that valid finish before the member interprets it.
func TestReview4964ClaudeShimFinishAloneEarnsNativeOK(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the profile shims are POSIX shell")
	}
	d, root, _, repoURL, _ := review4964WorkSetup(t, false)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	harness := review4964Harness(t, `
set -eu
cd repo
printf 'shim-only finish\n' >> f
git commit -q -am 'shim-only finish'
git push -q origin HEAD:refs/heads/child
gh pr create --title 'Shim-only finish' --body 'No extra RESULT.md'
`)
	first, rest, _ := strings.Cut(memberCard, "\n")
	p := member.Packet{Card: "a-1", Kind: "work", As: "m1", Primary: "a-1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 1,
		Brief: first + "\nbase-repo: " + repoURL + "\nBASE: main\n" + rest, Branch: "sprint/a-1.w1"}
	rn := &nativeRunner{self: review4964NativeLauncher(t, root), sprintBin: d.bin, harness: harness,
		model: "fake/claude-card-contract", root: root, slots: filepath.Join(root, "slots"),
		resultsRoot: filepath.Join(root, "results"), deadline: time.Minute,
		tokens: "unmetered", noWall: true, stderr: io.Discard}
	child, err := rn.Start(p)
	require.NoError(t, err)
	nc, ok := child.(*nativeChild)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for !child.Done() {
		require.NoError(t, ctx.Err(), "native child did not end; log: %s", nc.logPath)
		time.Sleep(100 * time.Millisecond)
	}
	outer, err := os.ReadFile(nc.logPath)
	require.NoError(t, err)
	require.NoError(t, nc.err, "native output:\n%s", outer)
	finish, present := cardcontract.ReadFinish(nc.job)
	require.True(t, present, "the gh shim must have published its result")
	require.True(t, finish.Shaped, "the shim result must carry the complete result shape")
	assert.Equal(t, "ok", finish.Verdict)
	assert.Equal(t, strings.TrimSpace(runGit(t, filepath.Join(nc.job, "repo"), "rev-parse", "HEAD")), finish.Head)
	for _, result := range []string{filepath.Join(nc.job, "RESULT.md"), filepath.Join(nc.job, "repo", "RESULT.md")} {
		_, err = os.Stat(result)
		require.ErrorIs(t, err, os.ErrNotExist, "the profile promises no extra RESULT.md write at %s", result)
	}
	assert.Contains(t, string(outer), "NATIVE OK ", "a valid shim-only finish must earn native completion")
	require.True(t, child.Result().Ran, "native must accept the shaped shim-only finish:\n%s", outer)
}
