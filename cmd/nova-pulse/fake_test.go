package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// The fakes this package's tests put in front of PATH, sharing internal/pulse's fake
// program (internal/pulse/testdata/fakebin).
//
// TestPoolMaxFlagBoundsCandidates used to write a `#!/bin/sh` gh. Windows does not execute
// one, so the PATH lookup walked past it and the REAL gh answered -- exit 4, "check gh auth
// and the repo name", on a hosted runner with no credential. A test must never invoke the
// real gh on any platform, so the fixture is a Go program that records its argv and the
// test asserts what it recorded.

type fakeRule struct {
	Arg        int    `json:"arg,omitempty"`
	Equals     string `json:"equals,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	StdoutFile string `json:"stdoutFile,omitempty"`
	Exit       int    `json:"exit,omitempty"`
}

type fakeSpec struct {
	Log     string     `json:"log,omitempty"`
	Rules   []fakeRule `json:"rules,omitempty"`
	Default fakeRule   `json:"default"`
}

// fakeTools are the programs nova-pulse starts. A name with no spec in the test's
// directory exits 97 and says so, which no real gh or git ever does.
var fakeTools = []string{"gh", "git", "nova-bus", "nova-pulse", "nova-swarm"}

var (
	fakeRoot    string
	fakeBinOnce sync.Once
	fakeBinDir  string
	fakeBinErr  error
)

// TestMain owns the one directory outside t.TempDir(): the shared bin directory the fake
// is built into, gone when the process is, including on a failing run.
func TestMain(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "nova-pulse-cmd-fakes-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "the fake PATH directory: %v\n", err)
			return 2
		}
		defer os.RemoveAll(dir)
		fakeRoot = dir
		return m.Run()
	}())
}

// fakeBins builds the fake ONCE for the test binary and copies it under every name.
func fakeBins(t *testing.T) string {
	t.Helper()
	fakeBinOnce.Do(func() {
		build := filepath.Join(fakeRoot, "build")
		if err := os.MkdirAll(build, 0o755); err != nil {
			fakeBinErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", build, "../../internal/pulse/testdata/fakebin")
		if raw, err := cmd.CombinedOutput(); err != nil {
			fakeBinErr = fmt.Errorf("building the fake: %v\n%s", err, raw)
			return
		}
		raw, err := os.ReadFile(filepath.Join(build, "fakebin"+exeSuffix()))
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
