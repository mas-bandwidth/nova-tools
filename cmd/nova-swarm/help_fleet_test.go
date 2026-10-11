package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// memberFlags is member -h's flag lines, by flag name.
func memberFlags(t *testing.T) map[string]string {
	t.Helper()
	help := swarmHelp(t, "member", "-h")
	at := strings.Index(help, "\nflags:\n")
	require.GreaterOrEqual(t, at, 0, help)
	lines := map[string]string{}
	for _, l := range strings.Split(help[at+len("\nflags:\n"):], "\n") {
		if !strings.HasPrefix(l, "  --") {
			break
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(l, "  --"), " ")
		lines[name] = l
	}
	require.NotEmpty(t, lines, help)
	return lines
}

// Every flag of member says what it is for, and the banner's member usage line names
// every flag the verb takes, so a newcomer reads each one from the help alone.
func TestEveryMemberFlagIsDescribedAndInTheUsageLine(t *testing.T) {
	t.Parallel()
	var usageLine string
	for _, l := range strings.Split(usage, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "nova-swarm member ") {
			usageLine = l
			break
		}
	}
	require.NotEmpty(t, usageLine, "the banner has a member usage line")
	for name, line := range memberFlags(t) {
		// `--name <kind>  description`, or `--name  description` for a boolean
		_, desc, ok := strings.Cut(strings.TrimPrefix(line, "  --"+name), "  ")
		assert.True(t, ok && strings.TrimSpace(desc) != "", "member -h's --%s has no description: %q", name, line)
		named := strings.Contains(usageLine, "--"+name+" ") || strings.Contains(usageLine, "--"+name+"]")
		assert.True(t, named, "the member usage line does not name --%s", name)
	}
}

// The member example is a line the verb's own flags take: given -h after it, every
// flag before parses and the help is printed (exit 0), never a usage refusal.
func TestTheMemberExampleParses(t *testing.T) {
	t.Parallel()
	words := strings.Fields(verbExamples["member"])
	require.Greater(t, len(words), 2)
	require.Equal(t, []string{"nova-swarm", "member"}, words[:2])
	var out, errb bytes.Buffer
	code := swarmRun(append(words[1:], "-h"), &out, &errb)
	assert.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "usage: nova-swarm member")
}

// The card template agrees with the no-push-steps rule: no STEP runs a push or gh, since
// inside a job the shim records a push and the finish is JOB.md's (pkg/cardcontract).
func TestTheCardTemplateHasNoPushStep(t *testing.T) {
	t.Parallel()
	card := swarmHelp(t, "template", "--name", "card")
	for _, f := range swarm.LintCardBase([]byte(card), swarm.BaseCheck{}) {
		assert.NotEqual(t, "no-push-steps", f.Check, "the card template has a push step: %d: %s", f.Line, f.Excerpt)
	}
}

// An empty card is said once, as empty: by the child rules (what nova-sprint add holds a
// brief to) and by lint --card, never as every rule it fails.
func TestAnEmptyCardIsSaidOnce(t *testing.T) {
	t.Parallel()
	got := swarm.LintCardChildWith([]byte(" \n\t\n"), swarm.DefaultChildRules)
	require.Len(t, got, 1)
	assert.Equal(t, swarm.EmptyCardCheck, got[0].Check)
	assert.Equal(t, swarm.EmptyCardRemedy, swarm.ChildRemedy(swarm.DefaultChildRules, got[0].Check))

	path := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	var out, errb bytes.Buffer
	code := swarmRun([]string{"lint", "--card", path, "--child-rules"}, &out, &errb)
	assert.Equal(t, 1, code, errb.String())
	assert.Equal(t, 1, strings.Count(out.String(), "LINT DRIFT"), out.String())
	assert.Contains(t, out.String(), "LINT DRIFT card=empty.md empty: 1: the card is empty remedy=")
}

// lint's help says which of its rules nova-sprint add holds a brief to.
func TestLintHelpSaysWhichRulesAddHolds(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "lint", "-h")
	assert.Contains(t, help, "nova-sprint add holds a brief to the --child-rules tokens only")
	assert.Contains(t, help, "nova-sprint add holds a brief to the rule-<name> and step-<what> tokens")
}
