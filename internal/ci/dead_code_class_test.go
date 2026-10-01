//go:build functional

package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// deadCodeLedgerPath is the shrink-only per-package ledger of unreachable functions
// across the codebase from cmd/ production roots (nova-tools#4902).
//
// Maintainer directive (2026-09-30, contraction phase):
// "dead code to zero with a class test holding it".
//
// Every row is `<package> <count>`.
// The list is shrink-only: NOVA_CI_UPDATE=1 lowers counts and drops rows at zero;
// it never raises a count and never adds a row.
const (
	deadCodeLedgerPath = "testdata/dead_code_allowlist.txt"
	deadCodeRemedy     = "delete the unreachable function(s) or wire them into cmd/...; the dead code ledger only shrinks and refuses to raise counts or add rows"
)

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

// deadcodeToolBinary resolves the deadcode tool binary path via `go tool -n deadcode`.
// If not yet compiled in the cache, it runs `go tool deadcode` with a clean host environment
// to ensure the binary is built.
func deadcodeToolBinary(t *testing.T, ctx context.Context) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "go", "tool", "-n", "deadcode")
	cmd.Env = goenv.Clean(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	require.NoError(t, err, "resolving deadcode binary via go tool -n deadcode")

	bin := strings.TrimSpace(string(out))
	if _, err := os.Stat(bin); err != nil {
		build := exec.CommandContext(ctx, "go", "tool", "deadcode")
		build.Env = goenv.Clean(os.Environ())
		build.WaitDelay = 5 * time.Second
		_ = build.Run() // deadcode exits non-zero on no packages, but compiles into cache
	}
	require.FileExists(t, bin, "deadcode binary must exist")
	return bin
}

// runDeadcode runs deadcode on ./cmd/... roots for targetOS using the host tool binary.
func runDeadcode(t *testing.T, ctx context.Context, bin, root, targetOS string) ([]deadcodePackage, error) {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin, "-json", "./cmd/...")
	cmd.Dir = root
	cmd.Env = append(goenv.Clean(os.Environ()), "GOOS="+targetOS)
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("running deadcode for GOOS=%s: %w\nstderr: %s", targetOS, err, stderr.String())
	}

	var pkgs []deadcodePackage
	if err := json.Unmarshal(stdout.Bytes(), &pkgs); err != nil {
		return nil, fmt.Errorf("parsing deadcode json for GOOS=%s: %w", targetOS, err)
	}
	return pkgs, nil
}

// findDeadCodeUnion runs deadcode across linux, darwin, and windows, taking the union
// of dead functions per package.
func findDeadCodeUnion(t *testing.T, ctx context.Context, root string) (map[string]int, map[string][]string, error) {
	t.Helper()
	bin := deadcodeToolBinary(t, ctx)
	oses := []string{"linux", "darwin", "windows"}
	union := make(map[string]map[string]bool)
	modulePrefix := "github.com/mas-bandwidth/nova-tools/"

	for _, goos := range oses {
		pkgs, err := runDeadcode(t, ctx, bin, root, goos)
		if err != nil {
			return nil, nil, err
		}
		for _, pkg := range pkgs {
			p := pkg.Path
			if strings.HasPrefix(p, modulePrefix) {
				p = strings.TrimPrefix(p, modulePrefix)
			}
			for _, fn := range pkg.Funcs {
				if fn.Marker || fn.Generated {
					continue
				}
				if union[p] == nil {
					union[p] = make(map[string]bool)
				}
				union[p][fn.Name] = true
			}
		}
	}

	counts := make(map[string]int, len(union))
	byPkg := make(map[string][]string, len(union))
	for p, funcs := range union {
		counts[p] = len(funcs)
		names := make([]string, 0, len(funcs))
		for name := range funcs {
			names = append(names, name)
		}
		sort.Strings(names)
		byPkg[p] = names
	}
	return counts, byPkg, nil
}

// TestDeadCode is the class test holding unreachable functions to a shrink-only
// per-package ledger across linux, darwin, and windows (docs/SPEC-CI.md, "deadcode").
func TestDeadCode(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	root := repoRoot(t)
	l := loadAllowlist(t, deadCodeLedgerPath, allowlist.Options{Ceiling: true, Counted: true})

	measured, byPkg, err := findDeadCodeUnion(t, ctx, root)
	require.NoError(t, err, "detecting dead code across linux, darwin, windows")

	update := allowlist.Updating()
	res := allowlist.CheckCountedMode(t, l, measured, update)

	if !update {
		var problems []string
		for _, pkg := range res.Unlisted {
			funcs := byPkg[pkg]
			sample := funcs
			if len(sample) > 5 {
				sample = sample[:5]
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %d unreachable functions not listed in ledger (e.g. %s);\n  remedy: %s;\n  reproduce: GOOS=linux go tool deadcode ./cmd/... (or GOOS=darwin, windows)",
				pkg, measured[pkg], strings.Join(sample, ", "), deadCodeRemedy))
		}
		for _, row := range res.Over {
			funcs := byPkg[row.Key]
			sample := funcs
			if len(sample) > 5 {
				sample = sample[:5]
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %d unreachable functions, over ledger count of %d (e.g. %s);\n  remedy: %s;\n  reproduce: GOOS=linux go tool deadcode ./cmd/... (or GOOS=darwin, windows)",
				row.Key, row.Measured, row.Listed, strings.Join(sample, ", "), deadCodeRemedy))
		}
		for _, row := range res.Lowered {
			problems = append(problems, fmt.Sprintf(
				"%s: has %d unreachable functions, below ledger count of %d; lower the row with %s=1",
				row.Key, row.Measured, row.Listed, allowlist.UpdateEnv))
		}
		for _, row := range res.Stale {
			problems = append(problems, fmt.Sprintf(
				"%s: 0 unreachable functions in tree, but listed in ledger; drop the stale row with %s=1",
				row.Key, allowlist.UpdateEnv))
		}
		if len(problems) > 0 {
			sort.Strings(problems)
			assert.Failf(t, "dead code rule", "%s\n(ledger: %s)", strings.Join(problems, "\n"), deadCodeLedgerPath)
		}
	}
}

// deadCodeWitnessReporter records CheckCountedMode error output without failing the test runner.
type deadCodeWitnessReporter struct {
	lines []string
}

func (r *deadCodeWitnessReporter) Helper() {}
func (r *deadCodeWitnessReporter) Errorf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

// TestDeadCodeWitness verifies the shrink-only per-package ledger mechanics:
//   - An unlisted package with dead functions triggers refusal.
//   - A package with count above the ledger triggers refusal.
//   - A package with count below the ledger is lowered under update mode.
//   - A package with 0 dead functions is pruned under update mode.
//   - Under update mode, unlisted packages and count increases are refused.
func TestDeadCodeWitness(t *testing.T) {
	t.Parallel()

	fixture := "# ceiling: 2\npkg/a 5\npkg/b 3\n"
	tmp := filepath.Join(t.TempDir(), "witness_fixture.txt")
	err := os.WriteFile(tmp, []byte(fixture), 0o644)
	require.NoError(t, err)

	opt := allowlist.Options{Ceiling: true, Counted: true}
	l, err := allowlist.Load(tmp, opt)
	require.NoError(t, err)

	// 1. Unlisted package (pkg/c) and over count (pkg/a: 6 > 5) detected.
	rec1 := &deadCodeWitnessReporter{}
	res1 := allowlist.CheckCountedMode(rec1, l, map[string]int{
		"pkg/a": 6,
		"pkg/b": 3,
		"pkg/c": 2,
	}, false)
	assert.Len(t, res1.Unlisted, 1)
	assert.Contains(t, res1.Unlisted, "pkg/c")
	assert.Len(t, res1.Over, 1)
	assert.Equal(t, "pkg/a", res1.Over[0].Key)
	assert.Equal(t, 6, res1.Over[0].Measured)

	// 2. Lowered count (pkg/a: 3 < 5) and stale package (pkg/b: 0).
	rec2 := &deadCodeWitnessReporter{}
	res2 := allowlist.CheckCountedMode(rec2, l, map[string]int{
		"pkg/a": 3,
	}, false)
	assert.Len(t, res2.Lowered, 1)
	assert.Equal(t, "pkg/a", res2.Lowered[0].Key)
	assert.Equal(t, 3, res2.Lowered[0].Measured)
	assert.Len(t, res2.Stale, 1)
	assert.Equal(t, "pkg/b", res2.Stale[0].Key)

	// 3. Update mode lowers pkg/a to 3, drops pkg/b, and lowers ceiling to 1.
	rec3 := &deadCodeWitnessReporter{}
	res3 := allowlist.CheckCountedMode(rec3, l, map[string]int{
		"pkg/a": 3,
	}, true)
	assert.True(t, res3.Updated)

	updated, err := allowlist.Load(tmp, opt)
	require.NoError(t, err)
	ceil, ok := updated.Ceiling()
	assert.True(t, ok)
	assert.Equal(t, 1, ceil)
	assert.False(t, updated.Has("pkg/b"))
	assert.True(t, updated.Has("pkg/a"))
	assert.Contains(t, updated.Text(), "pkg/a 3")
	assert.NotContains(t, updated.Text(), "pkg/b")

	// 4. Update mode refuses to raise count or add unlisted package.
	rec4 := &deadCodeWitnessReporter{}
	_ = allowlist.CheckCountedMode(rec4, updated, map[string]int{
		"pkg/a": 4,
		"pkg/d": 1,
	}, true)
	assert.NotEmpty(t, rec4.lines)
	joined := strings.Join(rec4.lines, "\n")
	assert.Contains(t, joined, "refuses to raise a count")
	assert.Contains(t, joined, "refuses to grow")
}
