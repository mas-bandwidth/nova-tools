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

// TestAddrGrammarRefusesEverythingButHostPortWithoutLeakingASecret is the
// synthetic control for comment 5783202393 on #2612: maskAddr still echoed a
// query secret in a rejected rediss:// URL (it only ever masked userinfo),
// and a plain redis:// query string was never refused at all --
// redis://host:port?password=SECRET parsed straight through as a "clean"
// host:port carrying the secret inside it. The repair is a strict grammar --
// exactly host:port or redis://host:port, nothing else -- so every one of
// these shapes is refused by the same generic line, which never carries
// anything of the input beyond the scheme name and the bare host. No network
// is dialled here: Addr is pure string parsing.
func TestAddrGrammarRefusesEverythingButHostPortWithoutLeakingASecret(t *testing.T) {
	const secret = "SECRET"
	for _, c := range []struct {
		name    string
		in      string
		want    string // "" means Addr must refuse it; else the host:port it must parse to
		wantBad []string
	}{
		{name: "rediss with a query secret", in: "rediss://h:6380?password=" + secret, wantBad: []string{"scheme", "query"}},
		{name: "plain redis with a query secret", in: "redis://h:6380?password=" + secret, wantBad: []string{"query"}},
		{name: "userinfo carrying a secret", in: "redis://u:" + secret + "@h:6380", wantBad: []string{"userinfo"}},
		{name: "a path", in: "redis://h:6380/0", wantBad: []string{"path"}},
		{name: "a fragment carrying a secret", in: "redis://h:6380#" + secret, wantBad: []string{"fragment"}},
		{name: "bare host:port", in: "h:6380", want: "h:6380"},
		{name: "redis:// host:port", in: "redis://h:6380", want: "h:6380"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Addr(c.in)
			if c.want != "" {
				if err != nil {
					t.Fatalf("Addr(%q) = %v; want it to parse to %q", c.in, err, c.want)
				}
				if got != c.want {
					t.Errorf("Addr(%q) = %q; want %q", c.in, got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Addr(%q) = %q, <nil>; want a refusal", c.in, got)
			}
			if got != "" {
				t.Errorf("Addr(%q) returned a usable address %q alongside the error; want none", c.in, got)
			}
			if !strings.Contains(err.Error(), "store address refused: only host:port or redis://host:port is supported") {
				t.Errorf("Addr(%q) error = %q; want the one generic refusal", c.in, err.Error())
			}
			for _, word := range c.wantBad {
				if !strings.Contains(err.Error(), word) {
					t.Errorf("Addr(%q) error = %q; want it to name %q", c.in, err.Error(), word)
				}
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("Addr(%q) error = %q; the secret leaked", c.in, err.Error())
			}
		})
	}
}
