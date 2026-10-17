package up

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpSeatEnvironmentWrite pins the writer switch of the seat environment
// record (S4): writeSeatEnvironment writes the seatrec SeatEnvironment table's
// schema JSON text form, what it writes reads back equal through the dual
// reader the reader card landed (the seatcred reader), a legacy fixture of the
// same record still reads, and a value the table cannot hold is refused before
// a byte is written. The file is written under a t.TempDir() named through the
// getenv the function takes; no fixture holds a secret value.
func TestUpSeatEnvironmentWrite(t *testing.T) {
	t.Parallel()
	config := t.TempDir()
	environment := map[string]string{
		"XDG_CONFIG_HOME":    config,
		"NOVA_SPRINT_REDIS":  "store.example:6380",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}
	getenv := func(key string) string { return environment[key] }

	var out, errs strings.Builder
	call := &tool.Call{Ctx: context.Background(), Stdin: strings.NewReader(""),
		Stdout: &out, Stderr: &errs}
	res := writeSeatEnvironment(call, getenv)
	require.Equal(t, tool.OK, res.Status, out.String()+errs.String())

	path := filepath.Join(config, "nova-sprint", "seat-environment.json")
	_, err := os.ReadFile(path)
	require.NoError(t, err)

	// The switched writer's table form reads back equal through the dual reader.
	got, err := seatcred.LoadSeatEnvironment(path)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Version)
	assert.Equal(t, map[string]string{
		"NOVA_SPRINT_REDIS":  "store.example:6380",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}, got.Environment)

	// A legacy fixture of the same record still reads after the switch. The
	// dual reader enforces the per-user directory mode, so the fixture's
	// directory is sealed like the writer seals the record's.
	legacyDir := t.TempDir()
	require.NoError(t, os.Chmod(legacyDir, 0o700))
	legacyPath := filepath.Join(legacyDir, "seat-environment.json")
	legacy := `{"version":1,"environment":{"NOVA_SPRINT_REDIS":"store.example:6380","NOVA_SPRINT_SERVER":"sprint.example:6390","NOVA_BUS_REDIS":"bus.example:6381"}}`
	require.NoError(t, os.WriteFile(legacyPath, []byte(legacy), 0o600))
	legacyGot, err := seatcred.LoadSeatEnvironment(legacyPath)
	require.NoError(t, err)
	assert.Equal(t, got, legacyGot)

	// A value the table cannot hold is refused by name before a byte is written.
	_, err = seatEnvironmentTableJSON(seatcred.SeatEnvironment{Version: 2, Environment: environment})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot hold")
}
