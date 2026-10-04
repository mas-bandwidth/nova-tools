package converge

// forge_cover_test.go reaches the forge seam's small surface: the two pure
// helpers in full, and the three reads' refusal paths. A read's success path
// needs a real `gh` child, which a unit test does not start; that gap is named
// in the card's report, never hidden. Nothing here sleeps, opens a socket or
// starts a process: the missing binary makes exec's own lookup fail before a
// child is forked.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForgeCoverParseForgeTimeReadsRFC3339AndAnswersZero pins the main path (an
// RFC3339 stamp normalized to UTC) and the refusals (empty, padded, garbage and
// a bare date all answer the zero time, never a guess).
func TestForgeCoverParseForgeTimeReadsRFC3339AndAnswersZero(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want time.Time
	}{
		{"empty is zero", "", time.Time{}},
		{"whitespace is zero", "  \t ", time.Time{}},
		{"garbage is zero", "yesterday", time.Time{}},
		{"bare date is zero", "2026-09-18", time.Time{}},
		{"rfc3339 z stays z", "2026-09-18T12:00:00Z", time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)},
		{"rfc3339 offset becomes utc", "2026-09-18T12:00:00+02:00", time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)},
		{"rfc3339 nanos survive", "2026-09-18T12:00:00.5Z", time.Date(2026, 9, 18, 12, 0, 0, 500000000, time.UTC)},
		{"padded rfc3339 is trimmed", "  2026-09-18T12:00:00Z\n", time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)},
	} {
		got := parseForgeTime(tc.in)
		assert.True(t, got.Equal(tc.want), "%s: parseForgeTime(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		assert.Equal(t, time.UTC, got.Location(), "%s: parseForgeTime(%q) kept location %v, want UTC", tc.name, tc.in, got.Location())
	}
}

// TestForgeCoverReadsRefuseAMissingBinary reaches OpenPRs, ClosedSince and list
// through their refusal: with a binary that cannot be found, exec fails its own
// lookup before any child is forked, and each read answers an error naming the
// state it asked for. A deadline of zero also drives list's deadline branch,
// with no clock ever waited on.
func TestForgeCoverReadsRefuseAMissingBinary(t *testing.T) {
	t.Parallel()

	const missing = "nova-converge-no-such-gh"
	since := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		call    func(GH) error
		want    string
	}{
		{
			name:    "OpenPRs",
			timeout: time.Minute,
			call:    func(g GH) error { _, err := g.OpenPRs(context.Background()); return err },
			want:    "gh pr list --state open",
		},
		{
			name:    "ClosedSince",
			timeout: time.Minute,
			call:    func(g GH) error { _, err := g.ClosedSince(context.Background(), since); return err },
			want:    "gh pr list --state all",
		},
		{
			name:    "list",
			timeout: time.Minute,
			call:    func(g GH) error { _, err := g.list(context.Background(), "open", "closed:>=2026-09-18"); return err },
			want:    "gh pr list --state open",
		},
		{
			name:    "list at a past deadline",
			timeout: 0,
			call:    func(g GH) error { _, err := g.list(context.Background(), "open", ""); return err },
			want:    "--timeout",
		},
	} {
		g := GH{Repo: "mas-bandwidth/nova-tools", Timeout: tc.timeout, Bin: missing}
		err := tc.call(g)
		require.Error(t, err, "%s: a missing binary answered no error", tc.name)
		assert.ErrorContains(t, err, tc.want, "%s: the refusal does not name %q: %v", tc.name, tc.want, err)
	}
}
