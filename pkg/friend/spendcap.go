package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SpendCap is a friend's spend cap per day and per provider (docs/SPEC-FRIEND.md,
// spend caps). A friend daemon has a cap for each provider, read from its config,
// $20 a day by default. Spend is measured from the harness's own usage report,
// never estimated. At the cap the daemon stops launching turns and raises one
// judgment to the seat and the owner naming the friend, the provider and the
// spend; a provider refusal for funds (an insufficient balance, a 402) stops the
// turns at once with the same judgment rather than retrying hourly.
type SpendCap struct {
	Friend   string
	Provider string
	// Now is the daemon's clock (a fake in tests).
	Now func() time.Time
	// DayStart is the start of the spend day containing now; when nil the day
	// resets at UTC midnight.
	DayStart func(now time.Time) time.Time
	// Cap is the day's spend cap in USD for this provider; nil, or not
	// positive, is DefaultSpendCapUSD.
	Cap func() float64
	// Spent is today's spend in USD as the harness's own usage report says it
	// for the day that starts at dayStart. It is read before every turn launch.
	Spent func(dayStart time.Time) float64
	// Judgment is the one judgment raised to the seat and the owner when the cap
	// is passed; it returns the line naming the friend, the provider and the
	// spend. It is called once per spend day until the day resets, and once on a
	// provider refusal for funds.
	Judgment func(dayStart time.Time, spent, cap float64) string
	// Idle is whether the friend has no dealt work and no live session; when it
	// is set and tells, no turn launches at all, with no judgment (docs/SPEC-FRIEND.md,
	// idle). It is checked before the spend cap.
	Idle func() bool
	// Halt, when set, is called when the cap is passed or the provider refused
	// for funds, with the reason and the instant the stop ends: cmd/nova-friend
	// wires it to the harness's own limit gate (Limits.Refuse), so every turn
	// stops, the session liveness check included, not only the ones this gate
	// wraps. Nil: this gate's deliveries alone are stopped.
	Halt func(reason string, until time.Time)

	mu      sync.Mutex
	judged  dayStart // the day the judgment was raised for; zero: none
	text    string   // the judgment's line, for that day
	refused string   // the provider's funds refusal; "" none: turns are stopped until the daemon restarts
}

// DefaultSpendCapUSD is the spend cap in USD per day per provider when the
// config names none.
const DefaultSpendCapUSD = 20.0

// dayStart is an instant rounded to the start of its spend day, comparable with
// == (the wall clock is UTC).
type dayStart time.Time

func (s *SpendCap) dayStartOf(now time.Time) dayStart {
	if s.DayStart != nil {
		return dayStart(s.DayStart(now).UTC())
	}
	return dayStart(now.UTC().Truncate(24 * time.Hour))
}

// capOf is the day's cap for this provider, DefaultSpendCapUSD when the config
// names none or a non-positive one.
func (s *SpendCap) capOf() float64 {
	if s.Cap != nil {
		if c := s.Cap(); c > 0 {
			return c
		}
	}
	return DefaultSpendCapUSD
}

// spentOf is today's spend as the harness's usage report says it.
func (s *SpendCap) spentOf(day dayStart) float64 {
	if s.Spent == nil {
		return 0
	}
	return s.Spent(time.Time(day))
}

// judgmentText is the judgment's line for the day: the judgment hook's own
// wording when it is set, else the default naming the friend, the provider and
// the spend.
func (s *SpendCap) judgmentText(day dayStart) string {
	if s.Judgment != nil {
		return s.Judgment(time.Time(day), s.spentOf(day), s.capOf())
	}
	friend, provider := s.Friend, s.Provider
	if friend == "" {
		friend = "friend"
	}
	if provider == "" {
		provider = "the provider"
	}
	return fmt.Sprintf("%s's %s spend reached $%.2f of its $%.2f/day cap; turns are stopped until the spend day resets", friend, provider, s.spentOf(day), s.capOf())
}

// over is whether the day's measured spend has passed the cap.
func (s *SpendCap) over(day dayStart) bool {
	return s.spentOf(day) >= s.capOf()
}

// judge raises the one judgment for the day, once, when the spend has passed the
// cap. It reports whether the judgment was raised now.
func (s *SpendCap) judge(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Now == nil {
		now = time.Now()
	}
	day := s.dayStartOf(now)
	if s.judged == day || !s.over(day) {
		return false
	}
	s.judged = day
	s.text = s.judgmentText(day)
	return true
}

// Judged is the judgment raised for the day now, "" when none is.
func (s *SpendCap) Judged(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dayStartOf(now) == s.judged {
		return s.text
	}
	return ""
}

// Refuse marks the daemon stopped by a provider refusal for funds (an
// insufficient balance, a 402): the same judgment is raised to the seat and
// the owner, and no turn launches until the daemon restarts (docs/SPEC-FRIEND.md,
// spend caps). The judgment is the hook's own when it is set, so the refusal
// tells the seat and the owner exactly as a spent-out day does.
func (s *SpendCap) Refuse(reason string) {
	s.mu.Lock()
	if s.refused != "" {
		s.mu.Unlock()
		return
	}
	s.refused = reason
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	day := s.dayStartOf(now)
	s.judged = day
	if s.Judgment != nil {
		s.text = s.Judgment(time.Time(day), s.spentOf(day), s.capOf())
	} else {
		s.text = s.refusalText(reason)
	}
	s.mu.Unlock()
	s.halt(now)
}

// refusalText is the judgment's line for a provider refusal for funds: the
// friend, the provider and the refusal, the same shape as the cap's.
func (s *SpendCap) refusalText(reason string) string {
	friend, provider := s.Friend, s.Provider
	if friend == "" {
		friend = "friend"
	}
	if provider == "" {
		provider = "the provider"
	}
	return fmt.Sprintf("%s's %s provider refused for funds: %s; turns are stopped until the daemon restarts", friend, provider, reason)
}

// stopped is whether no turn may launch now: the day's measured spend has
// passed the cap, or the provider refused for funds.
func (s *SpendCap) stopped(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refused != "" {
		return true
	}
	return s.over(s.dayStartOf(now))
}

// halt tells the wired limit gate that no turn may launch: the day's cap is
// passed, or the provider refused for funds. A cap holds until the spend day
// resets; a refusal holds until the daemon restarts, long enough that the
// gate's own wake never brings a turn back (docs/SPEC-FRIEND.md, spend caps).
func (s *SpendCap) halt(now time.Time) {
	if s.Halt == nil {
		return
	}
	s.mu.Lock()
	refused := s.refused
	over := refused == "" && s.over(s.dayStartOf(now))
	s.mu.Unlock()
	switch {
	case refused != "":
		s.Halt(refused, now.Add(spendRefusalHold))
	case over:
		s.Halt(s.Judged(now), time.Time(s.dayStartOf(now)).Add(24*time.Hour))
	}
}

// spendRefusalHold is how long a provider refusal for funds holds the daemon's
// turns: the whole run, until the daemon restarts (docs/SPEC-FRIEND.md, spend
// caps).
const spendRefusalHold = 365 * 24 * time.Hour

// Check is the daemon's per-step read of the cap, for the steps where no
// delivery is attempted at all (a friend with nothing dealt): it raises the one
// judgment and halts the wired gate when the day's spend has passed the cap or
// the provider refused for funds, exactly as a delivery attempt does
// (docs/SPEC-FRIEND.md, spend caps). cmd/nova-friend calls it from its row hook,
// which the daemon's loop runs before it launches any turn.
func (s *SpendCap) Check(now time.Time) {
	if s.stopped(now) {
		s.judge(now) // the one judgment for the day, raised once
		s.halt(now)
	}
}

// HarnessSpent is a harness's spend so far in USD, as the harness's own usage
// report says it (docs/SPEC-FRIEND.md, spend caps): DSH's record, OpenCodePriced's
// priced runs, or Claude's cost. 0 for a harness that reports none, which is
// never capped by measurement and is stopped by its provider's funds refusal
// instead.
func HarnessSpent(d Deliverer) float64 {
	switch h := d.(type) {
	case interface{ SpentUSD() float64 }:
		return h.SpentUSD()
	case interface{ Spent() float64 }:
		return h.Spent()
	case interface{ Spent() (float64, Usage) }:
		cost, _ := h.Spent()
		return cost
	}
	return 0
}

// fundsRefusal is whether err is a provider refusal for funds (an
// insufficient balance, a 402): the reason, and whether it is one.
func fundsRefusal(err error) (string, bool) {
	var p ProviderRefused
	if errors.As(err, &p) && fundsLine.MatchString(p.Reason) {
		return p.Reason, true
	}
	var o OutOfFunds
	if errors.As(err, &o) {
		return o.Reason, true
	}
	return "", false
}

// SpendCapJudgmentText is what the seat and the owner are told when a
// friend's spend has passed its cap: one judgment naming the friend, the
// provider and the spend.
func SpendCapJudgmentText(friend string, p Pronouns, provider string, spent, cap float64) (subject, body string) {
	subject = fmt.Sprintf("friend %s: %s spend reached $%.2f of its $%.2f/day cap", friend, provider, spent, cap)
	body = p.Sprintf("{Their} %s spend reached $%.2f of its $%.2f/day cap; turns are stopped until the spend day resets. A payment or a higher cap is the owner's (nova-config friend set %s --spend-cap <usd>).\n", provider, spent, cap, friend)
	return subject, body
}

// Gate wraps a Deliverer so that no turn is launched on a day whose measured
// spend has passed the cap, or after a provider refusal for funds: Deliver
// answers Deferred, the message kept in hand, and the one judgment for the
// day is raised. It is used exactly where Limits gates a harness at its limit
// (limit.go), so a spend-cap day stops launching turns the same way a limit
// does. A LaneHarness stays one (spendCappedLanes): its batch Deliver is held,
// and its lanes are not.
func (s *SpendCap) Gate(d Deliverer) Deliverer {
	if _, passive := d.(interface{ Passive() }); passive {
		return d
	}
	g := &spendCapped{c: s, d: d}
	if lh, ok := d.(LaneHarness); ok {
		return &spendCappedLanes{spendCapped: g, lh: lh}
	}
	return g
}

type spendCapped struct {
	c *SpendCap
	d Deliverer
}

// idle is the reason a turn is not launched when the friend has no dealt work
// and no live session (docs/SPEC-FRIEND.md, idle). It is not a judgment and it
// names no spend.
const idle = "idle: the friend has no dealt work and no live session; no turns launch"

// isSessionCheck is text the daemon's liveness check carries: the one turn kept
// even when idle, so the daemon can tell a live session from a dead one.
func isSessionCheck(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), SessionCheckPrefix)
}

func (g *spendCapped) Deliver(ctx context.Context, text string) (int, error) {
	now := time.Now()
	if g.c.Now != nil {
		now = g.c.Now()
	}
	if g.c.Idle != nil && g.c.Idle() && !isSessionCheck(text) {
		return 0, Deferred{Reason: idle}
	}
	if g.c.stopped(now) {
		g.c.judge(now)
		g.c.halt(now)
		return 0, Deferred{Reason: g.c.Judged(now)}
	}
	exit, err := g.d.Deliver(ctx, text)
	if reason, ok := fundsRefusal(err); ok {
		g.c.Refuse(reason)
		g.c.halt(now)
		return exit, Deferred{Reason: g.c.Judged(now)}
	}
	return exit, err
}

// spendCappedLanes is a LaneHarness under the spend cap: Deliver, the batch
// turn and the session check, is spendCapped's. OpenSession and DeliverTo, the
// one-shot lanes, answer the same way as Deliver when the friend is idle, the
// day's measured spend has passed the cap, or the provider refused for funds: a
// lane launches no turn under the cap either (docs/SPEC-FRIEND.md, spend caps).
type spendCappedLanes struct {
	*spendCapped
	lh LaneHarness
}

func (g *spendCappedLanes) OpenSession(ctx context.Context, seed string) (string, error) {
	now := time.Now()
	if g.c.Now != nil {
		now = g.c.Now()
	}
	if g.c.Idle != nil && g.c.Idle() && !isSessionCheck(seed) {
		return "", Deferred{Reason: idle}
	}
	if g.c.stopped(now) {
		g.c.judge(now)
		g.c.halt(now)
		return "", Deferred{Reason: g.c.Judged(now)}
	}
	return g.lh.OpenSession(ctx, seed)
}

func (g *spendCappedLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	now := time.Now()
	if g.c.Now != nil {
		now = g.c.Now()
	}
	if g.c.Idle != nil && g.c.Idle() && !isSessionCheck(text) {
		return LaneTurn{}, Deferred{Reason: idle}
	}
	if g.c.stopped(now) {
		g.c.judge(now)
		g.c.halt(now)
		return LaneTurn{}, Deferred{Reason: g.c.Judged(now)}
	}
	turn, err := g.lh.DeliverTo(ctx, session, text)
	if reason, ok := fundsRefusal(err); ok {
		g.c.Refuse(reason)
		g.c.halt(now)
		return turn, Deferred{Reason: g.c.Judged(now)}
	}
	return turn, err
}
