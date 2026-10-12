package friend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
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
	// StateDir stores measured per-provider daily totals and the stop reason so
	// a daemon restart cannot reset the cap. Empty is for isolated unit tests.
	StateDir string
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
	// Usage is the harness's measured spend and whether it can report it. When
	// set, an unavailable report fails closed; the daemon never estimates cost.
	Usage func(dayStart time.Time) (spent float64, known bool)
	// Judgment is the one judgment raised to the seat and the owner when the cap
	// is passed; it returns the line naming the friend, the provider and the
	// spend. It is called once per spend day until the day resets.
	Judgment func(dayStart time.Time, spent, cap float64) string
	// OnJudgment publishes the once-per-day text after it is recorded.
	OnJudgment func(text string)

	mu         sync.Mutex
	judged     dayStart // the judgment was raised for; zero: none
	text       string   // the judgment's line, for that day
	ledgerMu   sync.Mutex
	ledgerRead bool
	ledgerErr  error
	ledger     spendLedger
	base       map[string]float64 // persisted amount at daemon start; source usage is added to it
}

type spendLedger struct {
	Spend   map[string]float64 `json:"spend_usd"`
	Stopped map[string]string  `json:"stopped"`
}

const SpendLedgerFile = "spend.json"

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

func spendKey(provider string, day dayStart) string {
	return provider + "/" + time.Time(day).UTC().Format("2006-01-02")
}

func (s *SpendCap) loadLedger() {
	if s.StateDir == "" {
		return
	}
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgerRead {
		return
	}
	s.ledgerRead = true
	var l spendLedger
	found, err := read(filepath.Join(s.StateDir, SpendLedgerFile), &l)
	if err != nil {
		s.ledgerErr = err
		return
	}
	if found {
		for key, value := range l.Spend {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				s.ledgerErr = fmt.Errorf("%s: invalid spend value for %s", SpendLedgerFile, key)
				return
			}
		}
	}
	if l.Spend == nil {
		l.Spend = map[string]float64{}
	}
	if l.Stopped == nil {
		l.Stopped = map[string]string{}
	}
	s.ledger = l
	s.base = make(map[string]float64, len(l.Spend))
	for key, value := range l.Spend {
		s.base[key] = value
	}
}

func (s *SpendCap) ledgerFailure() error {
	s.loadLedger()
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	return s.ledgerErr
}

// spentOf combines the last daemon's durable total with this process's own
// measured usage report for the UTC spend day.
func (s *SpendCap) spentOf(day dayStart) (float64, bool) {
	s.loadLedger()
	s.ledgerMu.Lock()
	key := spendKey(s.Provider, day)
	if s.ledgerErr != nil {
		s.ledgerMu.Unlock()
		return 0, false
	}
	prior := s.base[key]
	s.ledgerMu.Unlock()
	if s.Usage != nil {
		current, known := s.Usage(time.Time(day))
		return prior + current, known
	}
	if s.Spent == nil {
		return prior, s.StateDir == ""
	}
	return prior + s.Spent(time.Time(day)), true
}

func (s *SpendCap) persistSpend(day dayStart) error {
	if s.StateDir == "" {
		return nil
	}
	s.loadLedger()
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgerErr != nil {
		return s.ledgerErr
	}
	var current float64
	var known bool
	if s.Usage != nil {
		current, known = s.Usage(time.Time(day))
	} else if s.Spent != nil {
		current, known = s.Spent(time.Time(day)), true
	}
	if !known || current < 0 || math.IsNaN(current) || math.IsInf(current, 0) {
		return fmt.Errorf("harness usage is unavailable; refusing to persist an estimated spend")
	}
	key := spendKey(s.Provider, day)
	base := s.base[key]
	s.ledger.Spend[key] = max(s.ledger.Spend[key], base+current)
	if err := write(filepath.Join(s.StateDir, SpendLedgerFile), s.ledger); err != nil {
		s.ledgerErr = err
		return err
	}
	return nil
}

func (s *SpendCap) stopped(day dayStart) (string, error) {
	s.loadLedger()
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgerErr != nil {
		return "", s.ledgerErr
	}
	return s.ledger.Stopped[spendKey(s.Provider, day)], nil
}

func (s *SpendCap) saveStop(day dayStart, reason string) error {
	if s.StateDir == "" {
		return nil
	}
	s.loadLedger()
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgerErr != nil {
		return s.ledgerErr
	}
	key := spendKey(s.Provider, day)
	s.ledger.Stopped[key] = reason
	if err := write(filepath.Join(s.StateDir, SpendLedgerFile), s.ledger); err != nil {
		s.ledgerErr = err
		return err
	}
	return nil
}

// judgmentText is the judgment's line for the day: the judgment hook's own
// wording when it is set, else the default naming the friend, the provider and
// the spend.
func (s *SpendCap) judgmentText(day dayStart) string {
	if s.Judgment != nil {
		spent, known := s.spentOf(day)
		if !known {
			spent = 0
		}
		return s.Judgment(time.Time(day), spent, s.capOf())
	}
	friend, provider := s.Friend, s.Provider
	if friend == "" {
		friend = "friend"
	}
	if provider == "" {
		provider = "the provider"
	}
	spent, known := s.spentOf(day)
	if !known {
		if err := s.ledgerFailure(); err != nil {
			return fmt.Sprintf("%s's %s spend ledger cannot be read or written: %v; turns are stopped", friend, provider, err)
		}
		return fmt.Sprintf("%s's %s usage is unavailable from the harness; turns are stopped because spend cannot be measured against the $%.2f/day cap", friend, provider, s.capOf())
	}
	return fmt.Sprintf("%s's %s spend is $%.2f of its $%.2f/day cap; turns are stopped until the spend day resets", friend, provider, spent, s.capOf())
}

// over is whether the day's measured spend has passed the cap.
func (s *SpendCap) over(day dayStart) bool {
	spent, known := s.spentOf(day)
	return known && spent >= s.capOf()
}

// judge raises the one judgment for the day, once, when the spend has passed the
// cap. It reports whether the judgment was raised now.
func (s *SpendCap) judge(now time.Time, forced bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Now == nil {
		now = time.Now()
	}
	day := s.dayStartOf(now)
	if s.judged == day || (!forced && !s.over(day)) {
		return false
	}
	if text, err := s.stopped(day); err == nil && text != "" {
		s.judged, s.text = day, text
		return false
	}
	s.judged = day
	s.text = s.judgmentText(day)
	if err := s.persistSpend(day); err != nil {
		s.text = fmt.Sprintf("%s; persistent spend ledger write failed: %v; turns are stopped", s.text, err)
	}
	if err := s.saveStop(day, s.text); err != nil {
		s.text = fmt.Sprintf("%s; persistent spend ledger write failed: %v; turns are stopped", s.text, err)
	}
	if s.OnJudgment != nil {
		s.OnJudgment(s.text)
	}
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
	return s.wrap(d)
}

type spendCapped struct {
	c *SpendCap
	d Deliverer
}

// SpendCapReached is a lane turn held because the provider's measured spend
// reached its daily cap or the harness cannot report measured usage.
type SpendCapReached struct {
	Reason string
	Until  time.Time // zero means usage is unknown and remains stopped until corrected
}

func (e SpendCapReached) Error() string { return e.Reason }

type spendCappedLanes struct {
	spendCapped
	lanes LaneHarness
}

func (g *spendCappedLanes) OpenSession(ctx context.Context, seed string) (string, error) {
	return g.lanes.OpenSession(ctx, seed)
}

func (g *spendCappedLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	if err := g.stopReason(); err != nil {
		return LaneTurn{}, err
	}
	turn, err := g.lanes.DeliverTo(ctx, session, text)
	if saveErr := g.c.persistSpend(g.c.dayStartOf(g.now())); saveErr != nil {
		g.c.judge(g.now(), true)
		return turn, SpendCapReached{Reason: "the measured spend could not be saved: " + saveErr.Error()}
	}
	var funds OutOfFunds
	if errors.As(err, &funds) {
		g.c.judge(g.now(), true)
		return turn, SpendCapReached{Reason: g.c.Judged(g.now()), Until: time.Time(g.c.dayStartOf(g.now())).Add(24 * time.Hour)}
	}
	return turn, err
}

func (g *spendCapped) now() time.Time {
	if g.c.Now != nil {
		return g.c.Now()
	}
	return time.Now()
}

func (g *spendCapped) stopReason() error {
	now := g.now()
	day := g.c.dayStartOf(now)
	if prior, err := g.c.stopped(day); err != nil {
		g.c.judge(now, true)
		return SpendCapReached{Reason: fmt.Sprintf("the persistent spend ledger cannot be read: %v; turns are stopped", err)}
	} else if prior != "" {
		g.c.judge(now, true) // restores the persisted one-judgment state without sending it again
		return SpendCapReached{Reason: prior, Until: time.Time(day).Add(24 * time.Hour)}
	}
	spent, known := g.c.spentOf(day)
	if g.c.Judged(now) == "" && known && spent < g.c.capOf() {
		return nil
	}
	g.c.judge(now, true)
	until := time.Time{}
	if known {
		until = time.Time(day).Add(24 * time.Hour)
	}
	return SpendCapReached{Reason: g.c.Judged(now), Until: until}
}

func (g *spendCapped) Deliver(ctx context.Context, text string) (int, error) {
	if err := g.stopReason(); err != nil {
		return 0, Deferred{Reason: err.Error()}
	}
	n, err := g.d.Deliver(ctx, text)
	if saveErr := g.c.persistSpend(g.c.dayStartOf(g.now())); saveErr != nil {
		g.c.judge(g.now(), true)
		return n, Deferred{Reason: "the measured spend could not be saved; turns are stopped: " + saveErr.Error()}
	}
	var funds OutOfFunds
	if errors.As(err, &funds) {
		g.c.judge(g.now(), true)
		return n, funds
	}
	return n, err
}

// Gate also wraps a one-shot card runner: its cards bypass Deliver and must
// pass through the same spend preflight and provider-funds stop.
func (s *SpendCap) wrap(d Deliverer) Deliverer {
	if runner, ok := d.(CardRunner); ok {
		return &spendCappedRunner{spendCapped: spendCapped{c: s, d: d}, runner: runner}
	}
	if lanes, ok := d.(LaneHarness); ok {
		return &spendCappedLanes{spendCapped: spendCapped{c: s, d: d}, lanes: lanes}
	}
	return &spendCapped{c: s, d: d}
}

type spendCappedRunner struct {
	spendCapped
	runner CardRunner
}

func (r *spendCappedRunner) RunCard(ctx context.Context, c Card) (LaneTurn, error) {
	if err := r.stopReason(); err != nil {
		return LaneTurn{}, err
	}
	turn, err := r.runner.RunCard(ctx, c)
	if saveErr := r.c.persistSpend(r.c.dayStartOf(r.now())); saveErr != nil {
		r.c.judge(r.now(), true)
		return turn, SpendCapReached{Reason: "the measured spend could not be saved: " + saveErr.Error()}
	}
	var funds OutOfFunds
	if errors.As(err, &funds) {
		r.c.judge(r.now(), true)
		return turn, SpendCapReached{Reason: r.c.Judged(r.now()), Until: time.Time(r.c.dayStartOf(r.now())).Add(24 * time.Hour)}
	}
	return turn, err
}

func (r *spendCappedRunner) Refusal() string { return r.runner.Refusal() }
