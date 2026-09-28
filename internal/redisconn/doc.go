// Package redisconn is the one way a nova tool opens its Redis connection.
//
// It is what a tool uses in place of the deprecated internal/nsprint/store's
// Open, and it knows nothing of sprints, cards, seats or fleets: it imports
// the standard library, go-redis and internal/oneline.
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
// reads nothing. The names of the deprecated sprint (NOVA_SPRINT_REDIS,
// NOVA_SPRINT_REDIS_USER, NOVA_SPRINT_REDIS_PASSWORD_ENV) are not aliases
// here: a tool that still reads them passes them.
//
// # Adopting it in place of internal/nsprint/store
//
// Three things the old store did are not here, each on purpose:
//
//   - The default password variable. The old store (through
//     internal/nsprint/redisauth) read the password from
//     NOVA_REDIS_BENCH_PASSWORD whenever a user was named and no password
//     variable was, and docs/CLI.md and docs/nova-sprint/README.md document
//     the pair NOVA_SPRINT_REDIS_USER=bench plus NOVA_REDIS_BENCH_PASSWORD on
//     that strength. Here a user with no password variable is refused before
//     the dial ("no password variable is named"), and no default is read in
//     its place. A caller that had the default names the variable itself: it
//     sets Options.PasswordEnv (NOVA_REDIS_BENCH_PASSWORD, for the fleet
//     store), or has the variable its Env.PasswordEnv names hold that name.
//   - Seat selection (--seat, NOVA_SEAT, internal/seatcred). It stays the
//     caller's: nova-table does it in cmd/nova-table/main.go (selectSeat)
//     and hands the seat's address, user and password variable to Options.
//   - The no-user hint (#3520). The old store wrapped a NOAUTH refusal with
//     "NOVA_SPRINT_REDIS_USER is unset but NOVA_REDIS_BENCH_PASSWORD is set;
//     set the pair NOVA_SPRINT_REDIS_USER=bench and NOVA_REDIS_BENCH_PASSWORD
//     (password from nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD,
//     never a flag)". Its general form is this package's own next step on a
//     login the store refused with no user named: "name the user (<Env.User>)
//     and the variable that holds its password (<Env.PasswordEnv>)", in the
//     caller's names. The deployment's words (bench, nova-secrets exec) are
//     the caller's to add to its own message.
package redisconn
