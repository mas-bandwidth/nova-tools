//go:build functional

package main

// Benchmarks, functional tier (a redis-server and nova-bus processes of
// their own): how long a sent message takes to reach a hook's stdout, and
// what a hook costs while nothing is sent. Run them on a bench:
//
//	go test -tags functional -run '^$' -bench . -benchtime 3000x ./cmd/nova-bus/

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// benchRedis starts a disposable redis-server and returns its address.
func benchRedis(b *testing.B) string {
	b.Helper()
	server, err := exec.LookPath("redis-server")
	if err != nil {
		b.Skip("redis-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	if err := l.Close(); err != nil {
		b.Fatal(err)
	}
	cmd := exec.Command(server, "--port", port, "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", b.TempDir())
	if err := cmd.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: the server is ours and disposable
	addr := "127.0.0.1:" + port
	for end := time.Now().Add(wait); ; {
		if conn, err := net.Dial("tcp", addr); err == nil {
			_ = conn.Close() // ignored: a probe
			return addr
		}
		if time.Now().After(end) {
			b.Fatal("redis-server did not listen")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// benchHook builds nova-bus and starts `nova-bus hook --as beta` as its own
// process; it returns the binary, the process and its stdout lines, after
// HOOK OK.
func benchHook(b *testing.B, addr string) (string, *exec.Cmd, chan string) {
	b.Helper()
	bin := filepath.Join(b.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		b.Fatal(err)
	}
	cmd := exec.Command(bin, "hook", "--as", "beta")
	cmd.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	out, err := cmd.StdoutPipe()
	if err != nil {
		b.Fatal(err)
	}
	errOut, err := cmd.StderrPipe()
	if err != nil {
		b.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: may already have been stopped
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	sc := bufio.NewScanner(errOut)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "HOOK OK ") {
			go func() {
				for sc.Scan() { // drain: a full stderr pipe would block the hook
				}
			}()
			return bin, cmd, lines
		}
	}
	b.Fatal("the hook exited before HOOK OK")
	return "", nil, nil
}

// quantile is the q quantile of sorted ds.
func quantile(ds []time.Duration, q float64) time.Duration {
	return ds[min(len(ds)-1, int(q*float64(len(ds))))]
}

// BenchmarkFunctionalSendToHook is the latency of one message from the
// start of `nova-bus send` (a process, as nova-sprint sends) to its line on
// a hooked process's stdout: p50 and p99 over b.N messages, and the send
// process's own time.
func BenchmarkFunctionalSendToHook(b *testing.B) {
	addr := benchRedis(b)
	bin, _, lines := benchHook(b, addr)
	c := redis.NewClient(&redis.Options{Addr: addr})
	b.Cleanup(func() { _ = c.Close() }) // ignored: closing a bench client
	// the sender's presence, as its own hook would hold it: send refuses a sender no hook holds
	if err := c.Set(context.Background(), "bus:presence:alpha", "2026-11-05T12:00:00Z\tplanted", time.Hour).Err(); err != nil {
		b.Fatal(err)
	}
	var total, sends []time.Duration
	n := 0
	for b.Loop() {
		n++
		body := fmt.Sprintf("m%d", n)
		cmd := exec.Command(bin, "send", "--as", "alpha", "--to", "beta", "bench", body)
		cmd.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
		start := time.Now()
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("send: %v: %s", err, out)
		}
		sends = append(sends, time.Since(start))
		select {
		case l, ok := <-lines:
			if !ok {
				b.Fatal("the hook exited")
			}
			if !strings.HasSuffix(l, "\tbench\t"+body) {
				b.Fatalf("line %q, want %s", l, body)
			}
			total = append(total, time.Since(start))
		case <-time.After(wait):
			b.Fatal("no line from the hook")
		}
	}
	slices.Sort(total)
	slices.Sort(sends)
	b.ReportMetric(float64(quantile(total, 0.5).Microseconds()), "p50-us")
	b.ReportMetric(float64(quantile(total, 0.99).Microseconds()), "p99-us")
	b.ReportMetric(float64(quantile(sends, 0.5).Microseconds()), "send-p50-us")
	b.ReportMetric(float64(quantile(sends, 0.99).Microseconds()), "send-p99-us")
}

// BenchmarkFunctionalAddToHook is the latency from one XADD to the inbox,
// as send writes it, to its line on a hooked process's stdout: the hook's
// own delivery, without the send process.
func BenchmarkFunctionalAddToHook(b *testing.B) {
	addr := benchRedis(b)
	_, _, lines := benchHook(b, addr)
	c := redis.NewClient(&redis.Options{Addr: addr})
	b.Cleanup(func() { _ = c.Close() }) // ignored: closing a bench client
	ctx := context.Background()
	var total []time.Duration
	n := 0
	for b.Loop() {
		n++
		body := fmt.Sprintf("m%d", n)
		start := time.Now()
		if err := c.XAdd(ctx, &redis.XAddArgs{Stream: "bus:beta", Values: map[string]any{"from": "alpha", "subject": "bench", "to": "beta", "body": body, "re": "-"}}).Err(); err != nil {
			b.Fatal(err)
		}
		select {
		case l, ok := <-lines:
			if !ok {
				b.Fatal("the hook exited")
			}
			if !strings.HasSuffix(l, "\tbench\t"+body) {
				b.Fatalf("line %q, want %s", l, body)
			}
			total = append(total, time.Since(start))
		case <-time.After(wait):
			b.Fatal("no line from the hook")
		}
	}
	slices.Sort(total)
	b.ReportMetric(float64(quantile(total, 0.5).Microseconds()), "p50-us")
	b.ReportMetric(float64(quantile(total, 0.99).Microseconds()), "p99-us")
}

// switches is the context switches of every thread of process pid so far
// (Linux /proc), -1 where there is no /proc.
func switches(pid int) int64 {
	tasks, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/status", pid))
	if err != nil || len(tasks) == 0 {
		return -1
	}
	var n int64
	for _, t := range tasks {
		data, err := os.ReadFile(t)
		if err != nil {
			continue // a thread that ended between the glob and the read
		}
		for _, l := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(l, "voluntary_ctxt_switches:"); ok {
				k, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64) // a malformed count reads as 0
				n += k
			} else if v, ok := strings.CutPrefix(l, "nonvoluntary_ctxt_switches:"); ok {
				k, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				n += k
			}
		}
	}
	return n
}

// BenchmarkFunctionalHookIdle is what a hooked process costs while nothing
// is sent, over one minute: its context switches (wakeups) and the redis
// commands it issues (a script's own calls included), each per minute, and
// its CPU time and switches over its life. It runs once whatever b.N is, so
// ns/op is the minute divided by b.N and means nothing.
func BenchmarkFunctionalHookIdle(b *testing.B) {
	addr := benchRedis(b)
	_, cmd, _ := benchHook(b, addr)
	c := redis.NewClient(&redis.Options{Addr: addr})
	b.Cleanup(func() { _ = c.Close() }) // ignored: closing a bench client
	ctx := context.Background()
	calls := func() int64 {
		info, err := c.Info(ctx, "stats").Result()
		if err != nil {
			b.Fatal(err)
		}
		for _, l := range strings.Split(info, "\r\n") {
			if v, ok := strings.CutPrefix(l, "total_commands_processed:"); ok {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					b.Fatal(err)
				}
				return n
			}
		}
		b.Fatal("no total_commands_processed in INFO stats")
		return 0
	}
	const window = time.Minute
	time.Sleep(2 * time.Second) // past the hook's start
	before, sw := calls(), switches(cmd.Process.Pid)
	b.ResetTimer()
	time.Sleep(window)
	b.StopTimer()
	cmds := calls() - before - 1 // the INFO that read before
	if sw >= 0 {
		b.ReportMetric(float64(switches(cmd.Process.Pid)-sw)/window.Minutes(), "wakeups/min")
	}
	if err := cmd.Process.Signal(os.Kill); err != nil {
		b.Fatal(err)
	}
	_ = cmd.Wait() // ignored: killed
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		b.Skip("no rusage on this platform")
	}
	// rusage covers the whole life (start included); the window is most of it.
	b.ReportMetric(float64(time.Duration(ru.Utime.Nano()+ru.Stime.Nano()).Microseconds())/1000, "cpu-ms-life")
	b.ReportMetric(float64(ru.Nvcsw+ru.Nivcsw), "ctxsw-life")
	b.ReportMetric(float64(cmds)/window.Minutes(), "redis-cmds/min")
}
