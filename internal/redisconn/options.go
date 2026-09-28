package redisconn

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Env names the environment variables Resolve reads for the fields of an
// Options that were left empty: one variable per field, each read through
// the getenv Resolve is handed and never from the process on its own. A name
// that is empty is not read, so the zero Env reads nothing: what was given is
// all there is. The names are the caller's. This package has no name of its
// own for any tool's variable and keeps no older name as an alias: a tool
// that reads its own names passes them, and a tool with none passes
// GeneralEnv.
type Env struct {
	// Addr names the variable that holds the store's address: host:port, or
	// the absolute path of a Unix socket.
	Addr string
	// User names the variable that holds the ACL user; unset is the default
	// user.
	User string
	// PasswordEnv names the variable that holds the NAME of the variable
	// that holds the password, never the password.
	PasswordEnv string
}

// Secret is a password handed over from memory, for a caller that holds it
// (a seat's, read through the secrets library) and must not put it in the
// environment. It holds the function that reads the password, never the
// password, so printing a Secret, by any verb, prints nothing that must be
// kept.
type Secret struct {
	// From is the words every message names the password by, such as "the
	// seat bench-3"; empty is "the caller's secret". No message shows the
	// password, and words that hold it are not shown either.
	From string
	// Read returns the password. Resolve calls it once and Open once, when
	// each runs.
	Read func() string
}

// GeneralEnv is the general set of names, for a tool that has none of its
// own: NOVA_REDIS_ADDR, NOVA_REDIS_USER and NOVA_REDIS_PASSWORD_ENV.
var GeneralEnv = Env{Addr: "NOVA_REDIS_ADDR", User: "NOVA_REDIS_USER", PasswordEnv: "NOVA_REDIS_PASSWORD_ENV"}

// Options says where the store is and who logs in. It holds no secret and
// cannot: the password is named by the variable that holds it, or handed over
// by a function (Password), so printing an Options, by any verb, prints
// nothing that must be kept.
type Options struct {
	// Addr is host:port (a name or an IP, a port from 1 to 65535) or the
	// absolute path of a Unix socket.
	Addr string
	// User is the ACL user; empty is the store's default user.
	User string
	// PasswordEnv is the NAME of the environment variable that holds the
	// password; empty is a login with no password.
	PasswordEnv string
	// PasswordOptional says that the variable PasswordEnv names may be empty
	// or unset, and that then the login is the default user with no
	// password: for a tool whose contract is that an empty password variable
	// is no password. It never applies to a named user, whose login is made
	// with a password or not at all (an empty password would be the default
	// user's login in its place). The default, false, refuses an empty one.
	PasswordOptional bool
	// Password hands the password over from memory (Secret). It is used
	// when PasswordEnv is not given, and then in place of Env.PasswordEnv.
	Password *Secret
	// PoolSize is the most connections the client holds at once; zero or
	// less is go-redis's default. One is a client whose every command waits
	// for the one before it.
	PoolSize int
	// DialTimeout bounds each dial of the connection, Open's and every later
	// one. Zero or less, or more than the package's DialTimeout, is the
	// package's DialTimeout: it can shorten the bound, never lengthen it.
	DialTimeout time.Duration
	// Env names the variables Resolve reads for whichever of the three above
	// is empty. The zero Env reads nothing.
	Env Env
}

// String renders the options as three key=value fields on one line, each
// value a single token (oneline.Field), whatever the values hold.
func (o Options) String() string {
	return "addr=" + oneline.Field(o.Addr) + " user=" + oneline.Field(o.User) + " password-env=" + oneline.Field(o.PasswordEnv)
}

// Resolve fills the options a tool was given from the environment and
// refuses the ones that cannot make a connection. It dials nothing.
//
// Each field is resolved on its own: the value in explicit when it is not
// empty, else the variable explicit.Env names for it, when it names one.
// getenv is the only environment Resolve reads; nil is an empty environment,
// which is how a caller says "only what I gave you", as is the zero Env.
//
// What comes back is ready for Open, and resolving it again changes nothing.
// The refusals, each an error of this package with its class (Classify), one
// line that names what was tried and the next thing to do:
//
//   - no address anywhere, or an address that is neither host:port nor an
//     absolute path (Unreachable). An address that is a URL or carries a
//     login is refused without being shown.
//   - a user with no password variable named (AuthRefused): the login is
//     never made as the default user instead, and no default variable is
//     read in its place.
//   - a password variable whose name is not capital letters, digits and
//     underscores (AuthRefused). The name is not shown, in case the password
//     was put where its variable's name belongs.
//   - a password variable that is named and empty (AuthRefused), whoever the
//     user is, unless PasswordOptional is set and no user is named: then
//     the login is the default user with no password.
//
// No user and no password variable is the default user with no password, so
// a throwaway store needs nothing but its address.
func Resolve(explicit Options, getenv func(string) string) (Options, error) {
	l, _, err := resolve(explicit, getenv)
	if err != nil {
		return Options{}, err
	}
	return l.Options, nil
}

// login is the resolved options and where each came from: "" when it was
// given explicitly, else the variable that supplied it. It never holds the
// password.
type login struct {
	Options
	addrFrom, userFrom, envFrom string
	// secret is the words that name a password handed over by
	// Options.Password, "" when it came from a variable or there is none.
	secret string
	// empty is an optional password that was empty: the login is the
	// default user with no password (Options.PasswordOptional).
	empty bool
}

// resolve is Resolve, and the password beside it for Open. The password is
// returned on its own so that no value of this package has a field for it.
func resolve(explicit Options, getenv func(string) string) (login, string, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	l := login{Options: explicit}
	l.Addr, l.addrFrom = pick(explicit.Addr, getenv, l.Env.Addr)
	l.User, l.userFrom = pick(explicit.User, getenv, l.Env.User)
	if explicit.Password == nil || explicit.Password.Read == nil {
		l.PasswordEnv, l.envFrom = pick(explicit.PasswordEnv, getenv, l.Env.PasswordEnv)
	}

	const shape = "host:port (a port from 1 to 65535) or the absolute path of a Unix socket"
	switch {
	case l.Addr == "":
		return login{}, "", &failure{class: Unreachable, tried: "redis",
			cause: errors.New("no address: none was given" + orEmpty(l.Env.Addr)),
			next:  "pass the address" + orSet(l.Env.Addr) + ": " + shape}
	case strings.Contains(l.Addr, "://") || strings.Contains(l.Addr, "@"):
		return login{}, "", &failure{class: Unreachable, tried: "redis at the address " + where(l.addrFrom) + " (not shown)",
			cause: errors.New("not an address: it is a URL or carries a login"),
			next:  "give " + shape + ", the user" + inVar(l.Env.User) + " and the name of the password's variable" + inVar(l.Env.PasswordEnv) + " on their own"}
	}
	if fault := addrFault(l.Addr); fault != "" {
		return login{}, "", &failure{class: Unreachable, tried: "redis at " + oneline.Quote(l.Addr) + " " + where(l.addrFrom),
			cause: errors.New("not an address: " + fault),
			next:  "give " + shape}
	}

	if l.PasswordEnv == "" && l.Password != nil && l.Password.Read != nil {
		return l.handed()
	}
	if l.PasswordEnv == "" {
		if l.User == "" {
			return l, "", nil
		}
		next := "give the NAME of the variable that holds the password of " + l.User + ", never the password"
		if l.Env.PasswordEnv != "" {
			next = "set " + l.Env.PasswordEnv + " to the NAME of the variable that holds the password of " + l.User + ", never to the password"
		}
		return login{}, "", &failure{class: AuthRefused, tried: l.tried(),
			cause: errors.New("no password variable is named: none was given" + orEmpty(l.Env.PasswordEnv)),
			next:  next}
	}
	if !envName(l.PasswordEnv) {
		return login{}, "", &failure{class: AuthRefused, tried: l.who() + " (the password's variable is not shown)",
			cause: errors.New("the name of the password's variable, " + where(l.envFrom) + ", is not a name, and is not shown in case it is the password itself"),
			next:  "give the NAME of the variable that holds the password (capital letters, digits and underscores), never the password"}
	}
	password := getenv(l.PasswordEnv)
	if password == "" {
		if l.PasswordOptional && l.User == "" {
			l.empty = true
			return l, "", nil
		}
		return login{}, "", &failure{class: AuthRefused, tried: l.tried(),
			cause: errors.New(l.PasswordEnv + " is empty"),
			next:  "export " + l.PasswordEnv + ", holding the password, in the environment of this process"}
	}
	return l, password, nil
}

// handed is the last step of resolve for a password handed over by
// Options.Password.
func (l login) handed() (login, string, error) {
	password := l.Password.Read()
	l.secret = l.Password.From
	if l.secret == "" {
		l.secret = "the caller's secret"
	}
	if hide := hider(password); hide(l.secret) != l.secret {
		l.secret = "a secret whose name is not shown"
	}
	if password == "" {
		if l.PasswordOptional && l.User == "" {
			l.empty = true
			return l, "", nil
		}
		return login{}, "", &failure{class: AuthRefused, tried: l.tried(),
			cause: errors.New(l.secret + " holds no password"),
			next:  "put the password of " + l.whose() + " in " + l.secret}
	}
	return l, password, nil
}

// whose names, in words, the user whose password it is.
func (l login) whose() string {
	if l.User == "" {
		return "the default user"
	}
	return l.User
}

// source is the words that name where the password comes from: its
// variable, or the words of a password handed over; "" for none.
func (l login) source() string {
	if l.secret != "" {
		return l.secret
	}
	return l.PasswordEnv
}

// pick is the given value, else the value of the named variable when a name
// is given and the variable is not empty, with the name of the variable that
// supplied it ("" for given).
func pick(given string, getenv func(string) string, name string) (value, from string) {
	if given != "" {
		return given, ""
	}
	if name != "" {
		if v := getenv(name); v != "" {
			return v, name
		}
	}
	return "", ""
}

// orEmpty says, when a variable was named, that it is empty.
func orEmpty(name string) string {
	if name == "" {
		return ""
	}
	return ", and " + name + " is empty"
}

// orSet offers, when a variable was named, to set it.
func orSet(name string) string {
	if name == "" {
		return ""
	}
	return " or set " + name
}

// inVar names, when a variable was named, the variable in parentheses.
func inVar(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}

// where says in words where a value came from.
func where(from string) string {
	if from == "" {
		return "given to this tool"
	}
	return "from " + from
}

// who names the store and the user.
func (l login) who() string {
	if l.User != "" {
		return "redis at " + l.Addr + " as user " + l.User
	}
	return "redis at " + l.Addr + " as the default user"
}

// tried is who and where the password comes from: the words every message
// about a connection opens with.
func (l login) tried() string {
	if l.empty {
		return l.who() + ", no password (" + l.source() + " is empty)"
	}
	if l.source() != "" {
		return l.who() + " (password from " + l.source() + ")"
	}
	return l.who() + ", no password"
}

// network is the dialer's network for an address Resolve accepted.
func network(addr string) string {
	if strings.HasPrefix(addr, "/") {
		return "unix"
	}
	return "tcp"
}

// addrFault says why addr is neither host:port nor an absolute path, and ""
// when it is one of them.
func addrFault(addr string) string {
	path := strings.HasPrefix(addr, "/")
	for _, r := range addr {
		if unicode.IsControl(r) || (!path && unicode.IsSpace(r)) {
			return "it holds a space or a control character"
		}
	}
	if path {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// net says "address <addr>: <why>"; the address is already in the
		// message, so only the why is kept.
		why := err.Error()
		var ae *net.AddrError
		if errors.As(err, &ae) {
			why = ae.Err
		}
		return why
	}
	if host == "" {
		return "it names no host"
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		return "its port is not a number from 1 to 65535"
	}
	return ""
}

// envName reports whether s has the shape of an environment variable's name
// as this repository writes them: capital letters, digits and underscores,
// not starting with a digit.
func envName(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return s != ""
}
