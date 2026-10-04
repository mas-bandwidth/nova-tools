//go:build functional

package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"strings"
	"testing"
	"time"
)

// TestTableVerbsOneExchange records the real Redis command stream. Every
// command below must send one application command after connection setup,
// including the first read and a read after a binding change.
func TestTableVerbsOneExchange(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, admin.Ping(ctx).Err())
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	require.NoError(t, err, "%v", err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(30*time.Second)))
	{
		_, err := fmt.Fprint(conn, "*1\r\n$7\r\nMONITOR\r\n")
		require.NoError(t, err, "%v", err)
	}
	reader := bufio.NewReader(conn)
	{
		line, err := reader.ReadString('\n')
		require.NoError(t, err, "MONITOR ready: %q %v", line, err)
		require.Equal(t, "+OK\r\n", line, "MONITOR ready: %q %v", line, err)
	}
	steps := [][]string{
		{"create", "triptable", "--columns", "ready,working"},
		{"row", "add", "triptable", "first"},
		{"cell", "add", "triptable", "first", "ready", "job", "--score", "7"},
		{"cell", "members", "triptable", "first", "ready"},
		{"cell", "move", "triptable", "first", "ready", "working", "job"},
		{"cell", "remove", "triptable", "first", "working", "job"},
		{"member", "create", "triptable", "unplaced", "--receipt"}, {"check", "triptable"}, {"show", "triptable", "--at-epoch", "0"}, {"show", "triptable"}, {"render", "triptable"}, {"watch", "triptable", "--once"}, {"list"},
		{"row", "add", "triptable", "view", "--owner", "nova-sprint task move", "ready=external:other:ready"},
		{"render", "triptable"}, {"watch", "triptable", "--once"},
		{"row", "del", "triptable", "view"}, {"clear", "triptable"}, {"drop", "triptable"}, {"drop", "triptable", "--definition"},
	}
	for i, args := range steps {
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: exit %d: %s %s", args, code, out, errout)
		marker := fmt.Sprintf("table-probe-%d", i)
		require.NoError(t, admin.Echo(ctx, marker).Err())
		var commands []string
		for {
			line, err := reader.ReadString('\n')
			require.NoError(t, err, "%v", err)
			if strings.Contains(line, `"echo" "`+marker+`"`) {
				break
			}
			if strings.Contains(line, "[0 lua]") {
				continue
			}
			parts := strings.SplitN(line, "] ", 2)
			require.Len(t, parts, 2, "MONITOR line %q", line)
			command := strings.TrimSpace(parts[1])
			lower := strings.ToLower(command)
			if strings.HasPrefix(lower, `"hello"`) || strings.HasPrefix(lower, `"client"`) || strings.HasPrefix(lower, `"auth"`) {
				continue
			}
			commands = append(commands, command)
		}
		t.Logf("verb=%q commands=%d wire=%v", strings.Join(args, " "), len(commands), commands)
		assert.Len(t, commands, 1, "%v sent %d application commands; want one atomic call", args, len(commands))
	}
}
