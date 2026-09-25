package life_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// TestBenchBeatCarriesFacts is the writer half of #3646: the bench measures
// its harness versions, mirrors and free disk under its root, and the beat
// carries them on bench:<b>:beat through ns_bench_beat, so preflight --fleet
// reads them from Redis without ssh.
func TestBenchBeatCarriesFacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{
		"harness-v1.18.20", "harness-v1.18.19",
		"mirror/nova-tools.git/objects", "mirror/schema.git/objects",
		"mirror/half.git", // no objects: not a mirror
		"results",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "harness-v9.9.9"), nil, 0o644); err != nil { // a file, not a harness
		t.Fatal(err)
	}
	f := life.MeasureBench(root)
	if f.Harness != "1.18.19,1.18.20" {
		t.Errorf("harness %q", f.Harness)
	}
	if f.Mirrors != "nova-tools,schema" {
		t.Errorf("mirrors %q", f.Mirrors)
	}
	if n, err := strconv.Atoi(f.DiskGiB); err != nil || n < 0 {
		t.Errorf("disk %q is not whole GiB", f.DiskGiB)
	}
	if missing := life.MeasureBench(filepath.Join(root, "absent")); missing != (life.Facts{}) {
		t.Errorf("an absent root measured %+v, want nothing", missing)
	}

	st, client, _ := controlRedis(t)
	ctx := context.Background()
	res, err := life.BenchBeat(ctx, st, life.BenchRequest{
		Bench: "b1", Host: "host-a", Session: "sess-a", Actor: "bench", Facts: f,
	})
	if err != nil || !res.Accepted {
		t.Fatalf("bench beat: %+v %v", res, err)
	}
	beat := client.HGetAll(ctx, "bench:b1:beat").Val()
	if beat["harness"] != f.Harness || beat["mirrors"] != f.Mirrors || beat["disk_gib"] != f.DiskGiB {
		t.Fatalf("beat %v does not carry the facts %+v", beat, f)
	}
}
