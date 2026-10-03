// Command nova-redis is the Layer 2 binary of docs/SPEC-REDIS.md, the owner of
// the local instance: `serve` launches it bound to loopback and the tailnet,
// with auth from nova-secrets and the fleet store's rules: AOF on, no eviction,
// no TTL policy, the store in --dir (serve.go). It also
// carries the scratch verbs: `spill` writes a value under an owner
// prefix with a required TTL, and `recall` reads it back and refuses a missing
// or expired key. A write with no owner or no TTL is refused before the
// instance is dialled, so an unbounded key never reaches Redis (the spill and
// recall section of the spec). The fn verbs
// load and check the store's function library (fn.go, over internal/redisfn).
//
// Scratch is scratch: nothing spilled is a record, and recall is allowed to
// miss. The password comes from the environment (NOVA_REDIS_PASSWORD, or the
// variable --password-env names, which a bench fills from nova-secrets at run
// time), never from an argument.
//
// Every verb that dials a store (spill, recall, fn load, fn check) opens it
// through internal/redisconn, the one way a nova tool opens its Redis
// connection (connect): one dial, the handshake and the login bounded by
// redisconn.OpenTimeout, no retry, and go-redis's own log kept off stderr. A
// store that cannot be reached or a login it refuses exits 2.
//
// The dispatch, the banner, the help, the version verb, the refusals and the
// output envelope are internal/tool's. spill and recall return a tool.Out;
// serve, fn and acl print their own lines (a stream, or a line the skeleton
// cannot render) behind a Prints verb.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/redis/go-redis/v9"
)

var version string

// PasswordEnv names the variable the password is read from when neither
// --password-env nor PasswordEnvEnv names another; never an argument.
const PasswordEnv = "NOVA_REDIS_PASSWORD"

// PasswordEnvEnv names the variable that names the password's variable when
// --password-env is not given, so a seat whose secret has its own name
// (nova-secrets exec --only <NAME>) needs no copy of it.
const PasswordEnvEnv = "NOVA_REDIS_PASSWORD_ENV"

// UserEnv names the ACL user a verb logs in as when --user is not given.
// With neither, the verb logs in as the store's default user.
const UserEnv = "NOVA_REDIS_USER"

// The hash fields a spilled key carries: the value, and the moment it lapses
// by the spiller's clock, so recall can refuse an expired key even before the
// instance has aged it out.
const (
	fieldValue   = "v"
	fieldExpires = "expires_ms"
)

// deps are the seams the verbs reach the world through: the clock and the
// environment. main() passes the real ones; tests pass a controlled clock and
// an environment of their own. The store is not a seam: spill and recall
// open it through redisconn (connect) at the --addr they were given.
type deps struct {
	now    func() time.Time
	getenv func(string) string

	// The serve seams: the environment handed to the child, where the
	// instance program is found, and the launch itself.
	environ  func() []string
	lookPath func(string) (string, error)
	launch   func(ctx context.Context, spec launchSpec, stdout, stderr io.Writer) error

	// The fn and acl seams: the store opener, so the unit tests hand a fake
	// that answers FUNCTION or ACL, which miniredis does not.
	fnOpen  func(ctx context.Context, store login) (redis.UniversalClient, func() error, error)
	aclOpen func(ctx context.Context, store login) (aclServer, func() error, error)
}

func realDeps() deps {
	return deps{
		now:      time.Now,
		getenv:   os.Getenv,
		environ:  os.Environ,
		lookPath: exec.LookPath,
		launch:   launchRedis,
	}
}

func main() { os.Exit(redisTool(realDeps()).Main()) }

// usage is the banner, for the tests that read it directly.
var usage = redisTool(deps{}).Banner()

// run is the test seam: the tool built over d, run in process.
func run(args []string, stdout, stderr io.Writer, d deps) int {
	return redisTool(d).Run(args, os.Stdin, stdout, stderr)
}

// redisTool is nova-redis on internal/tool. The verbs' bodies live in their
// own files; here is the one Tool and the shared login.
func redisTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-redis",
		What:  "run a local Redis store, and keep short-lived named values in it",
		Stamp: version,
		How: `serve runs redis-server on loopback or tailnet addresses only, with its data in --dir.
spill writes a value under <owner>:<name> with a required expiry; recall reads it back.
fn load and fn check install and verify the functions nova-table and nova-sprint call.
Passwords come from an environment variable, never from an argument.
first run: the --dry-run line needs no store; spill and recall need a Redis at 127.0.0.1:6379.`,
		ExitTable: "0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).",
		Words:     []string{"UNCONFIRMED", "MISSING", "EXPIRED", "UNBOUNDED"},
		Verbs: []tool.Verb{
			serveVerb(d),
			spillVerb(d),
			recallVerb(d),
			fnLoadVerb(d),
			fnCheckVerb(d),
			aclRenderVerb(d),
			aclCheckVerb(d),
			aclApplyVerb(d),
		},
	}
}

// spillVerb is the spill verb: a store write of one key with its TTL.
func spillVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "spill",
		Usage:   "spill --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]",
		Example: "spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi\nspill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi",
		Effect:  tool.LocalWrite,
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			loginFlags(f)
			f.String("owner", "", "the key's owner prefix, the part before the colon: no ':' or whitespace (a tool's or a worker's name)")
			f.String("name", "", "the key's name, the part after <owner>: (no whitespace)")
			f.String("ttl", "", "how long the value lives, a Go duration above zero (10m, 1h30m); a key never lives forever")
			f.String("value", "", "the text stored under <owner>:<name>; may be empty but must be given")
			f.Check(func(c *tool.Call) {
				if !c.Given("value") {
					c.Problem("--value is required: the text stored under <owner>:<name>; may be empty but must be given; refusing to guess")
				}
				ttl, err := time.ParseDuration(cmp.Or(c.Str("ttl"), "0s"))
				if err != nil {
					c.Problem(fmt.Sprintf("--ttl %q is not a duration (try 10m)", c.Str("ttl")))
				}
				for _, e := range keyErrors(c.Str("owner"), c.Str("name"), ttl) {
					if !(err != nil && e == errNoTTL) {
						c.Problem(e.Error())
					}
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out { return spillRun(c, d) },
	}
}

// recallVerb is the recall verb: an inspection that reads one key.
func recallVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "recall",
		Usage:   "recall --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>",
		Example: "recall --addr 127.0.0.1:6379 --owner ada --name note",
		Effect:  tool.Inspection,
		Flags: func(f *tool.Flags) {
			loginFlags(f)
			f.String("owner", "", "the key's owner prefix, as spill was given it: the part before the colon")
			f.String("name", "", "the key's name, as spill was given it: the part after <owner>:")
			f.Check(func(c *tool.Call) {
				for _, e := range keyErrors(c.Str("owner"), c.Str("name"), time.Hour) {
					c.Problem(e.Error())
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out { return recallRun(c, d) },
	}
}

// spillRun is spill's body: a store write of one key, <owner>:<name>, with its
// TTL. --dry-run checks the line and the login and prints the write, dialling
// nothing.
func spillRun(c *tool.Call, d deps) *tool.Out {
	store := loginFrom(c)
	ttl, _ := time.ParseDuration(cmp.Or(c.Str("ttl"), "0s"))
	if err := store.check(d); err != nil {
		return tool.Refuse(err.Error())
	}
	expires := d.now().Add(ttl).UTC().Format(time.RFC3339)
	if c.DryRun() {
		return tool.Done().Fact("key", c.Str("owner")+":"+c.Str("name")).Fact("ttl", ttl.String()).
			Fact("expires", expires).Fact("bytes", len(c.Str("value"))).Fact("store", c.Str("addr")).Fact("written", 0)
	}
	ctx := context.Background()
	conn, err := connect(ctx, store, d)
	if err != nil {
		return failed("SPILL", c.Str("owner")+":"+c.Str("name"), err, store, d)
	}
	// ignored: a deferred close after the verb's answer is printed; the answer is the report
	defer func() { _ = conn.Close() }()
	s := &scratch{rdb: conn.Client(), now: d.now}
	key, err := s.spill(ctx, c.Str("owner"), c.Str("name"), c.Str("value"), ttl)
	if err != nil {
		err = conn.Explain(err)
		if redisconn.Classify(err) == redisconn.Unreachable {
			// Open succeeded, so the transaction was handed to a store that was
			// up: a connection that dropped or a reply that never came after
			// that is not "could not run". The EXEC may have committed.
			return unconfirmed(conn, store, c.Str("owner"), c.Str("name"), err)
		}
		return failed("SPILL", c.Str("owner")+":"+c.Str("name"), err, store, d)
	}
	return tool.Done().Fact("key", key).Fact("ttl", ttl.String()).Fact("expires", expires).Fact("bytes", len(c.Str("value")))
}

// recallRun is recall's body: reads one key and writes nothing.
func recallRun(c *tool.Call, d deps) *tool.Out {
	store := loginFrom(c)
	if err := store.check(d); err != nil {
		return tool.Refuse(err.Error())
	}
	key := c.Str("owner") + ":" + c.Str("name")
	ctx := context.Background()
	conn, err := connect(ctx, store, d)
	if err != nil {
		return failed("RECALL", key, err, store, d)
	}
	// ignored: a deferred close after the verb's answer is printed; the answer is the report
	defer func() { _ = conn.Close() }()
	s := &scratch{rdb: conn.Client(), now: d.now}
	v, err := s.recall(ctx, c.Str("owner"), c.Str("name"))
	switch {
	case errors.Is(err, errMissing):
		return tool.Fail().As("MISSING").Fact("key", key)
	case errors.Is(err, errExpired):
		return tool.Fail().As("EXPIRED").Fact("key", key)
	case errors.Is(err, errUnbounded):
		return tool.Fail().As("UNBOUNDED").Fact("key", key).
			Fact("remedy", tool.Text("an unbounded key is a bug; it was not written by nova-redis spill"))
	case err != nil:
		// A read has no side effect, so a reply that never came leaves the
		// store as it was: unreachable exits 2 here, honestly, where spill's
		// lost reply exits 1 (unconfirmed).
		return failed("RECALL", key, conn.Explain(err), store, d)
	}
	// The line's value is one token (oneline.Field); --json carries it exactly.
	return tool.Done().Fact("key", key).Fact("bytes", len(v)).Fact("value", v)
}

var (
	errNoOwner   = errors.New("--owner is required and may not be empty or hold ':' or whitespace; every key carries an owner prefix")
	errNoName    = errors.New("--name is required and may not be empty or hold whitespace")
	errNoTTL     = errors.New("--ttl is required and must be above zero; an unbounded key is a bug")
	errMissing   = errors.New("missing")
	errExpired   = errors.New("expired")
	errUnbounded = errors.New("unbounded")
)

// login is the store a verb dials: --addr, --user with UserEnv as its
// default, and --password-env naming the variable that holds the password
// (PasswordEnvEnv, else PasswordEnv). The password is never an argument.
// Every verb that dials a store takes these flags and opens the store through
// connect. The environment is read by check, after every refusal a verb makes
// of its own flags, so a refused invocation reads no login.
type login struct {
	addr, user, passwordEnv *string
	givenFn                 func(string) bool
}

func loginFlags(f *tool.Flags) {
	f.String("addr", "", "the store's address as <host:port>, such as 127.0.0.1:6379 (no default)")
	f.String("user", "", "the ACL user to log in as (default $"+UserEnv+"; with neither, the store's default user)")
	f.String("password-env", "", "the NAME of the variable that holds the password, never the password itself (default: the variable $"+PasswordEnvEnv+" names, else "+PasswordEnv+")")
	f.Check(func(c *tool.Call) {
		if !c.Given("addr") {
			c.Problem("--addr is required: the store's address as <host:port>, such as 127.0.0.1:6379 (no default); refusing to guess")
		}
	})
}

// loginFrom reads the login flags from a call.
func loginFrom(c *tool.Call) login {
	addr := c.Str("addr")
	user := c.Str("user")
	passwordEnv := c.Str("password-env")
	return login{addr: &addr, user: &user, passwordEnv: &passwordEnv, givenFn: c.Given}
}

// loginFromFlags reads the login flags from a flag set, for the unit tests
// that hold the login logic directly.
func loginFromFlags(fs *flag.FlagSet) login {
	return login{
		addr:        fs.String("addr", "", "the store's address as <host:port>, such as 127.0.0.1:6379 (no default)"),
		user:        fs.String("user", "", "the ACL user to log in as (default $"+UserEnv+"; with neither, the store's default user)"),
		passwordEnv: fs.String("password-env", "", "the NAME of the variable that holds the password, never the password itself (default: the variable $"+PasswordEnvEnv+" names, else "+PasswordEnv+")"),
		givenFn: func(name string) bool {
			on := false
			fs.Visit(func(f *flag.Flag) { on = on || f.Name == name })
			return on
		},
	}
}

// given reports whether the flag was on the line, even empty.
func (l login) given(flagName string) bool { return l.givenFn(flagName) }

// from names where a flag's value came from: the flag, or the variable that
// is its default.
func (l login) from(flagName, env string) string {
	if l.given(flagName) {
		return "--" + flagName
	}
	return env
}

// envName is the shape of a variable's name as redisconn takes it: capital
// letters, digits and underscores, not starting with a digit.
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// check fills in the login flags that were not given from the environment
// (--user from UserEnv; --password-env from PasswordEnvEnv, else
// PasswordEnv) and refuses, before anything is dialled, a login the verb
// could not make. Each refusal names where the bad value came from. It is
// the first read of the environment a verb makes.
func (l login) check(d deps) error {
	if err := validAddr(*l.addr); err != nil {
		return err
	}
	if !l.given("user") {
		*l.user = d.getenv(UserEnv)
	}
	if !l.given("password-env") {
		*l.passwordEnv = d.getenv(PasswordEnvEnv)
		if *l.passwordEnv == "" {
			*l.passwordEnv = PasswordEnv
		}
	}
	if !envName.MatchString(*l.passwordEnv) {
		return fmt.Errorf("%s %q is not a variable name; name the variable that holds the password (default %s)", l.from("password-env", PasswordEnvEnv), *l.passwordEnv, PasswordEnv)
	}
	switch {
	case *l.user == "":
		return nil
	case strings.ContainsAny(*l.user, " \t\r\n"):
		return fmt.Errorf("%s %q holds whitespace; give the ACL user's name", l.from("user", UserEnv), *l.user)
	case d.getenv(*l.passwordEnv) == "":
		return fmt.Errorf("user %s (from %s) but %s is empty; run under nova-secrets exec --only %s, refusing to log in without a password", *l.user, l.from("user", UserEnv), *l.passwordEnv, *l.passwordEnv)
	}
	return nil
}

// options is the login check accepted in redisconn's terms. The password's
// variable is named only when it holds something: the default user with no
// password is a store that asks for none (redisconn refuses a password
// variable that is named and empty, and check has refused a named user
// without one). Env is left zero, so redisconn reads no variable of its own.
func (l login) options(d deps) redisconn.Options {
	o := redisconn.Options{Addr: *l.addr, User: *l.user}
	if d.getenv(*l.passwordEnv) != "" {
		o.PasswordEnv = *l.passwordEnv
	}
	return o
}

// flags is the login as a remedy's command line writes it, so that command
// logs in as the verb did: --addr always; --user and --password-env when they
// were given on the line, even empty or equal to the default (an explicit
// flag overrides the environment, and a remedy that dropped it would log in
// as the environment says), and when the environment set them to other than
// the default. Every value is one POSIX shell word (shellWord). It is called
// after check.
func (l login) flags() string {
	line := "--addr " + shellWord(*l.addr)
	if *l.user != "" || l.given("user") {
		line += " --user " + shellWord(*l.user)
	}
	if *l.passwordEnv != PasswordEnv || l.given("password-env") {
		line += " --password-env " + shellWord(*l.passwordEnv)
	}
	return line
}

// validAddr refuses an address the tool would have to guess at. The Redis
// client fills an empty address in as localhost:6379 and an empty host as the
// local machine, so an address that is empty, blank, or lacks a host or a
// numeric port is refused before anything is dialled.
func validAddr(addr string) error {
	if strings.TrimSpace(addr) == "" {
		return errors.New("--addr is empty; give the instance as <host:port>, refusing to guess localhost")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q is not <host:port>; refusing to guess", addr)
	}
	if strings.TrimSpace(host) == "" || strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("--addr %q names no host; refusing to guess localhost", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("--addr %q needs a port from 1 to 65535; refusing to guess", addr)
	}
	return nil
}

// connect opens the store for the login check accepted through
// redisconn.Open, the one way a nova tool opens its Redis connection, with
// the environment d.getenv reads and nothing else.
func connect(ctx context.Context, store login, d deps) (*redisconn.Conn, error) {
	return redisconn.Open(ctx, store.options(d), d.getenv)
}

// failed is a verb's one FAILED line for err, which is redisconn's (an
// Open failure, or a command's error through Conn.Explain), so it names the
// store, the login, what came back and the next step. A store that could not
// be reached and a login it refused exit 2: the fix is the caller's. The
// store may still have taken the write: redisconn classes a connection that
// dropped, or a reply that never came, as unreachable too, and by then a
// spill's EXEC may have landed. Anything else the store answered exits 1.
//
// A refused login with the password's variable unset gets one more field,
// the variable this verb read: redisconn's next step names no variable when
// the caller named none, and the operator needs to know which one to set.
func failed(verb, key string, err error, store login, d deps) *tool.Out {
	class := redisconn.Classify(err)
	o := tool.Fail().Fact("key", key).Fact("class", fmt.Sprint(class)).Fact("err", oneline.Err(err))
	if class == redisconn.AuthRefused && d.getenv(*store.passwordEnv) == "" {
		o.Fact("remedy", tool.Text("nova-redis reads the store's password from "+*store.passwordEnv+", which is not set: export it, holding the password of the default user"))
	}
	if class == redisconn.Unreachable || class == redisconn.AuthRefused {
		o.Exit = 2
	}
	return o
}

// unconfirmed is spill's one line for a transaction whose confirmation was
// lost: the store was opened and the transaction handed to it, then the
// connection dropped or the reply did not come. The write may have committed,
// so the exit is 1 (it ran, the outcome is unknown) and the remedy is a
// read-back with the same login, owner and name, never a blind re-spill.
// redisconn's own next step ("start the store or correct the address") is
// left out: the store was up.
//
// err is redisconn's (Conn.Explain); what it unwraps to is the cause, with the
// password already taken out of any text that held it, and its words are
// Conn.String's and this function's, so neither "unreachable" nor redisconn's
// next step appears.
func unconfirmed(conn *redisconn.Conn, store login, owner, name string, err error) *tool.Out {
	cause := err
	if inner := errors.Unwrap(err); inner != nil {
		cause = inner
	}
	recall := "nova-redis recall " + store.flags() + " --owner " + shellWord(owner) + " --name " + shellWord(name)
	return tool.Fail().As("UNCONFIRMED").Fact("key", owner+":"+name).
		Fact("err", oneline.Escape(conn.String()+": the transaction was sent and its reply was lost: "+cause.Error())).
		Fact("remedy", tool.Text("confirmation was lost after the transaction was sent, so the write may have committed; read it back with the same login before spilling again: "+recall))
}

// shellWord is s as one POSIX shell word: as it is when it holds only
// characters no shell treats specially, and otherwise in single quotes, each
// single quote in it closing the quotes, written as a backslash and a quote,
// and opening them again. The empty string is two single quotes.
func shellWord(s string) string {
	plain := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.,:/@%+=", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// validKey is the one gate every write passes: an owner, a name and a TTL
// above zero, or nothing is written. It names every one that is wrong.
func validKey(owner, name string, ttl time.Duration) error {
	return errors.Join(keyErrors(owner, name, ttl)...)
}

func keyErrors(owner, name string, ttl time.Duration) []error {
	var errs []error
	if owner == "" || strings.ContainsAny(owner, ": \t\r\n") {
		errs = append(errs, errNoOwner)
	}
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		errs = append(errs, errNoName)
	}
	if ttl <= 0 {
		errs = append(errs, errNoTTL)
	}
	return errs
}

// scratch is spill/recall over one instance with an injected clock.
type scratch struct {
	rdb redis.Cmdable
	now func() time.Time
}

// spill writes value under <owner>:<name> with ttl, refusing a missing owner
// or TTL. The value, its expiry and the TTL are written in one transaction, so
// no reader ever sees the key without its TTL.
func (s *scratch) spill(ctx context.Context, owner, name, value string, ttl time.Duration) (string, error) {
	if err := validKey(owner, name, ttl); err != nil {
		return "", err
	}
	key := owner + ":" + name
	expires := s.now().Add(ttl).UnixMilli()
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, key)
		p.HSet(ctx, key, fieldValue, value, fieldExpires, strconv.FormatInt(expires, 10))
		p.PExpire(ctx, key, ttl)
		return nil
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// recall reads <owner>:<name> back. It refuses a missing key, a key whose
// expiry has passed by the injected clock (even if the instance has not aged
// it out yet), and a key that carries no TTL.
func (s *scratch) recall(ctx context.Context, owner, name string) (string, error) {
	key := owner + ":" + name
	var fields *redis.MapStringStringCmd
	var pttl *redis.DurationCmd
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		fields = p.HGetAll(ctx, key)
		pttl = p.PTTL(ctx, key)
		return nil
	})
	if err != nil {
		return "", err
	}
	m := fields.Val()
	if len(m) == 0 {
		return "", errMissing
	}
	expText, hasExp := m[fieldExpires]
	if !hasExp || pttl.Val() < 0 {
		return "", errUnbounded
	}
	expMS, err := strconv.ParseInt(expText, 10, 64)
	if err != nil {
		return "", errUnbounded
	}
	if !s.now().Before(time.UnixMilli(expMS)) {
		return "", errExpired
	}
	return m[fieldValue], nil
}
