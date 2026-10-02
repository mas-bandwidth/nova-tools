//go:build functional

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The end-to-end proof, against the real sops and the real age: a seat that exists only
// as a public key on 2026-09-18 comes out of this with a file it can open and nobody
// else can, carrying exactly the values it was told to carry.
//
// `seal` cannot do this and never could -- it decrypts the seat file before it writes
// one, and only the new seat's key opens the new seat's file. The store's PR #15 broke
// that circle with a hand pipe; this test holds the verb to what the hand did.
func TestSeatAddAgainstRealBinaries(t *testing.T) {
	t.Parallel()

	sopsPath := findRealSops(t)
	_ = findRealAgeKeygen(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "secrets")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	recovery := genRealKey(t, td, "recovery")
	rowan := genRealKey(t, td, "rowan")
	air := genRealKey(t, td, "air") // the new bench's own keygen, on the new bench

	if err := os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n", rowan.pubKey, recovery.pubKey)
	if err := os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	sealFileWithRealSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"),
		[]string{rowan.pubKey, recovery.pubKey},
		"GH_TOKEN: carried-token\nDEEPSEEK_API_KEY: carried-deepseek\nLEFT_BEHIND: stays-home\n")
	commitAndPush(t, storeDir)

	out, errOut, code := runNovaSecrets(bin, "seat", "add",
		"--store", storeDir, "--as", "air", "--pub", air.pubKey,
		"--from", "rowan", "--only", "GH_TOKEN,DEEPSEEK_API_KEY",
		"--key", rowan.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("seat add exited %d: %s", code, errOut)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "SECRETS SEAT ADD OK ") {
		t.Errorf("the last line is not the verdict:\n%s", out)
	}
	for _, want := range []string{"as=air", "from=rowan", "keys=2", "file=air.yaml"} {
		if !strings.Contains(last, want) {
			t.Errorf("the OK line carries no %q: %s", want, last)
		}
	}
	for _, secret := range []string{"carried-token", "carried-deepseek", "stays-home"} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("a value reached a stream:\nstdout:\n%s\nstderr:\n%s", out, errOut)
		}
	}

	// The new seat opens its own file, with the values intact.
	plain := sopsDecrypt(t, sopsPath, air.privPath, filepath.Join(storeDir, "air.yaml"))
	if !strings.Contains(plain, "GH_TOKEN: carried-token") || !strings.Contains(plain, "DEEPSEEK_API_KEY: carried-deepseek") {
		t.Errorf("the carried values did not survive the pipe:\n%s", plain)
	}
	if strings.Contains(plain, "LEFT_BEHIND") {
		t.Errorf("a key nobody asked for was carried over:\n%s", plain)
	}

	// The seat it came from cannot open it: the rule picked the recipients, not the source.
	if _, err := exec.Command(sopsPath, "-d", filepath.Join(storeDir, "air.yaml")).Output(); err == nil {
		t.Error("air.yaml decrypted with no identity at all")
	}
	cmd := exec.Command(sopsPath, "-d", filepath.Join(storeDir, "air.yaml"))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOPS_AGE_KEY_FILE=" + rowan.privPath, "HOME=" + td}
	if _, err := cmd.Output(); err == nil {
		t.Error("the source seat can still open the new seat's file")
	}

	// And the store is green for the new seat once the two changed files are committed,
	// which is the next step the receipt names.
	commitAndPush(t, storeDir)
	out, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "air",
		"--key", air.privPath, "--sops", sopsPath)
	if code != 0 {
		t.Fatalf("check on the new seat exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "mine=1") {
		t.Errorf("the new seat does not own exactly its own file: %s", out)
	}

	out, _, code = runNovaSecrets(bin, "names", "--store", storeDir, "--as", "air")
	if code != 0 {
		t.Fatalf("names on the new seat exited %d", code)
	}
	if !strings.Contains(out, "key=GH_TOKEN") || !strings.Contains(out, "key=DEEPSEEK_API_KEY") {
		t.Errorf("the new seat does not list the carried keys:\n%s", out)
	}
	if strings.Contains(out, "clear=true") {
		t.Errorf("the new seat carries a value in the clear:\n%s", out)
	}
}
