package sandbox

import (
	"strings"
	"testing"
)

// Every deny the filled darwin profile carries is no-report: a reporting deny hung a walled
// Bun harness forever on macOS 27.0.1 (a darwin/arm64 bench, 2026-10-18), the denied thread waiting on
// a violation report that never came. The --read-noexec exec deny is emitted here, after the
// template's default deny, so it is checked in the filled text, not the template alone.
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
	if err != nil {
		t.Fatalf("DarwinProfile: %v", err)
	}
	denies := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "(deny ") {
			continue
		}
		denies++
		if !strings.HasSuffix(line, "(with no-report))") {
			t.Errorf("a reporting deny in the filled darwin profile: %s", line)
		}
	}
	if denies < 2 {
		t.Fatalf("the filled profile carries %d deny lines; want the default deny and the --read-noexec exec deny", denies)
	}
}
