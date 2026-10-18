package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/profiles"
)

// The darwin wall's denies are NO-REPORT: a reporting deny hung a walled Bun harness
// (opencode 1.18.20) forever on most cold starts on a darwin/arm64 bench (macOS 27.0.1,
// measured 2026-10-18), the denied thread waiting on a violation report that never came.

// The template carries exactly one default rule, and it is the no-report deny: neither a
// reporting deny nor an allow default passes.
func TestDarwinTemplateDefaultDenyIsNoReport(t *testing.T) {
	t.Parallel()
	var defaults []string
	for _, line := range strings.Split(profiles.DarwinTemplate, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "(deny default") || strings.HasPrefix(line, "(allow default") {
			defaults = append(defaults, line)
		}
	}
	assert.Equal(t, []string{"(deny default (with no-report))"}, defaults, "the darwin template's default rule")
}

// Every deny in the FILLED profile is no-report: the --read-noexec exec deny is emitted after
// the template's default deny, so it is checked in the filled text, not the template alone.
func TestDarwinProfileEveryDenyIsNoReport(t *testing.T) {
	t.Parallel()
	p := &Policy{
		Reads:       []string{"/wall/read"},
		ReadsNoExec: []string{"/wall/noexec"},
		Writes:      []string{"/wall/job"},
		Tmp:         "/wall/job/tmp",
		Cwd:         "/wall/job",
		Home:        "/wall/job",
	}
	text, _, err := DarwinProfile(p)
	require.NoError(t, err)
	var denies []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "(deny ") {
			denies = append(denies, line)
		}
	}
	require.Len(t, denies, 2, "the filled profile carries the default deny and the --read-noexec exec deny: %v", denies)
	for _, d := range denies {
		assert.True(t, strings.HasSuffix(d, "(with no-report))"), "a reporting deny in the filled darwin profile: %s", d)
	}
}
