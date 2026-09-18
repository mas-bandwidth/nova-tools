package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestFleetPlanReportsTheHandEditOverFakeSSH drives the `fleet plan` verb
// against a fake ssh on PATH: the module's declaration is clean, the host file
// matches, and then the file is hand-edited while the declaration is unchanged.
// The line the verb prints is the drift the null_resource trigger cannot see.
func TestFleetPlanReportsTheHandEditOverFakeSSH(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "ssh")
	// argv is `ssh -o BatchMode=yes -o ConnectTimeout=5 <target> sha256sum <path>`:
	// drop the transport options and the target, run the remote command locally.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nshift 5\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	host := t.TempDir()
	remote := filepath.Join(host, "nova-runner-1.service")
	if err := os.WriteFile(remote, []byte("declared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("declared\n"))
	declared := hex.EncodeToString(sum[:])

	module := t.TempDir()
	manifest := `{"runner-1":{"path":` + strconv.Quote(remote) + `,"sha256":"` + declared + `","witness":true,"content":"declared\n"}}`
	if err := os.WriteFile(filepath.Join(module, "files.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"fleet", "plan", "--module", module, "--bench", "space"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("clean plan exit = %d, want 0; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FLEET space PLAN CLEAN") {
		t.Fatalf("clean plan printed %q, want FLEET space PLAN CLEAN", out.String())
	}

	if err := os.WriteFile(remote, []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code = run([]string{"fleet", "plan", "--module", module, "--bench", "space"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("drift plan exit = %d, want 2; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FLEET space DRIFT runner-1") {
		t.Fatalf("drift plan printed %q, want FLEET space DRIFT runner-1", out.String())
	}
}

// TestFleetPlanRefusesWithoutAModule: a plan that cannot read the declaration is
// a refusal with one remedy line, exit 2.
func TestFleetPlanRefusesWithoutAModule(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "plan", "--bench", "space"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("fleet plan without --module exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--module is required") {
		t.Fatalf("refusal %q does not name the remedy", errb.String())
	}
}
