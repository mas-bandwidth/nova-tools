package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureFixture(t *testing.T, finish string, duplicate bool) string {
	t.Helper()
	var output bytes.Buffer
	enc := json.NewEncoder(&output)
	for seed := int64(1); seed <= 8; seed++ {
		for _, keep := range []bool{false, true} {
			a := action{Kind: "ok", Command: "create fixture", Write: true}
			tr := trace{Seed: seed, Keep: keep, Actions: []action{a}, Steps: []step{{Action: a,
				Stdout: "TABLE RECEIPT event=1-0 epoch=0 before=0 after=1 outcome=changed\n",
				Receipts: []receipt{{ID: "1-0", Values: map[string]any{
					"rev_before": "0", "rev_after": "1", "actor": "trace",
				}}},
			}}, EOF: true}
			if duplicate {
				tr.Seed, tr.Keep = 1, false
			}
			raw, err := json.Marshal(tr)
			require.NoError(t, err)
			line := append([]byte("    test.go:1: SESSION_TRACE "), raw...)
			line = append(line, '\n')
			// Split inside JSON tokens as test2json does for long log lines.
			for len(line) > 0 {
				n := min(37, len(line))
				require.NoError(t, enc.Encode(testEvent{Action: "output", Test: "TestShellRandomSequencesProduceSessionTrace", Output: string(line[:n])}))
				line = line[n:]
			}
		}
	}
	if finish != "" {
		require.NoError(t, enc.Encode(testEvent{Action: finish}))
	}
	return output.String()
}

func TestCaptureRequiresCompletePassingSessions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, finish string
		duplicate    bool
		wantOK       bool
	}{
		{"fragmented", "pass", false, true},
		{"missing completion", "", false, false},
		{"failed", "fail", false, false},
		{"skipped", "skip", false, false},
		{"duplicate session", "pass", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			traces, raw, err := readTraces(strings.NewReader(captureFixture(t, tc.finish, tc.duplicate)))
			assert.Equal(t, tc.wantOK, err == nil, "readTraces error=%v", err)
			if tc.wantOK {
				assert.Len(t, raw, 16, "fragmented output lost a session")
				assert.Len(t, traces, 16, "fragmented output lost a session")
			}
		})
	}
}

func TestReceiptCorruptionAndCloneIndependence(t *testing.T) {
	t.Parallel()
	traces, _, err := readTraces(strings.NewReader(captureFixture(t, "pass", false)))
	require.NoError(t, err)
	require.NoError(t, checkReceipts(traces))
	for i, mutate := range []func(*trace){
		func(tr *trace) { tr.Steps[0].Receipts = append(tr.Steps[0].Receipts, tr.Steps[0].Receipts[0]) },
		func(tr *trace) { tr.Steps[0].Receipts[0].Values["rev_after"] = "9" },
		func(tr *trace) { tr.Steps[0].Receipts[0].ID = "wrong-event" },
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			bad := clone(traces)
			mutate(&bad[0])
			assert.Error(t, checkReceipts(bad), "a corrupted receipt was accepted")
			require.NoError(t, checkReceipts(traces), "the negative control changed the original trace")
		})
	}
}

func TestHelpAndArgumentRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		help bool
	}{
		{[]string{"-h"}, true},
		{[]string{"--help"}, true},
		{nil, false},
		{[]string{"--unknown"}, false},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := command(tc.args, &stdout, &stderr)
			if tc.help {
				require.Equal(t, 0, code, "stdout=%q stderr=%q", stdout.String(), stderr.String())
				assert.Empty(t, stderr.String())
				assert.Contains(t, stdout.String(), "usage: sessiontrace")
				return
			}
			require.Equal(t, 2, code, "stdout=%q stderr=%q", stdout.String(), stderr.String())
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), "state:")
			assert.Contains(t, stderr.String(), "next:")
		})
	}
}

func TestTruncatedCaptureNamesEvidenceAndRecovery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "capture.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"Action":`), 0o600))
	_, _, err := readCapture(path)
	require.Error(t, err)
	for _, want := range []string{path, "state:", "next:", "invalid test event", "unexpected EOF"} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestDuplicateControlPreservesCountsAndOriginalTrace(t *testing.T) {
	t.Parallel()
	traces, _, err := readTraces(strings.NewReader(captureFixture(t, "pass", false)))
	require.NoError(t, err)
	err = checkDuplicateControl(traces)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "two write receipts")
	tr := &traces[0]
	a := action{Kind: "ok", Command: "put fixture", Write: true}
	tr.Actions = append(tr.Actions, a)
	tr.Steps = append(tr.Steps, step{Action: a,
		Stdout: "TABLE RECEIPT event=2-0 epoch=0 before=1 after=2 outcome=changed\n",
		Receipts: []receipt{{ID: "2-0", Values: map[string]any{
			"rev_before": "1", "rev_after": "2", "actor": "trace",
		}}},
	})
	require.NoError(t, checkReceipts(traces))
	require.NoError(t, checkDuplicateControl(traces))
	require.NoError(t, checkReceipts(traces), "the control modified the original trace")
}
