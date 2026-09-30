package ci

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
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

// slotSubtraction finds a slot computation in Lua: `slots - <something>`
// (d.slots), or the desired hash's slots field read as a number and
// subtracted from (`(tonumber(desired[1]) or 0) -`, the form deal.lua's
// in-Redis re-check used and the first pattern missed).
var slotSubtraction = regexp.MustCompile(`\b[sS]lots\s*-\s*[A-Za-z(]|\bdesired\b[^\n]*\)\s*-\s*[A-Za-z(]`)

// goSlotLines are the lines of a Go file holding a slot computation: a
// subtraction whose left operand is slots or Slots (`slots - ci`,
// `b.Slots - b.CI`). Go is read by its syntax, not by slotSubtraction: over
// the whole live tree the pattern also matched the flag name --slots-store
// inside strings (cmd/nova-swarm, internal/swarm), which is no subtraction.
func goSlotLines(fset *token.FileSet, f *ast.File) map[int]bool {
	lines := map[int]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		b, ok := n.(*ast.BinaryExpr)
		if !ok || b.Op != token.SUB {
			return true
		}
		name := ""
		switch x := b.X.(type) {
		case *ast.Ident:
			name = x.Name
		case *ast.SelectorExpr:
			name = x.Sel.Name
		}
		if name == "slots" || name == "Slots" {
			lines[fset.Position(b.Pos()).Line] = true
		}
		return true
	})
	return lines
}

// namesCILegs is what a slot computation must name to be taking the CI legs
// off: the Go field or variable ci/CI, or the Lua TM.ci_legs.
var namesCILegs = regexp.MustCompile(`\bci\b|\bCI\b|TM\.ci_legs\(`)

// staticShareExempt are the slot computations that are not free slots and so
// need not take the CI legs off, each with its reason. A line is exempt only
// by its file and its exact code; every other line is judged by the rule.
//
// internal/config/width.go: the sprint's width is the static share nova-config
// declares, the machine's slots less the slots of the friends charged to it.
// It is the same on every read of the same rows, so `nova-sprint fleet sync`
// can write it and read it back unchanged; a width that followed the CI legs
// would drift with every CI run. The legs are taken off at take time, by the
// lease a member holds in the machine's one slot store (`nova-swarm slots
// take`, cmd/nova-swarm/slots.go cmdSlotsTake over internal/swarm
// TakeSlotLeases; H2's lease-before-take: the member takes min(width - held,
// free leases)), so no slot is oversubscribed.
var staticShareExempt = map[string]string{
	"internal/config/width.go": "w.Width = w.Slots - w.Charged",
}

func TestSlotsShrinkByCILegs(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// 1. The beat writes the legs it counted, every beat.
	lua := readFile(t, filepath.Join(root, "internal/nsprint/fn/lua/presence.lua"))
	if !strings.Contains(funcBody(t, "presence.lua", lua, "local function bench_beat("), "'ci', args[21] or ''") {
		t.Errorf("presence.lua bench_beat: the beat must write the CI leg count as ci (args[21]) every beat")
	}
	if !strings.Contains(funcBody(t, "presence.lua", lua, "local function friend_beat("), "'ci', args[5] or ''") {
		t.Errorf("presence.lua friend_beat: a friend's beat must write the CI leg count of its machine as ci (args[5]) every beat")
	}
	// The Go side of the beat, where the legs are measured (cmd/nova-sprint's
	// life.go, life.CILegsNow for the first beat and every tick), was read
	// here; nova-sprint is deprecated (Glenn 2026-09-27: deprecated code is
	// not tested and never blocks CI), and no live tool writes a bench beat,
	// so that clause has no live subject. The Lua the beat calls is live
	// (internal/nsprint/fn, kept in deprecated/PACKAGES) and is read above.

	// 2. Every slot computation, Go and Lua, in the live packages (liveTree,
	// the reading CI's selection uses; a class rule over deprecated code is
	// a test of it) takes the legs off. Comments and tests do not count; a
	// line does.
	lt := loadLiveTree(t, root)
	var checked int
	tree := repoTree(t)
	for _, f := range tree.Files {
		if !f.InAnyDir("cmd", "internal") || f.Test {
			continue
		}
		if !strings.HasSuffix(f.Rel, ".go") && !strings.HasSuffix(f.Rel, ".lua") {
			continue
		}
		if !lt.File(f.Rel) {
			continue
		}
		src := f.Src
		if len(src) == 0 {
			raw, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			src = raw
		}
		var goLines map[int]bool
		if f.Go && f.AST != nil {
			goLines = goSlotLines(tree.FSet, f.AST)
		}
		for i, line := range strings.Split(string(src), "\n") {
			code := strings.TrimSpace(line)
			if goLines != nil {
				if !goLines[i+1] {
					continue
				}
			} else if strings.HasPrefix(code, "--") || !slotSubtraction.MatchString(code) {
				continue
			}
			checked++
			if staticShareExempt[f.Rel] == code {
				continue
			}
			if !namesCILegs.MatchString(code) {
				t.Errorf("%s:%d: %q computes free slots without the CI legs running on the bench (nova-tools#4293)", f.Rel, i+1, code)
			}
		}
	}
	// The rule was written against eight; the Go deal passes and the
	// preflight row are gone, and the six live ones are the Lua in
	// internal/nsprint/fn: ns_cm_work, TM.room, the width, deal.lua's
	// re-check and the friend deal's two. Fewer means one moved out of the
	// sweep's reach. A friend is not free of legs: the Studio hosts friends
	// and CI both, so a friend's slots shrink by its own beat's ci like a
	// bench's.
	if checked < 6 {
		t.Errorf("found %d slot computations in the live packages, want at least 6 (ns_cm_work, TM.room, width, ns_card_deal, DF x2)", checked)
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
