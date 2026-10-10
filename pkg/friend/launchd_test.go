package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agent() Agent {
	return Agent{Friend: "bob", Harness: "opencode", Dir: "/w/bob", Width: 4, Binary: "/opt/nova/bin/nova-friend",
		Redis: "store:6379", Server: "127.0.0.1:6390", Home: "/home/bob", Path: "/usr/bin:/bin", LaunchdLog: "/home/bob/Library/Logs/nova-friend-bob.log"}
}

func TestThePlistRunsTheDaemonAtLoadAndKeepsItAlive(t *testing.T) {
	t.Parallel()
	a := agent()
	p := a.Plist()
	for _, want := range []string{
		"<string>com.nova.friend-bob</string>", "<string>/opt/nova/bin/nova-friend</string>", "<string>run</string>",
		"<string>--as</string>\n    <string>bob</string>", "<string>--harness</string>\n    <string>opencode</string>",
		"<string>--dir</string>\n    <string>/w/bob</string>", "<string>--redis</string>\n    <string>store:6379</string>",
		"<string>--server</string>\n    <string>127.0.0.1:6390</string>", "<string>--width</string>\n    <string>4</string>",
		"<key>RunAtLoad</key><true/>", "<key>KeepAlive</key><true/>",
		"<key>StandardOutPath</key><string>/home/bob/Library/Logs/nova-friend-bob.log</string>",
		"<key>HOME</key><string>/home/bob</string>",
	} {
		assert.Contains(t, p, want)
	}
	assert.NotContains(t, p, "--session", "no session named: the newest is found at each delivery")
	a.Session = "ses_1"
	assert.Contains(t, a.Plist(), "<string>--session</string>\n    <string>ses_1</string>")
	a.Dir = "/w/a&b"
	assert.Contains(t, a.Plist(), "<string>/w/a&amp;b</string>", "the plist is XML")
	assert.Equal(t, "/home/bob/Library/LaunchAgents/com.nova.friend-bob.plist", a.PlistPath())
}

func TestInstallBootsOutThenBootstrapsAndIsTheSameTwice(t *testing.T) {
	t.Parallel()
	var ran []string
	files := map[string]string{}
	ctl := func(_ context.Context, args ...string) (string, error) {
		ran = append(ran, strings.Join(args, " "))
		if args[0] == "bootout" && len(files) == 0 {
			return "Boot-out failed: 3: No such process", errors.New("exit 3")
		}
		return "", nil
	}
	write := func(path string, data []byte) error { files[path] = string(data); return nil }
	a := agent()
	a.LaunchdLog = t.TempDir() + "/launchd.log"
	for i := 0; i < 2; i++ {
		path, commands, err := Install(context.Background(), a, 501, ctl, write, func() {})
		require.NoError(t, err, "run %d", i)
		assert.Equal(t, a.PlistPath(), path)
		assert.Equal(t, []string{"launchctl bootout gui/501/com.nova.friend-bob", "launchctl bootstrap gui/501 " + path}, commands)
	}
	assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "print gui/501/com.nova.friend-bob", "bootstrap gui/501 " + a.PlistPath(),
		"bootout gui/501/com.nova.friend-bob", "print gui/501/com.nova.friend-bob", "bootstrap gui/501 " + a.PlistPath()}, ran, "the bootstrap waits for launchd to release the label")
	assert.Contains(t, files[a.PlistPath()], "<string>com.nova.friend-bob</string>")

	ctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "bootstrap" {
			return "Bootstrap failed: 5: Input/output error", errors.New("exit 5")
		}
		return "", nil
	}
	_, commands, err := Install(context.Background(), a, 501, ctl, write, func() {})
	assert.ErrorContains(t, err, "launchctl bootstrap: exit 5: Bootstrap failed: 5: Input/output error")
	assert.Len(t, commands, 1+BootstrapTries, "EIO is launchd still tearing the old agent down: tried again after each wait")

	// the old agent gone after two tries: the third bootstrap loads
	tries := 0
	ctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "bootstrap" {
			tries++
			if tries < 3 {
				return "Bootstrap failed: 5: Input/output error", errors.New("exit 5")
			}
		}
		return "", nil
	}
	waited := 0
	_, commands, err = Install(context.Background(), a, 501, ctl, write, func() { waited++ })
	require.NoError(t, err)
	assert.Equal(t, 2, waited)
	assert.Len(t, commands, 4)

	ctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "bootstrap" {
			return "Bootstrap failed: 2: No such file or directory", errors.New("exit 2")
		}
		return "", nil
	}
	_, commands, err = Install(context.Background(), a, 501, ctl, write, func() {})
	assert.ErrorContains(t, err, "No such file")
	assert.Len(t, commands, 2, "any other failure is final")

	removed := []string{}
	commands, err = Uninstall(context.Background(), a, 501, ctl, func(p string) error { removed = append(removed, p); return nil })
	require.NoError(t, err)
	assert.Equal(t, []string{"launchctl bootout gui/501/com.nova.friend-bob"}, commands)
	assert.Equal(t, []string{a.PlistPath()}, removed)
}

// Three of the seat's adopt runs of 2026-10-07 rolled back: install booted the old
// agent out and bootstrapped before launchd had removed it; a daemon that takes about 5 s
// to exit made each of the five bootstraps, one second apart, answer "37: Operation already
// in progress". The bootstrap now waits until `launchctl print` no longer finds the label,
// every ReleasePoll, up to the plist's exit timeout. No real launchd and no real time.
func TestInstallWaitsForLaunchdToReleaseTheLabelAfterTheBootout(t *testing.T) {
	t.Parallel()
	const target = "gui/501/com.nova.friend-bob"
	// held answers print as launchd does: the service's block for n calls, then not found
	held := func(n int, calls *[]string) Launchctl {
		return func(_ context.Context, args ...string) (string, error) {
			*calls = append(*calls, strings.Join(args, " "))
			if args[0] != "print" {
				return "", nil
			}
			if n > 0 {
				n--
				return target + " = {\n\tactive count = 1\n}\n", nil
			}
			return "Bad request.\nCould not find service \"com.nova.friend-bob\" in domain for user gui: 501\n", errors.New("exit status 113")
		}
	}

	t.Run("the wait loop: polls until print stops finding it", func(t *testing.T) {
		t.Parallel()
		var calls []string
		var slept []time.Duration
		waited, err := WaitReleased(context.Background(), held(20, &calls), target, DefaultExitTimeout, ReleasePoll, func(d time.Duration) { slept = append(slept, d) })
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, waited, "20 looks found it, 250 ms apart") // wall-ok: the fake's count of polls, no clock runs
		assert.Len(t, calls, 21, "the 21st look finds it gone")
		assert.Len(t, slept, 20)
		assert.Equal(t, ReleasePoll, slept[0])
	})
	t.Run("the wait loop: gone at once waits nothing", func(t *testing.T) {
		t.Parallel()
		var calls []string
		waited, err := WaitReleased(context.Background(), held(0, &calls), target, DefaultExitTimeout, ReleasePoll, func(time.Duration) { t.Fatal("slept") })
		require.NoError(t, err)
		assert.Zero(t, waited)
		assert.Equal(t, []string{"print " + target}, calls)
	})
	t.Run("the wait loop: past the exit timeout it refuses naming the label and the seconds", func(t *testing.T) {
		t.Parallel()
		var calls []string
		_, err := WaitReleased(context.Background(), held(1000, &calls), target, 3*time.Second, ReleasePoll, func(time.Duration) {}) // wall-ok: a fake sleep, no clock runs
		require.Error(t, err)
		assert.Contains(t, err.Error(), "launchd still holds "+target+" 3s after its bootout")
		assert.Len(t, calls, 13, "a look at 0, every 250 ms, and the last at 3 s")
	})
	t.Run("install: bootout, the wait, then one bootstrap", func(t *testing.T) {
		t.Parallel()
		var calls []string
		a := agent()
		a.LaunchdLog = t.TempDir() + "/launchd.log"
		var slept time.Duration
		a.Sleep = func(d time.Duration) { slept += d }
		path, commands, err := Install(context.Background(), a, 501, held(20, &calls), recordWrite(map[string]string{}), func() { t.Fatal("a bootstrap was retried") })
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, slept) // wall-ok: the fake sleep's sum, no clock runs
		assert.Equal(t, "bootstrap gui/501 "+path, calls[len(calls)-1], "the bootstrap is sent once, after launchd released the label")
		assert.Equal(t, []string{"launchctl bootout " + target, "launchctl print " + target + " (every 250ms until launchd released it: 5s)", "launchctl bootstrap gui/501 " + path}, commands)
	})
	t.Run("install: a label never released is refused and nothing is bootstrapped", func(t *testing.T) {
		t.Parallel()
		var calls []string
		a := agent()
		a.LaunchdLog = t.TempDir() + "/launchd.log"
		a.Sleep = func(time.Duration) {}
		_, _, err := Install(context.Background(), a, 501, held(1000, &calls), recordWrite(map[string]string{}), func() {})
		require.ErrorContains(t, err, "launchd still holds "+target+" 20s after its bootout")
		for _, c := range calls {
			assert.NotContains(t, c, "bootstrap")
		}
	})
	t.Run("the plist's exit timeout bounds the wait", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, DefaultExitTimeout, ExitTimeout(agent().Plist()), "the friend's plist sets none: launchd's default")
		assert.Equal(t, 180*time.Second, ExitTimeout("<key>ExitTimeOut</key><integer>180</integer>"))
	})
}

// A binary under /Volumes is on the removable-volume wall: launchd starts it
// and it does nothing (docs/SPEC-FRIEND.md). Install copies it under the home
// and the plist names the copy, or refuses and writes nothing.
func TestInstallRefusesOrCopiesABinaryOnARemovableVolume(t *testing.T) {
	t.Parallel()
	const src = "/Volumes/disk/bin/nova-friend"

	t.Run("copies into the home and the plist names the copy", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		a := agent()
		a.Home, a.Binary, a.LaunchdLog = home, src, filepath.Join(home, "launchd.log")
		var copied []string
		a.Copy = func(from, to string) error {
			copied = append(copied, from+" -> "+to)
			return nil
		}
		files := map[string]string{}
		path, commands, err := Install(context.Background(), a, 501, recordCtl(nil), recordWrite(files), func() {})
		require.NoError(t, err)
		dst := InstalledBinary(home)
		assert.Equal(t, []string{src + " -> " + dst}, copied)
		assert.Equal(t, a.PlistPath(), path)
		assert.Contains(t, files[path], "<string>"+dst+"</string>")
		assert.NotContains(t, files[path], "/Volumes/")
		assert.Equal(t, []string{"launchctl bootout gui/501/com.nova.friend-bob", "launchctl bootstrap gui/501 " + path}, commands)
	})

	t.Run("refuses when the copy fails and writes nothing", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		a := agent()
		a.Home, a.Binary, a.LaunchdLog = home, src, filepath.Join(home, "launchd.log")
		a.Copy = func(string, string) error { return errors.New("disk full") }
		wrote, launched := false, false
		_, commands, err := Install(context.Background(), a, 501,
			func(context.Context, ...string) (string, error) { launched = true; return "", nil },
			func(string, []byte) error { wrote = true; return nil }, func() {})
		require.ErrorIs(t, err, ErrBinaryOnRemovableVolume)
		assert.ErrorContains(t, err, "disk full")
		assert.False(t, wrote)
		assert.False(t, launched)
		assert.Empty(t, commands)
		assert.NoFileExists(t, filepath.Join(home, "launchd.log"))
	})

	t.Run("refuses when no copy is offered", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		a := agent()
		a.Home, a.Binary, a.LaunchdLog = home, src, filepath.Join(home, "launchd.log")
		wrote := false
		_, commands, err := Install(context.Background(), a, 501, recordCtl(nil), func(string, []byte) error { wrote = true; return nil }, func() {})
		require.ErrorIs(t, err, ErrBinaryOnRemovableVolume)
		assert.ErrorContains(t, err, InstalledBinary(home))
		assert.False(t, wrote)
		assert.Empty(t, commands)
	})

	t.Run("refuses when the home is on a removable volume", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.Home, a.Binary = "/Volumes/disk/home", src
		a.LaunchdLog = filepath.Join(t.TempDir(), "launchd.log")
		called, wrote := false, false
		a.Copy = func(string, string) error { called = true; return nil }
		_, commands, err := Install(context.Background(), a, 501, recordCtl(nil), func(string, []byte) error { wrote = true; return nil }, func() {})
		require.ErrorIs(t, err, ErrBinaryOnRemovableVolume)
		assert.False(t, called, "a copy onto the same wall is not a remedy")
		assert.False(t, wrote)
		assert.Empty(t, commands)
	})

	t.Run("a path that leaves the volume is not copied", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		a := agent()
		a.Home, a.LaunchdLog = home, filepath.Join(home, "launchd.log")
		a.Binary = "/Volumes/disk/../../opt/bin/nova-friend"
		called := false
		a.Copy = func(string, string) error { called = true; return errors.New("copied") }
		files := map[string]string{}
		_, _, err := Install(context.Background(), a, 501, recordCtl(nil), recordWrite(files), func() {})
		require.NoError(t, err)
		assert.False(t, called)
		assert.Contains(t, files[a.PlistPath()], "<string>/opt/bin/nova-friend</string>")
		assert.NotContains(t, files[a.PlistPath()], "/Volumes/")
	})
}

func recordCtl(ran *[]string) Launchctl {
	return func(_ context.Context, args ...string) (string, error) {
		if ran != nil {
			*ran = append(*ran, strings.Join(args, " "))
		}
		return "", nil
	}
}

func recordWrite(files map[string]string) func(string, []byte) error {
	return func(path string, data []byte) error { files[path] = string(data); return nil }
}

// CopyExecutable is what install uses when the binary is on /Volumes. It is
// pinned on a temp directory, never on a live install.
func TestCopyExecutableKeepsTheModeAndLeavesTheOldFileOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\necho hi\n"), 0o644))
	dst := filepath.Join(dir, "sub", "nova-friend")
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0o644))
	require.NoError(t, CopyExecutable(src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho hi\n", string(got))
	st, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), st.Mode().Perm()&0o755)
	require.Error(t, CopyExecutable(filepath.Join(dir, "missing"), dst))
	got, err = os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho hi\n", string(got), "a failed copy leaves the previous binary")
	require.ErrorContains(t, CopyExecutable(dir, filepath.Join(dir, "nope")), "is not a file")
}

// With --secrets the agent's command is nova-secrets exec around the daemon:
// the store and key under the home directory, the seat's identity, the tool
// and sops by absolute path, exactly the named secrets (--only) and every
// one required, then the daemon after the --: the shape of a hand-written
// friend agent of 2026-10-04, written by the tool instead.
func TestThePlistWrapsTheDaemonInNovaSecretsExecForItsSecrets(t *testing.T) {
	t.Parallel()
	a := agent()
	a.Secrets, a.Seat, a.SecretsTool, a.Sops = []string{"DEEPSEEK_API_KEY", "GH_TOKEN"}, "studio", "/opt/nova/bin/nova-secrets", "/opt/homebrew/bin/sops"
	want := []string{"/opt/nova/bin/nova-secrets", "exec", "--store", "/home/bob/nova-bench/secrets", "--as", "studio", "--key", "/home/bob/.config/nova-secrets/studio.key",
		"--sops", "/opt/homebrew/bin/sops", "--only", "DEEPSEEK_API_KEY,GH_TOKEN", "--require", "DEEPSEEK_API_KEY", "--require", "GH_TOKEN", "--",
		"/opt/nova/bin/nova-friend", "run", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--redis", "store:6379", "--server", "127.0.0.1:6390", "--width", "4"}
	assert.Equal(t, want, a.Args())
	p := a.Plist()
	for i, arg := range want {
		assert.Contains(t, p, "<string>"+arg+"</string>", "argument %d", i)
	}
	assert.Less(t, strings.Index(p, "<string>--</string>"), strings.Index(p, "<string>run</string>"), "the daemon comes after the --")
	a.Secrets = nil
	assert.Equal(t, "/opt/nova/bin/nova-friend", a.Args()[0], "no secrets, no wrap")
}

// Notification installation has its own label and carries its safe mode and policy;
// it never boots out the ordinary daemon (SPEC-FRIEND.md, notifications).
func TestNotificationsAgentKeepsPolicyAndSeparateLabel(t *testing.T) {
	t.Parallel()
	a := agent()
	normal := a.Label()
	a.NotificationsOnly = true
	a.NotifyKinds = "request,blocker,report"
	a.NotifyWindow = NotificationWindow
	assert.NotEqual(t, normal, a.Label())
	assert.Contains(t, a.Args(), "--notifications-only")
	assert.Contains(t, a.Args(), "--notify-kinds")
	assert.Contains(t, a.Args(), a.NotifyKinds)
	assert.Contains(t, a.Args(), "--notify-window")
	assert.Contains(t, a.Plist(), a.Label())
	assert.NotContains(t, a.Plist(), "<string>"+normal+"</string>")
}

// Each notification artifact has an immutable path of its own; a source change never
// overwrites either the native daemon or a previous notification version.
func TestNotificationBinaryPlanIsContentAddressedAndNativeBinaryIsSeparate(t *testing.T) {
	t.Parallel()
	a := agent()
	a.Home = t.TempDir()
	a.Binary = filepath.Join(t.TempDir(), "source")
	a.NotificationsOnly = true
	require.NoError(t, os.WriteFile(a.Binary, []byte("first executable"), 0o755))
	first, copy, err := a.BinaryPlan()
	require.NoError(t, err)
	assert.True(t, copy)
	assert.NotEqual(t, InstalledBinary(a.Home), first)
	require.NoError(t, os.WriteFile(a.Binary, []byte("second executable"), 0o755))
	second, _, err := a.BinaryPlan()
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.Contains(t, first, filepath.Join(a.Home, ".nova-friend", "notifications", "bin"))
}
