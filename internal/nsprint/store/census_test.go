package store_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// flushConn counts request flushes on the one connection: a write that is the
// first write, or that follows a read, starts a new flush. A pipeline that the
// buffered writer splits into several writes before any reply is read is still
// one flush, one round trip; a second flush means a second exchange.
type flushConn struct {
	net.Conn
	mu        *sync.Mutex
	flushes   *int
	lastWrite *bool
}

func (c flushConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if !*c.lastWrite {
		*c.flushes++
	}
	*c.lastWrite = true
	c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c flushConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	*c.lastWrite = false
	c.mu.Unlock()
	return c.Conn.Read(p)
}

type flushCounter struct {
	mu        sync.Mutex
	flushes   int
	lastWrite bool
}

func (f *flushCounter) reset() {
	f.mu.Lock()
	f.flushes, f.lastWrite = 0, false
	f.mu.Unlock()
}

func (f *flushCounter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushes
}

func countedClient(t *testing.T, addr string) (*redis.Client, *flushCounter) {
	t.Helper()
	fc := &flushCounter{}
	client := redis.NewClient(&redis.Options{
		Addr: addr, PoolSize: 1, MinIdleConns: 0,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return flushConn{Conn: conn, mu: &fc.mu, flushes: &fc.flushes, lastWrite: &fc.lastWrite}, nil
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return client, fc
}

const censusBenches = 1042

func seedBenches(t *testing.T, client *redis.Client) []string {
	t.Helper()
	ctx := context.Background()
	seed := client.Pipeline()
	keys := make([]string, censusBenches)
	for i := range keys {
		name := fmt.Sprintf("b%04d", i)
		keys[i] = "bench:" + name
		seed.SAdd(ctx, "benches", name)
		seed.HSet(ctx, keys[i], "host", "h"+name, "at", fmt.Sprint(1790000000+i))
	}
	if _, err := seed.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return keys
}

func censusLines(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], "CENSUS ") {
		t.Fatalf("census output has no CENSUS summary line: %q", out)
	}
	return lines
}

// TestCensusOnePipeline is the DONE-WHEN of nova-tools #3037: 1,042 bench
// hashes in a throwaway Redis, read by `census --set benches --fields host,at`
// and by `census --keys-from <file>`, every row back with exactly one pipeline
// flush for the reads; a key missing in Redis prints MISSING <key>, never a
// blank row or the value an earlier census read.
func TestCensusOnePipeline(t *testing.T) {
	addr := startRedis(t)
	client, flushes := countedClient(t, addr)
	keys := seedBenches(t, client)
	st := store.New(client)
	ctx := context.Background()
	fields := []string{"host", "at"}

	wantRow := func(i int) string {
		return fmt.Sprintf("bench:b%04d host=hb%04d at=%d", i, i, 1790000000+i)
	}
	checkAll := func(t *testing.T, out string) {
		t.Helper()
		lines := censusLines(t, out)
		rows := lines[:len(lines)-1]
		if len(rows) != censusBenches {
			t.Fatalf("census printed %d rows; want %d", len(rows), censusBenches)
		}
		for i, row := range rows {
			if row != wantRow(i) {
				t.Fatalf("row %d = %q; want %q", i, row, wantRow(i))
			}
		}
		if want := fmt.Sprintf("CENSUS keys=%d present=%d missing=0 err=0", censusBenches, censusBenches); lines[len(lines)-1] != want {
			t.Fatalf("summary = %q; want %q", lines[len(lines)-1], want)
		}
	}

	t.Run("flush counter control", func(t *testing.T) {
		// The counter must see unpipelined reads: three separate HGETs are
		// three flushes, or a count of 1 below would prove nothing.
		flushes.reset()
		for i := 0; i < 3; i++ {
			if err := client.HGet(ctx, keys[i], "host").Err(); err != nil {
				t.Fatal(err)
			}
		}
		if got := flushes.count(); got != 3 {
			t.Fatalf("three separate HGETs counted %d flushes; want 3", got)
		}
	})

	t.Run("set benches", func(t *testing.T) {
		var out bytes.Buffer
		flushes.reset()
		start := time.Now()
		if _, err := store.RunCensus(ctx, st, store.CensusRequest{Set: "benches", Fields: fields}, &out); err != nil {
			t.Fatal(err)
		}
		t.Logf("census --set benches: %d rows in %s", censusBenches, time.Since(start))
		// One flush resolves the set (SMEMBERS), exactly one flush carries every read.
		if got := flushes.count(); got != 2 {
			t.Fatalf("census --set benches used %d flushes; want 2 (SMEMBERS, then one pipeline for all %d reads)", got, censusBenches)
		}
		checkAll(t, out.String())
	})

	t.Run("keys-from file", func(t *testing.T) {
		list := "# bench hashes, one key per line\n" + strings.Join(keys, "\n") + "\n\n"
		var out bytes.Buffer
		flushes.reset()
		start := time.Now()
		if _, err := store.RunCensus(ctx, st, store.CensusRequest{KeysFrom: strings.NewReader(list), Fields: fields}, &out); err != nil {
			t.Fatal(err)
		}
		t.Logf("census --keys-from: %d rows in %s", censusBenches, time.Since(start))
		if got := flushes.count(); got != 1 {
			t.Fatalf("census --keys-from used %d flushes; want exactly 1 pipeline for all %d reads", got, censusBenches)
		}
		checkAll(t, out.String())
	})

	t.Run("missing key prints MISSING, never a blank or a carried value", func(t *testing.T) {
		if err := client.SAdd(ctx, "benches", "zz-gone").Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Del(ctx, "bench:b0007").Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.HDel(ctx, "bench:b0008", "at").Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.HSet(ctx, "bench:b0009", "host", "").Err(); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		flushes.reset()
		sum, err := store.RunCensus(ctx, st, store.CensusRequest{Set: "benches", Fields: fields}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if got := flushes.count(); got != 2 {
			t.Fatalf("census used %d flushes; want 2", got)
		}
		lines := censusLines(t, out.String())
		rows := lines[:len(lines)-1]
		if len(rows) != censusBenches+1 {
			t.Fatalf("census printed %d rows; want %d (one per member, missing ones included)", len(rows), censusBenches+1)
		}
		byKey := map[string]string{}
		for _, row := range rows {
			if strings.TrimSpace(row) == "" {
				t.Fatalf("census printed a blank row")
			}
			key := strings.Fields(row)[0]
			if key == "MISSING" {
				key = strings.Fields(row)[1]
			}
			byKey[key] = row
		}
		for key, want := range map[string]string{
			"bench:b0007":   "MISSING bench:b0007",
			"bench:zz-gone": "MISSING bench:zz-gone",
			"bench:b0008":   "bench:b0008 host=hb0008 at=MISSING",
			"bench:b0009":   `bench:b0009 host="" at=1790000009`,
			"bench:b0010":   wantRow(10),
		} {
			if byKey[key] != want {
				t.Fatalf("row for %s = %q; want %q", key, byKey[key], want)
			}
		}
		if strings.Contains(out.String(), "hb0007") {
			t.Fatalf("census carried a deleted key's value from an earlier read: %q", byKey["bench:b0007"])
		}
		if want := fmt.Sprintf("CENSUS keys=%d present=%d missing=2 err=0", censusBenches+1, censusBenches-1); lines[len(lines)-1] != want {
			t.Fatalf("summary = %q; want %q", lines[len(lines)-1], want)
		}
		if sum.Keys != censusBenches+1 || sum.Missing != 2 || sum.Present != censusBenches-1 {
			t.Fatalf("summary struct = %+v", sum)
		}
	})

	t.Run("a key that is not a hash is an ERR row", func(t *testing.T) {
		if err := client.Set(ctx, "bench:b0011", "scalar", 0).Err(); err != nil {
			t.Fatal(err)
		}
		list := "bench:b0010\nbench:b0011\nbench:b0012\n"
		var out bytes.Buffer
		sum, err := store.RunCensus(ctx, st, store.CensusRequest{KeysFrom: strings.NewReader(list), Fields: fields}, &out)
		if err != nil {
			t.Fatal(err)
		}
		lines := censusLines(t, out.String())
		if lines[0] != wantRow(10) || !strings.HasPrefix(lines[1], "ERR bench:b0011 WRONGTYPE") || lines[2] != wantRow(12) {
			t.Fatalf("rows = %q", lines)
		}
		if sum.Err != 1 || sum.Present != 2 {
			t.Fatalf("summary = %+v", sum)
		}
	})
}

func TestCensusRefusals(t *testing.T) {
	ctx := context.Background()
	st := store.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	defer st.Close()
	for name, req := range map[string]store.CensusRequest{
		"no source":    {Fields: []string{"host"}},
		"two sources":  {Set: "benches", KeysFrom: strings.NewReader("bench:a\n"), Fields: []string{"host"}},
		"no fields":    {Set: "benches"},
		"blank field":  {Set: "benches", Fields: []string{"host", ""}},
		"unknown set":  {Set: "everything", Fields: []string{"host"}},
		"sprint shape": {Set: "sprint:only-name", Fields: []string{"state"}},
	} {
		var out bytes.Buffer
		if _, err := store.RunCensus(ctx, st, req, &out); err == nil {
			t.Errorf("%s: census ran; want a refusal", name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: refusal printed rows %q", name, out.String())
		}
	}
}

func TestCensusSetKeys(t *testing.T) {
	for set, want := range map[string][2]string{
		"benches":            {"benches", "bench:"},
		"friends":            {"friends", "friend:"},
		"sprint:s1:working":  {"s:s1:idx:task:working", "task:"},
		"sprint:s-2:claimed": {"s:s-2:idx:task:claimed", "task:"},
	} {
		index, prefix, err := store.CensusSet(set)
		if err != nil || index != want[0] || prefix != want[1] {
			t.Errorf("CensusSet(%q) = %q, %q, %v; want %q, %q", set, index, prefix, err, want[0], want[1])
		}
	}
}
