package friend

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WakePingLabel is the wake ping loop's launchd label; its plist is WakePingLabel.plist.
const WakePingLabel = "nova-friend.wake-ping"

// WakePingService is the wake ping loop's systemd user unit.
const WakePingService = "nova-friend-wake-ping.service"

// WakePingUnit is the wake ping loop as a service: the binary, the coordinator it acts as,
// the interval and bound, the stores it reaches, and the log file.
type WakePingUnit struct {
	OS, Exe, As   string
	Every, Bound  time.Duration
	Redis, Server string
	Log           string
}

// WakePingResult is what install or uninstall did: the unit's path, and whether the file
// was written or removed.
type WakePingResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

// WakePingInstaller writes units into Dir; Load loads the unit at a path and Unload unloads it.
type WakePingInstaller struct {
	Dir          string
	Load, Unload func(path string) error
}

// WakePingUnitFile is the unit's file name on goos, "" where there is no service manager.
func WakePingUnitFile(goos string) string {
	switch goos {
	case "darwin":
		return WakePingLabel + ".plist"
	case "linux":
		return WakePingService
	}
	return ""
}

// Args is the wake ping loop's command line:
// nova-friend ping --wake --every <d> --to-friends --as <coordinator> ...
func (u WakePingUnit) Args() []string {
	every := u.Every
	if every <= 0 {
		every = 10 * time.Minute
	}
	args := []string{u.Exe, "ping", "--wake", "--every", every.String(), "--to-friends"}
	if u.As != "" {
		args = append(args, "--as", u.As)
	}
	if u.Bound > 0 && u.Bound != Window {
		args = append(args, "--bound", u.Bound.String())
	}
	if u.Redis != "" {
		args = append(args, "--redis", u.Redis)
	}
	if u.Server != "" {
		args = append(args, "--server", u.Server)
	}
	return args
}

func (u WakePingUnit) env() [][2]string {
	var env [][2]string
	if u.Redis != "" {
		env = append(env, [2]string{"NOVA_BUS_REDIS", u.Redis})
	}
	if u.Server != "" {
		env = append(env, [2]string{"NOVA_SPRINT_SERVER", u.Server})
	}
	return env
}

func (u WakePingUnit) refusal() string {
	switch {
	case WakePingUnitFile(u.OS) == "":
		return "ping install installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on " + u.OS
	case !filepath.IsAbs(u.Exe):
		return "the unit runs nova-friend by its absolute path, not " + u.Exe
	case u.As == "":
		return "--as is required: the coordinator to ping as and report deaf sessions to"
	}
	return ""
}

// Text is the unit's file content: a launchd plist or systemd user unit.
func (u WakePingUnit) Text() (string, error) {
	if why := u.refusal(); why != "" {
		return "", errors.New(why)
	}
	if u.OS == "linux" {
		return u.systemd(), nil
	}
	return u.plist(), nil
}

func (u WakePingUnit) plist() string {
	return WakePingPlist(WakePingLabel, u.Args(), u.env(), u.Log)
}
func sdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

func (u WakePingUnit) systemd() string {
	var words []string
	for _, a := range u.Args() {
		words = append(words, sdQuote(a))
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=nova-friend wake ping loop (ping --wake --every --to-friends)\n\n[Service]\n")
	b.WriteString("ExecStart=" + strings.Join(words, " ") + "\n")
	for _, kv := range u.env() {
		b.WriteString("Environment=" + sdQuote(kv[0]+"="+kv[1]) + "\n")
	}
	b.WriteString("Restart=always\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// Install writes the unit into in.Dir and loads it.
func (in WakePingInstaller) Install(u WakePingUnit) (WakePingResult, error) {
	text, err := u.Text()
	if err != nil {
		return WakePingResult{}, err
	}
	r := WakePingResult{Path: filepath.Join(in.Dir, WakePingUnitFile(u.OS))}
	old, err := os.ReadFile(r.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return r, err
	}
	if !bytes.Equal(old, []byte(text)) {
		if err := os.MkdirAll(in.Dir, 0o755); err != nil {
			return r, err
		}
		tmp := r.Path + ".tmp"
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			return r, err
		}
		if err := os.Rename(tmp, r.Path); err != nil {
			return r, err
		}
		r.Changed = true
	}
	if in.Load != nil {
		if err := in.Load(r.Path); err != nil {
			return r, fmt.Errorf("the unit is written at %s and did not load: %w", r.Path, err)
		}
	}
	return r, nil
}

// Uninstall unloads the unit and removes its file.
func (in WakePingInstaller) Uninstall(goos string) (WakePingResult, error) {
	name := WakePingUnitFile(goos)
	if name == "" {
		return WakePingResult{}, errors.New("ping uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and " + goos + " has neither")
	}
	r := WakePingResult{Path: filepath.Join(in.Dir, name)}
	if _, err := os.Stat(r.Path); errors.Is(err, fs.ErrNotExist) {
		return r, nil
	} else if err != nil {
		return r, err
	}
	if in.Unload != nil {
		if err := in.Unload(r.Path); err != nil {
			return r, fmt.Errorf("the unit at %s did not unload, and is kept: %w", r.Path, err)
		}
	}
	if err := os.Remove(r.Path); err != nil {
		return r, err
	}
	r.Changed = true
	return r, nil
}
