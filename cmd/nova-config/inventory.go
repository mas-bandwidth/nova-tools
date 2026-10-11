// inventory.go holds the inventory verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
)

// localHost is the machine row this process runs on, which inventory marks
// ansible_connection=local. explicit is true when the env NOVA_MACHINE named
// it (matched by exact machine name, refused when no row has it); otherwise
// it is the lower-cased first label of the hostname (machine names are lower-case), matched the same way, and nothing is
// marked when no row has it.
func localHost(getenv func(string) string, hostname func() (string, error)) (name string, explicit bool) {
	if s := getenv(envMachine); s != "" {
		return s, true
	}
	if h, err := hostname(); err == nil {
		return strings.ToLower(strings.Split(h, ".")[0]), false
	}
	return "", false
}

// inventoryTimeout is --timeout's default, 10 s: how long inventory waits
// for the store in all, the connection and the two reads.
const inventoryTimeout = 10 * time.Second

// inventoryExample is the minimal fixture --example prints and --fixture
// reads back: one machine and the fleet row it needs. A reader with no store
// and no source tree runs the verb on it (docs/STANDARD.md, section 2: a tool
// runs on its own small input with no infrastructure).
const inventoryExample = `machines:
  m1: {user: nova, seat: m1, slots: 1}
fleet: {store: m1, coordinator: m1, redis_port: 6380, pg_dsn: postgres://nova@localhost:5432/nova, loops_dir: "~/nova-bench/loops"}
`

// runInventory prints the Ansible inventory of the applied state: the
// Redis view apply wrote (machines, the fleet row, loops, the machines'
// beats), never Postgres, or a fixture file in its place (docs/FLEET.md).
func runInventory(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "inventory"
	fs := verbflag.New(verb)
	redisFlag := fs.String("redis", "", "the Redis `host:port` of the applied state (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); exclusive with --fixture")
	fixture := fs.String("fixture", "", "a YAML or JSON `file` of machines, the fleet row, loops and each machine's os and arch, read in place of the store; print a minimal one with --example; opens no store")
	example := fs.Bool("example", false, "print a minimal fixture to stdout and open no store; save it and read it back with --fixture")
	list := fs.Bool("list", false, "print the whole inventory (hosts, groups and every host's variables under _meta.hostvars, so ansible never calls --host); the default when neither --list nor --host is given; exclusive with --host")
	host := fs.String("host", "", "print the variables of one machine, by `name`, as a JSON object; exits 1 when no machine row has that name")
	timeout := fs.Duration("timeout", inventoryTimeout, "a Go `duration`, above 0: how long to wait for the store before refusing; ansible runs the verb unattended, so it never waits forever")
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	// --example is the print-and-exit action -h is: it writes the fixture and
	// reads nothing, whatever else the line carries.
	if *example {
		fmt.Fprint(stdout, inventoryExample)
		return 0
	}
	given := map[string]bool{}
	fs.Visit(func(f *stdflag.Flag) { given[f.Name] = true })
	var problems []string
	if fs.NArg() > 0 {
		problems = append(problems, "inventory takes no arguments; flags only")
	}
	if given["host"] && *host == "" {
		problems = append(problems, "--host wants a machine name and got an empty value")
	}
	if *list && given["host"] {
		problems = append(problems, "--list and --host are exclusive: --list prints every host, --host prints one")
	}
	if *timeout <= 0 {
		problems = append(problems, "--timeout wants a Go duration above 0, like 10s")
	}
	if given["fixture"] && *fixture == "" {
		problems = append(problems, "--fixture wants a file and got an empty value")
	}
	if *fixture != "" && *redisFlag != "" {
		problems = append(problems, "--fixture and --redis are exclusive: the fixture stands in for the store")
	}
	var addr string
	if *fixture == "" {
		var err error
		if addr, err = redisAddress(*redisFlag, d.getenv); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	// again is the command that repeats this run with every input kept.
	again := func(extra ...string) string {
		parts := []string{toolName, verb}
		if *redisFlag != "" {
			parts = append(parts, "--redis", shq(*redisFlag))
		}
		if *fixture != "" {
			parts = append(parts, "--fixture", shq(*fixture))
		}
		if *list {
			parts = append(parts, "--list")
		}
		if given["host"] {
			parts = append(parts, "--host", shq(*host))
		}
		return strings.Join(append(parts, extra...), " ")
	}
	var snap *config.Snapshot
	if *fixture != "" {
		var err error
		if snap, err = config.LoadFixture(*fixture); err != nil {
			return refuse(stderr, verb, err.Error())
		}
	} else {
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		stage := "connecting"
		// fail is a store failure: the deadline, or the store's own words.
		fail := func(err error) int {
			if ctx.Err() != nil {
				return refuseLine(stderr, verb, fmt.Sprintf("timed out after %s waiting for the store at %s while %s; check that Redis answers there; run: %s", *timeout, addr, stage, again("--timeout", (*timeout*3).String())), 2)
			}
			return refuse(stderr, verb, err.Error())
		}
		rs, err := d.openRedis(ctx, addr)
		if err != nil {
			return fail(err)
		}
		defer func() { _ = rs.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
		stage = "reading the applied state"
		if snap, err = rs.Snapshot(ctx); err != nil {
			return fail(err)
		}
	}
	self, explicit := localHost(d.getenv, d.hostname)
	inv, err := config.BuildInventory(snap, self)
	if err != nil {
		if what, next, has := strings.Cut(err.Error(), "; run: "); has && strings.HasPrefix(what, "fleet:") {
			return refused(stderr, verb, what, next)
		}
		return refused(stderr, verb, err.Error(), again())
	}
	if explicit && !inv.Has(self) {
		return refused(stderr, verb, fmt.Sprintf("%s=%q names no machine row (the name is matched exactly); known machines: %s", envMachine, self, boundedNames(inv.All.Hosts, maxKnownNames)), toolName+" machine list")
	}
	if given["host"] {
		data, err := inv.HostJSON(*host)
		var unknown *config.UnknownHostError
		if errors.As(err, &unknown) {
			return refused(stderr, verb, fmt.Sprintf("--host %q names no machine row; known machines: %s", unknown.Name, boundedNames(unknown.Known, maxKnownNames)), toolName+" machine list")
		}
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	data, err := inv.JSON()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// maxKnownNames bounds the machine names a refusal lists.
const maxKnownNames = 20

// boundedNames lists at most max names, then how many more there are, and
// "none" for an empty list.
func boundedNames(names []string, max int) string {
	if len(names) == 0 {
		return "none"
	}
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:max], ", "), len(names)-max)
}
