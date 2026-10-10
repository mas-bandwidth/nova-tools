package units

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// LoadUnit loads (op load: unloaded first, so a changed unit is read again) or
// unloads the unit at path on this machine, named by its file: launchctl in the
// user's gui domain on macOS, its label the file's name without .plist, and
// systemctl --user on Linux, its service the file's name. It is the loader of every
// unit a nova verb installs (seat install, friend sync install, nova-sprint install,
// nova-redis install). Under NOVA_TEST_NO_HOST it refuses: a
// test gives its own loader.
func Load(goos, op, path string) error {
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

// UnitDir is where a unit goes when no directory is named: the user's LaunchAgents
// on macOS, the systemd user directory on Linux ($XDG_CONFIG_HOME/systemd/user when
// that is set).
func Dir(goos, home string, getenv func(string) string) string {
	if goos == "linux" {
		if x := getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, "systemd", "user")
		}
		return filepath.Join(home, ".config", "systemd", "user")
	}
	return filepath.Join(home, "Library", "LaunchAgents")
}
