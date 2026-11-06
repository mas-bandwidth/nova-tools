//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// functional_test.go builds nova-fleet and runs it against ansible-core on PATH:
// plays, show install, and the install play under --check against a stub
// inventory file of localhost. It is skipped where ansible-playbook is absent.

func buildFleet(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-fleet")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

func runFleet(t *testing.T, bin string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), "ANSIBLE_LOCAL_TEMP="+t.TempDir(), "ANSIBLE_NOCOLOR=1")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, o.String(), e.String()
}

func TestFleetBinaryAgainstAnsible(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	bin := buildFleet(t)

	code, out, errs := runFleet(t, bin, "plays")
	if code != 0 || !strings.Contains(out, "install ") || !strings.Contains(out, "services ") {
		t.Fatalf("plays: exit %d stdout %q stderr %q", code, out, errs)
	}
	want, err := os.ReadFile("install.yml")
	if err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runFleet(t, bin, "show", "install"); code != 0 || out != string(want) {
		t.Fatalf("show install: exit %d, stdout is not install.yml", code)
	}

	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("localhost ansible_connection=local ansible_python_interpreter=auto_silent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runFleet(t, bin, "install", "--check", "--inventory", inv, "--", "-e", "nova_tools_version=v0", "-l", "localhost")
	if !strings.Contains(errs, "nova-fleet: ansible-playbook=") || !strings.Contains(errs, "inventory="+inv) {
		t.Errorf("install: stderr does not echo the ansible-playbook and inventory used: %q", errs)
	}
	if !strings.Contains(out, "PLAY [nova-tools release on the machine]") || !strings.Contains(out, "localhost") {
		t.Fatalf("install --check: ansible-playbook did not run the play on localhost (exit %d):\n%s\n%s", code, out, errs)
	}
	// v0 is a tag no release carries: the role's version check refuses it, and
	// nova-fleet exits with ansible-playbook's status for that (2, a failed host).
	if code != 2 || !strings.Contains(out, "nova-tools-install: name the release") {
		t.Errorf("install --check -e nova_tools_version=v0: exit %d, want ansible-playbook's 2 with the role's refusal:\n%s\n%s", code, out, errs)
	}

	// Every play extracted from the binary passes ansible-playbook --syntax-check.
	for _, p := range plays(tree) {
		if code, out, errs := runFleet(t, bin, p, "--inventory", inv, "--", "--syntax-check"); code != 0 {
			t.Errorf("%s --syntax-check: exit %d\n%s\n%s", p, code, out, errs)
		}
	}
}
