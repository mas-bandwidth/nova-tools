package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			if err != nil {
				t.Fatal(err)
			}
			line := append([]byte("    test.go:1: SESSION_TRACE "), raw...)
			line = append(line, '\n')
			// Split inside JSON tokens as test2json does for long log lines.
			for len(line) > 0 {
				n := min(37, len(line))
				if err := enc.Encode(testEvent{Action: "output", Test: "TestShellRandomSequencesProduceSessionTrace", Output: string(line[:n])}); err != nil {
					t.Fatal(err)
				}
				line = line[n:]
			}
		}
	}
	if finish != "" {
		if err := enc.Encode(testEvent{Action: finish}); err != nil {
			t.Fatal(err)
		}
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
			traces, raw, err := readTraces(strings.NewReader(captureFixture(t, tc.finish, tc.duplicate)))
			if (err == nil) != tc.wantOK {
				t.Fatalf("error=%v wantOK=%v", err, tc.wantOK)
			}
			if tc.wantOK && (len(raw) != 16 || len(traces) != 16) {
				t.Fatal("fragmented output lost a session")
			}
		})
	}
}

func TestReceiptCorruptionAndCloneIndependence(t *testing.T) {
	t.Parallel()
	traces, _, err := readTraces(strings.NewReader(captureFixture(t, "pass", false)))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkReceipts(traces); err != nil {
		t.Fatal(err)
	}
	for i, mutate := range []func(*trace){
		func(tr *trace) { tr.Steps[0].Receipts = append(tr.Steps[0].Receipts, tr.Steps[0].Receipts[0]) },
		func(tr *trace) { tr.Steps[0].Receipts[0].Values["rev_after"] = "9" },
		func(tr *trace) { tr.Steps[0].Receipts[0].ID = "wrong-event" },
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			bad := clone(traces)
			mutate(&bad[0])
			if checkReceipts(bad) == nil {
				t.Fatal("corrupted receipt accepted")
			}
			if err := checkReceipts(traces); err != nil {
				t.Fatalf("negative control changed original trace: %v", err)
			}
		})
	}
}

func TestHelpAndArgumentRefusals(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-h"}, {"--help"}, {}, {"--unknown"}} {
		var stdout, stderr bytes.Buffer
		code := command(args, &stdout, &stderr)
		help := len(args) == 1 && (args[0] == "-h" || args[0] == "--help")
		if help {
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "usage: sessiontrace") {
				t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
			}
		} else if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "state:") || !strings.Contains(stderr.String(), "next:") {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestTruncatedCaptureNamesEvidenceAndRecovery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "capture.json")
	if err := os.WriteFile(path, []byte(`{"Action":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := readCapture(path)
	for _, want := range []string{path, "state:", "next:", "invalid test event", "unexpected EOF"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error=%v; want %q", err, want)
		}
	}
}

func TestDuplicateControlPreservesCountsAndOriginalTrace(t *testing.T) {
	t.Parallel()
	traces, _, err := readTraces(strings.NewReader(captureFixture(t, "pass", false)))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDuplicateControl(traces); err == nil || !strings.Contains(err.Error(), "two write receipts") {
		t.Fatalf("missing second write must refuse: %v", err)
	}
	tr := &traces[0]
	a := action{Kind: "ok", Command: "put fixture", Write: true}
	tr.Actions = append(tr.Actions, a)
	tr.Steps = append(tr.Steps, step{Action: a,
		Stdout: "TABLE RECEIPT event=2-0 epoch=0 before=1 after=2 outcome=changed\n",
		Receipts: []receipt{{ID: "2-0", Values: map[string]any{
			"rev_before": "1", "rev_after": "2", "actor": "trace",
		}}},
	})
	if err := checkReceipts(traces); err != nil {
		t.Fatal(err)
	}
	if err := checkDuplicateControl(traces); err != nil {
		t.Fatal(err)
	}
	if err := checkReceipts(traces); err != nil {
		t.Fatalf("control modified original trace: %v", err)
	}
}
