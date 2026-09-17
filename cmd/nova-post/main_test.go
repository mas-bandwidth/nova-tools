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
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/post"
)

// runPost drives the command as a test caller: it never exits the process and never
// touches the network unless a test has pointed a channel at its own httptest server.
func runPost(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func fieldOf(t *testing.T, line, key string) string {
	t.Helper()
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	t.Fatalf("no %s= in %q", key, line)
	return ""
}

func allowlist(t *testing.T, dir string, pairs ...[2]string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&b, "%s\t%s\n", p[0], p[1])
	}
	return write(t, dir, "allowlist", b.String())
}

func bodyFile(t *testing.T, dir string) string {
	t.Helper()
	return write(t, dir, "body.md", "# Hello\n\nWorld.\n")
}

func draftOnce(t *testing.T, ch, target, drafts, allow, body string) string {
	t.Helper()
	code, out, errs := runPost(t, "draft",
		"--channel", ch, "--target", target, "--file", body,
		"--drafts", drafts, "--allowlist", allow)
	if code != 0 {
		t.Fatalf("draft channel=%s exit=%d out=%q err=%q", ch, code, out, errs)
	}
	return fieldOf(t, out, "hash")
}

// approvalNote writes a bus note on the sender's own lane: a receipt whose body is the
// one approval line and whose Id is what --approval names.
func approvalNote(t *testing.T, busDir, id, sender, hash string, when time.Time) {
	t.Helper()
	slug := bus.SlugOfID(id)
	if slug == "" {
		t.Fatalf("test id %q is not a valid bus id", id)
	}
	lane := "from-" + slug
	content := fmt.Sprintf("From: %s\nTo: Rowan\nDate: %s\nId: %s\nSubject: approve\nKind: note\n\nAPPROVE nova-post sha256=%s\n",
		sender, when.UTC().Format(bus.DateLayout), id, hash)
	name := when.UTC().Format(bus.FileTimeLayout) + "-approve-" + id[len(id)-12:] + ".md"
	write(t, filepath.Join(busDir, lane), name, content)
}

func setSecrets(t *testing.T) {
	t.Helper()
	t.Setenv("GHOST_ADMIN_KEY", "1:secret")
	t.Setenv("BSKY_APP_PASSWORD", "app-password")
	t.Setenv("SMTP_PASSWORD", "smtp-password")
	t.Setenv("DISCORD_FRIENDS_WEBHOOK", "https://discord.invalid/webhook")
	t.Setenv("DISCORD_FLEET_WEBHOOK", "https://discord.invalid/fleet")
}

type counterServer struct {
	*httptest.Server
	n     atomic.Int64
	body  atomic.Value
	reply string
}

func newCounterServer(t *testing.T, reply string) *counterServer {
	t.Helper()
	cs := &counterServer{reply: reply}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cs.n.Add(1)
		cs.body.Store(string(b))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, cs.reply)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *counterServer) requests() int64 { return cs.n.Load() }

func (cs *counterServer) lastBody() string {
	v, _ := cs.body.Load().(string)
	return v
}

// pointChannel makes the named channel reach the test server. The Discord webhook IS the
// credential, so its env var is the endpoint; the other three keep their separate secret
// and take the endpoint from the test seam.
func pointChannel(t *testing.T, ch, url string) {
	t.Helper()
	switch ch {
	case "ghost":
		t.Setenv("NOVA_POST_GHOST_URL", url)
	case "bsky":
		t.Setenv("NOVA_POST_BSKY_URL", url)
	case "email":
		t.Setenv("NOVA_POST_EMAIL_URL", url)
	case "discord":
		t.Setenv("DISCORD_FRIENDS_WEBHOOK", url)
	}
}

func allowPair(ch string) [2]string {
	switch ch {
	case "ghost":
		return [2]string{"ghost", "example.com"}
	case "bsky":
		return [2]string{"bsky", "rowan.bsky.social"}
	case "email":
		return [2]string{"email", "announce"}
	default:
		return [2]string{"discord", "friends"}
	}
}

func targetFor(ch string) string { return allowPair(ch)[1] }

func TestSendWithoutApprovalIsRefused(t *testing.T) {
	setSecrets(t)
	srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
	pointChannel(t, "ghost", srv.URL)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	body := bodyFile(t, t.TempDir())
	hash := draftOnce(t, "ghost", "example.com", drafts, allow, body)

	code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "glenn-aaaaaaaaaaaa",
		"--drafts", drafts, "--bus", t.TempDir(), "--allowlist", allow)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (out=%q err=%q)", code, out, errs)
	}
	if n := srv.requests(); n != 0 {
		t.Fatalf("provider saw %d requests, want 0", n)
	}
	if !strings.Contains(errs, "approval") {
		t.Fatalf("refusal does not name the approval: %q", errs)
	}
}

func TestApprovalForAnotherHashIsRefused(t *testing.T) {
	setSecrets(t)
	srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
	pointChannel(t, "ghost", srv.URL)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	body := bodyFile(t, t.TempDir())
	hash := draftOnce(t, "ghost", "example.com", drafts, allow, body)

	busDir := t.TempDir()
	approvalNote(t, busDir, "glenn-aaaaaaaaaaaa", "Glenn", strings.Repeat("b", 64), time.Now().UTC())

	code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "glenn-aaaaaaaaaaaa",
		"--drafts", drafts, "--bus", busDir, "--allowlist", allow)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (out=%q err=%q)", code, out, errs)
	}
	if n := srv.requests(); n != 0 {
		t.Fatalf("provider saw %d requests, want 0", n)
	}
}

func TestShownBytesEqualSentBytes(t *testing.T) {
	setSecrets(t)
	for _, ch := range []string{"ghost", "bsky", "email", "discord"} {
		t.Run(ch, func(t *testing.T) {
			srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
			pointChannel(t, ch, srv.URL)
			drafts := t.TempDir()
			allow := allowlist(t, t.TempDir(), allowPair(ch))
			body := bodyFile(t, t.TempDir())
			target := targetFor(ch)
			hash := draftOnce(t, ch, target, drafts, allow, body)

			busDir := t.TempDir()
			approvalNote(t, busDir, "glenn-aaaaaaaaaaaa", "Glenn", hash, time.Now().UTC())

			code, shown, errs := runPost(t, "show", "--draft", hash, "--drafts", drafts)
			if code != 0 {
				t.Fatalf("show exit=%d err=%q", code, errs)
			}
			if !strings.Contains(errs, "POST SHOW OK") {
				t.Fatalf("show receipt missing or not on stderr: %q", errs)
			}
			code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "glenn-aaaaaaaaaaaa",
				"--drafts", drafts, "--bus", busDir, "--allowlist", allow)
			if code != 0 {
				t.Fatalf("send exit=%d out=%q err=%q", code, out, errs)
			}
			if n := srv.requests(); n != 1 {
				t.Fatalf("provider saw %d requests, want 1", n)
			}
			if got := srv.lastBody(); got != shown {
				t.Fatalf("shown and sent bytes differ:\nshown=%q\nsent =%q", shown, got)
			}
		})
	}
}

func TestStaleApprovalIsRefused(t *testing.T) {
	setSecrets(t)
	srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
	pointChannel(t, "ghost", srv.URL)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	body := bodyFile(t, t.TempDir())
	hash := draftOnce(t, "ghost", "example.com", drafts, allow, body)

	busDir := t.TempDir()
	approvalNote(t, busDir, "glenn-aaaaaaaaaaaa", "Glenn", hash, time.Now().UTC().Add(-24*time.Hour))

	code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "glenn-aaaaaaaaaaaa",
		"--drafts", drafts, "--bus", busDir, "--allowlist", allow)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (out=%q err=%q)", code, out, errs)
	}
	if n := srv.requests(); n != 0 {
		t.Fatalf("provider saw %d requests, want 0", n)
	}
}

func TestApprovalNotFromGlennIsRefused(t *testing.T) {
	setSecrets(t)
	srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
	pointChannel(t, "ghost", srv.URL)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	body := bodyFile(t, t.TempDir())
	hash := draftOnce(t, "ghost", "example.com", drafts, allow, body)

	busDir := t.TempDir()
	approvalNote(t, busDir, "bo-aaaaaaaaaaaa", "Bo", hash, time.Now().UTC())

	code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "bo-aaaaaaaaaaaa",
		"--drafts", drafts, "--bus", busDir, "--allowlist", allow)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (out=%q err=%q)", code, out, errs)
	}
	if n := srv.requests(); n != 0 {
		t.Fatalf("provider saw %d requests, want 0", n)
	}
}

func TestBodyNamingASecretIsRefused(t *testing.T) {
	setSecrets(t)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	denied := []struct {
		name   string
		secret string
	}{
		{"secrets store", "nova/secrets/db.yaml"},
		{"key file", "server.key"},
		{"recovery pub", "recovery.pub"},
		{"credential name", "GHOST_ADMIN_KEY"},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			body := write(t, t.TempDir(), "body.md", "# Hello\n\nsee "+tc.secret+" for it\n")
			code, out, errs := runPost(t, "draft",
				"--channel", "ghost", "--target", "example.com", "--file", body,
				"--drafts", drafts, "--allowlist", allow)
			if code != 2 {
				t.Fatalf("exit=%d want 2 (out=%q err=%q)", code, out, errs)
			}
			combined := out + errs
			if strings.Contains(combined, tc.secret) {
				t.Fatalf("refusal quoted the secret: %q", combined)
			}
		})
	}
}

func TestTargetNotInAllowlistIsRefused(t *testing.T) {
	setSecrets(t)
	srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
	pointChannel(t, "ghost", srv.URL)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	body := bodyFile(t, t.TempDir())
	hash := draftOnce(t, "ghost", "example.com", drafts, allow, body)

	strict := allowlist(t, t.TempDir(), [2]string{"ghost", "other.example"})
	busDir := t.TempDir()
	approvalNote(t, busDir, "glenn-aaaaaaaaaaaa", "Glenn", hash, time.Now().UTC())

	code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "glenn-aaaaaaaaaaaa",
		"--drafts", drafts, "--bus", busDir, "--allowlist", strict)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (out=%q err=%q)", code, out, errs)
	}
	if n := srv.requests(); n != 0 {
		t.Fatalf("provider saw %d requests, want 0", n)
	}
}

func TestDigestForFixtureDayMatchesGolden(t *testing.T) {
	cairn := readFixture(t, "cairn.md")
	fleet := readFixture(t, "fleet.tsv")
	golden := readFixture(t, "digest.golden")
	req := post.Request{
		Channel:    post.Email,
		Target:     "announce",
		Title:      "Nova digest 2026-09-17",
		DigestDate: "2026-09-17",
		CairnData:  cairn,
		FleetData:  fleet,
	}
	first, err := post.Render(req, time.Now().UTC())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := post.Render(req, time.Now().UTC())
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatalf("two renders differ")
	}
	if !bytes.Equal(first.Bytes, golden) {
		t.Fatalf("digest mismatch:\n--- got ---\n%s\n--- want ---\n%s", first.Bytes, golden)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestSendIsIdempotent(t *testing.T) {
	setSecrets(t)
	srv := newCounterServer(t, `{"id":"remote-1","url":"https://example.invalid/remote-1"}`)
	pointChannel(t, "ghost", srv.URL)
	drafts := t.TempDir()
	allow := allowlist(t, t.TempDir(), allowPair("ghost"))
	body := bodyFile(t, t.TempDir())
	hash := draftOnce(t, "ghost", "example.com", drafts, allow, body)

	busDir := t.TempDir()
	approvalNote(t, busDir, "glenn-aaaaaaaaaaaa", "Glenn", hash, time.Now().UTC())

	for i := 0; i < 2; i++ {
		code, out, errs := runPost(t, "send", "--draft", hash, "--approval", "glenn-aaaaaaaaaaaa",
			"--drafts", drafts, "--bus", busDir, "--allowlist", allow)
		if code != 0 {
			t.Fatalf("send #%d exit=%d out=%q err=%q", i+1, code, out, errs)
		}
	}
	if n := srv.requests(); n != 1 {
		t.Fatalf("provider saw %d requests, want 1 (idempotent second send)", n)
	}
}
