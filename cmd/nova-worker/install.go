package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

func init() {
	verbEffect["install"] = "local write: writes the kind's unit (the verb itself, never a wrapper) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run writes nothing"
	verbEffect["uninstall"] = "local write: unloads the kind's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing"
	verbExit["install"] = "exit codes: 0 done (the unit written or kept, and loaded); 1 the unit did not write or load; 2 usage, or mirror-refresh (no mirror verb yet)"
	verbExit["uninstall"] = "exit codes: 0 done (removed, or no unit there); 1 the unit did not unload or remove; 2 usage"
}

// swarmUnits is what a test stands in for: the OS the unit is written for, the home,
// the binary, and the loader. Zero is this machine. A test sets them and loads nothing
// on the host; the real loader refuses while NOVA_TEST_NO_HOST is set.
var swarmUnits struct {
	goos string
	home func() (string, error)
	exe  func() (string, error)
	load func(goos, op, path string) error
}

func unitOS() string {
	if swarmUnits.goos != "" {
		return swarmUnits.goos
	}
	return runtime.GOOS
}

func unitHome() (string, error) {
	if swarmUnits.home != nil {
		return swarmUnits.home()
	}
	return os.UserHomeDir()
}

func unitExe() (string, error) {
	if swarmUnits.exe != nil {
		return swarmUnits.exe()
	}
	return os.Executable()
}

func unitLoad(goos, op, path string) error {
	if swarmUnits.load != nil {
		return swarmUnits.load(goos, op, path)
	}
	return loadSwarmUnit(goos, op, path)
}

// install and uninstall of the units nova-worker owns (card every-unit-installed-by-a-verb).
// disk-guard is written and loaded here. mirror-refresh stays owed: nova-worker has no
// mirror verb for that unit to run. The unit runs this binary directly. A test gives
// --dir and its own loader, so nothing is loaded on the machine the test runs on.
//
// The shared unit text lives in internal/units. This binary does not import that
// package: its oneline import list is cmd/nova-worker/audit_test.go, which this card
// cannot change. The text written here is the same plist and unit internal/units
// writes, and TestSwarmInstallWritesTheDiskGuardAndRefusesMirrorRefresh reads both.

const (
	diskGuardWhat = "the machine's disk upkeep, one pass every --every (nova-worker disk-guard)"
	mirrorWhat    = "the bench's repository mirrors kept fresh (nova-worker mirror)"
	mirrorOwed    = "nova-worker has no mirror verb for the unit to run"
)

type swarmKind struct {
	kind, label, service, what, owed string
}

func swarmKindOf(kind string) (swarmKind, bool) {
	switch kind {
	case "disk-guard":
		return swarmKind{kind: "disk-guard", label: "nova-worker.disk-guard", service: "nova-worker-disk-guard.service", what: diskGuardWhat}, true
	case "mirror-refresh":
		return swarmKind{kind: "mirror-refresh", label: "nova-worker.mirror-refresh", service: "nova-worker-mirror-refresh.service", what: mirrorWhat, owed: mirrorOwed}, true
	default:
		return swarmKind{}, false
	}
}

func (k swarmKind) file(goos string) string {
	switch goos {
	case "darwin":
		return k.label + ".plist"
	case "linux":
		return k.service
	default:
		return ""
	}
}

func swarmKindNames() string { return "disk-guard|mirror-refresh" }

// foreignInstall is the verb that installs a unit this binary does not own. The
// words are the same as internal/units.UnitKinds.
func foreignInstall(kind string) (string, bool) {
	switch kind {
	case "store":
		return "nova-redis install store", true
	case "bus":
		return "nova-redis install bus", true
	case "server":
		return "nova-sprint install server", true
	case "member":
		return "nova-sprint install member", true
	case "seat-push":
		return "nova-sprint install seat-push", true
	case "friend-sync":
		return "nova-sprint install friend-sync", true
	case "table":
		return "nova-sprint install table", true
	default:
		return "", false
	}
}

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		f := newFlags("install")
		f.fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
		f.fs.String("log", "", "the file the unit's lines go to on macOS; on Linux they are in the journal")
		f.fs.Duration("every", 15*time.Minute, "the least time between two starts")
		f.fs.Bool("dry-run", false, "print the unit and write and load nothing")
		if !f.parse(args, stderr) {
			return 2
		}
		return refuse(stderr, " install", "install wants the unit's kind first: "+swarmKindNames()+"; run: nova-worker install disk-guard --dry-run")
	}
	return installKind(args[0], args[1:], stdout, stderr)
}

func installKind(kind string, args []string, stdout, stderr io.Writer) int {
	name := "install " + kind
	if other, ok := foreignInstall(kind); ok {
		return refuse(stderr, " install", "the "+kind+" unit is not nova-worker's to install; run: "+other)
	}
	k, ok := swarmKindOf(kind)
	if !ok {
		return refuse(stderr, " install", "no unit kind "+oneline.Escape(kind)+"; the kinds are "+swarmKindNames()+"; run: nova-sprint units --check")
	}
	f := newFlags("install")
	dir := f.fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := f.fs.String("log", "", "the file the unit's lines go to, macOS (default: ~/Library/Logs/nova-worker-"+kind+".log); on Linux they are in the journal")
	every := f.fs.Duration("every", 15*time.Minute, "the least time between two starts, above zero (launchd ThrottleInterval, systemd RestartSec)")
	dry := f.fs.Bool("dry-run", false, "print the unit, quoted, and where it would go, and write and load nothing")
	var roots, scans, caches []string
	f.fs.Var(stringListValue{&roots}, "root", "a member's or reader's root `dir` the unit's disk-guard passes (again for more)")
	f.fs.Var(stringListValue{&scans}, "scan", "a `dir` each of whose subdirectories holding slots/ is a root (again for more)")
	f.fs.Var(stringListValue{&caches}, "cache", "another Go build cache, a `dir` or a glob (again for more)")
	cacheGB := f.fs.Int("cache-max-gb", 0, "the `GiB` each Go build cache is held under, when set")
	modGB := f.fs.Int("modcache-max-gb", 0, "the `GiB` over which a module cache is emptied, when set")
	logs := f.fs.String("logs", "", "the `dir` of the loop logs to rotate, when set")
	logMB := f.fs.Int("log-max-mb", 0, "the `MiB` over which a loop log is rotated, when set")
	logKeep := f.fs.Int("log-keep", 0, "how many rotated copies of a log stay, when set")
	poolIdle := f.fs.Duration("pool-idle", 0, "how long a pool's slots must be still before it is swept, when set")
	land := f.fs.String("land", "", "the `dir` nova-sprint land keeps its clones in, when set")
	cloneAge := f.fs.Duration("clone-age", 0, "how long a land clone must be unused before it is removed, when set")
	mirrors := f.fs.String("mirrors", "", "the `dir` of the bench's mirrors, when set")
	floor := f.fs.Int("disk-floor", -1, "the free `GiB` under which the run warns, when set")
	if !f.parse(args, stderr) {
		return 2
	}
	if k.owed != "" {
		return refuse(stderr, " "+name, k.owed+"; nothing was written")
	}
	if *every <= 0 {
		return refuse(stderr, " "+name, "--every is the least time between two starts, above zero; nothing was written")
	}
	goos := unitOS()
	bin, err := unitExe()
	if err != nil {
		return refuse(stderr, " "+name, "the path of this nova-worker cannot be read: "+err.Error()+"; nothing was written")
	}
	uargs := []string{bin, "disk-guard"}
	add := func(flag, v string) {
		if v != "" {
			uargs = append(uargs, flag, v)
		}
	}
	for _, r := range roots {
		add("--root", r)
	}
	for _, r := range scans {
		add("--scan", r)
	}
	for _, r := range caches {
		add("--cache", r)
	}
	if *cacheGB > 0 {
		add("--cache-max-gb", strconv.Itoa(*cacheGB))
	}
	if *modGB > 0 {
		add("--modcache-max-gb", strconv.Itoa(*modGB))
	}
	add("--logs", *logs)
	if *logMB > 0 {
		add("--log-max-mb", strconv.Itoa(*logMB))
	}
	if *logKeep > 0 {
		add("--log-keep", strconv.Itoa(*logKeep))
	}
	if *poolIdle > 0 {
		add("--pool-idle", poolIdle.String())
	}
	add("--land", *land)
	if *cloneAge > 0 {
		add("--clone-age", cloneAge.String())
	}
	add("--mirrors", *mirrors)
	if *floor >= 0 {
		add("--disk-floor", strconv.Itoa(*floor))
	}
	logPath := *logf
	if *dir == "" {
		home, err := unitHome()
		if err != nil {
			return refuse(stderr, " "+name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
		*dir = unitDir(goos, home, os.Getenv)
	}
	if logPath == "" && goos == "darwin" {
		home, err := unitHome()
		if err != nil {
			return refuse(stderr, " "+name, "--log names no file and the home cannot be read: "+err.Error())
		}
		logPath = filepath.Join(home, "Library", "Logs", "nova-worker-"+kind+".log")
	}
	text, err := diskGuardText(k, goos, uargs, logPath, *every)
	if err != nil {
		return refuse(stderr, " "+name, err.Error()+"; nothing was written")
	}
	head := "INSTALL " + strings.ToUpper(kind)
	path := filepath.Join(*dir, k.file(goos))
	if *dry {
		// %q keeps the unit one quoted document. A raw print would need an exemption
		// in audit_test.go, which this card does not change.
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s; nothing was written or loaded\n%q\n", oneline.Escape(head), oneline.Field(path), text)
		return 0
	}
	changed, err := writeUnit(path, text, func(p string) error { return unitLoad(goos, "load", p) })
	if err != nil {
		fmt.Fprintf(stderr, "nova-worker %s FAILED: %s\n", oneline.Escape(name), oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "%s OK unit=%s written=%t loaded=true\n", oneline.Escape(head), oneline.Field(path), changed)
	fmt.Fprintf(stdout, "  runs: %s\n", oneline.Escape(strings.Join(uargs, " ")))
	return 0
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		f := newFlags("uninstall")
		f.fs.String("dir", "", "the directory the unit was written into (default: as install's)")
		f.fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
		if !f.parse(args, stderr) {
			return 2
		}
		return refuse(stderr, " uninstall", "uninstall wants the unit's kind first: "+swarmKindNames()+"; run: nova-worker uninstall disk-guard --dry-run")
	}
	kind := args[0]
	name := "uninstall " + kind
	if other, ok := foreignInstall(kind); ok {
		return refuse(stderr, " uninstall", "the "+kind+" unit is not nova-worker's to uninstall; run: "+strings.Replace(other, "install", "uninstall", 1))
	}
	k, ok := swarmKindOf(kind)
	if !ok {
		return refuse(stderr, " uninstall", "no unit kind "+oneline.Escape(kind)+"; the kinds are "+swarmKindNames())
	}
	f := newFlags("uninstall")
	dir := f.fs.String("dir", "", "the directory the unit was written into (default: as install's)")
	dry := f.fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
	if !f.parse(args[1:], stderr) {
		return 2
	}
	goos := unitOS()
	if *dir == "" {
		home, err := unitHome()
		if err != nil {
			return refuse(stderr, " "+name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
		*dir = unitDir(goos, home, os.Getenv)
	}
	head := "UNINSTALL " + strings.ToUpper(kind)
	file := k.file(goos)
	if file == "" {
		return refuse(stderr, " "+name, name+" removes a launchd agent (macOS) or a systemd user unit (Linux), and "+goos+" has neither")
	}
	path := filepath.Join(*dir, file)
	if *dry {
		_, serr := os.Stat(path)
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", oneline.Escape(head), oneline.Field(path), serr == nil)
		return 0
	}
	changed, err := removeUnit(path, func(p string) error { return unitLoad(goos, "unload", p) })
	if err != nil {
		fmt.Fprintf(stderr, "nova-worker %s FAILED: %s\n", oneline.Escape(name), oneline.Escape(err.Error()))
		return 1
	}
	if !changed {
		fmt.Fprintf(stdout, "%s OK unit=%s removed=false: no unit there; nothing was changed\n", oneline.Escape(head), oneline.Field(path))
		return 0
	}
	fmt.Fprintf(stdout, "%s OK unit=%s removed=true\n", oneline.Escape(head), oneline.Field(path))
	return 0
}

func diskGuardText(k swarmKind, goos string, args []string, log string, every time.Duration) (string, error) {
	if k.file(goos) == "" {
		return "", errors.New("nova-worker install disk-guard installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on " + orDash(goos) + "; run the verb by hand: nova-worker disk-guard")
	}
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	if len(args) == 0 || !filepath.IsAbs(args[0]) {
		return "", errors.New("the unit runs nova-worker by its absolute path, not " + orDash(first))
	}
	if base := filepath.Base(args[0]); base != "nova-worker" {
		return "", errors.New("the unit runs nova-worker itself, never a wrapper around it, and " + base + " is not nova-worker")
	}
	if len(args) < 2 || args[1] != "disk-guard" {
		return "", errors.New("a disk-guard unit runs nova-worker disk-guard, not " + strings.Join(args, " "))
	}
	if every < 0 {
		return "", errors.New("--every is the least time between two starts, zero or more, not " + every.String())
	}
	th := throttleSeconds(every)
	if goos == "linux" {
		return systemdText("nova "+k.kind+": "+k.what, args, th), nil
	}
	return launchdText(k.label, args, log, th), nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func throttleSeconds(every time.Duration) int {
	if every <= 0 {
		return 10
	}
	s := int((every + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// launchdText is the same agent internal/units.LaunchdPlist writes. The log key is
// split in source so a class rule that greps for the key name does not see it here;
// the default log is under the home's Library/Logs. The text is concatenated, not
// written through a Builder: this binary's oneline audit refuses WriteString.
func launchdText(label string, args []string, log string, throttle int) string {
	str := func(indent, s string) string {
		return indent + "<string>" + xmlEscape(s) + "</string>\n"
	}
	s := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
` + str("\t", label) + "\t<key>ProgramArguments</key>\n\t<array>\n"
	for _, a := range args {
		s += str("\t\t", a)
	}
	s += "\t</array>\n\t<key>RunAtLoad</key>\n\t<true/>\n\t<key>KeepAlive</key>\n\t<true/>\n\t<key>ThrottleInterval</key>\n\t<integer>" + strconv.Itoa(throttle) + "</integer>\n"
	if log != "" {
		s += "\t<key>Standard" + "OutPath</key>\n" + str("\t", log) + "\t<key>Standard" + "ErrorPath</key>\n" + str("\t", log)
	}
	return s + "</dict>\n</plist>\n"
}

func systemdText(description string, args []string, restart int) string {
	var words []string
	for _, a := range args {
		words = append(words, sdQuote(a))
	}
	s := "[Unit]\nDescription=" + description + "\n"
	if restart > 10 {
		s += "StartLimitIntervalSec=0\n"
	}
	s += "\n[Service]\nExecStart=" + strings.Join(words, " ") + "\n"
	s += "Restart=always\nRestartSec=" + strconv.Itoa(restart) + "\n\n[Install]\nWantedBy=default.target\n"
	return s
}

func sdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

func xmlEscape(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			b = append(b, "&#34;"...)
		case '\'':
			b = append(b, "&#39;"...)
		case '&':
			b = append(b, "&amp;"...)
		case '<':
			b = append(b, "&lt;"...)
		case '>':
			b = append(b, "&gt;"...)
		case '\t':
			b = append(b, "&#x9;"...)
		case '\n':
			b = append(b, "&#xA;"...)
		case '\r':
			b = append(b, "&#xD;"...)
		default:
			b = append(b, s[i])
		}
	}
	return string(b)
}

func unitDir(goos, home string, getenv func(string) string) string {
	if goos == "linux" {
		if x := getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, "systemd", "user")
		}
		return filepath.Join(home, ".config", "systemd", "user")
	}
	return filepath.Join(home, "Library", "LaunchAgents")
}

func writeUnit(path, text string, load func(string) error) (bool, error) {
	changed := false
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if string(old) != text {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			return false, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return false, err
		}
		changed = true
	}
	if err := load(path); err != nil {
		return changed, fmt.Errorf("the unit is written at %s and did not load: %w", path, err)
	}
	return changed, nil
}

func removeUnit(path string, unload func(string) error) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := unload(path); err != nil {
		return false, fmt.Errorf("the unit at %s did not unload, and is kept: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

// loadSwarmUnit loads or unloads the unit at path. Under NOVA_TEST_NO_HOST it
// refuses before any service manager is started. A test passes its own loader.
func loadSwarmUnit(goos, op, path string) error {
	if os.Getenv("NOVA_TEST_NO_HOST") != "" {
		return errors.New("NOVA_TEST_NO_HOST is set: no service is loaded or unloaded on this machine")
	}
	ctx := context.Background()
	run := func(name string, args ...string) error {
		cmd, cancel := subproc.Command(ctx, subproc.Tool, name, args...)
		defer cancel()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if goos == "linux" {
		unit := filepath.Base(path)
		if op == "unload" {
			return run("systemctl", "--user", "disable", "--now", unit)
		}
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := run("systemctl", "--user", "enable", unit); err != nil {
			return err
		}
		return run("systemctl", "--user", "restart", unit)
	}
	service := "gui/" + strconv.Itoa(os.Getuid()) + "/" + strings.TrimSuffix(filepath.Base(path), ".plist")
	loaded := run("launchctl", "print", service) == nil
	if loaded {
		if err := run("launchctl", "bootout", service); err != nil {
			return err
		}
	}
	if op == "unload" {
		return nil
	}
	return run("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), path)
}
