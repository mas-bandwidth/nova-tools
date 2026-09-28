package ci

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TWO REGISTERS FOR ONE CLASS: a definition the kernel makes twice.
//
// nova-tools#1612 found three of them on dev@11aa07a7 and the class had no
// register at all, because the only gate the kernel has -- lisp/nova-work/
// run-tests.sh -- loaded under `(handler-bind ((warning #'muffle-warning)) ...)`
// and so could not see the failure it was causing:
//
//	$ sbcl --eval '(asdf:load-system :nova-work)'
//	;   Duplicate definition for COPY-MACHINE found in one file.
//	Unhandled UIOP/LISP-BUILD:COMPILE-FILE-ERROR ... "src/fleet"
//	$ echo $?
//	1
//
// The system did not load, at all, and the suite reported
// `total=327 pass=327 fail=0` over it.
//
// These two tests are Go, in the fast tier, and need no SBCL: they read the
// same text the reader reads. They catch the class BEFORE the load, which is
// what makes them a register rather than a second copy of the runner.

var (
	// A top-level `(defun name`, `(defstruct name`, `(defstruct (name`,
	// `(defmacro name`, `(defparameter name`, `(defvar name`, `(defgeneric name`.
	lispTopLevelDef = regexp.MustCompile(`^\((defun|defmacro|defgeneric|defparameter|defvar|defstruct)\s+\(?([^\s()]+)`)
	// `(:file "src/x")` in the .asd -- the files SBCL actually reads.
	lispComponent = regexp.MustCompile(`\(:file\s+"([^"]+)"\s*\)`)
	// The `*acceptance-slices*` list body in tests/acceptance.lisp.
	lispSliceEntry = regexp.MustCompile(`"(slice-[^"]+\.lisp)"`)
)

// TestNoKernelFileDefinesTheSameNameTwice is the rule SBCL enforces and calls
// fatal: within ONE file, one name is defined once. A redefinition ACROSS files
// is a style-warning and is not this test's business -- the later file simply
// wins -- but two in one file is a full WARNING, and a full WARNING inside
// compile-file is a COMPILE-FILE-ERROR that ends the load.
//
// Read-time conditionals are not duplicates: `#+sbcl (defun f ...)` beside
// `#-sbcl (defun f ...)` is one definition in any one build, and src/transport
// .lisp has four such pairs. A definition whose preceding non-blank line opens
// with `#+` or `#-` is therefore skipped.
func TestNoKernelFileDefinesTheSameNameTwice(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	kernel := filepath.Join(root, "lisp", "nova-work")

	// Only the files the system actually reads: an unread file cannot break a
	// load, and nova-tools#1102's 28 of them are a separate debt.
	asd := readFile(t, filepath.Join(kernel, "nova-work.asd"))
	var files []string
	for _, m := range lispComponent.FindAllStringSubmatch(asd, -1) {
		files = append(files, m[1]+".lisp")
	}
	// The acceptance slices are `load`ed by tests/acceptance.lisp rather than
	// named in the .asd, and they are read all the same.
	for _, m := range lispSliceEntry.FindAllStringSubmatch(readFile(t, filepath.Join(kernel, "tests", "acceptance.lisp")), -1) {
		files = append(files, "tests/acceptance/"+m[1])
	}
	if len(files) == 0 {
		t.Fatal("no component or slice file found; this test is reading the wrong tree")
	}

	for _, rel := range files {
		rel := rel
		seen := map[string]int{}
		f, err := os.Open(filepath.Join(kernel, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		line, prev := 0, ""
		var dupes []string
		for scanner.Scan() {
			line++
			text := scanner.Text()
			m := lispTopLevelDef.FindStringSubmatch(text)
			if m == nil {
				if strings.TrimSpace(text) != "" {
					prev = strings.TrimSpace(text)
				}
				continue
			}
			// Namespaces, because Common Lisp has them: `(defstruct history-cost)`
			// defines a TYPE and `make-history-cost`, never a function of that
			// name, so `(defun history-cost ...)` beside it is legal and is what
			// src/closed-history.lisp, src/control.lisp and src/roadmap.lisp each
			// do. Only two definitions in the SAME namespace collide.
			ns := "fn"
			switch m[1] {
			case "defstruct":
				ns = "type"
			case "defparameter", "defvar":
				ns = "var"
			}
			name := ns + ":" + strings.ToLower(m[2])
			readConditional := strings.HasPrefix(prev, "#+") || strings.HasPrefix(prev, "#-")
			if first, ok := seen[name]; ok && !readConditional {
				dupes = append(dupes, fmt.Sprintf("%s: %s defined at line %d and again at line %d", rel, name, first, line))
			} else if !readConditional {
				seen[name] = line
			}
			prev = strings.TrimSpace(text)
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		sort.Strings(dupes)
		for _, d := range dupes {
			t.Errorf("%s -- SBCL reports this as \"Duplicate definition ... found in one file\", a full WARNING, which makes compile-file fail and `(asdf:load-system :nova-work)` exit 1. Keep one definition. A struct that wants two constructors takes two (:constructor ...) options rather than two defstructs.", d)
		}
	}
}

// TestNoAcceptanceSliceIsLoadedTwice is the rule that `*acceptance-slices*` is
// a set: tests/acceptance.lisp `load`s each name in it, so a name listed twice
// loads its file twice, REGISTERS every deftest in it twice, and the suite runs
// and counts the same case twice.
//
// On dev@11aa07a7 the list held slice-09-state-export-replays.lisp three times
// and slice-10-fleet.lisp twice: `total=335` was 314 distinct cases and 21
// repeat runs of sixteen of them.
func TestNoAcceptanceSliceIsLoadedTwice(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	path := filepath.Join(root, "lisp", "nova-work", "tests", "acceptance.lisp")
	matches := lispSliceEntry.FindAllStringSubmatch(readFile(t, path), -1)
	if len(matches) == 0 {
		t.Fatal("tests/acceptance.lisp names no slice file; this test is reading the wrong file")
	}

	at := map[string][]int{}
	var order []string
	for i, m := range matches {
		if _, ok := at[m[1]]; !ok {
			order = append(order, m[1])
		}
		at[m[1]] = append(at[m[1]], i+1)
	}
	sort.Strings(order)
	for _, name := range order {
		if len(at[name]) > 1 {
			t.Errorf("tests/acceptance.lisp lists %s %d times (entries %v): the file is loaded that many times, every deftest in it is registered that many times, and the suite's total= counts each of its cases that many times. List it once.",
				name, len(at[name]), at[name])
		}
	}
}
