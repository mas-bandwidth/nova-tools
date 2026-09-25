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
type Redis struct {
	rdb  *redis.Client
	addr string
}

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
	// No PING (#3277): the first command dials and authenticates, and its
	// error comes back through fail with the same diagnosis.
	return &Redis{rdb: redis.NewClient(opts), addr: a}, nil
}

// fail adds the remedy to a command's error when the store refused the login
// and no password is in the environment; any other error passes unchanged (a
// dial error already names the address). It never carries the credential:
// redis returns "NOAUTH"/"WRONGPASS" and go-redis does not echo the password.
func (r *Redis) fail(err error) error {
	if err == nil || os.Getenv(PasswordEnv) != "" || !isAuthError(err) {
		return err
	}
	return fmt.Errorf("store %s: %w; no %s in this environment -- run this under `nova-secrets exec --only %s`", r.addr, err, PasswordEnv, PasswordEnv)
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

// WriteBeat is one MULTI/EXEC under WATCH key: HSET key fields..., PEXPIRE key
// ttl, SET lastKey stamp with no expiry. Before it, TYPE key (and HEXISTS key
// up for a hash) decides whether the key is the beat's to write: absent, the
// beat's own hash, or a plain string left by a beat older than #2673 (deleted
// in the same MULTI). The friend row, or a key of any other type, answers a
// *KeyTypeError and nothing is written (#3447). The WATCH makes the check and
// the write one step: a row loop that creates the row in between fails the
// EXEC, and the beat checks again.
func (r *Redis) WriteBeat(ctx context.Context, key string, fields []string, ttl time.Duration, lastKey, stamp string) error {
	if len(fields) == 0 || len(fields)%2 != 0 {
		return fmt.Errorf("beat fields must be field, value pairs")
	}
	args := make([]interface{}, len(fields))
	for i, f := range fields {
		args[i] = f
	}
	write := func(tx *redis.Tx) error {
		typ, err := tx.Type(ctx, key).Result()
		if err != nil {
			return err
		}
		switch typ {
		case "none", "string":
		case "hash":
			row, err := tx.HExists(ctx, key, FieldUp).Result()
			if err != nil {
				return err
			}
			if row {
				return &KeyTypeError{Key: key, Type: typ, Row: true}
			}
		default:
			return &KeyTypeError{Key: key, Type: typ}
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			if typ == "string" {
				p.Del(ctx, key)
			}
			p.HSet(ctx, key, args...)
			p.PExpire(ctx, key, ttl)
			p.Set(ctx, lastKey, stamp, 0)
			return nil
		})
		return err
	}
	var err error
	for try := 0; try < 3; try++ {
		if err = r.rdb.Watch(ctx, write, key); !errors.Is(err, redis.TxFailedErr) {
			return r.fail(err)
		}
	}
	return err
}

// ReadBeats is one pipeline: per key HMGET key at width window up, PTTL key
// and GET key:last. An absent key or field answers "". A key that is not a
// hash (a beat older than #2673) reads its TTL and no fields. A hash with the
// up field is the friend row (Reading.Row).
func (r *Redis) ReadBeats(ctx context.Context, keys []string) ([]Reading, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	type cmds struct {
		fields *redis.SliceCmd
		ttl    *redis.DurationCmd
		last   *redis.StringCmd
	}
	cs := make([]cmds, len(keys))
	p := r.rdb.Pipeline()
	for i, k := range keys {
		cs[i] = cmds{
			fields: p.HMGet(ctx, k, FieldAt, FieldWidth, FieldWindow, FieldUp),
			ttl:    p.PTTL(ctx, k),
			last:   p.Get(ctx, k+LastSuffix),
		}
	}
	// Exec answers the first failed command's error; each command is read
	// on its own below, where a missing :last (redis.Nil) and a legacy
	// string key (WRONGTYPE) are readings, not failures.
	_, _ = p.Exec(ctx)
	out := make([]Reading, len(keys))
	for i, c := range cs {
		d, err := c.ttl.Result()
		if err != nil {
			return nil, r.fail(err)
		}
		out[i].Live = d > 0
		if vals, err := c.fields.Result(); err == nil {
			str := func(j int) string {
				if j < len(vals) {
					if s, ok := vals[j].(string); ok {
						return s
					}
				}
				return ""
			}
			out[i].At, out[i].Width, out[i].Window, out[i].Up = str(0), str(1), str(2), str(3)
			if len(vals) > 3 && vals[3] != nil {
				out[i].Row = true
			}
		} else if !strings.Contains(err.Error(), "WRONGTYPE") {
			return nil, err
		}
		if v, err := c.last.Result(); err == nil {
			out[i].Last = v
		} else if !errors.Is(err, redis.Nil) && !strings.Contains(err.Error(), "WRONGTYPE") {
			return nil, err
		}
	}
	return out, nil
}

// Members is SMEMBERS set.
func (r *Redis) Members(ctx context.Context, set string) ([]string, error) {
	m, err := r.rdb.SMembers(ctx, set).Result()
	return m, r.fail(err)
}
