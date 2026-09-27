//go:build functional

package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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
	if err := admin.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(conn, "*1\r\n$7\r\nMONITOR\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	if line, err := reader.ReadString('\n'); err != nil || line != "+OK\r\n" {
		t.Fatalf("MONITOR ready: %q %v", line, err)
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
		if code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, out, errout)
		}
		marker := fmt.Sprintf("table-probe-%d", i)
		if err := admin.Echo(ctx, marker).Err(); err != nil {
			t.Fatal(err)
		}
		var commands []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(line, `"echo" "`+marker+`"`) {
				break
			}
			if strings.Contains(line, "[0 lua]") {
				continue
			}
			parts := strings.SplitN(line, "] ", 2)
			if len(parts) != 2 {
				t.Fatalf("MONITOR line %q", line)
			}
			command := strings.TrimSpace(parts[1])
			lower := strings.ToLower(command)
			if strings.HasPrefix(lower, `"hello"`) || strings.HasPrefix(lower, `"client"`) || strings.HasPrefix(lower, `"auth"`) {
				continue
			}
			commands = append(commands, command)
		}
		t.Logf("verb=%q commands=%d wire=%v", strings.Join(args, " "), len(commands), commands)
		if len(commands) != 1 {
			t.Errorf("%v sent %d application commands; want one atomic call", args, len(commands))
		}
	}
}
