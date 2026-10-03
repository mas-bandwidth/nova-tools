package seatcred_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
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
			require.Contains(t, err.Error(), "--seat wants a seat name", "%v: err %v, want a refusal naming --seat", c.args, err)
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

// TestResolveReadsTheSeatThroughTheSecretsLibrary is the resolution half of
// #4052: a real store, sealed by sops to a fresh age key, read from the
// default layout under HOME with nothing in the environment but HOME and PATH.
func TestResolveReadsTheSeatThroughTheSecretsLibrary(t *testing.T) {
	t.Parallel()

	const coord, bench = "coord-test-pw-0f3a9c", "bench-test-pw-7d21e4"
	home := seattest.Home(t, "studio", map[string]string{
		"NOVA_REDIS_COORDINATOR_PASSWORD": coord,
		"NOVA_REDIS_BENCH_PASSWORD":       bench,
		"GH_TOKEN":                        "gh-test-value-not-redis",
	})
	mockEnv := map[string]string{
		"HOME":           home,
		seatcred.SopsEnv: seattest.Sops(t),
	}
	getenv := func(k string) string { return mockEnv[k] }

	c, err := seatcred.Resolve("studio", getenv)
	require.NoError(t, err, "Resolve: %v", err)
	require.Equal(t, "studio", c.Seat, "Resolve = %v; want studio as coordinator with the coordinator password", c)
	require.Equal(t, "coordinator", c.User, "Resolve = %v; want studio as coordinator with the coordinator password", c)
	require.Equal(t, "NOVA_REDIS_COORDINATOR_PASSWORD", c.Key, "Resolve = %v; want studio as coordinator with the coordinator password", c)
	require.True(t, same(c, coord), "Resolve = %v; want studio as coordinator with the coordinator password", c)
	for _, s := range []string{c.String(), fmt.Sprintf("%v %+v %#v %s %q %x", c, c, c, c, c, c)} {
		require.NotContains(t, s, coord, "a formatted Cred carries the password: %s", s)
		require.NotContains(t, s, coord[:8], "a formatted Cred carries the password: %s", s)
	}

	mockEnv[seatcred.UserEnv] = "bench"
	c, err = seatcred.Resolve("studio", getenv)
	require.NoError(t, err, "Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	require.Equal(t, "bench", c.User, "Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	require.True(t, same(c, bench), "Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	mockEnv[seatcred.UserEnv] = "ghost"
	_, err = seatcred.Resolve("studio", getenv)
	require.Error(t, err, "Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	require.Contains(t, err.Error(), "NOVA_REDIS_GHOST_PASSWORD", "Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	require.Contains(t, err.Error(), "nova-secrets seal", "Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	mockEnv[seatcred.UserEnv] = ""

	_, err = seatcred.Resolve("nobody", getenv)
	require.Error(t, err, "Resolve of an absent seat = %v; want a refusal naming it", err)
	require.Contains(t, err.Error(), "seat nobody", "Resolve of an absent seat = %v; want a refusal naming it", err)
	_, err = seatcred.Resolve("../x", getenv)
	require.Error(t, err, "Resolve accepted a seat name with a path in it")
	for _, kv := range os.Environ() {
		require.NotContains(t, kv, coord, "a password entered this process's environment: %s", strings.SplitN(kv, "=", 2)[0])
		require.NotContains(t, kv, bench, "a password entered this process's environment: %s", strings.SplitN(kv, "=", 2)[0])
	}
}

func TestActiveResolvesOnceAndOnlyWhenSelected(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "air", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": "air-bench-test-pw-11"})
	mockEnv := map[string]string{
		"HOME":           home,
		seatcred.SopsEnv: seattest.Sops(t),
	}
	getenv := func(k string) string { return mockEnv[k] }
	var s seatcred.Selection
	_, ok, _ := s.Active()
	require.False(t, ok, "Active with no seat selected reported one")
	s.SelectWith("air", "", func(seat string) (seatcred.Cred, error) { return seatcred.Resolve(seat, getenv) })
	c, ok, err := s.Active()
	require.True(t, ok, "Active = %v %v %v; want air as bench", c, ok, err)
	require.NoError(t, err, "Active = %v %v %v; want air as bench", c, ok, err)
	require.Equal(t, "bench", c.User, "Active = %v %v %v; want air as bench", c, ok, err)
	require.True(t, same(c, "air-bench-test-pw-11"), "Active = %v %v %v; want air as bench", c, ok, err)
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

func same(c seatcred.Cred, want string) bool {
	eq := false
	_ = c.Password.Use(func(v string) error { eq = v == want; return nil })
	return eq
}
