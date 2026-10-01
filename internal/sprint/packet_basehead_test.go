package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A later attempt starts from the head the attempt before finished ok at,
// never from a branch name alone: an attempt that failed, or finished at no
// commit, leaves no base head, and the attempt is staged from the card's base
// (docs/SPEC-CARD-CONTRACT.md layer 1).
func TestALaterAttemptStartsFromThePreviousPushedHead(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	work := &Card{ID: "p1.w2", Row: "m1", Fields: map[string]string{"kind": "work", "primary": "p1", "attempt": "2", "gen": "1"}}
	primary := &Card{ID: "p1", Fields: map[string]string{"attempt": "2", "brief": "b", "fix": "f.go:3 the bound"}}
	for _, tc := range []struct {
		name     string
		prev     map[string]string
		baseHead string
	}{
		{"finished ok at a pushed head", map[string]string{"ok": "yes", "head": sha, "branch": "sprint/p1.w1"}, sha},
		{"finished failed", map[string]string{"ok": "no", "head": sha, "branch": "sprint/p1.w1"}, ""},
		{"finished at no commit", map[string]string{"ok": "yes", "head": "p1.w1"}, ""},
	} {
		prev := &Card{ID: "p1.w1", Fields: tc.prev}
		p := PacketOf("", 3, work, primary, prev, nil)
		assert.Equal(t, tc.baseHead, p.BaseHead, tc.name)
		assert.Equal(t, "sprint/p1.w2", p.Branch, tc.name)
		assert.Equal(t, "f.go:3 the bound", p.Fix, tc.name)
	}
}
