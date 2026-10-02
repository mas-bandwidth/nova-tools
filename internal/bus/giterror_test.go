package bus

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/stretchr/testify/assert"
)

// ONE LINE IS NOT ONE BOUNDED LINE. Escape already folded git's output onto a single
// line; nothing bounded how long that line was, and git is verbose exactly when it fails.
func TestGitErrorCapsTheEmbeddedOutput(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("hint: a whole paragraph of git advice\n", 4000) // ~150 KB
	g := &gitError{args: []string{"push", "origin", "main"}, err: errors.New("exit status 1"), output: huge}

	text := g.Error()
	assert.LessOrEqual(t, len(text), gitOutputCap+200,
		"an error carrying %d bytes of git output renders %d bytes; the ceiling is %d",
		len(huge), len(text), gitOutputCap)
	if !strings.HasPrefix(text, "git push origin main: exit status 1: hint:") {
		assert.False(t, !strings.HasPrefix(text, "git push origin main: exit status 1: hint:"), "the head of the error is gone: %q", text[:60])
	}
	assert.Contains(t, text, "...+", "the cut is not marked, so a reader cannot tell there was more: %q", text)
	assert.NotContains(t, oneline.Err(g), "\n", "the rendered error is not one line")
}

// Under the ceiling nothing changes: the reason a person needs is the whole of what git
// said, and the overwhelming majority of git failures say it in a line or two.
func TestGitErrorLeavesShortOutputAlone(t *testing.T) {
	t.Parallel()
	g := &gitError{
		args:   []string{"fetch", "origin"},
		err:    errors.New("exit status 128"),
		output: "fatal: could not read from remote repository",
	}
	want := "git fetch origin: exit status 128: fatal: could not read from remote repository"
	got := g.Error()
	assert.Equal(t, want, got, "got  %q\nwant %q", got, want)
}

// An empty output keeps its own shape: a colon with nothing after it says less than
// nothing.
func TestGitErrorWithNoOutput(t *testing.T) {
	t.Parallel()
	g := &gitError{args: []string{"status"}, err: errors.New("exit status 1"), output: "   \n  "}
	got := g.Error()
	assert.Equal(t, "git status: exit status 1", got, "got %q", got)
}
