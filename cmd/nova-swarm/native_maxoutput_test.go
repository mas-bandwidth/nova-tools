package main

import (
	"bytes"
	"encoding/json"
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

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestNativeMaxOutputEnvForwarding verifies environment forwarding and ambient stripping:
// 1. Worker description specifying max_output_tokens forwards OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX
//    and NOVA_WORKER_MAX_OUTPUT_TOKENS.
// 2. CLI --max-output-tokens forwards both environment variables.
// 3. Ambient values in caller's environment are stripped when no cap is requested.
// 4. native-argv.log records the forwarded token limits without redacting them.
func TestNativeMaxOutputEnvForwarding(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("test card\nFAKE-RECORD-OUTPUT-LIMIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Worker description with max_output_tokens
	t.Run("worker description forwarding", func(t *testing.T) {
		home := t.TempDir()
		descPath := filepath.Join(t.TempDir(), "worker.json")
		desc := map[string]any{
			"name": "fake-1", "provider": "fake", "model": "fake-model",
			"env_var": "FAKE_KEY", "usage": "opencode",
			"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
			"harness_args":      []string{"run", "--model", "{model}", "--", "{prompt}"},
			"secret":            "FAKE_KEY",
			"max_output_tokens": 1600,
		}
		raw, _ := json.MarshalIndent(desc, "", "  ")
		if err := os.WriteFile(descPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAKE_KEY", fakeKey)

		var stdout, stderr bytes.Buffer
		rc := run([]string{
			"native", "--harness", bin, "--worker", descPath,
			"--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "10s", "--no-wall",
		}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("native run failed exit %d:\n%s%s", rc, stdout.String(), stderr.String())
		}

		jobDir := filepath.Join(slot, "jobs", "card")
		recPath := filepath.Join(jobDir, "output-limit-record")
		recBytes, err := os.ReadFile(recPath)
		if err != nil {
			t.Fatalf("output-limit-record was not written: %v", err)
		}
		rec := string(recBytes)
		mustContain(t, "output limit record", rec, "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=1600")
		mustContain(t, "output limit record", rec, "NOVA_WORKER_MAX_OUTPUT_TOKENS=1600")

		// Verify native-argv.log does not redact the limit integer
		argvLog, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
		if err != nil {
			t.Fatalf("native-argv.log missing: %v", err)
		}
		mustContain(t, "argv log", string(argvLog), "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=1600")
		mustContain(t, "argv log", string(argvLog), "NOVA_WORKER_MAX_OUTPUT_TOKENS=1600")
	})

	// 2. CLI --max-output-tokens flag forwarding
	t.Run("CLI flag forwarding", func(t *testing.T) {
		root2, slot2 := aSlot(t)
		cardPath2 := filepath.Join(root2, "card.md")
		if err := os.WriteFile(cardPath2, []byte("test card\nFAKE-RECORD-OUTPUT-LIMIT\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		rc := run([]string{
			"native", "--harness", bin, "--model", "fake/fake-model",
			"--card", cardPath2, "--slot", slot2, "--root", root2,
			"--deadline", "10s", "--no-wall",
			"--max-output-tokens", "3200",
		}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("native run failed exit %d:\n%s%s", rc, stdout.String(), stderr.String())
		}

		jobDir := filepath.Join(slot2, "jobs", "card")
		recBytes, err := os.ReadFile(filepath.Join(jobDir, "output-limit-record"))
		if err != nil {
			t.Fatalf("output-limit-record missing: %v", err)
		}
		rec := string(recBytes)
		mustContain(t, "output limit record", rec, "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=3200")
		mustContain(t, "output limit record", rec, "NOVA_WORKER_MAX_OUTPUT_TOKENS=3200")
	})

	// 3. Ambient stripping when unset
	t.Run("ambient stripping when unset", func(t *testing.T) {
		root3, slot3 := aSlot(t)
		cardPath3 := filepath.Join(root3, "card.md")
		if err := os.WriteFile(cardPath3, []byte("test card\nFAKE-RECORD-OUTPUT-LIMIT\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX", "99999")
		t.Setenv("NOVA_WORKER_MAX_OUTPUT_TOKENS", "99999")

		var stdout, stderr bytes.Buffer
		rc := run([]string{
			"native", "--harness", bin, "--model", "fake/fake-model",
			"--card", cardPath3, "--slot", slot3, "--root", root3,
			"--deadline", "10s", "--no-wall",
		}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("native run failed exit %d:\n%s%s", rc, stdout.String(), stderr.String())
		}

		jobDir := filepath.Join(slot3, "jobs", "card")
		recBytes, err := os.ReadFile(filepath.Join(jobDir, "output-limit-record"))
		if err != nil {
			t.Fatalf("output-limit-record missing: %v", err)
		}
		rec := string(recBytes)
		if strings.Contains(rec, "99999") {
			t.Fatalf("ambient token limit leaked into child:\n%s", rec)
		}
		mustContain(t, "output limit record", rec, "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=\n")
		mustContain(t, "output limit record", rec, "NOVA_WORKER_MAX_OUTPUT_TOKENS=\n")
	})
}

// TestNativeOKCarriesRequestedMaxOutputTokens: verifies that NATIVE OK prints
// requested_max_output_tokens=<n> beside binary_sha256=<sha> when present, and omits it when unset.
func TestNativeOKCarriesRequestedMaxOutputTokens(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("card text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. With cap requested
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--model", "fake/fake-model",
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall",
		"--max-output-tokens", "2048",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("native run failed exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	out := stdout.String()
	mustContain(t, "NATIVE OK output", out, "NATIVE OK ")
	mustContain(t, "NATIVE OK output", out, "requested_max_output_tokens=2048")
	// Verify it appears right beside binary_sha256
	if !strings.Contains(out, "binary_sha256=") || !strings.Contains(out, "requested_max_output_tokens=2048 config=") {
		t.Errorf("expected requested_max_output_tokens immediately beside binary_sha256 and before config=, got:\n%s", out)
	}

	// 2. Without cap requested
	root2, slot2 := aSlot(t)
	cardPath2 := filepath.Join(root2, "card.md")
	if err := os.WriteFile(cardPath2, []byte("card text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	rc2 := run([]string{
		"native", "--harness", bin, "--model", "fake/fake-model",
		"--card", cardPath2, "--slot", slot2, "--root", root2,
		"--deadline", "10s", "--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc2 != 0 {
		t.Fatalf("native run failed exit %d:\n%s%s", rc2, stdout.String(), stderr.String())
	}
	out2 := stdout.String()
	mustContain(t, "NATIVE OK output", out2, "NATIVE OK ")
	if strings.Contains(out2, "requested_max_output_tokens") {
		t.Errorf("unexpected requested_max_output_tokens in NATIVE OK line when omitted:\n%s", out2)
	}
}

// TestNativeRefusesUnsupportedHarness: native execution refuses exit 2 when max_output_tokens
// is requested and the harness is unsupported (not opencode or fake-harness).
func TestNativeRefusesUnsupportedHarness(t *testing.T) {
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("card text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an executable binary with an unsupported name
	unsupportedBin := filepath.Join(t.TempDir(), "claude-code")
	if err := os.WriteFile(unsupportedBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", unsupportedBin, "--model", "fake/fake-model",
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall",
		"--max-output-tokens", "1000",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("unsupported harness with cap requested must exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	mustContain(t, "refusal line", stderr.String(), "NATIVE REFUSED")
	mustContain(t, "refusal reason", stderr.String(), "does not support max_output_tokens")
}

// TestNativeRefusesMismatchedHarness: native execution refuses exit 2 when max_output_tokens
// is requested and --harness does not match the worker description's harness.
func TestNativeRefusesMismatchedHarness(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("card text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	descPath := filepath.Join(t.TempDir(), "worker.json")
	desc := map[string]any{
		"name": "w-mismatch", "provider": "opencode", "model": "model-1",
		"env_var": "KEY", "usage": "opencode",
		"harness": "opencode", "worker_dir": home, "deadline": "30s",
		"harness_args":      []string{"run", "--model", "{model}", "--", "{prompt}"},
		"secret":            "KEY",
		"max_output_tokens": 1000,
	}
	raw, _ := json.MarshalIndent(desc, "", "  ")
	if err := os.WriteFile(descPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEY", "dummy")

	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--worker", descPath,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("mismatched harness with cap requested must exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	mustContain(t, "refusal line", stderr.String(), "NATIVE REFUSED")
	mustContain(t, "refusal reason", stderr.String(), "differs from the worker description's harness")
}

// TestVerifyMaxOutputReceipt: nova-swarm verify writes requested_max_output_tokens to .receipt.
func TestVerifyMaxOutputReceipt(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "RESULT.md")
	if err := os.WriteFile(resultPath, []byte("CONTRACT LINE OK\npass\nevidence line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. verify with --max-output-tokens
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"verify", "--result", resultPath, "--contract", "CONTRACT LINE OK",
		"--label", "test-label", "--max-output-tokens", "4096",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("verify failed exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}

	receiptPath := resultPath + ".receipt"
	r, err := swarm.ReadReceipt(receiptPath)
	if err != nil {
		t.Fatalf("failed reading receipt: %v", err)
	}
	if r.RequestedMaxOutputTokens != 4096 {
		t.Errorf("expected RequestedMaxOutputTokens 4096, got %d", r.RequestedMaxOutputTokens)
	}

	// 2. verify with --worker carrying max_output_tokens
	home := t.TempDir()
	workerPath := filepath.Join(dir, "worker.json")
	desc := map[string]any{
		"name": "w-verify", "provider": "fake", "model": "fake-model",
		"env_var": "KEY", "usage": "none",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		"harness_args":      []string{"run", "--model", "{model}", "--", "{prompt}"},
		"secret":            "KEY",
		"max_output_tokens": 2048,
	}
	raw, _ := json.MarshalIndent(desc, "", "  ")
	if err := os.WriteFile(workerPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	rc = run([]string{
		"verify", "--result", resultPath, "--contract", "CONTRACT LINE OK",
		"--label", "test-label", "--worker", workerPath,
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("verify with worker failed exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}

	r, err = swarm.ReadReceipt(receiptPath)
	if err != nil {
		t.Fatalf("failed reading receipt: %v", err)
	}
	if r.RequestedMaxOutputTokens != 2048 {
		t.Errorf("expected RequestedMaxOutputTokens 2048, got %d", r.RequestedMaxOutputTokens)
	}

	// 3. verify refuses conflicting --max-output-tokens and --worker
	stdout.Reset()
	stderr.Reset()
	rc = run([]string{
		"verify", "--result", resultPath, "--contract", "CONTRACT LINE OK",
		"--label", "test-label", "--worker", workerPath,
		"--max-output-tokens", "1000",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("verify with conflicting flags must exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	mustContain(t, "conflict refusal", stderr.String(), "differs from the worker description's max_output_tokens")
}

// TestLoopbackRequestCaptureWithCountedRetries: loopback server captures dummy credentials
// and forwarded max output token bounds in request headers and body, with counted retries on 5xx.
func TestLoopbackRequestCaptureWithCountedRetries(t *testing.T) {
	origDelay := swarm.ProviderRetryDelay
	swarm.ProviderRetryDelay = func(int) time.Duration { return 0 }
	defer func() { swarm.ProviderRetryDelay = origDelay }()

	type capturedReq struct {
		authHeader      string
		novaTokenHeader string
		openCodeHeader  string
		body            string
	}
	var captured []capturedReq
	var attempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		b, _ := io.ReadAll(r.Body)
		captured = append(captured, capturedReq{
			authHeader:      r.Header.Get("Authorization"),
			novaTokenHeader: r.Header.Get("X-Nova-Max-Output-Tokens"),
			openCodeHeader:  r.Header.Get("X-Opencode-Output-Token-Max"),
			body:            string(b),
		})
		if att == 1 {
			// First attempt returns 503 to trigger launch retry
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "server unavailable")
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	}))
	defer ts.Close()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	// FAKE-LOOPBACK calls ts.URL. On attempt 1 it gets 503, emits provider 5xx error, and exits 1.
	// nativeRun catches the fast launch failure and retries attempt 2, which gets 200 OK and exits 0.
	cardContent := fmt.Sprintf("a loopback test card\nFAKE-LOOPBACK %s\n", ts.URL)
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--model", "fake/fake-model",
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall",
		"--max-output-tokens", "5000",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	if rc != 0 {
		t.Fatalf("native run with launch retry expected exit 0, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected 2 counted attempts against loopback server, got %d", attempts)
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(captured))
	}

	// Verify attempt 1 request
	first := captured[0]
	if first.authHeader != "Bearer dummy-token" {
		t.Errorf("attempt 1: expected dummy auth header 'Bearer dummy-token', got %q", first.authHeader)
	}
	if first.novaTokenHeader != "5000" {
		t.Errorf("attempt 1: expected X-Nova-Max-Output-Tokens 5000, got %q", first.novaTokenHeader)
	}
	if first.openCodeHeader != "5000" {
		t.Errorf("attempt 1: expected X-Opencode-Output-Token-Max 5000, got %q", first.openCodeHeader)
	}
	if !strings.Contains(first.body, `"max_output_tokens":"5000"`) {
		t.Errorf("attempt 1: expected body to contain max_output_tokens:5000, got %q", first.body)
	}

	// Verify attempt 2 request
	second := captured[1]
	if second.authHeader != "Bearer dummy-token" {
		t.Errorf("attempt 2: expected dummy auth header 'Bearer dummy-token', got %q", second.authHeader)
	}
	if second.novaTokenHeader != "5000" {
		t.Errorf("attempt 2: expected X-Nova-Max-Output-Tokens 5000, got %q", second.novaTokenHeader)
	}
	if second.openCodeHeader != "5000" {
		t.Errorf("attempt 2: expected X-Opencode-Output-Token-Max 5000, got %q", second.openCodeHeader)
	}
	if !strings.Contains(second.body, `"max_output_tokens":"5000"`) {
		t.Errorf("attempt 2: expected body to contain max_output_tokens:5000, got %q", second.body)
	}

	// Verify NATIVE OK line on final success
	mustContain(t, "NATIVE OK output", stdout.String(), "requested_max_output_tokens=5000")
}

// TestLoopbackRequestCaptureExhaustsRetries verifies that when 503 errors persist,
// nativeRun exhausts retries (attempts == swarm.MaxProviderAttempts) and exits 1.
func TestLoopbackRequestCaptureExhaustsRetries(t *testing.T) {
	origDelay := swarm.ProviderRetryDelay
	swarm.ProviderRetryDelay = func(int) time.Duration { return 0 }
	defer func() { swarm.ProviderRetryDelay = origDelay }()

	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "persistent 503")
	}))
	defer ts.Close()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	cardContent := fmt.Sprintf("persistent fail card\nFAKE-LOOPBACK %s\n", ts.URL)
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--model", "fake/fake-model",
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall",
		"--max-output-tokens", "4000",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	if rc != 1 {
		t.Fatalf("exhausted retries expected exit 1, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if got := int(atomic.LoadInt32(&attempts)); got != swarm.MaxProviderAttempts {
		t.Errorf("expected %d retry attempts, got %d", swarm.MaxProviderAttempts, got)
	}
}

