//go:build functional

package ci

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// deadcode_functional_test.go executes the heavy static analysis tools
// (deadcode and staticcheck U1000) under the functional tier.
// Fast allowlist parsing, merge-base shrink comparisons, and syntax
// validations remain in deadcode_class_test.go under the unit tier.

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
		key, isFinding, err := parseU1000Line(line)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if isFinding {
			found[key] = true
			total++
		}
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

func readDeadcodeAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	path := deadcodeAllowlistPath()
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
	path := u1000AllowlistPath()
	allow := loadAllowlist(t, path, shrinkOnly)
	for _, row := range allow.Rows() {
		if _, reason, _ := strings.Cut(row.Text, " "); strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d: %q carries no reason; every row is `pkg.Name <reason>`", path, row.Line, row.Text)
		}
	}
	return allow
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
