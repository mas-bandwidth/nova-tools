package redisconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassify is every kind of error a tool can hold after working with
// the store, as go-redis and the network return them, wrapped with %w, and
// flattened to their text.
func TestClassify(t *testing.T) {
	t.Parallel()
	conn, _ := opened(t, func(_ int, cmd []string) string {
		switch cmd[0] {
		case "hello":
			return helloAccepted
		case "PING":
			return "+PONG\r\n"
		}
		// The store answers each command with the line the test sent it.
		return "-" + cmd[1] + "\r\n"
	})
	// answered is the error go-redis returns for a line the store wrote.
	answered := func(line string) error {
		err := conn.Client().Do(context.Background(), "ECHO-AS-ERROR", line).Err()
		var re redis.Error
		require.ErrorAs(t, err, &re, "the store's %q came back as %T %v", line, err, err)
		require.EqualError(t, err, line, "the store's %q came back as %T %v", line, err, err)
		return err
	}
	timeout := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	_, ours := Resolve(Options{}, nil)

	for _, c := range []struct {
		name string
		err  error
		want Class
	}{
		{"nothing", nil, Other},

		{"the store: NOAUTH", answered("NOAUTH Authentication required."), AuthRefused},
		{"the store: WRONGPASS", answered("WRONGPASS invalid username-password pair or user is disabled."), AuthRefused},
		{"the store: the wrong password, as an older store says it", answered("ERR invalid password"), AuthRefused},
		{"the store: the wrong pair, as an older store says it", answered("ERR invalid username-password pair"), AuthRefused},
		{"the store: a login that may not run the command", answered("NOPERM this user has no permissions to run the 'set' command"), Other},
		{"the store: the wrong type", answered("WRONGTYPE Operation against a key holding the wrong kind of value"), Other},
		{"the store: loading", answered("LOADING Redis is loading the dataset in memory"), Other},
		{"the store: a refusal that quotes a word of the network", answered("ERR connection refused by script"), Other},
		{"the store: a refusal that quotes NOAUTH", answered("ERR unknown command 'NOAUTH '"), Other},
		{"the store: an absent value", redis.Nil, Other},

		{"a dial that was refused", refused, Unreachable},
		{"a read that timed out", timeout, Unreachable},
		{"a name that does not resolve", &net.DNSError{Err: "no such host", Name: "store.test", IsNotFound: true}, Unreachable},
		{"the deadline of a context", context.DeadlineExceeded, Unreachable},
		{"a connection that dropped", io.EOF, Unreachable},
		{"a connection that dropped inside a reply", io.ErrUnexpectedEOF, Unreachable},
		{"a closed socket", &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}, Unreachable},
		{"no free connection in time", redis.ErrPoolTimeout, Unreachable},

		{"a cancelled context", context.Canceled, Other},
		{"a closed client", redis.ErrClosed, Other},
		{"a pool with no room", redis.ErrPoolExhausted, Other},
		{"any other error", errors.New("row 3 holds no member"), Other},

		{"this package's: no address", ours, Unreachable},
		{"this package's, explained: the class it was made with", conn.Explain(answered("NOAUTH Authentication required.")), AuthRefused},
		{"this package's, explained: other", conn.Explain(errors.New("connection refused, said the script")), Unreachable},
	} {
		got := Classify(c.err)
		assert.Equal(t, c.want, got, "%s: Classify(%v) = %v; want %v", c.name, c.err, got, c.want)
		if c.err == nil {
			continue
		}
		got = Classify(fmt.Errorf("table list: %w", c.err))
		assert.Equal(t, c.want, got, "%s, wrapped: %v; want %v", c.name, got, c.want)
		got = Classify(fmt.Errorf("verb: %w", fmt.Errorf("table list: %w", c.err)))
		assert.Equal(t, c.want, got, "%s, wrapped twice: %v; want %v", c.name, got, c.want)
	}

	// An error that kept only its text is read by its words.
	for text, want := range map[string]Class{
		"table list: NOAUTH Authentication required.":                              AuthRefused,
		"WRONGPASS invalid username-password pair or user is disabled.":            AuthRefused,
		"failed to authenticate: ERR AUTH":                                         AuthRefused,
		"dial tcp 127.0.0.1:1: connect: connection refused":                        Unreachable,
		"dial tcp: lookup store.test: no such host":                                Unreachable,
		"read tcp 127.0.0.1:50000->127.0.0.1:6379: i/o timeout":                    Unreachable,
		"dial tcp 127.0.0.1:6379: connect: network is unreachable":                 Unreachable,
		"dial tcp 127.0.0.1:6379: connect: no route to host":                       Unreachable,
		"read tcp 127.0.0.1:50000->127.0.0.1:6379: read: connection reset by peer": Unreachable,
		"write tcp 127.0.0.1:50000->127.0.0.1:6379: write: broken pipe":            Unreachable,
		"NOAUTHORITY over this row":                                                Other,
		"WRONGTYPE Operation against a key holding the wrong kind of value":        Other,
		"": Other,
		"NOAUTH Authentication required. after connection refused: the login comes first": AuthRefused,
	} {
		got := Classify(errors.New(text))
		assert.Equal(t, want, got, "Classify of the text %q = %v; want %v", text, got, want)
	}
}

// TestClassNames: the three words, and the one for a value that is none of
// the three.
func TestClassNames(t *testing.T) {
	t.Parallel()
	for class, want := range map[Class]string{Other: "other", Unreachable: "unreachable", AuthRefused: "auth-refused", Class(7): "other", Class(-1): "other"} {
		got := class.String()
		assert.Equal(t, want, got, "Class(%d) reads %q; want %q", int(class), got, want)
		got = fmt.Sprintf("%v %s", class, class)
		assert.Equal(t, want+" "+want, got, "Class(%d) prints %q", int(class), got)
	}
	var zero Class
	assert.Equal(t, Other, zero, "the zero class is %v; want %v", zero, Other)
	for class, want := range map[Class]string{Other: "failed", Unreachable: "unreachable", AuthRefused: "login refused", Class(7): "failed"} {
		got := class.phrase()
		assert.Equal(t, want, got, "Class(%d) is said %q; want %q", int(class), got, want)
	}
}

// TestAFailureIsOneLine: whatever the address, the user and the cause hold,
// the message is one line and its control characters, line separators and
// bidi controls are visible. The characters are written by code point: none
// of them is in this file.
func TestAFailureIsOneLine(t *testing.T) {
	t.Parallel()
	const bs = "\\"
	separator, override := string(rune(0x2028)), string(rune(0x202e))
	l := login{Options: Options{Addr: "/var/run/a" + separator + "b.sock", User: "bench\r\nREFUSED", PasswordEnv: "PW"}}
	f := explain(l, hider("s3cret"), errors.New("first\nsecond\x00"+override), false)
	want := "redis at /var/run/a" + bs + "u2028b.sock as user bench" + bs + "x0d" + bs + "x0aREFUSED (password from PW): failed: first" + bs + "x0asecond" + bs + "x00" + bs + "u202e; next: the store answered, so the connection stands: read the refusal as the command's own"
	got := f.Error()
	assert.Equal(t, want, got, "\n got %s\nwant %s", got, want)
	for _, r := range f.Error() {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 || r == 0x202e {
			assert.False(t, r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 || r == 0x202e, "the message holds %U", r)
		}
	}
}

// TestHiderExamples: a text that holds the secret is withheld whole, a text
// without it is untouched, and what stands in its place holds no secret.
func TestHiderExamples(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ secret, text, want string }{
		{"", "anything at all", "anything at all"},
		{"", "", ""},
		{"s3cret", "", ""},
		{"s3cret", "nothing to hide", "nothing to hide"},
		{"s3cret", "s3cre and 3cret", "s3cre and 3cret"},
		{"s3cret", "s3cret", withheld},
		{"s3cret", "auth bench s3cret, then s3cret again", withheld},
		{"*", "a*b", withheld},
		{"***", "a***b", withheld},
		// A secret that is a word of the store's own refusal: its outline
		// is not left in the text.
		{"password", "WRONGPASS invalid username-password pair or user is disabled.", "***"},
		{"refused", "dial tcp 127.0.0.1:1: connect: connection refused", withheld},
		// A secret among the words that stand in the text's place.
		{"held", "it was held back", "***"},
		{"e", "EOF or not, there is an e in it", "***"},
		{withheld, "the text: " + withheld, "***"},
	} {
		got := hider(c.secret)(c.text)
		assert.Equal(t, c.want, got, "hider(%q)(%q) = %q; want %q", c.secret, c.text, got, c.want)
	}
}

// TestHiderProperty: for arbitrary secrets and arbitrary texts, what comes
// back never holds the secret, and a text that did not hold it comes back
// untouched. Fixed seeds: a failure names an input that fails again.
func TestHiderProperty(t *testing.T) {
	t.Parallel()
	pieces := []string{withheld, "withheld", "held", "***", "*", "**", "w", "it", " ", ":", "the password", "a", "\n", "%s", "\\", "\xff", "auth", "s3cret", ""}
	pieces = append(pieces, string(rune(0xe9)))
	for seed := uint64(1); seed <= 4; seed++ {
		r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
		draw := func(most int) string {
			var b strings.Builder
			for n := r.IntN(most + 1); n > 0; n-- {
				b.WriteString(pieces[r.IntN(len(pieces))])
			}
			return b.String()
		}
		for i := 0; i < 5000; i++ {
			secret := draw(3)
			text := draw(4)
			if r.IntN(2) == 0 {
				text += secret + draw(4)
			}
			got := hider(secret)(text)
			held := secret != "" && strings.Contains(text, secret)
			switch {
			case secret != "" && strings.Contains(got, secret):
				require.NotContains(t, got, secret, "seed %d case %d: hider(%q)(%q) = %q, which holds the secret", seed, i, secret, text, got)
			case !held && got != text:
				require.Equal(t, text, got, "seed %d case %d: hider(%q)(%q) = %q; the text did not hold the secret and was changed", seed, i, secret, text, got)
			case held && got != withheld && got != "***":
				require.True(t, got == withheld || got == "***", "seed %d case %d: hider(%q)(%q) = %q; want the text withheld", seed, i, secret, text, got)
			}
		}
	}
}

// TestPasswordHiddenInEncodedAndTruncatedDiagnostics: a handshake reply from
// an outside server or mock can echo the password with special characters or
// exceed the 100-character truncation limit of go-redis's %.100q wire reader.
// The error and unwrap chain must never expose the password, escaped or
// truncated.
func TestPasswordHiddenInEncodedAndTruncatedDiagnostics(t *testing.T) {
	t.Parallel()

	// 1. Password with double quote, escaped as \" in go-redis's %.100q map reply diagnostic.
	quotePass := `synth"pass`
	storeQuote := newFakeStore(t, func(_ int, cmd []string) string {
		return "+unexpected" + quotePass + "\r\n"
	})
	_, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": quotePass}), storeQuote.dial)
	require.Error(t, err, "expected Open to fail")
	for _, text := range errorsText(err) {
		require.NotContains(t, text, `synth"pass`, "error text exposed escaped password: %q", text)
		require.NotContains(t, text, `synth\"pass`, "error text exposed escaped password: %q", text)
	}
	cause := errors.Unwrap(err)
	require.NotNil(t, cause, "cause not withheld or has unwrap chain: %#v", cause)
	require.True(t, cause.Error() == withheld || cause.Error() == "***", "cause not withheld or has unwrap chain: %#v", cause)
	require.Nil(t, errors.Unwrap(cause), "cause not withheld or has unwrap chain: %#v", cause)
	got := Classify(err)
	assert.Equal(t, Other, got, "class = %v; want Other", got)

	// 2. Long password truncated at 100 characters in go-redis's %.100q map reply diagnostic.
	longPass := strings.Repeat("x", 150)
	storeLong := newFakeStore(t, func(_ int, cmd []string) string {
		return "+unexpected" + longPass + "\r\n"
	})
	_, err = open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": longPass}), storeLong.dial)
	require.Error(t, err, "expected Open to fail")
	for _, text := range errorsText(err) {
		require.NotContains(t, text, strings.Repeat("x", 20), "error text exposed truncated password: %q", text)
	}
	cause = errors.Unwrap(err)
	require.NotNil(t, cause, "cause not withheld or has unwrap chain: %#v", cause)
	require.True(t, cause.Error() == withheld || cause.Error() == "***", "cause not withheld or has unwrap chain: %#v", cause)
	require.Nil(t, errors.Unwrap(cause), "cause not withheld or has unwrap chain: %#v", cause)
	got = Classify(err)
	assert.Equal(t, Other, got, "class = %v; want Other", got)
}

// TestUnicodePreambleDoesNotExposePasswordPrefix: %.100q counts Unicode code
// points, not bytes. A non-ASCII preamble can expose fewer than 32 bytes of a
// long password past holdsSecret's tests unless rune count is checked.
func TestUnicodePreambleDoesNotExposePasswordPrefix(t *testing.T) {
	t.Parallel()
	const secret = "ReviewSyntheticLongPassword1234567890-unshared-tail"
	reply := "+" + strings.Repeat("x", 68) + "é" + secret + "\r\n"
	store := newFakeStore(t, func(_ int, _ []string) string {
		return reply
	})
	conn, err := open(context.Background(), Options{Addr: storeAddr, User: "review", PasswordEnv: "PW"}, environment(map[string]string{"PW": secret}), store.dial)
	if conn != nil {
		conn.Close()
		require.FailNow(t, "expected bad-handshake refusal")
	}
	require.Error(t, err, "expected refusal")
	for _, text := range errorsText(err) {
		assert.NotContains(t, text, secret[:30], "diagnostic exposes 30 bytes of synthetic password: %s", text)
	}
	cause := errors.Unwrap(err)
	require.NotNil(t, cause, "cause not withheld or has unwrap chain: %#v", cause)
	require.True(t, cause.Error() == withheld || cause.Error() == "***", "cause not withheld or has unwrap chain: %#v", cause)
	require.Nil(t, errors.Unwrap(cause), "cause not withheld or has unwrap chain: %#v", cause)
	got := Classify(err)
	assert.Equal(t, Other, got, "class = %v; want Other", got)
}
