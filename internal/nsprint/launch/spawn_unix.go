//go:build unix

package launch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
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
func startDetached(wrapper string, l Line, wait time.Duration) (int, string, error) {
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
		Path:        wrapper,
		Args:        []string{WrapperName, l.Card()},
		Stdin:       r,
		Env:         append(os.Environ(), LaunchAckFDEnv+"=3"),
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
	defer cmd.Process.Release() // the acknowledged wrapper outlives this process
	if werr != nil {
		ackR.Close()
		return pid, "", werr
	}
	if cerr != nil {
		ackR.Close()
		return pid, "", cerr
	}
	if wait <= 0 {
		ackR.Close()
		return pid, "", fmt.Errorf("wrapper acknowledgement timed out")
	}
	if err := ackR.SetReadDeadline(time.Now().Add(wait)); err != nil {
		ackR.Close()
		return pid, "", err
	}
	line, err := bufio.NewReader(io.LimitReader(ackR, maxLine)).ReadString('\n')
	ackR.Close()
	if err != nil {
		return pid, "", fmt.Errorf("wrapper acknowledgement: %w", err)
	}
	ack := strings.TrimSpace(line)
	if ack != "LAUNCHED" && !strings.HasPrefix(ack, "REFUSED ") {
		return pid, "", fmt.Errorf("wrapper acknowledgement %q is invalid", ack)
	}
	return pid, ack, nil
}
