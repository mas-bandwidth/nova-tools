//go:build functional

package ci

// This file runs tools/bench-standard.sh, a whole bash program, under a fake
// PATH: exec of a whole program is the functional tier's, not a unit test
// (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and frugal).
//
// Issue #1500. tools/bench-standard.sh held a bench against $NOVA_GO with a
// hardcoded default of go1.26.5. go.mod moved to go 1.26.6 and the fleet-standard
// check stayed green on a bench nova-merge batch refused: the wanted version was
// a flag default, not a fact about the tree.
//
// The method is the disk-headroom test's: a fake `go` first on PATH, a HOME of
// its own, and only the go DRIFT line is read. $NOVA_GO is stripped so the
// script has to find the directive itself.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

func TestBenchStandardGoWantTracksGoMod(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the bench standard is a bash script for a Linux bench")
	}

	want := treeGoWant(t)
	bin := t.TempDir()
	home := t.TempDir()
	// A toolchain one patch behind the tree: the shape that left captainamerica
	// "conforming" while batch refused it.
	fake := "#!/bin/sh\necho 'go version go1.26.5 linux/amd64'\n"
	if err := testbin.WriteExecutable(filepath.Join(bin, "go"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "tools", "bench-standard.sh"))
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "NOVA_GO=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
	)
	out, _ := cmd.CombinedOutput()
	got := string(out)

	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "DRIFT go") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("a bench on go1.26.5 printed no `DRIFT go` line; go.mod names %s and the wanted version is that directive, not a flag default:\n%s", want, got)
	}
	if !strings.Contains(line, want) {
		t.Errorf("the go DRIFT line does not name go.mod's %s:\n%s", want, line)
	}
}

func treeGoWant(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "go" {
			return "go" + fields[1]
		}
	}
	t.Fatal("go.mod carries no go directive")
	return ""
}
