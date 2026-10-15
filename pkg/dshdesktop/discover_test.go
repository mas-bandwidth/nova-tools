package dshdesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureHome builds a harness home shaped like ~/.dsh: storages/workspace.json
// and sessions/<project key>/<id>/session.v4.jsonl.zstd with fixed mtimes. No
// real install, clock, lock or process is read.
type fixtureSession struct {
	id       string
	modified time.Time
}

var epoch = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func fixtureHome(t *testing.T, archived []string, workspaces map[string][]fixtureSession) string {
	t.Helper()
	home := t.TempDir()
	type ws struct {
		Path       string   `json:"path"`
		Title      string   `json:"title"`
		SessionIDs []string `json:"sessionIds"`
	}
	tables := map[string]ws{}
	for title, sessions := range workspaces {
		path := "/Volumes/nova/ai/" + title
		w := ws{Path: path, Title: title}
		for _, s := range sessions {
			w.SessionIDs = append(w.SessionIDs, s.id)
			dir := filepath.Join(home, "sessions", "--Volumes-nova-ai-"+title+"--", s.id)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			log := filepath.Join(dir, "session.v4.jsonl.zstd")
			require.NoError(t, os.WriteFile(log, []byte("x"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "session.lock"), nil, 0o644))
			require.NoError(t, os.Chtimes(log, s.modified, s.modified))
		}
		tables["ws-"+title] = w
	}
	doc := map[string]any{
		"unit":   map[string]any{"name": "workspace", "version": 2},
		"global": map[string]any{"archivedSessionIds": archived},
		"tables": map[string]any{"workspaces": tables},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "storages"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "storages", "workspace.json"), data, 0o600))
	return home
}

func heldIDs(home string, ids ...string) HeldFunc {
	return func(locks []string) map[string]bool {
		out := map[string]bool{}
		for _, l := range locks {
			for _, id := range ids {
				if filepath.Base(filepath.Dir(l)) == id {
					out[l] = true
				}
			}
		}
		return out
	}
}

func TestDiscoverPicksNewestLiveUnarchivedSessionInFriendsWorkspace(t *testing.T) {
	t.Parallel()
	home := fixtureHome(t, []string{"session-archived"}, map[string][]fixtureSession{
		"ada": {
			{"session-old", epoch},
			{"session-archived", epoch.Add(3 * time.Hour)}, // archived but still held open: never chosen
			{"session-new", epoch.Add(time.Hour)},
			{"session-dead", epoch.Add(2 * time.Hour)}, // newest unarchived, but nobody holds its lock
		},
		"other": {{"session-elsewhere", epoch.Add(5 * time.Hour)}},
	})
	s, err := Discover(home, "ada", heldIDs(home, "session-old", "session-archived", "session-new", "session-elsewhere"))
	require.NoError(t, err)
	assert.Equal(t, "session-new", s.ID)
	assert.True(t, s.Live)
}

func TestDiscoverFallsBackToNewestWhenNothingIsLive(t *testing.T) {
	t.Parallel()
	home := fixtureHome(t, nil, map[string][]fixtureSession{
		"ada": {{"session-a", epoch}, {"session-b", epoch.Add(time.Minute)}},
	})
	s, err := Discover(home, "ada", heldIDs(home))
	require.NoError(t, err)
	assert.Equal(t, "session-b", s.ID)
	assert.False(t, s.Live)
}

func TestDiscoverRefusesWithoutWorkspaceOrSession(t *testing.T) {
	t.Parallel()
	home := fixtureHome(t, []string{"session-a"}, map[string][]fixtureSession{
		"ada": {{"session-a", epoch}},
	})
	_, err := Discover(home, "emma", nil)
	require.ErrorContains(t, err, "no DeepSeek Harness workspace named emma")
	_, err = Discover(home, "ada", nil)
	require.ErrorContains(t, err, "no unarchived DeepSeek Harness session")
	_, err = Discover(t.TempDir(), "ada", nil)
	require.Error(t, err)
}

func TestDiscoverFollowsANewSessionIntoTheTargetFile(t *testing.T) {
	t.Parallel()
	events := t.TempDir()
	home := fixtureHome(t, nil, map[string][]fixtureSession{
		"ada": {{"session-one", epoch}},
	})
	s, err := Discover(home, "ada", heldIDs(home, "session-one"))
	require.NoError(t, err)
	changed, err := WriteTarget(events, s.ID)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = WriteTarget(events, s.ID)
	require.NoError(t, err)
	assert.False(t, changed, "an unchanged session leaves the target alone")

	// She starts a new session and archives the old one; both locks are still open.
	home = fixtureHome(t, []string{"session-one"}, map[string][]fixtureSession{
		"ada": {{"session-one", epoch}, {"session-two", epoch.Add(time.Hour)}},
	})
	s, err = Discover(home, "ada", heldIDs(home, "session-one", "session-two"))
	require.NoError(t, err)
	changed, err = WriteTarget(events, s.ID)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "session-two", ReadTarget(events))
	info, err := os.Stat(TargetPath(events))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	_, err = WriteTarget(events, "bad id/../x")
	require.Error(t, err)
}

func TestHomeResolvesLikeTheHarness(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	h, err := Home(env(map[string]string{"HOME": "/Users/ada"}))
	require.NoError(t, err)
	assert.Equal(t, "/Users/ada/.dsh", h)
	h, err = Home(env(map[string]string{"HOME": "/Users/ada", "DSH_HOME": "  "}))
	require.NoError(t, err)
	assert.Equal(t, "/Users/ada/.dsh", h, "a blank DSH_HOME is unset")
	h, err = Home(env(map[string]string{"HOME": "/Users/ada", "DSH_HOME": "~/harness"}))
	require.NoError(t, err)
	assert.Equal(t, "/Users/ada/harness", h)
	_, err = Home(env(nil))
	require.Error(t, err)
}
