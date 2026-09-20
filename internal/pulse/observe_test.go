package pulse

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObservationWaitDelayIsPositive(t *testing.T) {
	if ObservationWaitDelay <= 0 {
		t.Fatal("ObservationWaitDelay is not positive; a killed observation whose descendants hold the pipe blocks forever")
	}
}

func TestIsolationArgvBypassesControlMaster(t *testing.T) {
	got := strings.Join(IsolationArgv(false, "bench.test", "true"), " ")
	for _, want := range []string{
		"-n",
		"BatchMode=yes",
		"ConnectTimeout=5",
		"ControlMaster=no",
		"ControlPath=none",
		"ServerAliveInterval=2",
		"ServerAliveCountMax=2",
		"bench.test",
		"true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("IsolationArgv missing %q: %q", want, got)
		}
	}
	stdin := strings.Join(IsolationArgv(true, "bench.test", "bash", "-s"), " ")
	if strings.Contains(stdin, "-n ") || strings.HasPrefix(stdin, "-n") {
		t.Errorf("stdin ssh must not pass -n (it disconnects the script): %q", stdin)
	}
}

// TestObserveHostsMarksAHangingHostMissedAndKeepsTheHealthyOne is the fake
// control for #2009: one hanging host beside one healthy host returns within
// the bound, retains the healthy result and marks the other missed. No live
// probe ran here.
func TestObserveHostsMarksAHangingHostMissedAndKeepsTheHealthyOne(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	healthy := make(chan struct{})
	done := make(chan []HostObservation[int], 1)
	go func() {
		done <- ObserveHosts(ctx, []string{"down.test", "up.test"}, func(ctx context.Context, host string) (int, error) {
			if host == "down.test" {
				<-ctx.Done()
				return 0, ctx.Err()
			}
			close(healthy)
			return 7, nil
		})
	}()
	select {
	case <-healthy:
	case <-time.After(fillTestWait(t)):
		t.Fatal("the healthy host never answered")
	}
	cancel()
	var obs []HostObservation[int]
	select {
	case obs = <-done:
	case <-time.After(fillTestWait(t)):
		t.Fatal("ObserveHosts did not return after the hanging host was cancelled")
	}
	if len(obs) != 2 {
		t.Fatalf("observations = %d, want 2", len(obs))
	}
	if !obs[0].Missed || obs[0].Host != "down.test" {
		t.Fatalf("down.test = %+v, want missed", obs[0])
	}
	if obs[1].Missed || obs[1].Value != 7 || obs[1].Err != nil {
		t.Fatalf("up.test = %+v, want value 7", obs[1])
	}
}

func TestSSHShellBypassesControlMaster(t *testing.T) {
	src := fakeBins(t)
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh"+exeSuffix())
	rawBin, err := os.ReadFile(filepath.Join(src, "gh"+exeSuffix()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ssh, rawBin, 0o755); err != nil {
		t.Fatal(err)
	}
	specs := filepath.Join(dir, "fakes")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_PULSE_FAKE_DIR", specs)
	log := filepath.Join(dir, "ssh.log")
	fakeTool(t, specs, "ssh", fakeSpec{Log: log, Default: fakeRule{Stdout: "ok\n"}})
	if _, err := (sshShell{Program: ssh}).Run("bench-x", "true"); err != nil {
		t.Fatalf("sshShell: %v", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"ControlMaster=no",
		"ControlPath=none",
		"ServerAliveInterval=2",
		"ServerAliveCountMax=2",
		"ConnectTimeout=",
		"BatchMode=yes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ssh argv missing %q: %q", want, got)
		}
	}
}
