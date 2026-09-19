package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/play"
)

func writeSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoVerbRefused(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(nil, &stdout, &stderr); got != 2 {
		t.Errorf("want exit 2, got %d", got)
	}
	if !strings.Contains(stderr.String(), "refusing to guess") {
		t.Errorf("stderr = %q, want refusing to guess", stderr.String())
	}
}

func TestUnknownVerbRefused(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"unknown"}, &stdout, &stderr); got != 2 {
		t.Errorf("want exit 2, got %d", got)
	}
}

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"help"}, &stdout, &stderr); got != 0 {
		t.Errorf("want exit 0, got %d", got)
	}
	if !strings.Contains(stdout.String(), "nova-play") {
		t.Errorf("stdout missing nova-play: %s", stdout.String())
	}
}

func TestAnnotateRequiresAllFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"annotate", "--source", "x.txt"}, &stdout, &stderr); got != 2 {
		t.Errorf("want exit 2, got %d", got)
	}
}

func TestAnnotateAndRead(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	var stdout, stderr bytes.Buffer
	got := run([]string{"annotate", "--source", src, "--author", "Emma", "--passage", "The lantern room held a brass fitting.", "--note", "I wonder."}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("annotate: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ANNOTATE OK") {
		t.Errorf("stdout = %q, want ANNOTATE OK", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	got = run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("read: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "READ OK") {
		t.Errorf("stdout = %q, want READ OK", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Emma") {
		t.Errorf("stdout missing Emma: %s", stdout.String())
	}
}

func TestReadEmptySource(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "No annotations here.\n")

	var stdout, stderr bytes.Buffer
	got := run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("read: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "READ OK") {
		t.Errorf("stdout = %q, want READ OK", stdout.String())
	}
	if !strings.Contains(stdout.String(), "notes=0") {
		t.Errorf("stdout missing notes=0: %s", stdout.String())
	}
}

func TestStaleAnchorInRead(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	var stdout, stderr bytes.Buffer
	got := run([]string{"annotate", "--source", src, "--author", "Emma", "--passage", "The lantern room held a brass fitting.", "--note", "Nice."}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("annotate: exit %d, stderr=%s", got, stderr.String())
	}

	// Edit the source.
	if err := os.WriteFile(src, []byte("The lantern room held a copper fitting.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	got = run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("want exit 1 for stale anchor, got %d", got)
	}
	if !strings.Contains(stdout.String(), "ANCHOR STALE") {
		t.Errorf("stdout = %q, want ANCHOR STALE", stdout.String())
	}
}

func TestCLIPathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story with spaces.txt", "The lantern room held a brass fitting.\n")

	var stdout, stderr bytes.Buffer
	got := run([]string{"annotate", "--source", src, "--author", "Emma", "--passage", "The lantern room held a brass fitting.", "--note", "Note in spaced path."}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("annotate: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ANNOTATE OK") {
		t.Fatalf("stdout = %q, want ANCHOR OK", stdout.String())
	}

	// Extract ID
	var noteID string
	for _, part := range strings.Fields(stdout.String()) {
		if strings.HasPrefix(part, "id=") {
			noteID = strings.TrimPrefix(part, "id=")
		}
	}
	if noteID == "" {
		t.Fatalf("could not find note ID in stdout: %s", stdout.String())
	}

	// Read immediately -- must be READ OK, not ANCHOR STALE
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("read: exit %d, stdout=%s, stderr=%s", got, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "READ OK") {
		t.Errorf("stdout = %q, want READ OK", stdout.String())
	}

	// Reply
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"reply", "--source", src, "--id", noteID, "--author", "Stella", "--body", "Reply in spaced path."}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("reply: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "REPLY OK") {
		t.Errorf("stdout = %q, want REPLY OK", stdout.String())
	}

	// Read after reply
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("read after reply: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Reply in spaced path.") {
		t.Errorf("stdout missing reply: %s", stdout.String())
	}
}

func TestCLIMultilineNoteAndReply(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "A shared passage.\n")

	var stdout, stderr bytes.Buffer
	got := run([]string{"annotate", "--source", src, "--author", "Emma", "--passage", "A shared passage.", "--note", "First line\nSecond line"}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("annotate: exit %d, stderr=%s", got, stderr.String())
	}

	var noteID string
	for _, part := range strings.Fields(stdout.String()) {
		if strings.HasPrefix(part, "id=") {
			noteID = strings.TrimPrefix(part, "id=")
		}
	}
	if noteID == "" {
		t.Fatalf("could not find note ID in stdout: %s", stdout.String())
	}

	// Read -- verify multiline body is preserved and rendered via oneline.Escape
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("read: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), `BODY First line\x0aSecond line`) {
		t.Errorf("stdout missing multiline note body: %s", stdout.String())
	}

	// Reply to note
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"reply", "--source", src, "--id", noteID, "--author", "Stella", "--body", "Reply line 1\nReply line 2"}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("reply: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "REPLY OK") {
		t.Errorf("stdout = %q, want REPLY OK", stdout.String())
	}

	// Read after reply -- verify original multiline note was NOT truncated by reply save,
	// and reply multiline body is also preserved.
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"read", "--source", src}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("read: exit %d, stderr=%s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), `BODY First line\x0aSecond line`) {
		t.Errorf("stdout missing preserved multiline note body: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `BODY Reply line 1\x0aReply line 2`) {
		t.Errorf("stdout missing multiline reply body: %s", stdout.String())
	}
}

func TestCLIStaleSourceReplyRefusal(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	var stdout, stderr bytes.Buffer
	got := run([]string{"annotate", "--source", src, "--author", "Emma", "--passage", "The lantern room held a brass fitting.", "--note", "Initial note."}, &stdout, &stderr)
	if got != 0 {
		t.Fatalf("annotate: exit %d, stderr=%s", got, stderr.String())
	}

	var noteID string
	for _, part := range strings.Fields(stdout.String()) {
		if strings.HasPrefix(part, "id=") {
			noteID = strings.TrimPrefix(part, "id=")
		}
	}
	if noteID == "" {
		t.Fatalf("could not find note ID in stdout: %s", stdout.String())
	}

	sidecarPath := play.NoteFile(src)
	sidecarBefore, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar before: %v", err)
	}

	// Edit source to make anchor stale
	if err := os.WriteFile(src, []byte("The lantern room held a copper fitting.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reply must exit 1 with ANCHOR STALE
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"reply", "--source", src, "--id", noteID, "--author", "Stella", "--body", "Reply to stale source."}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("want exit 1 for reply against stale anchor, got %d", got)
	}
	if !strings.Contains(stderr.String(), "REPLY FAIL") || !strings.Contains(stderr.String(), "ANCHOR STALE") {
		t.Errorf("stderr = %q, want REPLY FAIL with ANCHOR STALE", stderr.String())
	}

	// Sidecar must be byte-identical
	sidecarAfter, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar after: %v", err)
	}
	if !bytes.Equal(sidecarBefore, sidecarAfter) {
		t.Errorf("sidecar modified after stale reply refusal:\nbefore:\n%s\nafter:\n%s", sidecarBefore, sidecarAfter)
	}

	// Delete source
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	// Reply must exit 1 on missing source
	stdout.Reset()
	stderr.Reset()
	got = run([]string{"reply", "--source", src, "--id", noteID, "--author", "Stella", "--body", "Reply to missing source."}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("want exit 1 for reply against missing source, got %d", got)
	}
	if !strings.Contains(stderr.String(), "REPLY FAIL") {
		t.Errorf("stderr = %q, want REPLY FAIL", stderr.String())
	}

	// Sidecar must still be byte-identical
	sidecarAfterMissing, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar after missing: %v", err)
	}
	if !bytes.Equal(sidecarBefore, sidecarAfterMissing) {
		t.Errorf("sidecar modified after missing source reply refusal:\nbefore:\n%s\nafter:\n%s", sidecarBefore, sidecarAfterMissing)
	}
}
