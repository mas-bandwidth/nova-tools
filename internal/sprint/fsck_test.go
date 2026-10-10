package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFsckGit is the git fact seam the landed-on-base check reads in tests: one answer for
// every card, and the repository, base and head each card asked about.
type fakeFsckGit struct {
	Tip   string
	On    bool
	Why   string
	repos []string
	bases []string
	heads []string
}

func (f *fakeFsckGit) ask(repo, base, head string) (string, bool, string) {
	f.repos = append(f.repos, repo)
	f.bases = append(f.bases, base)
	f.heads = append(f.heads, head)
	return f.Tip, f.On, f.Why
}

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
		"brief": "REPO: https://example.invalid/foo.git\nBASE: main\nHEAD: abc123456789",
		"head":  "abc123456789",
	}}
	pr2 := &Card{ID: "s1-2", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "REPO: https://example.invalid/bar.git\nBASE: main\nHEAD: def987654321",
		"head":  "def987654321",
	}}
	w.s.Work.Put(pr1)
	w.s.Work.Put(pr2)

	// fake git: git says each head is not an ancestor of the base tip
	git := &fakeFsckGit{Tip: "1234567890abcdef", On: false}

	findings := LandOnBase(w.s, nil, git.ask)
	require.Len(t, findings, 2)
	for _, f := range findings {
		assert.Equal(t, FsckCheckName, f.Check)
		assert.False(t, f.Ancestor)
		assert.Contains(t, f.Line(), "FSCK VIOLATION")
		assert.Contains(t, f.Line(), "reopen")
	}
	// the check names the repository git can read (the resolved REPO: URL), the base and
	// the head, once per card: the command reads the fact in that repository's clone.
	require.Equal(t, []string{"https://example.invalid/foo.git", "https://example.invalid/bar.git"}, git.repos)
	assert.Equal(t, []string{"main", "main"}, git.bases)
	assert.Equal(t, []string{"abc123456789", "def987654321"}, git.heads)
}

// TestFsckOkWhenAllLandedOnBase checks that fsck reports OK when all landed records are on base.
func TestFsckOkWhenAllLandedOnBase(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1"})
	// Create a landed card whose head IS on base
	pr := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "REPO: https://example.invalid/foo.git\nBASE: main\nHEAD: 1234567890abcdef",
		"head":  "1234567890abcdef",
	}}
	w.s.Work.Put(pr)

	git := &fakeFsckGit{Tip: "1234567890abcdef", On: true}

	findings := LandOnBase(w.s, nil, git.ask)
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
		"brief": "REPO: https://example.invalid/foo.git\nBASE: main\nHEAD: abc123456789",
		"head":  "abc123456789",
	}}
	w.s.Work.Put(pr)
	git := &fakeFsckGit{Tip: "1234567890abcdef", On: false}

	// empty checks means all checks
	findings := LandOnBase(w.s, nil, git.ask)
	require.Len(t, findings, 1)

	// filtering to a non-existent check returns nothing and asks git nothing
	findings = LandOnBase(w.s, []string{"other-check"}, git.ask)
	require.Len(t, findings, 0)
	assert.Len(t, git.heads, 1, "a filtered-out check asks git nothing")

	// filtering to landed-on-base returns the finding
	findings = LandOnBase(w.s, []string{FsckCheckName}, git.ask)
	require.Len(t, findings, 1)
}
