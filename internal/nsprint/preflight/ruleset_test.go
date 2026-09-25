package preflight

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func stubGreenRuleset() []Ruleset {
	return []Ruleset{
		{
			Name:        "dev-integrity",
			Target:      "branch",
			Enforcement: "active",
			Conditions: Conditions{
				RefName: RefNameCondition{
					Include: []string{"refs/heads/dev"},
				},
			},
			Rules: []Rule{
				{Type: "deletion"},
				{Type: "non_fast_forward"},
			},
			BypassActors: nil,
		},
		{
			Name:        "dev-landing",
			Target:      "branch",
			Enforcement: "active",
			Conditions: Conditions{
				RefName: RefNameCondition{
					Include: []string{"refs/heads/dev"},
				},
			},
			Rules: []Rule{
				{Type: "pull_request"},
				{Type: "required_status_checks"},
				{Type: "merge_queue"},
			},
			BypassActors: []BypassActor{
				{
					ActorType:  "Integration",
					ActorName:  "nova-lander",
					BypassMode: "always",
				},
			},
		},
	}
}

// TestPreflightStubRulesetWithoutBypassPrintsNamedRefusal verifies DONE-WHEN:
// preflight against a stub ruleset without the bypass prints the named refusal.
func TestPreflightStubRulesetWithoutBypassPrintsNamedRefusal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// A stub ruleset where dev-landing has NO bypass for nova-lander
	stub := []Ruleset{
		{
			Name:        "dev-integrity",
			Target:      "branch",
			Enforcement: "active",
			Conditions: Conditions{
				RefName: RefNameCondition{
					Include: []string{"refs/heads/dev"},
				},
			},
			Rules: []Rule{
				{Type: "deletion"},
				{Type: "non_fast_forward"},
			},
			BypassActors: nil,
		},
		{
			Name:        "dev-landing",
			Target:      "branch",
			Enforcement: "active",
			Conditions: Conditions{
				RefName: RefNameCondition{
					Include: []string{"refs/heads/dev"},
				},
			},
			Rules: []Rule{
				{Type: "pull_request"},
				{Type: "required_status_checks"},
				{Type: "merge_queue"},
			},
			BypassActors: []BypassActor{}, // NO bypass
		},
	}

	reader := RulesetFunc(func(ctx context.Context, repo string) ([]Ruleset, error) {
		return stub, nil
	})

	var buf bytes.Buffer
	line := Preflight(ctx, reader, "mas-bandwidth/nova-tools", "dev", &buf)
	if !line.Red {
		t.Fatalf("stub ruleset without bypass must be RED, got: %s", line)
	}

	printed := buf.String()
	wantRefusal := `REFUSED dev-landing-bypass-missing remedy="apply G3 (fleet/land/ruleset-dev.json)"`
	if !strings.Contains(printed, wantRefusal) {
		t.Fatalf("preflight output does not contain named refusal %q:\n%s", wantRefusal, printed)
	}

	if !strings.Contains(line.String(), wantRefusal) {
		t.Fatalf("line.String() %q does not contain named refusal %q", line.String(), wantRefusal)
	}
	if line.Refusal() != wantRefusal {
		t.Fatalf("line.Refusal() = %q, want %q", line.Refusal(), wantRefusal)
	}
}

// TestPreflightStubRulesetWithBypassIsGreen verifies that a properly configured
// ruleset passes preflight.
func TestPreflightStubRulesetWithBypassIsGreen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stub := stubGreenRuleset()

	reader := RulesetFunc(func(ctx context.Context, repo string) ([]Ruleset, error) {
		return stub, nil
	})

	var buf bytes.Buffer
	line := Preflight(ctx, reader, "mas-bandwidth/nova-tools", "dev", &buf)
	if line.Red {
		t.Fatalf("stub ruleset with bypass must be GREEN, got: %s", line)
	}

	if !strings.HasPrefix(line.String(), "GREEN 7.3 ruleset:") {
		t.Fatalf("unexpected line: %s", line)
	}
}

// TestPreflightDevIntegrityBypassRefuses verifies that dev-integrity with any bypass
// actor is refused.
func TestPreflightDevIntegrityBypassRefuses(t *testing.T) {
	t.Parallel()

	stub := stubGreenRuleset()
	stub[0].BypassActors = []BypassActor{
		{ActorID: 1, ActorType: "Integration", BypassMode: "always"},
	}

	var buf bytes.Buffer
	line := CheckRulesetOutput(stub, "dev", &buf)
	if !line.Red {
		t.Fatalf("dev-integrity with bypass must be RED, got %s", line)
	}

	wantRefusal := `REFUSED dev-integrity-bypass-present remedy="remove all bypass actors from dev-integrity (no bypass for anyone)"`
	if !strings.Contains(buf.String(), wantRefusal) {
		t.Fatalf("output %q does not contain %q", buf.String(), wantRefusal)
	}
}

// TestPreflightDevLandingUnauthorizedBypassRefuses verifies that human/role bypass
// on dev-landing is refused (humans keep the PR path).
func TestPreflightDevLandingUnauthorizedBypassRefuses(t *testing.T) {
	t.Parallel()

	stub := stubGreenRuleset()
	stub[1].BypassActors = append(stub[1].BypassActors, BypassActor{
		ActorID: 5, ActorType: "RepositoryRole", BypassMode: "always",
	})

	var buf bytes.Buffer
	line := CheckRulesetOutput(stub, "dev", &buf)
	if !line.Red {
		t.Fatalf("dev-landing with repo role bypass must be RED, got %s", line)
	}

	wantRefusal := `REFUSED dev-landing-unauthorized-bypass remedy="remove non-lander bypass from dev-landing (humans keep PR path)"`
	if !strings.Contains(buf.String(), wantRefusal) {
		t.Fatalf("output %q does not contain %q", buf.String(), wantRefusal)
	}
}

// TestPreflightMissingRulesRefuses verifies that missing rules are refused.
func TestPreflightMissingRulesRefuses(t *testing.T) {
	t.Parallel()

	// Missing deletion
	stub := stubGreenRuleset()
	stub[0].Rules = []Rule{{Type: "non_fast_forward"}}

	line := CheckRuleset(stub, "dev")
	if !line.Red || !strings.Contains(line.Why, "rule-missing:deletion") {
		t.Fatalf("missing deletion rule should refuse, got: %s", line)
	}

	// Missing merge_queue
	stub = stubGreenRuleset()
	stub[1].Rules = []Rule{{Type: "pull_request"}, {Type: "required_status_checks"}}

	line = CheckRuleset(stub, "dev")
	if !line.Red || !strings.Contains(line.Why, "rule-missing:merge_queue") {
		t.Fatalf("missing merge_queue rule should refuse, got: %s", line)
	}
}

// TestFleetLandRulesetDevJSON verifies that the committed fleet/land/ruleset-dev.json
// matches the specification and passes CheckRuleset.
func TestFleetLandRulesetDevJSON(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	rulesetPath := filepath.Join(root, "fleet", "land", "ruleset-dev.json")

	rulesets, err := LoadRulesetFile(rulesetPath)
	if err != nil {
		t.Fatalf("loading %s: %v", rulesetPath, err)
	}

	if len(rulesets) != 2 {
		t.Fatalf("expected 2 rulesets in %s, got %d", rulesetPath, len(rulesets))
	}

	line := CheckRuleset(rulesets, "dev")
	if line.Red {
		t.Fatalf("fleet/land/ruleset-dev.json failed preflight check: %s", line)
	}

	lines, code := LandPreflight(rulesets, "dev", nil)
	if code != 0 {
		t.Fatalf("LandPreflight exit code = %d, want 0", code)
	}
	if len(lines) != 1 || lines[0].Red {
		t.Fatalf("LandPreflight lines = %v", lines)
	}
}
