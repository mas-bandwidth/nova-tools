package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// dashboardTablePath is the live table's view on the dashboard's page listeners: the
// sprint as `nova-sprint where` prints it, as text, read at most once per --every whoever
// is looking. It is what one coordinator's sprint-table-live loop wrote to a file in its
// session directory (docs/COORDINATOR-TOOLS.md); a session reads it with curl.
const dashboardTablePath = "/table"

// tableView serves /table and hands every other path to page.
type tableView struct {
	page  http.Handler
	read  func() ([]byte, error)
	now   func() time.Time
	every time.Duration

	mu   sync.Mutex
	at   time.Time
	text []byte
	err  error
}

func (v *tableView) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != dashboardTablePath {
		v.page.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "the table is read with GET", http.StatusMethodNotAllowed)
		return
	}
	text, err := v.table()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "TABLE FAILED: %s\n", err)
		return
	}
	_, _ = w.Write(text) // ignored: a reader that went away asks again
}

// table is the cached text, read again once every has passed since the last read.
func (v *tableView) table() ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if now := v.now(); v.at.IsZero() || now.Sub(v.at) >= v.every {
		v.at = now
		v.text, v.err = v.read()
	}
	return v.text, v.err
}

// whereText reads the sprint as `nova-sprint where` prints it, in this process, as
// whereJSON does.
func (a *app) whereText(addr string, given bool) ([]byte, error) {
	argv := []string{"where"}
	if given {
		argv = append(argv, "--redis", addr)
	}
	var out, errb bytes.Buffer
	if code := a.run(argv, &out, &errb); code != 0 {
		why := fmt.Sprintf("where exited %d", code)
		if line, _, _ := strings.Cut(strings.TrimSpace(errb.String()), "\n"); line != "" {
			why += ": " + line
		}
		return nil, errors.New(why)
	}
	return out.Bytes(), nil
}
