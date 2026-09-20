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
//
// Structured provider error markers ([PROVIDER_ERROR]) or bounded error lines emitted
// specifically by the provider runner or client are required to prevent model transcript
// prose (e.g. "reviewed lines 429 through 500", "test case for HTTP 500", or discussions
// of UnknownError) from spoofing a provider error verdict.

// ProviderFailure represents a classified provider-side failure.
type ProviderFailure struct {
	Why string // unexpected-server-error | provider-5xx | rate-limit | unknown-error
	Ref string // provider ref (e.g. err_29c29bd4), or empty
}

var (
	// ANSI escape code stripper
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

	// Provider refs in logs: ref=err_... or "ref": "err_..."
	providerRefKeyValRE = regexp.MustCompile(`(?i)\bref\s*=\s*([A-Za-z0-9_.:/-]+)`)
	providerRefJSONRE   = regexp.MustCompile(`(?i)"ref"\s*:\s*"([A-Za-z0-9_.:/-]+)"`)

	// Structured UnknownError in JSON payload
	providerJSONUnknownRE = regexp.MustCompile(`(?i)"name"\s*:\s*"UnknownError"`)

	// Specific rate limit error patterns on runner error lines
	providerRateLimitLineRE = regexp.MustCompile(`(?i)\b(http\s+429|429\s+too\s+many\s+requests|rate\s+limit\s+reached|quota\s+exceeded|input\s+token\s+limit\s+exceeded|code\s*=\s*resourceexhausted)\b`)

	// Specific server error patterns on runner error lines
	providerServerErrLineRE = regexp.MustCompile(`(?i)\b(unexpected\s+server\s+error|internal\s+server\s+error)\b`)

	// Specific HTTP 5xx error patterns on runner error lines
	provider5xxLineRE = regexp.MustCompile(`(?i)\b(502\s+bad\s+gateway|503\s+service\s+unavailable|504\s+gateway\s+timeout|\b529\b|returned\s+(?:500|502|503|504|529)|answered\s+(?:500|502|503|504|529)|http\s+(?:500|502|503|504|529))\b`)
)

// ClassifyProviderFailure inspects captured child output for provider-side errors.
// It requires structured error markers ([PROVIDER_ERROR]), JSON UnknownError payloads,
// or bounded stderr / runner error lines, ensuring transcript prose does not spoof verdicts.
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

	// 1. Structured JSON UnknownError payload (e.g. from SDK or gateway)
	if providerJSONUnknownRE.MatchString(s) {
		if providerServerErrLineRE.MatchString(s) {
			return ProviderFailure{Why: "unexpected-server-error", Ref: ref}, true
		}
		return ProviderFailure{Why: "unknown-error", Ref: ref}, true
	}

	// 2. Line-by-line check for structured runner error lines or [PROVIDER_ERROR] markers
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		clean := strings.TrimSpace(ansiRE.ReplaceAllString(l, ""))
		if clean == "" {
			continue
		}

		// Structured provider error marker
		if strings.HasPrefix(clean, "[PROVIDER_ERROR]") || strings.HasPrefix(clean, "PROVIDER_ERROR:") {
			lineRef := ref
			if m := providerRefKeyValRE.FindStringSubmatch(clean); len(m) > 1 {
				lineRef = strings.TrimSpace(m[1])
			}
			lower := strings.ToLower(clean)
			switch {
			case strings.Contains(lower, "rate-limit") || strings.Contains(lower, "429") || strings.Contains(lower, "quota"):
				return ProviderFailure{Why: "rate-limit", Ref: lineRef}, true
			case strings.Contains(lower, "unknown"):
				return ProviderFailure{Why: "unknown-error", Ref: lineRef}, true
			case strings.Contains(lower, "502") || strings.Contains(lower, "503") || strings.Contains(lower, "504") || strings.Contains(lower, "529") || strings.Contains(lower, "500"):
				return ProviderFailure{Why: "provider-5xx", Ref: lineRef}, true
			default:
				return ProviderFailure{Why: "unexpected-server-error", Ref: lineRef}, true
			}
		}

		// Runner error prefixes: error:, fake harness:, rpc error:, unexpected server error, etc.
		if isRunnerErrorLine(clean) {
			lineRef := ref
			if m := providerRefKeyValRE.FindStringSubmatch(clean); len(m) > 1 {
				lineRef = strings.TrimSpace(m[1])
			}

			// Rate limit
			if providerRateLimitLineRE.MatchString(clean) {
				return ProviderFailure{Why: "rate-limit", Ref: lineRef}, true
			}

			// Server error (unexpected / internal)
			if providerServerErrLineRE.MatchString(clean) {
				return ProviderFailure{Why: "unexpected-server-error", Ref: lineRef}, true
			}

			// 5xx error
			if provider5xxLineRE.MatchString(clean) {
				return ProviderFailure{Why: "provider-5xx", Ref: lineRef}, true
			}
		}
	}

	return ProviderFailure{}, false
}

func isRunnerErrorLine(line string) bool {
	lower := strings.ToLower(line)
	prefixes := []string{
		"error:",
		"fake harness:",
		"rpc error:",
		"unexpected server error",
		"internal server error",
		"the upstream",
		"upstream service",
		"provider is busy",
		"provider error",
		"http/",
		"status code:",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

