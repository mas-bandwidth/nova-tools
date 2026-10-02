//go:build functional

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReceiptCommittedWriteWithLostReplyIsReportedUnconfirmed is Stella's
// probe on #4495 (2026-09-27), promoted. A proxy between the verb and a
// throwaway redis-server commits every XADD to the real store and then drops
// the connection without a reply: the row IS on ev:github and the writer
// cannot know it. The verb must exit 1 with an empty stdout and say the write
// could not be confirmed, and must not claim that no receipt is on ev:github,
// because the store holds one (or several: the client's retries each commit a
// row, and repeat receipts are accepted wake hints). HELLO and CLIENT are
// answered explicitly, any other command fails the test, so nothing but the
// XADD ever reaches the store.
func TestReceiptCommittedWriteWithLostReplyIsReportedUnconfirmed(t *testing.T) {
	t.Parallel()
	// The dial takes the process's seat (seatcred.Process), as the sibling
	// TestReceiptVerbWritesTheRowAndARefusedWriteIsExitOne does; a seatless
	// environment connects with HELLO alone, and a command the proxy does not
	// expect fails the test by name.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend := redis.NewClient(&redis.Options{Addr: testredis.Start(t)})
	defer backend.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var proxyErr error
	fail := func(err error) {
		mu.Lock()
		if proxyErr == nil {
			proxyErr = err
		}
		mu.Unlock()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(30 * time.Second))
				rd := bufio.NewReader(c)
				for {
					args, err := readRESPCommand(rd)
					if err != nil {
						if err != io.EOF {
							fail(err)
						}
						return
					}
					switch strings.ToLower(args[0]) {
					case "hello":
						_, err = io.WriteString(c, "%0\r\n")
					case "client":
						// Refuse optional client features, as a Redis without them would.
						_, err = io.WriteString(c, "-ERR unknown subcommand\r\n")
					case "xadd":
						vals := make([]interface{}, len(args))
						for i, s := range args {
							vals[i] = s
						}
						id, e := backend.Do(ctx, vals...).Text()
						if e != nil || id == "" {
							fail(fmt.Errorf("XADD not committed: id=%q error=%v", id, e))
						}
						// The store has committed the row; the reply never reaches the writer.
						return
					default:
						fail(fmt.Errorf("unexpected command through the proxy %q", args[0]))
						return
					}
					if err != nil {
						fail(err)
						return
					}
				}
			}()
		}
	}()
	args := receiptArgs()
	args[3] = ln.Addr().String()
	var out, errOut bytes.Buffer
	code := cmdReceipt(ctx, args[1:], &out, &errOut, noEnv)
	_ = ln.Close()
	wg.Wait()
	require.NoError(t, proxyErr)
	n, err := backend.XLen(ctx, "ev:github").Result()
	require.NoError(t, err)
	t.Logf("exit=%d committed_rows=%d stdout=%q stderr=%q", code, n, out.String(), errOut.String())
	require.GreaterOrEqual(t, n, int64(1), "the probe did not reach the committed-but-unconfirmed write: rows=%d", n)
	assert.Equal(t, 1, code, "an unconfirmed write must be exit 1 with nothing on stdout: code=%d stdout=%q", code, out.String())
	assert.Equal(t, 0, out.Len(), "an unconfirmed write must be exit 1 with nothing on stdout: code=%d stdout=%q", code, out.String())
	assert.NotContains(t, errOut.String(), "no receipt is on ev:github", "claims no receipt although the store holds %d committed row(s): %q", n, errOut.String())
	assert.Contains(t, errOut.String(), "receipt write could not be confirmed", "missing the unconfirmed-write sentence: %q", errOut.String())
}

// readRESPCommand reads one RESP array of bulk strings, a client command.
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || line[0] != '*' {
		return nil, fmt.Errorf("expected an array, got %q", line)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || n < 1 || n > 100 {
		return nil, fmt.Errorf("bad argument count %q", line)
	}
	args := make([]string, n)
	for i := range args {
		head, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(head) < 3 || head[0] != '$' {
			return nil, fmt.Errorf("expected a bulk string, got %q", head)
		}
		size, err := strconv.Atoi(strings.TrimSpace(head[1:]))
		if err != nil || size < 0 || size > 100000 {
			return nil, fmt.Errorf("bad bulk length %q", head)
		}
		b := make([]byte, size+2)
		if _, err = io.ReadFull(r, b); err != nil {
			return nil, err
		}
		if string(b[size:]) != "\r\n" {
			return nil, fmt.Errorf("bad bulk terminator")
		}
		args[i] = string(b[:size])
	}
	return args, nil
}
