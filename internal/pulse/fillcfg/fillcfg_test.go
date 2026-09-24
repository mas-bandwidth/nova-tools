package fillcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// writeRegistry writes a machines registry with bench-a carrying the given notes.
func writeRegistry(t *testing.T, path, notes string) {
	t.Helper()
	line := "bench-a\tbench-a\tlinux/x64\tbench\tswarm-bench-a\t8\t" + notes + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestShareChangeMovesFillCap is the card's control: the fill cap is the bench's share in
// the machines registry minus the leases it holds, and the registry is read on every call,
// so editing the share moves the cap on the next tick with no restart and no argv.
func TestShareChangeMovesFillCap(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "machines.tsv")
	writeRegistry(t, reg, "certified=2026-09-20 share=10")
	src := Source{Machines: reg}
	held := "SLOT 1 owner=swarm-bench-a pid=9 label=- until=2026-09-20T00:00:00Z state=live\n" +
		"SLOT 2 owner=swarm-bench-a pid=10 label=- until=2026-09-20T00:00:00Z state=live\n"
	capacity := pulse.StoreCapacity{
		Probe: func(bench string) (string, error) {
			share, ok, err := src.Share(bench)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("no share for %s", bench)
			}
			return fmt.Sprintf("store share=%d cores=8 load1=0\nleases\n%s", share, held), nil
		},
	}
	got, err := capacity.Capacity("bench-a")
	if err != nil {
		t.Fatalf("capacity at share=10: %v", err)
	}
	if got != 8 {
		t.Fatalf("cap = %d at share=10 with 2 held, want 8", got)
	}
	writeRegistry(t, reg, "certified=2026-09-20 share=20")
	got, err = capacity.Capacity("bench-a")
	if err != nil {
		t.Fatalf("capacity at share=20: %v", err)
	}
	if got != 18 {
		t.Fatalf("cap = %d after the share moved to 20 with 2 held, want 18 (the config was not re-read)", got)
	}
}

// TestShareAbsentIsNotZero: a bench with no share= in its notes answers ok=false, never a
// zero share, so the caller can say which case it is.
func TestShareAbsentIsNotZero(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "machines.tsv")
	writeRegistry(t, reg, "certified=2026-09-20")
	n, ok, err := Source{Machines: reg}.Share("bench-a")
	if err != nil || ok || n != 0 {
		t.Fatalf("Share = %d, %v, %v; want 0, false, nil", n, ok, err)
	}
}

// TestShareRefusesAMalformedValue: share=abc or share=-3 is a refusal naming the bench,
// never a silent fallback to some other number.
func TestShareRefusesAMalformedValue(t *testing.T) {
	for _, notes := range []string{"share=abc", "share=-3", "share=", "share=4 share=5"} {
		reg := filepath.Join(t.TempDir(), "machines.tsv")
		writeRegistry(t, reg, notes)
		_, _, err := Source{Machines: reg}.Share("bench-a")
		if err == nil || !strings.Contains(err.Error(), "bench-a") {
			t.Fatalf("notes %q: err = %v, want a refusal naming bench-a", notes, err)
		}
	}
}

// TestShareRefusesABenchTheRegistryDoesNotName: a bench missing from the registry has no
// configured share, and that is an error rather than a guess.
func TestShareRefusesABenchTheRegistryDoesNotName(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "machines.tsv")
	writeRegistry(t, reg, "share=4")
	if _, _, err := (Source{Machines: reg}).Share("bench-z"); err == nil {
		t.Fatal("a bench the registry does not name answered a share")
	}
}
