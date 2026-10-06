package friend

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A rate limit is not out of funds (docs/SPEC-FRIEND.md, one-shot lanes: a
// rate limit backs off; the finding of 2026-10-05, 10:34 AM: a friend at 24
// lanes hit his provider's input-token rate limit, the runner held him down
// as if out of funds and returned 20 cards, and the coordinator cleared the
// pause by hand and lowered him to 12). A rate limit passes in a minute: the
// lanes pause new turns for a backoff, lower their live cap by a quarter and
// raise it again by measurement, with no hold and no person. Out of funds
// does not pass: it holds the lanes, with one judgment to the coordinator.

// The lane governor's numbers.
const (
	RateBackoffFirst = 30 * time.Second // the first pause after a rate limit
	RateBackoffMax   = 10 * time.Minute // the pause doubles up to this
	RateRaiseEvery   = 10 * time.Minute // a clean stretch this long raises the cap one lane
	RateJudgeWithin  = time.Hour        // RateJudgeAfter lowerings within this are one judgment
	RateJudgeAfter   = 3
)

// RateLimited is a lane's answer when the provider rate-limited the turn (a
// 429, "rate limit reached", "too many requests", "input token limit
// exceeded"): the turn's card stays in hand, counted toward nothing, and the
// lanes back off (LaneGovernor.RateLimit).
type RateLimited struct{ Session, Reason string }

func (r RateLimited) Error() string {
	return "the provider rate-limited the turn in session " + r.Session + ": " + r.Reason
}

// OutOfFunds is a lane's answer when the provider refused for want of funds
// or credit (a 402, insufficient balance): the lanes hold until the daemon
// restarts, and the coordinator is told once (LaneGovernor.Hold).
type OutOfFunds struct{ Session, Reason string }

func (o OutOfFunds) Error() string {
	return "the provider is out of funds for session " + o.Session + ": " + o.Reason
}

var (
	rateLimitLine = regexp.MustCompile(`(?i)(?:status|code|error|http)[^\n0-9]{0,24}429\b|\b429\b[^\n0-9]{0,8}(?:too many|rate)|rate[ _-]?limit[ _-]?(?:ed|error|reached|exceeded)\b|too many requests|input[ _-]tokens?[ _-](?:per[ _-]minute[ _-])?limit[ _-](?:exceeded|reached)|tokens per min`)
	fundsLine     = regexp.MustCompile(`(?i)(?:status|code|error|http)[^\n0-9]{0,24}402\b|payment required|insufficient (?:balance|funds|[a-z ]{0,20}credits)|out of (?:funds|credits?)\b|credit balance is too low`)
)

// ProviderLimit reads the tail of a lane turn's output (the last LimitTail
// bytes: a harness says it last) for a rate limit or out of funds, out of
// funds first: OutOfFunds, RateLimited, or nil. A line with its reset beside
// it ("Insufficient AI Credits ... will refresh 6:52 PM") is the harness's
// own limit (Limits: down until the reset, then woken), never out of funds
// here.
func ProviderLimit(session, out string) error {
	tail := out
	if len(tail) > LimitTail {
		tail = tail[len(tail)-LimitTail:]
	}
	var rate string
	for _, line := range strings.Split(tail, "\n") {
		line = stripANSI(line)
		if fundsLine.MatchString(line) {
			if lim, found := ReadLimit(line, time.Time{}); found && lim.Limited {
				continue // a reset beside it: the harness's limit
			}
			return OutOfFunds{Session: session, Reason: oneLine(line, 200)}
		}
		if rate == "" && rateLimitLine.MatchString(line) {
			rate = oneLine(line, 200)
		}
	}
	if rate != "" {
		return RateLimited{Session: session, Reason: rate}
	}
	return nil
}

// LaneGovernor is the lanes' live cap under rate limits, and their hold
// when out of funds. Its zero value is the row's width, never paused.
// RateLimit lowers the cap by a quarter (at least one lane, never below
// one) and pauses new turns for the backoff, which doubles from
// RateBackoffFirst to RateBackoffMax; turns that started before the last
// lowering are the same episode and change nothing. Step resumes after the
// pause and raises the cap one lane per clean RateRaiseEvery, measured: a
// turn ended clean in it (Clean). Back at the row's width the backoff starts
// over. Each change is one line.
type LaneGovernor struct {
	cap         int           // the live cap; 0 is the row's width
	pausedUntil time.Time     // no new turn before it
	resumed     bool          // the pause's end has been said
	backoff     time.Duration // the next pause; 0 is RateBackoffFirst
	last        time.Time     // the last lowering
	cleanSince  time.Time     // since when no rate limit: the pause's end, or the last raise
	measured    bool          // a turn ended clean since cleanSince
	lowered     []time.Time   // lowerings not yet judged, within RateJudgeWithin
	held        string        // out of funds: why; "" none
}

// Cap is the live cap at width: the row's width unless a rate limit has
// lowered it, never above the row.
func (g *LaneGovernor) Cap(width int) int {
	if g.cap == 0 {
		return width
	}
	return min(g.cap, width)
}

// Paused says whether no new turn or open may start now: a backoff under
// way, or out of funds.
func (g *LaneGovernor) Paused(now time.Time) bool {
	return g.held != "" || now.Before(g.pausedUntil)
}

// Release lifts a hold: a person brought the lanes up (the pause marker is gone).
func (g *LaneGovernor) Release() { g.held = "" }

// Held is why the lanes are held, out of funds; "" when they are not.
func (g *LaneGovernor) Held() string { return g.held }

// RateLimit is a rate limit met at now by a turn (or open) started at
// started, at the row's width: the line that says the change, and whether
// it is a judgment (the RateJudgeAfter-th lowering within RateJudgeWithin).
// A turn started before the last lowering is the same episode: no line.
func (g *LaneGovernor) RateLimit(now, started time.Time, width int, reason string) (line string, judge bool) {
	if !g.last.IsZero() && !started.After(g.last) {
		return "", false
	}
	from := g.Cap(width)
	to := max(1, from-max(1, from/4))
	pause := g.backoff
	if pause == 0 {
		pause = RateBackoffFirst
	}
	g.cap, g.last = to, now
	g.pausedUntil, g.resumed, g.cleanSince, g.measured = now.Add(pause), false, now.Add(pause), false
	g.backoff = min(2*pause, RateBackoffMax)
	kept := g.lowered[:0]
	for _, at := range g.lowered {
		if now.Sub(at) < RateJudgeWithin {
			kept = append(kept, at)
		}
	}
	g.lowered = append(kept, now)
	if len(g.lowered) >= RateJudgeAfter {
		judge, g.lowered = true, nil
	}
	return fmt.Sprintf("rate limit: lanes paused %s until %s, cap %d -> %d of %d: %s", pause, g.pausedUntil.UTC().Format(time.RFC3339), from, to, width, oneLine(reason, 200)), judge
}

// Clean is a lane turn that ended at now without a rate limit: the
// measurement a raise waits for.
func (g *LaneGovernor) Clean(now time.Time) {
	if !now.Before(g.pausedUntil) {
		g.measured = true
	}
}

// Step is the governor at now and the row's width: the pause's end said
// once, and a clean RateRaiseEvery with a clean turn in it raising the cap
// one lane; back at the width, the cap is the row's again and the backoff
// starts over. It answers the lines of what changed.
func (g *LaneGovernor) Step(now time.Time, width int) []string {
	var lines []string
	if !g.pausedUntil.IsZero() && !g.resumed && !now.Before(g.pausedUntil) {
		g.resumed = true
		lines = append(lines, fmt.Sprintf("rate limit: lanes resume at cap %d of %d", g.Cap(width), width))
	}
	if g.cap == 0 || g.held != "" || now.Before(g.pausedUntil) {
		return lines
	}
	if g.cap >= width {
		g.cap, g.backoff = 0, 0 // the row came down to the cap: the row's width again
		return lines
	}
	if !g.measured || now.Sub(g.cleanSince) < RateRaiseEvery {
		return lines
	}
	from := g.cap
	g.cap++
	g.cleanSince, g.measured = now, false
	line := fmt.Sprintf("rate limit: cap raised %d -> %d of %d after a clean %s", from, g.cap, width, RateRaiseEvery)
	if g.cap >= width {
		g.cap, g.backoff = 0, 0
		line += "; the row's width again"
	}
	return append(lines, line)
}

// Hold is out of funds: the lanes start nothing more until the daemon
// restarts. It answers whether this is the first hold, the one to say.
func (g *LaneGovernor) Hold(reason string) bool {
	if g.held != "" {
		return false
	}
	g.held = oneLine(reason, 200)
	if g.held == "" {
		g.held = "out of funds"
	}
	return true
}

// RateJudgmentText is what the coordinator is told when a friend's lane cap
// was lowered RateJudgeAfter times within RateJudgeWithin: one judgment.
func RateJudgmentText(friend string, cap, width int, reason string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: lane cap lowered %d times in %s by rate limits, now %d of %d", friend, RateJudgeAfter, RateJudgeWithin, cap, width)
	body = fmt.Sprintf("Her provider rate-limited her lanes %d times within %s; the last: %s. The daemon pauses and lowers the cap by itself and raises it one lane per clean %s, with no hold. A judgment: keep her row's width, lower it (nova-config friend set %s --width %d), or move her to another route.\n",
		RateJudgeAfter, RateJudgeWithin, oneLine(reason, 200), RateRaiseEvery, friend, cap)
	return subject, body
}

// FundsJudgmentText is what the coordinator is told when a friend's
// provider is out of funds: one judgment.
func FundsJudgmentText(friend, reason string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: out of funds: %s", friend, oneLine(reason, 160))
	body = fmt.Sprintf("Her provider refused a lane for want of funds or credit: %s. Her lanes start nothing more until her daemon restarts; every card stays in its lane's hand, none set aside. A payment is the owner's. To show it on her row: nova-sprint friend down %s --reason %s. Once paid, restart her daemon (nova-friend install again, or launchctl kickstart -k gui/<uid>/com.nova.friend-%s).\n",
		oneLine(reason, 200), friend, shellQuote("out of funds: "+oneLine(reason, 120)), friend)
	return subject, body
}
