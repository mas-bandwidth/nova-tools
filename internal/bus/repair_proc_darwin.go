//go:build darwin

package bus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// gitProcesses lists live git processes. ps -ww is the command line, wide enough that a
// checkout path is not cut off mid-spelling; lsof supplies the cwd for a git that was
// started inside the checkout and so does not carry -C. An lsof or ps failure is not an
// empty cwd. A git with no absolute -C whose cwd we could not read makes the scan
// unknown, and the lock stays. ps names each process's effective uid; a git of another
// account that nothing places is out of sight (see ownershipUnknown in repair.go).
func gitProcesses() ([]gitProc, error) {
	ps, stopPS := subproc.Command(context.Background(), subproc.Tool, "ps", "-axww", "-o", "uid=", "-o", "pid=", "-o", "command=")
	defer stopPS()
	out, err := ps.Output()
	if err != nil {
		return nil, ownershipUnknownErr("ps failed")
	}
	cwds, cwdErr := darwinGitCwd()
	return gitProcsFromPS(string(out), strconv.Itoa(os.Geteuid()), cwds, cwdErr, darwinPIDAlive)
}

// gitProcsFromPS turns one ps snapshot (uid, pid, command per line) into git processes.
// Every git, of any account, is placed by the cwd lsof gave or by a command line that
// names an absolute work tree or git dir (-C, --git-dir, --work-tree): a git of another
// account that names this checkout is an owner. cwdErr set means lsof did not run; a pid
// missing from cwds after a successful lsof is checked with alive, and a vanished pid is
// skipped. A still-present git with no cwd and no absolute location is an incomplete
// scan when it is this account's, and is skipped when it is another account's: lsof as
// this account cannot read it, so it is out of this scan's sight (see ownershipUnknown).
// Every skipped git of another account and every unreadable one of this account is in
// the list, flagged (gitProc.foreign, gitProc.unknown), so the caller can count them; the
// first unknown is the error, and the rest of the snapshot is still read, so an owner
// later in it is not hidden.
func gitProcsFromPS(psOut, self string, cwds map[string]string, cwdErr error, alive func(pid string) (bool, error)) ([]gitProc, error) {
	var procs []gitProc
	var first error
	unknown := func(command, why string) {
		procs = append(procs, gitProc{command: command, unknown: true, why: why})
		if first == nil {
			first = ownershipUnknownErr(why)
		}
	}
	for _, line := range strings.Split(psOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		uid, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pid, cmd, ok := strings.Cut(strings.TrimSpace(rest), " ")
		if !ok {
			continue
		}
		foreign := uid != self
		cmd = strings.TrimSpace(cmd)
		if !looksLikeGit(cmd) {
			continue
		}
		p := gitProc{command: cmd}
		if cwdErr != nil {
			if !commandLocatesAbsolutely(p) {
				if foreign {
					procs = append(procs, gitProc{command: cmd, foreign: true})
					continue
				}
				unknown(cmd, "cwd unreadable, "+strings.TrimPrefix(cwdErr.Error(), ownershipUnknown+": "))
				continue
			}
			procs = append(procs, p)
			continue
		}
		cwd, found := cwds[pid]
		if !found || cwd == "" {
			if foreign && !commandLocatesAbsolutely(p) {
				procs = append(procs, gitProc{command: cmd, foreign: true})
				continue
			}
			still, aerr := alive(pid)
			if aerr != nil {
				unknown(cmd, aerr.Error())
				continue
			}
			if !still {
				continue
			}
			if !commandLocatesAbsolutely(p) {
				unknown(cmd, "cwd unreadable pid="+pid)
				continue
			}
			procs = append(procs, p)
			continue
		}
		p.cwd = cwd
		p.cwdKnown = true
		procs = append(procs, p)
	}
	return procs, first
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
	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, "lsof", "-n", "-P", "-a", "-d", "cwd", "-c", "git", "-F", "pcn")
	defer cancel()
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
// matched -c git: no git is running at the moment it looks. That is a complete
// scan with no cwds, and every ps-listed git missing from it is re-checked by pid. Exit 1
// with anything said, any other failure, or output on a failed run is "lsof failed".
func lsofCwds(stdout, stderr string, code int, runErr error) (map[string]string, error) {
	cwds := map[string]string{}
	if runErr != nil {
		if code == 1 && stdout == "" && strings.TrimSpace(stderr) == "" {
			return cwds, nil
		}
		return nil, ownershipUnknownErr(fmt.Sprintf("lsof failed code=%d %s", code, stderr))
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

// darwinPIDAlive reports whether pid is still a live process. ps exiting 1 is the
// verified-vanished answer. A zombie is not live: it has exited and only waits for its
// parent to reap it, and lsof has no cwd for it. Any other failure is an
// inspection error.
func darwinPIDAlive(pid string) (bool, error) {
	ps, stopPS := subproc.Command(context.Background(), subproc.Tool, "ps", "-p", pid, "-o", "stat=")
	defer stopPS()
	out, err := ps.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return darwinStatAlive(string(out)), nil
}

// darwinStatAlive reads one `ps -o stat=` field. Empty is no process; a state that
// starts with Z is a zombie, which is dead.
func darwinStatAlive(stat string) bool {
	stat = strings.TrimSpace(stat)
	return stat != "" && stat[0] != 'Z'
}
