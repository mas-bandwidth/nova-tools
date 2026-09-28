package presence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// The fake store proves the logic; this file proves the commands the logic is
// made of are the ones a Redis actually honours -- HSET plus PEXPIRE in one
// MULTI, and a pipeline of HMGET, PTTL and GET whose absent keys come back
// empty -- against miniredis, whose clock the test moves instead of waiting.

func TestAgainstRedisTheKeyExpiresAndTheMemoryDoesNot(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := Open(ctx, mr.Addr(), DefaultUser)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)
	if err := Beat(ctx, st, "emma", now, DefaultTTL); err != nil {
		t.Fatalf("beat: %v", err)
	}
	sts, err := Read(ctx, st, []string{"emma"}, now.Add(12*time.Second))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sts[0].State != Up {
		t.Fatalf("inside the ttl: %v; want Up", sts[0].State)
	}

	mr.FastForward(DefaultTTL + time.Second)
	sts, err = Read(ctx, st, []string{"emma"}, now.Add(DefaultTTL+time.Second))
	if err != nil {
		t.Fatalf("read after the ttl: %v", err)
	}
	if sts[0].State != Away {
		t.Fatalf("past the ttl: %v; want Away", sts[0].State)
	}
	if !sts[0].Dated || !sts[0].Last.Equal(now) {
		t.Fatalf("the untimed key did not survive the expiry: dated=%v last=%v", sts[0].Dated, sts[0].Last)
	}
	if got := mr.TTL(LastKey("emma")); got != 0 {
		t.Fatalf("friend:emma:last carries a ttl of %s; it is the memory of a beat and never expires", got)
	}
}

func TestAddrTakesHostPortAndRefusesAGuess(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, want string }{
		{"store.invalid:6380", "store.invalid:6380"},
		{"redis://store.invalid:6380", "store.invalid:6380"},
		{" store.invalid:6380/ ", "store.invalid:6380"},
	} {
		got, err := Addr(c.in)
		if err != nil || got != c.want {
			t.Errorf("Addr(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"", "   ", "store.invalid"} {
		if got, err := Addr(in); err == nil {
			t.Errorf("Addr(%q) = %q; want a refusal, because the fleet store is on 6380 and the default is 6379", in, got)
		}
	}
}

// TestAddrGrammarRefusesEverythingButHostPortWithoutLeakingASecret is the
// synthetic control for comment 5783425783 on #2612, and the two comments
// before it on the same issue: a query VALUE containing "@" --
// redis://store.invalid:6380?password=prefix@SYNTHETIC_SECRET, the exact
// string from the comment -- still became a printed host, because the prior
// repair's addrProblems took strings.LastIndex(s, "@") over the raw
// remainder before it had located the query boundary, so the "@" inside the
// query value was mistaken for the userinfo delimiter and SYNTHETIC_SECRET
// (everything after it) came back as the "host" the refusal named. Before
// that, a plain query string went straight through unrefused, and before
// that, maskAddr echoed a query secret through a rejected rediss:// URL. All
// three were one class of bug: a message assembled from pieces of the input.
// The repair drops assembly entirely -- refusedAddr is a constant, and
// Addr's parse is structural (net.SplitHostPort plus a character-class and
// range check), never a LastIndex/Index scan for a delimiter byte in the raw
// string. This is pure string parsing: no network is dialled, and Stella's
// synthetic secret and synthetic host never named a real credential.
func TestAddrGrammarRefusesEverythingButHostPortWithoutLeakingASecret(t *testing.T) {
	t.Parallel()

	const secret = "SYNTHETIC_SECRET"
	refusals := []string{
		// The exact synthetic URL from comment 5783425783: a query value
		// containing "@".
		"redis://store.invalid:6380?password=prefix@" + secret,
		"redis://h:6380?x=a@b",
		"redis://a@b:6380",
		"redis://h:0",     // port below the 1-65535 range
		"redis://h:70000", // port above the 1-65535 range
		"redis://h",       // no port at all -- the same constant as every other refusal
		"rediss://h:6380", // a scheme other than redis
		"redis://u:pass@h:6380",
		"redis://h:6380/0",
		"redis://h:6380#frag",
	}
	for _, in := range refusals {
		t.Run(in, func(t *testing.T) {
			got, err := Addr(in)
			if err == nil {
				t.Fatalf("Addr(%q) = %q, <nil>; want a refusal", in, got)
			}
			if got != "" {
				t.Errorf("Addr(%q) returned a usable address %q alongside the error; want none", in, got)
			}
			// Byte-for-byte equality to the constant is the real proof:
			// refusedAddr is fixed at compile time from no part of addr, so
			// equality alone means nothing of any input, sensitive or not,
			// reached the message. This also checks explicitly for the
			// tokens these particular inputs carry that a per-shape or
			// LastIndex-built message (the two prior repairs) would have
			// echoed -- the synthetic secret and synthetic host chief among
			// them -- skipping only "redis", which the constant's own
			// "redis://host:port" example legitimately contains.
			if err.Error() != refusedAddr {
				t.Errorf("Addr(%q) error = %q; want the constant refusal %q byte-for-byte, nothing else", in, err.Error(), refusedAddr)
			}
			for _, sensitive := range []string{secret, "store.invalid", "prefix", "password", "70000", "frag", "u:pass"} {
				if strings.Contains(in, sensitive) && strings.Contains(err.Error(), sensitive) {
					t.Errorf("Addr(%q) error = %q; contains input fragment %q", in, err.Error(), sensitive)
				}
			}
		})
	}

	accepted := []struct{ in, want string }{
		{"h:6380", "h:6380"},
		{"redis://h:6380", "h:6380"},
		{"[::1]:6380", "[::1]:6380"},
	}
	for _, c := range accepted {
		t.Run(c.in, func(t *testing.T) {
			got, err := Addr(c.in)
			if err != nil {
				t.Fatalf("Addr(%q) = %v; want it to parse to %q", c.in, err, c.want)
			}
			if got != c.want {
				t.Errorf("Addr(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

// TestAgainstRedisAMissingBeatKeyIsAbsentAndALiveKeyIsPresent is the same
// rule as TestAMissingBeatKeyIsAbsentAndALiveKeyIsPresent, against the
// store's own expiry: a key Redis does not hold is absent, a key it still
// holds is present, and friend:<name>:last surviving the TTL does not
// flip that. miniredis moves its clock; the test does not wait.
func TestAgainstRedisAMissingBeatKeyIsAbsentAndALiveKeyIsPresent(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := Open(ctx, mr.Addr(), DefaultUser)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)
	sts, err := Read(ctx, st, []string{"stella"}, now)
	if err != nil {
		t.Fatalf("read of a missing key: %v", err)
	}
	if sts[0].Present() {
		t.Fatal("a missing beat key is present; want absent")
	}

	mr.Set(LastKey("stella"), now.Format(Stamp))
	sts, err = Read(ctx, st, []string{"stella"}, now)
	if err != nil {
		t.Fatalf("read of :last alone: %v", err)
	}
	if sts[0].Present() {
		t.Fatal("friend:stella:last with no beat key is present; want absent")
	}

	if err := Beat(ctx, st, "stella", now, DefaultTTL); err != nil {
		t.Fatalf("beat: %v", err)
	}
	sts, err = Read(ctx, st, []string{"stella"}, now)
	if err != nil {
		t.Fatalf("read of a live key: %v", err)
	}
	if !sts[0].Present() {
		t.Fatal("a live beat key is absent; want present")
	}

	mr.FastForward(DefaultTTL + time.Second)
	sts, err = Read(ctx, st, []string{"stella"}, now.Add(DefaultTTL+time.Second))
	if err != nil {
		t.Fatalf("read after the ttl: %v", err)
	}
	if sts[0].Present() {
		t.Fatal("an expired beat key is present; want absent")
	}
	if !mr.Exists(LastKey("stella")) {
		t.Fatal("friend:stella:last did not survive the beat key's expiry")
	}
}

// TestAgainstRedisBeatWritesWidthAndTTL is #2673's DONE-WHEN against a real
// protocol: `nova-wake beat --width 8` lands as the hash friend:<name> with
// at and width, carrying the beat's TTL, and :last with none.
func TestAgainstRedisBeatWritesWidthAndTTL(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := Open(ctx, mr.Addr(), DefaultUser)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)
	eight := int64(8)
	if err := BeatSide(ctx, st, "Emma", now, DefaultTTL, Side{Width: &eight}); err != nil {
		t.Fatalf("beat: %v", err)
	}
	if got := mr.HGet("friend:emma", "width"); got != "8" {
		t.Fatalf("friend:emma width = %q; want 8", got)
	}
	if got := mr.HGet("friend:emma", "at"); got != "2026-09-22T09:41:00Z" {
		t.Fatalf("friend:emma at = %q", got)
	}
	if got := mr.TTL("friend:emma"); got != DefaultTTL {
		t.Fatalf("friend:emma ttl = %s; want %s", got, DefaultTTL)
	}
	if got := mr.TTL(LastKey("emma")); got != 0 {
		t.Fatalf(":last ttl = %s; want none", got)
	}
	sts, err := Read(ctx, st, []string{"emma"}, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := sts[0].Phrase(now); got != "emma up 0s width=8" {
		t.Fatalf("phrase = %q", got)
	}
}

// TestAgainstRedisTheFriendRowIsPresenceAndBeatRefusesIt is #3447 on a real
// store: friend:<name> is the friend row, a hash with no TTL. Its up and at
// read up, with no beat; a beat on it answers a KeyTypeError naming the hash
// and leaves the row exactly as it was, no field written and no TTL given; and
// a key of another type is refused the same way, never deleted.
func TestAgainstRedisTheFriendRowIsPresenceAndBeatRefusesIt(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := Open(ctx, mr.Addr(), DefaultUser)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)
	rowAt := now.Add(-time.Second).Format(Stamp)

	mr.HSet("friend:stella", "at", rowAt, "up", "1", "ready", "4", "width", "15")
	sts, err := Read(ctx, st, []string{"stella"}, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !sts[0].Present() {
		t.Fatal("a row with up=1 and a fresh at reads down; want up")
	}
	if got := sts[0].Phrase(now); got != "stella up 1s width=15" {
		t.Fatalf("phrase = %q", got)
	}

	eight := int64(8)
	err = BeatSide(ctx, st, "stella", now, DefaultTTL, Side{Width: &eight})
	var kt *KeyTypeError
	if !errors.As(err, &kt) || kt.Type != "hash" || !kt.Row {
		t.Fatalf("beat on the row = %v; want a KeyTypeError for the row", err)
	}
	if got := mr.HGet("friend:stella", "at"); got != rowAt {
		t.Fatalf("the refused beat wrote at = %q", got)
	}
	if got := mr.HGet("friend:stella", "width"); got != "15" {
		t.Fatalf("the refused beat wrote width = %q", got)
	}
	if ttl := mr.TTL("friend:stella"); ttl != 0 {
		t.Fatalf("the refused beat gave the row a TTL of %s", ttl)
	}
	if mr.Exists("friend:stella:last") {
		t.Fatal("the refused beat wrote :last")
	}

	if _, err := mr.SAdd("friend:johnny", "x"); err != nil {
		t.Fatal(err)
	}
	err = Beat(ctx, st, "johnny", now, DefaultTTL)
	if !errors.As(err, &kt) || kt.Type != "set" || !strings.Contains(err.Error(), "friend:johnny is a set") {
		t.Fatalf("beat on a set = %v; want a KeyTypeError naming the set", err)
	}
	if !mr.Exists("friend:johnny") {
		t.Fatal("the refused beat deleted a key it does not own")
	}
}

// TestAgainstRedisALegacyStringBeatIsReplaced: a beat older than #2673 left
// friend:<name> as a plain string with a TTL. Reading it is not a failure,
// and the next beat replaces it with the hash.
func TestAgainstRedisALegacyStringBeatIsReplaced(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := Open(ctx, mr.Addr(), DefaultUser)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)

	mr.Set("friend:johnny", now.Format(Stamp))
	mr.SetTTL("friend:johnny", DefaultTTL)
	sts, err := Read(ctx, st, []string{"johnny"}, now)
	if err != nil {
		t.Fatalf("read over a legacy string: %v", err)
	}
	if !sts[0].Present() {
		t.Fatal("a legacy beat with a live TTL reads down; want up")
	}
	two := int64(2)
	if err := BeatSide(ctx, st, "johnny", now, DefaultTTL, Side{Width: &two}); err != nil {
		t.Fatalf("beat over a legacy string: %v", err)
	}
	if got := mr.HGet("friend:johnny", "width"); got != "2" {
		t.Fatalf("width = %q; want the hash to replace the string", got)
	}
}

// TestAgainstRedisFriendsReadsTheSet: the roster is SMEMBERS friends.
func TestAgainstRedisFriendsReadsTheSet(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := Open(ctx, mr.Addr(), DefaultUser)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := mr.SAdd(FriendsSet, "stella", "emma", "rowan"); err != nil {
		t.Fatal(err)
	}
	names, err := Friends(ctx, st)
	if err != nil {
		t.Fatalf("friends: %v", err)
	}
	if got := strings.Join(names, ","); got != "emma,stella" {
		t.Fatalf("roster = %q; want emma,stella", got)
	}
}
