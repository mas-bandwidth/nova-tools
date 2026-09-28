// Command nova-config is the one tool for the fleet's permanent,
// non-ephemeral configuration: it owns Postgres (schema `config`, its
// migrations, its history) and every registry of the fleet, and it applies
// that configuration into Redis so Redis is always a rebuildable copy. The
// runtime tools (nova-friend, nova-sprint) read configuration and never
// write it. The contract is docs/SPEC-CONFIG.md; the guide is
// docs/nova-config/README.md.
//
// Every kind (machine, friend, fleet) has the same six verbs -- add, remove,
// set, list, show, history -- generated from its descriptor in
// internal/config, so every kind has identical flags, help and refusals; a
// singleton kind (fleet: one row the migration creates) has set, show and
// history without a name. apply diffs Postgres
// against Redis per kind and writes the difference through the runtime's own
// Redis Functions, compare-and-set on a revision stamped in config:decl.
//
// Exit 0 done, 1 refused (the store or Redis said no: a duplicate, a missing
// row, a ceiling, a conflict), 2 usage (could not run: a flag, a store that
// did not answer). A refusal is one stderr line naming the next step.
package main

import (
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

var version string

const tool = "nova-config"

// The environment every verb reads (docs/nova-config/README.md, "Connecting").
const (
	envPG          = "NOVA_PG_DSN"
	envPGPassEnv   = "NOVA_PG_PASSWORD_ENV"
	defaultPassEnv = "NOVA_PG_PASSWORD"
	envSprintRedis = "NOVA_SPRINT_REDIS"
	envRedisAddr   = "NOVA_REDIS_ADDR"
	envActor       = "NOVA_FRIEND"
)

// The Redis login, the names the fleet's tools have always read
// (internal/nsprint/redisauth): the ACL user turns the login on, and its
// password is in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names, else in
// NOVA_REDIS_BENCH_PASSWORD. With no user the login is the default user's,
// with no password.
const (
	envRedisUser        = "NOVA_SPRINT_REDIS_USER"
	envRedisPassEnv     = "NOVA_SPRINT_REDIS_PASSWORD_ENV"
	defaultRedisPassEnv = "NOVA_REDIS_BENCH_PASSWORD"
)

const usageTop = `nova-config: the fleet's permanent configuration, in Postgres, applied into Redis (see docs/CLI.md)

usage:
  nova-config help
  nova-config version
  nova-config kinds
  nova-config migrate [--pg <dsn>] [--print]
  nova-config status [--pg <dsn>] [--redis <addr>]
  nova-config apply [--pg <dsn>] [--redis <addr>] --as <friend> [--kind <kind>] [--check]
  nova-config <kind> add <name> --<field> <value> ... --as <friend>
  nova-config <kind> set <name> --<field> <value> ... --as <friend>
  nova-config <kind> remove <name> --as <friend>
  nova-config <kind> list
  nova-config <kind> show <name>
  nova-config <kind> history <name>
  nova-config <kind> <verb> -h        prints the verb's usage line and every flag it takes
  nova-config machine list|show <name> [--redis <addr>]   with Redis, each line ends in the machine's live measured facts (its beat)
  nova-config fleet set --<field> <value> ... --as <friend>    the one fleet row (store, coordinator machine): no name, no add, remove or list
  nova-config sprint set --coordinator <friend> --as <friend>  the one sprint row: who coordinates; set it to hand over
  nova-config fleet|sprint show
  nova-config fleet|sprint history

Postgres is the permanent store; Redis is a copy of it that apply rebuilds.
--pg is postgres://user@host:port/db (env NOVA_PG_DSN) with NO password on the
line: the password is read from the variable NOVA_PG_PASSWORD_ENV names
(NOVA_PG_PASSWORD when unset). --redis is host:port (env NOVA_SPRINT_REDIS,
then NOVA_REDIS_ADDR, then the seat's address). --as is the friend making
the change (env NOVA_FRIEND); every write is a row in config.history with it.

A machine's row is the declared facts something reads (user, seat, slots,
runners); its name is the tailnet host ssh reaches. Measured facts (os, arch,
cores, memory) are never typed: machine list and show print them live from
the machine's beat when --redis (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) is
given, beat=none when it has none. A friend's row is what someone decides
for her (slots, tiers, roles); what she would just know is runtime data her
own presence reports. Who coordinates is the sprint row's one field.

migrate creates or upgrades schema config from the migrations in this binary
and applies nothing twice. apply reads Postgres and writes Redis, one kind at
a time, through the runtime's own Redis Functions, and refuses CONFLICT when
Redis holds a newer revision; --check prints the ADD, SET and REMOVE lines
and writes nothing. Lose Redis: run nova-config apply.

exit codes: 0 done, 1 refused, 2 usage

`

// usageExamples ends the banner: the two lines a stranger can paste that
// need no store (docs/ONBOARDING.md point 1). The heading's leading newline
// is what onboarding.ExampleHeading and the pasted-examples rule read.
const usageExamples = `
example:
  nova-config kinds
  nova-config migrate --print
`

// kindsUsage is the per-kind part of the banner, from the descriptors.
func kindsUsage() string {
	var b strings.Builder
	b.WriteString("kinds:\n")
	for _, k := range config.Kinds {
		fmt.Fprintf(&b, "  %-8s %s\n", k.Name, k.Doc)
		if k.Singleton {
			fmt.Fprintf(&b, "    %-10s one row, created by migrate: set, show and history take no name; no add, remove or list\n", "")
		}
		for _, f := range k.Fields {
			req := ""
			if f.Required {
				req = " (required on add)"
			}
			fmt.Fprintf(&b, "    --%-8s %s%s\n", f.Name, f.Help, req)
		}
	}
	return b.String()
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDeps())) }

// pgStore is the store a verb opens: config.Store plus the schema verbs.
type pgStore interface {
	config.Store
	Migrate(ctx context.Context) (from, to int, applied []int, err error)
	Version(ctx context.Context) (int, error)
	Close() error
}

// redisSide is the Redis a verb opens: apply's side and the beats machine
// list and show read live.
type redisSide interface {
	config.Applier
	config.BeatReader
	Close() error
}

// deps are the seams: the environment, the two stores and the clock. The
// unit tests hand in config.Mem and a fake Applier; main hands in Postgres
// and the fleet Redis. openRedis reads the login from the getenv it is
// handed, never from the process on its own.
type deps struct {
	getenv    func(string) string
	openStore func(ctx context.Context, dsn string) (pgStore, error)
	openRedis func(ctx context.Context, addr string, getenv func(string) string) (redisSide, error)
	now       func() time.Time
}

// redisLogin is who nova-config logs in to Redis as, from the variables
// above: redisconn reads no default password variable of its own, so the
// default is named here.
func redisLogin(addr string, getenv func(string) string) redisconn.Options {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: envRedisUser}}
	if getenv(envRedisUser) != "" {
		o.PasswordEnv = getenv(envRedisPassEnv)
		if o.PasswordEnv == "" {
			o.PasswordEnv = defaultRedisPassEnv
		}
	}
	return o
}

// redisError is a failure to reach the store or to log in to it after it
// was opened, already one line of redisconn's; any other error of the
// store (a refusal, a conflict) is passed through as it came.
type redisError struct{ error }

func (e redisError) Unwrap() error { return e.error }

// redisApplier is the Redis side on a redisconn connection. Every error
// that is the store being unreachable or refusing the login is explained by
// the connection (redisconn.Conn.Explain) and marked a redisError.
type redisApplier struct {
	a    *config.RedisApplier
	conn *redisconn.Conn
}

func (r redisApplier) explain(err error) error {
	if c := redisconn.Classify(err); c == redisconn.Unreachable || c == redisconn.AuthRefused {
		return redisError{r.conn.Explain(err)}
	}
	return err
}

func (r redisApplier) Read(ctx context.Context, kind string) (map[string]config.View, int64, error) {
	v, rev, err := r.a.Read(ctx, kind)
	return v, rev, r.explain(err)
}

func (r redisApplier) Prepare(ctx context.Context) error { return r.explain(r.a.Prepare(ctx)) }

func (r redisApplier) Write(ctx context.Context, kind string, row config.Row, prev config.View, actor, idem string) error {
	return r.explain(r.a.Write(ctx, kind, row, prev, actor, idem))
}

func (r redisApplier) Remove(ctx context.Context, kind, name, actor, idem string) error {
	return r.explain(r.a.Remove(ctx, kind, name, actor, idem))
}

func (r redisApplier) Stamp(ctx context.Context, kind string, prev, rev int64) error {
	return r.explain(r.a.Stamp(ctx, kind, prev, rev))
}

func (r redisApplier) Beats(ctx context.Context, names []string) (map[string]*config.Beat, error) {
	bs, err := r.a.Beats(ctx, names)
	return bs, r.explain(err)
}

func (r redisApplier) Close() error { return r.conn.Close() }

func realDeps() deps {
	return deps{
		getenv: os.Getenv,
		openStore: func(ctx context.Context, dsn string) (pgStore, error) {
			return config.OpenPG(ctx, dsn)
		},
		openRedis: func(ctx context.Context, addr string, getenv func(string) string) (redisSide, error) {
			conn, err := redisconn.Open(ctx, redisLogin(addr, getenv), getenv)
			if err != nil {
				return nil, redisError{err}
			}
			return redisApplier{a: &config.RedisApplier{Client: conn.Client()}, conn: conn}, nil
		},
		now: time.Now,
	}
}

func run(args []string, stdout, stderr io.Writer, d deps) (code int) {
	defer verbflag.Recover(stdout, tool, &code)
	ctx := context.Background()
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; want kinds, migrate, status, apply, or <kind> add|set|remove|list|show|history")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return refuse(stderr, "help", "help takes no arguments")
		}
		fmt.Fprint(stdout, usageTop+kindsUsage()+usageExamples)
		return 0
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line(tool, version))
		return 0
	case "kinds":
		if len(args) > 1 {
			return refuse(stderr, "kinds", "kinds takes no arguments")
		}
		for _, k := range config.Kinds {
			fmt.Fprintln(stdout, config.KindLine(k))
		}
		fmt.Fprintf(stdout, "CONFIG KINDS count=%d\n", len(config.Kinds))
		return 0
	case "migrate":
		return runMigrate(ctx, args[1:], stdout, stderr, d)
	case "status":
		return runStatus(ctx, args[1:], stdout, stderr, d)
	case "apply":
		return runApply(ctx, args[1:], stdout, stderr, d)
	}
	if k, ok := config.Lookup(args[0]); ok {
		return runKind(ctx, k, args[1:], stdout, stderr, d)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %s; want kinds, migrate, status, apply, or one of the kinds %s", args[0], strings.Join(config.KindNames(), ", ")))
}

// refuse is the exit 2 line: the invocation could not run.
func refuse(stderr io.Writer, verb, what string) int {
	where := ""
	if verb != "" {
		where = " " + verb
	}
	fmt.Fprintf(stderr, "%s%s: %s; run: %s help\n", tool, where, oneline.Escape(what), tool)
	return 2
}

// refused is the exit 1 line: the tool ran and the store or Redis said no.
// next names the command that resolves it.
func refused(stderr io.Writer, verb, what, next string) int {
	fmt.Fprintf(stderr, "%s %s: %s; run: %s\n", tool, verb, oneline.Escape(what), next)
	return 1
}

// redisRefused is the exit 2 line of a Redis that could not be used: it was
// not reached, or it refused the login. The line is redisconn's own, which
// names the store, the login and the next step.
func redisRefused(stderr io.Writer, verb string, err error) int {
	fmt.Fprintf(stderr, "%s %s: %s\n", tool, verb, oneline.Escape(err.Error()))
	return 2
}

// refuseErr is refuse for an error that may be a Redis that could not be
// used, which is redisRefused's line instead.
func refuseErr(stderr io.Writer, verb string, err error) int {
	if isRedisError(err) {
		return redisRefused(stderr, verb, err)
	}
	return refuse(stderr, verb, err.Error())
}

// isRedisError reports a Redis that could not be used (redisRefused).
func isRedisError(err error) bool {
	var r redisError
	return errors.As(err, &r)
}

// storeErr turns a store error into the right line: a refusal (exit 1) with
// its remedy, a Redis that could not be used (exit 2, redisRefused), or a
// store that did not answer (exit 2).
func storeErr(stderr io.Writer, verb string, err error, next string) int {
	if isRedisError(err) {
		return redisRefused(stderr, verb, err)
	}
	if config.Refused(err) {
		return refused(stderr, verb, err.Error(), next)
	}
	return refuse(stderr, verb, err.Error())
}

// --- connection flags -------------------------------------------------------

// connFlags adds --pg, --redis and --as to a verb's flag set.
func connFlags(fs *stdflag.FlagSet, withRedis, withActor bool) (pg, redisAddr, actor *string) {
	pg = fs.String("pg", "", "Postgres DSN postgres://user@host:port/db with no password (env NOVA_PG_DSN); the password comes from the variable NOVA_PG_PASSWORD_ENV names")
	if withRedis {
		redisAddr = fs.String("redis", "", "Redis address host:port (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	}
	if withActor {
		actor = fs.String("as", "", "the friend making the change (env NOVA_FRIEND); every write records it in config.history")
	}
	return pg, redisAddr, actor
}

// pgDSN resolves the Postgres DSN: the flag, else NOVA_PG_DSN; a flag that
// carries a password is refused (it would be on the command line, where a ps
// reads it); a DSN without one takes it from the variable
// NOVA_PG_PASSWORD_ENV names, NOVA_PG_PASSWORD when unset, and a named
// variable that is empty is refused with its name (the shape
// internal/nsprint/redisauth keeps for Redis).
func pgDSN(flagValue string, getenv func(string) string) (string, error) {
	dsn := flagValue
	if dsn == "" {
		dsn = getenv(envPG)
	}
	if dsn == "" {
		return "", fmt.Errorf("--pg is required: postgres://user@host:5432/nova (or %s)", envPG)
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("--pg: %v; want postgres://user@host:5432/nova", err)
	}
	if flagValue != "" && strings.Contains(flagValue, "://") {
		if u, err := url.Parse(flagValue); err == nil {
			if _, has := u.User.Password(); has {
				return "", fmt.Errorf("--pg carries a password; leave it out and export it as the variable %s names (a ps reads the line)", envPGPassEnv)
			}
		}
	}
	if cfg.Password != "" {
		return dsn, nil
	}
	name := getenv(envPGPassEnv)
	named := name != ""
	if !named {
		name = defaultPassEnv
	}
	pw := getenv(name)
	if pw == "" {
		if named {
			return "", fmt.Errorf("%s=%s but %s is empty; run under nova-secrets exec --only %s", envPGPassEnv, name, name, name)
		}
		return dsn, nil
	}
	return withPassword(dsn, cfg.User, pw)
}

// withPassword puts the password into the DSN in memory, in whichever of
// the two spellings it is written.
func withPassword(dsn, user, pw string) (string, error) {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("--pg: %v", err)
		}
		if user == "" {
			user = u.User.Username()
		}
		u.User = url.UserPassword(user, pw)
		return u.String(), nil
	}
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(pw)
	return dsn + " password='" + escaped + "'", nil
}

// redisAddress resolves --redis: the flag, else NOVA_SPRINT_REDIS, else
// NOVA_REDIS_ADDR, else the selected seat's address.
func redisAddress(flagValue string, getenv func(string) string) (string, error) {
	for _, v := range []string{flagValue, getenv(envSprintRedis), getenv(envRedisAddr), seatcred.Process().Addr()} {
		if v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("--redis is required: host:port (or %s, %s, or a seat)", envSprintRedis, envRedisAddr)
}

// liveRedisAddress is the Redis machine list and show read beats from when
// one is given: the flag, else NOVA_SPRINT_REDIS, else NOVA_REDIS_ADDR; ""
// (no live facts, no store opened) when none is. The seat is not consulted:
// a list that dials Redis nobody named would be a surprise.
func liveRedisAddress(flagValue string, getenv func(string) string) string {
	for _, v := range []string{flagValue, getenv(envSprintRedis), getenv(envRedisAddr)} {
		if v != "" {
			return v
		}
	}
	return ""
}

// actorName resolves --as: the flag, else NOVA_FRIEND.
func actorName(flagValue string, getenv func(string) string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v := getenv(envActor); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("--as is required: the friend making the change (or %s)", envActor)
}

// --- the kind verbs ---------------------------------------------------------

func runKind(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		if k.Singleton {
			return refuse(stderr, k.Name, "want set, show or history")
		}
		return refuse(stderr, k.Name, "want add, set, remove, list, show or history")
	}
	verb := k.Name + " " + args[0]
	if k.Singleton {
		switch args[0] {
		case "add", "remove", "list":
			return refuse(stderr, verb, k.Name+" is one row, created by migrate; want set, show or history")
		}
	}
	switch args[0] {
	case "add", "set":
		return runKindWrite(ctx, k, args[0] == "add", args[1:], stdout, stderr, d)
	case "remove":
		return runKindRemove(ctx, k, args[1:], stdout, stderr, d)
	case "list":
		return runKindList(ctx, k, args[1:], stdout, stderr, d)
	case "show", "history":
		return runKindRead(ctx, k, args[0], args[1:], stdout, stderr, d)
	}
	return refuse(stderr, verb, "unknown verb; want add, set, remove, list, show or history")
}

// nameAndRest takes the row name: the first argument when it is not a flag,
// else the one positional left after the flags. A singleton's name is the
// kind's own and the line carries none.
func nameAndRest(k *config.Kind, args []string) (string, []string) {
	if k.Singleton {
		return k.Name, args
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// positional resolves the one positional a verb allows: the name when
// nameAndRest found none. A singleton allows none at all.
func positional(k *config.Kind, fs *stdflag.FlagSet, name, verb string) (string, error) {
	switch {
	case k.Singleton && fs.NArg() > 0:
		return "", fmt.Errorf("%s takes no name: it is one row; want %s --<field> <value> ...", k.Name, verb)
	case name == "" && fs.NArg() == 1:
		return fs.Arg(0), nil
	case fs.NArg() > 0:
		return "", fmt.Errorf("want %s <name> --<field> <value> ...; flags follow the name", verb)
	}
	return name, nil
}

func runKindWrite(ctx context.Context, k *config.Kind, add bool, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " set"
	if add {
		verb = k.Name + " add"
	}
	fs := verbflag.New(verb)
	pg, _, as := connFlags(fs, false, true)
	values := map[string]*string{}
	for _, f := range k.Fields {
		values[f.Name] = fs.String(f.Name, "", f.Help)
	}
	name, rest := nameAndRest(k, args)
	if err := fs.Parse(rest); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	name, err := positional(k, fs, name, verb)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	given := map[string]string{}
	fs.Visit(func(f *stdflag.Flag) {
		if v, ok := values[f.Name]; ok {
			given[f.Name] = *v
		}
	})
	var problems []string
	actor, err := actorName(*as, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	var row config.Row
	var changes map[string]string
	if add {
		row, err = k.NewRow(name, given)
	} else {
		if err = config.ValidateName(name); err == nil {
			changes, err = k.Changes(given)
		}
	}
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	if add {
		id, err := st.Insert(ctx, k.Name, row, actor)
		if err != nil {
			return storeErr(stderr, verb, err, tool+" "+k.Name+" set "+name+" --<field> <value>")
		}
		fmt.Fprintf(stdout, "CONFIG ADD kind=%s name=%s rev=%d\n", k.Name, config.Value(name), id)
		return 0
	}
	_, id, err := st.Update(ctx, k.Name, name, changes, actor)
	if err != nil {
		next := tool + " " + k.Name + " add " + name + " --<field> <value> ..."
		if k.Singleton {
			next = tool + " " + k.Name + " show"
		}
		return storeErr(stderr, verb, err, next)
	}
	fields := make([]string, 0, len(changes))
	for f := range changes {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	fmt.Fprintf(stdout, "CONFIG SET kind=%s name=%s rev=%d changed=%s\n", k.Name, config.Value(name), id, config.Value(strings.Join(fields, ",")))
	return 0
}

func runKindRemove(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " remove"
	fs := verbflag.New(verb)
	pg, _, as := connFlags(fs, false, true)
	name, rest := nameAndRest(k, args)
	if err := fs.Parse(rest); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return refuse(stderr, verb, "want "+verb+" <name>")
	}
	var problems []string
	if err := config.ValidateName(name); err != nil {
		problems = append(problems, err.Error())
	}
	actor, err := actorName(*as, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	id, err := st.Delete(ctx, k.Name, name, actor)
	if err != nil {
		return storeErr(stderr, verb, err, tool+" "+k.Name+" list")
	}
	fmt.Fprintf(stdout, "CONFIG REMOVE kind=%s name=%s rev=%d\n", k.Name, config.Value(name), id)
	return 0
}

// live is true for the kind whose rows have a beat to read: a machine.
func live(k *config.Kind) bool { return k.Name == config.KindMachine }

// liveFlag adds --redis to a machine's list and show.
func liveFlag(fs *stdflag.FlagSet, k *config.Kind) *string {
	if !live(k) {
		return new(string)
	}
	return fs.String("redis", "", "Redis address host:port (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR); when given, each line ends in the machine's live measured facts from its beat")
}

// beats reads the named machines' beats when a Redis is named, else nil
// (no live facts on the lines).
func beats(ctx context.Context, addr string, names []string, d deps) (map[string]*config.Beat, error) {
	if addr == "" {
		return nil, nil
	}
	rs, err := d.openRedis(ctx, addr, d.getenv)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	return rs.Beats(ctx, names)
}

// liveSuffix is the line's live part: nothing when no Redis was named.
func liveSuffix(bs map[string]*config.Beat, name string) string {
	if bs == nil {
		return ""
	}
	return config.LiveLine(bs[name])
}

func runKindList(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " list"
	fs := verbflag.New(verb)
	pg, _, _ := connFlags(fs, false, false)
	redisFlag := liveFlag(fs, k)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "list takes no name; want "+verb)
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	rows, err := st.List(ctx, k.Name)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	var bs map[string]*config.Beat
	if live(k) {
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			names = append(names, row.Name)
		}
		if bs, err = beats(ctx, liveRedisAddress(*redisFlag, d.getenv), names, d); err != nil {
			return refuseErr(stderr, verb, err)
		}
	}
	for _, row := range rows {
		fmt.Fprintln(stdout, config.RowLine(k, row)+liveSuffix(bs, row.Name))
	}
	fmt.Fprintf(stdout, "CONFIG LIST kind=%s rows=%d\n", k.Name, len(rows))
	return 0
}

func runKindRead(ctx context.Context, k *config.Kind, which string, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " " + which
	fs := verbflag.New(verb)
	pg, _, _ := connFlags(fs, false, false)
	var redisFlag *string
	if which == "show" {
		redisFlag = liveFlag(fs, k)
	}
	name, rest := nameAndRest(k, args)
	if err := fs.Parse(rest); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	switch {
	case k.Singleton && fs.NArg() > 0:
		return refuse(stderr, verb, k.Name+" takes no name: it is one row; want "+verb)
	case name == "" && fs.NArg() == 1:
		name = fs.Arg(0)
	case fs.NArg() > 0:
		return refuse(stderr, verb, "want "+verb+" <name>")
	}
	var problems []string
	if err := config.ValidateName(name); err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	if which == "show" {
		row, found, err := st.Get(ctx, k.Name, name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		if !found {
			return refused(stderr, verb, k.Name+" "+name+" not found", tool+" "+k.Name+" list")
		}
		suffix := ""
		if live(k) {
			bs, err := beats(ctx, liveRedisAddress(*redisFlag, d.getenv), []string{name}, d)
			if err != nil {
				return refuseErr(stderr, verb, err)
			}
			suffix = liveSuffix(bs, name)
		}
		fmt.Fprintln(stdout, config.ShowLine(k, row)+suffix)
		return 0
	}
	changes, err := st.History(ctx, k.Name, name)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(changes) == 0 && !k.Singleton {
		return refused(stderr, verb, k.Name+" "+name+" has no history: it was never added", tool+" "+k.Name+" list")
	}
	for _, c := range changes {
		fmt.Fprintln(stdout, config.HistoryLine(c))
	}
	fmt.Fprintf(stdout, "CONFIG HISTORY kind=%s name=%s changes=%d\n", k.Name, config.Value(name), len(changes))
	return 0
}

// --- migrate, status, apply -------------------------------------------------

func runMigrate(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "migrate"
	fs := verbflag.New(verb)
	pg, _, _ := connFlags(fs, false, false)
	print := fs.Bool("print", false, "list the migrations this binary carries and connect to nothing")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "migrate takes no arguments")
	}
	all, err := config.Migrations()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *print {
		for _, m := range all {
			fmt.Fprintf(stdout, "MIGRATION version=%d file=%s lines=%d\n", m.Version, config.Value(m.Name), strings.Count(m.SQL, "\n"))
		}
		fmt.Fprintf(stdout, "CONFIG MIGRATE print=%d pg=-\n", len(all))
		return 0
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	from, to, applied, err := st.Migrate(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	fmt.Fprintf(stdout, "CONFIG MIGRATE pg=%s from=%d to=%d applied=%d\n", config.Value(config.Redact(dsn)), from, to, len(applied))
	return 0
}

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "status"
	fs := verbflag.New(verb)
	pg, redisFlag, _ := connFlags(fs, true, false)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "status takes no arguments")
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	schema, err := st.Version(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	line := "CONFIG STATUS pg=" + config.Value(config.Redact(dsn)) + " schema=" + strconv.Itoa(schema)
	if schema == 0 {
		fmt.Fprintln(stdout, line+" redis=-")
		fmt.Fprintf(stderr, "%s status: schema config is not there yet; run: %s migrate\n", tool, tool)
		return 1
	}
	counts, err := st.Counts(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	revs := map[string]int64{}
	for _, k := range config.Kinds {
		rev, err := st.Rev(ctx, k.Name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		revs[k.Name] = rev
		if k.Singleton {
			line += fmt.Sprintf(" %s_rev=%d", k.Name, rev)
			continue
		}
		line += fmt.Sprintf(" %s=%d %s_rev=%d", k.Name, counts[k.Name], k.Name, rev)
	}
	addr, addrErr := redisAddress(*redisFlag, d.getenv)
	if addrErr != nil {
		fmt.Fprintln(stdout, line+" redis=-")
		return 0
	}
	rs, err := d.openRedis(ctx, addr, d.getenv)
	if err != nil {
		return refuseErr(stderr, verb, err)
	}
	defer rs.Close()
	line += " redis=" + config.Value(addr)
	behind := 0
	for _, k := range config.Kinds {
		_, applied, err := rs.Read(ctx, k.Name)
		if err != nil {
			return refuseErr(stderr, verb, err)
		}
		line += fmt.Sprintf(" %s_applied=%d", k.Name, applied)
		if applied != revs[k.Name] {
			behind++
		}
	}
	fmt.Fprintln(stdout, line)
	if behind > 0 {
		fmt.Fprintf(stderr, "%s status: Redis is not at Postgres's revision for %d kind(s); run: %s apply\n", tool, behind, tool)
		return 1
	}
	return 0
}

func runApply(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "apply"
	fs := verbflag.New(verb)
	pg, redisFlag, as := connFlags(fs, true, true)
	kind := fs.String("kind", "", "one kind to apply ("+strings.Join(config.KindNames(), ", ")+"); every kind, in order, when unset")
	check := fs.Bool("check", false, "print the ADD, SET and REMOVE lines and write nothing")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "apply takes no arguments; flags only")
	}
	var problems []string
	kinds := config.KindNames()
	if *kind != "" {
		if _, ok := config.Lookup(*kind); !ok {
			problems = append(problems, fmt.Sprintf("--kind %s: want one of %s", *kind, strings.Join(config.KindNames(), ", ")))
		}
		kinds = []string{*kind}
	}
	actor, err := actorName(*as, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	addr, err := redisAddress(*redisFlag, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	rs, err := d.openRedis(ctx, addr, d.getenv)
	if err != nil {
		return refuseErr(stderr, verb, err)
	}
	defer rs.Close()
	word := "APPLY"
	if *check {
		word = "CHECK"
	}
	for _, kn := range kinds {
		start := d.now()
		res, err := config.Apply(ctx, st, rs, kn, actor, *check, func(op config.Op) {
			fmt.Fprintln(stdout, config.OpLine(word, kn, op))
		})
		if err != nil {
			if config.IsConflict(err) {
				return refused(stderr, verb, err.Error(), tool+" status (then apply from the Postgres that is ahead)")
			}
			return storeErr(stderr, verb, err, tool+" apply --check")
		}
		if *check {
			fmt.Fprintf(stdout, "CONFIG CHECK kind=%s add=%d set=%d remove=%d rev=%d applied=%d\n", kn, res.Add, res.Set, res.Remove, res.Rev, res.RedisRev)
			continue
		}
		fmt.Fprintf(stdout, "CONFIG APPLY kind=%s add=%d set=%d remove=%d rev=%d ms=%d\n", kn, res.Add, res.Set, res.Remove, res.Rev, d.now().Sub(start).Milliseconds())
	}
	return 0
}
