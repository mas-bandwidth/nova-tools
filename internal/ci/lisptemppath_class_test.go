package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// lisptemppath_class_test.go polices the Lisp acceptance tree from here, the way
// lispkernel_class_test.go already does: `internal/ci` is where this repository
// keeps the rules about `lisp/nova-work/`, because Go is what CI runs on every
// push and SBCL is not.
//
// THE HURT (nova-tools#1699, measured 2026-09-19). Every temp path the
// `lisp/nova-work` acceptance suite made was named from `(get-universal-time)`
// plus a counter that starts at zero in every image, under a directory name
// fixed in the source -- `<tmpdir>/nova-work-test-journals/`,
// `nova-work-state-load-<name>-<time>-<n>/`, and seven more. Two suites that
// start inside the same second on one host build the SAME path. One then finds
// the destination already there, or the journal's lock held by the other:
//
//	TEST state-load-is-isolated FAIL spec=docs/SPEC-WORK.md:5896 ...:
//	  destination /var/folders/.../T/nova-work-state-load-isolated-3998812362-1/export/ exists
//	TEST durable-journal-corrupt-data-refuses-without-truncation FAIL ...:
//	  journal /var/folders/.../T/nova-work-test-journals/corrupt-data-3998849935-5.journal
//	  is held by another process
//
// Three reds on PR #1682, a red `ci-ok` on PR #1692 on runner air-nova-2, and
// 12-17 manufactured failures with four suites parallel on hulk -- the same
// suites one at a time, green. CI runners share hosts (the Air runs two, the
// Studio several, superman ten), so this reddens PRs whose changes had nothing
// to do with it, and trains reviewers to rerun a red, which is the habit that
// lets a real red through.
//
// THE SECOND HURT, found while fixing the first, and the reason rule B exists.
// SBCL saves `*random-state*` into its core, so a fresh image returns the SAME
// sequence every time: three separate images each printed `113500 958198 129774`
// for three calls to `(random 1000000)`. The AF_UNIX fixtures named their socket
// directories `nw-<(random 1000000)>`, so two concurrent suites agreed exactly --
// and `short-socket-base` DELETED that directory tree before binding, taking the
// other suite's live socket with it. A name from `(random ...)` in a fresh SBCL
// image is a constant, not a nonce, and nothing about the code says so.
//
// THE RULES this test enforces mechanically over `lisp/nova-work/tests/`:
//
//	A. no test file names the SHARED temporary directory --
//	   `uiop:default-temporary-directory`, `uiop:temporary-directory`,
//	   `(getenv "TMPDIR")` or a `/tmp` literal -- to build a path;
//	B. no test file names `(random ...)`.
//
// Both are satisfied by building the path with the harness's per-run helpers:
// `test-temp-dir` / `test-temp-file` under `test-run-root`, or, for a path
// `sun_path` keeps out of that root, `test-short-tag`. Uniqueness comes from the
// run's token -- a real entropy source plus the pid -- never from a counter or a
// clock.
//
// Both allowlists are checked in BOTH directions, the same shape as
// sharedtemp_class_test.go's and lispkernel_class_test.go's ledgers, so an
// unlisted finding is a red run, a listed entry that no longer names one is also
// a red run, and the lists can only shrink.

const (
	lispTempAllowlistPath   = "testdata/lisptemppath_allowlist.txt"
	lispRandomAllowlistPath = "testdata/lisprandom_allowlist.txt"

	lispTempRemedy = "build the path with test-temp-dir or test-temp-file under test-run-root (lisp/nova-work/tests/harness.lisp), or, for a path sun_path keeps out of that root, name it with test-short-tag: uniqueness comes from the run's token -- a real entropy source plus the pid -- never from a counter or a clock"

	lispRandomRemedy = "SBCL saves *RANDOM-STATE* into its core, so (random ...) in a fresh image is a constant, not a nonce: name the path with test-short-tag or test-temp-dir instead (lisp/nova-work/tests/harness.lisp)"
)

// lispTestTree is the tree these rules cover.
const lispTestTree = "lisp/nova-work/tests"

// lispHelperHome is the file that DEFINES the per-run helper, so it is the one
// file that must name the shared temporary directory and must draw randomness:
// that is what `test-run-root` and `test-run-entropy` are. Scanning it would be
// circular -- the rule would forbid its own implementation -- so it is skipped
// here by name rather than carried as a permanent allowlist entry that reads
// like a debt. Every OTHER file is covered, which is the whole surface a new
// test file can appear on.
const lispHelperHome = lispTestTree + "/harness.lisp"

// lispSharedTempNames are the ways a Lisp form reaches the directory every other
// job on the box writes to.
var lispSharedTempNames = []*regexp.Regexp{
	regexp.MustCompile(`\buiop:default-temporary-directory\b`),
	regexp.MustCompile(`\buiop:temporary-directory\b`),
	regexp.MustCompile(`\b(?:sb-posix|uiop):getenv\s+"TMPDIR"`),
	regexp.MustCompile(`"/tmp/?"`),
	regexp.MustCompile(`"/var/tmp/?"`),
}

// lispRandomCall is a call to RANDOM. SEED-RANDOM-STATE is not one: reseeding is
// the fix, not the hazard, so `\(random\b` and not a bare `random`.
var lispRandomCall = regexp.MustCompile(`\(random\b`)

// lispDefiner opens a top-level definition. `deftest` names its case in a string;
// the rest name a symbol.
var lispDefiner = regexp.MustCompile(`^\((?:defun|defmacro|defvar|defparameter|defconstant|define-condition)\s+([^\s()]+)|^\(deftest\s+"([^"]+)"`)

// lispTempFinding is one form that names a shared temp directory or calls RANDOM.
type lispTempFinding struct {
	File string // repo-relative, slash-separated
	Def  string // the enclosing top-level definition, the allowlist's key
	Line int
	Text string // the offending source text, trimmed
}

func (f lispTempFinding) key() string { return f.File + ":" + f.Def }

func (f lispTempFinding) String(rule, remedy string) string {
	return fmt.Sprintf("%s:%d: %s in %s %s; %s", f.File, f.Line, strings.TrimSpace(f.Text), f.Def, rule, remedy)
}

// scrubLisp blanks out every comment, keeping byte offsets and line breaks
// intact, and leaves string literals alone.
//
// Comments go because otherwise this scanner reads its own explanation: the
// paragraphs that say what the old code did quote it exactly, and a scanner that
// matched prose would need an allowlist entry for every sentence that told the
// truth.
//
// Strings STAY because the rule is partly about them: `#p"/tmp/"` and
// `(sb-posix:getenv "TMPDIR")` are the offence, not a description of it. So a
// docstring that quotes a banned construct is a finding, and the fix is to write
// the prose without the literal form -- which is cheaper and more honest than
// teaching this scanner to tell a docstring from a pathname.
//
// Strings are still TRACKED, so a `;` inside one does not start a comment and
// swallow the rest of the line.
func scrubLisp(src string) string {
	out := []byte(src)
	inStr, inCom := false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		switch {
		case inCom:
			if c == '\n' {
				inCom = false
			} else {
				out[i] = ' '
			}
		case inStr:
			if c == '\\' {
				i++ // an escaped character, `\"` included, is never a delimiter
				continue
			}
			if c == '"' {
				inStr = false
			}
		default:
			switch {
			case c == ';':
				inCom = true
				out[i] = ' '
			case c == '"':
				inStr = true
			case c == '#' && i+1 < len(out) && out[i+1] == '\\':
				i += 2 // a character literal: #\" and #\; are not delimiters
			}
		}
	}
	return string(out)
}

// lispFindings scans one scrubbed file for the forms RULE names, attributing each
// to the top-level definition it sits in.
func lispFindings(rel, src string, pats []*regexp.Regexp) []lispTempFinding {
	var found []lispTempFinding
	def := "<toplevel>"
	for i, line := range strings.Split(scrubLisp(src), "\n") {
		if m := lispDefiner.FindStringSubmatch(line); m != nil {
			if m[1] != "" {
				def = m[1]
			} else {
				def = m[2]
			}
		}
		for _, p := range pats {
			if p.MatchString(line) {
				found = append(found, lispTempFinding{File: rel, Def: def, Line: i + 1, Text: line})
				break
			}
		}
	}
	return found
}

func lispTestFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	base := filepath.Join(root, filepath.FromSlash(lispTestTree))
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".lisp") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if slash == lispHelperHome {
			return nil
		}
		out = append(out, slash)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no .lisp files under %s; this test is reading the wrong tree", lispTestTree)
	}
	sort.Strings(out)
	return out
}

func readLispAllowlist(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		allow[line] = true
	}
	return allow
}

// checkLispRule is rules A and B: an unlisted finding fails, and a listed entry
// that no longer names a finding fails too.
func checkLispRule(t *testing.T, pats []*regexp.Regexp, allowPath, rule, remedy string) {
	t.Helper()
	root := repoRoot(t)
	allow := readLispAllowlist(t, allowPath)
	seen := map[string]bool{}
	var violations []string

	for _, rel := range lispTestFiles(t, root) {
		src := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))
		for _, f := range lispFindings(rel, src, pats) {
			seen[f.key()] = true
			if !allow[f.key()] {
				violations = append(violations, f.String(rule, remedy)+
					"\n  (or add "+f.key()+" to internal/ci/"+allowPath+" with the reason it cannot use the helper)")
			}
		}
	}
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but nothing there does that any more; delete the stale entry (the list only shrinks)",
				allowPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestNoLispTestBuildsATempPathWithoutTheHelper is rule A.
func TestNoLispTestBuildsATempPathWithoutTheHelper(t *testing.T) {
	t.Parallel()
	checkLispRule(t, lispSharedTempNames, lispTempAllowlistPath,
		"names the SHARED temporary directory, which every other job on the host writes to: two suites that start inside one second build the same path and one goes red for something it did not do (nova-tools#1699)",
		lispTempRemedy)
}

// TestNoLispTestNamesAPathWithRandom is rule B.
func TestNoLispTestNamesAPathWithRandom(t *testing.T) {
	t.Parallel()
	checkLispRule(t, []*regexp.Regexp{lispRandomCall}, lispRandomAllowlistPath,
		"calls RANDOM, which in a fresh SBCL image returns the same number every time because the state is saved in the core (nova-tools#1699)",
		lispRandomRemedy)
}

// TestLispTempScannerReadsTheFixtures is the red-test contract. Without it a
// scanner that had quietly stopped matching -- a renamed symbol, a scrubber that
// ate the code instead of the comments -- would keep the tree green by finding
// nothing at all, which is the exact failure mode this whole class exists to
// prevent.
func TestLispTempScannerReadsTheFixtures(t *testing.T) {
	t.Parallel()

	before := readFile(t, filepath.Join("testdata", "lisptemppath", "before.lisp.txt"))
	after := readFile(t, filepath.Join("testdata", "lisptemppath", "after.lisp.txt"))

	pats := append(append([]*regexp.Regexp{}, lispSharedTempNames...), lispRandomCall)

	got := lispFindings("lisp/nova-work/tests/fixture.lisp", before, pats)
	want := []struct {
		def  string
		frag string
	}{
		{"test-journal-path", "uiop:default-temporary-directory"},
		{"dedup-root-temp-dir", `sb-posix:getenv "TMPDIR"`},
		{"%request-line-dir", `"/tmp/"`},
		{"endpoint-is-local-and-private", "(random 1000000)"},
	}
	if len(got) != len(want) {
		t.Fatalf("the pre-fix fixture holds %d offending forms, the scanner found %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Def != w.def || !strings.Contains(got[i].Text, w.frag) {
			t.Errorf("finding %d: got %s / %q, want %s containing %q", i, got[i].Def, strings.TrimSpace(got[i].Text), w.def, w.frag)
		}
	}

	// The fixed text must be silent even though its COMMENTS quote the very
	// constructs the rule bans, because that is what they are explaining. Its
	// docstrings say the same thing without writing the literal form, which is
	// the rule the scrubber's comment states: strings stay visible, so prose
	// inside them spells the construct out in words.
	if clean := lispFindings("lisp/nova-work/tests/fixture.lisp", after, pats); len(clean) != 0 {
		t.Errorf("the post-fix fixture must hold no offending form, the scanner found %d: %v", len(clean), clean)
	}
}

// TestScrubLispKeepsCodeAndDropsProse pins the scrubber directly: it is the part
// that decides what the scanner can see, so a bug in it is silent in both
// directions.
func TestScrubLispKeepsCodeAndDropsProse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		src     string
		visible []string
		hidden  []string
	}{
		{
			name:    "a line comment is dropped",
			src:     "(defun f () (g)) ; it was (random 1000000) once\n",
			visible: []string{"(defun f () (g))"},
			hidden:  []string{"(random"},
		},
		{
			name: "a string literal is KEPT: it is where the offending path lives",
			src:  "(defun f () (merge-pathnames n #p\"/tmp/\"))\n",
			// The rule is partly about literals, so the scrubber must not hide
			// them; `#p"/tmp/"` is the offence itself.
			visible: []string{`#p"/tmp/"`},
		},
		{
			name:    "a real call survives",
			src:     "(defun f () (random 10))\n",
			visible: []string{"(random 10)"},
		},
		{
			name: "a semicolon INSIDE a string does not start a comment",
			src:  "(defun f () (g \"a;b\") (random 7))\n",
			// If the `;` were read as a comment the rest of the line would be
			// blanked and the (random 7) beside it would go unseen.
			visible: []string{`"a;b"`, "(random 7)"},
		},
		{
			name:    "an escaped quote does not end the string early",
			src:     "(defun f () \"a \\\" b\" (k) ; (random 5)\n  (m))\n",
			visible: []string{"(k)", "(m)"},
			hidden:  []string{"(random"},
		},
		{
			name:    "a character literal double quote is not a delimiter",
			src:     "(defun f () (char= c #\\\") (random 7))\n",
			visible: []string{"(random 7)"},
		},
		{
			name:    "a character literal semicolon is not a comment",
			src:     "(defun f () (char= c #\\;) (random 7))\n",
			visible: []string{"(random 7)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := scrubLisp(tc.src)
			if strings.Count(out, "\n") != strings.Count(tc.src, "\n") || len(out) != len(tc.src) {
				t.Fatalf("scrubLisp changed the shape of the source: %d bytes / %d lines in, %d / %d out",
					len(tc.src), strings.Count(tc.src, "\n"), len(out), strings.Count(out, "\n"))
			}
			for _, v := range tc.visible {
				if !strings.Contains(out, v) {
					t.Errorf("scrubLisp hid code it must keep: %q is not in %q", v, out)
				}
			}
			for _, h := range tc.hidden {
				if strings.Contains(out, h) {
					t.Errorf("scrubLisp kept prose it must drop: %q is still in %q", h, out)
				}
			}
		})
	}
}
