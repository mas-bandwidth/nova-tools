package update

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stubSource is a tool that prints one line for any argument: the fixture the two tests
// below ask their versions of. It reads the line from a file named after itself in a
// `lines` directory BESIDE --bin, so one build serves every tool and the bin directory
// holds tools and nothing else.
const stubSource = `package main

import (
	"os"
	"path/filepath"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	name := strings.TrimSuffix(filepath.Base(exe), ".exe")
	line, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "..", "lines", name))
	if err != nil {
		os.Exit(1)
	}
	os.Stdout.Write(line)
}
`

// stubBinary compiles stubSource ONCE per test binary and hands back its bytes. The
// build is the expensive part of the fixture and the result does not vary by test, so a
// package whose suite has to stay under a minute pays for it a single time.
var stubBuild struct {
	once sync.Once
	body []byte
	err  error
}

func stubBinary() ([]byte, error) {
	stubBuild.once.Do(func() {
		src, err := os.MkdirTemp("", "nova-version-stub")
		if err != nil {
			stubBuild.err = err
			return
		}
		defer os.RemoveAll(src)
		if stubBuild.err = os.WriteFile(filepath.Join(src, "main.go"), []byte(stubSource), 0644); stubBuild.err != nil {
			return
		}
		if stubBuild.err = os.WriteFile(filepath.Join(src, "go.mod"), []byte("module novaversionstub\n\ngo 1.26\n"), 0644); stubBuild.err != nil {
			return
		}
		built := filepath.Join(src, "stub"+exeSuffix())
		build := exec.Command("go", "build", "-o", built, ".")
		build.Dir = src
		if out, err := build.CombinedOutput(); err != nil {
			stubBuild.err = fmt.Errorf("building the version stub: %v\n%s", err, out)
			return
		}
		stubBuild.body, stubBuild.err = os.ReadFile(built)
	})
	return stubBuild.body, stubBuild.err
}

// toolStubs installs one fake tool per entry of lines under bin, each printing its line
// when the snapshot verb asks it for a version.
//
// THEY ARE BUILT BINARIES AND NOT `#!/bin/sh` SCRIPTS, and that is the whole of the
// windows failure of the hosted leg: windows has no shebang and exec.LookPath there will
// not run an extension-less file, so every script was "not_found" and the verb reported
// tools=0 unreadable=4 -- a fixture that cannot run on that platform, never a verb that
// refused. A stub compiled by the toolchain that is already running the test runs on
// every platform the tool is built for, `.exe` and all.
func toolStubs(t *testing.T, bin string, lines map[string]string) {
	t.Helper()
	linesDir := filepath.Join(filepath.Dir(bin), "lines")
	if err := os.MkdirAll(linesDir, 0755); err != nil {
		t.Fatal(err)
	}
	body, err := stubBinary()
	if err != nil {
		t.Fatal(err)
	}
	for name, line := range lines {
		if err := os.WriteFile(filepath.Join(bin, name+exeSuffix()), body, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(linesDir, name), []byte(line+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotWritesRuleTwoManifest(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	toolStubs(t, bin, map[string]string{
		"nova-bus":   "nova-bus v0.12.1-0.20260912-0459069+dirty darwin/arm64",
		"nova-check": "v7.8.9",
		"nova-wake":  "nova-wake 2.3.4",
		"nova-bad":   "hello world",
	})
	if err := os.WriteFile(filepath.Join(bin, "README"), []byte("not a tool"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.tsv")
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out, "--owner", "rowan"}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK tools=3 unreadable=1 out="+field(out))
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// kind is `tool`, one of the five curated kinds, because this file is read by
	// `nova-version report` and `binary` was refused there (#571).
	want := Header + "\n" +
		"nova-bus\ttool\t0.12.1-0.20260912-0459069+dirty\t-\t-\trowan\n" +
		"nova-check\ttool\t7.8.9\t-\t-\trowan\n" +
		"nova-wake\ttool\t2.3.4\t-\t-\trowan\n"
	if string(b) != want {
		t.Fatalf("manifest mismatch:\ngot:\n%s\nwant:\n%s", b, want)
	}

	// Default owner is "-", never guessed.
	o.Reset()
	code = Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	b, _ = os.ReadFile(out)
	if !strings.Contains(string(b), "nova-check\ttool\t7.8.9\t-\t-\t-\n") {
		t.Fatalf("default owner not '-':\n%s", b)
	}
}

// THE ROUND TRIP, which is the whole point of the verb: the manifest snapshot writes is
// the manifest report reads. Both verbs passed their own tests on 2026-09-15 while the
// sequence between them was broken three fields deep -- `kind=binary` against the five
// curated kinds, an installed column holding a version where report wanted the argv to run,
// and `latest=-` against `<scheme>:<locator>` (#571). Red before the three fixes: REPORT
// REFUSED at exit 2, on the file this tool wrote one command earlier.
func TestSnapshotThenReportRoundTrips(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	toolStubs(t, bin, map[string]string{
		"nova-bus":   "nova-bus v0.15.0 darwin/arm64",
		"nova-check": "nova-check 1.2.3",
	})
	out := filepath.Join(dir, "versions.tsv")
	var o, e bytes.Buffer
	if code := Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out, "--owner", "rowan"}, "", &o, &e, Environment{}); code != 0 {
		t.Fatalf("snapshot: exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK tools=2 unreadable=0 out="+field(out))

	o.Reset()
	e.Reset()
	if code := Run("nova-version", []string{"report", "--file", out}, "", &o, &e, Environment{}); code != 0 {
		t.Fatalf("report on the manifest snapshot just wrote: exit %d\nstdout: %s\nstderr: %s", code, o.String(), e.String())
	}
	need(t, o.String(), "REPORT OK checked=2 known=2 unknown=0")
	// The version each tool printed, carried through the file rather than read again: the
	// snapshot is a reading taken at a moment, and report says what that reading was.
	need(t, o.String(), "REPORT TOOL name=nova-bus kind=tool version=0.15.0")
	need(t, o.String(), "REPORT TOOL name=nova-check kind=tool version=1.2.3")
}
