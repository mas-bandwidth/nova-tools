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
first run: the example: lines need no database and write only ./try.json; the fleet's store is
  export NOVA_PG_DSN=postgres://user@host:5432/db
then migrate.

usage:
  nova-config help [<verb>]
  nova-config version
  nova-config kinds [--json]
  nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]
  nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>]
                    [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
                        [--timeout <duration>]
  nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> remove <name> --as <name> [--dry-run] [--json]
  nova-config <kind> list [--json]
  nova-config <kind> show <name> [--json]
  nova-config <kind> history <name> [--json]
  nova-config machine width <name> [--json]
  nova-config machine self [--check] [--json]
  nova-config fleet set|show|history        one row each, no name:
                                            fleet and sprint have no add, remove or list
  nova-config sprint set|show|history
  nova-config <kind> <verb> -h              the verb's flags (required ones marked),
                                            its effect and a worked example

The store is --pg <dsn> (or NOVA_PG_DSN; the password is never on the line:
NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password,
NOVA_PG_PASSWORD when it is unset, and never the password itself), or --file
<path>. --redis is host:port (NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then
the seat's address). --as is the name a write is recorded under (NOVA_FRIEND).
Lose Redis: run nova-config apply.

Fleet apply and inventory require explicit redis_port and pg_dsn; set both
with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>.

exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer);
machine self: 2 not a row, 3 unreadable

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
// the kind is and its fields' names, the required ones first. Every line is
// wrapped at maxHelpCols with a continuation indent.
func kindsUsage() string {
	var b strings.Builder
	b.WriteString("kinds (nova-config <kind> add -h describes each field):\n")
	for _, k := range config.Kinds {
		b.WriteString(wrapHelp(fmt.Sprintf("  %-8s", k.Name), k.Doc, kindOffset))
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
		b.WriteString(wrapHelp(strings.Repeat(" ", kindOffset-1), line, kindOffset))
	}
	return b.String()
}

// maxHelpCols is the column the help wraps at: a line past it is a wall a
// reader must scroll sideways to finish (docs/STANDARD.md, section 3).
const maxHelpCols = 100

// kindOffset is the continuation indent for a `kinds` entry: two columns for
// the two-blank indent, the eight-column kind name and the blank after it.
const kindOffset = 11

// wrapHelp writes one wrapped help line: head opens the first line (an indent,
// and the kind's name padded for a kind's first line), body is the prose, and
// cont is the column the continuation lines align under. The wrap is at a
// blank on or before maxHelpCols, and never inside a word. The head and the
// continuation column are set by the caller so the wrapped prose still reads
// as the entry it belongs to.
func wrapHelp(head, body string, cont int) string {
	if body == "" {
		return head + "\n"
	}
	if len(head)+1+len(body) <= maxHelpCols {
		return head + " " + body + "\n"
	}
	var b strings.Builder
	b.WriteString(head + " ")
	col := len(head) + 1
	for i, word := range strings.Fields(body) {
		if i > 0 && col+1+len(word) > maxHelpCols {
			b.WriteString("\n" + strings.Repeat(" ", cont))
			col = cont
		} else if i > 0 {
			b.WriteByte(' ')
			col++
		}
		b.WriteString(word)
		col += len(word)
	}
	return b.String() + "\n"
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
		pg:   fs.String("pg", "", "the PostgreSQL `dsn`, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file"),
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
	if add && k.Name == config.KindMachine && row.Fields["width"] == "" {
		// width is set apart from slots and is the default when unset: say so where a newcomer meets it
		notes = append(notes, fmt.Sprintf("machine=%s width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: %s machine set %s --width <n> (0: no member) --as %s%s", config.Value(name), toolName, name, actor, c.again()))
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
		fmt.Fprintln(stdout, config.ListLine(k, row)+liveSuffix(bs, row.Name))
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
	if laterKind(k) {
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

// laterKind is a kind whose table, or a column of it the verbs read and
// write, a later migration made (loops since version 6, routes since 7, tiers
// since 8, the machine's width since 12, fleet endpoints since 14, the note of
// a route and a machine since 15): each of its verbs refuses on a store older
// than this binary's migrations (behindSchema), which does not have it.
func laterKind(k *config.Kind) bool {
	return k.Name == config.KindLoop || k.Name == config.KindRoute || k.Name == config.KindTier || k.Name == config.KindFleet || k.Name == config.KindMachine
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

// shq single-quotes a word so a printed remedy pastes.
func shq(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./-_") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
