//go:build slow || functional

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// workerWithReadRoots writes a worker description whose read_roots names one directory: the
// shape a coordinator hands a card a staging mirror, a corpus or a toolchain with.
func workerWithReadRoots(t *testing.T, roots ...string) string {
	t.Helper()
	home := t.TempDir()
	// the key travels as a file the description names, the per-test seam beside the
	// environment: no variable is set on the whole process.
	keyFile := filepath.Join(t.TempDir(), "fake.key")
	require.NoError(t, os.WriteFile(keyFile, []byte("FAKE_KEY="+fakeKey+"\n"), 0o600))
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": "fake-model",
		"env_var": "FAKE_KEY", "key_file": keyFile, "usage": "opencode",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		"read_roots":   roots,
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "worker.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))
	return path
}
