package ci

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: pkg/ IS NOVA-TOOLS' PUBLIC FACE, AND IT NEVER REACHES INTO THE SPRINT.
//
// nova-sprint leaves for a repository of its own (the split, v1.2.3) and imports this
// module's building blocks from pkg/ (split L1, tools/split/l1-move.sh). A package under pkg/
// that imported nova-sprint's code (sprintOnlyPaths), in its code or in its tests, would tie
// the blocks back to the system built on them, and nova-sprint's module could not import it
// without a cycle between the two repositories. A pkg/ package's code that imported internal/
// would hand nova-sprint a type it cannot name. Both are red here.
func TestPkgNeverImportsTheSprintOrInternal(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)
	files := append(tree.GoFilesUnder(false, "pkg"), tree.GoFilesUnder(true, "pkg")...)
	require.NotEmpty(t, files, "no Go file under pkg/; a rule that checks nothing passes")
	for _, finding := range pkgBoundaryFindings(files) {
		t.Error(finding)
	}
}

// The rule's reversed witness: a pkg/ file importing nova-sprint's code (from its test as
// well as its code), and a pkg/ code file importing internal/, are each found; a pkg/ test
// importing internal/ and a pkg/ file importing pkg/ are not.
func TestPkgBoundaryFindsTheSprintAndInternalImports(t *testing.T) {
	t.Parallel()
	const m = "github.com/mas-bandwidth/nova-tools/"
	file := func(rel, src string) *treeFile {
		f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ImportsOnly)
		require.NoError(t, err)
		return &treeFile{Rel: rel, Go: true, Test: strings.HasSuffix(rel, "_test.go"), AST: f}
	}
	findings := pkgBoundaryFindings([]*treeFile{
		file("pkg/a/a.go", `package a; import _ "`+m+`internal/sprint/store"`),
		file("pkg/a/a_test.go", `package a; import _ "`+m+`cmd/nova-sprint"`),
		file("pkg/b/b.go", `package b; import _ "`+m+`internal/other"`),
		file("pkg/b/b_test.go", `package b; import _ "`+m+`internal/other"`),
		file("pkg/c/c.go", `package c; import ( _ "`+m+`pkg/a"; _ "strings" )`),
	})
	require.Len(t, findings, 4, "%q", findings)
	require.Contains(t, findings[0], "pkg/a/a.go imports internal/sprint/store")
	require.Contains(t, findings[1], "pkg/a/a.go imports internal/sprint/store")
	require.Contains(t, findings[2], "pkg/a/a_test.go imports cmd/nova-sprint")
	require.Contains(t, findings[3], "pkg/b/b.go imports internal/other")
}

// pkgBoundaryFindings is one line per import a pkg/ file may not make.
func pkgBoundaryFindings(files []*treeFile) []string {
	const module = "github.com/mas-bandwidth/nova-tools/"
	var out []string
	for _, f := range files {
		if f.AST == nil {
			continue
		}
		for _, imp := range f.AST.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !strings.HasPrefix(path, module) {
				continue
			}
			rel := strings.TrimPrefix(path, module)
			if sprintOnly(rel) {
				out = append(out, f.Rel+" imports "+rel+": pkg/ is nova-tools' public face and never imports nova-sprint's code")
			}
			if !f.Test && strings.HasPrefix(rel, "internal/") {
				out = append(out, f.Rel+" imports "+rel+": a pkg/ package's code imports only pkg/ and outside modules, so every type it hands out can be named")
			}
		}
	}
	return out
}
