package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// schema_fuzz_spec_test.go holds docs/SPEC-SCHEMA-FUZZ.md, the one source of
// the neighbour oracle every generated schema wire package's fuzz test
// copies, to its shape: exactly one fenced go block, which is the block the
// fuzz-<name>-prefix-oracle cards append, every one of the 16 packages named,
// both halves of the rule stated, and the rulings section present. It reads
// the document as text and runs no product code.

// specSchemaFuzzPath is the fuzz oracle spec, relative to this package.
const specSchemaFuzzPath = "../../docs/SPEC-SCHEMA-FUZZ.md"

// schemaFuzzGoBlockRe reads every fenced go block of the spec.
var schemaFuzzGoBlockRe = regexp.MustCompile("(?ms)^```go\n(.*?)^```")

// schemaFuzzPackages is every generated schema wire package with a fuzz test:
// 11 in nova-tools pkg/, 5 in nova-sprint internal/sprint/store/.
var schemaFuzzPackages = []string{
	"buswire", "configwire", "decidewire", "friendfilewire", "hookwire",
	"ledgerwire", "pongwire", "releasewire", "seatrecwire", "swarmwire",
	"tablemodelwire",
	"donewire", "friendrowswire", "oprowswire", "snaprowswire", "viewrowswire",
}

// TestSchemaFuzzSpecCarriesTheNeighbourOracle is the spec's contract: the one
// fenced go block starts with fuzzNeighbours, so the follow-on cards copy one
// text; all 16 packages are named; the strict prefix and one byte appended
// halves of the rule are stated; and the spec has its rulings.
func TestSchemaFuzzSpecCarriesTheNeighbourOracle(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(specSchemaFuzzPath)
	require.NoError(t, err, "%s: the schema fuzz oracle spec", specSchemaFuzzPath)
	doc := string(raw)

	blocks := schemaFuzzGoBlockRe.FindAllStringSubmatch(doc, -1)
	require.Len(t, blocks, 1, "%s: exactly one fenced go block, the block the follow-ons copy", specSchemaFuzzPath)
	body := strings.TrimLeft(blocks[0][1], "\n")
	first, _, _ := strings.Cut(body, "\n")
	require.Equal(t, "func fuzzNeighbours(root fuzzRoot, wire []byte) string {", first, "%s: the go block starts with fuzzNeighbours", specSchemaFuzzPath)

	for _, name := range schemaFuzzPackages {
		require.Contains(t, doc, name, "%s: package %s is not named", specSchemaFuzzPath, name)
	}
	for _, phrase := range []string{"strict prefix", "one byte appended"} {
		require.Contains(t, doc, phrase, "%s: the rule half %q is not stated", specSchemaFuzzPath, phrase)
	}
	require.Regexp(t, `(?m)^## Rulings\s*$`, doc, "%s: no ## Rulings heading", specSchemaFuzzPath)
}
