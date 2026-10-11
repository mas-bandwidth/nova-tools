package up

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// Tool is nova-up on pkg/tool, over the machine machine returns
// (Local on a real machine, a fake in a test). The one verb, up, is the
// default, so `nova-up --local` is `nova-up up --local` (docs/SPEC-UP.md).
func Tool(stamp string, machine func() (Machine, error)) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-up",
		What:  "sets up nova on one machine, from nothing to a first sprint",
		Stamp: stamp,
		How: `steps in order: platform, dirs, binaries, sprint, secrets, redis, seat, smoke.
each step plans first, one line: UP <step> <ok|create|change|missing> <detail>; then
apply runs every step not ok. A missing step (a program, the system) stops the run before
anything is written, naming the install command. State lives under one root (~/nova).
first run: needs git, redis, sops, age and the nova tools on PATH; nothing else.`,
		ExitTable: "0 done or nothing to change, 1 a step is missing or failed (the line names it), 2 could not run (a flag).",
		Words:     []string{"UNCHANGED"},
		Default:   "up",
		Verbs: []tool.Verb{{
			Name:    "up",
			Usage:   "--local [--root <dir>] [--dry-run] [--json]",
			Example: "--local --dry-run --root ./nova-try\nup -h\nversion",
			Effect: tool.LocalWrite + "; it writes under --root, one unit file in the service manager's directory, " +
				"and starts that unit; --dry-run reads (PATH, each tool's version, the secrets store's names) and writes nothing",
			Detail: `--local is the one mode: one machine, zero config. Steps, in order:
platform (darwin or linux), dirs (the root), binaries (each tool on PATH answering its version),
sprint (the twin store mem:<root>/stores/sprint.twin), secrets (a nova-secrets store for the seat
coordinator), redis (a loopback Redis as a supervised loop, its ACL passwords sealed, never
printed), seat (<root>/seat.env), smoke (one card landed on its own twin). A second run over an
applied machine prints ok on every line and UP UNCHANGED.`,
			DryRun: true,
			Flags: func(f *tool.Flags) {
				f.Bool("local", false, "set up this one machine with no config (the one mode; required)")
				f.String("root", "", "the directory everything is written under, made if absent (default ~/nova)")
				f.Check(func(c *tool.Call) {
					if !c.Bool("local") {
						c.Problem("--local is required; it wants nothing after it: the mode that sets up this one machine with no config")
					}
					if f.NArg() > 0 {
						c.Problem(fmt.Sprintf("takes no positional arguments, got %q; the root is --root <dir>", f.Arg(0)))
					}
				})
			},
			Run: func(c *tool.Call) *tool.Out { return run(c, machine) },
		}},
	}
}

func run(c *tool.Call, machine func() (Machine, error)) *tool.Out {
	dry := c.DryRun()
	m, err := machine()
	if err != nil {
		return tool.Refuse("this machine's home directory could not be read: " + err.Error())
	}
	root := c.Str("root")
	if root == "" {
		root = filepath.Join(m.Home, "nova")
	}
	if root, err = filepath.Abs(root); err != nil {
		return tool.Refuse("--root wants a directory path: " + err.Error())
	}
	r := Up(&Env{Machine: m, Ctx: context.Background(), Root: root}, dry)
	var lines []string
	for _, l := range r.Lines {
		lines = append(lines, l.String())
	}
	payload := strings.Join(lines, "\n")
	var o *tool.Out
	switch {
	case len(r.Missing) > 0:
		o = tool.Fail(fmt.Sprintf("missing: %s; nothing was applied; install what the missing line names and run again", strings.Join(r.Missing, ",")))
	case r.Err != nil:
		o = tool.Fail(r.Err.Error() + "; the steps before it are applied, and a run again starts from what they left")
	case r.Changes() == 0:
		o = tool.Done().As("UNCHANGED").Note("nothing to change: every step is ok")
	case dry:
		o = tool.Done().Note("dry run: nothing written; run it without --dry-run to apply")
	default:
		o = tool.Done().Note("load the coordinator's seat in a shell: set -a; . " + filepath.Join(root, SeatFile) + "; set +a")
	}
	o.Payload = payload
	return o.Fact("root", root).Fact("steps", len(r.Lines)).Fact("changes", r.Changes()).Fact("applied", r.Applied)
}
