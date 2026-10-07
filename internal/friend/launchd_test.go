package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "bootstrap gui/501 " + a.PlistPath(), "bootout gui/501/com.nova.friend-bob", "bootstrap gui/501 " + a.PlistPath()}, ran)
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

// The installed agent is one launchd keeps alive and the daemon it runs says
// which binary it is: the plist on disk carries RunAtLoad, KeepAlive, the
// throttle and the binary by absolute path, install refuses a plist without
// them and writes nothing, and the daemon's status carries its version and
// path, read back through the installed agent's state directory
// (docs/SPEC-FRIEND.md, daemon-supervised-r-b.w6).
func TestInstalledAgentKeepsAliveAndStatusSaysVersion(t *testing.T) {
	t.Parallel()
	ctl := func(context.Context, ...string) (string, error) { return "", nil }
	writeFile := func(path string, data []byte) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}

	t.Run("the installed plist keeps the daemon alive", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.Home = t.TempDir()
		a.LaunchdLog = filepath.Join(a.Home, "Library", "Logs", "nova-friend-bob.log")
		path, _, err := Install(context.Background(), a, 501, ctl, writeFile, func() {})
		require.NoError(t, err)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		plist := string(raw)
		v := PlistValues(plist)
		assert.Equal(t, "true", v["KeepAlive"])
		assert.Equal(t, "true", v["RunAtLoad"])
		assert.Equal(t, "5", v["ThrottleInterval"])
		args := PlistArgs(plist)
		require.NotEmpty(t, args)
		assert.Equal(t, "/opt/nova/bin/nova-friend", args[0], "the binary by absolute path")
		assert.NoError(t, CheckPlist(plist))
	})

	t.Run("a plist without them is refused, naming each", func(t *testing.T) {
		t.Parallel()
		good := agent().Plist()
		for _, c := range []struct{ name, from, to, want string }{
			{"no keep alive", "<key>KeepAlive</key><true/>", "", "KeepAlive is not true"},
			{"keep alive false", "<key>KeepAlive</key><true/>", "<key>KeepAlive</key><false/>", "KeepAlive is not true"},
			{"no run at load", "<key>RunAtLoad</key><true/>", "", "RunAtLoad is not true"},
			{"no throttle", "<key>ThrottleInterval</key><integer>5</integer>", "", "ThrottleInterval is not 5"},
			{"relative binary", "<string>/opt/nova/bin/nova-friend</string>", "<string>nova-friend</string>", "the program nova-friend is not an absolute path"},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				bad := strings.Replace(good, c.from, c.to, 1)
				require.NotEqual(t, good, bad)
				assert.ErrorContains(t, CheckPlist(bad), c.want)
			})
		}
		both := strings.Replace(strings.Replace(good, "<key>KeepAlive</key><true/>", "", 1), "<key>RunAtLoad</key><true/>", "", 1)
		assert.ErrorContains(t, CheckPlist(both), "RunAtLoad is not true; KeepAlive is not true", "every problem at once")

		a := agent()
		a.Home = t.TempDir()
		a.LaunchdLog = filepath.Join(a.Home, "launchd.log")
		a.Binary = "bin/nova-friend"
		ran := 0
		_, commands, err := Install(context.Background(), a, 501, func(context.Context, ...string) (string, error) { ran++; return "", nil }, writeFile, func() {})
		assert.ErrorContains(t, err, "the program bin/nova-friend is not an absolute path")
		assert.Empty(t, commands)
		assert.Zero(t, ran, "no launchctl")
		assert.NoFileExists(t, a.PlistPath(), "nothing written")
	})

	t.Run("the daemon's status says its version and binary", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		a := agent()
		a.Home, a.Dir = home, filepath.Join(home, "w", "bob")
		a.LaunchdLog = filepath.Join(home, "launchd.log")
		_, _, err := Install(context.Background(), a, 501, ctl, writeFile, func() {})
		require.NoError(t, err)
		agents, err := InstalledAgents(home)
		require.NoError(t, err)
		require.Len(t, agents, 1)
		assert.Equal(t, Installed{Friend: "bob", Plist: a.PlistPath(), StateDir: DefaultStateDir(home, "bob")}, agents[0])

		r := newRig(t)
		r.d.Version, r.d.Binary = "v1.2.0", "/opt/nova/bin/nova-friend"
		r.d.Status = func(s Status) error { return WriteStatus(agents[0].StateDir, s) }
		r.run(t, 8) // past StatusEvery on the rig's clock: a status written after a beat
		s, found, err := ReadStatus(agents[0].StateDir)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "v1.2.0", s.DaemonVersion)
		assert.Equal(t, "/opt/nova/bin/nova-friend", s.Binary)
		assert.True(t, s.LastBeat.After(t0), "the last beat, on the injected clock")
	})
}
