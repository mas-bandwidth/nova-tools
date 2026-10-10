package up

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeMachine struct {
	home string
}

func (f fakeMachine) machine(goos string) func() (Machine, error) {
	return func() (Machine, error) {
		return Machine{GOOS: goos, Home: f.home, UID: 501, Exec: f,
			Now:  func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
			Rand: strings.NewReader(strings.Repeat("0123456789abcdef", 64))}, nil
	}
}

func (f fakeMachine) LookPath(name string) (string, error) {
	return "/fake/bin/" + name, nil
}

func (f fakeMachine) Run(ctx context.Context, c Cmd) (string, error) {
	return "", nil
}

func TestUpJevStepPlansAndApplies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seatEnv := filepath.Join(root, "seat.env")
	require.NoError(t, os.WriteFile(seatEnv, []byte("NOVA_SPRINT_REDIS=mem://twin\n"), 0o644))

	fake := fakeMachine{home: root}
	m, _ := fake.machine("linux")()
	env := &Env{Machine: m, Root: root}
	finding := planJev(env)
	assert.Equal(t, OK, finding.State)

	err := applyJev(env)
	require.NoError(t, err)
}

func TestUpJevStepPlansOKWhenPresent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seatEnv := filepath.Join(root, "seat.env")
	require.NoError(t, os.WriteFile(seatEnv, []byte("NOVA_SPRINT_REDIS=mem://twin\nJEV_API_KEY=\n"), 0o644))

	fake := fakeMachine{home: root}
	m, _ := fake.machine("linux")()
	env := &Env{Machine: m, Root: root}
	finding := planJev(env)
	assert.Equal(t, OK, finding.State)
}

func TestUpJevStepPlansOKWhenNoSeatEnv(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	fake := fakeMachine{home: root}
	m, _ := fake.machine("linux")()
	env := &Env{Machine: m, Root: root}
	finding := planJev(env)
	assert.Equal(t, OK, finding.State)
}

func TestUpJevStepIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seatEnv := filepath.Join(root, "seat.env")
	require.NoError(t, os.WriteFile(seatEnv, []byte("NOVA_SPRINT_REDIS=mem://twin\n"), 0o644))

	fake := fakeMachine{home: root}
	m, _ := fake.machine("linux")()
	env := &Env{Machine: m, Root: root}

	err := applyJev(env)
	require.NoError(t, err)
	b1, _ := os.ReadFile(seatEnv)

	err = applyJev(env)
	require.NoError(t, err)
	b2, _ := os.ReadFile(seatEnv)

	assert.Equal(t, string(b1), string(b2), "second apply should not change file")
}
