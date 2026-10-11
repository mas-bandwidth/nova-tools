//go:build functional

package store

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// countedConn counts writes and reads on one connection so a batch shows as
// one exchange: a write after a reply has started is a second round trip.
type countedConn struct {
	net.Conn
	writes      *atomic.Int64
	reads       *atomic.Int64
	interleaved *atomic.Int64
}

func (c countedConn) Write(p []byte) (int, error) {
	if c.reads.Load() != 0 {
		c.interleaved.Add(1)
	}
	c.writes.Add(1)
	return c.Conn.Write(p)
}

func (c countedConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(p)
}

// TestPipelineThousandReadsOneRoundTrip pins PipelineHMGet over the live
// constructor (openWith, which Open calls). The throwaway store has no ACL
// user, so the dial drops whatever seat the environment names and speaks as
// the default user. A thousand independent reads are one exchange.
func TestPipelineThousandReadsOneRoundTrip(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	var writes atomic.Int64
	var readCalls atomic.Int64
	var interleaved atomic.Int64
	ctx := context.Background()
	s, err := openWith(ctx, addr, &seatcred.Selection{}, func(o *redis.Options) {
		o.Username, o.Password = "", ""
		o.PoolSize = 1
		o.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return countedConn{Conn: conn, writes: &writes, reads: &readCalls, interleaved: &interleaved}, nil
		}
	})
	require.NoError(t, err)
	defer func() { _ = s.Close() }() // ignored: a test fixture's cleanup; the test's own assertions are the report
	client := s.Client()
	require.NoError(t, client.Ping(ctx).Err())
	seed := client.Pipeline()
	reads := make([]HashRead, 1000)
	for i := range reads {
		key := fmt.Sprintf("task:%d", i)
		seed.HSet(ctx, key, "state", "open", "owner", "stella")
		reads[i] = HashRead{Key: key, Fields: []string{"state", "owner"}}
	}
	_, err = seed.Exec(ctx)
	require.NoError(t, err)
	writes.Store(0)
	readCalls.Store(0)
	interleaved.Store(0)
	got, err := s.PipelineHMGet(ctx, reads)
	require.NoError(t, err)
	require.NotZero(t, writes.Load(), "1000 HMGETs used no write")
	require.NotZero(t, readCalls.Load(), "1000 HMGETs used no read")
	require.Zero(t, interleaved.Load(), "1000 HMGETs wrote after a reply; want one pipelined exchange (%d writes, %d reads)", writes.Load(), readCalls.Load())
	require.Len(t, got, len(reads))
	for i, values := range got {
		require.Len(t, values, 2, "reply %d = %v", i, values)
		require.Equal(t, "open", values[0], "reply %d = %v", i, values)
		require.Equal(t, "stella", values[1], "reply %d = %v", i, values)
	}
}
