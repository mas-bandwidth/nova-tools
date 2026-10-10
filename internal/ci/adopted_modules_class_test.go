package ci

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

// adopted_modules_class_test.go holds docs/STANDARD.md section 7's rule in force
// for a dependency, "Library first": the adopted set is the module paths a
// direct require of go.mod may name, and docs/CONTRIBUTING.md points a reader at
// it. The set is written as module paths in backticks in section 7, so a reader
// following CONTRIBUTING finds the dependency the tree already holds and a
// reader following STANDARD can tell which ones were admitted and why.
//
// golang.org/x/mod/modfile (already a require) parses go.mod: every non-indirect
// require's path must appear, backticked, in section 7. The tool directive is a
// tool, not a dependency the tree holds, so its modules are not demanded. The
// reversed witness is a go.mod with one extra direct require: the same check
// reports it, so the rule is shown to fail when it is broken.

// adoptedStandardHeading opens docs/STANDARD.md section 7, the section the
// adopted set lives in.
const adoptedStandardHeading = "## 7. "

// adoptedSection reads docs/STANDARD.md from section 7 to the next `## `.
func adoptedSection(standard string) (string, error) {
	start := strings.Index(standard, adoptedStandardHeading)
	if start < 0 {
		return "", fmt.Errorf("docs/STANDARD.md carries no %q heading", strings.TrimSpace(adoptedStandardHeading))
	}
	rest := standard[start:]
	if end := strings.Index(rest[len(adoptedStandardHeading):], "\n## "); end >= 0 {
		rest = rest[:len(adoptedStandardHeading)+end]
	}
	return rest, nil
}

// adoptedBacktick reads a backticked span, the one shape the adopted set uses to
// name a module path.
var adoptedBacktick = regexp.MustCompile("`([^`]+)`")

// adoptedModuleProblems parses gomod with modfile and returns one problem for
// every non-indirect require whose path is not backticked in standard's section
// 7. It is the check the tests share, so the reversed witness is judged by the
// same reader as the real tree.
func adoptedModuleProblems(gomod, standard string) ([]string, error) {
	f, err := modfile.Parse("go.mod", []byte(gomod), nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	section, err := adoptedSection(standard)
	if err != nil {
		return nil, err
	}
	adopted := map[string]bool{}
	for _, m := range adoptedBacktick.FindAllStringSubmatch(section, -1) {
		adopted[m[1]] = true
	}
	var problems []string
	for _, r := range f.Require {
		if r.Indirect {
			continue
		}
		if !adopted[r.Mod.Path] {
			problems = append(problems, fmt.Sprintf("%s is a direct require of go.mod and is in no backticked module path in docs/STANDARD.md section 7; remedy: add `%s` to the adopted set with the reason the tree holds it, or drop the require", r.Mod.Path, r.Mod.Path))
		}
	}
	return problems, nil
}

// TestAdoptedModulesEveryDirectRequireIsAdopted holds the rule in force: a direct
// require of go.mod is in docs/STANDARD.md section 7's adopted set. The failure
// names the module and the remedy, so the next reader can admit it or drop it.
func TestAdoptedModulesEveryDirectRequireIsAdopted(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	gomod := readFile(t, filepath.Join(root, "go.mod"))
	standard := readFile(t, filepath.Join(root, "docs", "STANDARD.md"))
	problems, err := adoptedModuleProblems(gomod, standard)
	require.NoError(t, err)
	assert.Empty(t, problems, "every direct require of go.mod is named in docs/STANDARD.md section 7 (the adopted set); a require with no entry is a dependency no reader can find\n%s", strings.Join(problems, "\n"))
}

// TestAdoptedModulesTheReversedWitnessIsRefused proves the check fails when the
// rule is broken: a go.mod text with one extra direct require is reported by the
// same check function that judges the real tree.
func TestAdoptedModulesTheReversedWitnessIsRefused(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	gomod := readFile(t, filepath.Join(root, "go.mod"))
	standard := readFile(t, filepath.Join(root, "docs", "STANDARD.md"))

	witness := gomod + "\nrequire example.com/notadopted v1.0.0\n"
	problems, err := adoptedModuleProblems(witness, standard)
	require.NoError(t, err)
	require.NotEmpty(t, problems, "the check admits a direct require the adopted set never named; the rule is not held")
	assert.Contains(t, strings.Join(problems, "\n"), "example.com/notadopted", "the check reports the witness module by name")
}

// TestAdoptedModulesToolDirectivesAreNotRequires pins that a tool directive is a
// tool and not a dependency the tree holds: the check reads the require block
// only, so a go.mod whose sole entry is a tool directive yields no problem.
func TestAdoptedModulesToolDirectivesAreNotRequires(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	standard := readFile(t, filepath.Join(root, "docs", "STANDARD.md"))
	real, err := modfile.Parse("go.mod", []byte(readFile(t, filepath.Join(root, "go.mod"))), nil)
	require.NoError(t, err)
	require.NotEmpty(t, real.Tool, "go.mod carries a tool block; this test would pass by checking nothing")

	const toolOnly = "module example.com/toolonly\n\ngo 1.26\n\ntool example.com/toolonly/cmd/thing\n"
	crafted, err := modfile.Parse("go.mod", []byte(toolOnly), nil)
	require.NoError(t, err)
	require.Len(t, crafted.Tool, 1, "the fixture carries one tool directive")
	require.Empty(t, crafted.Require, "a tool directive is not a require")

	problems, err := adoptedModuleProblems(toolOnly, standard)
	require.NoError(t, err)
	assert.Empty(t, problems, "the adopted set demands the require block only; a tool directive is not demanded\n%s", strings.Join(problems, "\n"))
}
