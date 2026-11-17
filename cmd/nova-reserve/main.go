// Command nova-reserve arbitrates fleet and single-machine resources across
// peer friends, workers, CI runners, people, scripts, and nova-sprint.
package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const poll = time.Second

const banner = `nova-reserve: client/server fleet and local resource arbitrator

Peers connect to nova-reserve serve over Tailscale TCP ($NOVA_RESERVE_ADDR) or a
local Unix domain socket (--local, /var/tmp/nova-reserve/sock) using one
newline-delimited JSON protocol.

  nova-reserve serve [--local] [--listen ADDR] [--socket PATH] [--reconnect-grace D] [--idle-exit D] [--dir DIR]
  nova-reserve run [--host M] [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--source S] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--addr ADDR] [--dir DIR] -- <cmd> [args...]
  nova-reserve [flags] -- <cmd> [args...]
  nova-reserve hold [--host M] [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--source S] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--json] [--addr ADDR] [--dir DIR]
  nova-reserve ssh [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--source S] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--addr ADDR] [--dir DIR] -- <cmd> [args...]
  nova-reserve claim [--host M] [--needs SPEC | --exclusive] [--ttl D] [--source S] [--why TEXT] [--json] [--addr ADDR] [--dir DIR]
  nova-reserve release <lease_id> [--addr ADDR] [--dir DIR]
  nova-reserve status [--host M] [--json] [--addr ADDR] [--dir DIR]
  nova-reserve subscribe [--host M] [--addr ADDR] [--dir DIR]
  nova-reserve bench [--force] [--write-config] [--json] [--dir DIR]
  nova-reserve set [--cpu N] [--memory G] [--cores C] [--tags A,B] [--resources K=N,...] [--dir DIR]
  nova-reserve help [serve|run|hold|ssh|claim|release|status|subscribe|bench|set]

Exit: 0 ok; 1 refused (impossible, NO ROOM, timeout, RESERVE DEADLOCK, busy bench);
2 bad invocation or setup/connection fault; 126/127 command launch failure; else
the command's exit status.
`

var verbHelp = map[string]string{
	"serve": `nova-reserve serve [--local] [--listen ADDR] [--socket PATH] [--reconnect-grace D] [--idle-exit D] [--dir DIR]

Run the central fleet TCP arbitrator (reading config.machines from $NOVA_PG_DSN
and authenticating peers with $NOVA_RESERVE_PASSWORD) or the standalone local
Unix-socket arbitrator (--local).
  --local              serve single-machine Unix socket instead of fleet TCP
  --listen ADDR        TCP listen address (default :7680)
  --socket PATH        Unix socket path for --local (default DIR/sock)
  --reconnect-grace D  startup reconnect window before new non-CI grants (default 15s fleet, 0s local)
  --idle-exit D        exit cleanly after D with zero peers/leases/claims (0 disables)
  --dir DIR            local state directory (default /var/tmp/nova-reserve)
`,
	"run": `nova-reserve [run] [--host M] [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--source S] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--addr ADDR] [--dir DIR] -- <cmd> [args...]

Acquire a lease on this machine, steer CPU count, Linux affinity, and OS
priority, run the command, and release the lease; exit with the command's status.
  --host M       target machine name (default: this host)
  --needs SPEC   comma-separated tags and resource amounts, e.g. "cpu 4, memory 8"
  --exclusive    hold every resource on the machine
  --nowait       exit 1 (NO ROOM) immediately if resources are not free right now
  --wait D       give up after D (above 0: 90s, 15m), exit 1
  --holder H     friend, bench, ci, person, sprint, or script (default person)
  --source S     caller identity for round-robin fairness (default holder)
  --consumer ID  caller continuity id across steps
  --label L      status category (default ci for --holder ci, else work)
  --priority P   low, normal, high, read, critical, or blocker (default normal)
  --why TEXT     human note shown in status and diagnostics
  --addr ADDR    arbitrator TCP host:port (overrides $NOVA_RESERVE_ADDR)
  --dir DIR      local state directory when standalone (default /var/tmp/nova-reserve)
`,
	"hold": `nova-reserve hold [--host M] [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--source S] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--json] [--addr ADDR] [--dir DIR]

Acquire a fleet-any (no --host) or host-pinned (--host M) lease, print one HELD
line (or granted JSON with --json) on stdout, and hold until stdin EOF or
SIGINT/SIGTERM, reconnecting automatically across arbitrator restarts.
`,
	"ssh": `nova-reserve ssh [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--source S] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--addr ADDR] [--dir DIR] -- <cmd> [args...]

Acquire a fleet-any lease, connect over SSH to the granted machine with
NOVA_RESERVE_LEASE and thread-count steering exported, run <cmd>, and release
the lease when <cmd> exits.
`,
	"claim": `nova-reserve claim [--host M] [--needs SPEC | --exclusive] [--ttl D] [--source S] [--why TEXT] [--json] [--addr ADDR] [--dir DIR]

Place an immediate soft operator claim on a machine (default --ttl 4h). Running
commands finish undisturbed; no new non-CI work starts on claimed resources
until usage drops below the line or nova-reserve release <lease_id> is called.
`,
	"release": `nova-reserve release <lease_id> [--addr ADDR] [--dir DIR]

Release an active claim or lease early by its ID (e.g. C-1 or L-1).
`,
	"status": `nova-reserve status [--host M] [--json] [--addr ADDR] [--dir DIR]

Query fleet or single-machine capacity, free resources, active/paused leases,
soft claims, and queued waiters.
`,
	"subscribe": `nova-reserve subscribe [--host M] [--addr ADDR] [--dir DIR]

Stream the initial snapshot JSON frame followed by live event JSON frames until
interrupted.
`,
	"bench": `nova-reserve bench [--force] [--write-config] [--json] [--dir DIR]

Measure this machine's CPU units with the built-in compiler/test benchmark and
usable slice memory in GiB, write DIR/capacity.json, and optionally record the
result in nova-config (--write-config).
`,
	"set": `nova-reserve set [--cpu N] [--memory G] [--cores C] [--tags A,B] [--resources K=N,...] [--dir DIR]

Atomically create or update DIR/capacity.json for standalone local operation.
`,
}

var allVerbs = []string{"serve", "run", "hold", "ssh", "claim", "release", "status", "subscribe", "bench", "set", "help"}

var verbFlags = map[string][]string{
	"serve":     {"local", "listen", "socket", "reconnect-grace", "idle-exit", "dir"},
	"run":       {"host", "needs", "exclusive", "nowait", "wait", "holder", "source", "consumer", "label", "priority", "why", "addr", "socket", "dir"},
	"hold":      {"host", "needs", "exclusive", "nowait", "wait", "holder", "source", "consumer", "label", "priority", "why", "json", "addr", "socket", "dir"},
	"ssh":       {"host", "needs", "exclusive", "nowait", "wait", "holder", "source", "consumer", "label", "priority", "why", "addr", "socket", "dir"},
	"claim":     {"host", "needs", "exclusive", "ttl", "source", "why", "json", "addr", "socket", "dir"},
	"release":   {"addr", "socket", "dir"},
	"status":    {"host", "json", "addr", "socket", "dir"},
	"subscribe": {"host", "addr", "socket", "dir"},
	"bench":     {"force", "write-config", "json", "dir"},
	"set":       {"cpu", "memory", "cores", "tags", "resources", "dir"},
}

var boolFlags = map[string]bool{
	"local": true, "exclusive": true, "nowait": true, "force": true, "write-config": true, "json": true,
}

var wants = map[string]string{
	"listen":          "a TCP listen address like :7680",
	"socket":          "a Unix socket path",
	"reconnect-grace": "a duration >= 0, like 15s or 0s",
	"idle-exit":       "a duration >= 0, like 10s or 0s",
	"host":            "a machine name",
	"needs":           `a comma-separated resource/tag list like "cpu 4, memory 8"`,
	"wait":            "a duration above 0, like 90s or 15m",
	"ttl":             "a duration above 0, like 4h or 30m",
	"holder":          "one of friend, bench, ci, person, sprint, script",
	"source":          "a non-empty caller name",
	"consumer":        "a non-empty consumer id",
	"label":           "a non-empty label",
	"priority":        "one of low, normal, high, read, critical, blocker",
	"why":             "text",
	"addr":            "a host:port address",
	"dir":             "a directory",
	"cpu":             "an integer >= 1",
	"memory":          "an integer >= 1",
	"cores":           "an integer >= 1",
	"tags":            "comma-separated tag names",
	"resources":       "comma-separated k=n pairs with n >= 1",
}

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func refuse(code int, format string, a ...any) *exitError {
	return &exitError{code, fmt.Sprintf(format, a...)}
}

type options struct {
	verb        string
	rawVerb     string
	help        bool
	local       bool
	listen      string
	socket      string
	reconnGrace time.Duration
	reconnSet   bool
	idleExit    time.Duration
	host        string
	needsRaw    string
	needs       []needItem
	exclusive   bool
	nowait      bool
	wait        time.Duration
	ttl         time.Duration
	holder      string
	source      string
	consumer    string
	label       string
	priority    string
	why         string
	addr        string
	leaseID     string
	force       bool
	writeConfig bool
	jsonOut     bool
	cpu         int
	memory      int
	cores       int
	tags        []string
	tagsSet     bool
	res         map[string]int
	resSet      bool
	dir         string
	dirSet      bool
	dirFrom     string
	argv        []string
	args        []string
}

func (o options) hasSetField() bool {
	return o.cpu > 0 || o.memory > 0 || o.cores > 0 || o.tagsSet || o.resSet
}

func (o options) remedy(args ...string) string {
	if o.addr != "" {
		args = append(args, "--addr", o.addr)
	} else if o.dirSet {
		args = append(args, "--dir", o.dir)
	}
	return "nova-reserve " + quoteAll(args)
}

func helpFor(verb string) string {
	if verb == "" {
		return "nova-reserve -h"
	}
	return "nova-reserve " + verb + " -h"
}

func prefix(verb string) string {
	if verb == "" {
		return "nova-reserve:"
	}
	return "nova-reserve " + verb + ":"
}

func quoteAll(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:=@%+,-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func parse(args []string) (options, error) {
	o := options{
		verb: "run", rawVerb: "", listen: ":" + defaultReservePort,
		holder: "person", priority: "normal", ttl: defaultClaimTTLMs * time.Millisecond,
		dir: systemDir, dirFrom: "the default directory", args: args,
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		o.help, o.verb, o.rawVerb = true, "", ""
		return o, nil
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "serve", "run", "hold", "ssh", "claim", "release", "status", "subscribe", "bench", "set", "help":
			o.verb, o.rawVerb, args = args[0], args[0], args[1:]
		case "measure", "calibrate":
			o.verb, o.rawVerb, args = "bench", args[0], args[1:]
		default:
			return o, refuse(2, "unknown verb %q; verbs: %s; a command goes after --: nova-reserve -- %s; run: nova-reserve help",
				args[0], strings.Join(allVerbs, ", "), shellQuote(args[0]))
		}
	}
	if o.verb == "help" {
		if slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-") }) {
			return o, refuse(2, "help takes no flags; run: nova-reserve help")
		}
		switch {
		case len(args) == 0:
			o.help, o.verb, o.rawVerb = true, "", ""
		case len(args) == 1 && (args[0] == "measure" || args[0] == "calibrate"):
			o.help, o.verb = true, "bench"
		case len(args) == 1 && verbHelp[args[0]] != "":
			o.help, o.verb = true, args[0]
		default:
			return o, refuse(2, "help takes one of %s, not %q; run: nova-reserve help",
				strings.Join(allVerbs[:len(allVerbs)-1], ", "), strings.Join(args, " "))
		}
		return o, nil
	}

	end := slices.Index(args, "--")
	hasDashDash := end >= 0
	if !hasDashDash {
		end = len(args)
	}
	if slices.ContainsFunc(args[:end], func(a string) bool { return a == "-h" || a == "--help" }) {
		o.help = true
		return o, nil
	}

	hint := helpFor(o.rawVerb)
	seen := map[string]bool{}
	for i := 0; i < end; i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			if o.verb == "release" && o.leaseID == "" && a != "-" {
				o.leaseID = a
				continue
			}
			return o, refuse(2, "unexpected %q; a command goes after --; run: %s", a, hint)
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !slices.Contains(verbFlags[o.verb], name) {
			if _, known := wants[name]; known || boolFlags[name] {
				return o, refuse(2, "%s takes no --%s; it takes --%s; run: %s", o.verb, name, strings.Join(verbFlags[o.verb], ", --"), hint)
			}
			return o, refuse(2, "unknown flag %s; run: %s", a, hint)
		}
		if seen[name] {
			return o, refuse(2, "--%s given twice; run: %s", name, hint)
		}
		seen[name] = true
		if boolFlags[name] {
			if hasVal {
				return o, refuse(2, "--%s takes no value, not %q; run: %s", name, val, hint)
			}
			o.local = o.local || name == "local"
			o.exclusive = o.exclusive || name == "exclusive"
			o.nowait = o.nowait || name == "nowait"
			o.force = o.force || name == "force"
			o.writeConfig = o.writeConfig || name == "write-config"
			o.jsonOut = o.jsonOut || name == "json"
			continue
		}
		if !hasVal {
			if i+1 >= end {
				return o, refuse(2, "--%s wants %s; run: %s", name, wants[name], hint)
			}
			i++
			val = args[i]
		}
		if !o.setFlag(name, val) {
			return o, refuse(2, "--%s wants %s, not %q; run: %s", name, wants[name], val, hint)
		}
	}
	if hasDashDash {
		o.argv = args[end+1:]
	}
	if o.label == "" {
		switch {
		case o.verb == "claim":
			o.label = "claim"
		case o.holder == "ci":
			o.label = "ci"
		default:
			o.label = "work"
		}
	}
	switch {
	case (o.verb == "run" || o.verb == "ssh") && len(o.argv) == 0:
		sub := ""
		if o.rawVerb != "" {
			sub = " " + o.rawVerb
		}
		return o, refuse(2, "no command: nova-reserve%s [flags] -- <cmd> [args...]; run: %s", sub, hint)
	case o.verb != "run" && o.verb != "ssh" && hasDashDash:
		return o, refuse(2, "%s takes no command, not %q; run: %s", o.verb, strings.Join(o.argv, " "), hint)
	case o.verb == "release" && o.leaseID == "":
		return o, refuse(2, "release wants <lease_id>; run: %s", hint)
	case o.exclusive && len(o.needs) > 0:
		return o, refuse(2, "--exclusive takes every resource; --needs does not apply; run: %s", hint)
	case o.nowait && o.wait > 0:
		return o, refuse(2, "--nowait and --wait cannot both be given; run: %s", hint)
	case o.verb == "set" && !o.hasSetField():
		return o, refuse(2, "set wants at least one of --cpu, --memory, --cores, --tags, --resources; run: %s", hint)
	}
	return o, nil
}

func (o *options) setFlag(name, val string) bool {
	switch name {
	case "listen":
		o.listen = val
		return val != ""
	case "socket":
		o.socket = val
		return val != ""
	case "reconnect-grace":
		d, err := time.ParseDuration(val)
		o.reconnGrace, o.reconnSet = d, true
		return err == nil && d >= 0
	case "idle-exit":
		d, err := time.ParseDuration(val)
		o.idleExit = d
		return err == nil && d >= 0
	case "host":
		o.host = val
		return val != "" && !strings.ContainsAny(val, " \t\r\n")
	case "needs":
		items, err := parseNeedItems(val)
		o.needs, o.needsRaw = items, val
		return err == nil && strings.TrimSpace(val) != ""
	case "wait":
		d, err := time.ParseDuration(val)
		o.wait = d
		return err == nil && d > 0
	case "ttl":
		d, err := time.ParseDuration(val)
		o.ttl = d
		return err == nil && d > 0
	case "holder":
		o.holder = val
		return slices.Contains([]string{"friend", "bench", "ci", "person", "sprint", "script"}, val)
	case "source":
		o.source = val
		return val != "" && !strings.ContainsAny(val, " \t\r\n")
	case "consumer":
		o.consumer = val
		return val != "" && !strings.ContainsAny(val, " \t\r\n")
	case "label":
		o.label = val
		return identRe.MatchString(val)
	case "priority":
		_, ok := prioRanks[val]
		o.priority = val
		return ok
	case "why":
		o.why = val
		return val != ""
	case "addr":
		o.addr = val
		return val != "" && !strings.ContainsAny(val, " \t\r\n")
	case "dir":
		o.dir, o.dirSet, o.dirFrom = val, true, "--dir"
		return val != ""
	case "cpu", "memory", "cores":
		n, err := strconv.Atoi(val)
		switch name {
		case "cpu":
			o.cpu = n
		case "memory":
			o.memory = n
		case "cores":
			o.cores = n
		}
		return err == nil && n >= 1
	case "tags":
		o.tagsSet, o.tags = true, nil
		if val == "" {
			return true
		}
		seen := map[string]bool{}
		for _, t := range strings.Split(val, ",") {
			t = strings.TrimSpace(t)
			if !identRe.MatchString(t) || t == "cpu" || t == "memory" || seen[t] {
				return false
			}
			seen[t] = true
			o.tags = append(o.tags, t)
		}
		slices.Sort(o.tags)
		return true
	case "resources":
		o.resSet, o.res = true, map[string]int{}
		if val == "" {
			return true
		}
		for _, part := range strings.Split(val, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			n, err := strconv.Atoi(v)
			if !ok || !identRe.MatchString(k) || err != nil || n < 1 || o.res[k] > 0 {
				return false
			}
			o.res[k] = n
		}
		return true
	}
	return false
}

type world struct {
	env     func(string) string
	environ func() []string
	now     func() time.Time
	resolve func(name string) (string, error)
	exec    func(path string, argv, env []string, lower bool) int
	waitEOF func(in io.Reader)
	bench   func(cores int, force bool) (cpuUnits, memGB int, err error)
	dial    func(ep endpoint, o options) (net.Conn, error)
	pid     int
	cpus    int
	goos    string
	goarch  string
	usable  func() ([]int, error)
	groups  func(usable []int) [][]int
	pin     func(cpus []int) error
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

func main() {
	os.Exit(run(os.Args[1:], world{
		env: os.Getenv, environ: os.Environ, now: time.Now,
		resolve: exec.LookPath, exec: runCommand, waitEOF: waitHoldSignalOrEOF,
		bench: defaultBench, pid: os.Getpid(), cpus: runtime.NumCPU(),
		goos: runtime.GOOS, goarch: runtime.GOARCH,
		usable: usableCPUs, groups: smtGroups, pin: pin,
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
	}))
}

func run(args []string, w world) int {
	o, err := parse(args)
	if err == nil && o.help {
		fmt.Fprint(w.stdout, cmp.Or(verbHelp[o.verb], banner))
		return 0
	}
	if err == nil {
		err = do(o, w)
	}
	var e *exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &e):
		if e.msg != "" {
			fmt.Fprintln(w.stderr, prefix(o.rawVerb), e.msg)
		}
		return e.code
	default:
		fmt.Fprintln(w.stderr, prefix(o.rawVerb), err)
		return 2
	}
}

func flushOut(w io.Writer) {
	if fl, ok := w.(interface{ Flush() error }); ok {
		// ignored: stdout flush best-effort
		_ = fl.Flush()
	}
}

func do(o options, w world) error {
	switch o.verb {
	case "serve":
		return serveCmd(o, w)
	case "set":
		return setCapacity(o, w)
	case "bench":
		return benchCmd(o, w)
	case "claim":
		return claimRemote(o, w)
	case "release":
		return releaseRemote(o, w)
	case "status":
		return statusCmd(o, w)
	case "subscribe":
		return subscribeRemote(o, w)
	case "hold":
		return holdCmd(o, w)
	case "ssh":
		return sshCmd(o, w)
	case "run":
		return runCmdVerb(o, w)
	}
	return refuse(2, "unknown verb %q; run: nova-reserve help", o.verb)
}

func serveCmd(o options, w world) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if o.local {
		if err := ensureDir(o.dir, o.dirFrom, "nova-reserve "+quoteAll(o.args)); err != nil {
			return err
		}
		var initMachines []MachineSpec
		var mtime int64
		if m, mt, err := localMachineFromDir(o.dir); err == nil {
			initMachines = []MachineSpec{m}
			mtime = mt
		} else if filepath.Clean(o.dir) == systemDir {
			if mc, merr := calibrateLocal(o.dir, true, w); merr == nil {
				initMachines = []MachineSpec{{
					Host: "local", Online: true,
					Tags:        effectiveTags(mc.Tags, w.goos, w.goarch),
					UsableCores: mc.UsableCores, Resources: mc.Resources,
				}}
			}
		} else {
			return refuse(2, "%s %s has no capacity.json; run: %s", o.dirFrom, o.dir, o.remedy("bench", "--force"))
		}
		sock := resolveSocketPath(o.dir, o.socket)
		// ignored: remove stale Unix socket file before listening
		_ = os.Remove(sock)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			return refuse(2, "listen unix %s: %v; run: rm -f %s", sock, err, shellQuote(sock))
		}
		// ignored: widen socket permissions for shared local users
		_ = os.Chmod(sock, 0o666)
		defer func() {
			// ignored: cleanup Unix socket on exit
			_ = os.Remove(sock)
		}()
		grace := int64(0)
		if o.reconnSet {
			grace = o.reconnGrace.Milliseconds()
		}
		srv := NewServer(ServerConfig{
			Local: true, Dir: o.dir, SocketPath: sock,
			ReconnectGraceMs: grace, IdleExit: o.idleExit,
		}, initMachines)
		srv.capMtime = mtime
		fmt.Fprintf(w.stderr, "nova-reserve serve: listening unix %s\n", sock)
		return srv.Serve(ctx, ln)
	}

	dsn := strings.TrimSpace(w.env("NOVA_PG_DSN"))
	pgPass := w.env("NOVA_PG_PASSWORD")
	resPass := w.env("NOVA_RESERVE_PASSWORD")
	if dsn == "" || resPass == "" {
		var missing []string
		if dsn == "" {
			missing = append(missing, "NOVA_PG_DSN")
		}
		if resPass == "" {
			missing = append(missing, "NOVA_RESERVE_PASSWORD")
		}
		return refuse(2, "fleet serve wants %s in the environment (or pass --local); run: nova-reserve serve -h", strings.Join(missing, " and "))
	}
	loader := func(c context.Context) ([]MachineSpec, error) {
		return loadMachinesFromStore(c, dsn, pgPass)
	}
	initMachines, err := loader(ctx)
	if err != nil {
		return refuse(2, "%v; run: nova-config machine list", err)
	}
	ln, err := net.Listen("tcp", o.listen)
	if err != nil {
		return refuse(2, "listen tcp %s: %v; run: nova-reserve serve -h", o.listen, err)
	}
	grace := int64(defaultReconnectMs)
	if o.reconnSet {
		grace = o.reconnGrace.Milliseconds()
	}
	srv := NewServer(ServerConfig{
		Local: false, ListenAddr: o.listen, Password: resPass,
		ReconnectGraceMs: grace, IdleExit: o.idleExit, LoadMachines: loader,
	}, initMachines)

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				// ignored: SIGHUP reload errors leave previous machine table intact
				_ = srv.Reload(ctx)
			}
		}
	}()

	fmt.Fprintf(w.stderr, "nova-reserve serve: listening tcp %s machines=%d\n", ln.Addr().String(), len(initMachines))
	return srv.Serve(ctx, ln)
}

func runCmdVerb(o options, w world) error {
	p, err := w.resolve(o.argv[0])
	switch {
	case errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EISDIR):
		return refuse(126, "%s: not executable", o.argv[0])
	case err != nil:
		if strings.ContainsAny(o.argv[0], `/\`) {
			return refuse(127, "%s: no such file", o.argv[0])
		}
		return refuse(127, "%s: command not found on PATH=%s", o.argv[0], w.env("PATH"))
	}

	h, autoCap, err := acquireHold(o, w)
	if err != nil {
		return err
	}
	defer h.Close()

	if autoCap != nil {
		fmt.Fprintf(w.stderr, "nova-reserve: BENCH OK cpu=%d memory=%d cores=%d\n",
			autoCap.Resources["cpu"], autoCap.Resources["memory"], autoCap.UsableCores)
	}
	var pinWarn error
	if len(h.granted.CPUs) > 0 && w.pin != nil {
		if perr := w.pin(h.granted.CPUs); perr != nil {
			pinWarn = perr
			fmt.Fprintf(w.stderr, "nova-reserve: running unpinned: %v\n", perr)
		}
	}
	fmt.Fprintf(w.stderr, "nova-reserve: HELD %s\n", describeGranted(h.granted, w.pid, pinWarn, h.failOpen))
	code := w.exec(p, o.argv, mergeProcessEnv(w.environ, h.granted.Env), o.holder != "ci")
	h.Close()
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

func holdCmd(o options, w world) error {
	h, _, err := acquireHold(o, w)
	if err != nil {
		return err
	}
	defer h.Close()

	if o.jsonOut {
		b, merr := MarshalServerFrame(h.granted)
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w.stdout, string(b))
	} else {
		fmt.Fprintf(w.stdout, "HELD %s\n", describeGranted(h.granted, w.pid, nil, h.failOpen))
	}
	flushOut(w.stdout)
	w.waitEOF(w.stdin)
	return nil
}

func sshCmd(o options, w world) error {
	sshBin, err := w.resolve("ssh")
	if err != nil {
		return refuse(127, "ssh: command not found on PATH=%s", w.env("PATH"))
	}
	h, _, err := acquireHold(o, w)
	if err != nil {
		return err
	}
	defer h.Close()

	fmt.Fprintf(w.stderr, "nova-reserve ssh: HELD %s\n", describeGranted(h.granted, w.pid, nil, h.failOpen))
	target := cmp.Or(h.granted.Address, h.granted.Host)
	if h.granted.User != "" && !strings.Contains(target, "@") {
		target = h.granted.User + "@" + target
	}
	var envPairs []string
	for _, k := range slices.Sorted(maps.Keys(h.granted.Env)) {
		envPairs = append(envPairs, k+"="+shellQuote(h.granted.Env[k]))
	}
	remoteLine := "env " + strings.Join(envPairs, " ") + " " + quoteAll(o.argv)
	sshArgv := []string{"ssh", "-o", "BatchMode=yes", target, remoteLine}
	code := w.exec(sshBin, sshArgv, nil, false)
	h.Close()
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

func describeGranted(sf ServerFrame, pid int, pinWarn error, failOpen bool) string {
	s := fmt.Sprintf("lease_id=%s host=%s cpu=%d memory=%d cores=%d pid=%d",
		sf.LeaseID, sf.Host, sf.Needs["cpu"], sf.Needs["memory"], sf.Cores, pid)
	if pinWarn == nil && len(sf.CPUs) > 0 {
		s += " cpus=" + cpuList(sf.CPUs)
	}
	if sf.Sublease {
		s += " sublease=true"
	}
	if failOpen {
		s += " note=fail-open"
	}
	return s
}

func calibrateLocal(dir string, force bool, w world) (capRecord, error) {
	cores := max(1, w.cpus)
	if u, err := w.usable(); err == nil && len(u) > 0 {
		cores = len(u)
	}
	cpuUnits, memGB, err := w.bench(cores, force)
	if err != nil {
		return capRecord{}, refuse(1, "%v; run: nova-reserve bench --force --dir %s", err, shellQuote(dir))
	}
	var prevTags []string
	res := map[string]int{"cpu": max(1, cpuUnits), "memory": max(1, memGB)}
	if old, ok, rerr := readCapacity(dir); rerr == nil && ok {
		prevTags = old.Tags
		for k, v := range old.Resources {
			if k != "cpu" && k != "memory" {
				res[k] = v
			}
		}
	}
	c := capRecord{
		Tags:        prevTags,
		UsableCores: cores,
		Resources:   res,
		SetByPID:    w.pid,
		SetAt:       w.now().UTC().Format(time.RFC3339),
	}
	if err := writeCapacity(dir, c, w.pid); err != nil {
		return capRecord{}, err
	}
	return c, nil
}

func benchCmd(o options, w world) error {
	if err := ensureDir(o.dir, o.dirFrom, "nova-reserve "+quoteAll(o.args)); err != nil {
		return err
	}
	c, err := calibrateLocal(o.dir, o.force, w)
	if err != nil {
		return err
	}
	if o.writeConfig {
		cfgBin, lerr := w.resolve("nova-config")
		if lerr != nil {
			return refuse(2, "nova-config not found on PATH=%s; run: nova-config machine list", w.env("PATH"))
		}
		hn := shortHost()
		argv := []string{"nova-config", "machine", "set", hn,
			"--cpu", strconv.Itoa(c.Resources["cpu"]),
			"--memory", strconv.Itoa(c.Resources["memory"]),
			"--cores", strconv.Itoa(c.UsableCores)}
		if code := w.exec(cfgBin, argv, nil, false); code != 0 {
			return &exitError{code: code}
		}
	}
	if o.jsonOut {
		b, merr := json.Marshal(map[string]any{
			"cpu":          c.Resources["cpu"],
			"memory":       c.Resources["memory"],
			"usable_cores": c.UsableCores,
			"tags":         effectiveTags(c.Tags, w.goos, w.goarch),
			"resources":    c.Resources,
		})
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w.stdout, string(b))
		return nil
	}
	fmt.Fprintf(w.stdout, "BENCH OK cpu=%d memory=%d cores=%d\n",
		c.Resources["cpu"], c.Resources["memory"], c.UsableCores)
	return nil
}

func setCapacity(o options, w world) error {
	if err := ensureDir(o.dir, o.dirFrom, "nova-reserve "+quoteAll(o.args)); err != nil {
		return err
	}
	cur, have, err := readCapacity(o.dir)
	var bad corrupt
	if err != nil && !errors.As(err, &bad) {
		return err
	}
	if !have {
		cur = capRecord{
			Tags:        []string{},
			UsableCores: max(1, w.cpus),
			Resources:   map[string]int{"cpu": max(1, w.cpus), "memory": 4},
		}
	}
	if o.tagsSet {
		cur.Tags = slices.Clone(o.tags)
	}
	if o.resSet {
		keepCPU, keepMem := cur.Resources["cpu"], cur.Resources["memory"]
		cur.Resources = maps.Clone(o.res)
		if cur.Resources["cpu"] == 0 {
			cur.Resources["cpu"] = max(1, keepCPU)
		}
		if cur.Resources["memory"] == 0 {
			cur.Resources["memory"] = max(1, keepMem)
		}
	}
	if o.cpu > 0 {
		cur.Resources["cpu"] = o.cpu
	}
	if o.memory > 0 {
		cur.Resources["memory"] = o.memory
	}
	if o.cores > 0 {
		cur.UsableCores = o.cores
	} else if !have && o.cpu > 0 {
		cur.UsableCores = o.cpu
	}
	for _, t := range cur.Tags {
		if cur.Resources[t] > 0 {
			return refuse(2, "%q cannot be both a tag and a resource; run: %s", t, helpFor("set"))
		}
	}
	cur.SetByPID = w.pid
	cur.SetAt = w.now().UTC().Format(time.RFC3339)
	if err := writeCapacity(o.dir, cur, w.pid); err != nil {
		return err
	}
	fmt.Fprintf(w.stdout, "CAPACITY SET cpu=%d memory=%d cores=%d\n",
		cur.Resources["cpu"], cur.Resources["memory"], cur.UsableCores)
	return nil
}

func statusCmd(o options, w world) error {
	sf, err := querySnapshot(o, w)
	if err != nil {
		return err
	}
	if o.jsonOut {
		b, merr := MarshalServerFrame(sf)
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w.stdout, string(b))
		return nil
	}
	out := bufio.NewWriter(w.stdout)
	if len(sf.Machines) == 0 {
		fmt.Fprintf(out, "capacity=unset # run: %s\n", o.remedy("bench", "--force"))
		return out.Flush()
	}
	for _, m := range sf.Machines {
		tags := strings.Join(m.Tags, ",")
		fmt.Fprintf(out, "machine %s online=%t cpu=%d memory=%d cores=%d tags=%s\n",
			m.Host, m.Online, m.Resources["cpu"], m.Resources["memory"], m.UsableCores, cmp.Or(tags, "-"))
		for _, l := range m.Leases {
			st := "held"
			if l.Paused {
				st = "paused"
			}
			fmt.Fprintf(out, "lease %s state=%s host=%s holder=%s source=%s label=%s priority=%s %s cores=%d pid=%d since=%s",
				l.LeaseID, st, l.Host, l.Holder, l.Source, l.Label, l.Priority, formatNeedsKV(l.Needs), l.Cores, l.PID, cmp.Or(l.Since, "?"))
			if l.Sublease {
				fmt.Fprintf(out, " sublease=true parent=%s", l.ParentLease)
			}
			if l.Why != "" {
				fmt.Fprintf(out, " why=%s", esc(l.Why))
			}
			fmt.Fprintln(out)
		}
		for _, c := range m.Claims {
			fmt.Fprintf(out, "claim %s host=%s source=%s %s expires_at=%s",
				c.LeaseID, c.Host, c.Source, formatNeedsKV(c.Needs), c.ExpiresAt)
			if c.Why != "" {
				fmt.Fprintf(out, " why=%s", esc(c.Why))
			}
			fmt.Fprintln(out)
		}
		var fparts []string
		for _, r := range slices.Sorted(maps.Keys(m.Free)) {
			fparts = append(fparts, fmt.Sprintf("%s=%d/%d", r, m.Free[r], m.Resources[r]))
		}
		fmt.Fprintf(out, "free host=%s %s leases=%d claims=%d queued=%d\n",
			m.Host, strings.Join(fparts, " "), len(m.Leases), len(m.Claims), len(m.Queue))
	}
	return out.Flush()
}

func waitHoldSignalOrEOF(in io.Reader) {
	done := make(chan struct{}, 1)
	if in != nil {
		go func() {
			var buf [64]byte
			for {
				if _, err := in.Read(buf[:]); err != nil {
					done <- struct{}{}
					return
				}
			}
		}()
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	select {
	case <-done:
	case <-sigs:
	}
}

func esc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			b.WriteString(`\\`)
		case c == ' ':
			b.WriteString(`\s`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
