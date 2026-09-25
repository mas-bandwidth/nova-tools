package pulse

// progress.sh printed the estimate WITH its terms: pending, launched, the open PRs and the
// in-scope issues it counted twice, the p90 wall, the parallelism and the rate that
// produced the hours. A total with no terms is a number a reader cannot check, and an
// estimate nobody can check is one nobody acts on. The breakdown is appended, so the
// remaining_cards and hours contract the verb already had is unchanged.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// estimate-names-its-terms: every term of the arithmetic prints beside the answer.
func TestEstimateNamesItsTerms(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	queue := setupProgress(t, root)
	now, _ := time.Parse(time.RFC3339, "2026-09-15T12:00:00Z")
	writeProgressUsage(t, root, "slot-1", "j1", "2026-09-15T10:00:00Z", "2026-09-15T10:10:00Z", "0", "0.2000")
	writeProgressUsage(t, root, "slot-2", "j2", "2026-09-15T10:00:00Z", "2026-09-15T10:20:00Z", "0", "0.3000")
	writeStatusFile(t, queue, filepath.Join("pending", "card-1.md"), "one\n")
	writeStatusFile(t, queue, filepath.Join("pending", "card-2.md"), "two\n")
	writeStatusFile(t, queue, filepath.Join("launched", "card-3.md"), "three\n")

	out, errs, code := runProgress(t, queue, root, "2026-09-15", now)
	if code != 0 {
		t.Fatalf("progress exit = %d, want 0; stderr=%s", code, errs)
	}
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "ESTIMATE ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no ESTIMATE line:\n%s", out)
	}
	for _, want := range []string{
		"remaining_cards=3", "pending=2", "launched=1", "prs=0", "issues=0",
		"wall_p90_s=", "parallelism=", "rate=", "hours=",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the ESTIMATE line does not carry %q:\n%s", want, line)
		}
	}
}

// The terms must add up to the total the same way progress.sh added them:
// remaining = pending + launched + prs + 2 x issues. A breakdown that does not reconstruct
// the total is worse than none.
func TestEstimateTermsReconstructTheTotal(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	queue := setupProgress(t, root)
	now, _ := time.Parse(time.RFC3339, "2026-09-15T12:00:00Z")
	writeProgressUsage(t, root, "slot-1", "j1", "2026-09-15T10:00:00Z", "2026-09-15T10:10:00Z", "0", "0.2000")
	for i, name := range []string{"card-1.md", "card-2.md", "card-3.md"} {
		dir := "pending"
		if i == 2 {
			dir = "launched"
		}
		writeStatusFile(t, queue, filepath.Join(dir, name), "card\n")
	}
	out, _, _ := runProgress(t, queue, root, "2026-09-15", now)
	fields := estimateFields(t, out)
	got := fields["pending"] + fields["launched"] + fields["prs"] + 2*fields["issues"]
	if got != fields["remaining_cards"] {
		t.Fatalf("pending+launched+prs+2*issues = %d, but remaining_cards = %d:\n%s",
			got, fields["remaining_cards"], out)
	}
}

// estimateFields reads the whole-number terms of the ESTIMATE line.
func estimateFields(t *testing.T, out string) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "ESTIMATE ") {
			continue
		}
		for _, f := range strings.Fields(l) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			n := 0
			digits := true
			for _, r := range v {
				if r < '0' || r > '9' {
					digits = false
					break
				}
				n = n*10 + int(r-'0')
			}
			if digits && v != "" {
				got[k] = n
			}
		}
	}
	return got
}
