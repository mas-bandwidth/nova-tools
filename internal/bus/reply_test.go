package bus

import "testing"

// What "the same subject" is, at the unit that decides it. The rule is exact and
// case-sensitive after a leading Re: comes off, and every line below is one shape a
// hand-written reply's subject actually arrives in.
func TestReplySubjectStripsThePrefixAndNothingElse(t *testing.T) {
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
		if got := ReplySubject(tc.in); got != tc.want {
			t.Errorf("ReplySubject(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The trigger for the NOTE is the loose half, on purpose: it costs a reader one line they
// can ignore, and missing it costs them an open note for ever.
func TestIsReplySubjectIsTheLooseHalf(t *testing.T) {
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
		if got := IsReplySubject(tc.in); got != tc.want {
			t.Errorf("IsReplySubject(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Newest first, unreadable entries skipped, and nothing matched by a subject that is not
// the same subject.
func TestMatchOpenSubjectTakesTheNewestOfTheExactMatches(t *testing.T) {
	open := []OpenEntry{
		{ID: "bo-000000000001", Kind: OpenNote, From: "Bo", Date: "2026-09-07T00:00:00Z", Path: "from-bo/a.md", Subject: "the gate"},
		{ID: "bo-000000000002", Kind: OpenNote, From: "Bo", Date: "2026-09-09T00:00:00Z", Path: "from-bo/b.md", Subject: "Re: the gate"},
		{ID: "bo-000000000003", Kind: OpenNote, From: "Bo", Date: "2026-09-08T00:00:00Z", Path: "from-bo/c.md", Subject: "The Gate"},
		{Kind: OpenUnreadable, Path: "from-bo/d.md"},
	}
	got := MatchOpenSubject(open, "the gate")
	if len(got) != 2 {
		t.Fatalf("matched %d entries, want 2: %+v", len(got), got)
	}
	if got[0].ID != "bo-000000000002" {
		t.Fatalf("the newest match is %s, want bo-000000000002", got[0].ID)
	}
	if n := len(MatchOpenSubject(open, "The Gate")); n != 1 {
		t.Fatalf("a subject differing by case matched %d entries, want its own 1", n)
	}
	if n := len(MatchOpenSubject(open, "nothing anybody wrote")); n != 0 {
		t.Fatalf("a subject nobody wrote matched %d entries", n)
	}
	if n := len(MatchOpenSubject(open, "   ")); n != 0 {
		t.Fatalf("an empty subject matched %d entries; it must match nothing", n)
	}
}

// The narrow half of the guess: one recipient, and they are holding something of yours.
func TestSingleOpenSenderIsOneRecipientWhoIsWaiting(t *testing.T) {
	root := writeBus(t, nil)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	open := []OpenEntry{{ID: "bo-000000000001", Kind: OpenNote, From: "Bo", Path: "from-bo/a.md", Subject: "s"}}
	if got := SingleOpenSender(c, "Bo", open); got != "Bo" {
		t.Fatalf("SingleOpenSender(Bo) = %q, want Bo", got)
	}
	if got := SingleOpenSender(c, "Bo; Dana", open); got != "" {
		t.Fatalf("two recipients say nothing about either, got %q", got)
	}
	if got := SingleOpenSender(c, "Dana", open); got != "" {
		t.Fatalf("a recipient with nothing open is not waiting, got %q", got)
	}
	if got := SingleOpenSender(c, "nobody-by-that-name", open); got != "" {
		t.Fatalf("an unresolvable To line resolves to nobody, got %q", got)
	}
	if got := SingleOpenSender(c, "Bo", nil); got != "" {
		t.Fatalf("an empty open list has nobody waiting, got %q", got)
	}
}
