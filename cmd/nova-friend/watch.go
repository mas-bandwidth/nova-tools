package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friendwatch"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// watchVerb owns one blocking wait; it never registers native delivery capability.
// STANDARD section 2: direct argv and errors are explicit and bounded.
func watchVerb(ctx context.Context) tool.Verb {
	return tool.Verb{Name: "watch", Usage: "watch --server <host:port> --friend <name> --argv '<JSON argv>' [--sprint <executable>]", Effect: tool.Delivery + "; beats sprint presence while the invoking harness owns a blocking command", DryRun: true,
		Detail: "The child command runs directly, without a shell. Child output passes through.\nPresence ends when the command, invocation, or invoking parent ends.\n--stdin-lifetime owns and closes stdin on return; enable only for a closable pipe kept open by the harness.\nOn Darwin/Linux, stop cleans the owned command group; other targets cancel only the direct child.\nPresence does not establish native wake or business acceptance.",
		Flags: func(f *tool.Flags) {
			f.Required("server", "the actual sprint server address")
			f.Required("friend", "the registered friend identity")
			f.Required("argv", "a JSON array of direct blocking command arguments")
			f.String("sprint", "nova-sprint", "the sprint executable")
			f.Duration("every", time.Second, "presence interval; interval plus beat timeout must be at most 7.5s")
			f.Duration("timeout", 3*time.Second, "maximum time per presence beat; together with interval at most 7.5s")
			f.Bool("stdin-lifetime", false, "stop when the harness-owned stdin pipe closes")
			f.Prints()
			f.Check(func(c *tool.Call) {
				var argv []string
				if json.Unmarshal([]byte(c.Str("argv")), &argv) != nil || len(argv) == 0 || argv[0] == "" {
					c.Problem("--argv wants a nonempty JSON array naming a direct executable")
				}
				if err := friendwatch.CheckTiming(c.Dur("every"), c.Dur("timeout")); err != nil {
					c.Problem(err.Error())
				}
			})
		}, Run: func(c *tool.Call) *tool.Out {
			if c.DryRun() {
				return tool.Done().Fact("friend", c.Str("friend")).Note("plan only: no child or presence beat")
			}
			var argv []string
			if err := json.Unmarshal([]byte(c.Str("argv")), &argv); err != nil {
				return tool.Refuse(err.Error())
			}
			err := friendwatch.Run(ctx, friendwatch.Options{Sprint: c.Str("sprint"), Server: c.Str("server"), Friend: c.Str("friend"), Argv: argv, Every: c.Dur("every"), Timeout: c.Dur("timeout"), Parent: os.Getppid(), StdinLifetime: c.Bool("stdin-lifetime"), Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr})
			if err != nil {
				o := tool.Fail(err.Error())
				o.Remedy = "check the sprint server, registered friend and wait command, then run nova-friend watch -h"
				return o
			}
			return tool.Done().Fact("friend", c.Str("friend")).Note("owned wait completed; presence stopped")
		}}
}
