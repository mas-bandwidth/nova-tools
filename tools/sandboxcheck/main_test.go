package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox/darwincheck"
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

func (stopper) Stop() {}

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
		if code != 0 || !strings.HasPrefix(out, "usage: sandboxcheck ") || len(sys.ran) != 0 {
			t.Errorf("%s: exit %d, ran %d: %q", flag, code, len(sys.ran), out)
		}
	}
}

func TestABadFlagOrAnArgumentIsRefusedWithUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--bogus"}, {"extra"}} {
		code, out, errOut, sys := invoke(t, args, env())
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: sandboxcheck ") || len(sys.ran) != 0 {
			t.Errorf("%v: exit %d out %q err %q", args, code, out, errOut)
		}
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
		if code != 0 {
			t.Errorf("%s: exit %d:\n%s", name, code, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "@@") && !strings.HasPrefix(l, ";;") {
				t.Errorf("%s: an unfilled marker: %s", name, l)
			}
		}
		for _, want := range []string{`(param "READ0")`, `(param "WRITE0")`, `(literal "/private/var/run/mDNSResponder")`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the dump lacks %s", name, want)
			}
		}
		if strings.Contains(out, "CHECK ") {
			t.Errorf("%s: a dump ran checks:\n%s", name, out)
		}
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
		tc := tc
		args := append(append([]string{}, tc.args...), "--scratch", scratch(t))
		_, out, _, _ := invoke(t, args, tc.env)
		if got := strings.Contains(out, "CHECK SKIP name=dns_resolves reason=no_network"); got != tc.skip {
			t.Errorf("%s: skip = %v, want %v:\n%s", name, got, tc.skip, out)
		}
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
	_, _, _, sys := invoke(t, []string{"--no-network", "--scratch", scratch(t), "--fill", "/tmp/from-flag"}, env())
	if g := generators(sys); len(g) != 1 || g[0] != "/tmp/from-flag" {
		t.Errorf("flag: generators %v", g)
	}
	_, _, _, sys = invoke(t, []string{"--no-network", "--scratch", scratch(t)}, env("NOVA_SANDBOX_FILL", "/tmp/from-env"))
	if g := generators(sys); len(g) != 1 || g[0] != "/tmp/from-env" {
		t.Errorf("variable: generators %v", g)
	}
	_, _, _, sys = invoke(t, []string{"--no-network", "--scratch", scratch(t), "--fill", "/tmp/from-flag"}, env("NOVA_SANDBOX_FILL", "/tmp/from-env"))
	if g := generators(sys); len(g) != 1 || g[0] != "/tmp/from-flag" {
		t.Errorf("both: generators %v", g)
	}
	_, _, _, sys = invoke(t, []string{"--no-network", "--scratch", scratch(t)}, env())
	if g := generators(sys); len(g) != 0 {
		t.Errorf("neither: generators %v", g)
	}
}

func TestTheScratchFlagAndVariableNameWhereTheTreeIs(t *testing.T) {
	t.Parallel()
	flagDir, envDir := scratch(t), scratch(t)
	_, _, _, sys := invoke(t, []string{"--dump-profile", "--scratch", flagDir}, env("NOVA_CHECK_SCRATCH", envDir))
	if len(sys.ran) == 0 || !strings.HasPrefix(sys.ran[0].Args[len(sys.ran[0].Args)-1], evalDir(t, flagDir)) {
		t.Errorf("the flag's scratch was not used: %v", sys.ran)
	}
	_, _, _, sys = invoke(t, []string{"--dump-profile"}, env("NOVA_CHECK_SCRATCH", envDir))
	if len(sys.ran) == 0 || !strings.HasPrefix(sys.ran[0].Args[len(sys.ran[0].Args)-1], evalDir(t, envDir)) {
		t.Errorf("the variable's scratch was not used: %v", sys.ran)
	}
}

// evalDir is a directory's resolved spelling, which the check uses for every path.
func evalDir(t *testing.T, dir string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, filepath.Base(dir))
}

func TestNotDarwinIsOneFailLine(t *testing.T) {
	t.Parallel()
	sys := &recordingSystem{goos: "linux"}
	var out, errb bytes.Buffer
	code := run(nil, env(), &out, &errb, sys)
	if code != 1 || !strings.HasPrefix(out.String(), "CHECK FAIL name=platform") || len(sys.ran) != 0 {
		t.Errorf("exit %d: %q", code, out.String())
	}
}
