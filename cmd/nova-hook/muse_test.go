package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMuse writes a muse CLI stand-in that records each call's arguments, one
// line per call, and fails when failOn names the subcommand to refuse.
func fakeMuse(t *testing.T, failOn string) (binDir, logPath string) {
	t.Helper()
	binDir = t.TempDir()
	logPath = filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + logPath + "\"\n" +
		"if [ \"$2\" = \"" + failOn + "\" ]; then echo refused >&2; exit 1; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "muse"), []byte(script), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return binDir, logPath
}

func museBundleDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	bundle := filepath.Join(wd, "muse-bundle")
	_, err = os.Stat(filepath.Join(bundle, ".muse-plugin", "plugin.json"))
	require.NoError(t, err)
	return bundle
}

// TestMuseHarnessInstallsApprovesAndArms runs the install/approve/wake path
// against a fake muse CLI on PATH: the exact commands issued are the install
// of the bundle and the approve of its one hook capability, printed before
// the hook arms; wake_delay is stated, not stored.
func TestMuseHarnessInstallsApprovesAndArms(t *testing.T) {
	_, logPath := fakeMuse(t, "")
	bundle := museBundleDir(t)
	r := newRig(t)
	res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "Muse", "--muse-bundle", bundle, "--once")
	assert.Equal(t, 1, res.Code, res.Stdout+res.Stderr) // armed and live, not proven: the model still proves
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "HOOK STATE state=muse-installed plugin=nova-hook capability=nova-hook:hook:pending-challenges")
	assert.Contains(t, out, "wake_delay_ms=0 on each Monitor call")
	calls, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "plugins install "+bundle+"\nplugins approve nova-hook:hook:pending-challenges\n", string(calls))
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
}

// TestMuseInstallFailureRefusesBeforeArming pins that a Muse CLI refusal
// stops the hook before any record or lease is written.
func TestMuseInstallFailureRefusesBeforeArming(t *testing.T) {
	_, logPath := fakeMuse(t, "install")
	bundle := museBundleDir(t)
	r := newRig(t)
	res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "Muse", "--muse-bundle", bundle, "--once")
	assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	calls, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "plugins install "+bundle+"\n", string(calls), "approve never runs after a failed install")
}

// TestMuseHarnessRefusals pins the up-front refusals, before any CLI call.
func TestMuseHarnessRefusals(t *testing.T) {
	_, logPath := fakeMuse(t, "")
	r := newRig(t)
	cli := r.cli()
	res := cli.Run("hook", "--as", "ada", "--role", "comms", "--harness", "codex", "--once")
	assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
	res = cli.Run("hook", "--as", "ada", "--role", "comms", "--harness", "Muse", "--muse-bundle", filepath.Join(t.TempDir(), "nope"), "--once")
	assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "has no .muse-plugin/plugin.json")
	_, err := os.Stat(logPath)
	assert.True(t, os.IsNotExist(err), "no Muse CLI call before validation passes")
}

// TestPendingScriptPrintsChallengesAndAlwaysExitsZero runs the bundle's hook
// script against a fake nova-bus on PATH: pending challenge lines print one
// per line, stdin is discarded, and a missing or failing bus still exits 0.
func TestPendingScriptPrintsChallengesAndAlwaysExitsZero(t *testing.T) {
	binDir := t.TempDir()
	busScript := "#!/bin/sh\n" +
		"printf '%s\\n' 'PEEK OK pending=1 new=1' 'PEEK MESSAGE state=pending id=1-0 from=bob subject=\"HOOK CHALLENGE nonce=abc123\"' 'PEEK MESSAGE state=new id=2-0 from=bob subject=\"hello\"'\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "nova-bus"), []byte(busScript), 0o755))

	wd, err := os.Getwd()
	require.NoError(t, err)
	script := filepath.Join(wd, "muse-bundle", "hooks", "nova-hook-pending.sh")

	run := func(env ...string) (string, error) {
		cmd := exec.Command("sh", script)
		cmd.Stdin = strings.NewReader(`{"tool":"x"}`)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("NOVA_BUS_BIN="+filepath.Join(binDir, "nova-bus"), "NOVA_HOOK_AS=ada")
	require.NoError(t, err, out)
	assert.Contains(t, out, "PEEK MESSAGE state=pending id=1-0 from=bob subject=\"HOOK CHALLENGE nonce=abc123\"")
	assert.NotContains(t, out, "PEEK OK")

	out, err = run("NOVA_BUS_BIN=" + filepath.Join(binDir, "missing-bus"))
	require.NoError(t, err, out)
	assert.Empty(t, strings.TrimSpace(out))
}
