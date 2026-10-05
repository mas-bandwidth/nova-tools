package friend

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultLimitRest is the default duration a friend is down when a limit or
// out-of-credits message gives no reset time (--limit-rest, 1h).
const DefaultLimitRest = time.Hour

// The limit kinds: a usage/rate limit, or an out-of-credits condition.
const (
	LimitKindLimit   = "limit"
	LimitKindCredits = "credits"
)

// LimitInfo is what a harness error parser extracts: the kind (limit or credits),
// the reset time until when the friend is down, and the reason text.
type LimitInfo struct {
	Harness string
	Kind    string // LimitKindLimit ("limit") or LimitKindCredits ("credits")
	Until   time.Time
	Reason  string
}

// UsageLimit is a Deliverer's answer when a turn fails because the harness hit its
// usage limit or ran out of credits. The daemon marks the friend down until the reset.
type UsageLimit struct {
	Harness string
	Session string
	Kind    string
	Until   time.Time
	Reason  string
}

func (u UsageLimit) Error() string {
	return fmt.Sprintf("the harness %s is limited (%s) until %s: %s", u.Harness, u.Kind, u.Until.Format(time.RFC3339), u.Reason)
}

// Parser is one harness's parser of its usage-limit and out-of-credits messages:
// the 429 and 402 bodies and the harness's own wording. A text not recognised
// returns found=false and stays an ordinary failure.
type Parser func(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool)

// Parsers are the registered parsers, one per harness.
var Parsers = map[string]Parser{
	"claude":      parseClaude,
	"codex":       parseCodex,
	"opencode":    parseOpenCode,
	"grok":        parseGrok,
	"antigravity": parseAntigravity,
	"dsh":         parseDSH,
	"gemini":      parseGemini,
}

// ParseLimit parses the output of a failed turn in harness at now, answering
// the limit info and whether the output was recognised as a usage limit or
// out-of-credits condition. If no reset time is in the text, it answers now + defaultRest.
// If defaultRest <= 0, DefaultLimitRest (1h) is used.
// ParseUsageLimit is an alias for ParseLimit.
var ParseUsageLimit = ParseLimit

func ParseLimit(harness, out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	parser, ok := Parsers[harness]
	if !ok {
		return LimitInfo{}, false
	}
	if defaultRest <= 0 {
		defaultRest = DefaultLimitRest
	}
	if now.IsZero() {
		now = time.Now()
	}
	return parser(out, now, defaultRest)
}

var (
	limitEpoch   = regexp.MustCompile(`(?i)(?:limit reached\||"resetsAt"\s*:\s*|resets?_at\s*=\s*)(\d{10})\b`)
	limitRFC3339 = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))\b`)
	limitAt      = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)?\s*(?:at\s+)?(\d{1,2})(?::(\d{2}))?(?::(\d{2}))?\s*([ap]\.?m\.?)\b`)
	limitAt24    = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)?\s+at\s+(\d{1,2}):(\d{2})(?::(\d{2}))?\b`)
	limitAfter   = regexp.MustCompile(`(?i)(?:(?:refresh(?:es)?|resets?|try again|available again|retry|wait)?\s*(?:in|after|for)\s+|wait\s+)(\d{1,4})\s*(hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
)

func extractResetTime(out string, now time.Time, defaultRest time.Duration) time.Time {
	// 1. Epoch seconds
	if m := limitEpoch.FindStringSubmatch(out); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			t := time.Unix(sec, 0).In(now.Location())
			if t.After(now) {
				return t
			}
		}
	}
	// 2. RFC3339 timestamp
	if m := limitRFC3339.FindStringSubmatch(out); m != nil {
		if t, err := time.Parse(time.RFC3339, m[1]); err == nil {
			if t.After(now) {
				return t
			}
		}
	}
	// 3. Relative duration ("in 2 hours", "in 15 minutes", "in 20m")
	if m := limitAfter.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		unitStr := strings.ToLower(m[2])
		var unit time.Duration
		switch {
		case strings.HasPrefix(unitStr, "h"):
			unit = time.Hour
		case strings.HasPrefix(unitStr, "m"):
			unit = time.Minute
		case strings.HasPrefix(unitStr, "s"):
			unit = time.Second
		}
		if unit > 0 && n > 0 {
			return now.Add(time.Duration(n) * unit)
		}
	}
	// 4. Clock time with AM/PM ("6:52 PM", "at 11pm", "try again at 4:00 PM")
	if m := limitAt.FindStringSubmatch(out); m != nil {
		hour, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		sec := 0
		if m[3] != "" {
			sec, _ = strconv.Atoi(m[3])
		}
		ampm := strings.ToLower(strings.ReplaceAll(m[4], ".", ""))
		if ampm == "pm" && hour < 12 {
			hour += 12
		} else if ampm == "am" && hour == 12 {
			hour = 0
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t
	}
	// 5. 24-hour clock time ("at 14:30:00")
	if m := limitAt24.FindStringSubmatch(out); m != nil {
		hour, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		sec := 0
		if m[3] != "" {
			sec, _ = strconv.Atoi(m[3])
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t
	}
	// 6. Default fallback
	return now.Add(defaultRest)
}

func limitReason(out string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(stripANSI(line))
		if trimmed != "" {
			return oneLine(trimmed, 200)
		}
	}
	return oneLine(out, 200)
}

// 1. Claude
var (
	claudeCredits = regexp.MustCompile(`(?i)(?:credit balance is too low|insufficient credits|out of credits|402 payment required|402\b|"insufficient_credits")`)
	claudeLimit   = regexp.MustCompile(`(?i)(?:rate_limit_error|"rate_limit_event"|usage limit|limit reached|hit your (?:[a-z-]+ )?limit|daily rate limit|429\b|too many requests)`)
)

func parseClaude(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if claudeCredits.MatchString(out) {
		return LimitInfo{Harness: "claude", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if claudeLimit.MatchString(out) {
		return LimitInfo{Harness: "claude", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}

// 2. Codex
var (
	codexCredits = regexp.MustCompile(`(?i)(?:insufficient_quota|exceeded your current quota|billing details|out of credits|402 payment required|402\b)`)
	codexLimit   = regexp.MustCompile(`(?i)(?:rate_limit_exceeded|rate_limit_error|usage limit|rate limit reached|rate limit|quota exceeded|429\b|too many requests)`)
)

func parseCodex(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if codexCredits.MatchString(out) {
		return LimitInfo{Harness: "codex", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if codexLimit.MatchString(out) {
		return LimitInfo{Harness: "codex", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}

// 3. OpenCode
var (
	opencodeCredits = regexp.MustCompile(`(?i)(?:insufficient [a-z ]{0,10}credits|out of (?:ai )?credits|credits? (?:will refresh|exhausted|ran out)|402 payment required|402\b)`)
	opencodeLimit   = regexp.MustCompile(`(?i)(?:rate limit exceeded|rate_limit_error|usage limit|limit reached|hit your limit|429\b|too many requests)`)
)

func parseOpenCode(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if opencodeCredits.MatchString(out) {
		return LimitInfo{Harness: "opencode", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if opencodeLimit.MatchString(out) {
		return LimitInfo{Harness: "opencode", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}

// 4. Grok
var (
	grokCredits = regexp.MustCompile(`(?i)(?:credits exhausted|out of (?:ai )?credits|insufficient credits|"code"\s*:\s*402|402\b)`)
	grokLimit   = regexp.MustCompile(`(?i)(?:rate limit reached|query limit|rate_limit_error|"code"\s*:\s*429|429\b|too many requests)`)
)

func parseGrok(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if grokCredits.MatchString(out) {
		return LimitInfo{Harness: "grok", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if grokLimit.MatchString(out) {
		return LimitInfo{Harness: "grok", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}

// 5. Antigravity
var (
	antigravityCredits = regexp.MustCompile(`(?i)(?:run out of credits|billing account disabled|out of credits|insufficient credits|402 payment required|402\b)`)
	antigravityLimit   = regexp.MustCompile(`(?i)(?:resource has been exhausted|resource_exhausted|quota exceeded|usage limit reached|rate limit exceeded|429\b|too many requests)`)
)

func parseAntigravity(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if antigravityCredits.MatchString(out) {
		return LimitInfo{Harness: "antigravity", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if antigravityLimit.MatchString(out) {
		return LimitInfo{Harness: "antigravity", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}

// 6. DSH
var (
	dshCredits = regexp.MustCompile(`(?i)(?:insufficient balance|insufficient_balance|out of credits|please recharge|402 payment required|402\b)`)
	dshLimit   = regexp.MustCompile(`(?i)(?:rate limit reached|rate_limit_reached|rate_limit_error|429\b|too many requests)`)
)

func parseDSH(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if dshCredits.MatchString(out) {
		return LimitInfo{Harness: "dsh", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if dshLimit.MatchString(out) {
		return LimitInfo{Harness: "dsh", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}

// 7. Gemini
var (
	geminiCredits = regexp.MustCompile(`(?i)(?:prepaid credits exhausted|run out of gemini api credits|out of credits|insufficient credits|402 payment required|402\b)`)
	geminiLimit   = regexp.MustCompile(`(?i)(?:resource_exhausted|quota exceeded|usage limit reached|rate limit exceeded|429\b|too many requests)`)
)

func parseGemini(out string, now time.Time, defaultRest time.Duration) (LimitInfo, bool) {
	if geminiCredits.MatchString(out) {
		return LimitInfo{Harness: "gemini", Kind: LimitKindCredits, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	if geminiLimit.MatchString(out) {
		return LimitInfo{Harness: "gemini", Kind: LimitKindLimit, Until: extractResetTime(out, now, defaultRest), Reason: limitReason(out)}, true
	}
	return LimitInfo{}, false
}
