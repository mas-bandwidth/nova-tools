package main

import (
	"context"
	"encoding/json"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyProbe makes a historical scan observable without changing the store's answers.
type historyProbe struct {
	*store.Mem
	reads     *atomic.Int64
	workCells *atomic.Int64
}

func (p *historyProbe) AtEpoch(epoch uint64, old bool) store.Backend {
	return &historyProbe{Mem: p.Mem.AtEpoch(epoch, old).(*store.Mem), reads: p.reads, workCells: p.workCells}
}

func (p *historyProbe) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	for _, shape := range shapes {
		if shape.Name == (sprint.Names{}).Table(sprint.Work) {
			sets := 0
			for _, col := range shape.Columns {
				if col.HasSet() {
					sets++
				}
			}
			if sets > 1 {
				p.workCells.Add(1)
			}
		}
	}
	return p.Mem.CellIDs(ctx, shapes)
}

func (p *historyProbe) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	p.reads.Add(1)
	return p.Mem.LogSince(ctx, after, max)
}

// The history selector (SPEC-SPRINT-DASHBOARD, Serving and publishing) preserves
// ordinary where's series and removes the log scan only when explicitly disabled.
func TestWhereHistoricalSeriesIsOptionalAndDefaultOn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, flags string
		series      bool
	}{
		{"default", "", true}, {"explicit true", " --landed-series=true", true},
		{"explicit false", " --landed-series=false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --readers reader-a --members m1")
			ta.ok("add --stream s1 --count 3")
			p := &historyProbe{Mem: ta.m, reads: new(atomic.Int64), workCells: new(atomic.Int64)}
			ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return p, nil }
			out := ta.ok("where --json" + tc.flags)
			var v map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(out), &v))
			_, has := v["landedSeries"]
			assert.Equal(t, tc.series, has)
			assert.Equal(t, tc.series, p.reads.Load() > 0, "historical log reads: %d", p.reads.Load())
			assert.NotEmpty(t, v["tables"], "the current sprint projection remains")
		})
	}
}

// The dashboard selector crosses the server boundary too: neither client nor server
// scans historical lines on each refresh, and the server still serves ordinary where.
func TestDashboardHistoryOptOutWorksDirectAndThroughServer(t *testing.T) {
	t.Parallel()
	for _, remote := range []bool{false, true} {
		name := "direct"
		if remote {
			name = "server"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newServerRig(t)
			r.boss("nova-sprint init --readers reader-a --members m1")
			r.boss("nova-sprint add --stream s1 --count 3")
			var probes []*historyProbe
			backend := r.a.backend
			r.a.backend = func(ctx context.Context, addr string, names sprint.Names) (store.Backend, error) {
				b, err := backend(ctx, addr, names)
				if err != nil {
					return nil, err
				}
				m, ok := b.(*store.Mem)
				require.True(t, ok, "the strict in-memory backend retains optional record methods")
				p := &historyProbe{Mem: m, reads: new(atomic.Int64), workCells: new(atomic.Int64)}
				probes = append(probes, p)
				return p, nil
			}
			a := r.a
			var sent [][]string
			if remote {
				a, _ = clientOf(t, r, "boss", &sent)
			}
			body, err := a.whereJSON("", false)
			require.NoError(t, err)
			var v map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &v))
			assert.NotContains(t, v, "landedSeries")
			assert.NotEmpty(t, v["tables"])
			require.NotEmpty(t, probes)
			for _, p := range probes {
				assert.Zero(t, p.reads.Load())
			}
			if remote {
				require.Len(t, sent, 1)
				assert.Contains(t, sent[0], "--landed-series=false")
			}
		})
	}
}

// The visible projection is identical to full rows for each release, but the critical
// selector never loads all Work cells or primary fields (SPEC-SPRINT-DASHBOARD).
func TestDashboardCriticalPlacementsMatchFullRowsWithoutLoadingWork(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --coordinator lead")
	ta.ok("add --stream s1 --count 3 --actor lead")
	ta.ok("add --stream s2 --count 2 --actor lead")
	ta.ok("add --stream s1 dependent --one --needs s2-1 --actor lead")
	ta.ok("stream set s1 --release v1.0.0 --actor lead")
	ta.ok("stream set s2 --release v1.1.0 --actor lead")
	ta.ok("hold m1 --reason 'critical cards stay off fleet' --actor lead")
	ta.ok("start --actor lead")
	ta.ok("tick")
	p := &historyProbe{Mem: ta.m, reads: new(atomic.Int64), workCells: new(atomic.Int64)}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return p, nil }
	full := ta.ok("where --json --cards --rows --archived --landed-series=false")
	require.Positive(t, p.workCells.Load(), "the ordinary full-row path still loads Work")
	both := ta.ok("where --json --cards --rows --critical-rows --archived --landed-series=false")
	assert.JSONEq(t, full, both, "full rows takes precedence when both selectors are supplied")
	p.workCells.Store(0)
	small, err := ta.a.whereJSON("", false)
	require.NoError(t, err)
	assert.Zero(t, p.workCells.Load(), "critical placements never enumerate the full Work table")
	assert.Zero(t, p.reads.Load(), "neither selector scans history")
	var v whereView
	require.NoError(t, json.Unmarshal(small, &v))
	require.NotEmpty(t, v.Critical)
	require.NotEmpty(t, v.Rows)
	assert.LessOrEqual(t, len(v.Rows), 5)
	for _, row := range v.Rows {
		assert.Nil(t, row.Fields)
		assert.NotEmpty(t, row.Stream)
	}
	project := func(body []byte, release string) []byte {
		srv := &sprintdash.Server{Read: func() ([]byte, error) { return body, nil }, Now: ta.a.now, Every: time.Second}
		srv.Refresh()
		return srv.SnapshotOf(release)
	}
	for _, release := range []string{"", "all", "v1.0.0", "v1.1.0"} {
		assert.JSONEq(t, string(project([]byte(full), release)), string(project(small, release)), release)
	}
	// The real HTTP response shares that projection and omits the temporary rows.
	srv := &sprintdash.Server{Read: func() ([]byte, error) { return small, nil }, Now: ta.a.now, Every: time.Second}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
	assert.NotContains(t, w.Body.String(), `"rows"`)
}
