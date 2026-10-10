package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// demoReady is how long the demo's Redis has to say it is ready once started,
// and demoStopWait how long a signalled Redis has to hang up.
const (
	demoReady    = 30 * time.Second
	demoStopWait = 10 * time.Second
)

// demoReadyLine is what redis-server writes to its log once it accepts
// connections; the start waits on that line, read from the server's own
// output, never on a clock.
const demoReadyLine = "Ready to accept connections"

// startDemoRedis starts the redis-server program in dir, listening on one free
// port of 127.0.0.1 and nowhere else, keeping nothing (--save "", no
// append-only file). A server that lost its port to another process between
// the choice and the bind exits at start and is started again on another.
func startDemoRedis(ctx context.Context, program, dir string) (demoServer, error) {
	bin, err := exec.LookPath(program)
	if err != nil {
		return demoServer{}, fmt.Errorf("no %s to run the demo (%v); run: install Redis 8.10 or newer, or nova-sprint demo load --redis-server <the path of redis-server>", program, err)
	}
	var last error
	for try := 0; try < 3; try++ {
		srv, err := startDemoRedisOnce(ctx, bin, dir)
		if err == nil {
			return srv, nil
		}
		last = err
	}
	return demoServer{}, last
}

// startDemoRedisOnce starts one server and reads its output (into
// dir/redis.log) until it says it is ready, it exits, or demoReady passes;
// then it must answer INFO with the pid started.
func startDemoRedisOnce(ctx context.Context, bin, dir string) (demoServer, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return demoServer{}, err
	}
	addr := l.Addr().String()
	// ignored: the port is handed to redis-server; a close error leaves it bound, and the bind below fails and is retried
	_ = l.Close()
	_, port, _ := net.SplitHostPort(addr)
	logf, err := os.OpenFile(filepath.Join(dir, "redis.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return demoServer{}, err
	}
	// the server outlives the verb, and demo stop ends it: it runs under the
	// verb's own context, which demo load never cancels (the wait below has a
	// deadline of its own)
	cmd := subproc.Long(ctx, bin, "--bind", "127.0.0.1", "--port", port, "--dir", dir,
		"--save", "", "--appendonly", "no", "--protected-mode", "yes", "--daemonize", "no")
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = logf.Close() // ignored: the log of a server never started; the pipe's error is what is returned
		return demoServer{}, err
	}
	if err := cmd.Start(); err != nil {
		_ = logf.Close() // ignored: the log of a server never started; the start's error is what is returned
		return demoServer{}, fmt.Errorf("%s does not start: %v; run: install Redis 8.10 or newer, or nova-sprint demo load --redis-server <the path of redis-server>", bin, err)
	}
	ready, exited := make(chan struct{}), make(chan struct{})
	go func() {
		// the server's output is copied into the log while the verb runs, so
		// it never blocks on a full pipe; once the verb exits its later lines
		// have no reader, and redis-server ignores SIGPIPE
		r, said := bufio.NewReader(out), false
		for {
			line, err := r.ReadString('\n')
			// ignored: the log is a copy of what the server said; a short write loses a line of it
			_, _ = io.WriteString(logf, line)
			if !said && strings.Contains(line, demoReadyLine) {
				said = true
				close(ready)
			}
			// ignored: the read's error is the server's output ending as it exits; exited says so
			if err != nil {
				// ignored: the log's close; what it holds is read back for a failure's line
				_ = logf.Close()
				// ignored: the server's exit at start; its log says why
				_ = cmd.Wait()
				close(exited)
				return
			}
		}
	}()
	wait, cancel := context.WithTimeout(ctx, demoReady)
	defer cancel()
	srv := demoServer{Addr: addr, PID: cmd.Process.Pid}
	select {
	case <-ready:
	case <-exited:
		return demoServer{}, fmt.Errorf("redis-server exited at start: %s", demoLogTail(dir))
	case <-wait.Done():
		// ignored: the server that did not come up is ended; the timeout is what is reported
		_ = cmd.Process.Kill()
		return demoServer{}, fmt.Errorf("redis-server did not say it was ready on %s in %s: %s", addr, demoReady, demoLogTail(dir))
	}
	if err := demoOwns(demoState{Addr: addr, PID: srv.PID}); err != nil {
		// ignored: a server that is not the one started is not the demo's; the mismatch is what is reported
		_ = cmd.Process.Kill()
		return demoServer{}, err
	}
	return srv, nil
}

func demoLogTail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "redis.log"))
	if err != nil {
		return "no log"
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, " | ")
}

// demoProcessAlive is whether the pid names a running process.
func demoProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killDemoRedis asks the demo's Redis to stop (SIGTERM to the recorded pid)
// and returns once it has hung up a connection held open to it: the server
// closes its connections as it exits, so the wait is a blocking read, never a
// clock loop. One still connected after demoStopWait is killed, and must hang
// up in another demoStopWait.
func killDemoRedis(st demoState) error {
	p, err := os.FindProcess(st.PID)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp4", st.Addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("%s does not answer: %v", st.Addr, err)
	}
	// ignored: a connection only held to see the server hang up
	defer func() { _ = conn.Close() }()
	if err := p.Signal(syscall.SIGTERM); err != nil && demoProcessAlive(st.PID) {
		return err
	}
	if demoHungUp(conn, demoStopWait) {
		return nil
	}
	if err := p.Kill(); err != nil && demoProcessAlive(st.PID) {
		return err
	}
	if demoHungUp(conn, demoStopWait) {
		return nil
	}
	return fmt.Errorf("pid %d still holds %s open after SIGTERM and SIGKILL", st.PID, st.Addr)
}

// demoHungUp is whether the server ends the connection within wait: a read
// that returns anything but a timeout is the server's hang-up (the
// connection sends no command, so the server writes nothing to it).
func demoHungUp(conn net.Conn, wait time.Duration) bool {
	if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return false
	}
	var b [1]byte
	_, err := conn.Read(b[:])
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}
