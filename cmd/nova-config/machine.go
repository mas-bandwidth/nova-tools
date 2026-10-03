package main

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// Exit codes of machine self. They are the family's: 0 done, 2 a finding (the
// name is no machine row) or an invocation that could not run, 3 the name or
// the config could not be read.
const exitCannotRead = 3

// runMachineSelf is `machine self [--check]`. Kept as its own printer: the
// text form is the bare name, which the skeleton's OK line cannot be, and
// --json is the same machine fact the lines already carried (STANDARD §2,
// one value; the skeleton's Payload would move it).
func runMachineSelf(c *tool.Call, d deps) *tool.Out {
	const verb = "machine self"
	cannotRead := func(what, next string) *tool.Out {
		fmt.Fprintf(c.Stderr, "%s %s REFUSED: %s; run: %s\n", toolName, verb, plain(what), next)
		return tool.Exit(exitCannotRead)
	}
	name, _, err := config.SelfName(context.Background(), config.SelfSource{Getenv: d.getenv, Hostname: d.hostname, Tailscale: d.tailscale})
	if err != nil {
		return cannotRead(err.Error(), envMachine+"=<the name of this machine's row> "+toolName+" "+verb)
	}
	if c.Bool("check") {
		dsn, err := (conn{pg: c.Str("pg"), file: c.Str("file")}).dsn(d.getenv)
		if err != nil {
			return cannotRead("the config cannot be read: "+err.Error(), toolName+" "+verb+" -h")
		}
		st, err := d.openStore(context.Background(), dsn)
		if err != nil {
			return cannotRead("the config cannot be read: "+err.Error(), toolName+" "+verb+" -h")
		}
		defer st.Close()
		_, found, err := st.Get(context.Background(), config.KindMachine, name)
		if err != nil {
			return cannotRead("the config cannot be read: "+err.Error(), toolName+" "+verb+" -h")
		}
		if !found {
			fmt.Fprintf(c.Stderr, "%s %s REFUSED: %q is no machine row; run: %s machine add %s --user <login> --seat <seat> --slots <n> --width <n> --as <name>%s\n", toolName, verb, name, toolName, name, again(c))
			return tool.Exit(2)
		}
	}
	if c.Bool("json") {
		o := tool.Done().Fact("machine", name)
		o.Verb = verb
		if c.Bool("check") {
			o.Fact("row", true)
		}
		o.Render(c.Stdout, true)
		return tool.Exit(0)
	}
	fmt.Fprintln(c.Stdout, name)
	return tool.Exit(0)
}

// machineLoops names the loop rows that run on the machine, by name: what
// machine show lists after the machine's own fields (loops=<a,b>, - for none).
func machineLoops(ctx context.Context, st config.Store, machine string) ([]string, error) {
	rows, err := st.List(ctx, config.KindLoop)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if r.Fields["machine"] == machine {
			out = append(out, r.Name)
		}
	}
	return out, nil
}

// runMachineWidth is `machine width <name>`: the width of the sprint's member
// on the machine, the row's width field, and whether it is a member (width
// above 0). It reads the machine rows alone and opens no Redis
// (docs/SPEC-CONFIG.md, "The sprint's width").
func runMachineWidth(c *tool.Call, d deps) *tool.Out {
	const verb = "machine width"
	name := c.Str("name")
	if err := config.ValidateName(name); err != nil {
		return tool.Refuse(err.Error())
	}
	st, _, no := openStore(context.Background(), c, d)
	if no != nil {
		return no
	}
	defer st.Close()
	ws, err := config.Widths(context.Background(), st)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	w, found := config.WidthOf(ws, name)
	if !found {
		o := tool.Fail("machine " + name + " not found")
		o.Remedy = toolName + " machine list" + again(c)
		return o
	}
	return tool.Done().Fact("machine", w.Machine).Fact("width", w.Width).Fact("member", w.Member())
}
