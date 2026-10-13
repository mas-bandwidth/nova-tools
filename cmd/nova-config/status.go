// status.go holds the status verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
	dsn, pgFrom, _, err := c.discoverPG(ctx, d.getenv, d)
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
	var providerLines, providerWarnings []string
	line := "CONFIG STATUS " + key + "=" + config.Value(value) + " schema=" + strconv.Itoa(schema)
	if pgFrom != "" {
		line += " pg-from=" + pgFrom
		o.Fact("pg-from", pgFrom)
	}
	// finish prints the result (lines or JSON) and, for a refusal, its line.
	finish := func(code int, why, next string) int {
		if *asJSON {
			if code != 0 {
				o.Status, o.Exit, o.Why, o.Remedy = tool.Failed, code, []string{why}, next
			}
			emit(stdout, o)
		} else {
			fmt.Fprintln(stdout, line)
			for _, providerLine := range providerLines {
				fmt.Fprintln(stdout, providerLine)
			}
			printNotes(stdout, o.Notes[:len(o.Notes)-len(providerWarnings)])
			for _, warning := range providerWarnings {
				fmt.Fprintln(stdout, "CONFIG WARN "+warning)
			}
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
	providers, err := st.List(ctx, config.KindProvider)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	routes, err := st.List(ctx, config.KindRoute)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	tiers, err := st.List(ctx, config.KindTier)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	providerLines, providerWarnings = config.ProviderReport(providers, routes, tiers)
	seat := ""
	if c.seat != nil {
		seat = *c.seat
	}
	actor, actorErr := actorName("", d.getenv, seat)
	for i, warning := range providerWarnings {
		if strings.HasPrefix(warning, "provider=") {
			if actorErr == nil {
				warning += " --actor " + shq(actor)
			} else {
				warning += " --actor <name>"
			}
		}
		providerWarnings[i] = warning + c.again()
	}
	o.Fact("providers", providerLines)
	o.Notes = append(o.Notes, providerWarnings...)
	addr, redisFrom, addrErr := redisAddressFrom(*redisFlag, d.getenv)
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
	line += " redis=" + config.Value(addr) + " redis-from=" + redisFrom
	o.Fact("redis", addr).Fact("redis-from", redisFrom)
	behind := 0
	for _, k := range config.Kinds {
		_, applied, err := rs.Read(ctx, k.Name)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		line += fmt.Sprintf(" %s_applied=%d", k.Name, applied)
		o.Fact(k.Name+"_applied", applied)
		if applied != revs[k.Name] {
			behind++
		}
	}
	if behind > 0 {
		return finish(1, fmt.Sprintf("Redis is not at the store's revision for %d kind(s)", behind), toolName+" apply"+c.again())
	}
	return finish(0, "", "")
}
