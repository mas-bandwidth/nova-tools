package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DeniedWrites is the lane wall's deny list with ~/ made home (profile.go:243). A path
// that is already absolute is untouched; a path spelled through a symlink keeps that
// spelling, because a deny path is a claim about the path the wall must not grant, and the
// resolved form would hide the link the caller named.
func TestDeniedWritesExpandsHomeAndKeepsAbsolutePaths(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	got := DeniedWrites(home, []string{"~/coordinator", "/abs/path", "~/identity/"})
	want := []string{filepath.Join(home, "coordinator"), "/abs/path", filepath.Join(home, "identity")}
	assert.Equal(t, want, got, "~/ became the absolute home prefix and absolute paths passed through")
}

func TestDeniedWritesWithNoHomeDropsHomeRelativeOnly(t *testing.T) {
	t.Parallel()

	got := DeniedWrites("", []string{"~/coordinator", "/abs/path"})
	assert.Equal(t, []string{"/abs/path"}, got, "without a home, the ~/ entries are dropped and the absolute ones stand")
}

func TestDeniedWritesKeepsASymlinkedHomeSpelling(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(root, "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symlink here (%v); the deny path's symlinked spelling cannot be asserted", err)
	}

	got := DeniedWrites(link, []string{"~/self"})
	require.Len(t, got, 1)
	assert.True(t, filepath.IsAbs(got[0]), "the deny path became absolute: %q", got[0])
	assert.Contains(t, got[0], link, "the deny path keeps the symlinked home spelling, not the resolved one: %q", got[0])
}

// A lane profile refuses a name that is not a known profile, a missing working directory,
// and a list that denies nothing.
func TestLaneProfileInputRefusesAnUnknownName(t *testing.T) {
	t.Parallel()

	_, err := (LaneProfile{Name: "not-a-profile", Work: "/w", Deny: []string{"~/x"}}).Input("/w", []string{"sh"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no wall profile")
}

func TestLaneProfileInputRefusesNoWorkingDirectory(t *testing.T) {
	t.Parallel()

	_, err := (LaneProfile{Deny: []string{"~/x"}}).Input("", []string{"sh"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "working directory")
}

func TestLaneProfileInputRefusesDenyingNothing(t *testing.T) {
	t.Parallel()

	_, err := (LaneProfile{Work: "/w"}).Input("/w", []string{"sh"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denies nothing")
}

// A valid friend profile (the default name) produces a wall input: writes to the working
// directory and the config directory, the lane's ports, an enforced network denial, and
// HOME under the config directory.
func TestLaneProfileInputBuildsTheFriendWall(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	work := filepath.Join(home, "work")
	require.NoError(t, os.MkdirAll(work, 0o755))
	cfg := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(cfg, 0o755))

	lp := LaneProfile{
		Work:      work,
		ConfigDir: cfg,
		Reads:     []string{"/opt/data"},
		Deny:      []string{"~/coordinator", "/abs/path"},
		Home:      home,
	}
	wantDeny := []string{filepath.Join(home, "coordinator"), "/abs/path"}

	in, err := lp.Input(work, []string{"sh", "-c", "true"})
	require.NoError(t, err)
	assert.True(t, in.NetDeny, "a lane's wall enforces a network denial")
	assert.Equal(t, LaneNetPorts, in.NetPorts, "the lane's ports open outbound")
	assert.Equal(t, wantDeny, in.Deny, "the deny list reached the wall with ~/ made home")
	assert.Equal(t, cfg, in.Home, "the wall's HOME is the config directory")
	assert.Equal(t, work, in.Cwd, "the cwd inside the write set is the command's working directory")
	assert.Contains(t, in.Writes, work)
	assert.Contains(t, in.Writes, cfg)
	assert.Equal(t, []string{"/opt/data"}, in.Reads)
	assert.Equal(t, []string{"sh", "-c", "true"}, in.Argv)
}
