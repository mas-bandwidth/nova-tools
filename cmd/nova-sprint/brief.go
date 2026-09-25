// The brief verb registers itself through the S0 registry (registry.go), so
// adding it never edits main.go (nova-tools#3154). brief render renders a
// child brief from the task record in two sequential round trips and records
// its digests through ns_task_brief; brief lint refuses a brief that
// disagrees with the task record. Both are calls into internal/nsprint/brief.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "brief",
		Summary: "render a child brief from its task record, or lint a brief against it",
		Run:     runBrief,
	})
}

func runBrief(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "brief", "want render --task <sprint>/<id> [--out <file>] or lint <file> [--task <sprint>/<id>]")
	}
	verbflag.HelpIfAsked(args[1:], "brief "+args[0], "task", "out", "redis")
	flags, rest, err := briefArgs(args[1:])
	if err != nil {
		return refuse(errOut, "brief "+args[0], err.Error())
	}
	switch args[0] {
	case "render":
		if len(rest) != 0 || flags["task"] == "" {
			return refuse(errOut, "brief render", "want render --task <sprint>/<id> [--out <file>] [--redis <addr>]")
		}
		st, err := store.Open(ctx, flags["redis"])
		if err != nil {
			return refuse(errOut, "brief render", err.Error())
		}
		defer func() { _ = st.Close() }()
		return brief.Render(ctx, st, flags["task"], flags["out"], out, errOut)
	case "lint":
		if len(rest) != 1 || flags["out"] != "" {
			return refuse(errOut, "brief lint", "want lint <file> [--task <sprint>/<id>] [--redis <addr>]")
		}
		st, err := store.Open(ctx, flags["redis"])
		if err != nil {
			return refuse(errOut, "brief lint", err.Error())
		}
		defer func() { _ = st.Close() }()
		return brief.Lint(ctx, st, rest[0], flags["task"], out, errOut)
	default:
		return refuse(errOut, "brief", fmt.Sprintf("unknown subverb %s; want render or lint", args[0]))
	}
}

// briefArgs reads --task, --out and --redis (as `--f v` or `--f=v`) wherever
// they sit, so `lint <file> --task x` parses the way the spec prints it.
func briefArgs(args []string) (map[string]string, []string, error) {
	flags := map[string]string{}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch name {
		case "task", "out", "redis":
		default:
			return nil, nil, fmt.Errorf("unknown flag %s", a)
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag %s needs a value", a)
			}
			i++
			val = args[i]
		}
		flags[name] = val
	}
	return flags, rest, nil
}
