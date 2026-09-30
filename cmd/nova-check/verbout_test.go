package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestRefusalStructuredJSON(t *testing.T) {
	t.Parallel()
	verbs := []struct {
		name string
		args []string
	}{
		{"quickstart", []string{"quickstart", "--json"}},
		{"links", []string{"links", "--json"}},
		{"nocode", []string{"nocode", "--json"}},
		{"kernel", []string{"kernel", "--json"}},
		{"floors", []string{"floors", "--json"}},
		{"attest", []string{"attest", "--json"}},
		{"spelling", []string{"spelling", "--json"}},
		{"corpus", []string{"corpus", "--json"}},
		{"dogfood", []string{"dogfood", "--json"}},
		{"dogfood ledger", []string{"dogfood", "ledger", "--json"}},
		{"dogfood gate", []string{"dogfood", "gate", "--json"}},
		{"dogfood record", []string{"dogfood", "record", "--json"}},
	}

	for _, v := range verbs {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(v.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("%s --json exit = %d, want 2; stdout=%s, stderr=%s", v.name, code, stdout.String(), stderr.String())
			}

			var parsed struct {
				Result struct {
					Verb   string `json:"verb"`
					Status string `json:"status"`
					Exit   int    `json:"exit"`
					Remedy string `json:"remedy"`
				} `json:"result"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
				t.Fatalf("%s: invalid JSON refusal output: %v; raw: %s", v.name, err, stdout.String())
			}
			if parsed.Result.Status != "refused" {
				t.Errorf("%s: status = %q, want 'refused'", v.name, parsed.Result.Status)
			}
			if parsed.Result.Exit != 2 {
				t.Errorf("%s: exit = %d, want 2", v.name, parsed.Result.Exit)
			}
			if parsed.Result.Remedy == "" {
				t.Errorf("%s: remedy is empty", v.name)
			}
		})
	}
}

func TestNoCodeJSONPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello\nProse only.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"nocode", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("nocode --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "nocode" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["files"] != "1" || parsed.Facts["deny-list"] != "floor-list" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestNoCodeJSONFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "evil.sh"), []byte("#!/bin/sh\necho evil\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"nocode", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("nocode --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
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
	if parsed.Result.Verb != "nocode" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["findings"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Kind != "FAIL" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestKernelJSONBytesPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kernelPath := filepath.Join(dir, "KERNEL.md")
	if err := os.WriteFile(kernelPath, []byte("short kernel text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"kernel", "--file", kernelPath, "--max-bytes", "1000", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("kernel --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "kernel" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["budget"] != "1000" || parsed.Facts["bytes"] == "" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestKernelJSONTokensPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kernelPath := filepath.Join(dir, "KERNEL.md")
	if err := os.WriteFile(kernelPath, []byte("short kernel text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"kernel", "--file", kernelPath, "--max-tokens", "1000", "--bytes-per-token", "2.4", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("kernel --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "kernel" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["budget"] != "1000" || parsed.Facts["divisor"] != "2.4" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestKernelJSONFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kernelPath := filepath.Join(dir, "KERNEL.md")
	if err := os.WriteFile(kernelPath, []byte("this text is definitely longer than 5 bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"kernel", "--file", kernelPath, "--max-bytes", "5", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("kernel --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}

	var parsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Items []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}
	if parsed.Result.Verb != "kernel" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if len(parsed.Items) == 0 || parsed.Items[0].Kind != "FAIL" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestFloorsJSONPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "SEED-CORE.md", floorsCoreDoc)
	mustWrite(t, dir, "SEED.md", floorsSourceDoc)
	core := filepath.Join(dir, "SEED-CORE.md")
	source := filepath.Join(dir, "SEED.md")

	var stdout, stderr bytes.Buffer
	code := run([]string{"floors", "--core", core, "--source", source, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("floors --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "floors" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["floors"] != "8" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestFloorsJSONFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "SEED-CORE.md", strings.Replace(floorsCoreDoc, "5. **Secrets nowhere.** Elided.\n", "", 1))
	mustWrite(t, dir, "SEED.md", floorsSourceDoc)
	core := filepath.Join(dir, "SEED-CORE.md")
	source := filepath.Join(dir, "SEED.md")

	var stdout, stderr bytes.Buffer
	code := run([]string{"floors", "--core", core, "--source", source, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("floors --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}

	var parsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Items []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}
	if parsed.Result.Verb != "floors" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if len(parsed.Items) == 0 || parsed.Items[0].Kind != "FAIL" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestAttestJSONPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "a.txt", "content a\n")
	mustWrite(t, dir, "manifest.txt", "a.txt\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"attest", "--home", dir, "--manifest", filepath.Join(dir, "manifest.txt"), "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("attest --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "attest" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["files"] != "1" || parsed.Facts["sha256"] == "" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestAttestJSONFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "manifest.txt", "missing.txt\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"attest", "--home", dir, "--manifest", filepath.Join(dir, "manifest.txt"), "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("attest --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
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
	if parsed.Result.Verb != "attest" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["failed"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Kind != "FAIL" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestSpellingJSONPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "doc.md", "Correct words only.\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"spelling", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("spelling --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "spelling" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["files"] != "1" || parsed.Facts["misspellings"] != "0" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestSpellingJSONFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "doc.md", "Teh quick brown fox.\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"spelling", "--dir", dir, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("spelling --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
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
	if parsed.Result.Verb != "spelling" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["misspellings"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Kind != "FAIL" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestSpellingJSONWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, dir, "doc.md", "Teh quick brown fox.\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"spelling", "--dir", dir, "--write", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("spelling --write --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "spelling" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["written"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Kind != "FIXED" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestCorpusJSONPass(t *testing.T) {
	t.Parallel()
	const ledger = `# what this line protects

| fragment | home | given | by |
|---|---|---|---|
| the light is on | README.md | 2026-01-01 | a friend |
`
	dir := t.TempDir()
	mustWrite(t, dir, "ledger.md", ledger)
	mustWrite(t, dir, "repo/README.md", "and then: the light is on, still.\n")
	ledgerPath := filepath.Join(dir, "ledger.md")
	root := filepath.Join(dir, "repo")

	var stdout, stderr bytes.Buffer
	code := run([]string{"corpus", "--ledger", ledgerPath, "--root", root, "--min-anchors", "1", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("corpus --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "corpus" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["anchors"] != "1" || parsed.Facts["floor"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestCorpusJSONFail(t *testing.T) {
	t.Parallel()
	const ledger = `# what this line protects

| fragment | home | given | by |
|---|---|---|---|
| the light is on | README.md | 2026-01-01 | a friend |
`
	dir := t.TempDir()
	mustWrite(t, dir, "ledger.md", ledger)
	mustWrite(t, dir, "repo/README.md", "different content.\n")
	ledgerPath := filepath.Join(dir, "ledger.md")
	root := filepath.Join(dir, "repo")

	var stdout, stderr bytes.Buffer
	code := run([]string{"corpus", "--ledger", ledgerPath, "--root", root, "--min-anchors", "1", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("corpus --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
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
	if parsed.Result.Verb != "corpus" || parsed.Result.Status != "failed" || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["failed"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Kind != "FAIL" {
		t.Errorf("unexpected Items: %+v", parsed.Items)
	}
}

func TestDogfoodLedgerJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cliPath := writeCLI(t, dir)
	receiptsDir := filepath.Join(dir, "receipts")
	writeReceipt(t, receiptsDir, "0001-r1.json", map[string]any{
		"tool": "nova-example", "verb": "quickstart", "by": "Rowan",
		"at": "2026-09-18T10:00:00Z", "ok": true, "notes": "first pass",
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"dogfood", "ledger", "--cli", cliPath, "--receipts", receiptsDir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dogfood ledger --json exit = %d, want 0; stderr=%s", code, stderr.String())
	}

	var parsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]string `json:"facts"`
		Items []struct {
			Kind   string            `json:"kind"`
			Fields map[string]string `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}
	if parsed.Result.Verb != "dogfood ledger" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["verbs"] != "3" || parsed.Facts["dogfooded"] != "1" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
	if len(parsed.Items) != 3 {
		t.Errorf("expected 3 row items, got %d", len(parsed.Items))
	}
}

func TestDogfoodRecordJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cliPath := writeCLI(t, dir)
	receiptsDir := filepath.Join(dir, "receipts")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"dogfood", "record", "--cli", cliPath, "--receipts", receiptsDir,
		"--tool", "nova-example", "--verb", "quickstart", "--by", "Emma",
		"--notes", "tested JSON record output", "--ok", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dogfood record --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "dogfood record" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if parsed.Facts["tool"] != "nova-example" || parsed.Facts["verb"] != "quickstart" || parsed.Facts["ok"] != "yes" {
		t.Errorf("unexpected Facts: %+v", parsed.Facts)
	}
}

func TestDogfoodGateJSONPassAndFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cliPath := writeCLI(t, dir)
	receiptsDir := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receiptsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pass with --allow-empty
	var stdout, stderr bytes.Buffer
	code := run([]string{"dogfood", "gate", "--cli", cliPath, "--receipts", receiptsDir, "--allow-empty", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dogfood gate --json exit = %d, want 0; stderr=%s", code, stderr.String())
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
	if parsed.Result.Verb != "dogfood gate" || parsed.Result.Status != "ok" || parsed.Result.Exit != 0 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}

	// Fail with --require-all when empty
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"dogfood", "gate", "--cli", cliPath, "--receipts", receiptsDir, "--allow-empty", "--require-all", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("dogfood gate --require-all --json exit = %d, want 1; stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}
	var failParsed struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Items []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &failParsed); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, stdout.String())
	}
	if failParsed.Result.Verb != "dogfood gate" || failParsed.Result.Status != "failed" || failParsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", failParsed.Result)
	}
	if len(failParsed.Items) != 3 {
		t.Errorf("expected 3 findings items, got %d", len(failParsed.Items))
	}
}
