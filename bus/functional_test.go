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

// startRedis starts a redis-server on a free local port for this test only,
// with any extra configuration arguments.
func startRedis(t *testing.T, extra ...string) string {
	t.Helper()
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	require.NoError(t, l.Close())
	cmd := exec.Command(server, append([]string{"--port", port, "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", t.TempDir()}, extra...)...)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: the server is ours and disposable
	addr := "127.0.0.1:" + port
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			_ = conn.Close() // ignored: a probe
		}
		return err == nil
	}, wait, 20*time.Millisecond)
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
		cmd := exec.Command(bin, "send", "--as", "alpha", "--to", "beta,gamma", "--new", subject, body)
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

	hookIn(t, c, "alpha")
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

	hookIn(t, c, "alpha") // the sender, and beta for pending: both act as their name
	hookIn(t, c, "beta")
	assert.Equal(t, "PENDING OK waiting=0\n", bus("pending", "--as", "beta"), "no stream yet")
	one := strings.Fields(bus("send", "--as", "alpha", "--to", "beta", "--new", "one", "1"))[1]
	two := strings.Fields(bus("send", "--as", "alpha", "--to", "beta", "--new", "two", "2"))[1]
	assert.Equal(t, "PENDING OK waiting=2\n", bus("pending", "--as", "beta"), "no group yet: the whole stream waits")
	_, err := c.XGroupCreateMkStream(ctx, "bus:beta", group, "0").Result()
	require.NoError(t, err)
	_, err = c.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: group, Streams: []string{"bus:beta", ">"}, Count: 1, Block: -1}).Result()
	require.NoError(t, err, "one handed, not acked")
	assert.Equal(t, "PENDING OK waiting=2\n"+one+"\talpha\tone\n"+two+"\talpha\ttwo\n", bus("pending", "--as", "beta", "--list"))
	assert.Equal(t, "PENDING OK waiting=2\n", bus("pending", "--as", "beta"), "pending consumed nothing")
	refused := exec.Command(bin, "send", "--as", "alpha", "--to", "nobody", "s", "b")
	refused.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	rout, rerr := refused.CombinedOutput()
	assert.Error(t, rerr)
	assert.Equal(t, "nova-bus send: no hook has ever held nobody, so nothing would read it; run: nova-bus who, or send --new to a name about to hook in\n", string(rout), "XINFO GROUPS on a missing stream is no hook")
	assert.True(t, strings.HasPrefix(bus("send", "--as", "alpha", "--to", "beta", "three", "3"), "beta "), "a name with the group takes a send without --new")
	assert.Regexp(t, `^WHO OK present=2\nalpha\t\d+[hms0-9]*\nbeta\t`, bus("who"), "the planted keys show as present")
	unhook(t, c, "alpha")
	unhook(t, c, "beta")
	notIn := exec.Command(bin, "pending", "--as", "beta")
	notIn.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	nout, nerr := notIn.CombinedOutput()
	assert.Error(t, nerr)
	assert.Equal(t, "nova-bus pending: beta is not hooked in, so nothing would read an answer; hook in first: run `NOVA_BUS_REDIS="+addr+" nova-bus hook --as beta` as the Monitor tool's command; run: nova-bus show agents\n", string(nout))
	assert.Equal(t, "WHO OK present=0\n", bus("who"))

	h := startHook(t, bin, addr)
	h.next(t)
	h.next(t)
	require.Eventually(t, func() bool { return bus("pending", "--as", "beta") == "PENDING OK waiting=0\n" }, wait, 20*time.Millisecond)
	assert.Regexp(t, `^WHO OK present=1\nbeta\t\d+s\n$`, bus("who"))
	ttl, err := c.TTL(ctx, "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= presenceTTL, ttl)
	second := exec.Command(bin, "hook", "--as", "beta")
	second.Env = append(os.Environ(), "NOVA_BUS_REDIS="+addr)
	out, err := second.CombinedOutput()
	assert.Equal(t, 1, second.ProcessState.ExitCode(), string(out))
	assert.Regexp(t, `^HOOK WAIT name=beta held_since=\S+Z ttl=30s budget=40s\n`, string(out))
	assert.Contains(t, string(out), "name already hooked: beta after 40s", "after 40 s of the first hook refreshing")
	assert.Regexp(t, `^WHO OK present=1\nbeta\t\d+s\n$`, bus("who"), "the first hook's presence is untouched")
	stopped := time.Now()
	require.NoError(t, h.cmd.Process.Signal(syscall.SIGTERM))
	require.NoError(t, h.cmd.Wait(), "a clean exit")
	assert.Less(t, time.Since(stopped), 5*time.Second, "a blocked read does not hold up the exit")
	assert.Equal(t, "WHO OK present=0\n", bus("who"), "a clean exit deletes presence")

	k := startHook(t, bin, addr)
	require.Eventually(t, func() bool { return bus("who") != "WHO OK present=0\n" }, wait, 20*time.Millisecond)
	k.kill()
	assert.Regexp(t, `^WHO OK present=1\nbeta\t`, bus("who"), "a crashed hook stays present until the TTL")
	ttl, err = c.TTL(ctx, "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= presenceTTL, "and then lapses: %v", ttl)

	require.NoError(t, c.XAdd(ctx, &redis.XAddArgs{Stream: "bus:presence:zeta", Values: []string{"k", "v"}}).Err())
	assert.Regexp(t, `^WHO OK present=2\nbeta\t\d+s\nzeta\t\?\n$`, bus("who"), "a key that is not a string is shown, not fatal")
}

// TestHookOnAClosedPipe is hook | true: the reader is gone before the first
// line; the poll after the first read sees it, or the write fails with EPIPE
// (SIGPIPE ignored); either way the hook releases its presence and exits 1,
// leaving the message pending.
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
	hookIn(t, c, "alpha")
	send := exec.Command(bin, "send", "--as", "alpha", "--to", "beta", "--new", "one", "1")
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
	assert.Regexp(t, "broken pipe|its reader is gone", stderr.String())
	n, err := c.Exists(ctx, "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.Zero(t, n, "presence released")
	n, err = c.XLen(ctx, "bus:beta").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "the message was not printed, so not acked: it waits")
}

// TestHookEndsWhenItsReaderGoes: a running hook whose reader (the Monitor,
// the session) goes away ends within seconds, exit 1, and frees the name,
// without a message to write.
func TestHookEndsWhenItsReaderGoes(t *testing.T) {
	t.Parallel()
	addr := startRedis(t)
	bin := buildBus(t)
	r, w, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	hook := exec.Command(bin, "hook", "--as", "beta")
	hook.Env, hook.Stdout, hook.Stderr = append(os.Environ(), "NOVA_BUS_REDIS="+addr), w, errW
	require.NoError(t, hook.Start())
	require.NoError(t, w.Close())
	require.NoError(t, errW.Close())
	lines := bufio.NewScanner(errR)
	for lines.Scan() && !strings.HasPrefix(lines.Text(), "HOOK OK ") {
	}
	start := time.Now()
	require.NoError(t, r.Close()) // the reader goes
	var rest strings.Builder
	for lines.Scan() {
		rest.WriteString(lines.Text() + "\n")
	}
	err = hook.Wait()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 1, exit.ExitCode())
	assert.Contains(t, rest.String(), "stdout closed: its reader is gone")
	assert.Less(t, time.Since(start), 5*time.Second, "seen within a read")
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: closing a test client
	n, err := c.Exists(context.Background(), "bus:presence:beta").Result()
	require.NoError(t, err)
	assert.Zero(t, n, "presence released")
}

// TestHookTrimsOldAckedHistory: a stream an older build acked without
// deleting keeps its history until a hook finds an entry pending; the hook
// trims below it.
func TestHookTrimsOldAckedHistory(t *testing.T) {
	t.Parallel()
	addr := startRedis(t)
	bin := filepath.Join(t.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: closing a test client
	for _, s := range []string{"old1", "old2", "handed"} {
		require.NoError(t, c.XAdd(ctx, &redis.XAddArgs{Stream: "bus:beta", Values: []string{"from", "alpha", "subject", s, "body", s}}).Err())
	}
	require.NoError(t, c.XGroupCreate(ctx, "bus:beta", group, "0").Err())
	old, err := c.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: group, Streams: []string{"bus:beta", ">"}, Count: 3, Block: -1}).Result()
	require.NoError(t, err)
	for _, m := range old[0].Messages[:2] {
		require.NoError(t, c.XAck(ctx, "bus:beta", group, m.ID).Err()) // acked, not deleted: an older build
	}
	h := startHook(t, bin, addr)
	assert.True(t, strings.HasSuffix(h.next(t), "\thanded\thanded"), "only the unacked entry is printed")
	require.Eventually(t, func() bool { return c.XLen(ctx, "bus:beta").Val() == 0 }, wait, 20*time.Millisecond, "history trimmed, the pending entry deleted on ack")
}

// TestNamedACLUser: a server whose default user is off and whose bus user
// may touch only bus:* keys, with the command set the spec suggests. With NOVA_BUS_USER every verb works; without it
// the login is refused.
func TestNamedACLUser(t *testing.T) {
	t.Parallel()
	addr := startRedis(t, "--user", "bus", "on", ">secret", "~bus:*", "+@stream", "+@string", "+@keyspace", "+@transaction", "+eval", "+evalsha", "+@connection", "-@dangerous", "--user", "default", "off")
	bin := filepath.Join(t.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	env := append(os.Environ(), "NOVA_BUS_REDIS="+addr, "NOVA_BUS_PASSWORD=secret")
	bus := func(user string, args ...string) (int, string, string) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(env, "NOVA_BUS_USER="+user)
		var so, se strings.Builder
		cmd.Stdout, cmd.Stderr = &so, &se
		_ = cmd.Run() // the exit code is the result
		return cmd.ProcessState.ExitCode(), so.String(), se.String()
	}
	ac := redis.NewClient(&redis.Options{Addr: addr, Username: "bus", Password: "secret"})
	t.Cleanup(func() { _ = ac.Close() }) // ignored: closing a test client
	hookIn(t, ac, "alpha")
	hookIn(t, ac, "beta")
	code, _, errOut := bus("", "send", "--as", "alpha", "--to", "beta", "--new", "s", "b")
	assert.Equal(t, 1, code, "the default user is off")
	assert.Contains(t, errOut, "SEND FAILED to beta")
	code, out, errOut := bus("bus", "send", "--as", "alpha", "--to", "beta", "--new", "s", "b")
	require.Equal(t, 0, code, errOut)
	assert.Regexp(t, `^beta \d+-\d+\n$`, out)
	_, out, _ = bus("bus", "pending", "--as", "beta")
	assert.Equal(t, "PENDING OK waiting=1\n", out)
	unhook(t, ac, "alpha")
	unhook(t, ac, "beta") // the hook claims beta itself

	cmd := exec.Command(bin, "hook", "--as", "beta")
	cmd.Env = append(env, "NOVA_BUS_USER=bus")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: may already be gone
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(line, "\talpha\t-\ts\tb\n"), line)
	_, out, _ = bus("bus", "who")
	assert.Regexp(t, `^WHO OK present=1\nbeta\t`, out, "presence, claim and scan all work as the bus user")
}

// hookIn plants name's presence through c, as a hook of its own would: a verb
// that acts as name runs only while it is hooked in. unhook removes it.
func hookIn(t *testing.T, c *redis.Client, name string) {
	t.Helper()
	require.NoError(t, c.Set(context.Background(), "bus:presence:"+name, "2026-11-05T12:00:00Z\tplanted", time.Minute).Err())
}

func unhook(t *testing.T, c *redis.Client, name string) {
	t.Helper()
	require.NoError(t, c.Del(context.Background(), "bus:presence:"+name).Err())
}

func buildBus(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-bus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	return bin
}

// TestClaudeHookOnDevNull: a hook whose stdout is /dev/null (exec's nil
// Stdout, as `>&-` gets from the Go runtime) is refused before it reads.
func TestClaudeHookOnDevNull(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(buildBus(t), "hook", "--as", "beta")
	cmd.Env = append(os.Environ(), "NOVA_BUS_REDIS=127.0.0.1:1")
	var errOut strings.Builder
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 2, exit.ExitCode())
	assert.Equal(t, "nova-bus hook: stdout is /dev/null, nothing would read the messages; run: nova-bus hook -h\n", errOut.String())
}
