// nova-tools#3016: the `classify` verb is the production caller of
// merge.RecordGroupVerdict, so a classified merge-group run writes one
// group_start line and one group_verdict line to merge.DefaultEvents (and a
// park line when the poison detector arms). The test drives the verb itself,
// never a hand call of RecordGroupVerdict.
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// swapClassifyEvents points the production event sink at a buffer for one
// test. The sink is a package global, so these tests never run in parallel.
func swapClassifyEvents(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	saved := merge.DefaultEvents
	merge.DefaultEvents = &merge.Events{Sink: &buf}
	t.Cleanup(func() { merge.DefaultEvents = saved })
	return &buf
}

// classifyEventLines parses the sink: one JSON object per line.
func classifyEventLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("event line %d is not one JSON object: %v\n%s", i, err, line)
		}
		out = append(out, m)
	}
	return out
}

func countEvents(lines []map[string]any, name string) int {
	n := 0
	for _, m := range lines {
		if m["event"] == name {
			n++
		}
	}
	return n
}

func findEvent(lines []map[string]any, name string) map[string]any {
	for _, m := range lines {
		if m["event"] == name {
			return m
		}
	}
	return nil
}

func TestClassifyEmitsGroupStartAndVerdict(t *testing.T) {
	cases := []struct {
		name        string
		answer      string
		failures    []merge.Failure
		changed     []string
		wantVerdict string
		wantTest    string
		wantPark    int
	}{
		{
			name:        "flaky re-runs, no park",
			answer:      `{"kind":{"type":"choice","choice":"flaky-under-load","probabilities":{"flaky-under-load":0.95},"confidence":0.95}}`,
			wantVerdict: merge.ClassFlaky,
		},
		{
			name:        "armed own-change parks",
			answer:      `{"kind":{"type":"choice","choice":"own-change","probabilities":{"own-change":0.94},"confidence":0.94}}`,
			failures:    []merge.Failure{{Test: "TestRefillCounts", Package: "internal/pulse", Count: 2}},
			changed:     []string{"internal/pulse"},
			wantVerdict: merge.ClassOwnChange,
			wantTest:    "TestRefillCounts",
			wantPark:    1,
		},
		{
			name:        "below the floor records no verdict",
			answer:      `{"kind":{"type":"choice","choice":"own-change","probabilities":{"own-change":0.31},"confidence":0.31}}`,
			failures:    []merge.Failure{{Test: "TestRefillCounts", Package: "internal/pulse", Count: 2}},
			changed:     []string{"internal/pulse"},
			wantVerdict: "",
			wantTest:    "TestRefillCounts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := classifyLab(t)
			if l.host.Failures == nil {
				l.host.Failures = map[int][]merge.Failure{}
			}
			if l.host.Changed == nil {
				l.host.Changed = map[int][]string{}
			}
			l.host.Failures[77] = tc.failures
			l.host.Changed[77] = tc.changed
			seen := map[string]any{}
			srv := classifyDecideServer(t, classifyDecideHandler(t, &seen, tc.answer))
			defer srv.Close()
			t.Setenv("CARD896_JEV_KEY", "sekret")
			buf := swapClassifyEvents(t)

			exit, out, errb := l.run("classify", "--lane", l.lane, "--run", "42", "--base-url", srv.URL, "--key-env", "CARD896_JEV_KEY")
			if exit != 0 {
				t.Fatalf("classify exit=%d stdout=%s stderr=%s", exit, out, errb)
			}
			lines := classifyEventLines(t, buf)
			if n := countEvents(lines, merge.EventGroupStart); n != 1 {
				t.Fatalf("want one group_start line, got %d:\n%s", n, buf.String())
			}
			if n := countEvents(lines, merge.EventGroupVerdict); n != 1 {
				t.Fatalf("want one group_verdict line, got %d:\n%s", n, buf.String())
			}
			if n := countEvents(lines, merge.EventPark); n != tc.wantPark {
				t.Fatalf("want %d park lines, got %d:\n%s", tc.wantPark, n, buf.String())
			}
			start := findEvent(lines, merge.EventGroupStart)
			if start["group"] != "main" || start["run"] != float64(42) {
				t.Fatalf("group_start names the wrong group or run: %v", start)
			}
			verdict := findEvent(lines, merge.EventGroupVerdict)
			if verdict["conclusion"] != "failure" || verdict["failing_test"] != tc.wantTest ||
				verdict["poison_verdict"] != tc.wantVerdict {
				t.Fatalf("group_verdict = %v, want conclusion=failure failing_test=%q poison_verdict=%q",
					verdict, tc.wantTest, tc.wantVerdict)
			}
			if lineIdx(lines, merge.EventGroupStart) > lineIdx(lines, merge.EventGroupVerdict) {
				t.Fatalf("group_start must precede group_verdict:\n%s", buf.String())
			}
		})
	}
}

func lineIdx(lines []map[string]any, name string) int {
	for i, m := range lines {
		if m["event"] == name {
			return i
		}
	}
	return -1
}
