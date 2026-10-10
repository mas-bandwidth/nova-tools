package sprint

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/units"
)

// friend sync install (docs/SPEC-SPRINT.md, "The friend sync loop as a service"; the
// owner, 2026-10-05: "We need to get away from these one shot shell scripts"): the
// friend sync loop, friend sync --every <d>, as a service of the coordinator's
// machine, the way seat install runs the push loop (seatinstall.go): a launchd agent
// on macOS and a systemd user unit on Linux, kept alive and started again at login,
// in place of a zsh while-loop typed by hand. The loop takes its actor from the seat
// each pass, so the unit names none. Its installer is SeatInstaller with its own file.

// FriendSyncLabel is the friend sync loop's launchd label; its plist is FriendSyncLabel.plist.
const FriendSyncLabel = units.FriendSyncLabel

// FriendSyncService is the friend sync loop's systemd user unit.
const FriendSyncService = units.FriendSyncService

// FriendSyncEnv is the environment a friend sync unit carries as it was typed: the
// names of the variables that hold the store's passwords and the bus's address and
// user, never a password (the unit is a file anyone on the machine may read). The
// passwords those names point at are the service's environment to give.
var FriendSyncEnv = []string{"NOVA_PG_PASSWORD_ENV", "NOVA_SPRINT_REDIS_USER", "NOVA_SPRINT_REDIS_PASSWORD_ENV", "NOVA_BUS_REDIS", "NOVA_BUS_REDIS_USER", "NOVA_BUS_REDIS_PASSWORD_ENV"}

// envName is a variable's name: what a *_PASSWORD_ENV variable holds, never a value.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// FriendSyncUnit is the friend sync loop as a service: the binary, the period, the
// sprint's store (Redis, an address: friend sync is never the server's), the config
// store (PG, a DSN with no password), the friends' root (Root, else the unit's HOME),
// the file its lines go to (launchd; systemd keeps them in its journal), and Env,
// FriendSyncEnv's variables as they were set where it was typed, in that order.
type FriendSyncUnit struct {
	OS, Exe, Redis, PG, Root, Log string
	Every                         time.Duration
	Env                           [][2]string
}

// FriendSyncUnitFile is the unit's file name on goos, "" where there is no service
// manager the loop is installed under.
func FriendSyncUnitFile(goos string) string {
	switch goos {
	case "darwin":
		return FriendSyncLabel + ".plist"
	case "linux":
		return FriendSyncService
	}
	return ""
}

// Args is the loop's command line, the binary first: friend sync --every <d> on the
// store and the config the unit names, and no --actor: each pass acts as the seat.
func (u FriendSyncUnit) Args() []string {
	args := []string{u.Exe, "friend", "sync", "--every", u.Every.String(), "--redis", u.Redis}
	if u.PG != "" {
		args = append(args, "--pg", u.PG)
	}
	if u.Root != "" {
		args = append(args, "--root", u.Root)
	}
	return args
}

// refusal is why the unit is not installed, "" is it may be.
func (u FriendSyncUnit) refusal() string {
	switch {
	case FriendSyncUnitFile(u.OS) == "":
		return "friend sync install installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on " + orDash(u.OS) + "; run the loop by hand: nova-sprint friend sync --every 15s"
	case u.Every <= 0:
		return "the loop wants --every <d> above zero, the time between passes; run: nova-sprint friend sync install --every 15s"
	case !filepath.IsAbs(u.Exe):
		return "the unit runs nova-sprint by its absolute path, not " + orDash(u.Exe)
	case u.Redis == "":
		return "the loop reads the sprint's store itself (friend sync is never the server's): --redis <addr> (or NOVA_SPRINT_REDIS)"
	case strings.HasPrefix(u.Redis, "mem:"):
		return "the loop syncs the sprint's store, and the in-memory twin " + u.Redis + " is this process's alone: install it for a Redis store"
	case u.Root != "" && !filepath.IsAbs(u.Root):
		return "the unit names the friends' root by its absolute path, not " + u.Root
	}
	for _, kv := range u.Env {
		if strings.HasSuffix(kv[0], "_ENV") && !envName.MatchString(kv[1]) {
			return kv[0] + " does not name a variable (letters, digits and underscores only), and the unit carries no secret"
		}
	}
	return ""
}

// Text is the unit's file: a launchd plist, or a systemd user unit.
func (u FriendSyncUnit) Text() (string, error) {
	if why := u.refusal(); why != "" {
		return "", errors.New(why)
	}
	if u.OS == "linux" {
		return systemdUnit("nova-sprint friend sync loop (friend sync --every "+u.Every.String()+")", u.Args(), u.Env), nil
	}
	return launchdPlist(FriendSyncLabel, u.Args(), u.Env, u.Log), nil
}

// launchdPlist is a launchd agent kept alive that runs args with env, its lines to log,
// started again no sooner than every 10 s (pkg/units).
func launchdPlist(label string, args []string, env [][2]string, log string) string {
	return units.LaunchdPlist(label, args, env, log, 10)
}

// systemdUnit is a systemd user unit restarted always, 10 s apart, that runs args with env.
func systemdUnit(description string, args []string, env [][2]string) string {
	return units.SystemdUnit(description, args, env, 10)
}

// InstallFriendSync writes the friend sync loop's unit into the installer's directory
// (whole, beside it first, then renamed into place) unless the file there is already
// this text, and loads it either way, as Install does the push loop's.
func (in SeatInstaller) InstallFriendSync(u FriendSyncUnit) (SeatResult, error) {
	text, err := u.Text()
	if err != nil {
		return SeatResult{}, err
	}
	return in.writeUnit(filepath.Join(in.Dir, FriendSyncUnitFile(u.OS)), text)
}

// UninstallFriendSync unloads the friend sync loop's unit and removes its file; with
// no file there it does nothing, and says so by Changed false.
func (in SeatInstaller) UninstallFriendSync(goos string) (SeatResult, error) {
	name := FriendSyncUnitFile(goos)
	if name == "" {
		return SeatResult{}, errors.New("friend sync uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and " + orDash(goos) + " has neither")
	}
	return in.removeUnit(filepath.Join(in.Dir, name))
}
