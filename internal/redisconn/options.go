package redisconn

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The environment variables Resolve reads, each through the getenv it is
// handed and never from the process on its own. A value given to Resolve
// explicitly wins over all of them.
const (
	// EnvAddr names the store's address: host:port, or the absolute path of a
	// Unix socket.
	EnvAddr = "NOVA_REDIS_ADDR"
	// EnvUser names the ACL user that logs in; unset is the default user.
	EnvUser = "NOVA_REDIS_USER"
	// EnvPasswordEnv holds the NAME of the variable that holds the password,
	// never the password.
	EnvPasswordEnv = "NOVA_REDIS_PASSWORD_ENV"
)

// The older names of the same three variables, accepted as aliases. Each is
// read only when its general name above is unset or empty: when both are set
// the general name wins, and the alias is not consulted at all.
const (
	// AliasAddr is the older name of EnvAddr.
	AliasAddr = "NOVA_SPRINT_REDIS"
	// AliasUser is the older name of EnvUser.
	AliasUser = "NOVA_SPRINT_REDIS_USER"
	// AliasPasswordEnv is the older name of EnvPasswordEnv.
	AliasPasswordEnv = "NOVA_SPRINT_REDIS_PASSWORD_ENV"
)

// Options says where the store is and who logs in. It holds no secret and
// cannot: the password is named by the variable that holds it, so printing an
// Options, by any verb, prints nothing that must be kept.
type Options struct {
	// Addr is host:port (a name or an IP, a port from 1 to 65535) or the
	// absolute path of a Unix socket.
	Addr string
	// User is the ACL user; empty is the store's default user.
	User string
	// PasswordEnv is the NAME of the environment variable that holds the
	// password; empty is a login with no password.
	PasswordEnv string
}

// String renders the options as three key=value fields on one line, each
// value a single token (oneline.Field), whatever the values hold.
func (o Options) String() string {
	return "addr=" + oneline.Field(o.Addr) + " user=" + oneline.Field(o.User) + " password-env=" + oneline.Field(o.PasswordEnv)
}

// Resolve fills the options a tool was given from the environment and
// refuses the ones that cannot make a connection. It dials nothing.
//
// Each field is resolved on its own, in this order: the value in explicit
// when it is not empty, else the general variable (EnvAddr, EnvUser,
// EnvPasswordEnv), else its alias (AliasAddr, AliasUser, AliasPasswordEnv).
// getenv is the only environment Resolve reads; nil is an empty environment,
// which is how a caller says "only what I gave you".
//
// What comes back is ready for Open, and resolving it again changes nothing.
// The refusals, each an error of this package with its class (Classify), one
// line that names what was tried and the next thing to do:
//
//   - no address anywhere, or an address that is neither host:port nor an
//     absolute path (Unreachable). An address that is a URL or carries a
//     login is refused without being shown.
//   - a user with no password variable named (AuthRefused): the login is
//     never made as the default user instead.
//   - a password variable whose name is not capital letters, digits and
//     underscores (AuthRefused). The name is not shown, in case the password
//     was put where its variable's name belongs.
//   - a password variable that is named and empty (AuthRefused), whoever the
//     user is.
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
}

// resolve is Resolve, and the password beside it for Open. The password is
// returned on its own so that no value of this package has a field for it.
func resolve(explicit Options, getenv func(string) string) (login, string, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	var l login
	l.Addr, l.addrFrom = firstSet(explicit.Addr, getenv, EnvAddr, AliasAddr)
	l.User, l.userFrom = firstSet(explicit.User, getenv, EnvUser, AliasUser)
	l.PasswordEnv, l.envFrom = firstSet(explicit.PasswordEnv, getenv, EnvPasswordEnv, AliasPasswordEnv)

	const shape = "host:port (a port from 1 to 65535) or the absolute path of a Unix socket"
	switch {
	case l.Addr == "":
		return login{}, "", &failure{class: Unreachable, tried: "redis",
			cause: errors.New("no address: none was given, and " + EnvAddr + " and " + AliasAddr + " are empty"),
			next:  "pass the address or set " + EnvAddr + ": " + shape}
	case strings.Contains(l.Addr, "://") || strings.Contains(l.Addr, "@"):
		return login{}, "", &failure{class: Unreachable, tried: "redis at the address " + where(l.addrFrom) + " (not shown)",
			cause: errors.New("not an address: it is a URL or carries a login"),
			next:  "give " + shape + ", the user as " + EnvUser + " and the name of the password's variable as " + EnvPasswordEnv}
	}
	if fault := addrFault(l.Addr); fault != "" {
		return login{}, "", &failure{class: Unreachable, tried: "redis at " + oneline.Quote(l.Addr) + " " + where(l.addrFrom),
			cause: errors.New("not an address: " + fault),
			next:  "give " + shape}
	}

	if l.PasswordEnv == "" {
		if l.User == "" {
			return l, "", nil
		}
		return login{}, "", &failure{class: AuthRefused, tried: l.tried(),
			cause: errors.New("no password variable is named: none was given, and " + EnvPasswordEnv + " and " + AliasPasswordEnv + " are empty"),
			next:  "set " + EnvPasswordEnv + " to the NAME of the variable that holds the password of " + l.User + ", never to the password"}
	}
	if !envName(l.PasswordEnv) {
		return login{}, "", &failure{class: AuthRefused, tried: l.who() + " (the password's variable is not shown)",
			cause: errors.New("the name of the password's variable, " + where(l.envFrom) + ", is not a name, and is not shown in case it is the password itself"),
			next:  "give the NAME of the variable that holds the password (capital letters, digits and underscores), never the password"}
	}
	password := getenv(l.PasswordEnv)
	if password == "" {
		return login{}, "", &failure{class: AuthRefused, tried: l.tried(),
			cause: errors.New(l.PasswordEnv + " is empty"),
			next:  "export " + l.PasswordEnv + ", holding the password, in the environment of this process"}
	}
	return l, password, nil
}

// firstSet is the given value, else the first of the named variables that is
// not empty, with the name of the variable that supplied it ("" for given).
func firstSet(given string, getenv func(string) string, names ...string) (value, from string) {
	if given != "" {
		return given, ""
	}
	for _, name := range names {
		if v := getenv(name); v != "" {
			return v, name
		}
	}
	return "", ""
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
	if l.PasswordEnv != "" {
		return l.who() + " (password from " + l.PasswordEnv + ")"
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
