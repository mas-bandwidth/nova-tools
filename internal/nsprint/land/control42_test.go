package land_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestControl42 is nova-sprint control 42. A GATE-RED batch whose members
// each pass alone on a green base retries once and lands. One flaky hit files
// one issue under flaky:<repo>:<pkg>.<test>; a second hit of that key files
// nothing. A member read UNKNOWN twice and then MERGEABLE is not dropped.
func TestControl42(t *testing.T) {
	if !merge.GateRedException(true, []bool{true, true}) {
		t.Fatal("base green and every member green alone must be class gate-red")
	}
	if merge.GateRedException(false, []bool{true, true}) {
		t.Fatal("a red base is not class gate-red")
	}
	if merge.GateRedException(true, []bool{true, false}) {
		t.Fatal("a member red alone is not class gate-red")
	}
	if merge.GateRedException(true, nil) {
		t.Fatal("no members is not class gate-red")
	}

	ctx := context.Background()
	start := time.Date(2026, 9, 23, 11, 7, 0, 0, time.UTC)
	clock := &fakeClock{at: start}
	store := land.NewMemory()
	filer := &scriptFiler{}
	gate := &scriptGate{verdicts: []land.Verdict{
		{Step: "test", Package: "pkg", Test: "T"},
		{OK: true},
		{Step: "test", Package: "pkg", Test: "T"},
		{OK: true},
		{Step: "test", Package: "pkg", Test: "Other"},
	}}
	bisect := &scriptBisect{reports: []aloneReport{
		{base: true, members: []bool{true, true}},
		{base: true, members: []bool{true}},
		{base: true, members: []bool{true, false}},
	}}
	forge := &scriptForge{seq: map[int][]string{
		11: {"MERGEABLE"},
		12: {"UNKNOWN", "UNKNOWN", "MERGEABLE"},
		13: {"UNKNOWN", "UNKNOWN", "UNKNOWN"},
		21: {"MERGEABLE"},
		31: {"MERGEABLE"},
		32: {"MERGEABLE"},
	}}
	lander := &recordLand{}
	ln := land.Lane{
		Gate: gate, Bisect: bisect, Forge: forge, Land: lander,
		Store: store, Filer: filer, Clock: clock,
	}

	// First pass: #12 is UNKNOWN twice then MERGEABLE, so it stays. #13 is
	// UNKNOWN three times and drops. The kept batch is red on T, both members
	// green alone, the retry is green, and one issue is filed.
	first, err := ln.Run(ctx, land.Batch{
		Repo: "nova-tools", Name: "tools-20260923T110729Z",
		Members: []land.Member{
			{Number: 11, Head: strings.Repeat("a", 40)},
			{Number: 12, Head: strings.Repeat("b", 40)},
			{Number: 13, Head: strings.Repeat("c", 40)},
		},
	})
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !first.Landed || first.Retries != 1 || first.Runs != 2 || first.Class != merge.ExceptionGateRed {
		t.Fatalf("first pass landed=%v retries=%d runs=%d class=%q", first.Landed, first.Retries, first.Runs, first.Class)
	}
	if got, want := ints(first.LandedNumbers), "11,12"; got != want {
		t.Fatalf("landed numbers %s, want %s", got, want)
	}
	if len(lander.batches) != 1 || !lander.oks[0] {
		t.Fatalf("lander batches=%d oks=%v, want one green land", len(lander.batches), lander.oks)
	}
	if ints(numbers(lander.batches[0])) != "11,12" {
		t.Fatalf("lander members %s, want 11,12", ints(numbers(lander.batches[0])))
	}
	kept11 := findMember(t, first.Kept, 11)
	if kept11.State != "MERGEABLE" || kept11.Polls != 1 || kept11.Reason != "" {
		t.Fatalf("#11 %+v, want MERGEABLE on the first poll", kept11)
	}
	kept12 := findMember(t, first.Kept, 12)
	if kept12.State != "MERGEABLE" || kept12.Polls != 3 || kept12.Reason != "" {
		t.Fatalf("#12 %+v, want MERGEABLE on the third poll and not dropped", kept12)
	}
	drop13 := findMember(t, first.Dropped, 13)
	if drop13.Reason != land.DropMergeableUnknown || drop13.Polls != land.PollLimit || drop13.State != "UNKNOWN" {
		t.Fatalf("#13 %+v, want drop %s after %d UNKNOWN polls", drop13, land.DropMergeableUnknown, land.PollLimit)
	}
	if findOptional(first.LandedNumbers, 13) || memberKept(first.Kept, 13) {
		t.Fatal("#13 was dropped and must not be kept or landed")
	}
	if len(clock.slept) != 4 {
		t.Fatalf("poll gaps %d, want 4", len(clock.slept))
	}
	for i, d := range clock.slept {
		if d != land.PollGap {
			t.Fatalf("poll gap %d = %s, want %s", i, d, land.PollGap)
		}
	}
	wantKey := land.FlakyKey("nova-tools", "pkg", "T")
	if wantKey != "flaky:nova-tools:pkg.T" || first.FlakyKey != wantKey || !first.Filed {
		t.Fatalf("key %q filed %v, want %q filed once", first.FlakyKey, first.Filed, wantKey)
	}
	if first.Record.LanesHit != 1 || first.Record.Issue != 4001 {
		t.Fatalf("record %+v, want lanes_hit 1 issue 4001", first.Record)
	}
	if len(filer.calls) != 1 {
		t.Fatalf("issues filed %d, want 1", len(filer.calls))
	}
	if filer.calls[0].repo != "nova-tools" || !strings.Contains(filer.calls[0].title, "flaky:") || !strings.Contains(filer.calls[0].body, "dedup="+wantKey) {
		t.Fatalf("issue repo=%s title=%q body=%q", filer.calls[0].repo, filer.calls[0].title, filer.calls[0].body)
	}
	seen := first.Record.FirstSeen

	// Second hit of the same key: the lane retries and lands again, and the
	// store adds no issue.
	clock.at = clock.at.Add(time.Minute)
	second, err := ln.Run(ctx, land.Batch{
		Repo: "nova-tools", Name: "tools-second",
		Members: []land.Member{{Number: 21, Head: strings.Repeat("d", 40)}},
	})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if !second.Landed || second.Retries != 1 || second.Filed {
		t.Fatalf("second pass landed=%v retries=%d filed=%v", second.Landed, second.Retries, second.Filed)
	}
	if second.Record.LanesHit != 2 || second.Record.Issue != 4001 || second.Record.FirstSeen != seen {
		t.Fatalf("second record %+v, want hit 2 issue 4001 first_seen %s", second.Record, seen)
	}
	if second.Record.LastAt == seen {
		t.Fatal("second hit did not move last_at")
	}
	if len(filer.calls) != 1 {
		t.Fatalf("second hit filed %d issues, want 1", len(filer.calls))
	}
	stored, ok, err := store.Get(ctx, wantKey)
	if err != nil || !ok || stored.Issue != 4001 || stored.LanesHit != 2 {
		t.Fatalf("stored %+v ok=%v err=%v", stored, ok, err)
	}

	// A member red alone is not the class: no retry, no land, no new issue.
	third, err := ln.Run(ctx, land.Batch{
		Repo: "nova-tools", Name: "tools-alone-red",
		Members: []land.Member{
			{Number: 31, Head: strings.Repeat("e", 40)},
			{Number: 32, Head: strings.Repeat("f", 40)},
		},
	})
	if err != nil {
		t.Fatalf("alone-red pass: %v", err)
	}
	if third.Landed || third.Retries != 0 || third.Runs != 1 || third.Class != "" || third.Filed {
		t.Fatalf("alone-red %+v, want one run, no retry, no land, no file", third)
	}
	if _, ok, err := store.Get(ctx, land.FlakyKey("nova-tools", "pkg", "Other")); err != nil || ok {
		t.Fatalf("alone-red recorded a flaky key ok=%v err=%v", ok, err)
	}
	if len(filer.calls) != 1 || len(lander.batches) != 2 {
		t.Fatalf("issues=%d lands=%d, want 1 and 2", len(filer.calls), len(lander.batches))
	}
	if got := gate.attempts; len(got) != 5 || got[0] != 0 || got[1] != 1 || got[2] != 0 || got[3] != 1 || got[4] != 0 {
		t.Fatalf("gate attempts %v, want [0 1 0 1 0]", got)
	}
	if len(clock.slept) != 4 {
		t.Fatalf("later passes added poll gaps, now %d", len(clock.slept))
	}
}

type fakeClock struct {
	at    time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.at }

func (c *fakeClock) Sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.at = c.at.Add(d)
}

type scriptGate struct {
	verdicts []land.Verdict
	attempts []int
	i        int
}

func (g *scriptGate) Run(_ context.Context, batch land.Batch, attempt int) (land.Verdict, error) {
	if g.i >= len(g.verdicts) {
		return land.Verdict{}, fmt.Errorf("unexpected gate run %d", g.i)
	}
	g.attempts = append(g.attempts, attempt)
	v := g.verdicts[g.i]
	g.i++
	if len(batch.Members) == 0 {
		return land.Verdict{}, fmt.Errorf("gate of an empty batch")
	}
	return v, nil
}

type aloneReport struct {
	base    bool
	members []bool
}

type scriptBisect struct {
	reports []aloneReport
	i       int
}

func (b *scriptBisect) Alone(_ context.Context, batch land.Batch, v land.Verdict) (bool, []bool, error) {
	if b.i >= len(b.reports) {
		return false, nil, fmt.Errorf("unexpected bisect")
	}
	r := b.reports[b.i]
	b.i++
	if len(r.members) != len(batch.Members) {
		return false, nil, fmt.Errorf("bisect prepared for %d members, batch has %d", len(r.members), len(batch.Members))
	}
	if v.Test == "" {
		return false, nil, fmt.Errorf("bisect of a verdict with no test")
	}
	return r.base, r.members, nil
}

type scriptForge struct {
	seq map[int][]string
	n   map[int]int
}

func (f *scriptForge) Mergeable(_ context.Context, repo string, number int) (string, error) {
	if repo != "nova-tools" {
		return "", fmt.Errorf("repo %s", repo)
	}
	seq, ok := f.seq[number]
	if !ok {
		return "", fmt.Errorf("no mergeable sequence for #%d", number)
	}
	if f.n == nil {
		f.n = map[int]int{}
	}
	i := f.n[number]
	if i >= len(seq) {
		return "", fmt.Errorf("extra mergeable read for #%d", number)
	}
	f.n[number] = i + 1
	return seq[i], nil
}

type filed struct {
	repo, title, body string
}

type scriptFiler struct {
	calls []filed
	next  int
}

func (f *scriptFiler) File(_ context.Context, repo, title, body string) (int, error) {
	f.next++
	f.calls = append(f.calls, filed{repo: repo, title: title, body: body})
	return 4000 + f.next, nil
}

type recordLand struct {
	batches []land.Batch
	oks     []bool
}

func (r *recordLand) Land(_ context.Context, batch land.Batch, v land.Verdict) error {
	r.batches = append(r.batches, batch)
	r.oks = append(r.oks, v.OK)
	return nil
}

func findMember(t *testing.T, rows []land.MemberResult, number int) land.MemberResult {
	t.Helper()
	for _, row := range rows {
		if row.Number == number {
			return row
		}
	}
	t.Fatalf("member #%d not in %+v", number, rows)
	return land.MemberResult{}
}

func findOptional(numbers []int, number int) bool {
	for _, n := range numbers {
		if n == number {
			return true
		}
	}
	return false
}

func memberKept(rows []land.MemberResult, number int) bool {
	for _, row := range rows {
		if row.Number == number {
			return true
		}
	}
	return false
}

func numbers(batch land.Batch) []int {
	out := make([]int, len(batch.Members))
	for i, m := range batch.Members {
		out[i] = m.Number
	}
	return out
}

func ints(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ",")
}
