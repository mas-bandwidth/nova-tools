package friend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lanes' directories are allowed in the project config, so a headless
// opencode run never auto-rejects a tool call there (measured 2026-10-04: a
// path through the symlink in the home directory, external_directory): a
// new file names each path, an existing file keeps what it held, and a
// second call changes nothing.
func TestAllowDirsWritesTheFriendsPathsIntoTheProjectConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrote, err := AllowDirs(dir, []string{"/w/bob", "/Users/x/bob-working/"})
	require.NoError(t, err)
	assert.True(t, wrote)
	var cfg map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, OpenCodeConfig))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Equal(t, map[string]any{"/w/bob/**": "allow", "/Users/x/bob-working/**": "allow"}, cfg["permission"].(map[string]any)["external_directory"])
	wrote, err = AllowDirs(dir, []string{"/w/bob"})
	require.NoError(t, err)
	assert.False(t, wrote, "nothing new: the file is left alone")

	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, OpenCodeConfig), []byte(`{"model":"inception/mercury-2.5","permission":{"bash":"allow","external_directory":{"/tmp/**":"deny"}}}`), 0o644))
	_, err = AllowDirs(other, []string{"/w/bob"})
	require.NoError(t, err)
	raw, err = os.ReadFile(filepath.Join(other, OpenCodeConfig))
	require.NoError(t, err)
	cfg = nil
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Equal(t, "inception/mercury-2.5", cfg["model"], "what the file held is kept")
	perm := cfg["permission"].(map[string]any)
	assert.Equal(t, "allow", perm["bash"])
	assert.Equal(t, map[string]any{"/tmp/**": "deny", "/w/bob/**": "allow"}, perm["external_directory"])

	all := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(all, OpenCodeConfig), []byte(`{"permission":{"external_directory":"allow"}}`), 0o644))
	wrote, err = AllowDirs(all, []string{"/w/bob"})
	require.NoError(t, err)
	assert.False(t, wrote, "every directory is allowed already")
}

// A lane's session is opened by a run with no --session, and is the session
// the listing gained; a card's turn names it, and a refused permission in its
// output is the turn's Rejected.
func TestOpenCodeOpensALaneSessionAndDeliversIntoIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var calls [][]string
	lists := 0
	run := func(_ context.Context, d, name string, args []string, stdin string) (string, int, error) {
		calls = append(calls, append([]string{d, name}, args...))
		assert.Empty(t, stdin, "stdin is /dev/null")
		switch {
		case args[0] == "session":
			lists++
			if lists == 1 {
				return `[{"id":"old","directory":"` + dir + `","updated":5}]`, 0, nil
			}
			return `[{"id":"old","directory":"` + dir + `","updated":30},{"id":"new","directory":"` + dir + `","updated":20},{"id":"else","directory":"/w/ada","updated":40}]`, 0, nil
		case strings.Contains(strings.Join(args, " "), "card c1"):
			return "\x1b[91m**Blocked:** Permission to read `/Users/x/bob-working/jobs` was rejected\x1b[0m\nstopping\n", 0, nil
		}
		return "ready\n", 0, nil
	}
	o := &OpenCode{Dir: dir, Run: run, Allow: []string{"/Users/x/bob-working"}}
	id, err := o.OpenSession(context.Background(), "You are bob.")
	require.NoError(t, err)
	assert.Equal(t, "new", id, "the session the listing gained, not the newest")
	assert.Equal(t, []string{dir, "opencode", "run", "--dir", dir, "You are bob."}, calls[1], "a run with no --session opens one")
	raw, err := os.ReadFile(filepath.Join(dir, OpenCodeConfig))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"/Users/x/bob-working/**": "allow"`)
	assert.Contains(t, string(raw), `"`+dir+`/**": "allow"`)

	lt, err := o.DeliverTo(context.Background(), "new", "one card this turn, card c1")
	require.NoError(t, err)
	assert.Equal(t, []string{dir, "opencode", "run", "--session", "new", "--dir", dir, "one card this turn, card c1"}, calls[len(calls)-1])
	assert.Equal(t, "**Blocked:** Permission to read `/Users/x/bob-working/jobs` was rejected", lt.Rejected)
	lt, err = o.DeliverTo(context.Background(), "new", "another")
	require.NoError(t, err)
	assert.Empty(t, lt.Rejected)

	refusing := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "session" {
			return "[]", 0, nil
		}
		return freddyRefusal, 1, nil
	}
	_, err = (&OpenCode{Dir: dir, Run: refusing}).OpenSession(context.Background(), "seed")
	var refused ProviderRefused
	assert.ErrorAs(t, err, &refused, "a provider refusing the seed is said as such")
}

// An opencode lane's turn is priced from the session's own record
// (`opencode export <id>`), once however often it is read.
func TestAnOpenCodeLaneTurnIsPricedFromTheSessionsOwnRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	total := `{"info":{"id":"s1"},"messages":[{"info":{"role":"user"}},{"info":{"role":"assistant","cost":0.125}},{"info":{"role":"assistant","cost":0.25}}]}`
	run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "export" {
			assert.Equal(t, []string{"export", "s1"}, args)
			return "Exporting session: s1\n" + total, 0, nil
		}
		return "done\n", 0, nil
	}
	spend := &Spend{}
	o := &OpenCode{Dir: dir, Run: run, Spend: spend}
	_, err := o.DeliverTo(context.Background(), "s1", "card c1")
	require.NoError(t, err)
	_, err = o.DeliverTo(context.Background(), "s1", "card c2")
	require.NoError(t, err)
	assert.InDelta(t, 0.375, spend.Snapshot().CostUSD, 1e-9, "the record's total, not counted twice")
	total = `{"messages":[{"info":{"cost":0.375}},{"info":{"cost":0.5}}]}`
	_, err = o.DeliverTo(context.Background(), "s1", "card c3")
	require.NoError(t, err)
	assert.InDelta(t, 0.875, spend.Snapshot().CostUSD, 1e-9, "a later turn adds what the record grew by")

	var said strings.Builder
	broken := &OpenCode{Dir: dir, Spend: &Spend{}, Out: &said, Run: func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "export" {
			return "not json", 0, nil
		}
		return "done\n", 0, nil
	}}
	lt, err := broken.DeliverTo(context.Background(), "s1", "card")
	require.NoError(t, err, "a record that cannot be read fails no turn")
	assert.Zero(t, lt.Exit)
	assert.Contains(t, said.String(), "is not priced")
}
