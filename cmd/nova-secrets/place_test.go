package main

// Red tests for issue #764: `nova-secrets place` copies a named secret from the local
// store to a remote fleet machine over ssh with mode 0600 and writes a receipt; `placed`
// lists what is there by name and sealed file. The ssh child and sops are fakes on disk, so no
// test reaches a machine or a real credential.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, testbin.WriteExecutable(path, []byte(body), 0o755))
}

func newPlaceFixture(t *testing.T) placeFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh runs through /bin/sh; the fleet is a linux bench")
	}
	td := t.TempDir()

	bin := buildNovaSecrets(t)

	store := filepath.Join(td, "store")
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(store, "rowan.yaml"), []byte("DEEPSEEK_API_KEY: ENC[FAKE]\n"), 0o644))

	keyDir := filepath.Join(td, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0o700))
	key := filepath.Join(keyDir, "rowan.key")
	require.NoError(t, os.WriteFile(key, []byte("AGE-SECRET-KEY-FAKE\n"), 0o600))

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
	require.NoError(t, os.WriteFile(machines, []byte("mini\tmini.example\t/home/glenn\t-\n"), 0o644))
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
	require.Equal(t, 0, code, "place exit=%d stdout=%q stderr=%q", code, stdout, stderr)

	// What was placed is named by the sealed file's blob id, never a digest of the value.
	wantBlob := blobOf(t, filepath.Join(f.store, "rowan.yaml"))

	// The value reached the machine by stdin, not an argument.
	copied, err := os.ReadFile(f.sshStdinFile)
	require.NoError(t, err)
	require.Equal(t, f.value, string(copied), "ssh stdin = %q, want the secret value", copied)
	args, err := os.ReadFile(f.sshArgsFile)
	require.NoError(t, err)
	require.NotContains(t, string(args), f.value, "the value appears in the ssh argument list: %q", args)

	// The receipt carries secret, path, the sealed file and a stamp, and never the value.
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	require.NoError(t, err)
	require.NotContains(t, string(receipt), f.value, "the receipt holds the value: %q", receipt)
	require.Contains(t, string(receipt), wantBlob, "receipt = %q, want the sealed file's blob %s", receipt, wantBlob)
	require.Contains(t, string(receipt), f.remotePath, "receipt = %q, want remote path %s", receipt, f.remotePath)

	// placed lists the name and the sealed file.
	stdout, stderr, code = runNovaSecrets(f.bin, f.placedArgs("mini")...)
	require.Equal(t, 0, code, "placed exit=%d stderr=%q", code, stderr)
	require.Contains(t, stdout, "DEEPSEEK_API_KEY", "placed stdout = %q, want the secret name and blob %s", stdout, wantBlob)
	require.Contains(t, stdout, "blob="+wantBlob, "placed stdout = %q, want the secret name and blob %s", stdout, wantBlob)
}

func TestPlaceNeverPrintsTheValue(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	stdout, stderr, code := runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	require.Equal(t, 0, code, "place exit=%d stderr=%q", code, stderr)
	require.NotContains(t, stdout, f.value, "the value appears on a stream: stdout=%q stderr=%q", stdout, stderr)
	require.NotContains(t, stderr, f.value, "the value appears on a stream: stdout=%q stderr=%q", stdout, stderr)
	stdout, stderr, code = runNovaSecrets(f.bin, f.placedArgs("mini")...)
	require.Equal(t, 0, code, "placed exit=%d stderr=%q", code, stderr)
	require.NotContains(t, stdout, f.value, "placed printed the value: stdout=%q stderr=%q", stdout, stderr)
	require.NotContains(t, stderr, f.value, "placed printed the value: stdout=%q stderr=%q", stdout, stderr)
}

func TestPlaceRefusesMissingMachineWithRemedy(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	_, stderr, code := runNovaSecrets(f.bin, f.placeArgs("nowhere", "DEEPSEEK_API_KEY", f.remotePath)...)
	require.Equal(t, 2, code, "place unknown machine exit=%d, want 2; stderr=%q", code, stderr)
	require.Contains(t, stderr, "nowhere", "refusal %q must name the machine and the fleet registry", stderr)
	require.Contains(t, stderr, f.machines, "refusal %q must name the machine and the fleet registry", stderr)
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
	require.Equal(t, 2, code, "place missing store exit=%d, want 2; stderr=%q", code, stderr)
	require.Contains(t, stderr, "nope", "refusal %q must name the absent store", stderr)
}
