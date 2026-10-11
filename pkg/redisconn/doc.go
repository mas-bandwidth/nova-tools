// Package redisconn is the one way a nova tool opens its Redis connection.
//
// It is what a tool uses in place of the deprecated internal/nsprint/store's
// Open, and it knows nothing of sprints, cards, seats or fleets: it imports
// the standard library, go-redis and pkg/oneline.
//
// The API. Options says where the store is (Addr), who logs in (User), the
// NAME of the variable that holds the password (PasswordEnv), and Env, the
// caller's names for the variables that supply whichever of the three was not
// given. Resolve fills and refuses without dialing. Open dials once and
// returns within OpenTimeout with the handshake done, or with one error of
// this package. Conn is the connection: Client for commands and pipelines,
// Close, String (what every message about it opens with), Explain (a
// command's error as this package's). Classify reads any error's class,
// Unreachable, AuthRefused or Other. CountTrips counts a program's round
// trips; FirstError and Exec read a pipeline's first real error. No value of
// this package holds the password, and no error, String or formatting verb
// shows it.
//
// # The names come from the caller
//
// This package names no tool's environment variable and no deployment's. A
// tool passes its own names in Options.Env, or GeneralEnv (NOVA_REDIS_ADDR,
// NOVA_REDIS_USER, NOVA_REDIS_PASSWORD_ENV) when it has none; the zero Env
// reads nothing, and no older name of any tool is an alias here. A user with
// no password variable named is refused before the dial, and no default
// variable is read in its place: a caller that relied on a deployment's
// default password variable names that variable itself, in Options.PasswordEnv
// or through the variable its Env.PasswordEnv names. How a caller chooses its
// login (a seat, a profile, flags) is the caller's, and so are the words of
// its deployment in the message it prints; this package's next step on a
// login the store refused names the caller's variables and nothing else.
package redisconn
