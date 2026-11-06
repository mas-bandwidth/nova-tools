package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
OS, pasteable as it stands, a reason after # when there is one, in paste order>; this node on the tailnet and tagged tag:control; the store, key, seat and
pins of NOVA_SECRETS_STORE, NOVA_SECRETS_KEY, NOVA_SECRETS_SEAT and NOVA_SECRETS_PINS;
TAILSCALE_AUTHKEY (and GITHUB_RUNNER_TOKEN with --runners) read from the seat, the reason
printed when a read fails; and NOVA_PG_DSN once a fleet exists. Tailscale and Ansible are
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
		return "mkdir -p ~/.local/bin && ([ -d nova-tools ] || git clone --depth 1 https://github.com/mas-bandwidth/nova-tools) && go build -C nova-tools -o ~/.local/bin/" + name + " ./" + t
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
	needGo := slices.ContainsFunc(want, func(b string) bool { _, err := lookPath(b); return err != nil && strings.HasPrefix(b, "nova-") })
	u.bin = map[string]string{}
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
		p, err := lookPath(b)
		if err != nil {
			miss(b, fix(b, fam))
			continue
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
	if p := u.bin["ansible-playbook"]; p != "" { // the collections the plays use (requirements.yml)
		galaxy := filepath.Join(filepath.Dir(p), "ansible-galaxy")
		if o, _, _ := outCmd(galaxy, []string{"collection", "list"}, os.Environ()); !strings.Contains(o, "ansible.posix") {
			miss("ansible.posix", "nova-fleet show requirements > requirements.yml && "+galaxy+" collection install -r requirements.yml")
		} else {
			fmt.Fprintln(out, "CHECK OK ansible.posix")
		}
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
			miss("tailnet", "sudo tailscale up --advertise-tags=tag:control", "this node is not on the tailnet, which it reaches the machines over:", why, o)
		} else {
			fmt.Fprintln(out, "CHECK OK tailnet")
			var self struct{ Self struct{ Tags []string } }
			if o, _, _ := outCmd(p, []string{"status", "--json"}, os.Environ()); json.Unmarshal([]byte(o), &self) != nil || !slices.Contains(self.Self.Tags, "tag:control") {
				miss("tag:control", "sudo tailscale up --advertise-tags=tag:control --auth-key=<a key allowed that tag>", "or tag this node in the admin console: the policy (nova-fleet show tailscale-acl) lets tag:control reach the machines")
			} else {
				fmt.Fprintln(out, "CHECK OK tag:control")
			}
		}
	}
	u.store, u.key, u.seat, u.pins = os.Getenv("NOVA_SECRETS_STORE"), os.Getenv("NOVA_SECRETS_KEY"), os.Getenv("NOVA_SECRETS_SEAT"), os.Getenv("NOVA_SECRETS_PINS")
	inputs := []struct{ env, name, val, how, new string }{
		{"NOVA_SECRETS_STORE", "store", u.store, "export NOVA_SECRETS_STORE=<your clone of the secrets store>", "new: nova-secrets init --store <dir> --recovery <age public key> --pins <pins dir>"},
		{"NOVA_SECRETS_KEY", "key", u.key, "export NOVA_SECRETS_KEY=<this seat's age key file>", "new: age-keygen -o ~/.config/nova-secrets/<seat>.key"},
		{"NOVA_SECRETS_SEAT", "seat", u.seat, "export NOVA_SECRETS_SEAT=<this node's seat>", "new: nova-secrets seat add <seat> <age public key> --store <store>"},
		{"NOVA_SECRETS_PINS", "pins", u.pins, "export NOVA_SECRETS_PINS=<the directory of pinned machine keys, outside the store>", "new: mkdir -p ~/.config/nova-secrets/pins; nova-secrets init --pins writes recovery.pub there"},
	}
	ready := true
	for _, in := range inputs {
		_, err := os.Stat(in.val)
		switch {
		case in.val == "":
			miss(in.name, in.how, in.new)
			ready = false
		case in.name != "seat" && err != nil:
			miss(in.name, in.how, in.env+"="+in.val+" is not there;", in.new)
			ready = false
		default:
			fmt.Fprintf(out, "CHECK OK %s %s\n", in.name, in.val)
		}
	}
	read := []string{"TAILSCALE_AUTHKEY"}
	if u.runners != "" {
		read = append(read, "GITHUB_RUNNER_TOKEN")
	}
	for _, name := range read {
		if !ready || u.bin["nova-secrets"] == "" {
			miss(name, "nova-secrets set "+name+" --as <seat>", "the value on stdin, once the store, key and seat above are set")
			continue
		}
		if _, why, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{name}, "true")[1:], os.Environ()); c != 0 {
			miss(name, fmt.Sprintf("nova-secrets set %s --as %s", name, u.seat), "the value on stdin; the read said:", why)
		} else {
			fmt.Fprintf(out, "CHECK OK %s readable in seat %s\n", name, u.seat)
		}
	}
	// A fleet that exists (its store's password is in this seat) is read from NOVA_PG_DSN.
	if ready && u.bin["nova-secrets"] != "" {
		names, _, _ := outCmd(u.bin["nova-secrets"], []string{"names", "--as", u.seat}, os.Environ())
		switch dsn := os.Getenv("NOVA_PG_DSN"); {
		case dsn != "":
			fmt.Fprintf(out, "CHECK OK NOVA_PG_DSN %s\n", dsn)
		case slices.Contains(strings.Fields(names), "NOVA_PG_PASSWORD"):
			fmt.Fprintf(out, "CHECK NOTE NOVA_PG_DSN unset, and seat %s holds NOVA_PG_PASSWORD: up takes the first machine named for the store it made; for nova-config, and to add machines: export NOVA_PG_DSN=postgres://nova_config@<the store machine's tailnet name>:5432/nova\n", u.seat)
		default:
			fmt.Fprintln(out, "CHECK OK NOVA_PG_DSN none yet (a new fleet: up prints it)")
		}
	}
	return missing
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
	fmt.Fprintln(stdout, "CHECK OK missing=0")
	return 0
}
