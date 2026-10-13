// apply.go holds the apply verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func runApply(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "apply"
	fs := verbflag.New(verb)
	c := seatStoreFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` to write (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	as := actorFlag(fs)
	kind := fs.String("kind", "", "one `kind` to apply ("+strings.Join(config.KindNames(), ", ")+"); every kind, in order, when unset")
	check := fs.Bool("check", false, "the same as --dry-run")
	dry := fs.Bool("dry-run", false, "print the ADD, SET and REMOVE lines (CHECK ...) and write nothing; it still reads the store and Redis")
	moveSeat := fs.Bool("move-seat", false, "write the sprint row's coordinator over a live seat that differs (the owner's word); without it apply holds the live seat, writes every other field and prints one APPLY HELD line")
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
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	// The dry run takes the real run's checks: both resolve the actor the
	// write is recorded under, so a missing one refuses before either runs
	// (docs/STANDARD.md, "A verb that writes has a dry run").
	actor, err := actorName(*as, d.getenv, seatVal)
	if err != nil {
		problems = append(problems, err.Error())
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
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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
	// An enabled route with no price is refused before the plan and any write
	// (docs/SPEC-CONFIG.md, "route"): for an every-kind apply the route kind is
	// last, so a bare apply would otherwise write every earlier kind first.
	if *kind == "" || *kind == config.KindRoute {
		routes, err := st.List(ctx, config.KindRoute)
		if err != nil {
			return storeErr(stderr, verb, err, toolName+" apply --check")
		}
		if err := config.CheckRoutesPriced(routes); err != nil {
			return storeErr(stderr, verb, &config.RefusedError{Err: config.ErrInvalid, Detail: err.Error()}, toolName+" apply --dry-run")
		}
	}
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = rs.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
	word := "APPLY"
	if *check {
		word = "CHECK"
	}
	o := tool.Done().Fact("dry_run", *check)
	o.Verb = verb
	// Every kind is attempted, in order, whatever an earlier kind refused
	// (ConfigApply.tla, EveryKindAfterARefusalIsAttempted): a refused row is
	// named on one CONFIG REFUSED line, a row whose ref names it is refused
	// in its turn (Earlier), and only a store or connection error that is
	// not a refusal stops the run (exit 2).
	earlier := config.Earlier{}
	var applied, refusedKinds []string
	firstRemedy := ""
	refuseRow := func(kn, name string, err error) {
		why, remedy := refusalRemedy(kn, name, err)
		if firstRemedy == "" {
			firstRemedy = remedy
		}
		earlier.Add(kn, name, why)
		if *asJSON {
			o.Item("refused", "kind", kn, "name", name, "why", tool.Text(why), "run", tool.Text(remedy))
			return
		}
		fmt.Fprintf(stdout, "CONFIG %sREFUSED kind=%s name=%s why=%s run: %s\n", checkWord(*check), kn, name, oneLine(why), remedy)
	}
	for _, kn := range kinds {
		start := d.now()
		res, err := config.ApplyInRun(ctx, st, rs, kn, actor, *check, *moveSeat, earlier, func(op config.Op) {
			if *asJSON {
				o.Item("op", "kind", kn, "op", op.Op, "name", op.Name, "changed", op.Changed)
				return
			}
			fmt.Fprintln(stdout, config.SaidLine(word, kn, op)) // a held seat's line said whole
		})
		switch {
		case len(res.Refused) > 0:
			// The kind wrote every op it could and is not stamped
			// (ConfigApply.tla DoOps, DoStamp).
			for _, r := range res.Refused {
				refuseRow(kn, r.Name, r.Err)
			}
			refusedKinds = append(refusedKinds, fmt.Sprintf("%s:%d", kn, len(res.Refused)))
			continue
		case err != nil && config.IsConflict(err):
			// CONFLICT stops this kind only; the rows it leaves unwritten are
			// refused for the ref rule (ConfigApply.tla PendingMachines).
			if res.Written == 0 {
				for _, op := range res.Ops {
					if op.Op == config.OpAdd || op.Op == config.OpSet {
						earlier.Add(kn, op.Name, "CONFLICT "+kn)
					}
				}
			}
			if *asJSON {
				o.Item("refused", "kind", kn, "name", kn, "why", tool.Text(err.Error()), "run", tool.Text(conflictRemedy))
			} else {
				refused(stderr, verb, err.Error(), conflictRemedy)
			}
			if firstRemedy == "" {
				firstRemedy = conflictRemedy
			}
			refusedKinds = append(refusedKinds, kn+":1")
			continue
		case err != nil && config.Refused(err):
			// A refusal of the whole kind before any op (an invalid row the
			// kind checks first): every row of it counts as refused.
			refuseRow(kn, "*", err)
			refusedKinds = append(refusedKinds, kn+":1")
			continue
		case err != nil:
			return storeErr(stderr, verb, err, toolName+" apply --dry-run")
		}
		applied = append(applied, kn)
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
	code := 0
	if len(refusedKinds) > 0 {
		code = 1
		end := fmt.Sprintf("kinds=%s applied=%s", strings.Join(refusedKinds, ","), dashIfEmpty(strings.Join(applied, ",")))
		if *asJSON {
			o.Status = tool.Refused
			o.Exit = 1
			o.Why = append(o.Why, "refused "+end)
			o.Remedy = firstRemedy
		} else {
			fmt.Fprintf(stdout, "CONFIG %s REFUSED %s; run: %s\n", word, end, firstRemedy)
		}
	} else if !*check && !*asJSON {
		fmt.Fprintf(stdout, "CONFIG APPLY OK kinds=%d\n", len(applied))
	}
	note := actorAliasNote(fs)
	if note != "" {
		o.Note(note)
	}
	if *asJSON {
		return emit(stdout, o)
	}
	if note != "" {
		printNotes(stdout, []string{note})
	}
	return code
}

// conflictRemedy is what to run when a kind's stamp is ahead of this store.
var conflictRemedy = toolName + " status (then apply from the store that is ahead)"

// checkWord is the CHECK prefix of a refusal line --check prints.
func checkWord(check bool) string {
	if check {
		return "CHECK "
	}
	return ""
}

// dashIfEmpty is "-" for an empty list, as the tool's lines say none.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// oneLine keeps a reason on its line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// refusalRemedy is a refused row's one-line reason and the command that
// resolves it (docs/SPEC-CONFIG.md, "Apply"): a reason that names its own
// "; run: " keeps it; else the remedy follows the kind of refusal.
func refusalRemedy(kind, name string, err error) (string, string) {
	why := err.Error()
	if w, next, ok := strings.Cut(why, "; run: "); ok {
		return w, next
	}
	apply := toolName + " apply"
	switch {
	case errors.Is(err, config.ErrCeiling):
		if kind == config.KindMachine {
			return why, fmt.Sprintf("%s machine set %s --slots <n> (at least its friends' sum), then %s", toolName, name, apply)
		}
		return why, fmt.Sprintf("%s %s set %s --slots <n>, or raise the machine's ceiling with %s machine set <machine> --slots <n>, then %s", toolName, kind, name, toolName, apply)
	case errors.Is(err, config.ErrActor):
		return why, apply + " --as <a friend that holds the coordinator role>"
	case errors.Is(err, config.ErrInUse):
		return why, fmt.Sprintf("move or remove what %s %s still carries, then %s", kind, name, apply)
	case errors.Is(err, config.ErrRefusedRef):
		return why, "resolve the row refused earlier in this run (its CONFIG REFUSED line above), then " + apply
	case name == "*":
		return why, fmt.Sprintf("%s %s list, fix the row it names, then %s", toolName, kind, apply)
	}
	return why, fmt.Sprintf("%s %s show %s, fix it with %s %s set %s, then %s", toolName, kind, name, toolName, kind, name, apply)
}
