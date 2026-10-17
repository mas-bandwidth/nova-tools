package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cliTwin runs this test binary as the CLI (reexec_test.go) on the twin file, each
// line one verb as the coordinator boss, and fails the test on any refusal.
func cliTwin(t *testing.T, exe, twinFile string, lines ...[]string) {
	t.Helper()
	for _, words := range lines {
		cmd := exec.Command(exe, append(words, "--redis", "mem:"+twinFile, "--actor", "boss")...)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v: %s", words, out)
	}
}

// TestServerSwitchRunsAShadowTickAndRefusesABrokenBinary: server switch runs the
// candidate's tick --shadow against the store before the swap (docs/SPEC-SPRINT.md
// section 14, install-canary-shadow-tick-r.w1). On the twin store a working candidate
// (this binary) plans the tick, writes nothing, and is switched in with its plan size
// and time recorded beside the switch record; a candidate whose shadow tick exits
// non-zero, panics or misses the tick deadline is refused, and the old server's
// binary is left as it was.
func TestServerSwitchRunsAShadowTickAndRefusesABrokenBinary(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	twinFile := filepath.Join(dir, "sprint.twin")
	cliTwin(t, exe, twinFile,
		[]string{"init", "--readers", "reader-a", "--members", "m1"},
		[]string{"add", "--stream", "s1", "--count", "1", "--one"},
		[]string{"start"})
	twinBefore, err := os.ReadFile(twinFile)
	require.NoError(t, err)

	t.Run("a broken binary is refused and the old server keeps running", func(t *testing.T) {
		cases := []struct {
			name, script, extra, why string
		}{
			{"errors", "#!/bin/sh\necho 'tick: the store refused' >&2\nexit 2\n", "", "exited 2: tick: the store refused"},
			{"panics", "#!/bin/sh\necho 'panic: runtime error: index out of range [3] with length 3' >&2\necho 'goroutine 1 [running]:' >&2\nexit 2\n", "", "panicked: panic: runtime error: index out of range [3]"},
			{"misses the deadline", "#!/bin/sh\nexec sleep 60\n", " --tick-deadline 300ms", "missed the tick deadline 300ms"},
			{"prints no plan", "#!/bin/sh\necho 'TICK OK'\n", "", "exited 0 and printed no plan"},
		}
		for _, tc := range cases {
			sub := t.TempDir()
			target := filepath.Join(sub, "nova-sprint")
			candidate := filepath.Join(sub, "candidate")
			require.NoError(t, os.WriteFile(target, []byte("the running server"), 0o755))
			require.NoError(t, testbin.WriteExecutable(candidate, []byte(tc.script), 0o755))
			ta := newTestApp(t)
			code, out, errs := ta.do("server switch " + candidate + " --target " + target + " --redis mem:" + twinFile + " --rollback" + tc.extra)
			assert.Equal(t, 1, code, tc.name)
			assert.Contains(t, errs, "server switch REFUSED: the shadow tick of "+candidate, tc.name)
			assert.Contains(t, errs, tc.why, tc.name)
			assert.Contains(t, errs, "the old server keeps running", tc.name)
			assert.Contains(t, errs, "remedy: verify the candidate binary with "+candidate+" tick --shadow before switching; run: nova-sprint server switch -h", tc.name)
			assert.NotContains(t, out, "SERVER SWITCH OK", tc.name)
			kept, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, "the running server", string(kept), "%s: the old binary is in place", tc.name)
			for _, side := range []string{".prev", ".switch.json", ".shadow.json"} {
				_, err := os.Stat(target + side)
				assert.True(t, os.IsNotExist(err), "%s: nothing written beside the target (%s)", tc.name, side)
			}
		}
	})

	t.Run("a working binary plans, writes nothing, and is switched in", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "nova-sprint")
		require.NoError(t, os.WriteFile(target, []byte("the running server"), 0o755))
		ta := newTestApp(t)
		code, out, errs := ta.do("server switch " + exe + " --target " + target + " --redis mem:" + twinFile + " --rollback --dry-run")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "SHADOW TICK OK binary="+exe)
		assert.Contains(t, out, "SERVER SWITCH DRY-RUN target="+target+" binary="+exe)
		for _, side := range []string{".prev", ".switch.json", ".shadow.json"} {
			_, err := os.Stat(target + side)
			assert.True(t, os.IsNotExist(err), "a dry run writes nothing beside the target (%s)", side)
		}

		code, out, errs = ta.do("server switch " + exe + " --target " + target + " --redis mem:" + twinFile + " --rollback")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "SHADOW TICK OK binary="+exe+" epoch=0 state=RUNNING")
		assert.Contains(t, out, "SERVER SWITCH OK")
		twinAfter, err := os.ReadFile(twinFile)
		require.NoError(t, err)
		assert.Equal(t, string(twinBefore), string(twinAfter), "the shadow tick wrote nothing to the store")

		b, err := os.ReadFile(target + ".shadow.json")
		require.NoError(t, err)
		var rec shadowRecord
		require.NoError(t, json.Unmarshal(b, &rec))
		assert.Equal(t, exe, rec.Binary)
		assert.Positive(t, rec.Plan.Size, "the plan's size is recorded: the tick would deal the ready card")
		assert.Positive(t, rec.Wall, "the shadow's time is recorded")
		_, err = os.Stat(target + ".switch.json")
		assert.NoError(t, err, "the shadow record sits beside the switch record")
		prev, err := os.ReadFile(target + ".prev")
		require.NoError(t, err)
		assert.Equal(t, "the running server", string(prev))

		// the tick by hand writes what the shadow only planned
		cliTwin(t, exe, twinFile, []string{"tick"})
		ticked, err := os.ReadFile(twinFile)
		require.NoError(t, err)
		assert.NotEqual(t, string(twinBefore), string(ticked))
	})
}
