package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ci_goenv_test.go is the red-test contract of the goenv checker, and then the
// class test itself over this repository. Each fixture is a real .go file
// written into a throwaway tree and handed to CheckGoEnv, so the checker is
// exercised on a tree given to it and never by reaching into the repo. The
// fixtures live in testdata/ so the run over the repository never reads the
// offender it is meant to find.

// goEnvAllowlistPath is the shrink-only list of the child `go` commands this
// repository still permits to inherit the caller's environment. It is empty.
const goEnvAllowlistPath = "testdata/goenv-allowlist.txt"

// goEnvFixtureTree writes one fixture under <tmp>/internal/fixture/fixture.go
// and returns the tree root, the way a caller hands the checker a --dir.
func goEnvFixtureTree(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "goenv", name))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal", "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// goEnvLineAt reads the line a finding named and returns it trimmed, so a test
// asserts on the offender itself rather than on a line number the fixture's
// comments can move.
func goEnvLineAt(t *testing.T, root, rel string, line int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if line < 1 || line > len(lines) {
		t.Fatalf("%s:%d is outside the file (%d lines)", rel, line, len(lines))
	}
	return strings.TrimSpace(lines[line-1])
}

// 1. The bug itself: the pre-fix runUnits of nova-review mutate, an inner
// `go test` with no environment of its own. The checker refuses it and names
// the file, the line, the function and the remedy.
func TestGoEnvRefusesThePreFixMutate(t *testing.T) {
	root := goEnvFixtureTree(t, "mutate_prefix.go.txt")
	res, err := CheckGoEnv(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("the pre-fix mutate is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "inherit" {
		t.Errorf("kind = %q, want inherit", f.Kind)
	}
	if f.Func != "runUnits" {
		t.Errorf("func = %q, want runUnits", f.Func)
	}
	if f.Remedy != GoEnvRemedy {
		t.Errorf("remedy = %q, want %q", f.Remedy, GoEnvRemedy)
	}
	if got := goEnvLineAt(t, root, f.File, f.Line); !strings.Contains(got, `exec.CommandContext(ctx, "go", args...)`) {
		t.Errorf("finding names %s:%d = %q, want the inner go test line", f.File, f.Line, got)
	}
	if !strings.Contains(f.Render(), "CI-GOENV file=internal/fixture/fixture.go") {
		t.Errorf("the refusal line does not carry the file: %s", f.Render())
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", res.ExitCode())
	}
}

// 2. The fixed shape passes, both spellings: the plain Clean and an append
// onto it for the tool's own variables.
func TestGoEnvAllowsCleanEnvironment(t *testing.T) {
	root := goEnvFixtureTree(t, "mutate_fixed.go.txt")
	res, err := CheckGoEnv(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("goenv.Clean is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
	if res.Files != 1 {
		t.Errorf("files = %d, want 1", res.Files)
	}
	if !strings.HasSuffix(res.OKLine(), "refused=0") {
		t.Errorf("OK line = %q", res.OKLine())
	}
}

// 3. The near miss: an environment is set, but it is the caller's own, so
// GOFLAGS travels. A non-go command in the same file is nobody's business of
// this rule.
func TestGoEnvRefusesTheCallersOwnEnviron(t *testing.T) {
	root := goEnvFixtureTree(t, "rawenviron.go.txt")
	res, err := CheckGoEnv(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("append(os.Environ(), ...) is one refusal and git is none, got %d: %+v", len(res.Findings), res.Findings)
	}
	if res.Findings[0].Func != "buildTool" {
		t.Errorf("func = %q, want buildTool", res.Findings[0].Func)
	}
}

// 4. A row that names no offender is refused, so the list can only shrink.
func TestGoEnvRefusesAStaleAllowlistRow(t *testing.T) {
	root := goEnvFixtureTree(t, "mutate_fixed.go.txt")
	list := filepath.Join(t.TempDir(), "allow.txt")
	if err := os.WriteFile(list, []byte("internal/fixture/fixture.go:20 inherit 2026-09-18 fixed since\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckGoEnv(root, list)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stale) != 1 {
		t.Fatalf("a row naming no offender is one refusal, got %d: %+v", len(res.Stale), res.Stale)
	}
	if res.Stale[0].Remedy != GoEnvRemedyAllow {
		t.Errorf("remedy = %q, want %q", res.Stale[0].Remedy, GoEnvRemedyAllow)
	}
}

// 5. A row holds an offender still, and the run stays green with it counted.
func TestGoEnvAllowlistHoldsOneOffender(t *testing.T) {
	root := goEnvFixtureTree(t, "mutate_prefix.go.txt")
	list := filepath.Join(t.TempDir(), "allow.txt")
	if err := os.WriteFile(list, []byte("internal/fixture/fixture.go:1 inherit 2026-09-18 predates the checker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckGoEnv(root, list)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || res.Allowlisted != 1 {
		t.Fatalf("the row holds the offender: refused=%d allowlisted=%d %+v", res.Refused(), res.Allowlisted, res.Findings)
	}
}

// 6. The help line the class test is entered under, word for word as
// docs/SPEC-CI.md prints it.
func TestGoEnvVerbLineMatchesTheSpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), GoEnvVerbLine) {
		t.Errorf("the goenv verb line is not in docs/SPEC-CI.md:\n%s", GoEnvVerbLine)
	}
}

// 7. The class test itself. Every .go file under cmd/ and internal/ -- tests
// included, because a helper that builds a binary is a tool spawning go like
// any verb -- must build its child `go` command's environment with
// goenv.Clean. The caller's GOFLAGS is not allowed to reach a go command whose
// output this repository parses: CI's `make test` exports GOFLAGS=-json, and
// on 2026-09-18 that made `nova-review mutate` report red=2 green=0 on a range
// that is red=1 green=1, failing three legs of integration-4 (#1332).
func TestGoEnvClassRuleHoldsOverTheRepository(t *testing.T) {
	res, err := CheckGoEnv(repoRoot(t), goEnvAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Findings {
		t.Errorf("%s", f.Render())
	}
	for _, s := range res.Stale {
		t.Errorf("%s lists %s:%d, but nothing there inherits the environment any more; %s",
			goEnvAllowlistPath, s.File, s.Line, GoEnvRemedyAllow)
	}
	if res.Files == 0 {
		t.Fatal("the walk read no files; the repository root is wrong")
	}
	if res.Refused() != 0 {
		t.Fatalf("%s", res.FailLine())
	}
}
