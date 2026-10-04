package store

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// helloAccepted is the reply of a store that accepted HELLO 3. Only a store
// that accepts it draws go-redis's post-HELLO commands, which is why
// testutil.CommandCounter (it refuses HELLO) never saw them.
const helloAccepted = "%2\r\n$5\r\nproto\r\n:3\r\n$4\r\nmode\r\n$10\r\nstandalone\r\n"

// TestConnectIsHelloAlone (Glenn 2026-09-27: "You always need to batch
// redis"): the connect costs one round trip, HELLO, and nothing after it:
// no CLIENT SETINFO and no CLIENT MAINT_NOTIFICATIONS, which go-redis sends
// by default after an accepted HELLO 3 and Redis 8.10.2 refuses. The store
// speaks over net.Pipe, so no socket is opened and nothing waits on a clock.
func TestConnectIsHelloAlone(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var seen []string
	dial := func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			r := bufio.NewReader(server)
			for {
				cmd, err := readCommand(r)
				if err != nil {
					return
				}
				mu.Lock()
				// The command and, for CLIENT, its subcommand: never a
				// login's arguments, so no password reaches a failure line.
				name := strings.ToLower(cmd[0])
				if name == "client" && len(cmd) > 1 {
					name += " " + strings.ToLower(cmd[1])
				}
				seen = append(seen, name)
				mu.Unlock()
				reply := "+OK\r\n"
				switch strings.ToUpper(cmd[0]) {
				case "HELLO":
					reply = helloAccepted
				case "GET":
					reply = "_\r\n"
				}
				if _, err := io.WriteString(server, reply); err != nil {
					return
				}
			}
		}()
		return client, nil
	}
	s, err := openWith(context.Background(), "store.test:6379", &seatcred.Selection{}, func(o *redis.Options) {
		o.PoolSize, o.Dialer = 1, dial
	})
	if err != nil {
		require.NoError(t, err, err)
	}
	defer s.Close()
	if err := s.Client().Get(context.Background(), "k").Err(); err != redis.Nil {
		require.True(t, err == redis.Nil, "get: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"hello", "get"}; !reflect.DeepEqual(seen, want) {
		require.Equal(t, want, seen, "the store received %q; want the connect to be HELLO alone, %q", seen, want)
	}
}

// readCommand reads one RESP array of bulk strings.
func readCommand(r *bufio.Reader) ([]string, error) {
	var n int
	if _, err := fmt.Fscanf(r, "*%d\r\n", &n); err != nil {
		return nil, err
	}
	cmd := make([]string, n)
	for i := range cmd {
		var l int
		if _, err := fmt.Fscanf(r, "$%d\r\n", &l); err != nil {
			return nil, err
		}
		b := make([]byte, l+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		cmd[i] = string(b[:l])
	}
	return cmd, nil
}
