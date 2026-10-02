// Command nova-config is the one tool for the fleet's permanent,
// non-ephemeral configuration: it owns Postgres (schema `config`, its
// migrations, its history) and every registry of the fleet, and it applies
// that configuration into Redis so Redis is always a rebuildable copy. The
// runtime tools (nova-friend, nova-sprint) read configuration and never
// write it. The contract is docs/SPEC-CONFIG.md; the guide is
// docs/nova-config/README.md.
//
// Every kind (machine, fleet, friend, sprint, loop, route) has the same six verbs -- add, remove,
// set, list, show, history -- generated from its descriptor in
// internal/config, so every kind has identical flags, help and refusals; a
// singleton kind (fleet: one row the migration creates) has set, show and
// history without a name. apply diffs Postgres
// against Redis per kind and writes the difference through the runtime's own
// Redis Functions, compare-and-set on a revision stamped in config:decl.
//
// Exit 0 done, 1 refused (the store or Redis said no: a duplicate, a missing
// row, a ceiling, a conflict), 2 usage (could not run: a flag, a store that
// did not answer), plus two for machine self: 2 when --check finds the name is
// no machine row, and 3 when the name or the rows could not be read. A refusal
// is one stderr line naming the next step.
package main

import (
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

var version string

const tool = "nova-config"

// The environment every verb reads (docs/nova-config/README.md, "Connecting").
const (
	envPG          = config.EnvPG
	envPGPassEnv   = config.EnvPGPassEnv
	defaultPassEnv = config.DefaultPassEnv
	envSprintRedis = "NOVA_SPRINT_REDIS"
	envRedisAddr   = "NOVA_REDIS_ADDR"
	envActor       = "NOVA_FRIEND"
	envMachine     = "NOVA_MACHINE"
)

const usageTop = `nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: PostgreSQL holds the rows, in the schema migrate makes: a machine
row per host ssh reaches, a friend row per AI, one fleet row, one sprint row,
a loop row per supervised process, a route row per way to run a model tier
and a tier row per tier, its route array;
every write adds a history row naming who made it. apply copies the rows into
Redis, the view the fleet reads; status checks Redis is at Postgres's revision.
first run: the lines under example: need no database; the rest needs PostgreSQL:
export NOVA_PG_DSN=postgres://user@127.0.0.1:5432/db (a database you own), then
run migrate.

usage:
  nova-config help
  nova-config version
  nova-config kinds
  nova-config migrate [--pg <dsn>] [--print]
  nova-config status [--pg <dsn>] [--redis <addr>]
  nova-config apply [--pg <dsn>] [--redis <addr>] [--as <friend>] [--kind <kind>] [--check]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>] [--timeout <duration>]
      prints an Ansible dynamic JSON inventory of the applied state (the Redis view apply writes, never Postgres): groups all and benches are every machine, coordinator, store and store_deployer (the coordinator machine) come from the fleet row, runners is every machine with at least one runner; every host's variables are under _meta.hostvars: ansible_user, nova_seat, slots, runners, nova_os and nova_arch from the machine's beat when it has one, and nova_loops, its loop records, once the loop kind has been applied; all.vars holds nova_store and nova_config_rev
      first run, with no store: nova-config inventory --fixture fleet/testdata/inventory-fixture.yml
      against the store: export NOVA_SPRINT_REDIS=127.0.0.1:6379; nova-config inventory
      ansible's -i wants an executable file whose first line is #!/bin/sh at column one; write it with these two commands, then run ansible with ANSIBLE_INVENTORY_UNPARSED_FAILED=true, because without it a failed inventory is an empty inventory and the play does nothing (ansible.cfg: [inventory] unparsed_is_failed = True):
      printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
      chmod +x nova-inventory
      ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list
      env: NOVA_SPRINT_REDIS (then NOVA_REDIS_ADDR, then the seat's address) names the store; NOVA_MACHINE names the machine row this process runs on (an empty value counts as unset), matched by exact machine name and refused with the known names when it names no row; when it is unset the lower-cased first label of the hostname is matched, and nothing is marked local when that matches no row
      this verb exits 0 when it printed, 1 when the applied state or an unknown machine refused it, 2 when it could not run (usage, connection, timeout)
  nova-config <kind> add <name> --<field> <value> ... --as <friend>
  nova-config <kind> set <name> --<field> <value> ... --as <friend>
  nova-config <kind> remove <name> --as <friend>
  nova-config <kind> list
  nova-config <kind> show <name>
  nova-config <kind> history <name>
  nova-config <kind> <verb> -h        prints the verb's usage line and every flag it takes
  nova-config machine list|show <name> [--redis <addr>]   with Redis, each line ends in the machine's live measured facts (its beat); show names the machine's loops (loops=<a,b>)
  nova-config machine width <name> [--pg <dsn>] [--json]   the width of the sprint's member on the machine: the row's width field (machine set <name> --width <n>), what nova-sprint fleet sync sets; a machine with width above 0 is a member; no other row and no Redis takes part
  nova-config machine self [--check] [--pg <dsn>]          prints this machine's own name as the config keys it (NOVA_MACHINE, else the tailnet's name for the host when a tailnet is running, else the hostname's first label) and opens no store; --check reads the machine rows and exits 2 when the name is none of them, 3 when the name or the rows cannot be read (exit codes of this verb: 0 printed, 2 not a row or usage, 3 unreadable)
  nova-config fleet set --<field> <value> ... --as <friend>    the one fleet row (store, coordinator machine): no name, no add, remove or list
  nova-config sprint set --coordinator <friend> --as <friend>  the one sprint row: who coordinates; set it to hand over
  nova-config fleet|sprint show
  nova-config fleet|sprint history
  nova-config loop add <name> --machine <m> --argv '["/path/prog","--flag","v"]' (--every <seconds> | --keepalive true) [--seat <seat> --keys <NAME,...>] [--width <n>] [--enabled false] --as <friend>
      a supervised loop on one machine; the secrets it needs go by NAME in --keys, opened from --seat, never on the command; apply writes loop:<name> (its fields and its log path ~/nova-bench/loops/<name>.log) and the set loops, which the plays read
  nova-config route add <name> --tier flash|pro --provider <p> --model <m> --deadline <seconds> [--tokens <n>] [--enabled false] --as <friend>
      one way to run a model tier: the harness is launched with <provider>/<model>; the deal takes a card's route from its tier's array, and a card's model: header pins it instead; frontier cards are never dealt from routes, they escalate to the coordinator; apply writes route:<name> (its fields) and the set routes, which the deal reads
  nova-config route set <name> --price_input <usd> --price_cache_read <usd> --price_cache_write <usd> --price_output <usd> [--reasoning_as_output false] [--long_context <tokens> --price_input_long <usd> --price_output_long <usd>] [--price_request <usd>] [--billing metered|plan] [--gateway_percent <pct>] [--price_source <text>] [--price_as_of YYYY-MM-DD] --as <friend>
      the route's price sheet, optional: USD per million tokens of each class (per request for --price_request), each a decimal kept as typed in its one spelling, never a float; a price not set is empty, and a card on a route with none has no predicted cost; apply writes them into route:<name> beside its other fields
  nova-config tier set flash|pro --routes <route,route,...> --as <friend>
      the tier's route array, in order, a route named twice taking two turns; each an enabled route of the tier; the deal takes routes[index mod len] for each card of the tier and moves the index by one; empty takes the tier's enabled routes in name order; migrate makes both rows; tier list prints them; apply writes tier:<name> and the set tiers, which the deal reads with the routes

Postgres is the permanent store; Redis is a copy of it that apply rebuilds.
Connect with export NOVA_PG_DSN=postgres://user@host:5432/db (or --pg) with NO
password on the line: the password is read from the variable
NOVA_PG_PASSWORD_ENV names (NOVA_PG_PASSWORD when unset). --redis is host:port
(env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address). --as is
the friend making the change (env NOVA_FRIEND); every write is a row in
config.history with it (omitted on apply --check).

A machine's row is the declared facts something reads (user, seat, slots,
runners, width); its name is the tailnet host ssh reaches. Measured facts (os, arch,
cores, memory) are never typed: machine list and show print them live from
the machine's beat when --redis (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) is
given, beat=none when it has none. A friend's row is what someone decides
for her (slots, tiers, roles); what she would just know is runtime data her
own presence reports. Who coordinates is the sprint row's one field. A
loop's row names its machine, its command and the names of its secrets as
data; the code names none of them.

migrate creates or upgrades schema config from the migrations in this binary
and applies nothing twice. apply reads Postgres and writes Redis, one kind at
a time, through the runtime's own Redis Functions, and refuses CONFLICT when
Redis holds a newer revision; --check prints the ADD, SET and REMOVE lines
and writes nothing. inventory prints an Ansible dynamic JSON inventory of
the applied state and reads only Redis. Lose Redis: run nova-config apply.

exit codes: 0 done, 1 refused, 2 usage

`

// usageExamples ends the banner: the two lines a stranger can paste that
// need no store (docs/ONBOARDING.md point 1). The heading's leading newline
// is what onboarding.ExampleHeading and the pasted-examples rule read.
const usageExamples = `
example:
  nova-config kinds
  nova-config migrate --print
  nova-config machine add -h
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
	// Snapshot is the applied state inventory prints.
	Snapshot(ctx context.Context) (*config.Snapshot, error)
	Close() error
}

// deps are the seams: the environment, the two stores and the clock. The
// unit tests hand in config.Mem and a fake Applier; main hands in Postgres
// and the fleet Redis.
type deps struct {
	getenv    func(string) string
	openStore func(ctx context.Context, dsn string) (pgStore, error)
	openRedis func(ctx context.Context, addr string) (redisSide, error)
	now       func() time.Time
	hostname  func() (string, error)
	// tailscale is `tailscale status --json --peers=false`, config.ErrNoTailnet
	// when no tailnet is installed: machine self reads its name from it.
	tailscale func(ctx context.Context) ([]byte, error)
}

type redisApplier struct {
	*config.RedisApplier
	st *store.Store
}

func (r redisApplier) Close() error { return r.st.Close() }

func realDeps() deps {
	return deps{
		getenv: os.Getenv,
		openStore: func(ctx context.Context, dsn string) (pgStore, error) {
			return config.OpenPG(ctx, dsn)
		},
		openRedis: func(ctx context.Context, addr string) (redisSide, error) {
			st, err := store.Open(ctx, addr)
			if err != nil {
				return nil, err
			}
			return redisApplier{RedisApplier: &config.RedisApplier{Client: st.Client()}, st: st}, nil
		},
		now:       time.Now,
		hostname:  os.Hostname,
		tailscale: config.TailscaleStatus,
	}
}

func run(args []string, stdout, stderr io.Writer, d deps) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before anything is dialed or written (the CLI style's rule (b), #4505).
	defer verbflag.Recover(stdout, tool, usageTop+kindsUsage()+usageExamples, &code)
	ctx := context.Background()
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; want kinds, migrate, status, apply, inventory, or <kind> add|set|remove|list|show|history")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return run(append(args[1:], "--help"), stdout, stderr, d)
		}
		if len(args) > 1 && args[1] != "help" {
			return refuse(stderr, "help", "help takes a verb or nothing")
		}
		fmt.Fprint(stdout, usageTop+kindsUsage()+usageExamples)
		return 0
	case "version", "--version":
		verbflag.HelpIfAsked(args[1:], "version")
		if len(args) > 1 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line(tool, version))
		return 0
	case "kinds":
		verbflag.HelpIfAsked(args[1:], "kinds")
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
	case "inventory":
		return runInventory(ctx, args[1:], stdout, stderr, d)
	}
	if k, ok := config.Lookup(args[0]); ok {
		return runKind(ctx, k, args[1:], stdout, stderr, d)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %s; want kinds, migrate, status, apply, inventory, or one of the kinds %s", args[0], strings.Join(config.KindNames(), ", ")))
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

// storeErr turns a store error into the right line: a refusal (exit 1) with
// its remedy, or a store that did not answer (exit 2).
func storeErr(stderr io.Writer, verb string, err error, next string) int {
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

// pgDSN resolves the Postgres DSN by the rules every reader of nova-config
// shares (config.ResolveDSN).
func pgDSN(flagValue string, getenv func(string) string) (string, error) {
	return config.ResolveDSN(flagValue, getenv)
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
	if len(args) > 0 {
		verbflag.HelpIfAsked(args[:1], k.Name)
	}
	if len(args) == 0 {
		if k.Singleton {
			return refuse(stderr, k.Name, "want set, show or history")
		}
		return refuse(stderr, k.Name, "want add, set, remove, list, show or history")
	}
	verb := k.Name + " " + args[0]
	if k.Name == config.KindMachine {
		switch args[0] {
		case "self":
			return runMachineSelf(ctx, args[1:], stdout, stderr, d)
		case "width":
			return runMachineWidth(ctx, args[1:], stdout, stderr, d)
		}
	}
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
	if laterKind(k) {
		if code, stale := behindSchema(ctx, st, stderr, verb, *pg); stale {
			return code
		}
	}
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
		// A row that is not there is added; a set the row refuses (a ref
		// naming no row, a rule across its fields) starts from the row.
		next := tool + " " + k.Name + " add " + name + " --<field> <value> ..."
		if k.Singleton {
			next = tool + " " + k.Name + " show"
			if errors.Is(err, config.ErrNoRef) {
				if remedy := singletonRefRemedy(k); remedy != "" {
					next = remedy
				}
			}
		} else if !errors.Is(err, config.ErrNotFound) {
			next = tool + " " + k.Name + " show " + name
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

// singletonRefRemedy uses the descriptor, not the store's human error text.
// A singleton whose ref fields all point to one kind can safely direct an
// ErrNoRef refusal to that kind's list, even when an unchanged field failed.
// A future singleton with mixed ref kinds keeps its existing generic remedy.
func singletonRefRemedy(k *config.Kind) string {
	ref := ""
	for _, f := range k.Fields {
		if f.Type != config.TypeRef {
			continue
		}
		if ref != "" && ref != f.Ref {
			return ""
		}
		ref = f.Ref
	}
	if ref == "" {
		return ""
	}
	return tool + " " + ref + " list"
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
	if laterKind(k) {
		if code, stale := behindSchema(ctx, st, stderr, verb, *pg); stale {
			return code
		}
	}
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
	rs, err := d.openRedis(ctx, addr)
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
	if laterKind(k) {
		if code, stale := behindSchema(ctx, st, stderr, verb, *pg); stale {
			return code
		}
	}
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
			return refuse(stderr, verb, err.Error())
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
	// machine show reads the loops table beside the machine row.
	if laterKind(k) || (k.Name == config.KindMachine && which == "show") {
		if code, stale := behindSchema(ctx, st, stderr, verb, *pg); stale {
			return code
		}
	}
	if which == "show" {
		row, found, err := st.Get(ctx, k.Name, name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		if !found {
			return refused(stderr, verb, k.Name+" "+name+" not found", tool+" "+k.Name+" list")
		}
		suffix := ""
		if k.Name == config.KindMachine {
			loops, err := machineLoops(ctx, st, name)
			if err != nil {
				return refuse(stderr, verb, err.Error())
			}
			suffix = " loops=" + config.Value(strings.Join(loops, ","))
		}
		if live(k) {
			bs, err := beats(ctx, liveRedisAddress(*redisFlag, d.getenv), []string{name}, d)
			if err != nil {
				return refuse(stderr, verb, err.Error())
			}
			suffix += liveSuffix(bs, name)
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

// laterKind is a kind whose table a later migration made (loops since
// version 6, routes since 7, tiers since 8): each of its verbs refuses on a store older
// than this binary's migrations (behindSchema), which does not have it.
func laterKind(k *config.Kind) bool {
	return k.Name == config.KindLoop || k.Name == config.KindRoute || k.Name == config.KindTier
}

// behindSchema is the refusal for a store whose schema is older than this
// binary's migrations: a table a newer kind reads (laterKind) is
// not there, and the store's own "relation does not exist" says nothing about
// the cause. It returns the exit code and true when it refused, and writes
// nothing and returns false when the store is at (or past) this binary's
// version. pg is the --pg flag, repeated in the command it names.
func behindSchema(ctx context.Context, st pgStore, stderr io.Writer, verb, pg string) (int, bool) {
	have, err := st.Version(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error()), true
	}
	return behindVersion(have, stderr, verb, pg)
}

// behindVersion is behindSchema for a version already read.
func behindVersion(have int, stderr io.Writer, verb, pg string) (int, bool) {
	all, err := config.Migrations()
	if err != nil || have >= len(all) {
		return 0, false
	}
	migrate := tool + " migrate"
	if pg != "" {
		migrate += " --pg " + shq(pg)
	}
	return refused(stderr, verb, fmt.Sprintf("schema config is at version %d and this binary carries %d", have, len(all)), migrate), true
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
	if code, stale := behindVersion(schema, stderr, verb, *pg); stale {
		return code
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
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer rs.Close()
	line += " redis=" + config.Value(addr)
	behind := 0
	for _, k := range config.Kinds {
		_, applied, err := rs.Read(ctx, k.Name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
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
	var actor string
	if !*check {
		var err error
		actor, err = actorName(*as, d.getenv)
		if err != nil {
			problems = append(problems, err.Error())
		}
	} else if *as != "" {
		actor = *as
	} else if v := d.getenv(envActor); v != "" {
		actor = v
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
	if code, stale := behindSchema(ctx, st, stderr, verb, *pg); stale {
		return code
	}
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
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

// localHost is the machine row this process runs on, which inventory marks
// ansible_connection=local. explicit is true when the env NOVA_MACHINE named
// it (matched by exact machine name, refused when no row has it); otherwise
// it is the lower-cased first label of the hostname (machine names are lower-case), matched the same way, and nothing is
// marked when no row has it.
func localHost(getenv func(string) string, hostname func() (string, error)) (name string, explicit bool) {
	if s := getenv(envMachine); s != "" {
		return s, true
	}
	if h, err := hostname(); err == nil {
		return strings.ToLower(strings.Split(h, ".")[0]), false
	}
	return "", false
}

// inventoryTimeout is --timeout's default, 10 s: how long inventory waits
// for the store in all, the connection and the two reads.
const inventoryTimeout = 10 * time.Second

// runInventory prints the Ansible inventory of the applied state: the
// Redis view apply wrote (machines, the fleet row, loops, the machines'
// beats), never Postgres, or a fixture file in its place (docs/FLEET.md).
func runInventory(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "inventory"
	fs := verbflag.New(verb)
	redisFlag := fs.String("redis", "", "Redis address host:port of the applied state (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); exclusive with --fixture")
	fixture := fs.String("fixture", "", "a YAML or JSON file of machines, the fleet row, loops and each machine's os and arch, read in place of the store (docs/FLEET.md, \"A fixture inventory\"); opens no store")
	list := fs.Bool("list", false, "print the whole inventory (hosts, groups and every host's variables under _meta.hostvars, so ansible never calls --host); the default when neither --list nor --host is given; exclusive with --host")
	host := fs.String("host", "", "print the variables of one machine as a JSON object; exits 1 when no machine row has that name")
	timeout := fs.Duration("timeout", inventoryTimeout, "a Go duration, above 0: how long to wait for the store before refusing; ansible runs the verb unattended, so it never waits forever")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	given := map[string]bool{}
	fs.Visit(func(f *stdflag.Flag) { given[f.Name] = true })
	var problems []string
	if fs.NArg() > 0 {
		problems = append(problems, "inventory takes no arguments; flags only")
	}
	if given["host"] && *host == "" {
		problems = append(problems, "--host wants a machine name and got an empty value")
	}
	if *list && given["host"] {
		problems = append(problems, "--list and --host are exclusive: --list prints every host, --host prints one")
	}
	if *timeout <= 0 {
		problems = append(problems, "--timeout wants a Go duration above 0, like 10s")
	}
	if given["fixture"] && *fixture == "" {
		problems = append(problems, "--fixture wants a file and got an empty value")
	}
	if *fixture != "" && *redisFlag != "" {
		problems = append(problems, "--fixture and --redis are exclusive: the fixture stands in for the store")
	}
	var addr string
	if *fixture == "" {
		var err error
		if addr, err = redisAddress(*redisFlag, d.getenv); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	// again is the command that repeats this run with every input kept.
	again := func(extra ...string) string {
		parts := []string{tool, verb}
		if *redisFlag != "" {
			parts = append(parts, "--redis", shq(*redisFlag))
		}
		if *fixture != "" {
			parts = append(parts, "--fixture", shq(*fixture))
		}
		if *list {
			parts = append(parts, "--list")
		}
		if given["host"] {
			parts = append(parts, "--host", shq(*host))
		}
		return strings.Join(append(parts, extra...), " ")
	}
	var snap *config.Snapshot
	if *fixture != "" {
		var err error
		if snap, err = config.LoadFixture(*fixture); err != nil {
			return refuse(stderr, verb, err.Error())
		}
	} else {
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		stage := "connecting"
		// fail is a store failure: the deadline, or the store's own words.
		fail := func(err error) int {
			if ctx.Err() != nil {
				fmt.Fprintf(stderr, "%s %s: timed out after %s waiting for the store at %s while %s; check that Redis answers there; run: %s\n", tool, verb, *timeout, addr, stage, again("--timeout", (*timeout*3).String()))
				return 2
			}
			return refuse(stderr, verb, err.Error())
		}
		rs, err := d.openRedis(ctx, addr)
		if err != nil {
			return fail(err)
		}
		defer rs.Close()
		stage = "reading the applied state"
		if snap, err = rs.Snapshot(ctx); err != nil {
			return fail(err)
		}
	}
	self, explicit := localHost(d.getenv, d.hostname)
	inv, err := config.BuildInventory(snap, self)
	if err != nil {
		return refused(stderr, verb, err.Error(), again())
	}
	if explicit && !inv.Has(self) {
		return refused(stderr, verb, fmt.Sprintf("%s=%q names no machine row (the name is matched exactly); known machines: %s", envMachine, self, boundedNames(inv.All.Hosts, maxKnownNames)), tool+" machine list")
	}
	if given["host"] {
		data, err := inv.HostJSON(*host)
		var unknown *config.UnknownHostError
		if errors.As(err, &unknown) {
			return refused(stderr, verb, fmt.Sprintf("--host %q names no machine row; known machines: %s", unknown.Name, boundedNames(unknown.Known, maxKnownNames)), tool+" machine list")
		}
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	data, err := inv.JSON()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// maxKnownNames bounds the machine names a refusal lists.
const maxKnownNames = 20

// boundedNames lists at most max names, then how many more there are, and
// "none" for an empty list.
func boundedNames(names []string, max int) string {
	if len(names) == 0 {
		return "none"
	}
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:max], ", "), len(names)-max)
}

// shq single-quotes a word so a printed remedy pastes.
func shq(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./-_") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
