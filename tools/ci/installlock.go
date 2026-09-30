package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Several runners share one machine, and apt does not survive a concurrent
// dpkg, so an install takes a lock: a directory, created atomically by mkdir.
// A lock older than installLockStale belongs to a dead install and is taken
// over; a waiter polls every installLockPoll and gives up after
// installLockTries polls.
const (
	installLockStale = 600 * time.Second
	installLockPoll  = 3 * time.Second
	installLockTries = 40
)

// installHost is what an install verb needs from outside itself, so a test
// runs one with no package manager, no shared lock and no real clock.
type installHost struct {
	run            cmdRunner
	getenv         func(string) string
	stdout, stderr io.Writer
	sleep          func(time.Duration)
	now            func() time.Time
	// lock is the lock directory's path.
	lock string
	// isExec says whether a path is an executable file; glob lists the paths a
	// pattern matches, sorted.
	isExec func(string) bool
	glob   func(string) []string
}

// publish hands dir to the steps after this one (GITHUB_PATH).
func (h installHost) publish(dir string) {
	if err := appendPathFile(h.getenv, dir); err != nil {
		fmt.Fprintf(h.stderr, "cannot publish %s to GITHUB_PATH: %v\n", dir, err)
	}
}

// lockedInstall runs install once under the install lock, unless have turns
// true first: before the lock, while waiting for it, or once it is held, and
// then found is the verb's answer. It returns the verb's exit code: found's,
// install's, or 1 when the lock never came free.
func lockedInstall(h installHost, what string, have func() bool, found func() int, install func() int) int {
	if have() {
		return found()
	}
	if fi, err := os.Stat(h.lock); err == nil && fi.IsDir() && h.now().Sub(fi.ModTime()) > installLockStale {
		os.Remove(h.lock) // a stale lock is taken over; an empty directory is all it is
	}
	for n := 0; ; {
		if err := os.Mkdir(h.lock, 0o755); err == nil {
			break
		}
		if have() {
			return found()
		}
		n++
		if n > installLockTries {
			fmt.Fprintf(h.stderr, "timed out waiting to install %s\n", what)
			return 1
		}
		h.sleep(installLockPoll)
	}
	defer os.Remove(h.lock)
	if have() {
		return found()
	}
	return install()
}

// runInstallStep runs one command of an install with its output on the verb's
// streams and returns its exit code, 127 when it could not start (the shell's
// code for a missing program).
func (h installHost) runInstallStep(env []string, name string, args ...string) int {
	code, err := h.run.Run(cmdSpec{Name: name, Args: args, Env: env, Stdout: h.stdout, Stderr: h.stderr})
	if err != nil {
		fmt.Fprintf(h.stderr, "%s: %v\n", name, err)
		return 127
	}
	return code
}

// aptInstall runs `apt-get update` and `apt-get install` for pkgs as the user,
// and as root through `sudo -n` when either refuses. It returns the exit code
// of the last command it ran.
func (h installHost) aptInstall(pkgs ...string) int {
	install := append([]string{"install", "-y", "-qq"}, pkgs...)
	if h.runInstallStep(nil, "apt-get", "update", "-qq") == 0 && h.runInstallStep(nil, "apt-get", install...) == 0 {
		return 0
	}
	if code := h.runInstallStep(nil, "sudo", "-n", "apt-get", "update", "-qq"); code != 0 {
		return code
	}
	return h.runInstallStep(nil, "sudo", append([]string{"-n", "apt-get"}, install...)...)
}

// brewEnv keeps Homebrew from updating itself or upgrading what is installed
// while it installs one thing.
var brewEnv = []string{"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_UPGRADE=1"}

func osInstallHost(e env) installHost {
	return installHost{
		run:    osCmdRunner{},
		getenv: e.getenv,
		stdout: e.stdout,
		stderr: e.stderr,
		sleep:  time.Sleep,
		now:    time.Now,
		isExec: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
		},
		glob: globSorted,
	}
}
