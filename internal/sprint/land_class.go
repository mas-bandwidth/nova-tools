package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
)

// Class is one run of the base's class suite (SPEC-SPRINT, the base's class
// gate). Needs are tree packages the clone must hold.
type Class struct {
	Name  string
	Run   []string
	Test  string
	Needs []string
}

// BaseClasses includes the existing build and every class that guards a base.
// The whole-tree analyzers need the functional tag, but start no servers.
var BaseClasses = []Class{
	{Name: "build", Run: []string{"go", "build", "./..."}},
	{Name: "gofmt", Run: []string{"gofmt", "-l", "."}},
	{Name: "vet", Run: []string{"go", "vet", "./..."}},
	// vet-functional is the base's own functional-tier vet (Makefile vet-functional):
	// a plain vet compiles no file built only behind the functional tag.
	{Name: "vet-functional", Run: []string{"go", "vet", "-tags", "functional", "./..."}},
	{Name: "staticcheck", Run: []string{"go", "test", "-tags", "functional", "-count=1", "-timeout", "600s", "-run", "^TestStaticcheckFindings$", "./internal/ci/"}, Test: "internal/ci TestStaticcheckFindings", Needs: []string{"internal/ci"}},
	{Name: "errcheck", Run: []string{"go", "test", "-tags", "functional", "-count=1", "-timeout", "600s", "-run", "^TestUncheckedErrors$", "./internal/ci/"}, Test: "internal/ci TestUncheckedErrors", Needs: []string{"internal/ci"}},
	{Name: "dead-code", Run: []string{"go", "test", "-tags", "functional", "-count=1", "-timeout", "600s", "-run", "^TestDeadCode$", "./internal/ci/"}, Test: "internal/ci TestDeadCode", Needs: []string{"internal/ci"}},
	// onboarding is the fourth whole-tree functional check (ONBOARDING point 6): every
	// cmd/ tool's banner and example block, the same class the batch's gate runs.
	{Name: "onboarding", Run: []string{"go", "test", "-tags", "functional", "-count=1", "-timeout", "600s", "-run", "^TestEveryCommandMeetsTheOnboardingStandard$", "./internal/ci/"}, Test: "internal/ci TestEveryCommandMeetsTheOnboardingStandard", Needs: []string{"internal/ci"}},
	{Name: "class-tests", Run: []string{"go", "test", "-count=1", "-timeout", "600s"}, Needs: []string{"internal/ci", "internal/docs"}},
}

var (
	classFileRE    = regexp.MustCompile(`(?m)(?:^|[\s|])(?:\./)?([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*\.go)\b`)
	classFailRE    = regexp.MustCompile(`--- FAIL: (Test[A-Za-z0-9_]*)`)
	classFailPkgRE = regexp.MustCompile(`FAIL(?:\s|\\x09)+[^\s|]+/(internal/(?:ci|docs))\b`)
)

// ClassGateWhy names the first red run with the repair card the generator
// stamps for it. The existing base-gate rule stores this on its one judgment.
// This is the metadata half of SPEC-SPRINT's base class gate, not another gate.
func ClassGateWhy(dir, base, sha, why string) string {
	var class Class
	for _, c := range BaseClasses {
		if strings.HasPrefix(why, strings.Join(c.Run, " ")+":") || c.Name == "class-tests" && strings.HasPrefix(why, strings.Join(c.Run, " ")+" ") {
			class = c
			break
		}
	}
	if class.Name == "" {
		return why
	}
	test := class.Test
	if m := classFailRE.FindStringSubmatch(why); m != nil {
		pkg := "internal/ci"
		if p := classFailPkgRE.FindStringSubmatch(why); p != nil {
			pkg = p[1]
		}
		test = pkg + " " + m[1]
	}
	var files []string
	seen := map[string]bool{}
	for _, m := range classFileRE.FindAllStringSubmatch(why, -1) {
		f := m[1]
		if !filepath.IsLocal(f) || seen[f] {
			continue
		}
		seen[f] = true
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil && st.Mode().IsRegular() {
			files = append(files, f)
		}
	}
	card := cardgen.PlanClassRed(cardgen.ClassRed{Class: class.Name, Run: strings.Join(class.Run, " "), Test: test, Files: files, Finding: why}, base, "")
	return cutText(fmt.Sprintf("the base %s at %s is red on its class %s; fix-red card %s (TEST: %s); %s", base, sha[:min(12, len(sha))], class.Name, card.ID, card.Test, why), MaxCardTextBytes)
}
