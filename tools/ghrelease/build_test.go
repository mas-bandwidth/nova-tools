package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGoBuild answers `go build -o <dir>/ <pkgs...>` the way the toolchain does:
// one binary per package, named for the package's directory, plus .exe for a
// windows target. missing names a package it forgets to write.
func fakeGoBuild(missing string) func(c command, stdout, stderr io.Writer) int {
	return func(c command, stdout, stderr io.Writer) int {
		var dir string
		var pkgs []string
		for i, a := range c.args {
			if a == "-o" {
				dir = c.args[i+1]
			}
			if strings.HasPrefix(a, "./cmd/") {
				pkgs = append(pkgs, a)
			}
		}
		ext := ""
		for _, kv := range c.env {
			if kv == "GOOS=windows" {
				ext = ".exe"
			}
		}
		for _, p := range pkgs {
			name := filepath.Base(p)
			if name == missing {
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, name+ext), []byte(name), 0o755); err != nil {
				return 1
			}
		}
		return 0
	}
}

func TestBuildCompilesOnceAndNamesEveryArtifactTheReleaseWay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus", "nova-tokens")
	h.runner.stream = fakeGoBuild("")
	h.wantRC(h.do("build", "v1.2.3", "linux", "amd64", "dist"), 0)

	calls := h.runner.called()
	if len(calls) != 1 {
		t.Fatalf("%d programs run, want one go build over every tool", len(calls))
	}
	c := calls[0]
	if c.name != "go" || c.dir != h.dir {
		t.Errorf("ran %s in %q", c.name, c.dir)
	}
	if got := strings.Join(c.env, " "); got != "GOOS=linux GOARCH=amd64 CGO_ENABLED=0" {
		t.Errorf("env %q", got)
	}
	args := strings.Join(c.args, " ")
	for _, want := range []string{"build -trimpath -ldflags -s -w -X main.version=v1.2.3 -o ", "./cmd/nova-bus ./cmd/nova-tokens"} {
		if !strings.Contains(args, want) {
			t.Errorf("go build args %q lack %q", args, want)
		}
	}
	for _, n := range []string{"nova-bus_v1.2.3_linux_amd64", "nova-tokens_v1.2.3_linux_amd64"} {
		if _, err := os.Stat(filepath.Join(h.dir, "dist", n)); err != nil {
			t.Errorf("artifact %s: %v", n, err)
		}
	}
	h.mustContain("ldflags: -s -w -X main.version=v1.2.3\n")
	h.mustContain("built 2 tools for linux/amd64\n")
}

func TestBuildNamesWindowsBinariesWithExe(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.e.targets = []target{{"windows", "amd64"}}
	h.tools("nova-bus")
	h.runner.stream = fakeGoBuild("")
	h.wantRC(h.do("build", "v1.2.3", "windows", "amd64", "dist"), 0)
	if _, err := os.Stat(filepath.Join(h.dir, "dist", "nova-bus_v1.2.3_windows_amd64.exe")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRefusesAPlatformThatIsNotShipped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus")
	h.wantRC(h.do("build", "v1.2.3", "plan9", "mips", "dist"), 1)
	h.mustContain("refusing: plan9/mips is not a shipped platform")
	if n := len(h.runner.called()); n != 0 {
		t.Fatalf("%d programs run before the platform was refused", n)
	}
}

func TestBuildRefusesABadStampBeforeAnythingIsCompiled(t *testing.T) {
	t.Parallel()
	for _, stamp := range []string{"", "v1 2", "v1%s", "v1=2"} {
		h := newHarness(t)
		h.tools("nova-bus")
		h.wantRC(h.do("build", stamp, "linux", "amd64", "dist"), 1)
		h.mustContain("refusing: the release stamp")
		if n := len(h.runner.called()); n != 0 {
			t.Errorf("<%q>: %d programs run after a refused stamp; the build must never start", stamp, n)
		}
		if _, err := os.Stat(filepath.Join(h.dir, "dist")); err == nil {
			t.Errorf("<%q>: dist was created by a refused build", stamp)
		}
	}
}

func TestBuildRequireVTagRefusesAStampThatIsNotVPrefixed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus")
	h.wantRC(h.do("build", "--require-v-tag", "1.2.3", "linux", "amd64", "dist"), 1)
	h.mustContain("refusing: 1.2.3 is not a v-prefixed tag")
	if n := len(h.runner.called()); n != 0 {
		t.Fatalf("%d programs run", n)
	}

	h = newHarness(t)
	h.tools("nova-bus")
	h.runner.stream = fakeGoBuild("")
	h.wantRC(h.do("build", "1.2.3", "linux", "amd64", "dist"), 0) // without the flag a stamp is a stamp
}

func TestBuildWrongArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"build"}, {"build", "v1", "linux", "amd64"}, {"build", "v1", "linux", "amd64", "dist", "x"}} {
		h := newHarness(t)
		h.wantRC(h.do(args...), 2)
		h.mustContain("usage:")
	}
}

func TestBuildRefusesAnEmptyToolSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do("build", "v1.2.3", "linux", "amd64", "dist"), 1)
	h.mustContain("cmd/ matched nothing: this release would ship an empty set")
}

func TestBuildFailsWhenTheToolchainWritesNoBinaryForATool(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus", "nova-tokens")
	h.runner.stream = fakeGoBuild("nova-tokens")
	h.wantRC(h.do("build", "v1.2.3", "linux", "amd64", "dist"), 1)
	h.mustContain("go build wrote no binary for nova-tokens")
}

func TestBuildFailsWhenNovaBusIsNotInTheShippedSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-tokens")
	h.runner.stream = fakeGoBuild("")
	h.wantRC(h.do("build", "v1.2.3", "linux", "amd64", "dist"), 1)
	h.mustContain("nova-bus is not in the shipped set")
}

func TestBuildPassesTheToolchainsExitCodeOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus")
	h.runner.stream = func(c command, stdout, stderr io.Writer) int { return 3 }
	h.wantRC(h.do("build", "v1.2.3", "linux", "amd64", "dist"), 3)
}

func TestBuildToleratesACmdDirectoryWithAStrayFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus")
	h.write("cmd/AGENTS.md", "a file, not a tool\n", 0o644)
	h.write("cmd/.hidden/main.go", "package main\n", 0o644)
	h.runner.stream = fakeGoBuild("")
	h.wantRC(h.do("build", "v1.2.3", "linux", "amd64", "dist"), 0)
	h.mustContain("built 1 tools for linux/amd64")
}
