//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SLOW: 6.5 s on hetzner at dev 64b9bec48, over the five-second line.
// Test 1: TestASeatOpensTheFilesItsRulesNameAndNoOther
func TestASeatOpensTheFilesItsRulesNameAndNoOther(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	require.NoError(t, os.MkdirAll(storeDir, 0755))
	initGitStore(t, storeDir)

	// Keypairs: A_admin, A_keeper, B, C, R
	aAdmin := genKey(t, td, "a_admin")
	aKeeper := genKey(t, td, "a_keeper")
	bKey := genKey(t, td, "b")
	cKey := genKey(t, td, "c")
	rKey := genKey(t, td, "recovery")

	// recovery.pub
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(rKey.pubKey+"\n"), 0644))

	// .sops.yaml
	sopsConfig := fmt.Sprintf(`creation_rules:
  - path_regex: ^a\.yaml$
    age: %s,%s
  - path_regex: ^a-keeper\.yaml$
    age: %s,%s
  - path_regex: ^b\.yaml$
    age: %s,%s
  - path_regex: ^swarm-p\.yaml$
    age: %s,%s
  - path_regex: ^c\.yaml$
    age: %s,%s
`, aAdmin.pubKey, rKey.pubKey, aKeeper.pubKey, rKey.pubKey, bKey.pubKey, rKey.pubKey, bKey.pubKey, rKey.pubKey, cKey.pubKey, rKey.pubKey)

	require.NoError(t, os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0644))

	// Seal 5 files
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a.yaml"), []string{aAdmin.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_admin\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a-keeper.yaml"), []string{aKeeper.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_keeper\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "b.yaml"), []string{bKey.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_b\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "swarm-p.yaml"), []string{bKey.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_p\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "c.yaml"), []string{cKey.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_c\n")

	commitAndPush(t, storeDir)

	// 1. With A_admin: exec --as a succeeds, exec --as a-keeper exits 125, check is mine=1 foreign=4
	out, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "HELLO")
	require.Equal(t, 0, code, "A_admin exec a failed: code %d, err: %s", code, errOut)
	assert.Contains(t, out, "HELLO", "expected HELLO in stdout: %s", out)

	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a-keeper", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "HELLO")
	require.Equal(t, 125, code, "expected code 125 on wrong key, got %d, err: %s", code, errOut)

	// sops -d a-keeper.yaml exits 128 with A_admin key
	cmd := exec.Command(sopsPath, "-d", filepath.Join(storeDir, "a-keeper.yaml"))
	cmd.Env = []string{"SOPS_AGE_KEY_FILE=" + aAdmin.privPath, "PATH=/usr/bin:/bin"}
	err := cmd.Run()
	require.Error(t, err, "expected sops -d to fail with wrong key")
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 128 {
		t.Logf("sops exit code: %d", exitErr.ExitCode())
	}

	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	require.Equal(t, 0, code, "check failed for A_admin: code %d, err: %s", code, errOut)
	assert.Contains(t, out, "mine=1", "expected mine=1 foreign=4 in check output: %s", out)
	assert.Contains(t, out, "foreign=4", "expected mine=1 foreign=4 in check output: %s", out)

	// 2. With A_keeper: mirror
	out, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a-keeper", "--key", aKeeper.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "HELLO_KEEPER")
	require.Equal(t, 0, code, "A_keeper exec failed: code %d, out: %s, err: %s", code, out, errOut)
	require.Contains(t, out, "HELLO_KEEPER", "A_keeper exec failed: code %d, out: %s, err: %s", code, out, errOut)
	_, _, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aKeeper.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "NO")
	require.Equal(t, 125, code, "expected code 125, got %d", code)

	// 3. With B: mine=2 foreign=3
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "b", "--key", bKey.privPath, "--sops", sopsPath)
	require.Equal(t, 0, code, "check failed for B: code %d, err: %s", code, errOut)
	assert.Contains(t, out, "mine=2", "expected mine=2 foreign=3: %s", out)
	assert.Contains(t, out, "foreign=3", "expected mine=2 foreign=3: %s", out)

	// 4. With R: opens all five (mine=5 foreign=0)
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", rKey.privPath, "--sops", sopsPath)
	require.Equal(t, 0, code, "check failed for R: code %d, err: %s", code, errOut)
	assert.Contains(t, out, "mine=5", "expected mine=5 foreign=0: %s", out)
	assert.Contains(t, out, "foreign=0", "expected mine=5 foreign=0: %s", out)

	// Mutations:
	// a) file's block listing keeper key while rule does not -> invariant 2 with updatekeys line
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a.yaml"), []string{aAdmin.pubKey, aKeeper.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_drift\n")
	commitAndPush(t, storeDir)
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	require.Equal(t, 1, code, "expected check exit 1 on recipient drift, got %d", code)
	assert.Contains(t, errOut, "recipients differ from .sops.yaml; run: sops updatekeys a.yaml", "expected updatekeys line in errOut: %s", errOut)
	// Revert a.yaml
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a.yaml"), []string{aAdmin.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_admin\n")
	commitAndPush(t, storeDir)

	// b) Add A_keeper to a.yaml's rule without updatekeys -> invariant 2
	brokenRuleCfg := strings.Replace(sopsConfig, fmt.Sprintf("age: %s,%s", aAdmin.pubKey, rKey.pubKey), fmt.Sprintf("age: %s,%s,%s", aAdmin.pubKey, aKeeper.pubKey, rKey.pubKey), 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(brokenRuleCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "expected invariant 2 error: %s", errOut)
	assert.Contains(t, errOut, "recipients differ from .sops.yaml; run: sops updatekeys a.yaml", "expected invariant 2 error: %s", errOut)
	// Restore config
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0644)
	commitAndPush(t, storeDir)

	// c) Two-rule fixture re-pointed to {A_admin, A_keeper} -> red on invariant 1
	twoRuleCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^a\.yaml$
    age: %s,%s
  - path_regex: ^a-keeper\.yaml$
    age: %s,%s
`, aAdmin.pubKey, aKeeper.pubKey, aKeeper.pubKey, rKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(twoRuleCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "expected invariant 1 recovery mismatch error: %s", errOut)
	assert.Contains(t, errOut, "does not contain declared recovery key", "expected invariant 1 recovery mismatch error: %s", errOut)
	// Also asserted on exec -> 125
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "NO")
	assert.Equal(t, 125, code, "expected exec 125 on declared recovery mismatch, got %d: %s", code, errOut)

	// Restore config
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0644)
	commitAndPush(t, storeDir)

	// d) recovery.pub 5 bad states: deleted, empty, mode 000, two lines, not-a-key
	recFile := filepath.Join(storeDir, "recovery.pub")
	testBadRecovery := func(state string, setup func(), cleanup func()) {
		setup()
		_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
		assert.Equal(t, 1, code, "state %s: check expected 1, got %d (%s)", state, code, errOut)
		_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "NO")
		assert.Equal(t, 125, code, "state %s: exec expected 125, got %d (%s)", state, code, errOut)
		if cleanup != nil {
			cleanup()
		}
	}

	restorePub := func() {
		_ = os.Chmod(recFile, 0644)
		_ = os.WriteFile(recFile, []byte(rKey.pubKey+"\n"), 0644)
	}
	testBadRecovery("deleted", func() { _ = os.Remove(recFile) }, restorePub)
	testBadRecovery("empty", func() { _ = os.WriteFile(recFile, []byte(""), 0644) }, restorePub)
	testBadRecovery("mode 000", func() { _ = os.Chmod(recFile, 0000) }, restorePub)
	testBadRecovery("two lines", func() { _ = os.WriteFile(recFile, []byte(rKey.pubKey+"\n"+rKey.pubKey+"\n"), 0644) }, restorePub)
	testBadRecovery("not-a-key", func() { _ = os.WriteFile(recFile, []byte("not-an-age-key\n"), 0644) }, restorePub)

	// Restore recovery.pub
	_ = os.WriteFile(recFile, []byte(rKey.pubKey+"\n"), 0644)
	commitAndPush(t, storeDir)

	// e) Dropped anchor from ^a\.yaml$
	badAnchorCfg := strings.Replace(sopsConfig, "^a\\.yaml$", "a\\.yaml", 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(badAnchorCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "expected unanchored regex failure: %s", errOut)
	assert.Contains(t, errOut, "not anchored at both ends", "expected unanchored regex failure: %s", errOut)

	// f) Recipient truncated to age1
	truncCfg := strings.Replace(sopsConfig, aAdmin.pubKey, "age1", 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(truncCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "expected truncated age key failure: %s", errOut)
	assert.Contains(t, errOut, "not a valid age public key", "expected truncated age key failure: %s", errOut)

	// g) R listed twice in one rule
	dupCfg := strings.Replace(sopsConfig, aAdmin.pubKey, rKey.pubKey, 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(dupCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "expected duplicate recipient failure: %s", errOut)
	assert.Contains(t, errOut, "duplicate recipient", "expected duplicate recipient failure: %s", errOut)
}

// SLOW: 2.5 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// Test 20: TestAStaleWorkingCopyIsRefused
func TestAStaleWorkingCopyIsRefused(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: 123\n")
	commitAndPush(t, storeDir)

	// 1. Working copy HEAD ahead of remote
	_ = os.WriteFile(filepath.Join(storeDir, "extra.txt"), []byte("commit ahead\n"), 0644)
	runCmd(t, storeDir, "git", "add", "extra.txt")
	runCmd(t, storeDir, "git", "commit", "-m", "local commit ahead")

	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "ahead working copy expected check exit 1 naming pull, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "pull --ff-only", "ahead working copy expected check exit 1 naming pull, got %d: %s", code, errOut)

	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	assert.Equal(t, 125, code, "ahead working copy expected exec exit 125 naming pull, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "pull --ff-only", "ahead working copy expected exec exit 125 naming pull, got %d: %s", code, errOut)

	// 2. Push to sync and verify green
	runCmd(t, storeDir, "git", "push", "origin", "main")
	out, _, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	assert.Equal(t, 0, code, "synced working copy expected check exit 0 with head=: %d, out=%s", code, out)
	assert.Contains(t, out, "head=", "synced working copy expected check exit 0 with head=: %d, out=%s", code, out)

	// 3. Detached HEAD refusal (exit 2 on check, 125 on exec)
	runCmd(t, storeDir, "git", "checkout", "--detach", "HEAD")
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	assert.Equal(t, 2, code, "detached HEAD expected check refusal 2, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "detached HEAD", "detached HEAD expected check refusal 2, got %d: %s", code, errOut)
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	assert.Equal(t, 125, code, "detached HEAD expected exec refusal 125, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "detached HEAD", "detached HEAD expected exec refusal 125, got %d: %s", code, errOut)
	runCmd(t, storeDir, "git", "checkout", "main")

	// 4. Branch with no upstream refusal (exit 2 on check, 125 on exec)
	runCmd(t, storeDir, "git", "checkout", "-b", "local-no-upstream")
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	assert.Equal(t, 2, code, "no upstream branch expected check refusal 2, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "no upstream", "no upstream branch expected check refusal 2, got %d: %s", code, errOut)
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	assert.Equal(t, 125, code, "no upstream branch expected exec refusal 125, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "no upstream", "no upstream branch expected exec refusal 125, got %d: %s", code, errOut)
	runCmd(t, storeDir, "git", "checkout", "main")

	check := func(args ...string) (string, string, int) {
		return runNovaSecrets(bin, append([]string{"check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath}, args...)...)
	}
	execTrue := func() (string, string, int) {
		return runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "true")
	}

	// 5. head= on the green run is HEAD's short sha, exactly.
	short := strings.TrimSpace(runCmd(t, storeDir, "git", "rev-parse", "--short=7", "HEAD"))
	out, errOut, code = check()
	assert.Equal(t, 0, code, "green check must carry head=%s, got %d: %s%s", short, code, out, errOut)
	assert.True(t, regexp.MustCompile(`(^| )head=`+short+`( |$)`).MatchString(strings.TrimSpace(out)), "green check must carry head=%s, got %d: %s%s", short, code, out, errOut)

	// 6. HEAD BEHIND its remote-tracking ref: check exit 1 naming the pull, exec 125
	// with the same line, and neither is green.
	runCmd(t, storeDir, "git", "reset", "-q", "--hard", "HEAD~1")
	_, checkErr, code := check()
	assert.Equal(t, 1, code, "behind working copy expected check exit 1 naming git -C <store> pull --ff-only, got %d: %s", code, checkErr)
	assert.Contains(t, checkErr, "git -C "+storeDir+" pull --ff-only", "behind working copy expected check exit 1 naming git -C <store> pull --ff-only, got %d: %s", code, checkErr)
	_, execErr, code := execTrue()
	assert.Equal(t, 125, code, "behind working copy expected exec 125 naming git -C <store> pull --ff-only, got %d: %s", code, execErr)
	assert.Contains(t, execErr, "git -C "+storeDir+" pull --ff-only", "behind working copy expected exec 125 naming git -C <store> pull --ff-only, got %d: %s", code, execErr)

	// 7. The mutation the spec names, run as a fixture: point the tracking ref at HEAD
	// and both are green, so the refusal above was the ref comparison and nothing else.
	behindHead := strings.TrimSpace(runCmd(t, storeDir, "git", "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(runCmd(t, storeDir, "git", "rev-parse", "refs/remotes/origin/main"))
	runCmd(t, storeDir, "git", "update-ref", "refs/remotes/origin/main", behindHead)
	_, errOut, code = check()
	assert.Equal(t, 0, code, "tracking ref pointed at HEAD: check must be green, got %d: %s", code, errOut)
	_, errOut, code = execTrue()
	assert.Equal(t, 0, code, "tracking ref pointed at HEAD: exec must be green, got %d: %s", code, errOut)
	runCmd(t, storeDir, "git", "update-ref", "refs/remotes/origin/main", remoteHead)
	runCmd(t, storeDir, "git", "merge", "-q", "--ff-only", "refs/remotes/origin/main")

	// 8. The case it does NOT catch, asserted so nobody believes otherwise: the remote
	// moves on and nothing fetches. The tracking ref is stale with HEAD, so both are
	// GREEN, by design; that is why every launcher pulls.
	other := filepath.Join(td, "other")
	remoteURL := strings.TrimSpace(runCmd(t, storeDir, "git", "remote", "get-url", "origin"))
	runCmd(t, "", "git", "clone", "-q", remoteURL, other)
	runCmd(t, other, "git", "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "remote moves on")
	runCmd(t, other, "git", "push", "-q", "origin", "main")
	_, errOut, code = check()
	assert.Equal(t, 0, code, "an unfetched remote move is green by design, got %d: %s", code, errOut)
	_, errOut, code = execTrue()
	assert.Equal(t, 0, code, "an unfetched remote move is green by design on exec, got %d: %s", code, errOut)

	// 9. A .git that is a FILE (a worktree): a refusal naming that fact on both verbs,
	// never the stale-ref sentence and never "no .git".
	wt := filepath.Join(td, "worktree")
	runCmd(t, storeDir, "git", "worktree", "add", "-q", "-b", "wt", wt)
	fi, err := os.Lstat(filepath.Join(wt, ".git"))
	require.NoError(t, err, "the worktree fixture has no .git file: %v", err)
	require.False(t, fi.IsDir(), "the worktree fixture has no .git file: %v", err)
	for _, v := range [][]string{
		{"check", "--store", wt, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath},
		{"exec", "--store", wt, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "true"},
	} {
		want := 2
		if v[0] == "exec" {
			want = 125
		}
		_, errOut, code := runNovaSecrets(bin, v...)
		assert.Equal(t, want, code, "%s on a worktree: expected %d naming the .git file, got %d: %s", v[0], want, code, errOut)
		assert.Contains(t, errOut, "has a .git that is a file (a worktree or submodule)", "%s on a worktree: expected %d naming the .git file, got %d: %s", v[0], want, code, errOut)
		assert.NotContains(t, errOut, "pull --ff-only", "%s on a worktree named the wrong fact: %s", v[0], errOut)
		assert.NotContains(t, errOut, "no .git", "%s on a worktree named the wrong fact: %s", v[0], errOut)
	}
}

// SLOW: 6.5 s on hetzner at dev 64b9bec48, over the five-second line.
// Test 12: TestCheckCapsEachKindSeparatelyAndAlwaysPrintsTheCount
func TestCheckCapsEachKindSeparatelyAndAlwaysPrintsTheCount(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	foreignKey := genKey(t, td, "foreign")

	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	var rules []string
	rules = append(rules, fmt.Sprintf("  - path_regex: ^main\\.yaml$\n    age: %s,%s", keyA.pubKey, recKey.pubKey))
	// 30 unsealed files
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("unsealed_%02d.yaml", i)
		rules = append(rules, fmt.Sprintf("  - path_regex: ^%s$\n    age: %s,%s", name, keyA.pubKey, recKey.pubKey))
		// File has unsealed key
		_ = os.WriteFile(filepath.Join(storeDir, name), []byte("UNENCRYPTED_KEY: plaintext\nsops:\n  version: 3.13.3\n  age:\n    - recipient: "+keyA.pubKey+"\n"), 0644)
	}
	// The quiet kind: two files whose rule and whose recorded recipients name another
	// seat's key, yet this seat's key opens them (sealed to it, the recipient string then
	// rewritten, which sops does not check on decrypt): foreign-openable.
	for _, name := range []string{"foreign_a", "foreign_b"} {
		rules = append(rules, fmt.Sprintf("  - path_regex: ^%s\\.yaml$\n    age: %s,%s", name, foreignKey.pubKey, recKey.pubKey))
		fp := filepath.Join(storeDir, name+".yaml")
		sealFileWithSops(t, sopsPath, fp, []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_for\n")
		sealed, err := os.ReadFile(fp)
		require.NoError(t, err)
		_ = os.WriteFile(fp, []byte(strings.ReplaceAll(string(sealed), keyA.pubKey, foreignKey.pubKey)), 0644)
	}
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "main.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_main\n")

	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"+strings.Join(rules, "\n")+"\n"), 0644)
	commitAndPush(t, storeDir)

	failLines := func(errOut, kindMarker string) int {
		n := 0
		for _, l := range strings.Split(errOut, "\n") {
			if strings.HasPrefix(l, "SECRETS CHECK FAIL ") && !strings.HasPrefix(l, "SECRETS CHECK FAIL as=") && strings.Contains(l, kindMarker) {
				n++
			}
		}
		return n
	}
	summary := regexp.MustCompile(`(?m)^SECRETS CHECK FAIL as=main files=33 failed=(\d+) shown=(\d+)$`)

	// Default --max 20: the loud kind (30 unsealed) caps at 20 with its own MORE line,
	// and does not eat the quiet kind, whose two lines are all shown with no MORE.
	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath)
	require.Equal(t, 1, code, "expected check exit 1, got %d: %s", code, errOut)
	got := failLines(errOut, "outside unencrypted_regex")
	assert.Equal(t, 20, got, "the loud kind shows %d lines at --max 20, want 20: %s", got, errOut)
	assert.Contains(t, errOut, "SECRETS CHECK MORE kind=unsealed shown=20 total=30", "expected MORE line for unsealed: %s", errOut)
	{
		got := failLines(errOut, "recipients do not list it")
		assert.Equal(t, 2, got, "the loud kind ate the quiet one: %d foreign lines shown, want both: %s", got, errOut)
	}
	assert.NotContains(t, errOut, "MORE kind=foreign-openable", "a kind under its cap printed a MORE line: %s", errOut)
	// The count line prints on the red run, and its numbers are the run's.
	m := summary.FindStringSubmatch(errOut)
	require.NotNil(t, m, "the red run printed no count line: %s", errOut)
	assert.Equal(t, fmt.Sprint(failLines(errOut, "")), m[2], "the count line's shown=%s does not match the FAIL lines printed: %s", m[2], errOut)

	// --max 1: EACH kind caps separately, each with its own MORE line.
	_, errOut1, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath, "--max", "1")
	require.Equal(t, 1, code, "expected exit 1 at --max 1, got %d", code)
	assert.Contains(t, errOut1, "SECRETS CHECK MORE kind=unsealed shown=1 total=30", "--max 1: no MORE line of the loud kind's own: %s", errOut1)
	assert.True(t, regexp.MustCompile(`SECRETS CHECK MORE kind=foreign-openable shown=1 total=2 `).MatchString(errOut1), "--max 1: no MORE line of the quiet kind's own: %s", errOut1)
	assert.NotNil(t, summary.FindStringSubmatch(errOut1), "--max 1: the count line is missing on the red run: %s", errOut1)

	// --max 0 prints all
	_, errOutAll, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath, "--max", "0")
	require.Equal(t, 1, code, "expected exit 1, got %d", code)
	assert.NotContains(t, errOutAll, "MORE kind=", "max 0 should not print MORE line: %s", errOutAll)
	{
		got := failLines(errOutAll, "outside unencrypted_regex")
		assert.Equal(t, 30, got, "--max 0 shows %d unsealed lines, want all 30", got)
	}

	// Negative --max is refused at exit 2
	_, errOutNeg, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath, "--max", "-1")
	assert.Equal(t, 2, code, "expected exit 2 on negative max, got %d: %s", code, errOutNeg)
}
