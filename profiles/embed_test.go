package profiles

import (
	"strings"
	"testing"
)

// The darwin wall's one default deny is NO-REPORT. A reporting deny hung a walled Bun
// harness (opencode 1.18.20) forever on most cold starts on a darwin/arm64 bench (macOS 27.0.1,
// measured 2026-10-18): the denied thread waited on a violation report that never came.
// The deny itself must stay: this test refuses both a reporting deny and an allow default.
func TestDarwinTemplateDefaultDenyIsNoReport(t *testing.T) {
	t.Parallel()
	var defaults []string
	for _, line := range strings.Split(DarwinTemplate, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, ";;") {
			continue
		}
		if strings.HasPrefix(line, "(deny default") || strings.HasPrefix(line, "(allow default") {
			defaults = append(defaults, line)
		}
	}
	if len(defaults) != 1 || defaults[0] != "(deny default (with no-report))" {
		t.Fatalf("the darwin template's default rule is %q; want exactly one (deny default (with no-report))", defaults)
	}
}
