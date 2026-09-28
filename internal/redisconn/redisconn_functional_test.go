//go:build functional

package redisconn_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// These tests are the package against a redis-server: a throwaway one per
// test, started by the shared helper, on a loopback port or a socket of its
// own. They use the package as a tool does, through what it exports.

// nothing is the empty environment: only what a test gives is used.
func nothing(string) string { return "" }

func environment(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// open is a connection to the store at addr with no login, closed with the
// test.
func open(t *testing.T, addr string) *redisconn.Conn {
	t.Helper()
	conn, err := redisconn.Open(context.Background(), redisconn.Options{Addr: addr}, nothing)
	if err != nil {
		t.Fatalf("open %s: %v", addr, err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close %s: %v", addr, err)
		}
	})
	return conn
}

var statLine = regexp.MustCompile(`(?m)^cmdstat_([^:]+):calls=(\d+)`)

// calls is how often the store has run each command since its counts were
// last reset, as the store itself counts them.
func calls(t *testing.T, admin *redisconn.Conn) map[string]string {
	t.Helper()
	info, err := admin.Client().Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range statLine.FindAllStringSubmatch(info, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// names is the commands of calls, sorted, for a message.
func names(calls map[string]string) string {
	var out []string
	for name, n := range calls {
		out = append(out, name+"="+n)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// resetCalls sets the store's counts to zero.
func resetCalls(t *testing.T, admin *redisconn.Conn) {
	t.Helper()
	if err := admin.Client().ConfigResetStat(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
}

// connections is the number of connections the store has accepted so far.
func connections(t *testing.T, admin *redisconn.Conn) string {
	t.Helper()
	info, err := admin.Client().Info(context.Background(), "stats").Result()
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^total_connections_received:(\d+)`).FindStringSubmatch(info)
	if m == nil {
		t.Fatalf("no total_connections_received in %q", info)
	}
	return m[1]
}

// TestOpenOverTCP: Open reaches a store by host:port, and what the store
// ran for it is one HELLO: no PING, no CLIENT command. The first command is
// the first round trip.
func TestOpenOverTCP(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	admin := open(t, addr)
	before := connections(t, admin)
	resetCalls(t, admin)

	conn := open(t, addr)
	ran := calls(t, admin)
	if ran["hello"] != "1" {
		t.Errorf("the store ran HELLO %q times for one Open; want 1 (%s)", ran["hello"], names(ran))
	}
	for name := range ran {
		if name != "hello" && name != "config|resetstat" && name != "info" {
			t.Errorf("the store ran %s for an Open; want HELLO alone (%s)", name, names(ran))
		}
	}
	if after := connections(t, admin); after == before {
		t.Errorf("the store accepted no connection for an Open")
	}
	if got := conn.String(); got != "redis at "+addr+" as the default user, no password" {
		t.Errorf("the connection reads %q", got)
	}

	ctx := context.Background()
	trips := redisconn.CountTrips(conn.Client())
	if err := conn.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := conn.Client().Get(ctx, "k").Result(); err != nil || got != "v" {
		t.Errorf("GET = %q, %v", got, err)
	}
	if got, err := conn.Client().Ping(ctx).Result(); err != nil || got != "PONG" {
		t.Errorf("PING = %q, %v", got, err)
	}
	if got, err := conn.Client().Do(ctx, "PING").Text(); err != nil || got != "PONG" {
		t.Errorf("PING, as the probe spells it = %q, %v", got, err)
	}
	if trips.N() != 4 {
		t.Errorf("four commands took %d trips", trips.N())
	}
	// The caller's two PINGs are the caller's: the store ran them.
	if ran := calls(t, admin); ran["ping"] != "2" || ran["hello"] != "1" || ran["set"] != "1" || ran["get"] != "1" {
		t.Errorf("after four commands the store has run %s; want each once, PING twice, and the one HELLO", names(ran))
	}
}

// socketDir is a directory whose path is short enough to hold a socket: the
// test's own when it fits the 104 bytes a socket's path may have, else one
// under /tmp.
func socketDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) < 90 {
		return dir
	}
	dir, err := os.MkdirTemp("/tmp", "redisconn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startOnSocket runs a throwaway redis-server that listens on the socket and
// on one loopback port, and returns the port's address. testredis.Start
// promises a server on one TCP port and nowhere else, and refuses
// --unixsocket, so this one test launches the server itself with the program
// and the port testredis hands out, and is ready the way testredis is: when
// the server's own output says so. The test's cleanup kills it and waits for
// it.
const (
	socketWait = 30 * time.Second
	readyLine  = "Ready to accept connections"
)

func startOnSocket(t *testing.T, socket string) string {
	t.Helper()
	bin, port := testredis.Program(t), testredis.FreePort(t)
	cmd := exec.Command(bin, "--bind", "127.0.0.1", "--port", port, "--unixsocket", socket, "--unixsocketperm", "700",
		"--save", "", "--appendonly", "no", "--dir", t.TempDir())
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	_ = pw.Close()
	var (
		out   strings.Builder
		ready = make(chan struct{})
		ended = make(chan error, 1)
	)
	go func() {
		sc := bufio.NewScanner(pr)
		seen := false
		for sc.Scan() {
			out.WriteString(sc.Text() + "\n")
			if !seen && strings.Contains(sc.Text(), readyLine) {
				seen = true
				close(ready)
			}
		}
		ended <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-ended
		_ = pr.Close()
	})
	select {
	case <-ready:
	case err := <-ended:
		ended <- err
		t.Fatalf("redis-server exited before it was ready: %v\n%s", err, out.String())
	case <-time.After(socketWait):
		t.Fatalf("redis-server was not ready within %v\n%s", socketWait, out.String())
	}
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("the ready server's socket %s: %v", socket, err)
	}
	_ = c.Close()
	return net.JoinHostPort("127.0.0.1", port)
}

// TestOpenOverAUnixSocket: Open reaches a store by the absolute path of its
// socket, with the same one HELLO, and the connection is the socket's.
func TestOpenOverAUnixSocket(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(socketDir(t), "store.sock")
	addr := startOnSocket(t, socket)
	admin := open(t, addr)
	resetCalls(t, admin)

	conn := open(t, socket)
	if ran := calls(t, admin); ran["hello"] != "1" || ran["ping"] != "" {
		t.Errorf("the store ran %s for an Open over its socket; want HELLO alone", names(ran))
	}
	ctx := context.Background()
	if err := conn.Client().Set(ctx, "over", "the socket", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := admin.Client().Get(ctx, "over").Result(); err != nil || got != "the socket" {
		t.Errorf("read over TCP what was written over the socket: %q, %v", got, err)
	}
	list, err := conn.Client().ClientList(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	// The store marks a client that came over its socket with the flag U.
	if !strings.Contains(list, " flags=U ") {
		t.Errorf("the store lists no client on its socket %s:\n%s", socket, list)
	}

	// A socket that is not there is a store that is not there.
	gone := filepath.Join(filepath.Dir(socket), "gone.sock")
	none, err := redisconn.Open(ctx, redisconn.Options{Addr: gone}, nothing)
	if none != nil || redisconn.Classify(err) != redisconn.Unreachable {
		t.Errorf("Open of a socket that is not there = %v, %v; want unreachable", none, err)
	}
}

// TestOpenAsAnACLUser: the default user is off and one user is on, which is
// how a store that is shared is set up. The right password opens, as that
// user. The wrong password, a user the store does not have and no login at
// all are refused by the store; an empty password variable is refused
// before the store is dialed. No refusal holds a password.
func TestOpenAsAnACLUser(t *testing.T) {
	t.Parallel()
	const password, wrong = "bench-secret-K7", "bench-secret-K8"
	addr := testredis.Start(t, testredis.User("bench", password)...)
	ctx := context.Background()
	login := redisconn.Options{Addr: addr, User: "bench", PasswordEnv: "NOVA_TEST_PW"}

	conn, err := redisconn.Open(ctx, login, environment(map[string]string{"NOVA_TEST_PW": password}))
	if err != nil {
		t.Fatalf("Open with the right password: %v", err)
	}
	defer conn.Close()
	if who, err := conn.Client().ACLWhoAmI(ctx).Result(); err != nil || who != "bench" {
		t.Errorf("the connection is %q, %v; want bench", who, err)
	}
	if got := conn.String(); got != "redis at "+addr+" as user bench (password from NOVA_TEST_PW)" {
		t.Errorf("the connection reads %q", got)
	}

	// The same login from the environment alone, by a tool's own names.
	tool := redisconn.Env{Addr: "TOOL_REDIS", User: "TOOL_REDIS_USER", PasswordEnv: "TOOL_REDIS_PASSWORD_ENV"}
	aliased, err := redisconn.Open(ctx, redisconn.Options{Env: tool}, environment(map[string]string{
		tool.Addr: addr, tool.User: "bench", tool.PasswordEnv: "NOVA_TEST_PW", "NOVA_TEST_PW": password,
	}))
	if err != nil {
		t.Fatalf("Open from the environment: %v", err)
	}
	if who, err := aliased.Client().ACLWhoAmI(ctx).Result(); err != nil || who != "bench" {
		t.Errorf("the connection from the environment is %q, %v; want bench", who, err)
	}
	if err := aliased.Close(); err != nil {
		t.Error(err)
	}

	const tried = " as user bench (password from NOVA_TEST_PW): login refused: "
	accepted := connections(t, conn)
	for _, c := range []struct {
		name   string
		o      redisconn.Options
		env    map[string]string
		want   string
		dialed bool
	}{
		{"the wrong password", login, map[string]string{"NOVA_TEST_PW": wrong},
			"redis at " + addr + tried + "WRONGPASS invalid username-password pair or user is disabled.; next: check that NOVA_TEST_PW holds the password of bench and that the store has that user switched on", true},
		{"a user the store does not have", redisconn.Options{Addr: addr, User: "nobody", PasswordEnv: "NOVA_TEST_PW"}, map[string]string{"NOVA_TEST_PW": password},
			"redis at " + addr + " as user nobody (password from NOVA_TEST_PW): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: check that NOVA_TEST_PW holds the password of nobody and that the store has that user switched on", true},
		{"the right password and no user", redisconn.Options{Addr: addr, PasswordEnv: "NOVA_TEST_PW", Env: redisconn.GeneralEnv}, map[string]string{"NOVA_TEST_PW": password},
			"redis at " + addr + " as the default user (password from NOVA_TEST_PW): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: name the user (NOVA_REDIS_USER), or check that NOVA_TEST_PW holds the password of the default user", true},
		{"no login at all", redisconn.Options{Addr: addr, Env: redisconn.GeneralEnv}, nil,
			"redis at " + addr + " as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_REDIS_USER) and the variable that holds its password (NOVA_REDIS_PASSWORD_ENV)", true},
		{"an empty password variable", login, map[string]string{"NOVA_TEST_PW": ""},
			"redis at " + addr + tried + "NOVA_TEST_PW is empty; next: export NOVA_TEST_PW, holding the password, in the environment of this process", false},
		{"a password variable that is not set", login, nil,
			"redis at " + addr + tried + "NOVA_TEST_PW is empty; next: export NOVA_TEST_PW, holding the password, in the environment of this process", false},
		{"a user and no password variable", redisconn.Options{Addr: addr, User: "bench"}, map[string]string{"NOVA_TEST_PW": password},
			"redis at " + addr + " as user bench, no password: login refused: no password variable is named", false},
	} {
		refused, err := redisconn.Open(ctx, c.o, environment(c.env))
		if refused != nil || err == nil {
			t.Fatalf("%s: Open = %v, %v; want a refusal", c.name, refused, err)
		}
		if got := err.Error(); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		if got := redisconn.Classify(err); got != redisconn.AuthRefused {
			t.Errorf("%s: class %v; want %v", c.name, got, redisconn.AuthRefused)
		}
		for e := err; e != nil; e = errors.Unwrap(e) {
			if strings.Contains(e.Error(), password) || strings.Contains(e.Error(), wrong) {
				t.Errorf("%s: %q holds a password", c.name, e)
			}
		}
		now := connections(t, conn)
		if dialed := now != accepted; dialed != c.dialed {
			t.Errorf("%s: the store accepted a connection: %v; want %v", c.name, dialed, c.dialed)
		}
		accepted = now
	}

	// A command's own refusal, on a connection that stands.
	limited := testredis.Start(t, "--user", "default", "off", "--user", "reader", "on", ">"+password, "~*", "+get", "+hello", "+ping")
	reader, err := redisconn.Open(ctx, redisconn.Options{Addr: limited, User: "reader", PasswordEnv: "NOVA_TEST_PW"}, environment(map[string]string{"NOVA_TEST_PW": password}))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	err = reader.Client().Set(ctx, "k", "v", 0).Err()
	if err == nil || redisconn.Classify(err) != redisconn.Other {
		t.Fatalf("a write by a user who may only read = %v, %v; want the command's own refusal", err, redisconn.Classify(err))
	}
	explained := reader.Explain(err)
	if got := explained.Error(); !strings.HasPrefix(got, "redis at "+limited+" as user reader (password from NOVA_TEST_PW): failed: NOPERM ") ||
		!strings.HasSuffix(got, "; next: the store answered, so the connection stands: read the refusal as the command's own") {
		t.Errorf("the refusal, explained: %s", got)
	}
	if !errors.Is(explained, err) || redisconn.Classify(explained) != redisconn.Other {
		t.Errorf("the refusal, explained, is %v and unwraps to %v", redisconn.Classify(explained), errors.Unwrap(explained))
	}
}

// TestOpenToAClosedPort: nothing listens. Open returns before its bound has
// passed, having met a refusal, and says the store is unreachable.
func TestOpenToAClosedPort(t *testing.T) {
	t.Parallel()
	addr := net.JoinHostPort("127.0.0.1", testredis.FreePort(t))
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	conn, err := redisconn.Open(ctx, redisconn.Options{Env: redisconn.GeneralEnv}, environment(map[string]string{redisconn.GeneralEnv.Addr: addr}))
	if bound := ctx.Err(); bound != nil {
		t.Errorf("Open returned after its bound of %v had passed: %v", redisconn.OpenTimeout, bound)
	}
	if conn != nil || err == nil {
		t.Fatalf("Open = %v, %v; want unreachable", conn, err)
	}
	if got := redisconn.Classify(err); got != redisconn.Unreachable {
		t.Errorf("class %v; want %v", got, redisconn.Unreachable)
	}
	want := "redis at " + addr + " as the default user, no password: unreachable: dial tcp " + addr + ": connect: connection refused; next: start the store or correct the address, which was from NOVA_REDIS_ADDR"
	if got := err.Error(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("%v does not unwrap to the refusal", err)
	}
}

// TestTripsAgainstTheStore: single commands are one trip each, a pipeline
// and a transaction one each, a label counts its own, and the handshake of
// a connection dialed inside a command is not counted: the store ran a
// second HELLO and the counter did not move for it.
func TestTripsAgainstTheStore(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	admin := open(t, addr)
	conn := open(t, addr)
	client := conn.Client()
	ctx := context.Background()
	trips := redisconn.CountTrips(client)

	span := func(name string, want int64, work func() error) {
		t.Helper()
		before := trips.N()
		if err := work(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got := trips.N() - before; got != want {
			t.Errorf("%s took %d trips; want %d", name, got, want)
		}
	}
	span("a command", 1, func() error { return client.Set(ctx, "k", "v", 0).Err() })
	span("three commands", 3, func() error {
		for _, key := range []string{"a", "b", "c"} {
			if err := client.Set(ctx, key, "v", 0).Err(); err != nil {
				return err
			}
		}
		return nil
	})
	span("a pipeline of a hundred", 1, func() error {
		pipe := client.Pipeline()
		for i := 0; i < 100; i++ {
			pipe.Incr(ctx, "n")
		}
		_, err := pipe.Exec(ctx)
		return err
	})
	if n, err := client.Get(ctx, "n").Int(); err != nil || n != 100 {
		t.Errorf("after the pipeline n = %d, %v; want 100", n, err)
	}
	span("a transaction", 1, func() error {
		_, err := client.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Incr(ctx, "n")
			p.Incr(ctx, "n")
			return nil
		})
		return err
	})
	span("the caller's own CLIENT command", 1, func() error { return client.ClientID(ctx).Err() })
	span("a command the store refuses", 1, func() error {
		if err := client.LPush(ctx, "k", "v").Err(); err == nil {
			return errors.New("the store pushed onto a string")
		}
		return nil
	})
	span("a labelled pipeline", 1, func() error {
		labelled := redisconn.WithTripLabel(ctx, "fold")
		pipe := client.Pipeline()
		pipe.Get(labelled, "a")
		pipe.Get(labelled, "b")
		_, err := pipe.Exec(labelled)
		return err
	})
	if trips.Of("fold") != 1 || len(trips.ByLabel()) != 1 {
		t.Errorf("by label: %v; want fold=1", trips.ByLabel())
	}

	// The store hangs up on the connection. The command that comes next
	// either meets the dropped connection, and fails, or finds it dead
	// before it is used, and dials; whichever it is, each command sent is
	// one trip, and the handshake of the connection dialed is none.
	id, err := client.ClientID(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	resetCalls(t, admin)
	if n, err := admin.Client().ClientKillByFilter(ctx, "ID", strings.TrimSpace(itoa(id))).Result(); err != nil || n != 1 {
		t.Fatalf("the store hung up on %d connections, %v; want 1", n, err)
	}
	before, sent := trips.N(), int64(0)
	for {
		sent++
		err := client.Set(ctx, "after", "the hang-up", 0).Err()
		if err == nil {
			break
		}
		if sent == 2 || redisconn.Classify(err) != redisconn.Unreachable {
			t.Fatalf("command %d after the store hung up: %v", sent, err)
		}
	}
	if got := trips.N() - before; got != sent {
		t.Errorf("%d commands after the hang-up took %d trips", sent, got)
	}
	if ran := calls(t, admin); ran["hello"] != "1" || ran["set"] != "1" {
		t.Errorf("after the hang-up the store ran %s; want one HELLO, for the connection dialed, and the one SET", names(ran))
	}
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var out []byte
	for ; n > 0; n /= 10 {
		out = append([]byte{digits[n%10]}, out...)
	}
	return string(out)
}

// TestADeadConnectionIsFoundBeforeItIsUsed: the connection Open dialed is
// still a socket to go-redis, which looks at an idle socket before it uses
// it. So a connection the store has hung up on costs the next command a
// dial and not a failure.
func TestADeadConnectionIsFoundBeforeItIsUsed(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	admin := open(t, addr)
	ctx := context.Background()
	const rounds = 20
	failed := 0
	for i := 0; i < rounds; i++ {
		conn, err := redisconn.Open(ctx, redisconn.Options{Addr: addr}, nothing)
		if err != nil {
			t.Fatal(err)
		}
		id, err := conn.Client().ClientID(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := admin.Client().ClientKillByFilter(ctx, "ID", itoa(id)).Result(); err != nil || n != 1 {
			t.Fatalf("round %d: the store hung up on %d connections, %v; want 1", i, n, err)
		}
		if err := conn.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
			if redisconn.Classify(err) != redisconn.Unreachable {
				t.Fatalf("round %d: %v", i, err)
			}
			failed++
		}
		if err := conn.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
			t.Fatalf("round %d: the command after the one that met the hang-up: %v", i, err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// The store's hang-up reaches this side in its own time; a round can
	// lose that race. A connection that hid its socket loses every round.
	if failed > rounds/4 {
		t.Errorf("%d of %d commands met the dead connection; want it found dead before use", failed, rounds)
	}
}

// TestPipelineFirstErrorAgainstTheStore: a pipeline whose first command
// finds nothing and whose later command the store refuses. Exec answers the
// absence; FirstError and Exec of this package answer the refusal.
func TestPipelineFirstErrorAgainstTheStore(t *testing.T) {
	t.Parallel()
	conn := open(t, testredis.Start(t))
	client := conn.Client()
	ctx := context.Background()
	if err := client.Set(ctx, "text", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "row", "state", "open").Err(); err != nil {
		t.Fatal(err)
	}
	queue := func() (redis.Pipeliner, *redis.StringCmd, *redis.StringCmd, *redis.IntCmd) {
		pipe := client.Pipeline()
		return pipe, pipe.HGet(ctx, "row", "absent"), pipe.HGet(ctx, "row", "state"), pipe.LPush(ctx, "text", "v")
	}

	pipe, absent, present, refused := queue()
	cmds, bare := pipe.Exec(ctx)
	if !errors.Is(bare, redis.Nil) {
		t.Fatalf("Exec answered %v; want the absence of the first command", bare)
	}
	err := redisconn.FirstError(cmds, bare)
	if err == nil || !strings.HasPrefix(err.Error(), "WRONGTYPE") || err != refused.Err() {
		t.Errorf("FirstError = %v; want the refusal of the third command, %v", err, refused.Err())
	}
	if !errors.Is(absent.Err(), redis.Nil) || present.Val() != "open" {
		t.Errorf("the commands read %v and %q", absent.Err(), present.Val())
	}

	pipe, _, _, refused = queue()
	trips := redisconn.CountTrips(client)
	if err := redisconn.Exec(ctx, pipe); err == nil || err != refused.Err() || redisconn.Classify(err) != redisconn.Other {
		t.Errorf("Exec of this package = %v; want the refusal", err)
	}
	pipe = client.Pipeline()
	absent, present = pipe.HGet(ctx, "row", "absent"), pipe.HGet(ctx, "row", "state")
	if err := redisconn.Exec(ctx, pipe); err != nil {
		t.Errorf("a pipeline of an absent field and a present one: %v; want nil", err)
	}
	if !errors.Is(absent.Err(), redis.Nil) || present.Val() != "open" {
		t.Errorf("the commands read %v and %q", absent.Err(), present.Val())
	}
	if trips.N() != 2 {
		t.Errorf("two pipelines took %d trips", trips.N())
	}
}
