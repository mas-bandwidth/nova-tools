package card_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// gate3551 is the DONE-WHEN of #3551: each control must PASS exactly once.
var gate3551 = []string{"TestCardClaimIsAFunction", "TestCardPathMakesNoEvalCall"}

// gateRefused matches a FAIL or SKIP result line at any indentation, so a
// skipped or failed subtest nested under a passing parent refuses the gate.
var gateRefused = regexp.MustCompile(`(?m)^\s*--- (FAIL|SKIP): `)

// checkGate reads `go test -v` output and requires each named test's
// `--- PASS: <name> (` line exactly once at the top level, no FAIL or SKIP
// line at any indentation, no "no tests to run" warning, and the final
// top-level PASS. A name the run never selected is a refusal, not a pass.
func checkGate(out string, names []string) error {
	if m := gateRefused.FindString(out); m != "" {
		return fmt.Errorf("gate refused: %q", strings.TrimSpace(m))
	}
	if strings.Contains(out, "no tests to run") {
		return fmt.Errorf("gate refused: no tests to run")
	}
	for _, name := range names {
		pass := regexp.MustCompile(`(?m)^--- PASS: ` + regexp.QuoteMeta(name) + ` \(`)
		if n := len(pass.FindAllString(out, -1)); n != 1 {
			return fmt.Errorf("gate refused: %s PASS lines = %d, want exactly 1", name, n)
		}
	}
	if !regexp.MustCompile(`(?m)^PASS$`).MatchString(out) {
		return fmt.Errorf("gate refused: no final PASS")
	}
	return nil
}

// TestDoneWhenGateChecker is the checker's own controls: a complete run
// passes; a missing name, a duplicate PASS, a nested SKIP, a nested FAIL and
// a run that selected nothing are each refused.
func TestDoneWhenGateChecker(t *testing.T) {
	pass := func(name string) string { return "=== RUN   " + name + "\n--- PASS: " + name + " (0.01s)\n" }
	complete := pass(gate3551[0]) + pass(gate3551[1]) + "PASS\n"
	cases := []struct {
		name string
		out  string
		ok   bool
	}{
		{"complete", complete, true},
		{"missing", pass(gate3551[0]) + "PASS\n", false},
		{"duplicate", pass(gate3551[0]) + pass(gate3551[0]) + pass(gate3551[1]) + "PASS\n", false},
		{"nested skip", pass(gate3551[0]) + "--- PASS: " + gate3551[1] + " (0.01s)\n    --- SKIP: " + gate3551[1] + "/sub (0.00s)\nPASS\n", false},
		{"nested fail", complete + "        --- FAIL: " + gate3551[1] + "/sub/deep (0.00s)\n", false},
		{"top fail", pass(gate3551[0]) + "--- FAIL: " + gate3551[1] + " (0.01s)\nFAIL\n", false},
		{"nothing selected", "testing: warning: no tests to run\nPASS\n", false},
		{"no final pass", pass(gate3551[0]) + pass(gate3551[1]), false},
	}
	for _, tc := range cases {
		err := checkGate(tc.out, gate3551)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: checkGate = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	// The case the hold named: a -run pattern selecting one present control
	// plus one absent name exits 0 under go test, and the checker refuses it.
	if err := checkGate(pass(gate3551[0])+"PASS\n", []string{gate3551[0], "TestCardRequiredButAbsent"}); err == nil {
		t.Fatal("a present control plus an absent name passed the gate")
	}
}

// TestDoneWhen3551 is the executable DONE-WHEN of #3551: it runs this test
// binary again on exactly the two controls, verbose, and requires checkGate.
func TestDoneWhen3551(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run", "^("+strings.Join(gate3551, "|")+")$")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controls exited %v:\n%s", err, out)
	}
	if err := checkGate(string(out), gate3551); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
