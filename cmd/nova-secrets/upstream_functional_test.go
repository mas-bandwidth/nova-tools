//go:build functional

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoUpstreamRefusalNamesTheSafeNextAction: the issue's own transcript, a
// valid local store with no remote at all. Both verbs refuse naming the
// prerequisite and the next action, the offline remedy they print is followed
// literally against a throwaway bare repo in the test's temp dir, and then
// check is green and exec runs the command.
func TestNoUpstreamRefusalNamesTheSafeNextAction(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	runCmd(t, storeDir, "git", "init", "-q", "-b", "main")
	runCmd(t, storeDir, "git", "config", "user.name", "Test")
	runCmd(t, storeDir, "git", "config", "user.email", "test@example.com")

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(fmt.Sprintf("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n", keyA.pubKey, recKey.pubKey)), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "DOGFOOD_TOKEN: synthetic\n")
	runCmd(t, storeDir, "git", "add", "-A")
	runCmd(t, storeDir, "git", "commit", "-q", "-m", "store")

	checkArgs := []string{"check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath}
	execArgs := []string{"exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "DOGFOOD_TOKEN", "--require", "DOGFOOD_TOKEN", "--", "true"}

	wants := []string{
		"branch main has no upstream tracking branch",
		"check and exec compare HEAD with the ref the branch tracks",
		"nova-secrets check --help",
		"git -C " + storeDir + " branch --set-upstream-to=<remote>/main",
		"local/offline store",
		"git init --bare <dir> && git -C " + storeDir + " remote add origin <dir> && git -C " + storeDir + " push -u origin main",
	}
	_, errOut, code := runNovaSecrets(bin, checkArgs...)
	require.Equal(t, 2, code, "check on a no-upstream store: want one SECRETS CHECK REFUSED line and exit 2, got %d: %q", code, errOut)
	require.True(t, strings.HasPrefix(errOut, "SECRETS CHECK REFUSED: "), "check on a no-upstream store: want one SECRETS CHECK REFUSED line and exit 2, got %d: %q", code, errOut)
	require.Equal(t, 1, strings.Count(errOut, "\n"), "check on a no-upstream store: want one SECRETS CHECK REFUSED line and exit 2, got %d: %q", code, errOut)
	for _, w := range wants {
		assert.Contains(t, errOut, w, "check refusal lacks %q:\n%s", w, errOut)
	}
	_, errOut, code = runNovaSecrets(bin, execArgs...)
	require.Equal(t, 125, code, "exec on a no-upstream store: want one SECRETS EXEC REFUSED line and exit 125, got %d: %q", code, errOut)
	require.True(t, strings.HasPrefix(errOut, "SECRETS EXEC REFUSED: "), "exec on a no-upstream store: want one SECRETS EXEC REFUSED line and exit 125, got %d: %q", code, errOut)
	require.Equal(t, 1, strings.Count(errOut, "\n"), "exec on a no-upstream store: want one SECRETS EXEC REFUSED line and exit 125, got %d: %q", code, errOut)
	for _, w := range wants {
		assert.Contains(t, errOut, w, "exec refusal lacks %q:\n%s", w, errOut)
	}

	// Follow the printed offline remedy, <dir> a bare repo in this test's temp dir.
	bare := filepath.Join(td, "store.git")
	runCmd(t, "", "git", "init", "-q", "--bare", bare)
	runCmd(t, storeDir, "git", "remote", "add", "origin", bare)
	runCmd(t, storeDir, "git", "push", "-q", "-u", "origin", "main")

	out, errOut, code := runNovaSecrets(bin, checkArgs...)
	require.Equal(t, 0, code, "check after the printed remedy: want exit 0 with head=, got %d: out=%q err=%q", code, out, errOut)
	require.Contains(t, out, "head=", "check after the printed remedy: want exit 0 with head=, got %d: out=%q err=%q", code, out, errOut)
	_, errOut, code = runNovaSecrets(bin, execArgs...)
	require.Equal(t, 0, code, "exec after the printed remedy: want exit 0, got %d: %q", code, errOut)
}
