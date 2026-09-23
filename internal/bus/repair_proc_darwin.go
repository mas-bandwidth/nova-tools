//go:build darwin

package bus

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitProcesses lists live git processes. ps -ww is the command line, wide enough that a
// checkout path is not cut off mid-spelling; lsof supplies the cwd for a git that was
// started inside the checkout and so does not carry -C. An lsof or ps failure is not an
// empty cwd. A git with no absolute -C whose cwd we could not read makes the scan
// unknown, and the lock stays.
func gitProcesses() ([]gitProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=", "-o", "command=").Output()
	if err != nil {
		return nil, ownershipUnknownErr("ps failed")
	}
	cwds, cwdErr := darwinGitCwd()
	return gitProcsFromPS(string(out), cwds, cwdErr, darwinPIDAlive)
}

// gitProcsFromPS turns one ps snapshot into git processes. cwdErr set means lsof did not
// run; a pid missing from cwds after a successful lsof is checked with alive. A vanished
// pid is skipped. A still-present git whose cwd is missing and whose command line does
// not name an absolute work tree is an incomplete scan, not proof that nobody owns the
// checkout.
func gitProcsFromPS(psOut string, cwds map[string]string, cwdErr error, alive func(pid string) (bool, error)) ([]gitProc, error) {
	var procs []gitProc
	for _, line := range strings.Split(psOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, cmd, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		if !looksLikeGit(cmd) {
			continue
		}
		p := gitProc{command: cmd}
		if cwdErr != nil {
			if !commandLocatesAbsolutely(p) {
				return nil, ownershipUnknownErr("cwd unreadable")
			}
			procs = append(procs, p)
			continue
		}
		cwd, found := cwds[pid]
		if !found || cwd == "" {
			still, aerr := alive(pid)
			if aerr != nil {
				return nil, ownershipUnknownErr(aerr.Error())
			}
			if !still {
				continue
			}
			if !commandLocatesAbsolutely(p) {
				return nil, ownershipUnknownErr("cwd unreadable")
			}
			procs = append(procs, p)
			continue
		}
		p.cwd = cwd
		p.cwdKnown = true
		procs = append(procs, p)
	}
	return procs, nil
}

func looksLikeGit(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(fields[0])
	return base == "git" || base == "git.exe"
}

// darwinGitCwd asks lsof for every git process's cwd. Failure is returned. It is not
// an empty map: an empty map is a successful lsof that saw no git cwd.
func darwinGitCwd() (map[string]string, error) {
	var stdout, stderr strings.Builder
	cmd := exec.Command("lsof", "-n", "-P", "-a", "-d", "cwd", "-c", "git", "-F", "pcn")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return lsofCwds(stdout.String(), stderr.String(), code, err)
}

// lsofCwds reads one lsof run. lsof exits 1 with nothing on either stream when no process
// matched -c git: no git was running at the moment it looked (#3029). That is a complete
// scan with no cwds, and every ps-listed git missing from it is re-checked by pid. Exit 1
// with anything said, any other failure, or output on a failed run is "lsof failed".
func lsofCwds(stdout, stderr string, code int, runErr error) (map[string]string, error) {
	cwds := map[string]string{}
	if runErr != nil {
		if code == 1 && stdout == "" && strings.TrimSpace(stderr) == "" {
			return cwds, nil
		}
		return nil, ownershipUnknownErr("lsof failed")
	}
	var pid string
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid = line[1:]
		case 'n':
			if pid != "" {
				cwds[pid] = line[1:]
			}
		}
	}
	return cwds, nil
}

// darwinPIDAlive reports whether pid is still in the process table. ps exiting 1 is
// the verified-vanished answer. Any other failure is an inspection error.
func darwinPIDAlive(pid string) (bool, error) {
	out, err := exec.Command("ps", "-p", pid, "-o", "pid=").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}
