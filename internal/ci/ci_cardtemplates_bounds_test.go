package ci

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// attachedOrFallback handles input starting with '&' without panic from negative indexing.
func TestAttachedOrFallbackLeadingAmpersand(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		rest string
		want bool
	}{
		{"&", false},
		{"&>out", false},
		{"&>out || true", true},
	} {
		assert.Equal(t, c.want, attachedOrFallback(c.rest), "attachedOrFallback(%q)", c.rest)
	}
}
