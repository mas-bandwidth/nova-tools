package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A launched pulse has no pulses/<id>.packet, so harvest must sum the usd
// column of the folded jobs' usage.tsv rather than printing usd=-.
func TestHarvestSumsUsageSpendWithoutPacket(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")

	addCard(t, root, "fixlaunchprobe", "101", "flash", "RESULT fixlaunchprobe sha=aaa",
		"RESULT fixlaunchprobe sha=aaa\nDONE\nBRANCH br1\nREPO owner/repo\n")
	usage := "job\tattempt\tstarted\tended\trc\tprovider\tmodel\tin\tout\tcache_w\tcache_r\treason\tusd\n" +
		"fixlaunchprobe\t1\t2026-09-19T17:42:31Z\t2026-09-19T17:42:40Z\t0\tdeepseek\tdeepseek-flash\t5931\t166\t0\t16384\t324\t0.0012\n"
	if err := os.WriteFile(filepath.Join(root, "101", "jobs", "fixlaunchprobe", "usage.tsv"), []byte(usage), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _ := runHarvest(t, root)
	if strings.Contains(out, "usd=-") {
		t.Fatalf("harvest reports usd=- although usage.tsv holds 0.0012:\n%s", out)
	}
	if !strings.Contains(out, "usd=0.0012") {
		t.Fatalf("harvest must sum usage.tsv spend without a packet, want usd=0.0012, got:\n%s", out)
	}
}
