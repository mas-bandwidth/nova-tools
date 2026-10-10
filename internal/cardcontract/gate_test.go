package cardcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateTree writes a module of files (relative path to content) under a temp dir.
func gateTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.com/m\n\ngo 1.26\n"
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return root
}

// gateFixture is the shape of nova-tools at the read: a package the diff touches, a package
// whose test reads docs/SPEC-WORKER.md, and internal/ci, the class tests the pull request's
// CI runs on every change, with a test that builds every command and a test that reads the
// doc.
var gateFixture = map[string]string{
	"internal/sprint/x.go":                "package sprint\n",
	"internal/sprint/x_test.go":           "package sprint\n",
	"internal/swarm/swarm.go":             "package swarm\n",
	"internal/swarm/doc_test.go":          "package swarm\n\nfunc TestDoc(t *testing.T) { os.ReadFile(filepath.Join(\"..\", \"..\", \"docs\", \"SPEC-WORKER.md\")) }\n",
	"internal/other/other_test.go":        "package other\n\n// SPEC-WORKER.md says so in prose, which reads nothing\n",
	"internal/ci/build_test.go":           "package ci\n\nfunc TestBuildsEveryCommand(t *testing.T) { exec.Command(\"go\", \"build\", \"./cmd/...\") }\n",
	"internal/ci/roots_class_test.go":     "package ci\n\nfunc TestRootsAreDocumented(t *testing.T) { read(\"docs/SPEC-WORKER.md\") }\n\nfunc TestRootsHelper(t *testing.T) {}\n",
	"internal/ci/ci.go":                   "package ci\n",
	"docs/SPEC-WORKER.md":                  "# swarm\n",
	"docs/OTHER.md":                       "# other\n",
	"internal/sprint/testdata/golden.txt": "golden\n",
}

// A read runs the tests of the packages its diff touches and of the packages whose tests read
// a doc it changes (nova-tools#5111), never ./internal/ci/ whole: a diff touching
// internal/sprint/x.go and docs/SPEC-WORKER.md is gated on internal/sprint and internal/swarm,
// and internal/ci, whose functional tests build every command, runs only the tests that read
// the changed doc (a 36-thread bench, 2026-10-02: 16 reads each running ./internal/ci/ kept the
// machine 85% in the kernel).
func TestAReadIsGatedOnThePackagesItsDiffTouches(t *testing.T) {
	t.Parallel()
	repo := gateTree(t, gateFixture)
	for _, tc := range []struct {
		name    string
		changed []string
		want    Gate
	}{
		{"a package and a doc", []string{"internal/sprint/x.go", "docs/SPEC-WORKER.md"},
			Gate{Packages: []string{"./internal/sprint", "./internal/swarm"}, Runs: []GateRun{{Pkg: "./internal/ci", Tests: []string{"TestRootsAreDocumented", "TestRootsHelper"}}}}},
		{"a package alone", []string{"internal/sprint/x.go"}, Gate{Packages: []string{"./internal/sprint"}}},
		{"a doc no test reads", []string{"docs/OTHER.md"}, Gate{}},
		{"a test file of internal/ci runs its own tests only", []string{"internal/ci/build_test.go"},
			Gate{Runs: []GateRun{{Pkg: "./internal/ci", Tests: []string{"TestBuildsEveryCommand"}}}}},
		{"internal/ci's own code is the pull request's CI's", []string{"internal/ci/ci.go"}, Gate{}},
		{"a package's testdata", []string{"internal/sprint/testdata/golden.txt"}, Gate{Packages: []string{"./internal/sprint"}}},
		{"a package deleted whole", []string{"internal/gone/gone.go"}, Gate{}},
		{"go.mod builds the module", []string{"go.mod", "internal/swarm/swarm.go"}, Gate{Packages: []string{"./internal/swarm"}, Module: true}},
	} {
		g, err := ReadGate(repo, tc.changed)
		require.NoError(t, err, tc.name)
		require.NotNil(t, g, tc.name)
		assert.Equal(t, tc.want, *g, tc.name)
	}
	g, err := ReadGate(t.TempDir(), []string{"x.go"})
	require.NoError(t, err)
	assert.Nil(t, g, "a checkout with no go.mod has no read gate: the card's gate stands")
}

// The read's JOB.md names its gate in every profile, as commands, in place of the card's.
func TestAReadsJobTextNamesItsGate(t *testing.T) {
	t.Parallel()
	f := Frame{Kind: "read", Card: "c1.r1.x", Attempt: 1, Repo: cardURL, ReviewBase: "dev", Branch: "sprint/c1"}
	s := Staged{Job: "/j", Repo: "/j/repo", Head: strings.Repeat("a", 40), Start: strings.Repeat("b", 40),
		Gate: &Gate{Packages: []string{"./internal/sprint", "./internal/swarm"}, Runs: []GateRun{{Pkg: "./internal/ci", Tests: []string{"TestA", "TestB"}}}}}
	for _, family := range []string{"claude", "openai", "plain"} {
		text := For(family).JobText(f, s)
		for _, want := range []string{
			"    nice -n 19 go vet ./internal/sprint ./internal/swarm\n",
			"    nice -n 19 go test -count=1 -timeout 600s ./internal/sprint ./internal/swarm\n",
			"    nice -n 19 go test -count=1 -timeout 600s -run '^(TestA|TestB)$' ./internal/ci\n",
			"the read's gate",
		} {
			assert.Contains(t, text, want, family)
		}
		assert.NotContains(t, text, "the card's gate", family)
	}
	s.Gate = &Gate{}
	assert.Contains(t, For("plain").JobText(f, s), "runs no go command", "a diff that touches no package says so")
	s.Gate = nil
	assert.Contains(t, For("plain").JobText(f, s), "the card's gate", "with no gate of its own the read runs the card's")
}

// Every job's GOCACHE is the machine's shared, warm build cache under the root
// (<root>/cache/go-build), never a cold one of its own under the job: JOB.md names it, and
// no job directory holds a gocache.
func TestJobTextNamesTheSharedBuildCache(t *testing.T) {
	t.Parallel()
	job := filepath.Join(t.TempDir(), "jobs", "c1")
	require.NoError(t, os.MkdirAll(job, 0o755))
	s := Staged{Job: job, Repo: filepath.Join(job, "repo"), Head: strings.Repeat("a", 40), GoCache: "/r/cache/go-build"}
	for _, kind := range []string{"work", "read"} {
		f := Frame{Kind: kind, Card: "c1", Attempt: 1, Repo: cardURL, BaseRef: "dev", ReviewBase: "dev", Branch: "sprint/c1"}
		for _, family := range []string{"claude", "openai", "plain"} {
			require.NoError(t, Install(For(family), f, s, filepath.Join(t.TempDir(), "shim")))
			text, err := os.ReadFile(filepath.Join(job, JobName))
			require.NoError(t, err)
			assert.Contains(t, string(text), "GOCACHE is /r/cache/go-build", kind+" "+family)
			assert.Contains(t, string(text), `"The cache is safe for concurrent invocations of the go command."`, kind+" "+family)
			assert.NotContains(t, string(text), "/gocache", kind+" "+family)
			_, err = os.Stat(filepath.Join(job, "gocache"))
			assert.True(t, os.IsNotExist(err), "no job holds a gocache of its own")
		}
	}
}
