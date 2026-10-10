package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
)

// fakeStore answers a Redis client for the unit tier: it speaks the wire
// protocol over net.Pipe, so a test reaches libraryMatches with no socket,
// no subprocess, no Redis and no Postgres. Each command is answered by the
// first word of its name from scripts, and a command with no script gets a
// bulk string "OK".
type fakeStore struct {
	mu      sync.Mutex
	scripts map[string]string
}

func newFakeStore(scripts map[string]string) *fakeStore {
	return &fakeStore{scripts: scripts}
}

func (s *fakeStore) reply(word string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.scripts[word]; ok {
		return r
	}
	return "$2\r\nOK\r\n"
}

// Dial is redis.Options.Dialer: one net.Pipe per dial, no socket.
func (s *fakeStore) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	client, server := net.Pipe()
	go s.serve(server)
	return client, nil
}

func (s *fakeStore) serve(c net.Conn) {
	defer c.Close()
	rd := bufio.NewReader(c)
	for {
		word, err := readFakeCommand(rd)
		if err != nil {
			return
		}
		if _, err := io.WriteString(c, s.reply(word)); err != nil {
			return
		}
	}
}

// readFakeCommand reads one client command as an array of bulk strings and
// answers its name, uppercased from the first word.
func readFakeCommand(rd *bufio.Reader) (string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line, "*"), "\r\n"))
	if err != nil {
		return "", err
	}
	first := ""
	for i := range n {
		head, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		size, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(head, "$"), "\r\n"))
		if err != nil {
			return "", err
		}
		body := make([]byte, size+2)
		if _, err := io.ReadFull(rd, body); err != nil {
			return "", err
		}
		if i == 0 {
			first = strings.ToUpper(string(body[:size]))
		}
	}
	return first, nil
}

// helloAccepted is the RESP3 map a store sends for HELLO 3.
const helloAccepted = "%7\r\n$6\r\nserver\r\n$5\r\nredis\r\n$7\r\nversion\r\n$5\r\n8.0.0\r\n$5\r\nproto\r\n:3\r\n$2\r\nid\r\n:7\r\n$4\r\nmode\r\n$10\r\nstandalone\r\n$4\r\nrole\r\n$6\r\nmaster\r\n$7\r\nmodules\r\n*0\r\n"

// functionListReply is a RESP3 FUNCTION LIST reply: an array of one library
// map naming library, holding code.
func functionListReply(library, code string) string {
	return fmt.Sprintf("*1\r\n%%2\r\n$12\r\nlibrary_name\r\n$%d\r\n%s\r\n$12\r\nlibrary_code\r\n$%d\r\n%s\r\n",
		len(library), library, len(code), code)
}

// TestMainCoverOpenConnRefusesAStoreThatIsNotThereAndResolvesTheLogin
// covers openConn (cmd/nova-sprint/main.go:229, the finding's 0.0%). Its main
// path opens a store; the unit tier owns no socket to open, and the
// package's Open exposes no dialer to inject, so the path exercised here is
// the one a store that is not there takes: the login is resolved from the
// environment (both branches: a user with a named password variable, and no
// user) and Open is called, its refusal returned unchanged and nothing left
// open. The successful open needs a live store or a redisconn dial seam and
// is named in the report as not-done.
func TestMainCoverOpenConnRefusesAStoreThatIsNotThereAndResolvesTheLogin(t *testing.T) {
	t.Parallel()
	// a port no store listens on: the dial is refused at once, no process,
	// no bind and nothing started
	const absent = "127.0.0.1:1"
	t.Run("a user with a named password variable is resolved, then the dial is refused", func(t *testing.T) {
		env := map[string]string{
			"NOVA_SPRINT_REDIS_USER":         "sprint-user",
			"NOVA_SPRINT_REDIS_PASSWORD_ENV": "SPRINT_PW",
			"SPRINT_PW":                      "s3cr3t",
		}
		a := &app{getenv: func(k string) string { return env[k] }}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		conn, err := a.openConn(ctx, absent)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), absent, err.Error())
	})
	t.Run("no user and no password variable: the default login, then the dial is refused", func(t *testing.T) {
		a := &app{getenv: func(string) string { return "" }}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		conn, err := a.openConn(ctx, absent)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), absent, err.Error())
	})
}

// TestMainCoverLibraryMatchesAcceptsTheEmbeddedLibraryAndRefusesTheRest
// covers libraryMatches (cmd/nova-sprint/main.go:245, the finding's other
// 0.0%): the main path accepts a store holding this build's embedded
// library, and two refusals -- a store holding none, and one holding another
// build's. The client is built over the fake's net.Pipe dialer, and the
// embedded source is fn.Source, the same function the code under test reads.
func TestMainCoverLibraryMatchesAcceptsTheEmbeddedLibraryAndRefusesTheRest(t *testing.T) {
	t.Parallel()
	source, err := fn.Source()
	require.NoError(t, err)
	other := "#!lua name=" + fn.Library + "\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	ctx := context.Background()
	cases := []struct {
		name    string
		reply   string
		refused bool
		holds   []string
	}{
		{
			name:  "the store holds this build's library: accepted",
			reply: functionListReply(fn.Library, source),
		},
		{
			name:    "the store holds none: refused, naming the load",
			reply:   "*0\r\n",
			refused: true,
			holds:   []string{"holds no " + fn.Library + " function library", "nova-redis fn load --addr 127.0.0.1:6379"},
		},
		{
			name:    "the store holds another build's: refused, naming both sums",
			reply:   functionListReply(fn.Library, other),
			refused: true,
			holds:   []string{"holds " + fn.Library + " library " + fn.Sum(other), "this build is " + fn.Sum(source)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore(map[string]string{"HELLO": helloAccepted, "FUNCTION": tc.reply})
			err := libraryMatches(ctx, fakeClient(t, store), "127.0.0.1:6379")
			if !tc.refused {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tc.holds {
				assert.Contains(t, err.Error(), want, err.Error())
			}
		})
	}
}

// fakeClient is a go-redis client over the fake's net.Pipe dialer, closed at
// the test's end.
func fakeClient(t *testing.T, store *fakeStore) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", Dialer: store.dial})
	t.Cleanup(func() { _ = c.Close() })
	return c
}
