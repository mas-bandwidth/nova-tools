package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// /table is the sprint as where prints it, read in this process on the in-memory store.
func TestDashboardTableIsWhereText(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	want := ta.ok("where")
	page := http.NewServeMux()
	v := &tableView{page: page, read: func() ([]byte, error) { return ta.a.whereText("", false) }, now: ta.a.now, every: time.Second}
	w := httptest.NewRecorder()
	v.ServeHTTP(w, httptest.NewRequest(http.MethodGet, dashboardTablePath, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, want, w.Body.String())
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
}

// The table is read at most once per --every whoever is looking, on the clock the view
// is given; every other path is the page's.
func TestDashboardTableReadsAtMostOncePerEvery(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_790_000_000, 0)
	reads := 0
	page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	v := &tableView{page: page, read: func() ([]byte, error) { reads++; return []byte("table\n"), nil }, now: func() time.Time { return now }, every: time.Second}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		v.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	get(dashboardTablePath)
	get(dashboardTablePath)
	assert.Equal(t, 1, reads, "two looks inside one --every are one read")
	now = now.Add(time.Second)
	assert.Equal(t, "table\n", get(dashboardTablePath).Body.String())
	assert.Equal(t, 2, reads)
	assert.Equal(t, http.StatusTeapot, get("/").Code, "the page is the page's")
	assert.Equal(t, 2, reads)
}

// A where that refuses is a 503 naming its line; a write is refused.
func TestDashboardTableSaysAFailedRead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t) // no init: no sprint here yet
	v := &tableView{page: http.NewServeMux(), read: func() ([]byte, error) { return ta.a.whereText("", false) }, now: ta.a.now, every: time.Second}
	w := httptest.NewRecorder()
	v.ServeHTTP(w, httptest.NewRequest(http.MethodGet, dashboardTablePath, nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "TABLE FAILED: where exited 1: nova-sprint where: this store: no sprint here yet")

	v = &tableView{page: http.NewServeMux(), read: func() ([]byte, error) { return nil, errors.New("unused") }, now: time.Now, every: time.Second}
	w = httptest.NewRecorder()
	v.ServeHTTP(w, httptest.NewRequest(http.MethodPost, dashboardTablePath, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
