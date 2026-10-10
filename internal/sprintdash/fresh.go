package sprintdash

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The live read and its freshness (the owner, 2026-10-04: "I need to be able to always
// trust the dashboard"; "Golang nova-tools and nova-sprint verbs only"): the dashboard
// reads the sprint once a tick whether or not anyone is looking, in one place, and one
// check says when what it serves has gone old. A puller (Upstream) is the same server
// reading another dashboard's copy in place of the sprint, so the public page is the
// private page, a copy behind it.

const (
	// StaleAfterDefault is the age of the served data past which it is stale.
	StaleAfterDefault = 2 * time.Second
	// StaleForDefault is how long the data stays stale before the alarm is raised.
	StaleForDefault = 30 * time.Second
	// UpstreamTimeout bounds a puller's read of the other dashboard.
	UpstreamTimeout = 5 * time.Second
)

// Tick is one tick of the clock's ticker: a read of the sprint when no read is running,
// then the freshness check. Run calls it each tick; a test calls it by hand.
func (s *Server) Tick() {
	s.refresh(s.Every - s.Every/tickSlack)
	s.checkFresh(s.Now())
}

// freshness is the state of the freshness check, under the Server's mu.
type freshness struct {
	started time.Time // the first check: the data's age is from here before any good read
	at      time.Time // the served data's time: when it was read (upstream: when the upstream read it)
	since   time.Time // when the data went stale; zero while it is fresh
	alarmed bool      // the alarm is raised and not yet cleared: once an episode
}

// checkFresh is the freshness check at now: data older than StaleAfter for StaleFor
// raises the alarm, a line on Log, once an episode; data fresh again clears it, one more
// line. While the alarm stands, /healthz answers 503 and /api/sprint says stale.
func (s *Server) checkFresh(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := &s.fresh
	if f.started.IsZero() {
		f.started = now
	}
	from := f.at
	if from.IsZero() {
		from = f.started
	}
	age, after := now.Sub(from), orDefault(s.StaleAfter, StaleAfterDefault)
	if age <= after {
		if f.alarmed {
			s.logf(now, "FRESH again: the served data is %s old; it was older than %s from %s", age.Round(time.Millisecond), after, f.since.Format("3:04:05 PM"))
		}
		f.since, f.alarmed = time.Time{}, false
		return
	}
	if f.since.IsZero() {
		f.since = from.Add(after)
	}
	if forDur := orDefault(s.StaleFor, StaleForDefault); !f.alarmed && now.Sub(f.since) >= forDur {
		f.alarmed = true
		s.logf(now, "ALARM stale: the served data is %s old, older than %s for %s (since %s); the page holds it and /healthz answers 503 until a read is fresh", age.Round(time.Millisecond), after, forDur, f.since.Format("3:04:05 PM"))
	}
}

// Stale is why the served data is not to be trusted now, "" while the alarm is down.
func (s *Server) Stale() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fresh.alarmed {
		return ""
	}
	return fmt.Sprintf("stale: the served data is older than %s since %s", orDefault(s.StaleAfter, StaleAfterDefault), s.fresh.since.UTC().Format(time.RFC3339))
}

// healthz answers ok, or 503 with why while the freshness alarm stands.
func (s *Server) healthz(w http.ResponseWriter) {
	if why := s.Stale(); why != "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, why+"\n") // ignored: a checker gone away asks again
		return
	}
	s.send(w, "text/plain; charset=utf-8", []byte("ok\n"))
}

func orDefault(d, byDefault time.Duration) time.Duration {
	if d <= 0 {
		return byDefault
	}
	return d
}

// Upstream is the dashboard a puller reads its copy from (nova-sprint dashboard --pull
// <url>): URL is that dashboard's /api/sprint. The puller serves the data, its time and
// its throughput as the upstream read them, so the two pages agree, and the puller's
// freshness check sees the upstream's age.
type Upstream struct {
	URL string
	// Client reads the upstream; nil is a client bounded by UpstreamTimeout.
	Client *http.Client
}

// read is the upstream's last good copy: its data and the snapshot it came in. An
// upstream holding a failed read is a failed read here too, so the puller holds the same.
func (u *Upstream) read() ([]byte, *snapshot, error) {
	c := u.Client
	if c == nil {
		c = &http.Client{Timeout: UpstreamTimeout}
	}
	// every stream: the puller shows each release from the whole copy, as a reader does
	at, err := url.Parse(u.URL)
	if err != nil {
		return nil, nil, err
	}
	q := at.Query()
	q.Set("release", AllReleases)
	at.RawQuery = q.Encode()
	resp, err := c.Get(at.String())
	if err != nil {
		return nil, nil, &ReadError{Why: "the upstream dashboard did not answer", Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the body is read to its end or abandoned
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("the upstream dashboard answered %s", resp.Status)
	}
	var snap snapshot
	if json.Unmarshal(body, &snap) != nil {
		return nil, nil, errors.New("the upstream dashboard answered no snapshot JSON")
	}
	if !snap.OK {
		why := "no read yet"
		if snap.Error != nil {
			why = *snap.Error
		}
		return nil, nil, fmt.Errorf("the upstream dashboard holds its last good copy: %s", oneline.Escape(why))
	}
	if snap.FetchedAt == nil {
		return nil, nil, errors.New("the upstream dashboard's snapshot has no fetchedAt")
	}
	return snap.Data, &snap, nil
}
