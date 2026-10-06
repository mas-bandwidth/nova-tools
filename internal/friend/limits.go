package friend

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// What a harness says when it is at its usage limit or out of credits, one
// parser per harness (docs/SPEC-FRIEND.md, limits-mean-down-w-r3.w1~15: a
// usage limit or an empty balance marks the friend down until the reset).
// The texts are in testdata/limits.tsv, one line each; a text not recognised
// is an ordinary failed turn.

// The kinds of a limit: the harness's usage window is spent, or the balance
// that pays for it is empty.
const (
	KindLimit   = "limit"
	KindCredits = "credits"
)

// SessionLimited is the status file's session while the harness is down at
// its limit: `session=limited kind=<k> until=<RFC3339>`.
const SessionLimited = "limited"

// LimitHit is a limit read from a failed turn's output: its kind, the reset
// (the text's own when Named, else now plus the rest), and the line.
type LimitHit struct {
	Kind   string
	Until  time.Time
	Named  bool
	Reason string
}

type limitWord struct {
	kind string
	re   *regexp.Regexp
}

func words(kind, pattern string) limitWord {
	return limitWord{kind: kind, re: regexp.MustCompile(`(?i)` + pattern)}
}

// harnessLimits is each harness's own wording, the 429 and 402 bodies and what
// the harness prints itself, credits first: a line that says both is the
// balance. A provider's transient rate_limit_error is in none of them.
var harnessLimits = map[string][]limitWord{
	"claude": {
		words(KindCredits, `credit balance is too low|"type"\s*:\s*"(?:billing_error|payment_required)"|\b402\b.{0,40}payment required`),
		words(KindLimit, `hit your (?:[a-z-]+ )?limit|usage limit reached|(?:5-hour|five-hour|weekly|session|opus|sonnet) limit reached|reached your specified api usage limits`),
	},
	"codex": {
		words(KindCredits, `insufficient_quota|exceeded your current quota|out of credits`),
		words(KindLimit, `hit your usage limit|usage_limit_reached`),
	},
	"opencode": {
		words(KindCredits, `insufficient balance|insufficient credits|"type"\s*:\s*"CreditsError"|no payment method|payment required`),
		words(KindLimit, `FreeUsageLimitError|usage limit (?:reached|exceeded)|reached your (?:[a-z]+ )?usage limit`),
	},
	"grok": {
		words(KindCredits, `used all available credits|out of credits|insufficient (?:credits|balance)|purchase more credits`),
		words(KindLimit, `usage limit (?:reached|exceeded)|reached your (?:[a-z]+ )?usage limit`),
	},
	"antigravity": {
		words(KindCredits, `insufficient (?:ai )?credits|out of (?:ai )?credits|credits? (?:exhausted|ran out)`),
		words(KindLimit, `exhausted your capacity|quota will reset after|usage limit (?:reached|exceeded)|quota (?:exceeded|exhausted)`),
	},
	"dsh": {
		words(KindCredits, `insufficient balance|\b402\b.{0,40}payment required`),
		words(KindLimit, `usage limit (?:reached|exceeded)|quota (?:exceeded|exhausted)`),
	},
	"gemini": {
		words(KindCredits, `prepayment credits are depleted|credits? (?:depleted|exhausted)`),
		words(KindLimit, `exceeded your current quota|quota (?:exceeded|exhausted)|usage limit (?:reached|exceeded)`),
	},
}

var (
	resetEpochAt  = regexp.MustCompile(`"resets_at"\s*:\s*(\d{10})\b`)
	resetSeconds  = regexp.MustCompile(`"resets_in_seconds"\s*:\s*(\d+)`)
	resetStamp    = regexp.MustCompile(`(?i)(?:reset|resets|until|retry|regain access)\w*\s+(?:on\s+|at\s+)?(\d{4}-\d\d-\d\dT[0-9:.]+(?:Z|[+-]\d\d:\d\d))`)
	resetDateAt   = regexp.MustCompile(`(?i)regain access on (\d{4}-\d\d-\d\d) at (\d\d:\d\d) UTC`)
	resetDuration = regexp.MustCompile(`(?i)(?:reset|resets|retry|try again|refresh|available again)\w*\s+(?:after|in)\s+((?:\d+\s*(?:days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)[\s,]*(?:and\s+)?)+)`)
	durationPart  = regexp.MustCompile(`(?i)(\d+)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)`)
	durationUnits = map[byte]time.Duration{'d': 24 * time.Hour, 'h': time.Hour, 'm': time.Minute, 's': time.Second}
)

// ParseLimit reads the tail of a failed turn's output (the last LimitTail
// bytes: a harness says it last) for harness's own wording of a usage limit
// or an empty balance: the kind, and the reset when the line names one, else
// now plus rest (DefaultLimitWait when rest is zero). A harness it has no
// words for, or a text it does not recognise, is not a limit. A reset that
// is not after now is no reset. The newest matching line wins.
func ParseLimit(harness, out string, now time.Time, rest time.Duration) (LimitHit, bool) {
	ws := harnessLimits[harness]
	if len(ws) == 0 {
		return LimitHit{}, false
	}
	if rest <= 0 {
		rest = DefaultLimitWait
	}
	tail := out
	if len(tail) > LimitTail {
		tail = tail[len(tail)-LimitTail:]
	}
	var hit LimitHit
	var found bool
	for _, line := range strings.Split(tail, "\n") {
		line = stripANSI(line)
		for _, w := range ws {
			if !w.re.MatchString(line) {
				continue
			}
			until, named := resetOfText(line, now)
			if !named {
				until = now.Add(rest)
			}
			hit, found = LimitHit{Kind: w.kind, Until: until, Named: named, Reason: oneLine(line, 200)}, true
			break
		}
	}
	return hit, found
}

// resetOfText is the reset a line names, after now: an epoch or seconds in a
// 429 body, a stamp, a date and a time in UTC, a duration ("after 3h12m5s",
// "in 2 days 3 hours"), or a clock time (resetOf).
func resetOfText(line string, now time.Time) (time.Time, bool) {
	var until time.Time
	switch {
	case resetEpochAt.MatchString(line):
		n, _ := strconv.ParseInt(resetEpochAt.FindStringSubmatch(line)[1], 10, 64) // ignored: ten digits always parse
		until = time.Unix(n, 0).In(now.Location())
	case resetSeconds.MatchString(line):
		n, _ := strconv.ParseInt(resetSeconds.FindStringSubmatch(line)[1], 10, 64) // ignored: digits always parse
		until = now.Add(time.Duration(n) * time.Second)
	case resetDateAt.MatchString(line):
		m := resetDateAt.FindStringSubmatch(line)
		until, _ = time.Parse("2006-01-02 15:04", m[1]+" "+m[2]) // ignored: the pattern is the layout; a zero time is no reset
	case resetStamp.MatchString(line):
		until, _ = time.Parse(time.RFC3339Nano, resetStamp.FindStringSubmatch(line)[1]) // ignored: a text that is no time is no reset
	case resetDuration.MatchString(line):
		var d time.Duration
		for _, p := range durationPart.FindAllStringSubmatch(resetDuration.FindStringSubmatch(line)[1], -1) {
			n, _ := strconv.Atoi(p[1]) // ignored: digits always parse
			d += time.Duration(n) * durationUnits[strings.ToLower(p[2])[0]]
		}
		until = now.Add(d)
	default:
		until, _ = resetOf(line, now)
	}
	return until, until.After(now)
}
