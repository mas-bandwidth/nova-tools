package ci

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// deadcode_class_test.go holds the tree free of dead and unreachable code
// (nova-tools #4312).
//
// An unreachable function is compiled, vetted, read and merged on every commit
// for nobody. Two complementary tools guard against this debt:
//
//   - deadcode (golang.org/x/tools/cmd/deadcode) reads the call graph starting
//     from the main packages under cmd/ down (Rapid Type Analysis). It sees
//     exported and unexported functions that no command reaches.
//   - staticcheck U1000 (honnef.co/go/tools/cmd/staticcheck) analyzes each package
//     individually, including test files. It catches unused internal test helpers,
//     types, variables, constants and struct fields that deadcode never visits.
//
// Both tools are declared as `tool` directives in go.mod, built on demand from
// the module cache without global installs, and run with a cleaned environment
// (goenv.Clean). Each check compares findings against a shrink-only allowlist
// under testdata/, keyed by `pkg.Name` (package path relative to repo root, a dot,
// and the identifier name; never line numbers).
//
// Because dead and unused code depends on build tags and GOOS (Linux and Darwin
// have different system integrations under cmd/nova-sandbox and internal/wake),
// each platform has an explicit shrink-only allowlist under testdata/.
//
// The allowlists only shrink: a new finding fails with the remedy to delete or
// list it; an allowlist row whose finding is no longer reported is stale and
// fails until removed. Under NOVA_CI_UPDATE=1, stale rows are automatically
// pruned and ceiling counts lowered.

const (
	deadcodeAllowlistDarwinPath = "testdata/deadcode_allowlist_darwin.txt"
	deadcodeAllowlistLinuxPath  = "testdata/deadcode_allowlist_linux.txt"
	u1000AllowlistDarwinPath    = "testdata/u1000_allowlist_darwin.txt"
	u1000AllowlistLinuxPath     = "testdata/u1000_allowlist_linux.txt"
)

func deadcodeAllowlistPath() string {
	if runtime.GOOS == "linux" {
		return deadcodeAllowlistLinuxPath
	}
	return deadcodeAllowlistDarwinPath
}

func u1000AllowlistPath() string {
	if runtime.GOOS == "linux" {
		return u1000AllowlistLinuxPath
	}
	return u1000AllowlistDarwinPath
}

// deadcodeLineRe reads one line of `deadcode`'s default output:
// `cmd/nova-table/cell.go:19:25: unreachable func: application.cmdCell`.
var deadcodeLineRe = regexp.MustCompile(`^(\S+?):\d+:\d+: unreachable func: (\S+)$`)

// u1000LineRe reads one line of `staticcheck -checks U1000`'s output:
// `cmd/nova-bus/norace_test.go:6:7: const raceEnabled is unused (U1000)`.
var u1000LineRe = regexp.MustCompile(`^(\S+?):\d+:\d+: (func|field|type|var|const) (.+) is unused \(U1000\)$`)

func TestDeadcode(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entrypoints := livingPackages(t, "cmd")
	out := goToolOutput(t, root, "deadcode", entrypoints...)
	found := map[string]bool{}
	total := 0
	for _, line := range nonEmptyLines(out) {
		m := deadcodeLineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("deadcode printed a line this reader does not know; fix deadcodeLineRe, not the tree: %q", line)
		}
		found[packageKey(m[1], m[2])] = true
		total++
	}

	allow := readDeadcodeAllowlist(t)
	allowPath := deadcodeAllowlistPath()
	res := allowlist.Check(t, allow, found)
	for _, key := range res.Unlisted {
		t.Errorf("%s is unreachable from every main package; delete it, or list it in %s with a reason", key, allowPath)
	}
	for _, row := range res.Stale {
		t.Errorf("%s lists %s, but deadcode no longer reports it (it is reachable now, or gone); delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)", allowPath, row.Key)
	}
	t.Logf("deadcode reports %d unreachable functions under cmd/ on %s", total, runtime.GOOS)
}

func TestU1000(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pkgs := livingPackages(t, "")
	args := append([]string{"-checks", "U1000"}, pkgs...)
	out := goToolOutput(t, root, "staticcheck", args...)
	found := map[string]bool{}
	total := 0
	for _, line := range nonEmptyLines(out) {
		if strings.Contains(line, "build constraints exclude all Go files") || strings.HasSuffix(line, "(compile)") {
			continue
		}
		m := u1000LineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("staticcheck printed a line this reader does not know; fix u1000LineRe, not the tree: %q", line)
		}
		found[packageKey(m[1], m[3])] = true
		total++
	}

	allow := readU1000Allowlist(t)
	allowPath := u1000AllowlistPath()
	res := allowlist.Check(t, allow, found)
	for _, key := range res.Unlisted {
		t.Errorf("%s is unused (staticcheck U1000); delete it, or list it in %s with a reason", key, allowPath)
	}
	for _, row := range res.Stale {
		t.Errorf("%s lists %s, but staticcheck no longer reports it (it is used now, or gone); delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)", allowPath, row.Key)
	}
	t.Logf("staticcheck U1000 reports %d unused identifiers on %s", total, runtime.GOOS)
}

// livingPackages returns repo-relative package paths ("./cmd/nova-bus", "./internal/ci", ...)
// for all live packages in the repository, matching the selection in
// .github/scripts/live-packages.sh and excluding retired packages named in
// deprecated/PACKAGES. When prefix is non-empty, only packages under that prefix
// (e.g. "cmd") are returned.
func livingPackages(t *testing.T, prefix string) []string {
	t.Helper()
	root := repoRoot(t)
	lt := loadLiveTree(t, root)
	tree := repoTree(t)
	seen := map[string]bool{}
	var pkgs []string
	for _, f := range tree.Files {
		if !f.Go || f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || f.HasDirNamed("testprobe") {
			continue
		}
		dir := path.Dir(f.Rel)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if prefix != "" && dir != prefix && !strings.HasPrefix(dir, prefix+"/") {
			continue
		}
		if lt.Package(dir) {
			pkgs = append(pkgs, "./"+dir)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// packageKey is the key an allowlist carries for a finding: the package directory
// relative to repo root, a dot, and the symbol name.
func packageKey(file, name string) string {
	return path.Dir(filepath.ToSlash(file)) + "." + name
}

func readDeadcodeAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	path := deadcodeAllowlistDarwinPath
	if runtime.GOOS == "linux" {
		path = deadcodeAllowlistLinuxPath
	}
	allow := loadAllowlist(t, path, shrinkOnly)
	for _, row := range allow.Rows() {
		if _, reason, _ := strings.Cut(row.Text, " "); strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d: %q carries no reason; every row is `pkg.Name <reason>`", path, row.Line, row.Text)
		}
	}
	return allow
}

func readU1000Allowlist(t *testing.T) *allowlist.List {
	t.Helper()
	path := u1000AllowlistDarwinPath
	if runtime.GOOS == "linux" {
		path = u1000AllowlistLinuxPath
	}
	allow := loadAllowlist(t, path, shrinkOnly)
	for _, row := range allow.Rows() {
		if _, reason, _ := strings.Cut(row.Text, " "); strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d: %q carries no reason; every row is `pkg.Name <reason>`", path, row.Line, row.Text)
		}
	}
	return allow
}

// deadcodeAllowlistBase is the commit allowlists are compared against: the merge
// base of HEAD and origin/dev when available and not HEAD itself, else HEAD's first parent.
func deadcodeAllowlistBase(root string) (string, error) {
	if _, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev^{commit}"); err == nil {
		mb, mbErr := gitOut(root, "merge-base", "HEAD", "refs/remotes/origin/dev")
		head, headErr := gitOut(root, "rev-parse", "HEAD")
		if mbErr == nil && headErr == nil && strings.TrimSpace(mb) != strings.TrimSpace(head) {
			return strings.TrimSpace(mb), nil
		}
	}
	return firstParent(root)
}

func checkAllowlistOnlyShrinksAgainstMergeBase(t *testing.T, root, allowPath string) {
	t.Helper()
	parent, err := deadcodeAllowlistBase(root)
	if err != nil {
		t.Fatal(err)
	}
	relPath := "internal/ci/" + allowPath
	if _, err := gitOut(root, "cat-file", "-e", parent+":"+relPath); err != nil {
		t.Logf("%s is not in the merge base %s: this change is the allowlist seed", relPath, parent[:9])
		return
	}
	baseText, err := gitOut(root, "show", parent+":"+relPath)
	if err != nil {
		t.Fatal(err)
	}
	baseList, err := allowlist.Parse(relPath, baseText, shrinkOnly)
	if err != nil {
		t.Fatalf("%s at %s: %v", relPath, parent[:9], err)
	}
	headList, err := allowlist.Load(filepath.Join(root, filepath.FromSlash(relPath)), shrinkOnly)
	if err != nil {
		t.Fatalf("%s at HEAD: %v", relPath, err)
	}
	for _, row := range headList.Rows() {
		if !baseList.Has(row.Key) {
			t.Errorf("%s adds the row %q, which its merge base %s does not have; the allowlist only shrinks: delete the dead symbol or drop the row",
				relPath, row.Key, parent[:9])
		}
	}
	headCeil, headHasCeil := headList.Ceiling()
	baseCeil, baseHasCeil := baseList.Ceiling()
	if headHasCeil && baseHasCeil && headCeil > baseCeil {
		t.Errorf("%s raises the ceiling from %d to %d (against merge base %s); ceiling only shrinks",
			relPath, baseCeil, headCeil, parent[:9])
	}
	if headList.Len() > baseList.Len() {
		t.Errorf("%s has %d rows, more than merge base %s (%d rows); allowlist only shrinks",
			relPath, headList.Len(), parent[:9], baseList.Len())
	}
}

// TestDeadcodeAllowlistOnlyShrinksAgainstMergeBase asserts that HEAD adds no row
// to either deadcode allowlist that its merge base lacks, and that ceilings never increase.
func TestDeadcodeAllowlistOnlyShrinksAgainstMergeBase(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, allowPath := range []string{deadcodeAllowlistDarwinPath, deadcodeAllowlistLinuxPath} {
		checkAllowlistOnlyShrinksAgainstMergeBase(t, root, allowPath)
	}
}

// TestU1000AllowlistOnlyShrinksAgainstMergeBase asserts that HEAD adds no row
// to either U1000 allowlist that its merge base lacks, and that ceilings never increase.
func TestU1000AllowlistOnlyShrinksAgainstMergeBase(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, allowPath := range []string{u1000AllowlistDarwinPath, u1000AllowlistLinuxPath} {
		checkAllowlistOnlyShrinksAgainstMergeBase(t, root, allowPath)
	}
}

// goToolOutput runs `go tool <name> <args...>` at root and returns its standard
// output. The child gets a cleaned environment (goenv.Clean).
func goToolOutput(t *testing.T, root, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("go", append([]string{"tool", name}, args...)...)
	cmd.Env = goenv.Clean(os.Environ())
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if strings.Contains(stderr.String(), "no such tool") {
		t.Skipf("`go tool %s` does not resolve on this toolchain (%s); go.mod carries the tool directive: %s",
			name, cmd.Path, strings.TrimSpace(stderr.String()))
	}
	// staticcheck exits 1 when it has findings and 0 when it has none; any
	// other exit, or anything on stderr, is the tool failing to read the tree.
	var errLines []string
	for _, l := range strings.Split(stderr.String(), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "go: downloading ") {
			errLines = append(errLines, l)
		}
	}
	realStderr := strings.Join(errLines, "\n")
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || len(errLines) > 0) {
		t.Fatalf("go tool %s %s: %v\n%s", name, strings.Join(args, " "), err, realStderr)
	}
	if len(errLines) > 0 {
		t.Fatalf("go tool %s %s wrote to stderr:\n%s", name, strings.Join(args, " "), realStderr)
	}
	return stdout.Bytes()
}

// nonEmptyLines splits tool output into its lines, blanks dropped.
func nonEmptyLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
