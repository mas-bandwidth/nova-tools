package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// THE CLASS RULE BEHIND A SKIPPED TRANSCRIPT (SPEC-TOOLWORK §7 rule 5).
//
// A `Platform: <goos>[,<goos>]` line under a `## <tool>` heading makes that
// section's transcript test a NAMED SKIP everywhere else. A skip is a hole, and a
// hole is only sound while something else fills it, so the line is checked
// against the legs `ci.yml` actually runs: a skipped transcript is still executed
// somewhere. A line naming a platform CI does not run is a transcript nobody
// runs at all, which reads in a green build exactly like one everybody runs.
//
// #1509 is the case this closes. `nova-sandbox probe` prints
// `backend=sandbox-exec` and an empty `abi=` on macOS, and `backend=landlock`
// with `hosts=`, `gpu=`, `used=` and `ancestors=` on Linux; the section's line
// said so in prose, which no test could act on and no class test could check.
//
// The legs are read out of `.github/workflows/ci.yml` rather than listed here, so
// that a leg added or dropped moves this rule with it. Windows is the reason that
// matters today: every `windows-latest` leg was dropped on 2026-09-18 (Glenn:
// "drop the native windows CI runners. WSL only from now on."), leaving a
// `GOOS=windows go vet` cross-check and no leg that RUNS anything. A
// `Platform: windows` line would therefore be a transcript no bench executes, and
// this test says so.

// platformLegAllowlistPath is the shrink-only list of `## <tool>` sections whose
// `Platform:` line does not parse as a GOOS list yet.
const platformLegAllowlistPath = "testdata/platform_line_allowlist.txt"

func TestPlatformLineMustNameACILeg(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
	allow := readAllowlist(t, platformLegAllowlistPath)
	legs := ciLegs(t, root)

	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	sections, parsed := 0, map[string]bool{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tool := e.Name()
		if _, ok := onboarding.Section(md, tool); !ok {
			continue
		}
		sections++
		p, found, err := onboarding.SectionPlatforms(md, tool)
		switch {
		case !found:
			continue
		case err != nil:
			if !allow[tool] {
				violations = append(violations, fmt.Sprintf(
					"%v\nA test cannot skip on a sentence and this rule cannot check one. Re-cut the line, or list %s in %s with the issue that owes it",
					err, tool, platformLegAllowlistPath))
			}
			continue
		}
		parsed[tool] = true
		if allow[tool] {
			continue
		}
		for _, goos := range p.GOOS {
			if legs[goos] {
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"the `## %s` section is recorded for %q and no leg of .github/workflows/ci.yml runs %s (the legs are: %s); its transcript would be skipped on every bench, which is a transcript nobody executes",
				tool, goos, goos, strings.Join(sortedKeys(legs), ", ")))
		}
		if p.Note == "" {
			violations = append(violations, fmt.Sprintf(
				"the `## %s` section's platform line names %s and says nothing after the dash; a reader on another bench is told which benches and never WHY, and the why is what tells them whether their own output is drift or a different platform",
				tool, p.Names()))
		}
	}

	if sections == 0 {
		t.Fatal("no `## <tool>` sections found in docs/TESTS.md; this walk was looking in the wrong place and would have passed by checking nothing")
	}
	// The list only shrinks: a section whose line now parses may not stay listed.
	for tool := range allow {
		if parsed[tool] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, and its platform line now parses as a GOOS list; delete the stale entry (the list only shrinks)",
				platformLegAllowlistPath, tool))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// ciLegs reads the platforms .github/workflows/ci.yml runs a suite on, off the
// merge gate's `leg:` matrix, whose entries are named by GOOS -- `- name: linux`,
// `- name: darwin`. That matrix is the file's own word for a leg and is where a
// platform is added to or dropped from this repository's CI.
func ciLegs(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	legs := map[string]bool{}
	inMatrix, indent := false, 0
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		depth := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "leg:" {
			inMatrix, indent = true, depth
			continue
		}
		if !inMatrix {
			continue
		}
		if depth <= indent {
			inMatrix = false
			continue
		}
		if name, ok := strings.CutPrefix(trimmed, "- name:"); ok {
			legs[strings.TrimSpace(name)] = true
		}
	}
	if len(legs) == 0 {
		t.Fatal(".github/workflows/ci.yml names no `leg:` matrix entries; this test was looking in the wrong place and would have passed by checking nothing")
	}
	return legs
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
