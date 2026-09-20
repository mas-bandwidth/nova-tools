package pulse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2001 & #2011: Triage classification of NATIVE PROVIDER as CaseProvider ("provider")
// rather than a card or test failure, routing to an alternate provider when available.

// TestTriageClassifiesNativeProviderAsCaseProvider verifies that NATIVE PROVIDER failures
// are classified as CaseProvider ("provider") while normal card and test failures are not.
func TestTriageClassifiesNativeProviderAsCaseProvider(t *testing.T) {
	providerCases := []struct {
		name     string
		evidence string
	}{
		{
			name:     "unexpected-server-error with ref",
			evidence: "NATIVE PROVIDER label=card-101 job=/slot/jobs/card-101 why=unexpected-server-error ref=err_29c29bd4",
		},
		{
			name:     "provider-5xx",
			evidence: "NATIVE PROVIDER label=card-102 why=provider-5xx ref=err_fake_5xx",
		},
		{
			name:     "rate-limit 429",
			evidence: "NATIVE PROVIDER label=card-103 why=rate-limit",
		},
		{
			name:     "unknown-error",
			evidence: "NATIVE PROVIDER label=card-104 why=unknown-error ref=err_unknown",
		},
		{
			name:     "harvest retry prefix with native provider",
			evidence: "HARVEST RETRY label=card-105 card=card-105.md: NATIVE PROVIDER why=unexpected-server-error",
		},
		{
			name:     "multiline evidence containing native provider",
			evidence: "RESULT card-106 sha=0123456789ab\nNATIVE PROVIDER label=card-106 why=unexpected-server-error ref=err_29c29bd4",
		},
	}

	for _, tc := range providerCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCase(tc.evidence)
			if got != CaseProvider {
				t.Fatalf("ClassifyCase(%q) = %q, want %q", tc.evidence, got, CaseProvider)
			}
			refusalKind := ClassifyRefusal(tc.evidence)
			if refusalKind != CaseProvider {
				t.Fatalf("ClassifyRefusal(%q) = %q, want %q", tc.evidence, refusalKind, CaseProvider)
			}
		})
	}

	// Non-provider failures must NOT be classified as CaseProvider
	nonProviderCases := []struct {
		name     string
		evidence string
		want     string
	}{
		{
			name:     "card rc failure",
			evidence: "NATIVE INCOMPLETE label=card-201 rc=1 why=rc",
			want:     "fence",
		},
		{
			name:     "card no result failure",
			evidence: "NATIVE INCOMPLETE label=card-202 why=no-result",
			want:     "fence",
		},
		{
			name:     "test failure",
			evidence: "test failure: TestParseTriage failed in 0.04s",
			want:     "fence",
		},
		{
			name:     "permission denied",
			evidence: "PERMISSION denied: reading protected resource",
			want:     "fence",
		},
		{
			name:     "signature mismatch",
			evidence: "signature mismatch: contract line does not match",
			want:     "signature",
		},
	}

	for _, tc := range nonProviderCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCase(tc.evidence)
			if got == CaseProvider {
				t.Fatalf("ClassifyCase(%q) must not classify as CaseProvider, got %q", tc.evidence, got)
			}
			if got != tc.want {
				t.Fatalf("ClassifyCase(%q) = %q, want %q", tc.evidence, got, tc.want)
			}
		})
	}
}

// TestTriageRoutesToAlternateProviderWhenAvailable verifies that when a card encounters
// a provider failure, triage routes to an alternate provider if available in the route list,
// leaving the card on the original route if no alternate provider exists.
func TestTriageRoutesToAlternateProviderWhenAvailable(t *testing.T) {
	// 1. ProviderOf extraction
	if p := ProviderOf("opencode/deepseek-v4-flash"); p != "opencode" {
		t.Errorf("ProviderOf = %q, want opencode", p)
	}
	if p := ProviderOf("openrouter/deepseek-chat"); p != "openrouter" {
		t.Errorf("ProviderOf = %q, want openrouter", p)
	}
	if p := ProviderOf("deepseek"); p != "deepseek" {
		t.Errorf("ProviderOf = %q, want deepseek", p)
	}

	// 2. AlternateRoute selection
	available := []string{
		"opencode/deepseek-v4-flash",
		"openrouter/deepseek-v4-flash",
		"deepseek/deepseek-chat",
	}

	alt, ok := AlternateRoute("opencode/deepseek-v4-flash", available)
	if !ok || alt != "openrouter/deepseek-v4-flash" {
		t.Fatalf("AlternateRoute got (%q, %t), want (openrouter/deepseek-v4-flash, true)", alt, ok)
	}

	// When only same-provider routes are available, AlternateRoute returns ("", false)
	sameProviderOnly := []string{
		"opencode/deepseek-v4-flash",
		"opencode/deepseek-chat",
	}
	alt, ok = AlternateRoute("opencode/deepseek-v4-flash", sameProviderOnly)
	if ok || alt != "" {
		t.Fatalf("AlternateRoute with only same-provider routes got (%q, %t), want ('', false)", alt, ok)
	}

	// 3. RouteToAlternateProvider rewrites card content
	cardContent := "RESULT: card-301 nova-tools #2001 test first\n" +
		"MODEL: opencode/deepseek-v4-flash\n" +
		"ATTEMPT: 1\n" +
		"Do the work.\n"

	rerouted, newRoute, routed := RouteToAlternateProvider(cardContent, available)
	if !routed {
		t.Fatalf("RouteToAlternateProvider returned routed=false")
	}
	if newRoute != "openrouter/deepseek-v4-flash" {
		t.Errorf("newRoute = %q, want openrouter/deepseek-v4-flash", newRoute)
	}
	if !strings.Contains(rerouted, "MODEL: openrouter/deepseek-v4-flash") {
		t.Errorf("rerouted card does not contain updated MODEL line:\n%s", rerouted)
	}
	// Card contract line and attempt count must be preserved (provider failure is not a card defect)
	if !strings.HasPrefix(rerouted, "RESULT: card-301") {
		t.Errorf("contract line was altered:\n%s", rerouted)
	}
	if !strings.Contains(rerouted, "ATTEMPT: 1") {
		t.Errorf("attempt count was altered:\n%s", rerouted)
	}

	// 4. RouteToAlternateProvider when no alternate provider is available
	unchanged, unchangedRoute, routed := RouteToAlternateProvider(cardContent, sameProviderOnly)
	if routed {
		t.Fatalf("RouteToAlternateProvider unexpectedly routed when no alternate was available")
	}
	if unchangedRoute != "opencode/deepseek-v4-flash" {
		t.Errorf("unchangedRoute = %q, want opencode/deepseek-v4-flash", unchangedRoute)
	}
	if unchanged != cardContent {
		t.Errorf("card was unexpectedly modified:\n%s", unchanged)
	}
}

// TestTriageProviderPacketAndVerdict verifies that CaseProvider packets build cleanly
// under PacketMax and verdicts round-trip through ParseTriage and Apply.
func TestTriageProviderPacketAndVerdict(t *testing.T) {
	queue := t.TempDir()

	// Seed candidate rule for CaseProvider in RULES.tsv
	if err := AppendRule(queue, RuleRow{
		Kind:      CaseProvider,
		Condition: "unexpected-server-error",
		Verdict:   "RETRY",
	}); err != nil {
		t.Fatal(err)
	}

	refusal := "NATIVE PROVIDER label=card-401 why=unexpected-server-error ref=err_29c29bd4"
	p := BuildPacket(queue, CaseProvider, "card-401", []string{"RESULT card-401 sha=0123456789ab"}, refusal)

	if p.Case != CaseProvider {
		t.Fatalf("p.Case = %q, want %q", p.Case, CaseProvider)
	}
	if p.label() != "triage-provider-card-401" {
		t.Fatalf("p.label() = %q, want triage-provider-card-401", p.label())
	}
	if len(p.Rules) != 1 {
		t.Fatalf("candidate rules count = %d, want 1", len(p.Rules))
	}

	card := p.Card()
	if len(card) > PacketMax {
		t.Fatalf("card length %d exceeds PacketMax %d", len(card), PacketMax)
	}
	if !strings.Contains(card, "CASE provider ref=card-401") {
		t.Errorf("card missing CASE provider line:\n%s", card)
	}
	if !strings.Contains(card, "TRIAGE provider <verdict> <rule-row-or-NEW>") {
		t.Errorf("card missing TRIAGE provider shape instruction:\n%s", card)
	}

	// Verify line 1 sha binding
	head, body, ok := strings.Cut(card, "\n")
	if !ok {
		t.Fatal("card has no line 1")
	}
	sum := sha256.Sum256([]byte(body))
	wantLine1 := fmt.Sprintf("RESULT triage-provider-card-401 sha=%s", hex.EncodeToString(sum[:])[:12])
	if head != wantLine1 {
		t.Errorf("line 1 = %q, want %q", head, wantLine1)
	}

	// Parse valid verdict naming existing rule
	v, err := ParseTriage("TRIAGE provider RETRY unexpected-server-error\n")
	if err != nil {
		t.Fatalf("ParseTriage failed: %v", err)
	}
	if v.Case != CaseProvider || v.Verdict != "RETRY" || v.Rule != "unexpected-server-error" {
		t.Errorf("parsed verdict = %+v", v)
	}

	// Parse valid verdict naming NEW rule and apply it
	vNew, err := ParseTriage("TRIAGE provider RETRY NEW\n")
	if err != nil {
		t.Fatalf("ParseTriage with NEW failed: %v", err)
	}
	wrote, err := vNew.Apply(queue, "provider-503-retry")
	if err != nil || !wrote {
		t.Fatalf("Apply gave (%t, %v), want (true, nil)", wrote, err)
	}
	rules := ReadRules(queue)
	if len(rules) != 2 {
		t.Fatalf("RULES.tsv has %d rows, want 2", len(rules))
	}
	if rules[1].Kind != CaseProvider || rules[1].Condition != "provider-503-retry" || rules[1].State != "pending" {
		t.Errorf("appended rule = %+v", rules[1])
	}
}

// TestTriageSplitEvidenceWithNativeProvider verifies that splitEvidence correctly
// separates RESULT lines from the NATIVE PROVIDER line.
func TestTriageSplitEvidenceWithNativeProvider(t *testing.T) {
	lines := []string{
		"RESULT card-501 sha=abcdef123456",
		"OUTPUT tail from job execution",
		"NATIVE PROVIDER label=card-501 why=rate-limit",
	}

	results, refusal := splitEvidence(lines)
	if len(results) != 2 {
		t.Errorf("results length = %d, want 2: %v", len(results), results)
	}
	if refusal != "NATIVE PROVIDER label=card-501 why=rate-limit" {
		t.Errorf("refusal = %q, want NATIVE PROVIDER line", refusal)
	}
}

// TestTriageVerbWithCaseProvider drives the Triage verb end to end with CaseProvider.
func TestTriageVerbWithCaseProvider(t *testing.T) {
	queue := t.TempDir()
	if err := os.MkdirAll(filepath.Join(queue, "UNDECIDED"), 0o755); err != nil {
		t.Fatal(err)
	}
	evidence := "RESULT card-601 sha=0123456789ab\nNATIVE PROVIDER label=card-601 why=unexpected-server-error ref=err_29c29bd4\n"
	if err := os.WriteFile(filepath.Join(queue, "UNDECIDED", "provider.txt"), []byte(evidence), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendRule(queue, RuleRow{Kind: CaseProvider, Condition: "unexpected-server-error", Verdict: "RETRY"}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "card.md")
	var stdout, stderr bytes.Buffer
	exit := Triage(TriageInput{
		Case:   CaseProvider,
		Queue:  queue,
		Out:    out,
		Ref:    "card-601",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if exit != 0 {
		t.Fatalf("Triage exited %d: stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}

	if !strings.HasPrefix(stdout.String(), "TRIAGE OK case=provider ref=card-601 route="+TriageRoute) {
		t.Errorf("stdout = %q", stdout.String())
	}

	card, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(card), "NATIVE PROVIDER label=card-601") {
		t.Errorf("card missing refusal evidence line:\n%s", card)
	}
	if !strings.Contains(string(card), "unexpected-server-error") {
		t.Errorf("card missing candidate rule row:\n%s", card)
	}
}
