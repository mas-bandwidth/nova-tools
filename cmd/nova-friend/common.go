package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/redis/go-redis/v9"
)

// The environment nova-friend reads, through the getenv seam and nowhere
// else (a test hands in its own map; never t.Setenv).
const (
	seatEnv        = "NOVA_FRIEND"
	envSprintRedis = "NOVA_SPRINT_REDIS"
	envRedisAddr   = "NOVA_REDIS_ADDR"
)

// redisHelp is the one help line every --redis flag carries.
const redisHelp = "redis address (default NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's)"

// asHelp is the one help line every --as flag carries.
const asHelp = "you: your friend name (default NOVA_FRIEND, which it must equal when set)"

// env is the process environment as the verbs see it.
type env struct{ getenv func(string) string }

// redis is the store address: the flag, else the environment; "" lets
// store.Open fall back to the seat's row.
func (e env) redis(flag string) string {
	if flag != "" {
		return flag
	}
	if v := e.getenv(envSprintRedis); v != "" {
		return v
	}
	return e.getenv(envRedisAddr)
}

// actor is who runs the verb: --as, which must be the seat (NOVA_FRIEND)
// when one is set, else the seat itself. Neither is a usage refusal naming
// what --as wants.
func (e env) actor(as string) (string, error) {
	seat := e.getenv(seatEnv)
	switch {
	case as == "" && seat == "":
		return "", fmt.Errorf("--as wants your friend name (%s is empty)", seatEnv)
	case as == "":
		as = seat
	case seat != "" && as != seat:
		return "", fmt.Errorf("--as %s is not the seat (%s=%s)", as, seatEnv, seat)
	}
	return as, checkName(as)
}

// checkName holds a friend name to the consumer rule (friend:<name>) in
// lower case: every friend:* key is spelled from it.
func checkName(name string) error {
	if name != strings.ToLower(name) {
		return fmt.Errorf("friend name %q wants lower case", name)
	}
	if _, err := taskcard.ParseConsumer("friend:" + name); err != nil {
		return fmt.Errorf("friend name %q wants letters, digits, dots and dashes", name)
	}
	return nil
}

// newFlags is the quiet flag set of one verb: a parse error comes back
// from Parse; -h unwinds to the dispatcher, which prints every flag with
// its help line.
func newFlags(verb string) *flag.FlagSet { return verbflag.New(verb) }

// parseNamed parses fs over args and returns the one positional, the friend
// the verb is about, which may stand before or after the flags (`away
// stella --as rowan` and `away --as rowan stella` are one call).
func parseNamed(fs *flag.FlagSet, args []string) (string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			pos = append(pos, rest...)
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	if len(pos) != 1 {
		return "", fmt.Errorf("want one friend name; flags may come before or after it")
	}
	if err := checkName(pos[0]); err != nil {
		return "", err
	}
	return pos[0], nil
}

// openStore dials the fleet store. A missing address is named with the
// three places one comes from.
func openStore(ctx context.Context, addr string) (*store.Store, error) {
	st, err := store.Open(ctx, addr)
	if err != nil && strings.Contains(err.Error(), "redis address is required") {
		return nil, fmt.Errorf("want --redis <addr> (or %s, %s, or a seat)", envSprintRedis, envRedisAddr)
	}
	return st, err
}

// openLoopStore is openStore with one connection, for the here loop: every
// tick rides the one authenticated connection.
func openLoopStore(ctx context.Context, addr string) (*store.Store, error) {
	st, err := store.OpenSingle(ctx, addr)
	if err != nil && strings.Contains(err.Error(), "redis address is required") {
		return nil, fmt.Errorf("want --redis <addr> (or %s, %s, or a seat)", envSprintRedis, envRedisAddr)
	}
	return st, err
}

// registered says whether name is in the roster nova-config applies (the
// friends set); false comes with the remedy.
func registered(ctx context.Context, c redis.Cmdable, name string) (bool, error) {
	ok, err := c.SIsMember(ctx, "friends", name).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, fmt.Errorf("read friends: %w", err)
	}
	return ok, nil
}

// unregistered is the refusal for a name the roster does not hold.
func unregistered(name string) string {
	return fmt.Sprintf("UNREGISTERED %s: not in the roster; run: nova-config friend add %s --slots <n> --as <you>, then nova-config apply", name, name)
}

// words are a Lua reply's values as strings.
func words(reply any) []string {
	values, ok := reply.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// word is words[i], or "" past the end.
func word(w []string, i int) string {
	if i < len(w) {
		return w[i]
	}
	return ""
}

// newSession is a fresh presence session identity.
func newSession() (string, error) {
	var bits [16]byte
	if _, err := rand.Read(bits[:]); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return hex.EncodeToString(bits[:]), nil
}

// quoteField keeps a value with spaces one field on a typed line.
func quoteField(s string) string {
	if s == "" {
		return "-"
	}
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}

// dash is "-" for an empty value.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// yesNo spells a bool on a typed line.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
