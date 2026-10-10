// status.go holds the status verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "status"
	fs := verbflag.New(verb)
	c := seatStoreFlags(fs)
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
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
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
			printNotes(stdout, o.Notes)
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
	if d.probe != nil {
		loops, err := st.List(ctx, config.KindLoop)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		o.Notes = config.DeadLoops(ctx, loops, d.probe)
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
	defer func() { _ = rs.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
	line += " redis=" + config.Value(addr)
	o.Fact("redis", addr)
	behind := 0
	var judgments []string
	for _, k := range config.Kinds {
		_, applied, err := rs.Read(ctx, k.Name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		line += fmt.Sprintf(" %s_applied=%d", k.Name, applied)
		o.Fact(k.Name+"_applied", applied)
		if applied != revs[k.Name] {
			behind++
			age, known, err := kindGapAge(ctx, st, k.Name, applied, d.now())
			if err != nil {
				return refuse(stderr, verb, err.Error())
			}
			if known {
				line += fmt.Sprintf(" %s_gap_age=%s", k.Name, ageText(age))
				o.Fact(k.Name+"_gap_age", ageText(age))
				if age > applyGapJudgment {
					judgment := judgmentLine(k.Name, revs[k.Name], applied, age)
					judgments = append(judgments, judgment)
					if *asJSON {
						// The line rendering below carries the judgment once
						// for a text status; only JSON, which prints no line,
						// needs it as a note too.
						o.Note(judgment)
					}
				}
			} else {
				line += " " + k.Name + "_gap_age=unknown"
				o.Fact(k.Name+"_gap_age", "unknown")
			}
		}
	}
	for _, judgment := range judgments {
		line += "\n" + judgment
	}
	if behind > 0 {
		return finish(1, fmt.Sprintf("Redis is not at the store's revision for %d kind(s)", behind), toolName+" apply"+c.again())
	}
	return finish(0, "", "")
}
