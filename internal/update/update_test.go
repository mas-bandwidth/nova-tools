package update

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var helperTiming func([]string) bool

func TestHelperProcess(t *testing.T) {
	t.Parallel()

	if os.Getenv("NOVA_UPDATE_HELPER") != "1" {
		return
	}
	a := os.Args
	for len(a) > 0 && a[0] != "--" {
		a = a[1:]
	}
	if len(a) < 2 {
		os.Exit(22)
	}
	a = a[1:]
	if p := os.Getenv("NOVA_UPDATE_CALLS"); p != "" {
		f, _ := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		fmt.Fprintln(f, strings.Join(a, " "))
		f.Close()
	}
	switch a[0] {
	case "print":
		b, _ := base64.StdEncoding.DecodeString(a[1])
		fmt.Print(string(b))
	case "stderr":
		b, _ := base64.StdEncoding.DecodeString(a[1])
		fmt.Fprint(os.Stderr, string(b))
	case "fail":
		fmt.Print("v9.9.9\n")
		os.Exit(3)
	case "huge":
		fmt.Println("v1.2.3")
		for i := 0; i < 2048; i++ {
			fmt.Print(strings.Repeat("x", 1024))
		}
	case "read":
		b, e := os.ReadFile(a[1])
		if e != nil {
			os.Exit(4)
		}
		fmt.Print(string(b))
	case "write":
		if os.WriteFile(a[1], []byte(a[2]+"\n"), 0600) != nil {
			os.Exit(4)
		}
	case "args":
		fmt.Print(strings.Join(a[1:], "|"))
	case "linger":
		// Print the version, then leave a grandchild holding stdout open and
		// exit. This is what a real version command that starts a helper and
		// does not wait for it does, and the pipe stays open after the command
		// a caller named is gone.
		b, _ := base64.StdEncoding.DecodeString(a[1])
		fmt.Print(string(b))
		c := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "hold", a[2])
		c.Env = append(os.Environ(), "NOVA_UPDATE_HELPER=1")
		c.Stdout = os.Stdout
		if c.Start() != nil {
			os.Exit(5)
		}
	case "flood":
		// Print a version, then a bounded but substantial stream, and exit at
		// once. This is the healthy child a deadline must still drain in full.
		n, err := strconv.Atoi(a[1])
		if err != nil {
			os.Exit(6)
		}
		fmt.Println("x 1.2.3")
		chunk := strings.Repeat("y", 1024)
		for i := 0; i < n; i++ {
			fmt.Print(chunk)
		}
	default:
		if helperTiming == nil || !helperTiming(a) {
			os.Exit(20)
		}
	}
	os.Exit(0)
}
func command(t *testing.T, action string, a ...string) string {
	t.Helper()
	t.Setenv("NOVA_UPDATE_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return strings.Join(append([]string{os.Args[0], "-test.run=TestHelperProcess", "--", action}, a...), " ")
}
func printer(t *testing.T, s string) string {
	return command(t, "print", base64.StdEncoding.EncodeToString([]byte(s)))
}
func manifest(t *testing.T, rows ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "versions.tsv")
	if e := os.WriteFile(p, []byte(Header+"\n"+strings.Join(rows, "\n")+"\n"), 0600); e != nil {
		require.NoError(t, e, e)
	}
	return p
}
func row(name, kind, installed, latest, apply string) string {
	return strings.Join([]string{name, kind, installed, latest, apply, "fixture-owner"}, "\t")
}
func run(t *testing.T, env Environment, a ...string) (int, string, string) {
	t.Helper()
	var out, err bytes.Buffer
	c := Run("nova-update", a, "", &out, &err, env)
	return c, out.String(), err.String()
}
func need(t *testing.T, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			require.Containsf(t, s, w, "missing %q in:\n%s", w, s)
		}
	}
}
func TestManifestRefusesBeforeAnyProcess(t *testing.T) {
	t.Parallel()

	good := row("x", "tool", "example version", "github:o/r", "none")
	for _, s := range []string{"", good + "\n", Header + "\nx\n", Header + "\n" + strings.Replace(good, "example version", "example  version", 1), Header + "\n" + strings.Replace(good, "tool", "weights", 1), Header + "\n" + good + "\n" + good, Header + "\n" + strings.Replace(good, "example version", "\"space path\" version", 1)} {
		if _, e := Load(strings.NewReader(s)); e == nil {
			require.Errorf(t, e, "accepted %q", s)
		}
	}
	es, e := Load(strings.NewReader(Header + "\n# comment\n" + good + "\n"))
	if e != nil || len(es) != 1 {
		require.Failf(t, "", "%v %v", es, e)
	}
}
func TestWholeVersionAndVerifiedOrder(t *testing.T) {
	t.Parallel()

	examples := map[string]string{"gh version 2.100.0 (2026-09-03)": "2.100.0", "go version go1.27.1 darwin/arm64": "1.27.1", "ollama version is 0.33.3": "0.33.3", "v1.3.2": "1.3.2", "1.18.30": "1.18.30", "codex-cli 0.153.4": "0.153.4", "0.46.0": "0.46.0", "sops 3.13.3": "3.13.3", "git version 2.55.0": "2.55.0", "v26.8.2": "26.8.2", "nova-bus v0.12.1-0.20260912135226-0459069+dirty darwin/arm64 go1.27.1": "0.12.1-0.20260912135226-0459069+dirty"}
	for line, want := range examples {
		r := identity(Entry{Kind: "tool"}, line+"\nsecond 900.0.0", true)
		if r.Version != want || r.Raw != line || !r.Known() {
			require.Failf(t, "", "%q: %+v", line, r)
		}
	}
	for _, line := range []string{"nova-wake devel darwin/arm64 go1.27.1", "nova-merge 0459069"} {
		r := identity(Entry{Kind: "tool"}, line, true)
		if !r.Known() || r.Version != "" {
			require.Fail(t, fmt.Sprintln(r))
		}
		r = identity(Entry{Kind: "tool"}, line, false)
		if r.Reason != "no_release_identity" {
			require.EqualValues(t, "no_release_identity", r.Reason, r)
		}
	}
	for _, c := range [][3]string{{"1.9.0", "1.10.0", "OLDER"}, {"1.10.0", "1.9.0", "NEWER"}, {"1.9", "1.9.0", "DIFFERENT"}, {"1.09.0", "1.9.0", "DIFFERENT"}, {"1.0.0-rc1", "1.0.0", "DIFFERENT"}, {"0.12.0", "0.12.1-0.1-abc", "DIFFERENT"}, {"999999999999999999999999.0", "1000000000000000000000000.0", "OLDER"}} {
		if got := Compare(c[0], c[1]); got != c[2] {
			require.EqualValuesf(t, c[2], got, "%v got %s", c, got)
		}
	}
}
func TestModelDigestAndPinIdentity(t *testing.T) {
	t.Parallel()

	r := identity(Entry{Name: "model:tag", Kind: "model"}, "NAME ID SIZE\nmodel:other ffffffffffff 4GB\nmodel:tag 07d35212591f 4GB\n", true)
	if r.Version != "07d35212591f" || r.Raw != "model:tag 07d35212591f 4GB" {
		require.Fail(t, fmt.Sprintln(r))
	}
	if identity(Entry{Name: "model", Kind: "model"}, "model:tag 07d35212591f 4GB", true).Known() {
		require.Fail(t, fmt.Sprintln("untagged match"))
	}
	for _, s := range []string{"v0.12.0", "devel", "v0.12.1-0.foo"} {
		r := entryRead{Entry: Entry{Kind: "pin"}, Installed: identity(Entry{Kind: "pin"}, "nova-wake "+s, false), Latest: identity(Entry{Kind: "pin"}, "nova-bus "+s, false)}
		if v, _ := verdict(r); v != "EQUAL" {
			require.EqualValues(t, "EQUAL", v, r)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(handler func(http.ResponseWriter, *http.Request)) (*http.Client, func()) {
	server := httptest.NewServer(http.HandlerFunc(handler))
	c := defaultClient()
	c.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(server.URL, "http://")
		copy.URL = &u
		return http.DefaultTransport.RoundTrip(copy)
	})
	return c, server.Close
}
func TestLatestSourcesFallbackBoundsAndFailures(t *testing.T) {
	t.Parallel()

	var calls []string
	client, close := testClient(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.WriteHeader(404)
		case strings.HasSuffix(r.URL.Path, "/tags"):
			fmt.Fprint(w, `[{"name":"v0.11.0"}]`)
		case strings.HasSuffix(r.URL.Path, "/latest"):
			fmt.Fprint(w, `{"version":"v1.2.3"}`)
		case strings.Contains(r.URL.Path, "/formula/"):
			fmt.Fprint(w, `{"versions":{"stable":"v2.3.4"}}`)
		default:
			fmt.Fprint(w, `{"schemaVersion":2,"layers":[]}`)
		}
	})
	defer close()
	for _, tc := range [][3]string{{"github:o/r", "tool", "0.11.0"}, {"npm:package", "tool", "1.2.3"}, {"brew:formula", "tool", "2.3.4"}} {
		r := Latest(context.Background(), Entry{Kind: tc[1], Latest: tc[0]}, time.Second, client)
		if !r.Known() || r.Version != tc[2] {
			require.Fail(t, fmt.Sprintln(r))
		}
	}
	if len(calls) != 4 || calls[1] != "/repos/o/r/tags?per_page=1" {
		require.Fail(t, fmt.Sprintln(calls))
	}
	r := Latest(context.Background(), Entry{Kind: "model", Latest: "ollama:model:tag"}, time.Second, client)
	if r.Version != shaText(`{"schemaVersion":2,"layers":[]}`)[:12] {
		require.Fail(t, fmt.Sprintln(r))
	}
	for _, tc := range []struct {
		status     int
		body, want string
	}{{500, "{}", "HTTP 500"}, {429, "{}", "HTTP 429"}, {200, "{}", "shape"}, {200, "bad", "shape"}, {200, strings.Repeat("x", HTTPCap), "output"}, {404, "{}", "tag_not_found"}} {
		c, done := testClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-ratelimit-reset", "123")
			w.WriteHeader(tc.status)
			fmt.Fprint(w, tc.body)
		})
		r := Latest(context.Background(), Entry{Kind: "model", Latest: "ollama:model:tag"}, time.Second, c)
		done()
		if r.Reason != tc.want {
			require.EqualValuesf(t, tc.want, r.Reason, "%s: %+v", tc.want, r)
		}
		if tc.status == 429 {
			need(t, r.Remedy, "123")
		}
	}
	c, done := testClient(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, r.URL.Path+"x", 302) })
	r = Latest(context.Background(), Entry{Kind: "tool", Latest: "npm:pkg"}, time.Second, c)
	done()
	if r.Known() {
		require.Fail(t, fmt.Sprintln("redirect loop accepted"))
	}
}
func TestReportNeverReadsLatestAndPartialIsVisible(t *testing.T) {
	p := manifest(t, row("good", "tool", printer(t, "tool v1.2.3-rc1+dirty\n"), "github:o/r", "none"), row("bad", "tool", "nova-version-no-such-binary", "npm:unused", "none"))
	env := Environment{Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		assert.Fail(t, fmt.Sprintln("report used HTTP"))
		return nil, fmt.Errorf("forbidden")
	})}}
	code, out, errs := run(t, env, "report", "--file", p, "--host", "air")
	if code != 1 {
		require.EqualValues(t, 1, code, code)
	}
	need(t, errs, "host=air", "REPORT TOOL name=good", "version=1.2.3-rc1+dirty", "REPORT UNKNOWN name=bad", "not_found")
	need(t, errs, "REPORT FAIL checked=2 known=1 unknown=1")
	code, out, errs = run(t, env, "report", "--file", p, "--draft", "--as", "fixture", "--to", "integrator")
	if code != 1 || !strings.HasPrefix(out, "From: fixture\nTo: integrator\nSubject: versions on - at ") {
		require.Failf(t, "", "%d %s %s", code, out, errs)
	}
}
func TestApplyOnlyNamedEntryAndExactTarget(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	os.WriteFile(state, []byte("1.0.0\n"), 0600)
	read := command(t, "read", state)
	write := command(t, "write", state, "{version}")
	p := manifest(t, row("x", "tool", read, "local:"+printer(t, "v1.2.0\n"), write), row("model:tag", "model", "should-not-run", "ollama:model:tag", write))
	for _, a := range [][]string{{"apply", "--file", p}, {"apply", "--file", p, "wrong"}, {"apply", "--file", p, "model:tag"}, {"apply", "--file", p, "--all"}} {
		c, _, _ := run(t, Environment{}, a...)
		if c != 2 {
			require.EqualValues(t, 2, c, fmt.Sprintln(a, c))
		}
	}
	c, out, err := run(t, Environment{}, "apply", "--file", p, "x", "--version", "1.2.0")
	if c != 0 {
		require.EqualValuesf(t, 0, c, "%d %s %s", c, out, err)
	}
	need(t, out, "APPLY BEFORE", "installed=1.0.0", "APPLY AFTER", "installed=1.2.0", "APPLY OK")
	p = manifest(t, row("x", "tool", read, "local:"+printer(t, "1.3.0"), command(t, "write", state, "1.1.1")))
	c, _, err = run(t, Environment{}, "apply", "--file", p, "x")
	if c != 1 {
		require.EqualValues(t, 1, c, c)
	}
	need(t, err, "installed 1.1.1, asked 1.3.0")
	calls := filepath.Join(dir, "calls")
	t.Setenv("NOVA_UPDATE_CALLS", calls)
	c, _, _ = run(t, Environment{}, "apply", "--file", p, "--version", "9.0.0", "x")
	if c != 2 {
		require.EqualValues(t, 2, c, c)
	}
	if _, e := os.Stat(calls); !os.IsNotExist(e) {
		require.Fail(t, fmt.Sprintln("refusal ran a process"))
	}
}
func TestFourReadLimitAndOverallBudget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		hang := transportFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			assert.True(t, ok)
			assert.Equal(t, start.Add(30*time.Second), deadline, "the whole budget bounds every transport request")
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		client := &http.Client{Transport: hang}

		entries := make([]Entry, 40)
		for i := range entries {
			entries[i] = Entry{Name: fmt.Sprint(i), Kind: "tool", Installed: []string{"1.0.0"}, Latest: "npm:pkg"}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		r := readEntries(ctx, entries, options{timeout: time.Minute}, Environment{Client: client}, false)
		if len(r) != 40 {
			require.Lenf(t, r, 40, "expected 40 results, got %d", len(r))
		}
		if r[39].Installed.Reason != "budget" && r[39].Latest.Reason != "budget" {
			require.Failf(t, "", "expected budget reason on unread entry, got: %+v", r[39])
		}
	})
}

func TestFourReadConcurrencyLimit(t *testing.T) {
	t.Parallel()

	var active, max atomic.Int32
	var fifthAttempt atomic.Bool
	ready4 := make(chan struct{})
	release := make(chan struct{})
	var closeOnce sync.Once

	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		cur := active.Add(1)
		defer active.Add(-1)
		for {
			old := max.Load()
			if cur <= old || max.CompareAndSwap(old, cur) {
				break
			}
		}
		if cur > 4 {
			fifthAttempt.Store(true)
			return nil, fmt.Errorf("concurrency exceeded 4: active=%d", cur)
		}
		if cur == 4 {
			closeOnce.Do(func() { close(ready4) })
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"version":"2.0.0"}`)),
			Header:     make(http.Header),
		}, nil
	})
	client := &http.Client{Transport: transport}

	entries := make([]Entry, 12)
	for i := range entries {
		entries[i] = Entry{Name: fmt.Sprint(i), Kind: "tool", Installed: []string{"1.0.0"}, Latest: "npm:pkg"}
	}

	var workersStarted atomic.Int32
	fifthAttempted := make(chan struct{})
	var fifthOnce sync.Once

	env := Environment{
		Client: client,
		WorkerStart: func(id int) {
			workersStarted.Add(1)
		},
		JobAttempt: func(i int) {
			if i == 4 {
				fifthOnce.Do(func() { close(fifthAttempted) })
			}
		},
	}

	done := make(chan struct{})
	go func() {
		readEntries(context.Background(), entries, options{timeout: 5 * time.Second}, env, false)
		close(done)
	}()

	<-ready4
	<-fifthAttempted

	if n := workersStarted.Load(); n != 4 {
		require.EqualValuesf(t, 4, n, "expected 4 workers launched, got %d", n)
	}
	if cur := active.Load(); cur != 4 {
		require.EqualValuesf(t, 4, cur, "expected active == 4 at barrier, got %d", cur)
	}
	if fifthAttempt.Load() {
		require.Fail(t, fmt.Sprintln("a 5th active read was attempted"))
	}

	close(release)
	<-done

	if max.Load() != 4 {
		require.Failf(t, "", "expected max active == 4, got %d", max.Load())
	}
}
func TestSnapshotObservationDoesNotSuppressDelivery(t *testing.T) {
	s := filepath.Join(t.TempDir(), "snapshot.json")
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, o, e := run(t, Environment{}, "report", "--file", p, "--snapshot", s)
	if c != 0 {
		require.EqualValuesf(t, 0, c, "%d %s %s", c, o, e)
	}
	need(t, o, "changed=yes")
	c, o, e = run(t, Environment{}, "report", "--file", p, "--snapshot", s)
	if c != 0 {
		require.EqualValuesf(t, 0, c, "%d %s %s", c, o, e)
	}
	need(t, o, "changed=no")
	state, err := readSnapshot(s)
	if err != nil || len(state.Delivered) != 0 || len(state.Pending) != 0 {
		require.Fail(t, fmt.Sprintln(state, err))
	}
	ctx := context.Background()
	unlock, err := lockSnapshot(ctx, s)
	if err != nil {
		require.NoError(t, err, err)
	}
	blocked, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if release, err := lockSnapshot(blocked, s); err == nil {
		release()
		require.Fail(t, fmt.Sprintln("two snapshot writers acquired lock"))
	}
	unlock()
	if release, err := lockSnapshot(ctx, s); err != nil {
		require.NoError(t, err, err)
	} else {
		release()
	}
}
func TestCheckCapsAndFilterActuallyAvoidsReads(t *testing.T) {
	rows := []string{}
	for i := 0; i < 26; i++ {
		rows = append(rows, row(fmt.Sprint(i), "tool", "1.0.0", "npm:pkg", "none"))
	}
	rows = append(rows, row("excluded", "model", "should-not-run", "ollama:model:tag", "none"))
	p := manifest(t, rows...)
	env := Environment{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"version":"2.0.0"}`)),
			Header:     make(http.Header),
		}, nil
	})}}
	c, o, e := run(t, env, "check", "--file", p, "--kind", "tool", "--max", "2")
	if c != 1 {
		require.EqualValues(t, 1, c, c)
	}
	need(t, e, "entries=27", "CHECK MORE kind=stale shown=2 total=26", "checked=26", "stale=26")
	if strings.Contains(o+e, "excluded") {
		require.Fail(t, fmt.Sprintln(o+e))
	}
}

// A held pipe is the cause the hosted macOS red had: the version command exits
// cleanly, its output is complete, and only the copy of that output is still
// finishing. A ten millisecond grace lost that race under load and reported a
// healthy tool as UNKNOWN. The grace has to outlast an ordinary handoff.
// The three process failures a person acts on differently must stay
// distinguishable in the reason, which one collapsed "execution failed" did not.
func TestProcessFailuresAreDistinguishable(t *testing.T) {
	if r := process(context.Background(), nil, nil, ChildCap); r.Reason != "empty argv" {
		require.EqualValues(t, "empty argv", r.Reason, r.Reason)
	}
	if r := process(context.Background(), []string{"nova-no-such-tool-exists"}, nil, ChildCap); r.Reason != "not_found" {
		require.EqualValues(t, "not_found", r.Reason, r.Reason)
	}
	if r := process(context.Background(), mustArgv(t, command(t, "fail")), nil, ChildCap); r.Reason != "exit 3" {
		require.EqualValues(t, "exit 3", r.Reason, r.Reason)
	}
}
func mustArgv(t *testing.T, s string) []string {
	t.Helper()
	a, err := argv(s)
	if err != nil {
		require.NoError(t, err, err)
	}
	return a
}

// #3518: local:<path> locator with a version-string installed column gets REPORT OK,
// not REPORT UNKNOWN not_found. The installed column is a version string (v1.2.3) and
// latest is local:/path/to/binary that prints that same version.
func TestReportLocalLocatorWithVersionStringInstalled(t *testing.T) {
	// The fake binary prints "tool v1.2.3", which versionKey extracts as "1.2.3".
	binCmd := printer(t, "tool v1.2.3\n")
	// installed is a version string, not a command; latest points to the real binary.
	p := manifest(t, row("mytool", "tool", "v1.2.3", "local:"+binCmd, "none"))
	code, out, errs := run(t, Environment{}, "report", "--file", p, "--host", "air")
	if code != 0 {
		require.EqualValuesf(t, 0, code, "want exit 0, got %d: out=%s errs=%s", code, out, errs)
	}
	need(t, out, "REPORT TOOL name=mytool", "version=1.2.3")
	need(t, out, "REPORT OK checked=1 known=1 unknown=0")
	if strings.Contains(out, "not_found") || strings.Contains(errs, "not_found") {
		require.Failf(t, "", "unexpected not_found in output: out=%s errs=%s", out, errs)
	}
}
