//go:build functional

package redisconn_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// relay stands between the client and a store and can lose a command on the
// way: before it is forwarded (the store never sees it), or after the store
// answered (the store did it and the client never hears). It counts what the
// client sends, one burst per round trip, since requests and replies
// alternate. Closing it closes its listener, so a later dial is refused.
type relay struct {
	ln     net.Listener
	store  string
	trips  atomic.Int64
	mu     sync.Mutex
	before []byte // a burst holding this is dropped before it is forwarded
	after  []byte // a burst holding this is forwarded, and its reply dropped
	conns  []net.Conn
}

func newRelay(t *testing.T, store string) *relay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &relay{ln: ln, store: store}
	t.Cleanup(r.close)
	go r.accept()
	return r
}

func (r *relay) addr() string { return r.ln.Addr().String() }

// drop sets what is lost next: before forwarding, or after the reply.
func (r *relay) drop(before, after string) {
	r.mu.Lock()
	r.before, r.after = []byte(before), []byte(after)
	r.mu.Unlock()
}

func (r *relay) close() {
	_ = r.ln.Close()
	r.mu.Lock()
	for _, c := range r.conns {
		_ = c.Close()
	}
	r.mu.Unlock()
}

func (r *relay) accept() {
	for {
		client, err := r.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", r.store)
		if err != nil {
			_ = client.Close()
			continue
		}
		r.mu.Lock()
		r.conns = append(r.conns, client, server)
		r.mu.Unlock()
		go r.serve(client, server)
	}
}

func (r *relay) serve(client, server net.Conn) {
	hangUp := func() { _ = client.Close(); _ = server.Close() }
	var swallow atomic.Bool
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := server.Read(buf)
			if err != nil {
				hangUp()
				return
			}
			if swallow.Load() {
				hangUp() // the store answered; the client never hears
				return
			}
			if _, err := client.Write(buf[:n]); err != nil {
				hangUp()
				return
			}
		}
	}()
	buf := make([]byte, 64<<10)
	for {
		n, err := client.Read(buf)
		if err != nil {
			hangUp()
			return
		}
		burst := buf[:n]
		r.trips.Add(1)
		r.mu.Lock()
		before := len(r.before) > 0 && bytes.Contains(burst, r.before)
		after := len(r.after) > 0 && bytes.Contains(burst, r.after)
		if before || after {
			r.before, r.after = nil, nil // once
		}
		r.mu.Unlock()
		if before {
			hangUp()
			return
		}
		if after {
			swallow.Store(true)
		}
		if _, err := server.Write(burst); err != nil {
			hangUp()
			return
		}
	}
}

// incr is how go-redis writes INCR n on the wire.
const incr = "$4\r\nincr\r\n$1\r\nn\r\n"

// TestWhereAFailureStandsAgainstTheCommand: the three a tool answers
// differently, against a redis-server behind a relay. A reply lost after
// the command was written is Unconfirmed, whether the store did it (the
// reply dropped after the store answered) or not (the command dropped
// before it was forwarded): the client cannot tell, and the line says the
// write may have committed. A command that met a refused dial, or a
// connection whose setup was lost, never reached the store and is
// Unreachable. A login the store refused on a connection dialed later is
// AuthRefused.
func TestWhereAFailureStandsAgainstTheCommand(t *testing.T) {
	t.Parallel()
	const password, changed = "relay-secret-A1", "relay-secret-A2"
	addr := testredis.Start(t, "--requirepass", password)
	env := environment(map[string]string{"NOVA_TEST_PW": password})
	admin, err := redisconn.Open(context.Background(), redisconn.Options{Addr: addr, PasswordEnv: "NOVA_TEST_PW"}, env)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	stored := func() string {
		v, err := admin.Client().Get(ctx, "n").Result()
		if errors.Is(err, redis.Nil) {
			return "absent"
		}
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	r := newRelay(t, addr)
	conn, err := redisconn.Open(ctx, redisconn.Options{Addr: r.addr(), PasswordEnv: "NOVA_TEST_PW", PoolSize: 1}, env)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	class := func(err error) redisconn.Class {
		c, ok := redisconn.Failed(err)
		if !ok {
			t.Fatalf("%v is not an error of redisconn", err)
		}
		if strings.Count(err.Error(), "redis at ") != 1 || conn.Explain(err) != err {
			t.Errorf("%q is not one line, or Explain changes it", err)
		}
		return c
	}

	// Lost before the store saw it: the store has nothing, and the client
	// cannot know that.
	r.drop(incr, "")
	err = conn.Client().Incr(ctx, "n").Err()
	if c := class(err); c != redisconn.Unconfirmed || !strings.Contains(err.Error(), "the write may have committed, so read it back before retrying") {
		t.Errorf("a command dropped before it was forwarded: %v, %v; want %v", err, c, redisconn.Unconfirmed)
	}
	if got := stored(); got != "absent" {
		t.Errorf("the store holds %s; want nothing: the command never reached it", got)
	}

	// Lost after the store answered: the store did it.
	r.drop("", incr)
	err = conn.Client().Incr(ctx, "n").Err()
	if c := class(err); c != redisconn.Unconfirmed {
		t.Errorf("a reply dropped after the store answered: %v, %v; want %v", err, c, redisconn.Unconfirmed)
	}
	if got := stored(); got != "1" {
		t.Errorf("the store holds %s; want 1: the write committed", got)
	}

	// The same in a pipeline and a transaction.
	for _, tx := range []bool{false, true} {
		r.drop("", "incr")
		pipe := conn.Client().Pipeline()
		if tx {
			pipe = conn.Client().TxPipeline()
		}
		cmd := pipe.Incr(ctx, "n")
		_, err := pipe.Exec(ctx)
		if class(err) != redisconn.Unconfirmed || class(cmd.Err()) != redisconn.Unconfirmed {
			t.Errorf("a pipeline (transaction %v) whose reply was lost: %v and %v; want %v", tx, err, cmd.Err(), redisconn.Unconfirmed)
		}
	}
	if got := stored(); got != "3" {
		t.Errorf("the store holds %s; want 3", got)
	}

	// The connection's setup lost: the next command dials, and the relay
	// drops its HELLO. Nothing of the command was sent.
	r.drop("hello", "")
	err = conn.Client().Incr(ctx, "n").Err()
	if c := class(err); c != redisconn.Unreachable || !strings.Contains(err.Error(), "next: start the store or correct the address") {
		t.Errorf("a command whose connection's setup was lost: %v, %v; want %v", err, c, redisconn.Unreachable)
	}
	if got := stored(); got != "3" {
		t.Errorf("the store holds %s; want 3: the command never reached it", got)
	}

	// The login refused on the connection dialed next.
	if err := admin.Client().ConfigSet(ctx, "requirepass", changed).Err(); err != nil {
		t.Fatal(err)
	}
	r.drop("", incr)
	_ = conn.Client().Incr(ctx, "n").Err() // the store does it; the reply is lost, and so is the connection
	err = conn.Client().Incr(ctx, "n").Err()
	if c := class(err); c != redisconn.AuthRefused {
		t.Errorf("a command on a connection whose login was refused: %v, %v; want %v", err, c, redisconn.AuthRefused)
	}

	// A refused dial: the relay is gone.
	r.close()
	err = conn.Client().Incr(ctx, "n").Err()
	if c := class(err); c != redisconn.Unreachable {
		t.Errorf("a command that met a refused dial: %v, %v; want %v", err, c, redisconn.Unreachable)
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), changed) {
		t.Errorf("%q shows a password", err)
	}
}

// TestAVerbCostsTwoRoundTripsOnTheWire: Open and one pipeline, counted by
// the relay on the wire and by the connection's own counter: two, one of
// them the handshake.
func TestAVerbCostsTwoRoundTripsOnTheWire(t *testing.T) {
	t.Parallel()
	r := newRelay(t, testredis.Start(t))
	ctx := context.Background()
	conn, err := redisconn.Open(ctx, redisconn.Options{Addr: r.addr()}, nothing)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	pipe := conn.Client().Pipeline()
	pipe.Set(ctx, "a", "1", 0)
	pipe.Incr(ctx, "b")
	pipe.Get(ctx, "a")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	trips := conn.Trips()
	if wire := r.trips.Load(); wire != 2 || trips.Total() != wire || trips.Setup() != 1 || trips.String() != "trips=2 setup=1" {
		t.Errorf("the wire carried %d round trips; the counter says %d, setup %d, %q; want 2, 2, 1", wire, trips.Total(), trips.Setup(), trips.String())
	}
}

// TestAnOptionalPasswordAgainstTheStore: PasswordOptional against a store
// with and without requirepass, the variable empty and set.
func TestAnOptionalPasswordAgainstTheStore(t *testing.T) {
	t.Parallel()
	const password = "optional-secret-B3"
	ctx := context.Background()
	o := redisconn.Options{PasswordEnv: "NOVA_REDIS_PASSWORD", PasswordOptional: true}
	open := addr(testredis.Start(t))
	locked := addr(testredis.Start(t, "--requirepass", password))
	for _, c := range []struct {
		name  string
		addr  string
		pw    string
		class redisconn.Class // Other: opened
	}{
		{"no requirepass, empty: no password", string(open), "", redisconn.Other},
		{"no requirepass, set: the default user takes any password", string(open), password, redisconn.Other},
		{"requirepass, set: the password", string(locked), password, redisconn.Other},
		{"requirepass, empty: refused, and the variable named", string(locked), "", redisconn.AuthRefused},
	} {
		o.Addr = c.addr
		conn, err := redisconn.Open(ctx, o, environment(map[string]string{"NOVA_REDIS_PASSWORD": c.pw}))
		if c.class == redisconn.Other {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
				continue
			}
			if err := conn.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
				t.Errorf("%s: a command: %v", c.name, err)
			}
			_ = conn.Close()
			continue
		}
		if got, _ := redisconn.Failed(err); got != c.class || !strings.Contains(err.Error(), "no password (NOVA_REDIS_PASSWORD is empty)") ||
			!strings.HasSuffix(err.Error(), "next: export NOVA_REDIS_PASSWORD, holding the password of the default user, in the environment of this process") {
			t.Errorf("%s: %v; want %v naming the variable", c.name, err, c.class)
		}
	}

	// A seat's password handed over from memory, wrong: the line names the
	// seat and never the value.
	const wrong = "seat-secret-wrong-C4"
	_, err := redisconn.Open(ctx, redisconn.Options{Addr: string(locked), Password: &redisconn.Secret{From: "the seat bench-3", Read: func() string { return wrong }}}, nothing)
	if got, _ := redisconn.Failed(err); got != redisconn.AuthRefused || !strings.Contains(err.Error(), "(password from the seat bench-3)") || strings.Contains(err.Error(), wrong) {
		t.Errorf("a wrong seat password: %v; want it refused, the seat named, the value not shown", err)
	}
}

type addr string
