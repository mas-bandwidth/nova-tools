package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test 16: TestNoKeychainAndNoCryptoDependency
func TestNoKeychainAndNoCryptoDependency(t *testing.T) {
	t.Parallel()
	pkgs := []string{"cmd/nova-secrets", "internal/secrets"}
	fset := token.NewFileSet()

	forbiddenImports := []string{
		"filippo.io/age",
		"getsops",
		"security",
		"keychain",
		"github.com/keybase/go-keychain",
	}

	for _, pkg := range pkgs {
		dir := filepath.Join("..", "..", pkg)
		// If running inside cmd/nova-secrets
		if _, err := os.Stat(dir); err != nil {
			dir = filepath.Join(".", pkg)
			if _, err2 := os.Stat(dir); err2 != nil {
				// Try from repo root
				dir = pkg
			}
		}

		pkgsMap, err := parser.ParseDir(fset, dir, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("failed to parse package in %s: %v", dir, err)
		}

		for _, p := range pkgsMap {
			for fileName, f := range p.Files {
				for _, imp := range f.Imports {
					pathVal := strings.Trim(imp.Path.Value, `"`)
					for _, forb := range forbiddenImports {
						if strings.Contains(pathVal, forb) {
							t.Errorf("%s imports %s; forbidden cryptography or keychain dependency", fileName, pathVal)
						}
					}
				}
			}
		}

		// Also check file content for security binary invocations
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, _ := os.ReadFile(path)
			sContent := string(content)
			if strings.Contains(sContent, `"security"`) {
				t.Errorf("%s contains reference to security binary", path)
			}
			return nil
		})
	}
}

// Test 17: TestREADMEFirstRunMatchesWhatTheToolPrints
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	ageKeygen := findAgeKeygen(t)
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "secrets")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"), 0644)
	commitAndPush(t, storeDir)

	configDir := filepath.Join(td, ".config", "nova-secrets")
	_ = os.MkdirAll(configDir, 0700)
	keyPath := filepath.Join(configDir, "rowan.key")

	// 1. keygen
	out, errOut, code := runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", keyPath, "--age-keygen", ageKeygen, "--store", storeDir)
	if code != 0 {
		t.Fatalf("keygen failed: code %d, err: %s", code, errOut)
	}
	if !strings.Contains(out, "SECRETS KEYGEN OK as=rowan") {
		t.Errorf("expected KEYGEN OK line: %s", out)
	}

	// 2. check on seat with no file of its own: exit 2 listing names
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "seat file rowan.yaml is absent") {
		t.Errorf("expected exit 2 when seat file absent: code %d, err: %s", code, errOut)
	}

	// 3. once a foreign file exists: green mine=0
	otherKey := genKey(t, td, "other")
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^other\.yaml$
    age: %s,%s
`, otherKey.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "other.yaml"), []string{otherKey.pubKey, recKey.pubKey}, "GH_TOKEN: 123\n")
	commitAndPush(t, storeDir)

	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "other", "--key", keyPath, "--sops", sopsPath)
	if code != 0 || !strings.Contains(out, "mine=0") {
		t.Errorf("expected green mine=0: code %d, out: %s, err: %s", code, out, errOut)
	}

	// 4. between the two pull requests state: rule merged, file not yet updatekeys-ed -> exit 1 on invariant 2
	sopsCfgBoth := fmt.Sprintf(`creation_rules:
  - path_regex: ^other\.yaml$
    age: %s,%s
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, otherKey.pubKey, recKey.pubKey, otherKey.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfgBoth), 0644)
	// rowan.yaml exists but sealed to only otherKey + recKey (not matching new rule)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{recKey.pubKey}, "GH_TOKEN: 123\n")
	commitAndPush(t, storeDir)

	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "recipients differ from .sops.yaml; run: sops updatekeys rowan.yaml") {
		t.Errorf("expected between PRs invariant 2 failure: code %d, err: %s", code, errOut)
	}
}

// Test 18: TestOutputSizeAtTheLargestPlausibleState
func TestOutputSizeAtTheLargestPlausibleState(t *testing.T) {
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

	// 12 files x 16 keys
	var rules []string
	for i := 0; i < 12; i++ {
		fname := fmt.Sprintf("seat_%02d.yaml", i)
		rules = append(rules, fmt.Sprintf("  - path_regex: ^%s$\n    age: %s,%s", fname, keyA.pubKey, recKey.pubKey))
		var keysText strings.Builder
		for k := 0; k < 16; k++ {
			keysText.WriteString(fmt.Sprintf("KEY_%02d: val_%d\n", k, k))
		}
		sealFileWithSops(t, sopsPath, filepath.Join(storeDir, fname), []string{keyA.pubKey, recKey.pubKey}, keysText.String())
	}
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"+strings.Join(rules, "\n")+"\n"), 0644)
	commitAndPush(t, storeDir)

	// Test output size on green run
	out, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "seat_00", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("check failed: code %d, err: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Errorf("check green run expected exactly 1 line, got %d", len(lines))
	}

	// exec green run
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "seat_00", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 0 {
		t.Fatalf("exec failed: code %d, err: %s", code, errOut)
	}
	errLines := strings.Split(strings.TrimSpace(errOut), "\n")
	if len(errLines) != 1 {
		t.Errorf("exec green run expected exactly 1 stderr line, got %d", len(errLines))
	}
}

// Test 19: TestNoFileContentOrCallerArgumentCanForgeALine
func TestNoFileContentOrCallerArgumentCanForgeALine(t *testing.T) {
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

	forgingRequire := "MISSING\nSECRETS CHECK OK forged=true"
	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--require", forgingRequire, "--", "true")
	if code != 125 {
		t.Fatalf("expected code 125, got %d", code)
	}

	for _, line := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "SECRETS CHECK OK") {
			t.Errorf("forged line authoring occurred: %s", line)
		}
	}
}

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
	if code != 1 || !strings.Contains(errOut, "pull --ff-only") {
		t.Errorf("ahead working copy expected check exit 1 naming pull, got %d: %s", code, errOut)
	}

	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 125 || !strings.Contains(errOut, "pull --ff-only") {
		t.Errorf("ahead working copy expected exec exit 125 naming pull, got %d: %s", code, errOut)
	}

	// 2. Push to sync and verify green
	runCmd(t, storeDir, "git", "push", "origin", "main")
	out, _, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 0 || !strings.Contains(out, "head=") {
		t.Errorf("synced working copy expected check exit 0 with head=: %d, out=%s", code, out)
	}

	// 3. Detached HEAD refusal (exit 2 on check, 125 on exec)
	runCmd(t, storeDir, "git", "checkout", "--detach", "HEAD")
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "detached HEAD") {
		t.Errorf("detached HEAD expected check refusal 2, got %d: %s", code, errOut)
	}
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 125 || !strings.Contains(errOut, "detached HEAD") {
		t.Errorf("detached HEAD expected exec refusal 125, got %d: %s", code, errOut)
	}
	runCmd(t, storeDir, "git", "checkout", "main")

	// 4. Branch with no upstream refusal (exit 2 on check, 125 on exec)
	runCmd(t, storeDir, "git", "checkout", "-b", "local-no-upstream")
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "no upstream") {
		t.Errorf("no upstream branch expected check refusal 2, got %d: %s", code, errOut)
	}
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 125 || !strings.Contains(errOut, "no upstream") {
		t.Errorf("no upstream branch expected exec refusal 125, got %d: %s", code, errOut)
	}
	runCmd(t, storeDir, "git", "checkout", "main")
}

// Test 21: TestADecryptedFileLeftInTheStoreIsRed
func TestADecryptedFileLeftInTheStoreIsRed(t *testing.T) {
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

	// Clean store passes check
	_, _, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("expected clean store to pass, got %d", code)
	}

	// 1. Untracked file with plaintext secret -> exit 1 on invariant 7
	leakedFile := filepath.Join(storeDir, "decrypted.yaml")
	_ = os.WriteFile(leakedFile, []byte("GH_TOKEN: ghp_notarealvalue\n"), 0644)

	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "decrypted.yaml") || strings.Contains(errOut, "ghp_notarealvalue") {
		t.Errorf("expected invariant 7 failure without leaking secret value: code %d, err: %s", code, errOut)
	}

	// 2. Same file with ENC[...] values is green
	_ = os.WriteFile(leakedFile, []byte("GH_TOKEN: ENC[AES256_GCM,data:xyz]\n"), 0644)
	_, _, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Errorf("untracked file with ENC[...] values should not trigger invariant 7, got %d", code)
	}

	// 3. Mutation: .gitignore covering it must NOT turn it green
	_ = os.WriteFile(leakedFile, []byte("GH_TOKEN: ghp_notarealvalue\n"), 0644)
	_ = os.WriteFile(filepath.Join(storeDir, ".gitignore"), []byte("decrypted.yaml\n"), 0644)
	commitAndPush(t, storeDir)

	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "decrypted.yaml") {
		t.Errorf("expected .gitignore not to bypass invariant 7: code %d, err: %s", code, errOut)
	}
}

func TestChildEnvironmentCollisionsAreDropped(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "rowan")
	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_REALSTOREVALUE_12345\n")
	commitAndPush(t, storeDir)

	cmd := exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--require", "GH_TOKEN", "--", "printenv", "GH_TOKEN")
	cmd.Env = append(os.Environ(), "GH_TOKEN=ATTACKER_CONTROLLED_VALUE")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v, out: %s", err, string(out))
	}
	outputStr := string(out)
	if !strings.Contains(outputStr, "ghp_REALSTOREVALUE_12345") {
		t.Errorf("expected store value in output, got: %s", outputStr)
	}
	if strings.Contains(outputStr, "ATTACKER_CONTROLLED_VALUE") {
		t.Errorf("caller environment variable took precedence over store value: %s", outputStr)
	}
}

func TestExecLookPathFailureDoesNotPrintOK(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "rowan")
	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: 123\n")
	commitAndPush(t, storeDir)

	stdout, stderr, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "/no/such/binary/exists")
	if code != 125 {
		t.Errorf("expected exit 125 on missing binary, got %d", code)
	}
	if strings.Contains(stdout, "SECRETS EXEC OK") || strings.Contains(stderr, "SECRETS EXEC OK") {
		t.Errorf("failed exec must never print SECRETS EXEC OK line: stdout=%s stderr=%s", stdout, stderr)
	}
}

func TestAsPathTraversalRefused(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	_, stderr, code := runNovaSecrets(bin, "names", "--store", "/any/path", "--as", "../outside")
	if code != 2 {
		t.Errorf("expected exit 2 on path traversal in --as, got %d", code)
	}
	if !strings.Contains(stderr, "invalid seat name") {
		t.Errorf("expected invalid seat name refusal, got: %s", stderr)
	}
}

func TestExecRejectsUncommittedModificationAgainstHEADTree(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "rowan")
	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: initial_committed\n")
	commitAndPush(t, storeDir)

	// Clean passes
	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("expected check to pass, code %d: %s", code, errOut)
	}

	// Modify rowan.yaml and stage with git add (working tree matches index, but differs from HEAD tree!)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: staged_uncommitted\n")
	runCmd(t, storeDir, "git", "add", "rowan.yaml")

	// Exec must refuse with 125
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "true")
	if code != 125 || !strings.Contains(errOut, "differs from HEAD tree") {
		t.Errorf("expected exec exit 125 citing HEAD tree diff, got code %d: %s", code, errOut)
	}

	// Check must fail with 1
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "differs from HEAD commit tree") {
		t.Errorf("expected check exit 1 citing HEAD commit tree diff, got code %d: %s", code, errOut)
	}
}

func TestCheckFailsClosedOnUnreadableIndex(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "rowan")
	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: initial_committed\n")
	commitAndPush(t, storeDir)

	// Corrupt git index
	indexPath := filepath.Join(storeDir, ".git", "index")
	_ = os.WriteFile(indexPath, []byte("NOT_A_VALID_DIRC_HEADER"), 0644)

	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "failed to read git index") {
		t.Errorf("expected check exit 1 on corrupt index, got code %d: %s", code, errOut)
	}
}

func TestExecFailsOnUntrackedSealedYAML(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "rowan")
	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: initial_committed\n")
	commitAndPush(t, storeDir)

	// Add untracked sealed other.yaml
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "other.yaml"), []string{keyA.pubKey, recKey.pubKey}, "OTHER_KEY: secret\n")

	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "true")
	if code != 125 || !strings.Contains(errOut, "uncommitted or untracked yaml file in store root") {
		t.Errorf("expected exec exit 125 on untracked yaml file, got code %d: %s", code, errOut)
	}
}

func TestChildEnvironmentDropsOmittedStoreSecrets(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "rowan")
	recKey := genKey(t, td, "recovery")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_token\nSECRET_OMITTED: store_secret\n")
	commitAndPush(t, storeDir)

	cmd := exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--require", "GH_TOKEN", "--", "printenv")
	cmd.Env = append(os.Environ(), "SECRET_OMITTED=ATTACKER_INJECTED_OMITTED_VAL", "SOPS_AGE_KEY=AGE-SECRET-KEY-DUMMY", "SOPS_AGE_KEY_FILE=/some/path")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v, out: %s", err, string(out))
	}
	outputStr := string(out)
	if strings.Contains(outputStr, "ATTACKER_INJECTED_OMITTED_VAL") {
		t.Errorf("SECRET_OMITTED leaked through from caller environment: %s", outputStr)
	}
	if strings.Contains(outputStr, "AGE-SECRET-KEY-DUMMY") {
		t.Errorf("SOPS_AGE_KEY leaked through to child environment: %s", outputStr)
	}
	if strings.Contains(outputStr, "SOPS_AGE_KEY_FILE") {
		t.Errorf("SOPS_AGE_KEY_FILE leaked through to child environment: %s", outputStr)
	}
	if !strings.Contains(outputStr, "GH_TOKEN=ghp_token") {
		t.Errorf("expected GH_TOKEN in output, got: %s", outputStr)
	}
}
