package cardhdr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A brief's tier is a header field read by the one grammar, `tier:` like `REPO:`
// (base.go Field): it stands on a line of its own anywhere in the header, and the
// line-1 words a brief led with still name it where they carry `tier:` (the
// pre-2026-10-07 form the existing briefs use).
func TestTierIsAHeaderField(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, brief, want string
	}{
		{"a field on its own line", "c1: the work\nREPO: o/r\ntier: heavy\n\nThe task.", "heavy"},
		{"a field as line 1", "tier: pro\nREPO: o/r\n\nThe task.", "pro"},
		{"line 1's own words still name it", "c1: do it tier: frontier\n\nThe task.", "frontier"},
		{"the field wins over line 1", "c1: do it tier: pro\ntier: heavy\n\nThe task.", "heavy"},
		{"named nowhere", "c1: the work\n\nThe task.", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m, why := ReadModel(c.brief)
			assert.Empty(t, why, c.brief)
			assert.Equal(t, c.want, m.Tier, c.brief)
		})
	}
}

// RepoAlone says a REPO value stands alone: one word and nothing after it. A REPO
// line with trailing words is refused by the card lint and never resolved by the
// lander (fix 3 of the defects of 2026-10-07).
func TestRepoAloneRefusesTrailingWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"o/r", true},
		{"o/r.git", true},
		{"mas-bandwidth/nova-tools", true},
		{"-", true},
		{"o/r tier: heavy", false},
		{"o/r extra", false},
		{"", false},
	} {
		assert.Equal(t, c.want, RepoAlone(c.v), "%q", c.v)
	}
}
