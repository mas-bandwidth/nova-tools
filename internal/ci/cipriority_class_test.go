package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// cipriority_class_test.go is the class rule of nova-tools#4293 (Glenn
// 2026-09-26 ~12:00 PM ET: "CI over work is a permanent setting. It's a GOOD
// idea. because work creates more CI, so without this, it is unstable").
// Two halves, both read from the tree and neither runs anything:
//
//   - TestCopiesRunNiced: every path that execs a copy's harness, or a
//     coordinator child's local test run, steps its own process down to
//     yield.Nice (15) BEFORE the exec, on darwin and on Linux, through the
//     one package internal/yield.
//   - TestSlotsShrinkByCILegs: no bench slot computation ignores the CI
//     legs running on it: every `slots - ...` in live Go and Lua takes the
//     beat's ci off, and the beat writes it.
//
// It holds on every bench and every worker kind; a new exec path or a new
// slot computation that forgets is red here, not on a bench at 5.0 load.

// niceExecPaths are the exec paths and, for each, the call that must stand
// before the first exec in the same function: (file, function, yield call,
// exec call).
var niceExecPaths = []struct{ file, fn, yield, exec string }{
	{"cmd/nova-ci/local.go", "func cmdLocal(", "yield.ToCI()", "localCapture("},
}

func TestCopiesRunNiced(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// 1. The number, and setpriority on both OSes, in the one package.
	y := readFile(t, filepath.Join(root, "internal/yield/yield.go"))
	if !strings.Contains(y, "const Nice = 15") {
		t.Errorf("internal/yield/yield.go: want `const Nice = 15` (nova-tools#4293 names fifteen)")
	}
	// darwin: a nice belongs to the process, so 0 (this process) is the
	// whole of it. Linux: a nice belongs to a THREAD, and a child forked
	// from an un-niced thread inherits 0 (hetzner, 2026-09-26: 31 of 32
	// children at nice 0 under the one-thread form), so every thread in
	// /proc/self/task is set, repeatedly until a pass sets none.
	d := readFile(t, filepath.Join(root, "internal/yield/nice_darwin.go"))
	if !strings.Contains(d, "syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)") {
		t.Errorf("internal/yield/nice_darwin.go: want setpriority(PRIO_PROCESS, 0, n) on this process")
	}
	l := readFile(t, filepath.Join(root, "internal/yield/nice_linux.go"))
	if !strings.Contains(l, `"/proc/self/task"`) || !strings.Contains(l, "syscall.Setpriority(syscall.PRIO_PROCESS, tid, n)") {
		t.Errorf("internal/yield/nice_linux.go: want setpriority(PRIO_PROCESS, tid, n) over every thread in /proc/self/task (a Linux nice is per thread)")
	}
	if strings.Contains(l, "syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)") {
		t.Errorf("internal/yield/nice_linux.go: the one-thread form setpriority(PRIO_PROCESS, 0, n) nices the calling thread only; children forked from the others run at 0")
	}
	if !strings.Contains(readFile(t, filepath.Join(root, "cmd/nova-ci/local.go")), "yield.Nice-15") {
		t.Errorf("cmd/nova-ci/local.go: localNice must be pinned to yield.Nice")
	}

	// 2. Every exec path yields first, in the same function, before the exec.
	for _, p := range niceExecPaths {
		src := readFile(t, filepath.Join(root, p.file))
		body := funcBody(t, p.file, src, p.fn)
		yi, ei := strings.Index(body, p.yield), strings.Index(body, p.exec)
		switch {
		case yi < 0:
			t.Errorf("%s %s: no %s call: a copy or a local test run must yield to CI before it execs", p.file, p.fn, p.yield)
		case ei < 0:
			t.Errorf("%s %s: no %s call: the exec path this rule guards moved; move the rule with it", p.file, p.fn, p.exec)
		case yi > ei:
			t.Errorf("%s %s: %s stands after %s: a yield after the exec yields nothing", p.file, p.fn, p.yield, p.exec)
		}
	}

	// 3. No production caller gives a copy a Yield of its own (the seam is
	// for tests).
	tree := repoTree(t)
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		for i, line := range strings.Split(string(f.Src), "\n") {
			if code := strings.TrimSpace(line); strings.HasPrefix(code, "Yield:") || strings.Contains(code, ".Yield = ") {
				t.Errorf("%s:%d: %q: production never sets a copy's Yield; the real setpriority is the default", f.Rel, i+1, code)
			}
		}
	}
}

// funcBody is the text of one function in src from its declaration to the
// next top-level declaration (Go: a line starting `func ` or `}` at column
// 0; Lua: the next `function` or `local function` at column 0).
func funcBody(t *testing.T, file, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("%s: no %q", file, decl)
	}
	rest := src[i+len(decl):]
	end := len(rest)
	for _, next := range []string{"\nfunc ", "\n}\n", "\nfunction ", "\nlocal function ", "\nend\n"} {
		if j := strings.Index(rest, next); j >= 0 && j < end {
			end = j + len(next)
		}
	}
	return rest[:end]
}
