// apply.go holds the apply verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func runApply(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "apply"
	fs := verbflag.New(verb)
	c := writeStoreFlags(fs)
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
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	if !*check {
		var err error
		actor, err = actorName(*as, d.getenv, seatVal)
		if err != nil {
			problems = append(problems, err.Error())
		}
	} else if *as != "" {
		actor = *as
	} else if v := d.getenv(envActor); v != "" {
		actor = v
	} else if seatVal != "" {
		actor = seatVal
	} else if getenv := d.getenv; getenv != nil && getenv(seatcred.SeatEnv) != "" {
		actor = getenv(seatcred.SeatEnv)
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
	_ = st.Close() // ignored: close after read
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
	_ = rs.Close() // ignored: close after read
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
