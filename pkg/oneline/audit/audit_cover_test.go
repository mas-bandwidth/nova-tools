package audit

// audit_cover_test.go reaches the audit walk with what an in-process unit test
// has: this package's own directory as the source under test, and the source
// struct as the seam for fabricated files. Two gaps are named, never hidden.
// Every refusal here reports through the caller's *testing.T, so a pin of one
// fails the very test that holds it; and Bypasses cannot complete on the only
// source an in-process test can read, because the substring check for
// io.WriteString matches audit.go's own naming of that check in its comment
// and in the message it prints. Both gaps need a clean package directory,
// which only a second test binary -- a subprocess -- can offer. Nothing here
// sleeps, dials, starts a process or reads a clock.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureSource parses code in memory and answers it as the package's own
// source value, the seam packageConsts and bypassesIn read.
func fixtureSource(t *testing.T, name, code string) source {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, name, code, 0)
	require.NoError(t, err, "%s: a fixture must parse", name)
	return source{name: name, src: []byte(code), fset: fset, parsed: parsed}
}

// TestAuditCoverSourcesReadsEveryNonTestFile pins sources' main path in the
// directory go test hands it: one non-test file, this package's audit.go,
// parsed under this package's name. The count also holds the filter, since the
// two _test.go files of this directory must stay invisible to the walk.
func TestAuditCoverSourcesReadsEveryNonTestFile(t *testing.T) {
	t.Parallel()

	files := sources(t)
	require.Len(t, files, 1, "the directory holds audit.go alone once the _test.go files are skipped")
	assert.Equal(t, "audit.go", files[0].name)
	assert.Equal(t, "audit", files[0].parsed.Name.Name, "the parsed file is this package's")
	assert.NotEmpty(t, files[0].src, "the raw source is kept beside the parse")
}

// TestAuditCoverTextAndAtLocateNodesInTheSource pins the two node accessors:
// text answers the file's own bytes over the node's span, at answers name:line.
// The exact line comes from an in-memory fixture, so nothing here holds a line
// number of audit.go, which other changes move; the real file pins its name.
func TestAuditCoverTextAndAtLocateNodesInTheSource(t *testing.T) {
	t.Parallel()

	const code = "package p\n\nfunc F() {\n\tvar x = 1\n}\n"
	f := fixtureSource(t, "fixture.go", code)
	fn := f.parsed.Decls[0].(*ast.FuncDecl)
	assert.Equal(t, strings.TrimSuffix(code, "\n"), f.text(f.parsed), "a file node's span is the file's own bytes up to the last syntax")
	assert.Equal(t, "F", f.text(fn.Name), "an identifier's text is its own spelling")
	assert.Equal(t, "fixture.go:3", f.at(fn), "F's declaration is on the fixture's third line")

	real := sources(t)[0]
	assert.Equal(t, "audit", real.text(real.parsed.Name), "the package clause of audit.go")
	assert.True(t, strings.HasPrefix(real.at(real.parsed.Name), "audit.go:"),
		"at answers name:line for the real file too, got %q", real.at(real.parsed.Name))
}

// TestAuditCoverPackageConstsCountsOnlyConstants pins packageConsts over two
// files: the four constants are collected, a var and a func are not, and this
// package -- which declares no constant -- answers an empty map, so no
// identifier passes as a literal here by accident.
func TestAuditCoverPackageConstsCountsOnlyConstants(t *testing.T) {
	t.Parallel()

	first := fixtureSource(t, "first.go", "package p\n\nconst A = \"a\"\n\nconst (\n\tB = 1\n\tC = 2\n)\n\nvar V = 3\n\nfunc F() {}\n")
	second := fixtureSource(t, "second.go", "package p\n\nconst D = true\n\nvar W = 4\n\nfunc G() {}\n")
	got := packageConsts([]source{first, second})
	assert.Equal(t, map[string]bool{"A": true, "B": true, "C": true, "D": true}, got,
		"the four constants are collected across both files; V, W, F and G are not")
	assert.Empty(t, packageConsts(sources(t)), "audit.go declares no package constant")
}

// TestAuditCoverPrintedArgumentsClassifiesThisPackage pins the walk's main path
// over the only source an in-process test can read, audit.go itself: the one
// fmt call in the file, the locator's Sprintf, is reached, its two verbs are
// paired with its two arguments, both are counted, and the file name is
// accepted under an exemption that names its reason. MinClassified is the exact
// count the file holds, so a walk that stops reaching the file, mispairs the
// verbs or loses the count turns this red through the shortfall finding, and a
// drifting exemption key turns it red through the raw-print finding.
func TestAuditCoverPrintedArgumentsClassifiesThisPackage(t *testing.T) {
	t.Parallel()

	PrintedArguments(t, Config{
		Escapers: []string{"oneline.Quote"}, // a name audit.go never calls; the seam is taken, the map stays honest
		Exempt: map[string]string{
			// at's Sprintf prints the file's own name, read by sources() from the
			// directory listing, beside a line the file set computed.
			"audit.go|at|f.name": "the audit's own file locator: a name from the listing and a line from the file set",
		},
		MinClassified: 2,
	})
}

// TestAuditCoverBypassesInWatchesAWholeFixturePackage pins bypassesIn's main
// path over a fabricated source: every shape the walk must accept goes by
// without a finding -- a fmt print whose argument went through oneline, a flag
// set writing to io.Discard, atomicfile.Write by name, and the four
// declaration forms -- with every import on the allowed list.
func TestAuditCoverBypassesInWatchesAWholeFixturePackage(t *testing.T) {
	t.Parallel()

	code := `package fixture

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

func Greeting(dir, name string) (string, error) {
	flags := flag.NewFlagSet("greeting", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var count int
	for i, word := range []string{"a", "b"} {
		count += i + len(word)
	}
	if err := atomicfile.Write(filepath.Join(dir, name), []byte(oneline.Escape(name))); err != nil {
		return "", err
	}
	return fmt.Sprintf("hello %s\n", oneline.Field(strconv.Quote(name))), nil
}
`
	f := fixtureSource(t, "fixture.go", code)
	shadows := map[string]bool{"oneline": true, "Escape": true, "Field": true, "Err": true}
	allowed := map[string]bool{
		`"github.com/mas-bandwidth/nova-tools/pkg/oneline"`:    true,
		`"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"`: true,
		`"flag"`: true, `"fmt"`: true, `"io"`: true, `"path/filepath"`: true, `"strconv"`: true,
	}
	bypassesIn(t, f, shadows, allowed)
}
