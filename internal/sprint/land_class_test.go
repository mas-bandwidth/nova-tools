package sprint_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The base's class gate on the twin store and a twin repository (docs/SPEC-SPRINT.md section
// 7, the tree gate; 2026-10-05 night: the base red for hours on gofmt, a staticcheck U1000,
// TestDeadCode and class tests the lander's gate never ran, and the lander kept landing onto
// it). The runner is the bench's stand-in: staticcheck is red while unused.go holds the
// unused helper, and the class tests are red until unused.go says it is tested.

// classRunner is the twin's runner: the runs it was given, in order.
func classRunner(runs *[]string) sprint.ClassRunner {
	return func(_ context.Context, dir string, run []string) (string, error) {
		name := strings.Join(run, " ")
		*runs = append(*runs, name)
		switch {
		case strings.Contains(name, "TestStaticcheckFindings"):
			b, _ := os.ReadFile(filepath.Join(dir, "unused.go"))
			if strings.Contains(string(b), "func helper") {
				return "--- FAIL: TestStaticcheckFindings (4.20s)\n    staticcheck_class_test.go:160: unused.go:3:6: func helper is unused (U1000)\nFAIL\nFAIL\tgithub.com/mas-bandwidth/nova-tools/internal/ci\t4.3s\n", errors.New("exit status 1")
			}
		case run[0] == "go" && run[1] == "test" && !strings.Contains(name, "functional"):
			if b, _ := os.ReadFile(filepath.Join(dir, "unused.go")); !strings.Contains(string(b), "// tested") {
				return "--- FAIL: TestNovaToolsIsEveryCommand (0.01s)\nFAIL\nFAIL\tgithub.com/mas-bandwidth/nova-tools/internal/docs\t0.1s\n", errors.New("exit status 1")
			}
		}
		return "", nil
	}
}

// moduleFiles is a module with both tree-test packages, so every class runs.
var moduleFiles = map[string]string{
	"go.mod":                "module example.com/m\n\ngo 1.26\n",
	"internal/ci/ci.go":     "package ci\n",
	"internal/docs/docs.go": "package docs\n",
}

func TestARedClassOnTheBaseStopsLandings(t *testing.T) {
	t.Parallel()
	repo := newSyncRepo(t)
	for f, body := range moduleFiles {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(repo.dev, f)), 0o755))
		repo.commit(syncBase, f, body, "add "+f)
	}
	// the base turns red on staticcheck (a U1000) and on a class test both; s1-1 is a
	// casualty
	repo.commit(syncBase, "unused.go", "package m\n\nfunc helper() {}\n", "an unused helper")
	casualty := repo.branchHead("card/s1-1", "other.go", "package m\n")
	baseTip := repo.tip(syncBase)
	repo.git(repo.land, "fetch", "-q", "origin")
	repo.git(repo.land, "checkout", "-q", "--detach", "origin/"+syncBase)

	var runs []string
	gate := &sprint.BaseClassGate{Run: classRunner(&runs)}
	red, err := gate.Gate(t.Context(), repo.land, syncBase, baseTip)
	require.NoError(t, err)
	require.True(t, red.Red())
	assert.Equal(t, "staticcheck", red.Class.Name, "the first red class is named, not the class tests after it")
	assert.Equal(t, []string{"gofmt -l .", "go vet ./...", strings.Join(sprint.BaseClasses[2].Run, " ")}, runs, "the suite stops at its first red")
	assert.Equal(t, "fix-red-staticcheck-sprint-mechanical-2026-10-02", red.Card.ID)
	assert.Equal(t, "fix-red", red.Card.Kind)
	assert.Equal(t, "internal/ci TestStaticcheckFindings", red.Card.Test)
	assert.Contains(t, red.Card.Paths, "unused.go", "the card may touch the file the finding names")
	assert.NotContains(t, red.Card.Paths, "staticcheck_class_test.go", "a test's file:line prefix is no path of the tree")

	// errcheck runs with staticcheck: its red names its own test
	errRed, err := (&sprint.BaseClassGate{Run: func(_ context.Context, _ string, run []string) (string, error) {
		if strings.Contains(strings.Join(run, " "), "TestUncheckedErrors") {
			return "--- FAIL: TestUncheckedErrors (3.10s)\nFAIL\nFAIL\tgithub.com/mas-bandwidth/nova-tools/internal/ci\t3.2s\n", errors.New("exit status 1")
		}
		return "", nil
	}}).Gate(t.Context(), repo.land, syncBase, baseTip)
	require.NoError(t, err)
	assert.Equal(t, "staticcheck", errRed.Class.Name)
	assert.Equal(t, "internal/ci TestUncheckedErrors", errRed.Card.Test)

	// a red is not kept: the base-gate rule's retry runs the suite afresh, to the same class
	again, err := gate.Gate(t.Context(), repo.land, syncBase, baseTip)
	require.NoError(t, err)
	assert.Equal(t, red, again)
	assert.Len(t, runs, 6)

	// landings onto the red base stop: each is refused and counted, no card moves, and the
	// rule's stop is one judgment naming the class and the fix-red card
	r := newConflictRig(t)
	for _, id := range []string{"s1-1", "s1-2"} {
		r.toMerging(id)
	}
	for n := 1; n < sprint.BaseGateStops; n++ {
		r.must(store.MergeStep(red.MergeReq("s1", "land", false)))
		s := r.snap()
		assert.Equal(t, strconv.Itoa(n), s.StreamCtl("s1").F(sprint.FieldBaseGateRefused))
		assert.Empty(t, r.open(sprint.NBaseRed), "refusal %d raises no judgment", n)
		for _, id := range []string{"s1-1", "s1-2"} {
			assert.Equal(t, sprint.Merging, s.Work.Card(id).Col, "%s did not land onto the red base", id)
		}
	}
	r.must(store.MergeStep(red.MergeReq("s1", "land", true)))
	s := r.snap()
	assert.Equal(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"))
	assert.Equal(t, "base", s.StreamCtl("s1").F("cause"))
	open := r.open(sprint.NBaseRed)
	require.Len(t, open, 1, "one judgment")
	for _, want := range []string{"red on its class staticcheck", "U1000", "the fix-red card fix-red-staticcheck-sprint-mechanical-2026-10-02", "TEST: internal/ci TestStaticcheckFindings", baseTip[:12]} {
		assert.Contains(t, open[0].Note.What, want)
	}
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.Equal(t, sprint.Merging, s.Work.Card(id).Col, "%s did not land", id)
	}
	res, err := r.st.Run(r.ctx, store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-1"}}))
	require.NoError(t, err)
	require.NotEmpty(t, res.Refused, "a landing onto the stopped stream is refused")

	// the cure: a head that passes the whole suite merged onto the base lands first; one
	// that cures staticcheck but leaves the class tests red is no cure
	half := repo.branchHead("card/s1-2", "unused.go", "package m\n")
	runs = nil
	repo.git(repo.land, "fetch", "-q", "origin")
	cure, err := sprint.FindBaseCure(t.Context(), sprint.BaseCureReq{RepoDir: repo.land, Base: baseTip, Env: repo.env, Gate: gate.Tree,
		Heads: []sprint.CureHead{{ID: "s1-1", Head: casualty}, {ID: "s1-2", Head: half}}})
	require.NoError(t, err)
	assert.False(t, cure.Found())
	require.Len(t, cure.Tried, 2)
	assert.Contains(t, cure.Tried[0].Why, "class staticcheck")
	assert.Contains(t, cure.Tried[1].Why, "class class-tests", "the half fix is red on the next class")
	assert.Contains(t, cure.Tried[1].Why, "TestNovaToolsIsEveryCommand")

	fix := repo.branchHead("card/s1-2-fix", "unused.go", "package m\n\n// tested\n")
	repo.git(repo.land, "fetch", "-q", "origin")
	cure, err = sprint.FindBaseCure(t.Context(), sprint.BaseCureReq{RepoDir: repo.land, Base: baseTip, Env: repo.env, Gate: gate.Tree,
		Heads: []sprint.CureHead{{ID: "s1-1", Head: casualty}, {ID: "s1-2", Head: fix}}})
	require.NoError(t, err)
	require.True(t, cure.Found())
	assert.Equal(t, "s1-2", cure.ID)
	repo.git(repo.land, "push", "-q", "origin", "HEAD:refs/heads/"+syncBase)
	green, err := gate.Gate(t.Context(), repo.land, syncBase, repo.tip(syncBase))
	require.NoError(t, err)
	assert.False(t, green.Red(), "the cured base is green on every class")
	assert.Empty(t, green.Why())
}

// gofmt is red when it prints, though it exits 0; a clone with no module has no gate, and
// a clone without a class's package skips that class.
func TestTheClassGateReadsGofmtsOutputAndSkipsWhatTheCloneLacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var runs []string
	run := func(_ context.Context, _ string, run []string) (string, error) {
		runs = append(runs, strings.Join(run, " "))
		if run[0] == "gofmt" {
			return "cmd/nova-secrets/main.go\n", nil
		}
		return "", nil
	}
	gate := &sprint.BaseClassGate{Run: run}
	red, err := gate.Gate(t.Context(), dir, "dev", "sha-0")
	require.NoError(t, err)
	assert.False(t, red.Red(), "no go.mod, no gate")
	assert.Empty(t, runs)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "nova-secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "nova-secrets", "main.go"), []byte("package main\n"), 0o644))
	red, err = gate.Gate(t.Context(), dir, "dev", "sha-1")
	require.NoError(t, err)
	require.True(t, red.Red())
	assert.Equal(t, "gofmt", red.Class.Name)
	assert.Contains(t, red.Finding, "cmd/nova-secrets/main.go")
	assert.Equal(t, "fix-red-gofmt-dev", red.Card.ID)
	assert.Equal(t, "internal/ci TestTheTreePassesGofmt", red.Card.Test, "gofmt has no test: the card writes the class test")
	assert.Contains(t, red.Card.Paths, "cmd/nova-secrets/main.go")

	runs = nil
	gate = &sprint.BaseClassGate{Run: func(_ context.Context, _ string, r []string) (string, error) {
		runs = append(runs, r[0]+" "+r[1])
		return "", nil
	}}
	red, err = gate.Gate(t.Context(), dir, "dev", "sha-2")
	require.NoError(t, err)
	assert.False(t, red.Red())
	assert.Equal(t, []string{"gofmt -l", "go vet"}, runs, "no internal/ci or internal/docs: only the module's classes run")
	runs = nil
	red, err = gate.Gate(t.Context(), dir, "dev", "sha-2")
	require.NoError(t, err)
	assert.False(t, red.Red())
	assert.Empty(t, runs, "a green base commit is gated once")
}
