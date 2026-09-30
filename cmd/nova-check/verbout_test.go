package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLinksJSONPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("[self](doc.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"links", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("links --json exit = %d, want 0; stderr=%s", code, stderr.String())
	}

	var parsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]string `json:"facts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}

	if parsed.Result.Verb != "links" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["files"] != "1" || parsed.Facts["links"] != "1" || parsed.Facts["excluded"] != "0" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestLinksJSONBroken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("[bad](missing.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"links", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("links --json exit = %d, want 1; stderr=%s", code, stderr.String())
	}

	var parsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]string `json:"facts"`
		Items []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}

	if parsed.Result.Verb != "links" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["broken"] != "1" {
		t.Errorf("expected facts.broken == 1, got %v", parsed.Facts)
	}
	if len(parsed.Items) != 1 {
		t.Errorf("expected 1 item, got %d: %+v", len(parsed.Items), parsed.Items)
	}
}

func TestQuickstartJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Clean\nNo code blocks, no links.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"quickstart", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("quickstart --json exit = %d, want 0; stderr=%s", code, stderr.String())
	}

	var parsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]string `json:"facts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}

	if parsed.Result.Verb != "quickstart" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["done"] != "2" || parsed.Facts["worst-exit"] != "0" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}
