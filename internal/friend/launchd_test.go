package friend

import (
	"context"
	"errors"
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
