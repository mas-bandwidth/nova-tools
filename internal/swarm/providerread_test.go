package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProviderReadDeadlinesStayUnderNinetySeconds(t *testing.T) {
	if ProviderHeaderTimeout <= 0 || ProviderHeaderTimeout >= 90*time.Second {
		t.Fatalf("header deadline is %s, want a black-holed attempt under 90s", ProviderHeaderTimeout)
	}
	if ProviderChunkTimeout <= 0 || ProviderChunkTimeout >= 90*time.Second {
		t.Fatalf("chunk deadline is %s, want a stalled stream under 90s", ProviderChunkTimeout)
	}
	if ProviderBodySilence != 45*time.Second || ProviderBodySilence >= 90*time.Second {
		t.Fatalf("body silence is %s, want 45s and under 90s", ProviderBodySilence)
	}
}

func TestApplyProviderReadDeadlineWritesBothAndKeepsTheKey(t *testing.T) {
	in := []byte(`{"provider":{"deepseek":{"options":{"apiKey":"k","baseURL":"https://example.invalid"}}}}`)
	out := ApplyProviderReadDeadline(in, "deepseek")
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	opts := cfg["provider"].(map[string]any)["deepseek"].(map[string]any)["options"].(map[string]any)
	if opts["apiKey"] != "k" || opts["baseURL"] != "https://example.invalid" {
		t.Fatalf("the existing options moved: %#v", opts)
	}
	if opts["headerTimeout"] != float64(ProviderHeaderTimeout/time.Millisecond) {
		t.Fatalf("headerTimeout = %v", opts["headerTimeout"])
	}
	if opts["chunkTimeout"] != float64(ProviderChunkTimeout/time.Millisecond) {
		t.Fatalf("chunkTimeout = %v", opts["chunkTimeout"])
	}
}

func TestScoreCardHoldsAnUnknownAcceptanceEvenWhenAResultExists(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "1", "jobs", "a")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("a card line 1\nDONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "provider-acceptance"), []byte("unknown\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, reason, _, _ := scoreCard(root, batchCard{label: "a", slot: 1, contract: "a card line 1"}, false, false, false, 0, 0, filepath.Join(job, "harness.log"), "")
	if state != "hold" || reason != "unknown-acceptance" {
		t.Fatalf("a result beside an unknown acceptance scored %s %s", state, reason)
	}
}

func TestAcceptanceUnknownHoldsAnEmptyMarker(t *testing.T) {
	job := t.TempDir()
	if err := os.WriteFile(filepath.Join(job, "provider-acceptance"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !AcceptanceUnknown(job) {
		t.Fatal("an empty marker was treated as reconciled")
	}
}

func TestAcceptanceUnknownFailsClosedWhenTheMarkerCannotBeRead(t *testing.T) {
	job := t.TempDir()
	if err := os.Mkdir(filepath.Join(job, "provider-acceptance"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !AcceptanceUnknown(job) {
		t.Fatal("an unreadable marker was treated as an ordinary missing result")
	}
}

func TestApplyProviderReadDeadlineLeavesAnUnreadableConfig(t *testing.T) {
	in := []byte(`not json`)
	out := ApplyProviderReadDeadline(in, "deepseek")
	if string(out) != string(in) {
		t.Fatalf("an unreadable config was rewritten:\n%s", out)
	}
}

func TestApplyProviderReadDeadlineCreatesAMissingProvider(t *testing.T) {
	out := ApplyProviderReadDeadline([]byte(`{}`), "deepseek")
	if !strings.Contains(string(out), `"headerTimeout"`) || !strings.Contains(string(out), `"chunkTimeout"`) {
		t.Fatalf("a built-in provider with no entry got no deadline:\n%s", out)
	}
}

func TestALostResponseIsNotALaunchFailure(t *testing.T) {
	// The server accepted the request, then the response never came. That is
	// UNKNOWN. A second launch would be a second request.
	for _, tail := range []string{
		"SSE read timed out",
		"Provider response headers timed out after 45000ms",
		"Headers Timeout Error",
		"HeadersTimeoutError: Headers Timeout Error",
	} {
		if _, ok := ProviderLaunchFailure([]byte(tail)); ok {
			t.Errorf("%q is classified as a launch failure; it must not retry", tail)
		}
		if !LostResponse([]byte(tail)) {
			t.Errorf("%q was not recognized as a lost response", tail)
		}
	}
	if _, ok := ProviderLaunchFailure([]byte("Unexpected server error ref=err_fake")); !ok {
		t.Fatal("the inherited classifier still matches a server-error tail")
	}
	// The match is the tail text. It is not evidence the provider never accepted the request.
	if LostResponse([]byte("Unexpected server error ref=err_fake")) {
		t.Fatal("a server error is not a lost response")
	}
	if LostResponse([]byte("go test ran with -timeout 30s and passed")) {
		t.Fatal("a card's own timeout word is not a lost provider response")
	}
}
