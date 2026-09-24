package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// Test 1: TestASeatOpensTheFilesItsRulesNameAndNoOther
func TestASeatOpensTheFilesItsRulesNameAndNoOther(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	// Keypairs: A_admin, A_keeper, B, C, R
	aAdmin := genKey(t, td, "a_admin")
	aKeeper := genKey(t, td, "a_keeper")
	bKey := genKey(t, td, "b")
	cKey := genKey(t, td, "c")
	rKey := genKey(t, td, "recovery")

	// recovery.pub
	if err := os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(rKey.pubKey+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

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

	if err := os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Seal 5 files
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a.yaml"), []string{aAdmin.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_admin\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a-keeper.yaml"), []string{aKeeper.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_keeper\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "b.yaml"), []string{bKey.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_b\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "swarm-p.yaml"), []string{bKey.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_p\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "c.yaml"), []string{cKey.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_c\n")

	commitAndPush(t, storeDir)

	// 1. With A_admin: exec --as a succeeds, exec --as a-keeper exits 125, check is mine=1 foreign=4
	out, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "HELLO")
	if code != 0 {
		t.Fatalf("A_admin exec a failed: code %d, err: %s", code, errOut)
	}
	if !strings.Contains(out, "HELLO") {
		t.Errorf("expected HELLO in stdout: %s", out)
	}

	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a-keeper", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "HELLO")
	if code != 125 {
		t.Fatalf("expected code 125 on wrong key, got %d, err: %s", code, errOut)
	}

	// sops -d a-keeper.yaml exits 128 with A_admin key
	cmd := exec.Command(sopsPath, "-d", filepath.Join(storeDir, "a-keeper.yaml"))
	cmd.Env = []string{"SOPS_AGE_KEY_FILE=" + aAdmin.privPath, "PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected sops -d to fail with wrong key")
	} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 128 {
		t.Logf("sops exit code: %d", exitErr.ExitCode())
	}

	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("check failed for A_admin: code %d, err: %s", code, errOut)
	}
	if !strings.Contains(out, "mine=1") || !strings.Contains(out, "foreign=4") {
		t.Errorf("expected mine=1 foreign=4 in check output: %s", out)
	}

	// 2. With A_keeper: mirror
	out, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a-keeper", "--key", aKeeper.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "HELLO_KEEPER")
	if code != 0 || !strings.Contains(out, "HELLO_KEEPER") {
		t.Fatalf("A_keeper exec failed: code %d, out: %s, err: %s", code, out, errOut)
	}
	_, _, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aKeeper.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "NO")
	if code != 125 {
		t.Fatalf("expected code 125, got %d", code)
	}

	// 3. With B: mine=2 foreign=3
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "b", "--key", bKey.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("check failed for B: code %d, err: %s", code, errOut)
	}
	if !strings.Contains(out, "mine=2") || !strings.Contains(out, "foreign=3") {
		t.Errorf("expected mine=2 foreign=3: %s", out)
	}

	// 4. With R: opens all five (mine=5 foreign=0)
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", rKey.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("check failed for R: code %d, err: %s", code, errOut)
	}
	if !strings.Contains(out, "mine=5") || !strings.Contains(out, "foreign=0") {
		t.Errorf("expected mine=5 foreign=0: %s", out)
	}

	// Mutations:
	// a) file's block listing keeper key while rule does not -> invariant 2 with updatekeys line
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a.yaml"), []string{aAdmin.pubKey, aKeeper.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_drift\n")
	commitAndPush(t, storeDir)
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	if code != 1 {
		t.Fatalf("expected check exit 1 on recipient drift, got %d", code)
	}
	if !strings.Contains(errOut, "recipients differ from .sops.yaml; run: sops updatekeys a.yaml") {
		t.Errorf("expected updatekeys line in errOut: %s", errOut)
	}
	// Revert a.yaml
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "a.yaml"), []string{aAdmin.pubKey, rKey.pubKey}, "GH_TOKEN: ghp_admin\n")
	commitAndPush(t, storeDir)

	// b) Add A_keeper to a.yaml's rule without updatekeys -> invariant 2
	brokenRuleCfg := strings.Replace(sopsConfig, fmt.Sprintf("age: %s,%s", aAdmin.pubKey, rKey.pubKey), fmt.Sprintf("age: %s,%s,%s", aAdmin.pubKey, aKeeper.pubKey, rKey.pubKey), 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(brokenRuleCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "recipients differ from .sops.yaml; run: sops updatekeys a.yaml") {
		t.Errorf("expected invariant 2 error: %s", errOut)
	}
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
	if code != 1 || !strings.Contains(errOut, "does not contain declared recovery key") {
		t.Errorf("expected invariant 1 recovery mismatch error: %s", errOut)
	}
	// Also asserted on exec -> 125
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "NO")
	if code != 125 {
		t.Errorf("expected exec 125 on declared recovery mismatch, got %d: %s", code, errOut)
	}

	// Restore config
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0644)
	commitAndPush(t, storeDir)

	// d) recovery.pub 5 bad states: deleted, empty, mode 000, two lines, not-a-key
	recFile := filepath.Join(storeDir, "recovery.pub")
	testBadRecovery := func(state string, setup func(), cleanup func()) {
		setup()
		_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
		if code != 1 {
			t.Errorf("state %s: check expected 1, got %d (%s)", state, code, errOut)
		}
		_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "echo", "NO")
		if code != 125 {
			t.Errorf("state %s: exec expected 125, got %d (%s)", state, code, errOut)
		}
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
	if code != 1 || !strings.Contains(errOut, "not anchored at both ends") {
		t.Errorf("expected unanchored regex failure: %s", errOut)
	}

	// f) Recipient truncated to age1
	truncCfg := strings.Replace(sopsConfig, aAdmin.pubKey, "age1", 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(truncCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "not a valid age public key") {
		t.Errorf("expected truncated age key failure: %s", errOut)
	}

	// g) R listed twice in one rule
	dupCfg := strings.Replace(sopsConfig, aAdmin.pubKey, rKey.pubKey, 1)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(dupCfg), 0644)
	commitAndPush(t, storeDir)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "a", "--key", aAdmin.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "duplicate recipient") {
		t.Errorf("expected duplicate recipient failure: %s", errOut)
	}
}

// Test 2: TestExecSetsExactlyTheKeysInTheFile
func TestExecSetsExactlyTheKeysInTheFile(t *testing.T) {
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

	plainSecrets := "GH_TOKEN: ghp_testtoken123\nDEEPSEEK_API_KEY: ds_456\nSPACE_USER: glenn\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, plainSecrets)
	commitAndPush(t, storeDir)

	// Test script that dumps environment
	// 1. --only GH_TOKEN puts exactly one key in child and only=1 on stderr line
	cmd := exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "env")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("exec failed: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "only=1") {
		t.Errorf("expected only=1 in stderr: %s", stderr.String())
	}
	envOutput := stdout.String()
	if !strings.Contains(envOutput, "GH_TOKEN=ghp_testtoken123") {
		t.Errorf("missing GH_TOKEN in child env: %s", envOutput)
	}
	if strings.Contains(envOutput, "DEEPSEEK_API_KEY") || strings.Contains(envOutput, "SPACE_USER") {
		t.Errorf("child env leaked keys outside --only: %s", envOutput)
	}
	if strings.Contains(envOutput, "NOVA_SECRETS_") {
		t.Errorf("child env has invented NOVA_SECRETS_ marker: %s", envOutput)
	}

	// 2. --only all puts every key and says only=all
	cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "env")
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("exec failed: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "only=all") {
		t.Errorf("expected only=all in stderr: %s", stderr.String())
	}
	envOutput = stdout.String()
	if !strings.Contains(envOutput, "GH_TOKEN=ghp_testtoken123") ||
		!strings.Contains(envOutput, "DEEPSEEK_API_KEY=ds_456") ||
		!strings.Contains(envOutput, "SPACE_USER=glenn") {
		t.Errorf("missing expected keys in only=all: %s", envOutput)
	}

	// 3. --require for an excluded key is 125
	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--require", "DEEPSEEK_API_KEY", "--", "echo", "NO")
	if code != 125 || !strings.Contains(errOut, "excluded by --only") {
		t.Errorf("expected 125 for excluded required key, got %d: %s", code, errOut)
	}

	// 4. --only naming a key the file does not hold is 125 naming that key
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "NONEXISTENT", "--", "echo", "NO")
	if code != 125 || !strings.Contains(errOut, "NONEXISTENT") {
		t.Errorf("expected 125 naming nonexistent key, got %d: %s", code, errOut)
	}

	// 5. No --only at all is 125 naming the flag
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--", "echo", "NO")
	if code != 125 || !strings.Contains(errOut, "--only") {
		t.Errorf("expected 125 naming missing --only, got %d: %s", code, errOut)
	}

	// 6. Every variable and every file in sops' identity lookup, planted and defeated.
	execIdentityLookupIsDefeated(t, bin, sopsPath)
}

// runWithEnv runs the tool with exactly env and returns both streams, the exit code and
// the pid the tool ran as.
func runWithEnv(bin string, env []string, args ...string) (stdout, stderr string, code, pid int) {
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Start()
	if err != nil {
		return "", "nova-secrets did not start: " + err.Error(), 1, 0
	}
	pid = cmd.Process.Pid
	err = cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code, pid
}

// sshKey makes an unencrypted ssh identity and returns its public half without comment.
func sshKey(t *testing.T, path, kind string) string {
	t.Helper()
	args := []string{"-q", "-t", kind, "-N", "", "-C", "", "-f", path}
	if kind == "rsa" {
		args = append(args, "-b", "2048")
	}
	runCmd(t, "", "ssh-keygen", args...)
	pub, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(pub))
	if len(f) < 2 {
		t.Fatalf("unreadable ssh public key %s", pub)
	}
	return f[0] + " " + f[1]
}

// execIdentityLookupIsDefeated is test 2's isolation half. Each foreign identity sops
// would find on its own is planted in the caller's environment or under the caller's HOME
// and XDG_CONFIG_HOME, and one store file is sealed to each of them and never to --key.
// Every exec of those files is refused as a failed decrypt; with the isolation removed
// sops finds the planted identity and the decrypt succeeds, which is the red this pins.
// The seat's own file still opens with --key alone, the _CMD variable is a command that
// would write a sentinel and is asserted never run, and no variable of the lookup reaches
// the command.
func execIdentityLookupIsDefeated(t *testing.T, bin, sopsPath string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not on PATH: the ssh identity files of sops' lookup cannot be planted")
	}
	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)
	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	home := filepath.Join(td, "callerhome")
	xdg := filepath.Join(td, "callerxdg")
	plant := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ageIdentity := func(name string) (priv []byte, pub string) {
		k := genKey(t, td, name)
		b, err := os.ReadFile(k.privPath)
		if err != nil {
			t.Fatal(err)
		}
		return b, k.pubKey
	}

	sealedTo := map[string]string{} // seat file -> the only non-recovery recipient
	xdgPriv, xdgPub := ageIdentity("xdg")
	plant(filepath.Join(xdg, "sops", "age", "keys.txt"), xdgPriv)
	sealedTo["idxdg"] = xdgPub
	libPriv, libPub := ageIdentity("lib")
	plant(filepath.Join(home, "Library", "Application Support", "sops", "age", "keys.txt"), libPriv)
	plant(filepath.Join(home, ".config", "sops", "age", "keys.txt"), libPriv)
	sealedTo["idlib"] = libPub
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0700)
	sealedTo["idsshed"] = sshKey(t, filepath.Join(home, ".ssh", "id_ed25519"), "ed25519")
	sealedTo["idsshrsa"] = sshKey(t, filepath.Join(home, ".ssh", "id_rsa"), "rsa")
	envKeyPriv, envKeyPub := ageIdentity("envkey")
	sealedTo["idenvkey"] = envKeyPub
	envFilePriv, envFilePub := ageIdentity("envfile")
	envFilePath := filepath.Join(td, "planted", "envfile.key")
	plant(envFilePath, envFilePriv)
	sealedTo["idenvfile"] = envFilePub
	envCmdPriv, envCmdPub := ageIdentity("envcmd")
	envCmdKey := filepath.Join(td, "planted", "envcmd.key")
	plant(envCmdKey, envCmdPriv)
	sentinel := filepath.Join(td, "SOPS_AGE_KEY_CMD-ran")
	envCmd := filepath.Join(td, "planted", "keycmd.sh")
	if err := os.WriteFile(envCmd, []byte("#!/bin/sh\n: > '"+sentinel+"'\ncat '"+envCmdKey+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	sealedTo["idenvcmd"] = envCmdPub
	sealedTo["idenvssh"] = sshKey(t, filepath.Join(td, "planted", "envssh"), "ed25519")

	var rules []string
	rules = append(rules, fmt.Sprintf("  - path_regex: ^rowan\\.yaml$\n    age: %s,%s", keyA.pubKey, recKey.pubKey))
	for seat, recipient := range sealedTo {
		// The rule names a seat key of its own (exec checks the rule's shape, never
		// whose key opens the file); the file is sealed to the planted identity.
		ruleKey := genKey(t, td, "rule-"+seat)
		rules = append(rules, fmt.Sprintf("  - path_regex: ^%s\\.yaml$\n    age: %s,%s", seat, ruleKey.pubKey, recKey.pubKey))
		sealFileWithSops(t, sopsPath, filepath.Join(storeDir, seat+".yaml"), []string{recipient, recKey.pubKey}, "GH_TOKEN: ghp_foreign_"+seat+"\n")
	}
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"+strings.Join(rules, "\n")+"\n"), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_mine\n")
	commitAndPush(t, storeDir)

	lookupVars := map[string]string{
		"SOPS_AGE_KEY":                  strings.TrimSpace(string(envKeyPriv)),
		"SOPS_AGE_KEY_FILE":             envFilePath,
		"SOPS_AGE_KEY_CMD":              envCmd,
		"SOPS_AGE_SSH_PRIVATE_KEY_FILE": filepath.Join(td, "planted", "envssh"),
		"SOPS_KEYSERVICE":               "unix://" + filepath.Join(td, "no-keyservice.sock"),
	}
	baseEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		baseEnv = append(baseEnv, "TMPDIR="+tmp)
	}
	for k, v := range lookupVars {
		baseEnv = append(baseEnv, k+"="+v)
	}
	// Two callers: one with XDG_CONFIG_HOME set, and one without it, whose sops would
	// fall back to the platform config dir under HOME.
	callers := map[string][]string{
		"xdg-set":   append(append([]string{}, baseEnv...), "XDG_CONFIG_HOME="+xdg),
		"xdg-unset": baseEnv,
	}
	for callerName, env := range callers {
		for seat := range sealedTo {
			out, errOut, code, _ := runWithEnv(bin, env, "exec", "--store", storeDir, "--as", seat, "--key", keyA.privPath,
				"--sops", sopsPath, "--only", "all", "--", "env")
			if code != 125 || !strings.Contains(errOut, "sops failed") {
				t.Errorf("caller %s: %s.yaml is sealed only to a planted identity and must be refused as a failed decrypt (125), got %d: %s", callerName, seat, code, errOut)
			}
			if strings.Contains(out+errOut, "ghp_foreign_") {
				t.Errorf("caller %s: a planted identity opened %s.yaml: %s%s", callerName, seat, out, errOut)
			}
		}

		out, errOut, code, _ := runWithEnv(bin, env, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath,
			"--sops", sopsPath, "--only", "all", "--", "env")
		if code != 0 || !strings.Contains(out, "GH_TOKEN=ghp_mine\n") {
			t.Errorf("caller %s: --key alone must open the seat's own file with every planted identity present, got %d: %s", callerName, code, errOut)
		}
		for name := range lookupVars {
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, name+"=") {
					t.Errorf("caller %s: %s reached the command: %s", callerName, name, line)
				}
			}
		}
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Errorf("SOPS_AGE_KEY_CMD was run: %s exists", sentinel)
	}
}

// Test 3: TestExecReplacesItselfAndPassesTheStatusThrough
func TestExecReplacesItselfAndPassesTheStatusThrough(t *testing.T) {
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
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_123\n")
	commitAndPush(t, storeDir)

	// Command exiting 7 makes nova-secrets exit 7
	cmd := exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "sh", "-c", "exit 7")
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 7 {
		t.Errorf("expected exit code 7, got %v", err)
	}

	// Command exiting 1 and 2
	for _, expectedCode := range []int{1, 2} {
		var stderr bytes.Buffer
		cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
			"--only", "GH_TOKEN", "--", "sh", "-c", fmt.Sprintf("exit %d", expectedCode))
		cmd.Stderr = &stderr
		err = cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != expectedCode {
			t.Errorf("expected exit code %d, got %v", expectedCode, err)
		}
		if strings.Contains(stderr.String(), "SECRETS EXEC FAIL") {
			t.Errorf("unexpected SECRETS EXEC FAIL on command exit %d: %s", expectedCode, stderr.String())
		}
	}

	// Command exiting 125 passed through, told from refusal by absence of our line
	var stderr bytes.Buffer
	cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "sh", "-c", "exit 125")
	cmd.Stderr = &stderr
	err = cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 125 {
		t.Errorf("expected exit code 125, got %v", err)
	}
	if strings.Contains(stderr.String(), "SECRETS EXEC FAIL") {
		t.Errorf("found SECRETS EXEC FAIL when command exited 125: %s", stderr.String())
	}

	if runtime.GOOS == "windows" {
		t.Log("windows has no execve: the pid, RLIMIT_CORE and signal clauses do not apply there")
		return
	}
	env := os.Environ()

	// Same pid before and after: the command IS the process this test started.
	out, errOut, code, pid := runWithEnv(bin, env, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath,
		"--sops", sopsPath, "--only", "GH_TOKEN", "--", "sh", "-c", "echo $$")
	if code != 0 || strings.TrimSpace(out) != fmt.Sprint(pid) {
		t.Errorf("exec must replace itself: started pid %d, the command reported %q (exit %d): %s", pid, strings.TrimSpace(out), code, errOut)
	}

	// A signal-killed command reproduces the shell's status: the process itself dies of
	// the signal, because there is no wrapper left to turn it into an exit code.
	stderr.Reset()
	cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "sh", "-c", "kill -TERM $$")
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("a signal-killed command must not look like success, got %v", err)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("the command killed by SIGTERM must leave the tool killed by SIGTERM (shell status 143), got %v", err)
	}
	if strings.Contains(stderr.String(), "SECRETS EXEC FAIL") {
		t.Errorf("a signal-killed command printed our failure line: %s", stderr.String())
	}

	// The command's RLIMIT_CORE is 0 even when the caller's soft limit is not: a core
	// dump would write every value to disk.
	wrapped := []string{"-c", `ulimit -c 1024 || exit 97; exec "$0" "$@"`, bin, "exec", "--store", storeDir, "--as", "rowan",
		"--key", keyA.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "sh", "-c", "ulimit -c"}
	var wOut, wErr bytes.Buffer
	cmd = exec.Command("sh", wrapped...)
	cmd.Stdout = &wOut
	cmd.Stderr = &wErr
	err = cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 97 {
		t.Skip("the caller's hard core limit is 0, so a nonzero soft limit cannot be planted; RLIMIT_CORE clause not provable here")
	}
	if err != nil || strings.TrimSpace(wOut.String()) != "0" {
		t.Errorf("the command's RLIMIT_CORE must be 0 with the caller's at 1024, got %q (%v): %s", strings.TrimSpace(wOut.String()), err, wErr.String())
	}
}

// Test 4: TestNoVerbPrintsAValue
func TestNoVerbPrintsAValue(t *testing.T) {
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

	secretVal := "distinctive_secret_val_40_bytes_long_here"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: "+secretVal+"\n")
	commitAndPush(t, storeDir)

	// A key file this verb must refuse (mode 0644), a key file that already exists for
	// keygen to refuse, and an empty receipts directory for placed.
	looseKey := filepath.Join(td, "loose.key")
	keyBytes, err := os.ReadFile(keyA.privPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(looseKey, keyBytes, 0644)
	_ = os.Chmod(looseKey, 0644)
	receipts := filepath.Join(td, "receipts")
	_ = os.MkdirAll(receipts, 0700)
	ageKeygen := findAgeKeygen(t)

	store := []string{"--store", storeDir, "--as", "rowan"}
	withKey := append(append([]string{}, store...), "--key", keyA.privPath, "--sops", sopsPath)
	cat := func(parts ...[]string) []string {
		var all []string
		for _, p := range parts {
			all = append(all, p...)
		}
		return all
	}
	verbs := [][]string{
		// green, every mode
		cat([]string{"names"}, store),
		cat([]string{"names"}, store, []string{"--max", "0"}),
		cat([]string{"names"}, store, []string{"--max", "1"}),
		cat([]string{"check"}, withKey),
		cat([]string{"check"}, withKey, []string{"--max", "0"}),
		cat([]string{"exec"}, withKey, []string{"--only", "GH_TOKEN", "--require", "GH_TOKEN", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "all", "--", "true"}),
		{"gate", "--store", storeDir, "--base", "HEAD", "--head", "HEAD"},
		{"placed", "--machine", "mini", "--receipts", receipts},
		{"version"},
		{"help"},
		// every refusal
		cat([]string{"exec"}, withKey, []string{"--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "NOPE", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "all", "--require", "NOPE", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "GH_TOKEN", "--", "no-such-command-anywhere"}),
		cat([]string{"exec"}, store, []string{"--key", looseKey, "--sops", sopsPath, "--only", "all", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "all", "true"}),
		cat([]string{"check"}, store, []string{"--key", looseKey, "--sops", sopsPath}),
		cat([]string{"check"}, withKey, []string{"--max", "-1"}),
		cat([]string{"names"}, store, []string{"--bogus"}),
		{"keygen", "--as", "rowan", "--key", keyA.privPath, "--age-keygen", ageKeygen},
		cat([]string{"place"}, withKey, []string{"--secret", "GH_TOKEN"}),
		cat([]string{"seat", "add"}, withKey),
		{"get", "--store", storeDir, "--as", "rowan", "GH_TOKEN"},
		{"print", "GH_TOKEN"}, {"show", "GH_TOKEN"}, {"cat", "GH_TOKEN"}, {"put", "GH_TOKEN"},
		{"no-such-verb", "GH_TOKEN"},
		{},
	}

	// The value's length is a fact about the value: a mutation printing len(value)
	// must turn this red as surely as printing the value.
	lengthToken := regexp.MustCompile(`(^|[^0-9A-Za-z_])` + fmt.Sprint(len(secretVal)) + `([^0-9A-Za-z_]|$)`)
	const green = 11 // the first eleven are green runs; every one after is a refusal
	for i, v := range verbs {
		out, errOut, code := runNovaSecrets(bin, v...)
		if (i < green) != (code == 0) {
			t.Errorf("verb %v: exit %d is not the mode this row pins (green=%t): %s%s", v, code, i < green, out, errOut)
		}
		both := strings.ReplaceAll(out+errOut, td, "<td>")
		if strings.Contains(both, secretVal) {
			t.Errorf("verb %v printed the value: %s", v, both)
		}
		if lengthToken.MatchString(both) {
			t.Errorf("verb %v printed the value's length %d: %s", v, len(secretVal), both)
		}
	}
}

// Test 5: TestGetIsRefusedBeforeAnythingIsRead
func TestGetIsRefusedBeforeAnythingIsRead(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	out, errOut, code := runNovaSecrets(bin, "get", "--store", "/nonexistent/store/that/would/panic")
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if out != "" {
		t.Errorf("expected empty stdout, got %s", out)
	}
	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly one line on stderr, got %d: %s", len(lines), errOut)
	}
	if !strings.Contains(errOut, "sops -d") {
		t.Errorf("expected stderr to name 'sops -d': %s", errOut)
	}
}
