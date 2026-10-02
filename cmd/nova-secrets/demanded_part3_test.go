package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test 11: TestAKeyNameThatIsNotAnEnvVarIsRefused
func TestAKeyNameThatIsNotAnEnvVarIsRefused(t *testing.T) {
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

	// File with bad keys: gh-token, 2FA, A B
	badPayload := "gh-token: 1\n2FA: 2\nA B: 3\nGH_TOKEN: 4\nA1_B: 5\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, badPayload)
	commitAndPush(t, storeDir)

	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	require.Equal(t, 125, code, "expected code 125, got %d", code)
	assert.Contains(t, errOut, "2FA, A B, gh-token", "expected sorted bad keys reported, got: %s", errOut)

	// Now valid payload
	validPayload := "GH_TOKEN: ghp_ok\nA1_B: ok2\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, validPayload)
	commitAndPush(t, storeDir)

	out, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "echo", "PASSED")
	assert.Equal(t, 0, code, "expected valid keys to pass, got %d: out=%s, err=%s", code, out, errOut)
	assert.Contains(t, out, "PASSED", "expected valid keys to pass, got %d: out=%s, err=%s", code, out, errOut)
}

// Test 13: TestCheckFailsClosedOnEverythingItCannotRead
func TestCheckFailsClosedOnEverythingItCannotRead(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()

	// 1. Store is a file -> exit 2
	fileAsStore := filepath.Join(td, "file-store")
	_ = os.WriteFile(fileAsStore, []byte("im a file"), 0644)
	_, errOut, code := runNovaSecrets(bin, "check", "--store", fileAsStore, "--as", "rowan", "--key", filepath.Join(td, "k"), "--sops", sopsPath)
	assert.Equal(t, 2, code, "file-as-store expected 2: code=%d, err=%s", code, errOut)
	assert.Contains(t, errOut, "not a directory", "file-as-store expected 2: code=%d, err=%s", code, errOut)

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
	assert.Equal(t, 2, code, "no .sops.yaml expected 2: code=%d, err=%s", code, errOut)
	assert.Contains(t, errOut, "no .sops.yaml", "no .sops.yaml expected 2: code=%d, err=%s", code, errOut)

	// 3. .sops.yaml that will not parse -> exit 1 on invariant 1
	_ = os.WriteFile(filepath.Join(noSopsStore, ".sops.yaml"), []byte("not_yaml: {{bad syntax"), 0644)
	commitAndPush(t, noSopsStore)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", noSopsStore, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "unparseable .sops.yaml expected 1 on invariant 1: code=%d, err=%s", code, errOut)
	assert.Contains(t, errOut, ".sops.yaml", "unparseable .sops.yaml expected 1 on invariant 1: code=%d, err=%s", code, errOut)

	// 4. Unreadable .sops.yaml -> exit 1 on invariant 1
	_ = os.Chmod(filepath.Join(noSopsStore, ".sops.yaml"), 0000)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", noSopsStore, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	assert.Equal(t, 1, code, "unreadable .sops.yaml expected 1: code=%d, err=%s", code, errOut)
	_ = os.Chmod(filepath.Join(noSopsStore, ".sops.yaml"), 0644)
}

// Test 14: TestKeygenNeverOverwritesAndNeverTouchesTheStore
func TestKeygenNeverOverwritesAndNeverTouchesTheStore(t *testing.T) {
	t.Parallel()
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
	assert.Equal(t, 2, code, "expected overwrite refusal, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "already exists; refusing to overwrite", "expected overwrite refusal, got %d: %s", code, errOut)
	data, _ := os.ReadFile(existingKey)
	assert.Equal(t, "ORIGINAL_BYTES", string(data), "existing key file modified!")

	// 2. 0755 parent refused naming mkdir -m 700
	badParentDir := filepath.Join(td, "badparent")
	_ = os.MkdirAll(badParentDir, 0755)
	targetInBadParent := filepath.Join(badParentDir, "test.key")
	_, errOut, code = runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", targetInBadParent, "--age-keygen", ageKeygen)
	assert.Equal(t, 2, code, "expected 0700 dir refusal, got %d: %s", code, errOut)
	assert.True(t, strings.Contains(errOut, "mkdir -m 700") || strings.Contains(errOut, "chmod 700"), "expected 0700 dir refusal, got %d: %s", code, errOut)

	// 3. Success creates key, private key on no stream, store untouched
	newKeyPath := filepath.Join(validKeyDir, "new.key")

	out, errOut, code := runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", newKeyPath, "--age-keygen", ageKeygen, "--store", storeDir)
	require.Equal(t, 0, code, "keygen failed: code %d, err: %s", code, errOut)

	// Store tree hash unchanged
	treeHashAfter := hashTree(storeDir)
	assert.Equal(t, treeHashAfter, treeHashBefore, "keygen modified the store tree!")

	// Private key appears on no stream
	privBytes, _ := os.ReadFile(newKeyPath)
	assert.NotContains(t, out, string(privBytes), "private key appeared in output!")
	assert.NotContains(t, errOut, string(privBytes), "private key appeared in output!")

	// Printed rule satisfies invariant 1 when pasted
	assert.Contains(t, out, recKey.pubKey, "expected recovery key in rule output: %s", out)
	assert.NotContains(t, out, "placeholder:", "with --store, rule should not contain placeholder note: %s", out)

	// 4. Run without --store: literal placeholder and NOTE line
	noStoreKey := filepath.Join(validKeyDir, "nostore.key")
	out, _, code = runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", noStoreKey, "--age-keygen", ageKeygen)
	require.Equal(t, 0, code, "keygen without store failed: %d", code)
	assert.Contains(t, out, "<recovery key>", "expected placeholder and note line without store: %s", out)
	assert.Contains(t, out, "SECRETS RULE NOTE  placeholder: no --store, so <recovery key> is filled by `nova-secrets seat add`", "expected placeholder and note line without store: %s", out)

	// 5. --store with empty recovery.pub is exit 2 naming file
	emptyRecStore := filepath.Join(td, "emptyrec")
	_ = os.MkdirAll(emptyRecStore, 0755)
	initGitStore(t, emptyRecStore)
	_ = os.WriteFile(filepath.Join(emptyRecStore, "recovery.pub"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(emptyRecStore, ".sops.yaml"), []byte("creation_rules:\n"), 0644)
	commitAndPush(t, emptyRecStore)

	errKeyPath := filepath.Join(validKeyDir, "err.key")
	_, errOut, code = runNovaSecrets(bin, "keygen", "--as", "rowan", "--key", errKeyPath, "--age-keygen", ageKeygen, "--store", emptyRecStore)
	assert.Equal(t, 2, code, "expected exit 2 naming recovery.pub empty, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "recovery.pub is empty", "expected exit 2 naming recovery.pub empty, got %d: %s", code, errOut)
	_ = sopsPath
}

// Test 15: TestTheLauncherOrderWorksWithTheStoreFullyDenied
//
// End to end in nova-sandbox's real grammar, built from this commit: the write set carries
// the probe's HOME, and neither the store nor the key directory is in any read set. In
// the launcher's order (nova-secrets outside, the wall inside) the probe sees its keys and
// cannot read the store or the key; in the reverse nesting the tool inside the wall cannot
// open the store and the command never starts. It skips, naming nova-sandbox's own
// refusal, only where this machine cannot build the wall.
func TestTheLauncherOrderWorksWithTheStoreFullyDenied(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)
	sb := buildNovaSandbox(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)
	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(fmt.Sprintf("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n", keyA.pubKey, recKey.pubKey)), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_launcher_value\n")
	commitAndPush(t, storeDir)

	home := filepath.Join(td, "probehome")
	_ = os.MkdirAll(home, 0700)
	requireWall(t, sb, home)

	// The probe prints the key it was given, then tries the store and the key file.
	probe := `echo "SEEN=$GH_TOKEN"; cat "$0" >/dev/null 2>&1 && echo STORE-READ; cat "$1" >/dev/null 2>&1 && echo KEY-READ; exit 0`
	storeFile := filepath.Join(storeDir, "rowan.yaml")

	// The launcher's order: nova-secrets opens the file and becomes nova-sandbox, which
	// becomes the probe. No read set names the store, the key or sops.
	wall := []string{sb, "--write", home}
	for _, r := range wallReads() {
		wall = append(wall, "--read", r)
	}
	wall = append(wall, "--net-deny", "--", "/bin/sh", "-c", probe, storeFile, keyA.privPath)
	launcher := append([]string{"exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--require", "GH_TOKEN", "--"}, wall...)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	out, errOut, code, _ := runWithEnv(bin, env, launcher...)
	require.Equal(t, 0, code, "launcher order: the probe must see its key, exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	require.Contains(t, out, "SEEN=ghp_launcher_value\n", "launcher order: the probe must see its key, exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	assert.NotContains(t, out, "STORE-READ", "launcher order: the probe read the store or the key through the wall: %s", out)
	assert.NotContains(t, out, "KEY-READ", "launcher order: the probe read the store or the key through the wall: %s", out)

	// The reverse nesting: the wall outside, nova-secrets inside. With the store and the
	// key in no read set it must FAIL, and the probe must never run.
	out, errOut, code = inWall(sb, home, []string{filepath.Dir(bin), filepath.Dir(sopsPath)}, true,
		bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "/bin/sh", "-c", probe, storeFile, keyA.privPath)
	assert.NotEqual(t, 0, code, "reverse nesting must fail before the command runs, got exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	assert.NotContains(t, out, "SEEN=", "reverse nesting must fail before the command runs, got exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	assert.NotContains(t, out+errOut, "ghp_launcher_value", "reverse nesting leaked the value: %s%s", out, errOut)
}
