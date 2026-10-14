package ci

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// TestTestifyLedgerMeasuresEachShape pins the detector of TestTestsUseTestify on a
// source that holds one site of every shape the rule names, and the clean forms beside
// them that it must not count.
func TestTestifyLedgerMeasuresEachShape(t *testing.T) {
	t.Parallel()

	const src = `package p

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestBare(t *testing.T) {
	if err := f(); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %v", got)
	}
	if !strings.Contains(s, "x") {
		t.Errorf("no x")
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("differ")
	}
	if len(xs) != 3 {
		t.Fatalf("len")
	}
	if ok && other {
		t.Fail()
	}
	t.Setenv("A", "b")
	t.Chdir("/")
	os.Chdir("/")
	t.Run("row", func(t *testing.T) {
		t.Parallel()
		if x == nil {
			t.Fatal("nil")
		}
	})
}

func TestClean(t *testing.T) {
	t.Parallel()
	if err := f(); err != nil {
		return
	}
	if x {
		log.Println("not a test call")
	}
	assert.Equal(t, a, b)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p/p_test.go", src, 0)
	require.NoError(t, err)
	sites := testifySitesInFile(fset, file, "p/p_test.go")

	byKind, shapes := map[string]int{}, map[string]int{}
	for _, s := range sites {
		assert.Equal(t, "p", s.Pkg)
		byKind[s.Kind]++
		if s.Kind == "assert" {
			shapes[s.Shape]++
		}
	}
	assert.Equal(t, map[string]int{"assert": 7, "env": 3}, byKind)
	assert.Equal(t, map[string]int{"err": 1, "compare": 1, "contains": 1, "deepequal": 1, "len": 1, "compound": 1, "nil": 1}, shapes)
}

// TestTestifyLedgerOnlyFalls pins the ledger's judgement: a count over its row, a site
// with no row, a row above the tree and a row for nothing are each refused, and an
// update lowers and drops but never raises or adds.
func TestTestifyLedgerOnlyFalls(t *testing.T) {
	t.Parallel()

	site := testifySite{Pkg: "a", Kind: "assert", Where: "a/x_test.go:1", Shape: "err"}
	byKey := map[string][]testifySite{"a:assert": {site}}
	tests := []struct {
		name     string
		counts   map[string]int
		rows     map[string]int
		update   bool
		problems int
		lowered  map[string]int
	}{
		{"equal", map[string]int{"a:assert": 2}, map[string]int{"a:assert": 2}, false, 0, map[string]int{}},
		{"over", map[string]int{"a:assert": 3}, map[string]int{"a:assert": 2}, false, 1, map[string]int{}},
		{"over under update", map[string]int{"a:assert": 3}, map[string]int{"a:assert": 2}, true, 1, map[string]int{}},
		{"unlisted", map[string]int{"a:assert": 1}, map[string]int{}, true, 1, map[string]int{}},
		{"below", map[string]int{"a:assert": 1}, map[string]int{"a:assert": 2}, false, 1, map[string]int{"a:assert": 1}},
		{"below under update", map[string]int{"a:assert": 1}, map[string]int{"a:assert": 2}, true, 0, map[string]int{"a:assert": 1}},
		{"row for nothing", map[string]int{}, map[string]int{"a:env": 1}, false, 1, map[string]int{"a:env": 0}},
		{"row for nothing under update", map[string]int{}, map[string]int{"a:env": 1}, true, 0, map[string]int{"a:env": 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems, _, lowered := testifyJudge(tc.counts, byKey, tc.rows, tc.update)
			assert.Len(t, problems, tc.problems)
			assert.Equal(t, tc.lowered, lowered)
		})
	}

	makeShards := func(t *testing.T) (string, string, string) {
		t.Helper()
		dir := t.TempDir()
		cmd := filepath.Join(dir, "cmd", "nova-ci.txt")
		internal := filepath.Join(dir, "internal", "foo.txt")
		for _, file := range []string{cmd, internal} {
			require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		}
		require.NoError(t, os.WriteFile(cmd, []byte("# cmd reason\n# ceiling: 2\ncmd/nova-ci:assert 3 bare checks\ncmd/nova-ci:env 1 inject config\n"), 0600))
		require.NoError(t, os.WriteFile(internal, []byte("# internal reason\n# ceiling: 1\ninternal/foo:assert 2 bare checks\n"), 0600))
		return dir, cmd, internal
	}
	load := func(t *testing.T, dir string) *allowlist.Packages {
		t.Helper()
		ledger, err := allowlist.LoadPackages(dir, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
		require.NoError(t, err)
		return ledger
	}
	t.Run("lower and drop preserve comments and reasons", func(t *testing.T) {
		t.Parallel()
		dir, cmd, internal := makeShards(t)
		unchanged, err := os.ReadFile(internal)
		require.NoError(t, err)
		reporter := &generalityMessageReporter{}
		result := allowlist.CheckPackagesCountedMode(reporter, load(t, dir), map[string]int{
			"cmd/nova-ci:assert":  2,
			"internal/foo:assert": 2,
		}, true)
		assert.True(t, result.Updated)
		assert.Contains(t, strings.Join(reporter.messages, "\n"), allowlist.UpdatedRerun)
		got, err := os.ReadFile(cmd)
		require.NoError(t, err)
		assert.Equal(t, "# cmd reason\n# ceiling: 1\ncmd/nova-ci:assert 2 bare checks\n", string(got))
		got, err = os.ReadFile(internal)
		require.NoError(t, err)
		assert.Equal(t, string(unchanged), string(got))
	})
	t.Run("growth refuses every shard write", func(t *testing.T) {
		t.Parallel()
		dir, cmd, internal := makeShards(t)
		beforeCmd, err := os.ReadFile(cmd)
		require.NoError(t, err)
		beforeInternal, err := os.ReadFile(internal)
		require.NoError(t, err)
		reporter := &generalityMessageReporter{}
		result := allowlist.CheckPackagesCountedMode(reporter, load(t, dir), map[string]int{
			"cmd/nova-ci:assert":  2,
			"cmd/nova-ci:env":     1,
			"internal/foo:assert": 3,
		}, true)
		assert.False(t, result.Updated)
		assert.Len(t, result.Over, 1)
		assert.Contains(t, strings.Join(reporter.messages, "\n"), "refuses to raise a count")
		got, err := os.ReadFile(cmd)
		require.NoError(t, err)
		assert.Equal(t, string(beforeCmd), string(got))
		got, err = os.ReadFile(internal)
		require.NoError(t, err)
		assert.Equal(t, string(beforeInternal), string(got))
	})
}
