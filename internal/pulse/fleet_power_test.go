package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fleet power tests (SPEC-PULSE ## Fleet, issue #880 item 17): `fleet suspend` puts
// the idle benches to sleep and `fleet wake` wakes them by magic packet. They drive a fake
// ssh on the test's own PATH that runs the piped remote script through `bash -s` under a
// fake HOME whose bin holds fake sudo, systemctl and pgrep, so no test reaches a machine
// and no test opens a socket.

func writeFleetPowerExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fleetPowerFakeSSH writes a fake ssh that runs the piped remote script through bash -s,
// plus fake sudo, systemctl and pgrep in a bin directory put first on PATH. answerAfter > 0
// makes every call before it fail with 255, the way a bench that is still asleep refuses
// ssh; 0 answers at once.
func fleetPowerFakeSSH(t *testing.T, answerAfter int) (sshPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh runs bash -s; the fleet tests are unix-only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFleetPowerExe(t, filepath.Join(bin, "sudo"),
		"#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"${NOVA_SUDO_LOG:-/dev/null}\"\nexit 0\n")
	writeFleetPowerExe(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\nexit 0\n")
	writeFleetPowerExe(t, filepath.Join(bin, "pgrep"), "#!/bin/sh\nexit 1\n")
	sshPath = filepath.Join(dir, "ssh")
	body := "#!/bin/sh\nd=$(dirname \"$0\")\n"
	if answerAfter > 0 {
		body += fmt.Sprintf("c=\"$d/calls\"; n=0; [ -f \"$c\" ] && n=$(cat \"$c\")\n"+
			"n=$((n+1)); printf '%%s' \"$n\" > \"$c\"\n[ \"$n\" -lt %d ] && exit 255\n", answerAfter)
	}
	body += "exec bash -s\n"
	writeFleetPowerExe(t, sshPath, body)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return sshPath
}

func writeFleetPowerBenches(t *testing.T, lines string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "benches.tsv")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fleetPowerClock is the injected clock: Sleep advances it, so a test reaches a wall of
// minutes without waiting.
type fleetPowerClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fleetPowerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fleetPowerClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fleet-wake-sends-magic-packet: the packet is six 0xFF bytes then the mac sixteen times,
// exactly, and nothing else.
func TestFleetMagicPacketIsExact(t *testing.T) {
	packet, err := MagicPacket("01:02:03:04:05:06")
	if err != nil {
		t.Fatalf("MagicPacket: %v", err)
	}
	if len(packet) != 102 {
		t.Fatalf("packet length = %d, want 102", len(packet))
	}
	want := make([]byte, 102)
	for i := 0; i < 6; i++ {
		want[i] = 0xFF
	}
	for i := 0; i < 16; i++ {
		for j := 0; j < 6; j++ {
			want[6+i*6+j] = byte(j + 1)
		}
	}
	if !bytes.Equal(packet, want) {
		t.Fatalf("packet = % x\nwant     % x", packet, want)
	}
}

// fleet-suspend-sleeps-only-idle: a bench whose swarm root holds a job directory with a
// live pid is BUSY, never suspended, and the verb exits 2.
func TestFleetSuspendBusyBenchIsRefused(t *testing.T) {
	ssh := fleetPowerFakeSSH(t, 0)
	home := t.TempDir()
	jobDir := filepath.Join(home, "rowan-swarm-root", "0", "jobs", "card-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	record := fmt.Sprintf("{\"job\":\"card-1\",\"slot\":0,\"state\":\"launched\",\"pid\":%d}\n", pid)
	if err := os.WriteFile(filepath.Join(jobDir, "pid"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	benches := writeFleetPowerBenches(t, "worker-1\tnova@worker1\t"+home+"\t-\n")

	var out, errb bytes.Buffer
	code := FleetSuspend(FleetSuspendInput{
		Benches: benches, Names: []string{"worker-1"}, SSH: ssh,
		Timeout: 10 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; out=%q err=%q", code, out.String(), errb.String())
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "FLEET worker-1 BUSY ") {
		t.Fatalf("line = %q, want a FLEET worker-1 BUSY line", got)
	}
}

// fleet-wake-sends-magic-packet + the wake poll: the fake ssh answers only on the third
// poll, so the verb sends exactly one magic packet and prints AWAKE with the wall the
// injected clock measured.
func TestFleetWakeAnswersOnThirdPoll(t *testing.T) {
	ssh := fleetPowerFakeSSH(t, 3)
	home := t.TempDir()
	benches := writeFleetPowerBenches(t, "worker-1\tnova@worker1\t"+home+"\t01:02:03:04:05:06\n")

	clock := &fleetPowerClock{now: time.Unix(1000, 0).UTC()}
	sent := 0
	var out, errb bytes.Buffer
	code := FleetWake(FleetWakeInput{
		Benches: benches, Names: []string{"worker-1"}, SSH: ssh,
		Wait: 3 * time.Minute, Timeout: 10 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep,
		Send:   func(packet []byte, addr string) error { sent++; return nil },
		Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q err=%q", code, out.String(), errb.String())
	}
	if sent != 1 {
		t.Fatalf("magic packets sent = %d, want 1", sent)
	}
	if got, want := strings.TrimSpace(out.String()), "FLEET worker-1 AWAKE wall=45"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}
