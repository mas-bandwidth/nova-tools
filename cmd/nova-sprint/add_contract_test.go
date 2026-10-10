package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// add holds a brief by reference as its lane reads it, the contract of its version in place
// of its Contract: line (docs/SPEC-CARD-CONTRACT.md section 7), with no contract option: under
// the default rules, and under the held rules the member injects (cardRules, rules by
// reference), the brief the generator writes by default is admitted, and stored as it was
// given; a version this build does not hold is the finding contract-version.
func TestAddAdmitsABriefByReferenceAsItsLaneReadsIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	remote, sha := twinRemote(t, ta, map[string]string{
		"internal/x/x.go":      "package x\n",
		"internal/x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n",
	}, "sprint/s1")
	c := cardgen.Card{ID: "contract-ref", File: "internal/x/x.go", Paths: []string{"internal/x/*.go", "internal/x/*_test.go", "internal/x"}, Test: "internal/x TestY",
		Tier: "pro", Wave: 1, Kind: "fix-red", Task: "Fix x."}
	brief := cardgen.Render(cardgen.Header{Repo: "mas-bandwidth/nova-tools", Base: "dev", Sha: sha}, c)
	require.True(t, strings.HasSuffix(brief, "\n"+cardgen.ContractLine()+"\n"))

	held, err := swarm.HeldRules(swarm.DefaultRulesName)
	require.NoError(t, err)
	for name, rs := range map[string]ruleSet{
		"the default rules":           {rules: swarm.DefaultChildRules},
		"the held rules by reference": {rules: held, held: swarm.DefaultRulesName},
	} {
		why, findings := lintBriefReads(brief, rs)
		assert.Empty(t, why, name)
		assert.Empty(t, findings, name)
		_, findings = lintBriefReads(strings.Replace(brief, " "+cardgen.ContractVersion+"\n", " v0\n", 1), rs)
		var checks []string
		for _, f := range findings {
			checks = append(checks, f.Check)
		}
		assert.Contains(t, checks, "contract-version", name)
	}

	// the add itself, at a base it reads (the brief checks at the base, section 11)
	brief = cardgen.Render(cardgen.Header{Repo: remote, Base: "sprint/s1", Sha: sha}, c)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, c.ID+".md"), []byte(brief), 0o600))
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	require.Contains(t, out, "ADD OK stream=s1 cards=1")
	assert.Equal(t, strings.TrimSuffix(brief, "\n"), ta.primary(c.ID).F("brief"), "the brief is stored by reference, as it was given (its one trailing newline cut)")
	ta.clean()
}
