package presence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// at is the moment every test in this file starts from; nothing here waits, so
// the clock is the fake store's and a day passes in a function call.
var at = time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)

func TestBeatWritesThePresenceKeyAndItsUntimedMemory(t *testing.T) {
	st := NewFakeStore(at)
	if err := Beat(context.Background(), st, "Johnny", at, DefaultTTL); err != nil {
		t.Fatalf("beat: %v", err)
	}
	vals, err := st.MGet(context.Background(), "friend:johnny", "friend:johnny:last")
	if err != nil {
		t.Fatalf("mget: %v", err)
	}
	want := "2026-09-22T09:41:00Z"
	if vals[0] != want || vals[1] != want {
		t.Fatalf("keys = %q, %q; want both %q (the name is normalized and the value is RFC3339 utc)", vals[0], vals[1], want)
	}
}

func TestTheTTLIsWhatMakesAFriendAway(t *testing.T) {
	st := NewFakeStore(at)
	if err := Beat(context.Background(), st, "emma", at, DefaultTTL); err != nil {
		t.Fatalf("beat: %v", err)
	}

	// One beat old, inside the TTL: up.
	st.Advance(12 * time.Second)
	sts := mustRead(t, st, []string{"emma"})
	if sts[0].State != Up {
		t.Fatalf("12s after a beat: state = %v; want Up", sts[0].State)
	}
	if got := sts[0].Phrase(st.Now()); got != "emma up 12s" {
		t.Fatalf("phrase = %q; want %q", got, "emma up 12s")
	}

	// Past the TTL with nothing written since: the key is gone and the
	// untimed one says when the window was last here. This is the whole
	// mechanism -- no shutdown hook wrote anything.
	st.Advance(DefaultTTL)
	sts = mustRead(t, st, []string{"emma"})
	if sts[0].State != Away {
		t.Fatalf("past the ttl: state = %v; want Away", sts[0].State)
	}
	if got := sts[0].Phrase(st.Now()); got != "emma AWAY 1m (last 09:41Z)" {
		t.Fatalf("phrase = %q; want %q", got, "emma AWAY 1m (last 09:41Z)")
	}
}

func TestTheLineSaysUpAwayAndNone(t *testing.T) {
	st := NewFakeStore(at)
	ctx := context.Background()
	// Emma's window died an hour and twelve minutes ago; johnny and stella
	// are beating; freddy has never had a window up.
	if err := Beat(ctx, st, "emma", at, DefaultTTL); err != nil {
		t.Fatalf("beat emma: %v", err)
	}
	st.Advance(72 * time.Minute)
	if err := Beat(ctx, st, "johnny", st.Now().Add(-12*time.Second), DefaultTTL); err != nil {
		t.Fatalf("beat johnny: %v", err)
	}
	if err := Beat(ctx, st, "stella", st.Now().Add(-4*time.Second), DefaultTTL); err != nil {
		t.Fatalf("beat stella: %v", err)
	}

	sts := mustRead(t, st, []string{"johnny", "stella", "emma", "freddy"})
	got := Line(sts, st.Now())
	want := "friends: johnny up 12s · stella up 4s · emma AWAY 1h12m (last 09:41Z) · freddy none"
	if got != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", got, want)
	}
}

func TestAStoreThatWillNotAnswerIsAnErrorAndNotAnEmptyRoom(t *testing.T) {
	// The one reading this verb must never invent: a store that cannot be
	// read is not four friends who are away.
	st := NewFakeStore(at)
	st.Err = errors.New("dial tcp: connection refused")
	if _, err := Read(context.Background(), st, []string{"johnny"}, at); err == nil {
		t.Fatal("Read returned no error from a store that refused the connection")
	}
	if err := Beat(context.Background(), st, "johnny", at, DefaultTTL); err == nil {
		t.Fatal("Beat returned no error from a store that refused the connection")
	}
}

func TestAKeyWhoseValueIsNotAStampIsStillPresence(t *testing.T) {
	// The key's EXISTENCE is the presence; its value only dates it. A
	// friend running an older beat must not read as away.
	st := NewFakeStore(at)
	if err := st.Set(context.Background(), Key("alex"), "here", DefaultTTL); err != nil {
		t.Fatalf("set: %v", err)
	}
	sts := mustRead(t, st, []string{"alex"})
	if got := sts[0].Phrase(at); got != "alex up" {
		t.Fatalf("phrase = %q; want %q", got, "alex up")
	}
}

func TestBeatRefusesAnEmptyNameOrAnEmptyTTL(t *testing.T) {
	st := NewFakeStore(at)
	if err := Beat(context.Background(), st, "  ", at, DefaultTTL); err == nil {
		t.Fatal("beat accepted an empty name")
	}
	if err := Beat(context.Background(), st, "johnny", at, 0); err == nil {
		t.Fatal("beat accepted a ttl of zero; a key with no expiry is a friend who is never away")
	}
}

func TestShortSaysTheAgeInTheFewestCharacters(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{-3 * time.Second, "0s"}, // a friend's clock ahead of the reader's
		{12 * time.Second, "12s"},
		{59*time.Second + 999*time.Millisecond, "59s"},
		{time.Minute, "1m"},
		{59 * time.Minute, "59m"},
		{time.Hour + 12*time.Minute, "1h12m"},
		{time.Hour + 2*time.Minute, "1h02m"},
		{25 * time.Hour, "1d01h"},
	} {
		if got := Short(c.d); got != c.want {
			t.Errorf("Short(%s) = %q; want %q", c.d, got, c.want)
		}
	}
}

func TestClockNamesTheDayOnceTheBeatIsOlderThanOne(t *testing.T) {
	last := at
	if got := Clock(last, at.Add(3*time.Hour)); got != "09:41Z" {
		t.Errorf("same day: %q; want %q", got, "09:41Z")
	}
	if got := Clock(last, at.Add(30*time.Hour)); got != "2026-09-22T09:41Z" {
		t.Errorf("a day later: %q; want %q", got, "2026-09-22T09:41Z")
	}
}

func TestTheRosterIsTheBusesMinusGlennAndRowan(t *testing.T) {
	names, err := ParticipantNames([]byte(`{"participants":[
	  {"name":"Rowan"},{"name":"Stella"},{"name":"Johnny"},
	  {"name":"Emma"},{"name":"Freddy"},{"name":"Alex"},{"name":"Glenn"}]}`))
	if err != nil {
		t.Fatalf("participants: %v", err)
	}
	want := "stella johnny emma freddy alex"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("roster = %q; want %q (file order, lower case, no Glenn and no Rowan)", got, want)
	}
}

func TestARosterOfNobodyIsARefusalAndNotAnEmptyLine(t *testing.T) {
	if _, err := ParticipantNames([]byte(`{"participants":[{"name":"Glenn"},{"name":"Rowan"}]}`)); err == nil {
		t.Fatal("a participants file with no friends returned a roster")
	}
	if _, err := ParticipantNames([]byte(`not json`)); err == nil {
		t.Fatal("a participants file that is not json returned a roster")
	}
}

func TestReadRefusesAnEmptyRoster(t *testing.T) {
	if _, err := Read(context.Background(), NewFakeStore(at), nil, at); err == nil {
		t.Fatal("Read with no names returned no error; an empty line reports nothing and looks like good news")
	}
}

func mustRead(t *testing.T, st Store, names []string) []Status {
	t.Helper()
	sts, err := Read(context.Background(), st, names, st.(*FakeStore).Now())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return sts
}
