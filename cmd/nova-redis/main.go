// Command nova-redis is the Layer 2 binary of docs/SPEC-REDIS.md, the owner of
// the local instance: `serve` launches it bound to loopback and the tailnet,
// with auth from nova-secrets and the fleet store's rules: AOF on, no eviction,
// no TTL policy, the store in --dir (serve.go, #2281, #3879). It also
// carries the scratch verbs: `spill` writes a value under an owner
// prefix with a required TTL, and `recall` reads it back and refuses a missing
// or expired key. A write with no owner or no TTL is refused before the
// instance is dialled, so an unbounded key never reaches Redis (rules 2 and
// the spill/recall paragraph of the spec; nova-tools #2279). The fn verbs
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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
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

const usage = `nova-redis: run a local Redis store, and keep short-lived named values in it

how it works: serve runs redis-server on loopback or tailnet addresses only,
with its data in --dir. spill writes a value under <owner>:<name> with a
required expiry, and recall reads it back (exit 1 once it has expired). fn load
and fn check install and verify the functions nova-table and nova-sprint call.
Passwords come from an environment variable, never from an argument.
first run: the --dry-run line needs no store; the spill and recall lines need a
Redis you may write to at 127.0.0.1:6379 (redis-server --port 6379 is enough).

usage:
  nova-redis serve  --bind <addr>[,<addr>...] --port <port> --dir <store-dir>
  nova-redis spill  --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
  nova-redis recall --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
  nova-redis fn load  --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis fn check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl render
  nova-redis acl check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl apply --addr <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
  nova-redis version
  nova-redis help [<verb>]

nova-redis help <verb> (or <verb> -h) prints a verb's flags and its effect.
Every verb but serve takes --json: the same result as one JSON object on
stdout (result {verb, status ok|failed|refused, exit, remedy, why}, items
(one per line, kind = the line's leading words), notes), refusals included.
A refused invocation prints one line per problem with the line, all at once:
nova-redis <verb> REFUSED: <what was wrong>; run: nova-redis help <verb>
(exit 2), and nothing is dialled or written.
The key is <owner>:<name>. Every verb that dials a store (spill, recall, fn
load, fn check, acl check, acl apply) takes it as --addr, or --redis (the name
every nova tool's store flag has), <host:port> or the absolute path of a Unix
socket, and refuses a missing or empty one, or one without a host and a port
(exit 2), before anything is dialled. spill refuses a
missing owner or a missing, zero or negative TTL (exit 2) and writes nothing;
an unbounded key is a bug. spill --dry-run makes every check, the login's too,
and prints the write it would make with written=0, dialling nothing.
recall exits 1 on a missing or expired key: scratch is allowed to miss.
The password is read from the variable --password-env names (default
NOVA_REDIS_PASSWORD_ENV, else NOVA_REDIS_PASSWORD), never from an argument.
--user names the ACL user to log in as (default NOVA_REDIS_USER; with
neither, the store's default user). A --password-env that is not a variable
name (capital letters, digits and underscores), a user name with whitespace,
and a user whose password variable is empty are refused (exit 2) before
anything is dialled. A store that cannot be reached, or a login it refuses,
is one FAIL line on stderr with the next step (exit 2). A spill whose reply
is lost after the store took it is SPILL UNCONFIRMED (exit 1): the write may
have committed, so read it back with recall before spilling again.
fn load puts the nova_sprint function library this binary embeds on the store
unless the store holds exactly its code (LOADED, UNCHANGED or REPLACED, with
its digest). fn check changes nothing: OK (exit 0), STALE or MISSING (exit 1)
with the store's digest and this binary's. A failure of either is one FAILED
line on stderr with the remedy for its cause: exit 1 when the store answered
with a refusal (NOPERM, a library it would not take), exit 2 when no answer
came or the login was refused. fn load is for the one place that deploys: it
replaces other code under the library's name.
acl render prints, with no store, one ACL SETUSER line per role of the fleet
store (coordinator, member, table, friend): its key families, its command
categories and FCALL of exactly the functions this binary's library registers
in the role's files, FCALL_RO of the no-writes ones. acl check (an
inspection) compares the store's live ACL with them: ACL OK, ACL DRIFT with
what apply would add and remove, or ACL MISSING per user, exit 1 on any.
acl apply (a store write) sets the users that differ and saves the ACL file
when the store keeps one; --dry-run writes nothing. acl apply never changes
the password of a user the store has; a user it creates gets the password in
the variable --password-env-for names, and without one apply refuses (exit 1).
Log in as a user that may run ACL.
serve runs redis-server in the foreground, bound only to loopback and tailnet
addresses (100.64.0.0/10, fd7a:115c:a1e0::/48); --bind has no default and a
wildcard, public or LAN address is refused (exit 2). The password reaches
redis-server on stdin, never in an argument. The store lives in --dir (absolute,
no default): AOF on, fsynced every second, no eviction, no TTL policy, so a
restart on the same --dir keeps every key. A bench runs it as
nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve
--bind 127.0.0.1,100.101.102.103 --port 6379 --dir /var/lib/nova-redis

exit codes: 0 done (spill written, recall found, fn load done, fn check finds the
library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired
or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a
refusal by the store, a serve that could not start); 2 could not run (a usage
error, a flag refused before dialling, a store that did not answer or a login it
refused).

example:
  nova-redis version
  nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note
`

// deps are the seams run() reaches the world through: the clock and the
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

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDeps())) }

// verbs is every verb as a reader types it, in the order the banner lists them.
const verbs = "serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, version, help"

// effects is what each verb's help says it does to the world (STANDARD
// §2-effects), printed above its flags; a verb group lists its subverbs.
var effects = map[string]string{
	"serve":      "effect: starts redis-server in the foreground with its store in --dir and runs until it is stopped; the password is read from " + PasswordEnv + ". Its output is redis-server's own, so serve takes no --json.",
	"spill":      "effect: a store write of one key, <owner>:<name>, with its TTL. --dry-run checks the line and the login and prints the write, dialling nothing.",
	"recall":     "effect: an inspection: reads one key and writes nothing.",
	"fn":         "subverbs: fn load (a store write: puts this binary's function library on the store) and fn check (an inspection: compares the store's with it). nova-redis help fn load prints its flags.",
	"fn load":    "effect: a store write: FUNCTION LOAD REPLACE of this binary's library, unless the store already holds exactly that code.",
	"fn check":   "effect: an inspection: FUNCTION LIST, compared with this binary's library; writes nothing.",
	"acl":        "subverbs: acl render (needs no store: prints this build's users), acl check (an inspection of the store's users) and acl apply (a store write; --dry-run writes nothing). nova-redis help acl apply prints its flags.",
	"acl render": "effect: prints this build's users from the binary alone; opens no store.",
	"acl check":  "effect: an inspection: ACL GETUSER, ACL USERS and ACL CAT; writes nothing.",
	"acl apply":  "effect: a store write: ACL SETUSER for each user that differs, then ACL SAVE; --dry-run prints ACL WOULD-SET lines and writes nothing.",
	"version":    "effect: prints this build's version line; reads nothing.",
}

func run(args []string, stdout, stderr io.Writer, d deps) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// with its effect, before anything is dialed, launched or written (the CLI
	// style's rule (b), #4505).
	defer verbflag.RecoverWith(stdout, "nova-redis", usage, &code, func(verb string) string {
		if e := effects[verb]; e != "" {
			return e + "\n"
		}
		return ""
	})
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; serve runs the instance, spill writes scratch, recall reads it, fn loads or checks the function library, acl renders, checks or applies the store's users")
	}
	switch args[0] {
	case "spill":
		return cmdSpill(args[1:], stdout, stderr, d)
	case "recall":
		return cmdRecall(args[1:], stdout, stderr, d)
	case "serve":
		return cmdServe(args[1:], stdout, stderr, d)
	case "fn":
		return cmdFn(args[1:], stdout, stderr, d)
	case "acl":
		return cmdACL(args[1:], stdout, stderr, d)
	case "version", "--version":
		fs := flag.NewFlagSet("version", flag.ContinueOnError)
		r := newReport("version", fs, args[1:], stdout, stderr)
		if problems, _ := parse(fs, args[1:]); len(problems) > 0 {
			return r.refuse(problems...)
		}
		if r.json {
			r.out.Payload = buildinfo.Line("nova-redis", version)
			return r.done(0)
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-redis", version))
		return 0
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return run(verbflag.HelpArgs(args[1:], usage, "nova-redis"), stdout, stderr, d)
		}
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown verb %q; the verbs are %s", args[0], verbs))
	}
}

// parse runs a verb's flag set and returns every problem with the line, not
// only the first, so one run teaches the whole invocation: a positional
// argument, and each required flag not GIVEN with what it wants (its usage).
// ok is false when the line did not parse at all (the flag package stops at
// the first flag it cannot take); that one problem names the flags the verb
// has.
func parse(fs *flag.FlagSet, args []string, required ...string) (problems []string, ok bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := verbflag.Parse(fs, args); err != nil {
		bad, unknown := strings.CutPrefix(err.Error(), "flag provided but not defined: -")
		if !unknown {
			return []string{err.Error()}, false
		}
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, "--"+f.Name) })
		return []string{fmt.Sprintf("unknown flag --%s; the flags of %s are %s", strings.TrimPrefix(bad, "-"), fs.Name(), strings.Join(names, ", "))}, false
	}
	if fs.NArg() > 0 {
		problems = append(problems, fmt.Sprintf("unexpected argument %q; every input is a flag", fs.Arg(0)))
	}
	both := 0
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "addr" || f.Name == "redis" {
			both++
		}
	})
	if both == 2 {
		problems = append(problems, "--addr and --redis name the same store; give one")
	}
	sort.Strings(required)
	for _, name := range required {
		if !given(fs, name) {
			problems = append(problems, fmt.Sprintf("--%s is required: %s; refusing to guess", name, fs.Lookup(name).Usage))
		}
	}
	return problems, true
}

// given reports whether the flag was on the line, even empty; --redis is
// --addr given (loginFlags).
func given(fs *flag.FlagSet, name string) bool {
	on := false
	fs.Visit(func(f *flag.Flag) { on = on || f.Name == name || (name == "addr" && f.Name == "redis") })
	return on
}

// keyProblems is every problem validKey finds, as text, but for the TTL's
// when --ttl was refused already as no duration.
func keyProblems(owner, name string, ttl time.Duration, ttlRefused bool) []string {
	var out []string
	for _, e := range keyErrors(owner, name, ttl) {
		if !(ttlRefused && e == errNoTTL) {
			out = append(out, e.Error())
		}
	}
	return out
}

func cmdSpill(args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("spill", flag.ContinueOnError)
	r := newReport("spill", fs, args, stdout, stderr)
	store := loginFlags(fs)
	owner := fs.String("owner", "", "the key's owner prefix, the part before the colon: no ':' or whitespace (a tool's or a worker's name)")
	name := fs.String("name", "", "the key's name, the part after <owner>: (no whitespace)")
	ttlText := fs.String("ttl", "", "how long the value lives, a Go duration above zero (10m, 1h30m); a key never lives forever")
	value := fs.String("value", "", "the text stored under <owner>:<name>; may be empty but must be given")
	dryRun := fs.Bool("dry-run", false, "check the line and the login, print the write spill would make, and dial nothing")
	problems, ok := parse(fs, args, "addr", "value")
	if !ok {
		return r.refuse(problems...)
	}
	if given(fs, "addr") {
		if err := validAddr(*store.addr); err != nil {
			problems = append(problems, err.Error())
		}
	}
	ttl, err := time.ParseDuration(cmp.Or(*ttlText, "0s"))
	if err != nil {
		problems = append(problems, fmt.Sprintf("--ttl %q is not a duration (try 10m)", *ttlText))
	}
	// Refused before the dial: a write with no owner or no TTL never reaches
	// the instance.
	problems = append(problems, keyProblems(*owner, *name, ttl, err != nil)...)
	if len(problems) > 0 {
		return r.refuse(problems...)
	}
	if err := store.check(d); err != nil {
		return r.refuse(err.Error())
	}
	expires := d.now().Add(ttl).UTC().Format(time.RFC3339)
	if *dryRun {
		r.line(false, "SPILL OK", "dry-run", true, "key", *owner+":"+*name, "ttl", ttl.String(), "expires", expires, "bytes", len(*value), "store", *store.addr, "written", 0)
		return r.done(0)
	}
	ctx := context.Background()
	conn, err := connect(ctx, store, d)
	if err != nil {
		return failed(r, "SPILL", *owner+":"+*name, err, store, d)
	}
	// ignored: a deferred close after the verb's answer is printed; the answer is the report
	defer func() { _ = conn.Close() }()
	s := &scratch{rdb: conn.Client(), now: d.now}
	key, err := s.spill(ctx, *owner, *name, *value, ttl)
	if err != nil {
		err = conn.Explain(err)
		if redisconn.Classify(err) == redisconn.Unreachable {
			// Open succeeded, so the transaction was handed to a store that was
			// up: a connection that dropped or a reply that never came after
			// that is not "could not run". The EXEC may have committed.
			return unconfirmed(r, conn, store, *owner, *name, err)
		}
		return failed(r, "SPILL", *owner+":"+*name, err, store, d)
	}
	r.line(false, "SPILL OK", "key", key, "ttl", ttl.String(), "expires", expires, "bytes", len(*value))
	return r.done(0)
}

func cmdRecall(args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	r := newReport("recall", fs, args, stdout, stderr)
	store := loginFlags(fs)
	owner := fs.String("owner", "", "the key's owner prefix, as spill was given it: the part before the colon")
	name := fs.String("name", "", "the key's name, as spill was given it: the part after <owner>:")
	problems, ok := parse(fs, args, "addr")
	if !ok {
		return r.refuse(problems...)
	}
	if given(fs, "addr") {
		if err := validAddr(*store.addr); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if problems = append(problems, keyProblems(*owner, *name, time.Hour, false)...); len(problems) > 0 {
		return r.refuse(problems...)
	}
	if err := store.check(d); err != nil {
		return r.refuse(err.Error())
	}
	key := *owner + ":" + *name
	ctx := context.Background()
	conn, err := connect(ctx, store, d)
	if err != nil {
		return failed(r, "RECALL", key, err, store, d)
	}
	// ignored: a deferred close after the verb's answer is printed; the answer is the report
	defer func() { _ = conn.Close() }()
	s := &scratch{rdb: conn.Client(), now: d.now}
	v, err := s.recall(ctx, *owner, *name)
	switch {
	case errors.Is(err, errMissing):
		r.line(false, "RECALL MISSING", "key", key)
		return r.done(1)
	case errors.Is(err, errExpired):
		r.line(false, "RECALL EXPIRED", "key", key)
		return r.done(1)
	case errors.Is(err, errUnbounded):
		r.line(false, "RECALL UNBOUNDED", "key", key, "remedy", quoted("an unbounded key is a bug; it was not written by nova-redis spill"))
		return r.done(1)
	case err != nil:
		// A read has no side effect, so a reply that never came leaves the
		// store as it was: unreachable exits 2 here, honestly, where spill's
		// lost reply exits 1 (unconfirmed).
		return failed(r, "RECALL", key, conn.Explain(err), store, d)
	}
	// The line's value is one token (oneline.Field); --json carries it exactly.
	r.line(false, "RECALL OK", "key", key, "bytes", len(v), "value", v)
	return r.done(0)
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
	fs                      *flag.FlagSet
	addr, user, passwordEnv *string
}

func loginFlags(fs *flag.FlagSet) login {
	l := login{
		fs:          fs,
		addr:        fs.String("addr", "", "the store's address as <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)"),
		user:        fs.String("user", "", "the ACL user to log in as (default $"+UserEnv+"; with neither, the store's default user)"),
		passwordEnv: fs.String("password-env", "", "the NAME of the variable that holds the password, never the password itself (default: the variable $"+PasswordEnvEnv+" names, else "+PasswordEnv+")"),
	}
	// --redis is --addr under the name every other nova tool's store flag has
	fs.StringVar(l.addr, "redis", "", "the same as --addr, under the name every nova tool's store flag has")
	return l
}

// given reports whether the flag was on the line, even empty.
func (l login) given(flagName string) bool { return given(l.fs, flagName) }

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
// numeric port is refused before anything is dialled. An absolute path is a
// Unix socket, which redisconn dials as it is.
func validAddr(addr string) error {
	if strings.TrimSpace(addr) == "" {
		return errors.New("--addr is empty; give the instance as <host:port> or the absolute path of a Unix socket, refusing to guess localhost")
	}
	if strings.HasPrefix(addr, "/") {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q is not <host:port> or the absolute path of a Unix socket; refusing to guess", addr)
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

// failed prints a verb's one FAIL line for err, which is redisconn's (an
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
func failed(r *report, verb, key string, err error, store login, d deps) int {
	class := redisconn.Classify(err)
	kv := []any{"key", key, "class", fmt.Sprint(class), "err", free(oneline.Err(err))}
	if class == redisconn.AuthRefused && d.getenv(*store.passwordEnv) == "" {
		kv = append(kv, "remedy", quoted("nova-redis reads the store's password from "+*store.passwordEnv+", which is not set: export it, holding the password of the default user"))
	}
	r.line(true, verb+" FAIL", kv...)
	if class == redisconn.Unreachable || class == redisconn.AuthRefused {
		return r.done(2)
	}
	return r.done(1)
}

// unconfirmed prints spill's one line for a transaction whose confirmation was
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
func unconfirmed(r *report, conn *redisconn.Conn, store login, owner, name string, err error) int {
	cause := err
	if inner := errors.Unwrap(err); inner != nil {
		cause = inner
	}
	recall := "nova-redis recall " + store.flags() + " --owner " + shellWord(owner) + " --name " + shellWord(name)
	r.line(true, "SPILL UNCONFIRMED", "key", owner+":"+name, "err", free(oneline.Escape(conn.String()+": the transaction was sent and its reply was lost: "+cause.Error())),
		"remedy", quoted("confirmation was lost after the transaction was sent, so the write may have committed; read it back with the same login before spilling again: "+recall))
	return r.done(1)
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
