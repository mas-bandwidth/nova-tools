package seatcred_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
)

func TestFromArgsTakesTheSeatFlagOrTheEnvironment(t *testing.T) {
	t.Parallel()

	t.Cleanup(func() { seatcred.Process().Select("") })
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == seatcred.SeatEnv {
				return v
			}
			return ""
		}
	}
	for _, c := range []struct {
		args     []string
		env      string
		seat     string
		rest     []string
		refusing bool
	}{
		{args: []string{"table", "--seat", "studio", "--redis", "h:1"}, seat: "studio", rest: []string{"table", "--redis", "h:1"}},
		{args: []string{"table", "--seat=studio"}, env: "other", seat: "studio", rest: []string{"table"}},
		{args: []string{"table", "-seat", "air"}, seat: "air", rest: []string{"table"}},
		{args: []string{"table"}, env: "studio", seat: "studio", rest: []string{"table"}},
		{args: []string{"table"}, seat: "", rest: []string{"table"}},
		{args: []string{"refresh", "--", "x", "--seat", "s"}, seat: "", rest: []string{"refresh", "--", "x", "--seat", "s"}},
		{args: []string{"table", "--seat"}, refusing: true},
		{args: []string{"table", "--seat", "--redis", "h:1"}, refusing: true},
		{args: []string{"table", "--seat="}, refusing: true},
	} {
		rest, err := seatcred.FromArgs(c.args, env(c.env))
		if c.refusing {
			require.Error(t, err, "%v: err %v, want a refusal naming --seat", c.args, err)
			require.Contains(t, err.Error(), "--seat <name>", "%v: err %v, want a refusal naming --seat", c.args, err)
			continue
		}
		require.NoError(t, err, "%v env=%q: rest %v seat %q err %v; want %v %q", c.args, c.env, rest, seatcred.Process().Selected(), err, c.rest, c.seat)
		require.Equal(t, c.rest, rest, "%v env=%q: rest %v seat %q err %v; want %v %q", c.args, c.env, rest, seatcred.Process().Selected(), err, c.rest, c.seat)
		require.Equal(t, c.seat, seatcred.Process().Selected(), "%v env=%q: rest %v seat %q err %v; want %v %q", c.args, c.env, rest, seatcred.Process().Selected(), err, c.rest, c.seat)
	}
}

func TestPasswordKeyNamesTheUsersVariable(t *testing.T) {
	t.Parallel()

	got := seatcred.PasswordKey("coordinator")
	require.Equal(t, "NOVA_REDIS_COORDINATOR_PASSWORD", got, "PasswordKey = %s", got)
}

// inHermeticChild runs the calling test again in a child of this test binary whose
// environment is only a fresh temp HOME and PATH, and says whether this is that child.
// A test that resolves through the process's own environment (a Selection with no
// lookup) does it in the child, so the store it looks for is under that temp HOME: no
// test reads, lists or stats a real store. The parent fails with the child's output.
func inHermeticChild(t *testing.T) bool {
	t.Helper()
	const marker = "SEATCRED_HERMETIC_CHILD"
	if os.Getenv(marker) == t.Name() {
		return true
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	// The missing store is refused before sops runs; the test binary supplies an inert path.
	cmd.Env = []string{marker + "=" + t.Name(), "HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), seatcred.SopsEnv + "=" + os.Args[0]}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the hermetic child failed:\n%s", out)
	return false
}

// A Selection given no resolver and no lookup resolves through the process's own
// environment: the store it looks for is the one under the process's HOME.
func TestDefaultResolverReadsTheProcessEnvironment(t *testing.T) {
	t.Parallel()
	if !inHermeticChild(t) {
		return
	}
	s := new(seatcred.Selection)
	s.Select("nonexistent-seat-probe-4717")
	_, ok, err := s.Active()
	require.True(t, ok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat nonexistent-seat-probe-4717: store "+filepath.Join(os.Getenv("HOME"), seatcred.DefaultStore)+" is not a directory",
		"the default resolver did not look for the store under the process's own HOME")
	assert.NotContains(t, err.Error(), "HOME is unset")
}

// Select and SelectWith clear the lookup FromArgs recorded, so Active then resolves
// through the process's own environment: in the hermetic child (inHermeticChild).
func TestSelectClearsLookupFromArgs(t *testing.T) {
	t.Parallel()
	if !inHermeticChild(t) {
		return
	}

	s := new(seatcred.Selection)
	called := false
	customLookup := func(k string) string {
		called = true
		return ""
	}
	_, err := s.FromArgs([]string{"--seat=nonexistent-seat"}, customLookup)
	require.NoError(t, err)
	called = false
	_, _, _ = s.Active()
	require.True(t, called, "Active did not use lookup recorded by FromArgs")

	// Select must clear it:
	s.Select("nonexistent-seat-2")
	called = false
	_, _, _ = s.Active()
	assert.False(t, called, "Select did not clear lookup recorded by FromArgs; custom lookup was still called")

	// FromArgs records lookup again:
	_, err = s.FromArgs([]string{"--seat=nonexistent-seat"}, customLookup)
	require.NoError(t, err)
	called = false
	_, _, _ = s.Active()
	require.True(t, called, "Active did not use lookup recorded by FromArgs")

	// SelectWith must clear it:
	s.SelectWith("nonexistent-seat-3", "", nil)
	called = false
	_, _, _ = s.Active()
	assert.False(t, called, "SelectWith did not clear lookup recorded by FromArgs; custom lookup was still called")
}

func TestSeatRefusalNamesNoMachine(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"table", "--seat"},
		{"table", "--seat="},
		{"table", "-seat"},
		{"table", "-seat="},
	} {
		_, err := seatcred.FromArgs(args, nil)
		require.Error(t, err)
		assert.Equal(t, "--seat <name>, for example --seat bench-a", err.Error())
		for _, name := range []string{
			"alex", "antman", "batman", "captainamerica", "emma",
			"freddy", "glenn", "hetzner", "hulk", "johnny",
			"macbook", "mas-bandwidth", "mini", "rowan", "space",
			"spacegame", "stella", "studio", "superman", "vision",
		} {
			assert.NotContains(t, err.Error(), name)
		}
	}
}
