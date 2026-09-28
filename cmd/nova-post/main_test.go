package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/post"
)

// The red list docs/SPEC-OUTBOUND.md carries, one test per numbered item. Each
// runs against fakes only: an httptest endpoint per channel, a fake mailer, a
// throwaway drafts/approvals/allowlist/credential tree and an injected clock.

type fakeMail struct {
	calls int
	got   []byte
}

func (f *fakeMail) Send(to string, payload []byte) error {
	f.calls++
	f.got = append([]byte(nil), payload...)
	return nil
}

type fixture struct {
	t      *testing.T
	root   string
	drafts string
	bus    string
	allow  string
	srv    *httptest.Server
	reqs   int
	last   []byte
	mail   *fakeMail
}

const testNow = "2026-09-17T12:00:00Z"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		t:      t,
		root:   root,
		drafts: filepath.Join(root, "drafts"),
		bus:    filepath.Join(root, "bus"),
		allow:  filepath.Join(root, "allowlist"),
		mail:   &fakeMail{},
	}
	mustMkdir(t, f.drafts, f.bus)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reqs++
		body, _ := io.ReadAll(r.Body)
		f.last = append([]byte(nil), body...)
		switch {
		case strings.Contains(r.URL.Path, "/ghost/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"posts":[{"id":"gp1","url":"https://example.test/gp1"}]}`)
		case strings.Contains(r.URL.Path, "/xrpc/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"uri":"at://did:plc:test/app.bsky.feed.post/1","cid":"bafy1"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	transport = post.DefaultTransport()
	transport.HTTP = f.srv.Client()
	transport.GhostBase = f.srv.URL
	transport.BskyBase = f.srv.URL
	transport.Mail = f.mail
	now = func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		f.srv.Close()
		transport = post.DefaultTransport()
		now = time.Now
	})
	return f
}

func mustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) run(args ...string) (int, string, string) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func (f *fixture) writeAllow(lines ...string) {
	f.t.Helper()
	if err := os.WriteFile(f.allow, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) bodyFile(content string) string {
	f.t.Helper()
	p := filepath.Join(f.root, "body.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) draft(channel, target, body string, extra ...string) string {
	f.t.Helper()
	args := []string{"draft", "--channel", channel, "--target", target,
		"--file", f.bodyFile(body), "--drafts", f.drafts, "--allowlist", f.allow}
	args = append(args, extra...)
	code, stdout, stderr := f.run(args...)
	if code != 0 {
		f.t.Fatalf("draft exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	for _, tok := range strings.Fields(stdout) {
		if strings.HasPrefix(tok, "hash=") {
			return strings.TrimPrefix(tok, "hash=")
		}
	}
	f.t.Fatalf("draft printed no hash= field: %q", stdout)
	return ""
}

func (f *fixture) approve(id, from, body string, at time.Time) {
	f.t.Helper()
	lane := "from-" + id[:strings.LastIndex(id, "-")]
	mustMkdir(f.t, filepath.Join(f.bus, lane))
	text := "From: " + from + "\n" +
		"To: Glenn\n" +
		"Date: " + at.UTC().Format(bus.DateLayout) + "\n" +
		"Id: " + id + "\n" +
		"Subject: approval\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(f.bus, lane, "note-"+id+".md"), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) setCred(channel, target string) {
	switch channel {
	case "ghost":
		f.t.Setenv("GHOST_ADMIN_KEY", "abc123:secret")
	case "bsky":
		f.t.Setenv("BSKY_APP_PASSWORD", "app-pw")
	case "email":
		f.t.Setenv("SMTP_PASSWORD", "smtp-pw")
	case "discord":
		if target == "friends" {
			f.t.Setenv("DISCORD_FRIENDS_WEBHOOK", f.srv.URL)
		} else {
			f.t.Setenv("DISCORD_FLEET_WEBHOOK", f.srv.URL)
		}
	}
}

func readPost(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".post") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatalf("no .post file in %s", dir)
	return nil
}

// 1. TestSendWithoutApprovalIsRefused — a valid draft and no receipt is exit 1,
// and the fake endpoint records zero requests.
func TestSendWithoutApprovalIsRefused(t *testing.T) {
	f := newFixture(t)
	f.writeAllow("ghost\tghost.example")
	f.setCred("ghost", "ghost.example")
	hash := f.draft("ghost", "ghost.example", "a body nobody approved")
	code, _, stderr := f.run("send", "--draft", hash, "--approval", "glenn-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", code, stderr)
	}
	if f.reqs != 0 {
		t.Fatalf("the fake endpoint saw %d requests, want 0", f.reqs)
	}
}

// 2. TestApprovalForAnotherHashIsRefused — a receipt naming a different hash is
// exit 1, zero requests.
func TestApprovalForAnotherHashIsRefused(t *testing.T) {
	f := newFixture(t)
	f.writeAllow("ghost\tghost.example")
	f.setCred("ghost", "ghost.example")
	hash := f.draft("ghost", "ghost.example", "a body")
	other := strings.Repeat("ab", 32)
	f.approve("glenn-0123456789ab", "Glenn", "APPROVE nova-post sha256="+other, now())
	code, _, stderr := f.run("send", "--draft", hash, "--approval", "glenn-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", code, stderr)
	}
	if f.reqs != 0 {
		t.Fatalf("the fake endpoint saw %d requests, want 0", f.reqs)
	}
}

// 3. TestShownBytesEqualSentBytes — show's stdout and the bytes the fake
// endpoint received are equal, for all four channels.
func TestShownBytesEqualSentBytes(t *testing.T) {
	for _, c := range []struct{ channel, target string }{
		{"ghost", "ghost.example"},
		{"bsky", "rowan.test"},
		{"email", "crew"},
		{"discord", "fleet"},
	} {
		t.Run(c.channel, func(t *testing.T) {
			f := newFixture(t)
			f.writeAllow(c.channel + "\t" + c.target)
			f.setCred(c.channel, c.target)
			hash := f.draft(c.channel, c.target, "the exact body bytes for "+c.channel)

			code, shown, showErr := f.run("show", "--draft", hash, "--drafts", f.drafts)
			if code != 0 {
				t.Fatalf("show exit = %d, want 0 (stderr=%q)", code, showErr)
			}
			if len(shown) == 0 {
				t.Fatal("show printed no payload bytes")
			}

			f.approve("glenn-0123456789ab", "Glenn", "APPROVE nova-post sha256="+hash, now())
			code, _, sendErr := f.run("send", "--draft", hash, "--approval", "glenn-0123456789ab",
				"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
			if code != 0 {
				t.Fatalf("send exit = %d, want 0 (stderr=%q)", code, sendErr)
			}
			sent := f.last
			if c.channel == "email" {
				sent = f.mail.got
			}
			if !bytes.Equal([]byte(shown), sent) {
				t.Fatalf("show bytes (%q) != bytes the endpoint received (%q)", shown, sent)
			}
		})
	}
}

// 4a. TestStaleApprovalIsRefused — 24h on the dot is exit 1, zero requests.
func TestStaleApprovalIsRefused(t *testing.T) {
	f := newFixture(t)
	f.writeAllow("ghost\tghost.example")
	f.setCred("ghost", "ghost.example")
	hash := f.draft("ghost", "ghost.example", "a body")
	f.approve("glenn-0123456789ab", "Glenn", "APPROVE nova-post sha256="+hash, now().Add(-24*time.Hour))
	code, _, stderr := f.run("send", "--draft", hash, "--approval", "glenn-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", code, stderr)
	}
	if f.reqs != 0 {
		t.Fatalf("the fake endpoint saw %d requests, want 0", f.reqs)
	}
}

// 4b. TestApprovalNotFromGlennIsRefused — any other sender is exit 1, zero requests.
func TestApprovalNotFromGlennIsRefused(t *testing.T) {
	f := newFixture(t)
	f.writeAllow("ghost\tghost.example")
	f.setCred("ghost", "ghost.example")
	hash := f.draft("ghost", "ghost.example", "a body")
	f.approve("ada-0123456789ab", "Ada", "APPROVE nova-post sha256="+hash, now())
	code, _, stderr := f.run("send", "--draft", hash, "--approval", "ada-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", code, stderr)
	}
	if f.reqs != 0 {
		t.Fatalf("the fake endpoint saw %d requests, want 0", f.reqs)
	}
}

// 5. TestBodyNamingASecretIsRefused — each denied shape is exit 2, and the
// output does not contain the matched text.
func TestBodyNamingASecretIsRefused(t *testing.T) {
	for _, snippet := range []string{"secrets/rowan.env", "id_ed25519.key", "recovery.pub", "GHOST_ADMIN_KEY"} {
		t.Run(snippet, func(t *testing.T) {
			f := newFixture(t)
			f.writeAllow("ghost\tghost.example")
			code, stdout, stderr := f.run("draft", "--channel", "ghost", "--target", "ghost.example",
				"--file", f.bodyFile("the body mentions "+snippet+" in passing"),
				"--drafts", f.drafts, "--allowlist", f.allow)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, snippet) {
				t.Fatalf("the refusal quoted the secret back: %q", stdout+stderr)
			}
		})
	}
}

// 6. TestTargetNotInAllowlistIsRefused — exit 1, zero requests.
func TestTargetNotInAllowlistIsRefused(t *testing.T) {
	f := newFixture(t)
	f.writeAllow("ghost\tgood.example")
	f.setCred("ghost", "good.example")
	hash := f.draft("ghost", "good.example", "a body")
	f.writeAllow("ghost\tother.example")
	code, _, stderr := f.run("send", "--draft", hash, "--approval", "glenn-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", code, stderr)
	}
	if f.reqs != 0 {
		t.Fatalf("the fake endpoint saw %d requests, want 0", f.reqs)
	}
}

// 7. TestDigestForFixtureDayMatchesGolden — a fixture cairn and fleet produce
// bytes equal to the committed golden, and two renders are equal.
func TestDigestForFixtureDayMatchesGolden(t *testing.T) {
	cairnPath := filepath.Join("testdata", "digest", "cairn.md")
	fleetPath := filepath.Join("testdata", "digest", "fleet.tsv")
	golden, err := os.ReadFile(filepath.Join("testdata", "digest", "golden.post"))
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.writeAllow("email\tcrew")
	render := func(dir string) []byte {
		mustMkdir(t, dir)
		code, stdout, stderr := f.run("draft", "--channel", "email", "--target", "crew",
			"--digest", "2026-09-17", "--cairn", cairnPath, "--fleet", fleetPath,
			"--drafts", dir, "--allowlist", f.allow)
		if code != 0 {
			t.Fatalf("digest draft exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
		}
		return readPost(t, dir)
	}
	first := render(filepath.Join(f.root, "d1"))
	second := render(filepath.Join(f.root, "d2"))
	if !bytes.Equal(first, golden) {
		t.Fatalf("digest bytes differ from the golden:\ngot  %q\nwant %q", first, golden)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two renders differ:\n%q\n%q", first, second)
	}
}

// 8. TestSendIsIdempotent — the second send of one hash makes no second request.
func TestSendIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.writeAllow("ghost\tghost.example")
	f.setCred("ghost", "ghost.example")
	hash := f.draft("ghost", "ghost.example", "an idempotent body")
	f.approve("glenn-0123456789ab", "Glenn", "APPROVE nova-post sha256="+hash, now())
	args := []string{"send", "--draft", hash, "--approval", "glenn-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow}
	code, first, stderr := f.run(args...)
	if code != 0 {
		t.Fatalf("first send exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	if f.reqs == 0 {
		t.Fatal("first send made no request to the fake endpoint")
	}
	seen := f.reqs
	code, second, stderr := f.run(args...)
	if code != 0 {
		t.Fatalf("second send exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	if f.reqs != seen {
		t.Fatalf("second send made a request: %d -> %d", seen, f.reqs)
	}
	if first != second {
		t.Fatalf("second send printed %q, want the recorded result %q", second, first)
	}
}
