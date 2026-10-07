// migrate.go holds the migrate verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nova-tools/internal/config"
	"github.com/nova-tools/internal/nsprint/verbflag"
	"github.com/nova-tools/internal/oneline"
	"github.com/nova-tools/internal/tool"
)

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
