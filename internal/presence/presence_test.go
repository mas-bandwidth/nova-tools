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

func TestBeatWritesThePresenceHashAndItsUntimedMemory(t *testing.T) {
	t.Parallel()

	st := NewFakeStore(at)
	if err := Beat(context.Background(), st, "Johnny", at, DefaultTTL); err != nil {
		t.Fatalf("beat: %v", err)
	}
	want := "2026-09-22T09:41:00Z"
	h := st.Hash("friend:johnny")
	if h[FieldAt] != want || st.String("friend:johnny:last") != want {
		t.Fatalf("friend:johnny = %v, :last = %q; want at and :last both %q (the name is normalized and the value is RFC3339 utc)", h, st.String("friend:johnny:last"), want)
	}
	if _, ok := h[FieldWidth]; ok {
		t.Fatalf("friend:johnny = %v; a beat with no width writes no width field", h)
	}
	if got := st.TTL("friend:johnny"); got != DefaultTTL {
		t.Fatalf("friend:johnny ttl = %s; want %s", got, DefaultTTL)
	}
	if st.Sets != 1 {
		t.Fatalf("writes = %d; one beat is one write", st.Sets)
	}
}

// TestBeatSideWritesWidthIntoTheHashWithTheTTL is #2673: the child count is a
// field of the beat's own hash, written with it and lapsing with it.
func TestBeatSideWritesWidthIntoTheHashWithTheTTL(t *testing.T) {
	t.Parallel()

	st := NewFakeStore(at)
	eight := int64(8)
	if err := BeatSide(context.Background(), st, "emma", at, DefaultTTL, Side{Width: &eight}); err != nil {
		t.Fatalf("beat: %v", err)
	}
	h := st.Hash("friend:emma")
	if h[FieldWidth] != "8" || h[FieldAt] != "2026-09-22T09:41:00Z" {
		t.Fatalf("friend:emma = %v; want at and width=8", h)
	}
	if got := st.TTL("friend:emma"); got != DefaultTTL {
		t.Fatalf("ttl = %s; want %s", got, DefaultTTL)
	}
	sts := mustRead(t, st, []string{"emma"})
	if got := sts[0].Phrase(at); got != "emma up 0s width=8" {
		t.Fatalf("phrase = %q", got)
	}
	st.Advance(DefaultTTL)
	if h := st.Hash("friend:emma"); h != nil {
		t.Fatalf("past the ttl friend:emma = %v; want it gone, width and all", h)
	}
	sts = mustRead(t, st, []string{"emma"})
	if got := sts[0].Phrase(st.Now()); got != "emma down 1m (last 09:41Z)" {
		t.Fatalf("phrase = %q; a down friend carries no width", got)
	}
}

func TestTheTTLIsWhatMakesAFriendAway(t *testing.T) {
	t.Parallel()

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
	if got := sts[0].Phrase(st.Now()); got != "emma down 1m (last 09:41Z)" {
		t.Fatalf("phrase = %q; want %q", got, "emma down 1m (last 09:41Z)")
	}
}

func TestTheLineSaysUpOrDown(t *testing.T) {
	t.Parallel()

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
	want := "friends: johnny up 12s · stella up 4s · emma down 1h12m (last 09:41Z) · freddy down"
	if got != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", got, want)
	}
}

func TestAStoreThatWillNotAnswerIsAnErrorAndNotAnEmptyRoom(t *testing.T) {
	t.Parallel()

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

// TestAMissingBeatKeyIsAbsentAndALiveKeyIsPresent is #2675: a friend is
// present only while friend:<name> itself is alive -- the row's up and at
// where the row loop writes one (#3447), the beat's TTL where it does not. A missing key is
// absent, the untimed :last memory of a beat is still absent, a live key
// is present, and the same key past its TTL is absent again. There is no
// override file to consult when the key is gone.
func TestAMissingBeatKeyIsAbsentAndALiveKeyIsPresent(t *testing.T) {
	t.Parallel()

	st := NewFakeStore(at)
	ctx := context.Background()

	sts := mustRead(t, st, []string{"stella"})
	if h := st.Hash(Key("stella")); h != nil {
		t.Fatalf("beat hash before any write = %v; want it missing", h)
	}
	if sts[0].Present() {
		t.Fatal("a missing beat key is present; want absent")
	}
	if got := sts[0].Phrase(at); got != "stella down" {
		t.Fatalf("missing key phrase = %q; want %q", got, "stella down")
	}

	// The friend row (#3447) is presence by its own up and at, never by a
	// TTL: up=0 is absent, up=1 with a fresh at is present. The row is the
	// row loop's, so the beat below runs on a friend with no row.
	st.SetHash(Key("walter"), 0, "at", at.UTC().Format(Stamp), "up", "0", "working", "3")
	if sts = mustRead(t, st, []string{"walter"}); sts[0].Present() {
		t.Fatal("a row that says up=0 is present; want absent")
	}
	st.SetHash(Key("walter"), 0, "up", "1")
	if sts = mustRead(t, st, []string{"walter"}); !sts[0].Present() {
		t.Fatal("a row that says up=1 with a fresh at is absent; want present")
	}

	// :last alone is the memory of a beat, not a friend who is here.
	st.SetString(LastKey("stella"), at.UTC().Format(Stamp))
	sts = mustRead(t, st, []string{"stella"})
	if sts[0].Present() {
		t.Fatal("friend:stella:last with no beat key is present; want absent")
	}
	if sts[0].State != Away {
		t.Fatalf("last-only state = %v; want Away (absent, dated)", sts[0].State)
	}

	if err := Beat(ctx, st, "stella", at, DefaultTTL); err != nil {
		t.Fatalf("beat: %v", err)
	}
	sts = mustRead(t, st, []string{"stella"})
	if !sts[0].Present() {
		t.Fatal("a live beat key is absent; want present")
	}
	if got := sts[0].Phrase(at); got != "stella up 0s" {
		t.Fatalf("live key phrase = %q; want %q", got, "stella up 0s")
	}

	st.Advance(DefaultTTL)
	sts = mustRead(t, st, []string{"stella"})
	if h, last := st.Hash(Key("stella")), st.String(LastKey("stella")); h != nil || last == "" {
		t.Fatalf("after the ttl, hash = %v, last = %q; want the beat hash missing and :last kept", h, last)
	}
	if sts[0].Present() {
		t.Fatal("an expired beat key is present; want absent")
	}
}

func TestAKeyWhoseValueIsNotAStampIsStillPresence(t *testing.T) {
	t.Parallel()

	// The hash's live TTL is the presence; its at field only dates it. A
	// friend running an older beat must not read as away.
	st := NewFakeStore(at)
	st.SetHash(Key("alex"), DefaultTTL, FieldAt, "here")
	sts := mustRead(t, st, []string{"alex"})
	if got := sts[0].Phrase(at); got != "alex up" {
		t.Fatalf("phrase = %q; want %q", got, "alex up")
	}
}

func TestBeatRefusesAnEmptyNameOrAnEmptyTTL(t *testing.T) {
	t.Parallel()

	st := NewFakeStore(at)
	if err := Beat(context.Background(), st, "  ", at, DefaultTTL); err == nil {
		t.Fatal("beat accepted an empty name")
	}
	if err := Beat(context.Background(), st, "johnny", at, 0); err == nil {
		t.Fatal("beat accepted a ttl of zero; a key with no expiry is a friend who is never away")
	}
}

func TestShortSaysTheAgeInTheFewestCharacters(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	last := at
	if got := Clock(last, at.Add(3*time.Hour)); got != "09:41Z" {
		t.Errorf("same day: %q; want %q", got, "09:41Z")
	}
	if got := Clock(last, at.Add(30*time.Hour)); got != "2026-09-22T09:41Z" {
		t.Errorf("a day later: %q; want %q", got, "2026-09-22T09:41Z")
	}
}

func TestTheRosterIsTheBusesMinusGlennAndRowan(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	if _, err := ParticipantNames([]byte(`{"participants":[{"name":"Glenn"},{"name":"Rowan"}]}`)); err == nil {
		t.Fatal("a participants file with no friends returned a roster")
	}
	if _, err := ParticipantNames([]byte(`not json`)); err == nil {
		t.Fatal("a participants file that is not json returned a roster")
	}
}

func TestFriendsIsTheRegistrySetSortedMinusGlennAndRowan(t *testing.T) {
	t.Parallel()

	st := NewFakeStore(at)
	if _, err := Friends(context.Background(), st); err == nil {
		t.Fatal("an empty friends set returned a roster; an empty line reads as good news")
	}
	st.AddMembers(FriendsSet, "stella", "Emma", "rowan", "glenn", "johnny")
	names, err := Friends(context.Background(), st)
	if err != nil {
		t.Fatalf("friends: %v", err)
	}
	if got := strings.Join(names, " "); got != "emma johnny stella" {
		t.Fatalf("roster = %q; want %q", got, "emma johnny stella")
	}
}

func TestReadRefusesAnEmptyRoster(t *testing.T) {
	t.Parallel()

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
