package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pool-reads-work-nodes (issue #466): a `work` source with one open, unleased,
// unblocked bug node and one open, unleased, unblocked item node yields work=2,
// two pool.tsv rows whose id is the node id, and candidates=2; a node that is
// leased or blocked is nowhere; the id is carried on the card's line 1 so
// harvest records the attempt on the node.
func TestPoolReadsWorkNodes(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	nodes := strings.Join([]string{
		"bug-1\tbug\topen\t\t\tFix the leak",
		"item-1\titem\topen\t\t\tAdd the widget",
		"bug-2\tbug\topen\tworker-1\t\tLeased bug",
		"item-2\titem\topen\t\tPR-7\tBlocked item",
		"bug-3\tbug\tclosed\t\t\tClosed bug",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(work, "nodes.tsv"), []byte(nodes), 0o644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeTestFile(t, dir, "sources.tsv", "work\t"+work+"\tfix\n")

	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("Pool exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "work=2") || !strings.Contains(out.String(), "candidates=2") {
		t.Fatalf("POOL OK line wrong: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 pool rows, got %d: %q", len(lines), string(raw))
	}
	ids := map[string]bool{}
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			t.Fatalf("row %q does not have 5 fields", line)
		}
		if parts[0] != "work" {
			t.Fatalf("row %q source = %q, want work", line, parts[0])
		}
		ids[parts[1]] = true
	}
	if !ids["bug-1"] || !ids["item-1"] {
		t.Fatalf("pool rows = %q, want ids bug-1 and item-1", string(raw))
	}
	// The node id rides on the card's line 1, so harvest can record the
	// attempt on the node: cutting the pool must name the node id there.
	poolPath := filepath.Join(root, "pool.tsv")
	rows, err := readPool(poolPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		card, reason := renderCard("RESULT label sha=000000000000\nYou are a worker. The deadline is the machinery's.\nSTEP 1. mkdir -p scratch && git clone -q https://github.com/o/r.git . && git checkout -b <branch>\nred line\ngreen line\n", row)
		if reason != "" {
			t.Fatalf("renderCard refused work row %q: %s", row.ID, reason)
		}
		first := strings.SplitN(card, "\n", 2)[0]
		if !strings.HasPrefix(first, "RESULT "+row.ID+" sha=") {
			t.Fatalf("card line 1 = %q, want RESULT %s sha=<sha12>", first, row.ID)
		}
	}
}
