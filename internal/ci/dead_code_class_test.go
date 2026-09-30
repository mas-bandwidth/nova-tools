package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// deadCodeAllowlistPath is the shrink-only ledger of unreachable functions,
// methods, and symbols across the codebase (nova-tools#4885).
//
// Maintainer directive (2026-09-30, contraction phase):
// "dead code to zero with a class test holding it".
//
// Every row is `path/to/file.go:Symbol # reason/status`.
// The list is ceiling-only: it only shrinks. When dead code is removed,
// NOVA_CI_UPDATE=1 drops the stale rows and lowers the ceiling. Any newly
// introduced dead code is refused outright.
const deadCodeAllowlistPath = "testdata/dead_code_allowlist.txt"

// deadCodeRemedy is the one thing to do about an unlisted dead function.
const deadCodeRemedy = "delete the unreachable function or method; the allowlist only shrinks and refuses to add rows"

// deadcodePackage represents one package in deadcode -json output.
type deadcodePackage struct {
	Name  string             `json:"Name"`
	Path  string             `json:"Path"`
	Funcs []deadcodeFunction `json:"Funcs"`
}

type deadcodeFunction struct {
	Name     string `json:"Name"`
	Position struct {
		File string `json:"File"`
		Line int    `json:"Line"`
		Col  int    `json:"Col"`
	} `json:"Position"`
	Generated bool `json:"Generated"`
	Marker    bool `json:"Marker"`
}

func readDeadCodeAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	return loadAllowlist(t, deadCodeAllowlistPath, shrinkOnly)
}

// findDeadCode executes the deadcode tool to find all unreachable functions
// across the repository. It checks the local PATH and Go bin directories
// first, falling back to `go run golang.org/x/tools/cmd/deadcode@latest`.
func findDeadCode(t *testing.T, root string) (map[string]bool, error) {
	t.Helper()

	bin, err := exec.LookPath("deadcode")
	if err != nil {
		var candidates []string
		if gp := os.Getenv("GOPATH"); gp != "" {
			candidates = append(candidates, filepath.Join(gp, "bin", "deadcode"))
		}
		if h := os.Getenv("HOME"); h != "" {
			candidates = append(candidates, filepath.Join(h, "go", "bin", "deadcode"))
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				bin = c
				break
			}
		}
	}

	var out []byte
	if bin != "" {
		cmd := exec.Command(bin, "-json", "-test", "./...")
		cmd.Dir = root
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("deadcode binary (%s) failed: %w", bin, err)
		}
	} else {
		cmd := exec.Command("go", "run", "golang.org/x/tools/cmd/deadcode@latest", "-json", "-test", "./...")
		cmd.Dir = root
		cmd.Env = goenv.Clean(os.Environ())
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("deadcode via go run failed: %w", err)
		}
	}

	var pkgs []deadcodePackage
	if err := json.Unmarshal(out, &pkgs); err != nil {
		return nil, fmt.Errorf("parsing deadcode json: %w", err)
	}

	measured := make(map[string]bool)
	for _, p := range pkgs {
		for _, fn := range p.Funcs {
			if fn.Marker {
				continue
			}
			f := filepath.ToSlash(fn.Position.File)
			key := fmt.Sprintf("%s:%s", f, fn.Name)
			measured[key] = true
		}
	}
	return measured, nil
}

// TestNoDeadCode is the class test holding unreachable functions and methods
// to a shrink-only ceiling (Maintainer directive 2026-09-30, contraction phase:
// "dead code to zero with a class test holding it").
//
// It runs reachability analysis over the entire repository (main executables
// and test entrypoints), and asserts:
//   (1) no unreachable function exists outside the allowlist;
//   (2) every allowlisted function is still present and still dead (stale rows
//       fail and must be pruned with NOVA_CI_UPDATE=1);
//   (3) the allowlist row count never exceeds its ceiling.
func TestNoDeadCode(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := readDeadCodeAllowlist(t)

	measured, err := findDeadCode(t, root)
	if err != nil {
		t.Fatalf("detecting dead code: %v", err)
	}

	var violations []string
	for k := range measured {
		if !allow.Has(k) {
			violations = append(violations, fmt.Sprintf("%s: unreachable dead code; %s", k, deadCodeRemedy))
		}
	}

	// The list only shrinks: a row whose dead code has been deleted is a red run.
	res := allowlist.Check(t, allow, measured)
	for _, row := range res.Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but it is no longer dead code (prune the stale row with %s=1; the list only shrinks)",
			deadCodeAllowlistPath, row.Key, allowlist.UpdateEnv))
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// deadCodeRecorder records allowlist error output without failing the test runner.
type deadCodeRecorder struct {
	lines []string
}

func (r *deadCodeRecorder) Helper() {}
func (r *deadCodeRecorder) Errorf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

// TestDeadCodeWitness tests the shrink-only allowlist mechanics:
//   - An unlisted dead function triggers an error.
//   - A deleted function becomes a stale row and triggers an error.
//   - Under update mode, stale rows are dropped and the ceiling is lowered.
//   - Under update mode, unlisted functions are refused and cannot grow the allowlist.
func TestDeadCodeWitness(t *testing.T) {
	t.Parallel()

	fixture := "# header\n# ceiling: 2\npkg/a.go:FuncA # dead\npkg/b.go:FuncB # dead\n"
	tmp := filepath.Join(t.TempDir(), "dead_code_allowlist.txt")
	if err := os.WriteFile(tmp, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	opt := allowlist.Options{Ceiling: true}
	l, err := allowlist.Load(tmp, opt)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Unlisted dead function (FuncC) must be detected.
	rec1 := &deadCodeRecorder{}
	res1 := allowlist.CheckMode(rec1, l, map[string]bool{
		"pkg/a.go:FuncA": true,
		"pkg/b.go:FuncB": true,
		"pkg/c.go:FuncC": true,
	}, false)
	if len(res1.Unlisted) != 1 || res1.Unlisted[0] != "pkg/c.go:FuncC" {
		t.Errorf("expected 1 unlisted func (FuncC), got: %v", res1.Unlisted)
	}

	// 2. Stale dead function (FuncB deleted) must be detected.
	rec2 := &deadCodeRecorder{}
	res2 := allowlist.CheckMode(rec2, l, map[string]bool{
		"pkg/a.go:FuncA": true,
	}, false)
	if len(res2.Stale) != 1 || res2.Stale[0].Key != "pkg/b.go:FuncB" {
		t.Errorf("expected 1 stale row (FuncB), got: %v", res2.Stale)
	}

	// 3. Update mode on stale row drops it and lowers ceiling to 1.
	rec3 := &deadCodeRecorder{}
	res3 := allowlist.CheckMode(rec3, l, map[string]bool{
		"pkg/a.go:FuncA": true,
	}, true)
	if !res3.Updated {
		t.Errorf("expected list to be updated")
	}

	updated, err := allowlist.Load(tmp, opt)
	if err != nil {
		t.Fatal(err)
	}
	if ceil, ok := updated.Ceiling(); !ok || ceil != 1 {
		t.Errorf("expected ceiling lowered to 1, got ceil=%d ok=%v", ceil, ok)
	}
	if updated.Has("pkg/b.go:FuncB") {
		t.Errorf("expected FuncB removed from allowlist")
	}

	// 4. Update mode refuses to add new row for unlisted func.
	rec4 := &deadCodeRecorder{}
	_ = allowlist.CheckMode(rec4, updated, map[string]bool{
		"pkg/a.go:FuncA": true,
		"pkg/d.go:FuncD": true,
	}, true)
	if len(rec4.lines) == 0 || !strings.Contains(rec4.lines[0], "refuses to grow") {
		t.Errorf("expected refusal to grow, got: %v", rec4.lines)
	}
}

