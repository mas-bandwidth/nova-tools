package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFsckFindsLandedRecordsMissingFromTheBase checks the landed-on-base fsck check
// on the twin store with a fake git interface. The fixture has two landed cards whose
// heads are not on the base. fsck prints one FSCK VIOLATION line for each, names the
// fix verb, and exits 1. A clean store prints FSCK OK and exits 0.
func TestFsckFindsLandedRecordsMissingFromTheBase(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1"})
	// Create two landed cards with heads not on base
	pr1 := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "REPO: example.com/foo\nBASE: main\nHEAD: abc123456789",
		"head":  "abc123456789",
	}}
	pr2 := &Card{ID: "s1-2", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "REPO: example.com/bar\nBASE: main\nHEAD: def987654321",
		"head":  "def987654321",
	}}
	w.s.Work.Put(pr1)
	w.s.Work.Put(pr2)

	// fake git: ls-remote returns a tip, merge-base returns exit 1 (not ancestor)
	runGit := func(cmd string, args ...string) (int, string, string) {
		if len(args) >= 2 && args[0] == "ls-remote" && args[2] == "main" {
			return 0, "1234567890abcdef refs/heads/main\n", ""
		}
		if len(args) >= 2 && args[0] == "merge-base" && args[1] == "--is-ancestor" {
			return 1, "", "" // not ancestor
		}
		return 0, "", ""
	}

	findings := LandOnBase(w.s, nil, runGit)
	require.Len(t, findings, 2)
	for _, f := range findings {
		assert.Equal(t, FsckCheckName, f.Check)
		assert.False(t, f.Ancestor)
		assert.Contains(t, f.Line(), "FSCK VIOLATION")
		assert.Contains(t, f.Line(), "reopen")
	}
}

// TestFsckOkWhenAllLandedOnBase checks that fsck reports OK when all landed records are on base.
func TestFsckOkWhenAllLandedOnBase(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1"})
	// Create a landed card whose head IS on base
	pr := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "REPO: example.com/foo\nBASE: main\nHEAD: 1234567890abcdef",
		"head":  "1234567890abcdef",
	}}
	w.s.Work.Put(pr)

	// fake git: merge-base returns exit 0 (is ancestor)
	runGit := func(cmd string, args ...string) (int, string, string) {
		if len(args) >= 3 && args[0] == "ls-remote" && args[2] == "main" {
			return 0, "1234567890abcdef refs/heads/main\n", ""
		}
		if len(args) >= 2 && args[0] == "merge-base" && args[1] == "--is-ancestor" {
			return 0, "", "" // is ancestor
		}
		return 0, "", ""
	}

	findings := LandOnBase(w.s, nil, runGit)
	require.Len(t, findings, 1)
	assert.True(t, findings[0].Ancestor)
	assert.Contains(t, findings[0].Line(), "FSCK OK")
}

// TestFsckCheckFilter verifies the --check flag filters checks properly.
func TestFsckCheckFilter(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "REPO: example.com/foo\nBASE: main\nHEAD: abc123456789",
		"head":  "abc123456789",
	}}
	w.s.Work.Put(pr)
	runGit := func(cmd string, args ...string) (int, string, string) {
		if len(args) >= 3 && args[0] == "ls-remote" {
			return 0, "1234567890abcdef refs/heads/main\n", ""
		}
		if len(args) >= 3 && args[0] == "merge-base" && args[1] == "--is-ancestor" {
			return 1, "", "" // not ancestor
		}
		return 0, "", ""
	}

	// empty checks means all checks
	findings := LandOnBase(w.s, nil, runGit)
	require.Len(t, findings, 1)

	// filtering to a non-existent check returns nothing
	findings = LandOnBase(w.s, []string{"other-check"}, runGit)
	require.Len(t, findings, 0)

	// filtering to landed-on-base returns the finding
	findings = LandOnBase(w.s, []string{FsckCheckName}, runGit)
	require.Len(t, findings, 1)
}
