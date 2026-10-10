package sprint

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/units"
)

// seat install (docs/SPEC-SPRINT.md, "Handing over the seat"; the owner, 2026-10-04:
// what is manual needs a verb): the seat's push loop, inbox --wait --push seat, runs
// as a service of the machine the seat works on, a launchd agent on macOS and a
// systemd user unit on Linux, kept alive and started again at login. The unit is the
// tool's own text: install writes it and loads it, uninstall unloads it and removes
// it. The unit's directory and its loader are the caller's, so a test writes into a
// directory of its own and loads nothing.

// SeatLabel is the push loop's launchd label; its plist is SeatLabel.plist.
const SeatLabel = units.SeatLabel

// SeatService is the push loop's systemd user unit.
const SeatService = units.SeatService

// SeatUnit is the push loop as a service: the binary, the store it reads (Redis, an
// address; else Server, the sprint's server), and the file its lines go to (launchd;
// systemd keeps them in its journal).
type SeatUnit struct {
	OS, Exe, Redis, Server, Log string
}

// SeatResult is what install or uninstall did: the unit's path, and whether the file
// there was written or removed.
type SeatResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

// SeatInstaller writes units into Dir; Load loads (or loads again) the unit at a path
// and Unload unloads it.
type SeatInstaller struct {
	Dir          string
	Load, Unload func(path string) error
}

// SeatUnitFile is the unit's file name on goos, "" where there is no service manager
// the push loop is installed under.
func SeatUnitFile(goos string) string {
	switch goos {
	case "darwin":
		return SeatLabel + ".plist"
	case "linux":
		return SeatService
	}
	return ""
}

// Args is the push loop's command line, the binary first: inbox --wait --push seat,
// on the store the unit names (docs/SPEC-SPRINT.md, "Handing over the seat").
func (u SeatUnit) Args() []string {
	args := []string{u.Exe, "inbox", "--wait", "--push", "seat"}
	if u.Redis != "" {
		args = append(args, "--redis", u.Redis)
	}
	return args
}

// env is the unit's environment: the sprint's server when the store is reached
// through it. Never a secret: the unit is a file anyone on the machine may read.
func (u SeatUnit) env() [][2]string {
	if u.Redis == "" && u.Server != "" {
		return [][2]string{{"NOVA_SPRINT_SERVER", u.Server}}
	}
	return nil
}

// refusal is why the unit is not installed, "" is it may be.
func (u SeatUnit) refusal() string {
	switch {
	case SeatUnitFile(u.OS) == "":
		return "seat install installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on " + orDash(u.OS) + "; run the loop by hand: nova-sprint inbox --wait --push seat"
	case !filepath.IsAbs(u.Exe):
		return "the unit runs nova-sprint by its absolute path, not " + orDash(u.Exe)
	case u.Redis == "" && u.Server == "":
		return "the push loop reads the sprint's store: --redis <addr> (or NOVA_SPRINT_REDIS), or the sprint's server, NOVA_SPRINT_SERVER"
	case strings.HasPrefix(u.Redis, "mem:"):
		return "the push loop waits on the sprint's machine, and the in-memory twin " + u.Redis + " has none: install it for a Redis store or the sprint's server"
	}
	return ""
}

// Text is the unit's file: a launchd plist, or a systemd user unit.
func (u SeatUnit) Text() (string, error) {
	if why := u.refusal(); why != "" {
		return "", errors.New(why)
	}
	if u.OS == "linux" {
		return u.systemd(), nil
	}
	return u.plist(), nil
}

func (u SeatUnit) plist() string {
	var b strings.Builder
	str := func(indent, s string) {
		b.WriteString(indent + "<string>")
		// ignored: a strings.Builder never fails a write
		_ = xml.EscapeText(&b, []byte(s))
		b.WriteString("</string>\n")
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
`)
	str("\t", SeatLabel)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range u.Args() {
		str("\t\t", a)
	}
	b.WriteString("\t</array>\n")
	if env := u.env(); len(env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, kv := range env {
			b.WriteString("\t\t<key>" + kv[0] + "</key>\n")
			str("\t\t", kv[1])
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n\t<key>KeepAlive</key>\n\t<true/>\n\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n")
	if u.Log != "" {
		b.WriteString("\t<key>StandardOutPath</key>\n")
		str("\t", u.Log)
		b.WriteString("\t<key>StandardErrorPath</key>\n")
		str("\t", u.Log)
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// sdQuote is one word of a systemd command line or assignment: double-quoted, its
// backslashes and quotes escaped, and its specifiers (%) and variables ($) doubled.
func sdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

func (u SeatUnit) systemd() string {
	var words []string
	for _, a := range u.Args() {
		words = append(words, sdQuote(a))
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=nova-sprint seat push loop (inbox --wait --push seat)\n\n[Service]\n")
	b.WriteString("ExecStart=" + strings.Join(words, " ") + "\n")
	for _, kv := range u.env() {
		b.WriteString("Environment=" + sdQuote(kv[0]+"="+kv[1]) + "\n")
	}
	b.WriteString("Restart=always\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// Install writes the unit into the installer's directory (whole, beside it first,
// then renamed into place) unless the file there is already this text, and loads it
// either way, so an install after the unit was unloaded by hand starts the loop
// again. A unit whose load fails stays written; the error says the load.
func (in SeatInstaller) Install(u SeatUnit) (SeatResult, error) {
	text, err := u.Text()
	if err != nil {
		return SeatResult{}, err
	}
	r := SeatResult{Path: filepath.Join(in.Dir, SeatUnitFile(u.OS))}
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
	if err := in.Load(r.Path); err != nil {
		return r, fmt.Errorf("the unit is written at %s and did not load: %w", r.Path, err)
	}
	return r, nil
}

// Uninstall unloads the unit and removes its file; with no file there it does
// nothing, and says so by Changed false.
func (in SeatInstaller) Uninstall(goos string) (SeatResult, error) {
	name := SeatUnitFile(goos)
	if name == "" {
		return SeatResult{}, errors.New("seat uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and " + orDash(goos) + " has neither")
	}
	r := SeatResult{Path: filepath.Join(in.Dir, name)}
	if _, err := os.Stat(r.Path); errors.Is(err, fs.ErrNotExist) {
		return r, nil
	} else if err != nil {
		return r, err
	}
	if err := in.Unload(r.Path); err != nil {
		return r, fmt.Errorf("the unit at %s did not unload, and is kept: %w", r.Path, err)
	}
	if err := os.Remove(r.Path); err != nil {
		return r, err
	}
	r.Changed = true
	return r, nil
}
