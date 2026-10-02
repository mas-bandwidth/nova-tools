package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The machine kind's two queries beside its six verbs: self, the machine's
// own name, and width, the width of the sprint's member on a machine. Both
// read the one inventory and type nothing into it (docs/SPEC-CONFIG.md, "The
// sprint's width"; internal/config/kind.go, the machine kind's fields).

// Exit codes of machine self. They are the family's: 0 done, 2 a finding (the
// name is no machine row) or an invocation that could not run, 3 the name or
// the config could not be read.
const exitCannotRead = 3

// runMachineSelf is `machine self [--check]`: it prints this machine's own
// name as the config keys it (NOVA_MACHINE, else the tailnet's name for the
// host when a tailnet is running, else the hostname's first label), and
// reads no store. With --check it reads the machine rows and exits 2 when
// the name is not one of them, so a member agent learns its own name and
// whether the inventory knows it in one command.
func runMachineSelf(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "machine self"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	check := fs.Bool("check", false, "read the machine rows and exit 2 when this machine's name is none of them (exit 3 when the rows cannot be read); without it no store is opened")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "self takes no name; flags only")
	}
	// cannotRead is the exit 3 line: the name or the rows could not be read.
	cannotRead := func(what, next string) int {
		o := tool.Refuse(plain(what))
		o.Exit, o.Remedy = exitCannotRead, next
		return answer(stderr, verb, o, fmt.Sprintf("%s %s REFUSED: %s; run: %s", toolName, verb, plain(what), next))
	}
	name, _, err := config.SelfName(ctx, config.SelfSource{Getenv: d.getenv, Hostname: d.hostname, Tailscale: d.tailscale})
	if err != nil {
		return cannotRead(err.Error(), envMachine+"=<the name of this machine's row> "+toolName+" "+verb)
	}
	if *check {
		// under --check exit 2 is "no such row" alone: no store to read is 3
		dsn, err := c.dsn(d.getenv)
		if err != nil {
			return cannotRead("the config cannot be read: "+err.Error(), helpFor(verb))
		}
		st, err := d.openStore(ctx, dsn)
		if err != nil {
			return cannotRead("the config cannot be read: "+err.Error(), helpFor(verb))
		}
		defer st.Close()
		_, found, err := st.Get(ctx, config.KindMachine, name)
		if err != nil {
			return cannotRead("the config cannot be read: "+err.Error(), helpFor(verb))
		}
		if !found {
			what, next := fmt.Sprintf("%q is no machine row", name), fmt.Sprintf("%s machine add %s --user <login> --seat <seat> --slots <n> --width <n> --as <name>%s", toolName, name, c.again())
			o := tool.Fail(what)
			o.Exit, o.Remedy = 2, next
			return answer(stderr, verb, o, fmt.Sprintf("%s %s REFUSED: %s; run: %s", toolName, verb, what, next))
		}
	}
	if *asJSON {
		o := tool.Done().Fact("machine", name)
		o.Verb = verb
		if *check {
			o.Fact("row", true)
		}
		return emit(stdout, o)
	}
	fmt.Fprintln(stdout, name)
	return 0
}

// machineLoops names the loop rows that run on the machine, by name: what
// machine show lists after the machine's own fields (loops=<a,b>, - for
// none).
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

// widthJSON is one machine's width as a program reads it.
type widthJSON struct {
	Machine string `json:"machine"`
	Width   int    `json:"width"`
	Member  bool   `json:"member"`
}

func toJSON(w config.MachineWidth) widthJSON {
	return widthJSON{Machine: w.Machine, Width: w.Width, Member: w.Member()}
}

// runMachineWidth is `machine width <name>`: the width of the sprint's member
// on the machine, the row's width field, and whether it is a member (width
// above 0). It reads the machine rows alone and opens no Redis.
func runMachineWidth(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "machine width"
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	asJSON := fs.Bool("json", false, "print one JSON object for a program instead of the line: {\"machine\",\"width\",\"member\"}")
	name, rest := nameAndRest(mustMachine(), args)
	if code, ok := parse(fs, rest, stderr, verb); !ok {
		return code
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return refuse(stderr, verb, "want "+verb+" <name>")
	}
	if err := config.ValidateName(name); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	ws, err := config.Widths(ctx, st)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	w, found := config.WidthOf(ws, name)
	if !found {
		return refused(stderr, verb, "machine "+name+" not found", toolName+" machine list"+c.again())
	}
	if *asJSON {
		out, err := json.Marshal(toJSON(w))
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintln(stdout, w.Line())
	return 0
}

func mustMachine() *config.Kind {
	k, ok := config.Lookup(config.KindMachine)
	if !ok {
		panic(errors.New("the machine kind is not registered"))
	}
	return k
}
