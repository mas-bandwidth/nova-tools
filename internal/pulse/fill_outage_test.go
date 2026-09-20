package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #2001 & #2011: Fleet wide provider outage simulation in pulse fill.
//
// Matches the hulk 40/40 outage from issue #2001:
// - Simulates 40 cards on bench hulk encountering provider faults (UnknownError and 5xx).
// - Proves that the provider error tripwire trips for the primary route.
// - Proves that subsequent cards automatically spill over to secondary and tertiary providers in cost order.
func TestFleetWideProviderOutageSimulation(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	launched := filepath.Join(dir, "launched")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}

	const benchName = "hulk"
	const concurrentCards = 40

	const primaryRoute = "deepseek-direct"
	const secondaryRoute = "opencode-deepseek"
	const tertiaryRoute = "openrouter-deepseek"

	// Provider registry with 3 tiers in cost order:
	// Primary: deepseek-direct ($0.14)
	// Secondary: opencode-deepseek ($0.25)
	// Tertiary: openrouter-deepseek ($0.50)
	provTSV := filepath.Join(dir, "providers.tsv")
	provContent := strings.Join([]string{
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		fmt.Sprintf("%s\tdeepseek\tdeepseek-v4-flash\tflash\t40\t0.14\t0.20\tprimary", primaryRoute),
		fmt.Sprintf("%s\topencode\tdeepseek-v4-flash\tflash\t40\t0.25\t0.20\tsecondary", secondaryRoute),
		fmt.Sprintf("%s\topenrouter\tdeepseek-v4-flash\tflash\t40\t0.50\t0.20\ttertiary", tertiaryRoute),
	}, "\n") + "\n"
	if err := os.WriteFile(provTSV, []byte(provContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Simulate 40 prior cards that failed with provider errors (matching hulk 40/40 outage)
	// Markers placed in ready directory as `.failed-<n>` markers with route=deepseek-direct
	now := time.Now().UTC()
	for i := 0; i < concurrentCards; i++ {
		cardName := fmt.Sprintf("card-hulk-%02d.md", i)
		markerPath := filepath.Join(ready, cardName+".failed-1")
		markerContent := fmt.Sprintf("%s\tattempt=1\troute=%s\twhy=unexpected-server-error\n",
			now.Add(-time.Duration(concurrentCards-i)*time.Second).Format(time.RFC3339),
			primaryRoute)
		if err := os.WriteFile(markerPath, []byte(markerContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 2. Prepare 10 new cards to fill
	for i := 0; i < 10; i++ {
		cardName := fmt.Sprintf("card-next-%02d.md", i)
		writeCard(t, ready, cardName, "TIER: flash\nfix the named bug\n")
	}

	launcher := &laneLauncher{}
	var out, errb bytes.Buffer

	code := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Machines:  machinesFile(t, dir, []string{benchName}, nil),
		Providers: provTSV,
		Benches:   []string{benchName},
		Once:      true,
		Stdout:    &out,
		Stderr:    &errb,
		Capacity:  laneCap{benchName: 10},
		Launcher:  launcher,
	})

	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}

	// Prove that all 10 cards launched
	if len(launcher.calls) != 10 {
		t.Fatalf("expected 10 launched calls, got %d: %v", len(launcher.calls), launcher.calls)
	}

	// Prove that all 10 spilled over to secondaryRoute ("opencode-deepseek", cost 0.25)
	// because primaryRoute ("deepseek-direct") tripped its provider error tripwire
	for i := 0; i < 10; i++ {
		cardName := fmt.Sprintf("card-next-%02d.md", i)
		m := readLaunchedMarker(launched, cardName)
		if m["route"] != secondaryRoute {
			t.Fatalf("card %s route = %q, want secondary route %q (spillover from tripped primary)",
				cardName, m["route"], secondaryRoute)
		}
		if m["provider"] != "opencode" {
			t.Fatalf("card %s provider = %q, want %q", cardName, m["provider"], "opencode")
		}
	}

	// 3. Now simulate secondaryRoute ALSO tripping its error threshold
	// Add 40 failure markers for secondaryRoute to trip its circuit breaker too
	for i := 0; i < concurrentCards; i++ {
		cardName := fmt.Sprintf("card-sec-fail-%02d.md", i)
		markerPath := filepath.Join(ready, cardName+".failed-1")
		markerContent := fmt.Sprintf("%s\tattempt=1\troute=%s\twhy=unexpected-server-error\n",
			now.Add(-time.Duration(concurrentCards-i)*time.Second).Format(time.RFC3339),
			secondaryRoute)
		if err := os.WriteFile(markerPath, []byte(markerContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Prepare 5 more cards
	for i := 0; i < 5; i++ {
		cardName := fmt.Sprintf("card-tertiary-%02d.md", i)
		writeCard(t, ready, cardName, "TIER: flash\nfix the named bug\n")
	}

	launcher2 := &laneLauncher{}
	var out2, errb2 bytes.Buffer

	code2 := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Machines:  machinesFile(t, dir, []string{benchName}, nil),
		Providers: provTSV,
		Benches:   []string{benchName},
		Once:      true,
		Stdout:    &out2,
		Stderr:    &errb2,
		Capacity:  laneCap{benchName: 5},
		Launcher:  launcher2,
	})

	if code2 != 0 {
		t.Fatalf("second fill exit = %d, want 0; stderr=%q", code2, errb2.String())
	}

	if len(launcher2.calls) != 5 {
		t.Fatalf("expected 5 launched calls, got %d: %v", len(launcher2.calls), launcher2.calls)
	}

	// Prove that all 5 cards automatically spilled over to tertiaryRoute ("openrouter-deepseek", cost 0.50)
	// in cost order because both primary and secondary tripped
	for i := 0; i < 5; i++ {
		cardName := fmt.Sprintf("card-tertiary-%02d.md", i)
		m := readLaunchedMarker(launched, cardName)
		if m["route"] != tertiaryRoute {
			t.Fatalf("card %s route = %q, want tertiary route %q (spillover from tripped secondary)",
				cardName, m["route"], tertiaryRoute)
		}
		if m["provider"] != "openrouter" {
			t.Fatalf("card %s provider = %q, want %q", cardName, m["provider"], "openrouter")
		}
	}
}
