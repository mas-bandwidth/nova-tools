package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The base-check parser layer of lintbase.go, tested without a repository: the DONE-WHEN
// readers, the runner-target readers and the small header helpers are pure parsing over
// the card's own words, so the unit tier covers them and nothing here touches git, the
// network or a store. The git-evidence half of the file (baseGit, pathsMissingAt, the
// go.mod lookup in goTestPathspecs, the tree lookup in pytestPathspecs, the grep in
// testDefinedAt) drives the repository through gitrun with no injection seam and runs
// only in the functional tier (lintbase_functional_test.go).

func TestLintbaseCoverDoneWhenTests(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		v    string
		want []doneTest
		why  string
	}{
		{"go-run", "go test ./pkg/swarm -run TestLintbaseCover",
			[]doneTest{{runner: "go", name: "TestLintbaseCover", scope: []string{"./pkg/swarm"}}}, ""},
		{"go-run-alternation", "go test ./x -run 'TestA|TestB'",
			[]doneTest{{runner: "go", name: "TestA", scope: []string{"./x"}},
				{runner: "go", name: "TestB", scope: []string{"./x"}}}, ""},
		{"go-run-anchored", "go test -run=^TestFoo$ ./y",
			[]doneTest{{runner: "go", name: "TestFoo", scope: []string{"./y"}}}, ""},
		{"go-test-run-subtest", "go test ./x -test.run TestA/sub",
			[]doneTest{{runner: "go", name: "TestA", scope: []string{"./x"}}}, ""},
		{"go-run-import-path", "go test github.com/mas-bandwidth/nova-tools/pkg/swarm -run TestBar",
			[]doneTest{{runner: "go", name: "TestBar", scope: []string{"github.com/mas-bandwidth/nova-tools/pkg/swarm"}}}, ""},
		{"go-run-regex-refused", "go test ./pkg -run TestDecide.*", nil,
			"runs `-run TestDecide.*`, a pattern that is no literal test name"},
		{"go-no-run-refused", "go test ./...", nil,
			"runs `go test` with no `-run <TestName>`, so no test is named that could be red"},
		{"pytest-node", "pytest tests/test_x.py::test_foo",
			[]doneTest{{runner: "pytest", name: "test_foo", scope: []string{"tests/test_x.py"}}}, ""},
		{"pytest-k", "pytest tests -k test_bar",
			[]doneTest{{runner: "pytest", name: "test_bar", scope: []string{"tests"}}}, ""},
		{"pytest-node-not-a-test-name", "pytest t/test.py::TestClass", nil,
			"names no test runner and no test"},
		{"cargo", "cargo test --release test_parse",
			[]doneTest{{runner: "cargo", name: "test_parse"}}, ""},
		{"cargo-path-name", "cargo test mods::test_thing",
			[]doneTest{{runner: "cargo", name: "test_thing"}}, ""},
		{"go-and-pytest", "go test ./x -run TestA && pytest tests -k test_b",
			[]doneTest{{runner: "go", name: "TestA", scope: []string{"./x"}},
				{runner: "pytest", name: "test_b", scope: []string{"tests"}}}, ""},
		{"english-outcome", "applied cleanly", nil,
			"names no test runner and no test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := doneWhenTests(tc.v)
			if tc.want == nil {
				assert.Empty(t, got, "no test is named")
				assert.Equal(t, tc.why, why)
				return
			}
			assert.Equal(t, tc.want, got)
			assert.Equal(t, "", why)
		})
	}
}

func TestLintbaseCoverGoTestTargets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		seg  string
		want []string
	}{
		{"the-card-own-command", "go test -count=1 -timeout 600s ./pkg/swarm/ -run TestLintbaseCover",
			[]string{"./pkg/swarm/"}},
		{"dot-and-recursive", "go test . ./x/... ...", []string{".", "./x/...", "..."}},
		{"parent-and-import-path", "go test ../x github.com/org/repo/x", []string{"../x", "github.com/org/repo/x"}},
		{"quoted", `go test "./y" -run TestA`, []string{"./y"}},
		{"flag-values-left-out", "go test -short -race decide some/dir", nil},
		{"no-targets", "go test -run TestA", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, goTestTargets(tc.seg))
		})
	}
}

func TestLintbaseCoverPytestTakesValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		f    string
		want bool
	}{
		{"-k", true},
		{"-m", true},
		{"-W", true},
		{"--rootdir", true},
		{"--tb", true},
		{"--maxfail", true},
		{"--cov", false},
		{"--cov=term", false},
		{"--maxfail=5", false},
		{"-ktest_bar", false},
		{"--nogui", false},
		{"tests", false},
	} {
		t.Run(tc.f, func(t *testing.T) {
			assert.Equal(t, tc.want, pytestTakesValue(tc.f), "%q", tc.f)
		})
	}
}

func TestLintbaseCoverPytestTargets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		seg  string
		want []string
	}{
		{"file-k-and-rootdir", "pytest tests/test_x.py -k test_y --rootdir build tests",
			[]string{"tests/test_x.py", "tests"}},
		{"joined-flag-value-skipped", "pytest --tb=short tests", []string{"tests"}},
		{"short-option-value-skipped", "pytest -W error tests", []string{"tests"}},
		{"node-id-is-no-target", "pytest tests/test_x.py::test_foo", nil},
		{"runner-alone", "pytest", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pytestTargets(tc.seg))
		})
	}
}

func TestLintbaseCoverGoTestPathspecs(t *testing.T) {
	t.Parallel()

	// The unit tier names no packages: a scope makes the lookup read go.mod at the sha
	// through git, so only the whole-repository fallback runs here, and it answers for
	// the card shape `go test -run TestX` with no package target.
	require.Equal(t, []string{"*_test.go"}, goTestPathspecs(t.TempDir(), "0000000000000000000000000000000000000000", nil))
	assert.Equal(t, []string{"*_test.go"}, goTestPathspecs(t.TempDir(), "0000000000000000000000000000000000000000", []string{}))
}

func TestLintbaseCoverPytestPathspecs(t *testing.T) {
	t.Parallel()

	// A `.py` target is its own pathspec, read as written; a directory asks the tree
	// through git and runs only in the functional tier. With no target at all the
	// search is the whole repository.
	require.Equal(t, []string{"*.py"}, pytestPathspecs(t.TempDir(), "0000000000000000000000000000000000000000", nil))
	assert.Equal(t, []string{":(literal)tests/test_x.py"},
		pytestPathspecs(t.TempDir(), "0000000000000000000000000000000000000000", []string{"tests/test_x.py"}))
	assert.Equal(t, []string{":(literal)pkg/a.py", ":(literal)b.py"},
		pytestPathspecs(t.TempDir(), "0000000000000000000000000000000000000000", []string{"./pkg/a.py", "b.py"}))
}

func TestLintbaseCoverGlobSpec(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		dir  string
		rec  bool
		base string
		want string
	}{
		{name: "root", dir: "", rec: false, base: "*_test.go", want: ":(glob)*_test.go"},
		{name: "dir", dir: "internal/x", rec: false, base: "*_test.go", want: ":(glob)internal/x/*_test.go"},
		{name: "root-recursive", dir: "", rec: true, base: "*.py", want: ":(glob)**/*.py"},
		{name: "dir-recursive", dir: "tests", rec: true, base: "*.py", want: ":(glob)tests/**/*.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, globSpec(tc.dir, tc.rec, tc.base))
		})
	}
}

func TestLintbaseCoverOneLineCap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		v    string
		n    int
		want string
	}{
		{"uncapped", "short", 120, "short"},
		{"exact", "abc", 3, "abc"},
		{"capped", "abcdefg", 3, "abc..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, oneLineCap(tc.v, tc.n))
		})
	}
}

func TestLintbaseCoverContractSha(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"contract-line", "RESULT cover-internal-swarm-lintbase sha=bd7949b97aec tier: flash", "bd7949b97aec"},
		{"full-sha", "sha=0000000000000000000000000000000000000000", "0000000000000000000000000000000000000000"},
		{"not-hex", "RESULT x sha=xyz12345 -- words", ""},
		{"too-long", "sha=abc12345678901234567890123456789012345678", ""},
		{"no-sha-field", "RESULT no hash here", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, contractSha(tc.line))
		})
	}
}

func TestLintbaseCoverShort12(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "bd7949b97aec", short12("bd7949b97aec11f39ee7406cd954efcb9c7c99d9"))
	assert.Equal(t, "abcdef012345", short12("abcdef012345"))
	assert.Equal(t, "abc", short12("abc"))
}

func TestLintbaseCoverSplitPathsValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		v    string
		want []string
	}{
		{"three-entries", "a.go, b.go ,, c.go", []string{"a.go", "b.go", "c.go"}},
		{"single", "pkg/swarm/lintbase.go", []string{"pkg/swarm/lintbase.go"}},
		{"empty", "", nil},
		{"blank-separators", " , , ", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, splitPathsValue(tc.v))
		})
	}
}

func TestLintbaseCoverNewTestFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		p    string
		want bool
	}{
		{"pkg/swarm/lintbase_cover_test.go", true},
		{"tests/test_run.py", true},
		{"a/x_test.go", true},
		{"a/*_test.go", false},
		{"a/b?.go", false},
		{"a/x[0].go", false},
		{"main.go", false},
	} {
		t.Run(tc.p, func(t *testing.T) {
			assert.Equal(t, tc.want, newTestFile(tc.p), "%q", tc.p)
		})
	}
}
