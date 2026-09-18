package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ci_net_test.go is the red-test contract of the net checker in
// docs/SPEC-CI.md, "The CI class test against a real network host on the CI
// path". Each fixture below is a real _test.go written into a throwaway tree
// and handed to CheckNet, so the checker is exercised on a tree given on the
// command line and never by reaching into the repository or the network. The
// fixtures live in testdata/ so the class test that walks the repo never reads
// the offenders it is meant to find.

// netFixtureTree writes one fixture under <tmp>/internal/fixture/fixture_test.go
// and returns the tree root, the way a caller hands the checker a --dir.
func netFixtureTree(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "net", name))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal", "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// netEmptyTree is a root with no _test.go at all, so the allowlist rule can be
// tested without a real offender standing behind the entry.
func netEmptyTree(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// netLineAt reads the line the finding named and returns it trimmed, so a test
// asserts on the offender itself rather than on a line number the fixture's
// comments can move.
func netLineAt(t *testing.T, root, rel string, line int) string {
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

// 1. A test carrying https://api.acme.com is refused with its file, line and
// host, and the remedy names httptest or a local fake.
func TestNetRefusesRealURLHost(t *testing.T) {
	root := netFixtureTree(t, "realurl.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a real URL host is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "url" {
		t.Errorf("kind = %q, want url", f.Kind)
	}
	if f.Host != "api.acme.com" {
		t.Errorf("host = %q, want api.acme.com", f.Host)
	}
	if f.Remedy != NetRemedy {
		t.Errorf("remedy = %q, want %q", f.Remedy, NetRemedy)
	}
	if got := netLineAt(t, root, f.File, f.Line); !strings.Contains(got, "api.acme.com/v1/status") {
		t.Errorf("finding names %s:%d = %q, want the real URL line", f.File, f.Line, got)
	}
}

// 2. A test whose only hosts are localhost, the loopback IPs, and the reserved
// test domains example.* / *.invalid / *.test is allowed.
func TestNetAllowsLocalAndReservedHosts(t *testing.T) {
	root := netFixtureTree(t, "allowed.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("local and reserved hosts are the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
	if res.Tests != 1 {
		t.Errorf("tests = %d, want 1", res.Tests)
	}
}

// 3. A file carrying //go:build nightly is exempt: the nightly suite is where
// the real network is allowed.
func TestNetAllowsNightlyBuildTag(t *testing.T) {
	root := netFixtureTree(t, "nightly.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("a nightly-tagged file is exempt, got %d refusals: %+v", res.Refused(), res.Findings)
	}
}

// 4. A file carrying //go:build soak is exempt, the same way.
func TestNetAllowsSoakBuildTag(t *testing.T) {
	root := netFixtureTree(t, "soak.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("a soak-tagged file is exempt, got %d refusals: %+v", res.Refused(), res.Findings)
	}
}

// 5. A bare host:port literal with a real host is refused.
func TestNetRefusesBareHostPort(t *testing.T) {
	root := netFixtureTree(t, "hostport.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a real host:port is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "hostport" {
		t.Errorf("kind = %q, want hostport", f.Kind)
	}
	if f.Host != "metrics.acme.com" {
		t.Errorf("host = %q, want metrics.acme.com", f.Host)
	}
}

// 6. A bare host:port on a local or reserved host is allowed.
func TestNetAllowsLocalHostPort(t *testing.T) {
	root := netFixtureTree(t, "allowedhostport.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("a local host:port is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
}

// 7. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a host, and the
// remedy says the file only shrinks.
func TestNetAllowlistGrowsRefused(t *testing.T) {
	root := netEmptyTree(t)
	allow := filepath.Join(t.TempDir(), "net-allowlist.txt")

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckNet(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())
	}

	if err := os.WriteFile(allow, []byte("internal/x/x_test.go:1 url 2026-09-17 parked here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckNet(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Stale) != 1 {
		t.Fatalf("adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	}
	if res.Stale[0].Remedy != NetRemedyAllow {
		t.Errorf("allowlist remedy = %q, want %q", res.Stale[0].Remedy, NetRemedyAllow)
	}

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckNet(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("removing the entry must be allowed, got %d refusals", res.Refused())
	}
}

// 8. A row allows one offender of its kind in its file wherever that offender
// now stands: the line in the row is for a reader and is never matched on. A
// second offender of the same kind in the file has no row and is refused.
func TestNetAllowlistSurvivesShiftedLines(t *testing.T) {
	root := netFixtureTree(t, "realurl.go.txt")
	first, err := CheckNet(root, "")
	if err != nil || len(first.Findings) != 1 {
		t.Fatalf("fixture must hold one real URL: %v %+v", err, first.Findings)
	}
	f := first.Findings[0]
	allow := filepath.Join(t.TempDir(), "net-allowlist.txt")
	row := fmt.Sprintf("%s:%d url 2026-09-17 written when the offender stood elsewhere\n", f.File, f.Line+40)
	if err := os.WriteFile(allow, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckNet(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || res.Allowlisted != 1 {
		t.Fatalf("a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.Refused(), res.Allowlisted, res.Stale)
	}

	// A second real URL in the same file has no row: the budget is one.
	path := filepath.Join(root, filepath.FromSlash(f.File))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	scheme := "http" + "s://"
	more := string(src) + "\nfunc TestSecondURL(t *testing.T) { _ = \"" + scheme + f.Host + "/again\" }\n"
	if err := os.WriteFile(path, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckNet(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Allowlisted != 1 {
		t.Fatalf("one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.Findings), res.Allowlisted)
	}
}

// TestNetOutputMatchesTheSpec pins the one-line grammar of the section: the OK
// line, the refusal line and the closing FAIL line, and the exit 2 a refusal
// costs.
func TestNetOutputMatchesTheSpec(t *testing.T) {
	root := netFixtureTree(t, "realurl.go.txt")
	res, err := CheckNet(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.OKLine(), "CI-NET OK tests=1 allowlisted=0 refused=0"; got != want {
		t.Errorf("clean line = %q, want %q", got, want)
	}
	if got, want := res.FailLine(), "CI-NET FAIL tests=1 allowlisted=0 refused=1"; got != want {
		t.Errorf("fail line = %q, want %q", got, want)
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", res.ExitCode())
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want one refusal, got %d", len(res.Findings))
	}
	line := res.Findings[0].Render()
	for _, want := range []string{
		"CI-NET file=internal/fixture/fixture_test.go line=",
		" host=api.acme.com ",
		`remedy="mock the endpoint with httptest or a local fake"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("refusal %q lacks %q", line, want)
		}
	}
}

// TestNetVerbLineMatchesTheSpec pins the help line the class test is entered
// under, word for word, to the section that prints it.
func TestNetVerbLineMatchesTheSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	if !strings.Contains(spec, NetVerbLine) {
		t.Errorf("the net verb line is not in docs/SPEC-CI.md:\n%s", NetVerbLine)
	}
}

// TestNoRealNetworkHostsOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only real hosts that pass are the
// ones the allowlist already names. The count is the truth about the CI path
// whether or not the lines printed.
func TestNoRealNetworkHostsOnTheCIPath(t *testing.T) {
	root := repoRoot(t)
	allow := filepath.Join(root, "internal", "ci", "testdata", "net-allowlist.txt")
	res, err := CheckNet(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(res.OKLine())
	for _, f := range res.Findings {
		t.Error(f.Render())
	}
	for _, f := range res.Stale {
		t.Error(f.Render())
	}
}
