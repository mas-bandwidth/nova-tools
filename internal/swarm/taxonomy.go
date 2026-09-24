package swarm

import (
	"fmt"
	"regexp"
	"strings"
)

// Failure taxonomy (Sprint Row 10, #2061).
//
// The taxonomy distinguishes four fundamental categories of failure:
//  1. Transient provider errors: external rate limits (HTTP 429), 5xx server errors,
//     connection resets, temporary gateway outages. These are retriable with jittered
//     backoff and do NOT quarantine the card until provider attempts are exhausted.
//  2. Infrastructure crashes: host resource exhaustion (OOM / exit 137), SSH dropped,
//     bench unreachable, disk full, Darwin exit 255 without verdict, runner segfault.
//     The card is valid, but the environment crashed.
//  3. Test failures: execution completed, code was generated, but test suites or
//     assertions failed (exit code != 0 from test command). Normal failure path,
//     eligible for automated fix card (red lane).
//  4. Unrecoverable card defects: prompt exceeds context window (input-limit), malformed
//     card syntax / frontmatter, invalid schema, negative or missing budget. Retrying
//     will always fail; these must be quarantined immediately to avoid wasting compute.

// FailureKind is the high-level category of failure under the taxonomy.
type FailureKind string

const (
	// FailureTransientProvider indicates a temporary provider error (429, 5xx, timeout).
	FailureTransientProvider FailureKind = "transient-provider"

	// FailureInfraCrash indicates an infrastructure or environment crash.
	FailureInfraCrash FailureKind = "infra-crash"

	// FailureTestFailure indicates legitimate test failures where code ran but assertions failed.
	FailureTestFailure FailureKind = "test-failure"

	// FailureCardDefect indicates unrecoverable flaws in the card specification itself.
	FailureCardDefect FailureKind = "card-defect"
)

// FailureClassification is the complete classification decision for a failure.
type FailureClassification struct {
	Kind          FailureKind `json:"kind"`
	Reason        string      `json:"reason"`        // Specific reason token, e.g. "rate-limit", "provider-5xx", "input-limit", "malformed", "oom", "test-failed"
	Retriable     bool        `json:"retriable"`     // Whether the failure may be retried automatically
	Quarantinable bool        `json:"quarantinable"` // Whether the failure should quarantine the card when retry budget is exhausted or unrecoverable
	Details       string      `json:"details"`       // Summary or excerpt of the failure
}

// Regular expressions for classifying failure patterns.
var (
	// Provider transient rate limits.
	reRateLimit = regexp.MustCompile(`(?i)(?:rate[_\s-]?limit|\b429\b|too many requests|resource_exhausted|quota[_\s-]?exceeded|tpm exceeded|rpm exceeded)`)

	// Provider transient server errors (5xx, unavailable, gateway timeout).
	reProvider5xx = regexp.MustCompile(`(?i)(?:unexpected server error|internal server error|service unavailable|bad gateway|gateway timeout|\b50[0234]\b|\bhttp 5\d\d\b|err_[0-9a-f]{8}|upstream connect error)`)

	// Provider transient network / transport issues.
	reProviderNetwork = regexp.MustCompile(`(?i)(?:connection reset by peer|tls handshake timeout|broken pipe|stream error: stream id|connection refused.*(?:api|provider|openai|anthropic|deepseek|google)|temporary failure in name resolution)`)

	// Infrastructure crash patterns: OOM, segfault, unreachable host, disk full, Darwin 255.
	reOOM              = regexp.MustCompile(`(?i)(?:out of memory|oomkilled|\bkilled\b.*(?:process|task)|exit code 137)`)
	reSegfault         = regexp.MustCompile(`(?i)(?:segmentation fault|segfault|sigsegv|exit code 139)`)
	reBenchUnreachable = regexp.MustCompile(`(?i)(?:bench unreachable|bench-unreachable|ssh: handshake failed|host key verification failed|connection timed out.*port 22)`)
	reDiskFull         = regexp.MustCompile(`(?i)(?:no space left on device|disk quota exceeded|write error: no space)`)

	// Card defect patterns: prompt size, malformed yaml/frontmatter, syntax errors.
	reInputLimit = regexp.MustCompile(`(?i)(?:input token limit exceeded|maximum context length|context length exceeded|prompt is too long|tokens? exceeds? context window|too many input tokens)`)
	reMalformed  = regexp.MustCompile(`(?i)(?:malformed card|invalid frontmatter|unparseable yaml|missing required field|card syntax error)`)

	// Test failure patterns.
	reTestFailure = regexp.MustCompile(`(?i)(?:--- FAIL:|FAIL\s+\S+|assertion failed|tests? failed|failed \d+ of \d+ tests?|test suite failed)`)
)

// ClassifyFailure inspects error, output log tail, return code, and end state to
// assign a failure classification.
func ClassifyFailure(err error, output string, rc int, end string) FailureClassification {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	combined := strings.TrimSpace(errStr + "\n" + output)

	// 1. Unrecoverable Card Defects: checked first because an input-limit or
	// malformed card must never be retried even if provider words appear.
	if end == EndInputLimit || reInputLimit.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureCardDefect,
			Reason:        "input-limit",
			Retriable:     false,
			Quarantinable: true,
			Details:       firstMatchingLine(combined, reInputLimit, "prompt exceeded model context limit"),
		}
	}
	if end == ClassMalformed || reMalformed.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureCardDefect,
			Reason:        "malformed-syntax",
			Retriable:     false,
			Quarantinable: true,
			Details:       firstMatchingLine(combined, reMalformed, "card syntax or frontmatter is malformed"),
		}
	}

	// 2. Transient Provider Errors: 429 rate limits, 5xx server errors, network resets.
	if rc == 429 || reRateLimit.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureTransientProvider,
			Reason:        "rate-limit",
			Retriable:     true,
			Quarantinable: false,
			Details:       firstMatchingLine(combined, reRateLimit, "provider rate limit (HTTP 429)"),
		}
	}
	if end == EndProvider || reProvider5xx.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureTransientProvider,
			Reason:        "provider-5xx",
			Retriable:     true,
			Quarantinable: false,
			Details:       firstMatchingLine(combined, reProvider5xx, "provider server error (HTTP 5xx)"),
		}
	}
	if reProviderNetwork.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureTransientProvider,
			Reason:        "provider-network",
			Retriable:     true,
			Quarantinable: false,
			Details:       firstMatchingLine(combined, reProviderNetwork, "provider network/transport failure"),
		}
	}

	// 3. Infrastructure Crashes: OOM, segfault, bench unreachable, Darwin 255 without verdict.
	if rc == 137 || reOOM.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureInfraCrash,
			Reason:        "oom-killed",
			Retriable:     true,
			Quarantinable: true,
			Details:       firstMatchingLine(combined, reOOM, "process killed by OOM killer (exit 137)"),
		}
	}
	if rc == 139 || reSegfault.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureInfraCrash,
			Reason:        "segfault",
			Retriable:     true,
			Quarantinable: true,
			Details:       firstMatchingLine(combined, reSegfault, "runner segmentation fault (exit 139)"),
		}
	}
	if reBenchUnreachable.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureInfraCrash,
			Reason:        "bench-unreachable",
			Retriable:     true,
			Quarantinable: false,
			Details:       firstMatchingLine(combined, reBenchUnreachable, "bench host is unreachable"),
		}
	}
	if reDiskFull.MatchString(combined) {
		return FailureClassification{
			Kind:          FailureInfraCrash,
			Reason:        "disk-full",
			Retriable:     false,
			Quarantinable: true,
			Details:       firstMatchingLine(combined, reDiskFull, "bench disk space exhausted"),
		}
	}
	if rc == 255 && !reTestFailure.MatchString(combined) {
		// Darwin exit 255 without verdict (issue #2058).
		return FailureClassification{
			Kind:          FailureInfraCrash,
			Reason:        "darwin-exit-255",
			Retriable:     true,
			Quarantinable: false,
			Details:       "process exited 255 without verdict",
		}
	}

	// 4. Test Failures: code executed and test runner returned non-zero.
	if reTestFailure.MatchString(combined) || (rc != 0 && rc != -1 && end == EndFailed) {
		return FailureClassification{
			Kind:          FailureTestFailure,
			Reason:        "test-failed",
			Retriable:     false,
			Quarantinable: false,
			Details:       firstMatchingLine(combined, reTestFailure, fmt.Sprintf("test suite failed with exit code %d", rc)),
		}
	}

	// Default fallback: if non-zero exit code or explicit error, treat as generic failure.
	reason := "unknown"
	if end != "" {
		reason = end
	}
	fallbackDetails := firstNonEmptyLine(strings.Split(combined, "\n"))
	if fallbackDetails == "" {
		fallbackDetails = "execution ended with failure"
	}
	return FailureClassification{
		Kind:          FailureTestFailure,
		Reason:        reason,
		Retriable:     false,
		Quarantinable: false,
		Details:       fallbackDetails,
	}
}

// firstMatchingLine extracts the first line matching re, or returns fallback.
func firstMatchingLine(s string, re *regexp.Regexp, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if re.MatchString(trimmed) {
			return trimmed
		}
	}
	return fallback
}
