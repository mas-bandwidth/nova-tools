// Package sprintdash is the sprint dashboard's server (docs/SPEC-SPRINT-DASHBOARD.md):
// one page, embedded in the binary, /api/sprint, a cached copy of the sprint as
// `nova-sprint where --json --cards` prints it, /events, each new copy pushed as it is
// read, and the pull routes (pull.go), a worker's own view of the same copy. The terminal
// table stays the canonical view; this is a second view of the same JSON.
//
// The server is a function of its requests and its clock: Read is how it reads the
// sprint and Now is its clock, so a test drives it with no socket and no real time.
// The server reads the sprint once per Every whoever is looking (Run, on a ticker the
// caller hands it: one poller, its reads back to back with Every as their floor), and
// while Run polls a request only ever answers from the copy. A read that fails holds the
// last good copy, marked ok false with a short reason (never the reader's own error
// text, which goes to Log only), and a read that runs past ReadTimeout is marked so.
// One freshness check (fresh.go) raises an alarm when the served data stays old; a
// puller (From) reads another dashboard's copy instead.
package sprintdash

import (
	"bytes"
	"cmp"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	htmlpkg "html"
	"io"
	"maps"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	// ReadTimeoutDefault is how long a read is waited on before it is marked failed.
	ReadTimeoutDefault = time.Minute
)

// ReadError is a failed read in two parts: Why is served in /api/sprint's error, and
// Detail (what the reader itself printed) goes to Log only, so nothing a read printed
// reaches the page.
type ReadError struct{ Why, Detail string }

func (e *ReadError) Error() string {
	if e.Detail == "" {
		return e.Why
	}
	return e.Why + ": " + e.Detail
}

// readFailureWhy is the part of a failed read's error the page is shown.
func readFailureWhy(err error) string {
	var re *ReadError
	if errors.As(err, &re) {
		return re.Why
	}
	return err.Error()
}

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
	// From is the dashboard a puller reads its copy from in place of Read; nil reads the
	// sprint.
	From *Upstream
	// StaleAfter and StaleFor are the freshness check's: the served data older than
	// StaleAfter for StaleFor raises the alarm; zero is StaleAfterDefault, StaleForDefault.
	StaleAfter, StaleFor time.Duration
	// ReadTimeout is how long a read is waited on before it is marked failed; the next
	// read still waits for it to end. Zero is ReadTimeoutDefault.
	ReadTimeout time.Duration
	// keepaliveTick is a test's keepalive ticker in place of the clock's; nil is the clock.
	keepaliveTick func(time.Duration) (<-chan time.Time, func())
	// readTimer is a test's read timeout in place of the clock's; nil is the clock.
	readTimer func(time.Duration) (<-chan time.Time, func())

	mu      sync.Mutex
	polling bool // Run is the one reader: a request answers from the copy only
	reading bool
	began   time.Time // when the last read began; zero before the first
	snap    snapshot
	copy    *sprintCopy   // the last good read as the pull routes read it; nil before one
	gen     uint64        // the good reads so far: an /events client sends each new one
	changed chan struct{} // closed, and replaced, at each good read
	samples []sample
	stats   readStats
	fresh   freshness
}

// snapshot is /api/sprint's body: the page reads data, throughput,
// throughputMinutes, build and the release fields; the rest says how the reads are going:
// attemptAt and readSeconds are the last read's, good or not, and minInterval is Every.
// Data is the copy as the release shows it (release.go): release is the one shown,
// current the one shown when none is asked, releases every label a stream carries, and
// releaseStreams the streams shown (absent for all).
type snapshot struct {
	OK                bool            `json:"ok"`
	Data              json.RawMessage `json:"data"`
	Release           string          `json:"release,omitempty"`
	Current           string          `json:"current,omitempty"`
	Releases          []string        `json:"releases,omitempty"`
	ReleaseStreams    []string        `json:"releaseStreams,omitempty"`
	FetchedAt         *time.Time      `json:"fetchedAt"`
	AttemptAt         *time.Time      `json:"attemptAt"`
	ReadSeconds       *float64        `json:"readSeconds"`
	MinInterval       float64         `json:"minInterval"`
	Error             *string         `json:"error"`
	Throughput        *float64        `json:"throughput"`
	ThroughputMinutes float64         `json:"throughputMinutes"`
	Build             string          `json:"build"`
	Stale             bool            `json:"stale"`
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
	case "/OFL.txt":
		s.send(w, "text/plain; charset=utf-8", file("OFL.txt"))
	case "/logo":
		if body, err := s.logo(); err == nil {
			s.send(w, logoType(s.Logo, body), body)
		} else {
			http.Error(w, "no logo", http.StatusNotFound)
		}
	case "/favicon.svg":
		if vb, inner, ok := s.svgLogo(); ok {
			s.send(w, "image/svg+xml", []byte(`<svg xmlns="`+svgNS+`" viewBox="`+vb+`" fill="currentColor">`+faviconStyle+inner+`</svg>`))
		} else {
			http.Error(w, "no svg logo", http.StatusNotFound)
		}
	case "/favicon.png", "/logo-icon.png", "/logo-tile-192.png", "/logo-tile-384.png", "/logo.webp", "/logo.png":
		// the raster logo's routes a page or a bookmark may still name: each is the --logo file
		if body, err := s.logo(); err == nil && !isSVG(s.Logo) {
			s.send(w, logoType(s.Logo, body), body)
		} else {
			http.Error(w, "no raster logo", http.StatusNotFound)
		}
	case "/api/sprint":
		s.Refresh()
		s.send(w, "application/json", s.SnapshotOf(r.URL.Query().Get("release")))
	case "/events":
		rel := r.URL.Query().Get("release")
		s.events(w, r, func(*sprintCopy) ([]byte, bool) { return s.SnapshotOf(rel), true })
	case "/healthz":
		s.healthz(w)
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
// read is running; otherwise the cached copy stands. While Run polls it reads nothing
// once a read has begun: the poller is the one reader, and a request answers from the
// copy (the verb makes the first read before it listens, so a page never reads).
func (s *Server) Refresh() {
	s.mu.Lock()
	cacheOnly := s.polling && !s.began.IsZero()
	s.mu.Unlock()
	if !cacheOnly {
		s.refresh(s.Every)
	}
}

// readResult is one read's outcome.
type readResult struct {
	body []byte
	up   *snapshot
	err  error
}

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

	done := make(chan readResult, 1)
	go func() {
		body, up, err := s.read()
		if err == nil {
			err = sprintJSON(body)
		}
		done <- readResult{body, up, err}
	}()
	timeout := orDefault(s.ReadTimeout, ReadTimeoutDefault)
	expired, stop := s.timer(timeout)
	defer stop()
	var res readResult
	select {
	case res = <-done:
	case <-expired:
		s.mu.Lock()
		s.record(start, s.Now(), nil, nil, &ReadError{Why: fmt.Sprintf("the read timed out after %s", timeout)})
		s.mu.Unlock()
		res = <-done // reads never overlap: the next one waits for this one to end
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reading = false
	s.record(start, s.Now(), res.body, res.up, res.err)
}

// timer is the read timeout's: the clock's, or a test's (readTimer).
func (s *Server) timer(d time.Duration) (<-chan time.Time, func()) {
	if s.readTimer != nil {
		return s.readTimer(d)
	}
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// read is one read: the sprint, or a puller's upstream with the snapshot it came in.
func (s *Server) read() ([]byte, *snapshot, error) {
	if s.From != nil {
		return s.From.read()
	}
	body, err := s.Read()
	return body, nil, err
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
// throughput sample (a puller's takes the upstream's time and throughput, up); a failed
// one keeps the copy, and a new failure is logged once.
func (s *Server) record(start, end time.Time, body []byte, up *snapshot, err error) {
	took := end.Sub(start)
	secs := float64(took.Milliseconds()) / 1000
	s.snap.AttemptAt, s.snap.ReadSeconds = &end, &secs
	if err != nil {
		why := oneline.Escape(readFailureWhy(err))
		if s.snap.OK || s.snap.Error == nil || *s.snap.Error != why {
			s.logf(end, "read failed: %s; the page holds the last good copy", oneline.Escape(err.Error()))
		}
		s.snap.OK, s.snap.Error = false, &why
	} else {
		// the samples are the epoch's landed cards: the table's and the archived streams'
		// (stream archive), so an archive, which lands nothing, is not a fall that starts
		// them again; a sprint done (done) carries the epoch's in landed already, so it is
		// landed alone then, and the finish is no spike
		var v struct {
			Landed         int64 `json:"landed"`
			ArchivedLanded int64 `json:"archived_landed"`
			Done           bool  `json:"done"`
		}
		// ignored: sprintJSON has read body as JSON; a landed that is no number is 0
		_ = json.Unmarshal(body, &v)
		at, rate, minutes := end, (*float64)(nil), 0.0
		if up != nil {
			at, rate, minutes = *up.FetchedAt, up.Throughput, up.ThroughputMinutes
		} else {
			epoch := v.Landed
			if !v.Done {
				epoch += v.ArchivedLanded
			}
			rate, minutes = s.sampleLanded(start, epoch)
		}
		s.fresh.at = at
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
		s.snap.Data = placed(bytes.TrimSpace(body))
		s.snap.FetchedAt, s.snap.Throughput, s.snap.ThroughputMinutes = &at, rate, minutes
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

// Snapshot is /api/sprint's body now, with the build number, as the default release shows it.
func (s *Server) Snapshot() []byte { return s.SnapshotOf("") }

// SnapshotOf is /api/sprint's body now as release shows it (release.go): "" is the
// default, all is every stream.
func (s *Server) SnapshotOf(release string) []byte {
	build := s.Build()
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snap
	snap.Build, snap.Stale, snap.MinInterval = build, s.fresh.alarmed, s.Every.Seconds()
	if len(snap.Data) > 0 {
		v := viewOf(snap.Data, release)
		v.Data = fixView(reviewSplit(v.Data))
		snap.Data, snap.Release, snap.Current, snap.Releases, snap.ReleaseStreams = v.Data, v.Release, v.Current, v.Releases, v.Streams
	}
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

// logoType is the logo's media type: a raster image's own magic bytes decide first (a
// webp named .png is image/webp), else its name's extension, else the bytes' own answer.
func logoType(name string, body []byte) string {
	if t := http.DetectContentType(body); strings.HasPrefix(t, "image/") {
		return t
	}
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); strings.HasPrefix(t, "image/") {
		return t
	}
	return http.DetectContentType(body)
}

// svgNS is the svg element's namespace, a name and never fetched.
const svgNS = "http://www.w3.org/2000/svg"

// faviconStyle colours an svg favicon for the browser's light or dark theme.
const faviconStyle = `<style>svg{color:#121417}@media (prefers-color-scheme:dark){svg{color:#eef0f3}}</style>`

var (
	svgRe     = regexp.MustCompile(`(?is)<svg\b([^>]*)>(.*)</svg>`)
	viewBoxRe = regexp.MustCompile(`viewBox\s*=\s*"([^"]+)"`)
)

func isSVG(name string) bool { return strings.EqualFold(filepath.Ext(name), ".svg") }

// svgLogo is an svg logo's drawing, read now: its viewBox (escaped) and its inner
// markup; false when the logo is no svg or has no <svg> element.
func (s *Server) svgLogo() (viewBox, inner string, ok bool) {
	if !isSVG(s.Logo) {
		return "", "", false
	}
	body, err := s.logo()
	if err != nil {
		return "", "", false
	}
	m := svgRe.FindSubmatch(body)
	if m == nil {
		return "", "", false
	}
	viewBox = "0 0 32 32"
	if v := viewBoxRe.FindSubmatch(m[1]); v != nil {
		viewBox = string(v[1])
	}
	return htmlpkg.EscapeString(viewBox), string(m[2]), true
}

// index is the page with its script versioned by the build and the logo slot and
// favicon filled when a logo is given and readable now: an svg logo is drawn inline in
// the page's text colour and is the favicon at /favicon.svg; any other image is /logo.
func (s *Server) index() []byte {
	build := s.Build()
	html := strings.Replace(string(file("index.html")), `src="app.js"`, `src="app.js?v=`+build+`"`, 1)
	slot, icon := "", ""
	if vb, inner, ok := s.svgLogo(); ok {
		slot = `<svg id="logo" viewBox="` + vb + `" width="32" height="32" fill="currentColor" aria-hidden="true">` + inner + `</svg>`
		icon = `<link rel="icon" type="image/svg+xml" href="/favicon.svg?v=` + build + `">`
	} else if _, err := s.logo(); err == nil {
		slot = `<img id="logo" class="logo-tile" src="/logo?v=` + build + `" alt="">`
		icon = `<link rel="icon" href="/logo?v=` + build + `"><link rel="apple-touch-icon" href="/logo?v=` + build + `">`
	}
	html = strings.Replace(html, "<!--LOGO-->", slot, 1)
	html = strings.Replace(html, "<!--FAVICON-->", icon, 1)
	return []byte(html)
}

// The fix state (docs/SPEC-SPRINT-DASHBOARD.md, "Fix"; the owner, 2026-10-07 5:58-6:01 PM ET:
// "I would like the cards that are awaiting rework to be purple", "between review and
// merging"): the view marks the cards awaiting rework on the copy it serves, so the page draws
// them purple. A dealt card is at fix when its level is fix (the fix level, card
// a-rework-is-priority-fix-bb), or, until where prints that level, when it is an attempt after
// the first (its attempt field, else its id's .w<n>) and not blocker or critical, which keep
// their red. The marks: fix true on the card; a fleet or friends row's fix, its fix cards
// working (where's fix_working when it prints one); a work row's fix, its primaries at fix,
// taken off its working, where a primary sent out again sits (lifecycle: review -> working on
// rework), unless where prints the row's fix itself; priorities' fix list, those primaries,
// off high and low; and the copy's fix, the work rows' summed. Every mark is absent when it
// is zero, so a copy with no card at fix is served exactly as where prints it.

// workAttempt reads a work card's attempt off its id: "ci-03.w2" is 2.
var workAttempt = regexp.MustCompile(`\.w(\d+)$`)

// urgentLevel is a level whose red a card keeps on any attempt: blocker, or a critical,
// set or by weight.
func urgentLevel(level string) bool {
	return level == "blocker" || strings.HasPrefix(level, "critical")
}

// fixCard is what the view reads of a dealt card.
type fixCard struct {
	ID       string `json:"id"`
	Primary  string `json:"primary"`
	Stream   string `json:"stream"`
	Member   string `json:"member"`
	State    string `json:"state"`
	Priority string `json:"priority"`
	Attempt  int    `json:"attempt"`
}

// atFix is whether the card c, whose primary is listed at listed (where's priorities), is at fix.
func (c fixCard) atFix(listed string) bool {
	level := cmp.Or(c.Priority, listed)
	// An explicit priority is authoritative once the server supports the ladder.
	// In particular, keep/high rework policies must not become fix by attempt.
	if c.Priority != "" {
		return c.Priority == "fix"
	}
	if level == "fix" {
		return true
	}
	attempt := c.Attempt
	if m := workAttempt.FindStringSubmatch(c.ID); attempt == 0 && m != nil {
		attempt, _ = strconv.Atoi(m[1]) // ignored: \d+ parses
	}
	return attempt >= 2 && !urgentLevel(level)
}

// cellCount is a table cell's number: the string where prints, or a number.
func cellCount(v any) int {
	switch n := v.(type) {
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n)) // ignored: not a number is none
		return i
	case float64:
		return int(n)
	}
	return 0
}

// cellOf is n written as the cell was: a number stays a number, else the string where prints.
func cellOf(was any, n int) any {
	if _, num := was.(float64); num {
		return n
	}
	return strconv.Itoa(n)
}

// fixView is the copy body with the cards awaiting rework marked (above); body itself when
// nothing is at fix or it is no JSON object.
func fixView(body json.RawMessage) json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil {
		return body
	}
	var cards []map[string]json.RawMessage
	var read []fixCard
	// ignored: no cards, or cards of another shape, is none at fix; the two reads are the same list
	if json.Unmarshal(top["cards"], &cards) != nil || json.Unmarshal(top["cards"], &read) != nil {
		cards, read = nil, nil
	}
	var prio map[string][]string
	_ = json.Unmarshal(top["priorities"], &prio) // ignored: no priorities is every card normal
	listed := map[string]string{}
	for _, level := range slices.Sorted(maps.Keys(prio)) {
		for _, id := range prio[level] {
			if urgentLevel(level) || listed[id] == "" {
				listed[id] = level
			}
		}
	}
	var tables map[string]json.RawMessage
	_ = json.Unmarshal(top["tables"], &tables) // ignored: sprintJSON has read the tables
	rows := map[string]map[string]map[string]any{}
	for _, t := range []string{"work", "fleet", "friends"} {
		var r map[string]map[string]any
		if json.Unmarshal(tables[t], &r) == nil && r != nil {
			rows[t] = r
		}
	}

	working := map[[2]string]int{}            // the fix cards working, by table and row
	primaries := map[string]map[string]bool{} // the primaries at fix, by stream
	fixIDs := slices.Clone(prio["fix"])
	marked := false
	for i, c := range read {
		raw := cards[i]
		if !c.atFix(listed[cmp.Or(c.Primary, c.ID)]) {
			continue
		}
		marked = true
		raw["fix"] = json.RawMessage("true")
		if c.State == "working" {
			if f, ok := strings.CutPrefix(c.Member, "friend."); ok {
				working[[2]string{"friends", f}]++
			} else {
				working[[2]string{"fleet", c.Member}]++
			}
		}
		p := cmp.Or(c.Primary, c.ID)
		if primaries[c.Stream] == nil {
			primaries[c.Stream] = map[string]bool{}
		}
		primaries[c.Stream][p] = true
		if !slices.Contains(fixIDs, p) {
			fixIDs = append(fixIDs, p)
		}
	}
	for _, t := range []string{"fleet", "friends"} {
		for name, row := range rows[t] {
			if _, has := row["fix_working"]; has {
				working[[2]string{t, name}] = cellCount(row["fix_working"])
			}
		}
	}
	for k, n := range working {
		if n > 0 && rows[k[0]][k[1]] != nil {
			rows[k[0]][k[1]]["fix"] = strconv.Itoa(n)
			marked = true
		}
	}
	total := 0
	for stream, row := range rows["work"] {
		if v, has := row["fix"]; has { // where's own count: its columns already leave these out
			total += cellCount(v)
			continue
		}
		n := min(len(primaries[stream]), cellCount(row["working"]))
		if n > 0 {
			row["working"] = cellOf(row["working"], cellCount(row["working"])-n)
			row["fix"] = cellOf(row["working"], n)
			total += n
		}
	}
	if !marked && total == 0 {
		return body
	}
	if cards != nil {
		top["cards"] = mustJSON(cards)
	}
	for t, r := range rows {
		tables[t] = mustJSON(r)
	}
	top["tables"] = mustJSON(tables)
	if total > 0 {
		top["fix"] = mustJSON(total)
	}
	if len(fixIDs) > 0 {
		slices.Sort(fixIDs)
		if prio == nil {
			prio = map[string][]string{}
		}
		for level, ids := range prio {
			if urgentLevel(level) || level == "fix" {
				continue
			}
			prio[level] = slices.DeleteFunc(ids, func(id string) bool { return slices.Contains(fixIDs, id) })
			if len(prio[level]) == 0 {
				delete(prio, level)
			}
		}
		prio["fix"] = fixIDs
		top["priorities"] = mustJSON(prio)
	}
	return mustJSON(top)
}
