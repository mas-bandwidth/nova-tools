// Command nova-redis is the Layer 2 binary of docs/SPEC-REDIS.md, the owner of
// the local instance: `serve` launches it bound to loopback and the tailnet,
// with auth from nova-secrets and persistence off (serve.go, #2281). It also
// carries the scratch verbs: `spill` writes a value under an owner
// prefix with a required TTL, and `recall` reads it back and refuses a missing
// or expired key. A write with no owner or no TTL is refused before the
// instance is dialled, so an unbounded key never reaches Redis (rules 2 and
// the spill/recall paragraph of the spec; nova-tools #2279).
//
// Scratch is scratch: nothing spilled is a record, and recall is allowed to
// miss. Auth comes from the environment (NOVA_REDIS_PASSWORD, which a bench
// fills from nova-secrets at run time), never from an argument.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

var version string

// PasswordEnv names the variable auth is read from; never an argument.
const PasswordEnv = "NOVA_REDIS_PASSWORD"

// The hash fields a spilled key carries: the value, and the moment it lapses
// by the spiller's clock, so recall can refuse an expired key even before the
// instance has aged it out.
const (
	fieldValue   = "v"
	fieldExpires = "expires_ms"
)

const usage = `nova-redis — owns the local Redis instance and its scratch verbs (docs/SPEC-REDIS.md)

usage:
  nova-redis serve  --bind <addr>[,<addr>...] --port <port>
  nova-redis spill  --addr <host:port> --owner <owner> --name <name> --ttl <duration> --value <text>
  nova-redis recall --addr <host:port> --owner <owner> --name <name>
  nova-redis version
  nova-redis help

The key is <owner>:<name>. Both verbs refuse a missing or empty --addr, or
one without a host and a port (exit 2), before anything is dialled. spill
refuses a missing owner or a missing, zero or negative TTL (exit 2) and
writes nothing; an unbounded key is a bug.
recall exits 1 on a missing or expired key: scratch is allowed to miss.
Auth is read from NOVA_REDIS_PASSWORD, never from an argument.
serve runs redis-server in the foreground, bound only to loopback and tailnet
addresses (100.64.0.0/10, fd7a:115c:a1e0::/48); --bind has no default and a
wildcard, public or LAN address is refused (exit 2). The password reaches
redis-server on stdin, never in an argument; persistence is off (no RDB, no
AOF) and every launch gets a fresh empty dir, so a restart is a clean slate.

example:
  nova-redis version
  nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve --bind 127.0.0.1,100.101.102.103 --port 6379
  nova-redis spill --addr 127.0.0.1:6379 --owner rowan --name note --ttl 10m --value hi
  nova-redis recall --addr 127.0.0.1:6379 --owner rowan --name note
`

// deps are the seams run() reaches the world through: the instance, the
// clock and the environment. main() passes the real ones; tests pass a fake
// instance and a controlled clock.
type deps struct {
	dial   func(addr, password string) redis.Cmdable
	now    func() time.Time
	getenv func(string) string

	// The serve seams: the environment handed to the child, where the
	// instance program is found, the root its fresh working dir is made
	// under, and the launch itself.
	environ  func() []string
	lookPath func(string) (string, error)
	tempRoot func() string
	launch   func(ctx context.Context, spec launchSpec, stdout, stderr io.Writer) error
}

func realDeps() deps {
	return deps{
		dial: func(addr, password string) redis.Cmdable {
			return redis.NewClient(&redis.Options{Addr: addr, Password: password})
		},
		now:      time.Now,
		getenv:   os.Getenv,
		environ:  os.Environ,
		lookPath: exec.LookPath,
		tempRoot: os.TempDir,
		launch:   launchRedis,
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDeps())) }

func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-redis%s: %s; run: nova-redis help\n", where, oneline.Escape(what))
	return 2
}

func run(args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; serve runs the instance, spill writes scratch, recall reads it")
	}
	switch args[0] {
	case "spill":
		return cmdSpill(args[1:], stdout, stderr, d)
	case "recall":
		return cmdRecall(args[1:], stdout, stderr, d)
	case "serve":
		return cmdServe(args[1:], stdout, stderr, d)
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, " version", fmt.Sprintf("takes no arguments, got %d", len(args)-1))
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-redis", version))
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// parse runs a verb's flag set and reports every required flag that was not
// GIVEN, not only the first, so one run teaches the whole invocation.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) bool {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		refuse(stderr, " "+fs.Name(), err.Error())
		return false
	}
	if fs.NArg() > 0 {
		refuse(stderr, " "+fs.Name(), fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
		return false
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	sort.Strings(required)
	ok := true
	for _, name := range required {
		if !given[name] {
			refuse(stderr, " "+fs.Name(), fmt.Sprintf("--%s is required; refusing to guess", name))
			ok = false
		}
	}
	return ok
}

func cmdSpill(args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("spill", flag.ContinueOnError)
	addr := fs.String("addr", "", "instance address")
	owner := fs.String("owner", "", "owner prefix")
	name := fs.String("name", "", "key name")
	ttlText := fs.String("ttl", "", "time to live")
	value := fs.String("value", "", "value to spill")
	if !parse(fs, args, stderr, "addr", "owner", "name", "ttl", "value") {
		return 2
	}
	if err := validAddr(*addr); err != nil {
		return refuse(stderr, " spill", err.Error())
	}
	ttl, err := time.ParseDuration(*ttlText)
	if err != nil {
		return refuse(stderr, " spill", fmt.Sprintf("--ttl %q is not a duration (try 10m)", *ttlText))
	}
	// Refused before the dial: a write with no owner or no TTL never reaches
	// the instance.
	if err := validKey(*owner, *name, ttl); err != nil {
		return refuse(stderr, " spill", err.Error())
	}
	s := &scratch{rdb: d.dial(*addr, d.getenv(PasswordEnv)), now: d.now}
	key, err := s.spill(context.Background(), *owner, *name, *value, ttl)
	if err != nil {
		fmt.Fprintf(stderr, "SPILL FAIL key=%s err=%s\n", oneline.Field(*owner+":"+*name), oneline.Err(err))
		return 1
	}
	expires := d.now().Add(ttl).UTC().Format(time.RFC3339)
	fmt.Fprintf(stdout, "SPILL OK key=%s ttl=%s expires=%s bytes=%d\n", oneline.Field(key), ttl, expires, len(*value))
	return 0
}

func cmdRecall(args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	addr := fs.String("addr", "", "instance address")
	owner := fs.String("owner", "", "owner prefix")
	name := fs.String("name", "", "key name")
	if !parse(fs, args, stderr, "addr", "owner", "name") {
		return 2
	}
	if err := validAddr(*addr); err != nil {
		return refuse(stderr, " recall", err.Error())
	}
	if err := validKey(*owner, *name, time.Hour); err != nil {
		return refuse(stderr, " recall", err.Error())
	}
	s := &scratch{rdb: d.dial(*addr, d.getenv(PasswordEnv)), now: d.now}
	key := *owner + ":" + *name
	v, err := s.recall(context.Background(), *owner, *name)
	switch {
	case errors.Is(err, errMissing):
		fmt.Fprintf(stdout, "RECALL MISSING key=%s\n", oneline.Field(key))
		return 1
	case errors.Is(err, errExpired):
		fmt.Fprintf(stdout, "RECALL EXPIRED key=%s\n", oneline.Field(key))
		return 1
	case errors.Is(err, errUnbounded):
		fmt.Fprintf(stdout, "RECALL UNBOUNDED key=%s remedy=%q\n", oneline.Field(key), "an unbounded key is a bug; it was not written by nova-redis spill")
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "RECALL FAIL key=%s err=%s\n", oneline.Field(key), oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "RECALL OK key=%s bytes=%d value=%s\n", oneline.Field(key), len(v), oneline.Field(v))
	return 0
}

var (
	errNoOwner   = errors.New("--owner is required and may not be empty or hold ':' or whitespace; every key carries an owner prefix")
	errNoName    = errors.New("--name is required and may not be empty or hold whitespace")
	errNoTTL     = errors.New("--ttl is required and must be above zero; an unbounded key is a bug")
	errMissing   = errors.New("missing")
	errExpired   = errors.New("expired")
	errUnbounded = errors.New("unbounded")
)

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

// validKey is the one gate every write passes: an owner, a name and a TTL
// above zero, or nothing is written.
func validKey(owner, name string, ttl time.Duration) error {
	if owner == "" || strings.ContainsAny(owner, ": \t\r\n") {
		return errNoOwner
	}
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return errNoName
	}
	if ttl <= 0 {
		return errNoTTL
	}
	return nil
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
