package redisconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/redis/go-redis/v9"
)

const storeAddr = "store.test:6379"

// TestOpenCostsTheHandshakeAlone: Open dials once, and the one thing the
// store receives is HELLO, carrying the login when there is one. No PING, no
// CLIENT SETINFO, no CLIENT MAINT_NOTIFICATIONS. After Open a PING of the
// caller's own is the caller's and reaches the store.
func TestOpenCostsTheHandshakeAlone(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		o     Options
		env   map[string]string
		hello string
		named string
	}{
		{"no login", Options{Addr: storeAddr}, nil,
			"1: hello 3", "redis at store.test:6379 as the default user, no password"},
		{"a user and its password", Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, map[string]string{"PW": "s3cret"},
			"1: hello 3 auth bench s3cret", "redis at store.test:6379 as user bench (password from PW)"},
		{"the default user with a password", Options{Addr: storeAddr, PasswordEnv: "PW"}, map[string]string{"PW": "s3cret"},
			"1: hello 3 auth default s3cret", "redis at store.test:6379 as the default user (password from PW)"},
		{"all of it from the environment", Options{Env: GeneralEnv},
			map[string]string{GeneralEnv.Addr: storeAddr, GeneralEnv.User: "bench", GeneralEnv.PasswordEnv: "NOVA_TEST_PW", "NOVA_TEST_PW": "s3cret"},
			"1: hello 3 auth bench s3cret", "redis at store.test:6379 as user bench (password from NOVA_TEST_PW)"},
	} {
		store := newFakeStore(t, accepting)
		conn, err := open(context.Background(), c.o, environment(c.env), store.dial)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := store.commands(); !reflect.DeepEqual(got, []string{c.hello}) {
			t.Errorf("%s: Open sent %q; want the handshake alone, %q", c.name, got, c.hello)
		}
		if n := store.dialed(); n != 1 {
			t.Errorf("%s: Open dialed %d times; want once", c.name, n)
		}
		if got := conn.String(); got != c.named {
			t.Errorf("%s: the connection reads %q; want %q", c.name, got, c.named)
		}
		ctx := context.Background()
		if err := conn.Client().Ping(ctx).Err(); err != nil {
			t.Errorf("%s: ping: %v", c.name, err)
		}
		if got, err := conn.Client().Do(ctx, "PING").Text(); err != nil || got != "PONG" {
			t.Errorf("%s: PING = %q, %v", c.name, got, err)
		}
		if got, err := conn.Client().Get(ctx, "key").Result(); err != nil || got != "value" {
			t.Errorf("%s: GET = %q, %v", c.name, got, err)
		}
		if got := store.commands(); !reflect.DeepEqual(got, []string{"1: ping", "1: PING", "1: get key"}) {
			t.Errorf("%s: after Open the store received %q; want the caller's three commands, its own PING among them", c.name, got)
		}
		if n := store.dialed(); n != 1 {
			t.Errorf("%s: %d dials after three commands; want the one connection", c.name, n)
		}
		if faults := store.faults(); len(faults) != 0 {
			t.Errorf("%s: %q", c.name, faults)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("%s: close: %v", c.name, err)
		}
		if n := store.open(); n != 0 {
			t.Errorf("%s: %d connections open after Close", c.name, n)
		}
	}
}

// TestOpenReturnsWhatTheStoreSaid: a store that refuses the login, a store
// that is older than HELLO, a thing that is not a store, a store that hangs
// up. One dial each, the class, the one line, and nothing left open.
func TestOpenReturnsWhatTheStoreSaid(t *testing.T) {
	t.Parallel()
	const (
		noauthHello = "-NOAUTH HELLO must be called with the client already authenticated, otherwise the HELLO <proto> AUTH <user> <pass> option can be used to authenticate the client and select the RESP protocol version at the same time\r\n"
		noauth      = "-NOAUTH Authentication required.\r\n"
		wrongpass   = "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
	)
	login := Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}
	for _, c := range []struct {
		name  string
		o     Options
		reply func(int, []string) string
		class Class
		want  string
		sent  []string
	}{
		{"a store that wants a login and was given none",
			Options{Addr: storeAddr, Env: GeneralEnv},
			func(_ int, cmd []string) string {
				if cmd[0] == "hello" {
					return noauthHello
				}
				return noauth
			},
			AuthRefused,
			"redis at store.test:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_REDIS_USER) and the variable that holds its password (NOVA_REDIS_PASSWORD_ENV)",
			// HELLO was refused, so the probe travels and the store answers it.
			[]string{"1: hello 3", "1: PING"}},
		{"a user whose password the store refuses",
			login, refusing(wrongpass), AuthRefused,
			"redis at store.test:6379 as user bench (password from PW): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: check that PW holds the password of bench and that the store has that user switched on",
			[]string{"1: hello 3 auth bench s3cret", "1: auth bench s3cret"}},
		{"the default user whose password the store refuses",
			Options{Addr: storeAddr, PasswordEnv: "PW"}, refusing(wrongpass), AuthRefused,
			// No Env names a variable for the user, so the next step names
			// none.
			"redis at store.test:6379 as the default user (password from PW): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: name the user, or check that PW holds the password of the default user",
			[]string{"1: hello 3 auth default s3cret", "1: auth s3cret"}},
		{"a thing that answers and is not a store",
			login, func(int, []string) string { return "HTTP/1.1 400 Bad Request\r\n\r\n" }, Other,
			"redis at store.test:6379 as user bench (password from PW): failed: redis: can't parse map reply: \"HTTP/1.1 400 Bad Request\"; next: check that the address is a Redis store, version 6 or later",
			[]string{"1: hello 3 auth bench s3cret"}},
		{"a store that hangs up on the handshake",
			login, func(int, []string) string { return hangUp }, Unreachable,
			"redis at store.test:6379 as user bench (password from PW): unreachable: EOF; next: start the store or correct the address, which was given to this tool",
			[]string{"1: hello 3 auth bench s3cret"}},
		{"a store that refuses the handshake and then hangs up on the probe",
			Options{Addr: storeAddr},
			func(_ int, cmd []string) string {
				if cmd[0] == "hello" {
					return "-ERR unknown command 'hello'\r\n"
				}
				return hangUp
			},
			Unreachable,
			"redis at store.test:6379 as the default user, no password: unreachable: EOF; next: start the store or correct the address, which was given to this tool",
			[]string{"1: hello 3", "1: PING"}},
	} {
		store := newFakeStore(t, c.reply)
		conn, err := open(context.Background(), c.o, environment(map[string]string{"PW": "s3cret"}), store.dial)
		if err == nil || conn != nil {
			t.Fatalf("%s: Open = %v, %v; want the refusal and no connection", c.name, conn, err)
		}
		if got := err.Error(); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		if got := Classify(err); got != c.class {
			t.Errorf("%s: class %v; want %v", c.name, got, c.class)
		}
		if got := store.commands(); !reflect.DeepEqual(got, c.sent) {
			t.Errorf("%s: the store received %q; want %q", c.name, got, c.sent)
		}
		if n := store.dialed(); n != 1 {
			t.Errorf("%s: %d dials; want one", c.name, n)
		}
		if n := store.open(); n != 0 {
			t.Errorf("%s: %d connections left open by a failed Open", c.name, n)
		}
		if faults := store.faults(); len(faults) != 0 {
			t.Errorf("%s: %q", c.name, faults)
		}
		for _, shown := range errorsText(err) {
			if strings.Contains(shown, "s3cret") {
				t.Errorf("%s: %q shows the password", c.name, shown)
			}
		}
	}
}

// TestOpenReachesAStoreOlderThanHello: a store that does not know HELLO is
// logged into the old way, and then the probe is sent for real: three
// exchanges, and the connection is good.
func TestOpenReachesAStoreOlderThanHello(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, func(conn int, cmd []string) string {
		if cmd[0] == "hello" {
			// A store of that age writes what it was sent into its refusal.
			return "-ERR unknown command `hello`, with args beginning with: `" + strings.Join(cmd[1:], "`, `") + "`, \r\n"
		}
		return accepting(conn, cmd)
	})
	conn, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), store.dial)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1: hello 3 auth bench s3cret", "1: auth bench s3cret", "1: PING"}
	if got := store.commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("the store received %q; want %q", got, want)
	}
	if err := conn.Client().Set(context.Background(), "k", "v", 0).Err(); err != nil {
		t.Error(err)
	}
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
}

// TestOpenDialsOnce: a store that is not there is one dial, no second
// attempt, classified unreachable, with the address's source named.
func TestOpenDialsOnce(t *testing.T) {
	t.Parallel()
	dials := 0
	conn, err := open(context.Background(), Options{Env: Env{Addr: "TOOL_REDIS"}}, environment(map[string]string{"TOOL_REDIS": "127.0.0.1:1"}), refusedDial(&dials))
	if err == nil || conn != nil {
		t.Fatalf("Open = %v, %v; want unreachable", conn, err)
	}
	const want = "redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was from TOOL_REDIS"
	if got := err.Error(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if got := Classify(err); got != Unreachable {
		t.Errorf("class %v; want %v", got, Unreachable)
	}
	if dials != 1 {
		t.Errorf("%d dials; want one", dials)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("%v does not unwrap to the refusal the dial returned", err)
	}
}

// TestOpenDialsTheNetworkOfTheAddress: host:port is dialed over tcp, an
// absolute path as a Unix socket, each with the address as given.
func TestOpenDialsTheNetworkOfTheAddress(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]string{
		storeAddr:             "tcp store.test:6379",
		"[::1]:6380":          "tcp [::1]:6380",
		"/var/run/store.sock": "unix /var/run/store.sock",
	} {
		store := newFakeStore(t, accepting)
		var dialed []string
		conn, err := open(context.Background(), Options{Addr: addr}, nil, func(ctx context.Context, network, to string) (net.Conn, error) {
			dialed = append(dialed, network+" "+to)
			return store.dial(ctx, network, to)
		})
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if !reflect.DeepEqual(dialed, []string{want}) {
			t.Errorf("%s: dialed %q; want %q", addr, dialed, want)
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}
}

// TestOpenRefusesBeforeItDials: what Resolve refuses, Open refuses, and the
// store is never dialed.
func TestOpenRefusesBeforeItDials(t *testing.T) {
	t.Parallel()
	for _, o := range []Options{
		{},
		{Addr: "store.test"},
		{Addr: storeAddr, User: "bench"},
		{Addr: storeAddr, User: "bench", PasswordEnv: "PW"},
		{Addr: storeAddr, PasswordEnv: "hunter2"},
	} {
		dials := 0
		conn, err := open(context.Background(), o, nothing, refusedDial(&dials))
		_, want := Resolve(o, nothing)
		if conn != nil || err == nil || want == nil || err.Error() != want.Error() || Classify(err) != Classify(want) {
			t.Errorf("Open(%+v) = %v, %v; want Resolve's refusal, %v", o, conn, err, want)
		}
		if dials != 0 {
			t.Errorf("Open(%+v) dialed %d times; want none", o, dials)
		}
	}
}

// sooner is a caller's deadline inside the bounds of the package.
const sooner = OpenTimeout / 5

// TestOpenIsBounded: whatever the store does not do, Open returns at its
// bound, or at the caller's deadline when that is sooner, having dialed once.
// The clock is synctest's: the waits are exact and cost no time.
func TestOpenIsBounded(t *testing.T) {
	t.Parallel()
	within := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), sooner)
	}
	for _, c := range []struct {
		name   string
		silent bool // the dial is never answered; else the handshake is not
		ctx    func() (context.Context, context.CancelFunc)
		after  time.Duration
		class  Class
		want   string
	}{
		{"a dial that is never answered", true, background,
			OpenTimeout, Unreachable, "context deadline exceeded; next: start the store"},
		{"a store that takes the connection and never answers the handshake", false, background,
			OpenTimeout, Unreachable, "i/o timeout; next: start the store"},
		{"a dial that is never answered, and a caller who waits less", true, within,
			sooner, Unreachable, "context deadline exceeded; next: start the store"},
		{"a store that never answers the handshake, and a caller who waits less", false, within,
			sooner, Unreachable, "i/o timeout; next: start the store"},
		{"a caller who has cancelled", true,
			func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			0, Other, "failed: context canceled; next: run it again: it was cancelled before the store answered"},
	} {
		synctest.Test(t, func(t *testing.T) {
			silent := &silentStore{}
			dial, dialed := silent.dial, silent.dialed
			if !c.silent {
				store := newFakeStore(t, func(int, []string) string { return hang })
				dial, dialed = store.dial, store.dialed
			}
			ctx, cancel := c.ctx()
			defer cancel()
			start := time.Now()
			conn, err := open(ctx, Options{Addr: storeAddr}, nothing, dial)
			took := time.Now().Sub(start)
			if err == nil || conn != nil {
				t.Fatalf("%s: Open = %v, %v; want an error", c.name, conn, err)
			}
			if took != c.after {
				t.Errorf("%s: Open returned after %v; want %v", c.name, took, c.after)
			}
			if got := Classify(err); got != c.class {
				t.Errorf("%s: class %v; want %v (%v)", c.name, got, c.class, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: %v; want %q in it", c.name, err, c.want)
			}
			// A dial that was under way when the caller stopped waiting ends
			// at the dial's own bound, and is the only one.
			silent.settled()
			if took := time.Now().Sub(start); took > DialTimeout {
				t.Errorf("%s: the dial ended after %v; want it within %v", c.name, took, DialTimeout)
			}
			if n := dialed(); n > 1 {
				t.Errorf("%s: %d dials; want at most one", c.name, n)
			}
		})
	}
}

func background() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// TestADialThatAnswersAfterOpenGaveUpIsClosed: the caller stopped waiting,
// Open returned, and then the store took the connection. Nothing uses it and
// nothing leaves it open.
func TestADialThatAnswersAfterOpenGaveUpIsClosed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, accepting)
		answer := make(chan struct{})
		var late sync.WaitGroup
		late.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), sooner)
		defer cancel()
		conn, err := open(ctx, Options{Addr: storeAddr}, nothing, func(dctx context.Context, network, addr string) (net.Conn, error) {
			defer late.Done()
			<-answer
			return store.dial(dctx, network, addr)
		})
		if conn != nil || Classify(err) != Unreachable {
			t.Fatalf("Open = %v, %v; want unreachable", conn, err)
		}
		close(answer)
		late.Wait()
		synctest.Wait()
		if n := store.open(); n != 0 || store.dialed() != 1 {
			t.Errorf("%d of %d connections left open after Open gave up", n, store.dialed())
		}
		if got := store.commands(); len(got) != 0 {
			t.Errorf("the store received %q on a connection nobody opened", got)
		}
	})
}

// TestCommandsAreBounded: after Open, a reply that never comes ends the
// command at the read bound, or at the caller's deadline when that is
// sooner, and the command was sent once.
func TestCommandsAreBounded(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		ctx   func() (context.Context, context.CancelFunc)
		after time.Duration
	}{
		{"the read bound", background, ReadTimeout},
		{"the caller's deadline", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), sooner)
		}, sooner},
	} {
		synctest.Test(t, func(t *testing.T) {
			store := newFakeStore(t, func(conn int, cmd []string) string {
				if cmd[0] == "get" {
					return hang
				}
				return accepting(conn, cmd)
			})
			conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, store.dial)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			store.commands()
			ctx, cancel := c.ctx()
			defer cancel()
			start := time.Now()
			err = conn.Client().Get(ctx, "key").Err()
			took := time.Now().Sub(start)
			if took != c.after {
				t.Errorf("%s: the command returned after %v; want %v", c.name, took, c.after)
			}
			if got := Classify(err); got != Unreachable {
				t.Errorf("%s: %v is %v; want %v", c.name, err, got, Unreachable)
			}
			if got := store.commands(); !reflect.DeepEqual(got, []string{"1: get key"}) {
				t.Errorf("%s: the store received %q; want the command once", c.name, got)
			}
			if faults := store.faults(); len(faults) != 0 {
				t.Errorf("%s: %q", c.name, faults)
			}
		})
	}
}

// TestEveryReadAndWriteHasADeadline: single commands, a pipeline and a
// transaction, over the first connection and over one dialed later, and not
// one read, write or dial without a deadline inside the bounds. The spy is
// shown to see what it is there to see.
func TestEveryReadAndWriteHasADeadline(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, func(conn int, cmd []string) string {
		if cmd[0] == "get" && cmd[1] == "drop" {
			return hangUp
		}
		return accepting(conn, cmd)
	})
	conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, store.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	work := func() {
		t.Helper()
		if err := conn.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
			t.Error(err)
		}
		pipe := conn.Client().Pipeline()
		pipe.Get(ctx, "a")
		pipe.Get(ctx, "b")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Error(err)
		}
		if _, err := conn.Client().TxPipelined(ctx, func(p redis.Pipeliner) error { return p.Set(ctx, "k", "v", 0).Err() }); err != nil {
			t.Error(err)
		}
	}
	work()
	if err := conn.Client().Get(ctx, "drop").Err(); !errors.Is(err, io.EOF) {
		t.Errorf("a command on a connection the store dropped = %v; want EOF", err)
	}
	work()
	if n := store.dialed(); n != 2 {
		t.Errorf("%d dials; want two, the second by the command after the drop", n)
	}
	if faults := store.faults(); len(faults) != 0 {
		t.Errorf("%q", faults)
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	spy := &spyConn{Conn: client}
	go func() { _, _ = io.Copy(io.Discard, server) }()
	if _, err := spy.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := spy.SetWriteDeadline(time.Now().Add(2 * WriteTimeout)); err != nil {
		t.Fatal(err)
	}
	if _, err := spy.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(spy.faults) != 2 || spy.faults[0] != "write with no deadline" || !strings.Contains(spy.faults[1], "over the bound") {
		t.Errorf("the spy saw %q; want a write with no deadline and one over the bound", spy.faults)
	}
}

// TestACommandIsSentOnceAndABrokenConnectionIsDialedOnce: a connection the
// store dropped fails the command that met it, which is not sent again; the
// next command dials once, shakes hands and runs. The connection dialed
// after Open is an ordinary one: nothing on it is answered in the store's
// place.
func TestACommandIsSentOnceAndABrokenConnectionIsDialedOnce(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, func(conn int, cmd []string) string {
		if cmd[0] == "incr" && conn == 1 {
			return hangUp
		}
		return accepting(conn, cmd)
	})
	conn, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), store.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store.commands()
	ctx := context.Background()
	err = conn.Client().Incr(ctx, "n").Err()
	if got := Classify(err); got != Unreachable {
		t.Errorf("the command the drop met = %v, %v; want %v", err, got, Unreachable)
	}
	if got := store.commands(); !reflect.DeepEqual(got, []string{"1: incr n"}) {
		t.Errorf("the store received %q; want the command once", got)
	}
	if got, err := conn.Client().Do(ctx, "PING").Text(); err != nil || got != "PONG" {
		t.Errorf("PING on the connection dialed next = %q, %v", got, err)
	}
	want := []string{"2: hello 3 auth bench s3cret", "2: PING"}
	if got := store.commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("the store received %q; want %q", got, want)
	}
	if n := store.dialed(); n != 2 {
		t.Errorf("%d dials; want two", n)
	}
}

// TestClose: closing twice and closing nothing are nil, and a command on a
// closed connection is the caller's own mistake, not the store's.
func TestClose(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, accepting)
	conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, store.dial)
	if err != nil {
		t.Fatal(err)
	}
	client := conn.Client()
	if client == nil || client != conn.Client() {
		t.Fatalf("Client = %v, then %v; want the one client", client, conn.Client())
	}
	if err := conn.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("close again: %v", err)
	}
	var none *Conn
	if err := none.Close(); err != nil {
		t.Errorf("close of nil: %v", err)
	}
	err = client.Ping(context.Background()).Err()
	if !errors.Is(err, redis.ErrClosed) || Classify(err) != Other {
		t.Errorf("a command after Close = %v, %v; want the closed client, %v", err, Classify(err), Other)
	}
}

// TestReconnectSocketCleanup: when a connection drops and a reconnect attempt
// fails its handshake (HELLO or AUTH refused), upstream go-redis drops the
// connection from its pool without closing the raw socket. Conn.Close must
// close all leaked sockets from failed reconnect handshakes, with repeated
// and concurrent failures covered, and Close must be idempotent.
func TestReconnectSocketCleanup(t *testing.T) {
	t.Parallel()

	// Single reconnect failure and idempotent close.
	store := newFakeStore(t, func(conn int, cmd []string) string {
		if conn == 1 {
			if cmd[0] == "hello" {
				return helloAccepted
			}
			return hangUp // drops conn 1 on first GET
		}
		// conn == 2 (reconnect attempt)
		if cmd[0] == "hello" || cmd[0] == "auth" {
			return "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
		}
		return accepting(conn, cmd)
	})
	conn, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), store.dial)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = conn.Client().Get(ctx, "k").Err()  // drops conn 1
	_ = conn.Client().Get(ctx, "k2").Err() // reconnect refused
	if n := store.dialed(); n != 2 {
		t.Fatalf("%d dials; want 2", n)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	if n := store.open(); n != 0 {
		t.Fatalf("%d sockets left open after Conn.Close; want 0", n)
	}

	// Repeated reconnect failures.
	storeRepeated := newFakeStore(t, func(conn int, cmd []string) string {
		if conn == 1 {
			if cmd[0] == "hello" {
				return helloAccepted
			}
			return hangUp
		}
		return "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
	})
	connRepeated, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), storeRepeated.dial)
	if err != nil {
		t.Fatal(err)
	}
	_ = connRepeated.Client().Get(ctx, "drop").Err()
	for i := 0; i < 4; i++ {
		_ = connRepeated.Client().Get(ctx, "fail").Err()
	}
	if n := storeRepeated.dialed(); n != 5 {
		t.Fatalf("repeated: %d dials; want 5", n)
	}
	if err := connRepeated.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := storeRepeated.open(); n != 0 {
		t.Fatalf("repeated: %d sockets left open after Conn.Close; want 0", n)
	}

	// Concurrent reconnect failures.
	storeConcurrent := newFakeStore(t, func(conn int, cmd []string) string {
		if conn == 1 {
			if cmd[0] == "hello" {
				return helloAccepted
			}
			return hangUp
		}
		return "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
	})
	connConcurrent, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), storeConcurrent.dial)
	if err != nil {
		t.Fatal(err)
	}
	_ = connConcurrent.Client().Get(ctx, "drop").Err()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = connConcurrent.Client().Get(ctx, "concurrent").Err()
		}()
	}
	wg.Wait()
	if err := connConcurrent.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := storeConcurrent.open(); n != 0 {
		t.Fatalf("concurrent: %d sockets left open after Conn.Close; want 0", n)
	}
}

// TestExplain: an error a command returned becomes this package's, with the
// connection named, the class kept and the next thing to do.
func TestExplain(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, accepting)
	conn, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), store.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.Explain(nil); got != nil {
		t.Errorf("Explain(nil) = %v", got)
	}
	const tried = "redis at store.test:6379 as user bench (password from PW): "
	refused := conn.Client().LPush(context.Background(), "k", "v").Err()
	for _, c := range []struct {
		err   error
		class Class
		want  string
	}{
		{refused, Other,
			tried + "failed: WRONGTYPE Operation against a key holding the wrong kind of value; next: the store answered, so the connection stands: read the refusal as the command's own"},
		{fmt.Errorf("row add: %w", io.EOF), Unreachable,
			tried + "unreachable: row add: EOF; next: start the store or correct the address, which was given to this tool"},
		{errors.New("NOAUTH Authentication required."), AuthRefused,
			tried + "login refused: NOAUTH Authentication required.; next: check that PW holds the password of bench and that the store has that user switched on"},
		{context.Canceled, Other,
			tried + "failed: context canceled; next: run it again: it was cancelled before the store answered"},
		{errors.New("two\nlines\x1b[2J"), Other,
			tried + `failed: two\x0alines\x1b[2J; next: the store answered, so the connection stands: read the refusal as the command's own`},
		{errors.New("ERR unknown command `hello`, with args beginning with: `3`, `auth`, `bench`, `s3cret`"), Other,
			tried + "failed: withheld: it held the password; next: the store answered, so the connection stands: read the refusal as the command's own"},
	} {
		got := conn.Explain(c.err)
		if got == nil || got.Error() != c.want {
			t.Errorf("Explain(%q):\n got %v\nwant %s", c.err, got, c.want)
			continue
		}
		if class := Classify(got); class != c.class {
			t.Errorf("Explain(%q) is %v; want %v", c.err, class, c.class)
		}
		if again := conn.Explain(got); again != got {
			t.Errorf("Explain of its own error = %v; want it unchanged", again)
		}
		if wrapped := fmt.Errorf("verb: %w", got); conn.Explain(wrapped) != wrapped {
			t.Errorf("Explain of its own error, wrapped, is not the wrapped error")
		}
		for _, shown := range errorsText(got) {
			if strings.Contains(shown, "s3cret") {
				t.Errorf("Explain(%q): %q shows the password", c.err, shown)
			}
		}
	}
	if got := conn.Explain(refused); !errors.Is(got, refused) {
		t.Errorf("%v does not unwrap to the error it explains", got)
	}
}

// rawSocket is a connection that is a socket: it hands out a raw connection.
type rawSocket struct {
	net.Conn
	asked int
}

var errRawSocket = errors.New("the raw connection of the fake socket")

func (s *rawSocket) SyscallConn() (syscall.RawConn, error) {
	s.asked++
	return nil, errRawSocket
}

// TestTheFirstConnectionOfASocketIsStillASocket: go-redis asks a connection
// for its raw socket to see whether it is still alive. The connection Open
// dials answers with the socket's own when it has one, and is not asked
// when it has none.
func TestTheFirstConnectionOfASocketIsStillASocket(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer server.Close()
	socket := &rawSocket{Conn: client}
	dialer := &firstDial{dial: func(context.Context, string, string) (net.Conn, error) { return socket, nil }}
	dialer.opening.Store(true)
	conn, err := dialer.dialer(context.Background(), "tcp", storeAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sys, ok := conn.(syscall.Conn)
	if !ok {
		t.Fatalf("%T is not a socket; the connection it wraps is", conn)
	}
	if _, err := sys.SyscallConn(); err != errRawSocket || socket.asked != 1 {
		t.Errorf("SyscallConn = %v after %d calls of the socket's own; want the socket's answer", err, socket.asked)
	}

	pipe, other := net.Pipe()
	defer other.Close()
	plain := &firstDial{dial: func(context.Context, string, string) (net.Conn, error) { return pipe, nil }}
	plain.opening.Store(true)
	conn, err = plain.dialer(context.Background(), "tcp", storeAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, ok := conn.(syscall.Conn); ok {
		t.Errorf("%T says it is a socket; the connection it wraps is not", conn)
	}

	// After Open, dials remain tracked and preserve socket identity.
	plain.done()
	conn, err = plain.dialer(context.Background(), "tcp", storeAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, ok := conn.(syscall.Conn); ok {
		t.Errorf("%T says it is a socket; the connection it wraps is not", conn)
	}

	socketPlain := &rawSocket{Conn: client}
	socketDialer := &firstDial{dial: func(context.Context, string, string) (net.Conn, error) { return socketPlain, nil }}
	socketDialer.done()
	sconn, err := socketDialer.dialer(context.Background(), "tcp", storeAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer sconn.Close()
	if sys, ok := sconn.(syscall.Conn); !ok {
		t.Fatalf("%T is not a socket; the connection it wraps is", sconn)
	} else if _, err := sys.SyscallConn(); err != errRawSocket || socketPlain.asked != 1 {
		t.Errorf("SyscallConn = %v after %d calls; want the socket's answer", err, socketPlain.asked)
	}
}
