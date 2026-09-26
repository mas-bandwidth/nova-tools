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
// ansible-playbook and ansible in a temp directory on the PATH the runner's
// Environ gives (testguard reads a program under the temp directory as a
// fake, never a host; no t.Setenv, so the tests run in parallel) print a
// fixture and record what they were started with: the directory, the argv,
// stdin, and the environment the release builds (goenv.Clean of Environ plus
// PlayEnv).

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

// fakeInventory is the play directory's inventory.py: it records the
// registry and argv it was started with and prints inventory.json.
const fakeInventory = `#!/bin/sh
d=$(dirname "$0")
echo "FLEET_REGISTRY=$FLEET_REGISTRY ARGV=$*" > "$d/inventory.seen"
cat "$d/inventory.json"
`

// fakeAnsibleBin writes the two fakes into a temp directory and returns it
// with the environment a runner starts them in: that directory first on
// PATH, a temp HOME, and a secret that must not reach ansible;
// <dir>/<prog>.out is what each prints.
func fakeAnsibleBin(t *testing.T) (string, []string) {
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
	return bin, []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GH_TOKEN=not-for-ansible"}
}

// execPlayDir is a play directory whose inventory.py is fakeInventory
// printing body.
func execPlayDir(t *testing.T, body string) string {
	t.Helper()
	d := releasePlayDir(t)
	inv := filepath.Join(d, fleetbuild.PlayInventory)
	if err := os.WriteFile(inv, []byte(fakeInventory), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inv, 0o755); err != nil { // releasePlayDir wrote it 0644
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "inventory.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
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
	t.Parallel()
	bin, environ := fakeAnsibleBin(t)
	real := ansibleFixture(t, "ansible-play-2026-09-26.log")
	if err := os.WriteFile(filepath.Join(bin, "ansible-playbook.out"), real, 0o644); err != nil {
		t.Fatal(err)
	}
	dir, reg := releasePlayDir(t), releaseRegistry(t)
	benches := []string{"space", "hetzner", "hulk", "vision", "batman", "superman"}
	argv := fleetbuild.PlayArgv(fleetbuild.DefaultPlay, "v0.16.0-dev.dec7b954", benches)
	out, err := releaseExec{Environ: environ}.Run(context.Background(), dir, fleetbuild.PlayEnv(reg), argv)
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
	if !strings.HasPrefix(s["PATH"], bin+string(os.PathListSeparator)) || "HOME="+s["HOME"] != environ[1] ||
		s["ANSIBLE_NOCOWS"] != "1" || s["ANSIBLE_HOST_KEY_CHECKING"] != "True" || s[fleetbuild.PlayRegistry] != reg {
		t.Errorf("env PATH=%q HOME=%q NOCOWS=%q HOSTKEY=%q REGISTRY=%q", s["PATH"], s["HOME"], s["ANSIBLE_NOCOWS"], s["ANSIBLE_HOST_KEY_CHECKING"], s[fleetbuild.PlayRegistry])
	}
	if _, ok := s["GH_TOKEN"]; ok {
		t.Errorf("a secret reached ansible")
	}
}

// execRelRunner is the verb's runner with inventory.py, ansible and
// ansible-playbook started for real (the fakes) and every other child
// faked; a beat ansible answered CHANGED for catches up in the store.
type execRelRunner struct {
	fake *verbRelFake
	exec releaseExec
}

func (e execRelRunner) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	if argv[0] != "ansible" && argv[0] != "ansible-playbook" && !strings.HasSuffix(argv[0], "/"+fleetbuild.PlayInventory) {
		return e.fake.Run(ctx, dir, env, argv)
	}
	out, err := e.exec.Run(ctx, dir, env, argv)
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
// inventory.py --list runs first over the play's registry and its INVENTORY
// line counts the benches; the real play's six RECAP rows and OK; an
// inventory naming no bench (what inventory.py printed for the 4-column
// registry of the 2026-09-26 13:52 run) is REFUSED before any ansible runs,
// naming the 6-column row and the registry inventory.py reads unset; a play
// printing nothing is a REFUSED saying so, with the restart skipped; never
// "no answer" for a bench; the receipt names the play log.
func TestFleetReleaseReportsWhatAnsibleSaid(t *testing.T) {
	t.Parallel()
	both := `{"benches": {"hosts": ["space", "hulk"]}, "store": {"hosts": ["space"]}, "coordinator": {"hosts": ["studio"]}, "_meta": {"hostvars": {}}}` + "\n"
	none := `{"benches": {"hosts": []}, "store": {"hosts": []}, "coordinator": {"hosts": []}, "_meta": {"hostvars": {}}}` + "\n"
	for _, tc := range []struct {
		name, inv, play string
		code            int
		want            []string
		ran             []string // the fakes that ran
	}{
		{"the real play", both, "ansible-play-2026-09-26.log", 0, []string{"INVENTORY hosts=2 registry=",
			"RELEASE play OK tools.yml version=" + verbVersion + " hosts=6 beats-restarted=1 log="}, []string{"ansible-playbook", "ansible"}},
		{"empty inventory", none, "ansible-play-empty-inventory.log", 1, []string{"INVENTORY hosts=0 registry=",
			"RELEASE play REFUSED: inventory.py --list names no bench from FLEET_REGISTRY=",
			" (inventory.py reads rows of 6 tab-separated columns (name ssh os/arch roles seat cores; a bench's roles hold bench) and drops the rest; unset, FLEET_REGISTRY is ~/rowan-working/queue/control/machines.tsv (--machines <that registry>))\n"}, nil},
		{"nothing printed", both, "", 1, []string{"BEAT skipped hulk: the play reached no bench\n",
			"RELEASE play REFUSED: ansible printed nothing (exit status 0) (the play reached none of space,hulk;"}, []string{"ansible-playbook"}},
	} {
		bin, environ := fakeAnsibleBin(t)
		var play []byte
		if tc.play != "" {
			play = ansibleFixture(t, tc.play)
		}
		os.WriteFile(filepath.Join(bin, "ansible-playbook.out"), play, 0o644)
		os.WriteFile(filepath.Join(bin, "ansible.out"), []byte("hulk | CHANGED | rc=0 >>\n\n"), 0o644)
		dir, reg := execPlayDir(t, tc.inv), releaseRegistry(t)

		mr := miniredis.RunT(t)
		mr.SAdd("benches", "space", "hulk")
		mr.HSet("bench:space:beat", "build", "nova-sprint "+verbVersion+" linux/amd64 go1.26.6")
		f := &verbRelFake{home: t.TempDir(), mr: mr}
		sleeps := 0
		deps := verbDeps(f, map[string]string{fleetbuild.PlayDirEnv: dir}, "studio", &sleeps)
		deps.Runner = execRelRunner{f, releaseExec{Environ: environ}}
		var out, errOut bytes.Buffer
		code := runFleetReleaseWith(context.Background(), []string{verbSha, "--redis", mr.Addr(), "--machines", reg, "--wait", "0s"}, &out, &errOut, deps)
		s := out.String()
		if code != tc.code || strings.Contains(s, "no answer") {
			t.Errorf("%s: code=%d\n%s\nerr=%s", tc.name, code, s, errOut.String())
		}
		for _, w := range tc.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q in\n%s", tc.name, w, s)
			}
		}
		if b, err := os.ReadFile(filepath.Join(dir, "inventory.seen")); err != nil || string(b) != "FLEET_REGISTRY="+reg+" ARGV=--list\n" {
			t.Errorf("%s: inventory.py saw %q (%v)", tc.name, b, err)
		}
		var ran []string
		for _, p := range []string{"ansible-playbook", "ansible"} {
			if _, err := os.Stat(filepath.Join(bin, p+".seen")); err == nil {
				ran = append(ran, p)
			}
		}
		if strings.Join(ran, ",") != strings.Join(tc.ran, ",") {
			t.Errorf("%s: ran %v, want %v", tc.name, ran, tc.ran)
		}
		logPath := filepath.Join(f.home, "nova-bench", "release-src", "logs", "play-"+verbVersion+".log")
		if tc.ran == nil {
			if strings.Contains(s, "ANSIBLE LOG") {
				t.Errorf("%s: a log of a play that never ran:\n%s", tc.name, s)
			}
			continue
		}
		if !strings.Contains(s, "ANSIBLE LOG "+logPath+" bytes="+strconv.Itoa(len(play))+"\n") {
			t.Errorf("%s: no play log line:\n%s", tc.name, s)
		}
		if b, err := os.ReadFile(logPath); err != nil || !bytes.Equal(b, play) {
			t.Errorf("%s: play log %v, %d bytes of %d", tc.name, err, len(b), len(play))
		}
	}
}
