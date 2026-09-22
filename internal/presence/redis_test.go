package presence

import (
	"context"
	"strings"
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

// TestAddrRefusesTLSAndUserinfoWithoutLeakingTheSecret is the synthetic
// control for comment 5782441213 on #2612: a rediss:// address used to be
// stripped of its extra "s" and dialled in plaintext with no word said, and a
// user:pass@ address was never parsed at all -- it rode along inside the
// "host:port" this function handed back, ready to be printed by the next
// caller. Both are now refused, by name, and the refusal never carries the
// password: no network is dialled here, Addr is pure string parsing.
func TestAddrRefusesTLSAndUserinfoWithoutLeakingTheSecret(t *testing.T) {
	const secret = "hunter2"
	for _, c := range []struct {
		name    string
		in      string
		wantErr string // substring the refusal must name
	}{
		{"tls", "rediss://store.invalid:6380", "rediss:// (TLS) is not supported"},
		{"userinfo", "redis://friend:" + secret + "@store.invalid:6380", "userinfo (user:pass@) in the URL is not supported"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Addr(c.in)
			if err == nil {
				t.Fatalf("Addr(%q) = %q, <nil>; want a refusal", c.in, got)
			}
			if got != "" {
				t.Errorf("Addr(%q) returned a usable address %q alongside the error; want none", c.in, got)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("Addr(%q) error = %q; want it to name %q", c.in, err.Error(), c.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("Addr(%q) error = %q; the password leaked", c.in, err.Error())
			}
		})
	}
}

// TestAddrStillAcceptsAPlainURL is the table's third leg: refusing rediss://
// and userinfo must not have touched the ordinary redis://host:port a caller
// pastes every day.
func TestAddrStillAcceptsAPlainURL(t *testing.T) {
	got, err := Addr("redis://store.invalid:6380")
	if err != nil {
		t.Fatalf("Addr(plain redis://) = %v; want it to still work", err)
	}
	if got != "store.invalid:6380" {
		t.Errorf("Addr(plain redis://) = %q; want %q", got, "store.invalid:6380")
	}
}

// TestMaskAddrNeverPrintsTheUserinfo is maskAddr's own control: whatever a
// caller hands it, the substring up to and including the last "@" never
// reaches the output, because that substring is where a pasted password
// lives.
func TestMaskAddrNeverPrintsTheUserinfo(t *testing.T) {
	const secret = "hunter2"
	for _, c := range []struct{ in, want string }{
		{"friend:" + secret + "@store.invalid:6380", "***@store.invalid:6380"},
		{"store.invalid:6380", "store.invalid:6380"},
		{"redis://friend:" + secret + "@store.invalid:6380", "***@store.invalid:6380"},
	} {
		got := maskAddr(c.in)
		if got != c.want {
			t.Errorf("maskAddr(%q) = %q; want %q", c.in, got, c.want)
		}
		if strings.Contains(got, secret) {
			t.Errorf("maskAddr(%q) = %q; still carries the password", c.in, got)
		}
	}
}
