package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

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
	if len(problems) != 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// ProviderReport shows declared funding and coverage of enabled routes by
// tier. A tier's membership is its enabled routes, read from route.tier
// (docs/SPEC-CONFIG.md, "tier"), so no route is outside its tier and the
// report is a count, never an array's fraction. No state is changed;
// warnings are advisory, among them a tier with no enabled route of a funded
// provider.
//
// At 100 providers and 10,000 routes (two orders beyond a small fleet), the rows
// occupy roughly 20 MB and fit one process; the store rows are the only truth.
// Libraries considered: slices sorting and strings.Join in the standard library
// cover the grouping and deterministic rendering; adopted modules add no simpler
// projection for these three small tables.
func ProviderReport(providers, routes, tiers []Row) (lines, warns []string) {
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
		if funded[r.Fields["provider"]] {
			fundedInTier[r.Fields["tier"]] = true
		}
	}
	ordered := slices.Clone(providers)
	slices.SortFunc(ordered, func(a, b Row) int { return strings.Compare(a.Name, b.Name) })
	for _, p := range ordered {
		line := fmt.Sprintf("PROVIDER name=%s funded=%s billing=%s balance_usd=%s as_of=%s", p.Name, Value(p.Fields["funded"]), Value(p.Fields["billing"]), Value(p.Fields["balance_usd"]), Value(p.Fields["balance_as_of"]))
		counts := map[string]int{}
		for _, r := range byProvider[p.Name] {
			counts[r.Fields["tier"]]++
		}
		for _, tier := range Tiers {
			if counts[tier] != 0 {
				line += fmt.Sprintf(" %s=%d", tier, counts[tier])
			}
		}
		if p.Fields["note"] != "" {
			line += " note=" + Value(p.Fields["note"])
		}
		lines = append(lines, line)
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
