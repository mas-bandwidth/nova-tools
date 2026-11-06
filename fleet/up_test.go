package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// upFake stands in for every program up runs: lookPath finds every helper, runCmd
// and outCmd record each command line, and outCmd answers by the command's words.
type upFake struct {
	calls   []string
	names   string // nova-secrets names
	readErr string // a nova-secrets exec -- true refusal
	ip      string
	sent    string
	fail    string // a call containing this word exits 4
	store   string
	key     string
	offnet  bool // tailscale status fails
	noVenv  bool // python3 -c 'import ensurepip, venv' fails
}

func (f *upFake) install(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	f.store, f.key = filepath.Join(t.TempDir(), "store"), filepath.Join(t.TempDir(), "control.key")
	if err := os.Mkdir(f.store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.key, []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_SECRETS_STORE", f.store)
	t.Setenv("NOVA_SECRETS_KEY", f.key)
	t.Setenv("NOVA_SECRETS_SEAT", "control")
	oldLook, oldRun, oldOut := lookPath, runCmd, outCmd
	t.Cleanup(func() { lookPath, runCmd, outCmd = oldLook, oldRun, oldOut })
	lookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	record := func(bin string, args []string) string {
		for i, a := range args {
			if strings.HasSuffix(a, ".yml") || strings.HasSuffix(a, ".ini") || strings.HasSuffix(a, "/inventory") {
				args[i] = filepath.Base(a)
			}
		}
		line := filepath.Base(bin) + " " + strings.Join(args, " ")
		f.calls = append(f.calls, line)
		return line
	}
	runCmd = func(bin string, args, env []string) (int, error) {
		line := record(bin, slices.Clone(args))
		if strings.Contains(line, "nova-bus send") {
			f.sent = args[len(args)-1]
		}
		if f.fail != "" && strings.Contains(line, f.fail) {
			return 4, nil
		}
		return 0, nil
	}
	outCmd = func(bin string, args, env []string) (string, string, int) {
		line := record(bin, slices.Clone(args))
		switch {
		case strings.Contains(line, "import ensurepip, venv") && f.noVenv:
			return "", "ModuleNotFoundError: No module named 'ensurepip'", 1
		case strings.Contains(line, "-- true") && f.readErr != "":
			return "", f.readErr, 125
		case strings.Contains(line, "nova-secrets names"):
			return f.names, "", 0
		case strings.HasSuffix(line, "--version") || strings.HasSuffix(line, " version"):
			return filepath.Base(bin) + " version 1.2.3\n", "", 0
		case strings.HasPrefix(line, "tailscale status") && f.offnet:
			return "", "Logged out.", 1
		case strings.Contains(line, "ls-remote --get-url"):
			return "git@example.com:o/store.git\n", "", 0
		case strings.HasPrefix(line, "tailscale ip"):
			return f.ip + "\n", "", 0
		case strings.Contains(line, "pending"):
			return "1 nova-fleet-up up check " + f.sent + "\n", "", 0
		}
		return "", "", 0
	}
}

// plays lists the plays a run ran, in order, with the inventory each used.
func (f *upFake) plays() []string {
	var out []string
	for _, c := range f.calls {
		w := strings.Fields(c)
		if i := slices.IndexFunc(w, func(s string) bool { return strings.HasSuffix(s, "ansible-playbook") }); i >= 0 && i+3 < len(w) && w[i+1] == "-i" {
			out = append(out, w[i+3]+"@"+w[i+2])
		}
	}
	return out
}

func TestUpParse(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "nova-fleet up: missing <name>=<address> and --tools; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1.0.0"}, "nova-fleet up: missing <name>=<address>; run: nova-fleet up -h\n"},
		{[]string{"--bogus"}, "nova-fleet up: unknown flag --bogus; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "Web=1.2.3.4"}, "nova-fleet up: want a machine as <name>=<address>[/<os>/<arch>] (a lowercase name), got Web=1.2.3.4; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "a=1.2.3.4/linux"}, "nova-fleet up: want a machine as"},
		{[]string{"--tools", "v1", "a=1", "a=2"}, "nova-fleet up: machine a is named twice"},
		{[]string{"--tools", "v1", "--runners", "https://gitlab.com/x", "a=1"}, "nova-fleet up: --runners wants"},
		{[]string{"--tools", "v1", "--runner-count", "0", "a=1"}, "nova-fleet up: --runner-count wants a number of at least 1"},
		{[]string{"--tools"}, "nova-fleet up: --tools wants a value"},
	} {
		code, out, errs := runArgs(append([]string{"up"}, tc.args...)...)
		if code != 2 || out != "" || !strings.HasPrefix(errs, tc.want) {
			t.Errorf("up %q: code %d stdout %q stderr %q, want %q", tc.args, code, out, errs, tc.want)
		}
	}
	u, _ := parseUp([]string{"--check", "--user=fleet", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "a=10.0.0.1", "b=b.example/darwin/arm64"}, nil)
	if !u.check || u.user != "fleet" || u.runners != "https://github.com/o/r" || u.runnerCount != 1 ||
		!slices.Equal(u.machines, []machine{{"a", "10.0.0.1", "linux", "amd64"}, {"b", "b.example", "darwin", "arm64"}}) {
		t.Errorf("parsed %+v", u)
	}
	for url, want := range map[string]string{"https://github.com/o/r": "repo", "https://github.com/o": "org", "https://github.com/o/r/x": "", "http://github.com/o": ""} {
		if got := runnerScope(url); got != want {
			t.Errorf("runnerScope(%s) = %q, want %q", url, got, want)
		}
	}
}

func TestUpControlRefusals(t *testing.T) {
	f := &upFake{}
	f.install(t)
	t.Setenv("NOVA_SECRETS_KEY", "")
	lookPath = func(name string) (string, error) {
		if name == "psql" || name == "gh" {
			return "", os.ErrNotExist
		}
		return "/bin/" + name, nil
	}
	code, out, errs := runArgs("up", "--tools", "v1.0.0", "--runners", "https://github.com/o/r", "a=1.2.3.4")
	for _, w := range []string{"CHECK MISSING psql: ", "CHECK MISSING gh: ", "CHECK MISSING key: export NOVA_SECRETS_KEY=",
		"CHECK MISSING TAILSCALE_AUTHKEY: set the store, key and seat above", "CHECK OK sops 1.2.3"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if code != 2 || !strings.HasPrefix(errs, "UP CONTROL FAILED missing=5;") || len(f.plays()) != 0 {
		t.Errorf("code %d stderr %q plays %v", code, errs, f.plays())
	}
	f = &upFake{readErr: "SECRETS EXEC REFUSED name=TAILSCALE_AUTHKEY reason=not-in-seat\n"}
	f.install(t)
	code, out, errs = runArgs("up", "--check", "--tools", "v1.0.0", "a=1.2.3.4")
	if code != 2 || !strings.Contains(out, "CHECK MISSING TAILSCALE_AUTHKEY: nova-secrets set TAILSCALE_AUTHKEY --as control (the value on stdin); the read said: SECRETS EXEC REFUSED name=TAILSCALE_AUTHKEY reason=not-in-seat") {
		t.Errorf("a refused secret read under --check: code %d stdout %q stderr %q", code, out, errs)
	}
	if len(f.plays()) != 0 {
		t.Errorf("plays ran after a refused read: %v", f.plays())
	}
}

func TestUpOrder(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", ip: "100.64.0.7"}
	f.install(t)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "--runner-count", "2", "first=203.0.113.7", "second=203.0.113.8")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	want := []string{"tailscale.yml@first.ini", "postgres.yml@first.ini", "redis.yml@first.ini", "join.yml@inventory", "join.yml@inventory",
		"secrets-refresh.yml@inventory", "health.yml@inventory", "install.yml@inventory", "services.yml@inventory", "github-runners.yml@inventory"}
	if got := f.plays(); !slices.Equal(got, want) {
		t.Errorf("plays = %v, want %v", got, want)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"nova-secrets exec --only TAILSCALE_AUTHKEY -- true",
		"nova-secrets exec --only GITHUB_RUNNER_TOKEN -- true",
		"nova-secrets exec --only TAILSCALE_AUTHKEY -- /bin/ansible-playbook -i first.ini tailscale.yml -e nova_tailscale_record_address=false -e nova_firewall=false",
		"redis.yml -e nova_seal_store=" + f.store + " -e nova_seal_seat=control -e nova_seal_key=" + f.key,
		"tailscale ip -4 first",
		"nova-secrets exec --only NOVA_PG_PASSWORD -- sh -c",
		"postgres://nova_config@100.64.0.7:5432/nova",
		"nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD -- /bin/ansible-playbook -i inventory join.yml -e nova_join_host=first -e nova_join_user=nova -e nova_join_address=203.0.113.7 -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=services,bench,runner",
		"-e nova_join_host=second -e nova_join_user=nova -e nova_join_address=203.0.113.8 -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner",
		"nova-secrets grant NOVA_BUS_PASSWORD --to first\n",
		"nova-secrets grant NOVA_BUS_PASSWORD --to second\n",
		"nova-secrets grant NOVA_BUS_PASSWORD --to second\ngit -C " + f.store + " rev-parse",
		"git -C " + f.store + " push --quiet",
		"secrets-refresh.yml -e nova_secrets_repo=git@example.com:o/store.git\n",
		"install.yml -e nova_tools_version=v2.0.0",
		"github-runners.yml -e github_runner_url=https://github.com/o/r -e github_runner_scope=repo -e runner_count=2",
		"nova-secrets exec --only NOVA_BUS_PASSWORD -- /bin/nova-bus send --as nova-fleet-up --to nova-fleet-up up check up-",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	for _, w := range []string{"UP CONTROL OK", "UP FIRST OK first pg=100.64.0.7:5432", "UP GRANT first OK NOVA_BUS_PASSWORD", "UP GRANT second OK NOVA_BUS_PASSWORD", "UP STORE OK", "UP BUS OK 100.64.0.7:6379 round-trip=up-",
		"UP MACHINE first OK address=203.0.113.7 steps=tailnet,postgres,redis,join,grant,refresh,health,tools,services,runners,bus",
		"UP MACHINE second OK address=203.0.113.8 steps=join,grant,refresh,health,tools,services,runners,bus"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
}

func TestUpStopsAtAFailedStep(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD", ip: "100.64.0.7", fail: "nova_join_host=second"}
	f.install(t)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1", "second=2.2.2.2")
	if code != 4 || !strings.Contains(errs, "UP join second FAILED exit=4;") || strings.Contains(out, "UP MACHINE") {
		t.Errorf("code %d\n%s\n%s", code, out, errs)
	}
	if got := f.plays(); got[len(got)-1] != "join.yml@inventory" {
		t.Errorf("ran past the failed join: %v", got)
	}
}

func TestUpCheck(t *testing.T) {
	// A fresh fleet: no PostgreSQL yet, so --check stops after the first machine's plays, saying so.
	f := &upFake{}
	f.install(t)
	code, out, errs := runArgs("up", "--check", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if got := f.plays(); !slices.Equal(got, []string{"tailscale.yml@first.ini", "postgres.yml@first.ini", "redis.yml@first.ini"}) {
		t.Errorf("plays = %v", got)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "tailscale.yml --check --diff -e") || !strings.Contains(out, "UP FIRST WOULD-RUN schema and every step after it") {
		t.Errorf("calls %v\nstdout %s", f.calls, out)
	}
	// A fleet that exists: every step runs under --check, nothing is pushed, applied or sent.
	f = &upFake{names: "NOVA_PG_PASSWORD", ip: "100.64.0.7"}
	f.install(t)
	code, out, _ = runArgs("up", "--check", "--grant", "NOVA_BUS_PASSWORD,EXTRA", "--tools", "v2.0.0", "first=1.1.1.1")
	all := strings.Join(f.calls, "\n")
	if code != 0 || len(f.plays()) != 8 || strings.Contains(all, "push") || strings.Contains(all, "sh -c") || strings.Contains(all, "nova-bus send") ||
		strings.Contains(all, "nova-secrets grant") || !strings.Contains(all, "nova-secrets exec --only NOVA_BUS_PASSWORD,EXTRA -- true") {
		t.Errorf("code %d plays %v calls:\n%s", code, f.plays(), all)
	}
	for _, w := range []string{"UP GRANT first WOULD-GRANT NOVA_BUS_PASSWORD,EXTRA (check: nothing changed)", "UP MACHINE first CHECKED", "steps=tailnet,postgres,redis,join,grant,refresh,health,tools,services"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	// --check reads what it would grant, and stops with the reason when it cannot.
	f = &upFake{names: "NOVA_PG_PASSWORD", ip: "100.64.0.7"}
	f.install(t)
	oldOut := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if strings.Join(args, " ") == "exec --only NOVA_BUS_PASSWORD -- true" {
			return "", "seat control holds no NOVA_BUS_PASSWORD", 3
		}
		return oldOut(bin, args, env)
	}
	code, _, errs = runArgs("up", "--check", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 3 || !strings.Contains(errs, "seat control cannot read NOVA_BUS_PASSWORD to grant it: seat control holds no NOVA_BUS_PASSWORD") || !strings.Contains(errs, "UP grant FAILED exit=3") {
		t.Errorf("unreadable grant: code %d stderr %q", code, errs)
	}
}

func TestLocalVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("abc  nova-bus_local_linux_amd64\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := localVersion(dir); err != nil || v != "local" {
		t.Errorf("localVersion = %q %v", v, err)
	}
	if _, err := localVersion(t.TempDir()); err == nil {
		t.Error("no SHA256SUMS: want an error")
	}
}

// Every play up runs is in the binary.
func TestUpPlaysAreEmbedded(t *testing.T) {
	for _, p := range []string{"tailscale", "postgres", "redis", "join", "secrets-refresh", "health", "install", "services", "github-runners"} {
		if !slices.Contains(plays(tree), p) {
			t.Errorf("up runs %s.yml, which is not embedded", p)
		}
	}
	b, _ := os.ReadFile("join.yml")
	if header(string(b), "argument") != "nova_join_host" {
		t.Error("join.yml does not take its machine as the argument (# argument: nova_join_host)")
	}
	if f, ok := showable(tree, "tailscale-acl"); !ok || f != "tailscale-acl.json" {
		t.Errorf("nova-fleet show tailscale-acl resolves to %q %v", f, ok)
	}
}

func TestCheck(t *testing.T) {
	f := &upFake{}
	f.install(t)
	code, out, errs := runArgs("check")
	if code != 0 || errs != "" || !strings.HasSuffix(out, "CHECK OK missing=0\n") {
		t.Fatalf("all present: code %d stdout %q stderr %q", code, out, errs)
	}
	for _, w := range []string{"CHECK OK ansible-playbook 1.2.3\n", "CHECK OK tailscale 1.2.3\n", "CHECK OK nova-config /bin/nova-config\n",
		"CHECK OK tailnet\n", "CHECK OK store " + f.store + "\n", "CHECK OK seat control\n", "CHECK OK TAILSCALE_AUTHKEY readable in seat control\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "GITHUB_RUNNER_TOKEN") || strings.Contains(out, " gh ") {
		t.Errorf("runner prerequisites checked without --runners:\n%s", out)
	}
	f.offnet = true
	lookPath = func(name string) (string, error) {
		if name == "age-keygen" || name == "nova-bus" || name == "gh" {
			return "", os.ErrNotExist
		}
		return "/bin/" + name, nil
	}
	code, out, errs = runArgs("check", "--runners", "https://github.com/o/r")
	for _, w := range []string{"CHECK MISSING age-keygen: ", "CHECK MISSING nova-bus: git clone --depth 1 https://github.com/mas-bandwidth/nova-tools && (cd nova-tools && go build -o ~/.local/bin/nova-bus ./bus)",
		"CHECK MISSING gh: ", "CHECK MISSING tailnet: sudo tailscale up", "Logged out.", "CHECK OK GITHUB_RUNNER_TOKEN readable"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if code != 1 || errs != "CHECK FAILED missing=4\n" {
		t.Errorf("code %d stderr %q", code, errs)
	}
	// No ansible-playbook and a python3 without venv: both are said.
	old := goos
	t.Cleanup(func() { goos = old })
	goos, f.offnet, f.noVenv = "linux", false, true
	lookPath = func(name string) (string, error) {
		if name == "ansible-playbook" {
			return "", os.ErrNotExist
		}
		return "/bin/" + name, nil
	}
	code, out, _ = runArgs("check")
	if code != 1 || !strings.Contains(out, "CHECK MISSING ansible-playbook: ") ||
		!strings.Contains(out, "CHECK MISSING python3-venv: /bin/python3 has no venv module (ModuleNotFoundError: No module named 'ensurepip'); ") {
		t.Errorf("venv-less python3: code %d\n%s", code, out)
	}
	f.noVenv = false
	if _, out, _ = runArgs("check"); strings.Contains(out, "CHECK MISSING python3") {
		t.Errorf("python3 with venv reported missing:\n%s", out)
	}
	for _, tc := range [][]string{{"--bogus"}, {"x"}, {"--runners"}, {"--runners", "https://example.com/x"}} {
		if code, _, errs := runArgs(append([]string{"check"}, tc...)...); code != 2 || !strings.HasPrefix(errs, "nova-fleet check: ") || !strings.HasSuffix(errs, "; run: nova-fleet check -h\n") {
			t.Errorf("check %q: code %d stderr %q", tc, code, errs)
		}
	}
}

func TestFixPerFamily(t *testing.T) {
	dir := t.TempDir()
	old, oldOS := osRelease, goos
	t.Cleanup(func() { osRelease, goos = old, oldOS })
	for _, tc := range []struct{ os, release, want string }{
		{"linux", "ID=ubuntu\nID_LIKE=debian\n", "apt"},
		{"linux", "ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n", "dnf"},
		{"linux", "ID=arch\n", ""},
		{"darwin", "", "brew"},
	} {
		osRelease, goos = filepath.Join(dir, "os-release"), tc.os
		if err := os.WriteFile(osRelease, []byte(tc.release), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := family(); got != tc.want {
			t.Errorf("%s %q: family %q, want %q", tc.os, tc.release, got, tc.want)
		}
	}
	for _, tc := range []struct{ name, fam, want string }{
		{"age-keygen", "apt", "sudo apt install -y age"},
		{"psql", "dnf", "sudo dnf install -y postgresql"},
		{"psql", "brew", "brew install libpq && brew link --force libpq"},
		{"gpg", "apt", "sudo apt install -y gnupg"},
		{"tailscale", "brew", "brew install --cask tailscale"},
		{"tailscale", "apt", "curl -fsSL https://tailscale.com/install.sh | sh"},
		{"sops", "brew", "brew install sops"},
		{"git", "", "install git with this system's package manager"},
		{"ansible-playbook", "brew", "brew install ansible"},
		{"ansible-playbook", "apt", "sudo apt install -y python3-venv && python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH"},
		{"ansible-playbook", "dnf", "python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH"},
		{"python3-venv", "apt", "sudo apt install -y python3-venv"},
	} {
		if got := fix(tc.name, tc.fam); got != tc.want {
			t.Errorf("fix(%s, %s) = %q, want %q", tc.name, tc.fam, got, tc.want)
		}
	}
}

// check's sops line installs the version and digest the plays pin, read from the same file.
func TestSopsPinIsThePlays(t *testing.T) {
	b, err := os.ReadFile("group_vars/all.yml")
	if err != nil {
		t.Fatal(err)
	}
	v, sum := pin("nova_sops_version"), pin("nova_sops_sha256", "linux-amd64")
	if v == "" || len(sum) != 64 || !strings.Contains(string(b), `nova_sops_version: "`+v+`"`) || !strings.Contains(string(b), "linux-amd64: "+sum) {
		t.Fatalf("pin read %q %q from group_vars/all.yml", v, sum)
	}
	old := goarch
	t.Cleanup(func() { goarch = old })
	goarch = "amd64"
	line := fix("sops", "apt")
	for _, w := range []string{"/download/v" + v + "/sops-v" + v + ".linux.amd64", "echo '" + sum + "  '", "sha256sum -c"} {
		if !strings.Contains(line, w) {
			t.Errorf("fix(sops) = %q lacks %q", line, w)
		}
	}
	if fix("sops", "brew") != "brew install sops" {
		t.Error("macOS takes sops from Homebrew, as join.yml does")
	}
}
