package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// Issue #2011: Provider Error Auto-Requeue in pulse harvest/fill.

// TestRequeueMovesCardBackToReadyAndIncrementsAttemptCounter proves that:
// 1. Requeue moves the card from launched back into ready.
// 2. The launched marker is removed (freeing the lane).
// 3. The .provider-retry marker is created with attempts=1 and increments on successive requeues.
func TestRequeueMovesCardBackToReadyAndIncrementsAttemptCounter(t *testing.T) {
	dir := t.TempDir()
	readyDir := filepath.Join(dir, "ready")
	launchedDir := filepath.Join(dir, "launched")

	if err := os.MkdirAll(readyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cardName := "card-001.md"
	cardPath := filepath.Join(launchedDir, cardName)
	if err := os.WriteFile(cardPath, []byte("RESULT: CARD-1\nLANE: pulse\nMODEL: deepseek-direct\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	launchedMarker := filepath.Join(launchedDir, cardName+".launched")
	if err := os.WriteFile(launchedMarker, []byte("lane=pulse\nbench=bench-a\nlabel=card-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1st requeue
	requeued, nextAttempt, err := RequeueProviderCard(readyDir, launchedDir, cardName, 3)
	if err != nil {
		t.Fatalf("RequeueProviderCard failed: %v", err)
	}
	if !requeued {
		t.Fatalf("expected requeued=true, got false")
	}
	if nextAttempt != 1 {
		t.Fatalf("expected nextAttempt=1, got %d", nextAttempt)
	}

	// Verify card moved to readyDir
	if _, err := os.Stat(filepath.Join(readyDir, cardName)); err != nil {
		t.Fatalf("card not found in readyDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchedDir, cardName)); !os.IsNotExist(err) {
		t.Fatalf("card still in launchedDir after requeue")
	}
	// Verify launched marker was cleaned up
	if _, err := os.Stat(launchedMarker); !os.IsNotExist(err) {
		t.Fatalf("launched marker was not removed from launchedDir")
	}

	// Verify retry marker in readyDir
	retryMarker := filepath.Join(readyDir, cardName+".provider-retry")
	raw, err := os.ReadFile(retryMarker)
	if err != nil {
		t.Fatalf("missing retry marker: %v", err)
	}
	markerStr := string(raw)
	if !strings.Contains(markerStr, "attempts=1") {
		t.Fatalf("retry marker wants attempts=1, got: %s", markerStr)
	}
	if !strings.Contains(markerStr, "failed_routes=deepseek-direct") {
		t.Fatalf("retry marker wants failed_routes=deepseek-direct, got: %s", markerStr)
	}

	// Simulate re-launching: move card back to launchedDir
	if err := os.Rename(filepath.Join(readyDir, cardName), filepath.Join(launchedDir, cardName)); err != nil {
		t.Fatal(err)
	}

	// 2nd requeue
	requeued, nextAttempt, err = RequeueProviderCard(readyDir, launchedDir, cardName, 3)
	if err != nil {
		t.Fatalf("2nd RequeueProviderCard failed: %v", err)
	}
	if !requeued {
		t.Fatalf("expected 2nd requeued=true, got false")
	}
	if nextAttempt != 2 {
		t.Fatalf("expected nextAttempt=2, got %d", nextAttempt)
	}

	raw2, err := os.ReadFile(retryMarker)
	if err != nil {
		t.Fatalf("missing retry marker: %v", err)
	}
	if !strings.Contains(string(raw2), "attempts=2") {
		t.Fatalf("retry marker wants attempts=2, got: %s", string(raw2))
	}
}

// TestRequeueMaximumRetriesBoundRespected proves that:
// 1. A card is requeued up to maxRetries (default 3).
// 2. On the 4th provider error, retries are exhausted.
// 3. The card stays in launched with a .provider-failed marker and requeued=false.
func TestRequeueMaximumRetriesBoundRespected(t *testing.T) {
	dir := t.TempDir()
	readyDir := filepath.Join(dir, "ready")
	launchedDir := filepath.Join(dir, "launched")

	if err := os.MkdirAll(readyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cardName := "card-002.md"
	cardPath := filepath.Join(launchedDir, cardName)
	if err := os.WriteFile(cardPath, []byte("RESULT: CARD-2\nMODEL: deepseek-direct\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	maxRetries := 3

	// Attempts 1, 2, 3 should all succeed
	for attempt := 1; attempt <= maxRetries; attempt++ {
		requeued, nextAttempt, err := RequeueProviderCard(readyDir, launchedDir, cardName, maxRetries)
		if err != nil {
			t.Fatalf("attempt %d requeue failed: %v", attempt, err)
		}
		if !requeued {
			t.Fatalf("attempt %d: expected requeued=true", attempt)
		}
		if nextAttempt != attempt {
			t.Fatalf("attempt %d: expected nextAttempt=%d, got %d", attempt, attempt, nextAttempt)
		}

		// Move card back to launched to simulate the subsequent attempt
		if err := os.Rename(filepath.Join(readyDir, cardName), filepath.Join(launchedDir, cardName)); err != nil {
			t.Fatal(err)
		}
	}

	// Attempt 4: retries exhausted
	requeued, nextAttempt, err := RequeueProviderCard(readyDir, launchedDir, cardName, maxRetries)
	if err != nil {
		t.Fatalf("exhaustion check returned error: %v", err)
	}
	if requeued {
		t.Fatalf("expected requeued=false after %d retries, got true", maxRetries)
	}
	if nextAttempt != 4 {
		t.Fatalf("expected nextAttempt=4, got %d", nextAttempt)
	}

	// Verify card stayed in launchedDir
	if _, err := os.Stat(filepath.Join(launchedDir, cardName)); err != nil {
		t.Fatalf("card should stay in launchedDir when retries exhausted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(readyDir, cardName)); !os.IsNotExist(err) {
		t.Fatalf("card should not be in readyDir when retries exhausted")
	}

	// Verify .provider-failed marker exists in launchedDir
	failedMarker := filepath.Join(launchedDir, cardName+".provider-failed")
	raw, err := os.ReadFile(failedMarker)
	if err != nil {
		t.Fatalf("missing .provider-failed marker in launchedDir: %v", err)
	}
	failedStr := string(raw)
	if !strings.Contains(failedStr, "retries-exhausted") {
		t.Fatalf("failed marker wants reason=retries-exhausted, got: %s", failedStr)
	}
	if !strings.Contains(failedStr, "attempts=3") {
		t.Fatalf("failed marker wants attempts=3, got: %s", failedStr)
	}
}

// TestRequeueFailedRoutesPreservedAndAlternateRouteSelected proves that:
// 1. Failed routes are recorded and preserved across requeues.
// 2. Successive route selection uses the preserved failed routes to pick an alternate route.
func TestRequeueFailedRoutesPreservedAndAlternateRouteSelected(t *testing.T) {
	dir := t.TempDir()
	readyDir := filepath.Join(dir, "ready")
	launchedDir := filepath.Join(dir, "launched")

	if err := os.MkdirAll(readyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create provider registry with 3 routes ordered by cost
	regTsv := "route\tprovider\tmodel\ttier\tconcurrency\tcost_per_mtoken\terror_threshold\tnotes\n" +
		"deepseek-direct\tdeepseek\tdeepseek-v4-flash\tflash\t10\t0.14\t0.20\tcheapest\n" +
		"opencode-deepseek\topencode\tdeepseek-v4-flash\tflash\t10\t0.20\t0.20\tmid\n" +
		"openrouter-deepseek\topenrouter\tdeepseek-v4-flash\tflash\t10\t0.30\t0.20\tfallback\n"
	reg, err := fleet.ParseProviderRegistry(strings.NewReader(regTsv))
	if err != nil {
		t.Fatalf("failed to parse registry: %v", err)
	}

	cardName := "card-003.md"
	cardPath := filepath.Join(launchedDir, cardName)
	if err := os.WriteFile(cardPath, []byte("RESULT: CARD-3\nMODEL: deepseek-direct\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1st failure on deepseek-direct
	requeued, nextAttempt, err := RequeueProviderCardWithRoute(readyDir, launchedDir, cardName, "deepseek-direct", 3)
	if err != nil || !requeued || nextAttempt != 1 {
		t.Fatalf("1st requeue failed: requeued=%v, nextAttempt=%d, err=%v", requeued, nextAttempt, err)
	}

	// Read retry marker and verify failed_routes contains deepseek-direct
	retryState, err := ReadProviderRetry(readyDir, cardName)
	if err != nil {
		t.Fatalf("ReadProviderRetry failed: %v", err)
	}
	if len(retryState.FailedRoutes) != 1 || retryState.FailedRoutes[0] != "deepseek-direct" {
		t.Fatalf("unexpected failed routes: %v", retryState.FailedRoutes)
	}

	// Next launch selects alternate route: deepseek-direct should be skipped, opencode-deepseek selected
	altRoute1, err := SelectAlternateRoute(reg, "flash", nil, nil, retryState.FailedRoutes)
	if err != nil {
		t.Fatalf("SelectAlternateRoute failed: %v", err)
	}
	if altRoute1.Route != "opencode-deepseek" {
		t.Fatalf("expected alternate route opencode-deepseek, got: %s", altRoute1.Route)
	}

	// Move card to launched with new route
	if err := os.Rename(filepath.Join(readyDir, cardName), filepath.Join(launchedDir, cardName)); err != nil {
		t.Fatal(err)
	}

	// 2nd failure on opencode-deepseek
	requeued, nextAttempt, err = RequeueProviderCardWithRoute(readyDir, launchedDir, cardName, "opencode-deepseek", 3)
	if err != nil || !requeued || nextAttempt != 2 {
		t.Fatalf("2nd requeue failed: requeued=%v, nextAttempt=%d, err=%v", requeued, nextAttempt, err)
	}

	// Read retry marker: BOTH failed routes must be preserved!
	retryState2, err := ReadProviderRetry(readyDir, cardName)
	if err != nil {
		t.Fatalf("ReadProviderRetry failed: %v", err)
	}
	if len(retryState2.FailedRoutes) != 2 {
		t.Fatalf("expected 2 failed routes, got: %v", retryState2.FailedRoutes)
	}
	if retryState2.FailedRoutes[0] != "deepseek-direct" || retryState2.FailedRoutes[1] != "opencode-deepseek" {
		t.Fatalf("expected [deepseek-direct opencode-deepseek], got: %v", retryState2.FailedRoutes)
	}

	// Next launch selects second alternate route: openrouter-deepseek
	altRoute2, err := SelectAlternateRoute(reg, "flash", nil, nil, retryState2.FailedRoutes)
	if err != nil {
		t.Fatalf("SelectAlternateRoute failed: %v", err)
	}
	if altRoute2.Route != "openrouter-deepseek" {
		t.Fatalf("expected openrouter-deepseek, got: %s", altRoute2.Route)
	}
}

// TestRequeueJobProviderErrorDetection verifies IsJobProviderError detects provider-side errors.
func TestRequeueJobProviderErrorDetection(t *testing.T) {
	jobDir := t.TempDir()

	// 1. Clean job: no provider error
	if IsJobProviderError(jobDir) {
		t.Fatalf("empty job dir should not be provider error")
	}

	// 2. usage.tsv with end=provider
	usagePath := filepath.Join(jobDir, "usage.tsv")
	usageContent := "started\tended\trc\tusd\ttokens\tend\n2026-09-20\t2026-09-20\t1\t0.00\t0\tprovider\n"
	if err := os.WriteFile(usagePath, []byte(usageContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsJobProviderError(jobDir) {
		t.Fatalf("job with usage.tsv end=provider must be detected as provider error")
	}

	// 3. native.log with NATIVE PROVIDER verdict
	jobDir2 := t.TempDir()
	nativeLog := filepath.Join(jobDir2, "native.log")
	if err := os.WriteFile(nativeLog, []byte("NATIVE PROVIDER label=test why=unexpected-server-error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsJobProviderError(jobDir2) {
		t.Fatalf("job with NATIVE PROVIDER in native.log must be detected as provider error")
	}

	// 4. Normal failure (NATIVE INCOMPLETE) is not a provider error
	jobDir3 := t.TempDir()
	nativeLog3 := filepath.Join(jobDir3, "native.log")
	if err := os.WriteFile(nativeLog3, []byte("NATIVE INCOMPLETE label=test why=rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsJobProviderError(jobDir3) {
		t.Fatalf("normal failure (NATIVE INCOMPLETE) must not be detected as provider error")
	}
}
