package seatcred_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred/seattest"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// The test values the fixture seals. No refusal may print one.
const (
	resolveCoordPW = "resolve-test-coord-pw-3f9c"
	resolveBenchPW = "resolve-test-bench-pw-7d21"
	resolveGHTok   = "resolve-test-gh-token-90ab"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// onlySops is an environment holding nothing but SopsEnv, answered with the sops
// seattest.Home sealed with, so a parallel test reads its fixture without
// touching the process's environment.
func onlySops(t *testing.T) func(string) string {
	t.Helper()
	sops := seattest.Sops(t)
	return func(k string) string {
		if k == seatcred.SopsEnv {
			return sops
		}
		return ""
	}
}

// seatEnv is HOME under the fixture plus SopsEnv, and any extra variables.
func seatEnv(t *testing.T, home string, extra map[string]string) func(string) string {
	t.Helper()
	m := map[string]string{"HOME": home, seatcred.SopsEnv: seattest.Sops(t)}
	for k, v := range extra {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

// credHas says whether the secret holds want, without printing it.
func credHas(s secrets.Secret, want string) bool {
	got := false
	_ = s.Use(func(v string) error { got = v == want; return nil })
	return got
}

// hides asserts no test value appears in text.
func hides(t *testing.T, text string, values ...string) {
	t.Helper()
	for _, v := range values {
		assert.NotContains(t, text, v, "a refusal printed a value")
	}
}

// TestPathsForTakesTheOverridesOverHome: NOVA_SECRETS_STORE, NOVA_SECRETS_KEY
// and NOVA_SECRETS_SOPS win wherever they are set.
func TestPathsForTakesTheOverridesOverHome(t *testing.T) {
	t.Parallel()

	env := envOf(map[string]string{
		"HOME":            t.TempDir(),
		seatcred.StoreEnv: "/override/store",
		seatcred.KeyEnv:   "/override/studio.key",
		seatcred.SopsEnv:  "/override/sops",
	})
	p, err := seatcred.PathsFor("studio", env)
	require.NoError(t, err, "PathsFor = %+v %v; want the overrides", p, err)
	require.Equal(t, seatcred.Paths{Store: "/override/store", Key: "/override/studio.key", Sops: "/override/sops"}, p,
		"PathsFor = %+v %v; want the overrides", p, err)
}

// TestPathsForFallsBackToTheFleetLayoutUnderHome: with no overrides, the store
// and key are the fleet layout under HOME and sops is the one on PATH.
func TestPathsForFallsBackToTheFleetLayoutUnderHome(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	p, err := seatcred.PathsFor("studio", envOf(map[string]string{"HOME": home}))
	require.NoError(t, err, "PathsFor = %+v %v; want the layout under HOME", p, err)
	require.Equal(t, filepath.Join(home, seatcred.DefaultStore), p.Store, "PathsFor store = %q; want the layout under HOME", p.Store)
	require.Equal(t, filepath.Join(home, seatcred.DefaultKeyDir, "studio.key"), p.Key, "PathsFor key = %q; want the layout under HOME", p.Key)
	require.NotEmpty(t, p.Sops, "PathsFor sops = %q; want the sops on PATH", p.Sops)
}

// TestPathsForTakesBothOverridesWhenHomeIsUnset: with both store and key
// named, HOME is not needed and no sops on PATH is looked up.
func TestPathsForTakesBothOverridesWhenHomeIsUnset(t *testing.T) {
	t.Parallel()

	env := envOf(map[string]string{
		seatcred.StoreEnv: "/s",
		seatcred.KeyEnv:   "/k/studio.key",
		seatcred.SopsEnv:  "/sops",
	})
	p, err := seatcred.PathsFor("studio", env)
	require.NoError(t, err, "PathsFor with both overrides and no HOME = %+v %v", p, err)
	require.Equal(t, seatcred.Paths{Store: "/s", Key: "/k/studio.key", Sops: "/sops"}, p, "PathsFor with both overrides and no HOME = %+v %v", p, err)
}

// TestPathsForRefusesNoHomeWithoutBothOverrides: with HOME unset and the store
// or the key not overridden, the refusal names StoreEnv and KeyEnv.
func TestPathsForRefusesNoHomeWithoutBothOverrides(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		env  map[string]string
	}{
		{name: "no overrides"},
		{name: "store only", env: map[string]string{seatcred.StoreEnv: "/s"}},
		{name: "key only", env: map[string]string{seatcred.KeyEnv: "/k/studio.key"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := seatcred.PathsFor("studio", envOf(c.env))
			require.Error(t, err, "PathsFor %s = %v; want a refusal naming %s and %s", c.name, err, seatcred.StoreEnv, seatcred.KeyEnv)
			require.Contains(t, err.Error(), "seat studio", "PathsFor %s = %v", c.name, err)
			require.Contains(t, err.Error(), seatcred.StoreEnv, "PathsFor %s = %v; want a refusal naming %s", c.name, err, seatcred.StoreEnv)
			require.Contains(t, err.Error(), seatcred.KeyEnv, "PathsFor %s = %v; want a refusal naming %s", c.name, err, seatcred.KeyEnv)
		})
	}
}

// TestPathsForRefusesSopsAbsentFromPath: with no SopsEnv the binary is looked
// up on PATH; when it is not there the refusal names SopsEnv. The child has a
// PATH of one empty directory, so no sops is found.
func TestPathsForRefusesSopsAbsentFromPath(t *testing.T) {
	t.Parallel()
	if !inNoSopsChild(t) {
		return
	}
	_, err := seatcred.PathsFor("studio", envOf(map[string]string{"HOME": t.TempDir()}))
	require.Error(t, err, "PathsFor with no sops on PATH = %v; want a refusal naming %s", err, seatcred.SopsEnv)
	require.Contains(t, err.Error(), "seat studio", "PathsFor with no sops on PATH = %v", err)
	require.Contains(t, err.Error(), seatcred.SopsEnv, "PathsFor with no sops on PATH = %v; want a refusal naming %s", err, seatcred.SopsEnv)
}

// inNoSopsChild runs the calling test again in a child whose PATH names no
// sops and says whether this is that child.
func inNoSopsChild(t *testing.T) bool {
	t.Helper()
	const marker = "SEATCRED_NOSOPS_CHILD"
	if os.Getenv(marker) == t.Name() {
		return true
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = []string{marker + "=" + t.Name(), "PATH=" + t.TempDir(), "TMPDIR=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the no-sops child failed:\n%s", out)
	return false
}

// TestResolveRefusesASeatNameThatIsNotOne: a name that fails
// secrets.IsValidAsName is refused before any path is built.
func TestResolveRefusesASeatNameThatIsNotOne(t *testing.T) {
	t.Parallel()

	for _, seat := range []string{"", "../studio", "studio air", "a/b"} {
		_, err := seatcred.Resolve(seat, func(string) string { return "" })
		require.Error(t, err, "Resolve(%q) = %v; want the name refusal", seat, err)
		require.Contains(t, err.Error(), "must match [A-Za-z0-9_-]+", "Resolve(%q) = %v; want the name refusal", seat, err)
		hides(t, err.Error(), resolveCoordPW, resolveBenchPW)
	}
}

// TestResolveRefusesAStoreThatDoesNotExist: the store under HOME is absent, so
// the refusal names the seat and the store path, never a value.
func TestResolveRefusesAStoreThatDoesNotExist(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	_, err := seatcred.Resolve("studio", envOf(map[string]string{"HOME": home, seatcred.SopsEnv: seattest.Sops(t)}))
	require.Error(t, err, "Resolve with no store = %v; want a refusal naming the store", err)
	require.Contains(t, err.Error(), "seat studio", "Resolve with no store = %v", err)
	require.Contains(t, err.Error(), filepath.Join(home, seatcred.DefaultStore), "Resolve with no store = %v; want the store path", err)
	hides(t, err.Error(), resolveCoordPW, resolveBenchPW)
}

// TestResolveRefusesWhenHomeAndTheOverridesAreUnset: Resolve reaches the store
// through PathsFor, so with HOME unset and neither override set, the refusal a
// coordinator reads names the seat, StoreEnv and KeyEnv, never a value.
func TestResolveRefusesWhenHomeAndTheOverridesAreUnset(t *testing.T) {
	t.Parallel()

	_, err := seatcred.Resolve("studio", envOf(nil))
	require.Error(t, err, "Resolve with no HOME and no overrides = %v; want the path refusal", err)
	require.Contains(t, err.Error(), "seat studio", "Resolve with no HOME and no overrides = %v", err)
	require.Contains(t, err.Error(), seatcred.StoreEnv, "Resolve with no HOME and no overrides = %v; want %s", err, seatcred.StoreEnv)
	require.Contains(t, err.Error(), seatcred.KeyEnv, "Resolve with no HOME and no overrides = %v; want %s", err, seatcred.KeyEnv)
	hides(t, err.Error(), resolveCoordPW, resolveBenchPW)
}

// TestActiveResolvesThroughTheLookupFromArgsRecorded: a Selection FromArgs was
// given resolves on first use through that getenv, not this process's own
// environment, so the store its refusal names is the one under the fixture's
// HOME, never a real one.
func TestActiveResolvesThroughTheLookupFromArgsRecorded(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	s := new(seatcred.Selection)
	_, err := s.FromArgs([]string{"--seat=studio"}, envOf(map[string]string{"HOME": home, seatcred.SopsEnv: seattest.Sops(t)}))
	require.NoError(t, err, "FromArgs = %v; want studio selected", err)
	c, ok, err := s.Active()
	require.True(t, ok, "Active = %v %v %v; want the seat selected", c, ok, err)
	require.Error(t, err, "Active with no store = %v; want the store refusal through the recorded lookup", err)
	require.Contains(t, err.Error(), filepath.Join(home, seatcred.DefaultStore), "Active with no store = %v; want the store under the recorded HOME", err)
	hides(t, err.Error(), resolveCoordPW, resolveBenchPW)
}

// TestResolveReadsTheCoordinatorPasswordByUsersOrder: both passwords sealed,
// no UserEnv, and Users' first user -- coordinator -- is the login. The
// formatted Cred never prints the password.
func TestResolveReadsTheCoordinatorPasswordByUsersOrder(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{
		"NOVA_REDIS_COORDINATOR_PASSWORD": resolveCoordPW,
		"NOVA_REDIS_BENCH_PASSWORD":       resolveBenchPW,
	})
	c, err := seatcred.Resolve("studio", seatEnv(t, home, nil))
	require.NoError(t, err, "Resolve = %v %v; want coordinator", c, err)
	require.Equal(t, "studio", c.Seat, "Resolve = %v; want studio", c)
	require.Equal(t, "coordinator", c.User, "Resolve = %v; want Users' first, coordinator", c)
	require.Equal(t, seatcred.PasswordKey("coordinator"), c.Key, "Resolve = %v; want the coordinator key", c)
	require.True(t, credHas(c.Password, resolveCoordPW), "Resolve did not read the coordinator password")
	hides(t, fmt.Sprintf("%v %+v %#v %q", c, c, c, c), resolveCoordPW, resolveBenchPW)
}

// TestResolveFallsBackToTheBenchPasswordWhenOnlyItIsSealed: Users' order means
// a file holding only the bench password resolves as bench, no refusal.
func TestResolveFallsBackToTheBenchPasswordWhenOnlyItIsSealed(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "air", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": resolveBenchPW})
	c, err := seatcred.Resolve("air", seatEnv(t, home, nil))
	require.NoError(t, err, "Resolve = %v %v; want bench", c, err)
	require.Equal(t, "bench", c.User, "Resolve = %v; want Users' bench fallback", c)
	require.True(t, credHas(c.Password, resolveBenchPW), "Resolve did not read the bench password")
}

// TestResolveRefusesAUserTheSeatFileLacks: UserEnv naming a user whose key the
// file does not hold is refused, naming the key and the seal remedy, never a
// value.
func TestResolveRefusesAUserTheSeatFileLacks(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": resolveCoordPW})
	_, err := seatcred.Resolve("studio", seatEnv(t, home, map[string]string{seatcred.UserEnv: "ghost"}))
	require.Error(t, err, "Resolve for an absent user = %v; want a refusal naming the key", err)
	require.Contains(t, err.Error(), "seat studio", "Resolve for an absent user = %v", err)
	require.Contains(t, err.Error(), "NOVA_REDIS_GHOST_PASSWORD", "Resolve for an absent user = %v; want the key", err)
	require.Contains(t, err.Error(), "nova-secrets seal", "Resolve for an absent user = %v; want the seal remedy", err)
	hides(t, err.Error(), resolveCoordPW, resolveBenchPW)
}

// TestResolveRefusesAFileHoldingNoPassword: a file with no NOVA_REDIS_*
// password is refused, naming the seat, the file, the keys and the seal
// command, never a value.
func TestResolveRefusesAFileHoldingNoPassword(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{"GH_GATE_TOKEN": resolveGHTok})
	_, err := seatcred.Resolve("studio", seatEnv(t, home, nil))
	require.Error(t, err, "Resolve of a file with no password = %v; want a refusal naming the seat, file and seal command", err)
	require.Contains(t, err.Error(), "seat studio", "Resolve of a file with no password = %v", err)
	require.Contains(t, err.Error(), filepath.Join(home, seatcred.DefaultStore, "studio.yaml"), "Resolve of a file with no password = %v; want the file", err)
	require.Contains(t, err.Error(), "nova-secrets seal", "Resolve of a file with no password = %v; want the seal remedy", err)
	require.Contains(t, err.Error(), "NOVA_REDIS_COORDINATOR_PASSWORD", "Resolve of a file with no password = %v; want the keys", err)
	hides(t, err.Error(), resolveCoordPW, resolveBenchPW, resolveGHTok)
}

// profileFor builds the row a seat's fixture serves.
func profileFor(home string, ghEnv string) seatcred.Profile {
	return seatcred.Profile{
		Name:      "studio",
		Addr:      "redis.invalid:6380",
		User:      "coordinator",
		SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD",
		Store:     filepath.Join(home, seatcred.DefaultStore),
		Key:       filepath.Join(home, seatcred.DefaultKeyDir, "studio.key"),
		GitHubEnv: ghEnv,
	}
}

// TestResolveProfileReadsTheUsersPasswordWithoutAGitHubEnv: a row naming no
// token env resolves the Redis login alone.
func TestResolveProfileReadsTheUsersPasswordWithoutAGitHubEnv(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": resolveCoordPW})
	c, err := seatcred.ResolveProfile(profileFor(home, ""), onlySops(t))
	require.NoError(t, err, "ResolveProfile = %v %v; want the coordinator login", c, err)
	require.Equal(t, "coordinator", c.User, "ResolveProfile = %v; want the coordinator login", c)
	require.Empty(t, c.GitHubKey, "ResolveProfile = %v; want no token env named", c)
	require.NoError(t, c.GitHubErr, "ResolveProfile = %v; want no token refusal when none is named", c)
	require.True(t, credHas(c.Password, resolveCoordPW), "ResolveProfile did not read the coordinator password")
	hides(t, fmt.Sprintf("%v %+v %#v %q", c, c, c, c), resolveCoordPW)
}

// TestResolveProfileReadsTheGitHubTokenTheRowNames: the seventh column's key is
// read from the same open of the seat's file.
func TestResolveProfileReadsTheGitHubTokenTheRowNames(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{
		"NOVA_REDIS_COORDINATOR_PASSWORD": resolveCoordPW,
		"GH_GATE_TOKEN":                   resolveGHTok,
	})
	c, err := seatcred.ResolveProfile(profileFor(home, "GH_GATE_TOKEN"), onlySops(t))
	require.NoError(t, err, "ResolveProfile = %v %v; want the token", c, err)
	require.NoError(t, c.GitHubErr, "ResolveProfile = %v; want no token refusal", c)
	require.Equal(t, "GH_GATE_TOKEN", c.GitHubKey, "ResolveProfile = %v; want GitHubKey GH_GATE_TOKEN", c)
	require.True(t, credHas(c.GitHub, resolveGHTok), "ResolveProfile did not read the token")
	hides(t, fmt.Sprintf("%v %+v %#v %q", c, c, c, c), resolveCoordPW, resolveGHTok)
}

// TestResolveProfileKeepsTheLoginWhenTheTokenIsAbsent: a row naming a token
// env the file lacks sets GitHubErr and still returns the Redis login.
func TestResolveProfileKeepsTheLoginWhenTheTokenIsAbsent(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": resolveCoordPW})
	c, err := seatcred.ResolveProfile(profileFor(home, "GH_GHOST_TOKEN"), onlySops(t))
	require.NoError(t, err, "ResolveProfile with no token = %v, login err %v; want the login", c, err)
	require.Equal(t, "coordinator", c.User, "ResolveProfile with no token = %v; want the login", c)
	require.True(t, credHas(c.Password, resolveCoordPW), "ResolveProfile did not read the coordinator password")
	require.Error(t, c.GitHubErr, "ResolveProfile with no token: GitHubErr = %v; want a refusal naming the key", c.GitHubErr)
	require.Contains(t, c.GitHubErr.Error(), "GH_GHOST_TOKEN", "ResolveProfile with no token = %v; want the key", c.GitHubErr)
	require.Contains(t, c.GitHubErr.Error(), "nova-secrets seal", "ResolveProfile with no token = %v; want the seal remedy", c.GitHubErr)
	hides(t, c.GitHubErr.Error(), resolveCoordPW, resolveGHTok)
}

// TestResolveProfileRefusesAFileWithoutThePassword: the row's SecretEnv absent
// from the file is a refusal naming the key and the seal command, never a
// value.
func TestResolveProfileRefusesAFileWithoutThePassword(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": resolveBenchPW})
	_, err := seatcred.ResolveProfile(profileFor(home, ""), onlySops(t))
	require.Error(t, err, "ResolveProfile with no password = %v; want a refusal naming the key and remedy", err)
	require.Contains(t, err.Error(), "NOVA_REDIS_COORDINATOR_PASSWORD", "ResolveProfile with no password = %v; want the key", err)
	require.Contains(t, err.Error(), "nova-secrets seal", "ResolveProfile with no password = %v; want the seal remedy", err)
	hides(t, err.Error(), resolveBenchPW)
}
