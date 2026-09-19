package ci

// Issue #1048, item 3. Its first two parts landed -- `native` points GOMODCACHE and GOCACHE
// at a per-bench cache (#1195, TestNativeSharedGoCaches), and the reaper deletes by lease
// and age -- but the third did not, and the third is the one that NOTICES.
//
// A Go card costs 5-7 GB in its own slot. hulk and vision filled to 100% under 120 cards
// and their runners died; one bench reached 0 free with 53 GB of slot data homes. Both
// launchers refuse a bench under 25 GB free (`(free_gb - 25) / 2` is a term of the capacity
// formula at cmd/nova-pulse/fill.go). A bench nothing may be launched onto is not a
// conforming bench -- and until this test, `tools/bench-standard.sh` said nothing about it
// at all: 283 lines with no `df`, no `free`, and no mention of space.
//
// The check has to NAME WHERE THE SPACE WENT, because "you are out of disk" is a sentence
// somebody then has to go and investigate by hand at whatever hour it is.
//
// The fake `df` is the whole method: the test cannot arrange for a real bench to be full,
// and a test that only runs where the developer's disk happens to be low is not a test.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// benchStandard runs tools/bench-standard.sh with a fake `df` first on PATH and a HOME of
// its own, and returns its combined output. Everything else about the bench is missing, so
// the script prints other DRIFT lines too and exits 1; only the disk line is read here.
func benchStandard(t *testing.T, freeGB string, home string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the bench standard is a bash script for a Linux bench")
	}
	bin := t.TempDir()
	// The fake answers `df -Pk` in 1K blocks and REFUSES the GNU-only `-BG`, the way a BSD
	// userland does. That is deliberate: the script must ask in the one POSIX spelling
	// `nova-pulse fleet standard`'s own `disk-free` probe uses, and a fake that answered
	// every flag would let a second, GNU-only spelling creep back in unnoticed.
	fake := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *-BG*) echo 'df: invalid option -- B' >&2; exit 1 ;;\n" +
		"esac\n" +
		"echo 'Filesystem 1024-blocks Used Available Capacity Mounted on'\n" +
		"echo \"/dev/fake 1048576000 1 $(( " + freeGB + " * 1048576 )) 99% /\"\n"
	if err := os.WriteFile(filepath.Join(bin, "df"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "tools", "bench-standard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
	)
	out, _ := cmd.CombinedOutput() // a bench missing everything exits 1; the lines are the answer
	return string(out)
}

// TestBenchStandardDriftsOnDiskHeadroom is #1048 item 3.
func TestBenchStandardDriftsOnDiskHeadroom(t *testing.T) {
	t.Parallel()

	// A bench with room: no disk DRIFT line, whatever else is missing.
	home := t.TempDir()
	// 400 GiB free.
	if got := benchStandard(t, "400", home); strings.Contains(got, "DRIFT disk") {
		t.Errorf("a bench with 400G free drifted on disk:\n%s", got)
	}

	// A bench under the floor: one DRIFT line naming the number, the floor, and the three
	// largest directories under HOME so nobody has to go and look.
	low := t.TempDir()
	for name, size := range map[string]int{"huge": 900, "big": 600, "medium": 300, "small": 40} {
		dir := filepath.Join(low, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, size*1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 3 GiB free: under the 25 G floor both launchers refuse below.
	got := benchStandard(t, "3", low)
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "DRIFT disk") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("a bench with 3G free printed no `DRIFT disk` line; the standard is green on a bench no launcher will use:\n%s", got)
	}
	for _, want := range []string{"free=3G", "want>=25G", "huge", "big", "medium"} {
		if !strings.Contains(line, want) {
			t.Errorf("the disk DRIFT line does not carry %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "small") {
		t.Errorf("the disk DRIFT line names more than the three largest directories:\n%s", line)
	}
	if strings.Count(got, "DRIFT disk") != 1 {
		t.Errorf("the disk finding is not one line:\n%s", got)
	}
}
