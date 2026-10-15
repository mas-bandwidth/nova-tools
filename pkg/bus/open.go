package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/redis/go-redis/v9"
)

// The store's address and login (SPEC-BUS.md, the config and the identity),
// one place for every tool that opens the bus store: nova-bus and nova-hook.
// RedisEnv names the store when --redis does not; with neither, the store is
// the fleet row's bus field as nova-config apply wrote it (FleetBusKey) into
// the sprint store at SprintRedisEnv, so no friend types the address.
const (
	RedisEnv       = "NOVA_BUS_REDIS"
	SprintRedisEnv = "NOVA_SPRINT_REDIS"
	FleetBusKey    = "fleet:bus"
)

// A friend's applied session, as nova-config apply writes it (pkg/config
// FriendDesiredKey and FriendSessionOneShot; TestTheBusReadsTheSessionApplyWrites
// holds the copies equal): the session field of FriendDesiredKey(name) says
// SessionOneShot for a friend with no live session, who is never on the psychic
// link (the v1.3 comms spec, guarantee 19). Kept here, not imported, so the
// bus does not link the configuration store's Postgres driver.
const (
	SessionField   = "session"
	SessionOneShot = "one-shot"
)

// FriendDesiredKey is the hash apply writes a friend row's desired fields to.
func FriendDesiredKey(name string) string { return "friend:" + name + ":desired" }

// LoginOptions is the fleet's login for a store at addr: NOVA_SPRINT_REDIS_USER
// names the user and NOVA_SPRINT_REDIS_PASSWORD_ENV the variable that holds its
// password (NOVA_REDIS_BENCH_PASSWORD when it names none); no user is the
// default user with no password. The password is never on the line and never
// printed (pkg/redisconn).
func LoginOptions(addr string, getenv func(string) string) redisconn.Options {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	return o
}

// ReadFleetBus is one GET of FleetBusKey on the sprint store at addr: the
// applied fleet row's bus address, "" when the row has none.
func ReadFleetBus(ctx context.Context, addr string, getenv func(string) string) (string, error) {
	conn, err := redisconn.Open(ctx, LoginOptions(addr, getenv), getenv)
	if err != nil {
		return "", err
	}
	defer conn.Close() // ignored: a connection opened for one GET; the GET's answer is the result
	v, err := conn.Client().Get(ctx, FleetBusKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// OpenRedis dials the bus store at addr through redisconn, the one way a nova
// tool opens Redis, with the fleet's login, and says which user it logged in
// as ("" when the store has no login: the default user), the identity every
// verb acts as.
func OpenRedis(ctx context.Context, addr string, getenv func(string) string) (Redis, string, func(), error) {
	o := LoginOptions(addr, getenv)
	resolved, err := redisconn.Resolve(o, getenv)
	if err != nil {
		return Redis{}, "", nil, err
	}
	conn, err := redisconn.Open(ctx, o, getenv)
	if err != nil {
		return Redis{}, "", nil, err
	}
	return Redis{C: conn.Client()}, resolved.User, func() { conn.Close() }, nil // ignored: closing at exit, nothing is left to report it to
}

// Address is the store a verb opens: given (a verb's --redis, whose default
// is RedisEnv), else the fleet row's bus field read from the sprint store at
// SprintRedisEnv; with none of the three, a refusal naming all three. The
// refusal is "" when there is an address.
func Address(ctx context.Context, given string, getenv func(string) string, fleetBus func(ctx context.Context, addr string) (string, error)) (addr, refusal string) {
	if strings.TrimSpace(given) != "" {
		return given, ""
	}
	sprint := getenv(SprintRedisEnv)
	if sprint == "" {
		return "", "--redis is required: " + RedisEnv + " is unset, and with no " + SprintRedisEnv + " the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess"
	}
	addr, err := fleetBus(ctx, sprint)
	if err != nil {
		return "", "--redis is required: " + RedisEnv + " is unset and the fleet's bus row could not be read from the sprint store: " + err.Error()
	}
	if addr == "" {
		return "", "--redis is required: " + RedisEnv + " is unset and the fleet's bus row is empty; set it once: nova-config fleet set --bus <host:port> --as <you>, then nova-config apply"
	}
	return addr, ""
}

// Identity is who a verb acts as: the user the connection logged in as, which
// as may repeat and never contradict (the store's login is the identity, not
// a word on the line; SPEC-BUS.md, the identity); with no login user, as
// alone. The refusal is "" when there is an identity.
func Identity(as, login string) (name, refusal string) {
	switch {
	case login == "" && strings.TrimSpace(as) == "":
		return "", "--as is required: this connection has no login user (" + redisauth.UserEnv + " is unset), so it wants your name; refusing to guess"
	case login != "" && as != "" && as != login:
		return "", fmt.Sprintf("--as %s is not the login user %s: this connection acts as %s; drop --as, or log in as %s (%s=%s with its password)", as, login, login, as, redisauth.UserEnv, as)
	case login != "":
		return login, ""
	}
	return as, ""
}
