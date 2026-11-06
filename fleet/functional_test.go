//go:build functional

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// install from a local build: a directory laid out as a release, copied after each
// file is checked against its SHA256SUMS line; a second run changes nothing, and a
// file that does not match its line is refused.
func TestInstallFromALocalBuild(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the local mode copies to Linux and macOS")
	}
	bin := buildFleet(t)
	dist := t.TempDir()
	asset := "nova-fleet_local_" + runtime.GOOS + "_" + runtime.GOARCH
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, asset), b, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("localhost ansible_connection=local ansible_python_interpreter=auto_silent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	args := []string{"install", "--inventory", inv, "--", "-e", "nova_tools_version=local", "-e", "nova_tools_local_dir=" + dist,
		"-e", "nova_bin_dir=" + binDir, "-e", `{"nova_tools_names":["nova-fleet"]}`}
	if code, out, errs := runFleet(t, bin, args...); code != 0 {
		t.Fatalf("install from %s: exit %d\n%s\n%s", dist, code, out, errs)
	}
	got, err := os.ReadFile(filepath.Join(binDir, "nova-fleet"))
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("the installed nova-fleet is not the local build: %v", err)
	}
	code, out, _ := runFleet(t, bin, append([]string{"install", "--check"}, args[1:]...)...)
	if code != 0 || !strings.Contains(out, "changed=0") {
		t.Errorf("a second run under --check: exit %d, want changed=0:\n%s", code, out)
	}
	if err := os.WriteFile(filepath.Join(dist, asset), append(b, 0), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runFleet(t, bin, args...)
	if code == 0 || !strings.Contains(out, "does not match its SHA256SUMS line") {
		t.Errorf("a file that does not match its line: exit %d, want the refusal:\n%s", code, out)
	}
}

// github-runners reads its token under --check too, and a read that fails says why.
func TestRunnerTokenReadFailsWithItsReason(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	bin := buildFleet(t)
	secrets := filepath.Join(t.TempDir(), "nova-secrets")
	if out, err := exec.Command("go", "build", "-o", secrets, "../secrets").CombinedOutput(); err != nil {
		t.Fatalf("go build ../secrets: %v: %s", err, out)
	}
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("[runner]\nlocalhost ansible_connection=local ansible_python_interpreter=auto_silent runners=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runFleet(t, bin, "github-runners", "--check", "--inventory", inv, "--", "-e", "github_runner_url=https://github.com/o/r",
		"-e", "github_runner_scope=repo", "-e", "github_runner_secrets_bin="+secrets, "-e", "github_runner_base_dir="+t.TempDir())
	if code == 0 || !strings.Contains(out, "GITHUB_RUNNER_TOKEN cannot be read on the control node") || strings.Contains(out, "censored") {
		t.Fatalf("--check with no store: exit %d, want the token refusal with its reason:\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "store") {
		t.Errorf("the refusal does not carry nova-secrets' reason:\n%s", out)
	}
}
