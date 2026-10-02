package main

// Red tests for issue #764: `nova-secrets place` copies a named secret from the local
// store to a remote fleet machine over ssh with mode 0600 and writes a receipt; `placed`
// lists what is there by name and sealed file. The ssh child and sops are fakes on disk, so no
// test reaches a machine or a real credential.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
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
	if err := testbin.WriteExecutable(path, []byte(body), 0o755); err != nil {
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

	// Shares no six bytes with any name, path or word the tool prints, so a leak test
	// that finds a fragment of it has found the value and not a coincidence.
	value := "sk-qzxjwkvbnmplrtq9x"
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

func TestPlaceThenPlacedShowsNameAndSealedFile(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	stdout, stderr, code := runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	if code != 0 {
		t.Fatalf("place exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// What was placed is named by the sealed file's blob id, never a digest of the value.
	wantBlob := blobOf(t, filepath.Join(f.store, "rowan.yaml"))

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

	// The receipt carries secret, path, the sealed file and a stamp, and never the value.
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(receipt), f.value) {
		t.Fatalf("the receipt holds the value: %q", receipt)
	}
	if !strings.Contains(string(receipt), wantBlob) {
		t.Fatalf("receipt = %q, want the sealed file's blob %s", receipt, wantBlob)
	}
	if !strings.Contains(string(receipt), f.remotePath) {
		t.Fatalf("receipt = %q, want remote path %s", receipt, f.remotePath)
	}

	// placed lists the name and the sealed file.
	stdout, stderr, code = runNovaSecrets(f.bin, f.placedArgs("mini")...)
	if code != 0 {
		t.Fatalf("placed exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "DEEPSEEK_API_KEY") || !strings.Contains(stdout, "blob="+wantBlob) {
		t.Fatalf("placed stdout = %q, want the secret name and blob %s", stdout, wantBlob)
	}
}

func TestPlaceNeverPrintsTheValue(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
