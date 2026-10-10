package friend

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Agent is one friend's launchd agent: the daemon as launchd runs it,
// never started by the model (SPEC-FRIEND.md, the daemon).
type Agent struct {
	Friend, Harness, Dir, Session string
	Adapter, DeliveryDir          string // explicit Codex folder route; session remains the real harness session
	StateDir                      string // the daemon's state files, when not the default under Home
	Width                         int
	Binary                        string   // this tool, by absolute path
	Copy                          CopyFile // places a removable-volume binary under Home; nil refuses it
	Redis, Server                 string   // the bus store and the sprint server
	Home, Path                    string   // the environment the agent runs in
	LaunchdLog                    string   // launchd's own stdout and stderr path, off the friend's volume
	// Secrets are the names of the secrets the daemon needs in its environment
	// (never values); with any, the command is wrapped in nova-secrets exec as
	// the seat Seat, with SecretsTool and Sops by absolute path, the store under
	// Home/nova-bench/secrets and the key under Home/.config/nova-secrets.
	Secrets                 []string
	Seat, SecretsTool, Sops string
	// Coordinator, SilentStop and BrokenAfter are the daemon's flags of the
	// same names, written only when set and not the default.
	Coordinator string
	SilentStop  time.Duration
	BrokenAfter int
	// ConfigDir is the friend's harness config directory (CLAUDE_CONFIG_DIR),
	// the daemon's --config-dir, written only when set.
	ConfigDir string
	// Command, when set, is what the agent runs in place of the daemon: this tool's
	// own verb and flags, after Binary (the wake ping loop, nova-friend ping-install).
	Command []string
	// Sleep is how Install waits between two looks at the old service after the
	// bootout (ReleasePoll); nil is time.Sleep. A test passes its own clock.
	Sleep func(time.Duration)
	// NotificationsOnly has its own agent label and carries its policy through install
	// (SPEC-FRIEND.md, notifications); it never replaces the native scheduler.
	NotificationsOnly bool
	NotifyKinds       string
	NotifyWindow      time.Duration
}

// Label is the agent's launchd label.
func (a Agent) Label() string {
	if a.NotificationsOnly {
		return "com.nova.friend-notifications-" + a.Friend
	}
	return "com.nova.friend-" + a.Friend
}

// PlistPath is where the agent's plist lives under home.
func (a Agent) PlistPath() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", a.Label()+".plist")
}

// Args is the daemon's command line: with Secrets, nova-secrets exec opens
// exactly those names for the daemon (--only) and refuses to start it without
// every one (--require), then the daemon itself after the --.
func (a Agent) Args() []string {
	if len(a.Command) > 0 {
		return append([]string{a.Binary}, a.Command...)
	}
	var args []string
	if len(a.Secrets) > 0 {
		args = []string{a.SecretsTool, "exec", "--store", filepath.Join(a.Home, "nova-bench", "secrets"), "--as", a.Seat,
			"--key", filepath.Join(a.Home, ".config", "nova-secrets", a.Seat+".key"), "--sops", a.Sops, "--only", strings.Join(a.Secrets, ",")}
		for _, name := range a.Secrets {
			args = append(args, "--require", name)
		}
		args = append(args, "--")
	}
	args = append(args, a.Binary, "run", "--as", a.Friend, "--harness", a.Harness, "--dir", a.Dir, "--redis", a.Redis)
	if a.Server != "" {
		args = append(args, "--server", a.Server)
	}
	args = append(args, "--width", fmt.Sprint(a.Width))
	if a.NotificationsOnly {
		args = append(args, "--notifications-only")
		if a.NotifyKinds != "" {
			args = append(args, "--notify-kinds", a.NotifyKinds)
		}
		if a.NotifyWindow > 0 {
			args = append(args, "--notify-window", a.NotifyWindow.String())
		}
	}
	if a.Session != "" {
		args = append(args, "--session", a.Session)
	}
	if a.Adapter != "" {
		args = append(args, "--adapter", a.Adapter, "--delivery-dir", a.DeliveryDir)
	}
	if a.StateDir != "" {
		args = append(args, "--state-dir", a.StateDir)
	}
	if a.Coordinator != "" {
		args = append(args, "--coordinator", a.Coordinator)
	}
	if a.ConfigDir != "" {
		args = append(args, "--config-dir", a.ConfigDir)
	}
	if a.SilentStop > 0 && a.SilentStop != DefaultSilentStop {
		args = append(args, "--silent-stop", a.SilentStop.String())
	}
	if a.BrokenAfter > 0 && a.BrokenAfter != DefaultBrokenAfter {
		args = append(args, "--broken-after", fmt.Sprint(a.BrokenAfter))
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

// CopyFile copies src onto dst. CopyExecutable is the one install uses; a
// test passes its own, and nil refuses a removable-volume binary.
type CopyFile func(src, dst string) error

// ErrBinaryOnRemovableVolume is the refusal when the agent's binary is on a
// removable volume and cannot be copied under the home (docs/SPEC-FRIEND.md).
// launchd starts that binary and it does nothing.
var ErrBinaryOnRemovableVolume = errors.New("binary on a removable volume: launchd starts it and it does nothing")

// InstalledBinary is the copy of a removable-volume binary, under the home,
// off /Volumes (docs/SPEC-FRIEND.md).
func InstalledBinary(home string) string {
	return filepath.Join(home, ".nova-friend", "bin", "nova-friend")
}

// PlanBinary is the path the plist will name. A binary off /Volumes is itself.
// A binary on /Volumes is copied to InstalledBinary before the plist is
// written; a home that is empty or itself on /Volumes is refused, because a
// copy there would stay on the wall (docs/SPEC-FRIEND.md).
func PlanBinary(binary, home string) (path string, copy bool, err error) {
	binary = slashClean(binary)
	home = slashClean(home)
	if !onRemovableVolume(binary) {
		return binary, false, nil
	}
	if home == "" || home == "." || home == "/" || onRemovableVolume(home) {
		return "", false, fmt.Errorf("%w; the home directory is not off /Volumes, so there is nowhere to copy it; move the binary off /Volumes and run nova-friend install again", ErrBinaryOnRemovableVolume)
	}
	return InstalledBinary(home), true, nil
}

func onRemovableVolume(p string) bool {
	p = slashClean(p)
	return p == "/Volumes" || strings.HasPrefix(p, "/Volumes/")
}

func slashClean(p string) string { return filepath.ToSlash(filepath.Clean(p)) }

// CopyExecutable copies src onto dst and keeps it executable. dst is replaced
// only after the copy is complete, so a failure leaves a previous dst in
// place (docs/SPEC-FRIEND.md).
func CopyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	st, err := in.Stat()
	if err != nil {
		return closeWith(in, err)
	}
	if !st.Mode().IsRegular() {
		return closeWith(in, fmt.Errorf("%s is not a file", src))
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return closeWith(in, err)
	}
	tmp, err := os.CreateTemp(dir, ".nova-friend-*")
	if err != nil {
		return closeWith(in, err)
	}
	_, copyErr := io.Copy(tmp, in)
	closeIn := in.Close()
	closeTmp := tmp.Close()
	if copyErr != nil {
		return removePartial(tmp.Name(), copyErr)
	}
	if closeIn != nil {
		return removePartial(tmp.Name(), closeIn)
	}
	if closeTmp != nil {
		return removePartial(tmp.Name(), closeTmp)
	}
	if err := os.Chmod(tmp.Name(), st.Mode().Perm()|0o111); err != nil {
		return removePartial(tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return removePartial(tmp.Name(), err)
	}
	return nil
}

func closeWith(f *os.File, err error) error {
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("%w; closing %s: %v", err, f.Name(), cerr)
	}
	return err
}

func removePartial(path string, err error) error {
	if rmErr := os.Remove(path); rmErr != nil {
		return fmt.Errorf("%w; removing the partial copy: %v", err, rmErr)
	}
	return err
}

// BootstrapTries is how many times a bootstrap is sent while launchd is
// still tearing the old agent down (it answers EIO, "Input/output error",
// for a second or so after the bootout, measured 2026-10-04).
const BootstrapTries = 5

// ReleasePoll is how often Install asks launchd, after the bootout, whether it still holds
// the old service; DefaultExitTimeout is how long it asks, the launchd default for a plist
// with no ExitTimeOut (the time launchd gives a daemon to exit before it kills it).
const (
	ReleasePoll        = 250 * time.Millisecond
	DefaultExitTimeout = 20 * time.Second
)

// ExitTimeout is the plist's ExitTimeOut, DefaultExitTimeout when it has none.
func ExitTimeout(plist string) time.Duration {
	_, rest, ok := strings.Cut(plist, "<key>ExitTimeOut</key>")
	if !ok {
		return DefaultExitTimeout
	}
	rest = strings.TrimSpace(rest)
	v, ok := strings.CutPrefix(rest, "<integer>")
	if !ok {
		return DefaultExitTimeout
	}
	v, _, ok = strings.Cut(v, "</integer>")
	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); !ok || err != nil || n <= 0 {
		return DefaultExitTimeout
	}
	return time.Duration(n) * time.Second
}

// WaitReleased waits until launchd no longer holds the service target (gui/<uid>/<label>)
// after its bootout: `launchctl print <target>` asked every poll, until it no longer finds
// the service (anything but exit 0 with the service's own `<target> = {` block), at most
// timeout. A bootstrap sent while launchd is still removing the old service is refused with
// "37: Operation already in progress" for as long as the old daemon takes to exit (about
// 5 s, the seat's adopt runs of 2026-10-07), more than BootstrapTries one second apart.
// It answers how long it waited, and on timeout an error naming the label and the seconds.
func WaitReleased(ctx context.Context, run Launchctl, target string, timeout, poll time.Duration, sleep func(time.Duration)) (time.Duration, error) {
	var waited time.Duration
	for {
		out, err := run(ctx, "print", target)
		if err != nil || !strings.Contains(out, target+" = {") {
			return waited, nil
		}
		if waited >= timeout {
			return waited, fmt.Errorf("launchd still holds %s %.0fs after its bootout (its exit timeout): the old daemon has not exited; nothing was bootstrapped", target, waited.Seconds())
		}
		if ctx.Err() != nil {
			return waited, ctx.Err()
		}
		sleep(poll)
		waited += poll
	}
}

// Install writes the plist and loads it. A binary on /Volumes is copied under
// the home first (PlanBinary); a copy that cannot be made is refused and
// nothing is written (docs/SPEC-FRIEND.md). Then a bootout of whatever that
// label runs now (nothing loaded is fine), a wait until launchd no longer holds
// the label (WaitReleased), then a bootstrap into the user's
// domain, sent again after wait() while launchd answers EIO, so running it
// again replaces the agent with the same result. It answers the plist's
// path and the commands it ran.
func Install(ctx context.Context, a Agent, uid int, run Launchctl, write func(path string, data []byte) error, wait func()) (path string, ran []string, err error) {
	placed, copy, err := a.BinaryPlan()
	if err != nil {
		return "", nil, err
	}
	if copy {
		if a.Copy == nil {
			return "", nil, fmt.Errorf("%w; copy it to %s and run nova-friend install again", ErrBinaryOnRemovableVolume, placed)
		}
		if err := a.Copy(a.Binary, placed); err != nil {
			return "", nil, fmt.Errorf("%w; the copy to %s failed (%v); move the binary off /Volumes and run nova-friend install again", ErrBinaryOnRemovableVolume, placed, err)
		}
	}
	a.Binary = placed
	if a.NotificationsOnly {
		verified, _, err := a.BinaryPlan()
		if err != nil {
			return "", nil, err
		}
		if verified != placed {
			return "", nil, fmt.Errorf("notification binary changed during copy; preserve the previous service and install the reviewed binary again")
		}
	}
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
	sleep := a.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	target := domain + "/" + a.Label()
	waited, err := WaitReleased(ctx, run, target, ExitTimeout(a.Plist()), ReleasePoll, sleep)
	if waited > 0 {
		ran = append(ran, fmt.Sprintf("launchctl print %s (every %s until launchd released it: %s)", target, ReleasePoll, waited))
	}
	if err != nil {
		return path, ran, err
	}
	bootstrap := []string{"bootstrap", domain, path}
	for try := 1; ; try++ {
		ran = append(ran, "launchctl "+strings.Join(bootstrap, " "))
		out, err := run(ctx, bootstrap...)
		if err == nil {
			return path, ran, nil
		}
		if try == BootstrapTries || !(strings.Contains(out, "Input/output error") || strings.Contains(out, "Operation already in progress")) {
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

// PlistArgs is the ProgramArguments array of a launchd plist, in order; nil
// when the plist has none or cannot be read as XML.
func PlistArgs(plist string) []string {
	dec := xml.NewDecoder(strings.NewReader(plist))
	dec.Strict = false
	var args []string
	key, inArray, found := "", false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			if end, ok := tok.(xml.EndElement); ok && end.Name.Local == "array" && inArray {
				return args
			}
			continue
		}
		switch el.Name.Local {
		case "key":
			var k string
			if dec.DecodeElement(&k, &el) == nil {
				key = strings.TrimSpace(k)
			}
		case "array":
			inArray = key == "ProgramArguments"
			found = found || inArray
		case "string":
			var v string
			if dec.DecodeElement(&v, &el) == nil && inArray {
				args = append(args, v)
			}
		}
	}
	if !found {
		return nil
	}
	return args
}

// PlistDriftLine is the line a daemon says on start when its own arguments
// (running, after the program's name) differ from the installed plist's: a
// launchctl kickstart restarts the agent launchd loaded, with the arguments
// it read then, so an edit to the plist is lost until it is booted out and
// bootstrapped again (the finding of 2026-10-05). The daemon's part of each
// is compared, from its verb "run" on (the secrets wrap and the binary's path
// are the plist's own). Empty when there is no plist, it names no run, or
// the two agree.
func PlistDriftLine(plist string, running []string) string {
	installed := daemonPart(PlistArgs(plist))
	mine := daemonPart(running)
	if installed == nil || mine == nil || slices.Equal(installed, mine) {
		return ""
	}
	return fmt.Sprintf("plist drift: this daemon's arguments differ from the installed plist (a kickstart keeps the arguments launchd loaded); running: %s; plist: %s; to run the plist's: nova-friend install again (it boots out and bootstraps)",
		strings.Join(mine, " "), strings.Join(installed, " "))
}

// daemonPart is args from the verb "run" on; nil when there is no run.
func daemonPart(args []string) []string {
	if i := slices.Index(args, "run"); i >= 0 {
		return args[i:]
	}
	return nil
}

// Said is the command line as a plan says it, with no path in it: the
// secrets wrap by its names and seat, then the daemon's own flags, --redis
// and --server left to the install line that gave them.
func (a Agent) Said() string {
	said := fmt.Sprintf("nova-friend run --as %s --harness %s --dir %s --width %d", a.Friend, a.Harness, a.Dir, a.Width)
	if a.Adapter == "folder" {
		said += " --session " + a.Session + " --adapter folder --delivery-dir " + a.DeliveryDir
	}
	said += ", with --redis and --server as given here"
	if len(a.Secrets) == 0 {
		return said
	}
	wrap := "nova-secrets exec --as " + a.Seat + " --only " + strings.Join(a.Secrets, ",")
	for _, name := range a.Secrets {
		wrap += " --require " + name
	}
	return wrap + " -- " + said
}

// BinaryPlan isolates notification executables by content hash (SPEC-FRIEND.md,
// notifications), so installing or rolling one back never overwrites the native binary.
func (a Agent) BinaryPlan() (path string, copy bool, err error) {
	if !a.NotificationsOnly {
		return PlanBinary(a.Binary, a.Home)
	}
	home := slashClean(a.Home)
	if home == "" || home == "." || home == "/" || onRemovableVolume(home) {
		return "", false, fmt.Errorf("notification binary wants a home off /Volumes")
	}
	in, err := os.Open(a.Binary)
	if err != nil {
		return "", false, err
	}
	st, err := in.Stat()
	if err != nil {
		return "", false, closeWith(in, err)
	}
	if !st.Mode().IsRegular() {
		return "", false, closeWith(in, fmt.Errorf("notification binary %s is not a regular file; install a reviewed executable", a.Binary))
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, in)
	closeErr := in.Close()
	if copyErr != nil {
		return "", false, copyErr
	}
	if closeErr != nil {
		return "", false, closeErr
	}
	path = filepath.Join(home, ".nova-friend", "notifications", "bin", fmt.Sprintf("%x", hash.Sum(nil)), "nova-friend")
	return path, slashClean(a.Binary) != slashClean(path), nil
}
