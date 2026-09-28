package redisconn

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Class is what a failure to work with the store comes down to, for a tool
// that answers each with its own exit code and its own next step.
type Class int

const (
	// Other is everything that is none of the three below: the store
	// answered and refused the command, the caller cancelled, the client was
	// closed. It is the zero value, and the class of a nil error.
	Other Class = iota
	// Unreachable is a store nothing was sent to: no address, a refused or
	// timed out dial, a name that does not resolve, no free connection in
	// time, a connection that dropped while it was being set up. The
	// command did not reach the store.
	Unreachable
	// AuthRefused is a login that was not accepted: refused by the store
	// (NOAUTH, WRONGPASS), or refused by Resolve before any dial because the
	// password could not be had.
	AuthRefused
	// Unconfirmed is a command that was sent and whose reply did not come:
	// the connection dropped, or the reply did not come in time, after the
	// command was written. The store may have done it, so a write may have
	// committed, and is read back before it is sent again. It is never said
	// to be Unreachable, which is a command that did not reach the store.
	Unconfirmed
)

// String is the class in one lower-case word: "other", "unreachable",
// "auth-refused" or "unconfirmed". A value outside the four reads "other".
func (c Class) String() string {
	switch c {
	case Unreachable:
		return "unreachable"
	case AuthRefused:
		return "auth-refused"
	case Unconfirmed:
		return "unconfirmed"
	}
	return "other"
}

// phrase is the class as a message says it.
func (c Class) phrase() string {
	switch c {
	case Unreachable:
		return "unreachable"
	case AuthRefused:
		return "login refused"
	case Unconfirmed:
		return "reply lost after the command was sent"
	}
	return "failed"
}

// Classify reads any error a tool holds after working with the store, from
// this package or straight from go-redis, wrapped or not, and answers its
// class. It is total and it never reads a secret.
//
// An error of this package keeps the class it was made with. Otherwise the
// store's own answer decides first: a refusal the store wrote is AuthRefused
// when it says NOAUTH or WRONGPASS and Other for anything else, never
// Unreachable, because a store that answers was reached. Then the transport,
// by whether the command can have been sent: a failed dial (a timed out one
// included), a name that does not resolve and a wait for a connection of the
// pool that ran out happen before anything is sent, and are Unreachable;
// any other failure of the transport (a dropped connection, EOF, a reset, a
// read or write that timed out, a deadline of the context) can come after
// the command was written, and is Unconfirmed. An error that kept only its
// text is read by the same words. Everything else, a cancelled context and a
// closed client among it, is Other.
//
// Classify sees only the error, so a dropped connection it cannot place is
// Unconfirmed: an uncertain write is never said to be unreachable. The hook
// Open installs knows more (a connection's setup that failed is before the
// command), and the errors it makes carry that class.
func Classify(err error) Class {
	if err == nil {
		return Other
	}
	var made *failure
	if errors.As(err, &made) {
		return made.class
	}
	var answered redis.Error
	if errors.As(err, &answered) {
		if startsWithAny(answered.Error(), "NOAUTH", "WRONGPASS", "ERR invalid password", "ERR invalid username-password pair") {
			return AuthRefused
		}
		return Other
	}
	var name *net.DNSError
	var op *net.OpError
	if errors.As(err, &name) || (errors.As(err, &op) && op.Op == "dial") || errors.Is(err, redis.ErrPoolTimeout) {
		return Unreachable
	}
	var transport net.Error
	if errors.As(err, &transport) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return Unconfirmed
	}
	text := err.Error()
	if containsAny(text, "NOAUTH ", "WRONGPASS ", "failed to authenticate") {
		return AuthRefused
	}
	if strings.HasPrefix(text, "dial ") || containsAny(text, "connection refused", "no such host", "network is unreachable", "no route to host", "connection pool timeout") {
		return Unreachable
	}
	if containsAny(text, "i/o timeout", "connection reset", "broken pipe") {
		return Unconfirmed
	}
	return Other
}

func startsWithAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsAny(s string, marks ...string) bool {
	for _, m := range marks {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// failure is the error of this package: a class, and one line that names
// what was tried, what came back and the next thing to do. Its text holds no
// secret: the login is named by its user and by the variable that holds the
// password, and a cause whose own text held the password is withheld
// (hider).
type failure struct {
	class Class
	tried string
	cause error
	next  string
}

// Failed reports whether err is, or wraps, an error of this package, and its
// class: a refusal of Resolve, a failure of Open, or an error Explain made,
// whether the caller called Explain or the connection's own hook did (Open).
// It is how a tool tells the store's failures from the other failures it
// holds of the same class, a database's among them; an error of any other
// package, go-redis's own included, is (Other, false).
func Failed(err error) (Class, bool) {
	var made *failure
	if errors.As(err, &made) {
		return made.class, true
	}
	return Other, false
}

// isFailure reports whether err is, or wraps, an error of this package.
func isFailure(err error) bool {
	_, ok := Failed(err)
	return ok
}

// Error is the one line: control characters, line separators and bidi
// controls in anything it quotes are escaped (oneline.Escape).
func (f *failure) Error() string {
	return oneline.Escape(f.tried + ": " + f.class.phrase() + ": " + f.cause.Error() + "; next: " + f.next)
}

// Unwrap is what came back, so errors.Is and errors.As see through.
func (f *failure) Unwrap() error { return f.cause }

// explain makes the failure of a connection as l out of what came back, of
// the class given. opening says the failure is Open's own rather than a
// later command's: Open sends nothing of the caller's, so a reply it lost is
// the store's not being reached.
func explain(l login, hide func(string) string, cause error, class Class, opening bool) *failure {
	if opening && class == Unconfirmed {
		class = Unreachable
	}
	f := &failure{class: class, tried: l.tried(), cause: cause}
	if text := cause.Error(); hide(text) != text {
		f.cause = errors.New(hide(text))
	}
	switch {
	case f.class == Unreachable:
		f.next = "start the store or correct the address, which was " + where(l.addrFrom)
	case f.class == Unconfirmed:
		// Rule (l): an uncertain write is never said to be absent or
		// unreachable.
		f.next = "the write may have committed, so read it back before retrying"
	case f.class == AuthRefused && l.empty && l.secret != "":
		f.next = "put the password of the default user in " + l.secret
	case f.class == AuthRefused && l.empty:
		// The caller's contract is that an empty variable is no password,
		// and the store wants one: the variable is the one to set.
		f.next = "export " + l.PasswordEnv + ", holding the password of the default user, in the environment of this process"
	case f.class == AuthRefused && l.User != "":
		f.next = "check that " + l.source() + " holds the password of " + l.User + " and that the store has that user switched on"
	case f.class == AuthRefused && l.source() != "":
		// A password and no user: the store's default user is off, or the
		// password is another user's. The old store's #3520 hint, in the
		// caller's own names.
		f.next = "name the user" + inVar(l.Env.User) + ", or check that " + l.source() + " holds the password of the default user"
	case f.class == AuthRefused:
		f.next = "name the user" + inVar(l.Env.User) + " and the variable that holds its password" + inVar(l.Env.PasswordEnv)
	case errors.Is(cause, context.Canceled) && opening:
		f.next = "run it again: it was cancelled before the store answered"
	case errors.Is(cause, context.Canceled):
		// Rule (l): the command may have been written before the cancel.
		f.next = "it was cancelled before its reply was read, so a write may have committed: read it back before running it again"
	case opening:
		f.next = "check that the address is a Redis store, version 6 or later"
	default:
		f.next = "the store answered, so the connection stands: read the refusal as the command's own"
	}
	return f
}

// withheld stands in the place of a text that held the password.
const withheld = "withheld: it held the password"

// hider returns the function that keeps secret out of a text that came from
// outside this package (a store's reply, a dialer's error), and this is its
// guarantee: what it returns never holds secret, whatever secret and the text
// are, and a text that did not hold it comes back untouched.
//
// A text that holds the secret is withheld whole. Taking out only the
// secret would leave its outline in a text whose other words are known: a
// password that is a word of the store's own refusal would be shown by the
// gap it left. The words that stand in the text's place hold no secret
// either: when the secret is among them, three asterisks stand there, and
// no secret is both. An empty secret hides nothing.
func hider(secret string) func(string) string {
	return func(text string) string {
		if secret == "" || !holdsSecret(text, secret) {
			return text
		}
		if strings.Contains(withheld, secret) {
			return "***"
		}
		return withheld
	}
}

// holdsSecret reports whether text holds secret as an exact substring, as a
// Go-escaped or quoted sequence (such as %.100q from go-redis's wire reader),
// or as a truncated diagnostic.
func holdsSecret(text, secret string) bool {
	if secret == "" {
		return false
	}
	if strings.Contains(text, secret) {
		return true
	}

	// Go's %q and strconv.Quote escapes quotes, backslashes and control characters.
	q := strconv.Quote(secret)
	escaped := q[1 : len(q)-1]
	if escaped != secret && strings.Contains(text, escaped) {
		return true
	}
	qa := strconv.QuoteToASCII(secret)
	escapedASCII := qa[1 : len(qa)-1]
	if escapedASCII != secret && escapedASCII != escaped && strings.Contains(text, escapedASCII) {
		return true
	}
	esc := oneline.Escape(secret)
	if esc != secret && strings.Contains(text, esc) {
		return true
	}

	// go-redis wire parse diagnostics quote wire replies with %.100q (e.g.
	// "redis: can't parse map reply: %.100q"). An unquoted wire line holds
	// the store's exact reply without Go escaping, bounded to 100 code points (runes).
	for _, quoted := range findQuotedStrings(text) {
		unquoted, err := strconv.Unquote(quoted)
		if err != nil {
			continue
		}
		if strings.Contains(unquoted, secret) {
			return true
		}
		if escaped != secret && strings.Contains(unquoted, escaped) {
			return true
		}
		// A line truncated at the 100-character bound of %.100q ends with a prefix of secret.
		if utf8.RuneCountInString(unquoted) == 100 {
			for k := min(len(secret), 100); k >= 3; k-- {
				if strings.HasSuffix(unquoted, secret[:k]) {
					return true
				}
			}
			if escaped != secret {
				for k := min(len(escaped), 100); k >= 3; k-- {
					if strings.HasSuffix(unquoted, escaped[:k]) {
						return true
					}
				}
			}
		}
		// A long secret whose beginning appears in the wire diagnostic.
		if len(secret) >= 16 {
			checkLen := min(len(secret), 32)
			if strings.Contains(unquoted, secret[:checkLen]) {
				return true
			}
			if len(escaped) >= 16 {
				checkEsc := min(len(escaped), 32)
				if strings.Contains(unquoted, escaped[:checkEsc]) {
					return true
				}
			}
		}
	}

	// A long plain or escaped secret truncated in a diagnostic outside quotes.
	if len(secret) >= 16 {
		checkLen := min(len(secret), 32)
		if strings.Contains(text, secret[:checkLen]) {
			return true
		}
		if len(escaped) >= 16 {
			checkEsc := min(len(escaped), 32)
			if strings.Contains(text, escaped[:checkEsc]) {
				return true
			}
		}
	}

	return false
}

// findQuotedStrings finds all double-quoted Go string literals within s.
func findQuotedStrings(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		start := strings.IndexByte(s[i:], '"')
		if start < 0 {
			break
		}
		start += i
		escaped := false
		end := -1
		for j := start + 1; j < len(s); j++ {
			if escaped {
				escaped = false
				continue
			}
			if s[j] == '\\' {
				escaped = true
				continue
			}
			if s[j] == '"' {
				end = j
				break
			}
		}
		if end < 0 {
			break
		}
		out = append(out, s[start:end+1])
		i = end + 1
	}
	return out
}
