package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

// localProviderPrefix marks a provider that serves a local engine on one
// machine (docs/SPEC-LOCAL.md, Fleet; docs/SPEC-CONFIG.md,
// local-config-provider-endpoint).
const localProviderPrefix = "local-"

// localTailnet is the address range a tailnet hands its machines, the same
// range as cmd/nova-local/host.go and pkg/bus/addr.go (docs/SPEC-LOCAL.md, rule 3).
var localTailnet = netip.MustParsePrefix("100.64.0.0/10")

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
	problems = append(problems, providerEndpointProblems(r)...)
	if len(problems) != 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// providerEndpointProblems is the row check of a local provider's endpoint
// (docs/SPEC-LOCAL.md, rule 3; docs/SPEC-CONFIG.md, local-config-provider-endpoint).
// A name is not resolved and no socket is opened: a literal IP is loopback or
// the tailnet, and a name waits for the store check.
func providerEndpointProblems(r Row) []string {
	endpoint := r.Fields["endpoint"]
	local := strings.HasPrefix(r.Name, localProviderPrefix)
	switch {
	case local && endpoint == "":
		return []string{fmt.Sprintf("--endpoint is required on a provider named %s<host>; run: nova-config provider set %s --endpoint http://<host>:11434/v1", localProviderPrefix, r.Name)}
	case !local && endpoint != "":
		return []string{fmt.Sprintf("--endpoint is empty on every provider not named %s<host>; run: nova-config provider set %s --endpoint ''", localProviderPrefix, r.Name)}
	case endpoint == "":
		return nil
	}
	if why := endpointShapeProblem(endpoint); why != "" {
		return []string{why + fmt.Sprintf("; run: nova-config provider set %s --endpoint http://<host>:11434/v1", r.Name)}
	}
	return nil
}

// endpointShapeProblem is why raw is not an engine base URL, "" when the
// shape is accepted. A name host is accepted here; the store check names the
// machine. A literal IP host is loopback or the tailnet, and is not looked up.
func endpointShapeProblem(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "http") || u.Hostname() == "" || u.Path != "/v1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Sprintf("--endpoint %q wants an http URL with a host and the path /v1, like http://<host>:11434/v1", raw)
	}
	host := u.Hostname()
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || localTailnet.Contains(ip) {
		return ""
	}
	return fmt.Sprintf("--endpoint host %s is neither loopback nor the tailnet (100.64.0.0/10)", ip)
}

// endpointNameHost is the host of an accepted endpoint when it is a name, ""
// when the endpoint is empty or the host is a literal IP (already checked).
func endpointNameHost(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	host := u.Hostname()
	if host == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	return host, true
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
