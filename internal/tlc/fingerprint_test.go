package tlc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryRunnerFileIsClassified(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var dir []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			dir = append(dir, n)
		}
	}
	listed := map[string]int{}
	for _, n := range append(slices.Clone(ResultFiles), BookkeepingFiles...) {
		listed[n]++
	}
	for n, times := range listed {
		assert.Equal(t, 1, times, "%s is in ResultFiles and BookkeepingFiles %d times, want exactly one list once", n, times)
	}
	for _, n := range dir {
		assert.Contains(t, listed, n, "%s is in neither ResultFiles nor BookkeepingFiles: say in fingerprint.go whether it decides how a result is produced and read", n)
		delete(listed, n)
	}
	for n := range listed {
		assert.Failf(t, "", "%s is listed in fingerprint.go and is not a non-test file of the package", n)
	}
}

func TestEmbeddedSourcesAreTheCheckedFiles(t *testing.T) {
	t.Parallel()
	files, err := CheckedSources()
	require.NoError(t, err)
	runner, err := RunnerFiles()
	require.NoError(t, err, "the fingerprint takes %d runner files, %d are result files (%v)", len(runner), len(ResultFiles), err)
	require.Equal(t, len(ResultFiles), len(runner), "the fingerprint takes %d runner files, %d are result files (%v)", len(runner), len(ResultFiles), err)
	for _, n := range InputListFiles {
		assert.True(t, slices.Contains(BookkeepingFiles, n), "%s is an input-list file: it is bookkeeping and no result file", n)
		assert.False(t, slices.Contains(ResultFiles, n), "%s is an input-list file: it is bookkeeping and no result file", n)
	}
	var got, want []string
	for name, raw := range files {
		got = append(got, name)
		disk := testkit.ReadFile(t, filepath.Join(".", filepath.Base(name)))
		assert.Equal(t, string(raw), disk, "%s: the embedded bytes are not the file's", name)
	}
	for _, n := range CheckedFiles() {
		want = append(want, RunnerDir+"/"+n)
	}
	slices.Sort(got)
	slices.Sort(want)
	require.Equal(t, want, got, "embedded %v, CheckedFiles names %v: fix the go:embed line", got, want)
	entries, err := sources.ReadDir(".")
	require.NoError(t, err)
	require.Len(t, entries, len(CheckedFiles()), "the embedded directory holds %d files, CheckedFiles %d", len(entries), len(CheckedFiles()))
}

func TestCheckRunnerComparesTheEmbeddedCheckedFilesWithTheRoot(t *testing.T) {
	t.Parallel()
	require.NoError(t, CheckRunner(t.TempDir()), "a root with no runner sources")
	require.NoError(t, CheckRunner(filepath.Join("..", "..")), "this repository")
	built, err := CheckedSources()
	require.NoError(t, err)
	for _, tc := range []struct {
		name, file, body, not string
		want                  []string
	}{
		{"the same files", "", "", "", nil},
		{"an edited result file", "suite.go", "other\n", "", []string{"internal/tlc/suite.go differ", "build tlacheck from this tree"}},
		{"an edited input-list file", "inputs.go", "another input list\n", "suite.go", []string{"internal/tlc/inputs.go differ"}},
		{"an edited bookkeeping file", "records.go", "another bookkeeping file\n", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, raw := range built {
				testkit.WriteFile(t, filepath.Join(root, path), string(raw))
			}
			if tc.file != "" {
				testkit.WriteFile(t, filepath.Join(root, RunnerDir, tc.file), tc.body)
			}
			err := CheckRunner(root)
			if tc.want == nil {
				assert.NoError(t, err, tc.name)
				return
			}
			for _, want := range tc.want {
				assert.ErrorContains(t, err, want, "%s: %v", tc.name, err)
			}
			if tc.not != "" {
				assert.NotContains(t, err.Error(), tc.not, "%s: %v", tc.name, err)
			}
		})
	}
}

// ParseCases, Case and Case.check turn a plan row into the case a run is judged
// by, so the file that declares them is a result file. Moving them fails this.
func TestTheFileThatReadsAPlanRowIsAResultFile(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	require.NoError(t, err)
	where := map[string]string{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch d := n.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.Name == "ParseCases" {
						where["ParseCases"] = name
					}
					if d.Recv != nil && d.Name.Name == "check" {
						where["Case.check"] = name
					}
				case *ast.TypeSpec:
					if d.Name.Name == "Case" {
						where["Case"] = name
					}
				}
				return true
			})
		}
	}
	for _, what := range []string{"ParseCases", "Case", "Case.check"} {
		file, ok := where[what]
		if !assert.True(t, ok, "%s is not declared in the package", what) {
			continue
		}
		assert.True(t, slices.Contains(ResultFiles, file), "%s is declared in %s, which is not a result file: it reads a plan row into the case a run is judged by", what, file)
	}
}
