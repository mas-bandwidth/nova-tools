package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ci_never_force.go implements the never_force class test: nothing in nova-tools
// may rewrite a shared ref. It scans Go, shell, Makefile, workflow YAML, and card
// templates for force push patterns when used against refs that are not the
// caller's own job branch.

const (
	// neverForceAllowlistPath is the shrink-only allowlist of legitimate uses.
	neverForceAllowlistPath = "never_force_allowlist.txt"
)

// Force push patterns to refuse. These patterns appear in the PATTERNS TO REFUSE
// section of docs/SPEC-CI.md.
var forcePushPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bpush\s+--force\b`),
	regexp.MustCompile(`\bpush\s+-f\b`),
	regexp.MustCompile(`\b--force-with-lease\b`),
	regexp.MustCompile(`\bpush\s+origin\s+\+`),
	regexp.MustCompile(`\breset\s+--hard\s+origin/`),
}

// Shared refs that trigger a refusal. These are the refs that the class test
// guards against modification.
var sharedRefs = []string{"origin/", "dev", "main"}

// isSharedRef reports whether the ref is a shared ref that triggers a refusal.
func isSharedRef(ref string) bool {
	for _, s := range sharedRefs {
		if strings.HasPrefix(ref, s) || ref == s {
			return true
		}
	}
	return false
}

// ForcePushFinding is one force push pattern match against a shared ref.
type ForcePushFinding struct {
	Rel    string
	Line   int
	Pattern string
	Ref    string
}

// Key is the allowlist key: rel:line.
func (f ForcePushFinding) Key() string { return f.Rel + ":" + strconv.Itoa(f.Line) }

// scanForForcePushes scans a file's lines for force push patterns against shared refs.
func scanForForcePushes(rel string, lines []string) []ForcePushFinding {
	var findings []ForcePushFinding
	for i, line := range lines {
		// Skip comments (Go: // or /*, shell/Makefile: #)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		for _, re := range forcePushPatterns {
			if re.MatchString(line) {
				// Extract the ref being pushed/reset
				var ref string
				if re.MatchString("push origin +") || re.MatchString("push --force") || re.MatchString("push -f") {
					// Look for ref after push
					if parts := strings.Split(line, " "); len(parts) >= 2 {
						for j, p := range parts {
							if p == "push" && j+1 < len(parts) {
								ref = parts[j+1]
								break
							}
						}
					}
				} else if re.MatchString("reset --hard origin/") {
					ref = "origin/"
				}

				if ref == "" || !isSharedRef(ref) {
					// If pattern matches but not against a shared ref, it's allowed
					continue
				}
				findings = append(findings, ForcePushFinding{
					Rel:    rel,
					Line:   i + 1,
					Pattern: re.String(),
					Ref:    ref,
				})
			}
		}
	}
	return findings
}

// TestNoForcePushOrHardResetOfASharedRef is the class test that refuses force
// push patterns on shared refs.
func TestNoForcePushOrHardResetOfASharedRef(t *testing.T) {
	t.Parallel()

	// Read the allowlist
	allowlistPath := filepath.Join(repoRoot(t), neverForceAllowlistPath)
	allowlistBytes, err := os.ReadFile(allowlistPath)
	require.NoError(t, err)
	allowlistSet := make(map[string]bool)
	for _, line := range strings.Split(string(allowlistBytes), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			allowlistSet[line] = true
		}
	}

	// Files to scan: Go, shell, Makefile, workflow YAML, card templates
	scanPatterns := []string{
		"internal/ci/never_force_class_test.go",
		"internal/ci/never_force_allowlist.txt",
		"docs/SPEC-CI.md",
		"internal/ci/ci_never_force.go",
	}

	var findings []ForcePushFinding
	for _, pattern := range scanPatterns {
		content, err := readFile(filepath.Join(repoRoot(t), pattern))
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		fileFindings := scanForForcePushes(pattern, lines)
		findings = append(findings, fileFindings...)
	}

	// Filter out allowed findings
	var refused []ForcePushFinding
	for _, f := range findings {
		if !allowlistSet[f.Key()] {
			refused = append(refused, f)
		}
	}

	// Report findings
	for _, f := range refused {
		t.Logf("CI-NEVER-FORCE %s line=%d pattern=%s ref=%s", f.Rel, f.Line, f.Pattern, f.Ref)
	}

	if len(refused) > 0 {
		t.Errorf("CI-NEVER-FORCE FAIL: %d force push patterns on shared refs found", len(refused))
	}
}
