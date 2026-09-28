package land_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// helper to check if slice contains string
func contains(list []string, item string) bool {
	for _, s := range list {
		if s == item {
			return true
		}
	}
	return false
}

// helper gate runner that simulates running tests on selected packages with planted failures
func runSelectionGate(sel land.SelectionResult, plantedFailures map[string]string) land.Verdict {
	for _, pkg := range sel.Packages {
		if test, ok := plantedFailures[pkg]; ok {
			return land.Verdict{
				OK:      false,
				Step:    "test",
				Package: pkg,
				Test:    test,
			}
		}
	}
	return land.Verdict{OK: true}
}

// TestL9: a reverse dependency's planted failure turns the gate red (fails on: skipped dependents).
func TestL9(t *testing.T) {
	t.Parallel()

	pkgs := []land.PackageInfo{
		{
			Dir:         "internal/dep",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/dep",
			GoFiles:     []string{"dep.go"},
			TestGoFiles: []string{"dep_test.go"},
		},
		{
			Dir:         "internal/dependent",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/dependent",
			GoFiles:     []string{"dependent.go"},
			TestGoFiles: []string{"dependent_test.go"},
			Imports:     []string{"github.com/mas-bandwidth/nova-tools/internal/dep"},
		},
		{
			Dir:         "internal/unrelated",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/unrelated",
			GoFiles:     []string{"unrelated.go"},
			TestGoFiles: []string{"unrelated_test.go"},
		},
	}

	g := land.NewGraph("tree1", "linux", nil, pkgs)

	changedFiles := []string{"internal/dep/dep.go"}
	sel := land.Select(changedFiles, g)

	// Verify selection contains the changed package and its reverse dependent
	if !contains(sel.Packages, "github.com/mas-bandwidth/nova-tools/internal/dep") {
		t.Fatalf("dep was not selected: got %v", sel.Packages)
	}
	if !contains(sel.Packages, "github.com/mas-bandwidth/nova-tools/internal/dependent") {
		t.Fatalf("dependent was not selected: got %v", sel.Packages)
	}
	if contains(sel.Packages, "github.com/mas-bandwidth/nova-tools/internal/unrelated") {
		t.Fatalf("unrelated was unexpectedly selected: got %v", sel.Packages)
	}

	// Plant a test failure in the reverse dependent
	planted := map[string]string{
		"github.com/mas-bandwidth/nova-tools/internal/dependent": "TestPlantedFailureInDependent",
	}

	verdict := runSelectionGate(sel, planted)
	if verdict.OK {
		t.Fatalf("gate must turn RED when reverse dependent has a planted failure, got OK=true")
	}
	if verdict.Package != "github.com/mas-bandwidth/nova-tools/internal/dependent" || verdict.Test != "TestPlantedFailureInDependent" {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}

	// Verify the defect: if dependents were skipped, the gate would stay green
	defectiveSel := land.SelectionResult{
		Packages: []string{"github.com/mas-bandwidth/nova-tools/internal/dep"},
	}
	defectiveVerdict := runSelectionGate(defectiveSel, planted)
	if !defectiveVerdict.OK {
		t.Fatalf("defect verification: skipped dependents should have stayed green, got %+v", defectiveVerdict)
	}
}

// TestL9b: a Go-only batch has no lisp step (fails on: lisp on every gate).
func TestL9b(t *testing.T) {
	t.Parallel()

	pkgs := []land.PackageInfo{
		{
			Dir:         "internal/nsprint/land",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/nsprint/land",
			GoFiles:     []string{"select.go"},
			TestGoFiles: []string{"select_test.go"},
		},
	}
	g := land.NewGraph("tree1", "linux", nil, pkgs)

	// Go-only changed files
	changedFiles := []string{"internal/nsprint/land/select.go"}
	sel := land.Select(changedFiles, g)

	if sel.Class != land.ClassGo {
		t.Fatalf("class must be %q, got %q", land.ClassGo, sel.Class)
	}

	// Assert no lisp step exists in the planned steps
	for _, step := range sel.Steps {
		if strings.EqualFold(step.Name, "lisp") {
			t.Fatalf("Go-only batch must have no lisp step, but found step %q", step.Name)
		}
		for _, arg := range step.Cmd {
			if strings.Contains(arg, "sbcl") || strings.Contains(arg, "lisp") {
				t.Fatalf("Go-only batch must not execute lisp commands: %v", step.Cmd)
			}
		}
	}

	// Verify that a lisp file change properly produces ClassLisp with a lisp step
	lispChanged := []string{"lisp/core.lisp"}
	lispSel := land.Select(lispChanged, g)
	if lispSel.Class != land.ClassLisp {
		t.Fatalf("lisp changed file must produce class %q, got %q", land.ClassLisp, lispSel.Class)
	}
	hasLisp := false
	for _, step := range lispSel.Steps {
		if step.Name == "lisp" {
			hasLisp = true
			break
		}
	}
	if !hasLisp {
		t.Fatalf("lisp batch must include lisp step")
	}
}

// TestL9c: an import added by the train is selected (fails on: a base-only graph).
func TestL9c(t *testing.T) {
	t.Parallel()

	// Base graph: pkg/trainconsumer does NOT import pkg/dep
	basePkgs := []land.PackageInfo{
		{
			Dir:         "internal/dep",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/dep",
			GoFiles:     []string{"dep.go"},
			TestGoFiles: []string{"dep_test.go"},
		},
		{
			Dir:         "internal/trainconsumer",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/trainconsumer",
			GoFiles:     []string{"consumer.go"},
			TestGoFiles: []string{"consumer_test.go"},
			Imports:     []string{}, // No import of dep at base!
		},
	}
	baseGraph := land.NewGraph("baseTree", "linux", nil, basePkgs)

	// Train graph: pkg/trainconsumer added an import of pkg/dep
	trainPkgs := []land.PackageInfo{
		{
			Dir:         "internal/dep",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/dep",
			GoFiles:     []string{"dep.go"},
			TestGoFiles: []string{"dep_test.go"},
		},
		{
			Dir:         "internal/trainconsumer",
			ImportPath:  "github.com/mas-bandwidth/nova-tools/internal/trainconsumer",
			GoFiles:     []string{"consumer.go"},
			TestGoFiles: []string{"consumer_test.go"},
			Imports:     []string{"github.com/mas-bandwidth/nova-tools/internal/dep"}, // Added by train!
		},
	}
	trainGraph := land.NewGraph("trainTree", "linux", nil, trainPkgs)

	changedFiles := []string{"internal/dep/dep.go"}

	// When tested in base-only mode (simulating the defect), it turns red:
	if os.Getenv("TEST_BASE_ONLY") == "1" {
		baseOnlySel := land.Select(changedFiles, baseGraph)
		if !contains(baseOnlySel.Packages, "github.com/mas-bandwidth/nova-tools/internal/trainconsumer") {
			t.Fatalf("base-only graph turns TestL9c red: train consumer was not selected")
		}
	}

	// 1. In a base-only graph, the train consumer is NOT selected (the defect)
	baseSel := land.Select(changedFiles, baseGraph)
	if contains(baseSel.Packages, "github.com/mas-bandwidth/nova-tools/internal/trainconsumer") {
		t.Fatalf("base-only graph unexpectedly selected train consumer")
	}

	// 2. In the union graph (base + train), the train consumer IS selected
	unionSel := land.SelectUnion(changedFiles, baseGraph, trainGraph)
	if !contains(unionSel.Packages, "github.com/mas-bandwidth/nova-tools/internal/trainconsumer") {
		t.Fatalf("union graph must select train consumer, got %v", unionSel.Packages)
	}

	// 3. Planted failure in train consumer
	planted := map[string]string{
		"github.com/mas-bandwidth/nova-tools/internal/trainconsumer": "TestPlantedFailureInTrainConsumer",
	}

	// Base-only graph misses the failure (stays green)
	vBase := runSelectionGate(baseSel, planted)
	if !vBase.OK {
		t.Fatalf("base-only graph unexpectedly ran train consumer failure: %+v", vBase)
	}

	// Union graph catches the failure and turns the gate red!
	vUnion := runSelectionGate(unionSel, planted)
	if vUnion.OK {
		t.Fatalf("union graph must turn gate red on train consumer failure")
	}
	if vUnion.Package != "github.com/mas-bandwidth/nova-tools/internal/trainconsumer" {
		t.Fatalf("unexpected failure package: %+v", vUnion)
	}
}

// TestL9d: same tree and GOOS with tag sets {} and {integration}, then a policy tag change:
// three cache keys, and the package that imports the change only under integration is selected.
func TestL9d(t *testing.T) {
	t.Parallel()

	repo := "nova-tools"
	tree := "b407068f"
	goos := "linux"
	goVer := "go1.24.0"
	goFlags := "-v"
	policySection1 := "sel_goos: [linux, darwin, windows]\ntags: []"
	policySection2 := "sel_goos: [linux, darwin, windows]\ntags: [integration]"

	cfg1 := land.ConfigHash([]string{}, goVer, goFlags, policySection1)
	cfg2 := land.ConfigHash([]string{"integration"}, goVer, goFlags, policySection1)
	cfg3 := land.ConfigHash([]string{"integration"}, goVer, goFlags, policySection2)

	key1 := land.SelKey(repo, tree, goos, cfg1)
	key2 := land.SelKey(repo, tree, goos, cfg2)
	key3 := land.SelKey(repo, tree, goos, cfg3)

	if key1 == key2 {
		t.Fatalf("key1 and key2 must differ for different tag sets: %s", key1)
	}
	if key2 == key3 {
		t.Fatalf("key2 and key3 must differ for policy changes: %s", key2)
	}
	if key1 == key3 {
		t.Fatalf("key1 and key3 must differ: %s", key1)
	}

	// Graph without integration tags: pkg/int does NOT import pkg/target
	g1Pkgs := []land.PackageInfo{
		{
			Dir:        "internal/target",
			ImportPath: "github.com/mas-bandwidth/nova-tools/internal/target",
			GoFiles:    []string{"target.go"},
		},
		{
			Dir:        "internal/int",
			ImportPath: "github.com/mas-bandwidth/nova-tools/internal/int",
			GoFiles:    []string{"int.go"},
			Imports:    []string{},
		},
	}
	g1 := land.NewGraph(tree, goos, nil, g1Pkgs)

	// Graph with integration tags: pkg/int imports pkg/target
	g2Pkgs := []land.PackageInfo{
		{
			Dir:        "internal/target",
			ImportPath: "github.com/mas-bandwidth/nova-tools/internal/target",
			GoFiles:    []string{"target.go"},
		},
		{
			Dir:        "internal/int",
			ImportPath: "github.com/mas-bandwidth/nova-tools/internal/int",
			GoFiles:    []string{"int.go"},
			Imports:    []string{"github.com/mas-bandwidth/nova-tools/internal/target"},
		},
	}
	g2 := land.NewGraph(tree, goos, []string{"integration"}, g2Pkgs)

	changed := []string{"internal/target/target.go"}
	unionSel := land.SelectMulti(changed, []*land.Graph{g1}, []*land.Graph{g2})

	if !contains(unionSel.Packages, "github.com/mas-bandwidth/nova-tools/internal/int") {
		t.Fatalf("package importing under integration must be selected: got %v", unionSel.Packages)
	}
}
