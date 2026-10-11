// migrate.go holds the migrate verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func runMigrate(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "migrate"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	print := fs.Bool("print", false, "list the migrations this binary carries and connect to nothing")
	max := fs.Int("max", bounded.Default, "SQL lines to print under each MIGRATION line before one MORE line stands for the rest; 0 prints each migration whole")
	window := fs.Bool("window", false, "the stopped window of the seat play: refuse, applying nothing, while any other nova role's session holds the database (the old server or member still runs); --dry-run reports the sessions and refuses nothing")
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
				sql := migrationLines(m.SQL)
				shown := shownSQL(sql, *max)
				o.Item("migration", "version", m.Version, "file", m.Name, "lines", len(sql),
					"sql", strings.Join(shown, "\n"), "sql_shown", len(shown), "sql_lines", len(sql))
			}
			return emit(stdout, o)
		}
		for _, m := range all {
			sql := migrationLines(m.SQL)
			shown := shownSQL(sql, *max)
			fmt.Fprintf(stdout, "MIGRATION version=%d file=%s lines=%d\n", m.Version, config.Value(m.Name), len(sql))
			for _, line := range shown {
				fmt.Fprintln(stdout, line)
			}
			if len(shown) < len(sql) {
				fmt.Fprintln(stdout, bounded.MoreLine("MIGRATE", "sql", len(shown), len(sql), tool.MaxRemedy))
			}
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
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
	key, value := where(dsn)
	have, err := st.Version(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	owners, err := st.Ownership(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	held, err := st.Sessions(ctx)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *dry {
		return migrateDryRun(ctx, st, all, owners, held, stdout, stderr, key, value, dsn, *asJSON)
	}
	gaps := config.MigrateGaps(owners, config.Pending(all, have))
	if len(gaps) > 0 {
		return refused(stderr, verb, ownershipWhy(owners, config.Pending(all, have), gaps), ownershipRemedy(owners, gaps, dsn))
	}
	if *window && len(held) > 0 && len(config.Pending(all, have)) > 0 {
		return refused(stderr, verb, windowWhy(owners.Role, held), windowRemedy)
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

// migrationLines is a migration's SQL as the lines a reader sees: one entry
// per line, the file's trailing newline ended rather than counting an empty
// last line, so lines= names the same count migrate has always printed.
func migrationLines(sql string) []string {
	return strings.Split(strings.TrimSuffix(sql, "\n"), "\n")
}

// shownSQL is the first max lines of a migration's SQL (all of it when max is
// zero or negative), the cut pkg/bounded's MORE line stands for
// (docs/STANDARD.md, section 2, "Output is bounded and keeps its totals").
func shownSQL(sql []string, max int) []string {
	if max <= 0 || len(sql) <= max {
		return sql
	}
	return sql[:max]
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
func migrateDryRun(ctx context.Context, st pgStore, all []config.Migration, owners config.Ownership, held []config.Session, stdout, stderr io.Writer, key, value, dsn string, asJSON bool) int {
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
	for _, h := range held {
		o.Item("session", "role", h.Role, "pid", h.PID, "app", h.Application)
		lines = append(lines, fmt.Sprintf("MIGRATE SESSION role=%s pid=%d app=%s", config.Value(h.Role), h.PID, config.Value(h.Application)))
	}
	// Readiness uses the same ledger as the rendered pending rows: another
	// migrate may advance it after Version (docs/SPEC-CONFIG.md, "The schema").
	gaps := config.MigrateGaps(owners, pending)
	ready := "yes"
	if len(gaps) > 0 {
		ready = "no"
		why, remedy := ownershipWhy(owners, pending, gaps), ownershipRemedy(owners, gaps, dsn)
		o.Status, o.Exit, o.Why, o.Remedy = tool.Failed, 1, []string{why}, remedy
		lines = append(lines, "MIGRATE WOULD-REFUSE "+plain(why)+"; run: "+remedy)
	}
	o.Fact(key, value).Fact("from", have).Fact("to", len(all)).Fact("applied", 0).Fact("dry_run", true).Fact("pending", len(pending)).Fact("missing", len(missing)).Fact("role", owners.Role).Fact("ready", ready).Fact("owner", wholeOwner(owners)).Fact("sessions", len(held))
	if len(missing) > 0 {
		o.Note(fmt.Sprintf("version(s) %s are not in the ledger and are below %d, the greatest recorded: migrate applies only versions above it, so it will not apply them", strings.Join(missing, ","), have))
	}
	if asJSON {
		return emit(stdout, o)
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	fmt.Fprintf(stdout, "CONFIG MIGRATE %s=%s from=%d to=%d applied=0 dry_run=true pending=%d missing=%d role=%s ready=%s owner=%s sessions=%d\n", key, config.Value(value), have, len(all), len(pending), len(missing), config.Value(owners.Role), ready, config.Value(wholeOwner(owners)), len(held))
	printNotes(stdout, o.Notes)
	return o.Exit
}

// wholeOwner is the owner= fact: the one role that owns schema config and
// every table in it (config.WholeOwner), "none" before the first migration
// makes the schema, "mixed" when the tables have more than one owner. The seat
// play reads it before its window and refuses a store that is not owned whole
// by the role it migrates as.
func wholeOwner(o config.Ownership) string {
	if owner, ok := config.WholeOwner(o); ok {
		return owner
	}
	if o.SchemaOwner == "" {
		return "none"
	}
	return "mixed"
}

// windowWhy is why migrate --window refuses: the sessions that hold the
// database, each named, so the person sees what still runs.
func windowWhy(role string, held []config.Session) string {
	names := make([]string, len(held))
	for i, h := range held {
		names[i] = h.String()
	}
	return fmt.Sprintf("role %s applied none: --window says the old server and member are stopped, and %d other nova session(s) hold the database: %s", role, len(held), oneline.Escape(strings.Join(names, ", ")))
}

const windowRemedy = "stop what holds the database (the seat play's window stops the old server and member and waits for ps to show none), then run the same migrate again; nova-config migrate --dry-run --json names the sessions (sessions=, one session item each) without refusing"

// gapName is the gap's object as the lines name it.
func gapName(g config.Gap) string {
	if g.Table == "" {
		return "schema config"
	}
	return "config." + g.Table
}

// ownershipWhy is why migrate refuses: the role, the migrations it cannot
// apply, the rule, and each owner with what it holds. When one other role
// owns schema config whole (config.SoleOwner), it names that role as the one
// that runs migrate: the store is not misowned, the run is as the wrong role.
func ownershipWhy(o config.Ownership, pending []config.Migration, gaps []config.Gap) string {
	which := fmt.Sprintf("migration %d", pending[0].Version)
	if len(pending) > 1 {
		which = fmt.Sprintf("migrations %d to %d", pending[0].Version, pending[len(pending)-1].Version)
	}
	if owner, ok := config.SoleOwner(o); ok {
		return fmt.Sprintf("role %s cannot apply %s and applied none: role %s owns schema config and every table in it, and only the owner alters and fills them, so %s runs this once",
			o.Role, which, owner, owner)
	}
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
	return fmt.Sprintf("role %s cannot apply %s and applied none: the role that runs migrate must own every table in schema config and be able to create in it, and %s, so a role with the owners' rights runs this once, in psql",
		o.Role, which, strings.Join(held, ", and "))
}

// ownershipRemedy is what closes every gap, on one line, printed for a
// person to run and never run by migrate: the same migrate as the owning
// role when one role owns schema config whole (the --pg with that user, the
// password from the variable NOVA_PG_PASSWORD_ENV names), else one ALTER
// OWNER statement per gap (migrate changes no ownership).
func ownershipRemedy(o config.Ownership, gaps []config.Gap, dsn string) string {
	if owner, ok := config.SoleOwner(o); ok {
		return oneline.Escape(fmt.Sprintf("NOVA_PG_PASSWORD_ENV=%s nova-config migrate --pg %s (as %s, the owner, with its password in the variable %s names)",
			config.PasswordEnvFor(owner), config.MigrateAs(dsn, owner), owner, config.PasswordEnvFor(owner)))
	}
	lines := make([]string, len(gaps))
	for i, g := range gaps {
		lines[i] = g.Remedy(o.Role)
	}
	return oneline.Escape(strings.Join(lines, " "))
}
