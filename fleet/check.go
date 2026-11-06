package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const checkHelp = `nova-fleet check [--runners URL]

The control node's preflight, the first step of nova-fleet up: one line per prerequisite,
CHECK OK <name> <version|path> or CHECK MISSING <name>: <the command that fixes it, for this
OS>, then the secrets up needs (the store, key and seat of NOVA_SECRETS_STORE, NOVA_SECRETS_KEY
and NOVA_SECRETS_SEAT; TAILSCALE_AUTHKEY, and GITHUB_RUNNER_TOKEN with --runners, read from the
seat, the reason printed when a read fails), and whether this node is on the tailnet. Nothing
is changed. Exit: 0 all present; 1 something missing (CHECK FAILED missing=<n>); 2 could not run.
`

// osRelease is read for the Linux family; tests point it elsewhere.
var osRelease = "/etc/os-release"

var versionRE = regexp.MustCompile(`[0-9]+\.[0-9]+(\.[0-9]+)?`)

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
		"age":        {"age", "age", "age"},
		"age-keygen": {"age", "age", "age"},
		"psql":       {"postgresql-client", "postgresql", "libpq && brew link --force libpq"},
		"git":        {"git", "git", "git"},
		"gpg":        {"gnupg", "gnupg2", "gnupg"},
		"gh":         {"gh", "gh", "gh"},
		"rsync":      {"rsync", "rsync", "rsync"},
	}
	switch {
	case strings.HasPrefix(name, "nova-"):
		t := strings.TrimPrefix(name, "nova-")
		return "git clone --depth 1 https://github.com/mas-bandwidth/nova-tools && (cd nova-tools && go build -o ~/.local/bin/" + name + " ./" + t + ")"
	case name == "ansible-playbook" && fam == "brew":
		return "brew install ansible"
	case name == "ansible-playbook":
		venv := "python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH"
		if fam == "apt" { // Debian and Ubuntu ship python3 without venv
			return "sudo apt install -y python3-venv && " + venv
		}
		return venv
	case name == "python3-venv":
		return map[bool]string{true: "sudo apt install -y python3-venv", false: "sudo dnf install -y python3"}[fam == "apt"]
	case name == "tailscale" && fam == "brew":
		return "brew install --cask tailscale"
	case name == "tailscale":
		return "curl -fsSL https://tailscale.com/install.sh | sh"
	case name == "sops" && fam == "brew":
		return "brew install sops"
	case name == "sops":
		// The pin is the plays' own (group_vars/all.yml), so this line and join.yml cannot drift.
		v, sum := pin("nova_sops_version"), pin("nova_sops_sha256", "linux-"+goarch)
		return "curl -fsSLo ~/.local/bin/sops https://github.com/getsops/sops/releases/download/v" + v + "/sops-v" + v + ".linux." + goarch +
			" && echo '" + sum + "  '\"$HOME\"/.local/bin/sops | sha256sum -c && chmod +x ~/.local/bin/sops"
	}
	p, ok := pkg[name]
	switch {
	case !ok:
		return "install " + name + " and put it on PATH"
	case fam == "apt":
		return "sudo apt install -y " + p[0]
	case fam == "dnf":
		return "sudo dnf install -y " + p[1]
	case fam == "brew":
		return "brew install " + p[2]
	}
	return "install " + p[0] + " with this system's package manager"
}

// control checks this node and fills u.bin, u.store, u.key and u.seat; it returns how
// many prerequisites are missing.
func (u *upRun) control(out io.Writer) int {
	want := []string{"ansible-playbook", "nova-config", "nova-secrets", "nova-bus", "tailscale", "psql", "git", "gpg", "sops", "age", "age-keygen", "rsync"}
	if u.runners != "" {
		want = append(want, "gh")
	}
	versionArgs := map[string][]string{"tailscale": {"version"}, "nova-config": nil, "nova-secrets": nil, "nova-bus": nil}
	fam, missing := family(), 0
	miss := func(name, how string) {
		missing++
		fmt.Fprintf(out, "CHECK MISSING %s: %s\n", name, how)
	}
	u.bin = map[string]string{}
	for _, b := range want {
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
	// The ansible-playbook line above makes a venv; say so when this python3 cannot.
	if u.bin["ansible-playbook"] == "" && fam != "brew" {
		if py, err := lookPath("python3"); err != nil {
			miss("python3", fix("python3-venv", fam))
		} else if _, why, c := outCmd(py, []string{"-c", "import ensurepip, venv"}, os.Environ()); c != 0 {
			miss("python3-venv", py+" has no venv module ("+oneLine(why)+"); "+fix("python3-venv", fam))
		}
	}
	if p := u.bin["tailscale"]; p != "" {
		if _, why, c := outCmd(p, []string{"status", "--peers=false"}, os.Environ()); c != 0 {
			miss("tailnet", "sudo tailscale up (this node on the tailnet, so it reaches the machines by name; "+oneLine(why)+")")
		} else {
			fmt.Fprintln(out, "CHECK OK tailnet")
		}
	}
	u.store, u.key, u.seat = os.Getenv("NOVA_SECRETS_STORE"), os.Getenv("NOVA_SECRETS_KEY"), os.Getenv("NOVA_SECRETS_SEAT")
	inputs := []struct{ env, name, val, how string }{
		{"NOVA_SECRETS_STORE", "store", u.store, "export NOVA_SECRETS_STORE=<your clone of the secrets store> (new: nova-secrets init --store <dir> --recovery <age public key>)"},
		{"NOVA_SECRETS_KEY", "key", u.key, "export NOVA_SECRETS_KEY=<this seat's age key file> (new: age-keygen -o ~/.config/nova-secrets/<seat>.key)"},
		{"NOVA_SECRETS_SEAT", "seat", u.seat, "export NOVA_SECRETS_SEAT=<this node's seat> (new: nova-secrets seat add <seat> <age public key> --store <store>)"},
	}
	ready := true
	for _, in := range inputs {
		_, err := os.Stat(in.val)
		switch {
		case in.val == "":
			miss(in.name, in.how)
			ready = false
		case in.name != "seat" && err != nil:
			miss(in.name, in.env+"="+in.val+" is not there; "+in.how)
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
			miss(name, "set the store, key and seat above, then: nova-secrets set "+name+" --as <seat> (the value on stdin)")
			continue
		}
		if _, why, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{name}, "true")[1:], os.Environ()); c != 0 {
			miss(name, fmt.Sprintf("nova-secrets set %s --as %s (the value on stdin); the read said: %s", name, u.seat, oneLine(why)))
		} else {
			fmt.Fprintf(out, "CHECK OK %s readable in seat %s\n", name, u.seat)
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
