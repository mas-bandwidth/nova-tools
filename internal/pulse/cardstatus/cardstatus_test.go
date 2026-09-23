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
	sort.Strings(got)

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
