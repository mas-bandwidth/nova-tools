package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// providersFile writes a provider registry TSV file for tests.
func providersFile(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "providers.tsv")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestFillProviderSpilloverWhenCeilingReached tests that cards spill over to alternate
// provider routes when the primary route hits its concurrency ceiling.
func TestFillProviderSpilloverWhenCeilingReached(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")

	writeCard(t, ready, "card-001.md", "RESULT CARD-001\nTIER: flash\n")
	writeCard(t, ready, "card-002.md", "RESULT CARD-002\nTIER: flash\n")

	provFile := providersFile(t, dir,
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		"primary\tdeepseek\tdeepseek-chat\tflash\t1\t0.05\t0.20\t-",
		"secondary\topenrouter\tdeepseek-chat\tflash\t5\t0.10\t0.20\t-",
	)

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Machines:  machinesFile(t, dir, []string{"bench-a"}, nil),
		Providers: provFile,
		Benches:   []string{"bench-a"},
		Once:      true,
		Stdout:    &out,
		Stderr:    &errb,
		Capacity:  laneCap{"bench-a": 10},
		Launcher:  l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 2 {
		t.Fatalf("launcher calls = %d, want 2: %v", len(l.calls), l.calls)
	}

	// Card 1 should take the cheaper primary route (ceiling = 1)
	m1 := readLaunchedMarker(launched, "card-001.md")
	if m1["route"] != "primary" || m1["provider"] != "deepseek" || m1["model"] != "deepseek-chat" {
		t.Errorf("card-001 marker = %v, want route=primary, provider=deepseek, model=deepseek-chat", m1)
	}

	// Card 2 should spill over to the secondary route since primary reached ceiling 1
	m2 := readLaunchedMarker(launched, "card-002.md")
	if m2["route"] != "secondary" || m2["provider"] != "openrouter" || m2["model"] != "deepseek-chat" {
		t.Errorf("card-002 marker = %v, want route=secondary, provider=openrouter, model=deepseek-chat", m2)
	}
}

// TestFillProviderSpilloverFromExistingLaunchedInFlight verifies that cards spill over
// when an existing card in launched already saturates the primary route's ceiling.
func TestFillProviderSpilloverFromExistingLaunchedInFlight(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-existing card in launched occupying primary route
	if err := os.WriteFile(filepath.Join(launched, "card-000.md"), []byte("RESULT CARD-000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launched, "card-000.md.launched"), []byte("route=primary\nprovider=deepseek\nmodel=deepseek-chat\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeCard(t, ready, "card-001.md", "RESULT CARD-001\nTIER: flash\n")

	provFile := providersFile(t, dir,
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		"primary\tdeepseek\tdeepseek-chat\tflash\t1\t0.05\t0.20\t-",
		"secondary\topenrouter\tdeepseek-chat\tflash\t5\t0.10\t0.20\t-",
	)

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Machines:  machinesFile(t, dir, []string{"bench-a"}, nil),
		Providers: provFile,
		Benches:   []string{"bench-a"},
		Once:      true,
		Stdout:    &out,
		Stderr:    &errb,
		Capacity:  laneCap{"bench-a": 10},
		Launcher:  l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1: %v", len(l.calls), l.calls)
	}

	m1 := readLaunchedMarker(launched, "card-001.md")
	if m1["route"] != "secondary" {
		t.Errorf("card-001 route = %q, want secondary (primary saturated by card-000)", m1["route"])
	}
}

// TestFillProviderExceedingErrorThresholdSkipped tests that routes exceeding their
// rolling error threshold are skipped, spilling over to the next eligible route.
func TestFillProviderExceedingErrorThresholdSkipped(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}

	// Completion markers for primary: 1 success, 1 failure => 50% error rate (> 20% threshold)
	if err := os.WriteFile(filepath.Join(launched, "c1.done"), []byte("route=primary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launched, "c2.failed"), []byte("route=primary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeCard(t, ready, "card-001.md", "RESULT CARD-001\nTIER: flash\n")

	provFile := providersFile(t, dir,
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		"primary\tdeepseek\tdeepseek-chat\tflash\t5\t0.05\t0.20\t-",
		"secondary\topenrouter\tdeepseek-chat\tflash\t5\t0.10\t0.20\t-",
	)

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Machines:  machinesFile(t, dir, []string{"bench-a"}, nil),
		Providers: provFile,
		Benches:   []string{"bench-a"},
		Once:      true,
		Stdout:    &out,
		Stderr:    &errb,
		Capacity:  laneCap{"bench-a": 10},
		Launcher:  l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1: %v", len(l.calls), l.calls)
	}

	m1 := readLaunchedMarker(launched, "card-001.md")
	if m1["route"] != "secondary" || m1["provider"] != "openrouter" {
		t.Errorf("card-001 route = %v, want route=secondary, provider=openrouter (primary tripped by error rate)", m1)
	}
}

// TestFillProviderMarkerRecording tests that writeLaunchedMarker records route,
// provider, and model alongside lane, bench, label, session, card, and at.
func TestFillProviderMarkerRecording(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")

	writeCard(t, ready, "card-001.md", "RESULT CARD-001 sha=fedcba\nTIER: pro\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")

	provFile := providersFile(t, dir,
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		"mercury-pro\tmercury\tmercury-coder\tpro\t10\t0.25\t0.15\tfast pro route",
	)

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Lanes:     lanes,
		Machines:  machinesFile(t, dir, []string{"bench-a"}, nil),
		Providers: provFile,
		Benches:   []string{"bench-a"},
		Session:   "sess-99",
		Once:      true,
		Stdout:    &out,
		Stderr:    &errb,
		Capacity:  laneCap{"bench-a": 10},
		Launcher:  l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}

	raw, err := os.ReadFile(filepath.Join(launched, "card-001.md.launched"))
	if err != nil {
		t.Fatalf("no launched marker found: %v", err)
	}
	markerText := string(raw)

	for _, want := range []string{
		"route=mercury-pro",
		"provider=mercury",
		"model=mercury-coder",
		"lane=pulse",
		"bench=bench-a",
		"label=card-001",
		"session=sess-99",
		"card=card-001.md",
		"at=",
	} {
		if !strings.Contains(markerText, want) {
			t.Errorf("launched marker missing %q:\n%s", want, markerText)
		}
	}
}

// TestFillProviderAllRoutesSaturatedHeld verifies that when all matching routes
// are saturated, cards are held and remain in ready.
func TestFillProviderAllRoutesSaturatedHeld(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")

	writeCard(t, ready, "card-001.md", "RESULT CARD-001\nTIER: flash\n")

	provFile := providersFile(t, dir,
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		"primary\tdeepseek\tdeepseek-chat\tflash\t0\t0.05\t0.20\t-",
	)

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready:     ready,
		Launched:  launched,
		Machines:  machinesFile(t, dir, []string{"bench-a"}, nil),
		Providers: provFile,
		Benches:   []string{"bench-a"},
		Once:      true,
		Stdout:    &out,
		Stderr:    &errb,
		Capacity:  laneCap{"bench-a": 10},
		Launcher:  l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 (route saturated)", len(l.calls))
	}
	if !strings.Contains(out.String(), "FILL HELD card=card-001.md tier=flash reason=providers-saturated") {
		t.Errorf("stdout missing FILL HELD notification:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(ready, "card-001.md")); err != nil {
		t.Errorf("card was not kept in ready: %v", err)
	}
}
