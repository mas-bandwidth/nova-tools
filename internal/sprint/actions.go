package sprint

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrInvalidState indicates an invalid state transition for the current lifecycle position.
var ErrInvalidState = errors.New("refusal: invalid state transition for current lifecycle position")

// CIOk is an alias for CIOK.
const CIOk = CIOK

// ReviewVerdict encapsulates evaluation details when a reader completes a review.
type ReviewVerdict struct {
	Score       float64     `json:"score"`
	CI          CIWord      `json:"ci,omitempty"`
	Comments    string      `json:"comments,omitempty"`
	Path        ReadLowPath `json:"path,omitempty"`
	FixConsumer ConsumerID  `json:"fix_consumer,omitempty"`
}

// ParseReviewVerdict parses diverse verdict representations into a typed ReviewVerdict.
// Supports float64/int scores, string keywords ("approved", "pass", "ok", "changes_requested"),
// or full ReviewVerdict instances.
func ParseReviewVerdict(v any) ReviewVerdict {
	if v == nil {
		return ReviewVerdict{}
	}
	switch val := v.(type) {
	case ReviewVerdict:
		return val
	case *ReviewVerdict:
		if val != nil {
			return *val
		}
		return ReviewVerdict{}
	case float64:
		return ReviewVerdict{Score: val, CI: CIOK}
	case float32:
		return ReviewVerdict{Score: float64(val), CI: CIOK}
	case int:
		return ReviewVerdict{Score: float64(val), CI: CIOK}
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		switch s {
		case "pass", "ok", "approved", "high":
			return ReviewVerdict{Score: 9.0, CI: CIOK, Comments: val}
		case "low", "changes_requested", "reject", "fail":
			return ReviewVerdict{Score: 5.0, Comments: val}
		default:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				ci := CIOK
				if f < 8.0 {
					ci = ""
				}
				return ReviewVerdict{Score: f, CI: ci}
			}
			return ReviewVerdict{Comments: val}
		}
	default:
		return ReviewVerdict{}
	}
}

// ============================================================================
// Action Configuration & Functional Options
// ============================================================================

// ActionConfig defines execution options for card transition rules.
type ActionConfig struct {
	DepsMet          bool
	HasDepsMet       bool
	DepsValidator    func(CardID) bool
	Capacity         *ConsumerCapacity
	Consumers        map[ConsumerID]*ConsumerCapacity
	Copies           map[string]*CopyRecord
	ToLanded         bool
	Outcome          CardOutcome
	Verdict          VerdictType
	ReassignConsumer ConsumerID
	Reason           string
	Score            float64
	Now              time.Time
}

// ActionOption is a functional option configuring an ActionConfig.
type ActionOption func(*ActionConfig)

// WithDepsMet explicitly declares whether card dependencies are satisfied.
func WithDepsMet(met bool) ActionOption {
	return func(c *ActionConfig) {
		c.DepsMet = met
		c.HasDepsMet = true
	}
}

// WithDepsValidator provides a custom predicate for validating card dependencies.
func WithDepsValidator(fn func(CardID) bool) ActionOption {
	return func(c *ActionConfig) {
		c.DepsValidator = fn
	}
}

// WithConsumerCapacity supplies capacity information for a specific consumer.
func WithConsumerCapacity(cap *ConsumerCapacity) ActionOption {
	return func(c *ActionConfig) {
		c.Capacity = cap
	}
}

// WithConsumerRegistry provides a map of consumers for capacity checks.
func WithConsumerRegistry(consumers map[ConsumerID]*ConsumerCapacity) ActionOption {
	return func(c *ActionConfig) {
		c.Consumers = consumers
	}
}

// WithCopyRegistry provides a map of copy records for dual-table synchronization.
func WithCopyRegistry(copies map[string]*CopyRecord) ActionOption {
	return func(c *ActionConfig) {
		c.Copies = copies
	}
}

// WithToLanded directs EndWorkDone to land the card directly (done-already flow).
func WithToLanded(toLanded bool) ActionOption {
	return func(c *ActionConfig) {
		c.ToLanded = toLanded
	}
}

// WithOutcome specifies the outcome for terminal transitions (OutcomeOK or OutcomeFail).
func WithOutcome(outcome CardOutcome) ActionOption {
	return func(c *ActionConfig) {
		c.Outcome = outcome
	}
}

// WithVerdictType specifies the verdict resolution (VerdictRecut, VerdictRedeal, VerdictDrop, VerdictReassign).
func WithVerdictType(v VerdictType) ActionOption {
	return func(c *ActionConfig) {
		c.Verdict = v
	}
}

// WithReassignConsumer specifies the target consumer for VerdictReassign.
func WithReassignConsumer(k ConsumerID) ActionOption {
	return func(c *ActionConfig) {
		c.ReassignConsumer = k
	}
}

// WithReason provides an explanatory cancellation or failure reason.
func WithReason(reason string) ActionOption {
	return func(c *ActionConfig) {
		c.Reason = reason
	}
}

// WithScore provides an explicit numeric review score.
func WithScore(score float64) ActionOption {
	return func(c *ActionConfig) {
		c.Score = score
	}
}

// WithNow overrides the current timestamp for deterministic testing.
func WithNow(t time.Time) ActionOption {
	return func(c *ActionConfig) {
		c.Now = t
	}
}

func applyOptions(opts []ActionOption) ActionConfig {
	cfg := ActionConfig{
		DepsMet: true, // Default to true unless dependencies are specified/checked
		Now:     time.Now(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// ============================================================================
// State Transition Rules & Helper Functions
// ============================================================================

// Release (Action 2): Moves a primary card from "waiting" to "ready" when all dependencies are closed.
// Enforces DepsMet and ReadyIsOneWay (Finding 2 fix: once ready, cannot regress to waiting).
func Release(card *CardRecord, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	// Invariant: ReadyIsOneWay — ready cards cannot be released or regressed back to waiting.
	if card.State == CardStateReady {
		return ErrReadyIsOneWay
	}

	// Only cards in waiting may transition to ready.
	if card.State != CardStateWaiting {
		return ErrInvalidState
	}

	// Validate dependencies
	if cfg.HasDepsMet && !cfg.DepsMet {
		return ErrDepsNotMet
	}
	if cfg.DepsValidator != nil && !cfg.DepsValidator(card.ID) {
		return ErrDepsNotMet
	}

	card.State = CardStateReady
	card.Placement = StreamPlacement(card.Stream, CardStateReady)
	card.UpdatedAt = cfg.Now
	return nil
}

// DealRead (Action 4): Assigns a reader to review a card currently in "review".
// Primary remains in "review" and names the cut read copy.
// Enforces ReadsInReview, author self-review refusal (AuthorSelfRead), and consumer capacity (Room > 0).
func DealRead(card *CardRecord, reader ConsumerID, opts ...ActionOption) (*CopyRecord, error) {
	if card == nil {
		return nil, ErrCardNotFound
	}
	cfg := applyOptions(opts)

	// Invariant: Reads allowed only when primary is in "review"
	if card.State != CardStateReview {
		return nil, ErrReadsInReview
	}

	// Invariant: Author cannot review their own PR
	if reader != "" && reader == card.Author {
		return nil, ErrAuthorSelfRead
	}

	// Check consumer capacity if provided
	if cfg.Capacity != nil {
		if !cfg.Capacity.Up || cfg.Capacity.Room() <= 0 {
			return nil, ErrNoRoom
		}
		cfg.Capacity.Ready++
	} else if cfg.Consumers != nil && reader != "" {
		if cap, ok := cfg.Consumers[reader]; ok {
			if !cap.Up || cap.Room() <= 0 {
				return nil, ErrNoRoom
			}
			cap.Ready++
		}
	}

	attempt := card.NCut + 1
	card.NCut = attempt
	copyID := CopyID{Card: card.ID, Attempt: attempt}

	// Record read copy in primary's LiveReads
	card.LiveReads = append(card.LiveReads, copyID)
	card.UpdatedAt = cfg.Now

	copyRec := &CopyRecord{
		ID:        copyID,
		Epoch:     card.Epoch,
		Primary:   card.ID,
		Consumer:  reader,
		Leg:       LegRead,
		State:     CopyStateReady,
		Placement: FleetPlacement(reader, CopyStateReady),
		Attempt:   attempt,
		Leased:    false,
		Score:     cfg.Score,
		CreatedAt: cfg.Now,
		UpdatedAt: cfg.Now,
	}

	if cfg.Copies != nil {
		cfg.Copies[copyID.String()] = copyRec
	}

	return copyRec, nil
}

// EndWorkDone (Action 6): Completes a work copy with direct resolution (no PR required).
// Primary transitions working -> done (or landed if ToLanded is set).
// Enforces LiveCopyNamed and TerminalIsQuiet.
func EndWorkDone(card *CardRecord, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	if card.State.IsTerminal() {
		return ErrTerminalIsQuiet
	}
	if card.State != CardStateWorking {
		return ErrInvalidState
	}

	// Working primary must have a live copy
	if card.LiveCopy == nil {
		return ErrLiveCopyNamed
	}

	targetState := CardStateDone
	if cfg.ToLanded {
		targetState = CardStateLanded
	}

	outcome := OutcomeOK
	if cfg.Outcome != "" {
		outcome = cfg.Outcome
	}

	// Retire work copy in fleet
	if cfg.Copies != nil && card.LiveCopy != nil {
		if copyRec, ok := cfg.Copies[card.LiveCopy.String()]; ok {
			copyRec.State = CopyStateOK
			copyRec.Placement = FleetPlacement(copyRec.Consumer, CopyStateOK)
			copyRec.UpdatedAt = cfg.Now
			if cfg.Consumers != nil {
				if cap, ok := cfg.Consumers[copyRec.Consumer]; ok && cap.Working > 0 {
					cap.Working--
				}
			}
		}
	}

	card.State = targetState
	card.Placement = StreamPlacement(card.Stream, targetState)
	card.Outcome = outcome
	card.LiveCopy = nil
	card.UpdatedAt = cfg.Now
	return nil
}

// EndWorkPR (Action 5): Completes work copy with a PR, moving primary from "working" to "review".
// Advances PRHead, sets author, marks CI pending, and retires the work copy to "ok".
func EndWorkPR(card *CardRecord, pr any, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	if card.State != CardStateWorking {
		return ErrInvalidState
	}

	if card.LiveCopy == nil {
		return ErrLiveCopyNamed
	}

	// Retire work copy in fleet and associate author
	if cfg.Copies != nil && card.LiveCopy != nil {
		if copyRec, ok := cfg.Copies[card.LiveCopy.String()]; ok {
			copyRec.State = CopyStateOK
			copyRec.Placement = FleetPlacement(copyRec.Consumer, CopyStateOK)
			copyRec.UpdatedAt = cfg.Now
			if copyRec.Consumer != "" {
				card.Author = copyRec.Consumer
			}
			if cfg.Consumers != nil {
				if cap, ok := cfg.Consumers[copyRec.Consumer]; ok && cap.Working > 0 {
					cap.Working--
				}
			}
		}
	}

	card.State = CardStateReview
	card.Placement = StreamPlacement(card.Stream, CardStateReview)
	card.LiveCopy = nil
	card.PRHead++
	card.Head = card.PRHead
	card.CI = CIPending
	card.UpdatedAt = cfg.Now
	return nil
}

// EndReadHigh (Action 7): Passes reader review with score >= 8.0 and CI OK.
// Primary transitions review -> merging.
// Enforces Fanout Cleanup & TerminalIsQuiet preparation:
// reporting reader moves to "ok"; all other open reads and active fix copies are retired to "fail".
func EndReadHigh(card *CardRecord, reader ConsumerID, verdict any, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	if card.State != CardStateReview {
		return ErrInvalidState
	}

	// Validate PR head
	if card.Head != card.PRHead {
		return ErrStaleHead
	}

	v := ParseReviewVerdict(verdict)
	if cfg.Score != 0 {
		v.Score = cfg.Score
	}

	// Score threshold verification (must be >= 8.0)
	if v.Score < 8.0 && v.Score != 0 {
		return errors.New("refusal: read score below threshold (must be >= 8.0 for EndReadHigh)")
	}

	// CI check: CI cannot be red
	if card.CI == CIRed || v.CI == CIRed {
		return errors.New("refusal: CI is red; cannot advance to merging")
	}

	// Verify that a live read copy exists for this reader (or in LiveReads)
	var reportingCopyID *CopyID
	if len(card.LiveReads) > 0 {
		if cfg.Copies != nil {
			for _, rID := range card.LiveReads {
				if cp, ok := cfg.Copies[rID.String()]; ok && cp.Consumer == reader {
					copyCopy := rID
					reportingCopyID = &copyCopy
					break
				}
			}
		}
		if reportingCopyID == nil {
			// If not found by consumer in copies map, take first reader copy
			copyCopy := card.LiveReads[0]
			reportingCopyID = &copyCopy
		}
	} else {
		return ErrCopyNotFound
	}

	// Primary transitions to merging
	card.State = CardStateMerging
	card.Placement = StreamPlacement(card.Stream, CardStateMerging)
	card.CI = CIOK

	// Fanout cleanup across fleet copies:
	if cfg.Copies != nil {
		// 1. Reporting reader moves to OK
		if reportingCopyID != nil {
			if cp, ok := cfg.Copies[reportingCopyID.String()]; ok {
				cp.State = CopyStateOK
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateOK)
				cp.UpdatedAt = cfg.Now
			}
		}

		// 2. All other reads retired to fail
		for _, rID := range card.LiveReads {
			if reportingCopyID != nil && rID.String() == reportingCopyID.String() {
				continue
			}
			if cp, ok := cfg.Copies[rID.String()]; ok && cp.State.IsLive() {
				cp.State = CopyStateFail
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
				cp.UpdatedAt = cfg.Now
			}
		}

		// 3. Active fix copy retired to fail
		if card.LiveCopy != nil {
			if cp, ok := cfg.Copies[card.LiveCopy.String()]; ok && cp.State.IsLive() {
				cp.State = CopyStateFail
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
				cp.UpdatedAt = cfg.Now
			}
		}
	}

	card.LiveReads = []CopyID{}
	card.LiveCopy = nil
	card.UpdatedAt = cfg.Now
	return nil
}

// EndReadLow (Action 8): Handles review branches when a reader scores < 8.0, requesting changes.
// Retires reporting reader to "ok", increments low read counter, and branches into:
//   - Path A  (low >= 1): 2nd low read; enters pending review (pending=true).
//   - Path B  (low == 0, active fix): joins existing fix brief.
//   - Path C1 (low == 0, no fix, author up): cuts fresh fix copy on author in ready.
//   - Path C2 (low == 0, no fix, author down): returns primary to waiting.
func EndReadLow(card *CardRecord, reader ConsumerID, verdict any, opts ...ActionOption) (*EndReadLowResult, error) {
	if card == nil {
		return nil, ErrCardNotFound
	}
	cfg := applyOptions(opts)

	if card.State != CardStateReview {
		return nil, ErrInvalidState
	}

	if card.Head != card.PRHead {
		return nil, ErrStaleHead
	}

	v := ParseReviewVerdict(verdict)
	if cfg.Score != 0 {
		v.Score = cfg.Score
	}

	// Score threshold verification (must be < 8.0)
	if v.Score >= 8.0 {
		return nil, errors.New("refusal: read score is 8.0 or above (must be < 8.0 for EndReadLow)")
	}

	// Find reporting reader copy
	var reportingIndex = -1
	var reportingCopyID *CopyID
	for i, rID := range card.LiveReads {
		if cfg.Copies != nil {
			if cp, ok := cfg.Copies[rID.String()]; ok && cp.Consumer == reader {
				reportingIndex = i
				copyCopy := rID
				reportingCopyID = &copyCopy
				break
			}
		}
	}
	if reportingIndex == -1 && len(card.LiveReads) > 0 {
		reportingIndex = 0
		copyCopy := card.LiveReads[0]
		reportingCopyID = &copyCopy
	}
	if reportingIndex == -1 {
		return nil, ErrCopyNotFound
	}

	// Retire reporting reader to ok
	if cfg.Copies != nil && reportingCopyID != nil {
		if cp, ok := cfg.Copies[reportingCopyID.String()]; ok {
			cp.State = CopyStateOK
			cp.Placement = FleetPlacement(cp.Consumer, CopyStateOK)
			cp.UpdatedAt = cfg.Now
		}
	}

	// Remove from LiveReads
	card.LiveReads = append(card.LiveReads[:reportingIndex], card.LiveReads[reportingIndex+1:]...)

	prevLow := card.LowReads
	card.LowReads++
	res := &EndReadLowResult{}

	// Evaluate branching path:
	if v.Path == ReadLowPathA || prevLow >= 1 {
		// Path A: Second low read, enters pending review
		card.Pending = true
		card.LiveCopy = nil
		res.Path = ReadLowPathA
		res.TargetState = CardStateReview
	} else if v.Path == ReadLowPathB || card.LiveCopy != nil {
		// Path B: A fix copy is already out; joins existing brief
		card.Pending = false
		res.Path = ReadLowPathB
		res.TargetState = CardStateReview
	} else {
		// Path C: No fix copy yet. Determine whether author has capacity/route.
		targetAuthor := card.Author
		if v.FixConsumer != "" {
			targetAuthor = v.FixConsumer
		}

		authorUp := targetAuthor != ""
		if cfg.Consumers != nil && targetAuthor != "" {
			if cap, ok := cfg.Consumers[targetAuthor]; ok {
				authorUp = cap.Up && cap.Room() > 0
			}
		}

		if v.Path == ReadLowPathC2 || !authorUp {
			// Path C2: Author unavailable or no fix route; primary returns to waiting
			card.State = CardStateWaiting
			card.Placement = StreamPlacement(card.Stream, CardStateWaiting)
			card.LiveCopy = nil
			card.Pending = false
			res.Path = ReadLowPathC2
			res.TargetState = CardStateWaiting
		} else {
			// Path C1: Cut fix copy on author in ready
			attempt := card.NCut + 1
			card.NCut = attempt
			fixCopyID := CopyID{Card: card.ID, Attempt: attempt}
			card.LiveCopy = &fixCopyID
			card.Pending = false
			res.Path = ReadLowPathC1
			res.TargetState = CardStateReview
			res.FixCopy = &fixCopyID

			if cfg.Copies != nil {
				fixRec := &CopyRecord{
					ID:        fixCopyID,
					Epoch:     card.Epoch,
					Primary:   card.ID,
					Consumer:  targetAuthor,
					Leg:       LegFix,
					State:     CopyStateReady,
					Placement: FleetPlacement(targetAuthor, CopyStateReady),
					Attempt:   attempt,
					CreatedAt: cfg.Now,
					UpdatedAt: cfg.Now,
				}
				cfg.Copies[fixCopyID.String()] = fixRec
			}

			if cfg.Consumers != nil {
				if cap, ok := cfg.Consumers[targetAuthor]; ok {
					cap.Ready++
				}
			}
		}
	}

	card.UpdatedAt = cfg.Now
	return res, nil
}

// EndFixOK (Action 9): Fix copy completes successfully; moves fix working -> review.
// Retires active fix copy to "ok", retires open reads to "fail", advances PR head, and resets CI to pending.
func EndFixOK(card *CardRecord, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	if card.State != CardStateReview {
		return ErrInvalidState
	}

	if card.LiveCopy == nil {
		return ErrLiveCopyNamed
	}

	// Retire fix copy to ok
	if cfg.Copies != nil && card.LiveCopy != nil {
		if cp, ok := cfg.Copies[card.LiveCopy.String()]; ok {
			if cp.Leg != LegFix && cp.Leg != LegWork {
				return ErrLiveCopyNamed
			}
			cp.State = CopyStateOK
			cp.Placement = FleetPlacement(cp.Consumer, CopyStateOK)
			cp.UpdatedAt = cfg.Now
			if cfg.Consumers != nil {
				if cap, ok := cfg.Consumers[cp.Consumer]; ok && cap.Working > 0 {
					cap.Working--
				}
			}
		}
	}

	// Retire open reads to fail (they must re-read the new fix head)
	if cfg.Copies != nil {
		for _, rID := range card.LiveReads {
			if cp, ok := cfg.Copies[rID.String()]; ok && cp.State.IsLive() {
				cp.State = CopyStateFail
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
				cp.UpdatedAt = cfg.Now
			}
		}
	}

	card.LiveReads = []CopyID{}
	card.LiveCopy = nil
	card.PRHead++
	card.Head = card.PRHead
	card.CI = CIPending
	card.UpdatedAt = cfg.Now
	return nil
}

// Verdict (Action 10): Resolves a card in review (default: review -> merging).
// Also supports coordinator branching via VerdictType:
//   - default: moves card to "merging"
//   - VerdictRecut: moves primary back to "waiting"
//   - VerdictRedeal: moves primary to "ready" for fresh assignment
//   - VerdictDrop: moves primary directly to "landed"
//   - VerdictReassign: moves primary to "working" on the reassign consumer
//
// Leaving review retires any remaining open reads to "fail".
func Verdict(card *CardRecord, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	if card.State != CardStateReview {
		return ErrInvalidState
	}

	verdict := cfg.Verdict

	// Apply verdict state resolution
	switch verdict {
	case VerdictRecut:
		card.State = CardStateWaiting
		card.Placement = StreamPlacement(card.Stream, CardStateWaiting)
		card.LiveCopy = nil
	case VerdictRedeal:
		card.State = CardStateReady
		card.Placement = StreamPlacement(card.Stream, CardStateReady)
		card.LiveCopy = nil
	case VerdictDrop:
		card.State = CardStateLanded
		card.Placement = StreamPlacement(card.Stream, CardStateLanded)
		card.Outcome = OutcomeOK
		card.LiveCopy = nil
	case VerdictReassign:
		if cfg.ReassignConsumer == "" {
			return errors.New("refusal: reassign consumer required for VerdictReassign")
		}
		card.State = CardStateWorking
		card.Placement = StreamPlacement(card.Stream, CardStateWorking)
		attempt := card.NCut + 1
		card.NCut = attempt
		copyID := CopyID{Card: card.ID, Attempt: attempt}
		card.LiveCopy = &copyID
		card.Author = cfg.ReassignConsumer

		if cfg.Copies != nil {
			cfg.Copies[copyID.String()] = &CopyRecord{
				ID:        copyID,
				Epoch:     card.Epoch,
				Primary:   card.ID,
				Consumer:  cfg.ReassignConsumer,
				Leg:       LegWork,
				State:     CopyStateReady,
				Placement: FleetPlacement(cfg.ReassignConsumer, CopyStateReady),
				Attempt:   attempt,
				CreatedAt: cfg.Now,
				UpdatedAt: cfg.Now,
			}
		}
	default:
		// Default action per roadmap and prompt specification: review -> merging
		card.State = CardStateMerging
		card.Placement = StreamPlacement(card.Stream, CardStateMerging)
		card.LiveCopy = nil
	}

	// Retire open reads to fail on leaving review
	if cfg.Copies != nil {
		for _, rID := range card.LiveReads {
			if cp, ok := cfg.Copies[rID.String()]; ok && cp.State.IsLive() {
				cp.State = CopyStateFail
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
				cp.UpdatedAt = cfg.Now
			}
		}
		if verdict != VerdictReassign && card.LiveCopy != nil {
			if cp, ok := cfg.Copies[card.LiveCopy.String()]; ok && cp.State.IsLive() {
				cp.State = CopyStateFail
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
				cp.UpdatedAt = cfg.Now
			}
		}
	}

	card.LiveReads = []CopyID{}
	card.Pending = false
	card.UpdatedAt = cfg.Now
	return nil
}

// CancelPrimary (Action 12): Explicit operator cancellation of a primary card.
// Transitions non-terminal card -> "done" with outcome "fail".
// Retires all live copies and reader copies to "fail", guaranteeing TerminalIsQuiet.
func CancelPrimary(card *CardRecord, opts ...ActionOption) error {
	if card == nil {
		return ErrCardNotFound
	}
	cfg := applyOptions(opts)

	// Invariant: TerminalIsQuiet — terminal cards cannot be cancelled again
	if card.State.IsTerminal() {
		return ErrTerminalIsQuiet
	}

	if card.State == CardStateNull {
		return ErrInvalidState
	}

	// Retire live work/fix copy in fleet
	if cfg.Copies != nil && card.LiveCopy != nil {
		if cp, ok := cfg.Copies[card.LiveCopy.String()]; ok && cp.State.IsLive() {
			cp.State = CopyStateFail
			cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
			cp.UpdatedAt = cfg.Now
		}
	}

	// Retire all open read copies in fleet
	if cfg.Copies != nil {
		for _, rID := range card.LiveReads {
			if cp, ok := cfg.Copies[rID.String()]; ok && cp.State.IsLive() {
				cp.State = CopyStateFail
				cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
				cp.UpdatedAt = cfg.Now
			}
		}
	}

	card.State = CardStateDone
	card.Placement = StreamPlacement(card.Stream, CardStateDone)
	card.Outcome = OutcomeFail
	card.Pending = false
	card.LiveCopy = nil
	card.LiveReads = []CopyID{}
	card.UpdatedAt = cfg.Now
	return nil
}

// ============================================================================
// Section 3: Invariant Validators & Top-Level Helper Functions
// ============================================================================

// CanTransition determines whether a direct state machine transition is structurally valid.
func CanTransition(from CardState, to CardState) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from.IsTerminal() {
		return false // Terminal states are immutable
	}

	switch from {
	case CardStateNull:
		return to == CardStateWaiting
	case CardStateWaiting:
		return to == CardStateReady || to == CardStateWorking || to == CardStateDone
	case CardStateReady:
		// ReadyIsOneWay: Cannot transition back to waiting
		return to == CardStateWorking || to == CardStateDone
	case CardStateWorking:
		return to == CardStateReview || to == CardStateDone || to == CardStateLanded
	case CardStateReview:
		return to == CardStateMerging || to == CardStateWaiting || to == CardStateReady ||
			to == CardStateWorking || to == CardStateLanded || to == CardStateDone
	case CardStateMerging:
		return to == CardStateLanded || to == CardStateDone
	default:
		return false
	}
}

// ValidateReadyIsOneWay checks if a proposed transition violates the ReadyIsOneWay invariant.
func ValidateReadyIsOneWay(current, target CardState) error {
	if current == CardStateReady && target == CardStateWaiting {
		return ErrReadyIsOneWay
	}
	return nil
}

// ValidateWorkingHasCopy verifies that a working primary names a valid live copy.
func ValidateWorkingHasCopy(card *CardRecord, copyRec *CopyRecord) error {
	if card.State == CardStateWorking {
		if card.LiveCopy == nil || copyRec == nil || !copyRec.State.IsLive() {
			return ErrWorkingHasCopy
		}
	}
	return nil
}

// ValidateLiveCopyNamed verifies that a live copy is properly named by its primary.
func ValidateLiveCopyNamed(card *CardRecord, copyRec *CopyRecord) error {
	if copyRec != nil && copyRec.State.IsLive() {
		if card.LiveCopy == nil && len(card.LiveReads) == 0 {
			return ErrLiveCopyNamed
		}
	}
	return nil
}

// ValidateTerminalQuiet verifies that a terminal card has no live copies.
func ValidateTerminalQuiet(card *CardRecord, copies map[string]*CopyRecord) error {
	if card.State.IsTerminal() || card.State == CardStateMerging {
		if card.LiveCopy != nil {
			return ErrTerminalIsQuiet
		}
		if len(card.LiveReads) > 0 {
			return ErrTerminalIsQuiet
		}
		if copies != nil {
			for _, cp := range copies {
				if cp.Primary == card.ID && cp.State.IsLive() {
					return ErrTerminalIsQuiet
				}
			}
		}
	}
	return nil
}

// NewCardRecord initializes a standard CardRecord in the waiting state.
func NewCardRecord(id CardID, stream string, author ConsumerID) *CardRecord {
	now := time.Now()
	return &CardRecord{
		ID:        id,
		Stream:    stream,
		State:     CardStateWaiting,
		Placement: StreamPlacement(stream, CardStateWaiting),
		Outcome:   OutcomeUnset,
		Author:    author,
		LiveReads: []CopyID{},
		CI:        CIPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewCopyRecord initializes a standard CopyRecord for a card attempt.
func NewCopyRecord(cardID CardID, attempt int, consumer ConsumerID, leg Leg, state CopyState) *CopyRecord {
	now := time.Now()
	copyID := CopyID{Card: cardID, Attempt: attempt}
	return &CopyRecord{
		ID:        copyID,
		Primary:   cardID,
		Consumer:  consumer,
		Leg:       leg,
		State:     state,
		Placement: FleetPlacement(consumer, state),
		Attempt:   attempt,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ============================================================================
// Section 4: CardStateMachine Coordinator
// ============================================================================

// CardStateMachine coordinates dual-table state machine transitions across cards, copies,
// consumer capacities, and dependency graphs.
type CardStateMachine struct {
	mu           sync.RWMutex
	cards        map[CardID]*CardRecord
	copies       map[string]*CopyRecord
	consumers    map[ConsumerID]*ConsumerCapacity
	dependencies map[CardID][]CardID
}

// NewCardStateMachine initializes an empty CardStateMachine.
func NewCardStateMachine() *CardStateMachine {
	return &CardStateMachine{
		cards:        make(map[CardID]*CardRecord),
		copies:       make(map[string]*CopyRecord),
		consumers:    make(map[ConsumerID]*ConsumerCapacity),
		dependencies: make(map[CardID][]CardID),
	}
}

// AddCard registers a primary card with the state machine.
func (sm *CardStateMachine) AddCard(card *CardRecord) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cards[card.ID] = card
}

// GetCard retrieves a primary card record.
func (sm *CardStateMachine) GetCard(id CardID) (*CardRecord, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	card, ok := sm.cards[id]
	if !ok {
		return nil, ErrCardNotFound
	}
	return card, nil
}

// SetDependencies sets prerequisite dependencies for a card.
func (sm *CardStateMachine) SetDependencies(c CardID, deps []CardID) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.dependencies[c] = deps
}

// SetConsumerCapacity configures a consumer's slot capacity and status.
func (sm *CardStateMachine) SetConsumerCapacity(k ConsumerID, slots int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.consumers[k] = &ConsumerCapacity{
		Consumer: k,
		Slots:    slots,
		Up:       true,
	}
}

func (sm *CardStateMachine) depsMetLocked(c CardID) bool {
	deps, ok := sm.dependencies[c]
	if !ok || len(deps) == 0 {
		return true
	}
	for _, depID := range deps {
		depCard, exists := sm.cards[depID]
		if !exists {
			return false
		}
		if depCard.State != CardStateLanded && (depCard.State != CardStateDone || depCard.Outcome != OutcomeOK) {
			return false
		}
	}
	return true
}

func (sm *CardStateMachine) defaultOptsLocked(opts []ActionOption) []ActionOption {
	baseOpts := []ActionOption{
		WithConsumerRegistry(sm.consumers),
		WithCopyRegistry(sm.copies),
	}
	return append(baseOpts, opts...)
}

// Release moves card from waiting to ready, checking registered dependencies.
func (sm *CardStateMachine) Release(cardID CardID, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	mergedOpts = append(mergedOpts, WithDepsMet(sm.depsMetLocked(cardID)))
	return Release(card, mergedOpts...)
}

// DealRead cuts a read copy on reader for a card in review.
func (sm *CardStateMachine) DealRead(cardID CardID, reader ConsumerID, opts ...ActionOption) (*CopyRecord, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return nil, ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return DealRead(card, reader, mergedOpts...)
}

// EndWorkDone completes a work copy without PR.
func (sm *CardStateMachine) EndWorkDone(cardID CardID, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return EndWorkDone(card, mergedOpts...)
}

// EndWorkPR completes a work copy with PR, moving to review.
func (sm *CardStateMachine) EndWorkPR(cardID CardID, pr any, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return EndWorkPR(card, pr, mergedOpts...)
}

// EndReadHigh passes reader review, advancing to merging.
func (sm *CardStateMachine) EndReadHigh(cardID CardID, reader ConsumerID, verdict any, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return EndReadHigh(card, reader, verdict, mergedOpts...)
}

// EndReadLow handles reader feedback < 8.0.
func (sm *CardStateMachine) EndReadLow(cardID CardID, reader ConsumerID, verdict any, opts ...ActionOption) (*EndReadLowResult, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return nil, ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return EndReadLow(card, reader, verdict, mergedOpts...)
}

// EndFixOK completes fix work, returning to review with incremented head.
func (sm *CardStateMachine) EndFixOK(cardID CardID, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return EndFixOK(card, mergedOpts...)
}

// Verdict resolves a card in review.
func (sm *CardStateMachine) Verdict(cardID CardID, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return Verdict(card, mergedOpts...)
}

// CancelPrimary cancels a primary card and retires live copies.
func (sm *CardStateMachine) CancelPrimary(cardID CardID, opts ...ActionOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	card, ok := sm.cards[cardID]
	if !ok {
		return ErrCardNotFound
	}

	mergedOpts := sm.defaultOptsLocked(opts)
	return CancelPrimary(card, mergedOpts...)
}
