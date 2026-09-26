package ci

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// forestWriterAllowlistPath is the shrink-only list of file writes the rule
// below still permits in a package that reads work sets. Every entry names the
// target it writes, and none of them is the forest.
const forestWriterAllowlistPath = "testdata/forestwriter_allowlist.txt"

// forestWriter is the one function in the tree that writes a work set back to
// disk, and it refuses a docs/roadmaps/ path before a byte moves (#3340).
const forestWriter = "cmd/nova-work/forestwrite.go:writeWorkSet"

// forestDirLiteral is the forest as a source string names it.
const forestDirLiteral = "docs/roadmaps/"

// worklangImport marks a package that can hold a work set's bytes.
const worklangImport = `"github.com/mas-bandwidth/nova-tools/internal/worklang"`

// TestForestWrittenOnlyByTheKernel is the class rule of #3340 (stage 1 of
// #3309): the forest -- every file under docs/roadmaps/ -- has one writer, the
// nova-work kernel, and no other code path in this tree writes it. Three reads
// of the tree hold it:
//
//  1. Every file write (os.WriteFile, os.Create, os.CreateTemp, a writable
//     os.OpenFile, os.Rename, os.Truncate, os.Link, os.Symlink and their ioutil
//     spellings) in a package that reads work sets -- internal/worklang and
//     every package with a non-test file importing it -- is the one writer or
//     an entry on the shrink-only list, checked both ways.
//  2. No function anywhere under cmd/ or internal/ both writes a file and names
//     docs/roadmaps/ in a string, the one writer excepted.
//  3. No script or workflow edits a file in place, redirects, tees, copies or
//     moves onto docs/roadmaps/ (the bash that did, rowan-tools bin/sprint-xy,
//     is in another repository and is deleted by #3340's rowan-tools half).
func TestForestWrittenOnlyByTheKernel(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readForestWriterAllowlist(t)
	files := tree.GoFilesUnder(false, "cmd", "internal")

	scope := map[string]bool{"internal/worklang": true}
	for _, src := range files {
		if src.ParseErr != nil {
			t.Fatal(src.ParseErr)
		}
		for _, imp := range src.AST.Imports {
			if imp.Path.Value == worklangImport {
				scope[path.Dir(src.Rel)] = true
			}
		}
	}

	seen := map[string]bool{}
	sawWriter := false
	var violations []string
	for _, src := range files {
		inScope := scope[path.Dir(src.Rel)]
		for _, decl := range src.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			writes := fileWriteCalls(fn.Body)
			if len(writes) == 0 {
				continue
			}
			key := src.Rel + ":" + removeAllFuncName(fn)
			if key == forestWriter {
				sawWriter = true
				continue
			}
			line := tree.FSet.Position(writes[0].Pos()).Line
			if namesForest(fn.Body) {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: %s writes a file and names %s; the forest is written only by the nova-work kernel (#3340), and a work set goes through %s, which refuses it",
					src.Rel, line, key, forestDirLiteral, forestWriter))
			}
			if !inScope {
				continue
			}
			seen[key] = true
			if !allow.Has(key) {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: %s writes a file in a package that reads work sets and is not on %s; write a work set through %s (it refuses the forest), or list this call with the target it writes",
					src.Rel, line, key, forestWriterAllowlistPath, forestWriter))
			}
		}
	}
	if !sawWriter {
		violations = append(violations, fmt.Sprintf(
			"%s is gone or writes nothing; it is the one writer of a work set and the rule is anchored on it", forestWriter))
	}
	for _, row := range allowlist.Check(t, allow, seen).Stale {
		key := row.Key
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no file write is there any more; delete the stale entry (the list only shrinks)",
			forestWriterAllowlistPath, key))
	}

	for _, f := range tree.Files {
		if f.Go || f.HasDirNamed("testdata") || !isScriptOrWorkflow(f.Rel) {
			continue
		}
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		violations = append(violations, scriptForestWrites(f.Rel, string(raw))...)
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestForestScriptRuleSeesAWrite pins rule 3 against the shapes it exists for,
// so a regexp that stops matching is a red run rather than a rule that passes
// by reading nothing.
func TestForestScriptRuleSeesAWrite(t *testing.T) {
	t.Parallel()
	for _, script := range []string{
		"sed -i 's/open/landed/' docs/roadmaps/nova-work.sexp\n",
		"F=docs/roadmaps/nova-work.sexp\nsed -i.bak \"s/a/b/\" \"$F\"\n",
		"perl -pi -e 's/a/b/' docs/roadmaps/x.sexp\n",
		"render > docs/roadmaps/nova-work.sexp\n",
		"render >>\"docs/roadmaps/nova-work.sexp\"\n",
		"render | tee docs/roadmaps/nova-work.sexp\n",
		"cp /tmp/x.sexp docs/roadmaps/nova-work.sexp\n",
		"mv /tmp/x.sexp docs/roadmaps/nova-work.sexp\n",
	} {
		if len(scriptForestWrites("x.sh", script)) == 0 {
			t.Errorf("rule 3 missed a forest write in %q", script)
		}
	}
	for _, script := range []string{
		"sexp=${2:-docs/roadmaps/nova-work.sexp}\nawk '{print}' \"$sexp\" > /tmp/out\n",
		"git show origin/forest:docs/roadmaps/nova-work.sexp | grep unit\n",
	} {
		if got := scriptForestWrites("x.sh", script); len(got) != 0 {
			t.Errorf("rule 3 flagged a read: %q -> %v", script, got)
		}
	}
}

// fileWriteCalls returns the calls in body that create, replace or rename a
// file. An os.OpenFile whose flag is exactly os.O_RDONLY is a read.
func fileWriteCalls(body *ast.BlockStmt) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch pkg.Name + "." + sel.Sel.Name {
		case "os.WriteFile", "os.Create", "os.CreateTemp", "os.Rename", "os.Truncate", "os.Link", "os.Symlink",
			"ioutil.WriteFile", "ioutil.TempFile":
			out = append(out, call)
		case "os.OpenFile":
			if len(call.Args) >= 2 && isSelector(call.Args[1], "os", "O_RDONLY") {
				return true
			}
			out = append(out, call)
		}
		return true
	})
	return out
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

// namesForest reports whether a string literal in body names docs/roadmaps/.
func namesForest(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, forestDirLiteral) {
			found = true
		}
		return !found
	})
	return found
}

// isScriptOrWorkflow is the text rule 3 reads: shell, Makefiles and workflows.
func isScriptOrWorkflow(rel string) bool {
	base := path.Base(rel)
	switch {
	case strings.HasSuffix(base, ".sh"), strings.HasSuffix(base, ".bash"), strings.HasSuffix(base, ".bats"):
		return true
	case base == "Makefile" || strings.HasSuffix(base, ".mk"):
		return true
	case strings.HasPrefix(rel, ".github/") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")):
		return true
	}
	return false
}

var (
	// inPlaceEdit is sed or perl editing a file where it lies.
	inPlaceEdit = regexp.MustCompile(`\bsed\s+(-[A-Za-z]*i|--in-place)|\bperl\s+-[A-Za-z]*i`)
	// forestOnto is a redirect, tee, cp, mv, install or truncate whose target is
	// a docs/roadmaps/ path.
	forestOnto = regexp.MustCompile(`(>>?\s*["']?|\btee\s+(-a\s+)?["']?|\b(cp|mv|install|truncate)\s+.*\s["']?)\S*docs/roadmaps/`)
)

// scriptForestWrites is rule 3 over one script: a line that writes onto a
// docs/roadmaps/ path, and an in-place edit anywhere in a script that names the
// forest at all (the path usually arrives through a variable).
func scriptForestWrites(rel, text string) []string {
	var out []string
	namesForest := strings.Contains(text, forestDirLiteral)
	if !namesForest {
		return nil
	}
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if inPlaceEdit.MatchString(line) || forestOnto.MatchString(line) {
			out = append(out, fmt.Sprintf(
				"%s:%d: a script writes the forest (%s); only the nova-work kernel writes it (#3340)",
				rel, i+1, strings.TrimSpace(line)))
		}
	}
	return out
}

// readForestWriterAllowlist reads `file:function # target` lines. An entry
// without the target it writes is itself a red run: the list says what each
// write is, so a reader can see that none of them is the forest.
func readForestWriterAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	allow := loadAllowlist(t, forestWriterAllowlistPath, shrinkOnly)
	for _, row := range allow.Rows() {
		i := strings.Index(row.Text, " #")
		if i < 0 || strings.TrimSpace(row.Text[i+2:]) == "" {
			t.Errorf("%s:%d: %q names no target; write `file:function # what it writes`", forestWriterAllowlistPath, row.Line, row.Text)
		}
	}
	return allow
}
