package presence

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// The fake store proves the logic; this file proves the two commands the logic
// is made of are the ones a Redis actually honours -- SET with EX, and an MGET
// whose absent keys come back empty -- against miniredis, whose clock the test
// moves instead of waiting.

func TestAgainstRedisTheKeyExpiresAndTheMemoryDoesNot(t *testing.T) {
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
