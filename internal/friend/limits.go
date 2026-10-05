package friend

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Each harness's own words for a usage limit and an empty balance, read
// behind Limits (limit.go), so there is one limit machine
// (docs/SPEC-FRIEND.md, limits-mean-down-w.w2; the owner, 2026-10-04: "out of
// credits = down"). ReadLimit reads a limit line only with its reset beside
// it; a failed turn whose tail is a harness's 429 or 402 body, or its own
// wording, is a limit too, until the reset the text names, else for the rest
// (--limit-rest). A text no parser knows stays an ordinary failure. The
// machine is tla/Friend.tla's limited: NoTurnWhileLimited,
// LimitEndsOnlyByAnAnswer, LimitedEnds.

// The kinds of limit: a usage or rate limit, or an empty balance.
const (
	LimitKindLimit   = "limit"
	LimitKindCredits = "credits"
)

// HarnessLimit is what a harness's failed turn said of its limit: the kind,
// until when it is down, and the line that says so.
type HarnessLimit struct {
	Kind   string
	Until  time.Time
	Reason string
}

// harnessWords is one harness's credits and limit lines.
type harnessWords struct{ credits, limit *regexp.Regexp }

// An HTTP status as a body or a harness prints it, never a bare number (a
// line 402, an issue 429).
const (
	status402 = `(?:HTTP\s+402|status[: ]\s*402|"code"\s*:\s*402|402\s+Payment\s+Required|402\s+Prepaid)`
	status429 = `(?:HTTP\s+429|status[: ]\s*429|"code"\s*:\s*429|429\s+Too\s+Many\s+Requests|Error\s+429|429\s+RESOURCE_EXHAUSTED|429\s+Quota)`
)

func words(credits, limit string) harnessWords {
	return harnessWords{regexp.MustCompile(`(?i)(?:` + credits + `|` + status402 + `)`), regexp.MustCompile(`(?i)(?:` + limit + `|` + status429 + `)`)}
}

// harnessLimitWords is each harness's parser; the fixtures are
// testdata/<harness>.txt, a credits line and a limit line each.
var harnessLimitWords = map[string]harnessWords{
	"claude":      words(`credit balance is too low|insufficient credits|out of credits|"insufficient_credits"`, `rate_limit_error|usage limit|limit reached|hit your (?:[a-z-]+ )?limit|daily rate limit|too many requests`),
	"codex":       words(`insufficient_quota|exceeded your current quota|out of credits`, `rate_limit_exceeded|rate_limit_error|usage limit|rate limit reached|quota exceeded|too many requests`),
	"opencode":    words(`insufficient [a-z ]{0,10}credits|out of (?:ai )?credits|credits? (?:exhausted|ran out)`, `rate limit exceeded|rate_limit_error|usage limit|limit reached|hit your limit|too many requests`),
	"grok":        words(`credits exhausted|out of (?:ai )?credits|insufficient credits`, `rate limit reached|query limit|rate_limit_error|too many requests`),
	"antigravity": words(`run out of credits|billing account disabled|out of credits|insufficient credits`, `resource has been exhausted|resource_exhausted|quota exceeded|usage limit reached|rate limit exceeded|too many requests`),
	"dsh":         words(`insufficient[ _]balance|out of credits|please recharge`, `rate limit reached|rate_limit_reached|rate_limit_error|too many requests`),
	"gemini":      words(`prepaid credits exhausted|run out of gemini api credits|out of credits|insufficient credits`, `resource_exhausted|quota exceeded|usage limit reached|rate limit exceeded|too many requests`),
}

// creditsWords is a reason that says the balance is empty, whichever reader
// found it (LimitKind).
var creditsWords = regexp.MustCompile(`(?i)credit|balance|billing|` + status402)

// LimitKind is the kind of a limit by its reason: credits when it speaks of
// the balance, else limit.
func LimitKind(reason string) string {
	if creditsWords.MatchString(reason) {
		return LimitKindCredits
	}
	return LimitKindLimit
}

var (
	resetRFC3339 = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))`)
	resetAt24    = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)\s+at\s+(\d{1,2}):(\d{2})(?::(\d{2}))?\b`)
	resetWait    = regexp.MustCompile(`(?i)(?:in|after|wait)\s+(\d{1,4})\s*(hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
)

// ReadHarnessLimit reads a failed turn's output from harness at now: a limit
// ReadLimit finds, else a line in the tail in the harness's own words, down
// until the reset it names (as ReadLimit reads one; an RFC3339 time; a
// 24-hour clock; "in", "after" or "wait" N units), else for rest
// (DefaultLimitWait when not positive). found is false for a text no parser
// knows, and for a harness with none.
func ReadHarnessLimit(harness, out string, now time.Time, rest time.Duration) (HarnessLimit, bool) {
	if lim, found := ReadLimit(out, now); found && lim.Limited {
		return HarnessLimit{Kind: LimitKind(lim.Reason), Until: lim.Until, Reason: lim.Reason}, true
	}
	w, ok := harnessLimitWords[harness]
	if !ok {
		return HarnessLimit{}, false
	}
	if rest <= 0 {
		rest = DefaultLimitWait
	}
	tail := out
	if len(tail) > LimitTail {
		tail = tail[len(tail)-LimitTail:]
	}
	var hl HarnessLimit
	found := false
	for _, line := range strings.Split(tail, "\n") {
		kind := ""
		switch {
		case w.credits.MatchString(line):
			kind = LimitKindCredits
		case w.limit.MatchString(line):
			kind = LimitKindLimit
		default:
			continue
		}
		until, ok := harnessReset(line, now)
		if !ok || !until.After(now) {
			until = now.Add(rest)
		}
		hl, found = HarnessLimit{Kind: kind, Until: until, Reason: harness + " " + kind + ": " + oneLine(strings.TrimSpace(line), 200)}, true
	}
	return hl, found
}

// harnessReset is the reset a harness's line names.
func harnessReset(line string, now time.Time) (time.Time, bool) {
	if t, ok := resetOf(line, now); ok {
		return t, true
	}
	if m := resetRFC3339.FindStringSubmatch(line); m != nil {
		if t, err := time.Parse(time.RFC3339, m[1]); err == nil {
			return t.In(now.Location()), true
		}
	}
	if m := resetAt24.FindStringSubmatch(line); m != nil {
		h, _ := strconv.Atoi(m[1])    // ignored: digits always parse
		mins, _ := strconv.Atoi(m[2]) // ignored: digits always parse
		secs := 0
		if m[3] != "" {
			secs, _ = strconv.Atoi(m[3]) // ignored: digits always parse
		}
		if h > 23 || mins > 59 || secs > 59 {
			return time.Time{}, false
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), h, mins, secs, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	}
	if m := resetWait.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1]) // ignored: digits always parse
		unit := time.Second
		switch strings.ToLower(m[2])[0] {
		case 'h':
			unit = time.Hour
		case 'm':
			unit = time.Minute
		}
		return now.Add(time.Duration(n) * unit), true
	}
	return time.Time{}, false
}

// WatchHarness is Watch, and a failed command's output read in harness's own
// words besides (ReadHarnessLimit, rest when it names no reset): a match the
// limit line did not already say is a limit like any other, Down once with
// its reset, the turn Deferred by Gate, and a wake after it. Output of a
// command that answered is read by Watch alone, so a reply that talks about
// limits sends no one down.
func (l *Limits) WatchHarness(harness string, rest time.Duration, run Exec) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		out, exit, err := run(ctx, dir, name, args, stdin)
		l.see(out)
		if exit != 0 || err != nil {
			l.seeHarness(harness, out, rest)
		}
		return out, exit, err
	}
}

// seeHarness takes a limit in harness's own words that ReadLimit did not.
func (l *Limits) seeHarness(harness, out string, rest time.Duration) {
	now := l.Now()
	if _, found := ReadLimit(out, now); found {
		return // see read it: the limit line or Claude's event says the rest
	}
	hl, found := ReadHarnessLimit(harness, out, now, rest)
	if !found {
		return
	}
	l.mu.Lock()
	if l.limited && now.Before(l.until) {
		l.mu.Unlock()
		return // down already, until a reset not yet come
	}
	l.limited, l.until, l.reason, l.waking, l.answered = true, hl.Until, hl.Reason, "", false
	l.episodes++
	l.mu.Unlock()
	if l.Down != nil {
		l.Down(hl.Until, hl.Reason)
	}
}

// LimitState is the limit for the status file: limited, its kind and until
// when (session=limited kind= until=).
func (l *Limits) LimitState() (kind string, until time.Time, limited bool) {
	until, reason, limited := l.Limited()
	if !limited {
		return "", time.Time{}, false
	}
	return LimitKind(reason), until, true
}
