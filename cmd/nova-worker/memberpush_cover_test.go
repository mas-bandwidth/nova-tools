package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The unit tier's per-function coverage table showed openPR (memberpush.go:254) at
// 0.0%: no test reached it. openPR's main path runs a real `gh pr create` child, a
// subprocess the unit tier refuses and one whose binary this card may not touch
// (touch no existing file), so the main path is not covered here; the report says
// so. The one path that needs no child is the refusal reached before the exec: gh
// is not on the machine's PATH, and openPR answers with a note naming it. No
// sleep, no clock, no file, no network and no store is touched.
func TestMemberpushCoverOpenPRNoGHOnPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		gh   string
	}{
		{name: "a bare name that is not on the PATH", gh: "nova-no-such-gh-for-memberpush-cover"},
		{name: "a path that does not exist", gh: filepath.Join(t.TempDir(), "no-gh")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := &gitPusher{gh: tc.gh}
			pr, note := g.openPR("https://example.com/o/n.git", "main", "sprint/c1", "a title", "a body")
			assert.Empty(t, pr, "no pull request is opened when gh is absent")
			assert.True(t, strings.HasPrefix(note, "no "+tc.gh+" on this machine's PATH: "),
				"the refusal names the missing binary, got %q", note)
			assert.NotContains(t, note, "\n", "the note is one line")
		})
	}
}
