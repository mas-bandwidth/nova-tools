package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fixture is built at test time and is a valid key for nothing. It is the string the
// assertions search the output for: a shim that printed it would have failed.
const shimFixtureValue = "sk-" + "notarealkey" + "0123456789abcdef"

// TestTheCardsShellNeverSeesASecret is the red team's probe, in Go: a shell started
// through the wrapper reports a length of 0 for a secret-named variable and a count of 0
// for every secret name, where the same shell started directly reports 35 and 1
// (nova-tools #1814). The control is one edit: drop pathWithShimFirst/SHELL from
// nativeChildEnv, or exec the real shell instead of the wrapper, and this goes red.
func TestTheCardsShellNeverSeesASecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shim is a /bin/sh script; windows writes none")
	}
	slot := t.TempDir()
	dir, shell, err := writeNativeShellShims(slot)
	if err != nil {
		t.Fatalf("the shims could not be written: %v", err)
	}
	if dir != nativeShellShimDir(slot) {
		t.Fatalf("shim dir = %q, want %q", dir, nativeShellShimDir(slot))
	}
	if filepath.Base(shell) != "bash" && filepath.Base(shell) != "sh" {
		t.Fatalf("SHELL would be pinned at %q, which names no wrapper", shell)
	}

	probe := `echo envlen=${#DEEPSEEK_API_KEY}; echo envnames=$(printenv | grep -c -E "KEY|TOKEN|SECRET")`
	env := append(os.Environ(),
		"DEEPSEEK_API_KEY="+shimFixtureValue,
		"SOME_TOKEN="+shimFixtureValue,
		"lower_case_secret="+shimFixtureValue,
		"MiXeD_KeY_NaMe="+shimFixtureValue,
	)

	// Red: the real shell, the environment as the harness holds it.
	real, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}
	before := runProbe(t, real, probe, env)
	if !strings.Contains(before, "envlen="+itoa(len(shimFixtureValue))) {
		t.Fatalf("the control did not see the key: %q", before)
	}
	if strings.Contains(before, "envnames=0") {
		t.Fatalf("the control counted no secret names, so this test proves nothing: %q", before)
	}

	// Green: the same probe through the wrapper.
	after := runProbe(t, filepath.Join(dir, "sh"), probe, env)
	if !strings.Contains(after, "envlen=0") {
		t.Fatalf("the wrapped shell still holds the key's length: %q", after)
	}
	if !strings.Contains(after, "envnames=0") {
		t.Fatalf("the wrapped shell still carries a secret name: %q", after)
	}
	if strings.Contains(after, shimFixtureValue) {
		t.Fatalf("the wrapper printed the fixture value")
	}
}

// TestTheShimNeverPrintsAValue reads the wrapper's own text: no value, of the fixture or
// of anything else, may appear in it, and the awk it runs prints names only.
func TestTheShimNeverPrintsAValue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shim is a /bin/sh script; windows writes none")
	}
	slot := t.TempDir()
	dir, _, err := writeNativeShellShims(slot)
	if err != nil {
		t.Fatalf("the shims could not be written: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sh"))
	if err != nil {
		t.Fatalf("the wrapper could not be read: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "$2") || strings.Contains(text, "print $0") {
		t.Fatalf("the wrapper's awk prints more than a name:\n%s", text)
	}
	if !strings.Contains(text, "command -v awk") {
		t.Fatalf("the wrapper does not fail closed when awk is absent:\n%s", text)
	}
	if !strings.Contains(text, "exit 127") {
		t.Fatalf("the wrapper does not refuse when it cannot list the names:\n%s", text)
	}
}

// TestTheShimIsInTheWallsReadSetAndNotItsWriteSet pins where the wrappers live: under the
// slot, which nativeSandboxArgv passes as --read, and never under the job directory or the
// data home, which it passes as --write and the card can rewrite.
func TestTheShimIsInTheWallsReadSetAndNotItsWriteSet(t *testing.T) {
	cfg := nativeRunConfig{slotDir: filepath.Join("root", "slot"), root: "root", label: "card"}
	dir := nativeShellShimDir(cfg.slotDir)
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	dataHome := filepath.Join(cfg.slotDir, "data")
	if !within(cfg.slotDir, dir) {
		t.Fatalf("the shim %q is outside the slot %q, which is the wall's read set", dir, cfg.slotDir)
	}
	if within(jobDir, dir) || within(dataHome, dir) {
		t.Fatalf("the shim %q sits inside a directory the card may write", dir)
	}
}

// TestTheChildEnvPutsTheShimFirstAndPinsShell asserts the two names the harness resolves
// its tool shell through.
func TestTheChildEnvPutsTheShimFirstAndPinsShell(t *testing.T) {
	shim := filepath.Join("slot", "shim")
	shell := filepath.Join(shim, "bash")
	env := nativeChildEnv("data", "job", "tmp", "", "", shim, shell)
	path, ok := lookup(env, "PATH")
	if !ok {
		t.Fatal("the child was handed no PATH")
	}
	if first := strings.Split(path, string(os.PathListSeparator))[0]; first != shim {
		t.Fatalf("PATH starts with %q, want the shim %q", first, shim)
	}
	got, ok := lookup(env, "SHELL")
	if !ok || got != shell {
		t.Fatalf("SHELL = %q (set=%v), want %q", got, ok, shell)
	}
	// Exactly once: a second SHELL would let the harness read either.
	n := 0
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == "SHELL" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("SHELL appears %d times, want 1", n)
	}
}

// TestTheChildEnvIsUnchangedWithoutAShim keeps the windows path and the argv-builder unit
// tests exactly as they were: no shim, no PATH edit, no SHELL.
func TestTheChildEnvIsUnchangedWithoutAShim(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	env := nativeChildEnv("data", "job", "tmp", "", "", "", "")
	if path, _ := lookup(env, "PATH"); path != "/usr/bin" {
		t.Fatalf("PATH = %q, want the caller's own", path)
	}
	if _, ok := lookup(env, "SHELL"); ok {
		t.Fatalf("SHELL was set with no shim to point it at")
	}
}

func lookup(env []string, want string) (string, bool) {
	for _, kv := range env {
		if name, val, _ := strings.Cut(kv, "="); name == want {
			return val, true
		}
	}
	return "", false
}

func runProbe(t *testing.T, shell, script string, env []string) string {
	t.Helper()
	cmd := exec.Command(shell, "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", shell, err, out)
	}
	return string(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
