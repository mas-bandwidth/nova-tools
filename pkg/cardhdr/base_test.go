package cardhdr

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A card's header value is read by key through KeyValue, the first line that carries it, and
// a BASE value is a ref, pinned or not: `<ref>@<sha40>` is the ref and its sha, anything after
// an @ that is no full sha is refused.
func TestValueAndParseBaseReadAPinnedBase(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("0123456789abcdef", 3)[:40]
	brief := "STATUS: nova-sprint card c\nWork in the job.\n\nRESULT: c sha= tier: heavy\nREPO:  o/r \nBASE: dev@" + sha + "\nBASE: later\n"
	v, ok := Value(brief, "REPO")
	assert.True(t, ok)
	assert.Equal(t, "o/r", v)
	v, ok = Value(brief, "BASE")
	assert.True(t, ok)
	assert.Equal(t, "dev@"+sha, v, "the first line that carries the key")
	_, ok = Value(brief, "PATHS")
	assert.False(t, ok)

	for _, c := range []struct {
		v, ref, sha string
		ok          bool
	}{
		{"dev@" + sha, "dev", sha, true},
		{"sprint/mechanical-2026-10-02", "sprint/mechanical-2026-10-02", "", true},
		{sha, sha, "", true},
		{"dev@abc123", "", "", false},
		{"dev@" + strings.ToUpper(sha), "", "", false},
		{"@" + sha, "", "", false},
		{"dev@" + sha + "@" + sha, "", "", false},
	} {
		ref, s, ok := ParseBase(c.v)
		assert.Equal(t, []any{c.ref, c.sha, c.ok}, []any{ref, s, ok}, "%q", c.v)
	}
}
