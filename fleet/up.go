package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const upHelp = `nova-fleet up [--check] [--user LOGIN] --tools TAG|DIR [--runners URL [--runner-count N]] <name>=<address>[/<os>/<arch>]...

From nothing to a fleet, in a fixed order (fleet/README.md, "up"):
  1 control   the helpers on PATH, NOVA_SECRETS_STORE/KEY/SEAT set, and the secrets read
  2 first     the first machine named: the tailnet, PostgreSQL, Redis, nova-config's tables
  3 join      every machine joins (the first too): its row, login, tailnet, seat
  4 store     the secrets store pushed, so the machines' clones see their seats
  5 tools     the nova-tools release (TAG) or local build (DIR) on every machine
  6 services  the service units
  7 runners   GitHub Actions runners (only with --runners)
  8 bus       a message sent and read back over the bus
  <name>=<address>  a machine, reached at first as root over ssh at <address>; os/arch default linux/amd64
  --user LOGIN      the login join makes on each machine (default nova)
  --tools TAG|DIR   a release tag (vX.Y.Z) or a directory laid out as a release
  --runners URL     https://github.com/<owner>/<repo> or https://github.com/<org>
  --runner-count N  runners per machine (default 1)
  --check           every step under ansible --check; the secrets are read all the same
Each step prints the plays' receipts and a line UP <step> OK|WOULD-RUN|FAILED; then one
UP MACHINE line per machine. A failed step stops the run; every step is idempotent, so the
same command resumes. Exit: 0 done; 2 could not run; else the failed step's status.
`

type machine struct{ name, address, os, arch string }

// upRun is one up: its inputs and what it has learnt so far.
type upRun struct {
	check                bool
	user, tools, runners string
	runnerCount          int
	machines             []machine
	dir, store, seat     string
	key, pgHost          string
	stdout, stderr       io.Writer
	bin                  map[string]string
}

func parseUp(args []string, stderr io.Writer) (*upRun, int) {
	u := &upRun{user: "nova", runnerCount: 1, stderr: stderr}
	refuse := func(format string, a ...any) (*upRun, int) {
		fmt.Fprintf(stderr, "nova-fleet up: %s; run: nova-fleet up -h\n", fmt.Sprintf(format, a...))
		return nil, 2
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, val, hasVal := strings.Cut(a, "=")
		takes := map[string]*string{"--user": &u.user, "--tools": &u.tools, "--runners": &u.runners}
		switch {
		case a == "-h" || a == "--help":
			return nil, -1
		case a == "--check":
			u.check = true
		case takes[flag] != nil || flag == "--runner-count":
			if !hasVal {
				if i+1 >= len(args) {
					return refuse("%s wants a value", flag)
				}
				i++
				val = args[i]
			}
			if flag == "--runner-count" {
				n, err := strconv.Atoi(val)
				if err != nil || n < 1 {
					return refuse("--runner-count wants a number of at least 1, got %s", val)
				}
				u.runnerCount = n
			} else {
				*takes[flag] = val
			}
		case strings.HasPrefix(a, "-"):
			return refuse("unknown flag %s", a)
		default:
			name, addr, ok := strings.Cut(a, "=")
			parts := strings.Split(addr, "/")
			if !ok || name == "" || addr == "" || (len(parts) != 1 && len(parts) != 3) || !machineName(name) {
				return refuse("want a machine as <name>=<address>[/<os>/<arch>] (a lowercase name), got %s", a)
			}
			m := machine{name, parts[0], "linux", "amd64"}
			if len(parts) == 3 {
				m.os, m.arch = parts[1], parts[2]
			}
			if slices.ContainsFunc(u.machines, func(o machine) bool { return o.name == m.name }) {
				return refuse("machine %s is named twice", m.name)
			}
			u.machines = append(u.machines, m)
		}
	}
	var missing []string
	if len(u.machines) == 0 {
		missing = append(missing, "<name>=<address>")
	}
	if u.tools == "" {
		missing = append(missing, "--tools")
	}
	if len(missing) > 0 {
		return refuse("missing %s", strings.Join(missing, " and "))
	}
	if u.runners != "" && runnerScope(u.runners) == "" {
		return refuse("--runners wants https://github.com/<owner>/<repo> or https://github.com/<org>, got %s", u.runners)
	}
	return u, 0
}

func machineName(s string) bool {
	for i, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' && i > 0) {
			return false
		}
	}
	return s != ""
}

// runnerScope is repo or org for a GitHub URL, "" for anything else.
func runnerScope(url string) string {
	p, ok := strings.CutPrefix(strings.TrimSuffix(url, "/"), "https://github.com/")
	switch n := strings.Count(p, "/"); {
	case !ok || p == "" || n > 1:
		return ""
	case n == 1:
		return "repo"
	}
	return "org"
}

// localVersion reads the version a local build's file names carry from its SHA256SUMS.
func localVersion(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			if p := strings.Split(strings.TrimPrefix(f[1], "*"), "_"); len(p) == 4 {
				return p[1], nil
			}
		}
	}
	return "", fmt.Errorf("no <tool>_<version>_<os>_<arch> line in %s", filepath.Join(dir, "SHA256SUMS"))
}

func (u *upRun) say(format string, a ...any) { fmt.Fprintf(u.stdout, format+"\n", a...) }

// secrets wraps argv in nova-secrets exec with only the named values.
func (u *upRun) secrets(names []string, argv ...string) []string {
	return append([]string{u.bin["nova-secrets"], "exec", "--only", strings.Join(names, ","), "--"}, argv...)
}

// play runs one embedded play under nova-secrets exec; the inventory is the hand one
// (first machine, before its row exists) or the nova-config script.
func (u *upRun) play(play string, hand bool, names []string, extra ...string) int {
	inv := filepath.Join(u.dir, "inventory")
	if hand {
		inv = filepath.Join(u.dir, "first.ini")
	}
	argv := []string{u.bin["ansible-playbook"], "-i", inv, filepath.Join(u.dir, play+".yml")}
	if u.check {
		argv = append(argv, "--check", "--diff")
	}
	argv = append(argv, extra...)
	env := append(os.Environ(), "ANSIBLE_INVENTORY_UNPARSED_FAILED=true")
	if u.pgHost != "" {
		env = append(env, "NOVA_PG_DSN=postgres://nova_config@"+u.pgHost+":5432/nova")
	}
	code, err := runCmd(u.bin["nova-secrets"], u.secrets(names, argv...)[1:], env)
	if err != nil {
		fmt.Fprintf(u.stderr, "nova-fleet up: run %s: %v\n", play, err)
		return 2
	}
	return code
}

func (u *upRun) seal() []string {
	return []string{"-e", "nova_seal_store=" + u.store, "-e", "nova_seal_seat=" + u.seat, "-e", "nova_seal_key=" + u.key}
}

// steps runs the fixed order; it returns the exit code and the steps each machine passed.
func (u *upRun) steps() int {
	first := u.machines[0]
	pass := map[string][]string{}
	mark := func(step string, ms ...machine) {
		for _, m := range ms {
			pass[m.name] = append(pass[m.name], step)
		}
	}
	fail := func(step string, code int) int {
		fmt.Fprintf(u.stderr, "UP %s FAILED exit=%d; fix what the lines above name, then run the same nova-fleet up again (every step is idempotent)\n", step, code)
		return code
	}
	authKey := []string{"TAILSCALE_AUTHKEY"}
	pg := []string{"NOVA_PG_PASSWORD"}
	names, _, _ := outCmd(u.bin["nova-secrets"], []string{"names", "--as", u.seat}, os.Environ())
	haveStore := slices.Contains(strings.Fields(names), "NOVA_PG_PASSWORD")

	// 2. the first machine: tailnet (no row yet, so no address recorded and the firewall
	// waits for the join), PostgreSQL and Redis sealing their passwords into this seat.
	hand := fmt.Sprintf("[services]\n%s ansible_host=%s ansible_user=root nova_os=%s nova_arch=%s\n", first.name, first.address, first.os, first.arch)
	if err := os.WriteFile(filepath.Join(u.dir, "first.ini"), []byte(hand), 0o600); err != nil {
		return fail("first", 2)
	}
	if c := u.play("tailscale", true, authKey, "-e", "nova_tailscale_record_address=false", "-e", "nova_firewall=false"); c != 0 {
		return fail("first tailscale", c)
	}
	for _, p := range []string{"postgres", "redis"} {
		if c := u.play(p, true, nil, u.seal()...); c != 0 {
			return fail("first "+p, c)
		}
	}
	ip, _, code := outCmd(u.bin["tailscale"], []string{"ip", "-4", first.name}, os.Environ())
	if ip = strings.TrimSpace(ip); code != 0 || ip == "" {
		if !u.check {
			fmt.Fprintf(u.stderr, "nova-fleet up: tailscale ip -4 %s found no address; is this control node on the tailnet?\n", first.name)
			return fail("first", 2)
		}
		ip = first.name
	}
	u.pgHost = ip
	if u.check && !haveStore {
		u.say("UP FIRST WOULD-RUN schema and every step after it: PostgreSQL does not exist yet (check: nothing changed)")
		for _, m := range u.machines {
			u.say("UP MACHINE %s CHECKED steps=tailnet (the rest after postgres) (check: nothing changed)", m.name)
		}
		return 0
	}
	schema := []string{"sh", "-c", `"$1" schema | PGPASSWORD="$NOVA_PG_PASSWORD" "$2" -q -v ON_ERROR_STOP=1 "$3"`, "sh",
		u.bin["nova-config"], u.bin["psql"], "postgres://nova_config@" + ip + ":5432/nova"}
	if u.check {
		u.say("UP FIRST WOULD-APPLY nova-config schema to %s (check: nothing changed)", ip)
	} else if c, err := runCmd(u.bin["nova-secrets"], u.secrets(pg, schema...)[1:], os.Environ()); err != nil || c != 0 {
		return fail("first schema", max(c, 2))
	}
	mark("tailnet,postgres,redis", first)
	u.say("UP FIRST OK %s pg=%s:5432 bus=%s:6379", first.name, ip, ip)

	// 3. every machine joins, the first too (its row, login, address, firewall, seat).
	for i, m := range u.machines {
		roles := "bench"
		if i == 0 {
			roles = "services,bench"
		}
		if u.runners != "" {
			roles += ",runner"
		}
		c := u.play("join", false, append(authKey, pg...), "-e", "nova_join_host="+m.name, "-e", "nova_join_user="+u.user,
			"-e", "nova_join_address="+m.address, "-e", "nova_join_os="+m.os, "-e", "nova_join_arch="+m.arch,
			"-e", "nova_join_roles="+roles, "-e", "nova_seal_store="+u.store, "-e", "nova_bus_redis="+ip+":6379")
		if c != 0 {
			return fail("join "+m.name, c)
		}
		mark("join", m)
	}

	// 4. the store pushed: the seats join added reach the machines' clones.
	if _, _, c := outCmd(u.bin["git"], []string{"-C", u.store, "rev-parse", "--abbrev-ref", "@{u}"}, os.Environ()); c != 0 {
		u.say("UP STORE NOTE %s has no upstream: push it where the machines clone it from", u.store)
	} else if u.check {
		u.say("UP STORE WOULD-PUSH %s (check: nothing changed)", u.store)
	} else if c, err := runCmd(u.bin["git"], []string{"-C", u.store, "push", "--quiet"}, os.Environ()); err != nil || c != 0 {
		return fail("store", max(c, 2))
	} else {
		u.say("UP STORE OK pushed %s", u.store)
	}

	// 5-7. tools, services, runners on every machine.
	tools := []string{"-e", "nova_tools_version=" + u.tools}
	if fi, err := os.Stat(u.tools); err == nil && fi.IsDir() {
		abs, _ := filepath.Abs(u.tools)
		v, err := localVersion(abs)
		if err != nil {
			fmt.Fprintf(u.stderr, "nova-fleet up: --tools %s: %v\n", u.tools, err)
			return fail("tools", 2)
		}
		tools = []string{"-e", "nova_tools_version=" + v, "-e", "nova_tools_local_dir=" + abs}
	}
	if c := u.play("install", false, pg, tools...); c != 0 {
		return fail("tools", c)
	}
	mark("tools", u.machines...)
	if c := u.play("services", false, pg); c != 0 {
		return fail("services", c)
	}
	mark("services", u.machines...)
	if u.runners != "" {
		if c := u.play("github-runners", false, pg, "-e", "github_runner_url="+u.runners, "-e",
			"github_runner_scope="+runnerScope(u.runners), "-e", "runner_count="+strconv.Itoa(u.runnerCount)); c != 0 {
			return fail("runners", c)
		}
		mark("runners", u.machines...)
	}

	// 8. the bus: a message to this control node's own inbox, read back.
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	word := "up-" + hex.EncodeToString(nonce)
	busEnv := append(os.Environ(), "NOVA_BUS_REDIS="+ip+":6379", "NOVA_BUS_USER=bus")
	bus := []string{"NOVA_BUS_PASSWORD"}
	if u.check {
		u.say("UP BUS WOULD-SEND to nova-fleet-up via %s:6379 (check: nothing changed)", ip)
	} else {
		if c, err := runCmd(u.bin["nova-secrets"], u.secrets(bus, u.bin["nova-bus"], "send", "--as", "nova-fleet-up", "--to", "nova-fleet-up", "up check", word)[1:], busEnv); err != nil || c != 0 {
			return fail("bus send", max(c, 2))
		}
		out, _, c := outCmd(u.bin["nova-secrets"], u.secrets(bus, u.bin["nova-bus"], "pending", "--as", "nova-fleet-up", "--list")[1:], busEnv)
		if c != 0 || !strings.Contains(out, word) {
			fmt.Fprintf(u.stderr, "nova-fleet up: the message %s sent to nova-fleet-up is not pending there (exit %d)\n", word, c)
			return fail("bus", max(c, 1))
		}
		mark("bus", u.machines...)
		u.say("UP BUS OK %s:6379 round-trip=%s", ip, word)
	}
	for _, m := range u.machines {
		state := "OK"
		if u.check {
			state = "CHECKED"
		}
		u.say("UP MACHINE %s %s address=%s steps=%s%s", m.name, state, m.address, strings.Join(pass[m.name], ","), map[bool]string{true: " (check: nothing changed)"}[u.check])
	}
	return 0
}

func runUp(args []string, stdout, stderr io.Writer) int {
	u, code := parseUp(args, stderr)
	if code == -1 {
		fmt.Fprint(stdout, upHelp)
		return 0
	}
	if u == nil {
		return code
	}
	u.stdout = stdout
	if noController("up", stderr) {
		return 2
	}

	// 1. the control node: every helper and input at once, then the secrets read.
	want := []string{"ansible-playbook", "nova-config", "nova-secrets", "nova-bus", "tailscale", "psql", "git", "sops", "age"}
	if u.runners != "" {
		want = append(want, "gh")
	}
	u.bin = map[string]string{}
	var missing []string
	for _, b := range want {
		if p, err := lookPath(b); err == nil {
			u.bin[b] = p
		} else {
			missing = append(missing, b)
		}
	}
	for _, e := range []string{"NOVA_SECRETS_STORE", "NOVA_SECRETS_KEY", "NOVA_SECRETS_SEAT"} {
		if os.Getenv(e) == "" {
			missing = append(missing, "$"+e)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "nova-fleet up: the control node lacks %s; run: nova-fleet up -h, then fleet/README.md \"up\" (what is yours, once)\n", strings.Join(missing, ", "))
		return 2
	}
	u.store, u.key, u.seat = os.Getenv("NOVA_SECRETS_STORE"), os.Getenv("NOVA_SECRETS_KEY"), os.Getenv("NOVA_SECRETS_SEAT")
	read := []string{"TAILSCALE_AUTHKEY"}
	if u.runners != "" {
		read = append(read, "GITHUB_RUNNER_TOKEN")
	}
	for _, name := range read {
		_, why, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{name}, "true")[1:], os.Environ())
		if c != 0 {
			fmt.Fprintf(stderr, "nova-fleet up: %s cannot be read from seat %s (exit %d): %s; store it: nova-secrets set %s --as %s (the value on stdin)\n",
				name, u.seat, c, strings.TrimSpace(why), name, u.seat)
			return 2
		}
	}
	u.say("UP CONTROL OK seat=%s store=%s secrets=%s", u.seat, u.store, strings.Join(read, ","))

	dir, err := extract(tree)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet up: extract the plays: %v\n", err)
		return 2
	}
	defer os.RemoveAll(dir)
	u.dir = dir
	script := fmt.Sprintf("#!/bin/sh\nexec '%s' inventory \"$@\"\n", strings.ReplaceAll(u.bin["nova-config"], "'", `'\''`))
	if err := os.WriteFile(filepath.Join(dir, "inventory"), []byte(script), 0o700); err != nil {
		fmt.Fprintf(stderr, "nova-fleet up: write the inventory script: %v\n", err)
		return 2
	}
	return u.steps()
}
