package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// harnessRig is a fake machine for the harnesses check: a fake exec answering
// `nova-config friend list --json` and each harness's `--version`, a filesystem
// rooted in t.TempDir() holding the harness binaries and the friends' config
// directories, and an injected clock. Nothing real runs and no socket is opened.
type harnessRig struct {
	friends  []map[string]any  // the friend rows `nova-config friend list --json` answers
	binaries map[string]string // harness name -> its `--version` line; absent is not on PATH
	unsup    map[string]bool   // harness name -> answer a line that is no version
	noExec   bool              // nova-config does not answer at all
	execErr  error
}

// ranHarnesses is what one check run was asked to run, so a test can prove the
// check never runs a harness against a model.
type ranHarnesses struct {
	cmds [][]string
}

func friendListJSON(rows ...map[string]any) string {
	type item struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	}
	items := make([]item, 0, len(rows))
	for _, r := range rows {
		items = append(items, item{Kind: "friend", Fields: r})
	}
	b, err := json.Marshal(map[string]any{
		"result": map[string]any{"verb": "friend list", "status": "ok", "exit": 0},
		"facts":  map[string]any{"kind": "friend", "rows": len(rows)},
		"items":  items,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func friendRow(name, mode, configDir string) map[string]any {
	m := map[string]any{"name": name, "mode": mode, "config_dir": configDir, "slots": 4, "tiers": "pro"}
	return m
}

// runHarnessCheck runs only the harness check over the rig.
func runHarnessCheck(t *testing.T, r harnessRig) Result {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for name := range r.binaries {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755))
	}
	for name := range r.unsup {
		if _, ok := r.binaries[name]; ok {
			continue
		}
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755))
	}
	// the friends' config directories: a config_dir of "present" is a directory
	// made under the rig root (the fake Env's filesystem is rooted there), so a
	// test names a missing dir by leaving it out.
	for _, f := range r.friends {
		if d, _ := f["config_dir"].(string); d == "present" {
			f["config_dir"] = filepath.Join("accounts", f["name"].(string))
			require.NoError(t, os.MkdirAll(filepath.Join(root, f["config_dir"].(string)), 0o755))
		}
	}
	ran := &ranHarnesses{}
	fe := fakeEnv{
		env:   map[string]string{"PATH": "bin"},
		root:  root,
		clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
	}
	fe.exec = func(name string, args ...string) (string, error) {
		ran.cmds = append(ran.cmds, append([]string{name}, args...))
		if filepath.Base(name) == "nova-config" {
			if r.noExec {
				return "", errors.New("nova-config: not found")
			}
			if r.execErr != nil {
				return "", r.execErr
			}
			return friendListJSON(r.friends...), nil
		}
		kind := filepath.Base(name)
		if v, ok := r.binaries[kind]; ok {
			if r.unsup[kind] {
				return "not a version line\n", nil
			}
			return v + "\n", nil
		}
		return "", errors.New("no such harness")
	}
	reg := NewRegistry()
	reg.Register(Default.checks["harness"])
	res, _, err := reg.Run(context.Background(), fe, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	// the check never runs a harness against a model: only --version is run.
	for _, c := range ran.cmds {
		if filepath.Base(c[0]) == "nova-config" {
			continue
		}
		require.Equal(t, []string{filepath.Base(c[0]), "--version"}, []string{filepath.Base(c[0]), c[1]}, "a harness is only asked its version")
	}
	return res[0]
}

// TestDoctorHarnessCheckNamesAFriendWhoseHarnessIsMissing pins the harnesses
// check: for each friend row on this machine it names the friend whose harness
// binary is not on PATH or answers no version, and whose claude config
// directory is not there; a friend whose harness and config directory are right
// passes (docs/SETUP.md, dep-harnesses-b.w2). The harness is never run against a
// model.
func TestDoctorHarnessCheckNamesAFriendWhoseHarnessIsMissing(t *testing.T) {
	t.Parallel()

	t.Run("a one-shot claude friend with no claude binary is a fail naming her", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{
			friends:  []map[string]any{friendRow("amy", "one-shot", "present")},
			binaries: map[string]string{},
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "amy")
		assert.Contains(t, r.Evidence, "claude")
		assert.Contains(t, r.Evidence, "not on PATH")
		assert.Contains(t, r.Fix, "nova-update apply --file <manifest>")
	})

	t.Run("a claude binary answering no version is a fail naming her", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{
			friends:  []map[string]any{friendRow("amy", "one-shot", "present")},
			binaries: map[string]string{"claude": "not a version line"},
			unsup:    map[string]bool{"claude": true},
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "amy")
		assert.Contains(t, r.Evidence, "claude")
		assert.Contains(t, r.Evidence, "no version")
	})

	t.Run("a claude friend whose config directory is not there is a fail naming her", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{
			friends:  []map[string]any{friendRow("amy", "one-shot", "accounts/amy-not-there")},
			binaries: map[string]string{"claude": "2.1.220 (Claude Code)"},
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "amy")
		assert.Contains(t, r.Evidence, "config directory")
		assert.Contains(t, r.Evidence, "accounts/amy-not-there")
		assert.Contains(t, r.Fix, "nova-config friend set amy --config_dir")
	})

	t.Run("a claude friend whose harness and config directory are right is ok", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{
			friends:  []map[string]any{friendRow("amy", "one-shot", "present")},
			binaries: map[string]string{"claude": "2.1.220 (Claude Code)"},
		})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "amy")
	})

	t.Run("a batch friend runs opencode and is ok when it answers", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{
			friends:  []map[string]any{friendRow("bob", "batch", "")},
			binaries: map[string]string{"opencode": "0.6.1"},
		})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "opencode")
	})

	t.Run("a friend whose row names codex is checked for codex", func(t *testing.T) {
		t.Parallel()
		row := friendRow("cy", "one-shot", "-")
		row["harness"] = "codex"
		row["config_dir"] = ""
		r := runHarnessCheck(t, harnessRig{friends: []map[string]any{row}, binaries: map[string]string{}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "cy")
		assert.Contains(t, r.Evidence, "codex")
	})

	t.Run("no friend names a harness is ok", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{friends: []map[string]any{}, binaries: map[string]string{}})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "no friend")
	})

	t.Run("a friend list that cannot be read is a fail", func(t *testing.T) {
		t.Parallel()
		r := runHarnessCheck(t, harnessRig{noExec: true})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "friend")
		assert.NotEmpty(t, r.Fix)
	})
}
