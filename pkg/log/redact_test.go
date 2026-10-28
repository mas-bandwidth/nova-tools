package log

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	for _, c := range secretShapes() {
		got := Redact(c.line)
		assert.NotContains(t, got, c.secret, "%s: the secret survived Redact: %s", c.name, got)
		assert.Contains(t, got, Redacted, "%s: nothing was marked redacted: %s", c.name, got)
	}
}

// A redaction that eats the fields we ask questions with is a worse tool than no
// redaction: a git sha, a card id, a PR number and an ordinary sentence all survive.
func TestRedactLeavesTheFieldsWeQueryWithAlone(t *testing.T) {
	t.Parallel()

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
		got := Redact(s)
		assert.Equal(t, s, got, "Redact changed a line it should have left alone:\n in: %s\nout: %s", s, got)
	}
}

// The hook is inside Write, so a caller cannot forget it: the leak is caught before the
// line leaves the process, whatever field carried it.
func TestWriteRedactsEveryVariableField(t *testing.T) {
	t.Parallel()

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
	require.NoError(t, l.Write(&b))
	require.False(t, bytes.Contains(b.Bytes(), []byte(secret)), "a secret value reached the writer: %s", b.String())
	require.True(t, bytes.Contains(b.Bytes(), []byte(Redacted)), "the line carries no redaction mark: %s", b.String())
	n := bytes.Count(bytes.TrimRight(b.Bytes(), "\n"), []byte("\n"))
	require.Zero(t, n, "Write emitted more than one line: %s", b.String())
}

// The fixed vocabulary the program itself writes -- ts, level, source, event -- is never
// touched, so a redaction bug can never rename an event out from under a query.
func TestWriteKeepsTheFixedVocabulary(t *testing.T) {
	t.Parallel()

	l := New(fixedClock(), func() string { return "guid-1" }, "nova-work")
	l.Verb = "events"
	l.Event = "pr-checks-done"
	l.Msg = "pr 42 concluded SUCCESS"
	var b bytes.Buffer
	require.NoError(t, l.Write(&b))
	for _, want := range []string{`"source":"nova-work"`, `"verb":"events"`, `"event":"pr-checks-done"`, `"level":"INFO"`} {
		assert.True(t, bytes.Contains(b.Bytes(), []byte(want)), "the line lost %s: %s", want, b.String())
	}
}

// A provider-key prefix counts only where it starts a token. Without a left boundary
// the sk- rule matched inside a nova-sprint store op id (<base36 nanos>-<pid>-<hex>)
// whose time part ended in "sk", about one id in 1296, and backup refused a good
// store file as secret-shaped (nova-sprint#602). The key is still caught at the start
// of the string and after every separator a line puts in front of a value.
func TestRedactCatchesAProviderKeyThatStartsAToken(t *testing.T) {
	t.Parallel()

	keys := []string{
		shape("sk", "-live-9aXbQ2mR7tZk4LpW8vNc3JdH"),
		shape("sk", "-proj-Ab12Cd34Ef56Gh78Ij90Kl"),
		shape("sk", "_live_9aXbQ2mR7tZk4LpW8vNc3JdH"),
		shape("ghp", "_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"),
		shape("glpat", "-Ab12Cd34Ef56Gh78Ij90"),
		shape("AKIA", "IOSFODNN7EXAMPLE"),
		shape("npm", "_Ab12Cd34Ef56Gh78Ij90Kl12Mn34Op56"),
	}
	fronts := []string{"", " ", "\t", "\n", `"`, "'", "=", ":", ": ", "(", "[", "/", ",", "_", "-"}
	for _, key := range keys {
		for _, front := range fronts {
			line := "env" + front + key + " tail"
			if front == "" {
				line = key + " tail"
			}
			got := Redact(line)
			assert.NotContains(t, got, key, "front %q: the key survived Redact: %s", front, got)
			assert.Contains(t, got, Redacted, "front %q: nothing was marked redacted: %s", front, got)
			assert.True(t, strings.HasSuffix(got, " tail"), "front %q: the rest of the line was eaten: %s", front, got)
			if front != "" {
				assert.True(t, strings.HasPrefix(got, "env"+front), "front %q: the separator before the key was eaten: %s", front, got)
			}
		}
	}

	// two keys back to back, one separator apart: the first match must not eat the
	// separator the second one needs.
	a, b := shape("sk", "-live-9aXbQ2mR7tZk4LpW8vNc3JdH"), shape("ghp", "_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8")
	got := Redact(a + " " + b)
	assert.NotContains(t, got, a)
	assert.NotContains(t, got, b)
	assert.Equal(t, Redacted+" "+Redacted, got)
}

// The store op ids and ordinary words that merely contain a prefix are not keys.
func TestRedactLeavesAPrefixInsideATokenAlone(t *testing.T) {
	t.Parallel()

	keep := []string{
		// the id from nova-sprint#602's bench run, its time part ending "sk"
		"backup: op dg9s0cfmsk-1929501-cd9a14e6b07f2a51 written",
		"dg9s0cfmsk-1929501-cd9a14e6b07f2a51",
		// words that end in a prefix, followed by a long tail from the key alphabet
		"ask-the-friend-for-a-second-read",
		"metric disk_usage_samples_for_bench",
		"task_queue_depth_per_member_slot",
	}
	// store.NewID shapes, built from fixed numbers (no clock): base36 nanos whose last
	// two digits are "sk", a pid and eight hex bytes.
	const sk = 28*36 + 20 // "sk" in base36
	for i := int64(0); i < 32; i++ {
		nanos := int64(1761300000000000000) + i*7777777777
		nanos += (sk - nanos%1296 + 1296) % 1296
		id := strconv.FormatInt(nanos, 36) + "-" + strconv.FormatInt(1000+i*7919, 10) + "-" + fmt.Sprintf("%016x", uint64(i+1)*0x9e3779b97f4a7c15)
		require.True(t, strings.Contains(id, "sk-"), "fixture %s does not carry the sk- the bug needs", id)
		keep = append(keep, id, "op "+id+" applied")
	}
	for _, s := range keep {
		assert.Equal(t, s, Redact(s), "Redact changed a line it should have left alone: %s", s)
	}
}
