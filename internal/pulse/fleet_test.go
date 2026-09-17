package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// SPEC-PULSE ## Fleet: the section names the benches file, the fleet rule,
// and every fleet verb. A verb renamed in the spec and not here (or vice
// versa) is red.
func TestFleetSectionListsVerbs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "\n## Fleet\n")
	if !ok {
		t.Fatal("the spec has no ## Fleet section")
	}
	end := strings.Index(after, "\n## ")
	section := after
	if end >= 0 {
		section = after[:end]
	}
	for _, want := range []string{
		"fleet survey",
		"fleet reboot",
		"fleet secrets",
		"fleet suspend",
		"fleet wake",
		"fleet standard",
		"FLEET ",
		"--ssh",
		"studio",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the ## Fleet section does not name %q", want)
		}
	}
}

// The fleet tests drive a fake ssh on the test's own PATH. There is no network call and no
// machine is touched: the fake runs the remote script locally through `bash -s` under a
// fake HOME whose bin holds fake sudo and systemctl, and it counts poll commands per
// target so a bench can be made to answer late or never.

func writeFleetExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeFleetSSH builds the fake and returns its path and the fake HOME the script runs under.
// The fake fails the first two poll commands for every target (the bench is still booting),
// answers the third, and answers nothing at all while $HOME/never exists.
func fakeFleetSSH(t *testing.T) (sshPath, home string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh runs bash -s; the fleet tests are unix-only")
	}
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFleetExe(t, filepath.Join(bin, "systemctl"), `#!/bin/sh
case "$1" in
  list-units) echo "nova-runner-1.service loaded active running"; exit 0;;
esac
echo active
exit 0
`)
	writeFleetExe(t, filepath.Join(bin, "sudo"), "#!/bin/sh\nexit 0\n")
	sshPath = filepath.Join(dir, "ssh")
	writeFleetExe(t, sshPath, `#!/bin/sh
# the remote command is the last argument, the ssh target the one before it
for a in "$@"; do target="$prev"; prev="$a"; done
cmd="$prev"
home="${NOVA_FAKE_HOME:?}"
if [ -f "$home/never" ]; then exit 255; fi
case "$cmd" in
  *is-active*)
    c="$home/polls-$target"
    n=0; [ -f "$c" ] && n=$(cat "$c")
    n=$((n+1)); printf '%s' "$n" > "$c"
    [ "$n" -lt 3 ] && exit 255
    ;;
esac
printf '%s\n' "$cmd" | HOME="$home" PATH="$home/bin:$PATH" bash -s
`)
	return sshPath, home
}

// fleetClock is the injected clock: Sleep advances it, so a test reaches a wall of minutes
// without waiting.
type fleetClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fleetClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fleetClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func writeFleetBenches(t *testing.T, lines string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fleet-reboot-waits-for-runners: the ssh answers only on the third poll, so the verb must
// print REBOOTED with the wall the injected clock measured and the active runner count.
func TestFleetRebootWaitsForRunners(t *testing.T) {
	ssh, home := fakeFleetSSH(t)
	t.Setenv("NOVA_FAKE_HOME", home)
	benches := writeFleetBenches(t, "worker-1\t nova@worker1\t/home/nova\t-\n")

	clock := &fleetClock{now: time.Unix(1000, 0).UTC()}
	var out, errb bytes.Buffer
	code := FleetReboot(FleetRebootInput{
		Benches: benches, Names: []string{"worker-1"}, SSH: ssh,
		Wait: 5 * time.Minute, Timeout: 10 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q err=%q", code, out.String(), errb.String())
	}
	if got, want := strings.TrimSpace(out.String()), "FLEET worker-1 REBOOTED wall=45 runners=1"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

// fleet-reboot-never-answers: a bench whose ssh never answers is a TIMEOUT after the whole
// wait and the verb exits 3.
func TestFleetRebootNeverAnswersTimesOut(t *testing.T) {
	ssh, home := fakeFleetSSH(t)
	t.Setenv("NOVA_FAKE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "never"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	benches := writeFleetBenches(t, "worker-2\t nova@worker2\t/home/nova\t-\n")

	clock := &fleetClock{now: time.Unix(1000, 0).UTC()}
	var out, errb bytes.Buffer
	code := FleetReboot(FleetRebootInput{
		Benches: benches, Names: []string{"worker-2"}, SSH: ssh,
		Wait: 45 * time.Second, Timeout: 10 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep, Stdout: &out, Stderr: &errb,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3; out=%q err=%q", code, out.String(), errb.String())
	}
	if got, want := strings.TrimSpace(out.String()), "FLEET worker-2 REBOOT TIMEOUT after 45s"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

// fleet-refuses-studio: any admin verb that names studio is refused by name, exits 2, and
// starts no ssh at all.
func TestFleetRebootRefusesStudio(t *testing.T) {
	ssh, home := fakeFleetSSH(t)
	t.Setenv("NOVA_FAKE_HOME", home)
	benches := writeFleetBenches(t, "studio\t rowan@studio\t/Users/rowan\t-\nworker-1\t nova@worker1\t/home/nova\t-\n")

	var out, errb bytes.Buffer
	code := FleetReboot(FleetRebootInput{
		Benches: benches, Names: []string{"studio"}, SSH: ssh,
		Now: func() time.Time { return time.Unix(1000, 0).UTC() }, Sleep: func(time.Duration) {},
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; out=%q err=%q", code, out.String(), errb.String())
	}
	if got := strings.TrimSpace(out.String()); !strings.Contains(got, "FLEET REFUSED bench=studio") {
		t.Fatalf("line = %q, want a FLEET REFUSED bench=studio line", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(home, "polls-*")); len(matches) != 0 {
		t.Fatalf("studio refusal started ssh: %v", matches)
	}
}

// fleet-refuses-an-unknown-bench: a name the file does not carry is a refusal, never a guess.
func TestFleetRebootRefusesUnknownBench(t *testing.T) {
	ssh, home := fakeFleetSSH(t)
	t.Setenv("NOVA_FAKE_HOME", home)
	benches := writeFleetBenches(t, "worker-1\t nova@worker1\t/home/nova\t-\n")

	var out, errb bytes.Buffer
	code := FleetReboot(FleetRebootInput{
		Benches: benches, Names: []string{"worker-9"}, SSH: ssh,
		Now: func() time.Time { return time.Unix(1000, 0).UTC() }, Sleep: func(time.Duration) {},
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; out=%q err=%q", code, out.String(), errb.String())
	}
	if got := strings.TrimSpace(out.String()); !strings.Contains(got, "FLEET REFUSED bench=worker-9") {
		t.Fatalf("line = %q, want a FLEET REFUSED bench=worker-9 line", got)
	}
}
