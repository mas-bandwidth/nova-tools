package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A provider whose first login is a sudo user: --first-login names it for every machine, and
// <name>=<login>@<address> for one. up reaches it there, checks it sudoes without a password,
// and join makes the fleet's login through it (nova_join_root_user), with its key when given.
func TestUpFirstLoginIsASudoUser(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1"},
		first: map[string]string{"203.0.113.8": "ubuntu", "203.0.113.9": "admin"}, keyed: map[string]bool{"203.0.113.8": true, "203.0.113.9": true},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}}]}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	key := filepath.Join(t.TempDir(), "first_key")
	if err := os.WriteFile(key, []byte("not a real key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "--first-login", "ubuntu", "--first-key", key, "second=203.0.113.8", "third=admin@203.0.113.9")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	all := strings.Join(f.calls, "\n")
	for _, w := range []string{
		"ssh -o BatchMode=yes -o ConnectTimeout=10 -i " + key + " -o IdentitiesOnly=yes ubuntu@203.0.113.8 true",
		"ssh -o BatchMode=yes -o ConnectTimeout=10 -i " + key + " -o IdentitiesOnly=yes ubuntu@203.0.113.8 sudo -n true",
		"ssh -o BatchMode=yes -o ConnectTimeout=10 -i " + key + " -o IdentitiesOnly=yes admin@203.0.113.9 sudo -n true",
		"join.yml -e nova_join_host=second -e nova_join_user=nova -e nova_join_address=203.0.113.8 -e nova_join_root_user=ubuntu -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_join_roles=bench -e nova_seal_store=" + f.store + " -e nova_bus_redis=store-1.ts.example:6379 -e ansible_ssh_private_key_file=" + key,
		"-e nova_join_host=third -e nova_join_user=nova -e nova_join_address=203.0.113.9 -e nova_join_root_user=admin",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no call contains %q; calls:\n%s", w, all)
		}
	}
	// after join, nothing uses the first login or its key: the grant reads the seat as the fleet's login over the tailnet
	if !strings.Contains(all, "ssh -o BatchMode=yes nova@second.ts.example sh -c") || strings.Contains(all, "root@") {
		t.Errorf("calls:\n%s", all)
	}
	for _, w := range []string{"UP REACH second OK ubuntu@203.0.113.8", "UP REACH third OK admin@203.0.113.9", "UP MACHINE second OK"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
}

// The default stays root: no --first-login, no key, no sudo check, join as root.
func TestUpFirstLoginDefaultsToRoot(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1"},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}}]}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
	all := strings.Join(f.calls, "\n")
	if code != 0 || !strings.Contains(all, "ssh -o BatchMode=yes -o ConnectTimeout=10 root@203.0.113.8 true") || strings.Contains(all, "sudo -n") ||
		strings.Contains(all, "ansible_ssh_private_key_file") || !strings.Contains(all, "-e nova_join_root_user=root") {
		t.Errorf("code %d\n%s\n%s\ncalls:\n%s", code, out, errs, all)
	}
}

// A first login that answers and cannot sudo without a password is refused before any play, under
// --check too, with the line that fixes it.
func TestUpFirstLoginWithoutSudoIsRefused(t *testing.T) {
	for _, check := range []bool{false, true} {
		f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1"},
			first: map[string]string{"203.0.113.8": "ubuntu"}, nosudo: map[string]bool{"203.0.113.8": true},
			rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}}]}`}
		f.install(t)
		t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
		args := []string{"up", "--tools", "v2.0.0", "second=ubuntu@203.0.113.8"}
		if check {
			args = append(args, "--check")
		}
		code, out, errs := runArgs(args...)
		want := "nova-fleet up: second answers ssh as ubuntu at 203.0.113.8, and ubuntu cannot sudo without a password (sudo: a password is required): join makes the fleet's login through it; run: ssh -t ubuntu@203.0.113.8 sudo visudo -f /etc/sudoers.d/90-ubuntu  # the one line: ubuntu ALL=(ALL) NOPASSWD:ALL\n"
		if code != 1 || !strings.HasPrefix(errs, want) || !strings.Contains(errs, "UP reach second FAILED") || len(f.plays()) != 0 {
			t.Errorf("check=%v: code %d plays %v\n%s\n%s", check, code, f.plays(), out, errs)
		}
	}
}

// Neither the first login nor anything else answers: the refusal names what was tried and how to
// name the provider's login and key.
func TestUpFirstLoginUnreachableNamesTheFix(t *testing.T) {
	f := &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1"},
		first: map[string]string{"203.0.113.8": "ubuntu"},
		rows:  `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}}]}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	code, _, errs := runArgs("up", "--tools", "v2.0.0", "second=203.0.113.8")
	want := "nova-fleet up: second does not answer ssh: ssh: connect to host 203.0.113.8 port 22: Operation timed out (tried root@203.0.113.8); run: ssh root@203.0.113.8 true  # a provider whose first login is not root: --first-login <login> or second=<login>@203.0.113.8, and --first-key <file> for its key\n"
	if code != 1 || !strings.HasPrefix(errs, want) || len(f.plays()) != 0 {
		t.Errorf("code %d plays %v stderr %q", code, f.plays(), errs)
	}
	// with a key the machine does not take: the remedy names the key it tried
	f = &upFake{names: "NOVA_PG_PASSWORD NOVA_BUS_PASSWORD", peers: map[string]string{"store-1": "100.64.0.1"},
		first: map[string]string{"203.0.113.8": "ubuntu"}, unreachable: map[string]bool{"203.0.113.8": true},
		rows: `{"items":[{"kind":"machine","fields":{"name":"store-1","address":"store-1.ts.example","user":"nova","os":"linux","arch":"amd64","roles":"services,bench"}}]}`}
	f.install(t)
	t.Setenv("NOVA_PG_DSN", "postgres://nova_config@store-1.ts.example:5432/nova")
	key := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(key, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs = runArgs("up", "--tools", "v2.0.0", "--first-login", "ubuntu", "--first-key", key, "second=203.0.113.8")
	if code != 1 || !strings.Contains(errs, "(tried ubuntu@203.0.113.8); run: ssh -i "+key+" -o IdentitiesOnly=yes ubuntu@203.0.113.8 true  # a provider whose first login is not ubuntu:") {
		t.Errorf("code %d stderr %q", code, errs)
	}
}

func TestUpFirstLoginParse(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--tools", "v1", "--first-login", "Root!", "a=1"}, "nova-fleet up: --first-login wants a login such as root or ubuntu, got \"Root!\"; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "--first-login"}, "nova-fleet up: --first-login wants a value; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "a=Bad@1.2.3.4"}, "nova-fleet up: want a machine as <name>=[<login>@]<address>[/<os>/<arch>] (a lowercase name and login), got a=Bad@1.2.3.4; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "a=ubuntu@"}, "nova-fleet up: want a machine as"},
		{[]string{"--tools", "v1", "a=nova@1.2.3.4"}, "nova-fleet up: a's first login is nova, the login join makes (--user nova): name the provider's login with --first-login, or another with --user; run: nova-fleet up -h\n"},
		{[]string{"--tools", "v1", "--first-key", "/nonexistent/key", "a=1"}, "nova-fleet up: --first-key /nonexistent/key is not a readable file; run: ls -l /nonexistent/key\n"},
	} {
		code, out, errs := runArgs(append([]string{"up"}, tc.args...)...)
		if code != 2 || out != "" || !strings.HasPrefix(errs, tc.want) {
			t.Errorf("up %q: code %d stdout %q stderr %q, want %q", tc.args, code, out, errs, tc.want)
		}
	}
	// a list goes as a JSON object with one name; its name is held to the same rule as name=value
	rule := `{"nova_firewall_allow_in": ["in on eno2 proto udp from 10.0.0.0/24"]}`
	if u, code := parseUp([]string{"--tools", "v1", "a=1", "--", "-e", rule}, nil); code != 0 || len(u.pass) != 2 || u.pass[1] != rule {
		t.Errorf("a JSON -e: code %d %+v", code, u)
	}
	for arg, want := range map[string]string{
		`{"nova_firewall": false}`: "nova-fleet up: -e nova_firewall is up's own",
		`{"a": 1, "b": 2}`:         "nova-fleet up: -e wants name=value or a JSON object with one name, got {\"a\": 1, \"b\": 2}",
	} {
		if code, _, errs := runArgs("up", "--tools", "v1", "a=1", "--", "-e", arg); code != 2 || !strings.HasPrefix(errs, want) {
			t.Errorf("-e %s: code %d stderr %q, want %q", arg, code, errs, want)
		}
	}
	if u, _ := parseUp([]string{"--tools", "v1", "a=1", "--", "-e", `{"nova_bin_dir": "/opt/nova/bin"}`}, nil); u == nil || u.binDir != "/opt/nova/bin" {
		t.Errorf("a JSON nova_bin_dir: %+v", u)
	}
	u, _ := parseUp([]string{"--tools", "v1", "--first-login", "ubuntu", "a=10.0.0.1", "b=ec2-user@b.example/linux/arm64", "c=root@10.0.0.3"}, nil)
	if u == nil || u.machines[0].first != "ubuntu" || u.machines[0].address != "10.0.0.1" || u.machines[1].first != "ec2-user" || u.machines[1].address != "b.example" ||
		u.machines[1].arch != "arm64" || u.machines[2].first != "root" {
		t.Errorf("parsed %+v", u)
	}
}
