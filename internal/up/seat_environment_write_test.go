package up_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpSeatEnvironmentWrite pins the writer switch (S4): writeSeatEnvironment
// writes the seat environment record in the table's schema JSON text form
// (S1, S2), what it writes reads back equal through the dual reader the reader
// card added, a legacy fixture of the same record still reads during the
// switch, and a value the table cannot hold exactly is refused by name before
// a byte is written, never written in the legacy form as a fallback.
func TestUpSeatEnvironmentWrite(t *testing.T) {
	t.Parallel()

	config := t.TempDir()
	path := filepath.Join(config, "nova-sprint", "seat-environment.json")
	environment := map[string]string{
		"XDG_CONFIG_HOME":    config,
		"NOVA_SPRINT_REDIS":  "store.example:6380",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}
	getenv := func(key string) string { return environment[key] }

	want := seatcred.SeatEnvironment{
		Version: 1,
		Environment: map[string]string{
			"NOVA_SPRINT_REDIS":  "store.example:6380",
			"NOVA_SPRINT_SERVER": "sprint.example:6390",
			"NOVA_BUS_REDIS":     "bus.example:6381",
		},
	}

	code, out, errs := newFake(t).novaWithEnv("linux", getenv, "--seat")
	require.Equal(t, 0, code, out+errs)
	got, err := seatcred.LoadSeatEnvironment(path)
	require.NoError(t, err)
	assert.Equal(t, want, got, "the switched writer's table form reads back equal through the dual reader")

	legacy := []byte(`{"version":1,"environment":{"NOVA_SPRINT_REDIS":"store.example:6380","NOVA_SPRINT_SERVER":"sprint.example:6390","NOVA_BUS_REDIS":"bus.example:6381"}}`)
	require.NoError(t, os.WriteFile(path, legacy, 0o600))
	got, err = seatcred.LoadSeatEnvironment(path)
	require.NoError(t, err)
	assert.Equal(t, want, got, "a legacy fixture of the same record still reads")

	refusalCfg := t.TempDir()
	refusalEnv := map[string]string{
		"XDG_CONFIG_HOME":    refusalCfg,
		"NOVA_SPRINT_REDIS":  "store\xff:6380",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}
	refusalGetenv := func(key string) string { return refusalEnv[key] }
	refusalPath := filepath.Join(refusalCfg, "nova-sprint", "seat-environment.json")
	code, out, errs = newFake(t).novaWithEnv("linux", refusalGetenv, "--seat")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "the table cannot hold")
	assert.NoFileExists(t, refusalPath)
}
