package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// numberWords spells the counts this test expects a document sentence to carry.
var numberWords = map[int]string{
	1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six",
	7: "seven", 8: "eight", 9: "nine", 10: "ten", 11: "eleven", 12: "twelve",
}

// docs/CLI.md is the command reference a stranger copies from, so a sentence
// in it about what a tool prints is a claim a reader trusts without running the
// tool. A COUNTED claim about a tool's own help output is worse: it rots
// silently as verbs are added, and nobody notices until someone counts.
//
// The definition in cmd/nova-tokens is the truth here, and the document is
// judged against it: internal/tool prints one usage block from every
// tool.Verb's Name and Usage, `version` added, and one example block from every
// Verb's Example lines. `report` carries two usage lines and one name, which is
// why this test counts names, not lines.
//
// The number in each expected sentence is derived from the definition rather
// than hard-coded, so the test keeps its meaning when a verb is added.
func TestTheCLIReferenceCountsNovaTokensHelpCorrectly(t *testing.T) {
	t.Parallel()

	const source = "../../cmd/nova-tokens"
	files, err := filepath.Glob(filepath.Join(source, "*.go"))
	require.NoError(t, err)
	verbs, examples := 1, 0 // version is every tool's, added by internal/tool
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if sel, isSel := lit.Type.(*ast.SelectorExpr); !isSel || sel.Sel.Name != "Verb" {
				return true
			}
			for _, e := range lit.Elts {
				kv := e.(*ast.KeyValueExpr)
				switch kv.Key.(*ast.Ident).Name {
				case "Name":
					verbs++
				case "Example":
					text, err := strconv.Unquote(kv.Value.(*ast.BasicLit).Value)
					require.NoError(t, err)
					for _, l := range strings.Split(text, "\n") {
						if strings.TrimSpace(l) != "" {
							examples++
						}
					}
				}
			}
			return true
		})
	}
	require.GreaterOrEqual(t, verbs, 5, "%s: the definition names %d verbs; a scan that finds almost none would pass by asking nothing", source, verbs)

	ref, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err, "../../docs/CLI.md: %v", err)
	doc := string(ref)

	verbWord, ok := numberWords[verbs]
	require.True(t, ok, "%s: the definition names %d verbs, outside the 1..12 this test can spell; widen numberWords", source, verbs)
	verbClaim := "`nova-tokens help` lists all " + verbWord + " verbs."
	assert.Contains(t, doc, verbClaim, "docs/CLI.md makes a counted claim about the tool's own help that is wrong: it should say %q, where %s defines %d verbs -- correct the sentence",
		verbClaim, source, verbs)

	exampleWord, ok := numberWords[examples]
	require.True(t, ok, "%s: the help carries %d example lines, outside the 1..12 this test can spell; widen numberWords", source, examples)
	exampleClaim := "`nova-tokens help` carries " + exampleWord + " example lines"
	assert.Contains(t, doc, exampleClaim, "docs/CLI.md makes a counted claim about the tool's own help that is wrong: it should say %q, where %s's verbs carry %d example lines -- correct the sentence",
		exampleClaim, source, examples)
}
