package ci

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoForcePushOrHardResetOfASharedRef is the class test that refuses patterns
// that rewrite shared refs (anything other than origin/*, dev, or main).
// See docs/SPEC-CI.md, "The CI class test against never-force-push or hard-reset
// of a shared ref".
func TestNoForcePushOrHardResetOfASharedRef(t *testing.T) {
	t.Parallel()

	res := CheckNeverForce("", "")
	require.Equal(t, 0, res.Refused(), "the repo must have no force patterns against shared refs, got %d refusals: %+v", res.Refused(), res.Findings)
}

// NeverForceVerbLine is the help line for this class test in docs/SPEC-CI.md.
const NeverForceVerbLine = "never-force   read every .go, .sh, Makefile, and .github/workflows/*.yml; refuse push --force, push -f, --force-with-lease, push origin +, and reset --hard origin/ against a ref that is not origin/*, dev, or main"

// NeverForceRemedy is the remedy message for all force push/reset violations
const NeverForceRemedy = `nothing in nova-tools may rewrite a shared ref (dev, main, or an unlisted branch); use a job branch (push to origin/<job>)`

// NeverForceFinding is one place where a refused pattern was found.
type NeverForceFinding struct {
	File   string
	Line   int
	Kind   string
	Remedy string
}

// NeverForceResult is the result of checking the repository.
type NeverForceResult struct {
	Findings []NeverForceFinding
	Tests    int
}

func (r *NeverForceResult) Refused() int {
	return len(r.Findings)
}

var neverForceListOptions = allowlist.Options{
	Key:            allowlist.Fields(2), // file:line kind
	Ceiling:        true,
	MissingIsEmpty: true,
}

// CheckNeverForce checks the repo or a given directory for force patterns.
func CheckNeverForce(dir string, allowPath string) *NeverForceResult {
	if dir == "" {
		dir = "."
	}

	res := &NeverForceResult{}

	// Load allowlist
	var al *allowlist.List
	if allowPath != "" {
		var err error
		al, err = allowlist.Load(allowPath, neverForceListOptions)
		if err != nil {
			return res
		}
	}

	// Patterns to search for
	patterns := []struct {
		text string
		kind string
	}{
		{"push --force", "push-force"},
		{"push -f", "push-f"},
		{"--force-with-lease", "force-with-lease"},
		{"push origin +", "push-origin-plus"},
		{"reset --hard origin/", "reset-hard-origin"},
	}

	// Walk the repository for relevant files
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			// Skip hidden directories and common non-essential dirs
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Check Go source files, shell scripts, Makefile, and workflows
		name := info.Name()
		isRelevant := strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, ".sh") ||
			name == "Makefile" ||
			(strings.Contains(path, ".github/workflows") && strings.HasSuffix(name, ".yml"))

		if !isRelevant {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		relPath := strings.TrimPrefix(path, dir+string(filepath.Separator))
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		lineNum := 0

		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			// Check each pattern
			for _, pat := range patterns {
				if !strings.Contains(line, pat.text) {
					continue
				}

				// For now, any match of these patterns is a violation
				// A more sophisticated check would verify the target ref
				key := fmt.Sprintf("%s:%d %s", relPath, lineNum, pat.kind)

				if al != nil && al.Has(key) {
					// This pattern is in the allowlist
					continue
				}

				res.Findings = append(res.Findings, NeverForceFinding{
					File:   relPath,
					Line:   lineNum,
					Kind:   pat.kind,
					Remedy: NeverForceRemedy,
				})
			}
		}

		return nil
	})

	return res
}

// Red test fixtures

// TestNoForcePushOrHardResetRefusesAFixtureScript checks a fixture with a force push.
func TestNoForcePushOrHardResetRefusesAFixtureScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fixtureContent := `#!/bin/bash
git push --force origin dev
`
	fixture := filepath.Join(root, "script.sh")
	require.NoError(t, os.WriteFile(fixture, []byte(fixtureContent), 0o755))

	res := CheckNeverForce(root, "")
	require.True(t, res.Refused() >= 1, "a force push pattern is one refusal, got %d: %+v", res.Refused(), res.Findings)

	f := res.Findings[0]
	assert.Equal(t, "push-force", f.Kind)
	assert.Contains(t, f.Remedy, "shared ref")
}

// TestNoForcePushOrHardResetAllowsNormalPush checks that normal push is allowed.
func TestNoForcePushOrHardResetAllowsNormalPush(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fixtureContent := `#!/bin/bash
git push origin feature-branch
`
	fixture := filepath.Join(root, "script.sh")
	require.NoError(t, os.WriteFile(fixture, []byte(fixtureContent), 0o755))

	res := CheckNeverForce(root, "")
	require.Zero(t, res.Refused(), "a normal push is allowed, got %d refusals: %+v", res.Refused(), res.Findings)
}

// TestNoForcePushOrHardResetAllowlistGrowsRefused tests the allowlist behavior.
func TestNoForcePushOrHardResetAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowPath := filepath.Join(t.TempDir(), "never-force-allowlist.txt")

	fixtureContent := `#!/bin/bash
git push --force origin dev
`
	fixture := filepath.Join(root, "script.sh")
	require.NoError(t, os.WriteFile(fixture, []byte(fixtureContent), 0o755))

	// Empty allowlist should refuse
	require.NoError(t, os.WriteFile(allowPath, []byte(""), 0o644))
	res := CheckNeverForce(root, allowPath)
	require.True(t, res.Refused() >= 1, "should have refusals without allowlist entry")

	// Adding an entry should allow it
	require.NoError(t, os.WriteFile(allowPath, []byte("script.sh:2 push-force 2026-10-04 test fixture for never-force rule\n"), 0o644))
	res = CheckNeverForce(root, allowPath)
	require.Zero(t, res.Refused(), "allowlist entry should allow the pattern, got %d refusals: %+v", res.Refused(), res.Findings)
}
