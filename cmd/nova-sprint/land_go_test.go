package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	gofmt := []string{"gofmt", "-l", "."}
	lint := []string{"go", "test", "-tags", "functional", "-count=1", "-run", "^(TestStaticcheckFindings|TestUncheckedErrors)$", "./internal/ci/"}
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, gofmt}, gateRuns(false, []string{"internal/docs"}))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, gofmt}, gateRuns(true, nil))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, gofmt, {"go", "test", "./internal/docs/"}},
		gateRuns(true, []string{"internal/docs"}), "no internal/ci, no linter class tests")
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, gofmt, {"go", "test", "./internal/docs/", "./internal/ci/"}, lint},
		gateRuns(true, []string{"internal/docs", "internal/ci"}))
	assert.Equal(t, []string{"sh", "-c", gofmtScript}, gateArgv(gofmt), "gofmt runs as the toolchain's own, any output red")
	assert.Equal(t, lint, gateArgv(lint))
	for p, want := range map[string]bool{
		"docs/CLI.md":            true,
		"a/b_test.go":            true,
		"a/b.go":                 true,
		"a/test.go":              true,
		"internal/ci/testdata/x": true,
		"testdata/cases.txt":     true,
		"README":                 false,
		"x.mdx":                  false,
		"notes.txt":              false,
	} {
		assert.Equal(t, want, treeTested(p), p)
	}
	assert.Equal(t, "vet, ./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string; go vet ./...: exit status 1: # example.com/m | ./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string",
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"), "# example.com/m\n./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string\n\n"))
	assert.Equal(t, "gofmt, internal/x/y.go is not gofmt-clean; gofmt -l .: exit status 1: internal/x/y.go | z.go",
		gateWhy(gofmt, errors.New("exit status 1"), "internal/x/y.go\nz.go\n"))
	lintOut := "--- FAIL: TestUncheckedErrors (9.10s)\n    errcheck_class_test.go:78: internal/bus/timeout_test.go:54: defer server.Close(): internal/bus:unchecked (no row in testdata/errcheck)\nFAIL\n"
	assert.True(t, strings.HasPrefix(gateWhy(lint, errors.New("exit status 1"), lintOut),
		"errcheck, internal/bus/timeout_test.go:54: defer server.Close(): internal/bus:unchecked (no row in testdata/errcheck); go test -tags functional"))
	assert.True(t, strings.HasPrefix(gateWhy(lint, errors.New("exit status 1"), strings.ReplaceAll(lintOut, "TestUncheckedErrors", "TestStaticcheckFindings")), "staticcheck, "))
	assert.True(t, strings.HasPrefix(gateWhy([]string{"go", "test", "./internal/docs/"}, errors.New("exit status 1"), "--- FAIL: TestTree (0.00s)\n    docs_test.go:9: NOTES.md says BAD\nFAIL\n"),
		"tree tests, NOTES.md says BAD; go test ./internal/docs/: "))
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
	if _, err := os.Stat("../../forbidden.txt"); err == nil {
		t.Fatal("forbidden.txt is in the module")
	}
}
`,
}

// lintModule stands in for the linter class tests on the base: internal/ci's functional
// TestUncheckedErrors fails, as errcheck's ledger does, while the module holds
// unchecked.go, an unchecked Close.
var lintModule = map[string]string{
	"internal/ci/ci.go": "package ci\n",
	"internal/ci/errcheck_class_test.go": `//go:build functional

package ci

import (
	"os"
	"testing"
)

func TestUncheckedErrors(t *testing.T) {
	if _, err := os.Stat("../../unchecked.go"); err == nil {
		t.Error("unchecked.go:6: f.Close(): example.com/m:unchecked (no row in testdata/errcheck)")
	}
}
`,
}

const (
	uglyGo      = "package main\n\nfunc  ugly()  {}\n"
	uncheckedGo = "package main\n\nimport \"os\"\n\nfunc unchecked(f *os.File) {\n\tf.Close()\n}\n"
	vetRed      = "package main\n\nimport \"fmt\"\n\nfunc bad() { fmt.Printf(\"%d\", \"s\") }\n"
	buildRed    = "package main\n\nfunc broken( {\n"
)

// Every tip of the batch branch passes the tree gate: a head whose merged tree fails the
// build, the vet, or, when it changes a document, a Go file or testdata, the tree tests,
// is taken off the batch branch and ends the batch as a head that does not merge does, the
// run and its output the finding; the heads before it land. A head that changes none of
// those is not tested, only built and vetted. A base whose tip is red refuses the batch
// before any merge, blaming no card.
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
			`fails the tree gate: vet, bad.go:5:26: fmt.Printf format %d has arg "s" of wrong type string; go vet ./...: exit status 1: bad.go:5:26: fmt.Printf format %d has arg "s" of wrong type string`, ""},
		{"a build failure", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"bad.go": buildRed},
			"fails the tree gate: build, ./bad.go:3:14: syntax error: unexpected {", ""},
		{"a document the tree tests refuse", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"NOTES.md": "BAD\n"},
			"fails the tree gate: tree tests, NOTES.md says BAD; go test ./internal/docs/: exit status 1: ", ""},
		{"a Go file the tree tests refuse", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"forbidden.go": "package main\n\nfunc forbidden() {}\n"},
			"fails the tree gate: tree tests, forbidden.go is in the module; go test ./internal/docs/: exit status 1: ", ""},
		{"an unformatted Go file", nil, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"ugly.go": uglyGo},
			"fails the tree gate: gofmt, ugly.go is not gofmt-clean; gofmt -l .: exit status 1: ugly.go", ""},
		{"an unchecked error the linter class tests refuse", lintModule, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"unchecked.go": uncheckedGo},
			"fails the tree gate: errcheck, unchecked.go:6: f.Close(): example.com/m:unchecked (no row in testdata/errcheck); go test -tags functional -count=1 -run ^(TestStaticcheckFindings|TestUncheckedErrors)$ ./internal/ci/: exit status 1: ", ""},
		{"a plain file the tree tests would refuse is not tested", nil, map[string]string{"forbidden.txt": "plain\n"}, map[string]string{"notes.txt": "more plain\n"},
			"", ""},
		{"a red base refuses the batch", map[string]string{"bad.go": vetRed}, map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}, map[string]string{"NOTES.md": "still fine\n"},
			"", "reason=the base main fails the tree gate at its tip, so no head is merged onto it; fix the base, then run land again: vet, bad.go:5:26: fmt.Printf format %d"},
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
				log := []string{"land s1-1 (sprint stream s1)", "the module", "base"}
				if tc.base != nil {
					log = []string{"land s1-1 (sprint stream s1)", "the base's change", "the module", "base"}
				}
				assert.Equal(t, log, r.mainLog())
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

// A red base and a queued head whose tree passes the gate the base fails: the lander gates
// the candidate's tree, not only the base's, lands that head first as the base fix, naming
// it in the landing note, and the batch goes on after it (sprint.FindBaseCure; 2026-10-05
// 7:30 PM, the fix card refused because the base it fixed was red).
func TestTheLanderLandsTheBaseFixFirst(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the base's change", map[string]string{"bad.go": vetRed})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}),
		"s1-2": r.card("s1-2", map[string]string{"bad.go": "package main\n\nfunc bad() {}\n"})}
	r.queued(heads, "s1-1", "s1-2")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	require.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	assert.Contains(t, out+errs, "s1-2 landed first as the base fix")
	log := r.mainLog()
	require.GreaterOrEqual(t, len(log), 2)
	assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "land s1-2 (sprint stream s1)"}, log[:2], "the fix lands first, then the casualty on the green base")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	r.clean()
}

// The base gate's green result is cached by base commit SHA: once gated, the same commit
// is not re-gated on subsequent calls even if the tree on disk changes. A red one is the
// base-gate rule's: reported with when it is gated again, and not re-gated before then
// (base_gate_rule_test.go has the retries and the stop).
func TestTreeGateBaseCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for f, content := range goModule {
		p := filepath.Join(dir, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	l := &lander{baseGateCache: map[string]string{}}
	green, _ := l.treeGateBase(context.Background(), dir, "base-1")
	assert.Equal(t, "", green)
	assert.Equal(t, "", l.baseGateCache["base-1"])

	// Corrupt main.go: cache hit for base-1 still reports green without running.
	mainGo := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(mainGo, []byte(buildRed), 0o600))
	green, _ = l.treeGateBase(context.Background(), dir, "base-1")
	assert.Equal(t, "", green)

	// A different base SHA (base-2) gates and records the failure for its retry.
	why, stop := l.treeGateBase(context.Background(), dir, "base-2")
	assert.Contains(t, why, "syntax error")
	assert.False(t, stop)
	require.NotNil(t, l.baseGateFails["base-2"])

	// Restore main.go: before its retry, base-2 still reports the failure, not re-gated.
	require.NoError(t, os.WriteFile(mainGo, []byte(goModule["main.go"]), 0o600))
	again, _ := l.treeGateBase(context.Background(), dir, "base-2")
	assert.Equal(t, why, again)
}

// On a bench the gofmt check is the same script as here: a tree gofmt lists a file in is
// red, the run named and the file printed; a clean tree is green.
func TestTheBenchGateRunsGofmtAsTheToolchainsOwn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.go"), []byte("package m\n"), 0o600))
	runs := [][]string{{"true"}, gofmtRun}
	cmd := exec.Command("sh", "-c", gateScript(runs))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ugly.go"), []byte(uglyGo), 0o600))
	cmd = exec.Command("sh", "-c", gateScript(runs))
	cmd.Dir = dir
	out, err = cmd.CombinedOutput()
	require.Error(t, err, string(out))
	assert.Equal(t, gofmtRun, redRun(runs, string(out)))
	assert.True(t, strings.HasPrefix(gateWhy(gofmtRun, err, strings.TrimPrefix(string(out), gateMark+"true\n"+gateMark+"gofmt -l .\n")), "gofmt, ugly.go is not gofmt-clean; "), string(out))
}
