package pulse

// Class M (#828): a rule lives in pulse.toml, in RULES.tsv or in a test, and POLICY.md
// keeps the record. The seed is the migration, and these are its proofs.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// policyFixture is six lines of the bench's own prose, one per kind the seed must guess.
// Every line is copied in shape from queue/POLICY.md of 2026-09-16.
const policyFixture = `# Manager policy (the finite policy the manager tier executes)
STOP on red (added 03:50Z): on MAIN-RED write queue/STOP with the sha; nothing new launches until main is green.
- 2026-09-16 03:46Z when the red is found the first action is the revert, never a fix first.
Uniform-abstain batches (added 00:45Z): a batch whose cards all abstain with one reason is escalated, never requeued.
- 2026-09-16 17:55Z Glenn: swarm cards run on the flash route only; a MODEL line naming another model is rewritten at cut.
- 2026-09-16 19:05Z Glenn: Space card slots are 128, and no bench may take more.
PIT STOP 2 OVER (16:00Z): items 1-6 landed or carded, and the failed pile was triaged.
a line with no date at all, which is prose and not a rule
`

func runRules(t *testing.T, in RulesInput) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	in.Stdout, in.Stderr = &out, &errs
	if in.Now == nil {
		in.Now = func() time.Time { return time.Date(2026, 9, 16, 20, 0, 0, 0, time.UTC) }
	}
	return Rules(in), out.String(), errs.String()
}

// rules-seed-from-policy: six dated lines, six rows, each with the kind its words say --
// and the undated line is not a row, because a row with no source cannot be argued with.
func TestRulesSeedFromPolicyGuessesTheKinds(t *testing.T) {
	queue := t.TempDir()
	policy := filepath.Join(queue, "POLICY.md")
	if err := os.WriteFile(policy, []byte(policyFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, out, errs := runRules(t, RulesInput{Queue: queue, SeedFrom: policy})
	if exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out, errs)
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 1 {
		t.Errorf("the seed printed %d lines, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "rows=6") {
		t.Errorf("the seed line does not say six rows: %q", out)
	}

	rows, err := LoadRules(queue)
	if err != nil {
		t.Fatalf("the table does not read back: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("seeded %d rows, want 6 (one per dated line, and the undated line is not one)", len(rows))
	}
	want := []string{"stop", "revert", "requeue", "route", "slots", "record"}
	for i, k := range want {
		if rows[i].Kind != k {
			t.Errorf("row %d kind = %q, want %q (condition %q)", i+1, rows[i].Kind, k, rows[i].Condition)
		}
	}
	// Every row carries where it came from, and none carries a whole paragraph.
	for i, r := range rows {
		if r.Since != "2026-09-16" {
			t.Errorf("row %d since = %q, want the day of the seed", i+1, r.Since)
		}
		if strings.TrimSpace(r.Source) == "" || r.Source == "-" {
			t.Errorf("row %d has no source: a rule nobody can date is prose", i+1)
		}
		if len(r.Condition) > 200 || len(r.Verdict) > 200 {
			t.Errorf("row %d is longer than a line: %d/%d bytes", i+1, len(r.Condition), len(r.Verdict))
		}
	}
	// The record line is a record: it says what happened, and rules nothing.
	if rows[5].Kind != "record" || !strings.Contains(rows[5].Condition+rows[5].Verdict, "landed") {
		t.Errorf("the last row is %+v, want the PIT STOP record", rows[5])
	}

	// --check reads the table it just wrote, in one line.
	exit, out, errs = runRules(t, RulesInput{Queue: queue, Check: true})
	if exit != 0 || !strings.HasPrefix(out, "RULES CHECK OK ") {
		t.Fatalf("check exit %d: %s%s", exit, out, errs)
	}
	if !strings.Contains(out, "kinds=") {
		t.Errorf("the check line carries no kind counts: %q", out)
	}
}

// rules-check-refuses-a-row-no-verb-can-execute, each refusal naming the row.
func TestRulesCheckRefusesABadRow(t *testing.T) {
	queue := t.TempDir()
	body := rulesHeader + "\n" +
		"stop\tdev is red\twrite STOP\t2026-09-16\tPOLICY 03:50Z\t-\n" +
		"weather\tit rains\twait\t2026-09-16\tnobody\t-\n" +
		"read\t\tAPPROVE\t2026-09-16\tPOLICY 01:40Z\t-\n"
	if err := os.WriteFile(filepath.Join(queue, RulesFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, out, errs := runRules(t, RulesInput{Queue: queue, Check: true, Max: 20})
	if exit != 2 {
		t.Fatalf("check exit %d, want 2: %s%s", exit, out, errs)
	}
	if !strings.Contains(errs, "row 2: kind weather") || !strings.Contains(errs, "row 3: no condition") {
		t.Errorf("the refusal does not name both bad rows:\n%s", errs)
	}
	if !strings.Contains(errs, "RULES REFUSED: 2 of 3 rows") {
		t.Errorf("the refusal does not count the rows:\n%s", errs)
	}
}

// rules-with-no-table: a bench that has not seeded one still answers, in one line, and
// never invents rows.
func TestRulesWithNoTableIsOneLine(t *testing.T) {
	queue := t.TempDir()
	exit, out, errs := runRules(t, RulesInput{Queue: queue})
	if exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out, errs)
	}
	if !strings.Contains(out, "rows=0") || len(strings.Split(strings.TrimSpace(out), "\n")) != 1 {
		t.Errorf("an unseeded queue printed %q, want one line with rows=0", out)
	}
	// A seed of prose with no dated line is a refusal that names what a source is.
	prose := filepath.Join(queue, "notes.md")
	if err := os.WriteFile(prose, []byte("we should probably stop when main is red\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exit, _, errs := runRules(t, RulesInput{Queue: queue, SeedFrom: prose}); exit != 2 ||
		!strings.Contains(errs, "carries no dated line") {
		t.Errorf("seeding undated prose: exit %d, %q", exit, errs)
	}
}
