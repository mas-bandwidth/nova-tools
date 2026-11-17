//go:build functional

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The branches a push may run CI on come from push-branches beside the hooks, which apply writes.

func TestCIRulesPushBranchesComeFromTheHookDirectory(t *testing.T) {
	t.Parallel()
	for _, sh := range ciShells(t) {
		dir := ciCopy(t, "")
		if err := os.WriteFile(filepath.Join(dir, "push-branches"), []byte("main release/* hotfix\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for ref, want := range map[string]int{
			"refs/heads/main":         0,
			"refs/heads/release/v2":   0,
			"refs/heads/hotfix":       0,
			"refs/tags/v1.2.3":        0,
			"refs/heads/sprint/v2":    1,
			"refs/heads/release/v2/x": 1,
			"refs/heads/release/":     1,
			"refs/heads/hotfix-2":     1,
		} {
			if got, _ := ciRun(t, sh, dir, "", nil, "push-ok", ref); got != want {
				t.Errorf("%s push-ok %s with push-branches = %d, want %d", sh.name, ref, got, want)
			}
		}
		code, out := ciRun(t, sh, dir, "", []string{"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/sprint/v2", "NOVA_CI_CHAIN_JOB_STARTED="}, "started")
		if code == 0 || !strings.Contains(out, "push CI runs only on main, release/*, hotfix and tags, not refs/heads/sprint/v2") ||
			!strings.Contains(out, "on.push.branches to ['main', 'release/*', 'hotfix']") {
			t.Errorf("%s started on a push outside push-branches = %d %q, want a refusal naming the configured branches", sh.name, code, out)
		}
	}
}

func TestCIRulesApplyWritesThePushBranches(t *testing.T) {
	t.Parallel()
	hooks := ciCopy(t, "")
	runner := t.TempDir()
	sh := ciShells(t)[0]
	code, out := ciRun(t, sh, hooks, "", nil, "apply", "90", "main release/*", runner)
	if code != 0 || !strings.Contains(out, "hooks=set") {
		t.Fatalf("apply = %d %q, want hooks=set", code, out)
	}
	if b, err := os.ReadFile(filepath.Join(runner, ".nova-ci-rules", "push-branches")); err != nil || string(b) != "main release/*\n" {
		t.Errorf("push-branches = %q, %v", b, err)
	}
	if code, out = ciRun(t, sh, hooks, "", nil, "apply", "90", "main release/*", runner); code != 0 || !strings.Contains(out, "hooks=ok") {
		t.Errorf("apply again = %d %q, want hooks=ok", code, out)
	}
	if code, _ = ciRun(t, sh, hooks, "", nil, "apply", "90"); code != 2 {
		t.Errorf("apply without BRANCHES = %d, want 2", code)
	}
}

// apply writes nothing through a link in the runner's directory (a runner login could plant one for root's apply to
// follow): a .nova-ci-rules link is replaced by a directory, a .env link by a file, and what they named is untouched.
func TestCIRulesApplyFollowsNoLinkInTheRunnerDirectory(t *testing.T) {
	t.Parallel()
	hooks := ciCopy(t, "")
	runner := t.TempDir()
	elsewhere := t.TempDir()
	victim := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(runner, ".nova-ci-rules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(runner, ".env")); err != nil {
		t.Fatal(err)
	}
	sh := ciShells(t)[0]
	if code, out := ciRun(t, sh, hooks, "", nil, "apply", "90", "main", runner); code != 0 || !strings.Contains(out, "hooks=set") {
		t.Fatalf("apply = %d %q, want hooks=set", code, out)
	}
	for _, name := range []string{".nova-ci-rules", ".env"} {
		if fi, err := os.Lstat(filepath.Join(runner, name)); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s after apply: %v, %v; want no link", name, fi, err)
		}
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep\n" {
		t.Errorf("the .env link's target = %q, %v; want it untouched", b, err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 1 {
		t.Errorf("the .nova-ci-rules link's target holds %d entries, %v; want only its own file", len(entries), err)
	}
}

func TestCIRulesHoldPausesWorkAndReleasesOnCompleted(t *testing.T) {
	t.Parallel()
	realReserve := filepath.Join(t.TempDir(), "nova-reserve-real")
	buildOut, err := exec.Command("go", "build", "-o", realReserve, "../nova-reserve").CombinedOutput()
	require.NoError(t, err, "build nova-reserve: %s", string(buildOut))

	work := t.TempDir()
	resDir := filepath.Join(work, "reserve")
	setOut, err := exec.Command(realReserve, "set", "--dir", resDir, "--cpu", "4", "--memory", "8", "--cores", "4").CombinedOutput()
	require.NoError(t, err, "nova-reserve set: %s", string(setOut))

	reserveWrap := filepath.Join(work, "nova-reserve")
	wrapScript := fmt.Sprintf("#!/bin/sh\nv=$1; shift\nexec %q \"$v\" --dir %q \"$@\"\n", realReserve, resDir)
	require.NoError(t, os.WriteFile(reserveWrap, []byte(wrapScript), 0o755))

	// Bench worker holds full machine capacity (cpu 4, memory 8).
	benchHold := exec.Command(realReserve, "hold", "--dir", resDir,
		"--holder", "bench", "--consumer", "card-1.w1@1", "--label", "work", "--needs", "cpu 4, memory 8")
	benchIn, err := benchHold.StdinPipe()
	require.NoError(t, err)
	benchOut, err := benchHold.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, benchHold.Start())
	t.Cleanup(func() {
		_ = benchIn.Close()
		_ = benchHold.Wait()
	})
	line, err := bufio.NewReader(benchOut).ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(line, "HELD "))

	// Fake Runner.Worker process watched by ci-hold.
	workerCmd := exec.Command("sleep", "60")
	require.NoError(t, workerCmd.Start())
	t.Cleanup(func() {
		if workerCmd.Process != nil {
			_ = workerCmd.Process.Kill()
		}
		_ = workerCmd.Wait()
	})

	hooks := ciCopy(t, "")
	sh := ciShells(t)[0]
	env := []string{
		"GITHUB_EVENT_NAME=schedule",
		"GITHUB_REF=refs/heads/main",
		"NOVA_CI_WORKER_PID=" + strconv.Itoa(workerCmd.Process.Pid),
		"NOVA_CI_RESERVE=" + reserveWrap,
		"NOVA_CI_NEEDS=cpu 4, memory 8",
		"NOVA_RESERVE_DIR=",
	}
	code, out := ciRun(t, sh, hooks, "", env, "started")
	require.Equal(t, 0, code, out)

	// Verify CI hold took over immediately and marked bench hold paused in snapshot JSON.
	statusBytes, err := exec.Command(realReserve, "status", "--dir", resDir, "--json").Output()
	require.NoError(t, err)
	type snapView struct {
		Op       string `json:"op"`
		Machines []struct {
			Leases []struct {
				Holder   string         `json:"holder"`
				Label    string         `json:"label"`
				Consumer string         `json:"consumer"`
				Paused   bool           `json:"paused"`
				Needs    map[string]int `json:"needs"`
			} `json:"leases"`
		} `json:"machines"`
	}
	var st snapView
	require.NoError(t, json.Unmarshal(statusBytes, &st))
	require.Equal(t, "snapshot", st.Op)
	require.Len(t, st.Machines, 1)
	require.Len(t, st.Machines[0].Leases, 2)
	var ciCPU, pausedWorkCPU int
	var pausedConsumers []string
	for _, l := range st.Machines[0].Leases {
		if l.Holder == "ci" && !l.Paused {
			ciCPU += l.Needs["cpu"]
		}
		if l.Label == "work" && l.Paused {
			pausedWorkCPU += l.Needs["cpu"]
			pausedConsumers = append(pausedConsumers, l.Consumer)
		}
	}
	assert.Equal(t, 4, ciCPU)
	assert.Equal(t, 4, pausedWorkCPU)
	assert.Equal(t, []string{"card-1.w1@1"}, pausedConsumers)

	// Completed disarms ci-hold; verify CI lease drops and bench hold unpauses.
	code, out = ciRun(t, sh, hooks, "", env, "completed")
	require.Equal(t, 0, code, out)

	require.Eventually(t, func() bool {
		b, err := exec.Command(realReserve, "status", "--dir", resDir, "--json").Output()
		if err != nil {
			return false
		}
		var cur snapView
		if json.Unmarshal(b, &cur) != nil || len(cur.Machines) != 1 || len(cur.Machines[0].Leases) != 1 {
			return false
		}
		l := cur.Machines[0].Leases[0]
		return !l.Paused && l.Consumer == "card-1.w1@1"
	}, 3*time.Second, 25*time.Millisecond)
}
