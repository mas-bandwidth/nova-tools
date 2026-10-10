// Package units is the text and install of every unit a running sprint needs.
// A worker's binary may import it: it writes files and loads them through the
// caller's loader, and it does not open the sprint's store.
package units

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The labels seat install and friend sync install already write. They live here so the
// kind table and those verbs name one file, and so this package does not import
// internal/sprint (a worker's binary may import this package, and internal/sprint
// reaches the store).
const (
	SeatLabel         = "nova-sprint.seat-push"
	SeatService       = "nova-sprint-seat-push.service"
	FriendSyncLabel   = "nova-sprint.friend-sync"
	FriendSyncService = "nova-sprint-friend-sync.service"
)

// envName is a variable's name: what a *_ENV variable holds, never a value.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sdQuote is one word of a systemd command line or assignment: double-quoted, its
// backslashes and quotes escaped, and its specifiers (%) and variables ($) doubled.
func sdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

// Every unit a running sprint needs is written and loaded by a nova verb (card
// every-unit-installed-by-a-verb; the owner, 2026-10-05: "We cannot release a product,
// when parts of it needed to use it effectively are still locked up for you only"). A
// unit runs one nova verb directly, by the tool's absolute path: never under nova-secrets
// exec, a shell or a single-instance wrapper. A secret it needs is resolved in its own
// process (the store's through the seat login, nova-sprint seat login; the Redis
// server's through its --secret login, nova-redis serve), so the unit file, which anyone
// on the machine may read, holds none. The pattern is seat install's (seatinstall.go):
// a launchd agent on macOS and a systemd user unit on Linux, kept alive and started
// again at login, written whole into the caller's directory and loaded by the caller's
// loader, so a test writes into a directory of its own and loads nothing.

// UnitKind is one kind of unit a running sprint needs: its file names, the nova tool it
// runs, the words every such unit starts with after the tool, and the verb that installs it.
type UnitKind struct {
	Kind    string   `json:"kind"`           // the word the install verb takes
	Label   string   `json:"label"`          // the launchd label; its plist is Label.plist
	Service string   `json:"service"`        // the systemd user unit
	Tool    string   `json:"tool"`           // the nova binary the unit runs, by its base name
	Verb    []string `json:"verb"`           // the words after the binary every such unit starts with
	Install string   `json:"install"`        // the verb line that writes and loads it
	What    string   `json:"what"`           // one line: what the unit is
	Owed    string   `json:"owed,omitempty"` // what the install verb still waits on, "" when it is there
}

// UnitKinds is every unit a running sprint needs on its coordinator's machine, in the
// order a machine is brought up: the store and the bus, the server, its member, the
// seat's loops, then the machine's upkeep.
var UnitKinds = []UnitKind{
	{Kind: "store", Label: "nova-redis.store", Service: "nova-redis-store.service", Tool: "nova-redis", Verb: []string{"serve"},
		Install: "nova-redis install store", What: "the sprint's store, a Redis server (nova-redis serve)"},
	{Kind: "bus", Label: "nova-redis.bus", Service: "nova-redis-bus.service", Tool: "nova-redis", Verb: []string{"serve"},
		Install: "nova-redis install bus", What: "the friends' bus, a Redis server (nova-redis serve)"},
	{Kind: "server", Label: "nova-sprint.server", Service: "nova-sprint-server.service", Tool: "nova-sprint", Verb: []string{"run", "--listen"},
		Install: "nova-sprint install server", What: "the sprint's server and its loop (nova-sprint run --listen)"},
	{Kind: "member", Label: "nova-sprint.member", Service: "nova-sprint-member.service", Tool: "nova-swarm", Verb: []string{"member"},
		Install: "nova-sprint install member", What: "this machine's fleet member (nova-swarm member)"},
	{Kind: "seat-push", Label: SeatLabel, Service: SeatService, Tool: "nova-sprint", Verb: []string{"inbox", "--wait", "--push", "seat"},
		Install: "nova-sprint install seat-push", What: "the seat's push loop (nova-sprint inbox --wait --push seat)"},
	{Kind: "friend-sync", Label: FriendSyncLabel, Service: FriendSyncService, Tool: "nova-sprint", Verb: []string{"friend", "sync", "--every"},
		Install: "nova-sprint install friend-sync", What: "the friend sync loop (nova-sprint friend sync --every)"},
	{Kind: "table", Label: "nova-sprint.table", Service: "nova-sprint-table.service", Tool: "nova-sprint", Verb: []string{"where", "--watch"},
		Install: "nova-sprint install table", What: "the live sprint table written to a file (nova-sprint where --watch)"},
	{Kind: "disk-guard", Label: "nova-swarm.disk-guard", Service: "nova-swarm-disk-guard.service", Tool: "nova-swarm", Verb: []string{"disk-guard"},
		Install: "nova-swarm install disk-guard", What: "the machine's disk upkeep, one pass every --every (nova-swarm disk-guard)"},
	{Kind: "mirror-refresh", Label: "nova-swarm.mirror-refresh", Service: "nova-swarm-mirror-refresh.service", Tool: "nova-swarm", Verb: []string{"mirror"},
		Install: "nova-swarm install mirror-refresh", What: "the bench's repository mirrors kept fresh (nova-swarm mirror)",
		Owed: "nova-swarm has no mirror verb for the unit to run"},
}

// UnitKindOf is the kind named k.
func UnitKindOf(k string) (UnitKind, bool) {
	for _, u := range UnitKinds {
		if u.Kind == k {
			return u, true
		}
	}
	return UnitKind{}, false
}

// UnitKindNames is every kind's word whose install verb is tool's, in UnitKinds' order.
func UnitKindNames(tool string) []string {
	var out []string
	for _, u := range UnitKinds {
		if strings.HasPrefix(u.Install, tool+" ") {
			out = append(out, u.Kind)
		}
	}
	return out
}

// File is the kind's unit file name on goos, "" where there is no service manager a
// unit is installed under.
func (k UnitKind) File(goos string) string {
	switch goos {
	case "darwin":
		return k.Label + ".plist"
	case "linux":
		return k.Service
	}
	return ""
}

// runs is the verb line every unit of the kind runs, the tool by its base name.
func (k UnitKind) runs() string { return strings.Join(append([]string{k.Tool}, k.Verb...), " ") }

// ServiceUnit is one unit of a kind as a service: its command line, the binary first by its
// absolute path; its environment, names and addresses and never a secret; the file its
// lines go to (launchd; systemd keeps them in its journal); and Every, the least time
// between two starts, so a verb that does one pass and exits runs once every Every
// (launchd's ThrottleInterval, systemd's RestartSec; 0 is 10 s).
type ServiceUnit struct {
	Kind  UnitKind
	OS    string
	Args  []string
	Env   [][2]string
	Log   string
	Every time.Duration
}

// secretWords mark an environment variable that may hold a secret: a unit carries the
// name of the variable a secret is in (a *_ENV variable), never one that is the secret.
var secretWords = []string{"PASSWORD", "SECRET", "TOKEN", "API_KEY", "_KEY"}

// refusal is why the unit is not written, "" is it may be.
func (u ServiceUnit) refusal() string {
	k := u.Kind
	if k.File(u.OS) == "" {
		return k.Install + " installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on " + orDash(u.OS) + "; run the verb by hand: " + k.runs()
	}
	if len(u.Args) == 0 || !filepath.IsAbs(u.Args[0]) {
		first := ""
		if len(u.Args) > 0 {
			first = u.Args[0]
		}
		return "the unit runs " + k.Tool + " by its absolute path, not " + orDash(first)
	}
	if base := filepath.Base(u.Args[0]); base != k.Tool {
		return "the unit runs " + k.Tool + " itself, never a wrapper around it, and " + base + " is not " + k.Tool
	}
	if !hasPrefix(u.Args[1:], k.Verb) {
		return "a " + k.Kind + " unit runs " + k.runs() + ", not " + strings.Join(append([]string{k.Tool}, u.Args[1:]...), " ")
	}
	if u.Every < 0 {
		return "--every is the least time between two starts, zero or more, not " + u.Every.String()
	}
	for _, kv := range u.Env {
		if !envName.MatchString(kv[0]) {
			return "the unit's environment names " + orDash(kv[0]) + ", which is no variable's name"
		}
		up := strings.ToUpper(kv[0])
		if strings.HasSuffix(up, "_ENV") {
			if !envName.MatchString(kv[1]) {
				return kv[0] + " does not name a variable (letters, digits and underscores only), and the unit carries no secret"
			}
			continue
		}
		for _, w := range secretWords {
			if strings.Contains(up, w) {
				return "the unit carries no secret, and " + kv[0] + " is one; the tool reads it in its own process (nova-sprint seat login, nova-redis serve --secret)"
			}
		}
	}
	return ""
}

func hasPrefix(words, prefix []string) bool {
	if len(words) < len(prefix) {
		return false
	}
	for i, w := range prefix {
		if words[i] != w {
			return false
		}
	}
	return true
}

// throttle is the least time between two starts in whole seconds, 10 when Every is 0.
func (u ServiceUnit) throttle() int {
	if u.Every <= 0 {
		return 10
	}
	s := int((u.Every + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// Text is the unit's file: a launchd plist, or a systemd user unit.
func (u ServiceUnit) Text() (string, error) {
	if why := u.refusal(); why != "" {
		return "", errors.New(why)
	}
	if u.OS == "linux" {
		return SystemdUnit("nova "+u.Kind.Kind+": "+u.Kind.What, u.Args, u.Env, u.throttle()), nil
	}
	return LaunchdPlist(u.Kind.Label, u.Args, u.Env, u.Log, u.throttle()), nil
}

// launchdPlistEvery is a launchd agent kept alive that runs args with env, its lines
// to log, started again no sooner than every throttle seconds.
func LaunchdPlist(label string, args []string, env [][2]string, log string, throttle int) string {
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
	str("\t", label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range args {
		str("\t\t", a)
	}
	b.WriteString("\t</array>\n")
	if len(env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, kv := range env {
			b.WriteString("\t\t<key>" + kv[0] + "</key>\n")
			str("\t\t", kv[1])
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n\t<key>KeepAlive</key>\n\t<true/>\n\t<key>ThrottleInterval</key>\n\t<integer>" + strconv.Itoa(throttle) + "</integer>\n")
	if log != "" {
		b.WriteString("\t<key>Standard" + "OutPath</key>\n")
		str("\t", log)
		b.WriteString("\t<key>Standard" + "ErrorPath</key>\n")
		str("\t", log)
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// systemdUnitEvery is a systemd user unit restarted always, no sooner than every
// restart seconds, that runs args with env.
func SystemdUnit(description string, args []string, env [][2]string, restart int) string {
	var words []string
	for _, a := range args {
		words = append(words, sdQuote(a))
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=" + description + "\n")
	if restart > 10 {
		// a pass a long time apart is no restart loop: never stop starting it
		b.WriteString("StartLimitIntervalSec=0\n")
	}
	b.WriteString("\n[Service]\n")
	b.WriteString("ExecStart=" + strings.Join(words, " ") + "\n")
	for _, kv := range env {
		b.WriteString("Environment=" + sdQuote(kv[0]+"="+kv[1]) + "\n")
	}
	b.WriteString("Restart=always\nRestartSec=" + strconv.Itoa(restart) + "\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// Result is what install or uninstall did: the unit's path, and whether the file
// there was written or removed.
type Result struct {
	Path    string
	Changed bool
}

// Installer writes units into Dir. Load loads (or loads again) the unit at a path
// and Unload unloads it. Both are the caller's, so a test loads nothing on the host.
type Installer struct {
	Dir          string
	Load, Unload func(path string) error
}

// Write writes text at path (whole, beside it first, then renamed into place)
// unless the file there is already this text, and loads it either way, so an install
// after the unit was unloaded by hand starts it again. A unit whose load fails stays
// written; the error says the load.
func (in Installer) Write(path, text string) (Result, error) {
	r := Result{Path: path}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return r, err
	}
	if !bytes.Equal(old, []byte(text)) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return r, err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			return r, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return r, err
		}
		r.Changed = true
	}
	if err := in.Load(path); err != nil {
		return r, fmt.Errorf("the unit is written at %s and did not load: %w", path, err)
	}
	return r, nil
}

// removeUnit unloads the unit at path and removes it; with no file there it does
// nothing, and says so by Changed false.
func (in Installer) Remove(path string) (Result, error) {
	r := Result{Path: path}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return r, nil
	} else if err != nil {
		return r, err
	}
	if err := in.Unload(path); err != nil {
		return r, fmt.Errorf("the unit at %s did not unload, and is kept: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return r, err
	}
	r.Changed = true
	return r, nil
}

// InstallUnit writes the unit into the installer's directory unless the file there is
// already this text, and loads it either way, as Install does the push loop's.
func (in Installer) Install(u ServiceUnit) (Result, error) {
	text, err := u.Text()
	if err != nil {
		return Result{}, err
	}
	return in.Write(filepath.Join(in.Dir, u.Kind.File(u.OS)), text)
}

// UninstallUnit unloads the kind's unit and removes its file; with no file there it
// does nothing, and says so by Changed false.
func (in Installer) Uninstall(k UnitKind, goos string) (Result, error) {
	name := k.File(goos)
	if name == "" {
		return Result{}, errors.New(strings.Replace(k.Install, "install", "uninstall", 1) + " removes a launchd agent (macOS) or a systemd user unit (Linux), and " + orDash(goos) + " has neither")
	}
	return in.Remove(filepath.Join(in.Dir, name))
}

// The states units --check gives a unit a running sprint needs.
const (
	UnitInstalled = "installed" // the kind's file is there and runs the kind's verb itself
	UnitMissing   = "missing"   // no file of the kind is there
	UnitDifferent = "different" // a file is there that runs something else, or is no unit
)

// UnitState is one needed unit as units --check found it: its kind, the file it is
// looked for at, its state, why it is different, the verb that installs it, and what
// that verb still waits on when it is not there yet.
type UnitState struct {
	Kind    string `json:"kind"`
	Owed    string `json:"owed,omitempty"`
	Path    string `json:"path"`
	State   string `json:"state"`
	Why     string `json:"why,omitempty"`
	Install string `json:"install"`
}

// CheckUnits looks for every kind's unit in dir on goos: missing when no file is
// there, different when the file there does not run the kind's tool itself with the
// kind's verb (a nova-secrets exec, a shell, a wrapper, another verb, a file that is no
// unit), installed when it does. It reads files only: nothing is loaded or changed.
func CheckUnits(dir, goos string, kinds []UnitKind) ([]UnitState, error) {
	var out []UnitState
	for _, k := range kinds {
		name := k.File(goos)
		if name == "" {
			return nil, errors.New("units --check reads launchd agents (macOS) or systemd user units (Linux), and " + orDash(goos) + " has neither")
		}
		s := UnitState{Kind: k.Kind, Path: filepath.Join(dir, name), Install: k.Install, Owed: k.Owed}
		b, err := os.ReadFile(s.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			s.State = UnitMissing
		case err != nil:
			return nil, err
		default:
			s.State, s.Why = UnitInstalled, ""
			if why := k.differs(goos, b); why != "" {
				s.State, s.Why = UnitDifferent, why
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// differs is why the unit text b is not the kind's, "" when it is.
func (k UnitKind) differs(goos string, b []byte) string {
	args, err := UnitArgs(goos, b)
	if err != nil {
		return "it is no unit this tool reads: " + err.Error()
	}
	if len(args) == 0 {
		return "it runs nothing"
	}
	if base := filepath.Base(args[0]); base != k.Tool {
		return "it runs " + base + ", not " + k.runs() + " itself"
	}
	if !hasPrefix(args[1:], k.Verb) {
		return "it runs " + strings.Join(append([]string{k.Tool}, args[1:]...), " ") + ", not " + k.runs()
	}
	return ""
}

// UnitArgs is the command line a unit file runs: a launchd plist's ProgramArguments,
// or a systemd unit's ExecStart words.
func UnitArgs(goos string, b []byte) ([]string, error) {
	if goos == "linux" {
		return systemdArgs(string(b))
	}
	return plistArgs(b)
}

// plistArgs is the strings of the array after the top dictionary's ProgramArguments key.
func plistArgs(b []byte) ([]string, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var (
		depth     int
		key, text string
		inArgs    bool
		args      []string
		found     bool
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the plist does not parse: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			text = ""
			if t.Name.Local == "array" && depth == 3 && key == "ProgramArguments" {
				inArgs, found = true, true
			}
		case xml.CharData:
			text += string(t)
		case xml.EndElement:
			switch {
			case t.Name.Local == "key" && depth == 3:
				key = strings.TrimSpace(text)
			case t.Name.Local == "string" && inArgs && depth == 4:
				args = append(args, text)
			case t.Name.Local == "array" && inArgs && depth == 3:
				inArgs = false
			}
			if depth == 3 && t.Name.Local != "key" {
				key = ""
			}
			depth--
		}
	}
	if !found {
		return nil, errors.New("the plist has no ProgramArguments")
	}
	return args, nil
}

// systemdArgs is the words of the unit's ExecStart line, unquoted as systemd does
// (sdQuote's inverse).
func systemdArgs(text string) ([]string, error) {
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart=")
		if !ok {
			continue
		}
		return sdSplit(rest)
	}
	return nil, errors.New("the unit has no ExecStart line")
}

func sdSplit(s string) ([]string, error) {
	var (
		out  []string
		cur  strings.Builder
		in   bool
		have bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case in && c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '"':
			in, have = !in, true
		case !in && (c == ' ' || c == '\t'):
			if have || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		case (c == '%' || c == '$') && i+1 < len(s) && s[i+1] == c:
			i++
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	if in {
		return nil, errors.New("the ExecStart line has a quote left open")
	}
	if have || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}
