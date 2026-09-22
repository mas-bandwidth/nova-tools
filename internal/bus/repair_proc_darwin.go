//go:build darwin

package bus

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// gitProcesses lists live git processes. ps -ww is the command line, wide enough that a
// checkout path is not cut off mid-spelling; lsof supplies the cwd for a git that was
// started inside the checkout and so does not carry the path in its arguments. If lsof
// cannot run, the command line is still enough for a git this tool started with -C, and
// a process whose cwd we cannot see is not treated as absent when its arguments name the
// checkout.
func gitProcesses() ([]gitProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=", "-o", "command=").Output()
	if err != nil {
		return nil, err
	}
	cwds := darwinGitCwd()
	var procs []gitProc
	for _, line := range strings.Split(string(out), "\n") {
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
		procs = append(procs, gitProc{command: cmd, cwd: cwds[pid]})
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

func darwinGitCwd() map[string]string {
	out, err := exec.Command("lsof", "-n", "-P", "-a", "-d", "cwd", "-c", "git", "-F", "pcn").Output()
	if err != nil {
		return nil
	}
	cwds := map[string]string{}
	var pid string
	for _, line := range strings.Split(string(out), "\n") {
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
	return cwds
}
