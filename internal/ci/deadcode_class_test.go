package ci

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// deadcode_class_test.go holds the tree free of dead code (nova-tools #4312).
//
// The audit of 2026-09-26 found about 1,800 unreachable functions with
// `deadcode ./cmd/...` and 155 unused identifiers with `staticcheck -checks
// U1000 ./...`, both run by hand. A hand step is a step that is not taken, so
// the two runs are class tests here: each one shells out to the tool as a `go
// tool` dependency of go.mod (no install, the toolchain builds it from the
// module cache), reads what it reports, and compares it against a shrink-only
// allowlist under testdata/, one `pkg.Name <reason>` per line.
//
// The allowlists were generated from the tree the day the rule landed, so the
// rule holds the tree THERE: a function the tool reports that is not listed is
// red with the remedy (delete it, or list it with a reason); a listed function
// the tool no longer reports -- it was deleted, or it is reachable now -- is red
// too, because the list only shrinks and a stale row is cover. A row is matched
// by package and name, never by line, so a merge that moves a file's lines does
// not turn dev red.
//
// Why both tools: deadcode reads the call graph from the main packages down
// (RTA), so it sees an exported function nothing calls; U1000 reads each
// package by itself, tests included, so it sees an unused test helper, const,
// var, type or field that deadcode never loads. Neither one subsumes the other.
//
// Both tests are held to the two-minute law with everyone else in the shard:
// measured on the Studio at dev 635eaca, deadcode is 4.5 s wall (13 s CPU) and
// staticcheck 14.5 s wall cold (185 s CPU, which includes the one-time build of
// the tool into GOCACHE) and 1.3 s warm from its own result cache.

const (
	deadcodeAllowlistPath = "testdata/deadcode_allowlist.txt"
	u1000AllowlistPath    = "testdata/u1000_allowlist.txt"
)

// deadcodeLineRe reads one line of `deadcode`'s default output:
// `cmd/nova-merge/batch.go:178:6: unreachable func: ciTestArgs`.
var deadcodeLineRe = regexp.MustCompile(`^(\S+?):\d+:\d+: unreachable func: (\S+)$`)

// u1000LineRe reads one line of `staticcheck -checks U1000`'s output:
// `cmd/nova-board/firstrun_test.go:313:6: func shapesOf is unused (U1000)`.
// The kind is func, field, type, var or const; a method is `(*lab).atomicHost`.
var u1000LineRe = regexp.MustCompile(`^(\S+?):\d+:\d+: (func|field|type|var|const) (.+) is unused \(U1000\)$`)

func TestNoNewUnreachableCode(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	out := goToolOutput(t, root, "deadcode", "./cmd/...")
	found := map[string]int{}
	total := 0
	for _, line := range nonEmptyLines(out) {
		m := deadcodeLineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("deadcode printed a line this reader does not know; fix deadcodeLineRe, not the tree: %q", line)
		}
		found[packageKey(m[1], m[2])]++
		total++
	}
	holdToAllowlist(t, deadcodeAllowlistPath, found,
		"is unreachable from every main package; delete it, or list it in %s with a reason",
		"but deadcode no longer reports it (it is reachable now, or gone); delete the stale row (the list only shrinks)")
	t.Logf("deadcode reports %d unreachable functions under cmd/", total)
}

func TestStaticcheckU1000(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	out := goToolOutput(t, root, "staticcheck", "-checks", "U1000", "./...")
	found := map[string]int{}
	total := 0
	for _, line := range nonEmptyLines(out) {
		m := u1000LineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("staticcheck printed a line this reader does not know; fix u1000LineRe, not the tree: %q", line)
		}
		found[packageKey(m[1], m[3])]++
		total++
	}
	holdToAllowlist(t, u1000AllowlistPath, found,
		"is unused (staticcheck U1000); delete it, or list it in %s with a reason",
		"but staticcheck no longer reports it (it is used now, or gone); delete the stale row (the list only shrinks)")
	t.Logf("staticcheck U1000 reports %d unused identifiers", total)
}

// packageKey is the row an allowlist carries for a finding: the package's
// repo-relative directory, a dot, and the name the tool printed
// (`cmd/nova-merge.ciTestArgs`, `cmd/nova-swarm.liveSampler.StopWord`,
// `cmd/nova-merge.(*lab).atomicHost`). No line number, so a moved line is not a
// changed row.
func packageKey(file, name string) string {
	return path.Dir(filepath.ToSlash(file)) + "." + name
}

// holdToAllowlist compares what the tool found against the shrink-only list at
// allowPath in both directions. Rows are a multiset: U1000 can report two
// unused fields of the same name in one package, and each one is a row.
func holdToAllowlist(t *testing.T, allowPath string, found map[string]int, newFmt, staleFmt string) {
	t.Helper()
	allow := readReasonedAllowlist(t, allowPath)
	var violations []string
	for key, n := range found {
		if n > allow[key] {
			violations = append(violations, fmt.Sprintf("%s %s", key, fmt.Sprintf(newFmt, allowPath)))
		}
	}
	for key, n := range allow {
		if n > found[key] {
			violations = append(violations, fmt.Sprintf("%s lists %s, %s", allowPath, key, staleFmt))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// readReasonedAllowlist reads `key <reason>` rows into a multiset; a row with
// no reason is refused, so every exception says why it is there.
func readReasonedAllowlist(t *testing.T, allowPath string) map[string]int {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(allowPath))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, reason, _ := strings.Cut(text, " ")
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d: %q carries no reason; every row is `pkg.Name <reason>`", allowPath, line, text)
			continue
		}
		out[key]++
	}
	return out
}

// goToolOutput runs `go tool <name> <args...>` at root and returns its standard
// output. The child gets a cleaned environment (goenv.Clean: CI's GOFLAGS=-json
// would otherwise turn the tool's own `go list` into a JSON stream). The test
// is skipped, with the line that says so, only when the toolchain has no such
// tool: the tool directive in go.mod is what makes it resolve, and a toolchain
// that cannot read the directive would report every tree clean.
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
		t.Skipf("`go tool %s` does not resolve on this toolchain (%s); go.mod carries the tool directive, so this is a toolchain older than the go line, not a clean tree: %s",
			name, cmd.Path, strings.TrimSpace(stderr.String()))
	}
	// staticcheck exits 1 when it has findings and 0 when it has none; any
	// other exit, or anything on stderr, is the tool failing to read the tree.
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || stderr.Len() > 0) {
		t.Fatalf("go tool %s %s: %v\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("go tool %s %s wrote to stderr:\n%s", name, strings.Join(args, " "), stderr.String())
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
