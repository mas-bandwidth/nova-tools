package secrets

// The unit cover for seal.go's terminal and post-seal paths. checkSeatDecrypts
// reaches RunCheck, whose refusals all fire before any child runs, so its pass and
// check-failed branches (which need the sops and git children RunCheck spawns) stay
// outside the unit tier; readSealFromTTY refuses when the process holds no
// controlling terminal, and its prompting path needs that terminal and the stty
// child disableEcho spawns. disableEcho and runStty build their own os/exec child
// and carry no seam, so no unit test reaches them at all.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sealCoverStore lays out a directory storeShape accepts (a .git directory and a
// .sops.yaml) without running git: every row here stops before the store's git reads.
func sealCoverStore(t *testing.T) string {
	t.Helper()
	storeDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.MkdirAll(filepath.Join(storeDir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, ".sops.yaml"),
		[]byte("creation_rules:\n  - path_regex: ^worker\\.yaml$\n    age: age1cover\n"), 0o644))
	return storeDir
}

// sealCoverKey writes the key file CheckInvariant6 and ExtractPublicKeyFromKeyFile
// accept: mode 0600 in a mode-0700 directory, carrying the public key comment.
func sealCoverKey(t *testing.T) string {
	t.Helper()
	keyDir := filepath.Join(t.TempDir(), "keys")
	require.NoError(t, os.Mkdir(keyDir, 0o700))
	keyPath := filepath.Join(keyDir, "worker.key")
	require.NoError(t, os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-1TEST\n# public key: age1cover\n"), 0o600))
	return keyPath
}

// TestSealCoverCheckSeatDecryptsSurfacesRunCheckRefusals pins the one branch of
// checkSeatDecrypts a unit test can reach with no child process: RunCheck's own
// refusals, one per guard, pass through unchanged.
func TestSealCoverCheckSeatDecryptsSurfacesRunCheckRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args func(t *testing.T) (storeDir, asName, keyPath, sopsPath string)
		want string
	}{
		{"missing flags name every one", func(*testing.T) (string, string, string, string) {
			return "", "", "", ""
		}, "missing --store <dir>, --as <name>, --key <path>, --sops <path>"},
		{"seat file absent", func(t *testing.T) (string, string, string, string) {
			return sealCoverStore(t), "worker", filepath.Join(t.TempDir(), "absent.key"), filepath.Join(t.TempDir(), "sops")
		}, "seat file worker.yaml is absent"},
		{"key file absent", func(t *testing.T) (string, string, string, string) {
			store := sealCoverStore(t)
			require.NoError(t, os.WriteFile(filepath.Join(store, "worker.yaml"), []byte("ENC[cover]\n"), 0o600))
			return store, "worker", filepath.Join(t.TempDir(), "absent.key"), filepath.Join(t.TempDir(), "sops")
		}, "key file"},
		{"sops binary not executable", func(t *testing.T) (string, string, string, string) {
			store := sealCoverStore(t)
			require.NoError(t, os.WriteFile(filepath.Join(store, "worker.yaml"), []byte("ENC[cover]\n"), 0o600))
			dead := filepath.Join(t.TempDir(), "sops")
			require.NoError(t, os.WriteFile(dead, []byte("#!/bin/sh\n"), 0o644))
			return store, "worker", sealCoverKey(t), dead
		}, "sops binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			storeDir, asName, keyPath, sopsPath := tt.args(t)
			err := checkSeatDecrypts(storeDir, asName, keyPath, sopsPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// TestSealCoverReadSealFromTTYRefusesWithoutTerminal pins the refusal a process
// without a controlling terminal gets, whose remedy is the --stdin route.
func TestSealCoverReadSealFromTTYRefusesWithoutTerminal(t *testing.T) {
	t.Parallel()
	if w, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		// ignored: the probe only decides whether the refusal is reachable here; nothing is written to the terminal
		_ = w.Close()
		t.Skip("the test process holds a controlling terminal; seal prompts on it instead of refusing")
	}
	_, err := readSealFromTTY()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no controlling terminal to read the value from")
	assert.Contains(t, err.Error(), "run with --stdin")
}
