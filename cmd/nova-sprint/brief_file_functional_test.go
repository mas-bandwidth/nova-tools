//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// add --brief-file on a real store: a brief of many paragraphs, read from a
// file, is stored byte for byte with its one trailing newline cut, and the
// packets of queue --json and take --json carry it whole.
func TestAddBriefFileOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, fn.Load(context.Background(), c))
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	run := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		require.Equal(t, 0, code, "%v: %d %s%s", args, code, out.String(), errb.String())
		return out.String()
	}
	// the card lint's passing brief, then the paragraphs under test
	brief := passingBrief("Fix the empty case.") + "\nThen:\n\t- keep the tab\n  - keep the indent, \"quotes\", 'ticks', ünï\n"
	path := filepath.Join(t.TempDir(), "brief.md")
	require.NoError(t, os.WriteFile(path, []byte(brief+"\n"), 0o600))
	run("init", "--readers", "reader-a,reader-b", "--members", "m1")
	// a brief that fails the card lint is refused on the store, exit 2, and writes nothing
	bare := filepath.Join(t.TempDir(), "bare.md")
	require.NoError(t, os.WriteFile(bare, []byte("Fix the empty case.\n"), 0o600))
	var out, errb bytes.Buffer
	code := a.run([]string{"add", "--stream", "s0", "--count", "1", "--brief-file", bare}, &out, &errb)
	require.Equal(t, 2, code, "a brief without the child rules: exit %d, out %q, err %q; want exit 2 with the lint's lines", code, out.String(), errb.String())
	require.Contains(t, errb.String(), "LINT DRIFT brief rule-worktree", "a brief without the child rules: exit %d, out %q, err %q; want exit 2 with the lint's lines", code, out.String(), errb.String())
	require.Zero(t, out.Len(), "a brief without the child rules: exit %d, out %q, err %q; want exit 2 with the lint's lines", code, out.String(), errb.String())
	run("add", "--stream", "s1", "--count", "1", "--brief-file", path)
	run("fleet", "beat", "m1")
	run("start")
	run("tick") // the fleet update, last in the tick, brings m1 up
	run("tick") // the pump deals to it
	var q struct{ Cards []queueCard }
	require.NoError(t, json.Unmarshal([]byte(run("queue", "--as", "m1", "--json")), &q))
	require.Len(t, q.Cards, 1, "queue --json: %+v", q.Cards)
	require.NotNil(t, q.Cards[0].Packet, "queue --json: %+v", q.Cards)
	require.Equal(t, brief, q.Cards[0].Packet.Brief, "queue --json: %+v", q.Cards)
	var took struct {
		Packets []struct{ Brief string }
	}
	require.NoError(t, json.Unmarshal([]byte(run("take", "--as", "m1", "--limit", "1", "--json")), &took))
	require.Len(t, took.Packets, 1, "take --json: %+v", took.Packets)
	require.Equal(t, brief, took.Packets[0].Brief, "take --json: %+v", took.Packets)
}
