// Package sprintdash is the sprint dashboard's server (docs/SPEC-SPRINT-DASHBOARD.md):
// one page, embedded in the binary, /api/sprint, a cached copy of the sprint as
// `nova-sprint where --json --cards` prints it, /events, each new copy pushed as it is
// read, and the pull routes (pull.go), a worker's own view of the same copy. The terminal
// table stays the canonical view; this is a second view of the same JSON.
//
// The server is a function of its requests and its clock: Read is how it reads the
// sprint and Now is its clock, so a test drives it with no socket and no real time.
// The server reads the sprint at most once per Every, and only while a page or a puller
// asks or an event stream is open (Run, on a ticker the caller hands it). A read that
// fails holds the last good copy: the page changes nothing and says nothing, and the
// failure is a line on Log.
package sprintdash

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// page is the page's files: index.html, app.js, the wordmark's face and its licence.
//
//go:embed page/index.html page/app.js page/nunito-800.woff2 page/OFL.txt
var page embed.FS

const (
	// Window is how far back the throughput's samples reach.
	Window = time.Hour
	// MinSpan is the samples throughput needs before it shows: until then it is null.
	MinSpan = 10 * time.Minute
	// LogEvery is the time between the read-time summary lines on Log.
	LogEvery = time.Minute
)

// Server serves the page and the sprint's cached copy. Read, Now and Every are
// required; the rest may be left zero.
type Server struct {
	// Read reads the sprint once: the bytes `where --json` prints. It is bounded by its
	// own transport (the sprint server's client, or the store's connection).
	Read func() ([]byte, error)
	// Now is the server's clock.
	Now func() time.Time
	// Every is the least time between two reads' starts.
	Every time.Duration
	// Logo is the image file served as the logo and the favicon; "" is none, and
	// the page's logo slot renders nothing.
	Logo string
	// Version is the binary's version, part of the build number.
	Version string
	// Log takes a line per new read failure and a read-time summary a minute.
	Log io.Writer
	// Keepalive is the time between two keepalive comments on an idle /events stream;
	// zero is KeepaliveDefault.
	Keepalive time.Duration
	// keepaliveTick is a test's keepalive ticker in place of the clock's; nil is the clock.
	keepaliveTick func(time.Duration) (<-chan time.Time, func())

	mu      sync.Mutex
	reading bool
	began   time.Time // when the last read began; zero before the first
	snap    snapshot
	copy    *sprintCopy   // the last good read as the pull routes read it; nil before one
	gen     uint64        // the good reads so far: an /events client sends each new one
	changed chan struct{} // closed, and replaced, at each good read
	streams int           // the /events clients connected
	samples []sample
	stats   readStats
}

// snapshot is /api/sprint's body: the page reads data, throughput,
// throughputMinutes and build; the rest says how the reads are going.
type snapshot struct {
	OK                bool            `json:"ok"`
	Data              json.RawMessage `json:"data"`
	FetchedAt         *time.Time      `json:"fetchedAt"`
	Error             *string         `json:"error"`
	Throughput        *float64        `json:"throughput"`
	ThroughputMinutes float64         `json:"throughputMinutes"`
	Build             string          `json:"build"`
}

// sample is one good read's landed count and when it began.
type sample struct {
	at     time.Time
	landed int64
}

// readStats is the reads since the last summary line.
type readStats struct {
	since     time.Time
	n, failed int
	sum, max  time.Duration
}

// ServeHTTP answers every path the page uses; every answer is no-store.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, max-age=0")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")
	switch r.URL.Path {
	case "/", "/index.html":
		s.send(w, "text/html; charset=utf-8", s.index())
	case "/app.js":
		s.send(w, "text/javascript; charset=utf-8", file("app.js"))
	case "/nunito-800.woff2":
		s.send(w, "font/woff2", file("nunito-800.woff2"))
	case "/logo":
		if body, err := s.logo(); err == nil {
			s.send(w, logoType(s.Logo, body), body)
		} else {
			http.Error(w, "no logo", http.StatusNotFound)
		}
	case "/api/sprint":
		s.Refresh()
		s.send(w, "application/json", s.Snapshot())
	case "/events":
		s.events(w, r, func(*sprintCopy) ([]byte, bool) { return s.Snapshot(), true })
	case "/healthz":
		s.send(w, "text/plain; charset=utf-8", []byte("ok\n"))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) send(w http.ResponseWriter, ctype string, body []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	_, _ = w.Write(body) // ignored: a page gone away asks again in a second
}

// file is an embedded file of the page; every name asked for is embedded.
func file(name string) []byte {
	b, err := page.ReadFile("page/" + name)
	if err != nil {
		panic("dashboard: " + name + " is not embedded")
	}
	return b
}

// Refresh reads the sprint when Every has passed since the last read began and no
// read is running; otherwise the cached copy stands.
func (s *Server) Refresh() { s.refresh(s.Every) }

// refresh reads the sprint when gap has passed since the last read began and no read is
// running.
func (s *Server) refresh(gap time.Duration) {
	s.mu.Lock()
	start := s.Now()
	if s.reading || !s.began.IsZero() && start.Sub(s.began) < gap {
		s.mu.Unlock()
		return
	}
	s.reading, s.began = true, start
	s.mu.Unlock()

	body, err := s.Read()
	if err == nil {
		err = sprintJSON(body)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reading = false
	s.record(start, s.Now(), body, err)
}

// sprintJSON is why body is not the sprint's JSON, nil when it is.
func sprintJSON(body []byte) error {
	var v struct {
		Tables json.RawMessage `json:"tables"`
	}
	if json.Unmarshal(body, &v) != nil {
		return errors.New("where printed no JSON")
	}
	if len(v.Tables) == 0 || string(v.Tables) == "null" {
		return errors.New("where JSON has no tables")
	}
	return nil
}

// record keeps a read's outcome, under mu: a good read replaces the copy and adds a
// throughput sample; a failed one keeps the copy, and a new failure is logged once.
func (s *Server) record(start, end time.Time, body []byte, err error) {
	took := end.Sub(start)
	if err != nil {
		why := oneline.Escape(err.Error())
		if s.snap.OK || s.snap.Error == nil || *s.snap.Error != why {
			s.logf(end, "read failed: %s; the page holds the last good copy", why)
		}
		s.snap.OK, s.snap.Error = false, &why
	} else {
		var v struct {
			Landed int64 `json:"landed"`
		}
		// ignored: sprintJSON has read body as JSON; a landed that is no number is 0
		_ = json.Unmarshal(body, &v)
		rate, minutes := s.sampleLanded(start, v.Landed)
		var c sprintCopy
		// ignored: sprintJSON has read body as JSON; a field of another shape is left zero
		_ = json.Unmarshal(body, &c)
		s.copy = &c
		s.gen++
		if s.changed != nil {
			close(s.changed)
		}
		s.changed = make(chan struct{})
		s.snap.OK, s.snap.Error = true, nil
		s.snap.Data = append(json.RawMessage(nil), bytes.TrimSpace(body)...)
		s.snap.FetchedAt, s.snap.Throughput, s.snap.ThroughputMinutes = &end, rate, minutes
	}
	s.summarize(end, took, err != nil)
}

// sampleLanded adds a sample and is the cards landed per hour over the samples of the
// last Window (one decimal), nil until they span MinSpan, with the minutes they span.
// A landed count lower than the last (a cleared sprint) starts the samples again.
func (s *Server) sampleLanded(at time.Time, landed int64) (*float64, float64) {
	if n := len(s.samples); n > 0 && landed < s.samples[n-1].landed {
		s.samples = s.samples[:0]
	}
	s.samples = append(s.samples, sample{at, landed})
	drop := 0
	for drop < len(s.samples) && at.Sub(s.samples[drop].at) > Window {
		drop++
	}
	s.samples = s.samples[drop:]
	span := at.Sub(s.samples[0].at)
	minutes := round1(span.Minutes())
	if span < MinSpan {
		return nil, minutes
	}
	rate := round1(float64(landed-s.samples[0].landed) * float64(time.Hour) / float64(span))
	return &rate, minutes
}

func round1(f float64) float64 {
	if f < 0 {
		return -round1(-f)
	}
	return float64(int64(f*10+0.5)) / 10
}

// summarize counts a read and writes the summary line once LogEvery has passed.
func (s *Server) summarize(now time.Time, took time.Duration, failed bool) {
	st := &s.stats
	if st.since.IsZero() {
		st.since = now
	}
	st.n++
	st.sum += took
	st.max = max(st.max, took)
	if failed {
		st.failed++
	}
	if now.Sub(st.since) < LogEvery {
		return
	}
	s.logf(now, "reads=%d failed=%d read_s mean=%.3f max=%.3f last=%.3f",
		st.n, st.failed, (st.sum / time.Duration(st.n)).Seconds(), st.max.Seconds(), took.Seconds())
	*st = readStats{since: now}
}

func (s *Server) logf(at time.Time, format string, args ...any) {
	if s.Log != nil {
		fmt.Fprintf(s.Log, "DASHBOARD %s %s\n", at.Format("2006-01-02 3:04:05 PM"), fmt.Sprintf(format, args...))
	}
}

// Snapshot is /api/sprint's body now, with the build number.
func (s *Server) Snapshot() []byte {
	build := s.Build()
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snap
	snap.Build = build
	b, err := json.Marshal(snap)
	if err != nil {
		panic("dashboard: the snapshot does not marshal: " + err.Error())
	}
	return b
}

// pageSum is the build number's part from the embedded files: it changes with the binary.
var pageSum = func() uint32 {
	h := crc32.NewIEEE()
	for _, n := range []string{"index.html", "app.js", "nunito-800.woff2"} {
		h.Write(file(n))
	}
	return h.Sum32()
}()

// Build is the build number: it changes with the page's files, the version, and the
// logo file (its name, size and time), and an open page reloads itself when it does.
func (s *Server) Build() string {
	sig := fmt.Sprintf("%08x|%s", pageSum, s.Version)
	if s.Logo != "" {
		if fi, err := os.Stat(s.Logo); err == nil {
			sig += fmt.Sprintf("|%s:%d:%d", s.Logo, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(sig)))
}

func (s *Server) logo() ([]byte, error) {
	if s.Logo == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(s.Logo)
}

// logoType is the logo's media type: by its name's extension, else by its bytes.
func logoType(name string, body []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); strings.HasPrefix(t, "image/") {
		return t
	}
	return http.DetectContentType(body)
}

// index is the page with its script versioned by the build and the logo slot and
// favicon filled when a logo is given and readable now.
func (s *Server) index() []byte {
	build := s.Build()
	html := strings.Replace(string(file("index.html")), `src="app.js"`, `src="app.js?v=`+build+`"`, 1)
	slot, icon := "", ""
	if _, err := s.logo(); err == nil {
		slot = `<img id="logo" class="logo-tile" src="/logo?v=` + build + `" alt="">`
		icon = `<link rel="icon" href="/logo?v=` + build + `"><link rel="apple-touch-icon" href="/logo?v=` + build + `">`
	}
	html = strings.Replace(html, "<!--LOGO-->", slot, 1)
	html = strings.Replace(html, "<!--FAVICON-->", icon, 1)
	return []byte(html)
}
