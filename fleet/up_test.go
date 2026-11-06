package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// upFake stands in for every program up runs: lookPath finds every helper, runCmd and outCmd
// record each command line, and outCmd answers by the command's words. A play that joins a
// machine to the tailnet (tailscale.yml on the hand inventory, join.yml) makes it a peer, as
// the real one would; under --check nothing joins.
type upFake struct {
	calls       []string
	names       string            // nova-secrets names
	readErr     string            // a nova-secrets exec -- true refusal
	rows        string            // nova-config machine list --json
	peers       map[string]string // tailnet peers: name -> ip
	unreachable map[string]bool   // addresses that answer no ssh at all
	sent        string
	fail        string // a call containing this word exits 4
	store       string
	key         string
	offnet      bool // tailscale status fails
	noVenv      bool // python3 -c 'import ensurepip, venv' fails
	untagged    bool // the control node lacks tag:control
}

func (f *upFake) install(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NOVA_PG_DSN", "")
	if f.peers == nil {
		f.peers = map[string]string{}
	}
	f.store, f.key = filepath.Join(t.TempDir(), "store"), filepath.Join(t.TempDir(), "control.key")
	pins := filepath.Join(t.TempDir(), "pins")
	for _, d := range []string{f.store, pins} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.key, []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_SECRETS_STORE", f.store)
	t.Setenv("NOVA_SECRETS_KEY", f.key)
	t.Setenv("NOVA_SECRETS_SEAT", "control")
	t.Setenv("NOVA_SECRETS_PINS", pins)
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
			f.sent = args[len(args)-2]
		}
		if f.fail != "" && strings.Contains(line, f.fail) {
			return 4, nil
		}
		if !strings.Contains(line, "--check") {
			if _, after, ok := strings.Cut(line, "nova_join_host="); ok {
				name := strings.Fields(after)[0]
				f.peers[name] = "100.64.0." + strconv.Itoa(len(f.peers)+10)
			} else if i := slices.IndexFunc(args, func(a string) bool { return strings.HasSuffix(a, "/first.ini") }); i >= 0 && strings.Contains(line, "tailscale.yml") {
				b, _ := os.ReadFile(args[i])
				for _, l := range strings.Split(string(b), "\n") {
					if w := strings.Fields(l); len(w) > 1 {
						f.peers[w[0]] = "100.64.0." + strconv.Itoa(len(f.peers)+10)
					}
				}
			}
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
		case strings.Contains(line, "machine list --json"):
			return f.rows, "", 0
		case strings.HasPrefix(line, "go version"):
			return "go version go1.99.0 linux/amd64\n", "", 0
		case strings.HasSuffix(line, "--version") || strings.HasSuffix(line, " version"):
			return filepath.Base(bin) + " version 1.2.3\n", "", 0
		case strings.HasPrefix(line, "tailscale status") && f.offnet:
			return "", "Logged out.", 1
		case strings.HasPrefix(line, "tailscale status --json"):
			var peers []string
			for name, ip := range f.peers {
				peers = append(peers, `"k-`+name+`":{"HostName":"`+name+`","DNSName":"`+name+`.ts.example.","TailscaleIPs":["`+ip+`"],"SSH_HostKeys":["ssh-ed25519 KEY-`+name+`"]}`)
			}
			tags := `["tag:control"]`
			if f.untagged {
				tags = `[]`
			}
			return `{"BackendState":"Running","Self":{"HostName":"control","Tags":` + tags + `},"Peer":{` + strings.Join(peers, ",") + `}}`, "", 0
		case strings.HasPrefix(line, "ssh -o BatchMode=yes -o ConnectTimeout=10 "):
			target := strings.Fields(line)[5]
			user, host, _ := strings.Cut(target, "@")
			name := strings.TrimSuffix(host, ".ts.example")
			if f.unreachable[host] || (user != "root" && f.peers[name] == "") {
				return "", "ssh: connect to host " + host + " port 22: Operation timed out", 255
			}
			return "", "", 0
		case strings.Contains(line, "age-keygen -y"):
			return "age1fake" + strings.Fields(line)[3] + "\n", "", 0
		case strings.Contains(line, "pending"):
			return "1\tnova-fleet-up\t" + f.sent + "\n", "", 0
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
		{[]string{"--tools", "v1", "a=1", "--", "-v"}, "nova-fleet up: after -- only -e name=value is passed on, got -v"},
		{[]string{"--tools", "v1", "a=1", "--", "-e"}, "nova-fleet up: -e wants name=value"},
		{[]string{"--tools", "v1", "a=1", "--", "-e", "@vars.yml"}, "nova-fleet up: -e wants name=value, got @vars.yml"},
		{[]string{"--tools", "v1", "a=1", "--", "-e", "nova_tailnet=false"}, "nova-fleet up: -e nova_tailnet is up's own (or skips a pillar: Tailscale, PostgreSQL, Redis and Ansible are not optional)"},
		{[]string{"--tools", "v1", "a=1", "--", "-e", "nova_join_user=x"}, "nova-fleet up: -e nova_join_user is up's own"},
	} {
		code, out, errs := runArgs(append([]string{"up"}, tc.args...)...)
		if code != 2 || out != "" || !strings.HasPrefix(errs, tc.want) {
			t.Errorf("up %q: code %d stdout %q stderr %q, want %q", tc.args, code, out, errs, tc.want)
		}
	}
	u, _ := parseUp([]string{"--check", "--user=fleet", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "a=10.0.0.1", "b=b.example/darwin/arm64", "--", "-e", "nova_pg_version=17"}, nil)
	if !u.check || u.user != "fleet" || u.runners != "https://github.com/o/r" || u.runnerCount != 1 || len(u.machines) != 2 ||
		fmt.Sprintf("%s %s %s %s", u.machines[0].name, u.machines[0].address, u.machines[0].os, u.machines[0].arch) != "a 10.0.0.1 linux amd64" ||
		fmt.Sprintf("%s %s %s %s", u.machines[1].name, u.machines[1].address, u.machines[1].os, u.machines[1].arch) != "b b.example darwin arm64" ||
		!slices.Equal(u.pass, []string{"-e", "nova_pg_version=17"}) {
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
	// A fleet exists (its store's password is in the seat), NOVA_PG_DSN does not say where, and the
	// first machine named is not on the tailnet: refused, with the export line.
	f = &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD"}
	f.install(t)
	code, out, errs = runArgs("up", "--tools", "v1.0.0", "a=1.2.3.4")
	if code != 2 || !strings.Contains(out, "CHECK NOTE NOVA_PG_DSN unset, and seat control holds NOVA_PG_PASSWORD") ||
		!strings.Contains(errs, "nova-fleet up: seat control holds NOVA_PG_PASSWORD, so a fleet exists, and a is not on the tailnet, so it is not its store; run: export NOVA_PG_DSN=") || len(f.plays()) != 0 {
		t.Errorf("fleet without NOVA_PG_DSN: code %d stdout %q stderr %q", code, out, errs)
	}
	// The store is another machine than the first named: refused, naming it.
	f = &upFake{names: "NOVA_PG_PASSWORD", peers: map[string]string{"a": "100.64.0.1", "store-1": "100.64.0.2"},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","roles":"services,bench"}}]}`}
	f.install(t)
	if code, _, errs = runArgs("up", "--tools", "v1.0.0", "a=1.2.3.4"); code != 2 || !strings.Contains(errs, "the store machine is store-1 (its row has the role services), not a; run: export NOVA_PG_DSN=postgres://nova_config@store-1.ts.example:5432/nova") {
		t.Errorf("another store: code %d stderr %q", code, errs)
	}
}

func TestUpOrder(t *testing.T) {
	f := &upFake{}
	f.install(t)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts"), []byte("203.0.113.7 ssh-ed25519 KEY-first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "--runner-count", "2", "first=203.0.113.7", "second=203.0.113.8")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	want := []string{"tailscale.yml@first.ini", "postgres.yml@first.ini", "redis.yml@first.ini", "join.yml@inventory", "join.yml@inventory",
		"health.yml@inventory", "install.yml@inventory", "services.yml@inventory", "github-runners.yml@inventory"}
	if got := f.plays(); !slices.Equal(got, want) {
		t.Errorf("plays = %v, want %v", got, want)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"nova-secrets exec --only TAILSCALE_AUTHKEY -- true",
		"nova-secrets exec --only GITHUB_RUNNER_TOKEN -- true",
		"ssh -o BatchMode=yes -o ConnectTimeout=10 root@203.0.113.7 true",
		"ssh -o BatchMode=yes -o ConnectTimeout=10 root@203.0.113.8 true",
		"nova-secrets exec --only TAILSCALE_AUTHKEY -- /bin/ansible-playbook -i first.ini tailscale.yml -e nova_tailscale_record_address=false -e nova_firewall=false",
		"ansible-playbook -i first.ini postgres.yml -e nova_seal_store=" + f.store + " -e nova_seal_seat=control -e nova_seal_key=" + f.key + " -e nova_store_hosts=first\n",
		"ansible-playbook -i first.ini redis.yml -e nova_seal_store=",
		"nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD -- /bin/ansible-playbook -i inventory join.yml -e nova_join_host=first -e nova_join_user=nova -e nova_join_address=203.0.113.7 -e nova_join_root_user=root -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=services,bench,runner -e nova_seal_store=" + f.store + " -e nova_bus_redis=first.ts.example:6379",
		"-e nova_join_host=second -e nova_join_user=nova -e nova_join_address=203.0.113.8 -e nova_join_root_user=root -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner",
		"nova-secrets exec --only NOVA_BUS_PASSWORD -- true",
		"ssh -o BatchMode=yes nova@first.ts.example sh -c 'PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH age-keygen -y $HOME/.config/nova-secrets/first.key'",
		"nova-secrets grant NOVA_BUS_PASSWORD --to first --pub age1fakenova@first.ts.example\n",
		"nova-secrets grant NOVA_BUS_PASSWORD --to second --pub age1fake",
		"git -C " + f.store + " push --quiet",
		"rsync -rltp --delete --exclude=.git --chmod=Du=rwx,Dgo=,Fu=rw,Fgo= --rsync-path=umask 077 && mkdir -p .local/share/nova/secrets/ && rsync " + f.store + "/ nova@second.ts.example:.local/share/nova/secrets/",
		"health.yml -l first,second\n",
		"install.yml -e nova_tools_version=v2.0.0 -l first,second\n",
		"github-runners.yml -e github_runner_url=https://github.com/o/r -e github_runner_scope=repo -e runner_count=2 -e {\"github_runner_secrets_args\":[\"--store\",\"" + f.store + "\",\"--key\",\"" + f.key + "\",\"--as\",\"control\"]} -l first,second\n",
		"nova-secrets exec --only NOVA_BUS_PASSWORD -- /bin/nova-bus send --as nova-fleet-up --to nova-fleet-up up-",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	for _, w := range []string{"UP CONTROL OK", "UP REACH first OK root@203.0.113.7", "UP HOSTKEY first OK pinned=1", "UP FIRST first OK pg=first.ts.example:5432 bus=first.ts.example:6379",
		"UP JOIN first OK", "UP JOIN second OK", "UP GRANT first OK NOVA_BUS_PASSWORD", "UP STORE OK", "UP REFRESH second OK nova@second.ts.example:.local/share/nova/secrets/",
		"UP HEALTH OK", "UP TOOLS OK v2.0.0", "UP SERVICES OK", "UP RUNNERS OK https://github.com/o/r", "UP BUS OK first.ts.example:6379 round-trip=up-",
		"UP MACHINE first OK address=203.0.113.7 steps=tailnet,postgres,redis,join,grant,refresh,health,tools,services,runners,bus",
		"UP MACHINE second OK address=203.0.113.8 steps=join,grant,refresh,health,tools,services,runners,bus",
		"UP DONE next: export NOVA_PG_DSN=postgres://nova_config@first.ts.example:5432/nova"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "UP ROLE") {
		t.Errorf("a new row already has the runner role:\n%s", out)
	}
	// the key that was already there under the public address is pinned again under the tailnet names
	kh, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts"))
	if !strings.Contains(string(kh), "203.0.113.7 ssh-ed25519 KEY-first\n") {
		t.Fatalf("the test's own pre-pinned line is missing:\n%s", kh)
	}
	if !strings.Contains(string(kh), "first,first.ts.example,100.64.0.10 ssh-ed25519 KEY-first\n") || strings.Count(string(kh), "KEY-second") != 1 {
		t.Errorf("known_hosts:\n%s", kh)
	}
}

// A fleet that exists: NOVA_PG_DSN names its store; its services row stays the store, and the
// machines named join it. A row that lacks the runner role gets it with --runners.
func TestUpAddsAMachine(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1", "second": "100.64.0.2"},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}},
		               {"kind":"machine","fields":{"name":"second","address":"second.ts.example","user":"ops","os":"linux","arch":"amd64","roles":"bench"}}],"facts":{}}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "second=203.0.113.8", "third=203.0.113.9")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if got := f.plays(); !slices.Equal(got, []string{"join.yml@inventory", "join.yml@inventory", "health.yml@inventory", "install.yml@inventory", "services.yml@inventory", "github-runners.yml@inventory"}) {
		t.Errorf("plays = %v", got)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"ssh -o BatchMode=yes -o ConnectTimeout=10 ops@second.ts.example true",
		"join.yml -e nova_join_host=second -e nova_join_user=ops -e nova_join_address=second.ts.example -e nova_join_root_user=ops -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner -e nova_seal_store=" + f.store + " -e nova_bus_redis=store-1.ts.example:6379",
		"join.yml -e nova_join_host=third -e nova_join_user=nova -e nova_join_address=203.0.113.9 -e nova_join_root_user=root -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner",
		"nova-config machine set second --roles bench,runner --actor control --reason nova-fleet up --runners\n",
		"health.yml -l second,third\n",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	if strings.Contains(all, "first.ini") || strings.Contains(all, "machine set third") {
		t.Errorf("the first machine's plays ran, or a new row was edited:\n%s", all)
	}
	for _, w := range []string{"UP REACH second OK ops@second.ts.example", "UP REACH third OK root@203.0.113.9", "UP ROLE second OK runner", "UP BUS OK store-1.ts.example:6379",
		"UP MACHINE second OK address=203.0.113.8 steps=join,grant,refresh,health,tools,services,runners,bus"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "UP FIRST") || strings.Contains(out, "UP DONE next: export") {
		t.Errorf("stdout:\n%s", out)
	}
}

// A first run that stopped after PostgreSQL: the same command resumes with no NOVA_PG_DSN, the first
// machine named being the store it made (its rows read from there), and every step runs again.
func TestUpResumes(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"first": "100.64.0.1"}, rows: `{"items":[]}`}
	f.install(t)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "first=203.0.113.7", "second=203.0.113.8")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if got := f.plays(); !slices.Equal(got, []string{"tailscale.yml@first.ini", "postgres.yml@first.ini", "redis.yml@first.ini", "join.yml@inventory", "join.yml@inventory", "health.yml@inventory", "install.yml@inventory", "services.yml@inventory"}) {
		t.Errorf("plays = %v", got)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{"ssh -o BatchMode=yes -o ConnectTimeout=10 nova@first.ts.example true", "ansible-playbook -i first.ini tailscale.yml", "nova_join_host=first -e nova_join_user=nova -e nova_join_address=first.ts.example -e nova_join_root_user=nova"} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	if !strings.Contains(out, "UP REACH first OK nova@first.ts.example") || !strings.Contains(out, "UP DONE next: export NOVA_PG_DSN=postgres://nova_config@first.ts.example:5432/nova") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestUpStopsAtAFailedStep(t *testing.T) {
	f := &upFake{fail: "nova_join_host=second"}
	f.install(t)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1", "second=2.2.2.2")
	if code != 1 || !strings.Contains(errs, "UP join second FAILED exit=4;") || strings.Contains(out, "UP MACHINE") {
		t.Errorf("code %d\n%s\n%s", code, out, errs)
	}
	if got := f.plays(); got[len(got)-1] != "join.yml@inventory" {
		t.Errorf("ran past the failed join: %v", got)
	}
	// A machine that answers no ssh stops a run before any play.
	f = &upFake{unreachable: map[string]bool{"2.2.2.2": true}}
	f.install(t)
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1", "second=2.2.2.2")
	if code != 1 || !strings.Contains(errs, "nova-fleet up: second does not answer ssh: ssh: connect to host 2.2.2.2 port 22: Operation timed out (tried root@2.2.2.2)") || len(f.plays()) != 0 {
		t.Errorf("unreachable: code %d plays %v\n%s\n%s", code, f.plays(), out, errs)
	}
}

func TestUpCheck(t *testing.T) {
	// A fresh fleet: --check cannot put the first machine on the tailnet, so PostgreSQL and the
	// steps after it are named WOULD-RUN, never reported green.
	f := &upFake{}
	f.install(t)
	code, out, errs := runArgs("up", "--check", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if got := f.plays(); !slices.Equal(got, []string{"tailscale.yml@first.ini"}) {
		t.Errorf("plays = %v", got)
	}
	for _, w := range []string{"UP FIRST postgres, redis WOULD-RUN (check: the machine is not on the tailnet yet)", "UP JOIN WOULD-RUN (check: after PostgreSQL exists)", "UP BUS WOULD-RUN",
		"UP MACHINE first CHECKED address=1.1.1.1 steps=tailnet (the rest once PostgreSQL exists)(check: nothing changed)"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "tailscale.yml --check --diff -e") || strings.Contains(out, "UP FIRST first") {
		t.Errorf("calls %v\nstdout %s", f.calls, out)
	}
	// An unreachable machine under --check is said, the rest is still named, and the check fails.
	f = &upFake{unreachable: map[string]bool{"203.0.113.7": true}}
	f.install(t)
	code, out, errs = runArgs("up", "--check", "--tools", "v2.0.0", "first=203.0.113.7", "second=203.0.113.8")
	for _, w := range []string{"UP first UNREACHABLE ssh: connect to host 203.0.113.7 port 22: Operation timed out (tried root@203.0.113.7)", "UP REACH second OK root@203.0.113.8",
		"UP FIRST tailscale, postgres, redis WOULD-RUN (check: not reachable)", "UP SERVICES WOULD-RUN", "UP MACHINE first UNREACHABLE address=203.0.113.7 steps=none", "UP MACHINE second CHECKED"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if code != 1 || !strings.Contains(errs, "UP CHECK FAILED unreachable=1") || len(f.plays()) != 0 {
		t.Errorf("code %d plays %v stderr %q", code, f.plays(), errs)
	}
	// A fleet that exists: every step runs under --check; nothing is pushed, granted, applied or sent.
	f = &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1", "first": "100.64.0.2"},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","roles":"services,bench"}},{"kind":"machine","fields":{"name":"first","address":"first.ts.example","user":"nova","roles":"bench"}}]}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	code, out, _ = runArgs("up", "--check", "--grant", "NOVA_BUS_PASSWORD,EXTRA", "--runners", "https://github.com/o/r", "--tools", "v2.0.0", "first=1.1.1.1")
	all := strings.Join(f.calls, "\n")
	if code != 0 || len(f.plays()) != 5 || strings.Contains(all, "push") || strings.Contains(all, "nova-bus send") || strings.Contains(all, "nova-secrets grant") ||
		!strings.Contains(all, "rsync --dry-run -rltp --delete") || !strings.Contains(all, "nova-secrets exec --only NOVA_BUS_PASSWORD,EXTRA -- true") ||
		!strings.Contains(all, "machine set first --roles bench,runner --actor control --reason nova-fleet up --runners --dry-run") {
		t.Errorf("code %d plays %v calls:\n%s", code, f.plays(), all)
	}
	for _, w := range []string{"UP REACH first OK nova@first.ts.example", "UP JOIN first WOULD-JOIN (check: nothing changed)", "UP ROLE first WOULD-SET (check: nothing changed) runner",
		"UP GRANT first WOULD-GRANT NOVA_BUS_PASSWORD,EXTRA to age1fakenova@first.ts.example (check: nothing changed)", "UP STORE WOULD-PUSH",
		"UP REFRESH first WOULD-PUSH (check: nothing changed) nova@first.ts.example:.local/share/nova/secrets/", "UP RUNNERS WOULD-RUN (check: nothing changed) https://github.com/o/r",
		"UP BUS WOULD-SEND", "UP MACHINE first CHECKED address=1.1.1.1 steps=join,grant,refresh,health,tools,services,runners (check: nothing changed)"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	// --check reads what it would grant, and stops with the reason when it cannot.
	oldOut := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if strings.Join(args, " ") == "exec --only NOVA_BUS_PASSWORD -- true" {
			return "", "seat control holds no NOVA_BUS_PASSWORD", 3
		}
		return oldOut(bin, args, env)
	}
	code, _, errs = runArgs("up", "--check", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 1 || !strings.Contains(errs, "seat control cannot read NOVA_BUS_PASSWORD to grant it: seat control holds no NOVA_BUS_PASSWORD") || !strings.Contains(errs, "UP grant FAILED exit=3") {
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
	for _, p := range []string{"tailscale", "postgres", "redis", "join", "health", "install", "services", "github-runners"} {
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
	for _, w := range []string{"CHECK OK ansible-playbook 1.2.3\n", "CHECK OK tailscale 1.2.3\n", "CHECK OK nova-config /bin/nova-config\n", "CHECK OK ssh /bin/ssh\n",
		"CHECK OK tailnet\n", "CHECK OK tag:control\n", "CHECK OK store " + f.store + "\n", "CHECK OK seat control\n", "CHECK OK pins " + os.Getenv("NOVA_SECRETS_PINS") + "\n",
		"CHECK OK TAILSCALE_AUTHKEY readable in seat control\n", "CHECK OK NOVA_PG_DSN none yet (a new fleet: up prints it)\n"} {
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
	for _, w := range []string{"CHECK MISSING age-keygen: ", "CHECK OK git 1.2.3\nCHECK OK go go1.99.0\n", "CHECK MISSING nova-bus: mkdir -p ~/.local/bin && ([ -d nova-tools ] || git clone --depth 1 https://github.com/mas-bandwidth/nova-tools) && go build -C nova-tools -o ~/.local/bin/nova-bus ./bus\n",
		"CHECK MISSING gh: ", "CHECK MISSING tailnet: sudo tailscale up --advertise-tags=tag:control", "Logged out.", "CHECK OK GITHUB_RUNNER_TOKEN readable"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if code != 1 || errs != "CHECK FAILED missing=4\n" {
		t.Errorf("code %d stderr %q", code, errs)
	}
	// A go too old for the nova-* lines, and a control node that is on the tailnet but not tagged.
	f.untagged, f.offnet = true, false
	outCmd2 := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if filepath.Base(bin) == "go" {
			return "go version go1.19.8 linux/amd64\n", "", 0
		}
		return outCmd2(bin, args, env)
	}
	code, out, _ = runArgs("check")
	if code != 1 || !strings.Contains(out, "CHECK MISSING go: go version go1.19.8 linux/amd64 is older than go1.") || !strings.Contains(out, "CHECK MISSING tag:control: this node is not tagged tag:control") {
		t.Errorf("old go, untagged: code %d\n%s", code, out)
	}
	outCmd, f.untagged = outCmd2, false
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
		{"ansible-playbook", "apt", "sudo apt install -y python3-venv && python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH && echo 'export PATH=~/.local/ansible/bin:$PATH' >> ~/.profile"},
		{"ansible-playbook", "dnf", "python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH && echo 'export PATH=~/.local/ansible/bin:$PATH' >> ~/.profile"},
		{"go", "brew", "brew install go"},
		{"ssh", "apt", "sudo apt install -y openssh-client"},
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
	for _, w := range []string{"mkdir -p ~/.local/bin && curl", "/download/v" + v + "/sops-v" + v + ".linux.amd64", "echo '" + sum + "  '", "sha256sum -c || { rm -f ~/.local/bin/sops; false; }"} {
		if !strings.Contains(line, w) {
			t.Errorf("fix(sops) = %q lacks %q", line, w)
		}
	}
	if fix("sops", "brew") != "brew install sops" {
		t.Error("macOS takes sops from Homebrew, as join.yml does")
	}
}
