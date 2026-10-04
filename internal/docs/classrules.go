package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const classRulesDoc = "docs/SPEC-CI.md"
const classTestSection = "## The class tests"
const classRulesStart = "<!-- class-rules:start -->"
const classRulesEnd = "<!-- class-rules:end -->"

var classRuleHeading = regexp.MustCompile("^### `([^`]+)` — (.+)$")

// classRules reads the named entries in SPEC-CI's The class tests section.
// Fenced examples and later sections are not declarations of live rules.
func classRules(spec string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	active, found := false, false
	var fence byte
	width := 0
	for _, line := range strings.Split(spec, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			n := 0
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			if fence == 0 {
				fence, width = trimmed[0], n
			} else if trimmed[0] == fence && n >= width && strings.TrimSpace(trimmed[n:]) == "" {
				fence = 0
			}
			continue
		}
		if fence != 0 {
			continue
		}
		if line == classTestSection {
			if found {
				return nil, fmt.Errorf("duplicate %s section", classTestSection)
			}
			active, found = true, true
			continue
		}
		if !active {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			active = false
			continue
		}
		m := classRuleHeading.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "### `") {
				return nil, fmt.Errorf("malformed class-rule heading %q", line)
			}
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || name != m[1] || strings.TrimSpace(m[2]) == "" {
			return nil, fmt.Errorf("malformed class-rule heading %q", line)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate class rule %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if !found || len(names) == 0 {
		return nil, fmt.Errorf("%s has no named class rules", classTestSection)
	}
	if active && fence != 0 {
		return nil, fmt.Errorf("unclosed fence in %s", classTestSection)
	}
	slices.Sort(names)
	return names, nil
}

// classRuleStandard generates only STANDARD section 12's marked name list;
// the full rule entries and their meaning remain in SPEC-CI.
func classRuleStandard(standard, spec string) (string, error) {
	names, err := classRules(spec)
	if err != nil {
		return "", fmt.Errorf("%s: %w", classRulesDoc, err)
	}
	start, end := "\n"+classRulesStart+"\n", "\n"+classRulesEnd+"\n"
	i, j := strings.Index(standard, start), strings.Index(standard, end)
	if strings.Count(standard, classRulesStart) != 1 || strings.Count(standard, classRulesEnd) != 1 || i < 0 || j < i+len(start)-1 {
		return "", fmt.Errorf("%s needs one ordered pair of standalone %s and %s markers", StandardDoc, classRulesStart, classRulesEnd)
	}
	for n := range names {
		names[n] = "`" + names[n] + "`"
	}
	return standard[:i+len(start)] + "\n" + strings.Join(names, ", ") + ".\n" + standard[j:], nil
}

// readClassRuleStandard supplies make map's STANDARD and root-map outputs
// from the same in-memory generation, so one invocation repairs both.
func readClassRuleStandard(root string) (string, error) {
	standard, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(StandardDoc)))
	if err != nil {
		return "", err
	}
	spec, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(classRulesDoc)))
	if err != nil {
		return "", err
	}
	return classRuleStandard(string(standard), string(spec))
}
