package redisconn

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Class is what a failure to work with the store comes down to, for a tool
// that answers each with its own exit code and its own next step.
type Class int

const (
	// Other is everything that is neither of the two below: the store
	// answered and refused the command, the caller cancelled, the client was
	// closed. It is the zero value, and the class of a nil error.
	Other Class = iota
	// Unreachable is a store that could not be reached or did not answer in
	// time: no address, a refused or timed out dial, a name that does not
	// resolve, a connection that dropped, a reply that did not come.
	Unreachable
	// AuthRefused is a login that was not accepted: refused by the store
	// (NOAUTH, WRONGPASS), or refused by Resolve before any dial because the
	// password could not be had.
	AuthRefused
)

// String is the class in one lower-case word: "other", "unreachable" or
// "auth-refused". A value outside the three reads "other".
func (c Class) String() string {
	switch c {
	case Unreachable:
		return "unreachable"
	case AuthRefused:
		return "auth-refused"
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
// Unreachable, because a store that answers was reached. Then the transport:
// a timeout (a deadline of the context included), a failed dial, a name that
// does not resolve, a dropped connection (EOF) and a wait for a connection
// of the pool that ran out are Unreachable. An error that kept only its text
// is read by the same words. Everything else, a cancelled context and a
// closed client among it, is Other.
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
	var transport net.Error
	if errors.As(err, &transport) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, redis.ErrPoolTimeout) {
		return Unreachable
	}
	text := err.Error()
	if containsAny(text, "NOAUTH ", "WRONGPASS ", "failed to authenticate") {
		return AuthRefused
	}
	if containsAny(text, "connection refused", "no such host", "i/o timeout", "network is unreachable", "no route to host", "connection reset", "broken pipe") {
		return Unreachable
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

// isFailure reports whether err is, or wraps, an error of this package.
func isFailure(err error) bool {
	var made *failure
	return errors.As(err, &made)
}

// Error is the one line: control characters, line separators and bidi
// controls in anything it quotes are escaped (oneline.Escape).
func (f *failure) Error() string {
	return oneline.Escape(f.tried + ": " + f.class.phrase() + ": " + f.cause.Error() + "; next: " + f.next)
}

// Unwrap is what came back, so errors.Is and errors.As see through.
func (f *failure) Unwrap() error { return f.cause }

// explain makes the failure of a connection as l out of what came back.
// opening says the failure is Open's own rather than a later command's.
func explain(l login, hide func(string) string, cause error, opening bool) *failure {
	f := &failure{class: Classify(cause), tried: l.tried(), cause: cause}
	if text := cause.Error(); hide(text) != text {
		f.cause = errors.New(hide(text))
	}
	switch {
	case f.class == Unreachable:
		f.next = "start the store or correct the address, which was " + where(l.addrFrom)
	case f.class == AuthRefused && l.User != "":
		f.next = "check that " + l.PasswordEnv + " holds the password of " + l.User + " and that the store has that user switched on"
	case f.class == AuthRefused && l.PasswordEnv != "":
		// A password and no user: the store's default user is off, or the
		// password is another user's. The old store's #3520 hint, in the
		// caller's own names.
		f.next = "name the user" + inVar(l.Env.User) + ", or check that " + l.PasswordEnv + " holds the password of the default user"
	case f.class == AuthRefused:
		f.next = "name the user" + inVar(l.Env.User) + " and the variable that holds its password" + inVar(l.Env.PasswordEnv)
	case errors.Is(cause, context.Canceled):
		f.next = "run it again: it was cancelled before the store answered"
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
		if secret == "" || !strings.Contains(text, secret) {
			return text
		}
		if strings.Contains(withheld, secret) {
			return "***"
		}
		return withheld
	}
}
