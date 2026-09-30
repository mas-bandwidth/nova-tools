package ci

// The bench standard asks whether `go` and `sbcl` can be EXECUTED inside the
// sandbox wall, not merely whether they are ON PATH. A card does not run in the
// bench user's shell: it runs behind the sandbox wall, whose linux read roots are
// the system table of internal/sandbox/wrap_linux.go plus the toolchain roots of
// internal/swarm/toolchain.go (sdk with EXECUTE, go/pkg/mod without). An
// interpreter at $HOME/.local/bin/sbcl satisfies `command -v` and is `Permission
// denied` inside the wall, so a bench passes the standard and every card on it
// dies. The wall is right, so the standard asks the wall's question.
//
// The behaviour is held by the standard's own unit tests (tools/benchstandard,
// wall_test.go: a tool under $HOME/.local/bin drifts, a tool under $HOME/sdk does
// not, the resolver directory is granted). What is held here is the agreement
// between the two lists, which no test of either side alone can see.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wallReadRootsBegin/End bracket the standard's copy of the wall's linux read-root table, as
// the NOVA_TOOLCHAIN_ROOTS markers bracket its copy of the toolchain roots.
const (
	wallReadRootsBegin = "// NOVA_WALL_READ_ROOTS BEGIN"
	wallReadRootsEnd   = "// NOVA_WALL_READ_ROOTS END"
)

// TestBenchStandardAndTheWallNameTheSameReadRoots is the hold on the standard's
// executable-root check: it first shipped with a HAND-PICKED SUBSET of the wall's roots (no
// /etc, no /run/systemd/resolve, no /dev, no /proc), so a toolchain the wall executes under one
// of those was reported "under NO read root" and a conforming bench was rejected. The two
// lists are ONE list, read here from both places -- linuxReadRoots in
// internal/sandbox/wrap_linux.go (a linux-tagged unexported var, so read as source, which
// also keeps this test running on the darwin benches) and the marker block in the standard's
// Go -- and must match in both directions and in order.
func TestBenchStandardAndTheWallNameTheSameReadRoots(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	wallSrc, err := os.ReadFile(filepath.Join(root, "internal", "sandbox", "wrap_linux.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^var linuxReadRoots = \[\]string\{([^}]*)\}`).FindSubmatch(wallSrc)
	if m == nil {
		t.Fatal("internal/sandbox/wrap_linux.go no longer declares `var linuxReadRoots = []string{...}` on one line; update this test's reader with it")
	}
	var fromWall []string
	for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllSubmatch(m[1], -1) {
		fromWall = append(fromWall, string(q[1]))
	}
	if len(fromWall) == 0 {
		t.Fatal("the wall's linux read-root table parsed empty")
	}

	body := string(rawBenchStandard(t, root))
	fromStandard := quotedBetween(t, body, wallReadRootsBegin, wallReadRootsEnd)
	if strings.Join(fromStandard, " ") != strings.Join(fromWall, " ") {
		t.Errorf("the executable-root check and the wall name different linux read roots:\n  %s: %v\n  internal/sandbox/wrap_linux.go linuxReadRoots: %v\nThey are ONE list: a root the wall grants and the check omits rejects a conforming bench. Edit both sides together.",
			benchStandardSource, fromStandard, fromWall)
	}
	// The block is only half the rule: the check must actually read it, with the
	// per-machine resolver directory and $HOME/sdk, or the block is decoration.
	i := strings.Index(body, "func wallExecRoots(")
	if i < 0 {
		t.Fatalf("%s no longer declares wallExecRoots, the function that joins the wall's table, the resolver directory and $HOME/sdk", benchStandardSource)
	}
	fn := body[i:]
	if j := strings.Index(fn, "\n}\n"); j >= 0 {
		fn = fn[:j]
	}
	for _, want := range []string{"wallReadRoots", "resolvDir", `"sdk"`} {
		if !strings.Contains(fn, want) {
			t.Errorf("wallExecRoots does not read %s", want)
		}
	}
	if !strings.Contains(body, "wallExecRoots(w.home, resolvDir)") {
		t.Errorf("the executable-root check does not call wallExecRoots")
	}
}

// quotedBetween is the double-quoted strings of the lines between two markers, in order.
func quotedBetween(t *testing.T, body, begin, end string) []string {
	t.Helper()
	_, after, found := strings.Cut(body, begin)
	if !found {
		t.Fatalf("%s carries no %q marker", benchStandardSource, begin)
	}
	block, _, found := strings.Cut(after, end)
	if !found {
		t.Fatalf("%s carries no %q marker", benchStandardSource, end)
	}
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(line, -1) {
			out = append(out, q[1])
		}
	}
	return out
}
