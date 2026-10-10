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
// pkg/config, so every kind has identical flags, help and refusals; a
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
	"bytes"
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--actor <name>]
                    [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
                        [--timeout <duration>] [--example]
  nova-config <kind> add <name> --<field> <value> ... --actor <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --actor <name> [--dry-run] [--json]
  nova-config <kind> remove <name> --actor <name> [--dry-run] [--json]
  nova-config <kind> list [--json]
  nova-config <kind> show <name> [--json]
  nova-config <kind> history <name> [--json]
  nova-config machine width <name> [--json]
  nova-config machine self [--check] [--json]
  nova-config loop run <name> [--run-dir <dir>] [--metrics <dir>] [-- <command> ...]
                    the loop's command under its one lock: a second copy exits 3
  nova-config login --store <dir> --as <seat> --key <file> --secret <NAME>
                    --dsn <dsn> --actor <name> [--sops <path>]
                    records the DSN and where the password is; never the password
  nova-config login --check
                    prints that login and whether the secret resolves
  nova-config logout
                    removes the recorded login
  nova-config fleet set|show|history        one row each, no name:
                                            fleet and sprint have no add, remove or list
  nova-config sprint set|show|history
  nova-config <kind> <verb> -h              the verb's flags (required ones marked),
                                            its effect and a worked example

The store is --pg <dsn> (or NOVA_PG_DSN; the password is never on the line:
NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password,
NOVA_PG_PASSWORD when it is unset, and never the password itself), or --file
<path>, or --seat <name> (or NOVA_SEAT) which supplies the DSN and password
variable name from the seat profile (nova-sprint seat install writes it; the
password, when its variable is unset, is read in this process from the
nova-secrets seat nova-sprint seat login names), or the login nova-config login records
(the DSN and friend; the password is read in this process from nova-secrets,
never recorded and never put in an environment). --pg, NOVA_PG_DSN and
NOVA_PG_PASSWORD_ENV still win when given. --redis is host:port
(NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address). --actor is
the name a write is recorded under (NOVA_FRIEND, the recorded friend, or the
seat name); its old spelling --as works for one release.
Lose Redis: run nova-config apply.

Fleet apply and inventory require explicit redis_port and pg_dsn; set both
with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --actor <name>.

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
  nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --actor a1 --file try.json
  nova-config machine set m1 --width 6 --actor a1 --file try.json
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
	// Sessions is every other nova session holding the database: migrate
	// --window refuses while there is one.
	Sessions(ctx context.Context) ([]config.Session, error)
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
	// probe asks a nova program whether it still has a verb (config.HelpProbe):
	// loop add and set refuse, and status names, a loop whose verb is gone. Nil
	// asks nothing.
	probe config.VerbProbe
	// runLoop runs loop run's command (startLoop); nil runs none.
	runLoop runLoop
}

type redisApplier struct {
	*config.RedisApplier
	st *store.Store
}

func (r redisApplier) Close() error { return r.st.Close() }

func realDeps() deps {
	return deps{
		getenv: withLogin(os.Getenv, nil),
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
		probe:     config.HelpProbe,
		runLoop:   startLoop,
	}
}

// run is the entry: a verb asked for --json goes through runJSON, so a refusal
// is the one object on stdout (jsonRefusals); every other run prints the
// refusal to stderr (docs/STANDARD.md, "One output structure, two renderings").
func run(args []string, stdout, stderr io.Writer, d deps) int {
	if verbflag.BoolAsked(args, "json") {
		return runJSON(args, stdout, stderr, d)
	}
	return dispatch(args, stdout, stderr, d)
}

// runJSON runs a verb that asked for --json: a refusal the verb recorded is
// rendered as the one object on stdout at the exit the refusal carried, and a
// result the verb rendered itself (a FAILED status, help) is left as it stands.
func runJSON(args []string, stdout, stderr io.Writer, d deps) int {
	j := &jsonRefusals{}
	w := &written{w: stdout}
	code := dispatch(args, w, j, d)
	if code == 0 || w.n > 0 {
		// ignored: the result is already on stdout, so a note that did not reach stderr changes nothing
		_, _ = stderr.Write(j.said.Bytes())
		return code
	}
	j.out.Exit = code
	if j.out.Status == "" {
		j.out.Status = tool.Refused
	}
	for _, line := range strings.Split(strings.TrimSpace(j.said.String()), "\n") {
		if line != "" {
			j.out.Notes = append(j.out.Notes, line)
		}
	}
	j.out.Render(stdout, true)
	return code
}

// written counts what a verb wrote to stdout: runJSON leaves a result the verb
// rendered itself alone, and renders only a refusal nothing else printed.
type written struct {
	w io.Writer
	n int
}

func (c *written) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// dispatch is the verb walk, with every stream the caller handed in.
func dispatch(args []string, stdout, stderr io.Writer, d deps) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before anything is dialed or written (the CLI style's rule (b)),
	// with the verb's effect and worked example (verbExtra).
	defer verbflag.RecoverWith(stdout, toolName, banner(), &code, verbExtra)
	ctx := context.Background()
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; want kinds, migrate, status, apply, inventory, login, logout, or a kind ("+strings.Join(config.KindNames(), ", ")+") then add|set|remove|list|show|history")
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
	case "login", "logout":
		return runLoginTool(ctx, args, stdout, stderr, d)
	}
	if k, ok := config.Lookup(args[0]); ok {
		return runKind(ctx, k, args[1:], stdout, stderr, d)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %s; want kinds, migrate, status, apply, inventory, login, logout, or a kind (%s) then add|set|remove|list|show|history", oneline.Quote(args[0]), strings.Join(config.KindNames(), ", ")))
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
	return refuseLine(stderr, verb, plain(what)+next, 2)
}

// refused is the exit 1 line: the verb ran and the store or Redis said no.
// next names the command that resolves it.
func refused(stderr io.Writer, verb, what, next string) int {
	return refuseLine(stderr, verb, plain(what)+"; run: "+next, 1)
}

// refuseLine is one refusal whose text is already the part after "REFUSED: ":
// it prints on stderr, or, under --json, becomes the one result object every
// verb's --json is (jsonRefusals; docs/STANDARD.md, "One output structure, two
// renderings"). code is the run's exit.
func refuseLine(stderr io.Writer, verb, line string, code int) int {
	if j, ok := stderr.(*jsonRefusals); ok {
		why, remedy := line, ""
		if i := strings.LastIndex(line, "; run: "); i >= 0 {
			why, remedy = line[:i], line[i+len("; run: "):]
		}
		j.out.Verb = verb
		j.out.Status = tool.Refused
		j.out.Exit = code
		j.out.Why = append(j.out.Why, why)
		if j.out.Remedy == "" {
			j.out.Remedy = remedy
		}
		return code
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s\n", strings.TrimSpace(toolName+" "+verb), line)
	return code
}

// jsonRefusals stands in for stderr while a verb asked for --json runs: the
// refusals go into out, and anything else written to stderr into said.
type jsonRefusals struct {
	out  tool.Out
	said bytes.Buffer
}

func (j *jsonRefusals) Write(p []byte) (int, error) { return j.said.Write(p) }

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

// emit writes a verb's --json result: one object, pkg/tool's shape
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
// PostgreSQL, --file for a local JSON file in its place, --seat (or NOVA_SEAT)
// for a seat profile in seats.tsv supplying the DSN and password variable name,
// or the login nova-config login recorded when none of those is given.
type conn struct {
	pg   *string
	file *string
	seat *string
}

// storeFlags adds --pg and --file to a verb's flag set.
func storeFlags(fs *stdflag.FlagSet) conn {
	return conn{
		pg:   fs.String("pg", "", "the PostgreSQL `dsn`, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file"),
		file: fs.String("file", "", "a local JSON file standing in for PostgreSQL, at `path` (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store"),
	}
}

// seatStoreFlags adds --pg, --file and --seat to a verb's flag set: every verb
// that opens the store but machine add and loop add, whose rows have a seat field.
func seatStoreFlags(fs *stdflag.FlagSet) conn {
	c := storeFlags(fs)
	c.seat = fs.String("seat", "", "the `seat` profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file")
	return c
}

// dsn is the store to open: the file when --file is given, else the DSN by
// the rules every reader of nova-config shares (config.ResolveDSN), or from
// the selected seat profile.
func (c conn) dsn(getenv func(string) string) (string, error) {
	seat := ""
	if c.seat != nil && *c.seat != "" {
		seat = *c.seat
	} else if getenv != nil {
		seat = getenv(seatcred.SeatEnv)
	}
	switch {
	case c.file != nil && *c.file != "" && c.pg != nil && *c.pg != "":
		return "", errors.New("--pg and --file are exclusive: --file keeps the rows in a local file in PostgreSQL's place")
	case c.file != nil && *c.file != "" && seat != "":
		return "", errors.New("--seat and --file are exclusive: --file keeps the rows in a local file in PostgreSQL's place")
	case c.file != nil && *c.file != "":
		return filePrefix + *c.file, nil
	}
	if seat != "" {
		path, err := seatcred.ProfilePath(toolName, getenv)
		if err != nil {
			return "", err
		}
		prof, err := seatcred.LoadConfigProfile(path, seat)
		if err != nil {
			return "", err
		}
		if getenv == nil {
			getenv = func(string) string { return "" }
		}
		// NOVA_PG_PASSWORD_ENV still wins when given, as the help says: the
		// seat's secret is not read for it (resolvePG's own rule for the
		// recorded login), so a coordinator that names a set variable needs no
		// store login at all.
		envNames := getenv(config.EnvPGPassEnv) != ""
		var pw string
		var read bool
		if !envNames {
			var err error
			pw, read, err = seatPassword(prof, getenv) // the variable unset: from the store login's seat (seat_secret.go)
			if err != nil {
				return "", err
			}
		}
		lookup := func(k string) string {
			if k == config.EnvPGPassEnv && !envNames {
				return prof.PasswordEnv
			}
			if read && k == prof.PasswordEnv {
				return pw
			}
			return getenv(k)
		}
		dsn := ""
		if c.pg != nil && *c.pg != "" {
			dsn = *c.pg
		} else {
			dsn = prof.DSN
		}
		return config.ResolveDSN(dsn, lookup)
	}
	pg := ""
	if c.pg != nil {
		pg = *c.pg
	}
	return resolvePG(pg, getenv)
}

// again is the store flag a printed command repeats.
func (c conn) again() string {
	switch {
	case c.file != nil && *c.file != "":
		return " --file " + shq(*c.file)
	case c.pg != nil && *c.pg != "":
		return " --pg " + shq(*c.pg)
	case c.seat != nil && *c.seat != "":
		return " --seat " + shq(*c.seat)
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

// actorFlag adds --actor, the one name the family gives the name a write is
// recorded under (docs/STANDARD.md, section 2, "One shape across the set").
// --as is the old spelling, bound to the same value and kept for one release;
// actorAliasNote says when a run spelled it.
func actorFlag(fs *stdflag.FlagSet) *string {
	actor := fs.String("actor", "", "the `name` a write is recorded under in the history (env NOVA_FRIEND)")
	fs.StringVar(actor, "as", "", "the old spelling of --actor, kept for one release; it sets the same `name`")
	return actor
}

// actorAliasNote is the NOTE a run owes when it spelled the actor --as, the
// old name of --actor: the spelling works for one release and says so.
func actorAliasNote(fs *stdflag.FlagSet) string {
	alias := false
	fs.Visit(func(f *stdflag.Flag) {
		if f.Name == "as" {
			alias = true
		}
	})
	if alias {
		return "--as is --actor"
	}
	return ""
}

// jsonFlag adds --json.
func jsonFlag(fs *stdflag.FlagSet) *bool {
	return fs.Bool("json", false, "print one JSON object (pkg/tool's result shape) instead of the lines")
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

// actorName resolves --actor: the flag, else NOVA_FRIEND, else the seat name.
func actorName(flagValue string, getenv func(string) string, seatValues ...string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if getenv != nil {
		if v := getenv(envActor); v != "" {
			return v, nil
		}
	}
	for _, s := range seatValues {
		if s != "" {
			return s, nil
		}
	}
	if getenv != nil {
		if v := getenv(seatcred.SeatEnv); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("--actor is required: the name the write is recorded under (or %s)", envActor)
}

// --- kinds ------------------------------------------------------------------

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

// printNotes writes a result's notes, one NOTE line each.
func printNotes(stdout io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintln(stdout, "NOTE "+n)
	}
}

// --- migrate, status, apply -------------------------------------------------

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
