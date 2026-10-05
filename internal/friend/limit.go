package friend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A harness at its usage limit or out of credits is down until its reset,
// and woken after it (docs/SPEC-FRIEND.md, a harness at its limit; the
// friend's side in docs/SPEC-SPRINT.md, friend down). The finding of 2026-10-04: a friend's harness
// stopped on "Insufficient AI Credits ... will refresh 6:52 PM" while her row
// read up with six working cards, and four Claude accounts ran out of their
// weekly usage unseen.

// LimitTail is how much of the end of a command's output is read for a
// limit line: a harness says it last, and the body of a reply that only
// talks about limits is far from the tail.
const LimitTail = 2048

// DefaultLimitWait is how long a harness that refused at its limit and
// named no reset is down before a wake is tried.
const DefaultLimitWait = time.Hour

// Usage is a harness's measured utilization of its five-hour and weekly
// windows, each a fraction (1 is the window spent), with when each resets
// and when it was measured (zero: never).
type Usage struct {
	FiveHour, SevenDay             float64
	FiveHourResets, SevenDayResets time.Time
	At                             time.Time
}

// Limit is what a command's output says of the harness's limit: Limited,
// until when and why; Usage when the output measured it; Overage when the
// harness is spending paid overage (a limit only if the owner does not allow
// it, Limits.AllowOverage). Unreadable is a usage-limit refusal whose reset
// could not be read: the friend is held, Reason is the text, and Until is zero.
type Limit struct {
	Limited    bool
	Until      time.Time
	Reason     string
	Usage      Usage
	Overage    bool
	Unreadable bool
}

var (
	// limitWords is a line that says the harness is at its limit or out of
	// credits. A reset beside it is the limit; a refusal with no readable
	// reset holds (limitRefusal); a mention is neither.
	limitWords = regexp.MustCompile(`(?i)insufficient [a-z ]{0,20}credits|out of (?:ai )?credits|credits? (?:exhausted|ran out)|usage limit|limit reached|hit your (?:[a-z-]+ )?limit|quota (?:exceeded|exhausted)`)
	// limitRefusal is the provider refusing, not a reply that mentions a limit.
	// With no reset that parses, the friend is held and the text is one judgment.
	limitRefusal = regexp.MustCompile(`(?i)you(?:'ve| have) hit your (?:[a-z0-9-]+ )?limit\b|usage limit reached|quota (?:exceeded|exhausted)|\blimit reached\b`)
	limitEpoch   = regexp.MustCompile(`(?i)limit reached\|(\d{10})\b`)
	// limitEpochWord is an epoch the reset words name: ten digits of seconds,
	// or thirteen of milliseconds.
	limitEpochWord = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again|until)\s+(?:at\s+)?(\d{10}|\d{13})\b`)
	limitDated     = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)\s+(?:on\s+)?((?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*)\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:\s*,)?(?:\s+at)?\s+(\d{1,2})(?::(\d{2}))?\s*([ap])\.?m\b`)
	limitAt        = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap])\.?m\b`)
	limitAfter     = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)\s+in\s+(\d{1,3})\s*(hours?|hrs?|h|minutes?|mins?|m)\b`)
	// limitZone is the IANA zone a provider puts in parentheses.
	limitZone = regexp.MustCompile(`\(([A-Za-z]+(?:/[A-Za-z0-9_+-]+)+|UTC|GMT)\)`)
)

// rateLimitEvent is Claude Code's stream-json rate_limit_event, printed per
// run with --output-format stream-json --verbose: the status (allowed,
// allowed_warning, rejected), paid overage, and each window's utilization
// (0 to 1) and reset (epoch seconds); an older shape names one window.
type rateLimitEvent struct {
	Type string `json:"type"`
	Info struct {
		Status         string   `json:"status"`
		ResetsAt       int64    `json:"resetsAt"`
		RateLimitType  string   `json:"rateLimitType"`
		Utilization    *float64 `json:"utilization"`
		IsUsingOverage bool     `json:"isUsingOverage"`
		Windows        map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// ReadLimit reads a command's output at now for the harness's limit: the
// last rate_limit_event anywhere in it (Claude Code), else a limit line in
// its tail with the reset beside it. The reset is a clock time ("resets
// 8pm"), today or else tomorrow when that time has passed, in the zone the
// line names ("America/New_York") or else now's zone; a calendar day
// ("resets Oct 10 at 5am") in that zone; "in N hours"; or an epoch ("resets
// 1760072400", or after "limit reached|"). A usage-limit refusal with no
// readable reset is Unreadable, not a guessed hour. found is whether the
// output said anything of the limit at all. A provider's transient
// rate_limit_error is no limit (ProviderRefusal passes it). A line that only
// mentions limits is not one.
func ReadLimit(out string, now time.Time) (lim Limit, found bool) {
	if strings.Contains(out, `"rate_limit_event"`) {
		for _, line := range strings.Split(out, "\n") {
			var ev rateLimitEvent
			if !strings.Contains(line, `"rate_limit_event"`) || json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "rate_limit_event" {
				continue
			}
			lim, found = claudeLimit(ev, now), true
		}
		if found {
			return lim, true
		}
	}
	tail := out
	if len(tail) > LimitTail {
		tail = tail[len(tail)-LimitTail:]
	}
	var unread string
	for _, line := range strings.Split(tail, "\n") {
		if !limitWords.MatchString(line) {
			continue
		}
		if until, ok := resetOf(line, now); ok && until.After(now) {
			lim = Limit{Limited: true, Until: until, Reason: oneLine(line, 200)}
			found, unread = true, ""
			continue
		} else if ok {
			continue // the reset was readable and has already passed
		}
		if !lim.Limited && unread == "" && limitRefusal.MatchString(line) {
			unread = oneLine(line, 200)
		}
	}
	if lim.Limited {
		return lim, true
	}
	if unread != "" {
		return Limit{Unreadable: true, Reason: unread}, true
	}
	return lim, found
}

// resetOf is the reset a limit line names. A clock time that has passed is
// the next day. A named zone that does not load is no reset.
func resetOf(line string, now time.Time) (time.Time, bool) {
	if m := limitEpoch.FindStringSubmatch(line); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64) // ignored: ten digits always parse
		return time.Unix(n, 0).In(now.Location()), true
	}
	if m := limitEpochWord.FindStringSubmatch(line); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64) // ignored: the pattern is digits
		if len(m[1]) == 13 {
			return time.UnixMilli(n).In(now.Location()), true
		}
		return time.Unix(n, 0).In(now.Location()), true
	}
	loc, ok := limitLocation(line, now)
	if !ok {
		return time.Time{}, false
	}
	if m := limitDated.FindStringSubmatch(line); m != nil {
		return datedReset(m, loc, now)
	}
	if m := limitAt.FindStringSubmatch(line); m != nil {
		h, mins, ok := clockOf(m[1], m[2], m[3])
		if !ok {
			return time.Time{}, false
		}
		day := now.In(loc)
		t := time.Date(day.Year(), day.Month(), day.Day(), h, mins, 0, 0, loc)
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	}
	if m := limitAfter.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1]) // ignored: digits always parse
		unit := time.Minute
		if strings.HasPrefix(strings.ToLower(m[2]), "h") {
			unit = time.Hour
		}
		return now.Add(time.Duration(n) * unit), true
	}
	return time.Time{}, false
}

// limitLocation is the zone the line names, or now's zone when it names none.
// ok is false when it names a zone that does not load.
func limitLocation(line string, now time.Time) (*time.Location, bool) {
	m := limitZone.FindStringSubmatch(line)
	if m == nil {
		if now.Location() == nil {
			return time.UTC, true
		}
		return now.Location(), true
	}
	loc, err := time.LoadLocation(m[1])
	if err != nil {
		return nil, false
	}
	return loc, true
}

// clockOf is a 12-hour clock as hours and minutes from midnight.
func clockOf(hour, mins, ap string) (int, int, bool) {
	h, _ := strconv.Atoi(hour) // ignored: the pattern is digits
	m := 0
	if mins != "" {
		m, _ = strconv.Atoi(mins) // ignored: the pattern is digits
	}
	if h < 1 || h > 12 || m > 59 {
		return 0, 0, false
	}
	h %= 12
	if strings.EqualFold(ap, "p") {
		h += 12
	}
	return h, m, true
}

// datedReset is a calendar day and a clock time in loc, in now's year there.
// A day that is not one (February 31) is no reset. It is not rolled forward:
// a date that has passed is a reset that has passed.
func datedReset(m []string, loc *time.Location, now time.Time) (time.Time, bool) {
	month, ok := monthOf(m[1])
	if !ok {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[2]) // ignored: the pattern is digits
	h, mins, ok := clockOf(m[3], m[4], m[5])
	if !ok || day < 1 || day > 31 {
		return time.Time{}, false
	}
	t := time.Date(now.In(loc).Year(), month, day, h, mins, 0, 0, loc)
	if t.Month() != month || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}

func monthOf(name string) (time.Month, bool) {
	name = strings.ToLower(name)
	if len(name) < 3 {
		return 0, false
	}
	const months = "janfebmaraprmayjunjulaugsepoctnovdec"
	i := strings.Index(months, name[:3])
	if i < 0 || i%3 != 0 {
		return 0, false
	}
	return time.Month(i/3 + 1), true
}

// claudeLimit is a rate_limit_event as a Limit: its windows the usage; a
// rejection, or overage, down until the spent window resets (the latest of
// the windows at 1, else the five-hour window's, else the event's own reset,
// else DefaultLimitWait).
func claudeLimit(ev rateLimitEvent, now time.Time) Limit {
	in := ev.Info
	u := Usage{At: now}
	if in.Utilization != nil && in.RateLimitType != "" && len(in.Windows) == 0 {
		in.Windows = map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
		}{in.RateLimitType: {Utilization: *in.Utilization, ResetsAt: in.ResetsAt}}
	}
	var spent []string
	var until time.Time
	for name, w := range in.Windows {
		reset := time.Time{}
		if w.ResetsAt > 0 {
			reset = time.Unix(w.ResetsAt, 0)
		}
		switch name {
		case "five_hour":
			u.FiveHour, u.FiveHourResets = w.Utilization, reset
		case "seven_day":
			u.SevenDay, u.SevenDayResets = w.Utilization, reset
		}
		if w.Utilization >= 1 {
			spent = append(spent, name)
			if reset.After(until) {
				until = reset
			}
		}
	}
	if until.IsZero() {
		until = u.FiveHourResets
	}
	if until.IsZero() && in.ResetsAt > 0 {
		until = time.Unix(in.ResetsAt, 0)
	}
	if !until.After(now) {
		until = now.Add(DefaultLimitWait)
	}
	lim := Limit{Usage: u, Overage: in.IsUsingOverage, Until: until}
	why := "usage limit"
	if len(spent) > 0 {
		slices.Sort(spent)
		why += " (" + strings.Join(spent, ", ") + " spent)"
	}
	switch {
	case in.Status == "rejected":
		lim.Limited, lim.Reason = true, "claude rate_limit_event rejected: "+why
	case in.IsUsingOverage:
		lim.Reason = "claude is spending paid overage: " + why
	}
	return lim
}

// Limits is one friend's harness limit: up, or down until the reset the
// message stated, then waking until a turn answers the current nonce, or
// held when that reset cannot be read (tla/FriendLimit.tla). Watch reads
// every command's output for it; Gate holds deliveries while it is down and
// wakes the session after the reset. The hooks say the change: Down once per
// limit with its reset (friend down --until), Judge once when the reset
// cannot be read, Up when a wake answered.
type Limits struct {
	Now   func() time.Time
	Nonce func() string // six random characters when nil
	Down  func(until time.Time, reason string)
	Up    func(nonce string)
	// Judge is told once when a usage-limit refusal names no readable reset.
	// The text is the provider's line. The friend is held, not guessed an hour.
	Judge func(text string)
	// AllowOverage is the owner's word that this friend may spend paid
	// overage; without it a harness on overage reads down.
	AllowOverage bool

	mu       sync.Mutex
	limited  bool
	until    time.Time
	reason   string
	held     string // the unreadable refusal; "" when the friend is not held for one
	episodes int    // limits seen, so a turn knows it hit one
	waking   string
	answered bool
}

// Limited is the limit now: until when and why, and whether there is one.
// A hold for an unreadable reset is limited with a zero until.
func (l *Limits) Limited() (until time.Time, reason string, limited bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.until, l.reason, l.limited
}

// Held is the usage-limit refusal whose reset could not be read, and whether
// the friend is held for it. One judgment named it; nothing was guessed.
func (l *Limits) Held() (text string, held bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held, l.held != ""
}

// Watch is run reading every command's output for a limit and, while a wake
// is open, for its nonce.
func (l *Limits) Watch(run Exec) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		out, exit, err := run(ctx, dir, name, args, stdin)
		l.see(out)
		return out, exit, err
	}
}

func (l *Limits) see(out string) {
	now := l.Now()
	lim, found := ReadLimit(out, now)
	l.mu.Lock()
	if l.waking != "" && strings.Contains(out, l.waking) {
		l.answered = true
	}
	if found && lim.Overage && !l.AllowOverage && !lim.Limited && !lim.Unreadable {
		lim.Limited = true
		lim.Reason += "; overage is not allowed for this friend"
	}
	if found && lim.Unreadable && !lim.Limited {
		if l.held != "" || l.limited {
			l.mu.Unlock()
			return // already held, or already down until a reset that did parse: one judgment
		}
		l.held = lim.Reason
		l.limited, l.until, l.reason, l.waking, l.answered = true, time.Time{}, lim.Reason, "", false
		l.episodes++
		text := lim.Reason
		l.mu.Unlock()
		if l.Judge != nil {
			l.Judge(text)
		}
		return
	}
	if !found || !lim.Limited || (l.limited && l.held == "" && l.until.Equal(lim.Until)) {
		l.mu.Unlock()
		return
	}
	l.held = ""
	l.limited, l.until, l.reason, l.waking, l.answered = true, lim.Until, lim.Reason, "", false
	l.episodes++
	l.mu.Unlock()
	if l.Down != nil {
		l.Down(lim.Until, lim.Reason)
	}
}

// Beat is beat held back while the harness is at its limit: no beat goes
// to the sprint server, so her row reads down, from the turn that hit the
// limit until a wake after the reset answers its nonce (Gate). A reset that
// passes with no answer (the harness not running, say) keeps her down: only
// the session's answer brings her up.
func (l *Limits) Beat(beat func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if until, reason, limited := l.Limited(); limited {
			if until.IsZero() {
				return fmt.Errorf("not beating: the harness named a usage limit with no readable reset, so the friend is held: %s", reason)
			}
			return fmt.Errorf("not beating: the harness is at its limit until %s, then until a wake is answered: %s", until.UTC().Format(time.RFC3339), reason)
		}
		return beat(ctx)
	}
}

// LimitDownText is what the seat is told when friend's harness hits its
// limit: the subject, and a body with the line that shows why and until when
// on her row (nova-sprint friend down --reason --until).
func LimitDownText(friend string, until time.Time, reason string) (subject, body string) {
	subject = fmt.Sprintf("friend %s down: her harness is at its limit until %s", friend, until.UTC().Format(time.RFC3339))
	body = fmt.Sprintf("%s: %s\nHer daemon has stopped beating and delivers nothing until a wake after the reset is answered; every message stays pending. To show why on her row: nova-sprint friend down %s --reason %s --until %s\n",
		subject, reason, friend, shellQuote("harness limit: "+reason), until.UTC().Format(time.RFC3339))
	return subject, body
}

// LimitHoldText is the one judgment when a usage-limit refusal names no
// readable reset. The subject names the text. Nothing in it is a guessed hour.
func LimitHoldText(friendName, text string) (subject, body string) {
	text = oneLine(text, 200)
	subject = fmt.Sprintf("friend %s: usage limit, no readable reset: %s", friendName, text)
	body = fmt.Sprintf("%s\nHer harness refused for a usage limit and the reset could not be read from that text. She is held, not down for a guessed hour, and nothing is delivered. One judgment. When the reset is known: nova-sprint friend down %s --reason %s --until <RFC3339>\n",
		subject, friendName, shellQuote("usage limit: "+text))
	return subject, body
}

// LimitUpText is what the seat is told when friend's session answered the
// wake after the reset.
func LimitUpText(friend string) (subject, body string) {
	subject = fmt.Sprintf("friend %s back: her harness answered the wake after its reset", friend)
	body = fmt.Sprintf("%s\nHer daemon beats and delivers again. If you held her with friend down: nova-sprint friend up %s\n", subject, friend)
	return subject, body
}

// shellQuote is s in single quotes for a line to paste.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// WakeText is the turn that wakes a session after its reset: one word back,
// the nonce, so only a session that ran this turn answers it.
func WakeText(nonce string) string {
	return "nova-friend: your harness's usage limit has reset. Answer this turn with exactly this word and nothing else: " + nonce + "\n"
}

// Gate is d held by the limit: a delivery while the harness is down is
// Deferred without running it (the daemon keeps the message in hand,
// counted toward nothing); the first after the reset is a wake turn
// (WakeText) whose output must carry its nonce, a fresh one each try, before
// the message goes in; a turn that hits a limit is Deferred too. A passive
// d is d. A LaneHarness stays one (gatedLanes): its batch Deliver is held,
// and its lanes are not.
func (l *Limits) Gate(d Deliverer) Deliverer {
	if _, passive := d.(interface{ Passive() }); passive {
		return d
	}
	g := &gated{l: l, d: d}
	if lh, ok := d.(LaneHarness); ok {
		return &gatedLanes{gated: g, lh: lh}
	}
	return g
}

// gatedLanes is a LaneHarness under the gate: Deliver, the batch turn and
// the session check, is gated's; OpenSession and DeliverTo, the one-shot
// lanes, go straight to the harness, and their output is still read for a
// limit (Watch is under the harness, not here), so a lane turn that hits
// one sends the friend Down all the same. They are exempt because a lane has
// no deferral: lanes.go counts an error from a lane turn toward CardTurns and
// sets the card aside at the last, and retries a failed open after
// LaneOpenRetry, so a Deferred lane turn would give up her card while she
// waits out the reset. Holding the lanes while she is down is owed to the
// lanes' loop (a deferral that keeps the card in hand), not to this gate.
type gatedLanes struct {
	*gated
	lh LaneHarness
}

func (g *gatedLanes) OpenSession(ctx context.Context, seed string) (string, error) {
	return g.lh.OpenSession(ctx, seed)
}

func (g *gatedLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	return g.lh.DeliverTo(ctx, session, text)
}

type gated struct {
	l *Limits
	d Deliverer
}

func (g *gated) Deliver(ctx context.Context, text string) (int, error) {
	l := g.l
	now := l.Now()
	l.mu.Lock()
	limited, until, reason, held := l.limited, l.until, l.reason, l.held
	l.mu.Unlock()
	if held != "" {
		return 0, Deferred{Reason: fmt.Sprintf("held: the harness named a usage limit and no readable reset: %s", reason)}
	}
	if limited && now.Before(until) {
		return 0, Deferred{Reason: fmt.Sprintf("the harness is at its limit until %s: %s", until.Format(time.RFC3339), reason)}
	}
	if limited {
		nonce := l.nonce()
		l.mu.Lock()
		l.waking, l.answered = nonce, false
		episodes := l.episodes
		l.mu.Unlock()
		exit, err := g.d.Deliver(ctx, WakeText(nonce))
		l.mu.Lock()
		again, answered := l.episodes != episodes, l.answered && exit == 0 && err == nil
		if answered && !again {
			l.limited, l.waking = false, ""
		}
		until, reason = l.until, l.reason
		l.mu.Unlock()
		if again {
			if until.IsZero() {
				return 0, Deferred{Reason: fmt.Sprintf("held: the harness named a usage limit and no readable reset: %s", reason)}
			}
			return 0, Deferred{Reason: fmt.Sprintf("the wake hit the limit again; down until %s: %s", until.Format(time.RFC3339), reason)}
		}
		if !answered {
			return 0, Deferred{Reason: fmt.Sprintf("woken after the reset; no answer with the nonce %s yet (exit %d)", nonce, exit)}
		}
		if l.Up != nil {
			l.Up(nonce)
		}
	}
	l.mu.Lock()
	episodes := l.episodes
	l.mu.Unlock()
	exit, err := g.d.Deliver(ctx, text)
	l.mu.Lock()
	hit := l.episodes != episodes && l.limited
	until, reason = l.until, l.reason
	l.mu.Unlock()
	if hit {
		if until.IsZero() {
			return 0, Deferred{Reason: fmt.Sprintf("held: the harness named a usage limit and no readable reset: %s", reason)}
		}
		return 0, Deferred{Reason: fmt.Sprintf("the turn hit the harness's limit; down until %s: %s", until.Format(time.RFC3339), reason)}
	}
	return exit, err
}

func (l *Limits) nonce() string {
	if l.Nonce != nil {
		return l.Nonce()
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	_, _ = rand.Read(b) // ignored: crypto/rand never fails on the platforms built for
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
