package docs

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// windows_bench_standard_test.go holds docs/BENCH-STANDARD-WINDOWS.md against the
// two facts in it that rot on their own, and against the README that points at it.
//
// The page is a provisioning standard for a machine nobody has yet: every word of
// it will be read once, by somebody at a keyboard in front of a new box, with no
// way to check it. A standard read that way is only worth something if it is TRUE,
// and the two claims most likely to go quietly wrong are (a) the Go version it
// tells you to install, which follows go.mod and nothing else, and (b) the twelve
// Windows sandbox rules it summarises, which are numbered so that a rule of that
// section is never confused with a rule of the platform-independent set -- and a
// summary missing one is a bench provisioned without it.
//
// It reads both sides as text and runs nothing.
//
// THE NAME IS LOAD-BEARING. This file was `bench_standard_windows_test.go` first,
// and it compiled, and it was green, and it ran NOWHERE: Go strips `_test` and
// reads the remaining `_windows` suffix as an implicit GOOS constraint, so the
// whole file was excluded on every machine in the fleet. `go test ./internal/docs/`
// said `ok` because the nine other tests in the package passed. A test about
// Windows must therefore not be NAMED for Windows at its end -- the word goes at
// the front.

const (
	winStandardPath = "../../docs/BENCH-STANDARD-WINDOWS.md"
	winReadmePath   = "../../README.md"
	winGoModPath    = "../../go.mod"
)

// goLineRe reads go.mod's own `go` line: the language version every toolchain on
// the fleet must be at least.
var goLineRe = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

// sdkGoRe reads the version out of the SDK path the page tells you to install
// under, which is the one place the page names a full patch version.
var sdkGoRe = regexp.MustCompile(`C:\\sdk\\go(\d+\.\d+)\.(\d+)`)

func TestWindowsBenchStandardInstallsGoModsOwnGo(t *testing.T) {
	t.Parallel()

	mod := readWindowsFile(t, winGoModPath)
	want := goLineRe.FindStringSubmatch(mod)
	if want == nil {
		t.Fatalf("%s carries no `go <version>` line", winGoModPath)
	}

	page := readWindowsFile(t, winStandardPath)
	got := sdkGoRe.FindAllStringSubmatch(page, -1)
	if len(got) == 0 {
		t.Fatalf("the page names no C:\\sdk\\go<version> to install; it is the one path a person copies")
	}
	for _, m := range got {
		if m[1] != want[1] {
			t.Errorf("the page installs go%s.%s but go.mod asks for go %s; a toolchain below go.mod's line does not fail loudly, it refuses the module by name in one line nobody reads",
				m[1], m[2], want[1])
		}
	}
	// And it must say WHY, because the version alone is a number somebody will round.
	if !strings.Contains(page, "go.mod") {
		t.Error("the page must name go.mod as where the version comes from, or the next person picks whatever is current")
	}
}

func TestWindowsBenchStandardCarriesEveryWindowsSandboxRule(t *testing.T) {
	t.Parallel()

	page := readWindowsFile(t, winStandardPath)
	for i := 1; i <= 12; i++ {
		rule := fmt.Sprintf("W%d", i)
		// The rules are a table, one row each: `| **W7** | ... |`.
		if !strings.Contains(page, "**"+rule+"**") {
			t.Errorf("the page does not carry rule %s; a bench provisioned without one of the twelve is a bench the sandbox contract does not hold on", rule)
		}
	}
	// W11 is the one that is the whole reason the page exists, so it is named twice:
	// once in the table and once where a reader cannot miss it.
	if strings.Count(page, "WSL") < 2 || !strings.Contains(page, "NEVER WSL") {
		t.Error("the page must refuse WSL where a reader cannot miss it: it is rule W11 and the reason this page is not `follow the Linux standard inside WSL2`")
	}
}

func TestReadmePointsAtTheWindowsBenchStandard(t *testing.T) {
	t.Parallel()

	readme := readWindowsFile(t, winReadmePath)
	if !strings.Contains(readme, "docs/BENCH-STANDARD-WINDOWS.md") {
		t.Error("README's `Where to go next` list must name the Windows bench standard; a provisioning page nobody can find is a page provisioned from memory")
	}
}

func readWindowsFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(raw)
}
