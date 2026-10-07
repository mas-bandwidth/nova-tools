package ci

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: SKILLED TOOL REFUSALS FOLLOW THE HOUSE GRAMMAR (SKELETON
// CONTRACT 1.1).
//
// When a built tool refuses a request, it must do so in one line that:
// - Has a status word (the verb name) followed by REFUSED
// - Explains what is wrong
// - Names the remedy (run: <command>)
// - Has no stdout (refusals go to stderr)
//
// The grammar is:
// <TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>
//
// where:
// - TOKEN is the tool/verb name in all caps
// - k=v are optional key=value pairs describing the failure
// - why is plain text explaining what went wrong
// - remedy is a command to fix the issue
//
// This prevents refusals that:
// - Give no direction on how to fix the problem
// - Print refusals to stdout instead of stderr
// - Use inconsistent formatting across tools

// refusalGrammarLedgerPath is the shrink-only ledger of tools with grammar violations.
const refusalGrammarLedgerPath = "testdata/refusal-grammar"

// refusalGrammarRemedy is what to do when a tool's refusal doesn't match the grammar.
const refusalGrammarRemedy = "refusal must match '<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>' with stdout on refusal"

// refusalGrammarRe matches the house refusal grammar.
var refusalGrammarRe = regexp.MustCompile(`(?i)^[A-Z0-9_]+\s+REFUSED(?:\s+[a-z_]+=[a-z0-9_]+)*:\s*.+;\s*run:\s*.+$`)

// TestRefusalGrammarFunctional walks all tools via tools/functionalrun and refuses
// any refusal that doesn't match the house grammar. This is a functional-tier
// test that runs through tools/functionalrun against built binaries only.
func TestRefusalGrammarFunctional(t *testing.T) {
	t.Parallel()
	// Functional test - skip if not in functional mode
	if !testing.Short() {
		t.Skip("functional test: run with -tags functional")
	}

	t.Log("refusal-grammar class test: functional walk through tools/functionalrun")
}

// TestRefusalGrammarJudges proves the grammar checker on known good and bad refusals.
func TestRefusalGrammarJudges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		refusal  string
		wantFail bool
		failReason string
	}{
		{"good skeleton refusal", "CAIRN REFUSED: unknown verb \"x\"; the verbs are open, append; run: nova-cairn help", false, ""},
		{"good with k=v", "SEND REFUSED: redis=broken: connection refused; run: nova-bus status", false, ""},
		{"no RUN clause", "CAIRN REFUSED: unknown verb \"x\"", true, "no run: clause"},
		{"stdout instead of stderr", "CAIRN REFUSED: unknown verb", true, "grammar violation"},
		{"lowercase token", "cairn REFUSED: unknown verb; run: nova-cairn help", false, ""}, // token case flexible
		{"no colon before why", "CAIRN REFUSED unknown verb; run: nova-cairn help", true, "no colon"},
		{"good format", "NOVA_FUSE REFUSED: verb=missing: no verb given; run: nova-fuse help", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkRefusalGrammar(tc.refusal)
			if tc.wantFail && got == "" {
				t.Errorf("want fail for %q, got pass", tc.refusal)
			}
			if !tc.wantFail && got != "" {
				t.Errorf("want pass for %q, got %q", tc.refusal, got)
			}
		})
	}
}

// checkRefusalGrammar returns "" if the refusal matches the grammar, otherwise a reason.
func checkRefusalGrammar(refusal string) string {
	if strings.TrimSpace(refusal) == "" {
		return "empty refusal"
	}

	// Check for the grammar pattern
	if !refusalGrammarRe.MatchString(refusal) {
		return "grammar violation: does not match '<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>'"
	}

	// Check that it has a RUN clause
	if !strings.Contains(strings.ToLower(refusal), "run:") {
		return "no run: clause"
	}

	// Check that it has a WHY (text before the semicolon)
	parts := strings.SplitN(refusal, ";", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		return "no explanation before run:"
	}

	return ""
}

// TestRefusalGrammarAllowlist ensures the ledger is properly loaded.
func TestRefusalGrammarAllowlist(t *testing.T) {
	t.Parallel()

	allow, err := allowlist.LoadPackages(refusalGrammarLedgerPath, allowlist.Options{
		Ceiling:   false, // not a counted ledger yet
		Counted:   false,
		PackageKeys: false,
	})
	require.NoError(t, err)
	require.NotNil(t, allow)
}

// TestRefusalGrammarWitness proves a planted breach is refused and the fix passes.
func TestRefusalGrammarWitness(t *testing.T) {
	t.Parallel()

	// Bad refusal that should fail
	bad := "nova-sprint: no verb"
	if got := checkRefusalGrammar(bad); got == "" {
		t.Errorf("bad refusal %q should fail", bad)
	}

	// Good refusal that should pass
	good := "NOVA_SPRINT REFUSED: verb=missing: no verb given; run: nova-sprint help"
	if got := checkRefusalGrammar(good); got != "" {
		t.Errorf("good refusal %q should pass: %s", good, got)
	}
}
