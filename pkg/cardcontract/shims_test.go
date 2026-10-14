package cardcontract

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShimPushToALocalPathRemoteReachesGit pins the local-path push
// (docs/SPEC-CARD-CONTRACT.md, the push): a push whose remote is a local path
// goes through to the real git unchanged, so the bare repository the push names
// holds the commit after, and records nothing in the job, while a push to
// origin is still answered by the shim (pushed.tsv written, origin untouched):
// a test or tool inside a card that pushes to a bare repository it made under
// its own temp directory needs the push to land.
func TestShimPushToALocalPathRemoteReachesGit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-SWARM.md)")
	}
	for _, family := range []string{"claude", "plain"} {
		root := t.TempDir()
		bare, job, work := filepath.Join(root, "origin.git"), filepath.Join(root, "job"), filepath.Join(root, "job", "repo")
		shimGit(t, root, "init", "-q", "--bare", bare)
		shimGit(t, root, "clone", "-q", bare, work)
		require.NoError(t, os.MkdirAll(job, 0o755))
		shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "one")
		head := shimGit(t, work, "rev-parse", "HEAD")
		f := Frame{Kind: "work", Card: "c1", Attempt: 1, Model: "test/model", Repo: cardURL, BaseRef: "main", Branch: "sprint/c1", ReviewBase: "main"}
		s := Staged{Job: job, Repo: work, Head: head, Git: realGit(t)}
		shims := filepath.Join(root, "shim")
		require.NoError(t, Install(For(family), f, s, shims), family)

		// a push to the bare repository by its local path reaches the real git
		code, _, errb := shimSh(t, shims, work, "git push "+shq(bare)+" HEAD:refs/heads/x")
		require.Equal(t, 0, code, "%s: %s", family, errb)
		assert.NotContains(t, errb, "pushed by the sprint", "%s: the push is not answered by the shim", family)
		assert.Equal(t, head, shimGit(t, bare, "rev-parse", "refs/heads/x"), "%s: the bare repository holds the commit", family)
		_, err := os.Stat(filepath.Join(job, PushedName))
		assert.True(t, os.IsNotExist(err), "%s: a push to a local-path remote records nothing", family)

		// a push to origin is still answered by the shim: pushed.tsv written, origin untouched
		code, _, errb = shimSh(t, shims, work, "git push origin HEAD")
		require.Equal(t, 0, code, "%s: %s", family, errb)
		assert.Contains(t, errb, "pushed by the sprint", family)
		branch, got := LastPushed(job)
		assert.Equal(t, "main", branch, family)
		assert.Equal(t, head, got, family)
		assert.Equal(t, "refs/heads/x", strings.TrimSpace(shimGit(t, bare, "for-each-ref", "--format=%(refname)", "refs/heads")), "%s: origin is untouched", family)
	}
}

// realGit finds the real git binary: the first git on PATH that is not a script
// (docs/SPEC-CARD-CONTRACT.md section 5, native fills Staged.Git with it). The
// wall installs the shims — scripts — first on the child's PATH, and a push
// handed through to a shim there is answered by it, never pushed, so the shims
// the tests render hand through to the real git.
func realGit(t *testing.T) string {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, "git")
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var head [2]byte
		n, _ := f.Read(head[:])
		require.NoError(t, f.Close())
		if n == 2 && head[0] == '#' && head[1] == '!' {
			continue // a script that answers for git: a shim, not the real git
		}
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	require.FailNow(t, "no real git on PATH: every git there is a script")
	return ""
}

// shimGit runs the real git in dir, hermetic, and returns its trimmed output:
// never the git of the surrounding PATH, whose shims answer for commands the
// test sets up with.
func shimGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(realGit(t), append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// shimSh runs one command line in dir with the rendered shims first on PATH; its
// exit, stdout and stderr.
func shimSh(t *testing.T, shims, dir, line string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = testgit.Environ("PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return code, out.String(), errb.String()
}

// TestShimRefusesRewriteAtOrBelowStaged pins harness card B (v1.2.17): a take
// may not rewrite at or below the staged commit. The git shim refuses reset,
// rebase and commit --amend whose target is the staged commit or below it, at
// take time (not only at push time), while work above the staged commit and
// git in any other repository pass through to the real git.
func TestShimRefusesRewriteAtOrBelowStaged(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-SWARM.md)")
	}
	for _, family := range []string{"claude", "plain", "openai"} {
		root := t.TempDir()
		bare, job, work := filepath.Join(root, "origin.git"), filepath.Join(root, "job"), filepath.Join(root, "job", "repo")
		shimGit(t, root, "init", "-q", "--bare", bare)
		shimGit(t, root, "clone", "-q", bare, work)
		require.NoError(t, os.MkdirAll(job, 0o755))
		shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "below")
		below := shimGit(t, work, "rev-parse", "HEAD")
		shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "staged")
		staged := shimGit(t, work, "rev-parse", "HEAD")
		f := Frame{Kind: "work", Card: "c1", Attempt: 1, Model: "test/model", Repo: cardURL, BaseRef: "main", Branch: "sprint/c1", ReviewBase: "main"}
		s := Staged{Job: job, Repo: work, Head: staged, Git: realGit(t)}
		shims := filepath.Join(root, "shim")
		require.NoError(t, Install(For(family), f, s, shims), family)

		// at the staged commit itself, an amend rewrites it: refused, HEAD unmoved
		code, _, errb := shimSh(t, shims, work, "git commit --am --allow-empty -m rewritten")
		require.NotEqual(t, 0, code, "%s amend at staged: %s", family, errb)
		assert.Contains(t, errb, "REFUSED", family)
		assert.Contains(t, errb, "staged commit", family)
		assert.Equal(t, staged, shimGit(t, work, "rev-parse", "HEAD"), family)

		shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "work")
		head := shimGit(t, work, "rev-parse", "HEAD")

		for _, line := range []string{
			"git reset --soft " + staged,
			"git reset --soft " + below,
			"git reset --soft HEAD~1",
			"git reset --hard HEAD~2",
			"git rebase " + below,
			"git rebase --onto " + below + " " + staged,
			"git rebase --on=" + below + " " + staged,
			"git rebase --on " + below + " " + staged,
			"git rebase --roo",
			"git rebase -X ours " + below,
			"git rebase -x true " + below,
			"git rebase --ex true " + below,
			"git rebase --empty drop " + below,
			"git rebase -f --empt keep " + below,
			"git rebase -f --strategy-o ours " + below,
			"git rebase --whitespace nowarn " + below,
			"git rebase --wh nowarn " + below,
			"git rebase -s ort " + below,
			"git rebase -C 1 " + below,
		} {
			code, _, errb := shimSh(t, shims, work, line)
			assert.NotEqual(t, 0, code, "%s %s", family, line)
			assert.Contains(t, errb, "REFUSED", "%s %s", family, line)
			assert.Contains(t, errb, "staged commit", "%s %s", family, line)
			assert.Equal(t, head, shimGit(t, work, "rev-parse", "HEAD"), "%s %s: HEAD unmoved", family, line)
		}

		// Linked worktrees share the staged history even with another worktree root.
		linked := filepath.Join(root, "linked")
		shimGit(t, work, "worktree", "add", "-q", "--detach", linked, staged)
		code, _, errb = shimSh(t, shims, linked, "git commit --am --allow-empty -m rewritten")
		assert.NotEqual(t, 0, code, "%s linked amend", family)
		assert.Contains(t, errb, "REFUSED", family)
		assert.Equal(t, staged, shimGit(t, linked, "rev-parse", "HEAD"), family)
		shimGit(t, linked, "commit", "-q", "--allow-empty", "-m", "linked-work")
		linkedHead := shimGit(t, linked, "rev-parse", "HEAD")
		code, _, errb = shimSh(t, shims, work, "git -C "+shq(linked)+" reset --soft "+below)
		assert.NotEqual(t, 0, code, "%s linked reset", family)
		assert.Contains(t, errb, "REFUSED", family)
		assert.Equal(t, linkedHead, shimGit(t, linked, "rev-parse", "HEAD"), family)

		// work above the staged commit is the child's own: amend, a reset that
		// stays above it, and a rebase onto it all reach the real git
		for _, line := range []string{
			"git commit --amend --allow-empty -m work-amended",
			"git reset --soft HEAD",
			"git rebase " + staged,
		} {
			code, _, errb := shimSh(t, shims, work, line)
			assert.Equal(t, 0, code, "%s %s: %s", family, line, errb)
		}

		// a repository that is not the staged checkout is not guarded
		scratch := filepath.Join(root, "scratch")
		shimGit(t, root, "init", "-q", scratch)
		shimGit(t, scratch, "commit", "-q", "--allow-empty", "-m", "one")
		shimGit(t, scratch, "commit", "-q", "--allow-empty", "-m", "two")
		code, _, errb = shimSh(t, shims, scratch, "git reset --soft HEAD~1 && git commit --amend --allow-empty -m one-amended")
		assert.Equal(t, 0, code, "%s scratch repo: %s", family, errb)
	}
}

// TestShimCommitValuesAreNotAmendFlags pins the normal-commit pass-through of
// docs/SPEC-CARD-CONTRACT.md section 5, including values and the path boundary.
func TestShimCommitValuesAreNotAmendFlags(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-SWARM.md)")
	}
	for _, family := range []string{"claude", "plain", "openai"} {
		for _, line := range []string{
			"git commit --allow-empty -m --amend",
			"git commit --allow-empty -am --amend",
			"git commit --allow-empty --message --amend",
			"git commit --allow-empty --mes --amend",
			"git commit --allow-empty -F --amend",
			"git commit --allow-empty --fil --amend",
			"git commit -m normal -- --amend",
		} {
			t.Run(family+"/"+line, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				work := filepath.Join(root, "repo")
				shimGit(t, root, "init", "-q", work)
				shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "staged")
				staged := shimGit(t, work, "rev-parse", "HEAD")
				require.NoError(t, os.WriteFile(filepath.Join(work, "--amend"), []byte("work"), 0o644))
				shimGit(t, work, "add", "--", "--amend")
				f := Frame{Kind: "work", Card: "c1", Model: "test/model", Repo: cardURL, Branch: "sprint/c1"}
				shims := filepath.Join(root, "shim")
				require.NoError(t, Install(For(family), f, Staged{Job: root, Repo: work, Head: staged, Git: realGit(t)}, shims))
				code, _, errb := shimSh(t, shims, work, line)
				assert.Equal(t, 0, code, "%s", errb)
				assert.Equal(t, staged, shimGit(t, work, "rev-parse", "HEAD^"))
			})
		}
	}
}

// TestShimResetPathspecsMatchGit pins section 5's ordinary-reset pass-through:
// index-only resets use Git's pathspec semantics, including mode incompatibilities.
func TestShimResetPathspecsMatchGit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-SWARM.md)")
	}
	for _, family := range []string{"claude", "plain", "openai"} {
		for _, line := range []string{
			"git reset -- HEAD~1",
			"git reset HEAD -- HEAD~1",
			"git reset STAGED -- HEAD~1",
			"git reset BELOW -- HEAD~1",
			"git reset --pathspec-from-file=PATHFILE",
			"git reset STAGED --pathspec-from-file=paths",
			"git -C nested reset STAGED --pathspec-from-file=../paths",
			"git reset STAGED --pathspec-from-file PATHFILE",
			"git reset BELOW --pathspec-from-fil=PATHFILE",
			"git reset STAGED --pathspec-from-file=NEWLINEFILE",
			"git reset STAGED --pathspec-from-file=NEWLINEFILE --pathspec-file-nul",
			"git reset STAGED --pathspec-from-file=NULFILE",
			"git reset STAGED --pathspec-from-file=NULFILE --pathspec-file-nul",
			"git reset --mixed STAGED -- HEAD~1",
			"git reset --soft STAGED -- HEAD~1",
			"git reset --merge STAGED -- HEAD~1",
			"git reset --keep STAGED -- HEAD~1",
			"git reset --hard STAGED -- HEAD~1",
			"git reset --hard STAGED --pathspec-from-file=PATHFILE",
		} {
			t.Run(family+"/"+line, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				work := filepath.Join(root, "repo")
				shimGit(t, root, "init", "-q", work)
				shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "below")
				below := shimGit(t, work, "rev-parse", "HEAD")
				path := filepath.Join(work, "HEAD~1")
				require.NoError(t, os.WriteFile(path, []byte("staged"), 0o644))
				shimGit(t, work, "add", "--", "HEAD~1")
				shimGit(t, work, "commit", "-q", "-m", "staged")
				staged := shimGit(t, work, "rev-parse", "HEAD")
				shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "own")
				head := shimGit(t, work, "rev-parse", "HEAD")
				pathfile := filepath.Join(root, "paths")
				require.NoError(t, os.WriteFile(pathfile, []byte("HEAD~1\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(work, "paths"), []byte("HEAD~1\n"), 0o644))
				require.NoError(t, os.Mkdir(filepath.Join(work, "nested"), 0o755))
				newlinefile, nulfile := filepath.Join(root, "newline-paths"), filepath.Join(root, "nul-paths")
				require.NoError(t, os.WriteFile(newlinefile, []byte("\n"), 0o644))
				require.NoError(t, os.WriteFile(nulfile, []byte{0}, 0o644))
				line = strings.NewReplacer("STAGED", staged, "BELOW", below, "PATHFILE", shq(pathfile), "NEWLINEFILE", shq(newlinefile), "NULFILE", shq(nulfile)).Replace(line)
				f := Frame{Kind: "work", Card: "c1", Model: "test/model", Repo: cardURL, Branch: "sprint/c1"}
				shims := filepath.Join(root, "shim")
				require.NoError(t, Install(For(family), f, Staged{Job: root, Repo: work, Head: staged, Git: realGit(t)}, shims))
				require.NoError(t, os.WriteFile(path, []byte("work"), 0o644))
				shimGit(t, work, "add", "--", "HEAD~1")
				wantCode, wantOut, wantErr := shimSh(t, shims, work, strings.Replace(line, "git ", shq(realGit(t))+" ", 1))
				require.Equal(t, head, shimGit(t, work, "rev-parse", "HEAD"), "real Git keeps HEAD")
				wantIndex := shimGit(t, work, "diff", "--cached", "--name-status")
				shimGit(t, work, "add", "--", "HEAD~1")
				code, out, errb := shimSh(t, shims, work, line)
				assert.Equal(t, wantCode, code, "%s", errb)
				assert.Equal(t, wantOut, out)
				assert.Equal(t, wantErr, errb)
				assert.Equal(t, head, shimGit(t, work, "rev-parse", "HEAD"))
				assert.Equal(t, wantIndex, shimGit(t, work, "diff", "--cached", "--name-status"))
				content, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "work", string(content))
				code, _, errb = shimSh(t, shims, work, "git reset --soft "+staged+" --")
				assert.NotEqual(t, 0, code)
				assert.Contains(t, errb, "REFUSED")
				assert.Equal(t, head, shimGit(t, work, "rev-parse", "HEAD"))
			})
		}
	}
}

// TestShimEmptyPathspecInputCannotMoveStagedHEAD pins section 5's staged ancestry
// guard: an empty pathspec source does not select an index-only Git reset.
func TestShimEmptyPathspecInputCannotMoveStagedHEAD(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-SWARM.md)")
	}
	for _, family := range []string{"claude", "plain", "openai"} {
		for _, mode := range []string{"--mixed", "--soft", "--hard"} {
			for _, source := range []string{"EMPTYFILE", "-"} {
				t.Run(family+"/"+mode+"/"+source, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					work := filepath.Join(root, "repo")
					shimGit(t, root, "init", "-q", work)
					shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "staged")
					staged := shimGit(t, work, "rev-parse", "HEAD")
					shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "own")
					head := shimGit(t, work, "rev-parse", "HEAD")
					file := filepath.Join(root, "empty-paths")
					require.NoError(t, os.WriteFile(file, nil, 0o644))
					f := Frame{Kind: "work", Card: "c1", Model: "test/model", Repo: cardURL, Branch: "sprint/c1"}
					shims := filepath.Join(root, "shim")
					require.NoError(t, Install(For(family), f, Staged{Job: root, Repo: work, Head: staged, Git: realGit(t)}, shims))
					line := "git reset " + mode + " " + staged + " --pathspec-from-file=" + strings.ReplaceAll(source, "EMPTYFILE", shq(file)) + " < " + shq(file)
					code, _, errb := shimSh(t, shims, work, line)
					assert.NotEqual(t, 0, code)
					assert.Contains(t, errb, "REFUSED")
					assert.Equal(t, head, shimGit(t, work, "rev-parse", "HEAD"))
					code, _, errb = shimSh(t, shims, work, strings.Replace(line, "git ", shq(realGit(t))+" ", 1))
					assert.Equal(t, 0, code, "%s", errb)
					assert.Equal(t, staged, shimGit(t, work, "rev-parse", "HEAD"), "real Git moves HEAD with empty input")
				})
			}
		}
	}
}
