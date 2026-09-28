package ci

// The bench standard used to ask only whether `go` and `sbcl` were ON PATH. A card does not
// run in the bench user's shell: it runs behind the sandbox wall, whose linux read roots are
// the system table of internal/sandbox/wrap_linux.go plus the toolchain roots of
// internal/swarm/toolchain.go (sdk with EXECUTE, go/pkg/mod without). An interpreter at
// $HOME/.local/bin/sbcl satisfies `command -v` and is `Permission denied` inside the wall,
// so the bench passes the standard and every card on it dies -- which is exactly what
// happened: every lisp card was forced onto the one bench whose sbcl is /usr/bin/sbcl,
// E09-G1 taking 1036 s on vision against 248-393 s for the same class on space, and the
// r1785 worker on mini fetching an SBCL 2.4.0 of its own into $TMPDIR before it could run a
// test. The wall was right and the standard was silent.
//
// This is the #1706 shape, the one benchstandard_disk_test.go already uses: run the REAL
// tools/bench-standard.sh with a FAKE PATH layout and a HOME of its own. The layout is the
// whole method -- the test cannot move a real bench's sbcl, and a test that only passes
// where the developer's own sbcl happens to live is not a test.

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
	wallReadRootsBegin = "# NOVA_WALL_READ_ROOTS BEGIN"
	wallReadRootsEnd   = "# NOVA_WALL_READ_ROOTS END"
)

// TestBenchStandardAndTheWallNameTheSameReadRoots is Stella's hold on #1870 made a class
// test: (3c) first shipped with a HAND-PICKED SUBSET of the wall's roots (no /etc, no
// /run/systemd/resolve, no /dev, no /proc), so a toolchain the wall executes under one of
// those was reported "under NO read root" and a conforming bench was rejected. The two
// lists are ONE list, read here from both places -- linuxReadRoots in
// internal/sandbox/wrap_linux.go (a linux-tagged unexported var, so read as source, which
// also keeps this test running on the darwin benches) and the marker block in the script --
// and must match in both directions and in order.
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

	script, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(benchStandardScript)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(script)
	b, e := strings.Index(body, wallReadRootsBegin), strings.Index(body, wallReadRootsEnd)
	if b < 0 || e < b {
		t.Fatalf("%s carries no %q ... %q block: check (3c)'s roots must be the wall's table, bracketed", benchStandardScript, wallReadRootsBegin, wallReadRootsEnd)
	}
	sm := regexp.MustCompile(`NOVA_WALL_READ_ROOTS="([^"]*)"`).FindStringSubmatch(body[b:e])
	if sm == nil {
		t.Fatalf("the %s block in %s sets no NOVA_WALL_READ_ROOTS=\"...\"", wallReadRootsBegin, benchStandardScript)
	}
	fromStandard := strings.Fields(sm[1])
	if strings.Join(fromStandard, " ") != strings.Join(fromWall, " ") {
		t.Errorf("check (3c) and the wall name different linux read roots:\n  %s: %v\n  internal/sandbox/wrap_linux.go linuxReadRoots: %v\nThey are ONE list: a root the wall grants and (3c) omits rejects a conforming bench. Edit both sides together.",
			benchStandardScript, fromStandard, fromWall)
	}
	// The block is only half the rule: the loop must actually read it (and the per-machine
	// resolver directory), or the block is decoration.
	if !strings.Contains(body, `for root in $NOVA_WALL_READ_ROOTS $wall_resolv_dir "$HOME_DIR/sdk"; do`) {
		t.Errorf("check (3c)'s root loop does not read $NOVA_WALL_READ_ROOTS, the resolver directory and $HOME/sdk")
	}
}
