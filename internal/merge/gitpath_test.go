package merge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A RECORD'S PATH IS A GIT PATH, NOT A PATH ON THIS MACHINE, and the difference is
// invisible on unix.
//
// `queue classify` was green on linux, darwin and the hulk gate and exited 1 on
// windows-latest alone (integration-6, #1335):
//
//	CLASSIFY FAIL run=run-9 file=classify\run-9-20260911T130000Z-b23c6f.json pushed=false:
//	the push landed and the confirming fetch did not find the record at the remote tip
//
// ClassifyFile had been built with filepath.Join, whose separator is the machine's. The
// bytes went to the right file and the push landed -- Windows takes a backslash as a
// separator too -- and then the confirming `git show FETCH_HEAD:classify\<name>.json`
// asked git for a file whose NAME contains a backslash, because a rev:path is spelled
// with forward slashes on every platform git runs on. There is no such file, so the
// confirm said no, the CAS loop retried four more times and the verb exited 1.
//
// These two tests are the class, not the instance, and they run the same paths on any
// host: the first pins every record-path builder to path.Join, and the second drives a
// Windows-shaped path through the one place a record's `file` becomes a git path.

// TestRecordPathsAreGitPathsNotMachinePaths reads this package's own source: every
// function that returns a record's path must build it with path.Join and must not mention
// filepath at all. It is a source test because the bug it catches has no output on the
// host that runs it -- filepath.Join and path.Join are the same function on unix, and CI's
// unix legs were all green while Windows was red.
func TestRecordPathsAreGitPathsNotMachinePaths(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	// The builders found, so that a rename cannot empty this test and leave it passing by
	// checking nothing.
	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			// A record-path builder is a plain function whose name ends in File and
			// whose one result is a string. The lock's tryLockFile and unlockFile take
			// an *os.File and return no path, so they are not among them.
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasSuffix(fn.Name.Name, "File") {
				continue
			}
			if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			if id, ok := fn.Type.Results.List[0].Type.(*ast.Ident); !ok || id.Name != "string" {
				continue
			}
			found[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "filepath" {
					return true
				}
				t.Errorf("%s:%d %s builds a record's path with filepath.%s.\n"+
					"A record's path is a path INSIDE THE RECORD BRANCH: it is written into the record's own `file` field, "+
					"it is a pathspec for `git add`, and it is the right-hand side of `git show <rev>:<path>` -- and git "+
					"spells all three with forward slashes on every platform. Use path.Join. On unix the two are the same "+
					"function and every test passes; on Windows this is a verb that exits 1 (#1335).",
					fset.Position(sel.Pos()).Filename, fset.Position(sel.Pos()).Line, fn.Name.Name, sel.Sel.Name)
				return false
			})
		}
	}
	for _, want := range []string{"ReadFile", "GateFile", "ClassifyFile", "SummaryFile"} {
		if !found[want] {
			t.Errorf("%s was not among the record-path builders this test read; it was renamed or removed, and this test would have passed by checking nothing", want)
		}
	}
}

// TestRecordPathBuildersHoldNoSeparatorOfTheHosts is the value side of the same rule, with
// WINDOWS-SHAPED INPUT on any host: a run id, an entry and a reader's name that each carry
// a backslash, a drive letter and a space. What comes back is one git path, every
// separator a forward slash, and nothing of the input's own punctuation left in a name.
func TestRecordPathBuildersHoldNoSeparatorOfTheHosts(t *testing.T) {
	t.Parallel()
	sub := Submission{At: "2026-09-11T13:00:00Z", Rand: "b23c6f"}
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	gate := GateFile("951", head, base, sub)
	for _, tc := range []struct{ name, got, want string }{
		{"ClassifyFile", ClassifyFile("run-9", sub), "classify/run-9-20260911T130000Z-b23c6f.json"},
		{`ClassifyFile with a windows run id`, ClassifyFile(`C:\runs\run 9`, sub), "classify/C__runs_run_9-20260911T130000Z-b23c6f.json"},
		{"ReadFile", ReadFile("951", "emma", head, sub), "reads/951/emma-aaaaaaaaaaaa-20260911T130000Z-b23c6f.json"},
		{`ReadFile with a windows who`, ReadFile("951", `lab\emma`, head, sub), "reads/951/lab_emma-aaaaaaaaaaaa-20260911T130000Z-b23c6f.json"},
		{"GateFile", gate, "gates/951/aaaaaaaaaaaa-bbbbbbbbbbbb-20260911T130000Z-b23c6f.json"},
		{"SummaryFile", SummaryFile(gate), "gates/951/aaaaaaaaaaaa-bbbbbbbbbbbb-20260911T130000Z-b23c6f.summary"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
		if strings.ContainsRune(tc.got, '\\') {
			t.Errorf("%s = %q, which carries a backslash: git reads that as one character of a file's NAME, not as a separator, and the confirming fetch will never find it", tc.name, tc.got)
		}
	}
}

// TestOutboxMakesAWindowsPathAGitPath drives the boundary itself. destinationOf is the one
// place a record's `file` field becomes the path the CAS loop hands to git -- the restore,
// the `add` pathspec and the `show <rev>:<path>` all come from here -- so it is where the
// whole class is made a no-op: a record an older build (or a builder that reaches for
// filepath.Join again) left with a backslash is delivered to the right git path anyway.
//
// It runs on any host: the outbox item is written with a backslash in its `file` on
// purpose, which is exactly what Windows produced.
func TestOutboxMakesAWindowsPathAGitPath(t *testing.T) {
	t.Parallel()
	lane := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lane, OutboxDir), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"run":"run-9","verdict":"own-change","at":"2026-09-11T13:00:00Z",` +
		`"file":"classify\\run-9-20260911T130000Z-b23c6f.json"}` + "\n")
	if err := os.WriteFile(filepath.Join(lane, OutboxDir, "20260911T130000Z-b23c6f.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Records{Lane: lane}
	items, taken, err := r.outbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(taken) != 1 {
		t.Fatalf("one item waiting, got %d items and %d taken", len(items), len(taken))
	}
	if want := "classify/run-9-20260911T130000Z-b23c6f.json"; items[0].Path != want {
		t.Errorf("the path git is handed = %q, want %q", items[0].Path, want)
	}
	// The guard is a spelling, not a licence: a path that climbs out of the lane is still
	// refused, whichever separator it climbs with.
	for _, bad := range []string{`..\\..\\etc\\passwd`, `../../etc/passwd`, `C:\\Windows\\win.ini`, `/etc/passwd`} {
		out := filepath.Join(lane, OutboxDir, "20260911T130000Z-ffffff.json")
		if err := os.WriteFile(out, []byte(`{"file":"`+bad+`"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.outbox(); err == nil {
			t.Errorf("a record whose file is %q was accepted; a record's file is a path under the lane", bad)
		}
		if err := os.Remove(out); err != nil {
			t.Fatal(err)
		}
	}
}
