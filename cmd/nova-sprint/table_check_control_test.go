package main

import (
	"bytes"
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// startThrowawayRedis starts a real redis-server on a free loopback port and
// returns its address. The controls need real Redis for FCALL_RO and the
// fixture keyspace; if a server cannot run the test skips rather than lies.
func startThrowawayRedis(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not available")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback port: %v", err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + port
	ln.Close()
	dir := t.TempDir()
	cmd := exec.Command(bin,
		"--port", port,
		"--bind", "127.0.0.1",
		"--save", "",
		"--appendonly", "no",
		"--dir", dir,
		"--logfile", dir+"/redis.log",
	)
	if err := cmd.Start(); err != nil {
		t.Skipf("redis-server did not start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := client.Ping(ctx).Err(); err == nil {
			return addr
		}
		if time.Now().After(deadline) {
			t.Skipf("redis-server at %s never became ready", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func seed(t *testing.T, addr string, cmds [][]string) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	for _, c := range cmds {
		args := make([]any, len(c))
		for i, a := range c {
			args[i] = a
		}
		if err := client.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", c, err)
		}
	}
}

// TestControl21TableCheckFixture renders the fixture keyspace of 6.8 and
// asserts the exact output and the sidecar refusal: because it holds a
// friend:<f>:width written by a second writer, table --check must fail with
// two writers.
func loadTableFunction(t *testing.T, addr string) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
}

func TestTableCLICheckFixture(t *testing.T) {
	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	seed(t, addr, table.DefectFixture())
	var stdout, stderr bytes.Buffer
	code := run([]string{"table", "--check", "--redis", addr}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("table --check exit %d, want 0; stderr %s", code, stderr.String())
	}
	if got, want := stdout.String(), table.DefectGolden(); got != want {
		t.Fatalf("table --check output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
	seed(t, addr, [][]string{{"SET", "friend:fran:width", "9"}})
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"table", "--check", "--redis", addr}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "two writers: friend:fran:width") {
		t.Fatalf("sidecar was accepted: exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

// TestControl24BenchCellsFromCardIndexes proves a bench's queue and done come
// only from s:<S>:bench:<b>:queue ZCARD and ...:ended SCARD: three queue cards
// and two ended cards render 3 and 2, and stray open:<f>/done:<f> keys that a
// buggy reader might use do not leak into the bench cells.
func TestTableCLIBenchCellsFromCardIndexes(t *testing.T) {
	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	seed(t, addr, [][]string{
		{"SADD", "sprints", "s1"},
		{"HSET", "s:s1", "status", "open"},
		{"SADD", "benches", "b1"},
		{"HSET", "bench:b1:desired", "slots", "4"},
		{"HSET", "bench:b1:beat", "at", "1"},
		{"ZADD", "s:s1:bench:b1:queue", "0", "c1", "0", "c2", "0", "c3"},
		{"SADD", "s:s1:bench:b1:ended", "e1", "e2"},
		// A friend that is not registered, with task indexes that must be
		// invisible to the bench cell: five open and seven done.
		{"ZADD", "s:s1:open:ghost", "0", "x1", "0", "x2", "0", "x3", "0", "x4", "0", "x5"},
		{"SADD", "s:s1:done:ghost", "y1", "y2", "y3", "y4", "y5", "y6", "y7"},
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"table", "--redis", addr}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("table exit %d; stderr %s", code, stderr.String())
	}
	want := "bench:b1 | up | 4 | 0 | 0 | 0 | 0 | 3 | 2 | \n"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("bench cell not from card indexes\ngot:\n%s\nwant line:\n%s", stdout.String(), want)
	}
	for _, leaked := range []string{" | 5 | 7 |", " | 5 |", " | 7 |"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("bench cell leaked friend task indexes: %q in\n%s", leaked, stdout.String())
		}
	}
}
