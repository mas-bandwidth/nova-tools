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
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestFailed: an error of this package, wrapped or not, is found with its
// class, each of the four; any other error, go-redis's own included, is not.
func TestFailed(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, accepting)
	conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, store.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, noAddress := Resolve(Options{}, nil)
	_, noPassword := Resolve(Options{Addr: storeAddr, User: "bench"}, nil)
	refused := conn.Client().LPush(context.Background(), "k", "v").Err()
	for _, c := range []struct {
		name  string
		err   error
		class Class
	}{
		{"a refusal of Resolve: no address", noAddress, Unreachable},
		{"a refusal of Resolve: no password", noPassword, AuthRefused},
		{"a reply lost, explained", conn.Explain(io.EOF), Unconfirmed},
		{"a refusal of the store, explained", conn.Explain(refused), Other},
	} {
		for _, err := range []error{c.err, fmt.Errorf("verb: %w", c.err), fmt.Errorf("a: %w", fmt.Errorf("b: %w", c.err))} {
			if class, ok := Failed(err); !ok || class != c.class {
				t.Errorf("%s: Failed(%v) = %v, %v; want %v, true", c.name, err, class, ok, c.class)
			}
		}
	}
	for _, err := range []error{nil, io.EOF, redis.Nil, refused, errors.New("dial tcp: connection refused"), context.Canceled} {
		if class, ok := Failed(err); ok || class != Other {
			t.Errorf("Failed(%v) = %v, %v; want %v, false", err, class, ok, Other)
		}
	}
}

// dropOn is a store that accepts, except that it hangs up on the command
// named on the connection numbered.
func dropOn(conn int, name string) func(int, []string) string {
	return func(c int, cmd []string) string {
		if c == conn && strings.EqualFold(cmd[0], name) {
			return hangUp
		}
		return accepting(c, cmd)
	}
}

// thenRefused is a dialer that dials the store the first time and meets a
// closed port every time after.
func thenRefused(store *fakeStore) dialFunc {
	var mu sync.Mutex
	dials, refusals := 0, 0
	refuse := refusedDial(&refusals)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dials++
		first := dials == 1
		mu.Unlock()
		if first {
			return store.dial(ctx, network, addr)
		}
		mu.Lock()
		defer mu.Unlock()
		return refuse(ctx, network, addr)
	}
}

// oneLine reports whether err is one line of this package: one "redis at",
// one "; next: ", no line break.
func oneLine(err error) bool {
	text := err.Error()
	return strings.Count(text, "redis at ") == 1 && strings.Count(text, "; next: ") == 1 && !strings.ContainsAny(text, "\r\n")
}

// TestTheConnectionExplainsItsOwnFailures: the hook Open installs. A
// command whose connection dropped after it was written is Unconfirmed; a
// command that met a refused dial or a setup that failed never reached the
// store and is Unreachable; a login the store refused on a later connection
// is AuthRefused. Each comes back as the call's error and the command's
// own, one line, found by Failed, and Explain of it changes nothing. An
// absent value, a refusal of the store and a cancelled context come back as
// go-redis made them.
func TestTheConnectionExplainsItsOwnFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		reply func(int, []string) string
		dial  func(*fakeStore) dialFunc
		class Class
		want  string
		sent  []string // what the store received after Open
	}{
		{"the connection drops after the command was written", dropOn(1, "incr"), nil, Unconfirmed,
			"reply lost after the command was sent: EOF; next: the write may have committed, so read it back before retrying",
			[]string{"1: incr n"}},
		{"the command meets a closed port", dropOn(1, "ping"), thenRefused, Unreachable,
			"unreachable: dial tcp store.test:6379: connect: connection refused; next: start the store or correct the address, which was given to this tool",
			[]string{"1: ping"}},
		{"the connection dialed for the command drops in its setup", func(c int, cmd []string) string {
			if c == 2 && cmd[0] == "hello" {
				return hangUp
			}
			return dropOn(1, "ping")(c, cmd)
		}, nil, Unreachable,
			"unreachable: EOF; next: start the store or correct the address, which was given to this tool",
			[]string{"1: ping", "2: hello 3 auth default s3cret"}},
		{"the connection dialed for the command is refused its login", func(c int, cmd []string) string {
			if c == 2 && (cmd[0] == "hello" || cmd[0] == "auth") {
				return "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
			}
			return dropOn(1, "ping")(c, cmd)
		}, nil, AuthRefused,
			"login refused: WRONGPASS invalid username-password pair or user is disabled.; next: name the user, or check that PW holds the password of the default user",
			[]string{"1: ping", "2: hello 3 auth default s3cret", "2: auth s3cret"}},
	} {
		store := newFakeStore(t, c.reply)
		dial := store.dial
		if c.dial != nil {
			dial = c.dial(store)
		}
		conn, err := open(ctx, Options{Addr: storeAddr, PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), dial)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		store.commands()
		if c.class != Unconfirmed {
			// The first connection drops on a PING of the caller's, so the
			// command below needs a connection of its own.
			_ = conn.Client().Ping(ctx).Err()
		}
		cmd := conn.Client().Incr(ctx, "n")
		err = cmd.Err()
		if class, ok := Failed(err); !ok || class != c.class {
			t.Errorf("%s: %v is %v, %v; want %v", c.name, err, class, ok, c.class)
		} else if want := "redis at store.test:6379 as the default user (password from PW): " + c.want; err.Error() != want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, err, want)
		}
		if !oneLine(err) || conn.Explain(err) != err {
			t.Errorf("%s: %q is not one line of this package, or Explain changes it", c.name, err)
		}
		if got := store.commands(); !reflect.DeepEqual(got, c.sent) {
			t.Errorf("%s: the store received %q; want %q", c.name, got, c.sent)
		}
		_ = conn.Close()
	}

	store := newFakeStore(t, accepting)
	conn, err := open(ctx, Options{Addr: storeAddr}, nothing, store.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Client().Get(ctx, "absent").Err(); err != redis.Nil {
		t.Errorf("an absent value = %#v; want redis.Nil itself", err)
	}
	wrongType := conn.Client().LPush(ctx, "k", "v").Err()
	var answered redis.Error
	if _, ok := Failed(wrongType); ok || !errors.As(wrongType, &answered) || !strings.HasPrefix(wrongType.Error(), "WRONGTYPE ") {
		t.Errorf("a refusal of the store = %v; want go-redis's own WRONGTYPE", wrongType)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := conn.Client().Get(cancelled, "key").Err(); !errors.Is(err, context.Canceled) || isFailure(err) {
		t.Errorf("a cancelled context = %v; want go-redis's own", err)
	}
}

// TestThePipelineExplainsItsOwnFailures: the same for a pipeline and a
// transaction: what Exec returns and every command's error, each Unconfirmed
// when the connection dropped after the commands were written and
// Unreachable when the dial was refused, and redisconn.Exec reads the same.
func TestThePipelineExplainsItsOwnFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		reply func(int, []string) string
		dial  func(*fakeStore) dialFunc
		class Class
	}{
		{"dropped after it was written", dropOn(1, "get"), nil, Unconfirmed},
		{"a closed port", dropOn(1, "ping"), thenRefused, Unreachable},
	} {
		for _, tx := range []bool{false, true} {
			reply := c.reply
			if tx && c.class == Unconfirmed {
				reply = dropOn(1, "multi")
			}
			store := newFakeStore(t, reply)
			dial := store.dial
			if c.dial != nil {
				dial = c.dial(store)
			}
			conn, err := open(ctx, Options{Addr: storeAddr}, nothing, dial)
			if err != nil {
				t.Fatal(err)
			}
			if c.class == Unreachable {
				_ = conn.Client().Ping(ctx).Err()
			}
			pipe := conn.Client().Pipeline()
			if tx {
				pipe = conn.Client().TxPipeline()
			}
			get, set := pipe.Get(ctx, "key"), pipe.Set(ctx, "key", "v", 0)
			cmds, err := pipe.Exec(ctx)
			name := fmt.Sprintf("%s, transaction %v", c.name, tx)
			if class, ok := Failed(err); !ok || class != c.class || !oneLine(err) {
				t.Errorf("%s: Exec = %v, %v, %v; want one line, %v", name, err, class, ok, c.class)
			}
			for _, cmd := range []redis.Cmder{get, set} {
				if class, ok := Failed(cmd.Err()); !ok || class != c.class || !oneLine(cmd.Err()) {
					t.Errorf("%s: %s = %v; want one line, %v", name, cmd.Name(), cmd.Err(), c.class)
				}
			}
			if class, _ := Failed(FirstError(cmds, err)); class != c.class {
				t.Errorf("%s: FirstError is %v; want %v", name, class, c.class)
			}
			_ = conn.Close()
		}
	}
}

// TestExplainingTwiceIsOneLine: the property over every error the package
// can hold, as the hook and a caller would meet them: Explain of anything
// Explain made is that error, and the line is one line with one "redis at".
func TestExplainingTwiceIsOneLine(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, accepting)
	conn, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": "s3cret"}), store.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	causes := []error{io.EOF, io.ErrUnexpectedEOF, context.DeadlineExceeded, context.Canceled, redis.ErrPoolTimeout, redis.ErrClosed,
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")},
		&net.OpError{Op: "read", Net: "tcp", Err: errors.New("i/o timeout")},
		&net.DNSError{Err: "no such host", Name: "store.test"},
		errors.New("NOAUTH Authentication required."), errors.New("WRONGTYPE Operation against a key holding the wrong kind of value"),
		errors.New("two\nlines"),
	}
	for _, cause := range causes {
		for _, err := range []error{cause, fmt.Errorf("row add: %w", cause)} {
			once := conn.Explain(err)
			twice := conn.Explain(once)
			if twice != once || !oneLine(twice) {
				t.Errorf("Explain(Explain(%q)) = %q; want %q, one line", err, twice, once)
			}
			wrapped := fmt.Errorf("verb: %w", once)
			if conn.Explain(wrapped) != wrapped {
				t.Errorf("Explain of %q, wrapped, is not the wrapped error", once)
			}
			if class, ok := Failed(once); !ok || class != Classify(err) {
				t.Errorf("Failed(Explain(%q)) = %v, %v; want %v", err, class, ok, Classify(err))
			}
			if Classify(err) == Unreachable || Classify(err) == Other {
				continue
			}
			// Rule (l): an uncertain write is never said to be unreachable
			// or absent.
			if Classify(err) == Unconfirmed && (strings.Contains(once.Error(), "unreachable") || !strings.Contains(once.Error(), "may have committed")) {
				t.Errorf("Explain(%q) = %q; an uncertain write reads as settled", err, once)
			}
		}
	}
}

// TestPasswordOptional: an empty variable is no password when the caller
// says so and no user is named; a set one is the password; a named user is
// refused, as is an empty variable by default. The message names the
// variable, and a login the store then refuses says to export it.
func TestPasswordOptional(t *testing.T) {
	t.Parallel()
	optional := Options{Addr: storeAddr, PasswordEnv: "NOVA_REDIS_PASSWORD", PasswordOptional: true}
	for _, c := range []struct {
		name  string
		o     Options
		env   map[string]string
		hello string // what the store receives first; "" for a refusal before the dial
		class Class
	}{
		{"empty, optional: no password", optional, nil, "1: hello 3", Other},
		{"set, optional: the password", optional, map[string]string{"NOVA_REDIS_PASSWORD": "s3cret"}, "1: hello 3 auth default s3cret", Other},
		{"empty, by default: refused", Options{Addr: storeAddr, PasswordEnv: "NOVA_REDIS_PASSWORD"}, nil, "", AuthRefused},
		{"empty, optional, a user: refused", Options{Addr: storeAddr, User: "bench", PasswordEnv: "NOVA_REDIS_PASSWORD", PasswordOptional: true}, nil, "", AuthRefused},
	} {
		resolved, err := Resolve(c.o, environment(c.env))
		if c.hello == "" {
			if class, ok := Failed(err); !ok || class != c.class || !strings.Contains(err.Error(), "NOVA_REDIS_PASSWORD is empty") {
				t.Errorf("%s: Resolve = %v; want %v naming the variable", c.name, err, c.class)
			}
			continue
		}
		if err != nil || resolved != c.o {
			t.Errorf("%s: Resolve = %+v, %v; want the options as they were", c.name, resolved, err)
		}
		store := newFakeStore(t, accepting)
		conn, err := open(context.Background(), c.o, environment(c.env), store.dial)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := store.commands(); len(got) == 0 || got[0] != c.hello {
			t.Errorf("%s: the store received %q; want %q first", c.name, got, c.hello)
		}
		_ = conn.Close()
	}

	// A store with requirepass refuses the handshake and every command.
	store := newFakeStore(t, func(int, []string) string { return "-NOAUTH Authentication required.\r\n" })
	_, err := open(context.Background(), optional, nothing, store.dial)
	want := "redis at store.test:6379 as the default user, no password (NOVA_REDIS_PASSWORD is empty): login refused: NOAUTH Authentication required.; next: export NOVA_REDIS_PASSWORD, holding the password of the default user, in the environment of this process"
	if err == nil || err.Error() != want {
		t.Errorf("a store that wants a password, the variable empty:\n got %v\nwant %s", err, want)
	}
}

// TestASecretHandedOver: the password from memory. The store receives it,
// every message names it by its words and never shows it, a wrong one is
// refused with the words, words that hold the password are not shown, and
// a variable given explicitly wins over it.
func TestASecretHandedOver(t *testing.T) {
	t.Parallel()
	const password = "Zq8-seat-password"
	reads := 0
	seat := &Secret{From: "the seat bench-3", Read: func() string { reads++; return password }}
	o := Options{Addr: storeAddr, User: "bench", Password: seat, Env: GeneralEnv}
	env := environment(map[string]string{GeneralEnv.PasswordEnv: "NOT_THIS", "NOT_THIS": "not-this"})

	store := newFakeStore(t, accepting)
	conn, err := open(context.Background(), o, env, store.dial)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.commands(); len(got) == 0 || got[0] != "1: hello 3 auth bench "+password {
		t.Errorf("the store received %q; want the secret, not the variable of Env", got)
	}
	if got, want := conn.String(), "redis at store.test:6379 as user bench (password from the seat bench-3)"; got != want {
		t.Errorf("String = %q; want %q", got, want)
	}
	_ = conn.Close()
	if reads != 1 {
		t.Errorf("the secret was read %d times by Open; want once", reads)
	}

	store = newFakeStore(t, refusing("-WRONGPASS invalid username-password pair or user is disabled.\r\n"))
	_, err = open(context.Background(), o, env, store.dial)
	want := "redis at store.test:6379 as user bench (password from the seat bench-3): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: check that the seat bench-3 holds the password of bench and that the store has that user switched on"
	if err == nil || err.Error() != want {
		t.Errorf("a wrong seat password:\n got %v\nwant %s", err, want)
	}
	for _, shown := range errorsText(err) {
		if strings.Contains(shown, password) {
			t.Errorf("%q shows the password", shown)
		}
	}

	for _, c := range []struct {
		name string
		o    Options
		want string
	}{
		{"empty", Options{Addr: storeAddr, User: "bench", Password: &Secret{From: "the seat bench-3", Read: func() string { return "" }}},
			"redis at store.test:6379 as user bench (password from the seat bench-3): login refused: the seat bench-3 holds no password; next: put the password of bench in the seat bench-3"},
		{"words that hold the password", Options{Addr: storeAddr, User: "bench", Password: &Secret{From: "seat " + password, Read: func() string { return password }}},
			"redis at store.test:6379 as user bench (password from a secret whose name is not shown): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: check that a secret whose name is not shown holds the password of bench and that the store has that user switched on"},
		{"no words", Options{Addr: storeAddr, Password: &Secret{Read: func() string { return password }}},
			"redis at store.test:6379 as the default user (password from the caller's secret): login refused: WRONGPASS invalid username-password pair or user is disabled.; next: name the user, or check that the caller's secret holds the password of the default user"},
	} {
		store := newFakeStore(t, refusing("-WRONGPASS invalid username-password pair or user is disabled.\r\n"))
		_, err := open(context.Background(), c.o, nothing, store.dial)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s:\n got %v\nwant %s", c.name, err, c.want)
		}
	}

	// A variable given explicitly wins, and the secret is not read.
	unread := &Secret{From: "the seat", Read: func() string { t.Error("the secret was read"); return "" }}
	resolved, err := Resolve(Options{Addr: storeAddr, PasswordEnv: "PW", Password: unread}, environment(map[string]string{"PW": "x"}))
	if err != nil || resolved.PasswordEnv != "PW" {
		t.Errorf("an explicit variable and a secret = %+v, %v; want the variable", resolved, err)
	}
}

// TestPoolSizeAndDialBound: the zero options keep go-redis's pool and the
// package's bound; a pool of one is one; a dial bound shortens every dial,
// Open's and a later one, and never lengthens it.
func TestPoolSizeAndDialBound(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		o          Options
		pool       int // 0: go-redis's default, which is more than one
		dialsUnder time.Duration
	}{
		{Options{Addr: storeAddr}, 0, DialTimeout},
		{Options{Addr: storeAddr, PoolSize: 1, DialTimeout: time.Second}, 1, time.Second},
		{Options{Addr: storeAddr, PoolSize: -1, DialTimeout: time.Hour}, 0, DialTimeout},
	} {
		store := newFakeStore(t, dropOn(1, "ping"))
		var mu sync.Mutex
		var bounds []time.Duration
		dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			deadline, ok := ctx.Deadline()
			mu.Lock()
			if ok {
				bounds = append(bounds, time.Until(deadline))
			} else {
				bounds = append(bounds, -1)
			}
			mu.Unlock()
			return store.dial(ctx, network, addr)
		}
		conn, err := open(context.Background(), c.o, nothing, dial)
		if err != nil {
			t.Fatal(err)
		}
		pool := conn.Client().Options().PoolSize
		if (c.pool == 0 && pool <= 1) || (c.pool != 0 && pool != c.pool) {
			t.Errorf("%+v: a pool of %d; want %d (0: go-redis's default)", c.o, pool, c.pool)
		}
		_ = conn.Client().Ping(context.Background()).Err() // drops; the next command dials again
		_ = conn.Client().Get(context.Background(), "key").Err()
		mu.Lock()
		if len(bounds) != 2 {
			t.Errorf("%+v: %d dials; want 2", c.o, len(bounds))
		}
		for _, b := range bounds {
			if b <= 0 || b > c.dialsUnder {
				t.Errorf("%+v: a dial bounded by %v; want within %v", c.o, b, c.dialsUnder)
			}
		}
		mu.Unlock()
		_ = conn.Close()
	}
}

// TestTheAddressOfAChainNamesItsVariable: a caller that resolves a chain of
// its own (a flag, then a variable, then another) passes the winning
// variable's name as Env.Addr and no Addr, and the messages say where the
// address came from.
func TestTheAddressOfAChainNamesItsVariable(t *testing.T) {
	t.Parallel()
	dials := 0
	_, err := open(context.Background(), Options{Env: Env{Addr: "NOVA_SPRINT_REDIS"}}, environment(map[string]string{"NOVA_SPRINT_REDIS": storeAddr}), refusedDial(&dials))
	if err == nil || !strings.HasSuffix(err.Error(), "next: start the store or correct the address, which was from NOVA_SPRINT_REDIS") {
		t.Errorf("an address from the winner of a chain: %v; want it named", err)
	}
}
