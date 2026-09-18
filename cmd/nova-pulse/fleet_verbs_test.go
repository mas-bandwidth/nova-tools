package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The command layer of the four retired bench scripts: `fleet standard`, `fleet mirror`,
// `fleet join` and `fleet sleep`. What each verb DOES is held in internal/pulse; what is
// held here is that the sub-verb is reachable, that a missing flag is one refusal line
// naming the flag rather than a guess, and that the auth key is never a flag value.

// fleetVerbsEnv puts a fake ssh (which runs the piped script through bash -s) and a fake
// systemctl and sudo on PATH, and writes a one-bench fleet file. No test reaches a machine.
func fleetVerbsEnv(t *testing.T) (benches, home string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh runs the remote script through bash -s; the fleet verbs are unix-only here")
	}
	bin := t.TempDir()
	writeBenchExe(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nexec bash -s\n")
	writeBenchExe(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\nexit 0\n")
	writeBenchExe(t, filepath.Join(bin, "sudo"), "#!/bin/sh\n"+
		"while [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\nexec \"$@\"\n")
	writeBenchExe(t, filepath.Join(bin, "pgrep"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home = t.TempDir()
	benches = filepath.Join(t.TempDir(), "fleet.tsv")
	if err := os.WriteFile(benches, []byte("bench1\tbench1\t"+home+"\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return benches, home
}

// Each of the four verbs is reachable as a fleet sub-verb, and each refuses a missing
// required flag by name, exit 2, without reaching a bench.
func TestFleetVerbsRefuseTheirMissingFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"fleet", "standard"}, "--benches"},
		{[]string{"fleet", "standard", "--benches", "f.tsv"}, "--bench"},
		{[]string{"fleet", "mirror", "--benches", "f.tsv", "--bench", "bench1"}, "--repo"},
		{[]string{"fleet", "mirror", "--benches", "f.tsv", "--bench", "bench1", "--repo", "https://example.com/x.git"}, "--path"},
		{[]string{"fleet", "join", "--benches", "f.tsv", "--bench", "bench1"}, "--tailscale"},
		{[]string{"fleet", "join", "--benches", "f.tsv", "--bench", "bench1", "--tailscale", "/usr/bin/tailscale"}, "--authkey-env"},
		{[]string{"fleet", "sleep", "--benches", "f.tsv"}, "--bench"},
	} {
		var out, errb bytes.Buffer
		if code := run(tc.args, &out, &errb, time.Now().UTC()); code != 2 {
			t.Errorf("%v exit = %d, want 2", tc.args, code)
		}
		if !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%v refusal does not name %s:\n%s", tc.args, tc.want, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("%v printed a bench line before it had its flags:\n%s", tc.args, out.String())
		}
	}
}

// An --os the standard does not have is a refusal that names what it takes.
func TestFleetStandardRefusesAnUnknownOS(t *testing.T) {
	benches, _ := fleetVerbsEnv(t)
	var out, errb bytes.Buffer
	if code := run([]string{"fleet", "standard", "--benches", benches, "--bench", "bench1", "--os", "plan9"},
		&out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--os is linux or darwin") {
		t.Fatalf("refusal does not say what --os takes:\n%s", errb.String())
	}
}

// `fleet standard` runs end to end through the command layer: one STANDARD line per check
// and one verdict.
func TestFleetStandardRunsThroughTheCommandLayer(t *testing.T) {
	benches, home := fleetVerbsEnv(t)
	writeBenchExe(t, filepath.Join(home, ".local", "bin", "nova-swarm"), "#!/bin/sh\necho 'nova-swarm abc123'\n")
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "standard", "--benches", benches, "--bench", "bench1",
		"--os", "linux", "--want", "abc123", "--min-free", "0"}, &out, &errb, time.Now().UTC())
	// The temp home has no Go SDK and no seat, so the standard drifts; what matters here is
	// that the verb ran, printed its check lines, and counted them into one verdict.
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (a bare temp home drifts)\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "STANDARD bench1 nova-stamp OK ") {
		t.Errorf("the stamp check did not pass against the fake nova-swarm:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FLEET bench1 STANDARD DRIFT drift=") {
		t.Errorf("no verdict line:\n%s", out.String())
	}
}

// `fleet sleep` suspends one idle bench through the command layer.
func TestFleetSleepRunsThroughTheCommandLayer(t *testing.T) {
	benches, _ := fleetVerbsEnv(t)
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "sleep", "--benches", benches, "--bench", "bench1"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if got := strings.TrimSpace(out.String()); got != "FLEET bench1 SUSPENDED" {
		t.Fatalf("line = %q", got)
	}
}

// The auth key is never a flag: there is no --authkey flag to give one to, and the refusal
// for an empty variable names nova-secrets exec.
func TestFleetJoinTakesNoKeyOnTheCommandLine(t *testing.T) {
	benches, _ := fleetVerbsEnv(t)
	var out, errb bytes.Buffer
	if code := run([]string{"fleet", "join", "--benches", benches, "--bench", "bench1",
		"--tailscale", "/usr/local/bin/tailscale", "--authkey", "tskey-auth-nope"},
		&out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("--authkey exit = %d, want 2: the key is never a flag value", code)
	}
	out.Reset()
	errb.Reset()
	t.Setenv("NOVA_TEST_TS_KEY_EMPTY", "")
	if code := run([]string{"fleet", "join", "--benches", benches, "--bench", "bench1",
		"--tailscale", "/usr/local/bin/tailscale", "--authkey-env", "NOVA_TEST_TS_KEY_EMPTY"},
		&out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("empty key exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "nova-secrets exec") {
		t.Fatalf("the refusal does not name the remedy:\n%s", errb.String())
	}
}

// help carries the four verbs, and an unknown sub-verb names them.
func TestFleetHelpAndUnknownSubVerbNameTheFour(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	for _, want := range []string{"fleet   standard", "fleet   mirror", "fleet   join", "fleet   sleep"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not carry %q", want)
		}
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"fleet", "nosuch"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("unknown sub-verb exit = %d, want 2", code)
	}
	for _, want := range []string{"standard", "mirror", "join", "sleep"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the unknown sub-verb line does not name %q:\n%s", want, errb.String())
		}
	}
}
