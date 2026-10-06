package doctor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisacl"
)

// redisProbeTimeout bounds one probe of a store: the dial, one
// `nova-redis acl check` and one `redis-server --version`.
const redisProbeTimeout = 10 * time.Second

// RedisVersionFloor is the oldest server major version the tools run on: the
// function library, ACL selectors and key permissions they use need Redis 7.
// It is the major alone, so the one-version class rule does not read it.
const RedisVersionFloor = 7

func init() {
	Default.Register(Check{Name: "redis", Dependency: "the Redis stores and their ACL users", Run: checkRedis})
}

// redisStore is one Redis store a machine names, and how the check reads it.
type redisStore struct {
	Name string // how the machine calls it: the sprint store, the bus store
	Addr string // host:port, or a unix socket path
}

// redisStores are the stores the environment names: the store NOVA_REDIS_ADDR
// points at (the sprint store, and the bus store of a local setup, which share
// one Redis) and the separate bus store NOVA_BUS_REDIS points at when it is
// set. A machine that names no address runs the local twin and needs no Redis.
func redisStores(env Env) []redisStore {
	var out []redisStore
	if a := strings.TrimSpace(env.Getenv("NOVA_REDIS_ADDR")); a != "" {
		out = append(out, redisStore{Name: "sprint", Addr: a})
	}
	if a := strings.TrimSpace(env.Getenv("NOVA_BUS_REDIS")); a != "" {
		out = append(out, redisStore{Name: "bus", Addr: a})
	}
	return out
}

// redisUsers are the ACL users internal/redisacl renders, in role order; the
// live ACL of every store must hold each of them with the rules the rendering
// gives it.
func redisUsers() []string {
	users := redisacl.Roles()
	out := make([]string, 0, len(users))
	for _, r := range users {
		out = append(out, r.User)
	}
	return out
}

// checkRedis holds each Redis store the environment names to what the tools
// need (docs/SETUP.md, dep-redis-stores-b.w4): reachable, a server at or above
// the version floor, and every ACL user internal/redisacl renders present with
// its expected command and key rules. The live comparison is the one
// `nova-redis acl check` makes, run under the seat's own login; the check reads
// and never writes, and no password is read or printed.
func checkRedis(ctx context.Context, env Env) Result {
	stores := redisStores(env)
	if len(stores) == 0 {
		return Result{Status: OK, Evidence: "no Redis store is named (NOVA_REDIS_ADDR, NOVA_BUS_REDIS): a local twin needs none"}
	}
	var problems, ok []string
	fix := ""
	for _, s := range stores {
		if err := dialStore(ctx, env, s.Addr); err != nil {
			problems = append(problems, fmt.Sprintf("%s store %s is not reachable (%s)", s.Name, s.Addr, oneLine(err.Error())))
			if fix == "" {
				fix = "nova-up --local (or the fleet play) starts the store (docs/SETUP.md, dep-redis-stores-b.w4)"
			}
			continue
		}
		missing, drift, err := aclCheckStore(ctx, env, s.Addr)
		if err != nil && len(missing)+len(drift) == 0 {
			problems = append(problems, fmt.Sprintf("%s store %s did not answer nova-redis acl check (%s)", s.Name, s.Addr, oneLine(err.Error())))
			if fix == "" {
				fix = "run nova-doctor under the seat's login (nova-secrets exec) so nova-redis acl check reads the ACL (docs/SETUP.md, dep-redis-stores-b.w4)"
			}
			continue
		}
		if len(missing)+len(drift) > 0 {
			var who []string
			for _, u := range missing {
				who = append(who, u+" (missing)")
			}
			for _, u := range drift {
				who = append(who, u+" (different)")
			}
			problems = append(problems, fmt.Sprintf("%s store %s: %s", s.Name, s.Addr, strings.Join(who, ", ")))
			if fix == "" {
				fix = "nova-redis acl apply --addr " + s.Addr
				if len(missing) > 0 {
					fix += " --password-env-for " + missing[0] + "=<VARIABLE>"
				}
				fix += " (the variable set under nova-secrets exec --only <VARIABLE>)"
			}
			continue
		}
		ok = append(ok, s.Name+" store "+s.Addr)
	}
	if len(problems) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(problems, "; "), Fix: fix}
	}
	where := strings.Join(ok, "; ") + " users " + strings.Join(redisUsers(), ", ")
	major, known := serverMajor(ctx, env)
	switch {
	case !known:
		return Result{Status: Warn, Evidence: where + "; the redis-server binary did not answer --version",
			Fix: "install redis-server, which nova-up --local does, so the server's version is known (docs/SETUP.md, dep-redis-stores-b.w4)"}
	case major < RedisVersionFloor:
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%s; the redis-server binary is major version %d, below the floor %d the tools need", where, major, RedisVersionFloor),
			Fix:      "install Redis 7 or later, which nova-up --local does (docs/SETUP.md, dep-redis-stores-b.w4)"}
	}
	return Result{Status: OK, Evidence: where}
}

// dialStore dials a store's address once; a store that does not answer is a
// failure the check reports before it reads the ACL.
func dialStore(ctx context.Context, env Env, addr string) error {
	cctx, cancel := context.WithTimeout(ctx, redisProbeTimeout)
	defer cancel()
	return env.Dial(cctx, "tcp", addr)
}

// aclCheckStore runs `nova-redis acl check` for one store, which compares the
// live ACL with what internal/redisacl renders, and reads the users it names
// missing or different. The command exits 1 on a difference, so its error is
// not a failure when it printed an answer.
func aclCheckStore(ctx context.Context, env Env, addr string) (missing, drift []string, err error) {
	cctx, cancel := context.WithTimeout(ctx, redisProbeTimeout)
	defer cancel()
	out, err := env.Exec(cctx, "nova-redis", "acl", "check", "--addr", addr)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "ACL" {
			continue
		}
		switch f[1] {
		case "MISSING":
			if u := redisField(f, "user"); u != "" {
				missing = append(missing, u)
			}
		case "DRIFT":
			if u := redisField(f, "user"); u != "" {
				drift = append(drift, u)
			}
		}
	}
	return missing, drift, err
}

// redisField is the value of key=<v> in a typed line, or "".
func redisField(fields []string, key string) string {
	for _, w := range fields {
		if v, ok := strings.CutPrefix(w, key+"="); ok {
			return v
		}
	}
	return ""
}

// serverMajor is the major version `redis-server --version` reports, or false
// when the binary is not on PATH or answers no version.
func serverMajor(ctx context.Context, env Env) (int, bool) {
	cctx, cancel := context.WithTimeout(ctx, redisProbeTimeout)
	defer cancel()
	out, err := env.Exec(cctx, "redis-server", "--version")
	if err != nil {
		return 0, false
	}
	return redisMajor(out)
}

// redisMajor reads the major number of a `redis-server --version` line's
// `v=X.Y.Z` token.
func redisMajor(out string) (int, bool) {
	for _, w := range strings.Fields(out) {
		v, ok := strings.CutPrefix(w, "v=")
		if !ok {
			continue
		}
		major, _, _ := strings.Cut(v, ".")
		n, err := strconv.Atoi(major)
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}
