package pulse

import (
	"os"
	"path/filepath"
	"testing"
)

// The 2026-09-16 measurement (issue #855, 1,068 jobs, all benches):
// input 62.1M, output 7.2M, cache_write 4.0M, cache_read 1,434.6M, reasoning 8.7M.
// Per job: reads 44.7k / 5.2k, fixes 66k / 7.3k, replays 86k / 8.9k,
// implementations 144k / 12.4k. Cache reads 23x input; reasoning 121% of output.
// Method: walk every usage.tsv, columns by header name, keep rc=0, join KIND
// from the sibling PROMPT.md (else RESULT.md line 1), fold by kind. Implied
// turns are cache_read / tokens_in. The two cheapest kinds by mean input are
// the two template cuts.

const usageHeader = "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

func TestGreenCardCostByKindFromUsage(t *testing.T) {
	root := t.TempDir()
	writeCostCard(t, root, "b1", "r1", "read", "0",
		"44700", "5200", "1000", "357600", "1000") // 8 implied turns
	writeCostCard(t, root, "b1", "f1", "fix", "0",
		"66000", "7300", "2000", "1320000", "8800") // 20 implied turns; reasoning > output
	writeCostCard(t, root, "b1", "x1", "read", "1",
		"99999", "9999", "0", "999999", "9999") // not green
	writeCostCard(t, root, "b2", "r2", "read", "0",
		"44700", "5200", "1000", "357600", "1000")
	// Columns by header name: a shuffled header still folds.
	job := filepath.Join(root, "b2", "jobs", "r3")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	shuffled := "rc\ttokens_in\ttokens_out\tcache_read\treasoning\tcache_write\tjob\n" +
		"0\t44700\t5200\t357600\t1000\t1000\tr3\n"
	if err := os.WriteFile(filepath.Join(job, "usage.tsv"), []byte(shuffled), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "PROMPT.md"), []byte("KIND: read\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := GreenCardCostByKind(root)
	if len(got) != 2 {
		t.Fatalf("kinds = %d, want 2 (the failed read is not green): %+v", len(got), got)
	}
	if got[0].Kind != "read" || got[1].Kind != "fix" {
		t.Fatalf("cheapest-first = %s then %s, want read then fix", got[0].Kind, got[1].Kind)
	}
	if got[0].Cards != 3 {
		t.Fatalf("read cards=%d, want 3", got[0].Cards)
	}
	if mean, ok := got[0].MeanInput(); !ok || mean != 44700 {
		t.Fatalf("read mean_in=%d ok=%v, want 44700 known", mean, ok)
	}
	if got[1].Cards != 1 {
		t.Fatalf("fix cards=%d, want 1", got[1].Cards)
	}
	if mean, ok := got[1].MeanInput(); !ok || mean != 66000 {
		t.Fatalf("fix mean_in=%d ok=%v, want 66000 known", mean, ok)
	}
	if g, ok := got[0].CachePerInput(); !ok || g < 7.9 || g > 8.1 {
		t.Fatalf("read cache/input = %v ok=%v, want ~8 (the turn multiplier)", g, ok)
	}
	if g, ok := got[1].CachePerInput(); !ok || g < 19.9 || g > 20.1 {
		t.Fatalf("fix cache/input = %v ok=%v, want ~20", g, ok)
	}
	if g, ok := got[1].ReasoningPerOutput(); !ok || g < 1.2 {
		t.Fatalf("fix reasoning/output = %v ok=%v, want > 1 (121%% on the 2026-09-16 day)", g, ok)
	}
}

// HOLD on #2127: absent, dash and malformed tokens_in are unknown, not a
// measured zero, and an unknown kind is not cheapest.
func TestGreenCardCostUnknownInputIsNotZeroAndNotCheapest(t *testing.T) {
	root := t.TempDir()
	writeCostCard(t, root, "b1", "fix1", "fix", "0",
		"66000", "7300", "2000", "1320000", "8800")
	writeCostCard(t, root, "b1", "dash", "tone", "0",
		"-", "100", "0", "100", "0")
	writeCostCard(t, root, "b1", "bad", "text", "0",
		"not-a-number", "100", "0", "100", "0")

	job := filepath.Join(root, "b1", "jobs", "absent")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	absent := "job\trc\ttokens_out\tcache_read\nabsent\t0\t100\t100\n"
	if err := os.WriteFile(filepath.Join(job, "usage.tsv"), []byte(absent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "PROMPT.md"), []byte("KIND: drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := GreenCardCostByKind(root)
	if len(got) == 0 {
		t.Fatal("no kinds folded")
	}
	if got[0].Kind != "fix" {
		t.Fatalf("cheapest kind = %s, want fix; unknown input must not rank as cheapest: %+v", got[0].Kind, got)
	}
	if mean, ok := got[0].MeanInput(); !ok || mean != 66000 {
		t.Fatalf("fix mean_in=%d ok=%v, want known 66000", mean, ok)
	}
	for _, k := range got[1:] {
		if mean, ok := k.MeanInput(); ok {
			t.Fatalf("kind %s reported a known mean_in=%d; dash/absent/malformed must stay unknown", k.Kind, mean)
		}
		if k.InputKnown != 0 {
			t.Fatalf("kind %s InputKnown=%d, want 0 (unknown is not a measured zero)", k.Kind, k.InputKnown)
		}
	}
}

func writeCostCard(t *testing.T, root, bench, label, kind, rc, in, out, cw, cr, rs string) {
	t.Helper()
	job := filepath.Join(root, bench, "jobs", label)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	row := "j\t1\t2026-09-16T00:00:00Z\t2026-09-16T00:10:00Z\t" + rc + "\topencode\tdeepseek-v4-flash\t" +
		in + "\t" + out + "\t" + cw + "\t" + cr + "\t" + rs + "\t0.01\n"
	if err := os.WriteFile(filepath.Join(job, "usage.tsv"), []byte(usageHeader+"\n"+row), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := "RESULT " + label + " sha=000000000000\nKIND: " + kind + "\nSTEP 1. do the work\n"
	if err := os.WriteFile(filepath.Join(job, "PROMPT.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
}
