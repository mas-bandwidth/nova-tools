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
// --file keeps the rows in a local JSON file in Postgres's place
// (config.FileStore), so every verb but apply's write runs with no database.
//
// Exit 0 done, 1 refused (the store or Redis said no: a duplicate, a missing
// row, a ceiling, a conflict), 2 usage (could not run: a flag, a store that
// did not answer), plus two for machine self: 2 when --check finds the name is
// no machine row, and 3 when the name or the rows could not be read. A refusal
// is one stderr line, `nova-config <verb> REFUSED: <what>; run: <next>`.
package main

import (
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
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
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

const toolName = "nova-config"

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

// usageTop is the banner (docs/STANDARD.md, section 3, point 6): what it
// does, how it works in five lines, the first run, then the usage. Each
// verb's flags, effect and worked example are in its own -h (verbExtra).
const usageTop = `nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: each kind (machine, fleet, friend, sprint, loop, route, tier) is a
table of rows in PostgreSQL's schema config, which migrate makes; every write
adds a history row naming who made it. apply copies the rows into Redis, the
view the fleet reads; inventory prints that view for Ansible. --file <path>
keeps the rows in a local JSON file instead, to try every verb with no database.
first run: the example: lines need no database and write only ./try.json; the fleet's store is export NOVA_PG_DSN=postgres://user@host:5432/db, then migrate.

usage:
  nova-config help [<verb>]
  nova-config version
  nova-config kinds [--json]
  nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]
  nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>] [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>] [--timeout <duration>]
  nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> remove <name> --as <name> [--dry-run] [--json]
  nova-config <kind> list [--json]
  nova-config <kind> show <name> [--json]
  nova-config <kind> history <name> [--json]
  nova-config machine width <name> [--json]
  nova-config machine self [--check] [--json]
  nova-config fleet set|show|history        one row each, no name: fleet and sprint have no add, remove or list
  nova-config sprint set|show|history
  nova-config <kind> <verb> -h              the verb's flags (required ones marked), its effect and a worked example

The store is --pg <dsn> (or NOVA_PG_DSN; never a password on the line: it is
read from the variable NOVA_PG_PASSWORD_ENV names, NOVA_PG_PASSWORD when
unset), or --file <path>. --redis is host:port (NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR, then the seat's address). --as is the name a write is
recorded under (NOVA_FRIEND). Lose Redis: run nova-config apply.

Fleet apply and inventory require explicit redis_port and pg_dsn; set both
with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>.

exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run: ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer); machine self: 2 not a row, 3 unreadable

`

// usageExamples ends the banner: a first run a stranger pastes, needing no
// database (docs/STANDARD.md, section 3, point 1). The heading's leading
// newline is what onboarding.ExampleHeading and the pasted-examples rule read.
const usageExamples = `
example:
  nova-config migrate --file try.json
  nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json
  nova-config machine set m1 --width 6 --as a1 --file try.json
  nova-config machine list --file try.json
  nova-config machine history m1 --file try.json
`

// kindsUsage is the per-kind part of the banner, from the descriptors: what
// the kind is and its fields' names, the required ones first.
func kindsUsage() string {
	var b strings.Builder
	b.WriteString("kinds (nova-config <kind> add -h describes each field):\n")
	for _, k := range config.Kinds {
		fmt.Fprintf(&b, "  %-8s %s\n", k.Name, k.Doc)
		var req, opt []string
		for _, f := range k.Fields {
			if f.Required {
				req = append(req, "--"+f.Name)
			} else {
				opt = append(opt, "--"+f.Name)
			}
		}
		line := ""
		if k.Singleton || len(k.Seed) > 0 {
			// rows migrate makes are set, never added
			line = "set takes "
		}
		if len(req) > 0 {
			line = "add needs " + strings.Join(req, " ")
			if len(opt) > 0 {
				line += "; also "
			}
		}
		if len(opt) > 0 {
			line += strings.Join(opt, " ")
		}
		fmt.Fprintf(&b, "  %-8s %s\n", "", line)
	}
	return b.String()
}

// banner is what help prints.
func banner() string { return usageTop + kindsUsage() + usageExamples }

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDeps())) }

// pgStore is the store a verb opens: config.Store plus the schema verbs.
type pgStore interface {
	config.Store
	Migrate(ctx context.Context) (from, to int, applied []int, err error)
	Version(ctx context.Context) (int, error)
	// Applied is the migration ledger, every version recorded, in order.
	Applied(ctx context.Context) ([]int, error)
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
// (or the --file) and the fleet Redis.
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
			if path, ok := strings.CutPrefix(dsn, filePrefix); ok {
				return config.OpenFile(path)
			}
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
	// before anything is dialed or written (the CLI style's rule (b)),
	// with the verb's effect and worked example (verbExtra).
	defer verbflag.RecoverWith(stdout, toolName, banner(), &code, verbExtra)
	ctx := context.Background()
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; want kinds, migrate, status, apply, inventory, or a kind ("+strings.Join(config.KindNames(), ", ")+") then add|set|remove|list|show|history")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return run(append(args[1:], "--help"), stdout, stderr, d)
		}
		fmt.Fprint(stdout, banner())
		return 0
	case "version", "--version":
		fs := verbflag.New("version")
		asJSON := jsonFlag(fs)
		if code, ok := parse(fs, args[1:], stderr, "version"); !ok {
			return code
		}
		if fs.NArg() > 0 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		if *asJSON {
			o := tool.Payload(buildinfo.Line(toolName, version))
			o.Verb = "version"
			return emit(stdout, o)
		}
		fmt.Fprintln(stdout, buildinfo.Line(toolName, version))
		return 0
	case "kinds":
		return runKinds(args[1:], stdout, stderr)
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
	return refuse(stderr, "", fmt.Sprintf("unknown verb %s; want kinds, migrate, status, apply, inventory, or a kind (%s) then add|set|remove|list|show|history", oneline.Quote(args[0]), strings.Join(config.KindNames(), ", ")))
}

// refuse is the exit 2 line: the invocation could not run (a flag, an input,
// a store that did not answer). It is one line in the one grammar,
// `nova-config[ <verb>] REFUSED: <what>; run: <the verb's help>`
// (docs/STANDARD.md, section 3, point 1).
func refuse(stderr io.Writer, verb, what string) int {
	next := "; run: " + helpFor(verb)
	if strings.Contains(what, "; run: ") {
		next = "" // the reason names its own next command (a --file migrate has not made)
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s%s\n", strings.TrimSpace(toolName+" "+verb), plain(what), next)
	return 2
}

// refused is the exit 1 line: the verb ran and the store or Redis said no.
// next names the command that resolves it.
func refused(stderr io.Writer, verb, what, next string) int {
	fmt.Fprintf(stderr, "%s %s REFUSED: %s; run: %s\n", toolName, verb, plain(what), next)
	return 1
}

// helpFor is the door a usage refusal names: the verb's own help, else the
// tool's.
func helpFor(verb string) string {
	if verb == "" || verb == "help" {
		return toolName + " help"
	}
	return toolName + " " + verb + " -h"
}

// plain is free text on one line as a reader reads it: every run of blanks,
// tabs and newlines (a library's among them) one blank, and any other control
// character escaped (oneline.Escape), never a hex escape for a blank.
func plain(s string) string { return oneline.Escape(strings.Join(strings.Fields(s), " ")) }

// parse parses a verb's flags, and is the refusal when they do not parse: an
// unknown flag names the flags the verb takes and the nearest one, never the
// flag package's stock line.
func parse(fs *stdflag.FlagSet, args []string, stderr io.Writer, verb string) (int, bool) {
	err := fs.Parse(args)
	if err == nil {
		return 0, true
	}
	return refuse(stderr, verb, parseErr(fs, err)), false
}

// parseErr is what a refusal says of a flag the parser refused.
func parseErr(fs *stdflag.FlagSet, err error) string {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
		return "--" + strings.TrimLeft(name, "-") + " wants a value"
	}
	name, unknown := strings.CutPrefix(msg, "flag provided but not defined: ")
	if !unknown {
		return msg
	}
	name = strings.TrimLeft(name, "-")
	var names []string
	fs.VisitAll(func(f *stdflag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	near, best := "", 0
	for _, n := range names {
		if s := commonPrefix(n, name); s > best {
			near, best = n, s
		}
	}
	text := "unknown flag --" + name
	if near != "" {
		text += " (nearest: --" + near + ")"
	}
	return text + "; this verb takes --" + strings.Join(names, ", --")
}

// commonPrefix is how many leading bytes a and b share.
func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// storeErr turns a store error into the right line: a refusal (exit 1) with
// its remedy, or a store that did not answer (exit 2).
func storeErr(stderr io.Writer, verb string, err error, next string) int {
	if config.Refused(err) {
		return refused(stderr, verb, err.Error(), next)
	}
	return refuse(stderr, verb, err.Error())
}

// emit writes a verb's --json result: one object, internal/tool's shape
// ({"result":{"verb","status","exit"},"facts":{},"items":[...],"notes":[]}).
func emit(stdout io.Writer, o *tool.Out) int {
	o.Render(stdout, true)
	return o.Exit
}

// --- connection flags -------------------------------------------------------

// filePrefix marks the store a --file names, in the one string openStore
// takes.
const filePrefix = "file:"

// conn is the store a verb opens, from its flags: --pg (or NOVA_PG_DSN) for
// PostgreSQL, --file for a local JSON file in its place.
type conn struct{ pg, file *string }

// storeFlags adds --pg and --file to a verb's flag set.
func storeFlags(fs *stdflag.FlagSet) conn {
	return conn{
		pg:   fs.String("pg", "", "the PostgreSQL `dsn`, postgres://user@host:port/db with no password (env NOVA_PG_DSN); the password comes from the variable NOVA_PG_PASSWORD_ENV names; exclusive with --file"),
		file: fs.String("file", "", "a local JSON file standing in for PostgreSQL, at `path` (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store"),
	}
}

// dsn is the store to open: the file when --file is given, else the DSN by
// the rules every reader of nova-config shares (config.ResolveDSN).
func (c conn) dsn(getenv func(string) string) (string, error) {
	switch {
	case *c.file != "" && *c.pg != "":
		return "", errors.New("--pg and --file are exclusive: --file keeps the rows in a local file in PostgreSQL's place")
	case *c.file != "":
		return filePrefix + *c.file, nil
	case *c.pg == "" && getenv(envPG) == "":
		return "", fmt.Errorf("--pg is required: postgres://user@host:5432/db (or %s), or --file <path> for a local file with no database", envPG)
	}
	return config.ResolveDSN(*c.pg, getenv)
}

// again is the store flag a printed command repeats.
func (c conn) again() string {
	switch {
	case *c.file != "":
		return " --file " + shq(*c.file)
	case *c.pg != "":
		return " --pg " + shq(*c.pg)
	}
	return ""
}

// where names the store on a result line: file=<path>, or pg=<the dsn
// without its password>.
func where(dsn string) (key, value string) {
	if p, ok := strings.CutPrefix(dsn, filePrefix); ok {
		return "file", p
	}
	return "pg", config.Redact(dsn)
}

// actorFlag adds --as.
func actorFlag(fs *stdflag.FlagSet) *string {
	return fs.String("as", "", "the `name` a write is recorded under in the history (env NOVA_FRIEND)")
}

// jsonFlag adds --json.
func jsonFlag(fs *stdflag.FlagSet) *bool {
	return fs.Bool("json", false, "print one JSON object (internal/tool's result shape) instead of the lines")
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
	return "", fmt.Errorf("--as is required: the name the write is recorded under (or %s)", envActor)
}

// --- kinds ------------------------------------------------------------------

func runKinds(args []string, stdout, stderr io.Writer) int {
	const verb = "kinds"
	fs := verbflag.New(verb)
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "kinds takes no arguments")
	}
	if *asJSON {
		o := tool.Done().Fact("count", len(config.Kinds))
		o.Verb = verb
		for _, k := range config.Kinds {
			var req []string
			for _, f := range k.Fields {
				if f.Required {
					req = append(req, f.Name)
				}
			}
			rows := "many"
			if k.Singleton {
				rows = "one"
			}
			o.Item("kind", "name", k.Name, "table", "config."+k.Table, "fields", k.FieldNames(), "required", req, "rows", rows, "doc", k.Doc)
		}
		return emit(stdout, o)
	}
	for _, k := range config.Kinds {
		fmt.Fprintln(stdout, config.KindLine(k))
	}
	fmt.Fprintf(stdout, "CONFIG KINDS count=%d\n", len(config.Kinds))
	return 0
}

// --- the kind verbs ---------------------------------------------------------

func runKind(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) > 0 {
		verbflag.HelpIfAsked(args[:1], k.Name)
	}
	want := "add, set, remove, list, show or history"
	switch {
	case k.Singleton:
		want = "set, show or history"
	case k.Name == config.KindMachine:
		want = "add, set, remove, list, show, history, width or self"
	}
	if len(args) == 0 {
		return refuse(stderr, k.Name, "want "+want)
	}
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
			return refuse(stderr, k.Name, k.Name+" is one row, created by migrate; want set, show or history")
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
	return refuse(stderr, k.Name, fmt.Sprintf("unknown verb %s; want %s", oneline.Quote(args[0]), want))
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

// typeWords is the value a field's flag wants, by its type, as its help
// names it (`--width <number>`).
var typeWords = map[config.Type]string{
	config.TypeText: "text", config.TypeInt: "number", config.TypeEnum: "word", config.TypeList: "list",
	config.TypeNames: "list", config.TypeRef: "name", config.TypeBool: "true|false", config.TypeKeys: "NAME,...",
	config.TypeArgv: "json", config.TypeSeq: "list", config.TypeDecimal: "decimal",
}

// fieldUsage is a field's flag help: what it wants (the backquoted word the
// help prints as the flag's value), whether add requires it, and its help.
func fieldUsage(f config.Field, add bool) string {
	word := typeWords[f.Type]
	if f.Type == config.TypeEnum {
		word = strings.Join(f.Enum, "|")
	}
	if word == "" {
		word = "value"
	}
	head := "`" + word + "`"
	if f.Type == config.TypeRef {
		head = "the `name` of a " + f.Ref + " row"
	}
	if add && f.Required {
		head = "required; " + head
	}
	return head + ": " + f.Help
}

func runKindWrite(ctx context.Context, k *config.Kind, add bool, args []string, stdout, stderr io.Writer, d deps) int {
	verb, op := k.Name+" set", config.OpSet
	if add {
		verb, op = k.Name+" add", config.OpAdd
	}
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	as := actorFlag(fs)
	dry := fs.Bool("dry-run", false, "print the change the write would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store")
	asJSON := jsonFlag(fs)
	values := map[string]*string{}
	for _, f := range k.Fields {
		values[f.Name] = fs.String(f.Name, "", fieldUsage(f, add))
	}
	name, rest := nameAndRest(k, args)
	if code, ok := parse(fs, rest, stderr, verb); !ok {
		return code
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
	dsn, err := c.dsn(d.getenv)
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
		row = config.Row{Name: name}
	}
	// Fleet endpoint checks need only the named fields, so malformed or
	// password-bearing DSNs refuse before a connection (docs/SPEC-CONFIG.md, "fleet").
	if err == nil && k.Name == config.KindFleet {
		err = k.Check(config.Row{Name: name, Fields: changes})
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
		if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
			return code
		}
	}
	// next is the command a refusal names: a row that is not there is added;
	// one there is shown, the start of a set it refused.
	next := toolName + " " + k.Name + " set " + name + " --<field> <value>"
	if !add {
		next = toolName + " " + k.Name + " add " + name + " --<field> <value> ..."
		if k.Singleton {
			next = toolName + " " + k.Name + " show"
		}
	}
	var notes []string
	width := row.Int("width")
	if add && k.Name == config.KindMachine && width == 0 {
		// width is set apart from slots and defaults to no member: say so where a newcomer meets it
		notes = append(notes, fmt.Sprintf("machine=%s width=0: no sprint member, so it is dealt no work; its width is set apart from its slots; run: %s machine set %s --width <n> --as %s%s", config.Value(name), toolName, name, actor, c.again()))
	}
	var id int64
	var changed []string
	if *dry {
		plan, err := config.PlanWrite(ctx, st, op, k.Name, row, changes)
		if err != nil {
			return storeErr(stderr, verb, err, writeRemedy(k, add, name, err, next)+c.again())
		}
		plan.Actor = actor
		if *asJSON {
			o := tool.Done().Fact("dry_run", true).Fact("op", plan.Op).Fact("kind", k.Name).Fact("name", name).Fact("before", plan.Before).Fact("after", plan.After)
			o.Verb, o.Notes = verb, notes
			return emit(stdout, o)
		}
		fmt.Fprintln(stdout, config.PlanLine(plan))
		printNotes(stdout, notes)
		return 0
	}
	if add {
		if id, err = st.Insert(ctx, k.Name, row, actor); err != nil {
			return storeErr(stderr, verb, err, writeRemedy(k, add, name, err, next)+c.again())
		}
	} else {
		if _, id, err = st.Update(ctx, k.Name, name, changes, actor); err != nil {
			return storeErr(stderr, verb, err, writeRemedy(k, add, name, err, next)+c.again())
		}
		changed = slices.Sorted(maps.Keys(changes))
	}
	if *asJSON {
		o := tool.Done().Fact("op", op).Fact("kind", k.Name).Fact("name", name).Fact("rev", id)
		if !add {
			o.Fact("changed", changed)
		}
		o.Verb, o.Notes = verb, notes
		return emit(stdout, o)
	}
	if add {
		fmt.Fprintf(stdout, "CONFIG ADD kind=%s name=%s rev=%d\n", k.Name, config.Value(name), id)
	} else {
		fmt.Fprintf(stdout, "CONFIG SET kind=%s name=%s rev=%d changed=%s\n", k.Name, config.Value(name), id, config.Value(strings.Join(changed, ",")))
	}
	printNotes(stdout, notes)
	return 0
}

// printNotes writes a result's notes, one NOTE line each.
func printNotes(stdout io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintln(stdout, "NOTE "+n)
	}
}

// writeRemedy is the command an add or set refusal names: for a ref naming
// no row, that kind's list; for a set the store refused because the row is
// there but its fields broke a rule, the row's show; else next (the set of a
// name taken, the add of a name missing, a singleton's show).
func writeRemedy(k *config.Kind, add bool, name string, err error, next string) string {
	if errors.Is(err, config.ErrNoRef) {
		if remedy := refRemedy(k); remedy != "" {
			return remedy
		}
	}
	switch {
	case add, k.Singleton, errors.Is(err, config.ErrNotFound):
		return next
	}
	return toolName + " " + k.Name + " show " + name
}

// refRemedy uses the descriptor, not the store's human error text. A kind
// whose ref fields all point to one kind can safely direct an ErrNoRef
// refusal to that kind's list, even when an unchanged field failed. A kind
// with mixed ref kinds keeps its generic remedy.
func refRemedy(k *config.Kind) string {
	ref := ""
	for _, f := range k.Fields {
		if f.Type != config.TypeRef && f.Type != config.TypeSeq {
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
	return toolName + " " + ref + " list"
}

func runKindRemove(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " remove"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	as := actorFlag(fs)
	dry := fs.Bool("dry-run", false, "print the change the remove would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store")
	asJSON := jsonFlag(fs)
	name, rest := nameAndRest(k, args)
	if code, ok := parse(fs, rest, stderr, verb); !ok {
		return code
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
	dsn, err := c.dsn(d.getenv)
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
		if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
			return code
		}
	}
	if *dry {
		plan, err := config.PlanWrite(ctx, st, config.OpRemove, k.Name, config.Row{Name: name}, nil)
		if err != nil {
			return storeErr(stderr, verb, err, toolName+" "+k.Name+" list"+c.again())
		}
		plan.Actor = actor
		if *asJSON {
			o := tool.Done().Fact("dry_run", true).Fact("op", plan.Op).Fact("kind", k.Name).Fact("name", name).Fact("before", plan.Before)
			o.Verb = verb
			return emit(stdout, o)
		}
		fmt.Fprintln(stdout, config.PlanLine(plan))
		return 0
	}
	id, err := st.Delete(ctx, k.Name, name, actor)
	if err != nil {
		return storeErr(stderr, verb, err, toolName+" "+k.Name+" list"+c.again())
	}
	if *asJSON {
		o := tool.Done().Fact("op", config.OpRemove).Fact("kind", k.Name).Fact("name", name).Fact("rev", id)
		o.Verb = verb
		return emit(stdout, o)
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
	return fs.String("redis", "", "the Redis `host:port` (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR); when given, each line ends in the machine's live measured facts from its beat")
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

// rowFields is a row as a JSON item's fields: its name, every field of the
// kind in declaration order, then any extra key=value pairs.
func rowFields(k *config.Kind, row config.Row, extra ...any) []any {
	kv := []any{"name", row.Name}
	for _, f := range k.Fields {
		kv = append(kv, f.Name, row.Fields[f.Name])
	}
	return append(kv, extra...)
}

// liveFields is a machine's beat as JSON item fields (none when no Redis was
// named; beat "none" for a machine with no beat).
func liveFields(bs map[string]*config.Beat, name string) []any {
	if bs == nil {
		return nil
	}
	b := bs[name]
	if b == nil {
		return []any{"beat", "none"}
	}
	return []any{"os", b.OS, "arch", b.Arch, "cores", b.Cores, "memory_gb", b.MemoryGB, "beat", b.At}
}

func runKindList(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " list"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	redisFlag := liveFlag(fs, k)
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "list takes no name; want "+verb)
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	if laterKind(k) {
		if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
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
	if *asJSON {
		o := tool.Done().Fact("kind", k.Name).Fact("rows", len(rows))
		o.Verb = verb
		for _, row := range rows {
			o.Item(k.Name, rowFields(k, row, liveFields(bs, row.Name)...)...)
		}
		return emit(stdout, o)
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
	c := storeFlags(fs)
	var redisFlag *string
	if which == "show" {
		redisFlag = liveFlag(fs, k)
	}
	asJSON := jsonFlag(fs)
	name, rest := nameAndRest(k, args)
	if code, ok := parse(fs, rest, stderr, verb); !ok {
		return code
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
	dsn, err := c.dsn(d.getenv)
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
		if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
			return code
		}
	}
	if which == "show" {
		return showRow(ctx, k, name, st, stdout, stderr, d, verb, *redisFlag, *asJSON, c.again())
	}
	changes, err := st.History(ctx, k.Name, name)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(changes) == 0 && !k.Singleton {
		return refused(stderr, verb, k.Name+" "+name+" has no history: it was never added", toolName+" "+k.Name+" list"+c.again())
	}
	if *asJSON {
		o := tool.Done().Fact("kind", k.Name).Fact("name", name).Fact("changes", len(changes))
		o.Verb = verb
		for _, ch := range changes {
			o.Item("change", "id", ch.ID, "op", ch.Op, "actor", ch.Actor, "at", ch.At, "before", ch.Before, "after", ch.After)
		}
		return emit(stdout, o)
	}
	for _, ch := range changes {
		fmt.Fprintln(stdout, config.HistoryLine(ch))
	}
	fmt.Fprintf(stdout, "CONFIG HISTORY kind=%s name=%s changes=%d\n", k.Name, config.Value(name), len(changes))
	return 0
}

// showRow is <kind> show: the row with its stamps; a machine's line names its
// loops (and its beat with a Redis), a loop's the command its unit runs.
func showRow(ctx context.Context, k *config.Kind, name string, st pgStore, stdout, stderr io.Writer, d deps, verb, redisAddr string, asJSON bool, again string) int {
	row, found, err := st.Get(ctx, k.Name, name)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if !found {
		return refused(stderr, verb, k.Name+" "+name+" not found", toolName+" "+k.Name+" list"+again)
	}
	suffix := ""
	extra := []any{"created", row.CreatedAt, "updated", row.UpdatedAt}
	if k.Name == config.KindMachine {
		loops, err := machineLoops(ctx, st, name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		suffix = " loops=" + config.Value(strings.Join(loops, ","))
		extra = append(extra, "loops", loops)
	}
	if k.Name == config.KindLoop {
		// the words the unit runs: the argv with the width field as its --width
		command, err := config.LoopCommandText(row)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		suffix = " command=" + config.Value(command)
		extra = append(extra, "command", config.LoopCommand(config.Argv(row.Fields["argv"]), row.Int("width")))
	}
	var bs map[string]*config.Beat
	if live(k) {
		if bs, err = beats(ctx, liveRedisAddress(redisAddr, d.getenv), []string{name}, d); err != nil {
			return refuse(stderr, verb, err.Error())
		}
		suffix += liveSuffix(bs, name)
		extra = append(extra, liveFields(bs, name)...)
	}
	if asJSON {
		o := tool.Done()
		o.Verb = verb
		o.Item(k.Name, rowFields(k, row, extra...)...)
		return emit(stdout, o)
	}
	fmt.Fprintln(stdout, config.ShowLine(k, row)+suffix)
	return 0
}

// --- migrate, status, apply -------------------------------------------------

func runMigrate(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "migrate"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	print := fs.Bool("print", false, "list the migrations this binary carries and connect to nothing")
	dry := fs.Bool("dry-run", false, "read the ledger (config.schema_migrations) and print every migration applied, pending (migrate applies it) or missing (below the greatest recorded, which migrate will not apply), and every table of schema config the role does not own, applying none; exit 0 when migrate would apply (ready=yes), 1 when it would refuse (ready=no)")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "migrate takes no arguments")
	}
	all, err := config.Migrations()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *print {
		if *asJSON {
			o := tool.Done().Fact("print", len(all))
			o.Verb = verb
			for _, m := range all {
				o.Item("migration", "version", m.Version, "file", m.Name, "lines", strings.Count(m.SQL, "\n"))
			}
			return emit(stdout, o)
		}
		for _, m := range all {
			fmt.Fprintf(stdout, "MIGRATION version=%d file=%s lines=%d\n", m.Version, config.Value(m.Name), strings.Count(m.SQL, "\n"))
		}
		fmt.Fprintf(stdout, "CONFIG MIGRATE print=%d pg=-\n", len(all))
		return 0
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	key, value := where(dsn)
	have, err := st.Version(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	owners, err := st.Ownership(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *dry {
		return migrateDryRun(ctx, st, all, owners, stdout, stderr, key, value, *asJSON)
	}
	gaps := config.MigrateGaps(owners, config.Pending(all, have))
	if len(gaps) > 0 {
		return refused(stderr, verb, ownershipWhy(owners.Role, config.Pending(all, have), gaps), ownershipRemedy(owners.Role, gaps))
	}
	from, to, applied, err := st.Migrate(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *asJSON {
		o := tool.Done().Fact(key, value).Fact("from", from).Fact("to", to).Fact("applied", len(applied)).Fact("versions", applied)
		o.Verb = verb
		return emit(stdout, o)
	}
	fmt.Fprintf(stdout, "CONFIG MIGRATE %s=%s from=%d to=%d applied=%d\n", key, config.Value(value), from, to, len(applied))
	return 0
}

// migrateDryRun is migrate --dry-run: the ledger read, every migration this
// binary carries with its state, and nothing applied. A migration is applied
// (its version is in the ledger), pending (above the greatest recorded:
// migrate applies it), or missing (not in the ledger and below the greatest:
// migrate, which applies only versions above the greatest, will not apply
// it, and a NOTE says so). Then the ownership finding: a MIGRATE NOT-OWNED
// line per gap the role has on schema config (config.Gaps) and, when migrate
// would refuse (config.MigrateGaps), a MIGRATE WOULD-REFUSE line with the
// refusal it would print. It exits 0 when migrate would apply (ready=yes)
// and 1 when it would refuse (ready=no), so a play or script gating on the
// dry run stops on it; nothing was attempted, so it prints no refusal line.
func migrateDryRun(ctx context.Context, st pgStore, all []config.Migration, owners config.Ownership, stdout, stderr io.Writer, key, value string, asJSON bool) int {
	const verb = "migrate"
	ledger, err := st.Applied(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	have := 0
	recorded := map[int]bool{}
	for _, v := range ledger {
		recorded[v] = true
		have = max(have, v)
	}
	o := tool.Done()
	o.Verb = verb
	var lines []string
	var missing []string
	var pending []config.Migration
	for _, m := range all {
		state := "applied"
		switch {
		case recorded[m.Version]:
		case m.Version > have:
			state = "pending"
			pending = append(pending, m)
		default:
			state = "missing"
			missing = append(missing, strconv.Itoa(m.Version))
		}
		o.Item("migration", "version", m.Version, "file", m.Name, "lines", strings.Count(m.SQL, "\n"), "state", state)
		lines = append(lines, fmt.Sprintf("MIGRATION version=%d file=%s lines=%d state=%s", m.Version, config.Value(m.Name), strings.Count(m.SQL, "\n"), state))
	}
	for _, g := range config.Gaps(owners) {
		o.Item("not_owned", "table", gapName(g), "owner", g.Owner, "role", owners.Role)
		lines = append(lines, fmt.Sprintf("MIGRATE NOT-OWNED table=%s owner=%s role=%s", config.Value(gapName(g)), config.Value(g.Owner), config.Value(owners.Role)))
	}
	// Readiness uses the same ledger as the rendered pending rows: another
	// migrate may advance it after Version (docs/SPEC-CONFIG.md, "The schema").
	gaps := config.MigrateGaps(owners, pending)
	ready := "yes"
	if len(gaps) > 0 {
		ready = "no"
		why, remedy := ownershipWhy(owners.Role, pending, gaps), ownershipRemedy(owners.Role, gaps)
		o.Status, o.Exit, o.Why, o.Remedy = tool.Failed, 1, []string{why}, remedy
		lines = append(lines, "MIGRATE WOULD-REFUSE "+plain(why)+"; run: "+remedy)
	}
	o.Fact(key, value).Fact("from", have).Fact("to", len(all)).Fact("applied", 0).Fact("dry_run", true).Fact("pending", len(pending)).Fact("missing", len(missing)).Fact("role", owners.Role).Fact("ready", ready)
	if len(missing) > 0 {
		o.Note(fmt.Sprintf("version(s) %s are not in the ledger and are below %d, the greatest recorded: migrate applies only versions above it, so it will not apply them", strings.Join(missing, ","), have))
	}
	if asJSON {
		return emit(stdout, o)
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	fmt.Fprintf(stdout, "CONFIG MIGRATE %s=%s from=%d to=%d applied=0 dry_run=true pending=%d missing=%d role=%s ready=%s\n", key, config.Value(value), have, len(all), len(pending), len(missing), config.Value(owners.Role), ready)
	printNotes(stdout, o.Notes)
	return o.Exit
}

// gapName is the gap's object as the lines name it.
func gapName(g config.Gap) string {
	if g.Table == "" {
		return "schema config"
	}
	return "config." + g.Table
}

// ownershipWhy is why migrate refuses: the role, the migrations it cannot
// apply, the rule, and each owner with what it holds.
func ownershipWhy(role string, pending []config.Migration, gaps []config.Gap) string {
	byOwner := map[string][]string{}
	var owners []string
	for _, g := range gaps {
		if byOwner[g.Owner] == nil {
			owners = append(owners, g.Owner)
		}
		byOwner[g.Owner] = append(byOwner[g.Owner], gapName(g))
	}
	held := make([]string, len(owners))
	for i, o := range owners {
		held[i] = o + " owns " + strings.Join(byOwner[o], ", ")
	}
	which := fmt.Sprintf("migration %d", pending[0].Version)
	if len(pending) > 1 {
		which = fmt.Sprintf("migrations %d to %d", pending[0].Version, pending[len(pending)-1].Version)
	}
	return fmt.Sprintf("role %s cannot apply %s and applied none: the role that runs migrate must own every table in schema config and be able to create in it, and %s, so a role with the owners' rights runs this once, in psql",
		role, which, strings.Join(held, ", and "))
}

// ownershipRemedy is the statements that close every gap, on one line:
// printed for a person to run, never run by migrate.
func ownershipRemedy(role string, gaps []config.Gap) string {
	lines := make([]string, len(gaps))
	for i, g := range gaps {
		lines[i] = g.Remedy(role)
	}
	return oneline.Escape(strings.Join(lines, " "))
}

// laterKind is a kind whose table a later migration made (loops since
// version 6, routes since 7, tiers since 8, fleet endpoints since 14): each
// of its verbs refuses on a store older
// than this binary's migrations (behindSchema), which does not have it.
func laterKind(k *config.Kind) bool {
	return k.Name == config.KindLoop || k.Name == config.KindRoute || k.Name == config.KindTier || k.Name == config.KindFleet
}

// behindSchema is the refusal for a store whose schema is older than this
// binary's migrations: a table a newer kind reads (laterKind) is
// not there, and the store's own "relation does not exist" says nothing about
// the cause. It returns the exit code and true when it refused, and writes
// nothing and returns false when the store is at (or past) this binary's
// version.
func behindSchema(ctx context.Context, st pgStore, stderr io.Writer, verb string, c conn) (int, bool) {
	have, err := st.Version(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error()), true
	}
	return behindVersion(have, stderr, verb, c)
}

// behindVersion is behindSchema for a version already read.
func behindVersion(have int, stderr io.Writer, verb string, c conn) (int, bool) {
	all, err := config.Migrations()
	if err != nil || have >= len(all) {
		return 0, false
	}
	return refused(stderr, verb, fmt.Sprintf("schema config is at version %d and this binary carries %d", have, len(all)), toolName+" migrate"+c.again()), true
}

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "status"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` apply writes (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); without one, status reads the store alone")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "status takes no arguments")
	}
	dsn, err := c.dsn(d.getenv)
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
	key, value := where(dsn)
	o := tool.Done().Fact(key, value).Fact("schema", schema)
	o.Verb = verb
	line := "CONFIG STATUS " + key + "=" + config.Value(value) + " schema=" + strconv.Itoa(schema)
	// finish prints the result (lines or JSON) and, for a refusal, its line.
	finish := func(code int, why, next string) int {
		if *asJSON {
			if code != 0 {
				o.Status, o.Exit, o.Why, o.Remedy = tool.Failed, code, []string{why}, next
			}
			emit(stdout, o)
		} else {
			fmt.Fprintln(stdout, line)
		}
		if code != 0 {
			return refused(stderr, verb, why, next)
		}
		return 0
	}
	if schema == 0 {
		o.Fact("redis", "")
		line += " redis=-"
		return finish(1, "schema config is not there yet", toolName+" migrate"+c.again())
	}
	if code, stale := behindVersion(schema, stderr, verb, c); stale {
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
			o.Fact(k.Name+"_rev", rev)
			continue
		}
		line += fmt.Sprintf(" %s=%d %s_rev=%d", k.Name, counts[k.Name], k.Name, rev)
		o.Fact(k.Name, counts[k.Name]).Fact(k.Name+"_rev", rev)
	}
	addr, addrErr := redisAddress(*redisFlag, d.getenv)
	if addrErr != nil {
		line += " redis=-"
		o.Fact("redis", "")
		return finish(0, "", "")
	}
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer rs.Close()
	line += " redis=" + config.Value(addr)
	o.Fact("redis", addr)
	behind := 0
	for _, k := range config.Kinds {
		_, applied, err := rs.Read(ctx, k.Name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		line += fmt.Sprintf(" %s_applied=%d", k.Name, applied)
		o.Fact(k.Name+"_applied", applied)
		if applied != revs[k.Name] {
			behind++
		}
	}
	if behind > 0 {
		return finish(1, fmt.Sprintf("Redis is not at the store's revision for %d kind(s)", behind), toolName+" apply"+c.again())
	}
	return finish(0, "", "")
}

func runApply(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "apply"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` to write (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	as := actorFlag(fs)
	kind := fs.String("kind", "", "one `kind` to apply ("+strings.Join(config.KindNames(), ", ")+"); every kind, in order, when unset")
	check := fs.Bool("check", false, "the same as --dry-run")
	dry := fs.Bool("dry-run", false, "print the ADD, SET and REMOVE lines (CHECK ...) and write nothing; it still reads the store and Redis")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	*check = *check || *dry
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
	dsn, err := c.dsn(d.getenv)
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
	if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
		return code
	}
	// The fleet lives in the authoritative store. Check its endpoints before
	// connecting to Redis or applying any kind (docs/SPEC-CONFIG.md, "Apply").
	if *kind == "" || *kind == config.KindFleet {
		fleet, _, err := st.Get(ctx, config.KindFleet, config.KindFleet)
		if err != nil {
			return storeErr(stderr, verb, err, toolName+" apply --check")
		}
		if err := config.ValidateFleetEndpoints(config.View(fleet.Fields)); err != nil {
			what, next, _ := strings.Cut(err.Error(), "; run: ")
			return refused(stderr, verb, what, next)
		}
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
	o := tool.Done().Fact("dry_run", *check)
	o.Verb = verb
	for _, kn := range kinds {
		start := d.now()
		res, err := config.Apply(ctx, st, rs, kn, actor, *check, func(op config.Op) {
			if *asJSON {
				o.Item("op", "kind", kn, "op", op.Op, "name", op.Name, "changed", op.Changed)
				return
			}
			fmt.Fprintln(stdout, config.OpLine(word, kn, op))
		})
		if err != nil {
			if config.IsConflict(err) {
				return refused(stderr, verb, err.Error(), toolName+" status (then apply from the store that is ahead)")
			}
			return storeErr(stderr, verb, err, toolName+" apply --dry-run")
		}
		if *asJSON {
			o.Item("kind", "kind", kn, "add", res.Add, "set", res.Set, "remove", res.Remove, "rev", res.Rev, "applied", res.RedisRev)
			continue
		}
		if *check {
			fmt.Fprintf(stdout, "CONFIG CHECK kind=%s add=%d set=%d remove=%d rev=%d applied=%d\n", kn, res.Add, res.Set, res.Remove, res.Rev, res.RedisRev)
			continue
		}
		fmt.Fprintf(stdout, "CONFIG APPLY kind=%s add=%d set=%d remove=%d rev=%d ms=%d\n", kn, res.Add, res.Set, res.Remove, res.Rev, d.now().Sub(start).Milliseconds())
	}
	if *asJSON {
		return emit(stdout, o)
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
	redisFlag := fs.String("redis", "", "the Redis `host:port` of the applied state (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); exclusive with --fixture")
	fixture := fs.String("fixture", "", "a YAML or JSON `file` of machines, the fleet row, loops and each machine's os and arch, read in place of the store (docs/FLEET.md, \"A fixture inventory\"); opens no store")
	list := fs.Bool("list", false, "print the whole inventory (hosts, groups and every host's variables under _meta.hostvars, so ansible never calls --host); the default when neither --list nor --host is given; exclusive with --host")
	host := fs.String("host", "", "print the variables of one machine, by `name`, as a JSON object; exits 1 when no machine row has that name")
	timeout := fs.Duration("timeout", inventoryTimeout, "a Go `duration`, above 0: how long to wait for the store before refusing; ansible runs the verb unattended, so it never waits forever")
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
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
		parts := []string{toolName, verb}
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
				fmt.Fprintf(stderr, "%s %s REFUSED: timed out after %s waiting for the store at %s while %s; check that Redis answers there; run: %s\n", toolName, verb, *timeout, addr, stage, again("--timeout", (*timeout*3).String()))
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
		if what, next, has := strings.Cut(err.Error(), "; run: "); has && strings.HasPrefix(what, "fleet:") {
			return refused(stderr, verb, what, next)
		}
		return refused(stderr, verb, err.Error(), again())
	}
	if explicit && !inv.Has(self) {
		return refused(stderr, verb, fmt.Sprintf("%s=%q names no machine row (the name is matched exactly); known machines: %s", envMachine, self, boundedNames(inv.All.Hosts, maxKnownNames)), toolName+" machine list")
	}
	if given["host"] {
		data, err := inv.HostJSON(*host)
		var unknown *config.UnknownHostError
		if errors.As(err, &unknown) {
			return refused(stderr, verb, fmt.Sprintf("--host %q names no machine row; known machines: %s", unknown.Name, boundedNames(unknown.Known, maxKnownNames)), toolName+" machine list")
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
