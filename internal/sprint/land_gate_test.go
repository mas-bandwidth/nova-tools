package sprint_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A batch whose own package is fine and whose tree packages pass, but which
// breaks a package that imports the one it changed, is refused, and the
// finding names that other package. The runner is a fake: no module is built.
func TestTheTreeGateRefusesABatchThatBreaksAnotherPackage(t *testing.T) {
	t.Parallel()
	pkgs := []sprint.ModulePackage{
		{Import: "example.com/m/internal/lib", Dir: "internal/lib"},
		{Import: "example.com/m/cmd/tool", Dir: "cmd/tool", Imports: []string{"example.com/m/internal/lib"}},
		{Import: "example.com/m/cmd/other", Dir: "cmd/other"},
		{Import: "example.com/m/internal/docs", Dir: "internal/docs"},
		{Import: "example.com/m/internal/ci", Dir: "internal/ci"},
	}
	got := sprint.TreeGatePkgs([]string{"internal/lib/lib.go"}, pkgs, []string{"internal/docs", "internal/ci"})
	assert.Equal(t, []string{"internal/docs", "internal/ci", "cmd/tool", "internal/lib"}, got)

	runs := sprint.TreeGateArgv(got, true)
	require.GreaterOrEqual(t, len(runs), 4)
	assert.Equal(t, []string{"go", "build", "./..."}, runs[0])
	assert.Equal(t, []string{"go", "vet", "./..."}, runs[1])
	assert.Contains(t, runs[2], "./cmd/tool/")
	assert.Contains(t, runs[2], "./internal/lib/")
	assert.NotContains(t, runs[2], "./cmd/other/")
	assert.Equal(t, []string{"go", "test", "-tags", "functional", "-run", sprint.FunctionalTreeRun, "-timeout", sprint.FunctionalTreeTimeout, "./internal/ci/"}, runs[len(runs)-1])
	why := sprint.FirstGateFinding(runs, func(argv []string) (string, error) {
		if len(argv) > 2 && argv[1] == "test" && slices.Contains(argv, "./cmd/tool/") && !slices.Contains(argv, "-tags") {
			return "--- FAIL: TestRemember\nFAIL\texample.com/m/cmd/tool\n", errors.New("exit status 1")
		}
		return "", nil
	})
	require.NotEmpty(t, why, "a red package the batch did not change is a refusal")
	assert.Contains(t, why, "./cmd/tool/")
	assert.Contains(t, why, "example.com/m/cmd/tool")
	assert.NotContains(t, why, "cmd/other")
	assert.NotContains(t, why, "-tags")
	assert.Empty(t, sprint.FirstGateFinding(runs, func([]string) (string, error) { return "", nil }))
}

// A file at the module root does not select every nested package. A file under
// testdata selects the package that contains it and the packages that import
// that one.
func TestTheTreeGateSelectsImportersAndNotTheWholeModule(t *testing.T) {
	t.Parallel()
	pkgs := []sprint.ModulePackage{
		{Import: "example.com/m", Dir: "."},
		{Import: "example.com/m/internal/lib", Dir: "internal/lib"},
		{Import: "example.com/m/internal/lib/sub", Dir: "internal/lib/sub", Imports: []string{"example.com/m/internal/lib"}},
		{Import: "example.com/m/cmd/reader", Dir: "cmd/reader", Imports: []string{"testing", "example.com/m/internal/lib"}},
	}
	assert.Equal(t, []string{"internal/docs", "."}, sprint.TreeGatePkgs([]string{"README.md"}, pkgs, []string{"internal/docs"}))
	assert.Equal(t, []string{"cmd/reader", "internal/lib", "internal/lib/sub"},
		sprint.TreeGatePkgs([]string{"internal/lib/testdata/case.txt"}, pkgs, nil))
	assert.Equal(t, []string{"cmd/reader", "internal/lib", "internal/lib/sub"},
		sprint.TreeGatePkgs([]string{"internal/lib/lib.go"}, pkgs, nil))
}

func TestReadGoListKeepsInModulePackages(t *testing.T) {
	t.Parallel()
	root := filepath.Join(string(filepath.Separator), "mod")
	lib := filepath.Join(root, "internal", "lib")
	tool := filepath.Join(root, "cmd", "tool")
	out := strings.Join([]string{
		strings.Join([]string{"example.com/m/internal/lib", lib, "", "example.com/m/internal/lib", ""}, "\t"),
		strings.Join([]string{"example.com/m/cmd/tool", tool, "", "", "example.com/m/internal/lib"}, "\t"),
		strings.Join([]string{"example.com/outside", filepath.Join(root, "..", "other"), "", "", ""}, "\t"),
		"",
	}, "\n")
	pkgs, err := sprint.ReadGoList(out, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	assert.Equal(t, "internal/lib", pkgs[0].Dir)
	assert.Equal(t, []string{"example.com/m/internal/lib"}, pkgs[0].Imports)
	assert.Equal(t, "cmd/tool", pkgs[1].Dir)
	assert.Equal(t, []string{"example.com/m/internal/lib"}, pkgs[1].Imports)
	_, err = sprint.ReadGoList("not a list\n", root)
	require.Error(t, err)
}
