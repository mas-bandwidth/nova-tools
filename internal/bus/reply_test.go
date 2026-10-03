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

// A Re target that matches nothing is refused with WHY the subject did not match (a subject
// is matched only on the reader's open list) and what to write instead: the id of the newest
// note on the bus with that subject, when there is one.
func TestAnUnresolvedReSaysWhyAndNamesTheId(t *testing.T) {
	t.Parallel()
	root := writeBus(t, map[string]string{
		"from-bo/2026-09-07T0001Z-hello-aaaaaaaaaaaa.md": "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-aaaaaaaaaaaa\nSubject: hello\n\nfirst\n",
		"from-bo/2026-09-08T0001Z-hello-bbbbbbbbbbbb.md": "From: Bo\nTo: Ada\nDate: Tue Sep  8 00:01:00 UTC 2026\nId: bo-bbbbbbbbbbbb\nSubject: hello\n\nsecond\n",
	})
	tab := loadBus(t, root)
	for _, tc := range []struct{ name, re, want string }{
		{"a subject on the bus", "Re: hello", "the bus holds 2 note(s) with that subject, the newest bo-bbbbbbbbbbbb; name it by id: Re: bo-bbbbbbbbbbbb"},
		{"a subject nowhere", "nothing like it", "no note on this bus has that subject; name the note by its id, which `nova-bus inbox --open` lists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := UnresolvedRe(tab, "Re:", tc.re)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "no note with that subject is on your open list (the list `inbox --advance` writes")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The newest note with a subject is the newest by date, across lanes: a newer note in
// from-ada/ is named over an older one in from-bo/, though from-bo/ sorts after it.
func TestAnUnresolvedReNamesTheNewestNoteAcrossLanes(t *testing.T) {
	t.Parallel()
	root := writeBus(t, map[string]string{
		"from-bo/2026-09-07T0001Z-hello-bbbbbbbbbbbb.md":  "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-bbbbbbbbbbbb\nSubject: hello\n\nolder, in the later lane\n",
		"from-ada/2026-09-09T0001Z-hello-aaaaaaaaaaaa.md": "From: Ada\nTo: Bo\nDate: Wed Sep  9 00:01:00 UTC 2026\nId: ada-aaaaaaaaaaaa\nSubject: hello\n\nnewer, in the earlier lane\n",
	})
	err := UnresolvedRe(loadBus(t, root), "Re:", "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the bus holds 2 note(s) with that subject, the newest ada-aaaaaaaaaaaa; name it by id: Re: ada-aaaaaaaaaaaa")
}
