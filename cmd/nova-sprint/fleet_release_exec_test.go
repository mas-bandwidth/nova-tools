package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
)

// The play answers (#4383 fix) through the REAL releaseExec.Run: a fake
// ansible-playbook and ansible in a temp directory on PATH (testguard reads
// a program under the temp directory as a fake, never a host) print a
// fixture and record what they were started with: the directory, the argv,
// stdin, and the environment the release builds (goenv.Clean of this
// process's plus PlayEnv).

const fakeAnsible = `#!/bin/sh
d=$(dirname "$0")
n=$(basename "$0")
{
	echo "PWD=$(pwd)"
	echo "ARGV=$*"
	if read -r l; then echo "STDIN=$l"; else echo "STDIN=EOF"; fi
	env
} > "$d/$n.seen"
cat "$d/$n.out"
`

// fakeAnsibleBin writes the two fakes into a temp directory, puts it first
// on PATH and returns it; <dir>/<prog>.out is what each prints.
func fakeAnsibleBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, p := range []string{"ansible-playbook", "ansible"} {
		if err := os.WriteFile(filepath.Join(bin, p), []byte(fakeAnsible), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, p+".out"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func ansibleFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "nsprint", "fleetbuild", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// seen is what a fake recorded: its PWD, ARGV and STDIN lines and its
// environment, by name.
func seen(t *testing.T, bin, prog string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bin, prog+".seen"))
	if err != nil {
		t.Fatalf("%s did not run: %v", prog, err)
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			if _, dup := m[k]; !dup {
				m[k] = v
			}
		}
	}
	return m
}

// TestReleaseExecRunsThePlayAsByHand: the play step's argv through
// releaseExec.Run reaches the fake ansible-playbook in the play directory
// with the hand run's argv, stdin at EOF, and an environment holding PATH,
// HOME, the play's ansible settings and FLEET_REGISTRY, and no secret; the
// output comes back byte for byte and Recap reads the real RECAP from it.
func TestReleaseExecRunsThePlayAsByHand(t *testing.T) {
	bin := fakeAnsibleBin(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GH_TOKEN", "not-for-ansible")
	real := ansibleFixture(t, "ansible-play-2026-09-26.log")
	if err := os.WriteFile(filepath.Join(bin, "ansible-playbook.out"), real, 0o644); err != nil {
		t.Fatal(err)
	}
	dir, reg := releasePlayDir(t), releaseRegistry(t)
	benches := []string{"space", "hetzner", "hulk", "vision", "batman", "superman"}
	argv := fleetbuild.PlayArgv(fleetbuild.DefaultPlay, "v0.16.0-dev.dec7b954", benches)
	out, err := releaseExec{}.Run(context.Background(), dir, fleetbuild.PlayEnv(reg), argv)
	if err != nil || out != string(real) {
		t.Fatalf("run: %v; %d bytes back of %d", err, len(out), len(real))
	}
	if rows := fleetbuild.Recap(out); len(rows) != 6 || rows[0] != "batman : ok=18 changed=3 unreachable=0 failed=0 skipped=5 rescued=0 ignored=0" {
		t.Errorf("recap %q", rows)
	}
	s := seen(t, bin, "ansible-playbook")
	pwd, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(s["PWD"]); got != pwd {
		t.Errorf("dir %q, want %q", s["PWD"], pwd)
	}
	if s["ARGV"] != "-i inventory.py tools.yml --forks 16 --diff -e nova_build=v0.16.0-dev.dec7b954 --limit space,hetzner,hulk,vision,batman,superman" {
		t.Errorf("argv %q", s["ARGV"])
	}
	if s["STDIN"] != "EOF" {
		t.Errorf("stdin %q: ansible must not wait on one", s["STDIN"])
	}
	if !strings.HasPrefix(s["PATH"], bin+string(os.PathListSeparator)) || s["HOME"] != os.Getenv("HOME") ||
		s["ANSIBLE_NOCOWS"] != "1" || s["ANSIBLE_HOST_KEY_CHECKING"] != "True" || s[fleetbuild.PlayRegistry] != reg {
		t.Errorf("env PATH=%q HOME=%q NOCOWS=%q HOSTKEY=%q REGISTRY=%q", s["PATH"], s["HOME"], s["ANSIBLE_NOCOWS"], s["ANSIBLE_HOST_KEY_CHECKING"], s[fleetbuild.PlayRegistry])
	}
	if _, ok := s["GH_TOKEN"]; ok {
		t.Errorf("a secret reached ansible")
	}
}

// execRelRunner is the verb's runner with ansible and ansible-playbook
// started for real (the fakes on PATH) and every other child faked; a beat
// ansible answered CHANGED for catches up in the store.
type execRelRunner struct{ fake *verbRelFake }

func (e execRelRunner) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	if argv[0] != "ansible" && argv[0] != "ansible-playbook" {
		return e.fake.Run(ctx, dir, env, argv)
	}
	out, err := releaseExec{}.Run(ctx, dir, env, argv)
	if argv[0] == "ansible" {
		for h, st := range fleetbuild.ParseAdhoc(out) {
			if st == "CHANGED" {
				e.fake.mr.HSet("bench:"+h+":beat", "build", "nova-sprint "+verbVersion+" linux/amd64 go1.26.6")
			}
		}
	}
	return out, err
}

// TestFleetReleaseReportsWhatAnsibleSaid is the verb over the real exec:
// the real play's six RECAP rows and OK; the empty inventory's play (exit 0,
// warnings, a PLAY RECAP with no row: the 2026-09-26 13:52 run) is one
// REFUSED naming ansible's first line; a play printing nothing is a REFUSED
// saying so; never "no answer" for a bench; the receipt names the play log.
func TestFleetReleaseReportsWhatAnsibleSaid(t *testing.T) {
	bin := fakeAnsibleBin(t)
	for _, tc := range []struct {
		name, play, adhoc string
		code              int
		want              string
	}{
		{"the real play", "ansible-play-2026-09-26.log", "", 0, "RELEASE play OK tools.yml version=" + verbVersion + " hosts=6 beats-restarted=1 log="},
		{"empty inventory", "ansible-play-empty-inventory.log", "ansible-adhoc-empty-inventory.log", 1,
			`RELEASE play REFUSED: ansible printed nothing parseable: "[WARNING]: provided hosts list is empty, only localhost is available. Note that the implicit localhost does not match 'all'" (the play reached none of space,hulk; the inventory read FLEET_REGISTRY=`},
		{"nothing printed", "", "", 1, "RELEASE play REFUSED: ansible printed nothing (exit status 0) (the play reached none of space,hulk;"},
	} {
		var play []byte
		adhoc := []byte("hulk | CHANGED | rc=0 >>\n\n")
		if tc.play != "" {
			play = ansibleFixture(t, tc.play)
		}
		if tc.adhoc != "" {
			adhoc = ansibleFixture(t, tc.adhoc)
		}
		if tc.name == "nothing printed" {
			adhoc = nil
		}
		os.WriteFile(filepath.Join(bin, "ansible-playbook.out"), play, 0o644)
		os.WriteFile(filepath.Join(bin, "ansible.out"), adhoc, 0o644)

		mr := miniredis.RunT(t)
		mr.SAdd("benches", "space", "hulk")
		mr.HSet("bench:space:beat", "build", "nova-sprint "+verbVersion+" linux/amd64 go1.26.6")
		f := &verbRelFake{home: t.TempDir(), mr: mr}
		sleeps := 0
		deps := verbDeps(f, map[string]string{fleetbuild.PlayDirEnv: releasePlayDir(t)}, "studio", &sleeps)
		deps.Runner = execRelRunner{f}
		var out, errOut bytes.Buffer
		code := runFleetReleaseWith(context.Background(), []string{verbSha, "--redis", mr.Addr(), "--machines", releaseRegistry(t), "--wait", "0s"}, &out, &errOut, deps)
		s := out.String()
		logPath := filepath.Join(f.home, "nova-bench", "release-src", "logs", "play-"+verbVersion+".log")
		if code != tc.code || !strings.Contains(s, tc.want) || strings.Contains(s, "no answer") ||
			!strings.Contains(s, "ANSIBLE LOG "+logPath+" bytes="+strconv.Itoa(len(play))+"\n") {
			t.Errorf("%s: code=%d\n%s\nerr=%s", tc.name, code, s, errOut.String())
		}
		if b, err := os.ReadFile(logPath); err != nil || !bytes.Equal(b, play) {
			t.Errorf("%s: play log %v, %d bytes of %d", tc.name, err, len(b), len(play))
		}
	}
}
