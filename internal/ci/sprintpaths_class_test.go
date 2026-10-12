package ci

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// aiSiblingPathRe finds the old top-level sibling layout: a Go expression that
// joins a name with the literal "-working" (for example seat+"-working", or
// name+"-working/.cache/go-build"), which built <root>/<name>-working beside
// <root>/ai/. Every path nova tools build for an AI now lives under
// <root>/ai/<name>/ (docs/FRIENDS.md, the ai/ layout), so the old join is banned
// from non-test code. The literal may carry a suffix path after "-working"
// (a closing quote or a "/"), so the scan stops at the component, not the quote.
var aiSiblingPathRe = regexp.MustCompile(`\+\s*"-working[/"]`)

// TestNoAISiblingWorkingPaths is the class rule that a non-test Go file never
// builds a <name>-working sibling path at the ai/ top level: the working
// directory is <root>/ai/<name>/working and its inbox is under ai/<name>/
// (docs/FRIENDS.md, the ai/ layout). The scan looks for the join itself — a
// name concatenated with "-working" — so a rename of the layout leaves a red
// run here instead of a new <name>-working sibling in the file system.
func TestNoAISiblingWorkingPaths(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var bad []string
	for _, f := range tree.Files {
		if !f.Go || f.Test {
			continue
		}
		for line, text := range strings.Split(string(f.Src), "\n") {
			if aiSiblingPathRe.MatchString(text) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s", f.Rel, line+1, strings.TrimSpace(text)))
			}
		}
	}
	assert.Empty(t, bad, "non-test Go builds the old top-level <name>-working sibling path; move it under ai/<name>/:\n%s", strings.Join(bad, "\n"))
}
