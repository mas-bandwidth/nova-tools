package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const upHelp = `nova-fleet up [--check] [--user LOGIN] [--grant NAME[,NAME]] --tools TAG|DIR [--runners URL [--runner-count N|cores]] <name>=<address>[/<os>/<arch>]... [-- -e name=value ...]

From nothing to a fleet, in a fixed order (cmd/nova-fleet/README.md, "up"). Tailscale, PostgreSQL, Redis and
Ansible are not optional: nothing here skips one.
   1 control   nova-fleet check: the helpers, the store, key, seat and pins, TAILSCALE_API_KEY readable
   2 tailnet   through the Tailscale API with the API access token: the policy merged, this node tagged
               tag:control (joined first if it is not on the tailnet), a single-use key for each machine
               not on it, the tags set on each that is
   3 reach     each machine answers ssh: as its login over the tailnet once it is there, else as root
   4 first     a new fleet's first machine: the tailnet, PostgreSQL (nova-config's tables), Redis
   5 join      every machine joins: its row, login, tailnet name and host key, firewall, seat
   6 grant     nova-secrets grant: the --grant values written to each machine's seat
   7 store     the secrets store pushed to its git upstream, if it has one
   8 refresh   secrets-refresh: the encrypted store pushed (rsync) to each machine
   9 health    the self-healing guards and the disk report (Linux)
  10 tools     the nova-tools release (TAG) or local build (DIR) on every machine
  11 services  the service units
  12 runners   GitHub Actions runners (only with --runners)
  13 bus       list present inboxes over the bus
  <name>=<address>  a machine, reached at first as root over ssh at <address>; os/arch default linux/amd64
  --user LOGIN      the login join makes on each machine (default nova)
  --grant NAMES     the values each machine's seat is granted (default NOVA_BUS_PASSWORD)
  --tools TAG|DIR   a release tag (vX.Y.Z) or a directory laid out as a release
  --runners URL     https://github.com/<owner>/<repo> or https://github.com/<org>
  --runner-count N  runners per machine; default cores: one per 4 cores of each machine, at least 1
                    (github_runner_cores_per_runner). Every machine named gets them except this control node
  --check           every step under ansible --check; the secrets are read all the same
  -- -e name=value  passed to every play (a variable up sets itself, or one that skips a pillar, is refused)
A fleet that exists is read from NOVA_PG_DSN (nova-config's rows): its store machine stays the store, and
the machines named join it ("Adding a machine"). Each step prints the plays' receipts and a line
UP <step> OK|WOULD-RUN|FAILED, then one UP MACHINE line per machine. A failed step stops the run; every
step is idempotent, so the same command resumes. Exit: 0 done; 1 a step failed, or a check found a
machine unreachable; 2 could not run.
`

// own are the variables up sets itself, and the switches that would skip a pillar: a -- -e naming one is refused.
var own = []string{"nova_join_host", "nova_join_user", "nova_join_address", "nova_join_os", "nova_join_arch", "nova_join_roles", "nova_join_root_user",
	"nova_seal_store", "nova_seal_seat", "nova_seal_key", "nova_store_hosts", "nova_bus_redis", "nova_tools_version", "nova_tools_local_dir",
	"nova_tailscale_record_address", "nova_firewall", "github_runner_url", "github_runner_scope", "runner_count",
	"nova_tailnet", "nova_pg_schema", "ansible_user", "ansible_host", "ansible_become"}

type machine struct {
	name, address, os, arch string
	user, dns               string // its login, and its tailnet name once it has one
	sshUser, host           string // how up reaches it now: root at the address given, or the login over the tailnet
	row                     map[string]any
	unreachable             string // the reason, under --check
	later                   bool   // under --check, a machine with no row yet: the steps after join wait for it
	roleLater               bool   // under --check, a row that lacks the runner role: the runners wait for it
	self                    bool   // this control node (tailscale status Self): already joined, so join skips it
}

// upRun is one up: its inputs and what it has learnt so far.
type upRun struct {
	check                bool
	user, tools, runners string
	grant                string
	runnerCount          string // N, or cores (the default): sized to each machine's cores by the role
	machines             []*machine
	pass                 []string
	dir, store, seat     string
	key, pins, dsn       string
	bus                  string            // the Redis the bus is at, host:port
	keys                 map[string]string // the join keys minted for the machines not on the tailnet, by name
	tskey                string            // the key the next play gets in its environment
	stdout, stderr       io.Writer
	bin                  map[string]string
	binDir               string // nova_bin_dir as the machines' shell reads it (-- -e nova_bin_dir=..., else ~/.local/bin)
	refused              bool   // the store's push was refused and went to a branch: the run continues, and exits 1
}

func parseUp(args []string, stderr io.Writer) (*upRun, int) {
	u := &upRun{user: "nova", grant: "NOVA_BUS_PASSWORD", runnerCount: "cores", stderr: stderr, binDir: "$HOME/.local/bin"}
	refuse := func(format string, a ...any) (*upRun, int) {
		fmt.Fprintf(stderr, "nova-fleet up: %s; run: nova-fleet up -h\n", fmt.Sprintf(format, a...))
		return nil, 2
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, val, hasVal := strings.Cut(a, "=")
		takes := map[string]*string{"--user": &u.user, "--tools": &u.tools, "--runners": &u.runners, "--grant": &u.grant}
		switch {
		case a == "-h" || a == "--help":
			return nil, -1
		case a == "--check":
			u.check = true
		case a == "--":
			rest := args[i+1:]
			for j := 0; j < len(rest); j++ {
				if rest[j] != "-e" && rest[j] != "--extra-vars" {
					return refuse("after -- only -e name=value is passed on, got %s", rest[j])
				}
				if j+1 >= len(rest) {
					return refuse("-e wants name=value")
				}
				name, _, ok := strings.Cut(rest[j+1], "=")
				if !ok || name == "" || strings.ContainsAny(name, "{@ ") {
					return refuse("-e wants name=value, got %s", rest[j+1])
				}
				if slices.Contains(own, name) {
					return refuse("-e %s is up's own (or skips a pillar: Tailscale, PostgreSQL, Redis and Ansible are not optional)", name)
				}
				if v, ok := strings.CutPrefix(rest[j+1], "nova_bin_dir="); ok {
					u.binDir = strings.NewReplacer("{{ nova_home }}", "$HOME", "~", "$HOME").Replace(v)
					if !binDirRE.MatchString(u.binDir) {
						return refuse("-e nova_bin_dir wants a plain path (or one under {{ nova_home }} or ~), got %s", v)
					}
				}
				u.pass, j = append(u.pass, "-e", rest[j+1]), j+1
			}
			i = len(args)
		case takes[flag] != nil || flag == "--runner-count":
			if !hasVal {
				if i+1 >= len(args) {
					return refuse("%s wants a value", flag)
				}
				i++
				val = args[i]
			}
			if flag == "--runner-count" {
				if n, err := strconv.Atoi(val); val != "cores" && (err != nil || n < 1) {
					return refuse("--runner-count wants a number of at least 1, or cores, got %s", val)
				}
				u.runnerCount = val
			} else {
				*takes[flag] = val
			}
		case strings.HasPrefix(a, "-"):
			return refuse("unknown flag %s", a)
		default:
			name, addr, ok := strings.Cut(a, "=")
			parts := strings.Split(addr, "/")
			if !ok || name == "" || addr == "" || (len(parts) != 1 && len(parts) != 3) || !nameRE.MatchString(name) {
				return refuse("want a machine as <name>=<address>[/<os>/<arch>] (a lowercase name), got %s", a)
			}
			m := &machine{name: name, address: parts[0], os: "linux", arch: "amd64"}
			if len(parts) == 3 {
				m.os, m.arch = parts[1], parts[2]
			}
			if slices.ContainsFunc(u.machines, func(o *machine) bool { return o.name == m.name }) {
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
	if u.grant == "" || slices.Contains(strings.Split(u.grant, ","), "") {
		return refuse("--grant wants NAME[,NAME], got %q", u.grant)
	}
	if u.runners != "" && runnerScope(u.runners) == "" {
		return refuse("--runners wants https://github.com/<owner>/<repo> or https://github.com/<org>, got %s", u.runners)
	}
	return u, 0
}

var nameRE, binDirRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`), regexp.MustCompile(`^(\$HOME)?[A-Za-z0-9_./-]+$`)

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
		if f := strings.Fields(line); len(f) == 2 && strings.Count(f[1], "_") == 3 {
			return strings.Split(f[1], "_")[1], nil
		}
	}
	return "", fmt.Errorf("no <tool>_<version>_<os>_<arch> line in %s", filepath.Join(dir, "SHA256SUMS"))
}

func (u *upRun) say(format string, a ...any) { fmt.Fprintf(u.stdout, format+"\n", a...) }

// would is the state word of a step under --check.
func (u *upRun) would(did string) string {
	return map[bool]string{true: "WOULD-" + did + " (check: nothing changed)", false: "OK"}[u.check]
}

// secrets wraps argv in nova-secrets exec with only the named values.
func (u *upRun) secrets(names []string, argv ...string) []string {
	return append([]string{u.bin["nova-secrets"], "exec", "--only", strings.Join(names, ","), "--"}, argv...)
}

// env is the plays' environment: the fleet's store for nova-config, unparsed inventories fatal.
func (u *upRun) env() []string {
	env := append(os.Environ(), "ANSIBLE_INVENTORY_UNPARSED_FAILED=true")
	if u.dsn != "" {
		env = append(env, "NOVA_PG_DSN="+u.dsn)
	}
	if u.tskey != "" { // the join key a play needs, in its environment only; never an argument
		env, u.tskey = append(env, "TAILSCALE_AUTHKEY="+u.tskey), ""
	}
	return env
}

// play runs one embedded play, under nova-secrets exec when it needs values; the inventory is
// the hand one (a new fleet's first machine, before its row exists) or the nova-config script.
func (u *upRun) play(play string, hand bool, names []string, extra ...string) int {
	inv := filepath.Join(u.dir, "inventory")
	if hand {
		inv = filepath.Join(u.dir, "first.ini")
	}
	argv := []string{u.bin["ansible-playbook"], "-i", inv, filepath.Join(u.dir, play+".yml")}
	if u.check {
		argv = append(argv, "--check", "--diff")
	}
	argv = append(append(argv, extra...), u.pass...)
	if len(names) > 0 {
		argv = u.secrets(names, argv...)
	}
	code, err := runCmd(argv[0], argv[1:], u.env())
	if err != nil {
		fmt.Fprintf(u.stderr, "nova-fleet up: run %s: %v\n", play, err)
		return 2
	}
	return code
}

func (u *upRun) seal() []string {
	return []string{"-e", "nova_seal_store=" + u.store, "-e", "nova_seal_seat=" + u.seat, "-e", "nova_seal_key=" + u.key}
}

// config runs nova-config under the store's password and parses its --json items' fields.
func (u *upRun) config(args ...string) ([]map[string]any, string, int) {
	out, why, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{"NOVA_PG_PASSWORD"}, append([]string{u.bin["nova-config"]}, args...)...)[1:], u.env())
	var v struct {
		Items []struct{ Fields map[string]any }
	}
	if c != 0 || (slices.Contains(args, "--json") && json.Unmarshal([]byte(out), &v) != nil) {
		return nil, oneLine(why + " " + out), max(c, 1)
	}
	var rows []map[string]any
	for _, it := range v.Items {
		rows = append(rows, it.Fields)
	}
	return rows, "", 0
}

// peer is the machine as the control node's tailscale sees it: its MagicDNS name, its address, and
// its Tailscale SSH host keys, which are pinned in this node's known_hosts (never trusted on first use).
func (u *upRun) peer(m *machine) bool {
	st, _ := u.status()
	p, self, ok, err := st.node(m.name)
	if err != nil {
		fmt.Fprintf(u.stderr, "nova-fleet up: %s on the tailnet: %v\n", m.name, err)
	}
	m.self = self
	if ok {
		m.dns = strings.TrimSuffix(p.DNSName, ".")
		if len(p.SSH_HostKeys) == 0 {
			fmt.Fprintf(u.stderr, "nova-fleet up: %s has no Tailscale SSH host key in tailscale status; its ssh over the tailnet is not pinned (is Tailscale SSH on?)\n", m.name)
			return true
		}
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, ".ssh", "known_hosts")
		have, _ := os.ReadFile(path)
		var add []string
		for _, k := range p.SSH_HostKeys {
			line := strings.Join([]string{m.name, m.dns, p.TailscaleIPs[0]}, ",") + " " + k
			pinned := slices.ContainsFunc(strings.Split(string(have), "\n"), func(l string) bool {
				hosts, key, _ := strings.Cut(l, " ")
				return key == k && slices.Contains(strings.Split(hosts, ","), m.dns)
			})
			if !pinned {
				add = append(add, line)
			}
		}
		if len(add) > 0 && !u.check {
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintln(f, strings.Join(add, "\n"))
				f.Close()
				u.say("UP HOSTKEY %s OK pinned=%d %s", m.name, len(add), path)
			}
		}
	}
	return ok
}

// reach finds how a machine answers: its login over the tailnet once it has a row (join closed
// the address given then), else root at the address given. A machine that answers nowhere is
// unreachable.
func (u *upRun) reach(m *machine) {
	m.user = u.user
	if user, _ := m.row["user"].(string); user != "" {
		m.user = user
	}
	var try [][2]string
	if u.peer(m) && m.row != nil {
		try = append(try, [2]string{m.user, m.dns})
	}
	if addr, _ := m.row["address"].(string); addr != "" && addr != m.dns {
		try = append(try, [2]string{m.user, addr})
	}
	if m.os != "darwin" {
		try = append(try, [2]string{"root", m.address})
	} else {
		try = append(try, [2]string{m.user, m.address})
	}
	if m.dns != "" && m.row == nil {
		try = append(try, [2]string{m.user, m.dns})
	}
	var why string
	for _, t := range try {
		_, e, c := outCmd(u.bin["ssh"], []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", t[0] + "@" + t[1], "true"}, os.Environ())
		if c == 0 {
			m.sshUser, m.host = t[0], t[1]
			return
		}
		why = oneLine(e)
	}
	m.unreachable = why + " (tried " + strings.Join(func() (s []string) {
		for _, t := range try {
			s = append(s, t[0]+"@"+t[1])
		}
		return
	}(), ", ") + ")"
}

// steps runs the fixed order; it returns the exit code and the steps each machine passed.
func (u *upRun) steps() int {
	pass := map[string][]string{}
	mark := func(step string, ms ...*machine) {
		for _, m := range ms {
			if m.unreachable == "" && !m.later {
				pass[m.name] = append(pass[m.name], step)
			}
		}
	}
	fail := func(step string, code int) int {
		fmt.Fprintf(u.stderr, "UP %s FAILED exit=%d; fix what the lines above name, then run the same nova-fleet up again (every step is idempotent)\n", step, code)
		return 1
	}
	pg := []string{"NOVA_PG_PASSWORD"}
	names, _, _ := outCmd(u.bin["nova-secrets"], []string{"names", "--as", u.seat}, os.Environ())
	haveStore := slices.Contains(strings.Fields(names), "NOVA_PG_PASSWORD")

	// The fleet that exists, if one does: NOVA_PG_DSN names its store. Without it, a seat that holds
	// the store's password means a run got as far as PostgreSQL: the first machine named must be
	// that store (a resume), and its rows say what the fleet is.
	var first, store *machine
	if u.dsn = os.Getenv("NOVA_PG_DSN"); u.dsn == "" && haveStore {
		first = u.machines[0]
		if !u.peer(first) {
			fmt.Fprintf(u.stderr, "nova-fleet up: seat %s holds NOVA_PG_PASSWORD, so a fleet exists, and %s is not on the tailnet, so it is not its store; run: export NOVA_PG_DSN=postgres://nova_config@<the store machine's tailnet name>:5432/nova\n", u.seat, first.name)
			return 2
		}
		u.dsn = "postgres://nova_config@" + first.dns + ":5432/nova"
	}
	if u.dsn != "" {
		rows, why, c := u.config("machine", "list", "--json")
		if c != 0 {
			fmt.Fprintf(u.stderr, "nova-fleet up: the fleet's rows (nova-config machine list via NOVA_PG_DSN=%s): %s; run: export NOVA_PG_DSN=postgres://nova_config@<the store machine's tailnet name>:5432/nova\n", u.dsn, why)
			return 2
		}
		for _, r := range rows {
			name, _ := r["name"].(string)
			roles, _ := r["roles"].(string)
			if i := slices.IndexFunc(u.machines, func(m *machine) bool { return m.name == name }); i >= 0 {
				u.machines[i].row = r
			}
			if store == nil && slices.Contains(strings.Split(roles, ","), "services") {
				addr, _ := r["address"].(string)
				store = &machine{name: name, dns: addr}
			}
		}
		switch {
		case store == nil && first == nil:
			fmt.Fprintf(u.stderr, "nova-fleet up: no machine row has the role services (the store machine); run: nova-config machine set <name> --roles services,bench --actor %s\n", u.seat)
			return 2
		case store != nil && first != nil && store.name != first.name:
			fmt.Fprintf(u.stderr, "nova-fleet up: the store machine is %s (its row has the role services), not %s; run: export NOVA_PG_DSN=postgres://nova_config@%s:5432/nova, then name the machines to add\n", store.name, first.name, store.dns)
			return 2
		}
	} else {
		first = u.machines[0]
	}

	// 2. the tailnet, through the Tailscale API with the one API access token (tailnet.go).
	if err := u.tailnet(); err != nil {
		fmt.Fprintf(u.stderr, "nova-fleet up: the tailnet: %v\n", err)
		return fail("tailnet", 1)
	}

	// 3. each machine answers ssh.
	for _, m := range u.machines {
		u.reach(m)
		switch {
		case m.unreachable != "" && !u.check:
			fmt.Fprintf(u.stderr, "nova-fleet up: %s does not answer ssh: %s\n", m.name, m.unreachable)
			return fail("reach "+m.name, 1)
		case m.unreachable != "":
			u.say("UP %s UNREACHABLE %s", m.name, m.unreachable)
		default:
			u.say("UP REACH %s OK %s@%s", m.name, m.sshUser, m.host)
		}
	}
	wouldRun := func(step, why string) { u.say("UP %s WOULD-RUN (check: %s)", step, why) }

	// 4. a new fleet's first machine: the tailnet (no row yet, so no address recorded and the
	// firewall waits for the join), then PostgreSQL and Redis sealing their passwords into this seat.
	if first != nil {
		hand := fmt.Sprintf("[services]\n%s ansible_host=%s ansible_user=%s nova_os=%s nova_arch=%s\n", first.name, first.host, first.sshUser, first.os, first.arch)
		if err := os.WriteFile(filepath.Join(u.dir, "first.ini"), []byte(hand), 0o600); err != nil {
			return fail("first", 2)
		}
		switch {
		case first.unreachable != "":
			wouldRun("FIRST tailscale, postgres, redis", "not reachable")
		default:
			u.tskey = u.keys[first.name]
			delete(u.keys, first.name) // single-use: its join below finds it Running
			if c := u.play("tailscale", true, nil, "-e", "nova_tailscale_record_address=false", "-e", "nova_firewall=false"); c != 0 {
				return fail("first tailscale", c)
			}
			mark("tailnet", first)
			if !u.peer(first) {
				if !u.check {
					fmt.Fprintf(u.stderr, "nova-fleet up: tailscale status on this control node has no peer %s; is this node on the same tailnet?\n", first.name)
					return fail("first", 1)
				}
				wouldRun("FIRST postgres, redis", "the machine is not on the tailnet yet")
				break
			}
			for _, p := range []string{"postgres", "redis"} {
				if c := u.play(p, true, nil, append(u.seal(), "-e", "nova_store_hosts="+first.name)...); c != 0 {
					return fail("first "+p, c)
				}
			}
			mark("postgres,redis", first)
		}
		store = first
		u.dsn = "postgres://nova_config@" + first.dns + ":5432/nova"
		if first.unreachable == "" && first.dns != "" {
			u.say("UP FIRST %s %s pg=%s:5432 bus=%s:6379", first.name, u.would("RUN"), first.dns, first.dns)
		}
		if u.check && !haveStore {
			for _, s := range []string{"JOIN", "GRANT", "STORE", "REFRESH", "HEALTH", "TOOLS", "SERVICES", "RUNNERS", "BUS"} {
				wouldRun(s, "after PostgreSQL exists")
			}
			return u.end(pass, "(the rest once PostgreSQL exists)")
		}
	}
	u.bus = store.dns + ":6379"

	// 5. every machine joins (its row, login, address, host key, firewall, seat).
	for _, m := range u.machines {
		if m.unreachable != "" {
			continue
		}
		roles := "bench"
		if m == first {
			roles = "services,bench"
		}
		if u.runners != "" && !m.self {
			roles += ",runner"
		}
		if m.self { // this control node: on the tailnet already, and join's peer check cannot see itself
			if m.row == nil && !u.check {
				fmt.Fprintf(u.stderr, "nova-fleet up: %s is this control node (tailscale status Self), so join skips it, and it has no row; run: nova-config machine add %s --user %s --actor %s, then the same nova-fleet up\n", m.name, m.name, m.user, u.seat)
				return fail("join "+m.name, 2)
			}
			u.say("UP JOIN %s SKIPPED this control node (tailscale status Self): already joined", m.name)
		} else {
			u.tskey = u.keys[m.name]
			c := u.play("join", false, pg, "-e", "nova_join_host="+m.name, "-e", "nova_join_user="+m.user,
				"-e", "nova_join_address="+m.host, "-e", "nova_join_root_user="+m.sshUser,
				"-e", "nova_join_os="+m.os, "-e", "nova_join_arch="+m.arch, "-e", "nova_join_roles="+roles,
				"-e", "nova_seal_store="+u.store, "-e", "nova_bus_redis="+u.bus)
			if c != 0 {
				return fail("join "+m.name, c)
			}
			u.peer(m)
			mark("join", m)
			u.say("UP JOIN %s %s", m.name, u.would("JOIN"))
		}
		if u.check && m.row == nil {
			m.later = true
			u.say("UP %s PENDING (check: its row does not exist yet; the steps after join run once join has made it)", m.name)
		}
		if u.runners != "" && m.row != nil && !m.self { // runners, like any heavy work, never default onto the control node
			if roles, _ := m.row["roles"].(string); !slices.Contains(strings.Split(roles, ","), "runner") {
				args := []string{"machine", "set", m.name, "--roles", roles + ",runner", "--actor", u.seat, "--reason", "nova-fleet up --runners"}
				if u.check {
					args = append(args, "--dry-run")
				}
				if _, why, c := u.config(args...); c != 0 {
					fmt.Fprintf(u.stderr, "nova-fleet up: the runner role on %s: %s\n", m.name, why)
					return fail("join "+m.name, c)
				}
				u.say("UP ROLE %s %s runner", m.name, u.would("SET"))
				m.roleLater = u.check
			}
		}
	}

	// 6. each machine's seat granted the values its services open, written encrypted to that seat's
	// pinned key (read from the machine once, over the tailnet); the operator is never a recipient.
	if u.check { // a check reads the values here, so a value this seat lacks stops it with the read's reason
		if _, why, c := outCmd(u.bin["nova-secrets"], u.secrets(strings.Split(u.grant, ","), "true")[1:], os.Environ()); c != 0 {
			fmt.Fprintf(u.stderr, "nova-fleet up: seat %s cannot read %s to grant it: %s\n", u.seat, u.grant, oneLine(why))
			return fail("grant", c)
		}
	}
	for _, m := range u.machines {
		if m.unreachable != "" || m.later {
			continue
		}
		pub, why, c := outCmd(u.bin["ssh"], []string{"-o", "BatchMode=yes", m.user + "@" + m.dns, "sh", "-c",
			"'PATH=" + strings.Join(slices.Compact([]string{u.binDir, "$HOME/.local/bin"}), ":") + `:/opt/homebrew/bin:$PATH age-keygen -y $HOME/.config/nova-secrets/` + m.name + `.key'`}, os.Environ())
		pub = strings.TrimSpace(pub)
		switch {
		case u.check:
			u.say("UP GRANT %s WOULD-GRANT %s to %s (check: nothing changed)", m.name, u.grant, map[bool]string{true: pub, false: "its seat key once made"}[strings.HasPrefix(pub, "age1")])
		case c != 0 || !strings.HasPrefix(pub, "age1"):
			fmt.Fprintf(u.stderr, "nova-fleet up: read %s's seat public key over the tailnet: %s\n", m.name, oneLine(why+" "+pub))
			return fail("grant "+m.name, 1)
		default:
			if c, err := runCmd(u.bin["nova-secrets"], []string{"grant", u.grant, "--to", m.name, "--pub", pub}, os.Environ()); err != nil || c != 0 {
				return fail("grant "+m.name, max(c, 1))
			}
			u.say("UP GRANT %s OK %s", m.name, u.grant)
		}
		mark("grant", m)
	}

	// 7. the store's upstream, if it has one, gets the new seats and grants (a copy kept off this
	// node; the machines never read it).
	// A push the remote refuses (a protected branch: changes through a pull request) goes to a branch of
	// its own instead, named by the commit; the run goes on, and ends 1 with the branch to open a PR from.
	git := func(args ...string) (string, string, int) {
		return outCmd(u.bin["git"], append([]string{"-C", u.store}, args...), os.Environ())
	}
	if up, _, c := git("rev-parse", "--abbrev-ref", "@{u}"); c != 0 {
		u.say("UP STORE NOTE %s has no upstream: nothing to push", u.store)
	} else if u.check {
		u.say("UP STORE WOULD-PUSH %s (check: nothing changed)", u.store)
	} else if _, why, c := git("push", "--quiet"); c != 0 && !strings.Contains(why, "[remote rejected]") {
		fmt.Fprintf(u.stderr, "nova-fleet up: git push of %s: %s\n", u.store, oneLine(why))
		return fail("store", c)
	} else if c != 0 {
		sha, _, _ := git("rev-parse", "--short", "HEAD") // ignored: a failure leaves the name nova-fleet-up/, and the push below says so
		branch := "nova-fleet-up/" + strings.TrimSpace(sha)
		remote, _, _ := strings.Cut(strings.TrimSpace(up), "/")
		if _, why2, c := git("push", "--quiet", cmp.Or(remote, "origin"), "HEAD:refs/heads/"+branch); c != 0 {
			fmt.Fprintf(u.stderr, "nova-fleet up: git push of %s was refused (%s), and so was the branch %s: %s\n", u.store, oneLine(why), branch, oneLine(why2))
			return fail("store", c)
		}
		fmt.Fprintf(u.stderr, "UP STORE PUSH REFUSED: %s is protected (%s); pushed branch %s; open a PR from it\n", strings.TrimSpace(up), oneLine(why), branch)
		u.refused = true
	} else {
		u.say("UP STORE OK pushed %s", u.store)
	}

	// 8. refresh: secrets-refresh pushes the encrypted store from this node's clone to each machine
	// (rsync over the tailnet: no .git, stale files deleted, 0700/0600); a rerun pushes again.
	in := u.ready(false)
	if len(in) == 0 { // every machine named waits for its row: nothing to check the plays against
		for _, s := range []string{"REFRESH", "HEALTH", "TOOLS", "SERVICES", "RUNNERS", "BUS"} {
			wouldRun(s, "once join has made the rows")
		}
		return u.end(pass, "")
	}
	limit := []string{"-l", strings.Join(in, ",")}
	if c := u.play("secrets-refresh", false, pg, append([]string{"-e", "nova_seal_store=" + u.store}, limit...)...); c != 0 {
		return fail("refresh", c)
	}
	mark("refresh", u.machines...)
	u.say("UP REFRESH %s %s", u.would("PUSH"), u.store)

	// 9-12. the guards, tools, services, runners on every machine that is reached.
	if c := u.play("health", false, pg, limit...); c != 0 {
		return fail("health", c)
	}
	mark("health", u.machines...)
	u.say("UP HEALTH %s", u.would("RUN"))
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
	if c := u.play("install", false, pg, append(tools, limit...)...); c != 0 {
		return fail("tools", c)
	}
	mark("tools", u.machines...)
	u.say("UP TOOLS %s %s", u.would("INSTALL"), u.tools)
	if c := u.play("services", false, pg, limit...); c != 0 {
		return fail("services", c)
	}
	mark("services", u.machines...)
	u.say("UP SERVICES %s", u.would("RUN"))
	if u.runners != "" {
		on := u.ready(true) // the machines whose row has the runner role, so the play finds them in its group
		if len(on) == 0 {
			wouldRun("RUNNERS", "once the runner role is on the rows")
		} else if c := u.play("github-runners", false, pg, "-e", "github_runner_url="+u.runners, "-e", "github_runner_scope="+runnerScope(u.runners),
			"-e", "runner_count="+u.runnerCount, "-l", strings.Join(on, ",")); c != 0 {
			return fail("runners", c)
		} else {
			for _, m := range u.machines {
				pass[m.name] = append(pass[m.name], map[bool][]string{true: {"runners"}}[slices.Contains(on, m.name)]...)
			}
			u.say("UP RUNNERS %s %s", u.would("RUN"), u.runners)
		}
	}

	// 13. the bus: `nova-bus who` with the fleet's credentials; it reads presence and sends nothing as a name.
	busEnv := append(os.Environ(), "NOVA_BUS_REDIS="+u.bus, "NOVA_BUS_USER=bus")
	if u.check {
		u.say("UP BUS WOULD-ASK who is present via %s (check: nothing changed)", u.bus)
	} else {
		out, _, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{"NOVA_BUS_PASSWORD"}, u.bin["nova-bus"], "who")[1:], busEnv)
		first, _, _ := strings.Cut(out, "\n")
		if c != 0 || !strings.HasPrefix(first, "WHO OK present=") {
			fmt.Fprintf(u.stderr, "nova-fleet up: nova-bus who did not answer at %s (exit %d)\n", u.bus, c)
			return fail("bus", 1)
		}
		mark("bus", u.machines...)
		u.say("UP BUS OK %s %s", u.bus, strings.TrimPrefix(first, "WHO OK "))
	}
	return u.end(pass, "")
}

// ready names the machines a play can run on: reached, with a row (and the runner role, with roles).
func (u *upRun) ready(roles bool) (names []string) {
	for _, m := range u.machines {
		if m.unreachable == "" && !m.later && !(roles && (m.roleLater || m.self)) {
			names = append(names, m.name)
		}
	}
	return
}

// end prints one line per machine and, after a run, the store's DSN for the next up.
func (u *upRun) end(pass map[string][]string, note string) int {
	bad := 0
	for _, m := range u.machines {
		switch {
		case m.unreachable != "":
			bad++
			u.say("UP MACHINE %s UNREACHABLE address=%s steps=none %s", m.name, m.address, m.unreachable)
		case u.check && m.later:
			u.say("UP MACHINE %s CHECKED address=%s steps=join (the rest once join has made its row) (check: nothing changed)", m.name, m.address)
		case u.check:
			u.say("UP MACHINE %s CHECKED address=%s steps=%s %s(check: nothing changed)", m.name, m.address, strings.Join(pass[m.name], ","), note)
		default:
			u.say("UP MACHINE %s OK address=%s steps=%s", m.name, m.address, strings.Join(pass[m.name], ","))
		}
	}
	if bad > 0 {
		fmt.Fprintf(u.stderr, "UP CHECK FAILED unreachable=%d; each UNREACHABLE line above says what was tried\n", bad)
		return 1
	}
	if !u.check && u.dsn != "" && os.Getenv("NOVA_PG_DSN") == "" {
		u.say("UP DONE: nova-config and the next nova-fleet up read the fleet from NOVA_PG_DSN\nnext: export NOVA_PG_DSN=%s", u.dsn)
	}
	if u.refused {
		fmt.Fprintf(u.stderr, "UP STORE FAILED: the store's commits are on a branch, not its upstream; merge the PR (the UP STORE PUSH REFUSED line above)\n")
		return 1
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

	// 1. the control node: nova-fleet check, every prerequisite and secret at once.
	if n := u.control(stdout); n > 0 {
		fmt.Fprintf(stderr, "UP CONTROL FAILED missing=%d; fix each CHECK MISSING line above (nova-fleet check shows them again), then run the same nova-fleet up\n", n)
		return 2
	}
	u.say("UP CONTROL OK seat=%s store=%s", u.seat, u.store)

	dir, err := extract(tree)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet up: extract the plays: %v\n", err)
		return 2
	}
	defer os.RemoveAll(dir)
	u.dir = dir
	if _, err := inventoryScript(dir, u.bin["nova-config"]); err != nil {
		fmt.Fprintf(stderr, "nova-fleet up: write the inventory script: %v\n", err)
		return 2
	}
	return u.steps()
}
