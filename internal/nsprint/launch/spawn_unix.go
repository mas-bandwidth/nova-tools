//go:build unix

package launch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// startDetached starts the wrapper for one line in its own session and does
// not wait for it. The wrapper gets:
//
//   - argv nova-card <sprint>/<label>/<attempt>, its command identity;
//   - stdin a pipe holding the one canonical line, then EOF;
//   - stdout and stderr the per-attempt wrapper log at logPath (a file
//     descriptor, never a pipe), so no descriptor of the ssh session
//     survives in it and the session closes when the launcher returns, and
//     what the wrapper prints after LAUNCHED -- nova-card's keepRefusal line
//     when Redis will not take a refusal -- is kept where the receipt says;
//   - setsid, so it leads a new session and process group: the session's
//     hang-up and a kill of the session's group do not reach it.
//
// The line is under PIPE_BUF, so the write completes without a reader.
func startDetached(wrapper string, l Line, deadline time.Time, logPath string) (int, string, error) {
	return startDetachedLogged(wrapper, []string{WrapperName, l.Card()}, l.String(), deadline, nil, logPath)
}

// startDetachedArgsEnv is startDetached for any argv, one stdin line (a
// sprint card's launch line, or a copy's <copy> <token>, #3998) and extra
// environment for the child (the bench's card.env, read by the beat and
// never applied to itself). The wrapper log is WrapperLogPath's.
func startDetachedArgsEnv(wrapper string, args []string, stdinLine string, deadline time.Time, env []string) (int, string, error) {
	return startDetachedLogged(wrapper, args, stdinLine, deadline, env, WrapperLogPath(wrapper, env, args))
}

// startDetachedLogged is the one process start. A wrapper log that cannot be
// opened is a refusal that names the path: a wrapper whose output has
// nowhere to go is not started.
func startDetachedLogged(wrapper string, args []string, stdinLine string, deadline time.Time, env []string, logPath string) (int, string, error) {
	if !deadline.After(time.Now()) {
		return 0, "", fmt.Errorf("wrapper acknowledgement timed out")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, "", fmt.Errorf("wrapper log %s: %w", logPath, err)
	}
	logf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return 0, "", fmt.Errorf("wrapper log %s: %w", logPath, err)
	}
	// The launcher's own descriptor closes on every path out; the started
	// wrapper holds its own copy.
	defer logf.Close()
	r, w, err := os.Pipe()
	if err != nil {
		return 0, "", err
	}
	ackR, ackW, err := os.Pipe()
	if err != nil {
		r.Close()
		w.Close()
		return 0, "", err
	}
	cmd := &exec.Cmd{
		Path:   wrapper,
		Args:   args,
		Stdin:  r,
		Stdout: logf,
		Stderr: logf,
		Env: append(append(os.Environ(), env...),
			LaunchAckFDEnv+"=3",
			LaunchDeadlineEnv+"="+strconv.FormatInt(deadline.UnixMilli(), 10),
		),
		ExtraFiles:  []*os.File{ackW},
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		ackR.Close()
		ackW.Close()
		return 0, "", err
	}
	r.Close()
	ackW.Close()
	_, werr := io.WriteString(w, stdinLine+"\n")
	cerr := w.Close()
	pid := cmd.Process.Pid
	release := true
	defer func() {
		if release {
			_ = cmd.Process.Release() // the acknowledged wrapper outlives this process
		}
	}()
	abort := func() {
		// The wrapper leads its own process group. Stop it before returning a
		// timeout refusal so it cannot continue toward a late launch.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
		release = false
	}
	if werr != nil {
		ackR.Close()
		abort()
		return pid, "", werr
	}
	if cerr != nil {
		ackR.Close()
		abort()
		return pid, "", cerr
	}
	if err := ackR.SetReadDeadline(deadline); err != nil {
		ackR.Close()
		abort()
		return pid, "", err
	}
	line, err := bufio.NewReader(io.LimitReader(ackR, maxLine)).ReadString('\n')
	ackR.Close()
	if err != nil {
		abort()
		return pid, "", fmt.Errorf("wrapper acknowledgement: %w", err)
	}
	ack := strings.TrimSpace(line)
	if ack != "LAUNCHED" && !strings.HasPrefix(ack, "REFUSED ") {
		abort()
		return pid, "", fmt.Errorf("wrapper acknowledgement %q is invalid", ack)
	}
	return pid, ack, nil
}
