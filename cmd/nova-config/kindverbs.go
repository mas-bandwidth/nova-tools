// runKind and the per-kind verbs it dispatches — add, set, remove, list, show, history, one set generated identically for every kind — are the CRUD over a kind's rows in the config store.

package main

import (
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

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
	case k.Name == config.KindRoute:
		want = "add, set, remove, list, show, history or prices"
	case k.Name == config.KindLoop:
		want = "add, set, remove, list, show, history or run"
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
	if k.Name == config.KindRoute && args[0] == "prices" {
		return runRoutePricesTool(ctx, args[1:], stdout, stderr, d)
	}
	if k.Name == config.KindLoop && args[0] == "run" {
		return runLoopRun(ctx, args[1:], stdout, stderr, d)
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

// positional resolves the one positional a verb allows: the name when
// nameAndRest found none. A singleton allows none at all.
func positional(k *config.Kind, fs *stdflag.FlagSet, name, verb string) (string, error) {
	switch {
	case k.Singleton && fs.NArg() > 0:
		return "", fmt.Errorf("%s takes no name: it is one row; want %s --<field> <value>", k.Name, verb)
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
	var c conn
	if add && (k.Name == config.KindMachine || k.Name == config.KindLoop) {
		c = storeFlags(fs)
	} else {
		c = seatStoreFlags(fs)
	}
	redisFlag := fs.String("redis", "", "the Redis `host:port` to apply this write (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	as := actorFlag(fs)
	reason := fs.String("reason", "", "why this change is made: one `line`, recorded in the history row beside the actor and the time; empty (the default) records none")
	dry := fs.Bool("dry-run", false, "print the change the write would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store")
	asJSON := jsonFlag(fs)
	values := map[string]*string{}
	for _, f := range k.Fields {
		if f.Name == "seat" && !add {
			continue
		}
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
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	actor, err := actorName(*as, d.getenv, seatVal)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	reasonText := strings.TrimSpace(*reason)
	if strings.ContainsAny(reasonText, "\n\r") {
		problems = append(problems, "--reason: want one line")
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
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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
	if k.Name == config.KindLoop && d.probe != nil {
		if err := checkLoopVerb(ctx, st, add, row, changes, d.probe); err != nil {
			return refuse(stderr, verb, err.Error())
		}
	}
	var notes []string
	if add && k.Name == config.KindMachine && row.Fields["width"] == "" {
		// width is set apart from slots and is the default when unset: say so where a newcomer meets it
		notes = append(notes, fmt.Sprintf("machine=%s width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: %s machine set %s --width <n> (0: no member) --actor %s%s", config.Value(name), toolName, name, actor, c.again()))
	}
	if note := actorAliasNote(fs); note != "" {
		notes = append(notes, note)
	}
	var id int64
	var changed []string
	if *dry {
		plan, err := config.PlanWrite(ctx, st, op, k.Name, row, changes)
		if err != nil {
			return storeErr(stderr, verb, err, writeRemedy(k, add, name, err, next)+c.again())
		}
		plan.Actor = actor
		plan.Reason = reasonText
		if *asJSON {
			o := tool.Done().Fact("dry_run", true).Fact("op", plan.Op).Fact("kind", k.Name).Fact("name", name).Fact("before", plan.Before).Fact("after", plan.After)
			if plan.Reason != "" {
				o.Fact("reason", plan.Reason)
			}
			o.Verb, o.Notes = verb, notes
			return emit(stdout, o)
		}
		fmt.Fprintln(stdout, config.PlanLine(plan))
		printNotes(stdout, notes)
		return 0
	}
	if add {
		if id, err = st.Insert(config.WithReason(ctx, reasonText), k.Name, row, actor); err != nil {
			return storeErr(stderr, verb, err, writeRemedy(k, add, name, err, next)+c.again())
		}
	} else {
		if _, id, err = st.Update(config.WithReason(ctx, reasonText), k.Name, name, changes, actor); err != nil {
			return storeErr(stderr, verb, err, writeRemedy(k, add, name, err, next)+c.again())
		}
		changed = slices.Sorted(maps.Keys(changes))
	}
	_, movesSeat := changes["coordinator"]
	applied, attempted, disposition := applyWrite(ctx, st, d, *redisFlag, k.Name, actor, id, k.Name == config.KindSprint && movesSeat)
	if *asJSON {
		o := tool.Done().Fact("op", op).Fact("kind", k.Name).Fact("name", name).Fact("rev", id)
		o.Fact("applied", applied).Fact("disposition", disposition)
		if !add {
			o.Fact("changed", changed)
		}
		o.Verb, o.Notes = verb, notes
		if attempted && !applied {
			o.Status, o.Exit, o.Why = tool.Failed, 1, []string{disposition}
		}
		return emit(stdout, o)
	}
	if add {
		fmt.Fprintf(stdout, "CONFIG ADD kind=%s name=%s rev=%d\n", k.Name, config.Value(name), id)
	} else {
		fmt.Fprintf(stdout, "CONFIG SET kind=%s name=%s rev=%d changed=%s\n", k.Name, config.Value(name), id, config.Value(strings.Join(changed, ",")))
	}
	printNotes(stdout, notes)
	fmt.Fprintln(stdout, disposition)
	if attempted && !applied {
		return 1
	}
	return 0
}

// applyWrite reports every committed write, including a missing Redis address.
// The store revision remains the truth when apply fails; a later loop pass can
// retry it without writing the row again.
func applyWrite(ctx context.Context, st pgStore, d deps, redisFlag, kind, actor string, rev int64, moveSeat bool) (bool, bool, string) {
	addr, err := redisAddress(redisFlag, d.getenv)
	if err != nil {
		return false, false, (&applyErr{rev: rev, err: err}).Error()
	}
	var rs redisSide
	rs, err = d.openRedis(ctx, addr)
	if err == nil {
		apply := config.Apply
		if moveSeat {
			apply = config.ApplyMovingSeat
		}
		_, err = apply(ctx, st, rs, kind, actor, false, func(config.Op) {})
		// ignored: apply's result is settled before close; a close error cannot change the revision stamp.
		_ = rs.Close()
	}
	if err != nil {
		return false, true, (&applyErr{rev: rev, err: err}).Error()
	}
	return true, true, fmt.Sprintf("APPLIED rev=%d", rev)
}

// checkLoopVerb refuses a loop whose verb is gone (config.CheckLoopVerb), the
// row as the write leaves it: an add's own, a set's changes over the stored
// row. A set of a row not there passes here; its update refuses it.
func checkLoopVerb(ctx context.Context, st pgStore, add bool, row config.Row, changes map[string]string, probe config.VerbProbe) error {
	if !add {
		cur, found, err := st.Get(ctx, config.KindLoop, row.Name)
		if err != nil || !found {
			return err
		}
		row = config.Row{Name: cur.Name, Fields: maps.Clone(cur.Fields)}
		maps.Copy(row.Fields, changes)
	}
	return config.CheckLoopVerb(ctx, row, probe)
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

// removeRemedy is the command a remove refusal names: a route a tier still
// lists is taken out by that tier's set, the remaining routes read from the
// store and filled in, so the next turn is a paste and not a search
// (docs/STANDARD.md, section 3, point 2). Every other refusal keeps next.
func removeRemedy(ctx context.Context, st pgStore, k *config.Kind, name, again, next string) string {
	if k.Name != config.KindRoute {
		return next
	}
	tiers, err := st.List(ctx, config.KindTier)
	if err != nil {
		return next
	}
	for _, tier := range tiers {
		var rest []string
		found := false
		for _, r := range strings.Split(tier.Fields["routes"], ",") {
			switch {
			case r == name:
				found = true
			case r != "":
				rest = append(rest, r)
			}
		}
		if !found {
			continue
		}
		value := strings.Join(rest, ",")
		if value == "" {
			value = "''" // an empty --routes: the shell word the flag takes
		}
		return toolName + " " + config.KindTier + " set " + tier.Name + " --routes " + value + again
	}
	return next
}

func runKindRemove(ctx context.Context, k *config.Kind, args []string, stdout, stderr io.Writer, d deps) int {
	verb := k.Name + " remove"
	fs := verbflag.New(verb)
	c := seatStoreFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` to apply this write (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	as := actorFlag(fs)
	reason := fs.String("reason", "", "why this change is made: one `line`, recorded in the history row beside the actor and the time; empty (the default) records none")
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
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	actor, err := actorName(*as, d.getenv, seatVal)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	reasonText := strings.TrimSpace(*reason)
	if strings.ContainsAny(reasonText, "\n\r") {
		problems = append(problems, "--reason: want one line")
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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
		plan.Reason = reasonText
		if *asJSON {
			o := tool.Done().Fact("dry_run", true).Fact("op", plan.Op).Fact("kind", k.Name).Fact("name", name).Fact("before", plan.Before)
			if plan.Reason != "" {
				o.Fact("reason", plan.Reason)
			}
			o.Verb = verb
			return emit(stdout, o)
		}
		fmt.Fprintln(stdout, config.PlanLine(plan))
		return 0
	}
	id, err := st.Delete(config.WithReason(ctx, reasonText), k.Name, name, actor)
	if err != nil {
		next := toolName + " " + k.Name + " list" + c.again()
		if errors.Is(err, config.ErrReferenced) {
			next = removeRemedy(ctx, st, k, name, c.again(), next)
		}
		return storeErr(stderr, verb, err, next)
	}
	applied, attempted, disposition := applyWrite(ctx, st, d, *redisFlag, k.Name, actor, id, false)
	if *asJSON {
		o := tool.Done().Fact("op", config.OpRemove).Fact("kind", k.Name).Fact("name", name).Fact("rev", id)
		o.Fact("applied", applied).Fact("disposition", disposition)
		if note := actorAliasNote(fs); note != "" {
			o.Note(note)
		}
		o.Verb = verb
		if attempted && !applied {
			o.Status, o.Exit, o.Why = tool.Failed, 1, []string{disposition}
		}
		return emit(stdout, o)
	}
	fmt.Fprintf(stdout, "CONFIG REMOVE kind=%s name=%s rev=%d\n", k.Name, config.Value(name), id)
	if note := actorAliasNote(fs); note != "" {
		printNotes(stdout, []string{note})
	}
	fmt.Fprintln(stdout, disposition)
	if attempted && !applied {
		return 1
	}
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
	defer func() { _ = rs.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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
	c := seatStoreFlags(fs)
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
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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
	c := seatStoreFlags(fs)
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
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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

// laterKind is a kind whose table, or a column of it the verbs read and
// write, a later migration made (loops since version 6, routes since 7, tiers
// since 8, the machine's width since 12, fleet endpoints since 14, the note of
// a route and a machine since 15): each of its verbs refuses on a store older
// than this binary's migrations (behindSchema), which does not have it.
func laterKind(k *config.Kind) bool {
	return k.Name == config.KindLoop || k.Name == config.KindRoute || k.Name == config.KindTier || k.Name == config.KindFleet || k.Name == config.KindMachine
}
