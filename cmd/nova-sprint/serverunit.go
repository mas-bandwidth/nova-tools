package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"text/template"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// server install (docs/SPEC-SPRINT.md, "The server", install-server-unit-by-verb-r.w1):
// the sprint server, run --listen, as the tool's own unit on the machine the verb is
// typed on, in place of a unit written and edited by hand. The unit names no actor
// (the seat is read from its record, "Handing over the seat"), reaches the store's
// credentials only through nova-secrets exec --only, which names key names and never a
// value, and is recorded by the hash of its bytes beside it, so a unit edited by hand
// is a drift the next install refuses with the diff.

func init() {
	notServed = append(notServed, "server install")
	verbClasses["server install"] = classMachine
}

// serverLabel is the server's launchd label; serverService its systemd user unit.
const (
	serverLabel   = "nova-sprint.server"
	serverService = "nova-sprint-server.service"
)

// serverUnitFile is the unit's file name on goos, "" where there is no service manager.
func serverUnitFile(goos string) string {
	switch goos {
	case "darwin":
		return serverLabel + ".plist"
	case "linux":
		return serverService
	}
	return ""
}

// serverUnit is what the unit is rendered from: the command line, nova-secrets exec
// first, and the environment, which holds addresses and key names, never a secret.
type serverUnit struct {
	Label, Log string
	Args       []string
	Env        [][2]string
}

var keyName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func xmlText(s string) string {
	var b strings.Builder
	// ignored: a strings.Builder never fails a write
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// sdWord is one word of a systemd line: double-quoted, its backslashes and quotes
// escaped, its specifiers (%) and variables ($) doubled.
func sdWord(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

// The unit's templates, the shape of fleet/templates/nova-loop.*.j2.
var serverUnitTemplates = map[string]*template.Template{
	"darwin": template.Must(template.New("plist").Funcs(template.FuncMap{"x": xmlText}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- written by nova-sprint server install: an edit here is a drift; change the verb's flags and install again -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{x .Label}}</string>
	<key>ProgramArguments</key>
	<array>
{{- range .Args}}
		<string>{{x .}}</string>
{{- end}}
	</array>
	<key>EnvironmentVariables</key>
	<dict>
{{- range .Env}}
		<key>{{x (index . 0)}}</key>
		<string>{{x (index . 1)}}</string>
{{- end}}
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>{{x .Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{x .Log}}</string>
</dict>
</plist>
`)),
	"linux": template.Must(template.New("systemd").Funcs(template.FuncMap{"q": sdWord}).Parse(`# written by nova-sprint server install: an edit here is a drift; change the verb's flags and install again
[Unit]
Description=nova-sprint server (run --listen)
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart={{range $i, $a := .Args}}{{if $i}} {{end}}{{q $a}}{{end}}
{{- range .Env}}
Environment={{q (print (index . 0) "=" (index . 1))}}
{{- end}}
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`)),
}

func (u serverUnit) render(goos string) string {
	var b strings.Builder
	// ignored: the templates are parsed at start and every value is a string
	_ = serverUnitTemplates[goos].Execute(&b, u)
	return b.String()
}

func unitHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// handEdit is why the unit on disk is not the one this verb last wrote: "" is it is
// (or there is none). A unit with no recorded hash was written by hand.
func handEdit(path string) (old []byte, why string, err error) {
	old, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	} else if err != nil {
		return nil, "", err
	}
	rec, err := os.ReadFile(path + ".sha256")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return old, "the unit at " + path + " has no recorded hash: it was written by hand", nil
	case err != nil:
		return nil, "", err
	case strings.TrimSpace(string(rec)) != unitHash(old):
		return old, "the unit at " + path + " is not the one server install wrote (its bytes differ from the recorded hash): it was edited by hand", nil
	}
	return old, "", nil
}

// secretLike is an environment name whose value may be a secret; a name ending _ENV
// names another variable and is shown.
var (
	secretLike = regexp.MustCompile(`(?i)(PASS|SECRET|TOKEN|_KEY)[A-Z0-9_]*$`)
	plistValue = regexp.MustCompile(`<string>.*</string>`)
)

// redact hides every value a unit gives a secret-like name (a plist's key and the
// string after it; NAME=value anywhere), so a diff never prints a secret's value.
func redact(text string) []string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	hide := false
	for i, l := range lines {
		if hide && strings.Contains(l, "<string>") {
			lines[i] = plistValue.ReplaceAllString(l, "<string><redacted></string>")
		}
		hide = false
		if k, ok := strings.CutPrefix(strings.TrimSpace(l), "<key>"); ok {
			k = strings.TrimSuffix(k, "</key>")
			hide = secretLike.MatchString(k) && !strings.HasSuffix(k, "_ENV")
		}
		lines[i] = redactAssignments(lines[i])
	}
	return lines
}

// redactAssignments hides the value after every NAME= whose NAME is secret-like, the
// name being the word just before its =, so Environment=NAME=value hides the value.
func redactAssignments(l string) string {
	var b strings.Builder
	for {
		at := strings.IndexByte(l, '=')
		if at < 0 {
			return b.String() + l
		}
		k := l[:at]
		k = k[strings.LastIndexFunc(k, func(r rune) bool { return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) })+1:]
		b.WriteString(l[:at+1])
		l = l[at+1:]
		if secretLike.MatchString(k) && !strings.HasSuffix(k, "_ENV") {
			end := strings.IndexFunc(l, func(r rune) bool { return r == '"' || unicode.IsSpace(r) })
			if end < 0 {
				end = len(l)
			}
			b.WriteString("<redacted>")
			l = l[end:]
		}
	}
}

// lineDiff is the unit on disk against the one rendered, a line each: "-" on disk
// only, "+" rendered only (the longest common subsequence kept as it is, unprinted).
func lineDiff(old, now string) []string {
	a, b := redact(old), redact(now)
	n := make([][]int, len(a)+1)
	for i := range n {
		n[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				n[i][j] = n[i+1][j+1] + 1
			} else {
				n[i][j] = max(n[i+1][j], n[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || n[i][j+1] >= n[i+1][j]):
			out = append(out, "+"+b[j])
			j++
		default:
			out = append(out, "-"+a[i])
			i++
		}
	}
	return out
}

// writeWhole writes b at path beside it first, then renames it into place.
func writeWhole(path string, b []byte) error {
	if err := os.WriteFile(path+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (a *app) cmdServerInstall(args []string, stdout, stderr io.Writer) int {
	const name = "server install"
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := fs.String("log", "", "the file the server's lines go to, macOS (default: ~/Library/Logs/nova-sprint-server.log); on Linux they are in the journal")
	dry := fs.Bool("dry-run", false, "print the unit, where it would go and what the install would do, and write and load nothing")
	replace := fs.Bool("replace-hand-edit", false, "install over a unit edited by hand (or written by hand: no recorded hash); without it that install is refused with the diff")
	listen := fs.String("listen", "", "the server's address, <address:port> on the fleet's private network (run --listen)")
	land := fs.Bool("land", false, "the server lands what the readers passed (run --land)")
	decide := fs.String("decide", "", "the directory of nova-decide's record (run --decide <dir>)")
	user := fs.String("redis-user", "", "the store's Redis user, carried as NOVA_SPRINT_REDIS_USER (default: none)")
	pwEnv := fs.String("password-env", "NOVA_REDIS_PASSWORD", "the key name of the store's password in nova-secrets; the unit names it, never its value")
	only := fs.String("only", "", "further key names nova-secrets exec passes, comma-separated (JEV_API_KEY for --decide); names only")
	sStore := fs.String("secrets-store", "", "nova-secrets exec --store: the secrets store directory")
	sAs := fs.String("secrets-as", "", "nova-secrets exec --as: the seat the secrets are opened as")
	sKey := fs.String("secrets-key", "", "nova-secrets exec --key: the age key file's path")
	sops := fs.String("sops", "", "nova-secrets exec --sops: the sops binary's path")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	goos := a.seatOS()
	redis := strings.TrimSpace(c.redis)
	var bad []string
	if serverUnitFile(goos) == "" {
		bad = append(bad, "server install installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on "+goos)
	}
	if *listen == "" {
		bad = append(bad, "--listen <address:port> is required: the address the server listens on")
	}
	if redis == "" || strings.HasPrefix(redis, "mem:") {
		bad = append(bad, "--redis <host:port> (or NOVA_SPRINT_REDIS) is required and is a Redis store, not "+dashed(redis))
	}
	names := []string{*pwEnv}
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" && n != *pwEnv {
			names = append(names, n)
		}
	}
	for _, n := range names {
		if !keyName.MatchString(n) {
			bad = append(bad, "--password-env and --only take key names (A-Z, 0-9, _), never a value: "+oneline.Quote(n)+" is not one")
		}
	}
	paths := map[string]*string{"--secrets-store": sStore, "--secrets-key": sKey, "--sops": sops}
	for _, f := range []string{"--secrets-store", "--secrets-key", "--sops"} {
		if *paths[f] == "" {
			bad = append(bad, f+" <path> is required: nova-secrets exec opens the store's credentials with it")
		} else if *paths[f], err = filepath.Abs(*paths[f]); err != nil {
			bad = append(bad, f+": "+err.Error())
		}
	}
	if *sAs == "" {
		bad = append(bad, "--secrets-as <seat> is required: the seat nova-secrets exec opens the secrets as")
	}
	if len(bad) > 0 {
		return refuse(stderr, name, strings.Join(bad, "; ")+"; nothing was written")
	}
	exe := a.executable
	if exe == nil {
		exe = os.Executable
	}
	bin, err := exe()
	if err != nil {
		return refuse(stderr, name, "the path of this nova-sprint cannot be read: "+err.Error()+"; nothing was written")
	}
	if *dir == "" {
		if *dir, err = a.seatDir(goos); err != nil {
			return refuse(stderr, name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
	}
	if *logf == "" && goos == "darwin" {
		home, err := a.home()
		if err != nil {
			return refuse(stderr, name, "--log names no file and the home cannot be read: "+err.Error())
		}
		*logf = filepath.Join(home, "Library", "Logs", "nova-sprint-server.log")
	}
	u := serverUnit{Label: serverLabel, Log: *logf,
		Args: []string{filepath.Join(filepath.Dir(bin), "nova-secrets"), "exec", "--store", *sStore, "--as", *sAs, "--key", *sKey, "--sops", *sops,
			"--only", strings.Join(names, ","), "--require", *pwEnv, "--", bin, "run", "--listen", *listen}}
	if *land {
		u.Args = append(u.Args, "--land")
	}
	if *decide != "" {
		u.Args = append(u.Args, "--decide", *decide)
	}
	if p := a.getenv("PATH"); p != "" {
		u.Env = append(u.Env, [2]string{"PATH", p})
	}
	u.Env = append(u.Env, [2]string{"NOVA_SPRINT_REDIS", redis})
	if *user != "" {
		u.Env = append(u.Env, [2]string{"NOVA_SPRINT_REDIS_USER", *user})
	}
	u.Env = append(u.Env, [2]string{"NOVA_SPRINT_REDIS_PASSWORD_ENV", *pwEnv})
	text := u.render(goos)
	path := filepath.Join(*dir, serverUnitFile(goos))
	old, why, err := handEdit(path)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: the unit at %s cannot be read: %s; nothing was written; run: ls -l %s (its permissions), then nova-sprint server install again\n", prog, name, oneline.Field(path), oneline.Escape(err.Error()), oneline.Field(path))
		return 1
	}
	plan := "write"
	switch {
	case why != "" && !*replace:
		plan = "refuse"
	case why == "" && bytes.Equal(old, []byte(text)):
		plan = "keep"
	}
	var diff []string
	if why != "" {
		diff = lineDiff(string(old), text)
	}
	if *dry {
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "unit": text, "hash": unitHash([]byte(text)), "plan": plan, "hand_edit": why, "diff": diff, "dry_run": true}) // ignored: strings always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "SERVER INSTALL DRY-RUN unit=%s plan=%s; nothing was written or loaded\n", oneline.Field(path), plan)
		if why != "" {
			fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(why))
			for _, l := range diff {
				fmt.Fprintln(stdout, "  "+l)
			}
		}
		fmt.Fprint(stdout, text)
		return 0
	}
	if plan == "refuse" {
		fmt.Fprintf(stderr, "%s %s REFUSED: %s; nothing was written; run: nova-sprint server install --replace-hand-edit (with the same flags) to replace it, or carry the edit into the flags\n", prog, name, oneline.Escape(why))
		for _, l := range diff {
			fmt.Fprintln(stderr, "  "+l)
		}
		return 1
	}
	if plan == "write" {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
			return 1
		}
		if err := writeWhole(path, []byte(text)); err != nil {
			fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
			return 1
		}
		if err := writeWhole(path+".sha256", []byte(unitHash([]byte(text))+"\n")); err != nil {
			fmt.Fprintf(stderr, "%s %s FAILED: the unit is written and its hash is not: %s\n", prog, name, oneline.Escape(err.Error()))
			return 1
		}
	}
	load := a.seatLoad
	if load == nil {
		load = loadServerUnit
	}
	if err := load(goos, "load", path); err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: the unit is written at %s and did not load: %s\n", prog, name, oneline.Field(path), oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"path": path, "hash": unitHash([]byte(text)), "written": plan == "write", "replaced_hand_edit": why != "", "loaded": true}) // ignored: strings and bools always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "SERVER INSTALL OK unit=%s written=%t replaced-hand-edit=%t loaded=true\n", oneline.Field(path), plan == "write", why != "")
	fmt.Fprintf(stdout, "  runs: %s\n", strings.Join(u.Args, " "))
	return 0
}

// loadServerUnit loads the server's unit on this machine (unloaded first, so a changed
// unit is read again): launchctl in the user's gui domain on macOS, systemctl --user on
// Linux. Under NOVA_TEST_NO_HOST it refuses: a test gives its own loader.
func loadServerUnit(goos, op, path string) error {
	if os.Getenv("NOVA_TEST_NO_HOST") != "" {
		return errors.New("NOVA_TEST_NO_HOST is set: no service is loaded or unloaded on this machine")
	}
	run := func(name string, args ...string) error {
		cmd, cancel := subproc.Command(context.Background(), subproc.Tool, name, args...)
		defer cancel()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if goos == "linux" {
		for _, args := range [][]string{{"daemon-reload"}, {"enable", serverService}, {"restart", serverService}} {
			if err := run("systemctl", append([]string{"--user"}, args...)...); err != nil {
				return err
			}
		}
		return nil
	}
	service := "gui/" + strconv.Itoa(os.Getuid()) + "/" + serverLabel
	if run("launchctl", "print", service) == nil {
		if err := run("launchctl", "bootout", service); err != nil {
			return err
		}
	}
	return run("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), path)
}
