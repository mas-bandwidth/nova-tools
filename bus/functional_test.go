//go:build functional

package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wait = 20 * time.Second

// startRedis starts a redis-server on a free local port for this test only.
func startRedis(t *testing.T) string {
	t.Helper()
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	require.NoError(t, l.Close())
	cmd := exec.Command(server, "--port", port, "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: the server is ours and disposable
	addr := "127.0.0.1:" + port
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: closing a test client
	require.Eventually(t, func() bool { return c.Ping(context.Background()).Err() == nil }, wait, 20*time.Millisecond)
	return addr
}

// hookProc is a nova-bus hook running as its own process; lines carries its stdout.
type hookProc struct {
	cmd   *exec.Cmd
	lines chan string
}

func startHook(t *testing.T, bin, addr string) *hookProc {
	t.Helper()
	cmd := exec.Command(bin, "hook", "--as", "beta")
	cmd.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	h := &hookProc{cmd: cmd, lines: make(chan string, 16)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			h.lines <- sc.Text()
		}
		close(h.lines)
	}()
	t.Cleanup(h.kill)
	return h
}

func (h *hookProc) kill() { _ = h.cmd.Process.Signal(os.Kill); _ = h.cmd.Wait() } // ignored: may already be dead

func (h *hookProc) next(t *testing.T) string {
	t.Helper()
	select {
	case l, ok := <-h.lines:
		require.True(t, ok, "hook exited")
		return l
	case <-time.After(wait):
		require.FailNow(t, "no line from the hook")
		return ""
	}
}

// TestAHookKilledAndRestarted sends to two inboxes, hooks one in as a process,
// SIGKILLs it while it is blocked in a read, leaves one message handed and
// unacked (a hook that died between print and ack), and hooks back in: the
// unacked message is printed again, the acked ones are not.
func TestAHookKilledAndRestarted(t *testing.T) {
	t.Parallel()
	addr := startRedis(t)
	bin := filepath.Join(t.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: closing a test client
	send := func(subject, body string) {
		t.Helper()
		cmd := exec.Command(bin, "send", "--as", "alpha", "--to", "beta,gamma", subject, body)
		cmd.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		require.Equal(t, 2, strings.Count(string(out), "\n"), string(out))
	}
	pending := func() int64 {
		p, err := c.XPending(ctx, "bus:beta", group).Result()
		require.NoError(t, err)
		return p.Count
	}

	send("one", "first\nline")
	send("two", "second")
	first := startHook(t, bin, addr)
	assert.True(t, strings.HasSuffix(first.next(t), "\talpha\t-\tone\tfirst\\nline"))
	assert.True(t, strings.HasSuffix(first.next(t), "\talpha\t-\ttwo\tsecond"))
	require.Eventually(t, func() bool { return pending() == 0 }, wait, 20*time.Millisecond, "both acked")
	first.kill() // blocked in XREADGROUP > with nothing pending

	send("three", "third")
	handed, err := c.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: group, Streams: []string{"bus:beta", ">"}, Count: 1, Block: -1}).Result()
	require.NoError(t, err)
	require.Len(t, handed[0].Messages, 1)
	require.Equal(t, int64(1), pending(), "three handed, not acked")

	second := startHook(t, bin, addr)
	assert.Equal(t, handed[0].Messages[0].ID+"\talpha\t-\tthree\tthird", second.next(t), "the unacked message is printed again first")
	send("four", "fourth")
	assert.True(t, strings.HasSuffix(second.next(t), "\tfour\tfourth"), "then the new one; one and two are never printed again")
	require.Eventually(t, func() bool { return pending() == 0 }, wait, 20*time.Millisecond, "all acked")
}
