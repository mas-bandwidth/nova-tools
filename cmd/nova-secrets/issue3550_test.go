package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nova-tools#3550: check and exec refuse a store whose branch has no upstream
// tracking ref (SPEC-SECRETS invariant 8), and neither their help nor the
// refusal said so. A clean local store got through names, gate, place, seal
// --no-pr and seat add, then the two consumption verbs refused at a
// prerequisite no installed text disclosed.

// TestCheckAndExecHelpNameTheUpstreamPrerequisite: each verb's own help says
// the store must be on a named branch with an upstream tracking ref, says why,
// and names the remedy for a local/offline store.
func TestCheckAndExecHelpNameTheUpstreamPrerequisite(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	for _, args := range [][]string{
		{"check", "--help"},
		{"check", "-h"},
		{"check", "help"},
		{"exec", "--help"},
		{"exec", "-h"},
		{"exec", "help"},
	} {
		out, errOut, code := runNovaSecrets(bin, args...)
		if code != 0 {
			t.Errorf("%v: exit %d, want 0; stderr=%q", args, code, errOut)
			continue
		}
		for _, want := range []string{
			"nova-secrets " + args[0] + " --store <dir>",
			"named branch",
			"upstream tracking ref",
			"branch.<name>.remote",
			"why:",
			"a value a rotation replaced",
			"a local edit nobody reviewed",
			"--set-upstream-to",
			"local/offline store",
			"git init --bare",
			"push -u origin",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%v help lacks %q:\n%s", args, want, out)
			}
		}
	}
	// The root help's --store line points at the prerequisite too.
	out, _, code := runNovaSecrets(bin, "help")
	if code != 0 || !strings.Contains(out, "upstream tracking ref") {
		t.Errorf("root help --store line does not name the upstream tracking ref (exit %d):\n%s", code, out)
	}
}

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
	if code != 2 || !strings.HasPrefix(errOut, "SECRETS REFUSED: ") || strings.Count(errOut, "\n") != 1 {
		t.Fatalf("check on a no-upstream store: want one SECRETS REFUSED line and exit 2, got %d: %q", code, errOut)
	}
	for _, w := range wants {
		if !strings.Contains(errOut, w) {
			t.Errorf("check refusal lacks %q:\n%s", w, errOut)
		}
	}
	_, errOut, code = runNovaSecrets(bin, execArgs...)
	if code != 125 || !strings.HasPrefix(errOut, "SECRETS EXEC FAIL ") || strings.Count(errOut, "\n") != 1 {
		t.Fatalf("exec on a no-upstream store: want one SECRETS EXEC FAIL line and exit 125, got %d: %q", code, errOut)
	}
	for _, w := range wants {
		if !strings.Contains(errOut, w) {
			t.Errorf("exec refusal lacks %q:\n%s", w, errOut)
		}
	}

	// Follow the printed offline remedy, <dir> a bare repo in this test's temp dir.
	bare := filepath.Join(td, "store.git")
	runCmd(t, "", "git", "init", "-q", "--bare", bare)
	runCmd(t, storeDir, "git", "remote", "add", "origin", bare)
	runCmd(t, storeDir, "git", "push", "-q", "-u", "origin", "main")

	out, errOut, code := runNovaSecrets(bin, checkArgs...)
	if code != 0 || !strings.Contains(out, "head=") {
		t.Fatalf("check after the printed remedy: want exit 0 with head=, got %d: out=%q err=%q", code, out, errOut)
	}
	_, errOut, code = runNovaSecrets(bin, execArgs...)
	if code != 0 {
		t.Fatalf("exec after the printed remedy: want exit 0, got %d: %q", code, errOut)
	}
}
