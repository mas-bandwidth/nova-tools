package main

// Red tests for issue #764: `nova-secrets place` copies a named secret from the local
// store to a remote fleet machine over ssh with mode 0600 and writes a receipt; `placed`
// lists what is there by name and hash. The ssh child and sops are fakes on disk, so no
// test reaches a machine or a real credential.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type placeFixture struct {
	bin          string
	store        string
	key          string
	sops         string
	ssh          string
	machines     string
	receipts     string
	remotePath   string
	value        string
	sshArgsFile  string
	sshStdinFile string
}

func writeFakeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newPlaceFixture(t *testing.T) placeFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh runs through /bin/sh; the fleet is a linux bench")
	}
	td := t.TempDir()

	bin := buildNovaSecrets(t)

	store := filepath.Join(td, "store")
	if err := os.MkdirAll(filepath.Join(store, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "rowan.yaml"), []byte("DEEPSEEK_API_KEY: ENC[FAKE]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	keyDir := filepath.Join(td, "keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(keyDir, "rowan.key")
	if err := os.WriteFile(key, []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	value := "sk-distinctive-DEEPSEEK-value-0001"
	sopsPath := filepath.Join(td, "fake-sops")
	writeFakeExe(t, sopsPath, "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"--version) echo 'sops 3.13.3'; exit 0 ;;\n"+
		"-d) printf 'DEEPSEEK_API_KEY: %s\\n' '"+value+"'; exit 0 ;;\n"+
		"esac\nexit 0\n")

	sshArgsFile := filepath.Join(td, "ssh.args")
	sshStdinFile := filepath.Join(td, "ssh.stdin")
	sshPath := filepath.Join(td, "fake-ssh")
	writeFakeExe(t, sshPath, "#!/bin/sh\n"+
		"printf '%s\\n' \"$@\" >> "+sshArgsFile+"\n"+
		"cat > "+sshStdinFile+"\n"+
		"exit 0\n")

	machines := filepath.Join(td, "machines.tsv")
	if err := os.WriteFile(machines, []byte("mini\tmini.example\t/home/glenn\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	receipts := filepath.Join(td, "receipts")

	return placeFixture{
		bin: bin, store: store, key: key, sops: sopsPath, ssh: sshPath,
		machines: machines, receipts: receipts, remotePath: "/home/glenn/nova-bench/deepseek.env",
		value: value, sshArgsFile: sshArgsFile, sshStdinFile: sshStdinFile,
	}
}

func (f placeFixture) placeArgs(machine, secret, remotePath string) []string {
	return []string{
		"place",
		"--store", f.store, "--as", "rowan", "--key", f.key, "--sops", f.sops,
		"--machine", machine, "--secret", secret, "--path", remotePath,
		"--machines", f.machines, "--receipts", f.receipts, "--ssh", f.ssh,
	}
}

func (f placeFixture) placedArgs(machine string) []string {
	return []string{"placed", "--machine", machine, "--receipts", f.receipts}
}

func TestPlaceThenPlacedShowsNameAndHash(t *testing.T) {
	f := newPlaceFixture(t)
	stdout, stderr, code := runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	if code != 0 {
		t.Fatalf("place exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	sum := sha256.Sum256([]byte(f.value))
	wantHash := hex.EncodeToString(sum[:])

	// The value reached the machine by stdin, not an argument.
	copied, err := os.ReadFile(f.sshStdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != f.value {
		t.Fatalf("ssh stdin = %q, want the secret value", copied)
	}
	args, err := os.ReadFile(f.sshArgsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), f.value) {
		t.Fatalf("the value appears in the ssh argument list: %q", args)
	}

	// The receipt carries machine, secret, path, sha256 and a stamp, and never the value.
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(receipt), f.value) {
		t.Fatalf("the receipt holds the value: %q", receipt)
	}
	if !strings.Contains(string(receipt), wantHash) {
		t.Fatalf("receipt = %q, want sha256 %s", receipt, wantHash)
	}
	if !strings.Contains(string(receipt), f.remotePath) {
		t.Fatalf("receipt = %q, want remote path %s", receipt, f.remotePath)
	}

	// placed lists the name and the hash.
	stdout, stderr, code = runNovaSecrets(f.bin, f.placedArgs("mini")...)
	if code != 0 {
		t.Fatalf("placed exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "DEEPSEEK_API_KEY") || !strings.Contains(stdout, wantHash) {
		t.Fatalf("placed stdout = %q, want the secret name and hash %s", stdout, wantHash)
	}
}

func TestPlaceNeverPrintsTheValue(t *testing.T) {
	f := newPlaceFixture(t)
	stdout, stderr, code := runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	if code != 0 {
		t.Fatalf("place exit=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stdout, f.value) || strings.Contains(stderr, f.value) {
		t.Fatalf("the value appears on a stream: stdout=%q stderr=%q", stdout, stderr)
	}
	stdout, stderr, code = runNovaSecrets(f.bin, f.placedArgs("mini")...)
	if code != 0 {
		t.Fatalf("placed exit=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stdout, f.value) || strings.Contains(stderr, f.value) {
		t.Fatalf("placed printed the value: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestPlaceRefusesMissingMachineWithRemedy(t *testing.T) {
	f := newPlaceFixture(t)
	_, stderr, code := runNovaSecrets(f.bin, f.placeArgs("nowhere", "DEEPSEEK_API_KEY", f.remotePath)...)
	if code != 2 {
		t.Fatalf("place unknown machine exit=%d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "nowhere") || !strings.Contains(stderr, f.machines) {
		t.Fatalf("refusal %q must name the machine and the fleet registry", stderr)
	}
}

func TestPlaceRefusesMissingStoreWithRemedy(t *testing.T) {
	f := newPlaceFixture(t)
	args := f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)
	for i, a := range args {
		if a == "--store" {
			args[i+1] = filepath.Join(f.store, "nope")
		}
	}
	_, stderr, code := runNovaSecrets(f.bin, args...)
	if code != 2 {
		t.Fatalf("place missing store exit=%d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "nope") {
		t.Fatalf("refusal %q must name the absent store", stderr)
	}
}
