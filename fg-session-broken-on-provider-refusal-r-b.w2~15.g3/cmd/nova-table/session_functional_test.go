//go:build functional

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/stretchr/testify/require"
)

func TestShellUsesOneConnectionAndExistingReceipts(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, admin.Ping(ctx).Err())
	monitor, err := net.DialTimeout("tcp", addr, 30*time.Second)
	require.NoError(t, err, "%v", err)
	defer monitor.Close()
	require.NoError(t, monitor.SetDeadline(time.Now().Add(30*time.Second)))
	{
		_, err := fmt.Fprint(monitor, "*1\r\n$7\r\nMONITOR\r\n")
		require.NoError(t, err, "%v", err)
	}
	reader := bufio.NewReader(monitor)
	{
		line, err := reader.ReadString('\n')
		require.NoError(t, err, "monitor: %q %v", line, err)
		require.Equal(t, "+OK\r\n", line, "monitor: %q %v", line, err)
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
	require.EqualValues(t, 0, code, "shell: %d\n%s\n%s", code, &out, &errout)
	require.EqualValues(t, 0, errout.Len(), "shell: %d\n%s\n%s", code, &out, &errout)
	require.NoError(t, admin.Echo(ctx, "shell-end").Err())
	hellos, calls := 0, 0
	clients := map[string]bool{}
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err, "%v", err)
		if strings.Contains(line, `"echo" "shell-end"`) {
			break
		}
		if strings.Contains(line, "[0 lua]") {
			continue
		}
		parts := strings.SplitN(line, "] ", 2)
		require.Len(t, parts, 2, "monitor: %q", line)
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
	require.EqualValues(t, 1, hellos, "wire: hello=%d clients=%d calls=%d", hellos, len(clients), calls)
	require.Len(t, clients, 1, "wire: hello=%d clients=%d calls=%d", hellos, len(clients), calls)
	require.EqualValues(t, 12, calls, "wire: hello=%d clients=%d calls=%d", hellos, len(clients), calls)
	require.EqualValues(t, 5, strings.Count(out.String(), "TABLE RECEIPT "), "mutation receipts:\n%s", &out)
	for _, line := range strings.Split(out.String(), "\n") {
		require.False(t, strings.Contains(line, " trips=") && !strings.Contains(line, " trips=1"), "per-verb trips grew across the session: %s", line)
	}
	tab, err := ntable.Read(ctx, admin, "jobs")
	require.NoError(t, err, "%v", err)
	require.EqualValues(t, 5, tab.Revision, "stored session state: %+v", tab)
	require.Equal(t, "--seat", tab.Rows[0].Label, "stored session state: %+v", tab)
	require.Equal(t, "$HOME $(touch file) `whoami` *", tab.Rows[0].Texts["note"], "stored session state: %+v", tab)
	records, err := admin.XRange(ctx, "table:jobs:changes", "-", "+").Result()
	require.NoError(t, err, "%v", err)
	require.Len(t, records, 5, "durable receipts: %d", len(records))
	for _, r := range records {
		require.Equal(t, "shell-test", r.Values["actor"], "session defaults lost: %v", r)
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
			require.EqualValues(t, 1, code, "%d %s %s", code, &out, &errout)
			require.Contains(t, errout.String(), "already has a place", "%d %s %s", code, &out, &errout)
			require.NotContains(t, out.String(), "TABLE RECEIPT", "%d %s %s", code, &out, &errout)
			c := redis.NewClient(&redis.Options{Addr: addr})
			defer c.Close()
			tab, err := ntable.Read(context.Background(), c, "jobs")
			require.NoError(t, err, "%v", err)
			want := 1
			if keep {
				want = 2
			}
			require.Equal(t, want, len(tab.Rows), "keep-going=%v rows=%d want %d", keep, len(tab.Rows), want)
			// A different address and a mid-session seat are rejected before any
			// connection switch. A later valid command still uses the first store.
			out.Reset()
			errout.Reset()
			script = "row add jobs forbidden --redis 127.0.0.1:1\nrow add jobs forbidden --seat elsewhere\nrow add jobs same --redis " + addr + "\n"
			code = (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--keep-going"}, &out, &errout)
			require.EqualValues(t, 2, code, "pin: %d %s %s", code, &out, &errout)
			require.Contains(t, errout.String(), "connection is fixed", "pin: %d %s %s", code, &out, &errout)
			tab, err = ntable.Read(context.Background(), c, "jobs")
			require.NoError(t, err, "%v", err)
			for _, r := range tab.Rows {
				require.NotEqual(t, "forbidden", r.Key, "%v", "a connection-changing line wrote")
			}
			require.Equal(t, "same", tab.Rows[len(tab.Rows)-1].Key, "same connection could not continue: %+v", tab.Rows)
		})
	}
}

func TestShellEpochAndMetadataDefaultsDoNotDrift(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, c.HSet(ctx, "generation", "n", "5").Err())
	script := `create jobs --columns ready --epoch-key generation
row add jobs stale --epoch 4
row add jobs override --actor alternate --receipt=false
row add jobs default
`
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--epoch", "5", "--actor", "session", "--keep-going"}, &out, &errout)
	require.EqualValues(t, 1, code, "%d %s %s", code, &out, &errout)
	require.Contains(t, errout.String(), "epoch", "%d %s %s", code, &out, &errout)
	require.EqualValues(t, 2, strings.Count(out.String(), "TABLE RECEIPT "), "per-command receipt override leaked: %s", &out)
	tab, err := ntable.Read(ctx, c, "jobs")
	require.NoError(t, err, "%v", err)
	require.EqualValues(t, 5, tab.Epoch, "epoch/session state: %+v", tab)
	require.EqualValues(t, 3, tab.Revision, "epoch/session state: %+v", tab)
	require.Len(t, tab.Rows, 2, "epoch/session state: %+v", tab)
	require.Equal(t, "override", tab.Rows[0].Key, "epoch/session state: %+v", tab)
	require.Equal(t, "default", tab.Rows[1].Key, "epoch/session state: %+v", tab)
	events, err := c.XRange(ctx, "table:jobs:changes", "-", "+").Result()
	require.NoError(t, err, "%v", err)
	for i, want := range []string{"session", "alternate", "session"} {
		require.Equal(t, want, events[i].Values["actor"], "event %d: %v", i, events[i])
		require.Equal(t, "5", events[i].Values["epoch"], "event %d: %v", i, events[i])
	}
}

func TestShellSyntaxErrorDoesNotPartiallyExecuteLine(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	script := "create t --columns ready\nrow add t bad; drop t\nrow add t 'unfinished\nrow add t good\n"
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr, "--keep-going"}, &out, &errout)
	require.EqualValues(t, 2, code, "%d %s %s", code, &out, &errout)
	require.Contains(t, errout.String(), "line 2", "%d %s %s", code, &out, &errout)
	require.Contains(t, errout.String(), "line 3", "%d %s %s", code, &out, &errout)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tab, err := ntable.Read(context.Background(), c, "t")
	require.NoError(t, err, "%v", err)
	require.Len(t, tab.Rows, 1, "bad input line wrote: %+v", tab)
	require.Equal(t, "good", tab.Rows[0].Key, "bad input line wrote: %+v", tab)
	require.EqualValues(t, 2, tab.Revision, "bad input line wrote: %+v", tab)
}

func TestDocumentedShellSessionRuns(t *testing.T) {
	t.Parallel()
	doc := readDoc(t, "nova-table/README.md")
	_, section, ok := strings.Cut(doc, "## Resident shell\n")
	require.True(t, ok, "%v", "resident shell guide missing")
	_, body, ok := strings.Cut(section, "<<'TABLE'\n")
	require.True(t, ok, "%v", "shell example missing")
	script, _, ok := strings.Cut(body, "\nTABLE\n")
	require.True(t, ok, "%v", "shell example has no terminator")
	addr := firstRunStore(t)
	var out, errout bytes.Buffer
	code := (&application{in: strings.NewReader(script)}).run([]string{"shell", "--redis", addr}, &out, &errout)
	require.EqualValues(t, 0, code, "documented shell: %d\n%s\n%s", code, &out, &errout)
	require.EqualValues(t, 0, errout.Len(), "documented shell: %d\n%s\n%s", code, &out, &errout)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tab, err := ntable.Read(context.Background(), c, "session-demo")
	require.NoError(t, err, "%v", err)
	require.EqualValues(t, 5, tab.Revision, "documented session result: %+v", tab)
	require.Len(t, tab.Rows, 1, "documented session result: %+v", tab)
	require.EqualValues(t, 2, tab.Rows[0].Cells[1].Count, "documented session result: %+v", tab)
	require.Equal(t, "Running both checks", tab.Rows[0].Texts["note"], "documented session result: %+v", tab)
}

// The first connection's store goes away (its sockets close and nothing
// listens at its address), reproducing a client whose every dial now fails.
// The failed create is not replayed; the next line that needs the store opens
// one fresh connection, once, at an address where the store answers, and the
// two later list calls reuse it. No sleep or race against a timer is needed.
func TestShellFreshDialAfterConnectionFailureWithoutReplay(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	gone := startRelay(t, addr, nil)
	first, err := openShellStore(gone.addr(), noEnv)
	require.NoError(t, err, "%v", err)
	gone.stop()
	back := startRelay(t, addr, nil)
	reopens := 0
	shared := sharedConnection(first, func() (*redisconn.Conn, error) {
		reopens++
		return openShellStore(back.addr(), noEnv)
	})
	defer func() { _ = shared.Conn.Close() }()
	var out, errs bytes.Buffer
	app := &application{shared: shared, addr: addr}
	// A failed create must not be replayed after the store recovers. Help needs
	// no connection; two later list calls must reuse the single fresh one.
	code := app.readCommands(strings.NewReader("create failed --columns ready\nversion\nlist\nlist\n"), &out, &errs, true, false)
	require.EqualValues(t, 2, code, "recovery: code=%d reopens=%d connections=%d out=%s err=%s", code, reopens, back.accepted.Load(), &out, &errs)
	require.EqualValues(t, 1, reopens, "recovery: code=%d reopens=%d connections=%d out=%s err=%s", code, reopens, back.accepted.Load(), &out, &errs)
	require.EqualValues(t, 1, back.accepted.Load(), "recovery: code=%d reopens=%d connections=%d out=%s err=%s", code, reopens, back.accepted.Load(), &out, &errs)
	require.EqualValues(t, 2, strings.Count(out.String(), "TABLE LIST tables=0 trips=1"), "recovery: code=%d reopens=%d connections=%d out=%s err=%s", code, reopens, back.accepted.Load(), &out, &errs)
	require.Contains(t, errs.String(), "line 1", "recovery: code=%d reopens=%d connections=%d out=%s err=%s", code, reopens, back.accepted.Load(), &out, &errs)
}

// The relay drops one reply only after the store produced it. This is an
// uncertain write outcome, not a dial failure: retrying would append a second
// receipt.
func TestShellLostWriteReplyIsNeverReplayed(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	{
		code, out, err := runTable(at(addr, "create", "jobs", "--columns", "ready")...)
		require.EqualValues(t, 0, code, "setup %d %s %s", code, out, err)
	}
	var lost atomic.Bool
	r := startRelay(t, addr, &lost)
	shared := sharedConnection(nil, func() (*redisconn.Conn, error) { return openShellStore(r.addr(), noEnv) })
	defer func() { _ = shared.Conn.Close() }()
	var out, errs bytes.Buffer
	code := (&application{shared: shared, addr: r.addr()}).readCommands(strings.NewReader("row add jobs committed\nlist\n"), &out, &errs, true, false)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	defer admin.Close()
	tab, err := ntable.Read(context.Background(), admin, "jobs")
	require.NoError(t, err, "%v", err)
	events, err := admin.XLen(context.Background(), ntable.ChangesKey("jobs")).Result()
	require.NoError(t, err, "%v", err)
	// One remedy, show: the write may have committed, so the line sends the
	// reader to look, never to start the store and write again.
	const wantErr = "ROW-ADD REFUSED: table \"jobs\" row \"committed\": ns_table_row_add: EOF; run: nova-table show 'jobs'\nnova-table shell: line 1 failed (exit 2)\n"
	require.EqualValues(t, 2, code, "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	require.True(t, lost.Load(), "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	require.EqualValues(t, 2, tab.Revision, "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	require.EqualValues(t, 2, events, "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	require.Len(t, tab.Rows, 1, "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	require.Contains(t, out.String(), "TABLE LIST tables=1 trips=1", "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
	require.Equal(t, wantErr, errs.String(), "lost reply: code=%d lost=%v revision=%d events=%d rows=%d out=%s err=%s", code, lost.Load(), tab.Revision, events, len(tab.Rows), &out, &errs)
}
