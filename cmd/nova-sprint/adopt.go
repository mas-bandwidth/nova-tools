package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// The engine play (docs/SPEC-FRIEND.md, "The engine lock"; docs/SPEC-RUNNER.md,
// "The unit the adopt writes"): the adopt installs one runner engine per
// mode=batch friend row, and `nova-sprint friend engine <f> restart` is the
// only sanctioned stop. The seat's `adopt` verb is the fleet play
// (adopt_play.go): the play reaches this seam as `nova-sprint friend engine
// --rows <file>` on the friend's machine, and this file holds what that call
// runs. The lock itself is internal/friend/engine_guard.go; the runner keeps
// it, so no hand starts or stops an engine.
func init() {
	notServed = append(notServed, "friend engine")
	installVerbs = append(installVerbs, verb{
		"friend engine",
		"<friend> (status | restart) [--state-dir <dir>] [--home <dir>] [--uid <n>] [--rows <file>] [--dry-run]",
		"friend engine ada status",
		(*app).cmdFriendEngine,
	})
	verbClasses["friend engine"] = classMachine
	verbExit["friend engine"] = "exit codes: 0 status printed, or restart kickstarted the runner (with --dry-run, the kickstart was named and not run), 1 restart or the engine play did not run, 2 usage"
	verbEffect["friend engine"] = "local write: status reads her engine lock and writes nothing; restart is the only sanctioned stop and it is a launchd kickstart -k that keeps the lanes; --rows installs one runner unit per batch friend in that file; --dry-run names the kickstart and runs nothing"
}

// installEngineJSON installs one engine unit per batch friend in the JSON rows
// read from an adopt's play output (friend.EngineRow), through the runner unit
// the play writes: one launchd unit per mode=batch row, its stale shell runner
// retired in place.
func installEngineJSON(raw, home, binary string, uid int) error {
	var rows []friend.EngineRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return fmt.Errorf("engine rows: %w", err)
	}
	return friend.Play{Home: home, Binary: binary, UID: uid, Launch: launchctl, Rows: rows}.Install()
}

// launchctl is the engine play's launchd seam: one bounded launchctl call, its
// output in the error.
func launchctl(args ...string) error {
	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, "launchctl", args...)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}

// engineDir is the friend's state directory: --state-dir, else the default
// under --home, else empty (nothing to read).
func engineDir(name, stateDir, home string) string {
	if stateDir != "" {
		return stateDir
	}
	if home != "" {
		return friend.DefaultStateDir(home, name)
	}
	return ""
}

// cmdFriendEngine is `nova-sprint friend engine <friend> status|restart`: the
// status reads her engine lock (the holder, since when, its lanes, its last
// lane start); restart is the only sanctioned stop and it is a launchd
// kickstart that keeps the lanes. --rows runs the engine play for the batch
// friends in that JSON file.
func (a *app) cmdFriendEngine(args []string, stdout, stderr io.Writer) int {
	const name = "friend engine"
	fs, _ := a.verbSetup(name)
	stateDir := fs.String("state-dir", "", "the friend's state directory, where the engine lock is written")
	home := fs.String("home", "", "the home directory; the state directory is <home>/.nova-friend/<friend> when --state-dir is empty")
	uid := fs.Int("uid", -1, "the launchd gui user id for restart (default: this process)")
	rows := fs.String("rows", "", "a JSON file of friend rows; the engine play installs one unit per batch friend in it")
	dry := fs.Bool("dry-run", false, "name the kickstart and write and run nothing")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 2 {
		return refuse(stderr, name, argErr("wants <friend> and status or restart", err, pos...))
	}
	friendName, action := pos[0], pos[1]
	if !friend.NameOK(friendName) {
		return refuse(stderr, name, "friend name "+oneline.Escape(friendName)+" is not a word of letters, digits, _ and -")
	}
	id := *uid
	if id < 0 {
		id = os.Getuid()
	}
	if *rows != "" {
		if *dry {
			fmt.Fprintf(stdout, "WOULD install engine rows from %s\n", *rows)
		} else if code := runEngineRows(*rows, *home, id, stderr); code != 0 {
			return code
		}
	}
	switch action {
	case "status":
		return engineStatus(friendName, *stateDir, *home, stdout, stderr)
	case "restart":
		return engineRestart(friendName, id, *stateDir, *home, *dry, stdout, stderr)
	default:
		return refuse(stderr, name, "wants status or restart, found "+oneline.Escape(action))
	}
}

// runEngineRows runs the engine play over the JSON rows at path.
func runEngineRows(path, home string, uid int, stderr io.Writer) int {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "%s friend engine FAILED: %s\n", prog, oneline.Err(err))
		return 1
	}
	if err := installEngineJSON(string(b), home, "", uid); err != nil {
		fmt.Fprintf(stderr, "%s friend engine FAILED: %s\n", prog, oneline.Err(err))
		return 1
	}
	return 0
}

// engineStatus prints the engine lock as her row reads it: held=none when the
// lock file is absent.
func engineStatus(name, stateDir, home string, stdout, stderr io.Writer) int {
	dir := engineDir(name, stateDir, home)
	if dir == "" {
		fmt.Fprintf(stdout, "ENGINE friend=%s held=none\n", name)
		return 0
	}
	view, err := friend.ReadEngine(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stdout, "ENGINE friend=%s held=none\n", name)
			return 0
		}
		fmt.Fprintf(stderr, "%s friend engine FAILED: %s\n", prog, oneline.Err(err))
		return 1
	}
	last := "none"
	if !view.LastLane.IsZero() {
		last = view.LastLane.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(stdout, "ENGINE friend=%s held=%d since=%s lanes=%d last_lane_start=%s\n", name, view.PID, view.Since.UTC().Format(time.RFC3339), view.Lanes, last)
	return 0
}

// engineRestart is the one sanctioned stop: release a dead holder's lock, then
// kickstart the runner unit, which keeps the lanes.
func engineRestart(name string, uid int, stateDir, home string, dry bool, stdout, stderr io.Writer) int {
	args := friend.KickstartArgs(uid, name)
	if dry {
		fmt.Fprintf(stdout, "WOULD launchctl %s\n", strings.Join(args, " "))
		return 0
	}
	if dir := engineDir(name, stateDir, home); dir != "" {
		if err := friend.ReleaseIfDead(dir, nil); err != nil {
			fmt.Fprintf(stderr, "%s friend engine FAILED: %s\n", prog, oneline.Err(err))
			return 1
		}
	}
	if err := launchctl(args...); err != nil {
		fmt.Fprintf(stderr, "%s friend engine FAILED: %s\n", prog, oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "ENGINE friend=%s restarted\n", name)
	return 0
}
