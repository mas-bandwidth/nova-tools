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
}

func (f *upFake) install(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	for _, e := range []string{"NOVA_SECRETS_STORE", "NOVA_SECRETS_KEY", "NOVA_SECRETS_SEAT"} {
		t.Setenv(e, "/x/"+strings.ToLower(e))
	}
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
		case strings.Contains(line, "-- true") && f.readErr != "":
			return "", f.readErr, 125
		case strings.Contains(line, "nova-secrets names"):
			return f.names, "", 0
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
		if i := slices.IndexFunc(w, func(s string) bool { return strings.HasSuffix(s, "ansible-playbook") }); i >= 0 {
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
	code, _, errs := runArgs("up", "--tools", "v1.0.0", "--runners", "https://github.com/o/r", "a=1.2.3.4")
	if code != 2 || !strings.HasPrefix(errs, "nova-fleet up: the control node lacks psql, gh, $NOVA_SECRETS_KEY; ") {
		t.Errorf("code %d stderr %q", code, errs)
	}
	f = &upFake{readErr: "SECRETS EXEC REFUSED name=TAILSCALE_AUTHKEY reason=not-in-seat\n"}
	f.install(t)
	code, out, errs := runArgs("up", "--check", "--tools", "v1.0.0", "a=1.2.3.4")
	if code != 2 || out != "" || !strings.Contains(errs, "TAILSCALE_AUTHKEY cannot be read from seat /x/nova_secrets_seat (exit 125): SECRETS EXEC REFUSED name=TAILSCALE_AUTHKEY reason=not-in-seat;") {
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
		"install.yml@inventory", "services.yml@inventory", "github-runners.yml@inventory"}
	if got := f.plays(); !slices.Equal(got, want) {
		t.Errorf("plays = %v, want %v", got, want)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"nova-secrets exec --only TAILSCALE_AUTHKEY -- true",
		"nova-secrets exec --only GITHUB_RUNNER_TOKEN -- true",
		"nova-secrets exec --only TAILSCALE_AUTHKEY -- /bin/ansible-playbook -i first.ini tailscale.yml -e nova_tailscale_record_address=false -e nova_firewall=false",
		"redis.yml -e nova_seal_store=/x/nova_secrets_store -e nova_seal_seat=/x/nova_secrets_seat -e nova_seal_key=/x/nova_secrets_key",
		"tailscale ip -4 first",
		"nova-secrets exec --only NOVA_PG_PASSWORD -- sh -c",
		"postgres://nova_config@100.64.0.7:5432/nova",
		"nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD -- /bin/ansible-playbook -i inventory join.yml -e nova_join_host=first -e nova_join_user=nova -e nova_join_address=203.0.113.7 -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=services,bench,runner",
		"-e nova_join_host=second -e nova_join_user=nova -e nova_join_address=203.0.113.8 -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench,runner",
		"git -C /x/nova_secrets_store push --quiet",
		"install.yml -e nova_tools_version=v2.0.0",
		"github-runners.yml -e github_runner_url=https://github.com/o/r -e github_runner_scope=repo -e runner_count=2",
		"nova-secrets exec --only NOVA_BUS_PASSWORD -- /bin/nova-bus send --as nova-fleet-up --to nova-fleet-up up check up-",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	for _, w := range []string{"UP CONTROL OK", "UP FIRST OK first pg=100.64.0.7:5432", "UP STORE OK", "UP BUS OK 100.64.0.7:6379 round-trip=up-",
		"UP MACHINE first OK address=203.0.113.7 steps=tailnet,postgres,redis,join,tools,services,runners,bus",
		"UP MACHINE second OK address=203.0.113.8 steps=join,tools,services,runners,bus"} {
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
	code, out, _ = runArgs("up", "--check", "--tools", "v2.0.0", "first=1.1.1.1")
	all := strings.Join(f.calls, "\n")
	if code != 0 || len(f.plays()) != 6 || strings.Contains(all, "push") || strings.Contains(all, "sh -c") || strings.Contains(all, "nova-bus send") {
		t.Errorf("code %d plays %v calls:\n%s", code, f.plays(), all)
	}
	if !strings.Contains(out, "UP MACHINE first CHECKED") || !strings.Contains(out, "(check: nothing changed)") {
		t.Errorf("stdout %s", out)
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
	for _, p := range []string{"tailscale", "postgres", "redis", "join", "install", "services", "github-runners"} {
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
