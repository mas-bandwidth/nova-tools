package update

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
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
	case "hang":
		time.Sleep(30 * time.Second)
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
	case "hold":
		d, err := time.ParseDuration(a[1])
		if err != nil {
			os.Exit(6)
		}
		time.Sleep(d)
	default:
		os.Exit(20)
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
		t.Fatal(e)
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
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
}
func TestManifestRefusesBeforeAnyProcess(t *testing.T) {
	good := row("x", "tool", "example version", "github:o/r", "none")
	for _, s := range []string{"", good + "\n", Header + "\nx\n", Header + "\n" + strings.Replace(good, "example version", "example  version", 1), Header + "\n" + strings.Replace(good, "tool", "weights", 1), Header + "\n" + good + "\n" + good, Header + "\n" + strings.Replace(good, "example version", "\"space path\" version", 1)} {
		if _, e := Load(strings.NewReader(s)); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	es, e := Load(strings.NewReader(Header + "\n# comment\n" + good + "\n"))
	if e != nil || len(es) != 1 {
		t.Fatalf("%v %v", es, e)
	}
}
func TestWholeVersionAndVerifiedOrder(t *testing.T) {
	examples := map[string]string{"gh version 2.100.0 (2026-09-03)": "2.100.0", "go version go1.27.1 darwin/arm64": "1.27.1", "ollama version is 0.33.3": "0.33.3", "v1.3.2": "1.3.2", "1.18.30": "1.18.30", "codex-cli 0.153.4": "0.153.4", "0.46.0": "0.46.0", "sops 3.13.3": "3.13.3", "git version 2.55.0": "2.55.0", "v26.8.2": "26.8.2", "nova-bus v0.12.1-0.20260912135226-0459069+dirty darwin/arm64 go1.27.1": "0.12.1-0.20260912135226-0459069+dirty"}
	for line, want := range examples {
		r := identity(Entry{Kind: "tool"}, line+"\nsecond 900.0.0", true)
		if r.Version != want || r.Raw != line || !r.Known() {
			t.Fatalf("%q: %+v", line, r)
		}
	}
	for _, line := range []string{"nova-wake devel darwin/arm64 go1.27.1", "nova-merge 0459069"} {
		r := identity(Entry{Kind: "tool"}, line, true)
		if !r.Known() || r.Version != "" {
			t.Fatal(r)
		}
		r = identity(Entry{Kind: "tool"}, line, false)
		if r.Reason != "no_release_identity" {
			t.Fatal(r)
		}
	}
	for _, c := range [][3]string{{"1.9.0", "1.10.0", "OLDER"}, {"1.10.0", "1.9.0", "NEWER"}, {"1.9", "1.9.0", "DIFFERENT"}, {"1.09.0", "1.9.0", "DIFFERENT"}, {"1.0.0-rc1", "1.0.0", "DIFFERENT"}, {"0.12.0", "0.12.1-0.1-abc", "DIFFERENT"}, {"999999999999999999999999.0", "1000000000000000000000000.0", "OLDER"}} {
		if got := Compare(c[0], c[1]); got != c[2] {
			t.Fatalf("%v got %s", c, got)
		}
	}
}
func TestModelDigestAndPinIdentity(t *testing.T) {
	r := identity(Entry{Name: "model:tag", Kind: "model"}, "NAME ID SIZE\nmodel:other ffffffffffff 4GB\nmodel:tag 07d35212591f 4GB\n", true)
	if r.Version != "07d35212591f" || r.Raw != "model:tag 07d35212591f 4GB" {
		t.Fatal(r)
	}
	if identity(Entry{Name: "model", Kind: "model"}, "model:tag 07d35212591f 4GB", true).Known() {
		t.Fatal("untagged match")
	}
	for _, s := range []string{"v0.12.0", "devel", "v0.12.1-0.foo"} {
		r := entryRead{Entry: Entry{Kind: "pin"}, Installed: identity(Entry{Kind: "pin"}, "nova-wake "+s, false), Latest: identity(Entry{Kind: "pin"}, "nova-bus "+s, false)}
		if verdict(r) != "EQUAL" {
			t.Fatal(r)
		}
	}
}
func TestProcessesAreBoundedAndRawSurvivesFailure(t *testing.T) {
	for _, tc := range []struct{ cmd, want string }{{command(t, "fail"), "exit 3"}, {command(t, "huge"), "output"}, {command(t, "hang"), "timeout"}, {"nova-version-no-such-binary", "not_found"}} {
		a, _ := argv(tc.cmd)
		start := time.Now()
		r := Installed(context.Background(), Entry{Kind: "tool", Installed: a}, 100*time.Millisecond, true)
		if r.Reason != tc.want {
			t.Fatalf("%s: %+v", tc.want, r)
		}
		if time.Since(start) > time.Second {
			t.Fatal("not bounded")
		}
		if tc.want == "exit 3" && r.Raw != "v9.9.9" {
			t.Fatal(r)
		}
	}
	a, _ := argv(command(t, "stderr", base64.StdEncoding.EncodeToString([]byte("v1.2.3\n"))))
	if r := Installed(context.Background(), Entry{Kind: "tool", Installed: a}, time.Second, true); r.Version != "1.2.3" {
		t.Fatal(r)
	}
	a, _ = argv(command(t, "args", ";", "&&", "|", "$(x)", "`x`", "*"))
	p := process(context.Background(), a, nil, ChildCap)
	if p.Stdout != ";|&&|||$(x)|`x`|*" {
		t.Fatalf("shell interpretation: %+v", p)
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
			t.Fatal(r)
		}
	}
	if len(calls) != 4 || calls[1] != "/repos/o/r/tags?per_page=1" {
		t.Fatal(calls)
	}
	r := Latest(context.Background(), Entry{Kind: "model", Latest: "ollama:model:tag"}, time.Second, client)
	if r.Version != shaText(`{"schemaVersion":2,"layers":[]}`)[:12] {
		t.Fatal(r)
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
			t.Fatalf("%s: %+v", tc.want, r)
		}
		if tc.status == 429 {
			need(t, r.Remedy, "123")
		}
	}
	c, done := testClient(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, r.URL.Path+"x", 302) })
	r = Latest(context.Background(), Entry{Kind: "tool", Latest: "npm:pkg"}, time.Second, c)
	done()
	if r.Known() {
		t.Fatal("redirect loop accepted")
	}
}
func TestReportNeverReadsLatestAndPartialIsVisible(t *testing.T) {
	p := manifest(t, row("good", "tool", printer(t, "tool v1.2.3-rc1+dirty\n"), "github:o/r", "none"), row("bad", "tool", "nova-version-no-such-binary", "npm:unused", "none"))
	env := Environment{Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("report used HTTP")
		return nil, fmt.Errorf("forbidden")
	})}}
	code, out, errs := run(t, env, "report", "--file", p, "--host", "air")
	if code != 1 {
		t.Fatal(code)
	}
	need(t, out, "host=air", "REPORT TOOL name=good", "version=1.2.3-rc1+dirty", "REPORT UNKNOWN name=bad", "not_found")
	need(t, errs, "REPORT FAIL checked=2 known=1 unknown=1")
	code, out, errs = run(t, env, "report", "--file", p, "--draft", "--as", "fixture", "--to", "integrator")
	if code != 1 || !strings.HasPrefix(out, "From: fixture\nTo: integrator\nSubject: versions on - at ") {
		t.Fatalf("%d %s %s", code, out, errs)
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
			t.Fatal(a, c)
		}
	}
	c, out, err := run(t, Environment{}, "apply", "--file", p, "x", "--version", "1.2.0")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, err)
	}
	need(t, out, "APPLY BEFORE", "installed=1.0.0", "APPLY AFTER", "installed=1.2.0", "APPLY OK")
	p = manifest(t, row("x", "tool", read, "local:"+printer(t, "1.3.0"), command(t, "write", state, "1.1.1")))
	c, _, err = run(t, Environment{}, "apply", "--file", p, "x")
	if c != 1 {
		t.Fatal(c)
	}
	need(t, err, "installed 1.1.1, asked 1.3.0")
	calls := filepath.Join(dir, "calls")
	t.Setenv("NOVA_UPDATE_CALLS", calls)
	c, _, _ = run(t, Environment{}, "apply", "--file", p, "--version", "9.0.0", "x")
	if c != 2 {
		t.Fatal(c)
	}
	if _, e := os.Stat(calls); !os.IsNotExist(e) {
		t.Fatal("refusal ran a process")
	}
}
func TestFourReadLimitAndOverallBudget(t *testing.T) {
	var active, max atomic.Int32
	client, done := testClient(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for {
			old := max.Load()
			if n <= old || max.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		<-r.Context().Done()
	})
	defer done()
	entries := []Entry{}
	a, _ := argv(printer(t, "v1.0.0"))
	for i := 0; i < 40; i++ {
		entries = append(entries, Entry{Name: fmt.Sprint(i), Kind: "tool", Installed: a, Latest: "npm:p"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	r := readEntries(ctx, entries, options{timeout: time.Second}, Environment{Client: client}, false)
	if time.Since(started) > time.Second || max.Load() > 4 {
		t.Fatal("budget or concurrency", time.Since(started), max.Load())
	}
	if len(r) != 40 || r[39].Installed.Reason != "budget" {
		t.Fatal(r[39])
	}
}
func TestSnapshotObservationDoesNotSuppressDelivery(t *testing.T) {
	s := filepath.Join(t.TempDir(), "snapshot.json")
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, o, e := run(t, Environment{}, "report", "--file", p, "--snapshot", s)
	if c != 0 {
		t.Fatalf("%d %s %s", c, o, e)
	}
	need(t, o, "changed=yes")
	c, o, e = run(t, Environment{}, "report", "--file", p, "--snapshot", s)
	if c != 0 {
		t.Fatalf("%d %s %s", c, o, e)
	}
	need(t, o, "changed=no")
	state, err := readSnapshot(s)
	if err != nil || len(state.Delivered) != 0 || len(state.Pending) != 0 {
		t.Fatal(state, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := lockSnapshot(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if release, err := lockSnapshot(blocked, s); err == nil {
		release()
		t.Fatal("two snapshot writers acquired lock")
	}
	unlock()
	if release, err := lockSnapshot(ctx, s); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}
func TestCheckCapsAndFilterActuallyAvoidsReads(t *testing.T) {
	rows := []string{}
	installed := printer(t, "1.0.0")
	latest := printer(t, "2.0.0")
	for i := 0; i < 26; i++ {
		rows = append(rows, row(fmt.Sprint(i), "tool", installed, "local:"+latest, "none"))
	}
	rows = append(rows, row("excluded", "model", "should-not-run", "ollama:model:tag", "none"))
	p := manifest(t, rows...)
	c, o, e := run(t, Environment{}, "check", "--file", p, "--kind", "tool", "--max", "2")
	if c != 1 {
		t.Fatal(c)
	}
	need(t, o, "entries=27", "UPDATE MORE kind=stale shown=2 total=26")
	need(t, e, "checked=26", "stale=26")
	if strings.Contains(o, "excluded") {
		t.Fatal(o)
	}
}

// A held pipe is the cause the hosted macOS red had: the version command exits
// cleanly, its output is complete, and only the copy of that output is still
// finishing. A ten millisecond grace lost that race under load and reported a
// healthy tool as UNKNOWN. The grace has to outlast an ordinary handoff.
func TestHealthyCommandWithLingeringGrandchildStillReads(t *testing.T) {
	e := Entry{Name: "x", Kind: "tool", Installed: mustArgv(t, command(t, "linger", base64.StdEncoding.EncodeToString([]byte("x 1.2.3\n")), "300ms"))}
	r := Installed(context.Background(), e, 5*time.Second, false)
	if !r.Known() || r.Version != "1.2.3" {
		t.Fatalf("healthy read refused: reason=%q version=%q raw=%q", r.Reason, r.Version, r.Raw)
	}
}

// Past the grace the refusal must name the pipe rather than blame the version
// command, and it must say so without echoing a byte the child wrote.
func TestHeldPipePastGraceIsNamedAndEchoesNoContent(t *testing.T) {
	secret := "x 9.9.9-secret"
	e := Entry{Name: "x", Kind: "tool", Installed: mustArgv(t, command(t, "linger", base64.StdEncoding.EncodeToString([]byte(secret+"\n")), (killGrace+time.Second).String()))}
	r := Installed(context.Background(), e, killGrace+5*time.Second, false)
	if r.Known() || r.Reason != "output_not_closed" {
		t.Fatalf("reason=%q remedy=%q", r.Reason, r.Remedy)
	}
	if r.Remedy != leakRemedy {
		t.Fatalf("remedy=%q", r.Remedy)
	}
	if strings.Contains(r.Reason, "9.9.9") || strings.Contains(r.Remedy, "9.9.9") {
		t.Fatalf("diagnostic echoed child content: %q %q", r.Reason, r.Remedy)
	}
}

// The three process failures a person acts on differently must stay
// distinguishable in the reason, which one collapsed "execution failed" did not.
func TestProcessFailuresAreDistinguishable(t *testing.T) {
	if r := process(context.Background(), nil, nil, ChildCap); r.Reason != "empty argv" {
		t.Fatal(r.Reason)
	}
	if r := process(context.Background(), []string{"nova-no-such-tool-exists"}, nil, ChildCap); r.Reason != "not_found" {
		t.Fatal(r.Reason)
	}
	if r := process(context.Background(), mustArgv(t, command(t, "fail")), nil, ChildCap); r.Reason != "exit 3" {
		t.Fatal(r.Reason)
	}
}
func mustArgv(t *testing.T, s string) []string {
	t.Helper()
	a, err := argv(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
