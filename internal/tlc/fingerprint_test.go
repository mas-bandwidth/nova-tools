package tlc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every non-test Go file of the package says whether it decides how a result is
// produced and read (ResultFiles: in every fingerprint) or not
// (BookkeepingFiles: in none), in exactly one of the two lists, so a file added
// later cannot be left out of the rule or counted twice.
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
	for _, n := range ResultFiles {
		listed[n]++
	}
	for _, n := range BookkeepingFiles {
		listed[n]++
	}
	for n, times := range listed {
		assert.Equal(t, 1, times, "%s is in ResultFiles and BookkeepingFiles %d times, want exactly one list once", n, times)
	}
	for _, n := range dir {
		assert.NotEqual(t, 0, listed[n], "%s is in neither ResultFiles nor BookkeepingFiles: say in fingerprint.go whether it decides how a result is produced and read", n)
		delete(listed, n)
	}
	for n := range listed {
		assert.Failf(t, "", "%s is listed in fingerprint.go and is not a non-test file of the package", n)
	}
}

// The binary carries the bytes of the files CheckRunner holds to the checkout
// (the result files and the input-list files), and no others; the fingerprint
// takes only the result files of them.
func TestEmbeddedSourcesAreTheCheckedFiles(t *testing.T) {
	t.Parallel()
	files, err := CheckedSources()
	require.NoError(t, err)
	{
		runner, err := RunnerFiles()
		require.NoError(t, err, "the fingerprint takes %d runner files, %d are result files (%v)", len(runner), len(ResultFiles), err)
		require.Equal(t, len(ResultFiles), len(runner), "the fingerprint takes %d runner files, %d are result files (%v)", len(runner), len(ResultFiles), err)
	}
	for _, n := range InputListFiles {
		if assert.True(t, slices.Contains(BookkeepingFiles, n), "%s is an input-list file: it is bookkeeping and no result file", n) {
			assert.False(t, slices.Contains(ResultFiles, n), "%s is an input-list file: it is bookkeeping and no result file", n)
		}
	}
	var got, want []string
	for name, raw := range files {
		got = append(got, name)
		disk, err := os.ReadFile(filepath.Join(".", filepath.Base(name)))
		if assert.NoError(t, err, "%s: the embedded bytes are not the file's", name) {
			assert.Equal(t, string(raw), string(disk), "%s: the embedded bytes are not the file's", name)
		}
	}
	for _, n := range CheckedFiles() {
		want = append(want, RunnerDir+"/"+n)
	}
	sort.Strings(got)
	sort.Strings(want)
	require.Equal(t, strings.Join(want, ","), strings.Join(got, ","), "embedded %v, CheckedFiles names %v: fix the go:embed line", got, want)
	entries, _ := sources.ReadDir(".")
	require.Len(t, entries, len(CheckedFiles()), "the embedded directory holds %d files, CheckedFiles %d", len(entries), len(CheckedFiles()))
}

func TestCheckRunnerComparesTheEmbeddedCheckedFilesWithTheRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, CheckRunner(root), "a root with no runner sources")
	require.NoError(t, CheckRunner(filepath.Join("..", "..")), "this repository")
	dir := filepath.Join(root, RunnerDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	built, _ := CheckedSources()
	for path, raw := range built {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), raw, 0o644))
	}
	require.NoError(t, CheckRunner(root), "the same files")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "suite.go"), []byte("other\n"), 0o644))
	err := CheckRunner(root)
	require.ErrorContains(t, err, "internal/tlc/suite.go differ", "an edited result file: %v", err)
	require.ErrorContains(t, err, "build tlacheck from this tree", "an edited result file: %v", err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "suite.go"), built[RunnerDir+"/suite.go"], 0o644))
	// inputs.go computes the list of files a case reads: a binary built from
	// another one computes other inputs than the checkout does.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inputs.go"), []byte("another input list\n"), 0o644))
	err = CheckRunner(root)
	require.ErrorContains(t, err, "internal/tlc/inputs.go differ", "an edited input-list file: %v", err)
	require.NotContains(t, err.Error(), "suite.go", "an edited input-list file: %v", err)
	// Any other bookkeeping file decides nothing about the inputs.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inputs.go"), built[RunnerDir+"/inputs.go"], 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "records.go"), []byte("another bookkeeping file\n"), 0o644))
	require.NoError(t, CheckRunner(root), "an edited bookkeeping file")
}

// The file that turns a row of CASES.tsv into a Case (its columns, its defaults,
// what it refuses) decides what a run is judged by, so it is a result file: an
// edit to it stales every record. The test finds the declarations by parsing the
// package, so moving them to a bookkeeping file fails it.
func TestTheFileThatReadsAPlanRowIsAResultFile(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	require.NoError(t, err)
	where := map[string]string{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.Name == "ParseCases" {
						where["ParseCases"] = name
					}
					if d.Recv != nil && d.Name.Name == "check" {
						where["Case.check"] = name
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == "Case" {
							where["Case"] = name
						}
					}
				}
			}
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
