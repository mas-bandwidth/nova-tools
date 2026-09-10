package bus

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ONE LINE IS NOT ONE BOUNDED LINE. Escape already folded git's output onto a single
// line; nothing bounded how long that line was, and git is verbose exactly when it fails.
func TestGitErrorCapsTheEmbeddedOutput(t *testing.T) {
	huge := strings.Repeat("hint: a whole paragraph of git advice\n", 4000) // ~150 KB
	g := &gitError{args: []string{"push", "origin", "main"}, err: errors.New("exit status 1"), output: huge}

	text := g.Error()
	if len(text) > gitOutputCap+200 {
		t.Errorf("an error carrying %d bytes of git output renders %d bytes; the ceiling is %d",
			len(huge), len(text), gitOutputCap)
	}
	if !strings.HasPrefix(text, "git push origin main: exit status 1: hint:") {
		t.Errorf("the head of the error is gone: %q", text[:60])
	}
	if !strings.Contains(text, "...+") {
		t.Errorf("the cut is not marked, so a reader cannot tell there was more: %q", text)
	}
	if strings.Contains(oneline.Err(g), "\n") {
		t.Errorf("the rendered error is not one line")
	}
}

// Under the ceiling nothing changes: the reason a person needs is the whole of what git
// said, and the overwhelming majority of git failures say it in a line or two.
func TestGitErrorLeavesShortOutputAlone(t *testing.T) {
	g := &gitError{
		args:   []string{"fetch", "origin"},
		err:    errors.New("exit status 128"),
		output: "fatal: could not read from remote repository",
	}
	want := "git fetch origin: exit status 128: fatal: could not read from remote repository"
	if got := g.Error(); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// An empty output keeps its own shape: a colon with nothing after it says less than
// nothing.
func TestGitErrorWithNoOutput(t *testing.T) {
	g := &gitError{args: []string{"status"}, err: errors.New("exit status 1"), output: "   \n  "}
	if got := g.Error(); got != "git status: exit status 1" {
		t.Errorf("got %q", got)
	}
}
