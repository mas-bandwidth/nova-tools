package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue1810 reproduces nova-tools#1810: a pulse root holding a job with a
// usage.tsv and no pulses/<id>.packet -- the file launch never writes -- must
// print the summed spend on the HARVEST line, not usd=-.
func TestIssue1810(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")

	id := "20260919T174228Z-pulse-7e1773"
	writePulseTable(t, root, id, 1, 1, "300")

	addCard(t, root, "fixlaunchprobe", "101", "flash", "RESULT fixlaunchprobe sha=aaa",
		"RESULT fixlaunchprobe sha=aaa\nDONE\nBRANCH br1\nREPO owner/repo\n")
	usage := "job\tattempt\tstarted\tended\trc\tprovider\tmodel\tin\tout\tcache_w\tcache_r\treason\tusd\n" +
		"fixlaunchprobe\t1\t2026-09-19T17:42:31Z\t2026-09-19T17:42:40Z\t0\tdeepseek\tdeepseek-flash\t5931\t166\t0\t16384\t324\t0.0012\n"
	if err := os.WriteFile(filepath.Join(root, "101", "jobs", "fixlaunchprobe", "usage.tsv"), []byte(usage), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	Harvest(HarvestInput{
		ID: id, Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
	})

	if strings.Contains(out.String(), "usd=-") {
		t.Fatalf("issue #1810: harvest reports usd=- on a launched pulse although the job's usage.tsv holds 0.0012:\n%s%s", out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "usd=0.0012") {
		t.Fatalf("issue #1810: harvest must sum the job's usage.tsv spend when no packet exists, want usd=0.0012, got:\n%s%s", out.String(), errs.String())
	}
}
