// Command nova-config is the one tool for the fleet's permanent,
// non-ephemeral configuration: it owns Postgres (schema `config`, its
// migrations, its history) and every registry of the fleet, and it applies
// that configuration into Redis so Redis is always a rebuildable copy. The
// runtime tools read configuration and never write it. The contract is
// docs/SPEC-CONFIG.md; the guide is docs/nova-config/README.md.
//
// Every kind has the same verbs, built from its descriptor in internal/config.
// The dispatch, the banner, the help, the version verb, the refusals and the
// output envelope are internal/tool's (docs/STANDARD.md, "When building a tool").
// A row name is still given as a positional (`machine add m1`); the skeleton
// takes flags only, so main lifts that one word to --name before Tool.Main.
// inventory and machine self keep their own printers: one writes the Ansible
// document a program reads, the other the bare machine name.
//
// Exit 0 done, 1 the verb ran and the store or Redis said no, 2 usage (could
// not run: a flag, a store that did not answer), plus two for machine self:
// 2 when --check finds the name is no machine row, and 3 when the name or the
// rows could not be read.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

const toolName = "nova-config"

const (
	envPG          = config.EnvPG
	envPGPassEnv   = config.EnvPGPassEnv
	defaultPassEnv = config.DefaultPassEnv
	envSprintRedis = "NOVA_SPRINT_REDIS"
	envRedisAddr   = "NOVA_REDIS_ADDR"
	envActor       = "NOVA_FRIEND"
	envMachine     = "NOVA_MACHINE"
)

const filePrefix = "file:"

func main() {
	os.Args = append([]string{os.Args[0]}, liftName(os.Args[1:])...)
	os.Exit(configTool(realDeps()).Main())
}

func run(args []string, stdout, stderr io.Writer, d deps) int {
	return configTool(d).Run(liftName(args), strings.NewReader(""), stdout, stderr)
}

// configTool is the command (docs/STANDARD.md section 2: one Tool, its verbs,
// one result rendered as lines or as JSON of the same value).
func configTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:  toolName,
		What:  "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis",
		Stamp: version,
		How: `each kind (machine, fleet, friend, sprint, loop, route, tier) is a table
of rows in PostgreSQL's schema config; migrate makes it, and every write
adds a history row naming who made it. apply copies the rows into Redis,
the view the fleet reads. inventory prints that view for Ansible. --file
keeps the rows in a local JSON file, so the examples need no database.`,
		ExitTable: "0 done, 1 refused (the verb ran and the store said no; migrate --dry-run: ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer); machine self: 2 not a row, 3 unreadable",
		Verbs:     configVerbs(d),
	}
}

var bannerExample = map[string]string{
	"migrate":         "migrate --file try.json",
	"machine add":     "machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json",
	"machine set":     "machine set m1 --width 6 --as a1 --file try.json",
	"machine list":    "machine list --file try.json",
	"machine history": "machine history m1 --file try.json",
}

func configVerbs(d deps) []tool.Verb {
	vs := []tool.Verb{
		{Name: "kinds", Usage: "kinds", Effect: effectBinary, Detail: detailFrom("kinds"), Flags: func(f *tool.Flags) { f.Max() }, Run: func(c *tool.Call) *tool.Out { return runKinds(c) }},
		{Name: "migrate", Usage: "migrate [--pg <dsn> | --file <path>] [--print] [--dry-run]", Example: bannerExample["migrate"], Effect: effectMigrate, Detail: detailFrom("migrate"), DryRun: true, Flags: func(f *tool.Flags) {
			storeFlags(f)
			f.Bool("print", false, "list the migrations this binary carries and connect to nothing")
		}, Run: func(c *tool.Call) *tool.Out { return runMigrate(c, d) }},
		{Name: "status", Usage: "status [--pg <dsn> | --file <path>] [--redis <addr>]", Effect: effectStatus, Detail: detailFrom("status"), Flags: func(f *tool.Flags) {
			storeFlags(f)
			f.String("redis", "", "the Redis host:port apply writes (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); without one, status reads the store alone")
			f.Check(func(c *tool.Call) { wantStore(c, d) })
		}, Run: func(c *tool.Call) *tool.Out { return runStatus(c, d) }},
		{Name: "apply", Usage: "apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>] [--kind <kind>] [--dry-run]", Effect: effectApply, Detail: detailFrom("apply"), DryRun: true, Flags: func(f *tool.Flags) {
			storeFlags(f)
			f.String("redis", "", "the Redis host:port to write (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
			f.String("as", "", "the name a write is recorded under (env NOVA_FRIEND)")
			f.String("kind", "", "one kind to apply ("+strings.Join(config.KindNames(), ", ")+"); every kind, in order, when unset")
			f.Bool("check", false, "the same as --dry-run")
			f.Check(func(c *tool.Call) { wantApply(c, d) })
		}, Run: func(c *tool.Call) *tool.Out { return runApply(c, d) }},
		{Name: "inventory", Usage: "inventory [--redis <addr> | --fixture <file>] [--list | --host <name>] [--timeout <duration>]", Effect: effectInventory, Detail: detailFrom("inventory"), Flags: func(f *tool.Flags) {
			f.Prints()
			f.String("redis", "", "the Redis host:port of the applied state (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); exclusive with --fixture")
			f.String("fixture", "", "a YAML or JSON file of machines, the fleet row, loops and each machine's os and arch, read in place of the store; opens no store")
			f.Bool("list", false, "print the whole inventory (hosts, groups and every host's variables under _meta.hostvars, so ansible never calls --host); the default when neither --list nor --host is given; exclusive with --host")
			f.String("host", "", "print the variables of one machine, by name, as a JSON object")
			f.Duration("timeout", inventoryTimeout, "a Go duration, above 0: how long to wait for the store before refusing")
			f.Check(func(c *tool.Call) { wantInventory(c, d) })
		}, Run: func(c *tool.Call) *tool.Out { return runInventory(c, d) }},
	}
	for _, k := range config.Kinds {
		vs = append(vs, kindVerbs(k, d)...)
	}
	return vs
}

func kindVerbs(k *config.Kind, d deps) []tool.Verb {
	var vs []tool.Verb
	for _, verb := range []string{"add", "set", "remove", "list", "show", "history"} {
		if k.Singleton && (verb == "add" || verb == "remove" || verb == "list") {
			continue
		}
		vs = append(vs, kindVerb(k, verb, d))
	}
	if k.Name == config.KindMachine {
		vs = append(vs,
			tool.Verb{Name: "machine width", Usage: "machine width <name> [--pg <dsn> | --file <path>]", Effect: effectInspect, Detail: detailFrom("machine width"), Flags: func(f *tool.Flags) {
				storeFlags(f)
				f.Required("name", "the machine row's name")
				f.Check(func(c *tool.Call) { wantStore(c, d) })
			}, Run: func(c *tool.Call) *tool.Out { return runMachineWidth(c, d) }},
			tool.Verb{Name: "machine self", Usage: "machine self [--check]", Effect: "inspection: prints this machine's name and opens no store; --check reads the machine rows", Detail: detailFrom("machine self"), Flags: func(f *tool.Flags) {
				f.Prints()
				storeFlags(f)
				f.Bool("check", false, "read the machine rows and exit 2 when this machine's name is none of them (exit 3 when the rows cannot be read); without it no store is opened")
				f.Bool("json", false, "print one JSON object instead of the name")
			}, Run: func(c *tool.Call) *tool.Out { return runMachineSelf(c, d) }},
		)
	}
	return vs
}

func kindVerb(k *config.Kind, verb string, d deps) tool.Verb {
	name := k.Name + " " + verb
	write := verb == "add" || verb == "set" || verb == "remove"
	effect := tool.Effect(effectInspect)
	if write {
		effect = effectStoreRow
	}
	return tool.Verb{
		Name: name, Usage: kindUsage(k, verb), Example: bannerExample[name], Effect: effect, Detail: detailFrom(name), DryRun: write,
		Flags: func(f *tool.Flags) { kindFlags(f, k, verb, d) },
		Run:   func(c *tool.Call) *tool.Out { return runKind(c, k, verb, d) },
	}
}

func kindUsage(k *config.Kind, verb string) string {
	var b strings.Builder
	b.WriteString(k.Name + " " + verb)
	if !k.Singleton && verb != "list" {
		b.WriteString(" <name>")
	}
	if verb == "add" || verb == "set" {
		for _, f := range k.Fields {
			b.WriteString(" --" + f.Name)
		}
		b.WriteString(" --as <name>")
	} else if verb == "remove" {
		b.WriteString(" --as <name>")
	}
	return b.String()
}

func kindFlags(f *tool.Flags, k *config.Kind, verb string, d deps) {
	storeFlags(f)
	write := verb == "add" || verb == "set" || verb == "remove"
	if write {
		f.String("as", "", "the name a write is recorded under in the history (env NOVA_FRIEND)")
	}
	if k.Singleton {
		f.String("name", "", "not taken: "+k.Name+" is one row")
	} else if verb != "list" {
		f.Required("name", "the row's name")
	}
	if verb == "add" || verb == "set" {
		for _, field := range k.Fields {
			if verb == "add" && field.Required {
				f.Required(field.Name, fieldWants(field))
				continue
			}
			f.String(field.Name, "", fieldUsage(field, false))
		}
	}
	if verb == "list" || verb == "show" || verb == "history" {
		f.Max()
	}
	if (verb == "list" || verb == "show") && live(k) {
		f.String("redis", "", "the Redis host:port (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR); when given, each line carries the machine's live measured facts from its beat")
	}
	f.Check(func(c *tool.Call) { wantKind(c, k, verb, d) })
}

func wantKind(c *tool.Call, k *config.Kind, verb string, d deps) {
	var problems []string
	if k.Singleton && c.Given("name") {
		problems = append(problems, k.Name+" takes no name: it is one row; want "+k.Name+" "+verb)
	}
	if verb == "set" {
		any := false
		for _, field := range k.Fields {
			if c.Given(field.Name) {
				any = true
			}
		}
		if !any {
			problems = append(problems, "set names no field")
		}
	}
	if verb == "add" {
		if _, err := k.NewRow(rowName(c, k), givenFields(c, k)); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if verb == "add" || verb == "set" || verb == "remove" {
		if _, err := actorName(c.Str("as"), d.getenv); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if _, err := (conn{c.Str("pg"), c.Str("file")}).dsn(d.getenv); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		c.Problem(strings.Join(problems, "; "))
	}
}

func wantStore(c *tool.Call, d deps) {
	if _, err := (conn{c.Str("pg"), c.Str("file")}).dsn(d.getenv); err != nil {
		c.Problem(err.Error())
	}
}

func wantApply(c *tool.Call, d deps) {
	var problems []string
	if c.Str("kind") != "" {
		if _, ok := config.Lookup(c.Str("kind")); !ok {
			problems = append(problems, fmt.Sprintf("--kind %s: want one of %s", c.Str("kind"), strings.Join(config.KindNames(), ", ")))
		}
	}
	dry := c.Bool("check") || (c.Given("dry-run") && c.Bool("dry-run"))
	if !dry {
		if _, err := actorName(c.Str("as"), d.getenv); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if _, err := (conn{c.Str("pg"), c.Str("file")}).dsn(d.getenv); err != nil {
		problems = append(problems, err.Error())
	}
	if _, err := redisAddress(c.Str("redis"), d.getenv); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		c.Problem(strings.Join(problems, "; "))
	}
}

func detailFrom(verb string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(verbExtra(verb), "\n"), "\n") {
		if strings.HasPrefix(l, "effect: ") {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.Trim(b.String(), "\n")
}

// liftName moves a verb's row name, given as a positional, to --name. The
// skeleton refuses a positional on every verb but the default, and this
// grammar's name is a positional (docs/SPEC-CONFIG.md, the six verbs).
func liftName(args []string) []string {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || args[0] == "help" {
		return args
	}
	k, ok := config.Lookup(args[0])
	if !ok || strings.HasPrefix(args[1], "-") || !nameVerb(k, args[1]) {
		return args
	}
	pos, flags := splitArgs(args[2:])
	if len(pos) != 1 {
		return args
	}
	return append([]string{args[0], args[1], "--name", pos[0]}, flags...)
}

func nameVerb(k *config.Kind, verb string) bool {
	switch verb {
	case "set", "show", "history":
		return true
	case "add", "remove":
		return !k.Singleton
	case "width":
		return k.Name == config.KindMachine
	}
	return false
}

func splitArgs(args []string) (pos, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			return pos, flags
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if inline || boolFlag[name] {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return pos, flags
}

var boolFlag = map[string]bool{"json": true, "dry-run": true, "print": true, "check": true, "list": true, "h": true, "help": true}

type pgStore interface {
	config.Store
	Migrate(ctx context.Context) (from, to int, applied []int, err error)
	Version(ctx context.Context) (int, error)
	Applied(ctx context.Context) ([]int, error)
	Close() error
}

type redisSide interface {
	config.Applier
	config.BeatReader
	Snapshot(ctx context.Context) (*config.Snapshot, error)
	Close() error
}

type deps struct {
	getenv    func(string) string
	openStore func(ctx context.Context, dsn string) (pgStore, error)
	openRedis func(ctx context.Context, addr string) (redisSide, error)
	now       func() time.Time
	hostname  func() (string, error)
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
		now: time.Now, hostname: os.Hostname, tailscale: config.TailscaleStatus,
	}
}

type conn struct{ pg, file string }

func storeFlags(f *tool.Flags) {
	f.String("pg", "", "the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); the password comes from the variable NOVA_PG_PASSWORD_ENV names; exclusive with --file")
	f.String("file", "", "a local JSON file standing in for PostgreSQL (migrate --file <path> makes it); never the fleet's store")
}

func (c conn) dsn(getenv func(string) string) (string, error) {
	switch {
	case c.file != "" && c.pg != "":
		return "", errors.New("--pg and --file are exclusive: --file keeps the rows in a local file in PostgreSQL's place")
	case c.file != "":
		return filePrefix + c.file, nil
	case c.pg == "" && getenv(envPG) == "":
		return "", fmt.Errorf("--pg is required: postgres://user@host:5432/db (or %s), or --file <path> for a local file with no database", envPG)
	}
	return config.ResolveDSN(c.pg, getenv)
}

func again(c *tool.Call) string {
	switch {
	case c.Str("file") != "":
		return " --file " + shq(c.Str("file"))
	case c.Str("pg") != "":
		return " --pg " + shq(c.Str("pg"))
	}
	return ""
}

func where(dsn string) (key, value string) {
	if p, ok := strings.CutPrefix(dsn, filePrefix); ok {
		return "file", p
	}
	return "pg", config.Redact(dsn)
}

func redisAddress(flagValue string, getenv func(string) string) (string, error) {
	for _, v := range []string{flagValue, getenv(envSprintRedis), getenv(envRedisAddr), seatcred.Process().Addr()} {
		if v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("--redis is required: host:port (or %s, %s, or a seat)", envSprintRedis, envRedisAddr)
}

func liveRedisAddress(flagValue string, getenv func(string) string) string {
	for _, v := range []string{flagValue, getenv(envSprintRedis), getenv(envRedisAddr)} {
		if v != "" {
			return v
		}
	}
	return ""
}

func actorName(flagValue string, getenv func(string) string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v := getenv(envActor); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("--as is required: the name the write is recorded under (or %s)", envActor)
}

func plain(s string) string { return oneline.Escape(strings.Join(strings.Fields(s), " ")) }

func shq(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./-_") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func openStore(ctx context.Context, c *tool.Call, d deps) (pgStore, string, *tool.Out) {
	dsn, err := (conn{c.Str("pg"), c.Str("file")}).dsn(d.getenv)
	if err != nil {
		return nil, "", tool.Refuse(err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return nil, "", tool.Refuse(err.Error())
	}
	return st, dsn, nil
}

func storeNo(err error, next string) *tool.Out {
	if config.Refused(err) {
		o := tool.Fail(plain(err.Error()))
		o.Remedy = next
		return o
	}
	return tool.Refuse(plain(err.Error()))
}

func versionBehind(have int, c *tool.Call) *tool.Out {
	all, err := config.Migrations()
	if err != nil || have >= len(all) {
		return nil
	}
	o := tool.Fail(fmt.Sprintf("schema config is at version %d and this binary carries %d", have, len(all)))
	o.Remedy = toolName + " migrate" + again(c)
	return o
}

func schemaBehind(ctx context.Context, st pgStore, c *tool.Call) *tool.Out {
	have, err := st.Version(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	return versionBehind(have, c)
}

func laterKind(k *config.Kind) bool {
	return k.Name == config.KindLoop || k.Name == config.KindRoute || k.Name == config.KindTier || k.Name == config.KindFleet
}

var typeWords = map[config.Type]string{
	config.TypeText: "text", config.TypeInt: "number", config.TypeEnum: "word", config.TypeList: "list",
	config.TypeNames: "list", config.TypeRef: "name", config.TypeBool: "true|false", config.TypeKeys: "NAME,...",
	config.TypeArgv: "json", config.TypeSeq: "list", config.TypeDecimal: "decimal",
}

func fieldWants(f config.Field) string {
	word := typeWords[f.Type]
	if f.Type == config.TypeEnum {
		word = strings.Join(f.Enum, "|")
	}
	if word == "" {
		word = "value"
	}
	if f.Type == config.TypeRef {
		return "the name of a " + f.Ref + " row: " + f.Help
	}
	return word + ": " + f.Help
}

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

func runKinds(c *tool.Call) *tool.Out {
	o := tool.Done().Fact("count", len(config.Kinds))
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
		o.Item("kind", "name", k.Name, "table", "config."+k.Table, "fields", strings.Join(k.FieldNames(), ","), "required", strings.Join(req, ","), "rows", rows, "doc", k.Doc)
	}
	return o
}

func runKind(c *tool.Call, k *config.Kind, verb string, d deps) *tool.Out {
	ctx := context.Background()
	switch verb {
	case "add", "set":
		return runKindWrite(ctx, c, k, verb == "add", d)
	case "remove":
		return runKindRemove(ctx, c, k, d)
	case "list":
		return runKindList(ctx, c, k, d)
	case "show", "history":
		return runKindRead(ctx, c, k, verb, d)
	}
	return tool.Refuse("unknown verb " + verb)
}

func givenFields(c *tool.Call, k *config.Kind) map[string]string {
	given := map[string]string{}
	for _, f := range k.Fields {
		if c.Given(f.Name) {
			given[f.Name] = c.Str(f.Name)
		}
	}
	return given
}

func rowName(c *tool.Call, k *config.Kind) string {
	if k.Singleton {
		return k.Name
	}
	return c.Str("name")
}

func runKindWrite(ctx context.Context, c *tool.Call, k *config.Kind, add bool, d deps) *tool.Out {
	dry := c.DryRun()
	op := config.OpSet
	if add {
		op = config.OpAdd
	}
	name := rowName(c, k)
	actor, _ := actorName(c.Str("as"), d.getenv)
	given := givenFields(c, k)
	var problems []string
	var row config.Row
	var changes map[string]string
	var err error
	if add {
		row, err = k.NewRow(name, given)
	} else {
		if err = config.ValidateName(name); err == nil {
			changes, err = k.Changes(given)
		}
		row = config.Row{Name: name}
	}
	if err == nil && k.Name == config.KindFleet {
		err = k.Check(config.Row{Name: name, Fields: changes})
	}
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		o := tool.Refuse(problems...)
		o.Remedy = toolName + " " + k.Name + " " + map[bool]string{true: "add", false: "set"}[add] + " -h"
		return o
	}
	st, dsn, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	if laterKind(k) {
		if behind := schemaBehind(ctx, st, c); behind != nil {
			return behind
		}
	}
	next := toolName + " " + k.Name + " set " + name + " --<field> <value>"
	if !add {
		next = toolName + " " + k.Name + " add " + name + " --<field> <value> ..."
		if k.Singleton {
			next = toolName + " " + k.Name + " show"
		}
	}
	var notes []string
	if add && k.Name == config.KindMachine && row.Int("width") == 0 {
		notes = append(notes, fmt.Sprintf("machine=%s width=0: no sprint member, so it is dealt no work; its width is set apart from its slots; run: %s machine set %s --width <n> --as %s%s", config.Value(name), toolName, name, actor, again(c)))
	}
	if dry {
		plan, err := config.PlanWrite(ctx, st, op, k.Name, row, changes)
		if err != nil {
			return storeNo(err, writeRemedy(k, add, name, err, next)+again(c))
		}
		plan.Actor = actor
		o := tool.Done().Fact("op", plan.Op).Fact("kind", k.Name).Fact("name", name).Fact("actor", actor).Fact("wrote", "nothing")
		o.Facts = append(o.Facts, changeFacts(plan)...)
		o.Notes = notes
		return o
	}
	var id int64
	var changed []string
	if add {
		if id, err = st.Insert(ctx, k.Name, row, actor); err != nil {
			return storeNo(err, writeRemedy(k, add, name, err, next)+again(c))
		}
	} else {
		if _, id, err = st.Update(ctx, k.Name, name, changes, actor); err != nil {
			return storeNo(err, writeRemedy(k, add, name, err, next)+again(c))
		}
		changed = slices.Sorted(maps.Keys(changes))
	}
	o := tool.Done().Fact("op", op).Fact("kind", k.Name).Fact("name", name).Fact("rev", id)
	if !add {
		o.Fact("changed", strings.Join(changed, ","))
	}
	o.Notes = notes
	_ = dsn
	return o
}

func changeFacts(ch config.Change) []tool.Field {
	var fs []tool.Field
	switch ch.Op {
	case config.OpAdd:
		for _, f := range slices.Sorted(maps.Keys(ch.After)) {
			fs = append(fs, tool.Field{K: f, V: ch.After[f]})
		}
	case config.OpRemove:
		for _, f := range slices.Sorted(maps.Keys(ch.Before)) {
			fs = append(fs, tool.Field{K: f, V: ch.Before[f]})
		}
	default:
		for _, f := range slices.Sorted(maps.Keys(ch.After)) {
			if ch.Before[f] != ch.After[f] {
				fs = append(fs, tool.Field{K: f, V: config.Value(ch.Before[f]) + ">" + config.Value(ch.After[f])})
			}
		}
	}
	return fs
}

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

func runKindRemove(ctx context.Context, c *tool.Call, k *config.Kind, d deps) *tool.Out {
	dry := c.DryRun()
	name := c.Str("name")
	actor, _ := actorName(c.Str("as"), d.getenv)
	if err := config.ValidateName(name); err != nil {
		return tool.Refuse(err.Error())
	}
	st, _, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	if laterKind(k) {
		if behind := schemaBehind(ctx, st, c); behind != nil {
			return behind
		}
	}
	if dry {
		plan, err := config.PlanWrite(ctx, st, config.OpRemove, k.Name, config.Row{Name: name}, nil)
		if err != nil {
			return storeNo(err, toolName+" "+k.Name+" list"+again(c))
		}
		plan.Actor = actor
		o := tool.Done().Fact("op", plan.Op).Fact("kind", k.Name).Fact("name", name).Fact("actor", actor).Fact("wrote", "nothing")
		o.Facts = append(o.Facts, changeFacts(plan)...)
		return o
	}
	id, err := st.Delete(ctx, k.Name, name, actor)
	if err != nil {
		return storeNo(err, toolName+" "+k.Name+" list"+again(c))
	}
	return tool.Done().Fact("op", config.OpRemove).Fact("kind", k.Name).Fact("name", name).Fact("rev", id)
}

func live(k *config.Kind) bool { return k.Name == config.KindMachine }

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

func rowKV(k *config.Kind, row config.Row, extra ...any) []any {
	kv := []any{"name", row.Name}
	for _, f := range k.Fields {
		kv = append(kv, f.Name, row.Fields[f.Name])
	}
	return append(kv, extra...)
}

func liveKV(bs map[string]*config.Beat, name string) []any {
	if bs == nil {
		return nil
	}
	b := bs[name]
	if b == nil {
		return []any{"beat", "none"}
	}
	return []any{"os", b.OS, "arch", b.Arch, "cores", b.Cores, "memory_gb", b.MemoryGB, "beat", b.At}
}

func runKindList(ctx context.Context, c *tool.Call, k *config.Kind, d deps) *tool.Out {
	st, _, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	if laterKind(k) {
		if behind := schemaBehind(ctx, st, c); behind != nil {
			return behind
		}
	}
	rows, err := st.List(ctx, k.Name)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	var bs map[string]*config.Beat
	if live(k) {
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			names = append(names, row.Name)
		}
		if bs, err = beats(ctx, liveRedisAddress(c.Str("redis"), d.getenv), names, d); err != nil {
			return tool.Refuse(err.Error())
		}
	}
	o := tool.Done().Fact("kind", k.Name).Fact("rows", len(rows))
	for _, row := range rows {
		o.Item(k.Name, rowKV(k, row, liveKV(bs, row.Name)...)...)
	}
	return o
}

func runKindRead(ctx context.Context, c *tool.Call, k *config.Kind, which string, d deps) *tool.Out {
	name := rowName(c, k)
	if err := config.ValidateName(name); err != nil {
		return tool.Refuse(err.Error())
	}
	st, _, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	if laterKind(k) || (k.Name == config.KindMachine && which == "show") {
		if behind := schemaBehind(ctx, st, c); behind != nil {
			return behind
		}
	}
	if which == "show" {
		return showRow(ctx, c, k, name, st, d)
	}
	changes, err := st.History(ctx, k.Name, name)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if len(changes) == 0 && !k.Singleton {
		o := tool.Fail(k.Name + " " + name + " has no history: it was never added")
		o.Remedy = toolName + " " + k.Name + " list" + again(c)
		return o
	}
	o := tool.Done().Fact("kind", k.Name).Fact("name", name).Fact("changes", len(changes))
	for _, ch := range changes {
		kv := []any{"id", ch.ID, "kind", ch.Kind, "name", ch.Name, "op", ch.Op, "actor", ch.Actor, "at", ch.At}
		for _, f := range changeFacts(ch) {
			kv = append(kv, f.K, f.V)
		}
		o.Item("change", kv...)
	}
	return o
}

func showRow(ctx context.Context, c *tool.Call, k *config.Kind, name string, st pgStore, d deps) *tool.Out {
	row, found, err := st.Get(ctx, k.Name, name)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if !found {
		o := tool.Fail(k.Name + " " + name + " not found")
		o.Remedy = toolName + " " + k.Name + " list" + again(c)
		return o
	}
	extra := []any{"created", row.CreatedAt, "updated", row.UpdatedAt}
	if k.Name == config.KindMachine {
		loops, err := machineLoops(ctx, st, name)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		extra = append(extra, "loops", strings.Join(loops, ","))
	}
	if k.Name == config.KindLoop {
		command, err := config.LoopCommandText(row)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		extra = append(extra, "command", command)
	}
	if live(k) {
		bs, err := beats(ctx, liveRedisAddress(c.Str("redis"), d.getenv), []string{name}, d)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		extra = append(extra, liveKV(bs, name)...)
	}
	return tool.Done().Item(k.Name, rowKV(k, row, extra...)...)
}

func runMigrate(c *tool.Call, d deps) *tool.Out {
	dry := c.DryRun()
	all, err := config.Migrations()
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if c.Bool("print") {
		o := tool.Done().Fact("print", len(all)).Fact("pg", "")
		for _, m := range all {
			o.Item("migration", "version", m.Version, "file", m.Name, "lines", strings.Count(m.SQL, "\n"))
		}
		return o
	}
	ctx := context.Background()
	st, dsn, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	key, value := where(dsn)
	// Version then Ownership, then the ledger: a dry run's readiness uses the
	// same Applied read the lines render (docs/SPEC-CONFIG.md, "The schema").
	have, err := st.Version(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	owners, err := st.Ownership(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if dry {
		return migrateDryRun(ctx, st, all, owners, key, value)
	}
	gaps := config.MigrateGaps(owners, config.Pending(all, have))
	if len(gaps) > 0 {
		o := tool.Fail(ownershipWhy(owners.Role, config.Pending(all, have), gaps))
		o.Remedy = ownershipRemedy(owners.Role, gaps)
		return o
	}
	from, to, applied, err := st.Migrate(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact(key, value).Fact("from", from).Fact("to", to).Fact("applied", len(applied))
}

func migrateDryRun(ctx context.Context, st pgStore, all []config.Migration, owners config.Ownership, key, value string) *tool.Out {
	ledger, err := st.Applied(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	have := 0
	recorded := map[int]bool{}
	for _, v := range ledger {
		recorded[v] = true
		have = max(have, v)
	}
	o := tool.Done()
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
			missing = append(missing, fmt.Sprint(m.Version))
		}
		o.Item("migration", "version", m.Version, "file", m.Name, "lines", strings.Count(m.SQL, "\n"), "state", state)
	}
	for _, g := range config.Gaps(owners) {
		o.Item("not-owned", "table", gapName(g), "owner", g.Owner, "role", owners.Role)
	}
	gaps := config.MigrateGaps(owners, pending)
	ready := "yes"
	if len(gaps) > 0 {
		ready = "no"
		why, remedy := ownershipWhy(owners.Role, pending, gaps), ownershipRemedy(owners.Role, gaps)
		o.Status, o.Exit, o.Why, o.Remedy = tool.Failed, 1, []string{why}, remedy
		o.ItemText("would-refuse", why+"; run: "+remedy)
		// A finding kind keeps a Failed result on stdout: nothing was attempted,
		// so the lines are the result, not a refusal on stderr (docs/SPEC-CONFIG.md, migrate --dry-run).
		o.Findings("kept-on-stdout")
	}
	o.Fact(key, value).Fact("from", have).Fact("to", len(all)).Fact("applied", 0).Fact("pending", len(pending)).Fact("missing", len(missing)).Fact("role", owners.Role).Fact("ready", ready)
	if ready == "no" {
		o.Fact("dry_run", true) // an OK result gets dry_run from the skeleton; a failed dry run does not
	}
	if len(missing) > 0 {
		o.Note(fmt.Sprintf("version(s) %s are not in the ledger and are below %d, the greatest recorded: migrate applies only versions above it, so it will not apply them", strings.Join(missing, ","), have))
	}
	return o
}

func gapName(g config.Gap) string {
	if g.Table == "" {
		return "schema config"
	}
	return "config." + g.Table
}

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

func ownershipRemedy(role string, gaps []config.Gap) string {
	lines := make([]string, len(gaps))
	for i, g := range gaps {
		lines[i] = g.Remedy(role)
	}
	return strings.Join(lines, " ")
}

func runStatus(c *tool.Call, d deps) *tool.Out {
	ctx := context.Background()
	st, dsn, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	schema, err := st.Version(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	key, value := where(dsn)
	o := tool.Done().Fact(key, value).Fact("schema", schema)
	if schema == 0 {
		o.Fact("redis", "")
		o.Status, o.Exit = tool.Failed, 1
		o.Why = []string{"schema config is not there yet"}
		o.Remedy = toolName + " migrate" + again(c)
		o.Findings("kept-on-stdout")
		return o
	}
	if behind := versionBehind(schema, c); behind != nil {
		return behind
	}
	counts, err := st.Counts(ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	revs := map[string]int64{}
	for _, k := range config.Kinds {
		rev, err := st.Rev(ctx, k.Name)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		revs[k.Name] = rev
		if k.Singleton {
			o.Fact(k.Name+"_rev", rev)
			continue
		}
		o.Fact(k.Name, counts[k.Name]).Fact(k.Name+"_rev", rev)
	}
	addr, addrErr := redisAddress(c.Str("redis"), d.getenv)
	if addrErr != nil {
		o.Fact("redis", "")
		return o
	}
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	defer rs.Close()
	o.Fact("redis", addr)
	behind := 0
	for _, k := range config.Kinds {
		_, applied, err := rs.Read(ctx, k.Name)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		o.Fact(k.Name+"_applied", applied)
		if applied != revs[k.Name] {
			behind++
		}
	}
	if behind > 0 {
		o.Status, o.Exit = tool.Failed, 1
		o.Why = []string{fmt.Sprintf("Redis is not at the store's revision for %d kind(s)", behind)}
		o.Remedy = toolName + " apply" + again(c)
		o.Findings("kept-on-stdout")
	}
	return o
}

func runApply(c *tool.Call, d deps) *tool.Out {
	dry := c.DryRun() || c.Bool("check")
	ctx := context.Background()
	kinds := config.KindNames()
	if c.Str("kind") != "" {
		kinds = []string{c.Str("kind")}
	}
	actor, _ := actorName(c.Str("as"), d.getenv)
	st, _, no := openStore(ctx, c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	if behind := schemaBehind(ctx, st, c); behind != nil {
		return behind
	}
	if c.Str("kind") == "" || c.Str("kind") == config.KindFleet {
		fleet, _, err := st.Get(ctx, config.KindFleet, config.KindFleet)
		if err != nil {
			return storeNo(err, toolName+" apply --check")
		}
		if err := config.ValidateFleetEndpoints(config.View(fleet.Fields)); err != nil {
			what, next, _ := strings.Cut(err.Error(), "; run: ")
			o := tool.Fail(what)
			o.Remedy = next
			return o
		}
	}
	addr, err := redisAddress(c.Str("redis"), d.getenv)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	defer rs.Close()
	o := tool.Done()
	for _, kn := range kinds {
		start := d.now()
		res, err := config.Apply(ctx, st, rs, kn, actor, dry, func(op config.Op) {
			kv := []any{"kind", kn, "name", op.Name}
			if op.Op == config.OpSet {
				kv = append(kv, "changed", strings.Join(op.Changed, ","))
			}
			o.Item(op.Op, kv...)
		})
		if err != nil {
			if config.IsConflict(err) {
				f := tool.Fail(err.Error())
				f.Remedy = toolName + " status (then apply from the store that is ahead)"
				f.Items = o.Items
				return f
			}
			return storeNo(err, toolName+" apply --dry-run")
		}
		if dry {
			o.Item("kind", "kind", kn, "add", res.Add, "set", res.Set, "remove", res.Remove, "rev", res.Rev, "applied", res.RedisRev)
			continue
		}
		o.Item("kind", "kind", kn, "add", res.Add, "set", res.Set, "remove", res.Remove, "rev", res.Rev, "ms", d.now().Sub(start).Milliseconds())
	}
	return o
}

const inventoryTimeout = 10 * time.Second

const maxKnownNames = 20

func wantInventory(c *tool.Call, d deps) {
	var problems []string
	if c.Given("host") && c.Str("host") == "" {
		problems = append(problems, "--host wants a machine name and got an empty value")
	}
	if c.Bool("list") && c.Given("host") {
		problems = append(problems, "--list and --host are exclusive: --list prints every host, --host prints one")
	}
	if c.Dur("timeout") <= 0 {
		problems = append(problems, "--timeout wants a Go duration above 0, like 10s")
	}
	if c.Given("fixture") && c.Str("fixture") == "" {
		problems = append(problems, "--fixture wants a file and got an empty value")
	}
	if c.Str("fixture") != "" && c.Str("redis") != "" {
		problems = append(problems, "--fixture and --redis are exclusive: the fixture stands in for the store")
	}
	if c.Str("fixture") == "" {
		if _, err := redisAddress(c.Str("redis"), d.getenv); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		c.Problem(strings.Join(problems, "; "))
	}
}

func runInventory(c *tool.Call, d deps) *tool.Out {
	const verb = "inventory"
	ctx := context.Background()
	again := func(extra ...string) string {
		parts := []string{toolName, verb}
		if c.Str("redis") != "" {
			parts = append(parts, "--redis", shq(c.Str("redis")))
		}
		if c.Str("fixture") != "" {
			parts = append(parts, "--fixture", shq(c.Str("fixture")))
		}
		if c.Bool("list") {
			parts = append(parts, "--list")
		}
		if c.Given("host") {
			parts = append(parts, "--host", shq(c.Str("host")))
		}
		return strings.Join(append(parts, extra...), " ")
	}
	fail := func(what, next string, code int) *tool.Out {
		fmt.Fprintf(c.Stderr, "%s %s REFUSED: %s; run: %s\n", toolName, verb, plain(what), next)
		return tool.Exit(code)
	}
	var snap *config.Snapshot
	if c.Str("fixture") != "" {
		var err error
		if snap, err = config.LoadFixture(c.Str("fixture")); err != nil {
			return fail(err.Error(), toolName+" "+verb+" -h", 2)
		}
	} else {
		addr, err := redisAddress(c.Str("redis"), d.getenv)
		if err != nil {
			return fail(err.Error(), toolName+" "+verb+" -h", 2)
		}
		tctx, cancel := context.WithTimeout(ctx, c.Dur("timeout"))
		defer cancel()
		stage := "connecting"
		storeFail := func(err error) *tool.Out {
			if tctx.Err() != nil {
				return fail(fmt.Sprintf("timed out after %s waiting for the store at %s while %s; check that Redis answers there", c.Dur("timeout"), addr, stage), again("--timeout", (c.Dur("timeout")*3).String()), 2)
			}
			return fail(err.Error(), toolName+" "+verb+" -h", 2)
		}
		rs, err := d.openRedis(tctx, addr)
		if err != nil {
			return storeFail(err)
		}
		defer rs.Close()
		stage = "reading the applied state"
		if snap, err = rs.Snapshot(tctx); err != nil {
			return storeFail(err)
		}
	}
	self, explicit := localHost(d.getenv, d.hostname)
	inv, err := config.BuildInventory(snap, self)
	if err != nil {
		if what, next, has := strings.Cut(err.Error(), "; run: "); has && strings.HasPrefix(what, "fleet:") {
			return fail(what, next, 1)
		}
		return fail(err.Error(), again(), 1)
	}
	if explicit && !inv.Has(self) {
		return fail(fmt.Sprintf("%s=%q names no machine row (the name is matched exactly); known machines: %s", envMachine, self, boundedNames(inv.All.Hosts, maxKnownNames)), toolName+" machine list", 1)
	}
	if c.Given("host") {
		data, err := inv.HostJSON(c.Str("host"))
		var unknown *config.UnknownHostError
		if errors.As(err, &unknown) {
			return fail(fmt.Sprintf("--host %q names no machine row; known machines: %s", unknown.Name, boundedNames(unknown.Known, maxKnownNames)), toolName+" machine list", 1)
		}
		if err != nil {
			return fail(err.Error(), toolName+" "+verb+" -h", 2)
		}
		fmt.Fprintln(c.Stdout, string(data))
		return tool.Exit(0)
	}
	data, err := inv.JSON()
	if err != nil {
		return fail(err.Error(), toolName+" "+verb+" -h", 2)
	}
	fmt.Fprintln(c.Stdout, string(data))
	return tool.Exit(0)
}

func localHost(getenv func(string) string, hostname func() (string, error)) (name string, explicit bool) {
	if s := getenv(envMachine); s != "" {
		return s, true
	}
	if h, err := hostname(); err == nil {
		return strings.ToLower(strings.Split(h, ".")[0]), false
	}
	return "", false
}

func boundedNames(names []string, max int) string {
	if len(names) == 0 {
		return "none"
	}
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:max], ", "), len(names)-max)
}
