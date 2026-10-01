package bus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What "the same subject" is, at the unit that decides it. The rule is exact and
// case-sensitive after a leading Re: comes off, and every line below is one shape a
// hand-written reply's subject actually arrives in.
func TestReplySubjectStripsThePrefixAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"the gate", "the gate"},
		{"Re: the gate", "the gate"},
		{"Re:the gate", "the gate"},
		{"Re: Re: the gate", "the gate"},
		{"  Re: the gate  ", "the gate"},
		{"re: the gate", "re: the gate"},       // lower case is a different word here
		{"RE: the gate", "RE: the gate"},       // and so is upper
		{"Reply: the gate", "Reply: the gate"}, // only the exact prefix comes off
		{"", ""},
	} {
		got := ReplySubject(tc.in)
		assert.Equal(t, tc.want, got, "ReplySubject(%q) = %q, want %q", tc.in, got, tc.want)
	}
}

// The trigger for the NOTE is the loose half, on purpose: it costs a reader one line they
// can ignore, and missing it costs them an open note for ever.
func TestIsReplySubjectIsTheLooseHalf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"Re: the gate", true},
		{"re: the gate", true},
		{"RE: the gate", true},
		{"  Re: the gate", true},
		{"the gate", false},
		{"Regarding the gate", false},
		{"", false},
	} {
		got := IsReplySubject(tc.in)
		assert.Equal(t, tc.want, got, "IsReplySubject(%q) = %v, want %v", tc.in, got, tc.want)
	}
}

// Newest first, unreadable entries skipped, and nothing matched by a subject that is not
// the same subject.
func TestMatchOpenSubjectTakesTheNewestOfTheExactMatches(t *testing.T) {
	t.Parallel()
	open := []OpenEntry{
		{ID: "bo-000000000001", Kind: OpenNote, From: "Bo", Date: "2026-09-07T00:00:00Z", Path: "from-bo/a.md", Subject: "the gate"},
		{ID: "bo-000000000002", Kind: OpenNote, From: "Bo", Date: "2026-09-09T00:00:00Z", Path: "from-bo/b.md", Subject: "Re: the gate"},
		{ID: "bo-000000000003", Kind: OpenNote, From: "Bo", Date: "2026-09-08T00:00:00Z", Path: "from-bo/c.md", Subject: "The Gate"},
		{Kind: OpenUnreadable, Path: "from-bo/d.md"},
	}
	got := MatchOpenSubject(open, "the gate")
	require.Len(t, got, 2, "matched %d entries, want 2: %+v", len(got), got)
	require.Equal(t, "bo-000000000002", got[0].ID, "the newest match is %s, want bo-000000000002", got[0].ID)
	n := len(MatchOpenSubject(open, "The Gate"))
	require.Equal(t, 1, n, "a subject differing by case matched %d entries, want its own 1", n)
	n = len(MatchOpenSubject(open, "nothing anybody wrote"))
	require.Zero(t, n, "a subject nobody wrote matched %d entries", n)
	n = len(MatchOpenSubject(open, "   "))
	require.Zero(t, n, "an empty subject matched %d entries; it must match nothing", n)
}

// The narrow half of the guess: one recipient, and they are holding something of yours.
func TestSingleOpenSenderIsOneRecipientWhoIsWaiting(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	c, err := LoadConfig(root)
	require.NoError(t, err)
	open := []OpenEntry{{ID: "bo-000000000001", Kind: OpenNote, From: "Bo", Path: "from-bo/a.md", Subject: "s"}}
	got := SingleOpenSender(c, "Bo", open)
	require.Equal(t, "Bo", got, "SingleOpenSender(Bo) = %q, want Bo", got)
	got = SingleOpenSender(c, "Bo; Dana", open)
	require.Empty(t, got, "two recipients say nothing about either, got %q", got)
	got = SingleOpenSender(c, "Dana", open)
	require.Empty(t, got, "a recipient with nothing open is not waiting, got %q", got)
	got = SingleOpenSender(c, "nobody-by-that-name", open)
	require.Empty(t, got, "an unresolvable To line resolves to nobody, got %q", got)
	got = SingleOpenSender(c, "Bo", nil)
	require.Empty(t, got, "an empty open list has nobody waiting, got %q", got)
}
