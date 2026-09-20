package swarm

import (
	"regexp"
	"strings"
)

// Issue #2001 & #2011: Provider infrastructure fault classification.
//
// When a harness encounters an upstream provider error (such as a 5xx server error,
// an UnknownError JSON payload from an SDK/gateway, or a 429 rate limit / quota exhaustion),
// the run is classified as a PROVIDER failure rather than INCOMPLETE or card bug.

// ProviderFailure represents a classified provider-side failure.
type ProviderFailure struct {
	Why string // unexpected-server-error | provider-5xx | rate-limit | unknown-error
	Ref string // provider ref (e.g. err_29c29bd4), or empty
}

var (
	// Rate limit patterns: 429 status, rate limit, quota exceeded, resource exhausted
	providerRateLimitRE = regexp.MustCompile(`(?i)\b(429)\b|rate limit|quota exceeded|resource exhausted|too many requests|resource_exhausted`)

	// Unexpected / internal server error
	providerServerErrorRE = regexp.MustCompile(`(?i)unexpected server error|internal server error`)

	// UnknownError JSON or string
	providerUnknownErrorRE = regexp.MustCompile(`(?i)"name"\s*:\s*"UnknownError"|\bUnknownError\b`)

	// HTTP 5xx codes
	provider5xxRE = regexp.MustCompile(`\b(500|502|503|504|529)\b`)

	// Provider refs in logs: ref=err_... or "ref": "err_..."
	providerRefKeyValRE = regexp.MustCompile(`(?i)\bref\s*=\s*([A-Za-z0-9_.:/-]+)`)
	providerRefJSONRE   = regexp.MustCompile(`(?i)"ref"\s*:\s*"([A-Za-z0-9_.:/-]+)"`)
)

// ClassifyProviderFailure inspects captured child output for provider-side errors.
func ClassifyProviderFailure(raw []byte) (ProviderFailure, bool) {
	if len(raw) == 0 {
		return ProviderFailure{}, false
	}
	s := string(raw)

	// Extract ref if present
	ref := ""
	if m := providerRefJSONRE.FindStringSubmatch(s); len(m) > 1 {
		ref = strings.TrimSpace(m[1])
	} else if m := providerRefKeyValRE.FindStringSubmatch(s); len(m) > 1 {
		ref = strings.TrimSpace(m[1])
	}

	// 1. Rate limit / 429
	if providerRateLimitRE.Match(raw) {
		return ProviderFailure{Why: "rate-limit", Ref: ref}, true
	}

	// 2. Server error (unexpected / internal)
	if providerServerErrorRE.Match(raw) {
		return ProviderFailure{Why: "unexpected-server-error", Ref: ref}, true
	}

	// 3. UnknownError
	if providerUnknownErrorRE.Match(raw) {
		return ProviderFailure{Why: "unknown-error", Ref: ref}, true
	}

	// 4. HTTP 5xx
	if provider5xxRE.Match(raw) {
		return ProviderFailure{Why: "provider-5xx", Ref: ref}, true
	}

	return ProviderFailure{}, false
}
