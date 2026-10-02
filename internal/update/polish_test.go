package update

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTool runs one invocation of either name in dir and returns its exit and streams.
func runTool(t *testing.T, name string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Run(name, args, "v0", &out, &errs, Environment{})
	return code, out.String(), errs.String()
}

// writeFile writes body to name under a fresh temporary directory and returns its path.
func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// A header refusal shows the tab as <TAB>, the way the banner spells it, never
// the escape \x09 (ledger U3, V6): a reader copies the header from the line.
func TestAHeaderRefusalSpellsTheTabAsTAB(t *testing.T) {
	t.Parallel()
	bad := writeFile(t, "bad.tsv", "name\tkind\n")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"check", []string{"check", "--file", bad}, "name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner"},
		{"version report", []string{"report", "--file", bad}, "name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner"},
		{"adoption", []string{"adoption", "--file", bad}, "tool<TAB>friend<TAB>state<TAB>version<TAB>detail"},
		{"watch", []string{"watch", "--adopt", bad}, "check<TAB>command<TAB>owner"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := "nova-update"
			if c.name == "version report" {
				name = "nova-version"
			}
			code, _, errs := runTool(t, name, c.args...)
			assert.Equal(t, 2, code)
			assert.Contains(t, errs, c.want)
			assert.NotContains(t, errs, `\x09`)
		})
	}
	t.Run("diff", func(t *testing.T) {
		code, _, errs := runTool(t, "nova-version", "diff", "--from", bad, "--to", bad)
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "name<TAB>stamp<TAB>revision<TAB>platform")
	})
}
