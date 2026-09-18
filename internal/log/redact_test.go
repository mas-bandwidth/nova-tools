package log

import (
	"bytes"
	"strings"
	"testing"
)

// SPEC-LOGS.md Part 2, "What must never be logged": a secret VALUE never leaves the
// process in an event. Part 5 makes that a test of the emitter and not of review, so
// every case below drives Redact and Line.Write directly rather than a verb.
//
// The shape of the promise is redaction, not refusal: an event whose message happened to
// carry a token is still the event that says what the part did, and dropping it would
// trade a leak for a blind spot. The value is replaced; the line still ships.

// shape assembles a credential-shaped fixture at run time rather than spelling it as one
// literal. None of these is a real key -- every one is invented here -- but a literal of
// some of these shapes in a committed file is refused by GitHub's push protection, which
// cannot tell a fixture from the thing itself and should not try. Splitting the prefix off
// keeps the fixture honest, keeps the repository pushable, and costs one function.
func shape(parts ...string) string { return strings.Join(parts, "") }

// The known secret values, each one shaped like a real credential, and the sentence each
// one is hidden in. secret is the substring that must not survive.
func secretShapes() []struct{ name, line, secret string } {
	keys := map[string]string{
		"openai style":        shape("sk", "-live-9aXbQ2mR7tZk4LpW8vNc3JdH"),
		"github pat":          shape("ghp", "_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"),
		"github fine grained": shape("github", "_pat_11ABCDEFG0aBcDeFgHiJkL_mNoPqRsTuVwXyZ0123456789"),
		"aws access key":      shape("AKIA", "IOSFODNN7EXAMPLE"),
		"slack bot token":     shape("xox", "b-2345678901-2345678901234-AbCdEfGhIjKlMnOpQrStUvWx"),
		"age secret key":      shape("AGE-SECRET", "-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQ8ACZJX"),
		"bearer header":       shape("eyJhbGciOiJIUzI1", "NiIsInR5cCI6IkpXVCJ9"),
		"keyed assignment":    shape("sk", "_9f8e7d6c5b4a3210zyxwvu"),
		"high entropy run":    shape("T0kenA9bC8dE7fG6", "hI5jK4lM3nO2pQ1rS0tU"),
	}
	sentences := map[string]string{
		"openai style":        "poll refused: %s",
		"github pat":          "push failed with %s",
		"github fine grained": "token %s rejected",
		"aws access key":      "creds %s expired",
		"slack bot token":     "bus said %s",
		"age secret key":      "seal read %s",
		"bearer header":       "forge answered 401 to Authorization: Bearer %s",
		"keyed assignment":    "env had DEEPSEEK_API_KEY=%s",
		"high entropy run":    "the forge handed back %s",
	}
	var out []struct{ name, line, secret string }
	for name, secret := range keys {
		out = append(out, struct{ name, line, secret string }{
			name: name, line: strings.Replace(sentences[name], "%s", secret, 1), secret: secret,
		})
	}
	return out
}

func TestRedactRemovesEverySecretShape(t *testing.T) {
	for _, c := range secretShapes() {
		got := Redact(c.line)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: the secret survived Redact: %s", c.name, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Errorf("%s: nothing was marked redacted: %s", c.name, got)
		}
	}
}

// A redaction that eats the fields we ask questions with is a worse tool than no
// redaction: a git sha, a card id, a PR number and an ordinary sentence all survive.
func TestRedactLeavesTheFieldsWeQueryWithAlone(t *testing.T) {
	keep := []string{
		"dev moved to 0d7393529d4a7f1c4e0b3a2d1f6e8c9b0a1d2e3f",
		"card 8973x finished on bench hulk",
		"pr 1302 checks concluded SUCCESS at head a1b2c3d",
		"poll the forge: gh exited 1",
		"rowan/lane-merge-2",
		"nova-work events --redis 127.0.0.1:6379 --once",
		// The id shapes the fleet already queries by. Each of these was eaten by an
		// earlier, greedier entropy rule; each one is why the floors in redact.go
		// are where they are, and a change that widens the rule fails here first.
		"launch: pulse 20260917T165603Z-pulse-f193e3 cards=2",
		"harvest: job 20260918T040506Z-hulk-7 finished",
		"clip: worktree /Users/glenn/rowan-working/tmp/lane-events/repo",
		"the base moved to 0d739352aa1f4c7e9b2d5a8f3e6c1b04d7a9f2e5",
	}
	for _, s := range keep {
		if got := Redact(s); got != s {
			t.Errorf("Redact changed a line it should have left alone:\n in: %s\nout: %s", s, got)
		}
	}
}

// The hook is inside Write, so a caller cannot forget it: the leak is caught before the
// line leaves the process, whatever field carried it.
func TestWriteRedactsEveryVariableField(t *testing.T) {
	const secret = "sk-live-9aXbQ2mR7tZk4LpW8vNc3JdH"
	l := New(fixedClock(), func() string { return "guid-1" }, "nova-work")
	l.Verb = "events"
	l.Bench = "hulk"
	l.Event = "card-done"
	l.Card = secret
	l.Job = secret
	l.Run = secret
	l.Slot = secret
	l.Msg = "events: card-done " + secret
	l.Err = "publish card-done: " + secret

	var b bytes.Buffer
	if err := l.Write(&b); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b.Bytes(), []byte(secret)) {
		t.Fatalf("a secret value reached the writer: %s", b.String())
	}
	if !bytes.Contains(b.Bytes(), []byte(Redacted)) {
		t.Fatalf("the line carries no redaction mark: %s", b.String())
	}
	if n := bytes.Count(bytes.TrimRight(b.Bytes(), "\n"), []byte("\n")); n != 0 {
		t.Fatalf("Write emitted more than one line: %s", b.String())
	}
}

// The fixed vocabulary the program itself writes -- ts, level, source, event -- is never
// touched, so a redaction bug can never rename an event out from under a query.
func TestWriteKeepsTheFixedVocabulary(t *testing.T) {
	l := New(fixedClock(), func() string { return "guid-1" }, "nova-work")
	l.Verb = "events"
	l.Event = "pr-checks-done"
	l.Msg = "pr 42 concluded SUCCESS"
	var b bytes.Buffer
	if err := l.Write(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"source":"nova-work"`, `"verb":"events"`, `"event":"pr-checks-done"`, `"level":"INFO"`} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("the line lost %s: %s", want, b.String())
		}
	}
}

// A COUNT OF TOKENS IS NOT A TOKEN. The keyed-value rule reads any key holding the word
// "token" as naming a credential, which ate the usage columns of `nova-swarm native`'s done
// line the first time one reached Loki -- `tokens_in=[redacted] tokens_out=[redacted]`, in a
// fleet where token spend reporting is an obligation. The rule now keeps a counted key
// carrying a number, and an absence whatever the key, and redacts everything else exactly as
// it did.
func TestACountOfTokensIsNotACredential(t *testing.T) {
	t.Parallel()
	kept := []string{
		"native: card r2 rc=0 tokens_in=41234 tokens_out=980 cache_write=- cache_read=- reasoning=- usd=0.42",
		"tokens=120000",
		"max_tokens=8192",
		"token_count=3",
		"tokens_in=-",
		// An absence is a secret in no reading at all, whatever the key names.
		"api_key=-",
		"password=-",
	}
	for _, line := range kept {
		if got := Redact(line); got != line {
			t.Errorf("a measurement was redacted:\n got: %s\nwant: %s", got, line)
		}
	}
	// And the rule still bites where it should: a counted key whose value is NOT a number
	// is redacted, and a real credential key is redacted whatever it holds.
	redacted := []string{
		"tokens_in=sk-abcdefghijklmnop0123456789",
		"token=ghp_abcdefghijklmnopqrstuvwxyz0123",
		"api_key=0123456789abcdef",
		"password=12345",
	}
	for _, line := range redacted {
		if got := Redact(line); !strings.Contains(got, Redacted) {
			t.Errorf("a credential survived: %s", got)
		}
	}
}
