package friend

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent is one friend's launchd agent: the daemon as launchd runs it,
// never started by the model (SPEC-FRIEND.md, the daemon).
type Agent struct {
	Friend, Harness, Dir, Session string
	StateDir                      string // the daemon's state files, when not the default under Home
	Width                         int
	Binary                        string // this tool, by absolute path
	Redis, Server                 string // the bus store and the sprint server
	Home, Path                    string // the environment the agent runs in
	LaunchdLog                    string // launchd's own stdout and stderr path, off the friend's volume
}

// Label is the agent's launchd label.
func (a Agent) Label() string { return "com.nova.friend-" + a.Friend }

// PlistPath is where the agent's plist lives under home.
func (a Agent) PlistPath() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", a.Label()+".plist")
}

// Args is the daemon's command line.
func (a Agent) Args() []string {
	args := []string{a.Binary, "run", "--as", a.Friend, "--harness", a.Harness, "--dir", a.Dir, "--redis", a.Redis, "--server", a.Server, "--width", fmt.Sprint(a.Width)}
	if a.Session != "" {
		args = append(args, "--session", a.Session)
	}
	if a.StateDir != "" {
		args = append(args, "--state-dir", a.StateDir)
	}
	return args
}

// Plist is the agent's plist: RunAtLoad and KeepAlive, so it starts at
// login and is restarted when it dies (pending messages redeliver first,
// nova-bus's rule). launchd opens its own log itself, before the daemon
// runs, and cannot open one on a network volume (EX_CONFIG, measured
// 2026-10-03), so that log is LaunchdLog, under the home directory, and so
// are the daemon's state files and record (DefaultStateDir).
func (a Agent) Plist() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + esc(a.Label()) + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, arg := range a.Args() {
		b.WriteString("    <string>" + esc(arg) + "</string>\n")
	}
	b.WriteString(`  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>` + esc(a.Home) + `</string>
    <key>PATH</key><string>` + esc(a.Path) + `</string>
  </dict>
  <key>WorkingDirectory</key><string>` + esc(a.Home) + `</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>` + esc(a.LaunchdLog) + `</string>
  <key>StandardErrorPath</key><string>` + esc(a.LaunchdLog) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s)) // ignored: a strings.Builder never fails to write
	return b.String()
}

// Launchctl runs launchctl with args and answers what it printed; the
// installer passes the real one, a test its own.
type Launchctl func(ctx context.Context, args ...string) (output string, err error)

// BootstrapTries is how many times a bootstrap is sent while launchd is
// still tearing the old agent down (it answers EIO, "Input/output error",
// for a second or so after the bootout, measured 2026-10-04).
const BootstrapTries = 5

// Install writes the plist and loads it: a bootout of whatever that label
// runs now (nothing loaded is fine), then a bootstrap into the user's
// domain, sent again after wait() while launchd answers EIO, so running it
// again replaces the agent with the same result. It answers the plist's
// path and the commands it ran.
func Install(ctx context.Context, a Agent, uid int, run Launchctl, write func(path string, data []byte) error, wait func()) (path string, ran []string, err error) {
	path = a.PlistPath()
	if err := os.MkdirAll(filepath.Dir(a.LaunchdLog), 0o755); err != nil {
		return path, nil, err
	}
	if err := write(path, []byte(a.Plist())); err != nil {
		return path, nil, err
	}
	domain := fmt.Sprintf("gui/%d", uid)
	bootout := []string{"bootout", domain + "/" + a.Label()}
	ran = append(ran, "launchctl "+strings.Join(bootout, " "))
	_, _ = run(ctx, bootout...) // ignored: a label that is not loaded answers an error, and that is the state wanted
	bootstrap := []string{"bootstrap", domain, path}
	for try := 1; ; try++ {
		ran = append(ran, "launchctl "+strings.Join(bootstrap, " "))
		out, err := run(ctx, bootstrap...)
		if err == nil {
			return path, ran, nil
		}
		if try == BootstrapTries || !strings.Contains(out, "Input/output error") {
			return path, ran, fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(out))
		}
		wait()
	}
}

// Uninstall boots the agent out and removes its plist; an agent that is
// not there is fine.
func Uninstall(ctx context.Context, a Agent, uid int, run Launchctl, remove func(path string) error) (ran []string, err error) {
	domain := fmt.Sprintf("gui/%d", uid)
	bootout := []string{"bootout", domain + "/" + a.Label()}
	ran = append(ran, "launchctl "+strings.Join(bootout, " "))
	_, _ = run(ctx, bootout...) // ignored: not loaded is the state wanted
	if err := remove(a.PlistPath()); err != nil && !os.IsNotExist(err) {
		return ran, err
	}
	return ran, nil
}

// Loaded says whether launchctl has the label: `launchctl print
// gui/<uid>/<label>` answers 0 when it does.
func Loaded(ctx context.Context, label string, uid int, run Launchctl) bool {
	_, err := run(ctx, "print", fmt.Sprintf("gui/%d/%s", uid, label))
	return err == nil
}
