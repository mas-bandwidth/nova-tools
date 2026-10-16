// apply.go holds the apply verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func runApply(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "apply"
	fs := verbflag.New(verb)
	c := seatStoreFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` to write; --kind route otherwise uses the fleet endpoint, other kinds use NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, then the seat")
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
	var invalidKind bool
	kinds := config.KindNames()
	if *kind != "" {
		if _, ok := config.Lookup(*kind); !ok {
			invalidKind = true
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
	dsn, err := c.dsn(ctx, d)
	if err != nil {
		problems = append(problems, err.Error())
	}
	var addr string
	if *kind != config.KindRoute || *redisFlag != "" {
		addr, err = redisAddress(*redisFlag, d.getenv)
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		if invalidKind {
			return refuseOptions(stderr, verb, strings.Join(problems, "; "), config.KindNames())
		}
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
	if *kind == config.KindRoute && *redisFlag == "" {
		addr, err = routeRedisAddress(ctx, st)
		if err != nil {
			return refused(stderr, verb, err.Error(), toolName+" fleet show"+c.again())
		}
	}
	// The fleet's endpoints and the routes' price sheets are each their own
	// kind's check (config.ApplyInRun): a problem refuses that kind only and
	// every other kind applies (docs/SPEC-CONFIG.md, "Apply").
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
	// (ConfigApply.tla, EveryPlannedKindIsAttemptedUnlessCrashed): a refused row is
	// named on one CONFIG REFUSED line, a row whose ref names it is refused
	// in its turn (Earlier), and only a store or connection error that is
	// not a refusal stops the run (exit 2).
	earlier := config.Earlier{}
	var applied, refusedKinds []string
	firstRemedy := ""
	refuseRow := func(kn, name string, err error, absent bool) {
		why, remedy := refusalRemedy(kn, name, err)
		if firstRemedy == "" {
			firstRemedy = remedy
		}
		earlier.Add(kn, name, why, absent)
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
		case err != nil && !config.Refused(err):
			// A store or connection error is never a refusal, even after a
			// refusal in this kind: the run stops at once, exit 2, and no
			// later kind is written (ConfigApply.tla StoreError). The kind's
			// refusals before it are still named.
			for _, r := range res.Refused {
				refuseRow(kn, r.Name, r.Err, r.Absent)
			}
			if *asJSON {
				// The one object carries the rows refused before the error,
				// at the error's exit.
				o.Status = tool.Refused
				o.Exit = 2
				o.Why = append(o.Why, plain(err.Error()))
				o.Remedy = toolName + " apply --dry-run"
				return emit(stdout, o)
			}
			return storeErr(stderr, verb, err, toolName+" apply --dry-run")
		case len(res.Refused) > 0:
			// The kind wrote every op it could and is not stamped
			// (ConfigApply.tla DoOps, DoStamp).
			for _, r := range res.Refused {
				refuseRow(kn, r.Name, r.Err, r.Absent)
			}
			refusedKinds = append(refusedKinds, fmt.Sprintf("%s:%d", kn, len(res.Refused)))
			continue
		case err != nil:
			// The whole kind is refused before any write (CONFLICT, the
			// fleet's endpoints, an unpriced route): it is said on stderr as
			// before, its unwritten rows count as refused for the ref rule
			// (ConfigApply.tla PendingMachines), and the later kinds go on.
			why, remedy, ok := strings.Cut(err.Error(), "; run: ")
			switch {
			case config.IsConflict(err):
				why, remedy = err.Error(), conflictRemedy
			case !ok:
				remedy = toolName + " apply --dry-run"
			}
			for _, op := range res.Pending {
				earlier.Add(kn, op.Name, oneLine(why), op.Op == config.OpAdd)
			}
			if *asJSON {
				o.Item("refused", "kind", kn, "name", kn, "why", tool.Text(why), "run", tool.Text(remedy))
			} else {
				refused(stderr, verb, why, remedy)
			}
			if firstRemedy == "" {
				firstRemedy = remedy
			}
			refusedKinds = append(refusedKinds, kn+":1")
			continue
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
	case errors.Is(err, config.ErrActor):
		return why, apply + " --as <a friend that holds the coordinator role>"
	case errors.Is(err, config.ErrInUse):
		return why, fmt.Sprintf("move or remove what %s %s still carries, then %s", kind, name, apply)
	case errors.Is(err, config.ErrRefusedRef):
		return why, "resolve the row refused earlier in this run (its CONFIG REFUSED line above), then " + apply
	}
	return why, fmt.Sprintf("%s %s show %s, fix it with %s %s set %s, then %s", toolName, kind, name, toolName, kind, name, apply)
}

// routeRedisAddress selects the copy the sprint reads, never an ambient
// endpoint (ConfigApply.tla NoSilentUnappliedWrite; SPEC-CONFIG Apply).
func routeRedisAddress(ctx context.Context, st config.Store) (string, error) {
	fleet, found, err := st.Get(ctx, config.KindFleet, config.KindFleet)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("fleet row is missing; migrate and declare its store and redis_port before applying")
	}
	host, port := fleet.Fields["store"], fleet.Fields["redis_port"]
	n, err := strconv.ParseUint(port, 10, 16)
	if host == "" || err != nil || n == 0 {
		return "", fmt.Errorf("fleet store and redis_port must name the sprint copy; set the missing endpoint before applying")
	}
	return net.JoinHostPort(host, port), nil
}

// copyRouteWrite uses the existing apply path after the store commits. Its
// stamp must reach this command's revision (ConfigApply.tla RouteResult).
func copyRouteWrite(ctx context.Context, st config.Store, actor string, rev int64, c conn, d deps) (config.Result, string, error) {
	res := config.Result{RedisRev: -1}
	remedy := toolName + " apply --kind route --actor " + shq(actor) + c.again()
	addr, err := routeRedisAddress(ctx, st)
	if err != nil {
		return res, remedy, err
	}
	remedy += " --redis " + shq(addr)
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return res, remedy, err
	}
	defer func() { _ = rs.Close() }() // ignored: the apply result is already read; a close error cannot change it
	res, err = config.Apply(ctx, st, rs, config.KindRoute, actor, false, func(config.Op) {})
	if res.Kind == "" {
		res.RedisRev = -1
	}
	if res.Stamped {
		res.RedisRev = res.Rev
	}
	if err == nil && res.RedisRev < rev {
		err = fmt.Errorf("copy revision %d is below committed revision %d", res.RedisRev, rev)
	}
	return res, remedy, err
}

// finishKindWrite completes a committed write. Only routes synchronously
// apply; text and JSON report the same failure, without undoing history
// (ConfigApply.tla NoSilentUnappliedWrite and RouteResult).
func finishKindWrite(ctx context.Context, st config.Store, kind, actor string, rev int64, c conn, d deps, stdout, stderr io.Writer, o *tool.Out) int {
	if kind != config.KindRoute {
		if o != nil {
			return emit(stdout, o)
		}
		return 0
	}
	res, remedy, err := copyRouteWrite(ctx, st, actor, rev, c, d)
	word, code := "APPLY", 0
	if err != nil {
		word, code = "NOT-APPLIED", 1
	}
	if o != nil {
		o.Fact("apply_status", word).Fact("applied", res.RedisRev).Fact("apply_rev", res.Rev)
		if err != nil {
			o.Status, o.Exit, o.Remedy = tool.Refused, 1, remedy
			o.Why = append(o.Why, plain(err.Error()))
		}
		return emit(stdout, o)
	}
	if err != nil {
		fmt.Fprintf(stderr, "CONFIG NOT-APPLIED kind=route rev=%d applied=%d why=%s; run: %s\n", rev, res.RedisRev, oneLine(err.Error()), remedy)
	} else {
		fmt.Fprintf(stdout, "CONFIG APPLY kind=route add=%d set=%d remove=%d rev=%d applied=%d\n", res.Add, res.Set, res.Remove, res.Rev, res.RedisRev)
	}
	return code
}
