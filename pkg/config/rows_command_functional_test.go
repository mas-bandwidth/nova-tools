//go:build functional

package config_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/stretchr/testify/require"
)

// TestConfigCommandReadsTableFile pins S4's production registration: the real
// command reads typed file stores without the test process's rowsread import.
func TestConfigCommandReadsTableFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-config")
	build := subproc.Context(t.Context(), "go", "build", "-o", bin, "../../cmd/nova-config")
	build.Env = append(goenv.Clean(os.Environ()), "GOFLAGS=-mod=readonly", "NOVA_TEST_NO_HOST=1")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	migrations, err := config.Migrations()
	require.NoError(t, err)
	state := sampleFile(len(migrations))
	wire := fileImage(t, state, true)
	legacy, err := json.Marshal(state)
	require.NoError(t, err)
	for name, data := range map[string][]byte{"binary": wire, "base64": []byte(base64.StdEncoding.EncodeToString(wire)), "legacy": legacy} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "store")
			require.NoError(t, os.WriteFile(path, data, 0o600))
			cmd := subproc.Context(t.Context(), bin, "machine", "show", "host-a", "--file", path, "--json")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "host-a")
		})
	}
}
