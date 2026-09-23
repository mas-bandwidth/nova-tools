package cardstatus_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/pulse/cardstatus"
	"github.com/redis/go-redis/v9"
)

func TestCardStatusHashMatchesBashFixture(t *testing.T) {
	ctx := context.Background()

	// Set up a fixture: results/<label>/RESULT.md for three cards.
	fixture := t.TempDir()
	resultsDir := filepath.Join(fixture, "results")
	outDir := filepath.Join(fixture, "recut-harvest")

	cards := []struct {
		label string
		sha   string
	}{
		{"card-001", "abcd12345678"},
		{"card-002", "ef9012345678"},
		{"card-003", "5678abcd1234"},
	}
	for _, c := range cards {
		dir := filepath.Join(resultsDir, c.label)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("RESULT %s sha=%s\nDONE\n", c.label, c.sha)
		if err := os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Bash over the fixture to get the expected set.
	cmd := exec.Command("ls", resultsDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(out))
	sort.Strings(want)

	// Start miniredis and write card status.
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	if err := cardstatus.WriteResults(ctx, resultsDir, outDir, rdb); err != nil {
		t.Fatal(err)
	}

	// SCAN over card:* to get the set from Redis.
	got, err := cardstatus.ScanLabels(ctx, rdb)
	if err != nil {
		t.Fatal(err)
	}
	// No sort here: ScanLabels owns the sorted contract.

	// Compare. The HSCAN on each card:* key returns fields; the set of keys
	// must match the bash listing.
	if len(got) != len(want) {
		t.Fatalf("card status HSCAN returned %d labels %v, want %d from bash %v",
			len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("card status HSCAN label[%d] = %q, bash want %q", i, got[i], want[i])
		}
	}

	// Verify HSCAN on each card:* hash returns the expected fields.
	for _, card := range cards {
		key := cardstatus.CardPrefix + card.label
		fields, err := rdb.HGetAll(ctx, key).Result()
		if err != nil {
			t.Fatalf("HGetAll %s: %s", key, err)
		}
		if fields["label"] != card.label {
			t.Errorf("HSCAN card:%s label = %q, want %q", card.label, fields["label"], card.label)
		}
		if fields["sha"] != card.sha {
			t.Errorf("HSCAN card:%s sha = %q, want %q", card.label, fields["sha"], card.sha)
		}
	}

	// posted.tsv and reports.tsv have zero writers (exist but empty).
	for _, name := range []string{"posted.tsv", "reports.tsv"} {
		path := filepath.Join(outDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s not created: %s", name, err)
		}
		if info.Size() != 0 {
			t.Errorf("%s has %d bytes, want 0 (zero writers)", name, info.Size())
		}
	}
}

// pagedScanner is a controlled SCAN source: each call returns the next page
// as given, unsorted and with keys repeated across pages, the way a real
// Redis may answer while the keyspace rehashes. miniredis cannot produce this
// (its SCAN sorts the matches and returns them in one page), so the contract
// test below does not use it.
type pagedScanner struct {
	pages   [][]string
	calls   int
	cursors []uint64
}

func (p *pagedScanner) Scan(_ context.Context, cursor uint64, match string, _ int64) *redis.ScanCmd {
	p.cursors = append(p.cursors, cursor)
	if match != cardstatus.CardPrefix+"*" {
		return redis.NewScanCmdResult(nil, 0, fmt.Errorf("unexpected match %q", match))
	}
	i := p.calls
	p.calls++
	if i >= len(p.pages) {
		return redis.NewScanCmdResult(nil, 0, fmt.Errorf("scan called %d times for %d pages", p.calls, len(p.pages)))
	}
	next := uint64(0)
	if i+1 < len(p.pages) {
		next = uint64(100 + i)
	}
	return redis.NewScanCmdResult(p.pages[i], next, nil)
}

// TestScanLabelsSortsAndDedupsUnsortedDuplicatePages is the regression control
// for ScanLabels' documented contract: labels come back sorted and unique even
// when SCAN pages arrive unsorted and repeat keys across cursors. Dropping
// sort.Strings or the seen-set in ScanLabels fails this test.
func TestScanLabelsSortsAndDedupsUnsortedDuplicatePages(t *testing.T) {
	src := &pagedScanner{pages: [][]string{
		{"card:card-003", "card:card-001"},
		{"card:card-004", "card:card-003", "card:card-002"},
		{"card:card-001", "card:card-004"},
	}}
	got, err := cardstatus.ScanLabels(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"card-001", "card-002", "card-003", "card-004"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ScanLabels over unsorted duplicate pages = %v, want sorted unique %v", got, want)
	}
	if src.calls != 3 {
		t.Fatalf("ScanLabels made %d SCAN calls, want 3 (one per page until cursor 0)", src.calls)
	}
	if src.cursors[0] != 0 || src.cursors[1] != 100 || src.cursors[2] != 101 {
		t.Fatalf("ScanLabels passed cursors %v, want [0 100 101] (each page's next cursor)", src.cursors)
	}
}
