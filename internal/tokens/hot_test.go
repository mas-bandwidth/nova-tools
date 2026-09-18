package tokens

import (
	"strings"
	"testing"
)

// The `hot` verb's one pass: rank a session's repeated reads and its largest
// step outputs, and say on one HOT NOTE line what the ranking cannot see.

func TestHotSessionReadsAndOutputs(t *testing.T) {
	reads := []ReadRecord{
		{Path: "a", Count: 3},
		{Path: "b", Count: 10},
		{Path: "c", Count: 5},
		{Path: "d", Count: 9},
	}
	outputs := []StepOutput{
		{Tool: "bash", Action: "ls", OutputBytes: 100},
		{Tool: "read", OutputBytes: 900},
		{Tool: "grep", Action: "x", OutputBytes: 400},
		{Tool: "write", OutputBytes: 700},
	}

	got := AnalyzeHotSession(reads, outputs, 2)

	// Top two repeated reads, largest first: b then d, with their counts.
	if len(got.TopReads) != 2 {
		t.Fatalf("%d top reads, want two: %v", len(got.TopReads), got.TopReads)
	}
	if got.TopReads[0].Path != "b" || got.TopReads[0].Count != 10 {
		t.Errorf("first read is %v, want b at 10", got.TopReads[0])
	}
	if got.TopReads[1].Path != "d" || got.TopReads[1].Count != 9 {
		t.Errorf("second read is %v, want d at 9", got.TopReads[1])
	}

	// Top two largest step outputs, largest first: read at 900, write at 700.
	if len(got.TopOutputs) != 2 {
		t.Fatalf("%d top outputs, want two: %v", len(got.TopOutputs), got.TopOutputs)
	}
	if got.TopOutputs[0].Tool != "read" || got.TopOutputs[0].OutputBytes != 900 {
		t.Errorf("first output is %v, want read at 900", got.TopOutputs[0])
	}
	if got.TopOutputs[1].Tool != "write" || got.TopOutputs[1].Action != "" || got.TopOutputs[1].OutputBytes != 700 {
		t.Errorf("second output is %v, want write at 700", got.TopOutputs[1])
	}

	report := FormatHotReport(got)
	// The line names a tool/action as one token, and ends on the fixed tail.
	if !strings.Contains(report, "read 900 bytes") || !strings.Contains(report, "write 700 bytes") {
		t.Errorf("the report does not name the ranked outputs:\n%s", report)
	}
	if strings.Contains(report, "bash/ls") {
		// The third output fell out of topN=2, so it must not be named.
		t.Errorf("the report names an output outside topN:\n%s", report)
	}
	const tail = "bytes are not tokens; truncated results; absent outputs; cache and reasoning are per message; a reclaimed swarm job has no steps"
	if !strings.HasSuffix(report, tail) {
		t.Errorf("the HOT NOTE line does not end with the fixed tail:\n%s", report)
	}

	// A topN over the length keeps everything, and a topN of zero names nothing
	// but still ends on the tail.
	all := FormatHotReport(AnalyzeHotSession(reads, outputs, 99))
	if !strings.Contains(all, "a 3 reads") || !strings.Contains(all, "bash/ls 100 bytes") {
		t.Errorf("topN past the end drops entries:\n%s", all)
	}
	none := FormatHotReport(AnalyzeHotSession(reads, outputs, 0))
	if strings.Contains(none, "reads;") || strings.Contains(none, "bytes;") {
		t.Errorf("topN zero still names something:\n%s", none)
	}
	if !strings.HasSuffix(none, tail) {
		t.Errorf("the empty report does not end with the fixed tail:\n%s", none)
	}
}
