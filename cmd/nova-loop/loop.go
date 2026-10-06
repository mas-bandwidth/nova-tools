package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// commandExec starts the wrapped command. replaced is true when a successful
// start does not return (unix syscall.Exec). False means the command was
// waited for, which is the Windows process and every test fake that returns.
type commandExec func(argv []string) (replaced bool, err error)

// loopNameRe is the lock's file name: one token, no slash, no parent step.
var loopNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// metricsLimit is how large a restart file may be before it is refused.
const metricsLimit = 1 << 20

func runLoop(args []string, stdout, stderr io.Writer, start commandExec) (int, *filelock.FileLock) {
	fs := verbflag.New("run")
	name := fs.String("name", "", "the loop's name, one token")
	dirFlag := fs.String("dir", "", "the directory of the lock and the default metrics file")
	metricsFlag := fs.String("metrics", "", "the file the restart counter is written to")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, verbflag.Explain(fs, err)), nil
	}
	if *name == "" {
		return refuse(stderr, "--name is required; it wants the loop's name, one token of letters, digits, dots, underscores or dashes"), nil
	}
	if !loopNameRe.MatchString(*name) || strings.Contains(*name, "..") {
		return refuse(stderr, "--name "+oneline.Escape(*name)+" is not a loop name; it wants one token, with no slash and no .."), nil
	}
	command := fs.Args()
	if len(command) == 0 {
		return refuse(stderr, "the command is missing; give it after the flags, or after --"), nil
	}

	home, homeErr := os.UserHomeDir()
	dir := *dirFlag
	if dir == "" {
		dir = "~/nova-bench/run"
	}
	dir, err := expandHome(dir, home, homeErr)
	if err != nil {
		return refuse(stderr, err.Error()), nil
	}
	metricsPath := *metricsFlag
	if metricsPath == "" {
		metricsPath = filepath.Join(dir, *name+".prom")
	} else {
		metricsPath, err = expandHome(metricsPath, home, homeErr)
		if err != nil {
			return refuse(stderr, err.Error()), nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "nova-loop: the lock directory %s could not be made: %s\n", oneline.Field(dir), oneline.Err(err))
		return 1, nil
	}

	lockPath := filepath.Join(dir, *name+".lock")
	lock, err := filelock.TryLock(lockPath, "nova-loop")
	if err != nil {
		if busy, pid := lockBusy(err); busy {
			fmt.Fprintf(stderr, "nova-loop BUSY name=%s lock=%s pid=%d\n", oneline.Field(*name), oneline.Field(lockPath), pid)
			return 3, nil
		}
		fmt.Fprintf(stderr, "nova-loop: the lock %s could not be taken: %s\n", oneline.Field(lockPath), oneline.Err(err))
		return 1, nil
	}

	n, err := nextRestart(metricsPath)
	if err != nil {
		release(lock, lockPath, stderr)
		fmt.Fprintf(stderr, "nova-loop: the metrics file %s could not be read: %s\n", oneline.Field(metricsPath), oneline.Err(err))
		return 1, nil
	}
	body := metricsText(*name, n)
	if err := atomicfile.Write(metricsPath, body, 0o644); err != nil {
		release(lock, lockPath, stderr)
		fmt.Fprintf(stderr, "nova-loop: the metrics file %s could not be written: %s\n", oneline.Field(metricsPath), oneline.Err(err))
		return 1, nil
	}
	if _, err := fmt.Fprintf(stdout, "nova-loop OK name=%s restarts=%d\n", oneline.Field(*name), n); err != nil {
		release(lock, lockPath, stderr)
		return 1, nil
	}

	replaced, err := start(command)
	if err != nil {
		release(lock, lockPath, stderr)
		var exitErr *exec.ExitError
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return refuse(stderr, "the command was not found: "+command[0]), nil
		case errors.As(err, &exitErr):
			code := exitErr.ExitCode()
			if code < 0 {
				code = 1
			}
			return code, nil
		default:
			fmt.Fprintf(stderr, "nova-loop: the command could not be started: %s\n", oneline.Err(err))
			return 1, nil
		}
	}
	if !replaced {
		release(lock, lockPath, stderr)
		return 0, nil
	}
	return 0, lock
}

func lockBusy(err error) (bool, int) {
	if he, ok := filelock.AsHeldError(err); ok {
		return true, he.Holder.PID
	}
	if errors.Is(err, filelock.ErrHeld) || errors.Is(err, filelock.ErrBusy) {
		return true, 0
	}
	return false, 0
}

func release(lock *filelock.FileLock, lockPath string, stderr io.Writer) {
	if err := lock.Unlock(); err != nil {
		fmt.Fprintf(stderr, "nova-loop: the lock %s could not be released: %s\n", oneline.Field(lockPath), oneline.Err(err))
	}
}

func expandHome(path, home string, homeErr error) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	if homeErr != nil || home == "" {
		return "", errors.New("the home directory could not be read, so ~ was not expanded; pass --dir")
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func nextRestart(path string) (int, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	if st.Size() > metricsLimit {
		return 0, fmt.Errorf("it is %d bytes, over %d", st.Size(), metricsLimit)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return restartCount(string(raw)) + 1, nil
}

func restartCount(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "nova_loop_restarts_total{") {
			continue
		}
		_, rest, ok := strings.Cut(line, "} ")
		if !ok {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil || v < 0 {
			continue
		}
		n = v
	}
	return n
}

func metricsText(name string, n int) []byte {
	return []byte(fmt.Sprintf("# HELP nova_loop_restarts_total Times this loop took its lock and started.\n# TYPE nova_loop_restarts_total counter\nnova_loop_restarts_total{loop=%q} %d\n", name, n))
}
