package sprint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The base's class gate (docs/SPEC-SPRINT.md section 7, the tree gate; section 8, the
// base-gate rule). On 2026-10-05 night the base was red for hours on gofmt, a staticcheck
// U1000, TestNovaToolsIsEveryCommand, TestDeadCode and three sprintdash tests, while the
// lander's base gate (go build, go vet, go test ./internal/docs ./internal/ci) passed and it
// kept landing onto the red base: every card whose TEST was one of those failed whatever it
// did. The base gate is the tree's class suite, run once a batch at the base's tip on the
// bench the lander uses: gofmt, vet, staticcheck and errcheck, dead code, then the class
// tests. The first class that is red stops every landing onto that base and is named in one
// judgment with the fix-red card the generator stamps for it (cardgen.PlanClassRed); a head
// whose tree, merged onto the base alone, passes the whole suite is the cure and lands first
// (FindBaseCure with BaseClassGate.Tree as its gate).

// Class is one class of the tree's class suite: its name, its run, the test that pins it
// ("pkg TestX"; "" read off a failing run's output, or none for a run with no test), the
// package directories the run needs (a clone that lacks one, as a repository with no
// internal/ci does, does not run the class), and Silent: red when the run prints anything,
// as gofmt -l does, even when it exits 0.
type Class struct {
	Name   string
	Run    []string
	Test   string
	Needs  []string
	Silent bool
}

// classTestsRun is the class suite's last class: the packages that test the tree itself,
// in the unit tier.
var classTestsRun = []string{"go", "test", "-count=1", "./internal/ci/", "./internal/docs/"}

// BaseClasses is the class suite, in the order it runs; the first red is the base's class.
// staticcheck and errcheck, and dead code, are class tests behind the functional tag
// (internal/ci staticcheck_class_test.go, errcheck_class_test.go, dead_code_class_test.go):
// a whole-tree analysis is over the unit tier's budget, so the gate runs them by name with
// the tag; they start no redis-server.
var BaseClasses = []Class{
	{Name: "gofmt", Run: []string{"gofmt", "-l", "."}, Silent: true},
	{Name: "vet", Run: []string{"go", "vet", "./..."}},
	{Name: "staticcheck", Run: []string{"go", "test", "-tags", "functional", "-count=1", "-run", "^(TestStaticcheckFindings|TestUncheckedErrors)$", "./internal/ci/"},
		Test: "internal/ci TestStaticcheckFindings", Needs: []string{"internal/ci"}},
	{Name: "dead-code", Run: []string{"go", "test", "-tags", "functional", "-count=1", "-run", "^TestDeadCode$", "./internal/ci/"},
		Test: "internal/ci TestDeadCode", Needs: []string{"internal/ci"}},
	{Name: "class-tests", Run: classTestsRun, Needs: []string{"internal/ci", "internal/docs"}},
}

// ClassRunner runs one class's run in the clone at dir: its combined output, and an error
// when it exits other than 0. The lander's runs every go command under GOFLAGS=-mod=readonly.
type ClassRunner func(ctx context.Context, dir string, run []string) (string, error)

// ClassRed is a base red on one class of its suite, the first that is red: the zero value is
// green. Base and Sha are the base's branch and the commit gated; Card is the fix-red card
// the generator stamps for it.
type ClassRed struct {
	Class   Class
	Base    string
	Sha     string
	Finding string
	Card    cardgen.Card
}

// Red says the base is red on a class.
func (r ClassRed) Red() bool { return r.Class.Name != "" }

// Why is the red as the stream's stop says it (MergeReq.BaseRed, the judgment NBaseRed): the
// base, the class and its run, the finding, and the fix-red card that cures it.
func (r ClassRed) Why() string {
	if !r.Red() {
		return ""
	}
	sha := r.Sha
	if len(sha) > 12 {
		sha = sha[:12]
	}
	return cutText(fmt.Sprintf("the base %s at %s is red on its class %s (%s): %s; the fix-red card %s (TEST: %s) cures it, and a head that passes the class suite merged onto the base lands first as the base fix",
		r.Base, sha, r.Class.Name, strings.Join(r.Class.Run, " "), r.Finding, r.Card.ID, r.Card.Test), MaxCardTextBytes)
}

// MergeReq is the landing the red stops: the stream stops at once with the judgment
// NBaseRed naming the class (a class is not retried as a transient tree gate is: the same
// tree is red on it again).
func (r ClassRed) MergeReq(stream, who string) MergeReq {
	return MergeReq{Stream: stream, Base: r.Base, BaseRed: r.Why(), Who: who}
}

// BaseClassGate runs the class suite once per base commit: a batch onto a base commit gated
// before reads the result kept for it, green or red, until the base moves.
type BaseClassGate struct {
	Run     ClassRunner
	Classes []Class // nil is BaseClasses
	mu      sync.Mutex
	seen    map[string]ClassRed
}

// Gate is the class suite at the base's tip (dir holds the clone at sha): the first red
// class, or the zero ClassRed when every class is green. A clone with no go.mod has no
// module and no gate.
func (g *BaseClassGate) Gate(ctx context.Context, dir, base, sha string) (ClassRed, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.seen[sha]; ok {
		return r, nil
	}
	r, err := g.suite(ctx, dir)
	if err != nil {
		return ClassRed{}, err
	}
	if r.Red() {
		r.Base, r.Sha = base, sha
		r.Card = classCard(r)
	}
	if g.seen == nil {
		g.seen = map[string]ClassRed{}
	}
	g.seen[sha] = r
	return r, nil
}

// Tree is the class suite on a tree no batch has gated (a cure's candidate, merged onto the
// red base): "" when green, else the class and its finding. Its result is not kept.
func (g *BaseClassGate) Tree(ctx context.Context, dir string) string {
	r, err := g.suite(ctx, dir)
	switch {
	case err != nil:
		return err.Error()
	case r.Red():
		return "class " + r.Class.Name + ": " + r.Finding
	}
	return ""
}

// suite runs each class the clone has, in order, and stops at the first red.
func (g *BaseClassGate) suite(ctx context.Context, dir string) (ClassRed, error) {
	if g.Run == nil {
		return ClassRed{}, errors.New("base class gate: no runner")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ClassRed{}, nil
	}
	classes := g.Classes
	if classes == nil {
		classes = BaseClasses
	}
	for _, c := range classes {
		if !holdsPackages(dir, c.Needs) {
			continue
		}
		out, err := g.Run(ctx, dir, c.Run)
		if cerr := ctx.Err(); cerr != nil {
			return ClassRed{}, fmt.Errorf("base class gate: %s: %w", c.Name, cerr)
		}
		if err == nil && (!c.Silent || strings.TrimSpace(out) == "") {
			continue
		}
		return ClassRed{Class: c, Finding: classFinding(c, err, out)}, nil
	}
	return ClassRed{}, nil
}

// holdsPackages says the clone at dir holds every one of pkgs as a Go package (a directory
// with a .go file in it).
func holdsPackages(dir string, pkgs []string) bool {
	for _, p := range pkgs {
		if m, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p), "*.go")); len(m) == 0 {
			return false
		}
	}
	return true
}

// classFinding is a red run on one line: how it ended and its output's lines.
func classFinding(c Class, err error, out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	how := "exit status 0"
	if err != nil {
		how = oneline.Err(err)
	}
	if c.Silent && err == nil {
		how = "it printed"
	}
	return how + ": " + oneline.Cap(strings.Join(lines, " | "), 1500)
}

var (
	// a repository-relative Go file, as gofmt -l, vet and staticcheck print one
	classFileRE = regexp.MustCompile(`(?m)(?:^|[\s|])(?:\./)?([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*\.go)\b`)
	// the first test that failed, and its package
	classFailRE    = regexp.MustCompile(`--- FAIL: (Test[A-Za-z0-9_]*)`)
	classFailPkgRE = regexp.MustCompile(`FAIL\s+github\.com/[^/\s]+/[^/\s]+/(\S+)`)
)

// classCard is the fix-red card for r (cardgen.PlanClassRed): the files its finding names
// and, for a class whose test is read off its run, the first test that failed.
func classCard(r ClassRed) cardgen.Card {
	test := r.Class.Test
	if test == "" && len(r.Class.Run) > 1 && r.Class.Run[1] == "test" {
		if m := classFailRE.FindStringSubmatch(r.Finding); m != nil {
			pkg := "internal/ci"
			if p := classFailPkgRE.FindStringSubmatch(r.Finding); p != nil {
				pkg = p[1]
			}
			test = pkg + " " + m[1]
		}
	}
	var files []string
	seen := map[string]bool{}
	for _, m := range classFileRE.FindAllStringSubmatch(r.Finding, -1) {
		if f := m[1]; !seen[f] && !strings.HasPrefix(f, "/") {
			seen[f] = true
			files = append(files, f)
		}
	}
	return cardgen.PlanClassRed(cardgen.ClassRed{Class: r.Class.Name, Run: strings.Join(r.Class.Run, " "), Test: test, Files: files, Finding: r.Finding}, r.Base, "")
}
