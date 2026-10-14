package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMuse writes a muse CLI stand-in that records each call's arguments, one
// line per call, and fails when failOn names the subcommand to refuse.
func fakeMuse(t *testing.T, failOn string) (binPath, logPath string) {
	t.Helper()
	binDir := t.TempDir()
	binPath = filepath.Join(binDir, "muse")
	logPath = filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + logPath + "\"\n" +
		"if [ \"$2\" = \"" + failOn + "\" ]; then echo refused >&2; exit 1; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))
	return binPath, logPath
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
// against a fake muse CLI: the exact commands issued are the install
// of the bundle and the approve of its one hook capability, printed before
// the hook arms; wake_delay is stated, not stored.
func TestMuseHarnessInstallsApprovesAndArms(t *testing.T) {
	t.Parallel()
	binPath, logPath := fakeMuse(t, "")
	bundle := museBundleDir(t)
	r := newRig(t)
	res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "Muse", "--muse-bundle", bundle, "--muse-bin", binPath, "--once")
	assert.Equal(t, 1, res.Code, res.Stdout+res.Stderr) // armed and live, not proven: the model still proves
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "HOOK STATE state=muse-installed plugin=nova-hook capability=nova-hook:hook:pending-challenges")
	assert.Contains(t, out, `Monitor(command="nova-hook hook --as ada --role comms --harness Muse", wake_delay_ms=0)`)
	assert.Contains(t, out, `Monitor(command="nova-bus recv --as ada --forever --exec '<deliver-into-session>'", wake_delay_ms=0)`)
	assert.Contains(t, out, "wake_delay_ms has no persistent store in Muse, so nothing is stored")
	calls, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "plugins install "+bundle+"\nplugins approve nova-hook:hook:pending-challenges\n", string(calls))
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
}

// TestMuseWakeNoteAndBundleReadmeSpellBothMonitors pins that the wake
// instruction is identical in shape in code and in the bundle README: both
// Monitor commands, each with wake_delay_ms=0, and no vague per-Monitor note.
func TestMuseWakeNoteAndBundleReadmeSpellBothMonitors(t *testing.T) {
	t.Parallel()
	note := museWakeNote("ada", "comms")
	assert.Contains(t, note, `Monitor(command="nova-hook hook --as ada --role comms --harness Muse", wake_delay_ms=0)`)
	assert.Contains(t, note, `Monitor(command="nova-bus recv --as ada --forever --exec '<deliver-into-session>'", wake_delay_ms=0)`)
	assert.Equal(t, 2, strings.Count(note, "Monitor(command="))
	readme, err := os.ReadFile(filepath.Join(museBundleDir(t), "README.md"))
	require.NoError(t, err)
	assert.Contains(t, string(readme), `Monitor(command="nova-hook hook --as <name> --role <role> --harness Muse", wake_delay_ms=0)`)
	assert.Contains(t, string(readme), `Monitor(command="nova-bus recv --as <name> --forever --exec '<deliver-into-session>'", wake_delay_ms=0)`)
	assert.NotContains(t, string(readme), "pass `wake_delay_ms=0` on each Monitor call")
}

// TestMuseInstallFailureRefusesBeforeArming pins that a Muse CLI refusal
// stops the hook before any record or lease is written.
func TestMuseInstallFailureRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	binPath, logPath := fakeMuse(t, "install")
	bundle := museBundleDir(t)
	r := newRig(t)
	res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "Muse", "--muse-bundle", bundle, "--muse-bin", binPath, "--once")
	assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	calls, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "plugins install "+bundle+"\n", string(calls), "approve never runs after a failed install")
}

// TestMuseHarnessRefusals pins the up-front refusals, before any CLI call.
func TestMuseHarnessRefusals(t *testing.T) {
	t.Parallel()
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

// TestPendingPrintsChallengesAndAlwaysExitsZero runs the pending verb
// against a fake nova-bus: pending challenge lines print one per line,
// stdin is discarded, and a missing bus still exits 0 with no output.
func TestPendingPrintsChallengesAndAlwaysExitsZero(t *testing.T) {
	t.Parallel()
	binDir := t.TempDir()
	busPath := filepath.Join(binDir, "nova-bus")
	busScript := "#!/bin/sh\n" +
		"printf '%s\\n' 'PEEK OK pending=1 new=1' 'PEEK MESSAGE state=pending id=1-0 from=bob subject=\"HOOK CHALLENGE nonce=abc123\"' 'PEEK MESSAGE state=new id=2-0 from=bob subject=\"hello\"'\n"
	require.NoError(t, os.WriteFile(busPath, []byte(busScript), 0o755))

	cli := newRig(t).cli()
	res := cli.RunIn(`{"tool":"x"}`, "pending", "--bus", busPath, "--as", "ada")
	assert.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout, "PEEK MESSAGE state=pending id=1-0 from=bob subject=\"HOOK CHALLENGE nonce=abc123\"")
	assert.Contains(t, res.Stdout, "PEEK MESSAGE state=new id=2-0 from=bob subject=\"hello\"")
	assert.NotContains(t, res.Stdout, "PEEK OK")

	res = cli.RunIn(`{"tool":"x"}`, "pending", "--bus", filepath.Join(binDir, "missing-bus"), "--as", "ada")
	assert.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	assert.Empty(t, res.Stdout)
}
