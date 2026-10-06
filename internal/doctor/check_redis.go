package doctor

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The redis check (docs/SPEC-DOCTOR.md, "The checks"; docs/SETUP.md, this
// card's section): the Redis stores the inventory names, each with the ACL
// users internal/redisacl renders per role. The check reads this build's
// rendering with `nova-redis acl render` (which opens no store) and each
// store's live ACL with `nova-redis acl check` (which only reads: ACL GETUSER,
// ACL USERS and ACL CAT, never SETUSER), then fails naming a user that is
// missing or different, with the `nova-redis acl apply` line that fixes it.
// A password is never read or printed: only the name of the variable holding
// it travels, in `--password-env`.
func init() {
	Default.Register(Check{Name: "redis", Dependency: "the Redis stores and their ACL users", Run: checkRedis})
}

// redisTimeout bounds one store's reachability dial and its live-ACL read.
const redisTimeout = 30 * time.Second

// redisMinVersion is the oldest Redis the renderer works on: ACL selectors,
// clearselectors and resetchannels are Redis 7's. Major and minor only, so the
// rule holding every place to one Redis version reads no version here.
const redisMinVersion = "7.0"

// redisStore is one Redis the inventory names: its label, the variable naming
// its address, and the variables naming its login user and the variable that
// holds its password.
type redisStore struct {
	name, addrEnv, userEnv, passwordEnvEnv string
}

// redisStores are the stores the check covers, in report order: the machine's
// Redis (the coordinator's seat names it), the sprint store and the bus store.
// A store whose address is unset, or is the sprint's in-process twin (mem:),
// holds no ACL and is skipped.
func redisStores() []redisStore {
	return []redisStore{
		{"the machine's Redis", "NOVA_REDIS_ADDR", "NOVA_REDIS_USER", "NOVA_REDIS_PASSWORD_ENV"},
		{"the sprint store", "NOVA_SPRINT_REDIS", "NOVA_SPRINT_REDIS_USER", "NOVA_SPRINT_REDIS_PASSWORD_ENV"},
		{"the bus store", "NOVA_BUS_REDIS", "NOVA_BUS_REDIS_USER", "NOVA_BUS_REDIS_PASSWORD_ENV"},
	}
}

// checkRedis is the check's Run.
func checkRedis(ctx context.Context, env Env) Result {
	var addrs []string
	var stores []redisStore
	for _, s := range redisStores() {
		addr := env.Getenv(s.addrEnv)
		if addr == "" || strings.HasPrefix(addr, "mem:") || slices.Contains(addrs, addr) {
			continue
		}
		addrs = append(addrs, addr)
		stores = append(stores, s)
	}
	if len(stores) == 0 {
		return Result{Status: Warn,
			Evidence: "no Redis store is named (NOVA_REDIS_ADDR, NOVA_SPRINT_REDIS and NOVA_BUS_REDIS are unset or the sprint's mem: twin)",
			Fix:      "nova-up --local for one machine, or the fleet play's redis.yml for a fleet"}
	}
	expected, res := redisRendering(ctx, env)
	if res != nil {
		return *res
	}
	if res := redisFloor(ctx, env); res != nil {
		return *res
	}
	var problems []string
	var remedy string
	for _, s := range stores {
		addr := env.Getenv(s.addrEnv)
		if err := redisReachable(ctx, env, addr); err != nil {
			problems = append(problems, fmt.Sprintf("%s at %s is unreachable: %v", s.name, addr, err))
			continue
		}
		out, err := redisCheck(ctx, env, s, addr)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s at %s did not answer `nova-redis acl check`: %v", s.name, addr, err))
			continue
		}
		if remedy == "" {
			remedy = redisRemedy(out)
		}
		problems = append(problems, redisDrift(expected, s.name, addr, out)...)
	}
	if len(problems) > 0 {
		if remedy == "" {
			remedy = "nova-redis acl apply --addr " + env.Getenv(stores[0].addrEnv) + " --help"
		}
		return Result{Status: Fail, Evidence: strings.Join(problems, "; "), Fix: remedy}
	}
	return Result{Status: OK,
		Evidence: fmt.Sprintf("%d Redis store(s) reachable, every ACL user matches this build (%s)",
			len(stores), strings.Join(expected, ", "))}
}

// redisRendering runs `nova-redis acl render`, which opens no store, and reads
// the users this build renders. A render that fails is the build's own defect,
// reported before any store is read.
func redisRendering(ctx context.Context, env Env) ([]string, *Result) {
	cctx, cancel := context.WithTimeout(ctx, redisTimeout)
	out, err := env.Exec(cctx, "nova-redis", "acl", "render")
	cancel()
	if err != nil {
		return nil, &Result{Status: Fail,
			Evidence: "this build's ACL users do not render: " + err.Error(),
			Fix:      "fix internal/redisacl and rebuild: nova-redis acl render"}
	}
	var users []string
	for _, l := range strings.Split(out, "\n") {
		w := strings.Fields(l)
		if len(w) >= 3 && w[0] == "ACL" && w[1] == "SETUSER" && !slices.Contains(users, w[2]) {
			users = append(users, w[2])
		}
	}
	if len(users) == 0 {
		return nil, &Result{Status: Fail,
			Evidence: "`nova-redis acl render` named no user",
			Fix:      "fix internal/redisacl and rebuild: nova-redis acl render"}
	}
	return users, nil
}

// redisFloor holds the redis-server the machine runs to the floor the ACL
// users need.
func redisFloor(ctx context.Context, env Env) *Result {
	cctx, cancel := context.WithTimeout(ctx, redisTimeout)
	out, err := env.Exec(cctx, "redis-server", "--version")
	cancel()
	if err != nil {
		return &Result{Status: Fail,
			Evidence: "redis-server is not on PATH or does not answer --version: " + err.Error(),
			Fix:      "brew install redis (darwin), or sudo apt-get install -y redis-server (linux)"}
	}
	got := redisVersionOf(out)
	if got == "" {
		return &Result{Status: Fail,
			Evidence: "redis-server printed no version: " + oneLine(out),
			Fix:      "reinstall redis-server: brew install redis (darwin), or sudo apt-get install -y redis-server (linux)"}
	}
	if redisOlderThan(got, redisMinVersion) {
		return &Result{Status: Fail,
			Evidence: fmt.Sprintf("redis-server %s is older than the %s the ACL users need", got, redisMinVersion),
			Fix:      "brew upgrade redis (darwin), or sudo apt-get install -y --only-upgrade redis-server (linux)"}
	}
	return nil
}

// redisReachable dials the store, so an address that is wrong or silent is
// named before the ACL is read.
func redisReachable(ctx context.Context, env Env, addr string) error {
	cctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	return env.Dial(cctx, "tcp", addr)
}

// redisCheck runs `nova-redis acl check` for one store, logging in as the
// store's named user with the password from the variable its seat names. The
// password's value is never read.
func redisCheck(ctx context.Context, env Env, s redisStore, addr string) (string, error) {
	args := []string{"acl", "check", "--addr", addr}
	if user := env.Getenv(s.userEnv); user != "" {
		args = append(args, "--user", user)
	}
	if passEnv := env.Getenv(s.passwordEnvEnv); passEnv != "" {
		args = append(args, "--password-env", passEnv)
	}
	cctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	return env.Exec(cctx, "nova-redis", args...)
}

// redisDrift reads the check's lines and returns one problem for each rendered
// user the store does not have right, and one for a rendered user the check
// did not report at all.
func redisDrift(expected []string, name, addr, out string) []string {
	status := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		w := strings.Fields(l)
		if len(w) < 3 || w[0] != "ACL" {
			continue
		}
		switch w[1] {
		case "OK", "MISSING", "DRIFT":
			if u := redisUserField(l); u != "" {
				status[u] = w[1]
			}
		}
	}
	var problems []string
	for _, u := range expected {
		switch status[u] {
		case "OK":
		case "MISSING":
			problems = append(problems, fmt.Sprintf("user %s is missing from %s at %s", u, name, addr))
		case "DRIFT":
			problems = append(problems, fmt.Sprintf("user %s differs on %s at %s", u, name, addr))
		default:
			problems = append(problems, fmt.Sprintf("%s at %s did not report user %s", name, addr, u))
		}
	}
	return problems
}

// redisUserField is the user=<name> field of one acl line, or "".
func redisUserField(line string) string {
	for _, w := range strings.Fields(line) {
		if u, ok := strings.CutPrefix(w, "user="); ok {
			return u
		}
	}
	return ""
}

// redisRemedy is the `remedy="..."` the check prints on a drift, the exact
// line that fixes the store, or "".
func redisRemedy(out string) string {
	const open = `remedy="`
	i := strings.Index(out, open)
	if i < 0 {
		return ""
	}
	rest := out[i+len(open):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// redisVersionOf is the v=<major.minor.patch> token of `redis-server
// --version`, or "".
func redisVersionOf(out string) string {
	for _, w := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(w, "v="); ok {
			return v
		}
	}
	return ""
}

// redisOlderThan reports whether got is a lower major.minor than min. A
// version either side cannot be read is not older, so an unreadable version is
// never a false floor failure.
func redisOlderThan(got, min string) bool {
	gMaj, gMin, ok1 := redisMajorMinor(got)
	mMaj, mMin, ok2 := redisMajorMinor(min)
	if !ok1 || !ok2 {
		return false
	}
	return gMaj < mMaj || gMaj == mMaj && gMin < mMin
}

func redisMajorMinor(v string) (int, int, bool) {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	return maj, min, err1 == nil && err2 == nil
}
