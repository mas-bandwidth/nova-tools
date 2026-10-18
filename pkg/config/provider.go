package config

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// envNameRE is a provider key's variable name: what key_env may hold. The value is never
// stored, only the name, so a row with a literal key in key_env is refused.
var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func checkProvider(r Row) error {
	var problems []string
	date := r.Fields["balance_as_of"]
	if r.Fields["balance_usd"] != "" && date == "" {
		problems = append(problems, "--balance_usd needs --balance_as_of YYYY-MM-DD")
	}
	if date != "" {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			problems = append(problems, fmt.Sprintf("--balance_as_of %q wants YYYY-MM-DD", date))
		}
	}
	if raw := r.Fields["console_url"]; raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			problems = append(problems, "--console_url wants an https:// console URL without credentials, or empty")
		}
	}
	if raw := r.Fields["base_url"]; raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			problems = append(problems, "--base_url wants an http:// or https:// API base without credentials, or empty")
		}
	}
	if raw := r.Fields["key_env"]; raw != "" && !envNameRE.MatchString(raw) {
		problems = append(problems, "--key_env wants the NAME of an environment variable ([A-Z_][A-Z0-9_]*), never its value, or empty")
	}
	if raw := r.Fields["npm"]; raw != "" && strings.ContainsFunc(raw, func(c rune) bool { return c == ' ' || c == '\t' || c == '\n' }) {
		problems = append(problems, "--npm wants one npm package name with no blank, or empty")
	}
	if len(problems) != 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// ProviderReport shows declared funding and coverage of enabled routes by the
// deal's tier arrays. Empty or absent arrays take every enabled route of that
// tier. No state is changed; warnings are advisory, including tiers with no
// enabled route of a funded provider.
//
// At 100 providers and 10,000 routes (two orders beyond a small fleet), the rows
// occupy roughly 20 MB and fit one process; the store rows are the only truth.
// Libraries considered: slices sorting and strings.Join in the standard library
// cover the grouping and deterministic rendering; adopted modules add no simpler
// projection for these three small tables.
func ProviderReport(providers, routes, tiers []Row) (lines, warns []string) {
	arrays := map[string]string{}
	selected := map[string]map[string]bool{}
	for _, t := range tiers {
		arrays[t.Name] = t.Fields["routes"]
		if arrays[t.Name] != "" {
			selected[t.Name] = map[string]bool{}
			for _, name := range strings.Split(arrays[t.Name], ",") {
				selected[t.Name][name] = true
			}
		}
	}
	funded := map[string]bool{}
	for _, p := range providers {
		funded[p.Name] = p.Fields["funded"] == "true"
	}
	byProvider := map[string][]Row{}
	fundedInTier := map[string]bool{}
	for _, r := range routes {
		if r.Fields["enabled"] != "true" {
			continue
		}
		byProvider[r.Fields["provider"]] = append(byProvider[r.Fields["provider"]], r)
		tier := r.Fields["tier"]
		if funded[r.Fields["provider"]] && (arrays[tier] == "" || selected[tier][r.Name]) {
			fundedInTier[tier] = true
		}
	}
	ordered := slices.Clone(providers)
	slices.SortFunc(ordered, func(a, b Row) int { return strings.Compare(a.Name, b.Name) })
	for _, p := range ordered {
		line := fmt.Sprintf("PROVIDER name=%s funded=%s billing=%s balance_usd=%s as_of=%s", p.Name, Value(p.Fields["funded"]), Value(p.Fields["billing"]), Value(p.Fields["balance_usd"]), Value(p.Fields["balance_as_of"]))
		total, inside := map[string]int{}, map[string]int{}
		missing := map[string][]string{}
		for _, r := range byProvider[p.Name] {
			tier := r.Fields["tier"]
			total[tier]++
			if arrays[tier] == "" || selected[tier][r.Name] {
				inside[tier]++
			} else {
				missing[tier] = append(missing[tier], r.Name)
			}
		}
		for _, tier := range Tiers {
			if total[tier] != 0 {
				line += fmt.Sprintf(" %s=%d/%d", tier, inside[tier], total[tier])
			}
		}
		if p.Fields["note"] != "" {
			line += " note=" + Value(p.Fields["note"])
		}
		lines = append(lines, line)
		if p.Fields["funded"] != "true" {
			continue
		}
		for _, tier := range Tiers {
			names := missing[tier]
			if len(names) == 0 {
				continue
			}
			slices.Sort(names)
			warns = append(warns, fmt.Sprintf("provider=%s funded=true routes-in-no-tier-array=%s; run: nova-config tier set %s --routes %s,%s", p.Name, strings.Join(names, ","), tier, arrays[tier], strings.Join(names, ",")))
		}
	}
	orderedTiers := slices.Clone(tiers)
	slices.SortFunc(orderedTiers, func(a, b Row) int { return strings.Compare(a.Name, b.Name) })
	for _, t := range orderedTiers {
		if !fundedInTier[t.Name] {
			warns = append(warns, fmt.Sprintf("tier=%s holds no route of a funded provider; run: nova-config tier show %s", t.Name, t.Name))
		}
	}
	return lines, warns
}

// ProviderNamesError lists every route whose legacy provider word cannot name
// a provider row. No store or ledger changes before this refusal is returned.
// The correction uses the previous binary, whose route provider is still text.
type ProviderNamesError struct{ Routes []Row }

func (e *ProviderNamesError) Error() string { return e.Remediation("", "<name>") }

// Remediation retains the selected store and actor in each previous-build
// correction command. again already holds the shell-quoted store flags, and
// actor is a shell-quoted identity or the explicit <name> placeholder.
func (e *ProviderNamesError) Remediation(again, actor string) string {
	parts := []string{"upgrade refuses non-conforming provider words; before upgrading, use the previous nova-config build matching this store's schema (the previous/schema40 nova-config build for a schema40 store) for these corrections"}
	for _, r := range e.Routes {
		parts = append(parts, fmt.Sprintf("route %s has --provider %q (provider rows require %s); run with the previous build: nova-config route set %s --provider <new> --actor %s%s", r.Name, r.Fields["provider"], NamePattern.String(), r.Name, actor, again))
	}
	return strings.Join(parts, "; ")
}

// CheckProviderNames is the shared upgrade preflight. It uses the existing
// row-name regexp and slices sorting; no renaming or funding inference occurs.
func CheckProviderNames(routes []Row) error {
	var invalid []Row
	for _, r := range routes {
		if !NamePattern.MatchString(r.Fields["provider"]) {
			invalid = append(invalid, r.Clone())
		}
	}
	if len(invalid) == 0 {
		return nil
	}
	slices.SortFunc(invalid, func(a, b Row) int { return strings.Compare(a.Name, b.Name) })
	return &ProviderNamesError{Routes: invalid}
}
