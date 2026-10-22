package docs

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// clidocBegin matches one `<!-- clidoc:begin <tool> -->` marker and captures
// the tool it names, the same shape tools/clidoc rewrites.
var clidocBegin = regexp.MustCompile(`<!-- clidoc:begin (nova-[a-z0-9-]+) -->`)

// markerTools returns the tools docs/CLI.md's begin markers name, each once,
// in file order: exactly the tools clidoc regenerates.
func markerTools(doc string) []string {
	var tools []string
	seen := map[string]bool{}
	for _, m := range clidocBegin.FindAllStringSubmatch(doc, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			tools = append(tools, m[1])
		}
	}
	return tools
}

// TestMarkerToolsHoldsTheScan pins the scan the generated check builds from: a
// marker names its tool once however often it stands, and text that is no
// marker names nothing.
func TestMarkerToolsHoldsTheScan(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"nova-check", "nova-fuse"},
		markerTools("<!-- clidoc:begin nova-check -->\n<!-- clidoc:begin nova-fuse -->\n<!-- clidoc:begin nova-check -->"))
	require.Empty(t, markerTools("no markers here\n<!-- clidoc:begin -->\n<!-- clidoc:end nova-check -->"))
}
