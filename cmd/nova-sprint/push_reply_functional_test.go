//go:build functional

package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This fixture runs only in functionalrun's Linux container. The real native
// command handlers, HTTP forwarding and two independent Redis stores remain
// the subject; the harness adapter only captures the delivered challenge.
func pushReplyStore(t *testing.T, actor string, socket bool) (*app, *store.Store, string) {
	t.Helper()
	var extra []string
	var sock string
	if socket {
		sock = quotedSocket(t)
		extra = []string{"--unixsocket", sock}
	}
	addr := testutil.Start(t, extra...)
	options := &redis.Options{Addr: addr}
	if socket {
		addr = sock
		options = &redis.Options{Network: "unix", Addr: sock}
	}
	admin := redis.NewClient(options)
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(context.Background(), admin))
	a := newApp(func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": actor}[k]
	})
	t.Cleanup(a.close)
	var out, errs bytes.Buffer
	require.Equal(t, 0, a.run([]string{"init"}, &out, &errs), "%s%s", out.String(), errs.String())
	st, err := a.store(common{redis: addr, actor: actor})
	require.NoError(t, err)
	return a, st, addr
}

// quotedSocket is defined in testhelpers_functional_test.go

func TestPushProofReplyIgnoresTheRecipientsWrongDefaultStore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		folder bool
		server bool
		socket bool
	}{
		{"native-direct", false, false, false},
		{"folder-direct", true, false, false},
		{"native-server", false, true, false},
		{"folder-server", true, true, false},
		{"quoted-socket", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			name := "reply-" + tc.name
			producer, target, address := pushReplyStore(t, name, tc.socket)
			_, wrong, wrongAddress := pushReplyStore(t, name, false)
			folder := t.TempDir()
			rec := sprint.PushRecord{Name: name, Harness: "opencode", Target: folder}
			session := &fakeSession{}
			if tc.folder {
				rec.Adapter = sprint.AdapterFolder
				pushTests.Store(name, true)
			} else {
				pushTests.Store(name, session)
			}
			t.Cleanup(func() { pushTests.Delete(name) })
			require.NoError(t, writePush(ctx, target, rec))
			source, err := producer.storeSource(ctx, target, address, 0, 0)
			require.NoError(t, err)
			var src inboxSource = source
			if tc.server {
				producer.serveAddr = address
				server := httptest.NewServer(localHandler{producer})
				t.Cleanup(server.Close)
				client := newApp(func(string) string { return "" })
				t.Cleanup(client.close)
				src = &serverSource{a: client, addr: strings.TrimPrefix(server.URL, "http://")}
			}
			var said bytes.Buffer
			producer.prove(ctx, src, name, false, &said)
			require.Contains(t, said.String(), "PUSH CHECK", said.String())
			text := session.last()
			if tc.folder {
				files, err := filepath.Glob(filepath.Join(folder, "PROOF-*"))
				require.NoError(t, err)
				require.Len(t, files, 1)
				body, err := os.ReadFile(files[0])
				require.NoError(t, err)
				nonce := strings.TrimPrefix(filepath.Base(files[0]), "PROOF-")
				text = strings.ReplaceAll(string(body), "<nonce>", nonce)
			}
			rec, ok, err := readPush(ctx, target, name)
			require.NoError(t, err)
			require.True(t, ok)
			require.NotEmpty(t, rec.Nonce)
			// Even the same actor and valid nonce on the wrong store must remain
			// unproven: this makes the old endpoint-free command fail the test.
			require.NoError(t, writePush(ctx, wrong, rec))
			_, command, found := strings.Cut(text, "end this turn: ")
			require.True(t, found, text)
			words, err := onboarding.SplitShell(strings.TrimSpace(command))
			require.NoError(t, err)
			env := map[string]string{"NOVA_SPRINT_REDIS": wrongAddress, "NOVA_SPRINT_ACTOR": "wrong-actor", "SECRET": "must-not-be-in-the-challenge"}
			if tc.folder || tc.server {
				env[ServerEnv] = "127.0.0.1:1"
			}
			if words[0] == "env" {
				key, value, ok := strings.Cut(words[1], "=")
				require.True(t, ok)
				env[key] = value
				words = words[2:]
			}
			require.Equal(t, "nova-sprint", words[0])
			receiver := newApp(func(k string) string { return env[k] })
			t.Cleanup(receiver.close)
			var out, errs bytes.Buffer
			require.Equal(t, 0, receiver.run(words[1:], &out, &errs), "%s%s", out.String(), errs.String())
			proven, ok, err := readPush(ctx, target, name)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, rec.Nonce, proven.PongOf)
			assert.False(t, proven.Proven.IsZero())
			unchanged, ok, err := readPush(ctx, wrong, name)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, rec, unchanged)
			assert.NotContains(t, text, env["SECRET"])
		})
	}
}
