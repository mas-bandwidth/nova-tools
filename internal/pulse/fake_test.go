package pulse

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// The fakes every test in this package puts in front of PATH.
//
// They used to be `#!/bin/sh` scripts. A shell script is not an executable on Windows, so
// the PATH lookup walked straight past them and the REAL gh, git and nova-swarm answered:
// on the hosted windows-latest leg that is `gh auth`, exit 4, and a refusal the test never
// asked for. A test must never invoke the real gh, on any platform, so the fixture is a Go
// program (testdata/fakebin) built once per test binary and copied into a shared bin
// directory under the five names nova-pulse starts.
//
// What each of them DOES is per test: the spec directory in NOVA_PULSE_FAKE_DIR is the
// test's own, so two benches in one test still answer differently, exactly as two bin
// directories on PATH used to.

// fakeRule is one arm of a fake's behaviour: `Arg` indexes os.Args the way a shell
// fixture's $1 and $2 did, and `Arg: 0` matches every invocation.
type fakeRule struct {
	Arg        int    `json:"arg,omitempty"`
	Equals     string `json:"equals,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	StdoutFile string `json:"stdoutFile,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Exit       int    `json:"exit,omitempty"`
}

// fakeSpec is one fake program: where it records its argv, the arms it answers, and the
// arm for everything else.
type fakeSpec struct {
	Log     string     `json:"log,omitempty"`
	Rules   []fakeRule `json:"rules,omitempty"`
	Default fakeRule   `json:"default"`
}

// fakeTools are the names the shared bin directory answers to: every program nova-pulse
// starts. A name with no spec in the test's directory exits 97 and says so, which is how a
// test proves the fake ran and the real tool did not.
var fakeTools = []string{"gh", "git", "nova-bus", "nova-pulse", "nova-swarm", "nova-merge", "systemctl", "nova-wake"}

var (
	fakeRoot    string // the one directory outside t.TempDir(), owned by TestMain
	fakeBinOnce sync.Once
	fakeBinDir  string
	fakeBinErr  error
)

// TestMain owns the shared bin directory: it has to outlive the first test that asks for
// it and be gone when the process is, including on a failing run.
func TestMain(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "nova-pulse-fakes-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "the fake PATH directory: %v\n", err)
			return 2
		}
		defer os.RemoveAll(dir)
		fakeRoot = dir
		return m.Run()
	}())
}

// fakeBins builds testdata/fakebin ONCE for the test binary and copies it under every name
// in fakeTools. One build, not one per test: the two-minute rule is a rule.
func fakeBins(t *testing.T) string {
	t.Helper()
	fakeBinOnce.Do(func() {
		build := filepath.Join(fakeRoot, "build")
		if err := os.MkdirAll(build, 0o755); err != nil {
			fakeBinErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", build, "./testdata/fakebin")
		cmd.Env = goenv.Clean(os.Environ())
		if raw, err := cmd.CombinedOutput(); err != nil {
			fakeBinErr = fmt.Errorf("building the fake: %v\n%s", err, raw)
			return
		}
		built := filepath.Join(build, "fakebin"+exeSuffix())
		raw, err := os.ReadFile(built)
		if err != nil {
			fakeBinErr = err
			return
		}
		bin := filepath.Join(fakeRoot, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			fakeBinErr = err
			return
		}
		for _, name := range fakeTools {
			if err := os.WriteFile(filepath.Join(bin, name+exeSuffix()), raw, 0o755); err != nil {
				fakeBinErr = err
				return
			}
		}
		fakeBinDir = bin
	})
	if fakeBinErr != nil {
		t.Fatal(fakeBinErr)
	}
	return fakeBinDir
}

// exeSuffix is what a program is called on this platform. Without it the fake is written
// as `gh`, which Windows will not execute -- the original bug, one layer down.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// fakePATH puts the fakes at the front of PATH and returns the directory this test writes
// its specs into. Nothing on PATH answers until fakeTool names it.
func fakePATH(t *testing.T) string {
	t.Helper()
	bin := fakeBins(t)
	specs := filepath.Join(t.TempDir(), "fakes")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_PULSE_FAKE_DIR", specs)
	return specs
}

// fakeTool teaches the fake called `name` what to answer in this test.
func fakeTool(t *testing.T, specs, name string, s fakeSpec) {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specs, name+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
