package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #1500. FleetStandardChecks pinned goWant = "go1.26.5" when --go was
// empty, one patch behind go.mod, so a bench on go1.26.5 read as conforming
// while nova-merge batch refused it. The wanted version is the tree's go line.
// --go stays an explicit override.

func TestGoDirectiveReadsTheGoLine(t *testing.T) {
	t.Parallel()
	if got := GoDirective([]byte("module x\n\ngo 1.99.1\n")); got != "go1.99.1" {
		t.Fatalf("GoDirective = %q, want go1.99.1", got)
	}
	if got := GoDirective([]byte("module x\n\ngo 1.26\n")); got != "go1.26" {
		t.Fatalf("GoDirective = %q, want go1.26", got)
	}
	if got := GoDirective([]byte("module x\n")); got != "" {
		t.Fatalf("GoDirective = %q, want empty", got)
	}
}

func TestFleetStandardChecksGoWantOverride(t *testing.T) {
	t.Parallel()
	for _, c := range FleetStandardChecks("linux", "go1.22.0", "", 25) {
		if c.Name == "go" && c.Want != "go1.22.0" {
			t.Fatalf("explicit --go was ignored: Want=%q", c.Want)
		}
	}
}

func TestFleetStandardChecksEmptyGoWantReadsGoMod(t *testing.T) {
	t.Parallel()
	want := treeGoWant(t)
	if want == "" {
		t.Fatal("go.mod carries no go directive")
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		found := false
		for _, c := range FleetStandardChecks(goos, "", "", 25) {
			if c.Name != "go" {
				continue
			}
			found = true
			if c.Want != want {
				t.Errorf("%s go Want = %q, go.mod names %q; empty goWant is a copy that ages, not the tree's go line", goos, c.Want, want)
			}
		}
		if !found {
			t.Errorf("%s has no go check", goos)
		}
	}
}

func TestFleetStandardDefaultGoWantIsNotAHardcodedPatch(t *testing.T) {
	t.Parallel()
	std, err := os.ReadFile("fleetstandard.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(std, []byte(`goWant = "go1.`)) {
		t.Error("FleetStandardChecks pins a go1.X.Y default; the wanted version is go.mod's go line, and --go is the override")
	}
	sh, err := os.ReadFile(filepath.Join("..", "..", "tools", "bench-standard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sh, []byte(`NOVA_GO="${NOVA_GO:-go1.`)) {
		t.Error("bench-standard.sh pins a go1.X.Y default; the wanted version is go.mod's go line, and $NOVA_GO is the override")
	}
}

func TestFleetStandardEmptyGoWantDriftsWhenGoIsBehindGoMod(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)
	want := treeGoWant(t)

	var out, errb bytes.Buffer
	code := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH, OS: "linux",
		Want: "abc123", MinFreeGB: 0,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	got := out.String()
	if strings.Contains(got, "STANDARD worker-1 go OK") {
		t.Fatalf("empty --go treated a go1.26.5 bench as conforming; go.mod names %s:\n%s", want, got)
	}
	if !strings.Contains(got, "STANDARD worker-1 go DRIFT") {
		t.Fatalf("empty --go printed no go DRIFT; go.mod names %s:\n%s", want, got)
	}
	if !strings.Contains(got, want) {
		t.Fatalf("go DRIFT does not name go.mod's %s:\n%s", want, got)
	}
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, got)
	}
}

func treeGoWant(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
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
