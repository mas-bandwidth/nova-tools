package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Several runners share one machine, and apt does not survive a concurrent
// dpkg, so an install takes a lock: a directory, created atomically by mkdir,
// holding one file with its holder's pid. A lock is stale, and taken over, when
// it is older than installLockStale, or when the pid it names is no longer a
// running process (a holder killed outright leaves the lock behind; the pid says
// so at once, where the age would take ten minutes). A holder that is told to
// stop (SIGTERM or SIGINT) releases its lock before it exits. A waiter polls
// every installLockPoll and gives up after installLockTries polls.
const (
	installLockStale = 600 * time.Second
	installLockPoll  = 3 * time.Second
	installLockTries = 40
	// installLockPidFile is the file in the lock directory naming the holder.
	installLockPidFile = "pid"
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
	// pid is this process's id (written into the lock it takes); alive says
	// whether a pid is a running process; sigCtx derives a context cancelled by
	// SIGTERM or SIGINT; exit ends the process with a code. The four are here
	// so a test stops an install with no signal sent to the test process. A nil
	// one is the real one.
	pid    func() int
	alive  func(pid int) bool
	sigCtx func(parent context.Context) (context.Context, context.CancelFunc)
	exit   func(code int)
}

func (h installHost) ownPID() int {
	if h.pid != nil {
		return h.pid()
	}
	return os.Getpid()
}

func (h installHost) pidAlive(pid int) bool {
	if h.alive != nil {
		return h.alive(pid)
	}
	return processAlive(pid)
}

func (h installHost) stopContext(parent context.Context) (context.Context, context.CancelFunc) {
	if h.sigCtx != nil {
		return h.sigCtx(parent)
	}
	return signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGINT)
}

func (h installHost) exitWith(code int) {
	if h.exit != nil {
		h.exit(code)
		return
	}
	os.Exit(code)
}

// lockHolder is the pid a lock names, 0 when it names none that can be read (a
// lock just made, or one written by an older tool: only its age speaks then).
func (h installHost) lockHolder() int {
	b, err := os.ReadFile(filepath.Join(h.lock, installLockPidFile))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// lockIsStale says whether the lock held at h.lock belongs to no live install.
func (h installHost) lockIsStale(fi os.FileInfo) bool {
	if h.now().Sub(fi.ModTime()) > installLockStale {
		return true
	}
	pid := h.lockHolder()
	return pid != 0 && !h.pidAlive(pid)
}

// removeLock takes the lock away: its pid file, then the directory. A path
// already gone is a lock already released, not an error.
func (h installHost) removeLock() {
	for _, p := range []string{filepath.Join(h.lock, installLockPidFile), h.lock} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(h.stderr, "cannot remove install lock %s: %v\n", p, err)
		}
	}
}

// releaseOwnLock releases the lock this process took, and only that one: a lock
// that names another pid was taken over as stale while this install ran, and is
// that install's now.
func (h installHost) releaseOwnLock() {
	if pid := h.lockHolder(); pid != 0 && pid != h.ownPID() {
		return
	}
	h.removeLock()
}

// takeOverStaleLock removes a lock that belongs to no live install.
func (h installHost) takeOverStaleLock() {
	if fi, err := os.Stat(h.lock); err == nil && fi.IsDir() && h.lockIsStale(fi) {
		h.removeLock()
	}
}

// publish hands dir to the steps after this one (GITHUB_PATH).
func (h installHost) publish(dir string) {
	if err := appendGitHubFile(h.getenv, "GITHUB_PATH", dir); err != nil && !errors.Is(err, errNoGitHubFile) {
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
	// SIGTERM and SIGINT are caught from here to the return: a waiter gives up
	// and a holder releases its lock and exits, where the default would leave
	// the lock behind.
	ctx, stop := h.stopContext(context.Background())
	defer stop()
	h.takeOverStaleLock()
	for n := 0; ; {
		if err := os.Mkdir(h.lock, 0o755); err == nil {
			break
		}
		if have() {
			return found()
		}
		if ctx.Err() != nil {
			fmt.Fprintf(h.stderr, "interrupted while waiting to install %s\n", what)
			return 1
		}
		n++
		if n > installLockTries {
			fmt.Fprintf(h.stderr, "timed out waiting to install %s\n", what)
			return 1
		}
		h.sleep(installLockPoll)
		h.takeOverStaleLock()
	}
	if err := os.WriteFile(filepath.Join(h.lock, installLockPidFile), []byte(strconv.Itoa(h.ownPID())+"\n"), 0o644); err != nil {
		fmt.Fprintf(h.stderr, "cannot write the install lock's pid: %v\n", err)
		h.removeLock()
		return 1
	}
	finished := make(chan struct{})
	released := make(chan struct{})
	go func() {
		defer close(released)
		select {
		case <-ctx.Done():
			h.releaseOwnLock()
			fmt.Fprintf(h.stderr, "interrupted while installing %s; install lock released\n", what)
			h.exitWith(1)
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		<-released
		h.releaseOwnLock()
	}()
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
