package dshdesktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSavedNoticeAt(t *testing.T, dir, id, nonce string, messages int, at time.Time) string {
	t.Helper()
	name := id
	if !strings.HasPrefix(name, "notice-") {
		name = "notice-" + name
	}
	path := filepath.Join(dir, name+".json")
	payload := map[string]any{
		"saved_at":   at.UTC().Format(time.RFC3339Nano),
		"hook_epoch": 1,
		"hook_role":  "friend",
		"event_id":   name,
		"messages":   messages,
	}
	if nonce != "" {
		payload["challenge"] = map[string]any{
			"id":      "c-" + nonce,
			"from":    "serve",
			"subject": "HOOK CHALLENGE nonce=" + nonce,
		}
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return name
}

func countDirNoticeFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "notice-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	return names
}

// Deliveries past the bound each meet the same state (the bound's resolved
// notices and one new one), so four times the bound holds the count over
// many prunes at the bound without a thousand file round trips.
func TestResolvedNoticesAreKeptToABoundedCount(t *testing.T) {
	t.Parallel()
	r := route(t)
	start := time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC)

	const deliveries = 4 * MaxResolvedNotices
	for i := 1; i <= deliveries; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("notice-%04d", i)
		writeSavedNoticeAt(t, r.EventsDir, id, fmt.Sprintf("nonce-%04d", i), 0, at)
		_, err := DeliverAt(r, id, ChallengeNotice, at)
		require.NoError(t, err)
		_, err = ResolveNotice(r.EventsDir, id)
		require.NoError(t, err)

		files := countDirNoticeFiles(t, r.EventsDir)
		require.LessOrEqual(t, len(files), MaxResolvedNotices, "resolved notices after %d deliveries must never exceed MaxResolvedNotices=%d", i, MaxResolvedNotices)
	}

	pendingAt := start.Add((deliveries + 1) * time.Second)
	pendingID := fmt.Sprintf("notice-%04d", deliveries+1)
	writeSavedNoticeAt(t, r.EventsDir, pendingID, fmt.Sprintf("nonce-%04d", deliveries+1), 0, pendingAt)
	_, err := DeliverAt(r, pendingID, ChallengeNotice, pendingAt)
	require.NoError(t, err)

	files := countDirNoticeFiles(t, r.EventsDir)
	require.Len(t, files, MaxResolvedNotices+1, "directory holds at most MaxResolvedNotices resolved notices plus the 1 active pending notice")
	assert.Contains(t, files, pendingID+".json", "the active pending notice is retained")
	assert.Contains(t, files, fmt.Sprintf("notice-%04d.json", deliveries), "the newest resolved notice is retained")
	assert.Contains(t, files, fmt.Sprintf("notice-%04d.json", deliveries-MaxResolvedNotices+1), "the oldest notice within the bound is retained")
	assert.NotContains(t, files, fmt.Sprintf("notice-%04d.json", deliveries-MaxResolvedNotices), "the newest notice past the bound is pruned")
	assert.NotContains(t, files, "notice-0001.json", "older resolved notices are pruned")
}

func TestAProveSuccessPrunesResolvedNotices(t *testing.T) {
	t.Parallel()
	r := route(t)
	start := time.Date(2026, 10, 15, 19, 30, 0, 0, time.UTC)

	const total = 40
	for i := 1; i <= total; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("notice-%04d", i)
		nonce := fmt.Sprintf("nonce-%04d", i)
		writeSavedNoticeAt(t, r.EventsDir, id, nonce, 0, at)
		require.NoError(t, MarkPending(r.EventsDir, id))
	}

	before := countDirNoticeFiles(t, r.EventsDir)
	require.Len(t, before, total)

	pruned, err := PruneOnProve(r.EventsDir, fmt.Sprintf("nonce-%04d", total))
	require.NoError(t, err)
	assert.Equal(t, total-MaxResolvedNotices, pruned)

	after := countDirNoticeFiles(t, r.EventsDir)
	require.Len(t, after, MaxResolvedNotices, "a prove success prunes resolved notices to MaxResolvedNotices")
	assert.Contains(t, after, fmt.Sprintf("notice-%04d.json", total))
	assert.Contains(t, after, fmt.Sprintf("notice-%04d.json", total-MaxResolvedNotices+1))
	assert.NotContains(t, after, "notice-0001.json")
}

func TestThePendingNoticeIsNeverPruned(t *testing.T) {
	t.Parallel()
	r := route(t)
	start := time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC)

	pendingID := "notice-0001"
	writeSavedNoticeAt(t, r.EventsDir, pendingID, "nonce-pending", 2, start)
	_, err := DeliverAt(r, pendingID, ChallengeNotice, start)
	require.NoError(t, err)
	require.NoError(t, MarkPending(r.EventsDir, pendingID))

	for i := 2; i <= MaxResolvedNotices+25; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("notice-%04d", i)
		writeSavedNoticeAt(t, r.EventsDir, id, fmt.Sprintf("nonce-%04d", i), 0, at)
		_, err := PruneResolvedNotices(r.EventsDir, pendingID)
		require.NoError(t, err)
	}

	// Even if a prove runs for an older nonce while pendingID still holds unconsumed messages, pendingID is never pruned.
	_, err = PruneOnProve(r.EventsDir, "nonce-0010")
	require.NoError(t, err)

	files := countDirNoticeFiles(t, r.EventsDir)
	require.Len(t, files, MaxResolvedNotices+1, "kept MaxResolvedNotices resolved notices plus the 1 active pending notice")
	assert.Contains(t, files, "notice-0001.json", "the active pending notice is never pruned even when it is the oldest file")
	assert.FileExists(t, filepath.Join(r.EventsDir, ".notices", "notice-notice-0001.json"), "the queued record in .notices is never pruned while unresolved")

	// A failing prune reports a loud error and never silently skips.
	badDir := filepath.Join(r.EventsDir, "notice-0000.json")
	require.NoError(t, os.MkdirAll(filepath.Join(badDir, "nonempty"), 0o700))
	_, err = PruneResolvedNotices(r.EventsDir, pendingID)
	require.Error(t, err, "a prune failure must report a loud error, never silently skip")
}
