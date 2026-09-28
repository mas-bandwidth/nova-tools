package preflight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStandard(t *testing.T) {
	t.Parallel()

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
