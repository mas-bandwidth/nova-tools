// nova-friend runs a registered harness against the team delivery queue.
// The CLI shape is internal/tool's; delivery and receipt rules belong to
// internal/friend and internal/friendbus.
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// request is a validated invocation; no PID or timer is evidence of a harness.
type request struct {
	Redis, Friend, Consumer, Adapter, Prefix string
	Lease, Block, Retry, Timeout             time.Duration
}

// world injects the runtime boundary so help, refusals and plans never dial.
type world struct {
	ctx          context.Context
	redisDefault string
	hasSeat      bool
	run          func(context.Context, string, request) *tool.Out
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sel := &seatcred.Selection{}
	args, err := sel.FromArgs(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nova-friend REFUSED: %s; run: nova-friend help\n", err)
		os.Exit(2)
	}
	os.Exit(friendTool(realWorld(ctx, sel)).Run(args, os.Stdin, os.Stdout, os.Stderr))
}

// friendTool inherits onboarding, version, JSON and refusal grammar from the
// tool skeleton (STANDARD sections 2 and 3).
func friendTool(w world) *tool.Tool {
	t := &tool.Tool{Name: "nova-friend", What: "delivers team messages to a registered harness and records its receipts", Stamp: version,
		Stage:     "nova-friend is pre-alpha: not ready for production use.",
		How:       "Redis holds the team queue and harness registrations, independent of a sprint.\nAn explicit adapter reaches the actual harness and returns a durable receipt.\nA listener or heartbeat alone never establishes that a harness is awake.\nfirst run: inspect the plans below; real delivery needs Redis and an adapter.",
		ExitTable: "0 done, 1 the harness refused delivery, 2 could not run (input, adapter or store)."}
	for _, name := range []string{"register", "startup", "listen", "serve", "status"} {
		name := name
		v := tool.Verb{Name: name, Usage: name + " --friend <name> --prefix <namespace> --redis <address> [--adapter <file>] [--consumer <id>] [--timeout <duration>]", Effect: tool.Inspection,
			Flags: func(f *tool.Flags) {
				f.Required("friend", "the configured recipient identity")
				f.Required("prefix", "the team queue namespace shared by its publishers")
				f.Redis(w.redisDefault)
				f.Duration("timeout", 30*time.Second, "maximum time for one adapter or store operation")
				if name != "status" {
					f.Required("adapter", "a JSON file containing the explicit harness adapter argv")
					f.String("consumer", "", "this listener's stable queue consumer identity")
					f.Duration("lease", time.Minute, "minimum pending-delivery idle duration before recovery")
					f.Duration("block", 5*time.Second, "maximum queue wait per read")
					f.Duration("retry", time.Second, "delay before retrying a failed delivery")
				}
				f.Check(func(c *tool.Call) {
					if !w.hasSeat {
						c.Want("redis", "the team Redis address, host:port or Unix socket path")
					}
					for _, flag := range []string{"timeout", "lease", "block", "retry"} {
						if name == "status" && flag != "timeout" {
							continue
						}
						if c.Dur(flag) <= 0 {
							c.Problem("--" + flag + " wants a duration greater than zero")
						}
					}
					if (name == "listen" || name == "serve") && c.Str("consumer") == "" {
						c.Problem("--consumer wants this listener's stable queue consumer identity")
					}
				})
			}, Run: func(c *tool.Call) *tool.Out {
				r := request{Redis: c.Str("redis"), Friend: c.Str("friend"), Prefix: c.Str("prefix"), Timeout: c.Dur("timeout")}
				if name != "status" {
					r.Adapter, r.Consumer = c.Str("adapter"), c.Str("consumer")
					r.Lease, r.Block, r.Retry = c.Dur("lease"), c.Dur("block"), c.Dur("retry")
					if c.DryRun() {
						return tool.Done().Fact("friend", r.Friend).Fact("action", name).Note("plan only: no store read, harness call or registration")
					}
				}
				return w.run(w.ctx, name, r)
			}}
		if name != "status" {
			v.Effect = tool.Delivery + "; writes registration or delivery receipts in Redis and calls the explicit harness adapter"
			v.DryRun = true
		}
		switch name {
		case "register":
			v.Example = "register --friend reader --prefix example --redis unused:1 --adapter adapter.json --dry-run"
		case "startup":
			v.Example = "startup --friend reader --prefix example --redis unused:1 --adapter adapter.json --dry-run"
		case "listen":
			v.Example = "listen --friend reader --prefix example --consumer worker --redis unused:1 --adapter adapter.json --dry-run"
		case "status":
			v.Flags = statusFlags(w.redisDefault, w.hasSeat)
			v.Detail = "Registration reports adapter evidence; it does not claim a heartbeat or process is awake."
		}
		t.Verbs = append(t.Verbs, v)
	}
	t.Verbs = append(t.Verbs, watchVerb(w.ctx))
	return t
}

// statusFlags declares the bounded read without requiring a harness adapter.
func statusFlags(addr string, hasSeat bool) func(*tool.Flags) {
	return func(f *tool.Flags) {
		f.Required("friend", "the configured recipient identity")
		f.Required("prefix", "the team queue namespace shared by its publishers")
		f.Redis(addr)
		f.Duration("timeout", 30*time.Second, "maximum time for one store operation")
		f.Max()
		f.Check(func(c *tool.Call) {
			if !hasSeat {
				c.Want("redis", "the team Redis address, host:port or Unix socket path")
			}
			if c.Dur("timeout") <= 0 {
				c.Problem("--timeout wants a duration greater than zero")
			}
		})
	}
}
