package tset

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func redisWireSteps(count int) []Step {
	steps := make([]Step, count)
	for i := range steps {
		op := fmt.Sprintf("wire-op-%d", i)
		intent := fmt.Sprintf(`{"part":%d}`, i)
		steps[i] = Step{Epoch: "0", Space: "wire", Op: &op, Intent: &intent, Entries: []Entry{}}
	}
	return steps
}

type redisCountingConn struct {
	net.Conn
	writes *atomic.Int32
}

func (conn *redisCountingConn) Write(data []byte) (int, error) {
	conn.writes.Add(1)
	return conn.Conn.Write(data)
}

func redisPipeClient(dial func(context.Context, string, string) (net.Conn, error)) (*redis.Client, *int, *atomic.Int32) {
	dials := 0
	writes := &atomic.Int32{}
	client := redis.NewClient(&redis.Options{
		Addr:            "net-pipe",
		Protocol:        2,
		MaxRetries:      -1,
		DisableIdentity: true,
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dials++
			conn, err := dial(ctx, "", "")
			if err != nil {
				return nil, err
			}
			return &redisCountingConn{Conn: conn, writes: writes}, nil
		},
	})
	return client, &dials, writes
}

func readRedisCommand(reader *bufio.Reader) ([][]byte, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 4 || line[0] != '*' {
		return nil, fmt.Errorf("expected RESP array header, got %q", line)
	}
	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(line[1:]), "%d", &count); err != nil || count < 0 {
		return nil, fmt.Errorf("invalid RESP array header %q", line)
	}
	args := make([][]byte, count)
	for i := range args {
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) < 4 || line[0] != '$' {
			return nil, fmt.Errorf("expected RESP bulk header, got %q", line)
		}
		var size int
		if _, err := fmt.Sscanf(strings.TrimSpace(line[1:]), "%d", &size); err != nil || size < 0 {
			return nil, fmt.Errorf("invalid RESP bulk header %q", line)
		}
		args[i] = make([]byte, size+2)
		if _, err := io.ReadFull(reader, args[i]); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(args[i][size:], []byte("\r\n")) {
			return nil, fmt.Errorf("bulk argument %d missing CRLF", i)
		}
		args[i] = args[i][:size]
	}
	return args, nil
}

func writeRedisBulk(conn net.Conn, value string) error {
	_, err := fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(value), value)
	return err
}

// go-redis v9.22 sends HELLO 2 even when Protocol is configured as RESP2.
// Returning the normal unsupported-HELLO Redis error exercises its documented
// RESP2 fallback. No AUTH, SELECT, or CLIENT SETINFO follows with the options
// below (no credentials, DB 0, and DisableIdentity=true).
func redisPipeHandshake(server net.Conn, reader *bufio.Reader) error {
	cmd, err := readRedisCommand(reader)
	if err != nil {
		return fmt.Errorf("read connection HELLO: %w", err)
	}
	if len(cmd) != 2 || !strings.EqualFold(string(cmd[0]), "hello") || string(cmd[1]) != "2" {
		return fmt.Errorf("unexpected connection setup command: %q", cmd)
	}
	if _, err := io.WriteString(server, "-ERR unknown command 'HELLO'\r\n"); err != nil {
		return fmt.Errorf("reply to HELLO: %w", err)
	}
	return nil
}

func TestStepsSingleFlushAlignedResults(t *testing.T) {
	t.Parallel()

	steps := redisWireSteps(3)
	server, clientConn := net.Pipe()
	client, dials, counted := redisPipeClient(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	defer client.Close()
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() {
		defer close(serverErr)
		_ = server.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(server)
		if err := redisPipeHandshake(server, reader); err != nil {
			serverErr <- err
			return
		}
		commands := make([][][]byte, len(steps))
		for i := range commands {
			cmd, err := readRedisCommand(reader)
			if err != nil {
				serverErr <- fmt.Errorf("read command %d: %w", i, err)
				return
			}
			commands[i] = cmd
		}
		for i, cmd := range commands {
			if len(cmd) != 5 || !strings.EqualFold(string(cmd[0]), "fcall") || string(cmd[1]) != "ns_tset_step" || string(cmd[2]) != "0" || string(cmd[3]) != Version {
				serverErr <- fmt.Errorf("command %d has unexpected FCALL argv: %q", i, cmd)
				return
			}
			want, err := EncodeStep(steps[i])
			if err != nil || !reflect.DeepEqual(cmd[4], want) {
				serverErr <- fmt.Errorf("command %d request bytes differ: got %q want %q (encode %v)", i, cmd[4], want, err)
				return
			}
		}
		for _, response := range []string{okWireReply("zero"), refusedWireReply("REVISION"), okWireReply("two")} {
			if err := writeRedisBulk(server, response); err != nil {
				serverErr <- err
				return
			}
		}
	}()

	results, err := newRedisWithClient(client).Steps(context.Background(), steps)
	if err != nil {
		t.Fatalf("Steps returned batch error: %v", err)
	}
	if len(results) != len(steps) {
		t.Fatalf("got %d result slots, want %d", len(results), len(steps))
	}
	if results[0].Err != nil || results[0].Reply.Result != "zero" {
		t.Errorf("slot 0 = (%+v, %v), want success zero", results[0].Reply, results[0].Err)
	}
	var refusal *Refusal
	if !errors.As(results[1].Err, &refusal) || refusal.Code != "REVISION" {
		t.Errorf("slot 1 error = %v, want REVISION refusal", results[1].Err)
	}
	if results[2].Err != nil || results[2].Reply.Result != "two" {
		t.Errorf("slot 2 = (%+v, %v), want success two", results[2].Reply, results[2].Err)
	}
	if *dials != 1 {
		t.Errorf("pipeline used %d connections, want one flush on one connection", *dials)
	}
	if got := counted.Load(); got != 2 { // one HELLO setup write plus one measured pipeline flush
		t.Errorf("connection performed %d socket writes, want HELLO setup plus one pipeline flush", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestStepsLostReplyRecovery(t *testing.T) {
	t.Parallel()

	steps := redisWireSteps(2)
	server, clientConn := net.Pipe()
	client, dials, _ := redisPipeClient(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	defer client.Close()
	serverErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(server)
		if err := redisPipeHandshake(server, reader); err != nil {
			serverErr <- err
			_ = server.Close()
			return
		}
		for i := range steps {
			cmd, err := readRedisCommand(reader)
			if err != nil {
				serverErr <- fmt.Errorf("read command %d: %w", i, err)
				_ = server.Close()
				return
			}
			if len(cmd) != 5 || string(cmd[1]) != "ns_tset_step" || string(cmd[2]) != "0" {
				serverErr <- fmt.Errorf("unexpected command %d: %q", i, cmd)
				_ = server.Close()
				return
			}
		}
		_ = server.SetDeadline(time.Now().Add(5 * time.Second))
		if err := writeRedisBulk(server, okWireReply("applied")); err != nil {
			serverErr <- fmt.Errorf("write first reply: %w", err)
			_ = server.Close()
			return
		}
		// The first slot has a complete reply. The second response is lost.
		serverErr <- nil
		_ = server.Close()
	}()

	results, err := newRedisWithClient(client).Steps(context.Background(), steps)
	if err != nil {
		t.Fatalf("dispatched Steps returned global error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d result slots, want 2", len(results))
	}
	if results[0].Err != nil || results[0].Reply.Result != "applied" {
		t.Fatalf("complete first reply was not retained: reply=%+v err=%v", results[0].Reply, results[0].Err)
	}
	if !errors.Is(results[1].Err, ErrOutcomeUnknown) {
		t.Fatalf("unanswered second slot error = %v, want OUTCOMEUNKNOWN", results[1].Err)
	}
	var unknown *OutcomeUnknownError
	if !errors.As(results[1].Err, &unknown) {
		t.Fatalf("second slot error has type %T, want *OutcomeUnknownError", results[1].Err)
	}
	for i, step := range steps {
		want, encodeErr := EncodeStep(step)
		if encodeErr != nil {
			t.Fatalf("EncodeStep(%d): %v", i, encodeErr)
		}
		if !reflect.DeepEqual(results[i].RawRequest, want) {
			t.Errorf("slot %d RawRequest = %q, want exact dispatched bytes %q", i, results[i].RawRequest, want)
		}
	}
	if !reflect.DeepEqual(unknown.RawRequest, results[1].RawRequest) {
		t.Errorf("unknown error request %q differs from slot bytes %q", unknown.RawRequest, results[1].RawRequest)
	}
	if *dials != 1 {
		t.Errorf("lost-reply batch dialed %d times, want one attempt and no retry", *dials)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestStepsNilReplyDoesNotBorrowSiblingClassification(t *testing.T) {
	t.Parallel()

	steps := redisWireSteps(2)
	server, clientConn := net.Pipe()
	client, _, _ := redisPipeClient(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	defer client.Close()
	serverErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(server)
		if err := redisPipeHandshake(server, reader); err != nil {
			serverErr <- err
			_ = server.Close()
			return
		}
		for i := range steps {
			if _, err := readRedisCommand(reader); err != nil {
				serverErr <- fmt.Errorf("read command %d: %w", i, err)
				_ = server.Close()
				return
			}
		}
		_ = server.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(server, "-ERR Function not found\r\n$-1\r\n"); err != nil {
			serverErr <- err
			_ = server.Close()
			return
		}
		serverErr <- nil
	}()

	results, err := newRedisWithClient(client).Steps(context.Background(), steps)
	if err != nil || len(results) != 2 {
		t.Fatalf("Steps returned (%d slots, %v), want two aligned slots", len(results), err)
	}
	var known *ClientError
	if !errors.As(results[0].Err, &known) || known.Code != "FUNCTIONMISSING" {
		t.Errorf("first slot error = %v, want known FUNCTIONMISSING", results[0].Err)
	}
	if !errors.Is(results[1].Err, ErrOutcomeUnknown) {
		t.Errorf("nil second reply inherited sibling classification: got %v, want OUTCOMEUNKNOWN", results[1].Err)
	}
	var unknown *OutcomeUnknownError
	if errors.As(results[1].Err, &unknown) && !reflect.DeepEqual(unknown.RawRequest, results[1].RawRequest) {
		t.Errorf("nil slot retained request %q, slot has %q", unknown.RawRequest, results[1].RawRequest)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
