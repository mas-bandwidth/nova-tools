package main

import (
	"cmp"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const checkHelp = `nova-fleet check [--runners URL]

The control node's preflight, the first step of nova-fleet up: one line per prerequisite,
CHECK OK <name> <version|path> or CHECK MISSING <name>: <the command that fixes it, for this
OS, pasteable as it stands, a reason after # when there is one, in paste order>; the helpers
are found in ~/.local/bin too, where the repairs install them, and CHECK MISSING PATH prints the
line that puts that directory on PATH; this node on
the tailnet (a NOTE when not: up joins it); with --runners gh and its login; the store, key,
seat and pins of NOVA_SECRETS_STORE, NOVA_SECRETS_KEY, NOVA_SECRETS_SEAT and NOVA_SECRETS_PINS;
TAILSCALE_API_KEY, the one credential a human gives, read from the seat, the reason printed when
the read fails; and NOVA_PG_DSN once a fleet exists. On the tailnet, one line per peer this node has
exchanged bytes with, CHECK traffic <peer> tx=<GB> rx=<GB> from tailscale status (this node's counters
since tailscaled started), and CHECK WARN traffic for a peer over 100 GB either way: something is
fetching or mirroring on a clock, which no service record may do. Tailscale and Ansible are
required here, never optional. Nothing is changed. Exit: 0 all present; 1 something missing
(CHECK FAILED missing=<n>); 2 could not run.
`

// osRelease is read for the Linux family; tests point it elsewhere.
var osRelease = "/etc/os-release"

var versionRE = regexp.MustCompile(`(go)?[0-9]+\.[0-9]+(\.[0-9]+)?`)

// family is how this machine installs packages: brew, apt, dnf or "".
func family() string {
	if goos == "darwin" {
		return "brew"
	}
	b, _ := os.ReadFile(osRelease)
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && (k == "ID" || k == "ID_LIKE") {
			for _, id := range strings.Fields(strings.Trim(v, `"`)) {
				switch id {
				case "debian", "ubuntu":
					return "apt"
				case "fedora", "rhel", "centos":
					return "dnf"
				}
			}
		}
	}
	return ""
}

// pin reads a value of the embedded group_vars/all.yml: the line "<key>: <value>", or with
// sub the line "  <sub>: <value>" under "<key>:". The plays read the same file.
func pin(key string, sub ...string) string {
	b, _ := fs.ReadFile(tree, "group_vars/all.yml")
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), ":")
		switch {
		case len(sub) == 0 && line == strings.TrimSpace(line) && k == key:
			return strings.Trim(strings.TrimSpace(v), `"`)
		case len(sub) > 0 && line == key+":":
			in = true
		case in && strings.HasPrefix(line, "  ") && k == sub[0]:
			return strings.Trim(strings.TrimSpace(v), `"`)
		case in && !strings.HasPrefix(line, "  "):
			in = false
		}
	}
	return ""
}

// fix is the pasteable command that installs a helper on this machine's family.
func fix(name, fam string) string {
	pkg := map[string][3]string{ // apt, dnf, brew
		"age":          {"age", "age", "age"},
		"age-keygen":   {"age", "age", "age"},
		"psql":         {"postgresql-client", "postgresql", "libpq && brew link --force libpq"},
		"git":          {"git", "git", "git"},
		"gpg":          {"gnupg", "gnupg2", "gnupg"},
		"gh":           {"gh", "gh", "gh"},
		"rsync":        {"rsync", "rsync", "rsync"},
		"ssh":          {"openssh-client", "openssh-clients", "openssh"},
		"python3-venv": {"python3-venv", "python3", "python3"},
	}
	switch {
	case strings.HasPrefix(name, "nova-"):
		t := strings.TrimPrefix(name, "nova-")
		return "mkdir -p ~/.local/bin && ([ -d nova-tools ] || git clone --depth 1 https://github.com/mas-bandwidth/nova-tools) && go build -C nova-tools -o ~/.local/bin/" + name + " ./cmd/nova-" + t
	case name == "go" && fam == "brew":
		return "brew install go"
	case name == "go":
		v := strings.TrimPrefix(runtime.Version(), "go")
		return "curl -fsSL https://go.dev/dl/go" + v + ".linux-" + goarch + ".tar.gz | sudo tar -C /usr/local -xz && export PATH=/usr/local/go/bin:$PATH && echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile"
	case name == "ansible-playbook" && fam == "brew":
		return "brew install ansible"
	case name == "ansible-playbook":
		venv := "python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH && echo 'export PATH=~/.local/ansible/bin:$PATH' >> ~/.profile"
		if fam == "apt" { // Debian and Ubuntu ship python3 without venv
			return "sudo apt-get update -qq && sudo apt-get install -y python3-venv && " + venv
		}
		return venv
	case name == "tailscale" && fam == "brew":
		return "brew install --cask tailscale"
	case name == "tailscale":
		return "curl -fsSL https://tailscale.com/install.sh | sh"
	case name == "sops" && fam == "brew":
		return "brew install sops"
	case name == "sops":
		// The pin is the plays' own (group_vars/all.yml), so this line and join.yml cannot drift.
		v, sum := pin("nova_sops_version"), pin("nova_sops_sha256", "linux-"+goarch)
		return "mkdir -p ~/.local/bin && curl -fsSLo ~/.local/bin/sops https://github.com/getsops/sops/releases/download/v" + v + "/sops-v" + v + ".linux." + goarch +
			" && (echo '" + sum + "  '\"$HOME\"/.local/bin/sops | sha256sum -c || { rm -f ~/.local/bin/sops; false; }) && chmod +x ~/.local/bin/sops"
	}
	p, ok := pkg[name]
	switch {
	case !ok:
		return "install " + name + " and put it on PATH"
	case fam == "apt":
		return "sudo apt-get update -qq && sudo apt-get install -y " + p[0]
	case fam == "dnf":
		return "sudo dnf install -y " + p[1]
	case fam == "brew":
		return "brew install " + p[2]
	}
	return "install " + p[0] + " with this system's package manager"
}

// localBin is the directory the nova-* and sops repairs install into.
func localBin() string {
	home, _ := os.UserHomeDir() // ignored: with no home and no HOME the path is relative and finds nothing
	return filepath.Join(cmp.Or(os.Getenv("HOME"), home), ".local", "bin")
}

// onPath reports whether dir is one of the directories of the PATH value path.
func onPath(dir, path string) bool {
	return slices.ContainsFunc(filepath.SplitList(path), func(d string) bool { return d != "" && filepath.Clean(d) == filepath.Clean(dir) })
}

// find is lookPath, then localBin: the repairs install there whether or not it is on PATH.
func find(name string) (string, error) {
	p, err := lookPath(name)
	if err == nil {
		return p, nil
	}
	local := filepath.Join(localBin(), name)
	if fi, e := os.Stat(local); e == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
		return local, nil
	}
	return "", err
}

// goVersion is major.minor of a "goX.Y[.Z]" string, or 0 0.
func goVersion(s string) (int, int) {
	var a, b int
	if f := strings.FieldsFunc(strings.TrimPrefix(versionRE.FindString(s), "go"), func(r rune) bool { return r == '.' }); len(f) >= 2 {
		a, _ = strconv.Atoi(f[0])
		b, _ = strconv.Atoi(f[1])
	}
	return a, b
}

// control checks this node and fills u.bin, u.store, u.key, u.seat and u.pins; it returns how
// many prerequisites are missing. Tailscale and Ansible are required here; PostgreSQL and
// Redis are what up puts on the first machine and requires after.
func (u *upRun) control(out io.Writer) int {
	want := []string{"ansible-playbook", "git", "nova-config", "nova-secrets", "nova-bus", "tailscale", "psql", "gpg", "sops", "age", "age-keygen", "rsync", "ssh"}
	if u.runners != "" {
		want = append(want, "gh")
	}
	versionArgs := map[string][]string{"tailscale": {"version"}, "nova-config": nil, "nova-secrets": nil, "nova-bus": nil, "ssh": nil, "go": {"version"}}
	fam, missing := family(), 0
	// A MISSING line is the command that fixes it, pasteable as it stands; a reason follows as a comment.
	miss := func(name, cmd string, why ...string) {
		missing++
		fmt.Fprintf(out, "CHECK MISSING %s: %s%s\n", name, cmd, map[bool]string{true: "  # " + oneLine(strings.Join(why, " "))}[len(why) > 0])
	}
	needGo := slices.ContainsFunc(want, func(b string) bool { _, err := find(b); return err != nil && strings.HasPrefix(b, "nova-") })
	u.bin = map[string]string{}
	var local []string // found only in localBin
	for _, b := range want {
		if needGo && b == "nova-config" { // the nova-* lines build from source: Go at least this binary's
			needGo = false
			wantMajor, wantMinor := goVersion(runtime.Version())
			if p, err := lookPath("go"); err != nil {
				miss("go", fix("go", fam))
			} else if o, _, _ := outCmd(p, []string{"version"}, os.Environ()); func() bool { a, c := goVersion(o); return a < wantMajor || a == wantMajor && c < wantMinor }() {
				miss("go", fix("go", fam), oneLine(o)+" is older than "+runtime.Version()+", which built this nova-fleet")
			} else {
				fmt.Fprintf(out, "CHECK OK go %s\n", versionRE.FindString(o))
			}
		}
		p, err := find(b)
		if err != nil {
			miss(b, fix(b, fam))
			continue
		}
		if _, e := lookPath(b); e != nil {
			local = append(local, b)
		}
		u.bin[b] = p
		what := p
		if args, ok := versionArgs[b]; !ok || args != nil {
			if !ok {
				args = []string{"--version"}
			}
			if o, _, _ := outCmd(p, args, os.Environ()); versionRE.MatchString(o) {
				what = versionRE.FindString(o)
			}
		}
		fmt.Fprintf(out, "CHECK OK %s %s\n", b, what)
	}
	if dir := localBin(); len(local) > 0 && !onPath(dir, os.Getenv("PATH")) {
		miss("PATH", `export PATH="$HOME/.local/bin:$PATH"`, strings.Join(local, " ")+" found only in "+dir+", which is not on PATH; append the line to ~/.profile to keep it")
	}
	galaxy, posix, why := "ansible-galaxy", true, []string(nil) // without ansible, the line is printed for after its fix line has run
	if p := u.bin["ansible-playbook"]; p != "" {                // the collections the plays use (requirements.yml)
		galaxy = filepath.Join(filepath.Dir(p), "ansible-galaxy")
		o, e, c := outCmd(galaxy, []string{"collection", "list"}, os.Environ())
		if posix = strings.Contains(o, "ansible.posix"); c == 127 {
			why = []string{galaxy + " could not run, so the collections are unchecked:", e}
		}
	}
	if !posix || u.bin["ansible-playbook"] == "" {
		miss("ansible.posix", "nova-fleet show requirements > requirements.yml && "+galaxy+" collection install -r requirements.yml", why...)
	} else {
		fmt.Fprintln(out, "CHECK OK ansible.posix")
	}
	// The ansible-playbook line above makes a venv; say so when this python3 cannot.
	if u.bin["ansible-playbook"] == "" && fam != "brew" {
		if py, err := lookPath("python3"); err != nil {
			miss("python3", fix("python3-venv", fam))
		} else if _, why, c := outCmd(py, []string{"-c", "import ensurepip, venv"}, os.Environ()); c != 0 {
			miss("python3-venv", fix("python3-venv", fam), py+" has no venv module: "+oneLine(why))
		}
	}
	if p := u.bin["tailscale"]; p != "" {
		if o, why, c := outCmd(p, []string{"status", "--peers=false"}, os.Environ()); c != 0 {
			fmt.Fprintf(out, "CHECK NOTE tailnet this node is not on it; up joins it, tagged tag:control, with a key minted by TAILSCALE_API_KEY: %s\n", oneLine(why+" "+o))
		} else {
			fmt.Fprintln(out, "CHECK OK tailnet")
			if st, on := u.status(); on {
				traffic(out, st)
			}
		}
	}
	if p := u.bin["gh"]; p != "" {
		if _, why, c := outCmd(p, []string{"auth", "status"}, os.Environ()); c != 0 {
			miss("gh-login", "gh auth login", "a browser login, not a paste: the runners register with a token gh asks GitHub for;", why)
		} else {
			fmt.Fprintln(out, "CHECK OK gh-login")
		}
	}
	// The secrets store, key, seat and pins: NOVA_SECRETS_*, else nova-secrets' own defaults (the short hostname,
	// ~/.config/nova-secrets/{store,<seat>.key,pins}), set in this process's environment so every nova-secrets and
	// play it runs reads the same ones. A default that is there prints nothing; one that is not prints the command
	// that makes it there.
	host, _ := os.Hostname() // ignored: no hostname leaves the seat unset, and its line says so
	home, _ := os.UserHomeDir()
	dir := filepath.Join(cmp.Or(os.Getenv("HOME"), home), ".config", "nova-secrets")
	seat, _, _ := strings.Cut(host, ".")
	seat = cmp.Or(os.Getenv("NOVA_SECRETS_SEAT"), seat)
	inputs := []struct {
		env, name, def, how, new string
		to                       *string
	}{
		{"NOVA_SECRETS_SEAT", "seat", seat, "export NOVA_SECRETS_SEAT=<this node's seat>", "this node has no hostname", &u.seat},
		{"NOVA_SECRETS_STORE", "store", filepath.Join(dir, "store"), "nova-secrets init --recovery <age public key> --pins " + filepath.Join(dir, "pins"), "a new store; the recovery key's private half is kept offline", &u.store},
		{"NOVA_SECRETS_KEY", "key", filepath.Join(dir, seat+".key"), "age-keygen -o " + filepath.Join(dir, seat+".key") + " && nova-secrets seat add " + seat + ` "$(age-keygen -y ` + filepath.Join(dir, seat+".key") + `)"`, "this seat's key", &u.key},
		{"NOVA_SECRETS_PINS", "pins", filepath.Join(dir, "pins"), "mkdir -p " + filepath.Join(dir, "pins"), "nova-secrets init --pins writes recovery.pub there", &u.pins},
	}
	// A fresh node (none of them set, none there) prints none of them: nova-secrets set makes them all.
	fresh := true
	for _, in := range inputs[1:] {
		_, err := os.Lstat(in.def)
		fresh = fresh && os.Getenv(in.env) == "" && err != nil
	}
	ready := !fresh
	for _, in := range inputs {
		val, set := os.LookupEnv(in.env)
		if val == "" {
			val, set = in.def, false
			_ = os.Setenv(in.env, val) // ignored: a valid name; the children read the default from here
		}
		_, err := os.Stat(val)
		switch {
		case val == "" || (in.name != "seat" && err != nil && set):
			miss(in.name, "unset "+in.env, in.env+"="+val+" is not there; unset, the default is used, or make it:", in.how)
			ready = false
		case in.name != "seat" && err != nil && !fresh:
			miss(in.name, in.how, in.new)
			ready = false
		case set:
			fmt.Fprintf(out, "CHECK OK %s %s\n", in.name, val)
		}
		*in.to = val
	}
	// The one credential a human gives: everything else up does with it through the Tailscale API.
	how := "paste the token when it asks: one line tskey-api-..., from the Tailscale admin console > Settings > Keys > Generate API access token"
	if fresh && u.bin["nova-secrets"] != "" {
		miss("TAILSCALE_API_KEY", "nova-secrets set TAILSCALE_API_KEY", how+"; on this fresh node it makes the secrets store, the keys and the seat first")
	} else if !ready || u.bin["nova-secrets"] == "" {
		miss("TAILSCALE_API_KEY", "nova-secrets set TAILSCALE_API_KEY", how+", once the lines above are done")
	} else if _, why, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{"TAILSCALE_API_KEY"}, "true")[1:], os.Environ()); c != 0 {
		miss("TAILSCALE_API_KEY", "nova-secrets set TAILSCALE_API_KEY", how+"; the read said:", why)
	} else {
		fmt.Fprintf(out, "CHECK OK TAILSCALE_API_KEY readable in seat %s\n", u.seat)
	}
	// A fleet that exists (its store's password is in this seat) is read from NOVA_PG_DSN.
	if ready && u.bin["nova-secrets"] != "" {
		names, why, c := outCmd(u.bin["nova-secrets"], []string{"names", "--as", u.seat}, os.Environ())
		switch dsn := os.Getenv("NOVA_PG_DSN"); {
		case dsn != "":
			fmt.Fprintf(out, "CHECK OK NOVA_PG_DSN %s\n", dsn)
		case c != 0:
			miss("NOVA_PG_DSN", u.bin["nova-secrets"]+" names --as "+u.seat, "the seat's names cannot be read, so whether a fleet exists is unknown:", why)
		case slices.Contains(strings.Fields(names), "NOVA_PG_PASSWORD"):
			fmt.Fprintf(out, "CHECK NOTE NOVA_PG_DSN unset, and seat %s holds NOVA_PG_PASSWORD: up takes the first machine named for the store it made; for nova-config, and to add machines: export NOVA_PG_DSN=postgres://nova_config@<the store machine's tailnet name>:5432/nova\n", u.seat)
		default:
			fmt.Fprintln(out, "CHECK OK NOVA_PG_DSN none yet (a new fleet: up prints it)")
		}
	}
	return missing
}

// trafficWarnBytes is the tailnet traffic between this node and one peer, either way, above which check prints
// CHECK WARN. D8 (2026-11-07): this node's tailscaled had sent 1.08 TB, nearly all of it to the benches' timed
// mirror fetches, and no tool measured it. The fleet's own traffic with a peer is small (binaries installed, the
// store pushed, nova-config rows and bus messages: megabytes a day), so 100 GB with one peer since tailscaled
// started is a saturation to look at whatever the fleet's size; a share of the total would fire on any two-machine fleet.
const trafficWarnBytes = 100e9

// traffic prints one CHECK traffic line per peer this node has exchanged bytes with, sorted by name, GB to one
// decimal, and CHECK WARN traffic for a peer over trafficWarnBytes in either direction. A peer with no bytes is no line.
func traffic(out io.Writer, st tsStatus) {
	gb := func(b int64) string { return fmt.Sprintf("%.1fGB", float64(b)/1e9) }
	for _, p := range slices.SortedFunc(maps.Values(st.Peer), func(a, b tsNode) int { return strings.Compare(a.DNSName, b.DNSName) }) {
		if p.TxBytes+p.RxBytes == 0 {
			continue
		}
		name, _, _ := strings.Cut(p.DNSName, ".")
		fmt.Fprintf(out, "CHECK traffic %s tx=%s rx=%s\n", name, gb(p.TxBytes), gb(p.RxBytes))
		over := ""
		if float64(p.TxBytes) > trafficWarnBytes {
			over = "tx=" + gb(p.TxBytes)
		} else if float64(p.RxBytes) > trafficWarnBytes {
			over = "rx=" + gb(p.RxBytes)
		}
		if over != "" {
			fmt.Fprintf(out, "CHECK WARN traffic %s %s is over %.0fGB with one peer since tailscaled started: something fetches or mirrors on a clock; find it: nova-config service list\n",
				name, over, trafficWarnBytes/1e9)
		}
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func runCheck(args []string, stdout, stderr io.Writer) int {
	u := &upRun{stderr: stderr}
	for i := 0; i < len(args); i++ {
		switch a, v, has := args[i], "", false; {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, checkHelp)
			return 0
		case a == "--runners" || strings.HasPrefix(a, "--runners="):
			if _, v, has = strings.Cut(a, "="); !has {
				if i+1 >= len(args) {
					fmt.Fprintln(stderr, "nova-fleet check: --runners wants a URL; run: nova-fleet check -h")
					return 2
				}
				i++
				v = args[i]
			}
			if runnerScope(v) == "" {
				fmt.Fprintf(stderr, "nova-fleet check: --runners wants https://github.com/<owner>/<repo> or https://github.com/<org>, got %s; run: nova-fleet check -h\n", v)
				return 2
			}
			u.runners = v
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "nova-fleet check: unknown flag %s; run: nova-fleet check -h\n", a)
			return 2
		default:
			fmt.Fprintf(stderr, "nova-fleet check: unexpected argument %s; run: nova-fleet check -h\n", a)
			return 2
		}
	}
	if n := u.control(stdout); n > 0 {
		fmt.Fprintf(stderr, "CHECK FAILED missing=%d\n", n)
		return 1
	}
	fmt.Fprintf(stdout, "CHECK OK missing=0\nnext: nova-fleet up --check --tools <TAG|DIR>%s <name>=<address>...\n", map[bool]string{true: " --runners " + u.runners}[u.runners != ""])
	return 0
}
