package ci

import (
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
//     legs running on it: every `slots - ...` in Go and Lua takes the
//     beat's ci off, and the beat writes it.
//
// It holds on every bench and every worker kind; a new exec path or a new
// slot computation that forgets is red here, not on a bench at 5.0 load.

// niceExecPaths are the exec paths and, for each, the call that must stand
// before the first exec in the same function: (file, function, yield call,
// exec call).
var niceExecPaths = []struct{ file, fn, yield, exec string }{
	{"internal/nsprint/card/wrapper.go", "func RunWrapper(", "yield()", "proc.start()"},
	{"internal/nsprint/card/run.go", "func Run(", "yield()", "cmd.Start()"},
	{"cmd/nova-ci/local.go", "func cmdLocal(", "yield.ToCI()", "runLocal("},
}

// wrapperDefault is how the two card paths reach the real setpriority: the
// config's Yield seam (a test's) falls back to the package's yieldToCI.
const wrapperDefault = "yield = yieldToCI"

func TestCopiesRunNiced(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// 1. The number, and setpriority on both OSes, in the one package.
	y := readFile(t, filepath.Join(root, "internal/yield/yield.go"))
	if !strings.Contains(y, "const Nice = 15") {
		t.Errorf("internal/yield/yield.go: want `const Nice = 15` (nova-tools#4293 names fifteen)")
	}
	for _, goos := range []string{"darwin", "linux"} {
		f := readFile(t, filepath.Join(root, "internal/yield/nice_"+goos+".go"))
		if !strings.Contains(f, "syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)") {
			t.Errorf("internal/yield/nice_%s.go: want setpriority(PRIO_PROCESS, 0, n) on this process", goos)
		}
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
		if p.yield == "yield()" {
			if di := strings.Index(body, wrapperDefault); di < 0 || di > yi {
				t.Errorf("%s %s: the yield must default to the package's yieldToCI (`%s`) before it is called", p.file, p.fn, wrapperDefault)
			}
		}
	}

	// 3. The wrapper's yield is the real one: production assigns yieldToCI
	// once, to yield.ToCI, and nowhere else (the tests swap it), and no
	// production caller gives a WrapperConfig or RunConfig a Yield of its
	// own (the seam is for tests).
	dir := filepath.Join(root, "internal/nsprint/card")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	assigns := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src := readFile(t, filepath.Join(dir, e.Name()))
		for _, line := range strings.Split(src, "\n") {
			if strings.Contains(line, "yieldToCI =") {
				assigns++
				if e.Name() != "nice.go" || !strings.Contains(line, "yield.ToCI") {
					t.Errorf("%s: %q: production may set the wrapper's yield only in nice.go, to yield.ToCI", e.Name(), strings.TrimSpace(line))
				}
			}
		}
	}
	if assigns != 1 {
		t.Errorf("internal/nsprint/card sets yieldToCI %d times in production, want exactly once (nice.go)", assigns)
	}
	for _, base := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, base), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src := readFile(t, path)
			for i, line := range strings.Split(src, "\n") {
				if code := strings.TrimSpace(line); strings.HasPrefix(code, "Yield:") || strings.Contains(code, ".Yield = ") {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d: %q: production never sets a copy's Yield; the real setpriority is the default", filepath.ToSlash(rel), i+1, code)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// slotSubtraction finds a slot computation: `slots - <something>`, in Go
// (slots, Slots) or Lua (d.slots).
var slotSubtraction = regexp.MustCompile(`\b[sS]lots\s*-\s*[A-Za-z(]`)

// namesCILegs is what a slot computation must name to be taking the CI legs
// off: the Go field or variable ci/CI, or the Lua TM.ci_legs.
var namesCILegs = regexp.MustCompile(`\bci\b|\bCI\b|TM\.ci_legs\(`)

// friendOnlyFiles compute a FRIEND's slots and nothing else: a friend is a
// seat on a machine of its own with no CI runner, so it has no legs to take
// off. A file here that grows a bench computation must leave this list.
var friendOnlyFiles = map[string]bool{
	"internal/nsprint/fn/lua/deal_friend.lua": true, // the friend queue deal (#3441)
}

func TestSlotsShrinkByCILegs(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// 1. The beat writes the legs it counted, every beat.
	lua := readFile(t, filepath.Join(root, "internal/nsprint/fn/lua/presence.lua"))
	if !strings.Contains(funcBody(t, "presence.lua", lua, "local function bench_beat("), "'ci', args[21] or ''") {
		t.Errorf("presence.lua bench_beat: the beat must write the CI leg count as ci (args[21]) every beat")
	}
	life := readFile(t, filepath.Join(root, "cmd/nova-sprint/life.go"))
	if strings.Count(life, "req.CI = life.CILegsNow()") < 2 {
		t.Errorf("cmd/nova-sprint/life.go: the bench beat must measure the CI legs (life.CILegsNow) for the first beat and every tick")
	}

	// 2. Every slot computation, Go and Lua, under the sprint tools takes
	// the legs off. Comments and tests do not count; a line does.
	var checked int
	for _, base := range []string{"internal/nsprint", "cmd/nova-sprint"} {
		err := filepath.WalkDir(filepath.Join(root, base), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || strings.HasSuffix(path, "_test.go") || (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".lua")) {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if friendOnlyFiles[filepath.ToSlash(rel)] {
				return nil
			}
			src := readFile(t, path)
			for i, line := range strings.Split(src, "\n") {
				code := strings.TrimSpace(line)
				if strings.HasPrefix(code, "//") || strings.HasPrefix(code, "--") || !slotSubtraction.MatchString(code) {
					continue
				}
				checked++
				if !namesCILegs.MatchString(code) {
					t.Errorf("%s:%d: %q computes free slots without the CI legs running on the bench (nova-tools#4293)", filepath.ToSlash(rel), i+1, code)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The four the rule was written against: the two deal passes, the fill
	// and the read route. Fewer means one moved out of the sweep's reach.
	if checked < 4 {
		t.Errorf("found %d slot computations under internal/nsprint and cmd/nova-sprint, want at least 4 (deal, taskcard, ns_cm_work, TM.room)", checked)
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
