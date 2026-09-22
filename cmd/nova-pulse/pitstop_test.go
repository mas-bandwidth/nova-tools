package main

// The pitstop verb's CLI dispatcher tests. The library tests prove the library; these prove
// the CLI wires the right flags into the library and that the help carries the verb.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// pitstop set --state pause --except harvest -> check launch exits 3 and check harvest exits 0.
// This is the CLI shape, against a miniredis the seam falls through to. The CLI does not
// dial directly: it hands the address to the library, the library dials. Here we just point
// --store at the miniredis and read the exit codes the verb returns.
func TestPitstopCLISetPauseThenCheckViaTheStore(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pitstop", "set", "--state", "pause", "--except", "harvest", "--store", addr}, &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Fatalf("set pause --except harvest exit = %d, stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"pitstop", "check", "launch", "--store", addr}, &stdout, &stderr, time.Now().UTC()); code != 3 {
		t.Errorf("check launch exit = %d, want 3 (paused)", code)
	}
	stdout.Reset()
	if code := run([]string{"pitstop", "check", "harvest", "--store", addr}, &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Errorf("check harvest exit = %d, want 0 (open)", code)
	}
}

// unknown subcommand on the CLI is a refusal that exits 2 and names the two.
func TestPitstopCLIUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pitstop", "stop"}, &stdout, &stderr, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "set or check") {
		t.Errorf("refusal = %q, want it to name set or check", stderr.String())
	}
}

// check without a verb is a refusal that names what is missing.
func TestPitstopCLICheckWithoutVerb(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pitstop", "check", "--store", "127.0.0.1:6379"}, &stdout, &stderr, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "verb") {
		t.Errorf("refusal = %q, want it to name the verb", stderr.String())
	}
}

// set without --state is a refusal that names what is missing.
func TestPitstopCLISetWithoutState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pitstop", "set", "--store", "127.0.0.1:6379"}, &stdout, &stderr, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--state") {
		t.Errorf("refusal = %q, want it to name --state", stderr.String())
	}
}

// set with a positional is a refusal that names the shape.
func TestPitstopCLISetRefusesPositional(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pitstop", "set", "launch", "--state", "pause", "--store", "127.0.0.1:6379"}, &stdout, &stderr, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "positional") {
		t.Errorf("refusal = %q, want it to name positionals", stderr.String())
	}
}

// help carries the pitstop verb line and is not marked unimplemented.
func TestHelpCarriesPitstop(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	for _, want := range []string{"nova-pulse pitstop set", "nova-pulse pitstop check"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not carry %q", want)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "nova-pulse" && f[1] == "pitstop" {
			if strings.Contains(line, "not yet implemented") {
				t.Errorf("a shipped verb is marked unimplemented: %q", line)
			}
		}
	}
}
