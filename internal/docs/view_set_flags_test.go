package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// view_set_flags_test.go pins the `view set` usage line of each nova-table
// reference to the flags the verb registers: a flag on the verb and not in a
// usage line is a flag a reader of that page cannot find. The flags are read
// from the `if sub == "set"` block of cmdView in cmd/nova-table/table.go.
func TestTheViewSetUsageLinesNameEveryFlag(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../cmd/nova-table/table.go")
	require.NoError(t, err)
	_, block, ok := strings.Cut(string(src), `if sub == "set" {`)
	require.True(t, ok, `cmd/nova-table/table.go: no if sub == "set" block`)
	block, _, _ = strings.Cut(block, "\n\t}\n")
	flags := regexp.MustCompile(`fs\.StringVar\(&\w+, "([a-z-]+)"`).FindAllStringSubmatch(block, -1)
	require.GreaterOrEqual(t, len(flags), 3, "read %d flags of view set, want at least 3", len(flags))
	for _, doc := range []string{"../../docs/nova-table/README.md", "../../docs/CLI.md", "../../docs/SPEC-NOVA-TABLE.md"} {
		text, err := os.ReadFile(doc)
		require.NoError(t, err)
		found := 0
		for _, line := range strings.Split(string(text), "\n") {
			if !strings.Contains(line, "nova-table view set ") && !strings.Contains(line, "`view set <name>") {
				continue
			}
			if !strings.Contains(line, "--tables <") {
				continue // an example line, not the usage line
			}
			found++
			for _, f := range flags {
				assert.Contains(t, line, "--"+f[1], "%s: the view set usage line does not name --%s: %s", doc, f[1], line)
			}
		}
		assert.NotZero(t, found, "%s: no view set usage line found", doc)
	}
}
