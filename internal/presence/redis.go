package presence

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// PasswordEnv is the one environment variable the heartbeat reads its password
// from, and it is the same one `bench-row` hands redis-cli: the password is
// never a flag, never a file this tool opens and never a word in a log. It
// reaches this process through `nova-secrets exec --only
// NOVA_REDIS_BENCH_PASSWORD` and no other way.
const PasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"

// DefaultUser is the ACL user the beat and the line authenticate as. `~friend:*`
// was added to its rules for #2610; it can touch nothing else on the store.
const DefaultUser = "bench"

// Redis is the live store: a Store over one go-redis client.
type Redis struct{ rdb *redis.Client }

// Open dials addr as user, with the password from the environment when there is
// one. A store with no ACL -- a test's miniredis, a local Redis -- is dialled
// with no credentials at all rather than with an empty password, because an
// empty AUTH is a failed login and not an anonymous one.
//
// The address is host:port, the spelling every other verb uses; a `redis://`
// prefix is accepted because people paste one. A bare host is refused rather
// than completed, because the fleet store's port is 6380 and the default is
// 6379, and a tool that guesses that wrong reports a friend as away.
func Open(ctx context.Context, addr, user string) (*Redis, error) {
	a, err := Addr(addr)
	if err != nil {
		return nil, err
	}
	opts := &redis.Options{Addr: a}
	if pw := os.Getenv(PasswordEnv); pw != "" {
		if strings.TrimSpace(user) == "" {
			user = DefaultUser
		}
		opts.Username, opts.Password = user, pw
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		// The error carries the address and the diagnosis, never the
		// credential: redis returns "NOAUTH"/"WRONGPASS" and go-redis
		// does not echo the password back.
		if os.Getenv(PasswordEnv) == "" && isAuthError(err) {
			return nil, fmt.Errorf("store %s: %w; no %s in this environment -- run this under `nova-secrets exec --only %s`", a, err, PasswordEnv, PasswordEnv)
		}
		return nil, fmt.Errorf("store %s: %w", a, err)
	}
	return &Redis{rdb: rdb}, nil
}

// refusedAddr is the one refusal every bad --store gets, verbatim and with no
// input folded in: not the scheme, not the host, not a name for what was
// wrong. Comment 5783425783 (#2612) found the last hole in a message that
// still built itself from the input: addrProblems took the LAST "@" in the
// raw string before it ever found the query boundary, so a query VALUE that
// happened to contain "@" -- redis://store.invalid:6380?password=prefix@SECRET
// -- was cut at that "@" and everything after it, SECRET included, came back
// as the "host" the refusal printed. Any parser that first looks for a
// delimiter byte anywhere in the raw string and only then decides what
// region it was in can be fooled the same way by that byte appearing inside
// a region it hasn't found yet. A constant string has no such hole: it is
// built from nothing, so there is nothing in it left to extract.
const refusedAddr = "store address refused: only host:port or redis://host:port is supported"

// Addr normalizes what a caller spelled into host:port, or refuses it with
// refusedAddr, under a grammar strict enough that no shape of --store can
// carry a secret past this function: the only two spellings accepted are
// host:port and redis://host:port, structurally -- a host with none of
// "@/?#" in it (a bracketed [ipv6] host may of course contain ":"), a colon,
// and a port that is one to five ASCII digits naming 1-65535. A single bare
// trailing "/" is tolerated as the empty path a URL library would normalize
// away; anything else, at any position, is refused. The refusal never
// contains any byte of addr: it is the same constant for a bad scheme, a
// bad host, a bad port and a value with no port at all, because a message
// built from the input -- even just its host, even just its scheme -- is a
// message a crafted input can aim.
//
// This replaced two narrower repairs on the same bug (#2612): first an Addr
// that refused rediss:// and user:pass@ by name but let a query string
// straight through as part of a "clean" host:port; then one that named each
// bad shape (scheme/userinfo/path/query/fragment) by scanning the raw string
// for delimiter bytes with strings.LastIndex/Index -- which is exactly the
// class of bug that let a query value's own "@" be mistaken for the
// userinfo delimiter and its tail print as the host. Structural parsing --
// split on the redis:// prefix, then require the remainder to fully match
// host:port with nothing left over -- never asks "does this byte occur
// somewhere", so no byte inside an otherwise-refused value can be
// misattributed to a region that hasn't been located yet.
func Addr(addr string) (string, error) {
	a := strings.TrimSpace(addr)
	if a == "" {
		return "", fmt.Errorf("--store is empty")
	}

	rest := a
	if i := strings.Index(a, "://"); i >= 0 {
		scheme := a[:i]
		if scheme != "redis" {
			return "", errors.New(refusedAddr)
		}
		rest = a[i+len("://"):]
	}
	rest = strings.TrimSuffix(rest, "/") // a bare trailing "/" is an empty path, not a path

	if !validHostPort(rest) {
		return "", errors.New(refusedAddr)
	}
	return rest, nil
}

// validHostPort reports whether s is structurally exactly host:port: a host
// with none of "@/?#" (net.SplitHostPort itself handles the [ipv6]:port
// bracket form, whose host may contain ":") and a port that is all ASCII
// digits naming 1-65535. It never inspects s for a delimiter byte without
// first knowing, from the split, which region that byte would be in.
func validHostPort(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return false
	}
	if host == "" || strings.ContainsAny(host, "@/?#") {
		return false
	}
	return validPort(port)
}

// validPort reports whether s is one to five ASCII digits naming a port
// from 1 to 65535 -- never a sign, never a decimal point, never anything a
// query string or a fragment could still smuggle through as "numeric".
func validPort(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return false
	}
	return n >= 1 && n <= 65535
}

func isAuthError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "NOAUTH") || strings.Contains(s, "WRONGPASS") || strings.Contains(s, "NOPERM")
}

// Close releases the connection pool.
func (r *Redis) Close() error {
	if r == nil || r.rdb == nil {
		return nil
	}
	return r.rdb.Close()
}

// Set is SET key value [EX ttl]; a ttl of 0 writes no expiry.
func (r *Redis) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, value, ttl).Err()
}

// MGet is one MGET over every key, absent keys coming back as "".
func (r *Redis) MGet(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			out[i] = s
		}
	}
	return out, nil
}
