package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/redisfn"
)

// library_cover_test.go reaches the three functions of library.go the unit
// tier left at 0.0% (tableLibrary, withLibrary, DialHook). Nothing here dials
// a store: withLibrary's hook is driven through an inner probe and a Dialer
// that fails at once, so the test owns no socket and starts no process.

// coverProbe is the hook under withLibrary's: it answers an FCALL from a fake
// store, answers the load's FUNCTION LIST, and hands every other command down
// the chain. The library hook withLibrary adds is the outer one, so it sees
// the miss and runs the load, which reaches the probe because it is sent
// through the same client. It records the commands it saw.
type coverProbe struct {
	answer  error
	loadErr error
	calls   []string
}

func (p *coverProbe) DialHook(next redis.DialHook) redis.DialHook { return next }

func (p *coverProbe) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		p.calls = append(p.calls, cmd.Name())
		switch cmd.Name() {
		case "fcall", "fcall_ro":
			if p.answer != nil {
				cmd.SetErr(p.answer)
				return p.answer
			}
		case "function":
			if p.loadErr != nil {
				cmd.SetErr(p.loadErr)
				return p.loadErr
			}
		}
		return next(ctx, cmd)
	}
}

func (p *coverProbe) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// TestLibraryCoverTableLibrary: tableLibrary is the embedded nova_sprint spec
// with nova-table's own remedy; it builds, and a library whose name is not a
// Redis name is refused.
func TestLibraryCoverTableLibrary(t *testing.T) {
	t.Parallel()

	lib := tableLibrary()
	require.Equal(t, "nova_sprint", lib.Name, "tableLibrary name")
	require.Equal(t, "nova-redis fn load --addr <host:port>", lib.Remedy, "tableLibrary remedy")
	require.Equal(t, "lua/*.lua", lib.Glob, "tableLibrary glob")
	require.NotNil(t, lib.Files, "tableLibrary files")
	digest, err := lib.Digest()
	require.NoError(t, err, "tableLibrary Digest")
	require.NotEmpty(t, digest, "tableLibrary digest")
	functions, err := lib.Registered()
	require.NoError(t, err, "tableLibrary Registered")
	var names []string
	for _, fn := range functions {
		names = append(names, fn.Name)
	}
	require.Contains(t, names, "ns_table_create", "tableLibrary functions")

	broken := lib
	broken.Name = "not a name"
	_, err = broken.Digest()
	require.ErrorIs(t, err, redisfn.ErrRefused, "a library with a broken name is refused")
}

// TestLibraryCoverWithLibrary: withLibrary installs the first-contact hook on
// the client. A missing FCALL the store answers loads through the client (the
// load's own FUNCTION LIST reaching the client's hook chain), and an FCALL the
// store refused for another reason is passed through without a load.
func TestLibraryCoverWithLibrary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		answer  error
		library bool
	}{
		{"a missing FCALL loads through the client", notFound, true},
		{"an FCALL refused for another reason is passed through", reply("ERR stale epoch"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processLibrary = &firstContact{}
			var dials int
			client := redis.NewClient(&redis.Options{
				Addr:       "example.test:1",
				MaxRetries: -1,
				Dialer: func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, io.EOF
				},
			})
			t.Cleanup(func() { _ = client.Close() })
			withLibrary(client)
			probe := &coverProbe{answer: tc.answer, loadErr: io.EOF}
			client.AddHook(probe)

			err := client.Process(ctx, fcall("ns_table_create"))
			require.Error(t, err, "%s: err %v; want an error", tc.name, err)
			var libErr *libraryError
			if tc.library {
				require.ErrorAs(t, err, &libErr, "%s: err %v; want the library hook's refusal", tc.name, err)
				require.Contains(t, err.Error(), "loading it failed", "%s: err %v", tc.name, err)
				require.Equal(t, redisconn.Unreachable, redisconn.Classify(err), "%s: class of %v", tc.name, err)
				require.Contains(t, probe.calls, "function", "%s: the load did not go through the client: %v", tc.name, probe.calls)
			} else {
				require.NotErrorAs(t, err, &libErr, "%s: err %v; want the store's own refusal, not the hook's", tc.name, err)
				require.ErrorIs(t, err, tc.answer, "%s: err %v", tc.name, err)
				require.Equal(t, []string{"fcall"}, probe.calls, "%s: a refusal for another reason must not load", tc.name)
			}
			assert.Zero(t, dials, "%s: nothing here dials: %d", tc.name, dials)
		})
	}
}

// TestLibraryCoverDialHook: DialHook passes a dial through unchanged, both
// when it succeeds and when the next hook refuses it.
func TestLibraryCoverDialHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sentinel := errors.New("dial refused")
	for _, tc := range []struct {
		name string
		next redis.DialHook
		err  error
	}{
		{"a dial that succeeds", func(context.Context, string, string) (net.Conn, error) { return nil, nil }, nil},
		{"a dial the next hook refuses", func(context.Context, string, string) (net.Conn, error) { return nil, sentinel }, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := libraryHook{}.DialHook(tc.next)
			conn, err := got(ctx, "tcp", "example.test:1")
			require.Nil(t, conn, "%s: conn %v", tc.name, conn)
			if tc.err == nil {
				require.NoError(t, err, "%s", tc.name)
			} else {
				require.ErrorIs(t, err, tc.err, "%s: err %v; want %v", tc.name, err, tc.err)
			}
		})
	}
}
