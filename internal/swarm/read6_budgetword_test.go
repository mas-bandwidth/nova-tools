package swarm

import (
	"testing"
	"time"
)

// RULE 13, VERBATIM (SPEC-SWARM.md:232-233): "a job that ends this way keeps the findings
// it appended so far (rule 3)."
//
// READ 6, FINDING 2: `violationWord` exempted only `EndKilled`, so a job the supervisor
// ended AT ITS BUDGET (`RUN BUDGET`) or because its usage source stopped being readable
// (`RUN BUDGET-UNVERIFIABLE`) got rule 11's word written into its sidecar whenever the same
// `Reap` left a process behind -- and `Jobs()` skips every sidecar with a `violation=`
// word, so the whole job vanished from `TRIAGE BATCH`, findings and all. The read-5
// reasoning for the deadline reap is the same reasoning here: a process that outlived the
// kill is a fact about the KILL, not a background subtask the prompt forbade.
func TestABudgetKillWithASurvivorKeepsItsFindings(t *testing.T) {
	for _, c := range []struct {
		name, end string
		survivors int
		want      string
	}{
		{"a job ended at its budget, nothing left", EndBudget, 0, ""},
		{"a job ended at its budget whose child survived the kill", EndBudget, 1, ""},
		{"a job whose usage stopped being readable, nothing left", EndUnverifiable, 0, ""},
		{"a job whose usage stopped being readable whose child survived the kill", EndUnverifiable, 1, ""},
		// Unchanged, and the reason the exemption is a list and not an else: rule 11's
		// own end, and a job with no evidence that left a process behind.
		{"rule 11's own end", EndViolation, 1, "background"},
		{"a job with no evidence that left a process behind", EndUnknown, 1, "background"},
	} {
		if got := violationWord(c.end, c.survivors); got != c.want {
			t.Errorf("%s: violation=%q, want %q", c.name, got, c.want)
		}
	}
}

// AND THE CONSEQUENCE, at the place the word is read: `triage` counts a budget-ended job.
func TestTriageCountsABudgetEndedJob(t *testing.T) {
	dir := t.TempDir()
	p, _ := recoveryPool(t, dir)
	sc := Sidecar{ID: NewID(time.Now().UTC(), "budgeted"), Files: 1, Tokens: 10, RC: -1,
		End: EndBudget, Class: ClassClean}
	sc.Violation = violationWord(EndBudget, 1)
	if err := p.Add([]byte("a task that ended at its budget with a survivor"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(sc.ID, Pending, Failed); err != nil {
		t.Fatal(err)
	}
	jobs, err := p.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("rule 13 keeps a budget-ended job's findings, so triage counts it: got %d jobs", len(jobs))
	}
}
