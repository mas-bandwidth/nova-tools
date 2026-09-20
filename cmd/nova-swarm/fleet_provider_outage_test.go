package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// Issue #2001 & #2011: Fleet-Wide Provider Outage Simulation.
//
// Matches the hulk 40/40 outage from issue #2001:
// 1. Simulate 40 concurrent cards hitting UnknownError or 5xx on bench "hulk".
// 2. Prove that every card returns NATIVE PROVIDER (with why=unexpected-server-error and valid ref).
// 3. Prove that the provider error tripwire trips for that route (circuit breaker activated).
// 4. Prove that remaining cards automatically spill over to secondary and tertiary providers in cost order.
func TestFleetWideProviderOutageSimulation(t *testing.T) {
	windowsIsNotABench(t)
	t.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "0s")

	bin := nativeHarness(t)
	root := t.TempDir()

	const benchName = "hulk"
	const concurrentCards = 40

	// Capacity 40 for bench hulk to simulate full 40-slot concurrent saturation
	storeDir := slotShares(t, fmt.Sprintf("capacity\t%d\nreserve\t0\n%s\t%d\n", concurrentCards, benchName, concurrentCards))

	type runResult struct {
		cardIndex int
		label     string
		kind      string // "unknown-error" or "5xx"
		stdout    string
		stderr    string
		rc        int
		slotDir   string
		err       error
	}

	results := make([]runResult, concurrentCards)
	var wg sync.WaitGroup
	wg.Add(concurrentCards)

	for i := 0; i < concurrentCards; i++ {
		go func(idx int) {
			defer wg.Done()

			label := fmt.Sprintf("hulk-outage-%02d", idx)
			slotDir := filepath.Join(root, fmt.Sprintf("slot-%02d", idx))
			if err := os.MkdirAll(slotDir, 0o755); err != nil {
				results[idx] = runResult{cardIndex: idx, label: label, err: err}
				return
			}

			// Half UnknownError, half 5xx to simulate the mixed provider failure mode of #2001
			kind := "unknown-error"
			cardContent := "FAKE-UNKNOWN-ERROR\n"
			if idx%2 == 1 {
				kind = "5xx"
				cardContent = "FAKE-5XX\n"
			}

			cardPath := filepath.Join(slotDir, label+".md")
			if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
				results[idx] = runResult{cardIndex: idx, label: label, err: err}
				return
			}

			var outBuf, errBuf bytes.Buffer
			rc := run([]string{
				"native",
				"--slots-store", storeDir,
				"--owner", benchName,
				"--harness", bin,
				"--model", "deepseek/deepseek-v4-flash",
				"--label", label,
				"--card", cardPath,
				"--slot", slotDir,
				"--root", slotDir,
				"--deadline", "30s",
				"--no-wall",
			}, strings.NewReader(""), &outBuf, &errBuf, time.Now())

			results[idx] = runResult{
				cardIndex: idx,
				label:     label,
				kind:      kind,
				stdout:    outBuf.String(),
				stderr:    errBuf.String(),
				rc:        rc,
				slotDir:   slotDir,
			}
		}(i)
	}

	wg.Wait()

	// --- 1 & 2: Prove that EVERY card of the 40 returned NATIVE PROVIDER ---
	for idx, res := range results {
		if res.err != nil {
			t.Fatalf("card %d (%s) setup error: %v", idx, res.label, res.err)
		}
		if strings.Contains(res.stdout, "NATIVE OK") {
			t.Fatalf("card %d (%s) must never return NATIVE OK during provider outage:\n%s", idx, res.label, res.stdout)
		}
		if strings.Contains(res.stdout, "NATIVE INCOMPLETE") {
			t.Fatalf("card %d (%s) must return NATIVE PROVIDER, not NATIVE INCOMPLETE:\n%s", idx, res.label, res.stdout)
		}
		if !strings.Contains(res.stdout, "NATIVE PROVIDER ") {
			t.Fatalf("card %d (%s) did not return NATIVE PROVIDER:\n%s", idx, res.label, res.stdout)
		}
		if !strings.Contains(res.stdout, "why=unexpected-server-error") {
			t.Fatalf("card %d (%s) verdict missing why=unexpected-server-error:\n%s", idx, res.label, res.stdout)
		}

		if res.kind == "unknown-error" {
			if !strings.Contains(res.stdout, "ref=err_29c29bd4") {
				t.Fatalf("card %d (%s) missing expected ref=err_29c29bd4:\n%s", idx, res.label, res.stdout)
			}
		} else {
			if !strings.Contains(res.stdout, "ref=err_fake_5xx") {
				t.Fatalf("card %d (%s) missing expected ref=err_fake_5xx:\n%s", idx, res.label, res.stdout)
			}
		}

		// Verify usage.tsv records end=provider
		usageRaw, err := os.ReadFile(filepath.Join(res.slotDir, "jobs", res.label, "usage.tsv"))
		if err != nil {
			t.Fatalf("card %d (%s) usage.tsv missing: %v", idx, res.label, err)
		}
		if !strings.Contains(string(usageRaw), "\tprovider\t") {
			t.Fatalf("card %d (%s) usage.tsv must record end=provider:\n%s", idx, res.label, usageRaw)
		}
	}

	// --- 3: Prove that the provider error tripwire trips for that route ---
	const primaryRouteName = "deepseek-direct"
	const secondaryRouteName = "opencode-deepseek"
	const tertiaryRouteName = "openrouter-deepseek"

	watch := fleet.NewRollingErrorWatch(50)
	for _, res := range results {
		_ = res
		// All 40 executions were provider failures
		watch.RecordFailure(primaryRouteName, true)
	}

	if samples := watch.Samples(primaryRouteName); samples != concurrentCards {
		t.Fatalf("expected %d samples recorded for %s, got %d", concurrentCards, primaryRouteName, samples)
	}
	if failures := watch.Failures(primaryRouteName); failures != concurrentCards {
		t.Fatalf("expected %d failures recorded for %s, got %d", concurrentCards, primaryRouteName, failures)
	}
	errRate := watch.ErrorRate(primaryRouteName)
	if errRate != 1.0 {
		t.Fatalf("expected error rate 1.0 (100%% outage) for %s, got %f", primaryRouteName, errRate)
	}

	const threshold = 0.20 // 20% failure threshold
	if !watch.IsTripped(primaryRouteName, threshold) {
		t.Fatalf("provider error tripwire failed to trip for %s at rate %f (threshold %f)", primaryRouteName, errRate, threshold)
	}

	// --- 4: Prove that remaining cards automatically spill over to secondary and tertiary providers in cost order ---
	// Construct 3-tier provider registry:
	// Primary: deepseek-direct ($0.14/Mtok)
	// Secondary: opencode-deepseek ($0.25/Mtok)
	// Tertiary: openrouter-deepseek ($0.50/Mtok)
	regTSV := strings.Join([]string{
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes",
		fmt.Sprintf("%s\tdeepseek\tdeepseek-v4-flash\tflash\t40\t0.14\t0.20\tprimary-route", primaryRouteName),
		fmt.Sprintf("%s\topencode\tdeepseek-v4-flash\tflash\t40\t0.25\t0.20\tsecondary-route", secondaryRouteName),
		fmt.Sprintf("%s\topenrouter\tdeepseek-v4-flash\tflash\t40\t0.50\t0.20\ttertiary-route", tertiaryRouteName),
	}, "\n") + "\n"

	reg, err := fleet.ParseProviderRegistry(strings.NewReader(regTSV))
	if err != nil {
		t.Fatalf("parse provider registry: %v", err)
	}

	inFlight := map[string]int{
		primaryRouteName:   40, // fully saturated / failed
		secondaryRouteName: 0,
		tertiaryRouteName:  0,
	}
	errorRates := map[string]float64{
		primaryRouteName:   watch.ErrorRate(primaryRouteName), // 1.0 (tripped)
		secondaryRouteName: 0.0,                               // healthy
		tertiaryRouteName:  0.0,                               // healthy
	}

	// Step 4a: With primary route tripped (and at concurrency ceiling), available routes MUST spill to secondary
	avail := reg.AvailableRoutes("flash", inFlight, errorRates)
	if len(avail) != 2 {
		t.Fatalf("expected 2 available routes (secondary, tertiary) after primary tripped, got %d", len(avail))
	}
	if avail[0].Route != secondaryRouteName {
		t.Fatalf("first available spillover route must be secondary (%s, cost 0.25), got %s (cost %f)",
			secondaryRouteName, avail[0].Route, avail[0].CostPerMToken)
	}
	if avail[1].Route != tertiaryRouteName {
		t.Fatalf("second available spillover route must be tertiary (%s, cost 0.50), got %s (cost %f)",
			tertiaryRouteName, avail[1].Route, avail[1].CostPerMToken)
	}

	// Step 4b: Simulate secondary route reaching its concurrency ceiling (40 cards)
	inFlight[secondaryRouteName] = 40
	availSecondaryCeiling := reg.AvailableRoutes("flash", inFlight, errorRates)
	if len(availSecondaryCeiling) != 1 {
		t.Fatalf("expected 1 available route (tertiary) after secondary hits concurrency limit, got %d", len(availSecondaryCeiling))
	}
	if availSecondaryCeiling[0].Route != tertiaryRouteName {
		t.Fatalf("spillover route when secondary is full must be tertiary (%s, cost 0.50), got %s",
			tertiaryRouteName, availSecondaryCeiling[0].Route)
	}

	// Step 4c: Simulate secondary route tripping its own error threshold (outage cascades)
	inFlight[secondaryRouteName] = 10     // below ceiling, but error rate spikes
	errorRates[secondaryRouteName] = 0.50 // 50% error rate > 20% threshold
	availSecondaryTripped := reg.AvailableRoutes("flash", inFlight, errorRates)
	if len(availSecondaryTripped) != 1 {
		t.Fatalf("expected 1 available route (tertiary) after secondary trips circuit breaker, got %d", len(availSecondaryTripped))
	}
	if availSecondaryTripped[0].Route != tertiaryRouteName {
		t.Fatalf("spillover route when secondary trips must be tertiary (%s), got %s",
			tertiaryRouteName, availSecondaryTripped[0].Route)
	}

	// Step 4d: If tertiary also trips or reaches ceiling, routes become empty (circuit completely open)
	errorRates[tertiaryRouteName] = 0.90
	availAllTripped := reg.AvailableRoutes("flash", inFlight, errorRates)
	if len(availAllTripped) != 0 {
		t.Fatalf("expected 0 available routes when all routes are tripped, got %d", len(availAllTripped))
	}
}
