// Package layer2draft defines the Go type definitions, interface signatures,
// domain models, and Redis function call wrappers for Layer 2 dual-table card
// lifecycle operations on nova-table.
//
// In accordance with the Layer 2 Roadmap Synthesis and
// Card Machine Layer 2 Mapping design specifications, Layer 2 elevates the card lifecycle from
// ad-hoc monolithic Lua scripts (02_card_move.lua) to formal execution over
// MemberTable / EpochMemberTable primitives across two synchronized tables:
//  1. "streams" table: tracks primary cards across lifecycle columns (waiting,
//     ready, working, review, merging, landed, done).
//  2. "fleet" table: tracks consumer allocations (benches, slots, friends) and
//     copy attempts across execution columns (ready, working, ok, fail).
//
// Member hash records (table::member:<id>) store placement reverse links:
//   - Primary cards: place:streams = "<stream>:<column>"
//   - Copy attempts: place:fleet = "<consumer>:<column>"
package sprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// Section 1: Core Identifiers and Primitives
// ============================================================================

// CardID is the unique primary card identifier within a sprint (e.g. "card-42", "c1").
type CardID string

func (c CardID) String() string { return string(c) }

// ConsumerID is the identifier of an execution node (bench or slot, e.g. "bench:darwin-arm64-1", "k1").
type ConsumerID string

func (k ConsumerID) String() string { return string(k) }

// EpochID is the active generational epoch identifier of the table schema.
type EpochID uint64

func (e EpochID) String() string { return strconv.FormatUint(uint64(e), 10) }

// CopyID represents a specific execution attempt tuple <card, attempt> for a card.
// In TLA+: CopyId == Cards \X (1..MaxCopies).
type CopyID struct {
	Card    CardID `json:"card"`
	Attempt int    `json:"attempt"`
}

// String formats CopyID as "<card>:<attempt>" (e.g. "card-42:1").
func (c CopyID) String() string {
	return fmt.Sprintf("%s:%d", c.Card, c.Attempt)
}

// ParseCopyID parses a formatted string into a CopyID.
func ParseCopyID(s string) (CopyID, error) {
	idx := strings.LastIndex(s, ":")
	if idx == -1 || idx == 0 || idx == len(s)-1 {
		return CopyID{}, fmt.Errorf("invalid copy ID %q: expected <card>:<attempt>", s)
	}
	n, err := strconv.Atoi(s[idx+1:])
	if err != nil || n <= 0 {
		return CopyID{}, fmt.Errorf("invalid copy attempt %q in %q: must be positive integer", s[idx+1:], s)
	}
	return CopyID{
		Card:    CardID(s[:idx]),
		Attempt: n,
	}, nil
}

// IsZero returns whether the CopyID is uninitialized.
func (c CopyID) IsZero() bool {
	return c.Card == "" && c.Attempt == 0
}

// ============================================================================
// Section 2: States, Enums, and Predicates
// ============================================================================

// CardState represents the lifecycle position of a primary card in the "streams" table.
// In TLA+: Where == {"null", "waiting", "ready", "working", "review", "merging", "landed", "done"}.
type CardState string

const (
	CardStateNull    CardState = "null"    // Unplaced or pre-push state
	CardStateWaiting CardState = "waiting" // Waiting for dependencies or release
	CardStateReady   CardState = "ready"   // Dependencies met; eligible for deal
	CardStateWorking CardState = "working" // Actively assigned to a consumer for work
	CardStateReview  CardState = "review"  // Work completed with PR; awaiting/in code review
	CardStateMerging CardState = "merging" // Approved (Score >= 8, CI OK); queued for landing
	CardStateLanded  CardState = "landed"  // Successfully merged to target branch
	CardStateDone    CardState = "done"    // Terminal completed (direct done or cancelled)
)

// Valid returns whether the CardState is a recognized state.
func (s CardState) Valid() bool {
	switch s {
	case CardStateNull, CardStateWaiting, CardStateReady, CardStateWorking,
		CardStateReview, CardStateMerging, CardStateLanded, CardStateDone:
		return true
	default:
		return false
	}
}

// IsTerminal returns whether the card has reached an immutable terminal state.
func (s CardState) IsTerminal() bool {
	return s == CardStateLanded || s == CardStateDone
}

// IsActive returns whether the card is in an active lifecycle state.
func (s CardState) IsActive() bool {
	return s != CardStateNull && !s.IsTerminal()
}

// CopyState represents the execution status of a copy attempt in the "fleet" table.
// In TLA+: CopyWhere == {"none", "ready", "working", "ok", "fail"}.
type CopyState string

const (
	CopyStateNone    CopyState = "none"    // Copy unallocated
	CopyStateReady   CopyState = "ready"   // Queued in consumer's ready slot; lease pending
	CopyStateWorking CopyState = "working" // Lease acquired; consumer actively executing
	CopyStateOK      CopyState = "ok"      // Completed execution successfully
	CopyStateFail    CopyState = "fail"    // Execution failed, timed out, or cancelled
)

// Valid returns whether the CopyState is recognized.
func (s CopyState) Valid() bool {
	switch s {
	case CopyStateNone, CopyStateReady, CopyStateWorking, CopyStateOK, CopyStateFail:
		return true
	default:
		return false
	}
}

// IsLive returns whether the copy holds an active slot on the consumer.
// In TLA+: Live == {"ready", "working"}.
func (s CopyState) IsLive() bool {
	return s == CopyStateReady || s == CopyStateWorking
}

// IsTerminal returns whether the copy execution has finished.
func (s CopyState) IsTerminal() bool {
	return s == CopyStateOK || s == CopyStateFail
}

// Leg designates the operational purpose of a copy attempt.
// In TLA+: Legs == {"none", "work", "read", "fix"}.
type Leg string

const (
	LegNone Leg = "none"
	LegWork Leg = "work" // Initial task implementation
	LegRead Leg = "read" // Peer review or code inspection
	LegFix  Leg = "fix"  // Bugfix responding to review comments
)

// CardOutcome records the final status of a completed primary card.
// In TLA+: ok \in [Cards -> {"-", "ok", "fail"}].
type CardOutcome string

const (
	OutcomeUnset CardOutcome = "-"
	OutcomeOK    CardOutcome = "ok"
	OutcomeFail  CardOutcome = "fail"
)

// CIWord indicates the automated continuous integration status for a PR head.
// In TLA+: ci \in [Cards -> {"pending", "ok", "red"}].
type CIWord string

const (
	CIPending CIWord = "pending"
	CIOK      CIWord = "ok"
	CIRed     CIWord = "red"
)

// VerdictType defines the coordinator or ask-table resolution for a card pending review.
// In TLA+: Verdict(c) branches into waiting, ready, landed, or working on k.
type VerdictType string

const (
	VerdictRecut    VerdictType = "recut"    // Move primary back to waiting (retires reads)
	VerdictRedeal   VerdictType = "redeal"   // Move primary to ready for fresh deal
	VerdictDrop     VerdictType = "drop"     // Move primary directly to landed
	VerdictReassign VerdictType = "reassign" // Move primary to working on reassign consumer
)

// ReadLowPath represents the four branching paths when EndReadLow receives score < 8.
type ReadLowPath string

const (
	ReadLowPathA  ReadLowPath = "A"  // low >= 1: 2nd low read; enters pending review
	ReadLowPathB  ReadLowPath = "B"  // low == 0 with active fix: joins existing fix brief
	ReadLowPathC1 ReadLowPath = "C1" // low == 0, no fix: author up, cuts fix copy in ready
	ReadLowPathC2 ReadLowPath = "C2" // low == 0, no fix: author down, primary returns to waiting
)

// ============================================================================
// Section 3: Dual Table Topology & Placement Links
// ============================================================================

// Logical Table Names in nova-table
const (
	TableStreams = "streams" // Primary lifecycle table (rows: stream names, cols: CardState)
	TableFleet   = "fleet"   // Consumer execution table (rows: consumers, cols: CopyState)
)

// Redis Hash Field Keys for Reverse Placements in table::member:<id>
const (
	FieldPlaceStreams = "place:streams" // Formatted as "<stream>:<col>"
	FieldPlaceFleet   = "place:fleet"   // Formatted as "<consumer>:<col>"
)

// Placement represents the structural coordinate of a member within a logical table.
type Placement struct {
	Table  string `json:"table"`  // TableStreams ("streams") or TableFleet ("fleet")
	Row    string `json:"row"`    // Stream name or Consumer ID
	Column string `json:"column"` // Lifecycle column name (CardState or CopyState)
}

// String returns the canonical "<row>:<column>" wire encoding.
func (p Placement) String() string {
	if p.Row == "" && p.Column == "" {
		return ""
	}
	return p.Row + ":" + p.Column
}

// ParsePlacement parses a "<row>:<column>" string for a given logical table.
// Because row keys (such as consumer IDs e.g. "bench:darwin-arm64-1") may contain
// colons, the placement splits on the final colon separating row from column.
func ParsePlacement(table, s string) (Placement, error) {
	if s == "" {
		return Placement{Table: table}, nil
	}
	idx := strings.LastIndex(s, ":")
	if idx == -1 || idx == 0 || idx == len(s)-1 {
		return Placement{}, fmt.Errorf("invalid placement string %q for table %q: expected <row>:<col>", s, table)
	}
	return Placement{
		Table:  table,
		Row:    s[:idx],
		Column: s[idx+1:],
	}, nil
}

// StreamPlacement creates a placement for the "streams" table.
func StreamPlacement(stream string, state CardState) Placement {
	return Placement{
		Table:  TableStreams,
		Row:    stream,
		Column: string(state),
	}
}

// FleetPlacement creates a placement for the "fleet" table.
func FleetPlacement(consumer ConsumerID, state CopyState) Placement {
	return Placement{
		Table:  TableFleet,
		Row:    string(consumer),
		Column: string(state),
	}
}

// CellKey returns the Redis key for an owned cell under an active epoch.
// Format: table:<t>:<epoch>:cell:<row>:<col> (or table:<t>:cell:<row>:<col> for epoch 0).
func CellKey(table string, epoch EpochID, row, col string) string {
	prefix := "table:" + table
	if epoch != 0 {
		prefix += ":" + strconv.FormatUint(uint64(epoch), 10)
	}
	return prefix + ":cell:" + row + ":" + col
}

// MemberKey returns the reserved Redis hash key for a member record.
// Format: table::member:<id>.
func MemberKey(id string) string {
	return "table::member:" + id
}

// ============================================================================
// Section 4: Domain Models & Member Records
// ============================================================================

// CardRecord represents the full state hash stored at table::member:<card_id>.
type CardRecord struct {
	ID        CardID      `json:"id"`
	Epoch     EpochID     `json:"epoch"`
	Stream    string      `json:"stream"`
	State     CardState   `json:"where"`
	Placement Placement   `json:"placement_streams"`
	Outcome   CardOutcome `json:"ok"`
	LiveCopy  *CopyID     `json:"copy,omitempty"`
	LiveReads []CopyID    `json:"reads,omitempty"`
	Pending   bool        `json:"pending"`
	LowReads  int         `json:"low"`
	NCut      int         `json:"ncut"`
	Author    ConsumerID  `json:"author,omitempty"`
	Head      int         `json:"head"`
	PRHead    int         `json:"pr_head"`
	CI        CIWord      `json:"ci"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// CopyRecord represents the state hash stored at table::member:<copy_id>.
type CopyRecord struct {
	ID         CopyID     `json:"id"`
	Epoch      EpochID    `json:"epoch"`
	Primary    CardID     `json:"primary"`
	Consumer   ConsumerID `json:"consumer"`
	Leg        Leg        `json:"leg"`
	State      CopyState  `json:"where"`
	Placement  Placement  `json:"placement_fleet"`
	Attempt    int        `json:"attempt"`
	Leased     bool       `json:"leased"`
	LeaseUntil time.Time  `json:"lease_until,omitempty"`
	Score      float64    `json:"score"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ConsumerCapacity represents a consumer's execution capacity.
type ConsumerCapacity struct {
	Consumer ConsumerID `json:"consumer"`
	Slots    int        `json:"slots"`   // Total permitted concurrent slots
	Working  int        `json:"working"` // Live working copies
	Ready    int        `json:"ready"`   // Queued ready copies
	CI       int        `json:"ci"`      // CI legs running on the bench (nova-tools#4293)
	Up       bool       `json:"up"`      // Whether consumer is online and heartbeating
}

// Room calculates available execution room on the consumer.
// In TLA+: Room(k) == Slots[k] - Cardinality(Working(k)) - Cardinality(Ready(k)) - CI(k).
func (c ConsumerCapacity) Room() int {
	return c.Slots - c.Working - c.Ready - c.CI
}

// ============================================================================
// Section 5: Core Invariants and Invariant Errors
// ============================================================================

var (
	// ErrWorkingHasCopy: A working primary must name a live copy in ready or working.
	// TLA+: WorkingHasCopy == \A c: where[c] = "working" => copy[c] /= NoCopy /\ cw[copy[c]] \in Live.
	ErrWorkingHasCopy = errors.New("invariant violation: working primary must have a live work or fix copy")

	// ErrLiveCopyNamed: Every live copy must be named by its primary.
	// TLA+: LiveCopyNamed == \A i: cw[i] \in Live => (copy[i[1]] = i \/ i \in reads[i[1]]) /\ where[i[1]] \in Want(cl[i]).
	ErrLiveCopyNamed = errors.New("invariant violation: live copy must be named by its primary in the expected state")

	// ErrNamedIsLive: A primary names only live copies.
	// TLA+: NamedIsLive == \A c: copy[c] /= NoCopy => cw[copy[c]] \in Live.
	ErrNamedIsLive = errors.New("invariant violation: primary names non-live copy")

	// ErrOneLiveWork: At most one live work or fix copy per primary.
	// TLA+: OneLiveWork == \A c: Cardinality({i \in LiveCopies(c): cl[i] \in {"work", "fix"}}) <= 1.
	ErrOneLiveWork = errors.New("invariant violation: at most one live work or fix copy per primary")

	// ErrReadsInReview: Read copies are only cut and maintained when primary is in "review".
	// TLA+: ReadsInReview == \A c: reads[c] /= {} => where[c] = "review".
	ErrReadsInReview = errors.New("invariant violation: reads allowed only when primary is in review")

	// ErrTerminalIsQuiet: Merging, landed, or done cards must have zero live copies in fleet.
	// TLA+: TerminalIsQuiet == \A c: where[c] \in {"merging", "landed", "done"} => LiveCopies(c) = {}.
	ErrTerminalIsQuiet = errors.New("invariant violation: merging or terminal card cannot hold live copies")

	// ErrSlotsHeld: A consumer cannot work more copies than its allocated slots.
	// TLA+: SlotsHeld == \A k: Cardinality(Working(k)) <= Slots[k].
	ErrSlotsHeld = errors.New("invariant violation: consumer working count exceeds slot capacity")

	// ErrPendingInReview: A verdict is pending only while the primary is in "review".
	// TLA+: PendingInReview == \A c: pending[c] => where[c] = "review".
	ErrPendingInReview = errors.New("invariant violation: pending verdict is only valid in review state")

	// ErrReadyIsOneWay: Transitions from waiting to ready are strictly one-way; DealReturn is permanently disabled.
	// TLA+: ReadyIsOneWay (Finding 2 fix).
	ErrReadyIsOneWay = errors.New("refusal: ready is one way; cards in ready cannot return to waiting")

	// ErrDepsNotMet: The primary's dependencies are not satisfied.
	ErrDepsNotMet = errors.New("refusal: card dependencies are not closed (DepsMet=false)")

	// ErrNoRoom: The requested consumer has zero available room (Room(k) <= 0).
	ErrNoRoom = errors.New("refusal: consumer has no available slot capacity")

	// ErrAuthorSelfRead: A consumer cannot read or review its own authored PR.
	ErrAuthorSelfRead = errors.New("refusal: consumer cannot review its own authored PR")

	// ErrStaleHead: A read report references a stale PR head.
	ErrStaleHead = errors.New("refusal: read report head does not match current PR head")

	// ErrStaleEpoch: The caller's observed epoch does not match the active epoch.
	ErrStaleEpoch = errors.New("refusal: observed epoch is stale")

	// ErrCardNotFound: Primary card member record does not exist.
	ErrCardNotFound = errors.New("card record not found")

	// ErrCopyNotFound: Copy member record does not exist.
	ErrCopyNotFound = errors.New("copy record not found")

	// ErrDuplicateMember: The member already exists in the table.
	ErrDuplicateMember = errors.New("member already placed in table")
)

// ============================================================================
// Section 6: Action Options, Receipts, and Parameters
// ============================================================================

// WriteOptions specifies fence and identity metadata for table mutations.
type WriteOptions struct {
	Epoch   EpochID  // Target active epoch (0 for unversioned)
	Actor   string   // Identity of author / worker performing mutation
	Fence   string   // Fencing token / lease proof
	Idem    string   // Idempotency key
	Receipt *Receipt // Optional pointer to receive committed receipt
}

// Receipt encapsulates the durable committed change event from nova-table.
type Receipt struct {
	ID      string  `json:"id"`
	Epoch   EpochID `json:"epoch"`
	Before  uint64  `json:"before"`
	After   uint64  `json:"after"`
	Outcome string  `json:"outcome"`
}

// DealWorkParams specifies parameters for Action 3 (DealWork).
type DealWorkParams struct {
	Card     CardID     `json:"card"`
	Consumer ConsumerID `json:"consumer"`
	Score    float64    `json:"score,omitempty"`
}

// DealWorkResult contains the output of DealWork.
type DealWorkResult struct {
	Receipt Receipt `json:"receipt"`
	Copy    CopyID  `json:"copy"`
}

// DealReadParams specifies parameters for Action 4 (DealRead).
type DealReadParams struct {
	Card     CardID     `json:"card"`
	Consumer ConsumerID `json:"consumer"`
	Score    float64    `json:"score,omitempty"`
}

// DealReadResult contains the output of DealRead.
type DealReadResult struct {
	Receipt Receipt `json:"receipt"`
	Copy    CopyID  `json:"copy"`
}

// EndWorkPRParams specifies parameters for Action 5 (EndWorkPR).
type EndWorkPRParams struct {
	Copy      CopyID `json:"copy"`
	CommitSHA string `json:"commit_sha,omitempty"`
}

// EndWorkDoneParams specifies parameters for Action 6 (EndWorkDone).
type EndWorkDoneParams struct {
	Copy     CopyID      `json:"copy"`
	Outcome  CardOutcome `json:"outcome"`   // OutcomeOK or OutcomeFail
	ToLanded bool        `json:"to_landed"` // If true, moves to landed directly (--sha done-already)
}

// EndReadHighParams specifies parameters for Action 7 (EndReadHigh).
type EndReadHighParams struct {
	Copy  CopyID  `json:"copy"`
	Score float64 `json:"score"` // Must be >= 8.0
}

// EndReadLowParams specifies parameters for Action 8 (EndReadLow).
type EndReadLowParams struct {
	Copy        CopyID      `json:"copy"`
	Score       float64     `json:"score"` // Must be < 8.0
	Path        ReadLowPath `json:"path,omitempty"`
	FixConsumer ConsumerID  `json:"fix_consumer,omitempty"` // Target for Path C1
}

// EndReadLowResult contains the output of EndReadLow.
type EndReadLowResult struct {
	Receipt     Receipt     `json:"receipt"`
	Path        ReadLowPath `json:"path"`
	FixCopy     *CopyID     `json:"fix_copy,omitempty"`
	TargetState CardState   `json:"target_state"`
}

// EndFixOKParams specifies parameters for Action 9 (EndFixOK).
type EndFixOKParams struct {
	Copy CopyID `json:"copy"`
}

// VerdictParams specifies parameters for Action 10 (Verdict).
type VerdictParams struct {
	Card             CardID      `json:"card"`
	Verdict          VerdictType `json:"verdict"`
	ReassignConsumer ConsumerID  `json:"reassign_consumer,omitempty"` // Required if verdict is VerdictReassign
}

// CancelPrimaryParams specifies parameters for Action 12 (CancelPrimary).
type CancelPrimaryParams struct {
	Card   CardID `json:"card"`
	Reason string `json:"reason"`
}

// ============================================================================
// Section 7: CardMachine Interface (Twelve Primary Actions + Auxiliary)
// ============================================================================

// CardMachine defines the contract for Layer 2 dual-table card lifecycle operations.
// All mutating operations must execute as atomic Redis functions (fcall) guaranteeing
// consistency across the "streams" and "fleet" tables and member hash records.
type CardMachine interface {
	// --- The 12 Primary Card Lifecycle Actions ---

	// Push (Action 1): Introduces a new primary card into a stream's "waiting" cell.
	// streams: null -> waiting (EpochAdd). fleet: none. Single-table.
	Push(ctx context.Context, c CardID, stream string, opts ...WriteOptions) (*Receipt, error)

	// Release (Action 2): Advances a waiting primary to "ready" when all dependencies are closed.
	// streams: waiting -> ready (EpochMove). fleet: none. Single-table guard: DepsMet.
	Release(ctx context.Context, c CardID, opts ...WriteOptions) (*Receipt, error)

	// DealWork (Action 3): Assigns primary card c to consumer k for an implementation work leg.
	// streams: waiting/ready -> working (EpochMove). fleet: null -> ready on k (EpochAdd).
	// Dual-Table Atomic. Enforces WorkingHasCopy, Room(k) > 0.
	DealWork(ctx context.Context, params DealWorkParams, opts ...WriteOptions) (*DealWorkResult, error)

	// DealRead (Action 4): Cuts a read copy for card c in review on consumer k (k != author).
	// streams: remains review. fleet: null -> ready on k (EpochAdd).
	// Cross-Table Guard & Mutation. Enforces ReadsInReview, Room(k) > 0.
	DealRead(ctx context.Context, params DealReadParams, opts ...WriteOptions) (*DealReadResult, error)

	// EndWorkPR (Action 5): Completes work copy with a PR, moving card c to "review".
	// streams: working -> review (EpochMove). fleet: working -> ok (EpochMove/EpochRemove).
	// Dual-Table Atomic. Enforces LiveCopyNamed.
	EndWorkPR(ctx context.Context, params EndWorkPRParams, opts ...WriteOptions) (*Receipt, error)

	// EndWorkDone (Action 6): Completes work copy with direct resolution (no PR).
	// streams: working -> done (or landed). fleet: working -> ok.
	// Dual-Table Atomic. Enforces TerminalIsQuiet.
	EndWorkDone(ctx context.Context, params EndWorkDoneParams, opts ...WriteOptions) (*Receipt, error)

	// EndReadHigh (Action 7): Read copy scores >= 8 with CI OK; advances primary to "merging".
	// streams: review -> merging (EpochMove). fleet: reporting reader -> ok, other reads/fix -> fail.
	// Batch Multi-Row Dual-Table Atomic. Enforces TerminalIsQuiet, Fanout Cleanup.
	EndReadHigh(ctx context.Context, params EndReadHighParams, opts ...WriteOptions) (*Receipt, error)

	// EndReadLow (Action 8): Read copy scores < 8; handles review branch (Paths A, B, C1, C2).
	// streams: review (or waiting on C2). fleet: reader -> ok; if C1, adds fix copy in ready.
	// Dual-Table Atomic. Enforces OneLiveWork, LiveCopyNamed.
	EndReadLow(ctx context.Context, params EndReadLowParams, opts ...WriteOptions) (*EndReadLowResult, error)

	// EndFixOK (Action 9): Fix copy completes; increments PR head and retires open reads to fail.
	// streams: remains review. fleet: fix copy -> ok, open reads -> fail.
	// Multi-Row Dual-Table Atomic. Enforces ReadsInReview.
	EndFixOK(ctx context.Context, params EndFixOKParams, opts ...WriteOptions) (*Receipt, error)

	// Verdict (Action 10): Resolves a pending review (recut, redeal, drop, or reassign).
	// streams: review -> waiting/ready/landed/working. fleet: reads -> fail, if reassign add work copy.
	// Dual-Table Atomic. Enforces PendingInReview.
	Verdict(ctx context.Context, params VerdictParams, opts ...WriteOptions) (*Receipt, error)

	// Land (Action 11): Stream lander confirms merge into target branch.
	// streams: merging -> landed (EpochMove). fleet: none. Single-table.
	// Triggers dependency release cascade for dependent cards.
	Land(ctx context.Context, c CardID, opts ...WriteOptions) (*Receipt, error)

	// CancelPrimary (Action 12): Explicit operator cancellation of a primary card.
	// streams: non-terminal -> done (ok=fail). fleet: all active copies & reads -> fail.
	// Batch Multi-Row Dual-Table Atomic. Enforces TerminalIsQuiet.
	CancelPrimary(ctx context.Context, params CancelPrimaryParams, opts ...WriteOptions) (*Receipt, error)

	// --- Copy-Level & Auxiliary Operations ---

	// Work: Starts execution on a ready copy, acquiring a lease and moving to "working".
	// fleet: ready -> working on k. Single-table. Enforces SlotsHeld.
	Work(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error)

	// Beat: Renews the heartbeat lease on an active working copy.
	Beat(ctx context.Context, i CopyID, leaseDuration time.Duration, opts ...WriteOptions) (*Receipt, error)

	// GiveBack: Returns a copy without advancing work; work/fix returns to waiting, read stays review.
	GiveBack(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error)

	// Expire: Handles a lapsed lease; read copy is given back; work/fix fails and enters review (pending=true).
	Expire(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error)

	// EndFail: Records copy failure; moves copy to fail and primary to review (pending=true).
	EndFail(ctx context.Context, i CopyID, reason string, opts ...WriteOptions) (*Receipt, error)

	// LandEvent: Out-of-band landing event; moves primary to landed and cancels any live copies.
	LandEvent(ctx context.Context, c CardID, opts ...WriteOptions) (*Receipt, error)

	// --- Inspection & State Queries ---

	// GetCard reads the live state and metadata of a primary card.
	GetCard(ctx context.Context, c CardID) (*CardRecord, error)

	// GetCopy reads the live state and metadata of a copy attempt.
	GetCopy(ctx context.Context, i CopyID) (*CopyRecord, error)

	// GetConsumerCapacity queries the current slot allocation and room for consumer k.
	GetConsumerCapacity(ctx context.Context, k ConsumerID) (*ConsumerCapacity, error)
}

// ============================================================================
// Section 8: Redis Function Name Constants (ns_card_*)
// ============================================================================

const (
	// The 12 Primary Action Redis Functions
	FnCardPush          = "ns_card_push"
	FnCardRelease       = "ns_card_release"
	FnCardDealWork      = "ns_card_deal_work"
	FnCardDealRead      = "ns_card_deal_read"
	FnCardEndWorkPR     = "ns_card_end_work_pr"
	FnCardEndWorkDone   = "ns_card_end_work_done"
	FnCardEndReadHigh   = "ns_card_end_read_high"
	FnCardEndReadLow    = "ns_card_end_read_low"
	FnCardEndFixOK      = "ns_card_end_fix_ok"
	FnCardVerdict       = "ns_card_verdict"
	FnCardLand          = "ns_card_land"
	FnCardCancelPrimary = "ns_card_cancel_primary"

	// Auxiliary & Copy-Level Redis Functions
	FnCardWork      = "ns_card_work"
	FnCardBeat      = "ns_card_beat"
	FnCardGiveBack  = "ns_card_give_back"
	FnCardExpire    = "ns_card_expire"
	FnCardEndFail   = "ns_card_end_fail"
	FnCardLandEvent = "ns_card_land_event"

	// Query / Read-Only Redis Functions
	FnCardGet              = "ns_card_get"
	FnCardCopyGet          = "ns_card_copy_get"
	FnCardConsumerCapacity = "ns_card_consumer_capacity"
)

// ============================================================================
// Section 9: Redis Function Call Client & Invocation Wrappers
// ============================================================================

// Client implements CardMachine by invoking registered Redis functions (ns_card_*).
type Client struct {
	rdb redis.Cmdable
}

// NewClient creates a new CardMachine client backed by a Redis Cmdable.
func NewClient(rdb redis.Cmdable) *Client {
	return &Client{rdb: rdb}
}

// Ensure CardMachine interface implementation.
var _ CardMachine = (*Client)(nil)

// ----------------------------------------------------------------------------
// Internal FCALL Dispatch Helper
// ----------------------------------------------------------------------------

func executeCardFCall(
	ctx context.Context,
	rdb redis.Cmdable,
	fn string,
	keys []string,
	ro bool,
	options []WriteOptions,
	args ...any,
) ([]any, *Receipt, error) {
	var opts WriteOptions
	if len(options) > 0 {
		opts = options[0]
	}

	headerJSON, err := json.Marshal(struct {
		Epoch string `json:"epoch"`
		Actor string `json:"actor"`
		Fence string `json:"fence"`
		Idem  string `json:"idem"`
	}{
		Epoch: strconv.FormatUint(uint64(opts.Epoch), 10),
		Actor: opts.Actor,
		Fence: opts.Fence,
		Idem:  opts.Idem,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal write options: %w", err)
	}

	callArgs := append([]any{string(headerJSON)}, args...)

	var cmd *redis.Cmd
	if ro {
		cmd = rdb.FCallRO(ctx, fn, keys, callArgs...)
	} else {
		cmd = rdb.FCall(ctx, fn, keys, callArgs...)
	}

	reply, err := cmd.Slice()
	if err != nil {
		return nil, nil, fmt.Errorf("fcall %s failed: %w", fn, err)
	}

	if len(reply) == 0 {
		return nil, nil, fmt.Errorf("fcall %s returned empty reply", fn)
	}

	// Check for REFUSED protocol reply: ["REFUSED", <reason>, <details...>]
	if status := fmt.Sprint(reply[0]); status == "REFUSED" {
		return nil, nil, parseRefusal(fn, reply)
	}

	// For read-only calls, no receipt is expected.
	if ro {
		return reply, nil, nil
	}

	// For write calls, the final element must be ["RECEIPT", id, epoch, before, after, outcome]
	if len(reply) < 2 {
		return nil, nil, fmt.Errorf("fcall %s: missing committed receipt", fn)
	}

	lastIdx := len(reply) - 1
	wireReceipt, ok := reply[lastIdx].([]any)
	if !ok || len(wireReceipt) < 6 || fmt.Sprint(wireReceipt[0]) != "RECEIPT" {
		return nil, nil, fmt.Errorf("fcall %s: malformed receipt element: %v", fn, reply[lastIdx])
	}

	r := Receipt{
		ID:      fmt.Sprint(wireReceipt[1]),
		Outcome: fmt.Sprint(wireReceipt[5]),
	}
	if ep, err := strconv.ParseUint(fmt.Sprint(wireReceipt[2]), 10, 64); err == nil {
		r.Epoch = EpochID(ep)
	}
	if b, err := strconv.ParseUint(fmt.Sprint(wireReceipt[3]), 10, 64); err == nil {
		r.Before = b
	}
	if a, err := strconv.ParseUint(fmt.Sprint(wireReceipt[4]), 10, 64); err == nil {
		r.After = a
	}

	if opts.Receipt != nil {
		*opts.Receipt = r
	}

	return reply[:lastIdx], &r, nil
}

// parseRefusal translates Redis function REFUSED replies into typed errors.
func parseRefusal(fn string, reply []any) error {
	if len(reply) < 2 {
		return fmt.Errorf("%s refused: unknown reason", fn)
	}
	reason := fmt.Sprint(reply[1])
	switch reason {
	case "WORKINGHASCOPY":
		return ErrWorkingHasCopy
	case "LIVECOPYNAMED":
		return ErrLiveCopyNamed
	case "NAMEDISLIVE":
		return ErrNamedIsLive
	case "ONELIVEWORK":
		return ErrOneLiveWork
	case "READSINREVIEW":
		return ErrReadsInReview
	case "TERMINALISQUIET":
		return ErrTerminalIsQuiet
	case "SLOTSHELD":
		return ErrSlotsHeld
	case "PENDINGINREVIEW":
		return ErrPendingInReview
	case "READYISONEWAY":
		return ErrReadyIsOneWay
	case "DEPSNOTMET":
		return ErrDepsNotMet
	case "NOROOM":
		return ErrNoRoom
	case "AUTHORSELFREAD":
		return ErrAuthorSelfRead
	case "STALEHEAD":
		return ErrStaleHead
	case "STALE":
		return fmt.Errorf("%w: %v", ErrStaleEpoch, reply[2:])
	case "CARDNOTFOUND":
		return ErrCardNotFound
	case "COPYNOTFOUND":
		return ErrCopyNotFound
	case "DUPLICATEMEMBER":
		return ErrDuplicateMember
	default:
		return fmt.Errorf("%s refused [%s]: %v", fn, reason, reply[2:])
	}
}

// ----------------------------------------------------------------------------
// Primary Actions Implementations (1 to 12)
// ----------------------------------------------------------------------------

// Push (Action 1): Introduces a new primary card into a stream's "waiting" cell.
func (c *Client) Push(ctx context.Context, card CardID, stream string, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:streams", MemberKey(string(card))}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardPush, keys, false, opts, string(card), stream)
	return receipt, err
}

// Release (Action 2): Moves a waiting primary to "ready" when dependencies are satisfied.
func (c *Client) Release(ctx context.Context, card CardID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:streams", MemberKey(string(card))}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardRelease, keys, false, opts, string(card))
	return receipt, err
}

// DealWork (Action 3): Atomically moves primary to working in streams and cuts a ready copy in fleet.
func (c *Client) DealWork(ctx context.Context, params DealWorkParams, opts ...WriteOptions) (*DealWorkResult, error) {
	keys := []string{"table:streams", "table:fleet", MemberKey(string(params.Card))}
	reply, receipt, err := executeCardFCall(ctx, c.rdb, FnCardDealWork, keys, false, opts, string(params.Card), string(params.Consumer), params.Score)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: missing copy ID in reply", FnCardDealWork)
	}
	copyID, err := ParseCopyID(fmt.Sprint(reply[1]))
	if err != nil {
		return nil, fmt.Errorf("%s: malformed copy ID %v: %w", FnCardDealWork, reply[1], err)
	}
	return &DealWorkResult{
		Receipt: *receipt,
		Copy:    copyID,
	}, nil
}

// DealRead (Action 4): Atomically cuts a ready read copy on consumer k for a card in review.
func (c *Client) DealRead(ctx context.Context, params DealReadParams, opts ...WriteOptions) (*DealReadResult, error) {
	keys := []string{"table:streams", "table:fleet", MemberKey(string(params.Card))}
	reply, receipt, err := executeCardFCall(ctx, c.rdb, FnCardDealRead, keys, false, opts, string(params.Card), string(params.Consumer), params.Score)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: missing copy ID in reply", FnCardDealRead)
	}
	copyID, err := ParseCopyID(fmt.Sprint(reply[1]))
	if err != nil {
		return nil, fmt.Errorf("%s: malformed copy ID %v: %w", FnCardDealRead, reply[1], err)
	}
	return &DealReadResult{
		Receipt: *receipt,
		Copy:    copyID,
	}, nil
}

// EndWorkPR (Action 5): Completes work copy with a PR, moving card to review.
func (c *Client) EndWorkPR(ctx context.Context, params EndWorkPRParams, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Copy.Card)),
		MemberKey(params.Copy.String()),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardEndWorkPR, keys, false, opts, params.Copy.String(), params.CommitSHA)
	return receipt, err
}

// EndWorkDone (Action 6): Completes work copy with direct resolution (no PR).
func (c *Client) EndWorkDone(ctx context.Context, params EndWorkDoneParams, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Copy.Card)),
		MemberKey(params.Copy.String()),
	}
	toLandedStr := "0"
	if params.ToLanded {
		toLandedStr = "1"
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardEndWorkDone, keys, false, opts, params.Copy.String(), string(params.Outcome), toLandedStr)
	return receipt, err
}

// EndReadHigh (Action 7): Read copy scores >= 8 with CI OK; advances primary to merging.
func (c *Client) EndReadHigh(ctx context.Context, params EndReadHighParams, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Copy.Card)),
		MemberKey(params.Copy.String()),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardEndReadHigh, keys, false, opts, params.Copy.String(), params.Score)
	return receipt, err
}

// EndReadLow (Action 8): Read copy scores < 8; resolves review branch.
func (c *Client) EndReadLow(ctx context.Context, params EndReadLowParams, opts ...WriteOptions) (*EndReadLowResult, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Copy.Card)),
		MemberKey(params.Copy.String()),
	}
	reply, receipt, err := executeCardFCall(
		ctx, c.rdb, FnCardEndReadLow, keys, false, opts,
		params.Copy.String(), params.Score, string(params.Path), string(params.FixConsumer),
	)
	if err != nil {
		return nil, err
	}
	res := &EndReadLowResult{Receipt: *receipt}
	if len(reply) >= 2 {
		res.Path = ReadLowPath(fmt.Sprint(reply[1]))
	}
	if len(reply) >= 3 && fmt.Sprint(reply[2]) != "" {
		res.TargetState = CardState(fmt.Sprint(reply[2]))
	}
	if len(reply) >= 4 && fmt.Sprint(reply[3]) != "" {
		if fixCopy, err := ParseCopyID(fmt.Sprint(reply[3])); err == nil {
			res.FixCopy = &fixCopy
		}
	}
	return res, nil
}

// EndFixOK (Action 9): Fix copy completes; increments PR head and retires open reads to fail.
func (c *Client) EndFixOK(ctx context.Context, params EndFixOKParams, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Copy.Card)),
		MemberKey(params.Copy.String()),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardEndFixOK, keys, false, opts, params.Copy.String())
	return receipt, err
}

// Verdict (Action 10): Resolves a pending review (recut, redeal, drop, or reassign).
func (c *Client) Verdict(ctx context.Context, params VerdictParams, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Card)),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardVerdict, keys, false, opts, string(params.Card), string(params.Verdict), string(params.ReassignConsumer))
	return receipt, err
}

// Land (Action 11): Stream lander confirms merge into target branch.
func (c *Client) Land(ctx context.Context, card CardID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:streams", MemberKey(string(card))}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardLand, keys, false, opts, string(card))
	return receipt, err
}

// CancelPrimary (Action 12): Explicit operator cancellation of a primary card.
func (c *Client) CancelPrimary(ctx context.Context, params CancelPrimaryParams, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(params.Card)),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardCancelPrimary, keys, false, opts, string(params.Card), params.Reason)
	return receipt, err
}

// ----------------------------------------------------------------------------
// Auxiliary and Copy-Level Implementations
// ----------------------------------------------------------------------------

func (c *Client) Work(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:fleet", MemberKey(i.String())}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardWork, keys, false, opts, i.String())
	return receipt, err
}

func (c *Client) Beat(ctx context.Context, i CopyID, leaseDuration time.Duration, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:fleet", MemberKey(i.String())}
	leaseMS := leaseDuration.Milliseconds()
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardBeat, keys, false, opts, i.String(), strconv.FormatInt(leaseMS, 10))
	return receipt, err
}

func (c *Client) GiveBack(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(i.Card)),
		MemberKey(i.String()),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardGiveBack, keys, false, opts, i.String())
	return receipt, err
}

func (c *Client) Expire(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(i.Card)),
		MemberKey(i.String()),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardExpire, keys, false, opts, i.String())
	return receipt, err
}

func (c *Client) EndFail(ctx context.Context, i CopyID, reason string, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(i.Card)),
		MemberKey(i.String()),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardEndFail, keys, false, opts, i.String(), reason)
	return receipt, err
}

func (c *Client) LandEvent(ctx context.Context, card CardID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{
		"table:streams",
		"table:fleet",
		MemberKey(string(card)),
	}
	_, receipt, err := executeCardFCall(ctx, c.rdb, FnCardLandEvent, keys, false, opts, string(card))
	return receipt, err
}

// ----------------------------------------------------------------------------
// Inspection & State Queries
// ----------------------------------------------------------------------------

func (c *Client) GetCard(ctx context.Context, card CardID) (*CardRecord, error) {
	keys := []string{"table:streams", MemberKey(string(card))}
	reply, _, err := executeCardFCall(ctx, c.rdb, FnCardGet, keys, true, nil, string(card))
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: malformed card get reply", FnCardGet)
	}
	jsonBytes, err := json.Marshal(reply[1])
	if err != nil {
		return nil, fmt.Errorf("%s: marshal card hash: %w", FnCardGet, err)
	}
	var rec CardRecord
	if err := json.Unmarshal(jsonBytes, &rec); err != nil {
		return nil, fmt.Errorf("%s: unmarshal card record: %w", FnCardGet, err)
	}
	return &rec, nil
}

func (c *Client) GetCopy(ctx context.Context, i CopyID) (*CopyRecord, error) {
	keys := []string{"table:fleet", MemberKey(i.String())}
	reply, _, err := executeCardFCall(ctx, c.rdb, FnCardCopyGet, keys, true, nil, i.String())
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: malformed copy get reply", FnCardCopyGet)
	}
	jsonBytes, err := json.Marshal(reply[1])
	if err != nil {
		return nil, fmt.Errorf("%s: marshal copy hash: %w", FnCardCopyGet, err)
	}
	var rec CopyRecord
	if err := json.Unmarshal(jsonBytes, &rec); err != nil {
		return nil, fmt.Errorf("%s: unmarshal copy record: %w", FnCardCopyGet, err)
	}
	return &rec, nil
}

func (c *Client) GetConsumerCapacity(ctx context.Context, k ConsumerID) (*ConsumerCapacity, error) {
	keys := []string{"table:fleet"}
	reply, _, err := executeCardFCall(ctx, c.rdb, FnCardConsumerCapacity, keys, true, nil, string(k))
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: malformed capacity reply", FnCardConsumerCapacity)
	}
	jsonBytes, err := json.Marshal(reply[1])
	if err != nil {
		return nil, fmt.Errorf("%s: marshal capacity hash: %w", FnCardConsumerCapacity, err)
	}
	var cap ConsumerCapacity
	if err := json.Unmarshal(jsonBytes, &cap); err != nil {
		return nil, fmt.Errorf("%s: unmarshal consumer capacity: %w", FnCardConsumerCapacity, err)
	}
	return &cap, nil
}
