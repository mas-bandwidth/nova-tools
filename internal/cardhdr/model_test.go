package cardhdr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The one parser of a brief's model lines: line 1's tier, and a model: pin under
// it with its optional tokens: and deadline:; a deadline: with no pin is the
// brief's own words, never read.
func TestReadModelReadsTheTierAndThePin(t *testing.T) {
	t.Parallel()
	five, half := 300, 1800 // a pin's deadline in seconds: 5m, and 1800 as written
	for _, c := range []struct {
		brief string
		want  Model
		why   string
	}{
		{"c1: do it (s1) tier: pro\nREPO: o/r\n\nThe task.", Model{Tier: "pro"}, ""},
		{"c1: do it (s1)\n\nThe task.", Model{}, ""},
		{"c1: do it tier: pro\nmodel: anthropic/claude-x\ntokens: 5000\ndeadline: 5m\n\nThe task.", Model{Tier: "pro", Pin: "anthropic/claude-x", Tokens: "5000", Deadline: five}, ""},
		{"c1: do it tier: frontier\nMODEL: openrouter/x-ai/grok-4\nTokens: unmetered\nDeadline: 1800\n", Model{Tier: "frontier", Pin: "openrouter/x-ai/grok-4", Tokens: "unmetered", Deadline: half}, ""},
		{"c1: do it tier: flash\ndeadline: finish within 30 minutes\n\nThe task.", Model{Tier: "flash"}, ""},
		{"c1: do it tier: pro\n\nmodel: a/b is prose here, under the blank line", Model{Tier: "pro"}, ""},
		{"c1: do it tier: medium", Model{}, "line 1 names tier medium"},
		{"c1: do it tier: pro\nmodel: claude-x", Model{}, "is not <provider>/<model>"},
		{"c1: do it tier: pro\nmodel: a/b\ntokens: lots\ndeadline: 0", Model{}, "tokens: lots is not a count"},
	} {
		got, why := ReadModel(c.brief)
		assert.Equal(t, c.want, got, c.brief)
		assert.Equal(t, c.why == "", why == "", "%q: %q", c.brief, why)
		assert.Contains(t, why, c.why, c.brief)
	}
}
