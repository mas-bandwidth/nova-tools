//go:build unix

package launch

import (
	"io"
	"os"
	"os/exec"
	"syscall"
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
func startDetached(wrapper string, l Line) (int, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	cmd := &exec.Cmd{
		Path:        wrapper,
		Args:        []string{WrapperName, l.Card()},
		Stdin:       r,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return 0, err
	}
	r.Close()
	_, werr := io.WriteString(w, l.String()+"\n")
	cerr := w.Close()
	pid := cmd.Process.Pid
	// Never waited: the wrapper outlives this process and is reparented.
	_ = cmd.Process.Release()
	if werr != nil {
		return pid, werr
	}
	return pid, cerr
}
