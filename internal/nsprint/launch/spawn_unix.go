//go:build unix

package launch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
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
//   - stdout and stderr /dev/null, so no descriptor of the ssh session
//     survives in it and the session closes when the launcher returns;
//   - setsid, so it leads a new session and process group: the session's
//     hang-up and a kill of the session's group do not reach it.
//
// The line is under PIPE_BUF, so the write completes without a reader.
func startDetached(wrapper string, l Line, deadline time.Time) (int, string, error) {
	if !deadline.After(time.Now()) {
		return 0, "", fmt.Errorf("wrapper acknowledgement timed out")
	}
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
		Path:  wrapper,
		Args:  []string{WrapperName, l.Card()},
		Stdin: r,
		Env: append(os.Environ(),
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
	_, werr := io.WriteString(w, l.String()+"\n")
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
