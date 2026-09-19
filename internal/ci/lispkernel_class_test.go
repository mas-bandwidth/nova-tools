package ci

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The nova-work kernel is one ASDF system, and ASDF loads a file because the
// system names it — never because it is in the directory. So a `.lisp` file in
// `lisp/nova-work/src/` that no `:components` list names is not slow-to-load or
// loaded-later: it is never read by SBCL at all. It compiles nothing, it is
// covered by nothing, and `run-tests.sh` is green without it.
//
// THE HURT (nova-tools#1102, measured 2026-09-19 on dev@47d81e9c, SBCL 2.6.8):
// 28 of the 60 files in `src/` were in no system -- all 17 `replays-86NN.lisp`
// and 11 feature-named `replays-*.lisp`, around 700 defuns and defstructs that
// SBCL never read, while the suite reported `total=327 pass=327 fail=0`. #1102
// called folding them into the feature files a mechanical rename. It is not:
// appending all 28 to the system and running the suite dies with
//
//	attempt to redefine the STRUCTURE-OBJECT class SAVEPOINT incompatibly
//	with the current definition            (loading src/replays-8641.fasl)
//
// exit 1, the 327 cases never reached. A card told to move that file into
// `src/savepoint.lisp` would have landed a kernel that does not load, or would
// have dropped the colliding form to get green with nobody the wiser.
//
// The ledger below is the point of this test rather than a hole in it. A silent
// file is invisible; a listed one is a debt with an issue number, it cannot grow
// without this test saying so, and an entry that outlives its file fails too, so
// the list can only shrink.
//
// TWO LISTS SINCE nova-tools#1947. `src/` is still written out component by
// component, because `:serial t` makes that order the load order and several
// files depend on earlier ones. `tests/` is not: writing it out cost thirteen
// pull requests in one night, all conflicting on that one file and on no other
// file at all, because every new test appends a line at the same position. The
// tests system now names an explicit PRELUDE (`tests/harness`, then
// `tests/acceptance`) and DISCOVERS the rest from the directory, sorted.
//
// That changes what this test can check, and it must not quietly weaken it.
// For `src/` the rule is unchanged: named, or a named debt. For `tests/` the
// rule becomes the SHAPE that makes discovery safe, which this test can read as
// text without an SBCL on the runner:
//
//	(1) the tests system writes out NO test component at all -- a hand-written
//	    per-test component is the conflict coming back, and discovery would
//	    register that file a second time;
//	(2) the prelude is declared once, as `+test-prelude+`, in its order, and
//	    both of its files exist;
//	(3) the .asd really does discover -- it reads the tests directory and sorts
//	    -- rather than having lost its test list altogether, which would be a
//	    suite of zero cases reporting green;
//	(4) `tests/asd-discovery.lisp` stands, because the parity between the
//	    components and the directory is a LISP fact and that file is where it is
//	    proved on every run.
//
// The lisp-side proof (parity, exactly-once, prelude order, no recursion into
// tests/acceptance/, and the refusals) is `lisp/nova-work/tests/asd-discovery.lisp`;
// the order-independence measurement is `lisp/nova-work/tools/asd-order-check.sh`.

// asdComponent reads a `(:file "src/x")` component name.
var asdComponent = regexp.MustCompile(`\(:file\s+"([^"]+)"\s*\)`)

// testsSystem cuts the `nova-work/tests` defsystem form out of the .asd, so the
// two systems' component lists are read apart.
var testsSystem = regexp.MustCompile(`(?s)\(asdf:defsystem "nova-work/tests".*`)

// testPrelude is the explicit, ordered head of the tests system: the only test
// files the .asd may name, and it names them in one place, the `+test-prelude+`
// parameter. harness.lisp defines `deftest`; acceptance.lisp defines the shared
// fixtures AND loads the per-slice files under tests/acceptance/, so every
// other test file depends on both.
var testPrelude = []string{"tests/harness", "tests/acceptance"}

// preludeDecl cuts the `+test-prelude+` parameter's quoted list out of the .asd,
// and preludeName reads each name from it. The prelude is ORDERED, so the list
// is read as a sequence, not a set.
var (
	preludeDecl = regexp.MustCompile(`(?s)\(defparameter \+test-prelude\+\s*'\(([^)]*)\)`)
	preludeName = regexp.MustCompile(`"([^"]+)"`)
)

// discoveryMarks are the forms that make the tests list a discovery rather than
// a list somebody deleted: the read-time call, the directory read, and the sort
// that makes the order the same in every checkout.
var discoveryMarks = []string{
	"#.(nova-work-asdf:test-components)",
	"uiop:directory-files",
	"#'string<",
}

// notCompiled names each `lisp/nova-work` source file that the ASDF system does
// NOT load, with the issue that owes its removal. Every entry is a file SBCL has
// never read. Removing an entry means either folding its forms into a feature
// file the system names (nova-tools#1102) or deleting the file; adding one means
// writing kernel code that nothing compiles, which needs the reason written here
// and read by whoever reviews it.
var notCompiled = map[string]string{
	"src/replays-8603":                  "nova-tools#1102",
	"src/replays-8605":                  "nova-tools#1102",
	"src/replays-8621":                  "nova-tools#1102",
	"src/replays-8641":                  "nova-tools#1102 (its `savepoint` defstruct collides with src/savepoint.lisp's)",
	"src/replays-8642":                  "nova-tools#1102",
	"src/replays-8643":                  "nova-tools#1102",
	"src/replays-8645":                  "nova-tools#1102",
	"src/replays-8646":                  "nova-tools#1102",
	"src/replays-8647":                  "nova-tools#1102",
	"src/replays-8648":                  "nova-tools#1102",
	"src/replays-8649":                  "nova-tools#1102",
	"src/replays-8650":                  "nova-tools#1102",
	"src/replays-8651":                  "nova-tools#1102",
	"src/replays-8660":                  "nova-tools#1102",
	"src/replays-8661":                  "nova-tools#1102",
	"src/replays-8663":                  "nova-tools#1102",
	"src/replays-8664":                  "nova-tools#1102",
	"src/replays-attempts-capabilities": "nova-tools#1102",
	"src/replays-closed-history":        "nova-tools#1102",
	"src/replays-efficiency-goal":       "nova-tools#1102",
	"src/replays-fleet-allocation":      "nova-tools#1102",
	"src/replays-fleet-assignment":      "nova-tools#1102",
	"src/replays-operations-undo":       "nova-tools#1102",
	"src/replays-priority-and-export":   "nova-tools#1102",
	"src/replays-publication":           "nova-tools#1102",
	"src/replays-render-priority":       "nova-tools#1102",
	"src/replays-slice-05":              "nova-tools#1102",
	"src/replays-wire-and-operations":   "nova-tools#1102",
}

// TestEveryKernelSourceIsACompiledComponent is the rule, in its two halves: a
// file under `lisp/nova-work/src/` is NAMED by the system, or it is named in
// notCompiled with the issue that owes its removal; and the tests system names
// its prelude and nothing else, still visibly discovering the rest from
// `lisp/nova-work/tests/` (nova-tools#1947), so no test file can be silent.
func TestEveryKernelSourceIsACompiledComponent(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	kernel := filepath.Join(root, "lisp", "nova-work")
	asd := readFile(t, filepath.Join(kernel, "nova-work.asd"))

	// The two lists are read apart: `src` is explicit and ordered, the tests
	// system writes out no component at all.
	testsPart := testsSystem.FindString(asd)
	if testsPart == "" {
		t.Fatal(`lisp/nova-work/nova-work.asd has no (asdf:defsystem "nova-work/tests" ...) form; this test is reading the wrong file`)
	}
	srcPart := strings.TrimSuffix(asd, testsPart)

	componentsOf := func(text string) []string {
		var out []string
		for _, m := range asdComponent.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
		return out
	}
	srcList := componentsOf(srcPart)
	component := map[string]bool{}
	for _, name := range srcList {
		component[name] = true
	}
	if len(component) == 0 {
		t.Fatal("lisp/nova-work/nova-work.asd names no (:file ...) component in the nova-work system; this test is reading the wrong file")
	}

	onDisk := map[string]bool{}
	onDiskIn := map[string]map[string]bool{"src": {}, "tests": {}}
	for _, dir := range []string{"src", "tests"} {
		entries, err := os.ReadDir(filepath.Join(kernel, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lisp") {
				continue
			}
			name := dir + "/" + strings.TrimSuffix(e.Name(), ".lisp")
			onDisk[name] = true
			onDiskIn[dir][name] = true
		}
	}

	// (a) Every src file is compiled, or is a named debt. tests/ is not read
	// here: discovery names every regular tests/*.lisp by construction, and
	// that the components really ARE the directory is proved every run by
	// lisp/nova-work/tests/asd-discovery.lisp.
	var silent []string
	for name := range onDiskIn["src"] {
		if component[name] || notCompiled[name] != "" {
			continue
		}
		silent = append(silent, name)
	}
	sort.Strings(silent)
	for _, name := range silent {
		t.Errorf("lisp/nova-work/%s.lisp is in no :components list, so SBCL never reads it: it compiles nothing, no acceptance case covers it, and run-tests.sh is green without it. Add it to the system in nova-work.asd, or name it in notCompiled with the issue that owes its removal.", name)
	}

	// (b) The nova-work system names nothing that is gone.
	var missing []string
	for name := range component {
		if !onDisk[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("nova-work.asd names the component %q, and lisp/nova-work/%s.lisp does not exist; the system would fail to load", name, name)
	}

	// (d) The tests system names its prelude, in order, and nothing else.
	t.Run("tests are discovered, not written out", func(t *testing.T) {
		if got := componentsOf(testsPart); len(got) != 0 {
			t.Errorf("the nova-work/tests system writes out the components %q. It may name NO test file: the prelude is declared once, in +test-prelude+, and the rest is DISCOVERED (nova-tools#1947).\n"+
				"A hand-written per-test component brings back the conflict that held thirteen pull requests -- every PR appends at the same position, so every pair of them conflicts -- and discovery would register the same file a second time.\n"+
				"Delete the line: adding lisp/nova-work/tests/<name>.lisp is all that is needed.", got)
		}

		// The prelude is explicit and ORDERED, and it is what every other test
		// file depends on, so both what it says and the order it says it in are
		// held here.
		decl := preludeDecl.FindStringSubmatch(asd)
		if decl == nil {
			t.Fatal("nova-work.asd declares no +test-prelude+; the tests system would discover its own harness and load it in sorted position, after the files that need it")
		}
		var prelude []string
		for _, m := range preludeName.FindAllStringSubmatch(decl[1], -1) {
			prelude = append(prelude, m[1])
		}
		if !reflect.DeepEqual(prelude, testPrelude) {
			t.Errorf("+test-prelude+ is %q; it must be %q, in that order. tests/harness.lisp defines `deftest` and the runner, and tests/acceptance.lisp defines the shared seed and request fixtures AND loads the per-slice files under tests/acceptance/ -- every other test file uses both.\n"+
				"Changing this is a design call (nova-tools#1947, Stella): it is the one part of the load order that carries meaning.", prelude, testPrelude)
		}
		for _, name := range testPrelude {
			if !onDiskIn["tests"][name] {
				t.Errorf("the prelude names %q and lisp/nova-work/%s.lisp does not exist; the prelude is what every other test file depends on", name, name)
			}
		}
	})

	// (e) It really discovers. A tests list that is simply gone is a suite of
	// zero cases reporting green, which is the hurt at the top of this file
	// wearing a different hat.
	t.Run("the discovery is still there", func(t *testing.T) {
		for _, mark := range discoveryMarks {
			if !strings.Contains(asd, mark) {
				t.Errorf("nova-work.asd no longer contains %q. The tests list is discovered at read time from the directory and sorted; without that form the system names two files and nothing else, and run-tests.sh reports green over the prelude alone.", mark)
			}
		}
		if _, err := os.Stat(filepath.Join(kernel, "tests", "asd-discovery.lisp")); err != nil {
			t.Errorf("lisp/nova-work/tests/asd-discovery.lisp: %v. It is where the components/directory parity, the exactly-once registration, the prelude order, the no-recursion rule and the refusals are proved on every suite run; this Go test only reads the shape.", err)
		}
		if _, err := os.Stat(filepath.Join(kernel, "tools", "asd-order-check.sh")); err != nil {
			t.Errorf("lisp/nova-work/tools/asd-order-check.sh: %v. Sorting the tests is only safe because the order after the prelude carries no meaning, and that is a measurement this script re-runs.", err)
		}
	})

	// (f) No tests/ debt. Under discovery a tests file cannot be silent, so an
	// entry claiming one is stale by construction.
	for name := range notCompiled {
		if strings.HasPrefix(name, "tests/") {
			t.Errorf("notCompiled names %q. Since nova-tools#1947 every regular tests/*.lisp is discovered, so a tests file cannot be uncompiled -- either the file is gone, or it is loading and the entry is cover.", name)
		}
	}

	// (c) The ledger only shrinks. An entry that outlives its file is a debt
	// somebody paid without crossing it off, and the next reader believes it.
	var stale []string
	for name := range notCompiled {
		switch {
		case component[name]:
			stale = append(stale, name+" is now a component of the system")
		case !onDisk[name]:
			stale = append(stale, name+".lisp no longer exists")
		}
	}
	sort.Strings(stale)
	for _, s := range stale {
		t.Errorf("notCompiled in this file still names a file whose debt is paid: %s. Delete the entry; a ledger nobody prunes is read as current.", s)
	}
}
