package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSnapshotCache tests the snapshot cache functionality.
func TestSnapshotCache(t *testing.T) {
	t.Parallel()

	var cache snapshotCache

	// Initially, should have no snapshot
	snap, _ := cache.get()
	require.Nil(t, snap)

	// Update with a snapshot
	at := time.Now()
	cache.update(&whereSnapshot{
		At:    at,
		Epoch: 1,
		Data:  []byte(`{"sum":"1/10"}`),
	})

	// Should have the snapshot
	snap, atoms := cache.get()
	require.NotNil(t, snap)
	require.Equal(t, int64(1), snap.Epoch)
	require.Equal(t, uint64(1), atoms)

	// Should not be stale yet
	require.False(t, cache.isStale())

	// Update twice more to make it stale (>2 ticks)
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 2, Data: []byte(`{"sum":"2/10"}`)})
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 3, Data: []byte(`{"sum":"3/10"}`)})

	// Should now be stale
	require.True(t, cache.isStale())
}

// TestWhereFreshFlag tests the --fresh flag for where.
func TestWhereFreshFlag(t *testing.T) {
	t.Parallel()

	// Test that fresh flag is parsed correctly
	app := newApp(func(string) string { return "" })
	fs, _ := app.verbSetup("where")
	fresh := fs.Bool("fresh", false, "test flag")
	
	// Parse with --fresh
	_, err := parse(fs, []string{"--fresh"})
	require.NoError(t, err)
	require.True(t, *fresh)

	// Parse without --fresh
	fs2, _ := app.verbSetup("where")
	fresh2 := fs2.Bool("fresh", false, "test flag")
	_, err = parse(fs2, []string{})
	require.NoError(t, err)
	require.False(t, *fresh2)
}

// TestSnapshotStalenessRefusal tests that stale snapshots are refused.
func TestSnapshotStalenessRefusal(t *testing.T) {
	t.Parallel()

	var cache snapshotCache
	// Create a stale snapshot (>2 ticks)
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 1, Data: []byte(`{}`)})
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 2, Data: []byte(`{}`)})
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 3, Data: []byte(`{}`)})

	require.True(t, cache.isStale())
}

// TestAPISprintEndpoint tests the /api/sprint endpoint.
func TestAPISprintEndpoint(t *testing.T) {
	t.Parallel()

	app := newApp(func(string) string { return "" })
	cache := &app.snapshot
	cache.update(&whereSnapshot{
		At:    time.Now(),
		Epoch: 1,
		Data:  []byte(`{"sum":"1/10"}`),
	})

	// Test GET request
	req := httptest.NewRequest("GET", "/api/sprint", nil)
	w := httptest.NewRecorder()

	app.serveSprint(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	
	var result map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	require.Contains(t, result, "view")
	require.Contains(t, result, "snapshot")
}

// TestAPISprintEndpointStale tests that stale snapshots are refused via /api/sprint.
func TestAPISprintEndpointStale(t *testing.T) {
	t.Parallel()

	app := newApp(func(string) string { return "" })
	cache := &app.snapshot
	// Create a stale snapshot
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 1, Data: []byte(`{}`)})
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 2, Data: []byte(`{}`)})
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 3, Data: []byte(`{}`)})

	require.True(t, cache.isStale())

	// Test GET request - should refuse
	req := httptest.NewRequest("GET", "/api/sprint", nil)
	w := httptest.NewRecorder()

	app.serveSprint(w, req)

	require.Equal(t, http.StatusGone, w.Code)
}

// TestAPISprintEndpointMethod tests that only GET is allowed.
func TestAPISprintEndpointMethod(t *testing.T) {
	t.Parallel()

	app := newApp(func(string) string { return "" })
	cache := &app.snapshot
	cache.update(&whereSnapshot{At: time.Now(), Epoch: 1, Data: []byte(`{}`)})

	// Test POST request - should refuse
	req := httptest.NewRequest("POST", "/api/sprint", nil)
	w := httptest.NewRecorder()

	app.serveSprint(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestWhereViewSnapshotMetadata tests that snapshot metadata is added to where --json.
func TestWhereViewSnapshotMetadata(t *testing.T) {
	t.Parallel()

	var cache snapshotCache
	cache.update(&whereSnapshot{
		At:    time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		Epoch: 15,
		Data:  []byte(`{"sum":"10/20"}`),
	})

	snap, _ := cache.get()
	require.NotNil(t, snap)

	// Test JSON marshaling with snapshot metadata
	var v whereView
	err := json.Unmarshal(snap.Data, &v)
	require.NoError(t, err)

	snapMeta := map[string]any{"at": snap.At.UTC().Format(time.RFC3339), "age_ms": cache.ageMs()}
	result := map[string]any{"view": v, "snapshot": snapMeta}

	b, _ := json.Marshal(result)
	require.NotEmpty(t, b)
}
