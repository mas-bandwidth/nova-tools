package presence

import (
	"context"
	"fmt"
	"os"
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

// Addr normalizes what a caller spelled into host:port, or says why it
// cannot, under a grammar strict enough that no shape of --store can carry a
// secret past this function: the only two spellings accepted are host:port
// and redis://host:port, with no userinfo, no query, no path (a single bare
// trailing "/" is tolerated as the empty path a URL library would normalize
// away) and no fragment. Anything else is refused.
//
// This replaced a looser Addr (comment 5782441213, #2612) that refused
// rediss:// and user:pass@ by name but let everything else -- a query string
// chief among them -- straight through: redis://host:port?password=SECRET
// parsed as host:port="host:port?password=SECRET" and that whole string,
// secret included, was the "clean" address handed back to be dialled,
// printed on the beat's startup line and folded into the next error. Naming
// each bad shape also meant echoing enough of it to prove the name, and
// maskAddr -- built to make that echo safe -- only ever masked userinfo, so
// the same query secret came back out through a rejected rediss:// URL too.
//
// The repair drops per-shape messages for one grammar and one refusal: a
// value is either exactly host:port (optionally redis://-prefixed) or it is
// refused with a generic line naming only the scheme and the bare host --
// never the port, the userinfo, the query or the fragment, so there is
// nothing left in the message a secret could hide inside.
func Addr(addr string) (string, error) {
	a := strings.TrimSpace(addr)
	if a == "" {
		return "", fmt.Errorf("--store is empty")
	}

	scheme, rest := "", a
	if i := strings.Index(a, "://"); i >= 0 {
		scheme, rest = a[:i], a[i+len("://"):]
	}
	rest = strings.TrimSuffix(rest, "/") // a bare trailing "/" is an empty path, not a path

	host, bad := addrProblems(rest)
	if scheme != "" && scheme != "redis" {
		bad = append([]string{"scheme"}, bad...)
	}
	if len(bad) > 0 {
		schemeDesc := scheme
		if schemeDesc == "" {
			schemeDesc = "none"
		}
		return "", fmt.Errorf("store address refused: only host:port or redis://host:port is supported (got scheme=%s host=%s with %s)",
			schemeDesc, host, strings.Join(bad, "/"))
	}
	if !strings.Contains(rest, ":") {
		return "", fmt.Errorf("--store %s names no port; the fleet store is host:6380", rest)
	}
	return rest, nil
}

// addrProblems reports the bare host a value names -- never more than the
// host, so a caller can name what is wrong without printing what is wrong --
// and which of userinfo, path, query and fragment are present in it. It never
// returns the port, the userinfo, the query or the fragment themselves: only
// their names, for a message, and the host, which nova-tools treats as public
// (it is the thing everyone already reads off the sprint table).
func addrProblems(rest string) (host string, bad []string) {
	s := rest
	if i := strings.LastIndex(s, "@"); i >= 0 {
		bad = append(bad, "userinfo")
		s = s[i+1:]
	}
	if strings.ContainsRune(s, '/') {
		bad = append(bad, "path")
	}
	if strings.ContainsRune(s, '?') {
		bad = append(bad, "query")
	}
	if strings.ContainsRune(s, '#') {
		bad = append(bad, "fragment")
	}
	cut := len(s)
	for _, sep := range []byte{'/', '?', '#'} {
		if i := strings.IndexByte(s, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	host = s[:cut]
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host, bad
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
