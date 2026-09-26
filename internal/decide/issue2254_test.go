package decide

// nova-tools #2254 — Reading 2 (harvest): the bench-red table/fact rule and
// the observed rows (docs/SPEC-DECIDE.md:1171-1190; the demanded tests 53 and
// 54, :1765-1766 and :1998-1999).
//
// The bench-red loosening -- a blocked-toolchain/bench unit is eligible for
// another bench and is NOT appended to the unit as a confirmed failure of its
// rung -- rests on mechanical facts and on no answer: it requires the rule
// table's toolchain row AND a fact the card did not write (the job's exit
// status being 126 or 127, or the supervisor's own harness-error line naming
// a missing executable), and a unit is moved to another bench on this ground
// at most once. A provider's blocked-toolchain without those facts is
// printed, is a label for the manager, and leaves the failure counted exactly
// as today. A later harvest of the SAME unit on another bench appends an
// observed row -- event=rerun-clean or event=rerun-red with the bench in
// fields -- and never a truth row.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchRedEvidence is the OUTCOME-token shape the bench-red rule reads: the
// gate left the question open (abstain/BLOCKED) and the reason token is the
// toolchain row's. The supervisor-owned facts ride beside it; no answer can
// set them.
func benchRedEvidence() HarvestEvidence {
	return HarvestEvidence{
		Accept: AcceptAbstain,
		Line2:  Line2Blocked,
		Reason: "toolchain-missing",
	}
}

// rulesToolchainClass is the rule table's toolchain row classified: the rules
// answer blocked-toolchain and no provider is asked.
func rulesToolchainClass(t *testing.T, ev HarvestEvidence) Classification {
	t.Helper()
	c := ClassifyHarvest(ev, Result{Answer: ClassBlockedToolchain, Confidence: 1.0, Decider: DeciderRules, Why: WhyNone}, 0.65)
	if c.Class != ClassBlockedToolchain || c.Decider != DeciderRules {
		t.Fatalf("fixture: the rule table's toolchain row must classify blocked-toolchain by rules, got %+v", c)
	}
	return c
}

func TestIssue2254(t *testing.T) {
	t.Parallel()

	// 53: `a-bench-red-needs-the-table-and-a-fact-the-card-did-not-write`.
	t.Run("a bench red needs the table and a fact the card did not write", func(t *testing.T) {
		// The loosening holds: the table's row AND the job's exit status 126.
		ev := benchRedEvidence()
		ev.ExitStatus = 126
		if !rulesToolchainClass(t, ev).ChangesBench() {
			t.Error("the table's toolchain row with exit status 126 must license the bench move")
		}
		// Exit status 127.
		ev = benchRedEvidence()
		ev.ExitStatus = 127
		if !rulesToolchainClass(t, ev).ChangesBench() {
			t.Error("the table's toolchain row with exit status 127 must license the bench move")
		}
		// Or the supervisor's own harness-error line naming a missing
		// executable, whatever the exit status.
		ev = benchRedEvidence()
		ev.ExitStatus = 1
		ev.HarnessError = `harness: exec: "gotoolchain-xyz": command not found`
		if !rulesToolchainClass(t, ev).ChangesBench() {
			t.Error("the table's toolchain row with a harness-error line naming a missing executable must license the bench move")
		}

		// The fact is the NAME of a missing executable. A bare "no such file
		// or directory" names a missing file, not an executable, and licenses
		// nothing; the same phrase with the exec it came from does.
		for _, line := range []string{
			`harness: open /tmp/job/REPORT: no such file or directory`,
			`no such file or directory`,
		} {
			ev = benchRedEvidence()
			ev.ExitStatus = 1
			ev.HarnessError = line
			if rulesToolchainClass(t, ev).ChangesBench() {
				t.Errorf("harness-error line %q names no executable yet licensed a bench move", line)
			}
		}
		for line, want := range map[string]string{
			`harness: fork/exec /usr/local/bin/gcc: no such file or directory`: "/usr/local/bin/gcc",
			`harness: exec: "sqlite3": executable file not found in $PATH`:     "sqlite3",
			`bash: line 1: cargo: command not found`:                           "cargo",
		} {
			if got := missingExecutable(line); got != want {
				t.Errorf("missingExecutable(%q) = %q, want %q", line, got, want)
			}
			ev = benchRedEvidence()
			ev.ExitStatus = 1
			ev.HarnessError = line
			if !rulesToolchainClass(t, ev).ChangesBench() {
				t.Errorf("harness-error line %q names a missing executable and must license the bench move", line)
			}
		}

		// The rules' toolchain row is keyed on the toolchain-missing token: a
		// rules-decided blocked-toolchain with another reason is not that row.
		for _, reason := range []string{"precondition", "-"} {
			ev = benchRedEvidence()
			ev.Reason = reason
			ev.ExitStatus = 126
			if rulesToolchainClass(t, ev).ChangesBench() {
				t.Errorf("reason %q paired with a rules blocked-toolchain licensed a bench move; only the toolchain-missing row may", reason)
			}
		}

		// S4's harvest fixture (:1687): blocked-toolchain/bench with exit
		// status 1 and no harness-error line is a label only -- the failure
		// IS counted, exactly as today.
		ev = benchRedEvidence()
		ev.ExitStatus = 1
		if rulesToolchainClass(t, ev).ChangesBench() {
			t.Error("exit status 1 with no harness-error line licensed a bench move; a bench red without a fact the card did not write is a label only and leaves the failure counted")
		}
		// No supervisor fact at all: the same.
		if rulesToolchainClass(t, benchRedEvidence()).ChangesBench() {
			t.Error("the table's row alone licensed a bench move; the loosening also needs a fact the card did not write")
		}

		// A PROVIDER's blocked-toolchain is a label for the manager, facts or
		// no facts: the loosening rests on the rule table's row and on no
		// answer.
		ev = benchRedEvidence()
		ev.ExitStatus = 126
		c := ClassifyHarvest(ev, Result{Answer: ClassBlockedToolchain, Confidence: 0.99, Decider: DeciderJev, Why: WhyNone}, 0.65)
		if c.Class != ClassBlockedToolchain || c.Decider != DeciderJev {
			t.Fatalf("fixture: the provider's answer must classify blocked-toolchain by jev, got %+v", c)
		}
		if c.ChangesBench() {
			t.Error("a provider's blocked-toolchain licensed a bench move; the loosening rests on the rule table's toolchain row and on no answer")
		}
		// A fact without the table's row is nothing either: a provider's
		// defect at exit 126 moves no bench.
		d := ClassifyHarvest(ev, Result{Answer: ClassDefect, Confidence: 0.99, Decider: DeciderJev, Why: WhyNone}, 0.65)
		if d.ChangesBench() {
			t.Error("a defect is not the toolchain row; exit 126 alone licensed a bench move")
		}

		// The supervisor's facts are for the rule, never for the provider:
		// none of them crosses into the framed state (S1/S2).
		ev.HarnessError = `harness: exec: "gotoolchain-xyz": command not found`
		state, err := HarvestState(ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"gotoolchain-xyz", "command not found", "exit", "harness"} {
			if strings.Contains(state, leak) {
				t.Errorf("a supervisor fact (%q) crossed into the provider frame:\n%s", leak, state)
			}
		}
	})

	// 54: `a-unit-changes-bench-on-this-ground-once`.
	t.Run("a unit changes bench on this ground once", func(t *testing.T) {
		ev := benchRedEvidence()
		ev.ExitStatus = 126
		if !rulesToolchainClass(t, ev).ChangesBench() {
			t.Fatal("fixture: the first move on this ground is licensed")
		}
		// The second such move is refused: `once` is a fact about the unit,
		// not a preference.
		ev.BenchMoved = true
		if rulesToolchainClass(t, ev).ChangesBench() {
			t.Error("a unit already moved on this ground was moved again; the second such move is refused")
		}
	})

	// 54: `a-defect-files-nothing`. The FINDING-CANDIDATE line itself is the
	// harvest lane's (internal/pulse harvest.go); at this boundary the rule is
	// that a defect routes and files nothing: its one outcome row carries no
	// finding and no truth (rule 7 -- a finding is a person's or a stronger
	// reader's to confirm).
	t.Run("a defect files nothing", func(t *testing.T) {
		c := ClassifyHarvest(benchRedEvidence(), Result{Answer: ClassDefect, Confidence: 0.90, Decider: DeciderJev, Why: WhyNone}, 0.65)
		if c.Class != ClassDefect {
			t.Fatalf("fixture: got %+v", c)
		}
		if c.ChangesBench() {
			t.Error("a defect moves no bench")
		}
		path := filepath.Join(t.TempDir(), "outcomes.jsonl")
		if err := AppendOutcomeRow(path, "unit-defect", c); err != nil {
			t.Fatal(err)
		}
		rows := readIssue2254Rows(t, path)
		if len(rows) != 1 {
			t.Fatalf("a defect appends its one outcome row and files nothing else, got %d rows", len(rows))
		}
		for _, key := range []string{"finding", "truth", "event", "issue"} {
			if _, ok := rows[0][key]; ok {
				t.Errorf("a defect's row files %q; a finding is a person's or a stronger reader's to confirm (rule 7)", key)
			}
		}
		// Control: the one row is the defect's own outcome row, so the
		// absence above is about filing, not about the row being empty.
		if rows[0]["class"] != ClassDefect {
			t.Errorf("control: the one row is the defect's own outcome row: %v", rows[0])
		}
	})

	// 54: `a-rerun-elsewhere-is-observed-and-writes-no-truth`.
	t.Run("a rerun elsewhere is observed and writes no truth", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "outcomes.jsonl")
		// The unit's own outcome row stands beside the rerun rows, untouched.
		first := ClassifyHarvest(benchRedEvidence(), Result{Answer: ClassClean, Confidence: 0.95, Decider: DeciderJev, Why: WhyNone}, 0.65)
		if err := AppendOutcomeRow(path, "unit-1", first); err != nil {
			t.Fatal(err)
		}
		// A later harvest of the SAME unit on another bench appends the
		// observed rows (D6): event=rerun-clean or event=rerun-red, with the
		// bench in fields.
		if err := AppendObservedRow(path, "unit-1", EventRerunClean, "hulk"); err != nil {
			t.Fatal(err)
		}
		if err := AppendObservedRow(path, "unit-1", EventRerunRed, "studio"); err != nil {
			t.Fatal(err)
		}

		rows := readIssue2254Rows(t, path)
		if len(rows) != 3 {
			t.Fatalf("the outcome row and two observed rows, got %d rows", len(rows))
		}
		clean, red := rows[1], rows[2]
		if clean["event"] != EventRerunClean {
			t.Errorf("the clean rerun's event is %v, want %s", clean["event"], EventRerunClean)
		}
		if red["event"] != EventRerunRed {
			t.Errorf("the red rerun's event is %v, want %s", red["event"], EventRerunRed)
		}
		fields, ok := clean["fields"].(map[string]any)
		if !ok || fields["bench"] != "hulk" {
			t.Errorf("the bench rides in fields: %v", clean)
		}
		fields, ok = red["fields"].(map[string]any)
		if !ok || fields["bench"] != "studio" {
			t.Errorf("the bench rides in fields: %v", red)
		}
		// Neither is truth: a card clean elsewhere may have been racing a
		// flaky fixture, and one red elsewhere may have hit a second bench's
		// second defect. Zero truth rows.
		for i, r := range rows[1:] {
			if r["observed"] != true {
				t.Errorf("row %d is not marked observed: %v", i+2, r)
			}
			for _, key := range []string{"truth", "class", "conf", "floor"} {
				if _, ok := r[key]; ok {
					t.Errorf("row %d carries %q: an observed row is never truth", i+2, key)
				}
			}
		}
		// Control: the unit's own outcome row DOES carry its class -- the
		// file can hold a verdict, and the rerun rows deliberately hold none.
		if rows[0]["class"] != ClassClean {
			t.Errorf("control: the outcome row keeps its class: %v", rows[0])
		}

		// The event set is closed, and the bench is never guessed.
		if err := AppendObservedRow(path, "unit-1", "truth", "hulk"); err == nil {
			t.Error("an event outside the closed set was written")
		}
		if err := AppendObservedRow(path, "unit-1", EventRerunClean, ""); err == nil {
			t.Error("a rerun row with no bench was written")
		}
		if err := AppendObservedRow(path, "", EventRerunClean, "hulk"); err == nil {
			t.Error("a rerun row with no unit was written")
		}
		if rows := readIssue2254Rows(t, path); len(rows) != 3 {
			t.Errorf("a refused row was appended: %d rows", len(rows))
		}
	})
}

// readIssue2254Rows reads the outcomes log back as raw objects.
func readIssue2254Rows(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("the row is not one JSON object: %v", err)
		}
		out = append(out, row)
	}
	return out
}
