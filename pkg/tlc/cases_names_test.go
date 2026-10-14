package tlc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// names a tree of files under a fresh root.
func casesTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for f, s := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
	}
	return dir
}

const casesHead = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

func caseRow(config, module string) string {
	return config + "\t" + module + "\tpass\t-\tcheck\talpha\trequired\t-\n"
}

// LoadCases holds the plan to the tree (docs/SPEC-SPRINT.md section 7, the lander's checks,
// "The run records"): its error keeps the opening it always had and names, in name order,
// every MC*.cfg the plan does not name, every config the plan names that has no file, and
// every row whose module is missing.
func TestLoadCasesNamesWhatThePlanAndTheTreeMiss(t *testing.T) {
	t.Parallel()

	t.Run("it names every file the plan does not name, in name order", func(t *testing.T) {
		t.Parallel()
		dir := casesTree(t, map[string]string{
			"tla/CASES.tsv":  casesHead + caseRow("MCA.cfg", "MCA.tla"),
			"tla/MCA.cfg":    "INIT Init\n",
			"tla/MCA.tla":    "---- MODULE MCA ----\n====\n",
			"tla/MCZed.cfg":  "INIT Init\n",
			"tla/MCAble.cfg": "INIT Init\n",
		})
		_, err := tlc.LoadCases(dir)
		require.Error(t, err)
		assert.Equal(t, "CASES.tsv must name every MC*.cfg exactly once: the plan does not name MCAble.cfg, MCZed.cfg", err.Error())
	})

	t.Run("it names every config the plan names that has no file", func(t *testing.T) {
		t.Parallel()
		dir := casesTree(t, map[string]string{
			"tla/CASES.tsv": casesHead + caseRow("MCA.cfg", "MCA.tla") + caseRow("MCGhost.cfg", "MCGhost.tla"),
			"tla/MCA.cfg":   "INIT Init\n",
			"tla/MCA.tla":   "---- MODULE MCA ----\n====\n",
		})
		_, err := tlc.LoadCases(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CASES.tsv must name every MC*.cfg exactly once")
		assert.Contains(t, err.Error(), "the plan names MCGhost.cfg, which has no file")
	})

	t.Run("it names every row whose module is missing, in name order", func(t *testing.T) {
		t.Parallel()
		dir := casesTree(t, map[string]string{
			"tla/CASES.tsv": casesHead + caseRow("MCA.cfg", "MCA.tla") + caseRow("MCB.cfg", "MCB.tla"),
			"tla/MCA.cfg":   "INIT Init\n",
			"tla/MCB.cfg":   "INIT Init\n",
		})
		_, err := tlc.LoadCases(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing instance module MCA.tla, MCB.tla")
	})

	t.Run("a plan that matches the tree is returned", func(t *testing.T) {
		t.Parallel()
		dir := casesTree(t, map[string]string{
			"tla/CASES.tsv": casesHead + caseRow("MCA.cfg", "MCA.tla"),
			"tla/MCA.cfg":   "INIT Init\n",
			"tla/MCA.tla":   "---- MODULE MCA ----\n====\n",
		})
		cases, err := tlc.LoadCases(dir)
		require.NoError(t, err)
		require.Len(t, cases, 1)
		assert.Equal(t, "MCA.cfg", cases[0].Config)
	})
}
