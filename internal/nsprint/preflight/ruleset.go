package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Check 7.3: ruleset check for land preflight (#3139 rev 7 section 7.3).
//
// `dev-integrity`: `deletion` and `non_fast_forward` with no bypass for anyone,
// the lander included.
// `dev-landing`: `pull_request`, `required_status_checks`, `merge_queue` with
// bypass for the `nova-lander` App only; humans keep the PR path.
// The JSON is `fleet/land/ruleset-dev.json`.
// `land preflight` reads the rules back once and refuses to go live on a
// difference with the named remedy.
//
// Section 7.7:
// `land preflight`: read-only checks (7.3, 10.3), one GREEN or RED line each,
// exit 0 ok, 1 any RED line, 2 refused or could not run with its named
// `REFUSED <reason> remedy=<cmd>` line.

const (
	RulesetCheckID   = "7.3"
	RulesetCheckName = "ruleset"

	RefusalBypassMissing          = "dev-landing-bypass-missing"
	RefusalIntegrityBypassPresent = "dev-integrity-bypass-present"
	RefusalUnauthorizedBypass     = "dev-landing-unauthorized-bypass"
	RefusalIntegrityMissing       = "dev-integrity-missing"
	RefusalLandingMissing         = "dev-landing-missing"
	RefusalNotActive              = "ruleset-not-active"
	RefusalRefMissing             = "ruleset-ref-missing"
	RefusalRuleMissing            = "ruleset-rule-missing"

	RemedyApplyG3 = "apply G3 (fleet/land/ruleset-dev.json)"
)

// Ruleset is one GitHub repository ruleset as returned by GitHub's REST API
// and declared in fleet/land/ruleset-<base>.json.
type Ruleset struct {
	ID           int64         `json:"id,omitempty"`
	Name         string        `json:"name"`
	Target       string        `json:"target,omitempty"`
	SourceType   string        `json:"source_type,omitempty"`
	Source       string        `json:"source,omitempty"`
	Enforcement  string        `json:"enforcement"`
	Conditions   Conditions    `json:"conditions,omitempty"`
	Rules        []Rule        `json:"rules"`
	BypassActors []BypassActor `json:"bypass_actors,omitempty"`
}

// Conditions defines criteria for branches/tags covered by a ruleset.
type Conditions struct {
	RefName RefNameCondition `json:"ref_name,omitempty"`
}

// RefNameCondition carries the include/exclude branch ref patterns.
type RefNameCondition struct {
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// Rule defines an individual rule in a ruleset.
type Rule struct {
	Type       string         `json:"type"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// BypassActor specifies an actor allowed to bypass rules.
type BypassActor struct {
	ActorID    int64  `json:"actor_id,omitempty"`
	ActorType  string `json:"actor_type"`
	ActorName  string `json:"actor_name,omitempty"`
	BypassMode string `json:"bypass_mode"`
}

// ParseRulesets parses rulesets from JSON data, accepting an array of rulesets,
// an object with a "rulesets" key, or a map of ruleset names to ruleset objects.
func ParseRulesets(data []byte) ([]Ruleset, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty ruleset JSON")
	}

	if trimmed[0] == '[' {
		var list []Ruleset
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("parsing ruleset list: %w", err)
		}
		return list, nil
	}

	// Try object with "rulesets" wrapper
	var wrapper struct {
		Rulesets []Ruleset `json:"rulesets"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err == nil && len(wrapper.Rulesets) > 0 {
		return wrapper.Rulesets, nil
	}

	// Try map[string]Ruleset
	var m map[string]Ruleset
	if err := json.Unmarshal(trimmed, &m); err == nil && len(m) > 0 {
		var list []Ruleset
		for _, r := range m {
			list = append(list, r)
		}
		return list, nil
	}

	// Try single Ruleset
	var single Ruleset
	if err := json.Unmarshal(trimmed, &single); err == nil && single.Name != "" {
		return []Ruleset{single}, nil
	}

	return nil, fmt.Errorf("unrecognized ruleset JSON structure")
}

// LoadRulesetFile reads and parses a ruleset JSON file.
func LoadRulesetFile(path string) ([]Ruleset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading ruleset file %s: %w", path, err)
	}
	return ParseRulesets(data)
}

// FormatRefusal formats a named refusal line with its remedy (7.7).
func FormatRefusal(reason, remedy string) string {
	return fmt.Sprintf("REFUSED %s remedy=%q", reason, remedy)
}

// Refusal extracts the named refusal substring (REFUSED <reason> remedy=...)
// from a line if it is RED and carries one.
func (l Line) Refusal() string {
	if !l.Red {
		return ""
	}
	idx := strings.Index(l.Why, "REFUSED ")
	if idx < 0 {
		return ""
	}
	sub := l.Why[idx:]
	// If it contains a trailing detail following ": ", take only the refusal clause
	// e.g. "REFUSED reason remedy=\"cmd\": details..." -> "REFUSED reason remedy=\"cmd\""
	if colon := strings.Index(sub, "\": "); colon >= 0 {
		return sub[:colon+1]
	}
	return sub
}

// CheckRuleset audits a set of rulesets against section 7.3 requirements for
// the specified base branch (defaulting to "dev").
func CheckRuleset(rulesets []Ruleset, base string) Line {
	if base == "" {
		base = "dev"
	}
	remedyApply := fmt.Sprintf("apply G3 (fleet/land/ruleset-%s.json)", base)
	if base == "dev" {
		remedyApply = RemedyApplyG3
	}

	integrity := findRuleset(rulesets, base, "integrity")
	landing := findRuleset(rulesets, base, "landing")

	var reds []string
	primaryReason := ""
	primaryRemedy := ""

	setPrimary := func(reason, remedy string) {
		if primaryReason == "" {
			primaryReason = reason
			primaryRemedy = remedy
		}
	}

	// 1. Audit integrity ruleset:
	// `dev-integrity`: deletion and non_fast_forward with no bypass for anyone,
	// the lander included.
	if integrity == nil {
		reds = append(reds, fmt.Sprintf("%s-integrity ruleset missing", base))
		setPrimary(fmt.Sprintf("%s-integrity-missing", base), remedyApply)
	} else {
		if integrity.Enforcement != "" && !strings.EqualFold(integrity.Enforcement, "active") {
			reds = append(reds, fmt.Sprintf("%s-integrity enforcement %q, want active", base, integrity.Enforcement))
			setPrimary(RefusalNotActive, fmt.Sprintf("enable active enforcement on %s-integrity", base))
		}
		if !coversBase(integrity.Conditions, base) {
			reds = append(reds, fmt.Sprintf("%s-integrity does not cover refs/heads/%s", base, base))
			setPrimary(RefusalRefMissing, fmt.Sprintf("add refs/heads/%s to %s-integrity conditions", base, base))
		}
		if !hasRuleType(integrity.Rules, "deletion") {
			reds = append(reds, fmt.Sprintf("%s-integrity missing deletion rule", base))
			setPrimary(fmt.Sprintf("%s-rule-missing:deletion", integrity.Name), remedyApply)
		}
		if !hasRuleType(integrity.Rules, "non_fast_forward") {
			reds = append(reds, fmt.Sprintf("%s-integrity missing non_fast_forward rule", base))
			setPrimary(fmt.Sprintf("%s-rule-missing:non_fast_forward", integrity.Name), remedyApply)
		}
		if len(integrity.BypassActors) > 0 {
			var actors []string
			for _, a := range integrity.BypassActors {
				actors = append(actors, fmt.Sprintf("%s:%d", a.ActorType, a.ActorID))
			}
			reds = append(reds, fmt.Sprintf("%s-integrity has bypass actor(s) (%s); want none (no bypass for anyone)",
				base, strings.Join(actors, ", ")))
			setPrimary(fmt.Sprintf("%s-integrity-bypass-present", base),
				fmt.Sprintf("remove all bypass actors from %s-integrity (no bypass for anyone)", base))
		}
	}

	// 2. Audit landing ruleset:
	// `dev-landing`: pull_request, required_status_checks, merge_queue with
	// bypass for the nova-lander App only; humans keep the PR path.
	if landing == nil {
		reds = append(reds, fmt.Sprintf("%s-landing ruleset missing", base))
		setPrimary(fmt.Sprintf("%s-landing-missing", base), remedyApply)
	} else {
		if landing.Enforcement != "" && !strings.EqualFold(landing.Enforcement, "active") {
			reds = append(reds, fmt.Sprintf("%s-landing enforcement %q, want active", base, landing.Enforcement))
			setPrimary(RefusalNotActive, fmt.Sprintf("enable active enforcement on %s-landing", base))
		}
		if !coversBase(landing.Conditions, base) {
			reds = append(reds, fmt.Sprintf("%s-landing does not cover refs/heads/%s", base, base))
			setPrimary(RefusalRefMissing, fmt.Sprintf("add refs/heads/%s to %s-landing conditions", base, base))
		}
		if !hasRuleType(landing.Rules, "pull_request") {
			reds = append(reds, fmt.Sprintf("%s-landing missing pull_request rule", base))
			setPrimary(fmt.Sprintf("%s-rule-missing:pull_request", landing.Name), remedyApply)
		}
		if !hasRuleType(landing.Rules, "required_status_checks") {
			reds = append(reds, fmt.Sprintf("%s-landing missing required_status_checks rule", base))
			setPrimary(fmt.Sprintf("%s-rule-missing:required_status_checks", landing.Name), remedyApply)
		}
		if !hasRuleType(landing.Rules, "merge_queue") {
			reds = append(reds, fmt.Sprintf("%s-landing missing merge_queue rule", base))
			setPrimary(fmt.Sprintf("%s-rule-missing:merge_queue", landing.Name), remedyApply)
		}

		// Bypass actors: bypass for the nova-lander App only; humans keep the PR path.
		hasLanderBypass := false
		var unauthorizedBypass []string
		for _, b := range landing.BypassActors {
			if strings.EqualFold(b.ActorType, "Integration") {
				hasLanderBypass = true
			} else {
				unauthorizedBypass = append(unauthorizedBypass, fmt.Sprintf("%s:%d", b.ActorType, b.ActorID))
			}
		}

		if !hasLanderBypass {
			reds = append(reds, fmt.Sprintf("%s-landing missing nova-lander bypass (no Integration bypass actor)", base))
			setPrimary(fmt.Sprintf("%s-landing-bypass-missing", base), remedyApply)
		}
		if len(unauthorizedBypass) > 0 {
			reds = append(reds, fmt.Sprintf("%s-landing has unauthorized bypass actor(s) (%s); want nova-lander only (humans keep PR path)",
				base, strings.Join(unauthorizedBypass, ", ")))
			setPrimary(fmt.Sprintf("%s-landing-unauthorized-bypass", base),
				fmt.Sprintf("remove non-lander bypass from %s-landing (humans keep PR path)", base))
		}
	}

	if len(reds) > 0 {
		refusalClause := FormatRefusal(primaryReason, primaryRemedy)
		return Line{
			N:    RulesetCheckID,
			Name: RulesetCheckName,
			Red:  true,
			Why:  fmt.Sprintf("%s: %s", refusalClause, strings.Join(reds, "; ")),
		}
	}

	return Line{
		N:    RulesetCheckID,
		Name: RulesetCheckName,
		Red:  false,
		Why: fmt.Sprintf("%s-integrity (deletion, non_fast_forward; no bypass), %s-landing (pull_request, required_status_checks, merge_queue; bypass: nova-lander)",
			base, base),
	}
}

// CheckRulesetOutput audits rulesets and prints the check line and bare refusal to out if non-nil.
func CheckRulesetOutput(rulesets []Ruleset, base string, out io.Writer) Line {
	l := CheckRuleset(rulesets, base)
	if out != nil {
		fmt.Fprintln(out, l.String())
		if l.Red {
			if ref := l.Refusal(); ref != "" {
				fmt.Fprintln(out, ref)
			}
		}
	}
	return l
}

// RulesetReader reads rulesets for a repository.
type RulesetReader interface {
	ReadRulesets(ctx context.Context, repo string) ([]Ruleset, error)
}

// RulesetFunc is an adapter to allow the use of functions as RulesetReader.
type RulesetFunc func(ctx context.Context, repo string) ([]Ruleset, error)

// ReadRulesets calls f(ctx, repo).
func (f RulesetFunc) ReadRulesets(ctx context.Context, repo string) ([]Ruleset, error) {
	return f(ctx, repo)
}

// Preflight audits the repository's rulesets for the given base branch (7.3).
// If out is non-nil, it prints the preflight result and any named refusal.
func Preflight(ctx context.Context, reader RulesetReader, repo, base string, out io.Writer) Line {
	if base == "" {
		base = "dev"
	}
	if reader == nil {
		l := Line{
			N:    RulesetCheckID,
			Name: RulesetCheckName,
			Red:  true,
			Why:  FormatRefusal("no-reader", RemedyApplyG3) + ": ruleset reader is nil",
		}
		if out != nil {
			fmt.Fprintln(out, l.String())
			fmt.Fprintln(out, l.Refusal())
		}
		return l
	}
	rulesets, err := reader.ReadRulesets(ctx, repo)
	if err != nil {
		l := Line{
			N:    RulesetCheckID,
			Name: RulesetCheckName,
			Red:  true,
			Why:  FormatRefusal("read-failed", RemedyApplyG3) + ": " + err.Error(),
		}
		if out != nil {
			fmt.Fprintln(out, l.String())
			fmt.Fprintln(out, l.Refusal())
		}
		return l
	}
	return CheckRulesetOutput(rulesets, base, out)
}

// LandPreflight executes the land preflight check (7.3) and returns the lines and exit code.
// Exit code 0 means all lines are green; 1 means at least one line is red (7.7).
func LandPreflight(rulesets []Ruleset, base string, out io.Writer) ([]Line, int) {
	l := CheckRulesetOutput(rulesets, base, out)
	code := 0
	if l.Red {
		code = 1
	}
	return []Line{l}, code
}

func findRuleset(rulesets []Ruleset, base, kind string) *Ruleset {
	exact := base + "-" + kind
	for i := range rulesets {
		if strings.EqualFold(rulesets[i].Name, exact) {
			return &rulesets[i]
		}
	}
	for i := range rulesets {
		name := strings.ToLower(rulesets[i].Name)
		if strings.Contains(name, base) && strings.Contains(name, kind) {
			return &rulesets[i]
		}
	}
	for i := range rulesets {
		if strings.EqualFold(rulesets[i].Name, kind) {
			return &rulesets[i]
		}
	}
	return nil
}

func coversBase(conditions Conditions, base string) bool {
	if len(conditions.RefName.Include) == 0 {
		return false
	}
	ref := "refs/heads/" + base
	for _, inc := range conditions.RefName.Include {
		if inc == ref || inc == "~DEFAULT_BRANCH" || inc == base {
			return true
		}
	}
	return false
}

func hasRuleType(rules []Rule, typ string) bool {
	for _, r := range rules {
		if strings.EqualFold(r.Type, typ) {
			return true
		}
	}
	return false
}
