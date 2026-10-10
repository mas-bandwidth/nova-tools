package friend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecondEngineIsRefusedWithTheLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := time.Date(2026, 10, 7, 19, 33, 0, 0, time.UTC)
	alive := map[int]bool{100: true, 200: true}
	isAlive := func(pid int) bool { return alive[pid] }

	h, err := AcquireEngine(dir, 100, since, isAlive)
	require.NoError(t, err)
	require.NoError(t, h.SetLanes(4, since.Add(time.Minute)))

	_, err = AcquireEngine(dir, 100, since.Add(time.Hour), isAlive)
	require.NoError(t, err)
	view, err := ReadEngine(dir)
	require.NoError(t, err)
	assert.True(t, view.Since.Equal(since), "the same pid keeps the original start, got %s", view.Since)
	assert.Equal(t, 4, view.Lanes)

	_, err = AcquireEngine(dir, 200, since.Add(2*time.Minute), isAlive)
	var held *EngineHeld
	require.ErrorAs(t, err, &held)
	want := "engine held by 100 since 2026-10-07T19:33:00Z"
	assert.Equal(t, want, err.Error())
	row, err := os.ReadFile(filepath.Join(dir, EngineRowFile))
	require.NoError(t, err)
	assert.Contains(t, string(row), want)
	n, ok := LanesFromHolder(dir, 200, isAlive)
	assert.False(t, ok)
	assert.Equal(t, 0, n)
	n, ok = LanesFromHolder(dir, 100, isAlive)
	assert.True(t, ok)
	assert.Equal(t, 4, n)

	alive[100] = false
	h2, err := AcquireEngine(dir, 200, since.Add(2*time.Minute), isAlive)
	require.NoError(t, err)
	require.NoError(t, h2.SetLanes(1, since.Add(3*time.Minute)))
	n, ok = LanesFromHolder(dir, 200, isAlive)
	assert.True(t, ok)
	assert.Equal(t, 1, n)
	view, err = ReadEngine(dir)
	require.NoError(t, err)
	assert.True(t, view.Since.Equal(since.Add(2*time.Minute)))
}

func TestLockReleasedOnExit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := time.Date(2026, 10, 7, 19, 33, 0, 0, time.UTC)
	alive := func(pid int) bool { return pid == 100 || pid == 200 }
	h, err := AcquireEngine(dir, 100, since, alive)
	require.NoError(t, err)
	require.NoError(t, h.Release())
	_, err = os.Stat(filepath.Join(dir, EngineLockFile))
	assert.True(t, os.IsNotExist(err))

	h2, err := AcquireEngine(dir, 200, since.Add(time.Minute), alive)
	require.NoError(t, err)
	require.NoError(t, h.Release())
	n, ok := LanesFromHolder(dir, 200, alive)
	assert.True(t, ok)
	assert.Equal(t, 0, n)
	require.NoError(t, h2.Release())
	_, err = os.Stat(filepath.Join(dir, EngineLockFile))
	assert.True(t, os.IsNotExist(err))
}

func TestReleaseIfDeadDropsOnlyADeadHoldersLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := time.Date(2026, 10, 7, 19, 33, 0, 0, time.UTC)
	alive := map[int]bool{100: true}
	isAlive := func(pid int) bool { return alive[pid] }

	h, err := AcquireEngine(dir, 100, since, isAlive)
	require.NoError(t, err)

	require.NoError(t, ReleaseIfDead(dir, isAlive))
	_, err = os.Stat(filepath.Join(dir, EngineLockFile))
	require.NoError(t, err, "a live holder keeps the lock")

	alive[100] = false
	require.NoError(t, ReleaseIfDead(dir, isAlive))
	_, err = os.Stat(filepath.Join(dir, EngineLockFile))
	assert.True(t, os.IsNotExist(err), "a dead holder's lock is dropped")

	require.NoError(t, h.Release())
	require.NoError(t, ReleaseIfDead(dir, isAlive))
}

func TestPlayWritesOneUnitPerBatchFriendAndRetiresAShellRunner(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	adaDir := filepath.Join(root, "ada")
	bobDir := filepath.Join(root, "bob")
	require.NoError(t, os.MkdirAll(adaDir, 0o755))
	require.NoError(t, os.MkdirAll(bobDir, 0o755))
	body := "#!/bin/zsh\necho lanes\n"
	script := filepath.Join(adaDir, "runner.zsh")
	bobScript := filepath.Join(bobDir, "runner.zsh")
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	require.NoError(t, os.WriteFile(bobScript, []byte(body), 0o755))

	home := filepath.Join(root, "home")
	var launches [][]string
	alive := map[int]bool{4242: true}
	p := Play{
		Home:   home,
		Binary: "/usr/local/bin/nova-runner",
		UID:    501,
		Now:    time.Date(2026, 10, 7, 19, 33, 0, 0, time.UTC),
		Launch: func(args ...string) error {
			launches = append(launches, append([]string(nil), args...))
			return nil
		},
		Stop: func(pid int) error {
			delete(alive, pid)
			return nil
		},
		Rows: []EngineRow{
			{Name: "ada", Mode: ModeBatch, Width: 16, Tiers: "heavy", Harness: "grok", Dir: adaDir, StateDir: filepath.Join(home, ".nova-friend", "ada")},
			{Name: "bob", Mode: ModeOneShot, Width: 4, Tiers: "flash", Harness: "claude", Dir: bobDir},
		},
		Shells: []Shell{{Path: script, PID: 4242, Label: "com.nova.shell-ada"}},
	}
	require.NoError(t, p.Install())

	agents := filepath.Join(home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(agents)
	require.NoError(t, err)
	var plists []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".plist") {
			plists = append(plists, e.Name())
		}
	}
	assert.Equal(t, []string{"com.nova.runner-ada.plist"}, plists)
	text, err := os.ReadFile(filepath.Join(agents, "com.nova.runner-ada.plist"))
	require.NoError(t, err)
	plist := string(text)
	assert.Contains(t, plist, "com.nova.runner-ada")
	assert.Contains(t, plist, "<key>KeepAlive</key><true/>")
	assert.Contains(t, plist, "<key>StandardOutPath</key>")
	assert.Contains(t, plist, "--width")
	assert.Contains(t, plist, ">16<")
	assert.Contains(t, plist, "heavy")
	assert.Contains(t, plist, "grok")
	assert.Contains(t, plist, adaDir)
	assert.Contains(t, plist, "Library/Logs")
	assert.Contains(t, plist, "/usr/local/bin/nova-runner")
	_, err = os.Stat(filepath.Join(agents, "com.nova.runner-bob.plist"))
	assert.True(t, os.IsNotExist(err))

	got, err := os.ReadFile(script)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(got), "# RETIRED"))
	assert.Contains(t, string(got), "echo lanes")
	assert.Contains(t, string(got), "nova-sprint friend engine ada restart")
	assert.False(t, alive[4242])
	bobGot, err := os.ReadFile(bobScript)
	require.NoError(t, err)
	assert.Equal(t, body, string(bobGot))

	require.GreaterOrEqual(t, len(launches), 3)
	assert.Equal(t, []string{"bootout", "gui/501/com.nova.shell-ada"}, launches[0])
	assert.Equal(t, []string{"bootout", "gui/501/com.nova.runner-ada"}, launches[1])
	assert.Equal(t, "bootstrap", launches[2][0])
	assert.Equal(t, "gui/501", launches[2][1])
	assert.True(t, strings.HasSuffix(launches[2][2], "com.nova.runner-ada.plist"))

	require.NoError(t, p.Install())
	got, err = os.ReadFile(script)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(got), "# RETIRED"))
}

func TestZeroLanesWithReadyCardsIsOneJudgment(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 7, 19, 33, 0, 0, time.UTC)
	sample := LaneSample{
		Friend: "ada", Mode: ModeBatch, Lanes: 0, Ready: 3,
		HolderPID: 100, Since: since,
	}
	var ep LaneEpisode
	start := since.Add(time.Hour)
	sample.Now = start
	ep, body, push := ConsiderLanes(ep, sample)
	assert.False(t, push)
	assert.Empty(t, body)

	sample.Now = start.Add(ZeroLanesFor - time.Second)
	ep, body, push = ConsiderLanes(ep, sample)
	assert.False(t, push)
	assert.Empty(t, body)

	sample.Now = start.Add(ZeroLanesFor)
	ep, body, push = ConsiderLanes(ep, sample)
	assert.True(t, push)
	assert.Contains(t, body, "judgment: ada zero lanes with ready cards")
	assert.Contains(t, body, "engine held by 100 since 2026-10-07T19:33:00Z")
	assert.Contains(t, body, "last lane start: none")
	assert.Contains(t, body, "nova-sprint friend engine ada restart")

	ep, body, push = ConsiderLanes(ep, sample)
	assert.False(t, push)
	assert.Empty(t, body)

	sample.Lanes = 2
	ep, _, push = ConsiderLanes(ep, sample)
	assert.False(t, push)
	sample.Lanes = 0
	sample.Now = sample.Now.Add(time.Hour)
	ep, _, push = ConsiderLanes(ep, sample)
	assert.False(t, push)
	sample.Now = sample.Now.Add(ZeroLanesFor)
	_, body, push = ConsiderLanes(ep, sample)
	assert.True(t, push)
	assert.Contains(t, body, "last lane start: none")

	sample.Mode = ModeOneShot
	sample.Now = start.Add(2 * ZeroLanesFor)
	_, _, push = ConsiderLanes(LaneEpisode{}, sample)
	assert.False(t, push)
}
