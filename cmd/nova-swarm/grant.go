package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/hookgate"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// cmdGrant uses the command skeleton for the grant verbs (tla/BenchGrant.tla).
// end/extend accept the documented positional id as well as --id.
func cmdGrant(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 && (args[0] == "end" || args[0] == "extend") && !strings.HasPrefix(args[1], "-") {
		args = append([]string{args[0], "--id", args[1]}, args[2:]...)
	}
	t := &tool.Tool{Name: "nova-swarm", What: "record server-clock bench grants and push their expiry", Stamp: version,
		How:       "Grants are hashes in the bus Redis. Changes wake the watcher; Redis time sets every deadline.\nThe watcher sends one expiry to the grantee and the hooked coordinator, with its told mark in the same write.\nSlot leases remain separate. A stale sender must resume within the bus's seven-day deduplication retention.",
		ExitTable: "0 done; 1 a send or watcher failed; 2 usage or a store that did not answer"}
	for _, verb := range []struct {
		name, usage, example string
		effect               tool.Effect
	}{
		{"add", "add --bench <name> --to <name> --for <duration>", "add --bench bench-a --to worker --for 30m", tool.Delivery},
		{"extend", "extend <id> --for <duration>", "extend --id recorded-id --for 30m", tool.Delivery},
		{"end", "end <id>", "end --id recorded-id", tool.Delivery},
		{"list", "list", "list", tool.Inspection},
		{"watch", "watch", "watch", tool.Delivery},
	} {
		action := verb.name
		t.Verbs = append(t.Verbs, tool.Verb{Name: "grant " + action, Usage: "grant " + verb.usage + " [--redis <host:port>] [--as <name>]", Example: "grant " + verb.example, Effect: verb.effect,
			Detail: "add/extend/end write the store; list inspects; watch delivers expiry messages until interrupted.\nAn extension after expiry retains the first expiry notice; add a new grant for a new notification lifecycle.",
			Flags: func(f *tool.Flags) {
				f.String("redis", os.Getenv(bus.RedisEnv), "bus store address, host:port (default: NOVA_BUS_REDIS, else applied fleet bus)")
				f.String("as", "", "your identity (default: the store login user)")
				if action == "add" {
					f.Required("bench", "bench name")
					f.Required("to", "grantee's bus name")
				}
				if action == "extend" || action == "end" {
					f.Required("id", "grant id from grant list")
				}
				if action == "add" || action == "extend" {
					f.Duration("for", 0, "duration from store time, at least 1ms, such as 30m")
					f.Check(func(c *tool.Call) {
						if c.Dur("for") < time.Millisecond {
							c.Problem("--for wants a duration of at least 1ms, such as 30m")
						}
					})
				}
				if action == "list" {
					f.Max()
				}
			}, Run: func(c *tool.Call) *tool.Out { return runGrant(c, action) },
		})
	}
	return t.Run(append([]string{"grant"}, args...), nil, stdout, stderr)
}

// runGrant gates the acting identity before any grant operation. Store TIME
// and atomic mutations implement tla/BenchGrant.tla, Add/Extend/End.
func runGrant(c *tool.Call, action string) *tool.Out {
	addr, why := bus.Address(c.Ctx, c.Str("redis"), os.Getenv, func(ctx context.Context, addr string) (string, error) { return bus.ReadFleetBus(ctx, addr, os.Getenv) })
	if why != "" {
		return tool.Refuse(why)
	}
	st, login, closeStore, err := bus.OpenRedis(c.Ctx, addr, os.Getenv)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	defer closeStore()
	// Closing this command's own client interrupts an otherwise infinite XREAD.
	stopClose := context.AfterFunc(c.Ctx, closeStore)
	defer stopClose()
	as, why := bus.Identity(c.Str("as"), login)
	if why != "" {
		return tool.Refuse(why)
	}
	if out := hookgate.Out(hookgate.Gate(c.Ctx, st, hook.Want{Name: as, Need: []hook.Push{hook.Bus}})); out != nil {
		return out
	}
	store := swarm.RedisGrantStore{C: st.C}
	if action == "watch" {
		return watchGrants(c, store, st, as)
	}
	if action == "list" {
		snap, err := store.Snapshot(c.Ctx)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		out := tool.Done().Fact("total", len(snap.Grants))
		for _, g := range snap.Grants {
			grantItem(out, g)
		}
		return out
	}
	g := swarm.BenchGrant{By: as}
	duration := time.Duration(0)
	if action == "add" {
		g.Bench, g.Grantee = c.Str("bench"), c.Str("to")
	} else {
		g.ID = c.Str("id")
	}
	if action != "end" {
		duration = c.Dur("for")
	}
	g, err = store.Change(c.Ctx, action, g, duration)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	out := tool.Done()
	grantItem(out, g)
	return out
}

// watchGrants selects the current proven coordinator as the seat, like the
// hook's notification audience, then runs BenchGrant Wake/Sweep/Send.
func watchGrants(c *tool.Call, store swarm.RedisGrantStore, st bus.Redis, as string) *tool.Out {
	hs := hook.Store{C: st.C, Timeout: bus.CallTimeout}
	names, err := hs.Names(c.Ctx)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	states, _, err := hs.Read(c.Ctx, names...)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	var seats []string
	for _, s := range states {
		if s.Role == hook.Coordinator && s.State == hook.StateProven && s.Down == "" {
			seats = append(seats, s.Name)
		}
	}
	sort.Strings(seats)
	if len(seats) == 0 {
		return tool.Refuse("grant watch needs a hooked coordinator to receive expiry; hook the seat with nova-hook hook --as <name> --role coordinator")
	}
	watcher := swarm.GrantWatcher{Store: store, Bus: &bus.Bus{Store: st}, From: as, Seat: seats[0]}
	err = watcher.Watch(c.Ctx, func(err error) {
		out := tool.Fail(err.Error())
		out.Verb = "grant watch"
		out.Render(c.Stderr, false)
	})
	if errors.Is(err, context.Canceled) || c.Ctx.Err() != nil {
		return tool.Done().Fact("stopped", true)
	}
	return tool.Fail(fmt.Sprintf("grant watcher stopped: %s", oneline.Err(err)))
}

func grantItem(out *tool.Out, g swarm.BenchGrant) {
	out.Item("grant", "id", g.ID, "bench", g.Bench, "grantee", g.Grantee, "by", g.By,
		"granted", g.Granted.Format(time.RFC3339Nano), "deadline", g.Deadline.Format(time.RFC3339Nano), "ended", g.Ended, "told", g.Told)
}
