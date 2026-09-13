package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test 1: TestASeatOpensTheFilesItsRulesNameAndNoOther
func TestASeatOpensTheFilesItsRulesNameAndNoOther(t *testing.T) {
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
}

// Test 3: TestExecReplacesItselfAndPassesTheStatusThrough
func TestExecReplacesItselfAndPassesTheStatusThrough(t *testing.T) {
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
}

// Test 4: TestNoVerbPrintsAValue
func TestNoVerbPrintsAValue(t *testing.T) {
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

	verbs := [][]string{
		{"names", "--store", storeDir, "--as", "rowan"},
		{"check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath},
		{"exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "true"},
	}

	for _, v := range verbs {
		out, errOut, _ := runNovaSecrets(bin, v...)
		if strings.Contains(out, secretVal) {
			t.Errorf("verb %v leaked secret in stdout: %s", v, out)
		}
		if strings.Contains(errOut, secretVal) {
			t.Errorf("verb %v leaked secret in stderr: %s", v, errOut)
		}
	}
}

// Test 5: TestGetIsRefusedBeforeAnythingIsRead
func TestGetIsRefusedBeforeAnythingIsRead(t *testing.T) {
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
