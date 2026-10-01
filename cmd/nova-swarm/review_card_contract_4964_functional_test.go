//go:build functional

package main

import (
	"context"
	"fmt"
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
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// These probes exercise the framed profile through the real member/native path. Their
// origin is a local bare repository; gh is a recorder, never a GitHub client.
func TestReview4964ScriptedSSHChildCreatesOriginCardBranchAndPR(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the profile shims are POSIX shell")
	}
	d, root, origin, repoURL, ghDir := review4964WorkSetup(t, false)
	harness := review4964Harness(t, `
set -eu
repo=$(sed -n 's/^You are in a checkout of \([^ ]*\) on branch.*/\1/p' JOB.md)
host=${repo%%/*}
path=${repo#*/}
git clone "git@$host:$path" worker-clone
cd worker-clone
git switch -q -c child/review-4964
printf 'child change\n' >> f
git commit -q -am 'child change'
git push -q -u origin child/review-4964
gh pr create --title 'Card-contract probe' --body 'body from child'
`)
	m := review4964Member(t, d, root, harness, "fake/claude-card-contract", false, origin, repoURL, review4964GH(t, ghDir))
	story := review4964RunCard(t, d, m, "a-1", "finished attempt 1")

	got := strings.TrimSpace(runGit(t, origin, "rev-parse", "--verify", "refs/heads/sprint/a-1.w1"))
	require.Len(t, got, 40, "the member pushes only to the card's branch")
	assert.Equal(t, "child change", strings.TrimSpace(runGit(t, origin, "log", "-1", "--format=%s", got)))
	assert.Empty(t, strings.TrimSpace(runGit(t, origin, "for-each-ref", "--format=%(refname)", "refs/heads/child/")), "the child's branch name is not published as a second origin branch")
	assert.Contains(t, story, "pushed="+got+" to sprint/a-1.w1")

	args, err := os.ReadFile(filepath.Join(ghDir, "args"))
	require.NoError(t, err, "the member translates the child's request into one PR create")
	assert.Equal(t, []string{"pr", "create", "--repo", strings.TrimSuffix(repoURL, ".git"), "--head", "sprint/a-1.w1", "--title", "Card-contract probe", "--body-file", "-", "--base", "main"}, strings.Split(strings.TrimSpace(string(args)), "\n"))
	body, err := os.ReadFile(filepath.Join(ghDir, "body"))
	require.NoError(t, err)
	assert.Equal(t, "body from child", strings.TrimSpace(string(body)))
	assert.Contains(t, story, "pr=https://example.invalid/review/4964")
}

func TestReview4964ScriptedReaderDiffRequestsChanges(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the profile shims are POSIX shell")
	}
	d, root, origin, repoURL, ghDir := review4964WorkSetup(t, true)
	workHarness := review4964Harness(t, `
set -eu
(cd repo && printf 'review this change\n' >> f && git commit -q -am 'reviewable change')
printf 'head: %s\nbranch: %s\nverdict: ok\ngate: -\noutput: -\nreport: prepared for review\n' "$(git -C repo rev-parse HEAD)" "$(git -C repo symbolic-ref --short HEAD)" > RESULT.md
`)
	work := review4964Member(t, d, root, workHarness, "fake/plain-card-contract", false, origin, repoURL, review4964GH(t, ghDir))
	workStory := review4964RunCard(t, d, work, "a-1", "finished attempt 1")
	assert.NotContains(t, workStory, "FAILED")
	assert.Len(t, strings.TrimSpace(runGit(t, origin, "rev-parse", "--verify", "refs/heads/sprint/a-1.w1")), 40)

	readerRoot := filepath.Join(t.TempDir(), "reader-a")
	require.NoError(t, os.MkdirAll(filepath.Join(readerRoot, "slots"), 0o755))
	write(t, filepath.Join(readerRoot, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Reader\treader@example.com\n")
	readerHarness := review4964Harness(t, `
set -eu
diff=$(gh pr diff)
case "$diff" in *'review this change'*) ;; *) echo 'the staged diff was not visible' >&2; exit 1 ;; esac
gh pr review --request-changes --body x
`)
	rn := &nativeRunner{self: review4964NativeLauncher(t, root), sprintBin: d.bin, harness: readerHarness, model: "fake/claude-card-reader", root: readerRoot,
		slots: filepath.Join(readerRoot, "slots"), resultsRoot: filepath.Join(readerRoot, "results"), deadline: time.Minute,
		tokens: "unmetered", noWall: true, stderr: io.Discard}
	sp := &execSprint{bin: d.bin, actor: "reader-a", env: []string{"NOVA_SPRINT_REDIS=" + d.addr}}
	out := &lockedBuf{}
	reader := member.New(member.Config{As: "reader-a", Width: 1, Reader: true}, sp, rn, nil, out)
	var story string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for {
		d.must("tick")
		_, err := reader.Tick(time.Now())
		require.NoError(t, err)
		story = d.must("card", "a-1")
		if strings.Contains(story, "broken") {
			break
		}
		memberOutput := out.String()
		if reader.Running() == 0 && strings.Contains(memberOutput, "read a-1: no verdict") {
			finish := review4964FindFinish(t, filepath.Join(readerRoot, "slots"))
			require.True(t, finish.Shaped, "the scripted gh review should have left a shaped native finish")
			require.Equal(t, "broken", finish.Verdict)
			require.Equal(t, "x", finish.Body)
			logs, err := filepath.Glob(filepath.Join(readerRoot, "slots", "*.native.log"))
			require.NoError(t, err)
			var nativeLogs strings.Builder
			for _, path := range logs {
				b, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				fmt.Fprintf(&nativeLogs, "%s:\n%s\n", filepath.Base(path), b)
			}
			require.Failf(t, "reader child ended without a native verdict", "member Running=%d\nmember output:\n%s\nnative log:\n%s\nfinish: %+v", reader.Running(), memberOutput, nativeLogs.String(), finish)
			return
		}
		require.NoError(t, ctx.Err(), "reader did not publish its request-changes verdict:\n%s\n%s", story, out.String())
		time.Sleep(20 * time.Millisecond)
	}
	assert.Contains(t, story, "broken")
	finish := review4964FindFinish(t, filepath.Join(readerRoot, "slots"))
	assert.True(t, finish.Shaped)
	assert.Equal(t, "broken", finish.Verdict)
	assert.Equal(t, "x", finish.Body)
}

func TestReview4964CreateWithoutNewCommitFinishesFailedWithoutPush(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the profile shims are POSIX shell")
	}
	d, root, origin, repoURL, ghDir := review4964WorkSetup(t, false)
	harness := review4964Harness(t, `
set -eu
gh pr create --title 'No commit' --body 'must not open'
`)
	m := review4964Member(t, d, root, harness, "fake/claude-card-contract", false, origin, repoURL, review4964GH(t, ghDir))
	story := review4964RunCard(t, d, m, "a-1", "finished attempt 1")
	assert.Contains(t, story, "FAILED")
	assert.Contains(t, story, "no commit")
	assert.Empty(t, strings.TrimSpace(runGit(t, origin, "for-each-ref", "--format=%(refname)", "refs/heads/sprint/a-1.w1")), "no result branch is pushed without a new commit")
	assert.NoFileExists(t, filepath.Join(ghDir, "args"), "a failed no-commit finish never opens a PR")
}

func TestReview4964PusherRejectsCachedCommitOutsideCurrentCheckout(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	push := b.pusher()
	foreignHead := b.commit(t, "foreign card change\n")
	first := push.Push(b.p, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: foreignHead})
	require.Equal(t, foreignHead, first.Sha, "seed the member push repository with a different card's object")

	otherRoot := filepath.Join(b.root, "other")
	otherSeed := filepath.Join(otherRoot, "seed")
	otherOrigin := filepath.Join(otherRoot, "origin.git")
	require.NoError(t, os.MkdirAll(otherRoot, 0o755))
	runGit(t, "", "init", "-q", "-b", "main", "--", otherSeed)
	require.NoError(t, os.WriteFile(filepath.Join(otherSeed, "f"), []byte("other base\n"), 0o644))
	gitAs(t, otherSeed, "add", "f")
	gitAs(t, otherSeed, "commit", "-q", "-m", "other base")
	runGit(t, "", "clone", "-q", "--bare", "--", otherSeed, otherOrigin)
	otherPacket := member.Packet{Card: "c2", Kind: "work", As: "m1", Primary: "p2", Stream: "s2", Attempt: 1, Gen: 1, Epoch: 7,
		Brief: "RESULT: c2\nbase-repo: " + otherOrigin + "\nBASE: main\n\nOther work.", Branch: "sprint/c2"}
	otherCheckout := filepath.Join(b.slots, launchName(otherPacket), "jobs", otherPacket.Card, swarm.JobRepo)
	require.NoError(t, os.MkdirAll(filepath.Dir(otherCheckout), 0o755))
	runGit(t, "", "clone", "-q", "--", otherOrigin, otherCheckout)
	gitAs(t, otherCheckout, "switch", "-q", "-c", "child/c2")
	otherStaged := gitAs(t, otherOrigin, "rev-parse", "refs/heads/main")
	write(t, filepath.Join(b.slots, launchName(otherPacket), cardcontract.StagedName), otherStaged+"\n")

	got := push.Push(otherPacket, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: foreignHead})
	assert.NotEmpty(t, got.Refused, "an object left in push.git is not evidence that this checkout produced it")
	assert.Empty(t, got.Sha)
	assert.Empty(t, strings.TrimSpace(runGit(t, otherOrigin, "for-each-ref", "--format=%(refname)", "refs/heads/sprint/c2")))
}

func review4964WorkSetup(t *testing.T, readers bool) (*memberDrive, string, string, string, string) {
	t.Helper()
	// The relative repository name makes the child's scp-style clone resolve through
	// the profile shim to this temporary local bare origin without network access.
	dir := t.TempDir()
	hostDir, err := os.MkdirTemp(dir, "host-4964-")
	require.NoError(t, err)
	repoName := filepath.Base(hostDir) + ".git"
	origin := filepath.Join(hostDir, "nested", repoName)
	repoURL, err := filepath.Rel(dir, origin)
	require.NoError(t, err)
	repoURL = filepath.ToSlash(repoURL)
	require.NoError(t, os.MkdirAll(filepath.Dir(origin), 0o755))
	seed := filepath.Join(t.TempDir(), "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	require.NoError(t, os.WriteFile(filepath.Join(seed, "f"), []byte("base\n"), 0o644))
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	first, rest, _ := strings.Cut(memberCard, "\n")
	brief := first + "\nbase-repo: " + repoURL + "\nBASE: main\n" + rest
	d := &memberDrive{t: t, addr: "mem:" + filepath.Join(dir, "sprint.twin"), bin: builtSprint(t)}
	if readers {
		d.must("init", "--members", "m1:1", "--readers", "reader-a,reader-b")
	} else {
		d.must("init", "--members", "m1:1")
	}
	d.must("add", "--stream", "a", "--count", "1", "--brief", brief)
	d.must("start")
	ghDir := filepath.Join(dir, "gh-bin")
	require.NoError(t, os.MkdirAll(ghDir, 0o755))
	return d, dir, origin, repoURL, ghDir
}

func review4964GH(t *testing.T, ghDir string) string {
	t.Helper()
	gh := filepath.Join(ghDir, "gh")
	ghScript := `#!/bin/sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
printf '%s\n' "$@" > "$here/args"
cat > "$here/body"
echo https://example.invalid/review/4964
`
	require.NoError(t, testbin.WriteExecutable(gh, []byte(ghScript), 0o755))
	return gh
}

func review4964Harness(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child.sh")
	require.NoError(t, testbin.WriteExecutable(path, []byte("#!/bin/sh\n"+strings.TrimSpace(body)+"\n"), 0o755))
	return path
}

func review4964NativeLauncher(t *testing.T, cwd string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "native-launcher.sh")
	script := "#!/bin/sh\nset -eu\ncd " + review4964ShellQuote(cwd) + "\nexec " + review4964ShellQuote(builtTool) + " \"$@\"\n"
	require.NoError(t, testbin.WriteExecutable(path, []byte(script), 0o755))
	return path
}

func review4964ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func review4964Member(t *testing.T, d *memberDrive, root, harness, model string, reader bool, origin, repoURL, gh string) *member.Member {
	t.Helper()
	slots := filepath.Join(root, "slots")
	results := filepath.Join(root, "results")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	sp := &execSprint{bin: d.bin, actor: "m1", env: []string{"NOVA_SPRINT_REDIS=" + d.addr}}
	rn := &nativeRunner{self: review4964NativeLauncher(t, root), sprintBin: d.bin, harness: harness, model: model, root: root, slots: slots,
		resultsRoot: results, deadline: time.Minute, tokens: "unmetered", noWall: true, stderr: io.Discard}
	var pu member.Pusher
	if !reader {
		gp := newGitPusher(root, slots, d.bin)
		gp.gh = gh
		// Push from the member's bare repository to the same local origin. The card
		// keeps the relative repo name so its child can use the actual scp spelling.
		baseGit := gp.git
		gp.git = func(ctx context.Context, opts gitrun.Options, args ...string) (gitrun.Result, error) {
			rewritten := append([]string(nil), args...)
			for i := range rewritten {
				if rewritten[i] == repoURL {
					rewritten[i] = origin
				}
			}
			return baseGit(ctx, opts, rewritten...)
		}
		pu = gp
	}
	out := &lockedBuf{}
	actor := "m1"
	if reader {
		actor = "reader-a"
		sp.actor = actor
	}
	return member.New(member.Config{As: actor, Width: 1, Reader: reader}, sp, rn, pu, out)
}

func review4964RunCard(t *testing.T, d *memberDrive, m *member.Member, card, done string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for {
		d.must("tick")
		_, err := m.Tick(time.Now())
		require.NoError(t, err)
		story := d.must("card", card)
		if strings.Contains(story, done) {
			return story
		}
		require.NoError(t, ctx.Err(), "card did not reach %q:\n%s", done, story)
		time.Sleep(20 * time.Millisecond)
	}
}

func review4964FindFinish(t *testing.T, root string) typedrec.CardResult {
	t.Helper()
	var found typedrec.CardResult
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(filepath.ToSlash(path), "/"+filepath.ToSlash(cardcontract.FinishName)) {
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			found = typedrec.ParseCardResult(b)
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, found.Shaped, "native's typed finish record carries a shaped reader result")
	return found
}
