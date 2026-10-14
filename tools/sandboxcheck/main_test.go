package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox/darwincheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSystem is a darwin whose processes all succeed and are recorded, so the
// tool's flags and environment variables are held by what reaches the check and
// not by a wall.
type recordingSystem struct {
	darwincheck.OSSystem
	goos string
	ran  []darwincheck.Spec
}

func (r *recordingSystem) GOOS() string { return r.goos }
func (r *recordingSystem) LookPath(name string) (string, bool) {
	return "/usr/bin/" + name, true
}
func (r *recordingSystem) Run(s darwincheck.Spec) darwincheck.Result {
	r.ran = append(r.ran, s)
	return darwincheck.Result{}
}
func (r *recordingSystem) Start(darwincheck.Spec) (darwincheck.Process, error) {
	return stopper{}, nil
}
func (r *recordingSystem) IsSocket(string) bool { return true }
func (r *recordingSystem) Sleep(time.Duration)  {}

type stopper struct{}

func (stopper) Stop() error { return nil }

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func invoke(t *testing.T, args []string, getenv func(string) string) (int, string, string, *recordingSystem) {
	t.Helper()
	sys := &recordingSystem{goos: "darwin"}
	var out, errb bytes.Buffer
	code := run(args, getenv, &out, &errb, sys)
	return code, out.String(), errb.String(), sys
}

func scratch(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "check")
}

func TestHelpPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "--help"} {
		code, out, _, sys := invoke(t, []string{flag}, env())
		assert.Equal(t, 0, code, "%s: exit", flag)
		assert.True(t, strings.HasPrefix(out, "usage: sandboxcheck "), "%s: %q", flag, out)
		assert.Empty(t, sys.ran, "%s: the check ran", flag)
	}
}

func TestABadFlagOrAnArgumentIsRefusedWithUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--bogus"}, {"extra"}} {
		code, out, errOut, sys := invoke(t, args, env())
		assert.Equal(t, 2, code, "%v: exit", args)
		assert.Empty(t, out, "%v: stdout", args)
		assert.Contains(t, errOut, "usage: sandboxcheck ", "%v", args)
		assert.Empty(t, sys.ran, "%v: the check ran", args)
	}
}

func TestDumpProfilePrintsTheFilledEmbeddedTemplate(t *testing.T) {
	t.Parallel()
	dir := scratch(t)
	for name, tc := range map[string]struct {
		args []string
		env  func(string) string
	}{
		"flag":     {[]string{"--dump-profile", "--scratch", dir}, env()},
		"variable": {nil, env("NOVA_CHECK_DUMP_PROFILE", "1", "NOVA_CHECK_SCRATCH", dir)},
	} {
		code, out, _, _ := invoke(t, tc.args, tc.env)
		require.Equal(t, 0, code, "%s:\n%s", name, out)
		for _, l := range strings.Split(out, "\n") {
			assert.False(t, strings.Contains(l, "@@") && !strings.HasPrefix(l, ";;"), "%s: an unfilled marker: %s", name, l)
		}
		for _, want := range []string{`(param "READ0")`, `(param "WRITE0")`, `(literal "/private/var/run/mDNSResponder")`} {
			assert.Contains(t, out, want, "%s", name)
		}
		assert.NotContains(t, out, "CHECK ", "%s: a dump ran checks", name)
	}
}

func TestNoNetworkFromTheFlagAndFromTheVariable(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		args []string
		env  func(string) string
		skip bool
	}{
		"flag":           {[]string{"--no-network"}, env(), true},
		"variable":       {nil, env("NOVA_CHECK_NO_NETWORK", "1"), true},
		"variable not 1": {nil, env("NOVA_CHECK_NO_NETWORK", "yes"), false},
		"neither":        {nil, env(), false},
	} {
		args := append(append([]string{}, tc.args...), "--scratch", scratch(t))
		_, out, _, _ := invoke(t, args, tc.env)
		assert.Equal(t, tc.skip, strings.Contains(out, "CHECK SKIP name=dns_resolves reason=no_network"), "%s:\n%s", name, out)
	}
}

func TestFillFromTheFlagAndFromTheVariableAndTheFlagWins(t *testing.T) {
	t.Parallel()
	generators := func(sys *recordingSystem) []string {
		var g []string
		for _, s := range sys.ran {
			if len(s.Args) > 0 && s.Args[0] == "policy" {
				g = append(g, s.Name)
			}
		}
		return g
	}
	for name, tc := range map[string]struct {
		args []string
		env  func(string) string
		want []string
	}{
		"flag":     {[]string{"--no-network", "--scratch", scratch(t), "--fill", "/tmp/from-flag"}, env(), []string{"/tmp/from-flag"}},
		"variable": {[]string{"--no-network", "--scratch", scratch(t)}, env("NOVA_SANDBOX_FILL", "/tmp/from-env"), []string{"/tmp/from-env"}},
		"both":     {[]string{"--no-network", "--scratch", scratch(t), "--fill", "/tmp/from-flag"}, env("NOVA_SANDBOX_FILL", "/tmp/from-env"), []string{"/tmp/from-flag"}},
		"neither":  {[]string{"--no-network", "--scratch", scratch(t)}, env(), nil},
	} {
		_, _, _, sys := invoke(t, tc.args, tc.env)
		assert.Equal(t, tc.want, generators(sys), "%s", name)
	}
}

func TestTheScratchFlagAndVariableNameWhereTheTreeIs(t *testing.T) {
	t.Parallel()
	flagDir, envDir := scratch(t), scratch(t)
	for name, tc := range map[string]struct {
		args []string
		env  func(string) string
		dir  string
	}{
		"flag":     {[]string{"--dump-profile", "--scratch", flagDir}, env("NOVA_CHECK_SCRATCH", envDir), flagDir},
		"variable": {[]string{"--dump-profile"}, env("NOVA_CHECK_SCRATCH", envDir), envDir},
	} {
		_, _, _, sys := invoke(t, tc.args, tc.env)
		require.NotEmpty(t, sys.ran, "%s", name)
		assert.True(t, strings.HasPrefix(sys.ran[0].Args[len(sys.ran[0].Args)-1], evalDir(t, tc.dir)), "%s: %v", name, sys.ran)
	}
}

// evalDir is a directory's resolved spelling, which the check uses for every path.
func evalDir(t *testing.T, dir string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	require.NoError(t, err)
	return filepath.Join(parent, filepath.Base(dir))
}

func TestNotDarwinIsOneFailLine(t *testing.T) {
	t.Parallel()
	sys := &recordingSystem{goos: "linux"}
	var out, errb bytes.Buffer
	code := run(nil, env(), &out, &errb, sys)
	assert.Equal(t, 1, code, "exit: %q", out.String())
	assert.True(t, strings.HasPrefix(out.String(), "CHECK FAIL name=platform"), "%q", out.String())
	assert.Empty(t, sys.ran)
}
