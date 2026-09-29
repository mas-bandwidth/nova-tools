package sprint

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// Constants & Sentinel Errors
// ============================================================================

// DefaultMaxClockSkew defines the default maximum backward clock regression
// tolerated (500ms) to accommodate minor NTP adjustments and cross-node jitter.
const DefaultMaxClockSkew = 500 * time.Millisecond

var (
	// ErrDuplicateSequence is returned when a step proposes a sequence number
	// that has already been committed.
	ErrDuplicateSequence = errors.New("ratchet: duplicate sequence number")

	// ErrRegressingSequence is returned when a step proposes a sequence number
	// strictly less than the currently committed sequence.
	ErrRegressingSequence = errors.New("ratchet: sequence number regressed")

	// ErrSequenceGap is returned when strict contiguous sequencing is enabled
	// and a step's sequence number does not equal lastSeq + 1.
	ErrSequenceGap = errors.New("ratchet: sequence number has non-contiguous gap")

	// ErrClockRegression is returned when a proposed timestamp regresses beyond
	// the maximum tolerated clock skew.
	ErrClockRegression = errors.New("ratchet: timestamp regressed beyond max tolerated skew")

	// ErrDivergentPriorState is returned when a proposed step's PriorStateHash
	// does not match the ratchet's current committed state hash.
	ErrDivergentPriorState = errors.New("ratchet: prior state hash diverges from current commitment")

	// ErrZeroNewState is returned when a step attempts to commit an uninitialized
	// or all-zero state hash.
	ErrZeroNewState = errors.New("ratchet: new state commitment cannot be zero hash")

	// ErrRatchetClosed is returned when an operation is attempted on a closed ratchet.
	ErrRatchetClosed = errors.New("ratchet: ratchet is closed")
)

// ============================================================================
// Data Structures
// ============================================================================

// RatchetStep represents a proposed state transition to be validated and committed.
type RatchetStep struct {
	// Seq is the monotonic sequence number of the transition (must be > current seq).
	Seq uint64 `json:"seq"`

	// Timestamp is the wall-clock time at which the transition occurred.
	Timestamp time.Time `json:"timestamp"`

	// PriorStateHash is the cryptographic hash of the state before this step.
	// Must match the current commitment hash of the ratchet.
	PriorStateHash [32]byte `json:"prior_state_hash"`

	// NewStateHash is the cryptographic commitment of the state after this step.
	NewStateHash [32]byte `json:"new_state_hash"`

	// Payload is an optional arbitrary descriptor or serialized event body.
	Payload []byte `json:"payload,omitempty"`
}

// RatchetCommitment represents an immutable, finalized record of a committed step.
type RatchetCommitment struct {
	// Seq is the finalized monotonic sequence number.
	Seq uint64 `json:"seq"`

	// Timestamp is the effective monotonic timestamp of this commitment.
	// If the observed timestamp was slightly in the past (within skew),
	// Timestamp is clamped to lastTimestamp to preserve strict forward clock monotonicity.
	Timestamp time.Time `json:"timestamp"`

	// ObservedTimestamp is the raw, unadjusted timestamp passed in the step.
	ObservedTimestamp time.Time `json:"observed_timestamp"`

	// PriorStateHash is the state hash that was superseded by this step.
	PriorStateHash [32]byte `json:"prior_state_hash"`

	// StateHash is the newly committed state hash.
	StateHash [32]byte `json:"state_hash"`

	// StepDigest is the SHA-256 hash sealing this transition:
	// SHA-256(Seq || Timestamp || PriorStateHash || NewStateHash || PayloadLen || Payload).
	StepDigest [32]byte `json:"step_digest"`

	// CommittedAt is the local monotonic instant when this commitment was sealed.
	CommittedAt time.Time `json:"committed_at"`
}

// RatchetSnapshot captures a point-in-time state of the ratchet.
type RatchetSnapshot struct {
	Seq         uint64    `json:"seq"`
	Timestamp   time.Time `json:"timestamp"`
	StateHash   [32]byte  `json:"state_hash"`
	StepCount   uint64    `json:"step_count"`
	CommittedAt time.Time `json:"committed_at"`
}

// ============================================================================
// MonotoneRatchet Implementation
// ============================================================================

// RatchetOption configures a MonotoneRatchet.
type RatchetOption func(*MonotoneRatchet)

// WithMaxClockSkew sets the maximum backward clock regression tolerated.
func WithMaxClockSkew(skew time.Duration) RatchetOption {
	return func(r *MonotoneRatchet) {
		r.maxClockSkew = skew
	}
}

// WithStrictContiguous configures whether sequence numbers must strictly advance
// by exactly +1 (lastSeq + 1), or may advance by any positive increment (seq > lastSeq).
func WithStrictContiguous(strict bool) RatchetOption {
	return func(r *MonotoneRatchet) {
		r.strictContiguous = strict
	}
}

// WithInitialState sets the seed sequence, timestamp, and state hash for the ratchet.
func WithInitialState(seq uint64, ts time.Time, stateHash [32]byte) RatchetOption {
	return func(r *MonotoneRatchet) {
		r.lastSeq = seq
		r.lastTimestamp = ts
		r.lastStateHash = stateHash
	}
}

// WithHistoryRetention configures the capacity of the in-memory commitment history buffer.
// Set to 0 to disable history retention.
func WithHistoryRetention(capacity int) RatchetOption {
	return func(r *MonotoneRatchet) {
		r.historyCap = capacity
		if capacity > 0 {
			r.history = make([]*RatchetCommitment, 0, capacity)
		}
	}
}

// MonotoneRatchet enforces monotonic invariants across distributed state machine
// sequence numbers, clock timestamps, and cryptographic state commitments.
type MonotoneRatchet struct {
	mu sync.RWMutex

	// Configuration
	maxClockSkew     time.Duration
	strictContiguous bool
	historyCap       int

	// Current commitment state
	lastSeq       uint64
	lastTimestamp time.Time
	lastStateHash [32]byte
	stepCount     uint64
	lastCommitAt  time.Time

	// Atomic mirror of lastSeq for fast lockless queries
	atomicSeq uint64

	// History buffer (ring / slice)
	history []*RatchetCommitment

	closed bool
}

// NewMonotoneRatchet constructs a new MonotoneRatchet initialized with safe defaults.
func NewMonotoneRatchet(opts ...RatchetOption) *MonotoneRatchet {
	r := &MonotoneRatchet{
		maxClockSkew:     DefaultMaxClockSkew,
		strictContiguous: true,
		historyCap:       1024,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	if r.historyCap > 0 && r.history == nil {
		r.history = make([]*RatchetCommitment, 0, r.historyCap)
	}
	r.atomicSeq = r.lastSeq
	return r
}

// ComputeStepDigest computes the cryptographic hash binding all fields of a step.
func ComputeStepDigest(seq uint64, ts time.Time, prior, current [32]byte, payload []byte) [32]byte {
	h := sha256.New()
	var numBuf [8]byte

	binary.BigEndian.PutUint64(numBuf[:], seq)
	h.Write(numBuf[:])

	binary.BigEndian.PutUint64(numBuf[:], uint64(ts.UnixNano()))
	h.Write(numBuf[:])

	h.Write(prior[:])
	h.Write(current[:])

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	h.Write(lenBuf[:])

	if len(payload) > 0 {
		h.Write(payload)
	}

	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// Verify validates a proposed step against the current ratchet state without committing it.
// Returns nil if the step would successfully commit, or an error detailing the invariant violation.
func (r *MonotoneRatchet) Verify(step RatchetStep) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.closed {
		return ErrRatchetClosed
	}

	return r.validateStepLocked(step)
}

// Step advances the ratchet by validating all monotonic invariants and committing the new state.
// Thread-safe and atomic.
func (r *MonotoneRatchet) Step(step RatchetStep) (*RatchetCommitment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, ErrRatchetClosed
	}

	if err := r.validateStepLocked(step); err != nil {
		return nil, err
	}

	// 1. Calculate effective monotonic timestamp:
	// If the observed timestamp is ahead, advance clock to step.Timestamp.
	// If the observed timestamp is behind (within tolerated skew), clamp forward
	// to r.lastTimestamp so downstream consumers never see time move backwards.
	effectiveTS := step.Timestamp
	if !r.lastTimestamp.IsZero() && effectiveTS.Before(r.lastTimestamp) {
		effectiveTS = r.lastTimestamp
	}

	// 2. Compute cryptographic step digest
	digest := ComputeStepDigest(step.Seq, effectiveTS, step.PriorStateHash, step.NewStateHash, step.Payload)
	now := time.Now()

	commitment := &RatchetCommitment{
		Seq:               step.Seq,
		Timestamp:         effectiveTS,
		ObservedTimestamp: step.Timestamp,
		PriorStateHash:    step.PriorStateHash,
		StateHash:         step.NewStateHash,
		StepDigest:        digest,
		CommittedAt:       now,
	}

	// 3. Mutate ratchet state
	r.lastSeq = step.Seq
	r.lastTimestamp = effectiveTS
	r.lastStateHash = step.NewStateHash
	r.stepCount++
	r.lastCommitAt = now
	atomic.StoreUint64(&r.atomicSeq, step.Seq)

	// 4. Retain in history if configured
	if r.historyCap > 0 {
		if len(r.history) >= r.historyCap {
			// Evict oldest
			copy(r.history, r.history[1:])
			r.history[len(r.history)-1] = commitment
		} else {
			r.history = append(r.history, commitment)
		}
	}

	return commitment, nil
}

// validateStepLocked enforces all invariants under lock:
// 1. Sequence numbers must be strictly increasing (and contiguous if configured).
// 2. Clock timestamps must not regress beyond max tolerated skew.
// 3. State commitments must lock monotonically (prior state must match current commitment).
// 4. New state hash must not be all-zeros.
func (r *MonotoneRatchet) validateStepLocked(step RatchetStep) error {
	// Invariant 1: Sequence monotonicity
	if r.lastSeq > 0 || r.stepCount > 0 {
		if step.Seq == r.lastSeq {
			return fmt.Errorf("%w: seq=%d already committed", ErrDuplicateSequence, step.Seq)
		}
		if step.Seq < r.lastSeq {
			return fmt.Errorf("%w: proposed seq=%d < current seq=%d", ErrRegressingSequence, step.Seq, r.lastSeq)
		}
		if r.strictContiguous && step.Seq != r.lastSeq+1 {
			return fmt.Errorf("%w: proposed seq=%d, want contiguous %d", ErrSequenceGap, step.Seq, r.lastSeq+1)
		}
	} else {
		// First step
		if r.strictContiguous && step.Seq != r.lastSeq+1 && step.Seq != 1 {
			return fmt.Errorf("%w: initial proposed seq=%d, expected 1", ErrSequenceGap, step.Seq)
		}
	}

	// Invariant 2: Clock monotonicity with bounded skew tolerance
	if !r.lastTimestamp.IsZero() {
		if step.Timestamp.Before(r.lastTimestamp) {
			skew := r.lastTimestamp.Sub(step.Timestamp)
			if skew > r.maxClockSkew {
				return fmt.Errorf("%w: regressed by %v (max tolerated: %v)", ErrClockRegression, skew, r.maxClockSkew)
			}
		}
	}

	// Invariant 3: State commitment chaining
	if step.PriorStateHash != r.lastStateHash {
		return fmt.Errorf("%w: proposed prior %x != current commitment %x",
			ErrDivergentPriorState, step.PriorStateHash[:8], r.lastStateHash[:8])
	}

	// Invariant 4: Non-zero target commitment
	var zeroHash [32]byte
	if step.NewStateHash == zeroHash {
		return ErrZeroNewState
	}

	return nil
}

// CurrentSeq returns the latest committed sequence number (lockless atomic read).
func (r *MonotoneRatchet) CurrentSeq() uint64 {
	return atomic.LoadUint64(&r.atomicSeq)
}

// CurrentStateHash returns the current committed state hash.
func (r *MonotoneRatchet) CurrentStateHash() [32]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastStateHash
}

// CurrentTimestamp returns the current effective monotonic clock timestamp.
func (r *MonotoneRatchet) CurrentTimestamp() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastTimestamp
}

// Snapshot returns an immutable point-in-time view of the ratchet state.
func (r *MonotoneRatchet) Snapshot() RatchetSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return RatchetSnapshot{
		Seq:         r.lastSeq,
		Timestamp:   r.lastTimestamp,
		StateHash:   r.lastStateHash,
		StepCount:   r.stepCount,
		CommittedAt: r.lastCommitAt,
	}
}

// History returns a copy of all retained commitments in chronological order.
func (r *MonotoneRatchet) History() []*RatchetCommitment {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RatchetCommitment, len(r.history))
	copy(out, r.history)
	return out
}

// StepCount returns the total number of committed steps.
func (r *MonotoneRatchet) StepCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stepCount
}

// StepAndFrame advances the ratchet by validating all monotonic invariants and committing
// the new state, and directly encodes the committed transition into a signed, framed journal buffer.
func (r *MonotoneRatchet) StepAndFrame(step RatchetStep) (*RatchetCommitment, []byte, *Frame, error) {
	c, err := r.Step(step)
	if err != nil {
		return nil, nil, nil, err
	}
	buf, frame, err := EncodeFrame(c.Seq, c.Timestamp, step.Payload)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ratchet journal framing failed: %w", err)
	}
	return c, buf, frame, nil
}

// VerifyFrame checks that an existing journal frame conforms to the ratchet's
// sequence and timestamp monotonicity invariants.
func (r *MonotoneRatchet) VerifyFrame(frame *Frame) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.closed {
		return ErrRatchetClosed
	}
	if frame == nil {
		return errors.New("ratchet: nil frame")
	}

	if r.lastSeq > 0 || r.stepCount > 0 {
		if frame.Seq <= r.lastSeq {
			return fmt.Errorf("%w: frame.Seq=%d <= current seq=%d", ErrRegressingSequence, frame.Seq, r.lastSeq)
		}
		if r.strictContiguous && frame.Seq != r.lastSeq+1 {
			return fmt.Errorf("%w: frame.Seq=%d, want %d", ErrSequenceGap, frame.Seq, r.lastSeq+1)
		}
	} else if r.strictContiguous && frame.Seq != 1 {
		return fmt.Errorf("%w: initial frame.Seq=%d, expected 1", ErrSequenceGap, frame.Seq)
	}

	if !r.lastTimestamp.IsZero() && frame.Timestamp.Before(r.lastTimestamp) {
		skew := r.lastTimestamp.Sub(frame.Timestamp)
		if skew > r.maxClockSkew {
			return fmt.Errorf("%w: regressed by %v (max tolerated: %v)", ErrClockRegression, skew, r.maxClockSkew)
		}
	}

	return nil
}

// Close marks the ratchet as closed, preventing further state mutations.
func (r *MonotoneRatchet) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}
