package main

import (
	"cmp"
	"encoding/json"
	"errors"
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
	names       string              // nova-secrets names
	readErr     string              // a nova-secrets exec -- true refusal
	rows        string              // nova-config machine list --json
	peers       map[string]string   // tailnet peers: name -> ip
	bytes       map[string][2]int64 // a peer's TxBytes and RxBytes in tailscale status, 0 0 when unnamed
	unreachable map[string]bool     // addresses that answer no ssh at all
	first       map[string]string   // an address's first login when it is not root
	keyed       map[string]bool     // addresses that answer only ssh -i <key> -o IdentitiesOnly=yes
	nosudo      map[string]bool     // addresses whose first login cannot sudo without a password
	fail        string              // a call containing this word exits 4
	store       string
	key         string
	offnet      bool   // tailscale status fails
	noVenv      bool   // python3 -c 'import ensurepip, venv' fails
	untagged    bool   // the control node lacks tag:control
	noPosix     bool   // ansible.posix is not installed
	noGalaxy    bool   // ansible-galaxy cannot run (exit 127)
	noIdent     bool   // git has no commit identity: git var GIT_*_IDENT exits 128
	namesErr    string // nova-secrets names exits 2 with this stderr
	busErr      string // nova-bus who exits 1 with this stderr
	api         *fakeAPI
	self        string // this control node's own MagicDNS label (tailscale status Self), when it has one
	pushErr     string // the store's plain git push exits 1 with this stderr
	branchErr   string // and a push to a branch exits 1 with this one
}

// fakeAPI is the Tailscale API: the policy and its ETag, the calls made, the keys minted.
type fakeAPI struct {
	policy   string
	etag     int
	conflict int // POSTs to the policy answered 412 before one lands
	calls    []string
	keys     int
	expired  bool // every call answers 401
}

func (a *fakeAPI) call(method, path, ctype, ifMatch string, body []byte) (int, string, []byte, error) {
	a.calls = append(a.calls, strings.TrimSpace(method+" "+path+" "+ifMatch+" "+string(body)))
	switch {
	case ctype != map[bool]string{true: "application/hujson", false: "application/json"}[path == "/tailnet/-/acl"]:
		return 415, "", []byte(`{"message":"content type ` + ctype + `"}`), nil
	case a.expired:
		return 401, "", []byte(`{"message":"API token invalid"}`), nil
	case method == "GET" && path == "/tailnet/-/acl":
		return 200, `"e` + strconv.Itoa(a.etag) + `"`, []byte(a.policy), nil
	case method == "POST" && path == "/tailnet/-/acl" && a.conflict > 0:
		a.conflict--
		a.etag++
		return 412, "", []byte(`{"message":"precondition failed"}`), nil
	case method == "POST" && path == "/tailnet/-/acl":
		if ifMatch != `"e`+strconv.Itoa(a.etag)+`"` {
			return 412, "", nil, nil
		}
		a.policy, a.etag = string(body), a.etag+1
		return 200, "", nil, nil
	case method == "POST" && path == "/tailnet/-/keys":
		a.keys++
		return 200, "", []byte(`{"key":"` + fakeKey("auth", "SECRET") + strconv.Itoa(a.keys) + `"}`), nil
	case method == "POST" && strings.HasPrefix(path, "/device/"):
		return 200, "", nil, nil
	}
	return 404, "", []byte(`{"message":"not found"}`), nil
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
	oldLook, oldRun, oldOut, oldAPI := lookPath, runCmd, outCmd, newTailnet
	t.Cleanup(func() { lookPath, runCmd, outCmd, newTailnet = oldLook, oldRun, oldOut, oldAPI })
	if f.api == nil {
		f.api = &fakeAPI{policy: "{}"}
	}
	newTailnet = func(key string) tsAPI {
		if key != fakeKey("api", "kSECRET") {
			t.Errorf("the API client got %q", key)
		}
		return f.api
	}
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
		if k := slices.IndexFunc(env, func(e string) bool { return strings.HasPrefix(e, "TAILSCALE_AUTHKEY=") }); k >= 0 {
			f.calls[len(f.calls)-1] += " [env " + env[k] + "]"
		}
		if f.fail != "" && strings.Contains(line, f.fail) {
			return 4, nil
		}
		if i := slices.Index(args, "--to"); args[0] == "grant" && i >= 0 && i+1 < len(args) && args[i+1] == os.Getenv("NOVA_SECRETS_SEAT") {
			return 2, nil // nova-secrets refuses a grant to the granting seat
		}
		if strings.Contains(line, "up --reset --auth-key=file:/dev/stdin --advertise-tags=tag:control") {
			f.offnet = false // this node joined the tailnet
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
		case strings.HasSuffix(line, `printf %s "$TAILSCALE_API_KEY"`):
			return fakeKey("api", "kSECRET") + "\n", "", 0
		case strings.Contains(line, "nova-secrets names") && f.namesErr != "":
			return "", f.namesErr, 2
		case strings.Contains(line, "nova-secrets names"):
			return f.names, "", 0
		case strings.Contains(line, "machine list --json"):
			return f.rows, "", 0
		case strings.HasSuffix(line, "collection list") && f.noGalaxy:
			return "", "exec: \"/bin/ansible-galaxy\": file does not exist", 127
		case strings.HasSuffix(line, "collection list") && !f.noPosix:
			return "ansible.posix   2.2.2\nansible.windows 2.0.0\n", "", 0
		case strings.HasPrefix(line, "git var GIT_") && f.noIdent:
			return "", "fatal: no email was given and auto-detection is disabled", 128
		case strings.HasPrefix(line, "go version"):
			return "go version go1.99.0 linux/amd64\n", "", 0
		case strings.HasSuffix(line, "--version") || strings.HasSuffix(line, " version"):
			return filepath.Base(bin) + " version 1.2.3\n", "", 0
		case strings.HasPrefix(line, "tailscale status") && f.offnet:
			return "", "Logged out.", 1
		case strings.HasPrefix(line, "tailscale status --json"):
			var peers []string
			for name, ip := range f.peers {
				peers = append(peers, `"k-`+name+`":{"ID":"n-`+name+`","HostName":"`+name+`","Tags":["tag:nova"],"DNSName":"`+name+`.ts.example.","TailscaleIPs":["`+ip+`"],"SSH_HostKeys":["ssh-ed25519 KEY-`+name+`"],"TxBytes":`+strconv.FormatInt(f.bytes[name][0], 10)+`,"RxBytes":`+strconv.FormatInt(f.bytes[name][1], 10)+`}`)
			}
			tags := `["tag:control"]`
			if f.untagged {
				tags = `[]`
			}
			self := ""
			if f.self != "" {
				self = `,"DNSName":"` + f.self + `.ts.example.","TailscaleIPs":["100.64.0.99"]`
			}
			return `{"BackendState":"Running","Self":{"ID":"n-control","HostName":"control","Tags":` + tags + self + `},"Peer":{` + strings.Join(peers, ",") + `}}`, "", 0
		case strings.HasPrefix(line, "ssh -o BatchMode=yes -o ConnectTimeout=10 "):
			w := strings.Fields(line)
			at := slices.IndexFunc(w, func(s string) bool { return strings.Contains(s, "@") })
			user, host, _ := strings.Cut(w[at], "@")
			name := strings.TrimSuffix(host, ".ts.example")
			first := cmp.Or(f.first[host], "root")
			if f.unreachable[host] || (user != first && f.peers[name] == "" && name != f.self) || (f.keyed[host] && !strings.Contains(line, " -o IdentitiesOnly=yes ")) {
				return "", "ssh: connect to host " + host + " port 22: Operation timed out", 255
			}
			if strings.HasSuffix(line, " sudo -n true") && f.nosudo[host] {
				return "", "sudo: a password is required", 1
			}
			return "", "", 0
		case strings.Contains(line, "age-keygen -y"):
			return "age1fake" + strings.Fields(line)[3] + "\n", "", 0
		case strings.HasSuffix(line, "rev-parse --abbrev-ref @{u}"):
			return "origin/main\n", "", 0
		case strings.HasSuffix(line, "rev-parse --short HEAD"):
			return "abc1234\n", "", 0
		case strings.HasSuffix(line, " push --quiet") && f.pushErr != "":
			return "", f.pushErr, 1
		case strings.Contains(line, " push --quiet origin HEAD:refs/heads/") && f.branchErr != "":
			return "", f.branchErr, 1
		case strings.Contains(line, "nova-bus who") && f.busErr != "":
			return "", f.busErr, 1
		case strings.Contains(line, "nova-bus who"):
			return "WHO OK present=2\nclaude\t1m0s\ncodex\t2m0s\n", "", 0
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
		{[]string{"--tools", "v1", "Web=1.2.3.4"}, "nova-fleet up: want a machine as <name>=[<login>@]<address>[/<os>/<arch>] (a lowercase name and login), got Web=1.2.3.4; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "a=1.2.3.4/linux"}, "nova-fleet up: want a machine as"},
		{[]string{"--tools", "v1", "a=1", "a=2"}, "nova-fleet up: machine a is named twice"},
		{[]string{"--tools", "v1", "--runners", "https://gitlab.com/x", "a=1"}, "nova-fleet up: --runners wants"},
		{[]string{"--tools", "v1", "--runner-count", "0", "a=1"}, "nova-fleet up: --runner-count wants a number of at least 1, or cores"},
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
	if !u.check || u.user != "fleet" || u.runners != "https://github.com/o/r" || u.runnerCount != "cores" || len(u.machines) != 2 ||
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
	for _, w := range []string{"CHECK MISSING psql: ", "CHECK MISSING gh: ", "CHECK MISSING key: age-keygen -o " + filepath.Join(os.Getenv("HOME"), ".config", "nova-secrets", "control.key") + " && nova-secrets seat add control \"$(age-keygen -y ",
		"CHECK MISSING TAILSCALE_API_KEY: nova-secrets set TAILSCALE_API_KEY  # paste the token when it asks: one line " + fakeKey("api", "") + "..., from the Tailscale admin console > Settings > Keys > Generate API access token, once the lines above are done\n", "CHECK OK sops 1.2.3"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if code != 2 || !strings.HasPrefix(errs, "UP CONTROL FAILED missing=4;") || len(f.plays()) != 0 {
		t.Errorf("code %d stderr %q plays %v", code, errs, f.plays())
	}
	f = &upFake{readErr: "SECRETS EXEC REFUSED name=TAILSCALE_API_KEY reason=not-in-seat\n"}
	f.install(t)
	code, out, errs = runArgs("up", "--check", "--tools", "v1.0.0", "a=1.2.3.4")
	if code != 2 || !strings.Contains(out, "CHECK MISSING TAILSCALE_API_KEY: nova-secrets set TAILSCALE_API_KEY  # paste the token when it asks: one line "+fakeKey("api", "")+"..., from the Tailscale admin console > Settings > Keys > Generate API access token; the read said: SECRETS EXEC REFUSED name=TAILSCALE_API_KEY reason=not-in-seat\n") {
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
		"secrets-refresh.yml@inventory", "health.yml@inventory", "install.yml@inventory", "services.yml@inventory", "github-runners.yml@inventory"}
	if got := f.plays(); !slices.Equal(got, want) {
		t.Errorf("plays = %v, want %v", got, want)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"nova-secrets exec --only TAILSCALE_API_KEY -- true",
		"nova-secrets exec --only TAILSCALE_API_KEY -- sh -c printf %s \"$TAILSCALE_API_KEY\"",
		"gh auth status",
		"ssh -o BatchMode=yes -o ConnectTimeout=10 root@203.0.113.7 true",
		"ssh -o BatchMode=yes -o ConnectTimeout=10 root@203.0.113.8 true",
		"ansible-playbook -i first.ini tailscale.yml -e nova_tailscale_record_address=false -e nova_firewall=false [env TAILSCALE_AUTHKEY=" + fakeKey("auth", "SECRET1") + "]",
		"ansible-playbook -i first.ini postgres.yml -e nova_seal_store=" + f.store + " -e nova_seal_seat=control -e nova_seal_key=" + f.key + " -e nova_store_hosts=first\n",
		"ansible-playbook -i first.ini redis.yml -e nova_seal_store=",
		"nova-secrets exec --only NOVA_PG_PASSWORD -- /bin/ansible-playbook -i inventory join.yml -e nova_join_host=first -e nova_join_user=nova -e nova_join_address=203.0.113.7 -e nova_join_root_user=root -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=services,bench,runner -e nova_seal_store=" + f.store + " -e nova_bus_redis=first.ts.example:6379",
		"-e nova_join_host=second -e nova_join_user=nova -e nova_join_address=203.0.113.8 -e nova_join_root_user=root -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner -e nova_seal_store=" + f.store + " -e nova_bus_redis=first.ts.example:6379 [env TAILSCALE_AUTHKEY=" + fakeKey("auth", "SECRET2") + "]",
		"ssh -o BatchMode=yes nova@first.ts.example sh -c 'PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH age-keygen -y $HOME/.config/nova-secrets/first.key'",
		"nova-secrets grant NOVA_BUS_PASSWORD --to first --pub age1fakenova@first.ts.example\n",
		"nova-secrets grant NOVA_BUS_PASSWORD --to second --pub age1fake",
		"git -C " + f.store + " push --quiet",
		"secrets-refresh.yml -e nova_seal_store=" + f.store + " -l first,second\n",
		"health.yml -l first,second\n",
		"install.yml -e nova_tools_version=v2.0.0 -l first,second\n",
		"github-runners.yml -e github_runner_url=https://github.com/o/r -e github_runner_scope=repo -e runner_count=2 -l first,second\n",
		"nova-secrets exec --only NOVA_BUS_PASSWORD -- /bin/nova-bus who",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	for _, w := range []string{"UP CONTROL OK", "UP REACH first OK root@203.0.113.7", "UP HOSTKEY first OK pinned=1", "UP FIRST first OK pg=first.ts.example:5432 bus=first.ts.example:6379",
		"UP JOIN first OK", "UP JOIN second OK", "UP GRANT first OK NOVA_BUS_PASSWORD", "UP STORE OK", "UP REFRESH OK " + f.store,
		"UP HEALTH OK", "UP TOOLS OK v2.0.0", "UP SERVICES OK", "UP RUNNERS OK https://github.com/o/r", "UP BUS OK first.ts.example:6379 present=2",
		"UP MACHINE first OK address=203.0.113.7 steps=tailnet,postgres,redis,join,grant,refresh,health,tools,services,runners,bus",
		"UP MACHINE second OK address=203.0.113.8 steps=join,grant,refresh,health,tools,services,runners,bus",
		"UP DONE: nova-config and the next nova-fleet up read the fleet from NOVA_PG_DSN\nnext: export NOVA_PG_DSN=postgres://nova_config@first.ts.example:5432/nova"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "UP ROLE") {
		t.Errorf("a new row already has the runner role:\n%s", out)
	}
	if strings.Contains(all, "nova-secrets exec --only NOVA_BUS_PASSWORD -- true") {
		t.Errorf("a run that grants read the granted values first, which only --check needs:\n%s", all)
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
	if got := f.plays(); !slices.Equal(got, []string{"join.yml@inventory", "join.yml@inventory", "secrets-refresh.yml@inventory", "health.yml@inventory", "install.yml@inventory", "services.yml@inventory", "github-runners.yml@inventory"}) {
		t.Errorf("plays = %v", got)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"ssh -o BatchMode=yes -o ConnectTimeout=10 ops@second.ts.example true",
		"join.yml -e nova_join_host=second -e nova_join_user=ops -e nova_join_address=second.ts.example -e nova_join_root_user=ops -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner -e nova_seal_store=" + f.store + " -e nova_bus_redis=store-1.ts.example:6379",
		"join.yml -e nova_join_host=third -e nova_join_user=nova -e nova_join_address=203.0.113.9 -e nova_join_root_user=root -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner",
		"nova-config machine set second --roles bench,runner --actor control --reason nova-fleet up --runners\n",
		"health.yml -l second,third\n",
		"github-runners.yml -e github_runner_url=https://github.com/o/r -e github_runner_scope=repo -e runner_count=cores -l second,third\n",
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
	if strings.Contains(out, "UP FIRST") || strings.Contains(out, "\nnext: export NOVA_PG_DSN") {
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
	if got := f.plays(); !slices.Equal(got, []string{"tailscale.yml@first.ini", "postgres.yml@first.ini", "redis.yml@first.ini", "join.yml@inventory", "join.yml@inventory", "secrets-refresh.yml@inventory", "health.yml@inventory", "install.yml@inventory", "services.yml@inventory"}) {
		t.Errorf("plays = %v", got)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{"ssh -o BatchMode=yes -o ConnectTimeout=10 root@203.0.113.7 true", "ansible-playbook -i first.ini tailscale.yml", "nova_join_host=first -e nova_join_user=nova -e nova_join_address=203.0.113.7 -e nova_join_root_user=root"} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	if !strings.Contains(out, "UP REACH first OK root@203.0.113.7") || !strings.Contains(out, "UP DONE: nova-config and the next nova-fleet up read the fleet from NOVA_PG_DSN\nnext: export NOVA_PG_DSN=postgres://nova_config@first.ts.example:5432/nova") {
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
	if code != 0 || len(f.plays()) != 5 || strings.Contains(all, "push") || strings.Contains(all, "nova-bus who") || strings.Contains(all, "nova-secrets grant") ||
		!strings.Contains(all, "secrets-refresh.yml --check --diff -e nova_seal_store=") || !strings.Contains(all, "nova-secrets exec --only NOVA_BUS_PASSWORD,EXTRA -- true") ||
		!strings.Contains(all, "machine set first --roles bench,runner --actor control --reason nova-fleet up --runners --dry-run") {
		t.Errorf("code %d plays %v calls:\n%s", code, f.plays(), all)
	}
	for _, w := range []string{"UP REACH first OK nova@first.ts.example", "UP JOIN first WOULD-JOIN (check: nothing changed)", "UP ROLE first WOULD-SET (check: nothing changed) runner",
		"UP GRANT first WOULD-GRANT NOVA_BUS_PASSWORD,EXTRA to age1fakenova@first.ts.example (check: nothing changed)", "UP STORE WOULD-PUSH",
		"UP REFRESH WOULD-PUSH (check: nothing changed) " + f.store, "UP RUNNERS WOULD-RUN (check: once the runner role is on the rows)",
		"UP BUS WOULD-ASK", "UP MACHINE first CHECKED address=1.1.1.1 steps=join,grant,refresh,health,tools,services (check: nothing changed)"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	// A new machine under --check has no row after join, so the plays that need one are named, not run.
	f = &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1"},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","roles":"services,bench"}}]}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	code, out, errs = runArgs("up", "--check", "--tools", "v2.0.0", "new-1=203.0.113.9")
	if code != 0 || len(f.plays()) != 1 || !strings.Contains(out, "UP new-1 PENDING (check: its row does not exist yet") || !strings.Contains(out, "UP TOOLS WOULD-RUN (check: once join has made the rows)") ||
		!strings.Contains(out, "UP MACHINE new-1 CHECKED address=203.0.113.9 steps=join (the rest once join has made its row)") || strings.Contains(out, "WOULD-GRANT") {
		t.Errorf("new machine under --check: code %d plays %v\n%s\n%s", code, f.plays(), out, errs)
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
	// the same lines on every platform: a Linux of no known family, amd64
	oldOS, oldArch, oldRel := goos, goarch, osRelease
	t.Cleanup(func() { goos, goarch, osRelease = oldOS, oldArch, oldRel })
	goos, goarch, osRelease = "linux", "amd64", filepath.Join(t.TempDir(), "no-os-release")
	f := &upFake{}
	f.install(t)
	code, out, errs := runArgs("check")
	if code != 0 || errs != "" || !strings.HasSuffix(out, "CHECK OK missing=0\nnext: nova-fleet up --check --tools <TAG|DIR> <name>=<address>...\n") {
		t.Fatalf("all present: code %d stdout %q stderr %q", code, out, errs)
	}
	for _, w := range []string{"CHECK OK ansible-playbook 1.2.3\n", "CHECK OK ansible.posix\n", "CHECK OK tailscale 1.2.3\n", "CHECK OK nova-config /bin/nova-config\n", "CHECK OK ssh /bin/ssh\n",
		"CHECK OK tailnet\n", "CHECK OK store " + f.store + "\n", "CHECK OK seat control\n", "CHECK OK pins " + os.Getenv("NOVA_SECRETS_PINS") + "\n",
		"CHECK OK TAILSCALE_API_KEY readable in seat control\n", "CHECK OK NOVA_PG_DSN none yet (a new fleet: up prints it)\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "gh-login") || strings.Contains(out, " gh ") || strings.Count(out, "CHECK MISSING") != 0 {
		t.Errorf("runner prerequisites checked without --runners:\n%s", out)
	}
	if strings.Contains(out, "traffic") {
		t.Errorf("traffic lines with no peer bytes:\n%s", out)
	}
	// Each peer's traffic with this node follows the tailnet line (D8, 2026-11-07); a peer with no bytes is no line.
	f.peers, f.bytes = map[string]string{"bench-a": "100.64.0.11", "studio": "100.64.0.12", "idle": "100.64.0.13"}, map[string][2]int64{"bench-a": {2_500_000_000, 700_000_000}, "studio": {1_080_000_000_000, 12_345_678_901}}
	code, out, errs = runArgs("check")
	want := "CHECK OK tailnet\nCHECK traffic bench-a tx=2.5GB rx=0.7GB\nCHECK traffic studio tx=1080.0GB rx=12.3GB\n" +
		"CHECK WARN traffic studio tx=1080.0GB is over 100GB with one peer since tailscaled started: something fetches or mirrors on a clock; find it: nova-config service list\nCHECK OK seat"
	if code != 0 || errs != "" || !strings.Contains(out, want) {
		t.Errorf("traffic: code %d stderr %q, stdout lacks %q:\n%s", code, errs, want, out)
	}
	f.peers, f.bytes = map[string]string{}, nil
	f.offnet = true
	lookPath = func(name string) (string, error) {
		if name == "age-keygen" || name == "nova-bus" || name == "gh" {
			return "", os.ErrNotExist
		}
		return "/bin/" + name, nil
	}
	code, out, errs = runArgs("check", "--runners", "https://github.com/o/r")
	for _, w := range []string{"CHECK MISSING age-keygen: ", "CHECK OK git 1.2.3\nCHECK OK go go1.99.0\n", "CHECK MISSING nova-bus: mkdir -p ~/.local/bin && ([ -d nova-tools ] || git clone --depth 1 https://github.com/mas-bandwidth/nova-tools) && go build -C nova-tools -o ~/.local/bin/nova-bus ./cmd/nova-bus\n",
		"CHECK MISSING gh: install gh with this system's package manager\n", "CHECK NOTE tailnet this node is not on it; up joins it, tagged tag:control, with a key minted by TAILSCALE_API_KEY: Logged out.\n", "CHECK OK TAILSCALE_API_KEY readable"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if code != 1 || errs != "CHECK FAILED missing=3\n" {
		t.Errorf("code %d stderr %q", code, errs)
	}
	// A go too old for the nova-* lines; a control node on the tailnet but not tagged is no missing line (up tags it).
	f.untagged, f.offnet, f.noPosix = true, false, true
	outCmd2 := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if filepath.Base(bin) == "go" {
			return "go version go1.19.8 linux/amd64\n", "", 0
		}
		return outCmd2(bin, args, env)
	}
	code, out, _ = runArgs("check")
	if code != 1 || !strings.Contains(out, "CHECK MISSING go: curl -fsSL https://go.dev/dl/go") || !strings.Contains(out, ".linux-amd64.tar.gz | sudo tar -C /usr/local -xz && export PATH=/usr/local/go/bin:$PATH && echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile  # go version go1.19.8 linux/amd64 is older than go1.") || strings.Contains(out, "tag:control:") ||
		!strings.Contains(out, "CHECK MISSING ansible.posix: nova-fleet show requirements > requirements.yml && /bin/ansible-galaxy collection install -r requirements.yml") {
		t.Errorf("old go, untagged: code %d\n%s", code, out)
	}
	outCmd, f.untagged, f.noPosix = outCmd2, false, false
	// No ansible-playbook and a python3 without venv: both are said.
	f.offnet, f.noVenv = false, true
	lookPath = func(name string) (string, error) {
		if name == "ansible-playbook" {
			return "", os.ErrNotExist
		}
		return "/bin/" + name, nil
	}
	code, out, _ = runArgs("check")
	if code != 1 || !strings.Contains(out, "CHECK MISSING ansible-playbook: ") || !strings.Contains(out, "CHECK MISSING ansible.posix: nova-fleet show requirements > requirements.yml && ansible-galaxy collection install -r requirements.yml") ||
		!strings.Contains(out, "CHECK MISSING python3-venv: install python3-venv with this system's package manager  # /bin/python3 has no venv module: ModuleNotFoundError: No module named 'ensurepip'\n") {
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

// check says whether git can commit, beside git itself: nova-secrets set commits what it writes, so a node whose git
// has no author or committer is not ready for the token.
func TestCheckNamesAMissingGitIdentity(t *testing.T) {
	f := &upFake{}
	f.install(t)
	if code, out, errs := runArgs("check"); code != 0 || errs != "" || !strings.Contains(out, "CHECK OK git-identity\n") {
		t.Errorf("git can commit: code %d stderr %q\n%s", code, errs, out)
	}
	for _, who := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if !slices.Contains(f.calls, "git var "+who) {
			t.Errorf("check did not ask git for %s: %q", who, f.calls)
		}
	}
	f.noIdent = true
	code, out, errs := runArgs("check")
	want := "CHECK MISSING git-identity: git config --global user.name 'Your Name' && git config --global user.email you@example.com  # nova-secrets set commits what it writes, and git has no author or committer to commit as\n"
	if code != 1 || errs != "CHECK FAILED missing=1\n" || !strings.Contains(out, want) || strings.Contains(out, "CHECK OK git-identity") {
		t.Errorf("no identity: code %d stderr %q, stdout lacks %q:\n%s", code, errs, want, out)
	}
}

// TestCheckTraffic reads a canned tailscale status: the lines are sorted by peer name, GB to one decimal, and a
// peer over trafficWarnBytes in either direction warns once.
func TestCheckTraffic(t *testing.T) {
	t.Parallel()
	var st tsStatus
	canned := `{"BackendState":"Running","Self":{"ID":"n0","DNSName":"control.ts.example.","TxBytes":0,"RxBytes":0},"Peer":{
	  "k1":{"ID":"n1","DNSName":"studio.ts.example.","Tags":["tag:nova"],"TxBytes":1080000000000,"RxBytes":12345678901},
	  "k2":{"ID":"n2","DNSName":"bench-a.ts.example.","Tags":["tag:nova"],"TxBytes":2500000000,"RxBytes":700000000},
	  "k3":{"ID":"n3","DNSName":"phone.ts.example.","TxBytes":0,"RxBytes":0},
	  "k4":{"ID":"n4","DNSName":"puller.ts.example.","TxBytes":40000000,"RxBytes":250000000000}}}`
	if err := json.Unmarshal([]byte(canned), &st); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	traffic(&out, st)
	want := "CHECK traffic bench-a tx=2.5GB rx=0.7GB\n" +
		"CHECK traffic puller tx=0.0GB rx=250.0GB\nCHECK WARN traffic puller rx=250.0GB is over 100GB with one peer since tailscaled started: something fetches or mirrors on a clock; find it: nova-config service list\n" +
		"CHECK traffic studio tx=1080.0GB rx=12.3GB\nCHECK WARN traffic studio tx=1080.0GB is over 100GB with one peer since tailscaled started: something fetches or mirrors on a clock; find it: nova-config service list\n"
	if out.String() != want {
		t.Errorf("traffic lines:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestOnPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir, path string
		want      bool
	}{
		{"/h/.local/bin", "/usr/bin:/h/.local/bin:/bin", true},
		{"/h/.local/bin", "/usr/bin:/h/.local/bin/:/bin", true},
		{"/h/.local/bin", "/usr/bin:/h/.local/binx:/h/.local", false},
		{"/h/.local/bin", "", false},
		{"/h/.local/bin", "::bin", false},
	} {
		if got := onPath(tc.dir, tc.path); got != tc.want {
			t.Errorf("onPath(%q, %q) = %v, want %v", tc.dir, tc.path, got, tc.want)
		}
	}
}

// A tool the repair built into ~/.local/bin is found there; with that directory off PATH, check says so once and
// prints the line that puts it on, instead of repeating the build.
func TestCheckFindsLocalBin(t *testing.T) {
	oldOS, oldArch, oldRel := goos, goarch, osRelease
	t.Cleanup(func() { goos, goarch, osRelease = oldOS, oldArch, oldRel })
	goos, goarch, osRelease = "linux", "amd64", filepath.Join(t.TempDir(), "no-os-release")
	f := &upFake{}
	f.install(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "nova-config"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	lookPath = func(name string) (string, error) {
		if name == "nova-config" {
			return "", os.ErrNotExist
		}
		return "/bin/" + name, nil
	}
	t.Setenv("PATH", "/usr/bin")
	code, out, errs := runArgs("check")
	if code != 1 || errs != "CHECK FAILED missing=1\n" ||
		!strings.Contains(out, "CHECK OK nova-config "+filepath.Join(home, ".local", "bin", "nova-config")+"\n") ||
		!strings.Contains(out, "CHECK MISSING PATH: export PATH=\"$HOME/.local/bin:$PATH\"  # nova-config found only in "+filepath.Join(home, ".local", "bin")+", which is not on PATH; ") ||
		strings.Contains(out, "CHECK MISSING nova-config") {
		t.Errorf("tool in ~/.local/bin, directory off PATH: code %d stdout %q stderr %q", code, out, errs)
	}
	t.Setenv("PATH", "/usr/bin:"+filepath.Join(home, ".local", "bin"))
	if code, out, errs = runArgs("check"); code != 0 || errs != "" || strings.Contains(out, "CHECK MISSING") {
		t.Errorf("directory on PATH: code %d stdout %q stderr %q", code, out, errs)
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
		{"age-keygen", "apt", "sudo apt-get update -qq && sudo apt-get install -y age"},
		{"psql", "dnf", "sudo dnf install -y postgresql"},
		{"psql", "brew", "brew install libpq && brew link --force libpq"},
		{"gpg", "apt", "sudo apt-get update -qq && sudo apt-get install -y gnupg"},
		{"tailscale", "brew", "brew install --cask tailscale"},
		{"tailscale", "apt", "curl -fsSL https://tailscale.com/install.sh | sh"},
		{"sops", "brew", "brew install sops"},
		{"git", "", "install git with this system's package manager"},
		{"ansible-playbook", "brew", "brew install ansible"},
		{"ansible-playbook", "apt", "sudo apt-get update -qq && sudo apt-get install -y python3-venv && python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH && echo 'export PATH=~/.local/ansible/bin:$PATH' >> ~/.profile"},
		{"ansible-playbook", "dnf", "python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core && export PATH=~/.local/ansible/bin:$PATH && echo 'export PATH=~/.local/ansible/bin:$PATH' >> ~/.profile"},
		{"go", "brew", "brew install go"},
		{"ssh", "apt", "sudo apt-get update -qq && sudo apt-get install -y openssh-client"},
		{"python3-venv", "apt", "sudo apt-get update -qq && sudo apt-get install -y python3-venv"},
		{"python3-venv", "dnf", "sudo dnf install -y python3"},
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

// A control node laid out as nova-secrets' defaults (no NOVA_SECRETS_*): check prints no store, key, seat or
// pins line, and what it runs reads the defaults from its environment.
func TestCheckSecretsDefaults(t *testing.T) {
	f := &upFake{}
	f.install(t)
	host, _ := os.Hostname()
	seat, _, _ := strings.Cut(host, ".")
	dir := filepath.Join(os.Getenv("HOME"), ".config", "nova-secrets")
	for _, d := range []string{filepath.Join(dir, "store"), filepath.Join(dir, "pins")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, seat+".key"), []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"NOVA_SECRETS_STORE", "NOVA_SECRETS_KEY", "NOVA_SECRETS_SEAT", "NOVA_SECRETS_PINS"} {
		t.Setenv(e, "")
	}
	code, out, errs := runArgs("check")
	for _, w := range []string{"CHECK OK store", "CHECK OK key", "CHECK OK seat", "CHECK OK pins", "CHECK MISSING", "NOVA_SECRETS_"} {
		if strings.Contains(out, w) {
			t.Errorf("stdout has %q:\n%s", w, out)
		}
	}
	if code != 0 || errs != "" || !strings.Contains(out, "CHECK OK TAILSCALE_API_KEY readable in seat "+seat+"\n") {
		t.Errorf("code %d stderr %q\n%s", code, errs, out)
	}
	if os.Getenv("NOVA_SECRETS_KEY") != filepath.Join(dir, seat+".key") || os.Getenv("NOVA_SECRETS_STORE") != filepath.Join(dir, "store") || os.Getenv("NOVA_SECRETS_SEAT") != seat {
		t.Errorf("the defaults are not in the environment the children get")
	}
	// a fresh node (none there): exactly one CHECK MISSING line, TAILSCALE_API_KEY, which makes them all
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"NOVA_SECRETS_STORE", "NOVA_SECRETS_KEY", "NOVA_SECRETS_SEAT", "NOVA_SECRETS_PINS"} {
		t.Setenv(e, "")
	}
	code, out, errs = runArgs("check")
	if code != 1 || errs != "CHECK FAILED missing=1\n" || strings.Count(out, "CHECK MISSING") != 1 || strings.Contains(out, "CHECK OK store") ||
		!strings.Contains(out, "CHECK MISSING TAILSCALE_API_KEY: nova-secrets set TAILSCALE_API_KEY  # paste the token when it asks: one line "+fakeKey("api", "")+"..., from the Tailscale admin console > Settings > Keys > Generate API access token; on this fresh node it makes the secrets store, the keys and the seat first\n") {
		t.Errorf("fresh: code %d stderr %q\n%s", code, errs, out)
	}
	// part there (a store, no key): the missing one is named with the line that makes it
	if err := os.MkdirAll(filepath.Join(dir, "store"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"NOVA_SECRETS_STORE", "NOVA_SECRETS_KEY", "NOVA_SECRETS_SEAT", "NOVA_SECRETS_PINS"} {
		t.Setenv(e, "") // the run above set the defaults in this process's environment
	}
	_, out, _ = runArgs("check")
	for _, w := range []string{"CHECK MISSING key: age-keygen -o " + filepath.Join(dir, seat+".key") + " && nova-secrets seat add " + seat + " ",
		"CHECK MISSING pins: mkdir -p " + filepath.Join(dir, "pins") + "  #", "CHECK MISSING TAILSCALE_API_KEY: nova-secrets set TAILSCALE_API_KEY  # paste the token"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
}

// existing is a fleet that exists: store-1 is its store, and the rows of the machines named.
func existing(t *testing.T, f *upFake, rows string) {
	t.Helper()
	f.names, f.rows = "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}}`+rows+`],"facts":{}}`
	if f.peers == nil {
		f.peers = map[string]string{"store-1": "100.64.0.1"}
	}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
}

// A store whose upstream refuses a direct push (its main takes changes through a pull request only):
// the commits go to a branch of their own, the run goes on to the end, and exits 1 for that one step.
func TestUpStorePushRefused(t *testing.T) {
	f := &upFake{pushErr: "remote: error: GH013: Repository rule violations found for refs/heads/main.\n ! [remote rejected] main -> main (push declined due to repository rule violations)"}
	existing(t, f, "")
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
	all := strings.Join(f.calls, "\n")
	if code != 1 || !strings.Contains(all, "git -C "+f.store+" push --quiet origin HEAD:refs/heads/nova-fleet-up/abc1234") ||
		!strings.Contains(errs, "UP STORE PUSH REFUSED: origin/main is protected (") || !strings.Contains(errs, "pushed branch nova-fleet-up/abc1234; open a PR from it") ||
		!strings.Contains(errs, "UP STORE FAILED") || strings.Contains(out, "UP STORE OK") ||
		!strings.Contains(out, "UP SERVICES OK") || !strings.Contains(out, "UP BUS OK") || !strings.Contains(out, "UP MACHINE second OK") {
		t.Errorf("code %d\n%s\n%s", code, out, errs)
	}
	// a refused branch too, or a push that fails some other way (the network), stops the run at the store
	for _, g := range []*upFake{{pushErr: f.pushErr, branchErr: "fatal: could not read from remote"}, {pushErr: "fatal: unable to access: Could not resolve host"}} {
		existing(t, g, "")
		code, out, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
		if code != 1 || !strings.Contains(errs, "UP store FAILED exit=1;") || strings.Contains(out, "UP REFRESH") ||
			strings.Contains(strings.Join(g.calls, "\n"), "HEAD:refs/heads/") != (g.branchErr != "") {
			t.Errorf("code %d\n%s\n%s\n%s", code, out, errs, strings.Join(g.calls, "\n"))
		}
	}
}

// The seat key is read with the configured bin directory first on PATH, ~/.local/bin after it.
func TestUpSeatKeyUsesTheBinDir(t *testing.T) {
	f := &upFake{}
	existing(t, f, "")
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8", "--", "-e", "nova_bin_dir={{ nova_home }}/.local/nova/bin")
	if all := strings.Join(f.calls, "\n"); code != 0 || !strings.Contains(all, "sh -c 'PATH=$HOME/.local/nova/bin:$HOME/.local/bin:/opt/homebrew/bin:$PATH age-keygen -y $HOME/.config/nova-secrets/second.key'") ||
		!strings.Contains(all, "install.yml -e nova_tools_version=v2.0.0 -l second -e nova_bin_dir={{ nova_home }}/.local/nova/bin") {
		t.Errorf("code %d\n%s\n%s\n%s", code, out, errs, all)
	}
	for _, dir := range []string{"/opt/nova bin", "$(true)", "{{ other }}/bin"} {
		if code, _, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8", "--", "-e", "nova_bin_dir="+dir); code != 2 || !strings.Contains(errs, "-e nova_bin_dir wants a plain path") {
			t.Errorf("%q: code %d %s", dir, code, errs)
		}
	}
}

// The control node named in the inventory is tailscale status Self, never a Peer: it gets no join key and
// no join (join's peer check cannot see the node it runs on), and no grant (its own seat holds the values, and
// nova-secrets refuses a grant to the granting seat); every other step runs on it as on any.
func TestUpControlNodeInTheInventory(t *testing.T) {
	f := &upFake{self: "control"}
	existing(t, f, `,{"kind":"machine","fields":{"name":"control","address":"control.ts.example","user":"ops","os":"linux","arch":"amd64","roles":"bench"}}`)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "control=203.0.113.5", "second=203.0.113.8")
	all := strings.Join(f.calls, "\n")
	if code != 0 || f.api.keys != 1 || strings.Contains(all, "nova_join_host=control") || !strings.Contains(all, "nova_join_host=second") ||
		strings.Contains(strings.Join(f.api.calls, "\n"), "/device/n-control/tags  {\"tags\":[\"tag:control\",\"tag:nova\"]}") ||
		strings.Contains(all, "nova-secrets grant NOVA_BUS_PASSWORD --to control") || !strings.Contains(all, "nova-secrets grant NOVA_BUS_PASSWORD --to second") ||
		!strings.Contains(out, "UP REACH control OK ops@control.ts.example") || !strings.Contains(out, "UP JOIN control SKIPPED this control node (tailscale status Self): already joined") ||
		!strings.Contains(out, "UP GRANT control SKIPPED this control node (tailscale status Self): its own seat holds the values") ||
		!strings.Contains(out, "UP MACHINE control OK address=203.0.113.5 steps=refresh,health,tools,services,bus") {
		t.Errorf("code %d keys %d\n%s\n%s\n%s", code, f.api.keys, out, errs, all)
	}
	// with --runners, the control node gets no runner role and no runners: heavy work never defaults onto it
	f = &upFake{self: "control"}
	existing(t, f, `,{"kind":"machine","fields":{"name":"control","address":"control.ts.example","user":"ops","os":"linux","arch":"amd64","roles":"bench"}}`)
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "control=203.0.113.5", "second=203.0.113.8")
	all = strings.Join(f.calls, "\n")
	if code != 0 || strings.Contains(all, "machine set control") || !strings.Contains(all, "-e runner_count=cores -l second\n") ||
		!strings.Contains(out, "UP MACHINE control OK address=203.0.113.5 steps=refresh,health,tools,services,bus") {
		t.Errorf("runners: code %d\n%s\n%s\n%s", code, out, errs, all)
	}
	// with no row, join cannot be skipped into one: the run stops with the line that adds it
	f = &upFake{self: "control"}
	existing(t, f, "")
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "control=203.0.113.5")
	if code != 1 || !strings.Contains(errs, "nova-fleet up: control is this control node (tailscale status Self), so join skips it, and it has no row; run: nova-config machine add control --user nova --actor control") ||
		!strings.Contains(errs, "UP join control FAILED exit=1;") || strings.Contains(strings.Join(f.calls, "\n"), "join.yml") {
		t.Errorf("no row: code %d\n%s\n%s", code, out, errs)
	}
}

// With --runners and only this control node named, no machine can take a runner: a real run says so and
// fails the step, never a check-mode WOULD-RUN line and UP DONE.
func TestUpRunnersOnlyOnTheControlNodeFails(t *testing.T) {
	f := &upFake{self: "control"}
	existing(t, f, `,{"kind":"machine","fields":{"name":"control","address":"control.ts.example","user":"ops","os":"linux","arch":"amd64","roles":"bench"}}`)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "control=203.0.113.5")
	if code != 1 || strings.Contains(out, "WOULD-RUN") || strings.Contains(out, "UP MACHINE") ||
		!strings.Contains(errs, "nova-fleet up: no runner was registered: --runners puts runners on the machines named but this control node (control), and no other machine is named; run: nova-fleet up --tools v2.0.0 --runners https://github.com/o/r <name>=<address>\n") ||
		!strings.Contains(errs, "UP runners FAILED exit=1;") {
		t.Errorf("code %d\n%s\n%s", code, out, errs)
	}
}

// A --tools directory with no readable version stops the run at tools before the install play runs.
func TestUpToolsDirWithoutVersionFails(t *testing.T) {
	f := &upFake{}
	existing(t, f, "")
	dir := t.TempDir()
	code, out, errs := runArgs("up", "--tools", dir, "second=203.0.113.8")
	if code != 1 || !strings.Contains(errs, "nova-fleet up: --tools "+dir+": ") || !strings.Contains(errs, "UP tools FAILED exit=1;") ||
		strings.Contains(strings.Join(f.calls, "\n"), "install.yml") || strings.Contains(out, "UP MACHINE") {
		t.Errorf("code %d\n%s\n%s", code, out, errs)
	}
}

// A seat whose names cannot be read is that failure, never "no NOVA_PG_PASSWORD" (a new fleet).
func TestSeatNamesFailureIsSaid(t *testing.T) {
	f := &upFake{namesErr: "nova-secrets names: seat control: grants file is corrupt"}
	f.install(t)
	code, out, errs := runArgs("check")
	if code != 1 || strings.Contains(out, "NOVA_PG_DSN none yet") ||
		!strings.Contains(out, "CHECK MISSING NOVA_PG_DSN: /bin/nova-secrets names --as control  # the seat's names cannot be read, so whether a fleet exists is unknown: nova-secrets names: seat control: grants file is corrupt\n") {
		t.Errorf("check: code %d\n%s\n%s", code, out, errs)
	}
	// up's own read, after the control step's read passed
	f = &upFake{}
	f.install(t)
	reads, out0 := 0, outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if filepath.Base(bin) == "nova-secrets" && len(args) > 0 && args[0] == "names" {
			if reads++; reads > 1 {
				return "", "nova-secrets names: seat control: grants file is corrupt", 2
			}
		}
		return out0(bin, args, env)
	}
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 2 || len(f.plays()) != 0 ||
		!strings.HasSuffix(errs, "nova-fleet up: seat control's names cannot be read (exit 2: nova-secrets names: seat control: grants file is corrupt), so whether a fleet exists is unknown; run: /bin/nova-secrets names --as control\n") {
		t.Errorf("up: code %d plays %v\n%s\n%s", code, f.plays(), out, errs)
	}
}

// An ansible-galaxy that cannot run leaves ansible.posix unchecked: missing, with the reason, never OK.
func TestGalaxyThatCannotRunIsNotPosix(t *testing.T) {
	f := &upFake{noGalaxy: true}
	f.install(t)
	code, out, _ := runArgs("check")
	if code != 1 || strings.Contains(out, "CHECK OK ansible.posix") ||
		!strings.Contains(out, "CHECK MISSING ansible.posix: nova-fleet show requirements > requirements.yml && /bin/ansible-galaxy collection install -r requirements.yml  # /bin/ansible-galaxy could not run, so the collections are unchecked: exec: \"/bin/ansible-galaxy\": file does not exist\n") {
		t.Errorf("check: code %d\n%s", code, out)
	}
}

// tailscale status failing on this control node is said as that, not as the store being off the tailnet.
func TestUpTailscaleStatusFailureIsSaid(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD", offnet: true}
	f.install(t)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 2 || strings.Contains(errs, "is not on the tailnet") ||
		errs != "nova-fleet up: seat control holds NOVA_PG_PASSWORD, so a fleet exists, and this control node cannot look its store up on the tailnet (tailscale status exit 1: Logged out.); run: export NOVA_PG_DSN=postgres://nova_config@<the store machine's tailnet name>:5432/nova\n" {
		t.Errorf("code %d\n%s\n%s", code, out, errs)
	}
}

// --check pins the host keys of machines already on the tailnet, the one thing it writes on this node, so its
// ssh probes over the tailnet are checked against them.
func TestUpCheckPinsHostKeys(t *testing.T) {
	f := &upFake{peers: map[string]string{"first": "100.64.0.10", "second": "100.64.0.11"}}
	f.install(t)
	_, out, errs := runArgs("up", "--check", "--tools", "v2.0.0", "first=203.0.113.7", "second=203.0.113.8")
	if !strings.Contains(out, "UP HOSTKEY first OK pinned=1") {
		t.Errorf("stdout lacks the pin:\n%s\n%s", out, errs)
	}
	kh, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")) // ignored: absent is checked below
	if !strings.Contains(string(kh), "first,first.ts.example,100.64.0.10 ssh-ed25519 KEY-first\n") {
		t.Errorf("known_hosts:\n%s", kh)
	}
}

// A known_hosts whose last line has no newline keeps that line whole: the pins start on a line of their own.
func TestUpPinsAfterALastLineWithoutNewline(t *testing.T) {
	f := &upFake{peers: map[string]string{"first": "100.64.0.10", "second": "100.64.0.11"}}
	f.install(t)
	kh := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(kh), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kh, []byte("existing.example.com ssh-ed25519 KEY-existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	runArgs("up", "--tools", "v2.0.0", "first=203.0.113.7", "second=203.0.113.8")
	b, _ := os.ReadFile(kh) // ignored: written above
	if !strings.HasPrefix(string(b), "existing.example.com ssh-ed25519 KEY-existing\nfirst,first.ts.example,100.64.0.10 ssh-ed25519 KEY-first\n") {
		t.Errorf("known_hosts:\n%s", b)
	}
}

// A known_hosts that cannot be written leaves the host keys unpinned, and up says so.
func TestUpUnpinnedHostKeyIsSaid(t *testing.T) {
	f := &upFake{}
	existing(t, f, "")
	ssh := filepath.Join(os.Getenv("HOME"), ".ssh")
	if err := os.WriteFile(ssh, nil, 0o600); err != nil { // a file where the directory goes
		t.Fatal(err)
	}
	_, out, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
	if strings.Contains(out, "UP HOSTKEY") || !strings.Contains(errs, "nova-fleet up: second's Tailscale SSH host keys are not pinned in "+filepath.Join(ssh, "known_hosts")+": ") ||
		!strings.Contains(errs, "; run: ls -ld "+ssh+" "+filepath.Join(ssh, "known_hosts")+"\n") {
		t.Errorf("%s\n%s", out, errs)
	}
}

// The failures of the bus check, the first machine's inventory and a grant that cannot start each say why.
func TestUpFailuresSayWhy(t *testing.T) {
	f := &upFake{busErr: "nova-bus who: WRONGPASS invalid username-password pair"}
	existing(t, f, "")
	code, _, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
	if code != 1 || !strings.Contains(errs, "nova-fleet up: nova-bus who did not answer at store-1.ts.example:6379 (exit 1): nova-bus who: WRONGPASS invalid username-password pair; run: NOVA_BUS_REDIS=store-1.ts.example:6379 NOVA_BUS_USER=bus /bin/nova-secrets exec --only NOVA_BUS_PASSWORD -- /bin/nova-bus who\n") {
		t.Errorf("bus: code %d\n%s", code, errs)
	}

	f = &upFake{}
	existing(t, f, "")
	run := runCmd
	runCmd = func(bin string, args, env []string) (int, error) {
		if len(args) > 0 && args[0] == "grant" {
			return 0, errors.New("fork/exec /bin/nova-secrets: permission denied")
		}
		return run(bin, args, env)
	}
	code, _, errs = runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
	if code != 1 || !strings.Contains(errs, "nova-fleet up: run nova-secrets grant: fork/exec /bin/nova-secrets: permission denied\nUP grant second FAILED exit=1;") {
		t.Errorf("grant: code %d\n%s", code, errs)
	}

	f = &upFake{}
	f.install(t)
	out := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if filepath.Base(bin) == "ssh" { // the extracted directory exists by now: put a directory where first.ini goes
			dirs, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "nova-fleet-*"))
			for _, d := range dirs {
				_ = os.Mkdir(filepath.Join(d, "first.ini"), 0o700) // ignored: a second call finds it there
			}
		}
		return out(bin, args, env)
	}
	code, _, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 1 || !strings.Contains(errs, "nova-fleet up: write the first machine's inventory: open ") ||
		!strings.Contains(errs, "first.ini: is a directory; run: the same nova-fleet up with TMPDIR set to a writable directory\nUP first FAILED exit=1;") {
		t.Errorf("first.ini: code %d\n%s", code, errs)
	}
}
