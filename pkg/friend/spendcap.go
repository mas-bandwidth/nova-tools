package friend

import (
	"context"
	"fmt"
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
	// spend. It is called once per spend day until the day resets.
	Judgment func(dayStart time.Time, spent, cap float64) string

	mu    sync.Mutex
	judged dayStart // the day the judgment was raised for; zero: none
	text   string   // the judgment's line, for that day
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

// Gate wraps a Deliverer so that no turn is launched on a day whose measured
// spend has passed the cap: Deliver answers Deferred, the message kept in hand,
// and the one judgment for the day is raised. It is used exactly where Limits
// gates a harness at its limit (limit.go), so a spend-cap day stops launching
// turns the same way a limit does.
func (s *SpendCap) Gate(d Deliverer) Deliverer {
	return &spendCapped{c: s, d: d}
}

type spendCapped struct {
	c *SpendCap
	d Deliverer
}

func (g *spendCapped) Deliver(ctx context.Context, text string) (int, error) {
	now := time.Now()
	if g.c.Now != nil {
		now = g.c.Now()
	}
	day := g.c.dayStartOf(now)
	if g.c.over(day) {
		g.c.judge(now)
		return 0, Deferred{Reason: g.c.Judged(now)}
	}
	return g.d.Deliver(ctx, text)
}
