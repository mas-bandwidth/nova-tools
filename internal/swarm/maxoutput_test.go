package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for Row 5: max_output_tokens worker field, environment forwarding, and requested-receipt slice.

func TestWorkerMaxOutputTokensValidation(t *testing.T) {
	dir := t.TempDir()

	writeWorkerJSON := func(t *testing.T, filename, content string) string {
		t.Helper()
		p := filepath.Join(dir, filename)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("omitted preserves nil", func(t *testing.T) {
		p := writeWorkerJSON(t, "omitted.json", fmt.Sprintf(`{
			"name": "w-omitted",
			"provider": "opencode",
			"model": "model-a",
			"env_var": "KEY",
			"key_file": "dummy.key",
			"harness": "opencode",
			"usage": "none",
			"harness_args": ["run", "--model", "{model}", "--", "{prompt}"],
			"worker_dir": %q,
			"deadline": "5m"
		}`, home))
		w, problems := LoadWorker(p)
		if len(problems) > 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
		if w.MaxOutputTokens != nil {
			t.Errorf("expected MaxOutputTokens to be nil when omitted, got %d", *w.MaxOutputTokens)
		}
		if w.HasMaxOutputTokens() {
			t.Errorf("HasMaxOutputTokens() returned true, want false")
		}
	})

	t.Run("positive count accepted", func(t *testing.T) {
		p := writeWorkerJSON(t, "positive.json", fmt.Sprintf(`{
			"name": "w-pos",
			"provider": "opencode",
			"model": "model-a",
			"env_var": "KEY",
			"key_file": "dummy.key",
			"harness": "opencode",
			"usage": "none",
			"harness_args": ["run", "--model", "{model}", "--", "{prompt}"],
			"worker_dir": %q,
			"deadline": "5m",
			"max_output_tokens": 4096
		}`, home))
		w, problems := LoadWorker(p)
		if len(problems) > 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
		if w.MaxOutputTokens == nil {
			t.Fatal("expected MaxOutputTokens to be non-nil")
		}
		if *w.MaxOutputTokens != 4096 {
			t.Errorf("expected 4096, got %d", *w.MaxOutputTokens)
		}
		if !w.HasMaxOutputTokens() {
			t.Errorf("HasMaxOutputTokens() returned false, want true")
		}
	})

	t.Run("explicit zero refused", func(t *testing.T) {
		p := writeWorkerJSON(t, "zero.json", fmt.Sprintf(`{
			"name": "w-zero",
			"provider": "opencode",
			"model": "model-a",
			"env_var": "KEY",
			"key_file": "dummy.key",
			"harness": "opencode",
			"usage": "none",
			"harness_args": ["run", "--model", "{model}", "--", "{prompt}"],
			"worker_dir": %q,
			"deadline": "5m",
			"max_output_tokens": 0
		}`, home))
		_, problems := LoadWorker(p)
		if len(problems) == 0 {
			t.Fatal("expected problem for explicit zero max_output_tokens, got none")
		}
		found := false
		for _, err := range problems {
			if strings.Contains(err.Error(), "max_output_tokens wants a positive token count, got 0") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected refusal mentioning positive token count, got: %v", problems)
		}
	})

	t.Run("negative count refused", func(t *testing.T) {
		p := writeWorkerJSON(t, "neg.json", fmt.Sprintf(`{
			"name": "w-neg",
			"provider": "opencode",
			"model": "model-a",
			"env_var": "KEY",
			"key_file": "dummy.key",
			"harness": "opencode",
			"usage": "none",
			"harness_args": ["run", "--model", "{model}", "--", "{prompt}"],
			"worker_dir": %q,
			"deadline": "5m",
			"max_output_tokens": -500
		}`, home))
		_, problems := LoadWorker(p)
		if len(problems) == 0 {
			t.Fatal("expected problem for negative max_output_tokens, got none")
		}
		found := false
		for _, err := range problems {
			if strings.Contains(err.Error(), "max_output_tokens wants a positive token count, got -500") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected refusal mentioning positive token count, got: %v", problems)
		}
	})
}

func TestChildEnvOutputTokensForwarding(t *testing.T) {
	t.Run("forwarded when set", func(t *testing.T) {
		n := 2048
		w := Worker{
			WorkerDir:       t.TempDir(),
			MaxOutputTokens: &n,
		}
		env := childEnv(w, 1, "job-1", "dummy-key")
		var hasOpenCode, hasNova bool
		for _, e := range env {
			if e == "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=2048" {
				hasOpenCode = true
			}
			if e == "NOVA_WORKER_MAX_OUTPUT_TOKENS=2048" {
				hasNova = true
			}
		}
		if !hasOpenCode {
			t.Errorf("childEnv missing OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=2048: %v", env)
		}
		if !hasNova {
			t.Errorf("childEnv missing NOVA_WORKER_MAX_OUTPUT_TOKENS=2048: %v", env)
		}
	})

	t.Run("omitted when unset", func(t *testing.T) {
		w := Worker{
			WorkerDir: t.TempDir(),
		}
		env := childEnv(w, 1, "job-1", "dummy-key")
		for _, e := range env {
			if strings.HasPrefix(e, "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=") {
				t.Errorf("unexpected OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX in childEnv: %s", e)
			}
			if strings.HasPrefix(e, "NOVA_WORKER_MAX_OUTPUT_TOKENS=") {
				t.Errorf("unexpected NOVA_WORKER_MAX_OUTPUT_TOKENS in childEnv: %s", e)
			}
		}
	})
}

func TestReceiptRequestedMaxOutputTokensRoundtrip(t *testing.T) {
	dir := t.TempDir()
	receiptPath := filepath.Join(dir, "test.receipt")

	c := Contract{
		Label:                    "card-test",
		ContractLine:             "PASS",
		Card:                     []byte("test card content"),
		HaveRun:                  true,
		WallSeconds:              42,
		ExitCode:                 0,
		HaveUsage:                true,
		TokensIn:                 100,
		TokensOut:                50,
		USD:                      "0.0012",
		RequestedMaxOutputTokens: 8192,
	}
	out := Outcome{
		Line:      "OK card-test",
		Line2:     "pass",
		LineCount: 5,
		OK:        true,
	}

	if err := WriteReceipt(receiptPath, out, c); err != nil {
		t.Fatalf("WriteReceipt failed: %v", err)
	}

	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "requested_max_output_tokens=8192\n") {
		t.Errorf("expected receipt to contain requested_max_output_tokens=8192, got:\n%s", string(raw))
	}

	r, err := ReadReceipt(receiptPath)
	if err != nil {
		t.Fatalf("ReadReceipt failed: %v", err)
	}
	if r.Label != "card-test" {
		t.Errorf("expected label card-test, got %s", r.Label)
	}
	if r.WallSeconds != 42 {
		t.Errorf("expected WallSeconds 42, got %d", r.WallSeconds)
	}
	if r.ExitCode != 0 {
		t.Errorf("expected ExitCode 0, got %d", r.ExitCode)
	}
	if r.TokensIn != 100 || r.TokensOut != 50 {
		t.Errorf("expected tokens 100/50, got %d/%d", r.TokensIn, r.TokensOut)
	}
	if r.USD != "0.0012" {
		t.Errorf("expected usd 0.0012, got %s", r.USD)
	}
	if r.RequestedMaxOutputTokens != 8192 {
		t.Errorf("expected RequestedMaxOutputTokens 8192, got %d", r.RequestedMaxOutputTokens)
	}

	// Now verify omitting requested_max_output_tokens when 0
	c.RequestedMaxOutputTokens = 0
	receiptPath2 := filepath.Join(dir, "test2.receipt")
	if err := WriteReceipt(receiptPath2, out, c); err != nil {
		t.Fatalf("WriteReceipt failed: %v", err)
	}
	raw2, err := os.ReadFile(receiptPath2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw2), "requested_max_output_tokens") {
		t.Errorf("receipt should not contain requested_max_output_tokens when 0, got:\n%s", string(raw2))
	}
	r2, err := ReadReceipt(receiptPath2)
	if err != nil {
		t.Fatalf("ReadReceipt failed: %v", err)
	}
	if r2.RequestedMaxOutputTokens != 0 {
		t.Errorf("expected RequestedMaxOutputTokens 0, got %d", r2.RequestedMaxOutputTokens)
	}
}
