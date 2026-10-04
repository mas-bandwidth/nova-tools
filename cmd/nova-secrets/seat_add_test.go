package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The end-to-end proof, against the real sops and the real age: a seat that exists only
// as a public key on 2026-09-18 comes out of this with a file it can open and nobody
// else can, carrying exactly the values it was told to carry.
//
// `seal` cannot do this and never could -- it decrypts the seat file before it writes
// one, and only the new seat's key opens the new seat's file. The store's PR #15 broke
// that circle with a hand pipe; this test holds the verb to what the hand did.
func TestSeatAddGivesANewSeatItsFirstValues(t *testing.T) {
	t.Parallel()

	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "secrets")
	require.NoError(t, os.MkdirAll(storeDir, 0755))
	initGitStore(t, storeDir)

	recovery := genKey(t, td, "recovery")
	rowan := genKey(t, td, "rowan")
	air := genKey(t, td, "air") // the new bench's own keygen, on the new bench

	require.NoError(t, os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644))
	cfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n", rowan.pubKey, recovery.pubKey)
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(cfg), 0644))
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"),
		[]string{rowan.pubKey, recovery.pubKey},
		"GH_TOKEN: carried-token\nDEEPSEEK_API_KEY: carried-deepseek\nLEFT_BEHIND: stays-home\n")
	commitAndPush(t, storeDir)

	out, errOut, code := runNovaSecrets(bin, "seat", "add",
		"--store", storeDir, "--as", "air", "--pub", air.pubKey,
		"--from", "rowan", "--only", "GH_TOKEN,DEEPSEEK_API_KEY",
		"--key", rowan.privPath, "--sops", sopsPath)
	require.Equal(t, 0, code, "seat add exited %d: %s", code, errOut)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(last, "SECRETS SEAT ADD OK "), "the last line is not the verdict:\n%s", out)
	for _, want := range []string{"as=air", "from=rowan", "keys=2", "file=air.yaml"} {
		assert.Contains(t, last, want, "the OK line carries no %q: %s", want, last)
	}
	// Not a value, not on stdout, not on stderr, not in the progress lines.
	for _, secret := range []string{"carried-token", "carried-deepseek", "stays-home"} {
		assert.NotContains(t, out, secret, "a value reached a stream:\nstdout:\n%s\nstderr:\n%s", out, errOut)
		assert.NotContains(t, errOut, secret, "a value reached a stream:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}

	// The new seat opens its own file, with the values intact.
	plain := sopsDecrypt(t, sopsPath, air.privPath, filepath.Join(storeDir, "air.yaml"))
	assert.Contains(t, plain, "GH_TOKEN: carried-token", "the carried values did not survive the pipe:\n%s", plain)
	assert.Contains(t, plain, "DEEPSEEK_API_KEY: carried-deepseek", "the carried values did not survive the pipe:\n%s", plain)
	assert.NotContains(t, plain, "LEFT_BEHIND", "a key nobody asked for was carried over:\n%s", plain)

	// The seat it came from cannot open it: the rule picked the recipients, not the source.
	_, err := exec.Command(sopsPath, "-d", filepath.Join(storeDir, "air.yaml")).Output()
	assert.Error(t, err, "air.yaml decrypted with no identity at all")
	cmd := exec.Command(sopsPath, "-d", filepath.Join(storeDir, "air.yaml"))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOPS_AGE_KEY_FILE=" + rowan.privPath, "HOME=" + td}
	_, err = cmd.Output()
	assert.Error(t, err, "the source seat can still open the new seat's file")

	// And the store is green for the new seat once the two changed files are committed,
	// which is the next step the receipt names.
	commitAndPush(t, storeDir)
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "air",
		"--key", air.privPath, "--sops", sopsPath)
	require.Equal(t, 0, code, "check on the new seat exited %d: %s", code, errOut)
	assert.Contains(t, out, "mine=1", "the new seat does not own exactly its own file: %s", out)

	out, _, code = runNovaSecrets(bin, "names", "--store", storeDir, "--as", "air")
	require.Equal(t, 0, code, "names on the new seat exited %d", code)
	assert.Contains(t, out, "key=GH_TOKEN", "the new seat does not list the carried keys:\n%s", out)
	assert.Contains(t, out, "key=DEEPSEEK_API_KEY", "the new seat does not list the carried keys:\n%s", out)
	assert.NotContains(t, out, "clear=true", "the new seat carries a value in the clear:\n%s", out)
}

// TestSeatAddRefusesASecondTimeOnTheSameSeat: run twice, and the second run must find
// both the file and the rule already there and change nothing.
func TestSeatAddRefusesASecondTimeOnTheSameSeat(t *testing.T) {
	t.Parallel()

	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "secrets")
	require.NoError(t, os.MkdirAll(storeDir, 0755))
	initGitStore(t, storeDir)

	recovery := genKey(t, td, "recovery")
	rowan := genKey(t, td, "rowan")
	air := genKey(t, td, "air")

	require.NoError(t, os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644))
	cfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n", rowan.pubKey, recovery.pubKey)
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(cfg), 0644))
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"),
		[]string{rowan.pubKey, recovery.pubKey}, "GH_TOKEN: carried-token\n")
	commitAndPush(t, storeDir)

	args := []string{"seat", "add", "--store", storeDir, "--as", "air", "--pub", air.pubKey,
		"--from", "rowan", "--only", "GH_TOKEN", "--key", rowan.privPath, "--sops", sopsPath}
	{
		_, errOut, code := runNovaSecrets(bin, args...)
		require.Equal(t, 0, code, "the first seat add exited %d: %s", code, errOut)
	}
	before, err := os.ReadFile(filepath.Join(storeDir, ".sops.yaml"))
	require.NoError(t, err)
	fileBefore, err := os.ReadFile(filepath.Join(storeDir, "air.yaml"))
	require.NoError(t, err)

	_, errOut, code := runNovaSecrets(bin, args...)
	require.Equal(t, 2, code, "the second seat add exited %d, want 2", code)
	assert.Contains(t, errOut, "air.yaml", "the refusal does not name the file: %s", errOut)
	after, _ := os.ReadFile(filepath.Join(storeDir, ".sops.yaml"))
	assert.Equal(t, string(before), string(after), ".sops.yaml changed under a refusal:\n%s", after)
	fileAfter, _ := os.ReadFile(filepath.Join(storeDir, "air.yaml"))
	assert.Equal(t, string(fileBefore), string(fileAfter), "the seat file was rewritten by a refused run")
}

func sopsDecrypt(t *testing.T, sopsPath, keyPath, filePath string) string {
	t.Helper()
	cmd := exec.Command(sopsPath, "-d", filePath)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOPS_AGE_KEY_FILE=" + keyPath, "HOME=" + t.TempDir()}
	out, err := cmd.Output()
	require.NoError(t, err, "sops -d %s failed: %v", filePath, err)
	return string(out)
}
