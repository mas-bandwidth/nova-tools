package ci

import (
	"os"
	"path/filepath"
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

// asdComponent reads a `(:file "src/x")` component name.
var asdComponent = regexp.MustCompile(`\(:file\s+"([^"]+)"\s*\)`)

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

// TestEveryKernelSourceIsACompiledComponent is the rule: a file under
// `lisp/nova-work/src/` or `lisp/nova-work/tests/` is named by the system, or it
// is named in notCompiled with the issue that owes its removal.
func TestEveryKernelSourceIsACompiledComponent(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	kernel := filepath.Join(root, "lisp", "nova-work")
	asd := readFile(t, filepath.Join(kernel, "nova-work.asd"))

	component := map[string]bool{}
	for _, m := range asdComponent.FindAllStringSubmatch(asd, -1) {
		component[m[1]] = true
	}
	if len(component) == 0 {
		t.Fatal("lisp/nova-work/nova-work.asd names no (:file ...) component; this test is reading the wrong file")
	}

	onDisk := map[string]bool{}
	for _, dir := range []string{"src", "tests"} {
		entries, err := os.ReadDir(filepath.Join(kernel, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lisp") {
				continue
			}
			onDisk[dir+"/"+strings.TrimSuffix(e.Name(), ".lisp")] = true
		}
	}

	// (a) Every file on disk is compiled, or is a named debt.
	var silent []string
	for name := range onDisk {
		if component[name] || notCompiled[name] != "" {
			continue
		}
		silent = append(silent, name)
	}
	sort.Strings(silent)
	for _, name := range silent {
		t.Errorf("lisp/nova-work/%s.lisp is in no :components list, so SBCL never reads it: it compiles nothing, no acceptance case covers it, and run-tests.sh is green without it. Add it to the system in nova-work.asd, or name it in notCompiled with the issue that owes its removal.", name)
	}

	// (b) The system names nothing that is gone.
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
