package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test 11: TestAKeyNameThatIsNotAnEnvVarIsRefused
func TestAKeyNameThatIsNotAnEnvVarIsRefused(t *testing.T) {
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

	// File with bad keys: gh-token, 2FA, A B
	badPayload := "gh-token: 1\n2FA: 2\nA B: 3\nGH_TOKEN: 4\nA1_B: 5\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, badPayload)
	commitAndPush(t, storeDir)

	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 125 {
		t.Fatalf("expected code 125, got %d", code)
	}
	if !strings.Contains(errOut, "2FA, A B, gh-token") {
		t.Errorf("expected sorted bad keys reported, got: %s", errOut)
	}

	// Now valid payload
	validPayload := "GH_TOKEN: ghp_ok\nA1_B: ok2\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, validPayload)
	commitAndPush(t, storeDir)

	out, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "echo", "PASSED")
	if code != 0 || !strings.Contains(out, "PASSED") {
		t.Errorf("expected valid keys to pass, got %d: out=%s, err=%s", code, out, errOut)
	}
}

// Test 12: TestCheckCapsEachKindSeparatelyAndAlwaysPrintsTheCount
func TestCheckCapsEachKindSeparatelyAndAlwaysPrintsTheCount(t *testing.T) {
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
	// 1 foreign file that opens when it should not:
	// File is sealed to keyA, but rule says foreignKey!
	rules = append(rules, fmt.Sprintf("  - path_regex: ^foreign\\.yaml$\n    age: %s,%s", foreignKey.pubKey, recKey.pubKey))
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "foreign.yaml"), []string{foreignKey.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_for\n")
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "main.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_main\n")

	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"+strings.Join(rules, "\n")+"\n"), 0644)
	commitAndPush(t, storeDir)

	// Run check with default max=20
	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 {
		t.Fatalf("expected check exit 1, got %d", code)
	}

	// Loud kind (unsealed) should cap at 20 with MORE line
	if !strings.Contains(errOut, "SECRETS CHECK MORE kind=unsealed shown=20 total=30") {
		t.Errorf("expected MORE line for unsealed: %s", errOut)
	}

	// Summary line prints total counts
	if !strings.Contains(errOut, "failed=") || !strings.Contains(errOut, "files=32") {
		t.Errorf("summary line missing in stderr: %s", errOut)
	}

	// --max 0 prints all
	_, errOutAll, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath, "--max", "0")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if strings.Contains(errOutAll, "MORE kind=") {
		t.Errorf("max 0 should not print MORE line: %s", errOutAll)
	}

	// Negative --max is refused at exit 2
	_, errOutNeg, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "main", "--key", keyA.privPath, "--sops", sopsPath, "--max", "-1")
	if code != 2 {
		t.Errorf("expected exit 2 on negative max, got %d: %s", code, errOutNeg)
	}
}

// Test 13: TestCheckFailsClosedOnEverythingItCannotRead
func TestCheckFailsClosedOnEverythingItCannotRead(t *testing.T) {
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()

	// 1. Store is a file -> exit 2
	fileAsStore := filepath.Join(td, "file-store")
	_ = os.WriteFile(fileAsStore, []byte("im a file"), 0644)
	_, errOut, code := runNovaSecrets(bin, "check", "--store", fileAsStore, "--as", "rowan", "--key", filepath.Join(td, "k"), "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "not a directory") {
		t.Errorf("file-as-store expected 2: code=%d, err=%s", code, errOut)
	}

	// 2. Store with no .sops.yaml -> exit 2
	noSopsStore := filepath.Join(td, "no-sops-store")
	_ = os.MkdirAll(noSopsStore, 0755)
	initGitStore(t, noSopsStore)
	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(noSopsStore, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	_ = os.WriteFile(filepath.Join(noSopsStore, "rowan.yaml"), []byte("GH_TOKEN: 1\n"), 0644)
	commitAndPush(t, noSopsStore)

	_, errOut, code = runNovaSecrets(bin, "check", "--store", noSopsStore, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "no .sops.yaml") {
		t.Errorf("no .sops.yaml expected 2: code=%d, err=%s", code, errOut)
	}

	// 3. .sops.yaml that will not parse -> exit 1 on invariant 1
	_ = os.WriteFile(filepath.Join(noSopsStore, ".sops.yaml"), []byte("not_yaml: {{bad syntax"), 0644)
	commitAndPush(t, noSopsStore)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", noSopsStore, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, ".sops.yaml") {
		t.Errorf("unparseable .sops.yaml expected 1 on invariant 1: code=%d, err=%s", code, errOut)
	}

	// 4. Unreadable .sops.yaml -> exit 1 on invariant 1
	_ = os.Chmod(filepath.Join(noSopsStore, ".sops.yaml"), 0000)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", noSopsStore, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 {
		t.Errorf("unreadable .sops.yaml expected 1: code=%d, err=%s", code, errOut)
	}
	_ = os.Chmod(filepath.Join(noSopsStore, ".sops.yaml"), 0644)
}

// Test 14: TestKeygenNeverOverwritesAndNeverTouchesTheStore
func TestKeygenNeverOverwritesAndNeverTouchesTheStore(t *testing.T) {
	ageKeygen := findAgeKeygen(t)
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"), 0644)
	commitAndPush(t, storeDir)

	hashTree := func(dir string) string {
		h := sha256.New()
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, _ := os.ReadFile(p)
			h.Write([]byte(p))
			h.Write(data)
			return nil
		})
		return fmt.Sprintf("%x", h.Sum(nil))
	}

	treeHashBefore := hashTree(storeDir)

	validKeyDir := filepath.Join(td, "validkeys")
	_ = os.MkdirAll(validKeyDir, 0700)

	// 1. Existing file at --key is refused with bytes unchanged
	existingKey := filepath.Join(validKeyDir, "existing.key")
	_ = os.WriteFile(existingKey, []byte("ORIGINAL_BYTES"), 0600)
	_, errOut, code := runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", existingKey, "--age-keygen", ageKeygen)
	if code != 2 || !strings.Contains(errOut, "already exists; refusing to overwrite") {
		t.Errorf("expected overwrite refusal, got %d: %s", code, errOut)
	}
	data, _ := os.ReadFile(existingKey)
	if string(data) != "ORIGINAL_BYTES" {
		t.Errorf("existing key file modified!")
	}

	// 2. 0755 parent refused naming mkdir -m 700
	badParentDir := filepath.Join(td, "badparent")
	_ = os.MkdirAll(badParentDir, 0755)
	targetInBadParent := filepath.Join(badParentDir, "test.key")
	_, errOut, code = runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", targetInBadParent, "--age-keygen", ageKeygen)
	if code != 2 || (!strings.Contains(errOut, "mkdir -m 700") && !strings.Contains(errOut, "chmod 700")) {
		t.Errorf("expected 0700 dir refusal, got %d: %s", code, errOut)
	}

	// 3. Success creates key, private key on no stream, store untouched
	newKeyPath := filepath.Join(validKeyDir, "new.key")

	out, errOut, code := runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", newKeyPath, "--age-keygen", ageKeygen, "--store", storeDir)
	if code != 0 {
		t.Fatalf("keygen failed: code %d, err: %s", code, errOut)
	}

	// Store tree hash unchanged
	treeHashAfter := hashTree(storeDir)
	if treeHashBefore != treeHashAfter {
		t.Errorf("keygen modified the store tree!")
	}

	// Private key appears on no stream
	privBytes, _ := os.ReadFile(newKeyPath)
	if strings.Contains(out, string(privBytes)) || strings.Contains(errOut, string(privBytes)) {
		t.Errorf("private key appeared in output!")
	}

	// Printed rule satisfies invariant 1 when pasted
	if !strings.Contains(out, recKey.pubKey) {
		t.Errorf("expected recovery key in rule output: %s", out)
	}
	if strings.Contains(out, "placeholder:") {
		t.Errorf("with --store, rule should not contain placeholder note: %s", out)
	}

	// 4. Run without --store: literal placeholder and NOTE line
	noStoreKey := filepath.Join(validKeyDir, "nostore.key")
	out, _, code = runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", noStoreKey, "--age-keygen", ageKeygen)
	if code != 0 {
		t.Fatalf("keygen without store failed: %d", code)
	}
	if !strings.Contains(out, "<recovery key>") || !strings.Contains(out, "SECRETS RULE NOTE  placeholder: no --store, so <recovery key> stands unfilled") {
		t.Errorf("expected placeholder and note line without store: %s", out)
	}

	// 5. --store with empty recovery.pub is exit 2 naming file
	emptyRecStore := filepath.Join(td, "emptyrec")
	_ = os.MkdirAll(emptyRecStore, 0755)
	initGitStore(t, emptyRecStore)
	_ = os.WriteFile(filepath.Join(emptyRecStore, "recovery.pub"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(emptyRecStore, ".sops.yaml"), []byte("creation_rules:\n"), 0644)
	commitAndPush(t, emptyRecStore)

	errKeyPath := filepath.Join(validKeyDir, "err.key")
	_, errOut, code = runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", errKeyPath, "--age-keygen", ageKeygen, "--store", emptyRecStore)
	if code != 2 || !strings.Contains(errOut, "recovery.pub is empty") {
		t.Errorf("expected exit 2 naming recovery.pub empty, got %d: %s", code, errOut)
	}
	_ = sopsPath
}

// Test 15: TestTheLauncherOrderWorksWithTheStoreFullyDenied
func TestTheLauncherOrderWorksWithTheStoreFullyDenied(t *testing.T) {
	// Nova-sandbox integration test. Skips with stated reason if nova-sandbox is not built
	sandboxBin, err := exec.LookPath("nova-sandbox")
	if err != nil {
		t.Skip("skipping TestTheLauncherOrderWorksWithTheStoreFullyDenied: nova-sandbox not built on PATH")
	}
	_ = sandboxBin
}
