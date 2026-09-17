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

// SPEC-PULSE ## Fleet / issue #880 item 16: `nova-pulse fleet secrets` runs the seat
// check on every bench and prints one FLEET <name> line per bench with the seat name
// and the store head. The ssh child is fakeable (--ssh, default ssh), so the tests put
// a fake ssh on PATH that runs the piped script through bash -s under a fake HOME; no
// test reaches a machine and no test reads a value.

const fleetSecretsOKScript = "#!/bin/sh\n" +
	"echo 'SECRETS CHECK OK  as=rowan recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=abcdef1234567890'\n" +
	"exit 0\n"

const fleetSecretsRefusedScript = "#!/bin/sh\n" +
	"echo 'SECRETS CHECK FAIL rowan.yaml: recipients differ from .sops.yaml; run: sops updatekeys rowan.yaml' >&2\n" +
	"exit 1\n"

// fleetSecretsEnv builds a fake ssh that runs the piped remote script in bash, a fake
// nova-secrets whose answer `script` is, a bench home with one *.key per seat named in
// `seats`, and a one-line benches file naming that home.
func fleetSecretsEnv(t *testing.T, seats []string, script string) (benchesFile, home string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fleet is a linux bench; the fake ssh runs the remote script through bash -s")
	}
	bin := t.TempDir()
	writeBenchExe(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nexec bash -s\n")
	writeBenchExe(t, filepath.Join(bin, "nova-secrets"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "nova-bench", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	seatDir := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(seatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, s := range seats {
		if err := os.WriteFile(filepath.Join(seatDir, s+".key"), []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	benchesFile = filepath.Join(t.TempDir(), "benches.tsv")
	if err := os.WriteFile(benchesFile, []byte("bench1\tbench1\t"+home+"\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return benchesFile, home
}

// The seat check passes and the line carries the seat and the store head, in one
// FLEET <name> line.
func TestFleetSecretsSeatCheckOK(t *testing.T) {
	benches, _ := fleetSecretsEnv(t, []string{"rowan"}, fleetSecretsOKScript)
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "secrets", "--benches", benches}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fleet secrets exit = %d, want 0; stderr=%q", code, errb.String())
	}
	got := strings.TrimSpace(out.String())
	want := "FLEET bench1 SEAT rowan check=OK head=abcdef12 names=1"
	if got != want {
		t.Fatalf("fleet secrets line = %q, want %q", got, want)
	}
}

// Two key files mean no seat was chosen: NO-SEAT with the count, exit 2.
func TestFleetSecretsTwoKeysNoSeat(t *testing.T) {
	benches, _ := fleetSecretsEnv(t, []string{"rowan", "keeper"}, fleetSecretsOKScript)
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "secrets", "--benches", benches}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("fleet secrets two keys exit = %d, want 2; stderr=%q", code, errb.String())
	}
	got := strings.TrimSpace(out.String())
	want := "FLEET bench1 NO-SEAT (found 2 keys)"
	if got != want {
		t.Fatalf("fleet secrets line = %q, want %q", got, want)
	}
}

// A refused seat check exits 2 and the line names the reason.
func TestFleetSecretsRefused(t *testing.T) {
	benches, _ := fleetSecretsEnv(t, []string{"rowan"}, fleetSecretsRefusedScript)
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "secrets", "--benches", benches}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("fleet secrets refused exit = %d, want 2; stderr=%q", code, errb.String())
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "FLEET bench1 SEAT rowan check=REFUSED ") {
		t.Fatalf("fleet secrets refused line = %q, want it to name the seat and the refusal", got)
	}
	if !strings.Contains(got, "recipients differ from .sops.yaml") {
		t.Fatalf("fleet secrets refused line does not name the reason: %q", got)
	}
}
