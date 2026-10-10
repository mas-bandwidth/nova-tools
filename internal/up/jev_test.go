package up_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpJevStepPlansAndApplies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seatEnv := filepath.Join(root, "seat.env")
	require.NoError(t, os.WriteFile(seatEnv, []byte("NOVA_SPRINT_REDIS=mem://twin\n"), 0o644))

	// Plan should create
	fake := fakeMachine{home: root}
	env := &Env{Machine: fake.machine("linux"), Root: root}
	finding := planJev(env)
	assert.Equal(t, Create, finding.State)

	// Apply
	err := applyJev(env)
	require.NoError(t, err)

	// Verify
	b, err := os.ReadFile(seatEnv)
	require.NoError(t, err)
	assert.Contains(t, string(b), "JEV_API_KEY")
}

func TestUpJevStepPlansOKWhenPresent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seatEnv := filepath.Join(root, "seat.env")
	require.NoError(t, os.WriteFile(seatEnv, []byte("NOVA_SPRINT_REDIS=mem://twin\nJEV_API_KEY=\n"), 0o644))

	fake := fakeMachine{home: root}
	env := &Env{Machine: fake.machine("linux"), Root: root}
	finding := planJev(env)
	assert.Equal(t, OK, finding.State)
}

func TestUpJevStepPlansMissingWhenNoSeatEnv(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	fake := fakeMachine{home: root}
	env := &Env{Machine: fake.machine("linux"), Root: root}
	finding := planJev(env)
	assert.Equal(t, Missing, finding.State)
}

func TestUpJevStepIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seatEnv := filepath.Join(root, "seat.env")
	require.NoError(t, os.WriteFile(seatEnv, []byte("NOVA_SPRINT_REDIS=mem://twin\n"), 0o644))

	fake := fakeMachine{home: root}
	env := &Env{Machine: fake.machine("linux"), Root: root}

	// First apply
	err := applyJev(env)
	require.NoError(t, err)
	b1, _ := os.ReadFile(seatEnv)

	// Second apply should be no-op
	err = applyJev(env)
	require.NoError(t, err)
	b2, _ := os.ReadFile(seatEnv)

	assert.Equal(t, string(b1), string(b2), "second apply should not change file")
}
