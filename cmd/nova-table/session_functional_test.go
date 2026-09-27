//go:build functional

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestShellUsesOneConnectionAndExistingReceipts(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	monitor, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	if err := monitor.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(monitor, "*1\r\n$7\r\nMONITOR\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(monitor)
	if line, err := reader.ReadString('\n'); err != nil || line != "+OK\r\n" {
		t.Fatalf("monitor: %q %v", line, err)
	}
	script := `# One session uses the same commands and quote rules throughout.
create jobs --columns 'ready,working,done,note:text,pct:pct(done)'
row add jobs 'the tests' --label '--seat'
cell add jobs 'the tests' ready m1 m2 --score 7
row set jobs 'the tests' 'note=$HOME $(touch file) ` + "`" + `whoami` + "`" + ` *'
cell move jobs 'the tests' ready working m1 m2
member find jobs m1
view set work --tables jobs --summary done
watch --view work --once
render --view work
nova-table show jobs
quit
drop jobs
`
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--actor", "shell-test"}, &out, &errout)
	if code != 0 || errout.Len() != 0 {
		t.Fatalf("shell: %d\n%s\n%s", code, &out, &errout)
	}
	if err := admin.Echo(ctx, "shell-end").Err(); err != nil {
		t.Fatal(err)
	}
	hellos, calls := 0, 0
	clients := map[string]bool{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"echo" "shell-end"`) {
			break
		}
		if strings.Contains(line, "[0 lua]") {
			continue
		}
		parts := strings.SplitN(line, "] ", 2)
		if len(parts) != 2 {
			t.Fatalf("monitor: %q", line)
		}
		command := strings.ToLower(parts[1])
		if strings.HasPrefix(command, `"hello"`) {
			hellos++
		}
		if strings.HasPrefix(command, `"fcall"`) || strings.HasPrefix(command, `"fcall_ro"`) {
			calls++
			clients[strings.SplitN(parts[0], "[0 ", 2)[1]] = true
		}
	}
	t.Logf("wire: hellos=%d client_connections=%d application_calls=%d", hellos, len(clients), calls)
	if hellos != 1 || len(clients) != 1 || calls != 12 {
		t.Fatalf("wire: hello=%d clients=%d calls=%d", hellos, len(clients), calls)
	}
	if strings.Count(out.String(), "TABLE RECEIPT ") != 5 {
		t.Fatalf("mutation receipts:\n%s", &out)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, " trips=") && !strings.Contains(line, " trips=1") {
			t.Fatalf("per-verb trips grew across the session: %s", line)
		}
	}
	tab, err := ntable.Read(ctx, admin, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	if tab.Revision != 5 || tab.Rows[0].Label != "--seat" || tab.Rows[0].Texts["note"] != "$HOME $(touch file) `whoami` *" {
		t.Fatalf("stored session state: %+v", tab)
	}
	records, err := admin.XRange(ctx, "table:jobs:changes", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 {
		t.Fatalf("durable receipts: %d", len(records))
	}
	for _, r := range records {
		if r.Values["actor"] != "shell-test" {
			t.Fatalf("session defaults lost: %v", r)
		}
	}
}

func TestShellRefusalControlsAndPinnedStore(t *testing.T) {
	t.Parallel()
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			t.Parallel()
			addr := firstRunStore(t)
			script := `create jobs --columns ready
row add jobs r
cell add jobs r ready m
cell add jobs r ready m
row add jobs later
`
			var out, errout bytes.Buffer
			args := []string{"shell", "--redis", addr, "--keep-going=" + fmt.Sprint(keep), "--receipt=false"}
			code := (&application{in: strings.NewReader(script)}).run(args, &out, &errout)
			if code != 1 || !strings.Contains(errout.String(), "already has a place") || strings.Contains(out.String(), "TABLE RECEIPT") {
				t.Fatalf("%d %s %s", code, &out, &errout)
			}
			c := redis.NewClient(&redis.Options{Addr: addr})
			defer c.Close()
			tab, err := ntable.Read(context.Background(), c, "jobs")
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if keep {
				want = 2
			}
			if len(tab.Rows) != want {
				t.Fatalf("keep-going=%v rows=%d want %d", keep, len(tab.Rows), want)
			}
			// A different address and a mid-session seat are rejected before any
			// connection switch. A later valid command still uses the first store.
			out.Reset()
			errout.Reset()
			script = "row add jobs forbidden --redis 127.0.0.1:1\nrow add jobs forbidden --seat elsewhere\nrow add jobs same --redis " + addr + "\n"
			code = (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--keep-going"}, &out, &errout)
			if code != 2 || !strings.Contains(errout.String(), "connection is fixed") {
				t.Fatalf("pin: %d %s %s", code, &out, &errout)
			}
			tab, err = ntable.Read(context.Background(), c, "jobs")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range tab.Rows {
				if r.Key == "forbidden" {
					t.Fatal("a connection-changing line wrote")
				}
			}
			if tab.Rows[len(tab.Rows)-1].Key != "same" {
				t.Fatalf("same connection could not continue: %+v", tab.Rows)
			}
		})
	}
}

func TestShellEpochAndMetadataDefaultsDoNotDrift(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := c.HSet(ctx, "generation", "n", "5").Err(); err != nil {
		t.Fatal(err)
	}
	script := `create jobs --columns ready --epoch-key generation
row add jobs stale --epoch 4
row add jobs override --actor alternate --receipt=false
row add jobs default
`
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--epoch", "5", "--actor", "session", "--keep-going"}, &out, &errout)
	if code != 1 || !strings.Contains(errout.String(), "epoch") {
		t.Fatalf("%d %s %s", code, &out, &errout)
	}
	if strings.Count(out.String(), "TABLE RECEIPT ") != 2 {
		t.Fatalf("per-command receipt override leaked: %s", &out)
	}
	tab, err := ntable.Read(ctx, c, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	if tab.Epoch != 5 || tab.Revision != 3 || len(tab.Rows) != 2 || tab.Rows[0].Key != "override" || tab.Rows[1].Key != "default" {
		t.Fatalf("epoch/session state: %+v", tab)
	}
	events, err := c.XRange(ctx, "table:jobs:changes", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"session", "alternate", "session"} {
		if events[i].Values["actor"] != want || events[i].Values["epoch"] != "5" {
			t.Fatalf("event %d: %v", i, events[i])
		}
	}
}

func TestShellSyntaxErrorDoesNotPartiallyExecuteLine(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	script := "create t --columns ready\nrow add t bad; drop t\nrow add t 'unfinished\nrow add t good\n"
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--keep-going"}, &out, &errout)
	if code != 2 || !strings.Contains(errout.String(), "line 2") || !strings.Contains(errout.String(), "line 3") {
		t.Fatalf("%d %s %s", code, &out, &errout)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tab, err := ntable.Read(context.Background(), c, "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Rows) != 1 || tab.Rows[0].Key != "good" || tab.Revision != 2 {
		t.Fatalf("bad input line wrote: %+v", tab)
	}
}

func TestDocumentedShellSessionRuns(t *testing.T) {
	t.Parallel()
	doc := readDoc(t, "nova-table/README.md")
	_, section, ok := strings.Cut(doc, "## Resident shell\n")
	if !ok {
		t.Fatal("resident shell guide missing")
	}
	_, body, ok := strings.Cut(section, "<<'TABLE'\n")
	if !ok {
		t.Fatal("shell example missing")
	}
	script, _, ok := strings.Cut(body, "\nTABLE\n")
	if !ok {
		t.Fatal("shell example has no terminator")
	}
	addr := firstRunStore(t)
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr}, &out, &errout)
	if code != 0 || errout.Len() != 0 {
		t.Fatalf("documented shell: %d\n%s\n%s", code, &out, &errout)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tab, err := ntable.Read(context.Background(), c, "session-demo")
	if err != nil {
		t.Fatal(err)
	}
	if tab.Revision != 5 || len(tab.Rows) != 1 || tab.Rows[0].Cells[1].Count != 2 || tab.Rows[0].Texts["note"] != "Running both checks" {
		t.Fatalf("documented session result: %+v", tab)
	}
}

// The first client's dial always refuses, reproducing a saturated pool even
// if the library starts a background probe. A fresh client's first dial can
// reach the same store. No sleep or race against the probe's timer is needed.
func TestShellFreshDialAfterConnectionFailureWithoutReplay(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	bad := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 1, MaxRetries: -1, DialerRetries: 1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		},
	})
	reopens := 0
	var hellos atomic.Int64
	shared := sharedConnection(store.New(bad), func() (*store.Store, error) {
		reopens++
		return store.New(redis.NewClient(&redis.Options{Addr: addr, PoolSize: 1, DisableIdentity: true,
			OnConnect: func(context.Context, *redis.Conn) error { hellos.Add(1); return nil },
		})), nil
	})
	defer func() { _ = shared.Store.Close() }()
	var out, errs bytes.Buffer
	app := &application{shared: shared, addr: addr}
	// A failed create must not be replayed after the store recovers. Help needs
	// no connection; two later list calls must reuse the single fresh one.
	code := app.readCommands(strings.NewReader("create failed --columns ready\nversion\nlist\nlist\n"), &out, &errs, true, false)
	if code != 2 || reopens != 1 || hellos.Load() != 1 || strings.Count(out.String(), "TABLE LIST tables=0 trips=1") != 2 || !strings.Contains(errs.String(), "line 1") {
		t.Fatalf("recovery: code=%d reopens=%d hellos=%d out=%s err=%s", code, reopens, hellos.Load(), &out, &errs)
	}
}

// Drop one reply only after the server produced it. This is an uncertain
// write outcome, not a dial failure: retrying would append a second receipt.
type shellLostReplyConn struct {
	net.Conn
	lost *atomic.Bool
	drop bool
}

func (c *shellLostReplyConn) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("\r\nfcall\r\n")) && c.lost.CompareAndSwap(false, true) {
		c.drop = true
	}
	return c.Conn.Write(p)
}
func (c *shellLostReplyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.drop && n > 0 {
		c.drop = false
		return 0, io.EOF
	}
	return n, err
}

type shellLostReplyHook struct{ lost *atomic.Bool }

func (h shellLostReplyHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := next(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &shellLostReplyConn{Conn: c, lost: h.lost}, nil
	}
}
func (shellLostReplyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (shellLostReplyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestShellLostWriteReplyIsNeverReplayed(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	if code, out, err := runTable(at(addr, "create", "jobs", "--columns", "ready")...); code != 0 {
		t.Fatalf("setup %d %s %s", code, out, err)
	}
	var lost atomic.Bool
	open := func() (*store.Store, error) {
		st, err := openShellStore(addr)
		if err == nil {
			st.Client().AddHook(shellLostReplyHook{lost: &lost})
		}
		return st, err
	}
	st, err := open()
	if err != nil {
		t.Fatal(err)
	}
	shared := sharedConnection(st, open)
	defer func() { _ = shared.Store.Close() }()
	var out, errs bytes.Buffer
	code := (&application{shared: shared, addr: addr}).readCommands(strings.NewReader("row add jobs committed\nlist\n"), &out, &errs, true, false)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	defer admin.Close()
	tab, err := ntable.Read(context.Background(), admin, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	events, err := admin.XLen(context.Background(), ntable.ChangesKey("jobs")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || !lost.Load() || tab.Revision != 2 || events != 2 || len(tab.Rows) != 1 || !strings.Contains(out.String(), "TABLE LIST tables=1 trips=1") {
		t.Fatalf("lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	}
}
