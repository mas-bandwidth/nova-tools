package docs

import (
	"os"
	"strings"
	"testing"
)

// TestNovaTestVerbsFromProposal247 pins the second slice of the nova-test
// proposal (nova-tools #561, from proposal #247): the test and CI rules of
// 2026-09-15 as five verbs -- fast, slow, ci, runners, local -- with the
// replays that make each rule red when it is broken. The issue supersedes the
// proposal text of #247 where the two differ, so docs/SPEC-TEST.md must carry
// this section; a missing verb or replay is a bug.
func TestNovaTestVerbsFromProposal247(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-TEST.md")
	if err != nil {
		t.Fatalf("docs/SPEC-TEST.md: %v", err)
	}
	content := string(body)

	for _, want := range []string{
		"`fast`",
		"`slow`",
		"`ci`",
		"`runners`",
		"`local`",
		"docs/TEST-DURATIONS.md",
		"fail-fast off",
		"fork guard",
		"concurrency group",
		"self-hosted",
		"ci-ok",
		"registration token",
		"core pinning",
		"#516",
		"ci-on-main-only-self-hosted-parallel",
		"slow CI is a crawl forever",
		"fast-fails-on-budget-breach",
		"slow-runs-only-with-tag",
		"ci-matrix-matches-package-list",
		"ci-prs-never-use-hosted-runners",
		"ci-fork-guard-present",
		"runners-register-with-pinning",
		"local-runs-fast-suite",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-TEST.md missing %q (nova-tools #561)", want)
		}
	}
}
