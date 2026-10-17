package up

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSeat runs nova-up --seat with the environment getenv names and returns
// its exit and streams.
func runSeat(getenv func(string) string, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := ToolWithEnv("test", nil, getenv).Run(append([]string{"--seat"}, args...), strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}

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
	path := filepath.Join(config, "nova-sprint", seatcred.EnvironmentFile)

	code, out, errs := runSeat(getenv)
	require.Equal(t, 0, code, out+errs)

	// The writer writes the table form, and the dual reader reads it back
	// equal (S2, C13).
	want := seatcred.SeatEnvironment{
		Version: 1,
		Environment: map[string]string{
			"NOVA_SPRINT_REDIS": "store.example:6380", "NOVA_SPRINT_SERVER": "sprint.example:6390", "NOVA_BUS_REDIS": "bus.example:6381",
		},
	}
	got, err := seatcred.LoadSeatEnvironment(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	// A legacy fixture of the same record still reads: the writer switch does
	// not break readers of the legacy form (S4).
	legacy := []byte("{\"version\":1,\"environment\":{\"NOVA_SPRINT_REDIS\":\"store.example:6380\",\"NOVA_SPRINT_SERVER\":\"sprint.example:6390\",\"NOVA_BUS_REDIS\":\"bus.example:6381\"}}\n")
	legacyPath := filepath.Join(t.TempDir(), "nova-sprint", seatcred.EnvironmentFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o700))
	require.NoError(t, os.WriteFile(legacyPath, legacy, 0o600))
	legacyGot, err := seatcred.LoadSeatEnvironment(legacyPath)
	require.NoError(t, err)
	assert.Equal(t, want, legacyGot)

	// A value the table cannot hold (a version other than 1) is refused by
	// name before a byte is written, and is never written in the legacy form
	// as a fallback.
	_, err = seatEnvironmentTableJSON(seatcred.SeatEnvironment{Version: 2, Environment: map[string]string{"NOVA_BUS_REDIS": "127.0.0.1:6390"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clamped (version)")
}
