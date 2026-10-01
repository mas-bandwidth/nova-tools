package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the harness's own output when its catalog refused the model (opencode v1.18.20, verbatim
// shape, 2026-10-01): its printed ERROR line, then the UnknownError envelope
const startRefusal = `timestamp=2026-10-01T20:02:53.818Z level=ERROR run=5c22c68d message=failed ref=err_1ad647ee error="ProviderModelNotFoundError: Model not found: openrouter/x-ai/grok-4.7. Did you mean: x-ai/grok-4.20?"
Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_1ad647ee"}}`

// TestHarnessStartFailedIsNarrow: only a launch with no result, no tokens, inside the
// window, whose output is the catalog refusal or the bare envelope, is a failed start; a
// provider that answered (a status, an API error, a stream error), a token spent, a
// result written, or a run past the window never is.
func TestHarnessStartFailedIsNarrow(t *testing.T) {
	t.Parallel()
	envelope := `Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details."}}`
	cases := []struct {
		name      string
		tail      string
		elapsed   time.Duration
		tokens    int
		published bool
		cause     string // "" is not a failed start
	}{
		{"the catalog refusal", startRefusal, 3 * time.Second, 0, false, "unknown-model"},
		{"the bare envelope", envelope, 3 * time.Second, 0, false, "unexpected-server-error"},
		{"a provider 503", "Unexpected server error: the provider answered 503; ref=err_x\n" + envelope, 2 * time.Second, 0, false, ""},
		{"an upstream stream error", `level=ERROR message="stream error" error.error="AI_APICallError: Upstream request failed: Endpoint is unavailable."` + "\n" + envelope, 3 * time.Second, 0, false, ""},
		{"tokens spent", startRefusal, 3 * time.Second, 12, false, ""},
		{"a result written", startRefusal, 3 * time.Second, 0, true, ""},
		{"past the window", startRefusal, 11 * time.Second, 0, false, ""},
		{"a quiet zero-token exit", "nothing to say\n", 2 * time.Second, 0, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cause, failed := harnessStartFailed([]byte(c.tail), c.elapsed, c.tokens, c.published)
			assert.Equal(t, c.cause != "", failed)
			assert.Equal(t, c.cause, cause)
		})
	}
}

// TestTheStartScheduleIsTheOwners pins the waits: "maybe: 1-1-1-2-2-4-4-8-8 is better ;)",
// nine retries and 31 s in all.
func TestTheStartScheduleIsTheOwners(t *testing.T) {
	t.Parallel()
	var total time.Duration
	var secs []int
	for _, d := range harnessStartWaits {
		total += d
		secs = append(secs, int(d/time.Second))
	}
	assert.Equal(t, []int{1, 1, 1, 2, 2, 4, 4, 8, 8}, secs)
	assert.Equal(t, 31*time.Second, total)
}

// startRun runs one card through nativeRun with the start waits recorded, not slept.
func startRun(t *testing.T, label, card string) (nativeRunResult, string, []time.Duration, string) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	var waits []time.Duration
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label, card: []byte(card),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
		startSleep: func(d time.Duration) { waits = append(waits, d) },
	}, &errOut)
	require.Equal(t, 0, code, "%s", errOut.String())
	return res, errOut.String(), waits, filepath.Join(slot, "jobs", label)
}

// TestAFailedStartIsRetriedInPlace: a harness whose first three starts die at start is
// started again in the same job after 1, 1, 1 s, each retry one NATIVE RETRY line; the
// fourth start runs and publishes, and the run is OK with one result.
func TestAFailedStartIsRetriedInPlace(t *testing.T) {
	t.Parallel()
	res, log, waits, job := startRun(t, "start-retry", "a card\nFAKE-LAUNCHES\nFAKE-START-FAIL 3\n")
	assert.Equal(t, []time.Duration{time.Second, time.Second, time.Second}, waits, "the owner's schedule, from its start")
	assert.Equal(t, 3, strings.Count(log, "NATIVE RETRY "), "%s", log)
	assert.Contains(t, log, "NATIVE RETRY start=2 wait=1s cause=unknown-model")
	assert.Contains(t, log, "NATIVE RETRY start=4 wait=1s cause=unknown-model")
	assert.Equal(t, 0, res.rc, "the fourth start ran: %s", log)
	assert.NotContains(t, log, "PROVIDER-5XX", "a start that got past is no hand-back")
	launches, err := os.ReadFile(filepath.Join(job, "launches"))
	require.NoError(t, err)
	assert.Equal(t, 4, strings.Count(string(launches), "\n"), "four starts, one job: %s", launches)
	assert.Zero(t, res.starts)
}

// TestEveryStartFailedHandsBackNamingThem: a harness that never gets past its start is
// started ten times (the first and nine retries, waiting 31 s in all), then handed back as
// a provider failure whose cause is the harness catalog's refusal and names the starts.
func TestEveryStartFailedHandsBackNamingThem(t *testing.T) {
	t.Parallel()
	res, log, waits, job := startRun(t, "start-spent", "a card\nFAKE-LAUNCHES\nFAKE-START-FAIL 99\n")
	assert.Equal(t, harnessStartWaits, waits, "every wait of the schedule, once")
	assert.Equal(t, 9, strings.Count(log, "NATIVE RETRY "))
	assert.Equal(t, 10, res.starts)
	launches, err := os.ReadFile(filepath.Join(job, "launches"))
	require.NoError(t, err)
	assert.Equal(t, 10, strings.Count(string(launches), "\n"))
	assert.Contains(t, log, "NATIVE PROVIDER-5XX ")
	assert.Contains(t, log, "reason=provider: class=unknown-model status=- msg=model not found in the harness catalog: openrouter/x-ai/grok-4.7 (harness starts tried: 10)")
}

// TestALaunchReadsTheMachinesOneCatalog: with a catalog in place under the root, the
// launch is handed a copy of it in its own data home and the harness's own fetch is off,
// so the model is found with the same catalog whether or not the network answers; with
// none, the launch says so once and the harness fetches its own.
func TestALaunchReadsTheMachinesOneCatalog(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	catalog := `{"fake":{"id":"fake","models":{"fake-model":{"id":"fake-model","limit":{"context":200000,"output":32000}}}}}`
	require.NoError(t, os.MkdirAll(filepath.Join(root, "catalog"), 0o755))
	require.NoError(t, os.WriteFile(catalogFile(root), []byte(catalog), 0o644))
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "seeded", card: []byte("FAKE-CATALOG\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "%s", errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "seeded", "RESULT.md"))
	require.NoError(t, err)
	got := string(raw)
	assert.Contains(t, got, "/slot-1/data/.cache/opencode/models.json\n", "a copy in the launch's own data home")
	assert.Contains(t, got, "fetch_disabled=1", "the harness's own fetch is off")
	assert.Contains(t, got, "print_logs=1", "the harness prints its ERROR lines (the providers table's --print-logs)")
	assert.Contains(t, got, fmt.Sprintf("sha256=%x", sha256.Sum256([]byte(catalog))), "the launch reads the machine's catalog byte for byte")
	assert.NotContains(t, errOut.String(), "NATIVE NOTE catalog")

	_, bare := aSlot(t)
	var none bytes.Buffer
	_, code = nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "bare", card: []byte("FAKE-CATALOG\n"),
		slotDir: bare, root: filepath.Dir(bare), deadline: 30 * time.Second, noWall: true,
	}, &none)
	require.Equal(t, 0, code, "%s", none.String())
	assert.Equal(t, 1, strings.Count(none.String(), "NATIVE NOTE catalog: no catalog at "), "%s", none.String())
}
