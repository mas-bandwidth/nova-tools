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
	"syscall"
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
	return h.nextWithin(t, wait)
}

func (h *hookProc) nextWithin(t *testing.T, d time.Duration) string {
	t.Helper()
	select {
	case l, ok := <-h.lines:
		require.True(t, ok, "hook exited")
		return l
	case <-time.After(d):
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

	// The killed hook's key holds for up to 30 s: the restarted hook waits it
	// out, as a supervised restart does, then claims the name.
	second := startHook(t, bin, addr)
	assert.Equal(t, handed[0].Messages[0].ID+"\talpha\t-\tthree\tthird", second.nextWithin(t, presenceTTL+wait), "the unacked message is printed again first")
	send("four", "fourth")
	assert.True(t, strings.HasSuffix(second.next(t), "\tfour\tfourth"), "then the new one; one and two are never printed again")
	require.Eventually(t, func() bool { return pending() == 0 }, wait, 20*time.Millisecond, "all acked")
	n, err := c.XLen(ctx, "bus:beta").Result()
	require.NoError(t, err)
	assert.Zero(t, n, "each acked entry is deleted: the inbox holds only what waits")
}

// TestPresenceWhoAndPending runs who and pending against a real Redis while a
// hook process runs, then stops it cleanly (SIGTERM: presence deleted) and by
// SIGKILL (presence left to lapse by its TTL).
func TestPresenceWhoAndPending(t *testing.T) {
	t.Parallel()
	addr := startRedis(t)
	bin := filepath.Join(t.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: closing a test client
	bus := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}

	assert.Equal(t, "0\n", bus("pending", "--as", "beta"), "no stream yet")
	one := strings.Fields(bus("send", "--as", "alpha", "--to", "beta", "one", "1"))[1]
	two := strings.Fields(bus("send", "--as", "alpha", "--to", "beta", "two", "2"))[1]
	assert.Equal(t, "2\n", bus("pending", "--as", "beta"), "no group yet: the whole stream waits")
	_, err := c.XGroupCreateMkStream(ctx, "bus:beta", group, "0").Result()
	require.NoError(t, err)
	_, err = c.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: group, Streams: []string{"bus:beta", ">"}, Count: 1, Block: -1}).Result()
	require.NoError(t, err, "one handed, not acked")
	assert.Equal(t, "2\n"+one+"\tone\n"+two+"\ttwo\n", bus("pending", "--as", "beta", "--list"))
	assert.Equal(t, "2\n", bus("pending", "--as", "beta"), "pending consumed nothing")
	assert.Empty(t, bus("who"))

	h := startHook(t, bin, addr)
	h.next(t)
	h.next(t)
	require.Eventually(t, func() bool { return bus("pending", "--as", "beta") == "0\n" }, wait, 20*time.Millisecond)
	assert.Regexp(t, `^beta\t\d+s\n$`, bus("who"))
	ttl, err := c.TTL(ctx, "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= presenceTTL, ttl)
	second := exec.Command(bin, "hook", "--as", "beta")
	second.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	out, err := second.CombinedOutput()
	assert.Equal(t, 1, second.ProcessState.ExitCode(), string(out))
	assert.Contains(t, string(out), "HOOK WAIT name=beta ttl=30\n")
	assert.Contains(t, string(out), "name already hooked: beta", "after 40 s of the first hook refreshing")
	assert.Regexp(t, `^beta\t\d+s\n$`, bus("who"), "the first hook's presence is untouched")
	stopped := time.Now()
	require.NoError(t, h.cmd.Process.Signal(syscall.SIGTERM))
	require.NoError(t, h.cmd.Wait(), "a clean exit")
	assert.Less(t, time.Since(stopped), 5*time.Second, "a blocked read does not hold up the exit")
	assert.Empty(t, bus("who"), "a clean exit deletes presence")

	k := startHook(t, bin, addr)
	require.Eventually(t, func() bool { return bus("who") != "" }, wait, 20*time.Millisecond)
	k.kill()
	assert.Regexp(t, `^beta\t`, bus("who"), "a crashed hook stays present until the TTL")
	ttl, err = c.TTL(ctx, "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= presenceTTL, "and then lapses: %v", ttl)

	require.NoError(t, c.XAdd(ctx, &redis.XAddArgs{Stream: "bus:presence:zeta", Values: []string{"k", "v"}}).Err())
	assert.Regexp(t, `^beta\t\d+s\nzeta\t\?\n$`, bus("who"), "a key that is not a string is shown, not fatal")
}

// TestHookOnAClosedPipe is hook | true: the reader is gone before the first
// line, the write fails with EPIPE (SIGPIPE ignored), and the hook releases
// its presence and exits 1, leaving the message pending.
func TestHookOnAClosedPipe(t *testing.T) {
	t.Parallel()
	addr := startRedis(t)
	bin := filepath.Join(t.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: closing a test client
	env := append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	send := exec.Command(bin, "send", "--as", "alpha", "--to", "beta", "one", "1")
	send.Env = env
	out, err := send.CombinedOutput()
	require.NoError(t, err, string(out))

	r, w, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, r.Close())
	var stderr strings.Builder
	hook := exec.Command(bin, "hook", "--as", "beta")
	hook.Env, hook.Stdout, hook.Stderr = env, w, &stderr
	err = hook.Run()
	require.NoError(t, w.Close())
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 1, exit.ExitCode(), "exit 1, not killed by SIGPIPE: %s", stderr.String())
	assert.Contains(t, stderr.String(), "broken pipe")
	n, err := c.Exists(ctx, "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.Zero(t, n, "presence released")
	p, err := c.XPending(ctx, "bus:beta", group).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), p.Count, "the message was not printed, so not acked")
}
