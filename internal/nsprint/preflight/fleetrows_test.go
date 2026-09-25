package preflight

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestReadLibraryAsTheSeat is #3646's seat rule on a real redis-server with
// the fleet's ACL shape: the coordinator seat reads FUNCTION LIST and sees the
// binary's library; the bench seat is refused, and the fleet line says FAIL
// with NOPERM and the seat to run as, never PASS on an unread library.
func TestReadLibraryAsTheSeat(t *testing.T) {
	addr := testutil.Start(t,
		"--user", "default", "off",
		"--user", "coordinator", "on", ">coord-secret", "~*", "&*", "+@all",
		"--user", "bench", "on", ">bench-secret", "~*", "&*", "+@all", "-function")
	ctx := context.Background()
	coord := redis.NewClient(&redis.Options{Addr: addr, Username: "coordinator", Password: "coord-secret"})
	bench := redis.NewClient(&redis.Options{Addr: addr, Username: "bench", Password: "bench-secret"})
	t.Cleanup(func() { _ = coord.Close(); _ = bench.Close() })
	if err := fn.Load(ctx, coord); err != nil {
		t.Fatalf("load %s: %v", fn.Library, err)
	}

	lib := ReadLibrary(ctx, coord)
	if lib.Err != nil || lib.Have == "" || lib.Have != lib.Want {
		t.Fatalf("coordinator seat: %+v, want the binary's library", lib)
	}
	std := Standard{Build: "aa87769b", Harness: "1.18.20", Mirrors: []string{"nova-tools"}, DiskFloorGiB: 200}
	lines, _ := FleetRows(ctx, Fleet{}, std, lib)
	if last := lines[len(lines)-1]; !strings.Contains(last, "fnlib="+lib.Want) || strings.Contains(last, "fnlib(") {
		t.Fatalf("a matching library is named as failing: %s", last)
	}

	refused := ReadLibrary(ctx, bench)
	if refused.Err == nil {
		t.Fatalf("bench seat read FUNCTION LIST: %+v", refused)
	}
	lines, code := FleetRows(ctx, Fleet{}, std, refused)
	last := lines[len(lines)-1]
	if code != 1 || !strings.Contains(last, "fleet FAIL") || !strings.Contains(last, "fnlib=MISSING") {
		t.Fatalf("exit %d; a refused library did not fail the fleet line: %s", code, last)
	}
	for _, want := range []string{"NOPERM", "seat bench", "coordinator seat", "NOVA_SPRINT_REDIS_USER=coordinator"} {
		if !strings.Contains(last, want) {
			t.Errorf("fleet line lacks %q: %s", want, last)
		}
	}
}

func TestReadStandard(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "all.yml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	full := "nova_build: \"v0.16.0-dev.aa87769b\"\nharness_dir: harness-v1.18.20\nharness_version: \"1.18.20\"\n" +
		"mirrors: [nova-tools, schema, nova-work, rowan-tools, message-bus, serialize]   # the one declared list: x: y\n" +
		"friends:\n  mirrors: [not, this]\n"
	s, err := ReadStandard(write(full))
	if err != nil {
		t.Fatal(err)
	}
	if s.Build != "aa87769b" || s.Harness != "1.18.20" || s.DiskFloorGiB != DefaultDiskFloorGiB ||
		strings.Join(s.Mirrors, ",") != "nova-tools,schema,nova-work,rowan-tools,message-bus,serialize" {
		t.Fatalf("standard %+v", s)
	}
	if s, err := ReadStandard(write(full + "disk_floor_gib: 250\n")); err != nil || s.DiskFloorGiB != 250 {
		t.Fatalf("disk_floor_gib override: %+v %v", s, err)
	}
	for _, bad := range []string{
		"nova_build: \"v0.16.0-dev.aa87769b\"\nmirrors: [nova-tools]\n",
		"harness_version: \"1.18.20\"\nmirrors: [nova-tools]\n",
		"nova_build: \"v0.16.0-dev.aa87769b\"\nharness_version: \"1.18.20\"\n",
		full + "disk_floor_gib: lots\n",
	} {
		if _, err := ReadStandard(write(bad)); err == nil {
			t.Errorf("an incomplete standard was accepted:\n%s", bad)
		}
	}
}
