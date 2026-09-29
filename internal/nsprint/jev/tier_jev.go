package jev

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// TierJev is the classification of a card into an answer tier and work type
// (nova-tools #4316), written to the card at cut as the TIER-JEV header line.
type TierJev struct {
	Tier       string  // "frontier", "pro", "flash"
	Type       string  // "fixture", "test-fix", "verb", "lua", "refactor", "spec", "docs", "read"
	Confidence float64 // 0.00 to 1.00
	Why        string
}

// Format returns the line value: "tier=<tier> type=<type> conf=%.2f why=<why>".
func (tj TierJev) Format() string {
	return fmt.Sprintf("tier=%s type=%s conf=%.2f why=%s", tj.Tier, tj.Type, tj.Confidence, tj.Why)
}

// String returns the full header line: "TIER-JEV tier=<tier> type=<type> conf=%.2f why=<why>".
func (tj TierJev) String() string {
	return "TIER-JEV " + tj.Format()
}

var (
	explicitTypeRx  = regexp.MustCompile(`(?m)^\s*[-*]*\s*(?:TYPE|type):\s*([a-z-]+)`)
	explicitRouteRx = regexp.MustCompile(`(?m)^\s*[-*]*\s*(?:ROUTE|route|TIER|tier):\s*([a-z]+)`)
)

// ParseTierJev parses a TIER-JEV line, whether formatted as "TIER-JEV: ...",
// "TIER-JEV ...", or "tier=... type=... conf=... why=...". It validates that
// Tier is one of Tiers and Type is one of TypeWorkType's options.
func ParseTierJev(line string) (TierJev, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "TIER-JEV:")
	line = strings.TrimPrefix(line, "TIER-JEV")
	line = strings.TrimSpace(line)
	if line == "" {
		return TierJev{}, false
	}
	var tj TierJev
	var why string
	if idx := strings.Index(line, "why="); idx != -1 {
		why = strings.TrimSpace(line[idx+4:])
		line = strings.TrimSpace(line[:idx])
	}
	for _, f := range strings.Fields(line) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "tier":
			tj.Tier = strings.ToLower(strings.TrimSpace(v))
		case "type":
			tj.Type = strings.ToLower(strings.TrimSpace(v))
		case "conf":
			if c, err := strconv.ParseFloat(v, 64); err == nil {
				tj.Confidence = c
			}
		}
	}
	tj.Why = why
	if !IsTier(tj.Tier) {
		return TierJev{}, false
	}
	p, ok := PromptFor(TypeWorkType)
	if !ok || !p.Has(tj.Type) {
		return TierJev{}, false
	}
	return tj, true
}

func splitPaths(paths string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(paths, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func distinctPackages(paths []string) int {
	pkgs := map[string]bool{}
	for _, p := range paths {
		dir := path.Dir(p)
		if dir == "." || dir == "" {
			dir = p
		}
		pkgs[dir] = true
	}
	return len(pkgs)
}

// ClassifyCut classifies a card into a work type and answer tier from its title,
// PATHS, DONE-WHEN, and issue text.
func ClassifyCut(title, pathsStr, doneWhen, body string) TierJev {
	titleLower := strings.ToLower(title)
	doneLower := strings.ToLower(doneWhen)
	bodyLower := strings.ToLower(body)
	pathsList := splitPaths(pathsStr)

	// 1. Check for explicit TYPE and ROUTE/TIER declarations in the issue body.
	var declaredType, declaredTier string
	if m := explicitTypeRx.FindStringSubmatch(body); m != nil {
		t := strings.ToLower(strings.TrimSpace(m[1]))
		if p, ok := PromptFor(TypeWorkType); ok && p.Has(t) {
			declaredType = t
		}
	}
	if m := explicitRouteRx.FindStringSubmatch(body); m != nil {
		r := strings.ToLower(strings.TrimSpace(m[1]))
		if IsTier(r) {
			declaredTier = r
		}
	}

	// 2. Path-based signals.
	hasPaths := len(pathsList) > 0
	allDocs := hasPaths
	allFixtures := hasPaths
	hasLua := false
	hasSpec := false
	hasCmd := false

	for _, p := range pathsList {
		pLower := strings.ToLower(p)
		if strings.Contains(pLower, "spec-") || (strings.HasPrefix(pLower, "docs/") && strings.Contains(pLower, "spec")) {
			hasSpec = true
		}
		if strings.HasPrefix(pLower, "docs/") || strings.HasSuffix(pLower, ".md") {
			if strings.Contains(pLower, "spec") {
				allDocs = false
			}
		} else {
			allDocs = false
		}
		if strings.Contains(pLower, "testdata/") || strings.HasSuffix(pLower, "_fixture.go") ||
			strings.HasSuffix(pLower, "fixture_test.go") {
			// fixture-related
		} else {
			allFixtures = false
		}
		if strings.HasSuffix(pLower, ".lua") || strings.Contains(pLower, "fn/lua") {
			hasLua = true
		}
		if strings.HasPrefix(pLower, "cmd/") || strings.Contains(pLower, "/cmd/") {
			hasCmd = true
		}
	}

	numPkgs := distinctPackages(pathsList)

	// Determine type and tier heuristics.
	var resType, resTier, why string
	var conf float64

	switch {
	case allDocs || strings.HasPrefix(titleLower, "docs:") || strings.HasPrefix(titleLower, "doc:"):
		resType = "docs"
		resTier = "flash"
		conf = 0.95
		why = "documentation only"

	case hasSpec || strings.HasPrefix(titleLower, "spec:") || strings.Contains(titleLower, "specification"):
		resType = "spec"
		resTier = "frontier"
		conf = 0.90
		why = "specification across packages"

	case allFixtures:
		resType = "fixture"
		resTier = "flash"
		conf = 0.90
		why = "test fixture or test data only"

	case hasLua || strings.Contains(titleLower, "lua") || strings.Contains(bodyLower, "redis function"):
		resType = "lua"
		resTier = "pro"
		conf = 0.90
		why = "Redis Function (Lua) and caller"

	case strings.HasPrefix(titleLower, "read:") || strings.HasPrefix(titleLower, "audit:") ||
		strings.HasPrefix(titleLower, "judge:"):
		resType = "read"
		resTier = "flash"
		conf = 0.85
		why = "read and judge existing work"

	case strings.Contains(titleLower, "flaky") || strings.Contains(titleLower, "flake") ||
		strings.Contains(titleLower, "failing test") || strings.Contains(titleLower, "repair test") ||
		strings.Contains(doneLower, "passes 100 times") || strings.Contains(titleLower, "fix test"):
		resType = "test-fix"
		if len(pathsList) <= 1 && !hasCmd {
			resTier = "flash"
		} else {
			resTier = "pro"
		}
		conf = 0.85
		why = "failing or flaky test repaired"

	case numPkgs >= 3 || strings.HasPrefix(titleLower, "refactor:") || strings.Contains(titleLower, "across packages"):
		resType = "refactor"
		resTier = "frontier"
		conf = 0.85
		why = "change across several packages"

	case hasCmd || strings.HasPrefix(titleLower, "verb:") || strings.Contains(titleLower, "flag"):
		resType = "verb"
		resTier = "pro"
		conf = 0.85
		why = "command-line verb or flag with tests"

	default:
		// Fallbacks based on size and title
		if strings.Contains(titleLower, "fix") {
			resType = "test-fix"
			if len(pathsList) <= 1 {
				resTier = "flash"
			} else {
				resTier = "pro"
			}
			conf = 0.75
			why = "fix with tests in package"
		} else {
			resType = "verb"
			resTier = "pro"
			conf = 0.70
			why = "production change with tests"
		}
	}

	// Apply explicit declarations if present.
	if declaredType != "" {
		resType = declaredType
		conf = 0.95
		why = "card declared TYPE: " + declaredType
	}
	if declaredTier != "" {
		resTier = declaredTier
		conf = 0.95
		if declaredType != "" {
			why = "card declared TYPE: " + declaredType + " and ROUTE/TIER: " + declaredTier
		} else {
			why = "card declared ROUTE/TIER: " + declaredTier
		}
	}

	return TierJev{
		Tier:       resTier,
		Type:       resType,
		Confidence: conf,
		Why:        why,
	}
}
