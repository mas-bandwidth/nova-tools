package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seed of 2026-10-02 (tools/notes-2026-10-02.sh): the reasons the first
// real sprint disabled eight routes, and why superman is held, written as
// notes through nova-config itself. Here the script runs against a --file
// store with this test binary standing in for nova-config, so every flag it
// passes is the real grammar's and every refusal the real one.

// TestNovaConfigAsAProcessForTheScripts is the stand-in nova-config: the
// scripts below call the test binary, which runs one nova-config command
// from the arguments after "--" and exits with its code. Run as an ordinary
// test it does nothing.
func TestNovaConfigAsAProcessForTheScripts(t *testing.T) {
	if os.Getenv("NOVA_CONFIG_AS_PROCESS") != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Exit(run(os.Args[i+1:], os.Stdout, os.Stderr, realDeps()))
		}
	}
	os.Exit(2)
}

// seedScript runs the script in dir against try.json there, as the process
// stand-in, and returns its output and exit code.
func seedScript(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(dir, "nova-config")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \""+self+"\" -test.run='^TestNovaConfigAsAProcessForTheScripts$' -- \"$@\"\n"), 0o755))
	script, err := filepath.Abs(filepath.Join("..", "..", "tools", "notes-2026-10-02.sh"))
	require.NoError(t, err)
	cmd := exec.Command("/bin/bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "NOVA_CONFIG=" + wrapper, "NOTES_CONN=--file try.json", "NOVA_CONFIG_AS_PROCESS=1"}
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	require.NoError(t, err, string(out))
	return string(out), 0
}

func TestTheSeedScriptWritesTheDaysReasonsAsNotesAndTouchesNothingElse(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()
	h.env["NOVA_FRIEND"] = "rowan"
	disabled := []string{"flash-mimo26pro-openrouter", "flash-luna6-opencode", "flash-luna6-openrouter", "flash-mercury-openrouter",
		"flash-nemotron-openrouter", "flash-gemini31lite-openrouter", "flash-mimo26-openrouter"} // flash-luna56-opencode is no row; flash-mercury is on
	for _, args := range [][]string{
		{"migrate", "--file", "try.json"},
		{"machine", "add", "superman", "--user", "u", "--seat", "s", "--slots", "8", "--width", "8", "--file", "try.json"},
	} {
		code, _, errs := h.run(t, args...)
		require.Equal(t, 0, code, errs)
	}
	for _, name := range append([]string{"flash-mercury"}, disabled...) {
		code, _, errs := h.run(t, "route", "add", name, "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--file", "try.json")
		require.Equal(t, 0, code, errs)
	}
	for _, name := range disabled[:6] { // flash-mimo26-openrouter stays on: the script skips a route that is not disabled
		code, _, errs := h.run(t, "route", "set", name, "--enabled", "false", "--note", "by hand", "--file", "try.json")
		require.Equal(t, 0, code, errs)
	}

	// a dry run writes nothing
	out, code := seedScript(t, h.dir, "--dry-run")
	require.Equal(t, 1, code, out)
	assert.Equal(t, 6, strings.Count(out, "CONFIG DRY-RUN op=set kind=route"), "six routes: %s", out)
	assert.Equal(t, 1, strings.Count(out, "CONFIG DRY-RUN op=set kind=machine name=superman"), "and the machine: %s", out)
	code, shown, errs := h.run(t, "route", "show", "flash-luna6-opencode", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, shown, " note=by\\x20hand ", "the dry run wrote nothing")

	out, code = seedScript(t, h.dir)
	assert.Equal(t, 1, code, "one route is no row, so the script ends 1 after writing the rest: %s", out)
	assert.Contains(t, out, "NOTES MISSING route=flash-luna56-opencode")
	assert.Contains(t, out, "NOTES SKIP route=flash-mimo26-openrouter reason=enabled")
	assert.Contains(t, out, "NOTES DONE routes_written=6 routes_skipped=1 routes_missing=1 machine=superman dry_run=false")
	for name, want := range map[string][]string{
		"flash-luna6-opencode":          {"2\\x20ok\\x20of\\x2012"},
		"flash-luna6-openrouter":        {"4\\x20ok\\x20of\\x2014"},
		"flash-mimo26pro-openrouter":    {"3\\x20ok\\x20of\\x207", "1200\\x20s\\x20deadline"},
		"flash-nemotron-openrouter":     {"4\\x20ok\\x20of\\x2052", "48\\x20ended\\x20with\\x20no\\x20result"},
		"flash-gemini31lite-openrouter": {"34\\x20ok\\x20of\\x2058"},
		"flash-mercury-openrouter":      {"use\\x20mercury\\x20only\\x20direct", "10\\x20ok\\x20of\\x2016"},
	} {
		code, shown, errs := h.run(t, "route", "show", name, "--file", "try.json")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, shown, " enabled=false ", "%s stays disabled: the script enables nothing", name)
		assert.NotContains(t, shown, "by\\x20hand", "%s: the note is replaced", name)
		for _, w := range want {
			assert.Contains(t, shown, w, name)
		}
		assert.Contains(t, shown, "nova-tools#5101", name)
	}
	_, shown, _ = h.run(t, "route", "show", "flash-mimo26-openrouter", "--file", "try.json")
	assert.Contains(t, shown, " enabled=true ")
	assert.Contains(t, shown, " note=- ", "a route that is on got no note")
	_, shown, _ = h.run(t, "machine", "show", "superman", "--file", "try.json")
	assert.Contains(t, shown, "held\\x201:46\\x20PM\\x20ET\\x202026-10-02:\\x20reads\\x20kernel-bound")
	assert.Contains(t, shown, " width=8 ", "the script changes no width")
	_, hist, _ := h.run(t, "route", "history", "flash-luna6-opencode", "--file", "try.json")
	assert.Contains(t, hist, "op=set actor=rowan ", "the history says who wrote the note")
	assert.Equal(t, 3, strings.Count(hist, "HISTORY id="), "the add, the hand disable and the script's note: %s", hist)

	// a refused call is the script's: an unknown argument names itself
	out, code = seedScript(t, h.dir, "--now")
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "REFUSED: unknown argument --now")
}
