package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTSetPendingRowsNeverClaimPass(t *testing.T) {
	t.Parallel()
	rows := tsetPendingRows(true)
	if len(rows) != 21*2*2 {
		t.Fatalf("got %d rows, want all 21 at both sizes and routes", len(rows))
	}
	for _, row := range rows {
		if row.verdict() != "NOT MEASURED" {
			t.Fatalf("%s/%d/%s: %s", row.row, row.cards, row.scenario, row.verdict())
		}
	}
	var report bytes.Buffer
	if err := tsetReport(&report, "composed", rows); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(report.String(), "L1 composed profile;") {
		t.Fatalf("unexpected header prefix: %s", report.String())
	}
	if got := strings.Count(report.String(), "NOT MEASURED"); got != len(rows) {
		t.Fatalf("report has %d unmeasured rows, want %d", got, len(rows))
	}
}

func TestTSetVerdictRequiresMatchedStoreMeasurement(t *testing.T) {
	t.Parallel()
	row := tsetSizeResult{row: "L1-3", cards: 100_000, scenario: "local", limit: 25 * time.Millisecond, correct: true, complete: true,
		samples: []tsetSizeSample{{wall: 5 * time.Millisecond}}}
	if got := row.verdict(); got != "NOT MEASURED" {
		t.Fatalf("unmatched slowlog called %s", got)
	}
	row.samples[0].store = 25 * time.Millisecond
	if got := row.verdict(); got != "INSIDE" {
		t.Fatalf("inclusive limit called %s", got)
	}
	row.samples = append(row.samples, tsetSizeSample{store: 25*time.Millisecond + time.Microsecond, wall: 27 * time.Millisecond})
	if got := row.verdict(); got != "OVER" {
		t.Fatalf("one exceeded sample called %s", got)
	}
	row.correct = false
	if got := row.verdict(); got != "FAIL" {
		t.Fatalf("wrong result called %s", got)
	}
}

func TestTSetQuantileUsesObservedRank(t *testing.T) {
	t.Parallel()
	values := make([]time.Duration, 100)
	for i := range values {
		values[i] = time.Duration(100-i) * time.Microsecond
	}
	if got := tsetQuantile(values, .99); got != 99*time.Microsecond {
		t.Fatalf("p99 = %s", got)
	}
	if got := tsetQuantile(nil, .99); got != 0 {
		t.Fatalf("empty p99 = %s", got)
	}
}

func TestTSetFillEntriesCoverExactPhysicalCandidates(t *testing.T) {
	t.Parallel()
	for _, cards := range []int{100_000, 1_000_000} {
		for _, first := range []int{0, tsetSizeChunk, cards/2 - tsetSizeChunk, cards - tsetSizeChunk} {
			entries, err := tsetFillEntries(cards, first, tsetSizeChunk)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			seen := make(map[string]bool, tsetSizeChunk)
			for _, entry := range entries {
				if entry.Kind != "create" || entry.Table != "work" || len(entry.IDs) != len(entry.Scores) || len(entry.IDs) != len(entry.About) {
					t.Fatalf("%d: malformed aligned fill entry", cards)
				}
				for _, id := range entry.IDs {
					if seen[id] {
						t.Fatalf("%d: duplicate %q", cards, id)
					}
					seen[id] = true
					count++
				}
			}
			if count != tsetSizeChunk {
				t.Fatalf("%d: fill step has %d physical members", cards, count)
			}
		}
	}
	if _, err := tsetFillEntries(100_000, 99_000, 2_001); err == nil {
		t.Fatal("over-cap step accepted")
	}
}

func TestTSetFillGateUsesTotalAndGrowth(t *testing.T) {
	t.Parallel()
	row := tsetSizeResult{row: "L1-1", cards: 100_000, scenario: "local", limit: 2 * time.Second, correct: true, complete: true}
	for range 50 {
		row.samples = append(row.samples, tsetSizeSample{store: 30 * time.Millisecond, wall: 31 * time.Millisecond})
	}
	if got := row.verdict(); got != "INSIDE" {
		t.Fatalf("50 calls totaling 1.5s: %s", got)
	}
	row.samples[49].store = 46 * time.Millisecond
	if got := row.verdict(); got != "OVER" {
		t.Fatalf("last call grew over 1.5x: %s", got)
	}
	row.samples[49].store = 30 * time.Millisecond
	for i := range row.samples {
		row.samples[i].store = 41 * time.Millisecond
	}
	if got := row.verdict(); got != "OVER" {
		t.Fatalf("aggregate over 2s: %s", got)
	}
	row.samples = row.samples[:49]
	if got := row.verdict(); got != "NOT MEASURED" {
		t.Fatalf("missing one call: %s", got)
	}
}

func TestTSetCountersChargeWorkAndBytes(t *testing.T) {
	t.Parallel()
	var sample tsetSizeSample
	raw := []byte(`{"store_commands":21,"planned_commands":7,"planned_argv_bytes":1800,"fetched_bytes":600,"field":3,"cell":2,"record":1,"range_id":0,"log_id":1,"generated_log_bytes":300}`)
	if err := tsetDecodeCounters(raw, &sample); err != nil {
		t.Fatal(err)
	}
	if !sample.costOK || sample.commands != 21 || sample.probes != 2 || sample.fields != 3 || sample.fetched != 600 || sample.argvBytes != 1800 || sample.logBytes != 300 {
		t.Fatalf("incorrect work accounting: %+v", sample)
	}
	sample.costOK = false
	if err := tsetDecodeCounters([]byte(`{"store_commands":21}`), &sample); err == nil || sample.costOK {
		t.Fatal("incomplete counters accepted as evidence")
	}
}

func TestTSetConfigRequiresExplicitOwnedAddress(t *testing.T) {
	t.Parallel()
	valid := tsetSizeConfig{redisAddr: "127.0.0.1:6401", proxyAddr: "127.0.0.1:6402", space: "dev-size-unique:", owned: true}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []tsetSizeConfig{
		{redisAddr: valid.redisAddr, space: valid.space},
		{space: valid.space, owned: true},
		{redisAddr: valid.redisAddr, space: "sprint:", owned: true},
		{redisAddr: valid.redisAddr, space: valid.space, proxyAddr: valid.redisAddr, owned: true},
	} {
		if err := invalid.validate(); err == nil {
			t.Fatalf("unsafe config accepted: %+v", invalid)
		}
	}
}

func TestTSetCardEntriesKeepAlignedGuardsAcrossRows(t *testing.T) {
	t.Parallel()
	for _, cards := range []int{100_000, 1_000_000} {
		entries, err := tsetCardEntries(cards, "guard", 2_000, 4_000, "waiting", "", nil, "1")
		if err != nil {
			t.Fatal(err)
		}
		var count int
		for _, entry := range entries {
			if len(entry.IDs) == 0 || len(entry.IDs) > tsetSizeChunk || len(entry.IDs) != len(entry.Revs) || len(entry.About) != 0 {
				t.Fatalf("%d: bad guard alignment", cards)
			}
			for _, id := range entry.IDs {
				count++
				if id == "" {
					t.Fatal("empty ID")
				}
			}
		}
		if count != 4_000 {
			t.Fatalf("%d: guarded %d members", cards, count)
		}
	}
}

func TestTSetServerIdentityFields(t *testing.T) {
	t.Parallel()
	info := "# Server\r\nredis_version:8.10.2\r\nredis_mode:standalone\r\n"
	if tsetInfoField(info, "redis_version") != "8.10.2" || tsetInfoField(info, "redis_mode") != "standalone" {
		t.Fatalf("identity fields not read from INFO: %q", info)
	}
	if tsetInfoField(info, "absent") != "" {
		t.Fatal("missing identity field invented")
	}
}

func TestTSetByteWorkloadSplitsLineWithoutShrinkingAtomicStep(t *testing.T) {
	t.Parallel()
	entries := tsetWordEntries(1_024, "word")
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want four bounded log lines", len(entries))
	}
	var candidates int
	for _, entry := range entries {
		if len(entry.IDs) != 500 || len(entry.About) != 500 || len(entry.Each) != 500 {
			t.Fatalf("entry alignment: ids=%d about=%d each=%d", len(entry.IDs), len(entry.About), len(entry.Each))
		}
		for _, fields := range entry.Each {
			if len(fields["brief"]) != 1_024 {
				t.Fatal("brief width differs from gate")
			}
		}
		candidates += len(entry.IDs)
	}
	if candidates != 2_000 {
		t.Fatalf("atomic step has %d physical candidates", candidates)
	}
}

func TestTSetRatioBeatAndMemoryVerdicts(t *testing.T) {
	t.Parallel()
	betweenRows := tsetSizeResult{row: "L1-12", cards: 100_000, complete: true, correct: true, samples: []tsetSizeSample{
		{store: 10 * time.Millisecond, wall: 11 * time.Millisecond}, {store: 12 * time.Millisecond, wall: 13 * time.Millisecond},
	}}
	if got := betweenRows.verdict(); got != "INSIDE" {
		t.Fatalf("ratio boundary: %s", got)
	}
	betweenRows.samples[1].store += time.Microsecond
	if got := betweenRows.verdict(); got != "OVER" {
		t.Fatalf("ratio over bound: %s", got)
	}
	memory := tsetSizeResult{row: "L1-21", cards: 100_000, complete: true, correct: true, memorySamples: 100, memoryPerCard: 400}
	if got := memory.verdict(); got != "INSIDE" {
		t.Fatalf("memory boundary: %s", got)
	}
	memory.memoryPerCard = 400.1
	if got := memory.verdict(); got != "OVER" {
		t.Fatalf("memory over bound: %s", got)
	}
	beat := tsetSizeResult{row: "L1-13", cards: 100_000, complete: true, correct: true, beatRate: 125}
	beat.beatWalls = make([]time.Duration, 100)
	for i := range beat.beatWalls {
		beat.beatWalls[i] = time.Millisecond
	}
	beat.samples = make([]tsetSizeSample, 1_000)
	for i := range beat.samples {
		beat.samples[i] = tsetSizeSample{store: time.Microsecond, wall: time.Millisecond}
	}
	if got := beat.verdict(); got != "INSIDE" {
		t.Fatalf("beat boundary: %s", got)
	}
	beat.beatRate = 124.9
	if got := beat.verdict(); got != "NOT MEASURED" {
		t.Fatalf("insufficient load called %s", got)
	}
}

func TestTSetMemorySnapshotRejectsMissingAndBadMetrics(t *testing.T) {
	t.Parallel()
	info := "used_memory:100\r\nused_memory_dataset:40\r\nused_memory_peak:120\r\nused_memory_rss:130\r\n" +
		"used_memory_vm_functions:24\r\nused_memory_vm_eval:3\r\nused_memory_functions:7\r\n"
	snapshot, err := tsetMemorySnapshotFromInfo(info, "allocator_allocated_lua:29\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Used != 100 || snapshot.Dataset != 40 || snapshot.VMFunctions != 24 ||
		snapshot.VMEval != 3 || snapshot.LuaAllocated == nil || *snapshot.LuaAllocated != 29 {
		t.Fatalf("incorrect Redis memory accounting: %+v", snapshot)
	}
	if _, err := tsetMemorySnapshotFromInfo(strings.Replace(info, "used_memory_vm_functions:24\r\n", "", 1), ""); err == nil {
		t.Fatal("missing Functions VM bytes accepted as zero")
	}
	if _, err := tsetMemorySnapshotFromInfo(strings.Replace(info, "used_memory_dataset:40", "used_memory_dataset:bad", 1), ""); err == nil {
		t.Fatal("invalid dataset bytes accepted")
	}
}

func TestTSetMemoryClassesKeepLogsAndReceiptsSeparate(t *testing.T) {
	t.Parallel()
	space := "dev-size:memory:100000:"
	cases := map[string]string{
		space + "member:work:card-0000000":           "records",
		space + "table:work:cell:stream-000:waiting": "cells",
		space + "table:work:rows":                    "row_indexes",
		space + "table:work:definition":              "definitions",
		space + "sprint:log@0":                       "logs",
		space + "sprint:done@0":                      "receipts",
		space + "sprint:cl@0":                        "change_log",
		space + "sprint:epoch@0":                     "epoch",
		space + "size:claimed":                       "fixture_or_other",
	}
	for key, want := range cases {
		if got := tsetMemoryClass(space, key); got != want {
			t.Fatalf("%q class=%q want=%q", key, got, want)
		}
	}
}

func TestTSetMemoryGrowthAddsFunctionVMOnce(t *testing.T) {
	t.Parallel()
	before := tsetMemorySnapshot{Used: 100, Dataset: 40, VMFunctions: 24, VMEval: 3, FunctionLibrary: 7}
	after := tsetMemorySnapshot{Used: 200, Dataset: 110, VMFunctions: 34, VMEval: 3, FunctionLibrary: 8}
	delta, err := tsetMemoryGrowth(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Allocator != 100 || delta.Dataset != 70 || delta.Functions != 10 || delta.Whole != 110 {
		t.Fatalf("wrong growth accounting: %+v", delta)
	}
	after.VMFunctions = 23
	if _, err := tsetMemoryGrowth(before, after); err == nil {
		t.Fatal("decreasing Functions VM heap invented a positive delta")
	}
}

func TestTSetReportProfileHeader(t *testing.T) {
	t.Parallel()
	rows := tsetPendingRows(false)
	var reportL1, reportComposed bytes.Buffer
	if err := tsetReport(&reportL1, "l1_only", rows); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reportL1.String(), "L1 l1_only profile;") {
		t.Fatalf("want L1 l1_only profile; got: %s", reportL1.String())
	}
	if err := tsetReport(&reportComposed, "composed", rows); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reportComposed.String(), "L1 composed profile;") {
		t.Fatalf("want L1 composed profile; got: %s", reportComposed.String())
	}
}

func TestTSetL1_19VerdictRequiresTwoSamples(t *testing.T) {
	t.Parallel()
	row := tsetSizeResult{
		row:             "L1-19",
		cards:           100_000,
		scenario:        "local",
		limit:           10 * time.Millisecond,
		correct:         true,
		complete:        true,
		requireCounters: true,
		samples:         []tsetSizeSample{{store: 2 * time.Millisecond, wall: 3 * time.Millisecond, costOK: true}},
	}
	// With only 1 sample, verdict is NOT MEASURED (Stella's audit stella-22f1e487f378).
	if got := row.verdict(); got != "NOT MEASURED" {
		t.Fatalf("1 sample verdict=%s, want NOT MEASURED", got)
	}
	// With 2 samples totaling <= 10ms, verdict is INSIDE.
	row.samples = append(row.samples, tsetSizeSample{store: 2 * time.Millisecond, wall: 3 * time.Millisecond, costOK: true})
	if got := row.verdict(); got != "INSIDE" {
		t.Fatalf("2 samples sum 4ms verdict=%s, want INSIDE", got)
	}
	// With 2 samples totaling > 10ms, verdict is OVER.
	row.samples[1].store = 9 * time.Millisecond
	if got := row.verdict(); got != "OVER" {
		t.Fatalf("2 samples sum 11ms verdict=%s, want OVER", got)
	}
}
