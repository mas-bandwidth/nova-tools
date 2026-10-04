package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withEnv replaces a variable the environment holds and adds one it does not, the rest
// in place; gateRuns is the build and the vet, then the tree tests the clone holds when
// asked; treeTested is a document or a test file; gateWhy is one line of the run, how it
// ended and its output.
func TestTreeGateWords(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"GOFLAGS=-mod=readonly", "PATH=/bin", "NOVA_CI_UPDATE=1"},
		withEnv([]string{"GOFLAGS=-mod=mod", "PATH=/bin"}, "GOFLAGS=-mod=readonly", "NOVA_CI_UPDATE=1"))
	assert.Equal(t, []string{"GOFLAGS=-mod=readonly", "PATH=/bin"},
		withEnv([]string{"GOFLAGS=-mod=vendor", "PATH=/bin", "GOFLAGS=-mod=mod"}, "GOFLAGS=-mod=readonly"))
	assert.Equal(t, []string{"GOFLAGS=-mod=readonly"}, withEnv(nil, "GOFLAGS=-mod=readonly"))
	assert.Equal(t, "GOFLAGS=-mod=readonly", readonlyGoFlags(nil))
	assert.Equal(t, "GOFLAGS=-tags=custom -mod=readonly", readonlyGoFlags([]string{"GOFLAGS=-tags=custom -mod=mod"}))
	assert.Equal(t, "GOFLAGS=-tags=custom -count=1 -mod=readonly",
		readonlyGoFlags([]string{"GOFLAGS=-tags=custom", "PATH=/bin", "GOFLAGS=-count=1 -mod=vendor"}))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}, gateRuns(false, []string{"internal/docs"}))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}, gateRuns(true, nil))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, {"go", "test", "./internal/docs/", "./internal/ci/"}},
		gateRuns(true, []string{"internal/docs", "internal/ci"}))
	for p, want := range map[string]bool{"docs/CLI.md": true, "a/b_test.go": true, "a/b.go": false, "a/test.go": false, "README": false, "x.mdx": false} {
		assert.Equal(t, want, treeTested(p), p)
	}
	assert.Equal(t, "go vet ./...: exit status 1: # example.com/m | ./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string",
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"), "# example.com/m\n./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string\n\n"))
	long := gateWhy([]string{"go", "build", "./..."}, errors.New("exit status 2"), strings.Repeat("x", 2000))
	assert.Less(t, len(long), 1600, "the output is capped")
}

// goModule is the tree gate's rig: a Go module whose main package builds and vets, with
// a package that tests the tree (internal/docs) whose one test fails when NOTES.md says
// BAD or the module holds forbidden.go.
var goModule = map[string]string{
	"go.mod":                "module example.com/m\n\ngo 1.21\n",
	"main.go":               "package main\n\nfunc main() {}\n",
	"NOTES.md":              "fine\n",
	"internal/docs/docs.go": "package docs\n",
	"internal/docs/docs_test.go": `package docs

import (
	"os"
	"strings"
	"testing"
)

func TestTree(t *testing.T) {
	b, err := os.ReadFile("../../NOTES.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "BAD") {
		t.Fatal("NOTES.md says BAD")
	}
	if _, err := os.Stat("../../forbidden.go"); err == nil {
		t.Fatal("forbidden.go is in the module")
	}
}
`,
}

const (
	vetRed   = "package main\n\nimport \"fmt\"\n\nfunc bad() { fmt.Printf(\"%d\", \"s\") }\n"
	buildRed = "package main\n\nfunc broken( {\n"
)

// Every tip of the batch branch passes the tree gate: a head whose merged tree fails the
// build, the vet, or, when it changes a document or a test file, the tree tests, is
// taken off the batch branch and ends the batch as a head that does not merge does, the
// run and its output the finding; the heads before it land. A head that changes no
// document or test file is not tested, only built and vetted. A base whose tip is red
// refuses the batch before any merge, blaming no card.
func TestLandGatesEveryTipOfTheBatchBranch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		base          map[string]string // changes to the module on the base, none for none
		first, second map[string]string
		why           string // the finding on s1-2; "" lands both
		base_red      string // the base's finding, which refuses the batch; "" for a green base
	}{
		{"a vet failure", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"bad.go": vetRed},
			`fails the tree gate: go vet ./...: exit status 1: bad.go:5:26: fmt.Printf format %d has arg "s" of wrong type string`, ""},
		{"a build failure", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"bad.go": buildRed},
			"fails the tree gate: go build ./...: exit status 1: # example.com/m | ./bad.go:3:14: syntax error:", ""},
		{"a document the tree tests refuse", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"NOTES.md": "BAD\n"},
			"fails the tree gate: go test ./internal/docs/: exit status 1: ", ""},
		{"a Go file the tree tests would refuse is not tested", nil, map[string]string{"forbidden.go": "package main\n\nfunc forbidden() {}\n"}, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"},
			"", ""},
		{"a red base refuses the batch", map[string]string{"bad.go": vetRed}, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"NOTES.md": "still fine\n"},
			"", "reason=the base main fails the tree gate at its tip, so no head is merged onto it; fix the base, then run land again: go vet ./...: exit status 1: bad.go:5:26: fmt.Printf format %d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			r.files("the module", goModule)
			if tc.base != nil {
				r.files("the base's change", tc.base)
			}
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", tc.first), "s1-2": r.card("s1-2", tc.second)}
			r.queued(heads, "s1-1", "s1-2")
			before := r.git(r.remote, "rev-parse", "main")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			switch {
			case tc.base_red != "":
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2 ")
				assert.Contains(t, errs, tc.base_red)
				assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "nothing was pushed")
				assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"), "no card is blamed")
			case tc.why != "":
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 "+tc.why)
				assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "the module", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Equal(t, "", r.git(r.remote, "ls-tree", "main", "bad.go"), "the red head is off the batch branch")
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the clone is clean")
			default:
				assert.Equal(t, 0, code, out+errs)
				assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
				assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the module", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			}
			r.clean()
		})
	}
}

// The base gate result is cached by base commit SHA: once gated, the same commit is
// not re-gated on subsequent calls even if the tree on disk changes.
func TestTreeGateBaseCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for f, content := range goModule {
		p := filepath.Join(dir, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	l := &lander{baseGateCache: map[string]string{}}
	assert.Equal(t, "", l.treeGateBase(context.Background(), dir, "base-1"))
	assert.Equal(t, "", l.baseGateCache["base-1"])

	// Corrupt main.go: cache hit for base-1 still reports green without running.
	mainGo := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(mainGo, []byte(buildRed), 0o600))
	assert.Equal(t, "", l.treeGateBase(context.Background(), dir, "base-1"))

	// A different base SHA (base-2) gates and caches the failure.
	why := l.treeGateBase(context.Background(), dir, "base-2")
	assert.Contains(t, why, "syntax error")
	assert.Equal(t, why, l.baseGateCache["base-2"])

	// Restore main.go: cache hit for base-2 still reports the cached failure.
	require.NoError(t, os.WriteFile(mainGo, []byte(goModule["main.go"]), 0o600))
	assert.Equal(t, why, l.treeGateBase(context.Background(), dir, "base-2"))
}
