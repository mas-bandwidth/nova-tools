package sprint

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// A friend's usage source (docs/SPEC-CONFIG.md, friend, usage; docs/SPEC-SPRINT.md section
// 1, sub-pacingb.w1): where the sprint reads a limit from in the text a friend's run
// leaves (her REPORT.md, which friend sync reads; a harness's own readout when her row
// names one). One interface, one reader per word of config.FriendUsages, so a later
// harness adds one reader and nothing else changes. The reader is generic across
// harnesses: it reads words, never a process. What it reads feeds the pace record
// (sub_pacing.go, FriendPace.limit) and nothing else; the daemon's own limit parsing
// (internal/friend, limits-mean-down) stays the daemon's, and reaches the sprint as her
// health's down with its until.

// LimitRead is what a reader found in a text: a limit was hit, until when (zero when the
// text names no reset: the friend is down until her daemon says otherwise), and the
// words.
type LimitRead struct {
	Limited bool
	Until   time.Time
	Reason  string
}

// UsageReader reads a run's text for its limit; ok is false when the text says nothing
// of one.
type UsageReader interface {
	ReadUsage(text string, now time.Time) (LimitRead, bool)
}

// UsageReaderOf is the reader of a usage source word (config.FriendUsages): the limit
// messages reader for limit-messages and for a word it does not know (the default).
func UsageReaderOf(source string) UsageReader {
	switch source {
	case config.FriendUsageClaudeStatus:
		return claudeStatusReader{}
	case config.FriendUsageCodexLimit:
		return codexLimitReader{}
	}
	return limitMessagesReader{}
}

// ReadUsage reads the text with the reader of the source.
func ReadUsage(source, text string, now time.Time) (LimitRead, bool) {
	return UsageReaderOf(source).ReadUsage(text, now)
}

var (
	// limitWords are the words any harness says at its limit, the daemon's list
	// (internal/friend/limit.go) read the same way here.
	limitWords = regexp.MustCompile(`(?i)insufficient [a-z ]{0,20}credits|out of (?:ai )?credits|credits? (?:exhausted|ran out)|usage limit|limit reached|hit your (?:[a-z-]+ )?limit|quota (?:exceeded|exhausted)|rate.limited|usage limited`)
	// resetAt is a reset named as a time: "resets at 2026-10-05T18:00:00Z", "until <RFC3339>".
	resetAt = regexp.MustCompile(`(?i)(?:reset(?:s)?(?: at)?|until|back at)[:\s]+(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?(?:Z|[+-]\d{2}:\d{2})?)`)
	// resetIn is a reset named as a wait: "resets in 2h 15m", "try again in 45 minutes".
	resetIn  = regexp.MustCompile(`(?i)(?:reset(?:s)?|again|back|retry)\s+in\s+((?:\d+\s*(?:h|hr|hours?|m|min|minutes?|s|sec|seconds?)\s*)+)`)
	waitPart = regexp.MustCompile(`(?i)(\d+)\s*(h|hr|hours?|m|min|minutes?|s|sec|seconds?)`)
)

// limitMessagesReader is the generic reader: the limit words, and a reset said as a
// time or as a wait.
type limitMessagesReader struct{}

func (limitMessagesReader) ReadUsage(text string, now time.Time) (LimitRead, bool) {
	return readLimitWords(text, now)
}

// readLimitWords is the generic read: ok when the text holds a limit word; the reset is
// the first time or wait named after it, when one is.
func readLimitWords(text string, now time.Time) (LimitRead, bool) {
	loc := limitWords.FindStringIndex(text)
	if loc == nil {
		return LimitRead{}, false
	}
	r := LimitRead{Limited: true, Reason: strings.TrimSpace(limitFirstLine(text[loc[0]:]))}
	r.Until = resetOf(text[loc[0]:], now)
	return r, true
}

// resetOf is the reset a text names after a limit: a time, else now plus a wait; zero
// for none.
func resetOf(text string, now time.Time) time.Time {
	if m := resetAt.FindStringSubmatch(text); m != nil {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
			if t, err := time.Parse(layout, m[1]); err == nil {
				return t.UTC()
			}
		}
	}
	if m := resetIn.FindStringSubmatch(text); m != nil {
		var d time.Duration
		for _, p := range waitPart.FindAllStringSubmatch(m[1], -1) {
			n, _ := strconv.Atoi(p[1])
			switch strings.ToLower(p[2])[0] {
			case 'h':
				d += time.Duration(n) * time.Hour
			case 'm':
				d += time.Duration(n) * time.Minute
			default:
				d += time.Duration(n) * time.Second
			}
		}
		if d > 0 {
			return now.Add(d).UTC()
		}
	}
	return time.Time{}
}

func limitFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// claudeStatusReader reads the Claude /status readout: a window line at 100% is the
// limit, its "resets" the until; else the generic words.
type claudeStatusReader struct{}

var claudeFull = regexp.MustCompile(`(?i)(?:current session|5.?h(?:our)?|weekly|all models)[^\n]*?\b100%[^\n]*`)

func (claudeStatusReader) ReadUsage(text string, now time.Time) (LimitRead, bool) {
	if m := claudeFull.FindString(text); m != "" {
		return LimitRead{Limited: true, Until: resetOf(m, now), Reason: strings.TrimSpace(m)}, true
	}
	return readLimitWords(text, now)
}

// codexLimitReader reads a Codex usage-limit notice: "You've hit your usage limit ...
// try again in 2h 10m" or a reset time; else the generic words.
type codexLimitReader struct{}

var codexLimit = regexp.MustCompile(`(?i)(?:you(?:'ve| have) hit your usage limit|usage limit reached)[^\n]*`)

func (codexLimitReader) ReadUsage(text string, now time.Time) (LimitRead, bool) {
	if m := codexLimit.FindString(text); m != "" {
		return LimitRead{Limited: true, Until: resetOf(m, now), Reason: strings.TrimSpace(m)}, true
	}
	return readLimitWords(text, now)
}
