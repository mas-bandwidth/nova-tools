package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// The machine kind's two queries beside its six verbs: self, the machine's
// own name, and width, the room the sprint's member on a machine has. Both
// read the one inventory and type nothing into it (docs/SPEC-CONFIG.md, "The
// sprint's width"; internal/config/kind.go, the machine kind's four fields).

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
	pg, _, _ := connFlags(fs, false, false)
	check := fs.Bool("check", false, "read the machine rows and exit 2 when this machine's name is none of them (exit 3 when the rows cannot be read); without it no store is opened")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "self takes no name; flags only")
	}
	name, _, err := config.SelfName(ctx, config.SelfSource{Getenv: d.getenv, Hostname: d.hostname, Tailscale: d.tailscale})
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: %s=<the name of this machine's row> %s %s\n", tool, verb, err.Error(), envMachine, tool, verb)
		return exitCannotRead
	}
	if *check {
		dsn, err := pgDSN(*pg, d.getenv)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		st, err := d.openStore(ctx, dsn)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s\n", tool, verb, oneLine(err))
			return exitCannotRead
		}
		defer st.Close()
		_, found, err := st.Get(ctx, config.KindMachine, name)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s\n", tool, verb, oneLine(err))
			return exitCannotRead
		}
		if !found {
			fmt.Fprintf(stderr, "%s %s: %q is no machine row; run: %s machine add %s --user <login> --seat <seat> --slots <n> --as <friend>\n", tool, verb, name, tool, name)
			return 2
		}
	}
	fmt.Fprintln(stdout, name)
	return 0
}

func oneLine(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }

// widths reads every machine row's width: the rows from the store, and the
// friends' beats from Redis when a friend row carries slots (Redis is given
// by --redis, else NOVA_SPRINT_REDIS, else NOVA_REDIS_ADDR).
func widths(ctx context.Context, st config.Store, redisFlag string, d deps) ([]config.MachineWidth, error) {
	var hosts config.HostReader
	if addr := liveRedisAddress(redisFlag, d.getenv); addr != "" {
		rs, err := d.openRedis(ctx, addr)
		if err != nil {
			return nil, err
		}
		defer rs.Close()
		hosts = rs
	}
	return config.Widths(ctx, st, hosts)
}

// widthJSON is one machine's width as a program reads it.
type widthJSON struct {
	Machine string `json:"machine"`
	Slots   int    `json:"slots"`
	Charged int    `json:"charged"`
	Width   int    `json:"width"`
	Member  bool   `json:"member"`
}

func toJSON(w config.MachineWidth) widthJSON {
	return widthJSON{Machine: w.Machine, Slots: w.Slots, Charged: w.Charged, Width: w.Width, Member: w.Member()}
}

// runMachineWidth is `machine width <name>`: the width of the sprint's member
// on the machine, its slots less the friend slots charged to it, and whether
// it is a member (width above 0).
func runMachineWidth(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "machine width"
	fs := verbflag.New(verb)
	pg, redisFlag, _ := connFlags(fs, true, false)
	asJSON := fs.Bool("json", false, "print one JSON object for a program instead of the line")
	name, rest := nameAndRest(mustMachine(), args)
	if err := fs.Parse(rest); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return refuse(stderr, verb, "want "+verb+" <name>")
	}
	if err := config.ValidateName(name); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	ws, err := widths(ctx, st, *redisFlag, d)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	w, found := config.WidthOf(ws, name)
	if !found {
		return refused(stderr, verb, "machine "+name+" not found", tool+" machine list")
	}
	if *asJSON {
		out, _ := json.Marshal(toJSON(w))
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

// machineJSON is one machine row of `machine list --json`: its declared
// fields and its width.
type machineJSON struct {
	widthJSON
	User    string `json:"user"`
	Seat    string `json:"seat"`
	Runners int    `json:"runners"`
}

// printMachinesJSON is `machine list --json`: one array, in name order.
func printMachinesJSON(ctx context.Context, st config.Store, rows []config.Row, redisFlag string, stdout, stderr io.Writer, d deps) int {
	const verb = "machine list"
	ws, err := widths(ctx, st, redisFlag, d)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	out := make([]machineJSON, 0, len(rows))
	for _, r := range rows {
		w, _ := config.WidthOf(ws, r.Name)
		out = append(out, machineJSON{widthJSON: toJSON(w), User: r.Fields["user"], Seat: r.Fields["seat"], Runners: r.Int("runners")})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
