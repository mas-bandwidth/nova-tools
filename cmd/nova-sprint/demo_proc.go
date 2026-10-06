package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// demoReady is how long the demo's Redis has to answer once started, and
// demoStopWait how long a signalled Redis has to be gone.
const (
	demoReady    = 30 * time.Second
	demoStopWait = 10 * time.Second
)

// startDemoRedis starts, under the verb's context, the redis-server on PATH in dir, listening on one
// free port of 127.0.0.1 and nowhere else, keeping nothing (--save "", no
// append-only file), its log in dir. It is ready when it answers PING and
// says, by INFO, that it is the process started; a server that lost its port
// to another process between the choice and the bind is started again on
// another.
func startDemoRedis(ctx context.Context, dir string) (demoServer, error) {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		return demoServer{}, fmt.Errorf("no redis-server on PATH (%v); run: install Redis 8.10 or newer, then nova-sprint demo load again", err)
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

func startDemoRedisOnce(ctx context.Context, bin, dir string) (demoServer, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return demoServer{}, err
	}
	addr := l.Addr().String()
	// ignored: the port is handed to redis-server; a close error leaves it bound, and the bind below fails and is retried
	_ = l.Close()
	_, port, _ := net.SplitHostPort(addr)
	// the verb's context ends a start it gives up; the server outlives the
	// verb, and demo stop ends it
	cmd := subproc.Long(ctx, bin, "--bind", "127.0.0.1", "--port", port, "--dir", dir,
		"--save", "", "--appendonly", "no", "--protected-mode", "yes", "--daemonize", "no",
		"--logfile", filepath.Join(dir, "redis.log"))
	if err := cmd.Start(); err != nil {
		return demoServer{}, err
	}
	exited := make(chan struct{})
	go func() {
		// ignored: the server outlives this process; an early exit is seen by exited
		_ = cmd.Wait()
		close(exited)
	}()
	srv := demoServer{Addr: addr, PID: cmd.Process.Pid}
	deadline := time.Now().Add(demoReady)
	for {
		select {
		case <-exited:
			return demoServer{}, fmt.Errorf("redis-server exited at start: %s", demoLogTail(dir))
		default:
		}
		if demoOwns(demoState{Addr: addr, PID: srv.PID}) == nil {
			return srv, nil
		}
		if time.Now().After(deadline) {
			// ignored: the server that did not come up is ended; the timeout is what is reported
			_ = cmd.Process.Kill()
			return demoServer{}, fmt.Errorf("redis-server did not answer on %s in %s: %s", addr, demoReady, demoLogTail(dir))
		}
		time.Sleep(50 * time.Millisecond)
	}
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

// killDemoRedis asks the pid to stop (SIGTERM), and kills it when it is still
// running after demoStopWait; it returns once the pid is gone.
func killDemoRedis(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil && demoProcessAlive(pid) {
		return err
	}
	if demoGone(pid, demoStopWait) {
		return nil
	}
	if err := p.Kill(); err != nil && demoProcessAlive(pid) {
		return err
	}
	if demoGone(pid, demoStopWait) {
		return nil
	}
	return fmt.Errorf("pid %d is still running after SIGTERM and SIGKILL", pid)
}

func demoGone(pid int, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for demoProcessAlive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}
