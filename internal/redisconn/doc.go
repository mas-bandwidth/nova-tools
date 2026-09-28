// Package redisconn is the one way a nova tool opens its Redis connection.
//
// It is what a tool uses in place of the deprecated internal/nsprint/store's
// Open, and it knows nothing of sprints, cards, seats or fleets: it imports
// the standard library, go-redis and internal/oneline.
//
// The API. Options says where the store is (Addr), who logs in (User), the
// NAME of the variable that holds the password (PasswordEnv, which
// PasswordOptional lets be empty for the default user) or a password handed
// over from memory (Password, a Secret named by its words), the pool's size
// and the dial's bound, and Env, the caller's names for the variables that
// supply whichever of the first three was not given. Resolve fills and
// refuses without dialing. Open dials once and returns within OpenTimeout
// with the handshake done, or with one error of this package. Conn is the
// connection: Client for commands and pipelines, Close, String (what every
// message about it opens with), Explain (a command's error as this
// package's), Trips (its round trips, the handshake's among them). Classify
// reads any error's class, Unreachable, AuthRefused, Unconfirmed or Other;
// Failed says whether an error is this package's, and its class. CountTrips
// counts a program's round trips; FirstError and Exec read a pipeline's
// first real error. No value of this package holds the password, and no
// error, String or formatting verb shows it.
//
// # A tool needs no wrapper around the client
//
// The connection explains its own failures: a command or pipeline on
// Client whose error is Unreachable, AuthRefused or Unconfirmed comes back
// as this package's one line, in what the call returns and in the command's
// Err, and every other error (redis.Nil, the store's refusals) as go-redis
// made it. So a tool tells the store's failures from its other failures by
// Failed, answers each class with its own exit code, and prints the error as
// it is; calling Explain on it again changes nothing.
//
// The three classes a tool answers differently, by where the failure stands
// against the command. Unreachable: nothing was sent (the dial failed, or
// the connection's setup did); the next step is the address or the store.
// AuthRefused: the login was refused. Unconfirmed: the command was written
// and its reply was lost, so a write may have committed; the next step is
// to read it back before retrying, and it is never said to be unreachable.
// tla/FirstConn.tla states this as the classification of the failure edge;
// it adds no state to the connection.
//
// # The address of a chain
//
// A tool that finds its address along a chain of its own (a flag, then one
// variable, then another) passes the flag as Addr when it won, and
// otherwise no Addr and the name of the variable that won as Env.Addr:
// Resolve reads it from there, and every message says the address came
// "from" that variable rather than "given to this tool".
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
