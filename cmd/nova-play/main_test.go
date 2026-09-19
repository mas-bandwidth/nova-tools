package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
