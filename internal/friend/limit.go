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

// DefaultLimitSlack is how long past a provider's reset the limit layer waits
// before a wake is tried: a reset named to the second is met with a moment to
// spare, never a guess (docs/SPEC-FRIEND.md, a provider failure is typed).
const DefaultLimitSlack = 30 * time.Second

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
// it, Limits.AllowOverage).
type Limit struct {
	Limited bool
	Until   time.Time
	Reason  string
	Usage   Usage
	Overage bool
}

var (
	// limitWords is a line that says the harness is at its limit or out of
	// credits; a line counts only with its reset beside it (limitReset).
	limitWords = regexp.MustCompile(`(?i)insufficient [a-z ]{0,20}credits|out of (?:ai )?credits|credits? (?:exhausted|ran out)|usage limit|limit reached|hit your (?:[a-z-]+ )?limit|quota (?:exceeded|exhausted)`)
	limitEpoch = regexp.MustCompile(`(?i)limit reached\|(\d{10})\b`)
	limitAt    = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap])\.?m\b`)
	limitAfter = regexp.MustCompile(`(?i)(?:refresh(?:es)?|resets?|try again|available again)\s+in\s+(\d{1,3})\s*(hours?|hrs?|h|minutes?|mins?|m)\b`)
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
// its tail with the reset beside it (a clock time, today or else tomorrow
// in now's zone or the zone it names, on the date it names; "in N hours";
// an epoch after "limit reached|"; resetOfText). found is
// whether the output said anything of the limit at all. A provider's
// transient rate_limit_error is no limit (ProviderRefusal passes it).
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
	for _, line := range strings.Split(tail, "\n") {
		if !limitWords.MatchString(line) {
			continue
		}
		if until, ok := resetOfText(stripANSI(line), now); ok {
			lim = Limit{Limited: true, Until: until, Reason: oneLine(line, 200)}
			found = true
		}
	}
	return lim, found
}

// resetOf is the reset a limit line names.
func resetOf(line string, now time.Time) (time.Time, bool) {
	if m := limitEpoch.FindStringSubmatch(line); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64) // ignored: ten digits always parse
		return time.Unix(n, 0).In(now.Location()), true
	}
	if m := limitAt.FindStringSubmatch(line); m != nil {
		h, _ := strconv.Atoi(m[1]) // ignored: digits always parse
		mins := 0
		if m[2] != "" {
			mins, _ = strconv.Atoi(m[2]) // ignored: digits always parse
		}
		if h < 1 || h > 12 || mins > 59 {
			return time.Time{}, false
		}
		h %= 12
		if strings.EqualFold(m[3], "p") {
			h += 12
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), h, mins, 0, 0, now.Location())
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

// Limits is one friend's harness limit: up, or down until a reset, then
// waking until a turn answers the current nonce (tla/FriendLimit.tla is
// owed). Watch reads every command's output for it; Gate holds deliveries
// while it is down and wakes the session after the reset. The hooks say the
// change to the sprint: Down once per limit with its reset (friend down
// --until), Up when a wake answered.
type Limits struct {
	Now   func() time.Time
	Nonce func() string // six random characters when nil
	Down  func(until time.Time, reason string)
	Up    func(nonce string)
	// Unread is the judgment when a limit's text names no reset this reads: the
	// friend is held for Rest and the text goes to the coordinator, once until
	// a wake is answered, so the reset is set by a person (friend down --until)
	// and not guessed again each Rest (usage-limit-reset-read-from-the-message-b.w1).
	Unread func(text string)
	// Harness is the harness whose own wording a failed turn is read in
	// (ParseLimit), and Rest how long it is down when the text names no
	// reset (DefaultLimitWait when zero): --limit-rest.
	Harness string
	Rest    time.Duration
	// Slack is how long past a named reset the layer waits before a wake
	// (DefaultLimitSlack when zero): a provider failure's retry-after is a
	// floor, never a guess.
	Slack time.Duration
	// Record, when set, is told each typed provider failure (ProviderFailure)
	// the layer holds the lanes for, one line (docs/SPEC-FRIEND.md, a provider
	// failure is typed): the kind, the status, the retry-after and the
	// request id.
	Record func(line string)
	// AllowOverage is the owner's word that this friend may spend paid
	// overage; without it a harness on overage reads down.
	AllowOverage bool
	// Pacing is the row's pacing (the fraction of each subscription window the
	// sprint may spend), read at each batch turn; nil, or out of (0, 1], is
	// DefaultPacing. Every output's rate_limit_event feeds the pacer, and a batch
	// turn while a window is at the pacing is Deferred until it resets
	// (pacing.go).
	Pacing func() float64

	mu       sync.Mutex
	beatMu   sync.Mutex // a down beat's look and its send, against a wake ending the limit (BeatOrDown, gated.Deliver)
	pace     Pacer
	limited  bool
	until    time.Time
	reason   string
	kind     string // KindLimit or KindCredits while limited; empty when the text gave none
	episodes int    // limits seen, so a turn knows it hit one
	waking   string
	answered bool
	judged   bool // Unread said for this hold; cleared when a wake is answered
}

// Limited is the limit now: until when and why, and whether there is one.
func (l *Limits) Limited() (until time.Time, reason string, limited bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.until, l.reason, l.limited
}

// Kind is what the limit is, KindLimit or KindCredits, while there is one.
func (l *Limits) Kind() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.kind
}

// limitKindOf is a limit line's kind: credits when it says so, else a limit.
func limitKindOf(line string) string {
	if limitCredits.MatchString(line) {
		return KindCredits
	}
	return KindLimit
}

var limitCredits = regexp.MustCompile(`(?i)credits?|balance|billing|payment`)

// Watch is run reading every command's output for a limit and, while a wake
// is open, for its nonce.
func (l *Limits) Watch(run Exec) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		out, exit, err := run(ctx, dir, name, args, stdin)
		l.see(out, exit != 0 || err != nil)
		return out, exit, err
	}
}

func (l *Limits) see(out string, failed bool) {
	now := l.Now()
	lim, found := ReadLimit(out, now)
	uses := ReadRateLimitEvents(out, now)
	kind, named := "", true
	if !strings.Contains(out, `"rate_limit_event"`) && failed {
		// the provider's own typed failure first: read once and driven by its
		// kind, never the text again (docs/SPEC-FRIEND.md, a provider failure
		// is typed). A funds line with its reset beside it is the harness's own
		// limit (ClassifyProviderFailure) and falls through to its wording below.
		if f, ok := ClassifyProviderFailure("", out, now); ok && f.IsLimit() {
			l.failAt(f, now)
			return
		}
		// the harness's own wording of a failed turn, with its kind and a default reset
		// when it names none; a successful turn that only talks of limits is no limit
		if hit, ok := ParseLimit(l.Harness, out, now, l.Rest); ok {
			lim, found, kind, named = Limit{Limited: true, Until: hit.Until, Reason: hit.Reason}, true, hit.Kind, hit.Named
		}
	}
	if found && lim.Limited && kind == "" {
		kind = limitKindOf(lim.Reason)
	}
	l.mu.Lock()
	l.pace.Observe(uses)
	if l.waking != "" && strings.Contains(out, l.waking) {
		l.answered = true
	}
	if found && lim.Overage && !l.AllowOverage && !lim.Limited {
		lim.Limited = true
		lim.Reason += "; overage is not allowed for this friend"
	}
	if !found || !lim.Limited || (l.limited && l.until.Equal(lim.Until)) {
		l.mu.Unlock()
		return
	}
	l.limited, l.until, l.reason, l.kind, l.waking, l.answered = true, lim.Until, lim.Reason, kind, "", false
	l.episodes++
	judge := !named && !l.judged
	if judge {
		l.judged = true
	}
	l.mu.Unlock()
	if l.Down != nil {
		l.Down(lim.Until, lim.Reason)
	}
	if judge && l.Unread != nil {
		l.Unread(lim.Reason)
	}
}

// Fail is one attempt's typed provider failure (ClassifyProviderFailure)
// driving the layer by its kind, never by the text again (docs/SPEC-FRIEND.md,
// a provider failure is typed): a rate limit or a usage limit with a known
// reset parks the lanes until the reset plus Slack, then a wake; one with no
// reset known, and out of funds, holds with no wake time (until zero: the gate
// never guesses one). One typed record is said per attempt
// (ProviderFailure.RecordLine), and Down tells the sprint once, on the first
// time the hold or its reset changes, never again while it stands.
func (l *Limits) Fail(f ProviderFailure) { l.failAt(f, l.Now()) }

func (l *Limits) failAt(f ProviderFailure, now time.Time) {
	if !f.IsLimit() {
		return
	}
	slack := l.Slack
	if slack <= 0 {
		slack = DefaultLimitSlack
	}
	until := time.Time{}
	if f.RetryAfter > 0 {
		until = now.Add(f.RetryAfter + slack)
	}
	l.mu.Lock()
	same := l.limited && l.kind == f.Kind && l.until.Equal(until)
	if !same {
		l.limited, l.kind, l.reason, l.waking, l.answered = true, f.Kind, f.Reason, "", false
		l.until = until
		l.episodes++
	}
	l.mu.Unlock()
	if l.Record != nil {
		l.Record(f.RecordLine()) // one typed record per attempt, however alike
	}
	if !same && l.Down != nil {
		l.Down(until, f.Reason)
	}
}

// Refuse is a credit or quota refusal a caller read where this harness's own
// wording parse (ParseLimit) and the limit tail found none: the daemon's
// per-harness table in cmd/nova-friend/limit.go reads a lane's evidence and
// hands the hit here (docs/SPEC-FRIEND.md, every harness's credit and quota
// refusal). It takes the same state and hooks see takes for a limit a
// harness's own wording names: the friend is down until the refusal's until,
// her turns and beats are held, Down says it to the sprint once, and status
// reads session=limited limit_kind=kind limit_until=until. A refusal equal to
// the one that already holds her is not a second down.
func (l *Limits) Refuse(kind, reason string, until time.Time) {
	l.mu.Lock()
	if l.limited && l.kind == kind && l.reason == reason {
		l.mu.Unlock()
		return
	}
	l.limited, l.until, l.reason, l.kind, l.waking, l.answered = true, until, reason, kind, "", false
	l.episodes++
	l.mu.Unlock()
	if l.Down != nil {
		l.Down(until, reason)
	}
}

// WindowUse is the subscription windows' use as the harness last reported
// it in any command's output ("5h 62% 7d 31%"), empty when none is live.
func (l *Limits) WindowUse() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pace.Use(l.Now())
}

// pacedOut is why a batch turn at now is held by the pacing, empty when it
// is not: a batch turn is one lane, so it is held when the pacer allows none.
func (l *Limits) pacedOut(now time.Time) string {
	pacing := DefaultPacing
	if l.Pacing != nil {
		pacing = PacingOf(l.Pacing())
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pace.held(now, pacing)
}

// Beat is beat held back while the harness is at its limit: no beat goes
// to the sprint server, so her row reads down, from the turn that hit the
// limit until a wake after the reset answers its nonce (Gate). A reset that
// passes with no answer (the wake never reached a session, say) keeps her
// down: only the session's answer brings her up, never a process seen or not
// seen in the process table (HarnessWatch is advisory).
func (l *Limits) Beat(beat func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if until, reason, limited := l.Limited(); limited {
			return fmt.Errorf("not beating: the harness is at its limit until %s, then until a wake is answered: %s", until.UTC().Format(time.RFC3339), reason)
		}
		return beat(ctx)
	}
}

// BeatOrDown is beat while the harness answers, and down while it is at its limit
// (limits-mean-down-w-r5.w1~15): her beat says down with the until and the reason
// (nova-sprint friend beat --until --reason), so her row reads down and why rather
// than going silent, until a wake after the reset is answered. A nil down holds the
// beat back as Beat does; a down the sprint server refuses is an error, and her row
// reads down by the lapse as before.
func (l *Limits) BeatOrDown(beat func(ctx context.Context) error, down func(ctx context.Context, until time.Time, reason string) error) func(ctx context.Context) error {
	if down == nil {
		return l.Beat(beat)
	}
	return func(ctx context.Context) error {
		// the look and the down beat are one step against the wake's answer ending the
		// limit, so no down beat is sent after she is up again
		l.beatMu.Lock()
		until, reason, limited := l.Limited()
		if !limited {
			l.beatMu.Unlock()
			return beat(ctx)
		}
		defer l.beatMu.Unlock()
		if err := down(ctx, until, "harness limit: "+reason); err != nil {
			return fmt.Errorf("beating down until %s: %w", until.UTC().Format(time.RFC3339), err)
		}
		return nil
	}
}

// LimitDownText is what the seat is told when friend's harness hits its
// limit: the subject, and a body with the line that shows why and until when
// on her row (nova-sprint friend down --reason --until).
func LimitDownText(friend string, until time.Time, reason string) (subject, body string) {
	subject = fmt.Sprintf("friend %s down: her harness is at its limit until %s", friend, until.UTC().Format(time.RFC3339))
	body = fmt.Sprintf("%s: %s\nHer daemon beats down with that reset and reason and delivers nothing until a wake after the reset is answered; every message stays pending. To show why on her row: nova-sprint friend down %s --reason %s --until %s\n",
		subject, reason, friend, shellQuote("harness limit: "+reason), until.UTC().Format(time.RFC3339))
	return subject, body
}

// LimitUnreadText is the one judgment the coordinator is told when friend's
// harness refused at its limit with a text that names no reset this reads:
// the text, the rest she is held for, and the line that sets the true reset.
func LimitUnreadText(friend string, rest time.Duration, text string) (subject, body string) {
	if rest <= 0 {
		rest = DefaultLimitWait
	}
	subject = fmt.Sprintf("friend %s held: her harness is at its limit and its message names no reset I can read", friend)
	body = fmt.Sprintf("%s: %q\nHer daemon holds her and tries a wake every %s until one is answered; this is said once until then. A judgment: read the reset from the text and set it (nova-sprint friend down %s --reason %s --until <RFC3339>), and add the text to internal/friend/testdata/limits.tsv so the next one is read.\n",
		subject, text, rest, friend, shellQuote("harness limit: "+text))
	return subject, body
}

// LimitAlikeText is the one judgment the coordinator is told when three lanes
// in a row ended with the same first error line and none names a limit or a
// credit or quota refusal the daemon's table knows (cmd/nova-friend/limit.go,
// RefusalWatch): the line, so the wording is added or the cause is found,
// never a silent fourth retry.
func LimitAlikeText(friend, line string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: her lanes are failing alike", friend)
	body = fmt.Sprintf("%s: %q\nThree lanes in a row ended with that first error line and none names a limit or a credit wording I know. Her daemon holds it to this one judgment, never a silent fourth retry. Read the line, fix the cause or add its wording to cmd/nova-friend/limit.go, then let her go on: nova-sprint friend up %s\n",
		subject, line, friend)
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
	limited, until, reason := l.limited, l.until, l.reason
	l.mu.Unlock()
	if limited && until.IsZero() {
		// no reset known: the lanes are held with no wake time, never a
		// guessed one (docs/SPEC-FRIEND.md, a provider failure is typed)
		return 0, Deferred{Reason: fmt.Sprintf("the harness is down with no reset known: %s", reason)}
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
		l.beatMu.Lock() // a down beat in flight lands before the limit ends (BeatOrDown)
		l.mu.Lock()
		again, answered := l.episodes != episodes, l.answered && exit == 0 && err == nil
		if answered && !again {
			l.limited, l.waking, l.judged = false, "", false
		}
		until, reason = l.until, l.reason
		l.mu.Unlock()
		l.beatMu.Unlock()
		if again {
			return 0, Deferred{Reason: fmt.Sprintf("the wake hit the limit again; down until %s: %s", until.Format(time.RFC3339), reason)}
		}
		if !answered {
			return 0, Deferred{Reason: fmt.Sprintf("woken after the reset; no answer with the nonce %s yet (exit %d)", nonce, exit)}
		}
		if l.Up != nil {
			l.Up(nonce)
		}
	}
	if why := l.pacedOut(now); why != "" {
		return 0, Deferred{Reason: why}
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
