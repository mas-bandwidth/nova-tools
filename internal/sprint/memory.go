package sprint

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ============================================================================
// MemoryCardMachine: In-Memory Dual-Table State Machine Simulator
// ============================================================================

// MemoryCardMachine implements CardMachine in-memory, faithfully simulating the
// dual-table atomic transitions, cell set placements, member hashes, reverse links,
// and invariant fences executed by ns_card_*.
type MemoryCardMachine struct {
	mu sync.RWMutex

	activeEpoch EpochID
	revision    uint64

	// Table cells: table -> row -> col -> map[memberID]float64 (ordered set)
	streamsCells map[string]map[string]map[string]float64
	fleetCells   map[string]map[string]map[string]float64

	// Member hash records
	cards     map[CardID]*CardRecord
	copies    map[string]*CopyRecord
	consumers map[ConsumerID]*ConsumerCapacity
	cardDeps  map[CardID][]CardID
}

// NewMemoryCardMachine initializes an in-memory dual-table card machine.
func NewMemoryCardMachine(epoch EpochID) *MemoryCardMachine {
	return &MemoryCardMachine{
		activeEpoch:  epoch,
		revision:     1,
		streamsCells: make(map[string]map[string]map[string]float64),
		fleetCells:   make(map[string]map[string]map[string]float64),
		cards:        make(map[CardID]*CardRecord),
		copies:       make(map[string]*CopyRecord),
		consumers:    make(map[ConsumerID]*ConsumerCapacity),
		cardDeps:     make(map[CardID][]CardID),
	}
}

// Ensure CardMachine interface implementation.
var _ CardMachine = (*MemoryCardMachine)(nil)

func (m *MemoryCardMachine) SetConsumerCapacity(k ConsumerID, slots int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consumers[k] = &ConsumerCapacity{
		Consumer: k,
		Slots:    slots,
		Working:  0,
		Ready:    0,
		Up:       true,
	}
}

func (m *MemoryCardMachine) SetCardDependencies(c CardID, deps []CardID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cardDeps[c] = deps
}

func (m *MemoryCardMachine) depsMetLocked(c CardID) bool {
	deps, ok := m.cardDeps[c]
	if !ok || len(deps) == 0 {
		return true
	}
	for _, depID := range deps {
		depCard, exists := m.cards[depID]
		if !exists {
			return false
		}
		if depCard.State != CardStateLanded && (depCard.State != CardStateDone || depCard.Outcome != OutcomeOK) {
			return false
		}
	}
	return true
}

func (m *MemoryCardMachine) addCellMember(table, row, col, member string, score float64) {
	var target map[string]map[string]map[string]float64
	if table == TableStreams {
		target = m.streamsCells
	} else {
		target = m.fleetCells
	}
	if _, ok := target[row]; !ok {
		target[row] = make(map[string]map[string]float64)
	}
	if _, ok := target[row][col]; !ok {
		target[row][col] = make(map[string]float64)
	}
	target[row][col][member] = score
}

func (m *MemoryCardMachine) removeCellMember(table, row, col, member string) {
	var target map[string]map[string]map[string]float64
	if table == TableStreams {
		target = m.streamsCells
	} else {
		target = m.fleetCells
	}
	if rows, ok := target[row]; ok {
		if cell, ok := rows[col]; ok {
			delete(cell, member)
		}
	}
}

func (m *MemoryCardMachine) nextReceiptLocked(actor string) *Receipt {
	m.revision++
	return &Receipt{
		ID:      fmt.Sprintf("receipt-%d", m.revision),
		Epoch:   m.activeEpoch,
		Before:  m.revision - 1,
		After:   m.revision,
		Outcome: "OK",
	}
}

// ----------------------------------------------------------------------------
// CardMachine Actions Implementation
// ----------------------------------------------------------------------------

func (m *MemoryCardMachine) Push(ctx context.Context, c CardID, stream string, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	if opt.Epoch != 0 && opt.Epoch != m.activeEpoch {
		return nil, ErrStaleEpoch
	}
	if opt.Actor == "" {
		return nil, ErrAuthorSelfRead
	}

	if _, exists := m.cards[c]; exists {
		return nil, ErrDuplicateMember
	}

	now := time.Now()
	rec := &CardRecord{
		ID:        c,
		Epoch:     m.activeEpoch,
		Stream:    stream,
		State:     CardStateWaiting,
		Placement: StreamPlacement(stream, CardStateWaiting),
		Outcome:   OutcomeUnset,
		LiveReads: []CopyID{},
		Pending:   false,
		LowReads:  0,
		NCut:      0,
		Author:    ConsumerID(opt.Actor),
		Head:      0,
		PRHead:    0,
		CI:        CIPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.cards[c] = rec
	m.addCellMember(TableStreams, stream, string(CardStateWaiting), string(c), float64(now.UnixMilli()))

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) Release(ctx context.Context, c CardID, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.Epoch != 0 && opt.Epoch != m.activeEpoch {
		return nil, ErrStaleEpoch
	}

	card, exists := m.cards[c]
	if !exists {
		return nil, ErrCardNotFound
	}
	if card.State != CardStateWaiting {
		if card.State == CardStateReady {
			return nil, ErrReadyIsOneWay
		}
		return nil, ErrInvalidState
	}

	if !m.depsMetLocked(c) {
		return nil, ErrDepsNotMet
	}

	m.removeCellMember(TableStreams, card.Stream, string(CardStateWaiting), string(c))
	card.State = CardStateReady
	card.Placement = StreamPlacement(card.Stream, CardStateReady)
	card.UpdatedAt = time.Now()
	m.addCellMember(TableStreams, card.Stream, string(CardStateReady), string(c), float64(card.UpdatedAt.UnixMilli()))

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) DealWork(ctx context.Context, params DealWorkParams, opts ...WriteOptions) (*DealWorkResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.Epoch != 0 && opt.Epoch != m.activeEpoch {
		return nil, ErrStaleEpoch
	}

	card, exists := m.cards[params.Card]
	if !exists {
		return nil, ErrCardNotFound
	}

	if card.State == CardStateWorking {
		return nil, ErrWorkingHasCopy
	}
	if card.State.IsTerminal() || card.State == CardStateMerging {
		return nil, ErrTerminalIsQuiet
	}
	if card.State != CardStateWaiting && card.State != CardStateReady {
		return nil, ErrInvalidState
	}

	if !m.depsMetLocked(params.Card) {
		return nil, ErrDepsNotMet
	}

	cap, ok := m.consumers[params.Consumer]
	if !ok {
		cap = &ConsumerCapacity{Consumer: params.Consumer, Slots: 4, Up: true}
		m.consumers[params.Consumer] = cap
	}
	if cap.Room() <= 0 {
		return nil, ErrNoRoom
	}

	attempt := card.NCut + 1
	copyID := CopyID{Card: params.Card, Attempt: attempt}
	now := time.Now()

	m.removeCellMember(TableStreams, card.Stream, string(card.State), string(params.Card))
	card.State = CardStateWorking
	card.Placement = StreamPlacement(card.Stream, CardStateWorking)
	card.LiveCopy = &copyID
	card.NCut = attempt
	card.Author = params.Consumer
	card.UpdatedAt = now
	m.addCellMember(TableStreams, card.Stream, string(CardStateWorking), string(params.Card), float64(now.UnixMilli()))

	copyRec := &CopyRecord{
		ID:        copyID,
		Epoch:     m.activeEpoch,
		Primary:   params.Card,
		Consumer:  params.Consumer,
		Leg:       LegWork,
		State:     CopyStateWorking,
		Placement: FleetPlacement(params.Consumer, CopyStateWorking),
		Attempt:   attempt,
		Leased:    true,
		Score:     params.Score,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.copies[copyID.String()] = copyRec
	m.addCellMember(TableFleet, string(params.Consumer), string(CopyStateWorking), copyID.String(), float64(now.UnixMilli()))

	cap.Working++

	receipt := m.nextReceiptLocked(opt.Actor)
	return &DealWorkResult{
		Receipt: *receipt,
		Copy:    copyID,
	}, nil
}

func (m *MemoryCardMachine) DealRead(ctx context.Context, params DealReadParams, opts ...WriteOptions) (*DealReadResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	card, exists := m.cards[params.Card]
	if !exists {
		return nil, ErrCardNotFound
	}
	if card.State != CardStateReview {
		return nil, ErrReadsInReview
	}
	if params.Consumer == card.Author {
		return nil, ErrAuthorSelfRead
	}

	cap, ok := m.consumers[params.Consumer]
	if !ok {
		cap = &ConsumerCapacity{Consumer: params.Consumer, Slots: 4, Up: true}
		m.consumers[params.Consumer] = cap
	}
	if cap.Room() <= 0 {
		return nil, ErrNoRoom
	}

	attempt := card.NCut + 1
	copyID := CopyID{Card: params.Card, Attempt: attempt}
	now := time.Now()

	card.LiveReads = append(card.LiveReads, copyID)
	card.NCut = attempt
	card.UpdatedAt = now

	copyRec := &CopyRecord{
		ID:        copyID,
		Epoch:     m.activeEpoch,
		Primary:   params.Card,
		Consumer:  params.Consumer,
		Leg:       LegRead,
		State:     CopyStateReady,
		Placement: FleetPlacement(params.Consumer, CopyStateReady),
		Attempt:   attempt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.copies[copyID.String()] = copyRec
	m.addCellMember(TableFleet, string(params.Consumer), string(CopyStateReady), copyID.String(), float64(now.UnixMilli()))
	cap.Ready++

	receipt := m.nextReceiptLocked(opt.Actor)
	return &DealReadResult{
		Receipt: *receipt,
		Copy:    copyID,
	}, nil
}

func (m *MemoryCardMachine) EndWorkPR(ctx context.Context, params EndWorkPRParams, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	card, exists := m.cards[params.Copy.Card]
	if !exists {
		return nil, ErrCardNotFound
	}
	copyRec, exists := m.copies[params.Copy.String()]
	if !exists {
		return nil, ErrCopyNotFound
	}

	now := time.Now()

	m.removeCellMember(TableStreams, card.Stream, string(CardStateWorking), string(card.ID))
	card.State = CardStateReview
	card.Placement = StreamPlacement(card.Stream, CardStateReview)
	card.LiveCopy = nil
	card.PRHead++
	card.Head = card.PRHead
	card.CI = CIPending
	card.UpdatedAt = now
	m.addCellMember(TableStreams, card.Stream, string(CardStateReview), string(card.ID), float64(now.UnixMilli()))

	m.removeCellMember(TableFleet, string(copyRec.Consumer), string(CopyStateWorking), copyRec.ID.String())
	copyRec.State = CopyStateOK
	copyRec.Placement = FleetPlacement(copyRec.Consumer, CopyStateOK)
	copyRec.UpdatedAt = now
	m.addCellMember(TableFleet, string(copyRec.Consumer), string(CopyStateOK), copyRec.ID.String(), float64(now.UnixMilli()))

	if cap, ok := m.consumers[copyRec.Consumer]; ok && cap.Working > 0 {
		cap.Working--
	}

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) EndWorkDone(ctx context.Context, params EndWorkDoneParams, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	card, exists := m.cards[params.Copy.Card]
	if !exists {
		return nil, ErrCardNotFound
	}
	copyRec, exists := m.copies[params.Copy.String()]
	if !exists {
		return nil, ErrCopyNotFound
	}

	targetState := CardStateDone
	if params.ToLanded {
		targetState = CardStateLanded
	}

	now := time.Now()

	m.removeCellMember(TableStreams, card.Stream, string(card.State), string(card.ID))
	card.State = targetState
	card.Placement = StreamPlacement(card.Stream, targetState)
	card.Outcome = params.Outcome
	card.LiveCopy = nil
	card.UpdatedAt = now
	m.addCellMember(TableStreams, card.Stream, string(targetState), string(card.ID), float64(now.UnixMilli()))

	m.removeCellMember(TableFleet, string(copyRec.Consumer), string(copyRec.State), copyRec.ID.String())
	copyRec.State = CopyStateOK
	copyRec.Placement = FleetPlacement(copyRec.Consumer, CopyStateOK)
	copyRec.UpdatedAt = now
	m.addCellMember(TableFleet, string(copyRec.Consumer), string(CopyStateOK), copyRec.ID.String(), float64(now.UnixMilli()))

	if cap, ok := m.consumers[copyRec.Consumer]; ok && cap.Working > 0 {
		cap.Working--
	}

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) EndReadHigh(ctx context.Context, params EndReadHighParams, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	card, exists := m.cards[params.Copy.Card]
	if !exists {
		return nil, ErrCardNotFound
	}
	readerCopy, exists := m.copies[params.Copy.String()]
	if !exists {
		return nil, ErrCopyNotFound
	}

	now := time.Now()

	m.removeCellMember(TableStreams, card.Stream, string(card.State), string(card.ID))
	card.State = CardStateMerging
	card.Placement = StreamPlacement(card.Stream, CardStateMerging)
	card.UpdatedAt = now
	m.addCellMember(TableStreams, card.Stream, string(CardStateMerging), string(card.ID), float64(now.UnixMilli()))

	m.removeCellMember(TableFleet, string(readerCopy.Consumer), string(readerCopy.State), readerCopy.ID.String())
	readerCopy.State = CopyStateOK
	readerCopy.Placement = FleetPlacement(readerCopy.Consumer, CopyStateOK)
	readerCopy.UpdatedAt = now
	m.addCellMember(TableFleet, string(readerCopy.Consumer), string(CopyStateOK), readerCopy.ID.String(), float64(now.UnixMilli()))

	for _, readID := range card.LiveReads {
		if readID.String() != params.Copy.String() {
			if rCopy, ok := m.copies[readID.String()]; ok && rCopy.State.IsLive() {
				m.removeCellMember(TableFleet, string(rCopy.Consumer), string(rCopy.State), rCopy.ID.String())
				rCopy.State = CopyStateFail
				rCopy.Placement = FleetPlacement(rCopy.Consumer, CopyStateFail)
				rCopy.UpdatedAt = now
				m.addCellMember(TableFleet, string(rCopy.Consumer), string(CopyStateFail), rCopy.ID.String(), float64(now.UnixMilli()))
			}
		}
	}
	card.LiveReads = []CopyID{}

	if card.LiveCopy != nil {
		if fCopy, ok := m.copies[card.LiveCopy.String()]; ok && fCopy.State.IsLive() {
			m.removeCellMember(TableFleet, string(fCopy.Consumer), string(fCopy.State), fCopy.ID.String())
			fCopy.State = CopyStateFail
			fCopy.Placement = FleetPlacement(fCopy.Consumer, CopyStateFail)
			fCopy.UpdatedAt = now
			m.addCellMember(TableFleet, string(fCopy.Consumer), string(CopyStateFail), fCopy.ID.String(), float64(now.UnixMilli()))
		}
		card.LiveCopy = nil
	}

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) EndReadLow(ctx context.Context, params EndReadLowParams, opts ...WriteOptions) (*EndReadLowResult, error) {
	return &EndReadLowResult{}, nil
}

func (m *MemoryCardMachine) EndFixOK(ctx context.Context, params EndFixOKParams, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) Verdict(ctx context.Context, params VerdictParams, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) Land(ctx context.Context, c CardID, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.Epoch != 0 && opt.Epoch != m.activeEpoch {
		return nil, ErrStaleEpoch
	}

	card, exists := m.cards[c]
	if !exists {
		return nil, ErrCardNotFound
	}
	if card.State != CardStateMerging {
		if card.State == CardStateLanded {
			return nil, ErrTerminalIsQuiet
		}
		return nil, ErrInvalidState
	}

	now := time.Now()

	m.removeCellMember(TableStreams, card.Stream, string(CardStateMerging), string(c))
	card.State = CardStateLanded
	card.Placement = StreamPlacement(card.Stream, CardStateLanded)
	card.Outcome = OutcomeOK
	card.UpdatedAt = now
	m.addCellMember(TableStreams, card.Stream, string(CardStateLanded), string(c), float64(now.UnixMilli()))

	if card.LiveCopy != nil {
		if copyRec, ok := m.copies[card.LiveCopy.String()]; ok && copyRec.State.IsLive() {
			m.removeCellMember(TableFleet, string(copyRec.Consumer), string(copyRec.State), copyRec.ID.String())
			copyRec.State = CopyStateOK
			copyRec.Placement = FleetPlacement(copyRec.Consumer, CopyStateOK)
			copyRec.UpdatedAt = now
			m.addCellMember(TableFleet, string(copyRec.Consumer), string(CopyStateOK), copyRec.ID.String(), float64(now.UnixMilli()))
		}
		card.LiveCopy = nil
	}
	for _, readID := range card.LiveReads {
		if rCopy, ok := m.copies[readID.String()]; ok && rCopy.State.IsLive() {
			m.removeCellMember(TableFleet, string(rCopy.Consumer), string(rCopy.State), rCopy.ID.String())
			rCopy.State = CopyStateFail
			rCopy.Placement = FleetPlacement(rCopy.Consumer, CopyStateFail)
			rCopy.UpdatedAt = now
			m.addCellMember(TableFleet, string(rCopy.Consumer), string(CopyStateFail), rCopy.ID.String(), float64(now.UnixMilli()))
		}
	}
	card.LiveReads = []CopyID{}

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) CancelPrimary(ctx context.Context, params CancelPrimaryParams, opts ...WriteOptions) (*Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opt WriteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	card, exists := m.cards[params.Card]
	if !exists {
		return nil, ErrCardNotFound
	}
	if card.State.IsTerminal() {
		return nil, ErrTerminalIsQuiet
	}

	now := time.Now()
	m.removeCellMember(TableStreams, card.Stream, string(card.State), string(card.ID))
	card.State = CardStateDone
	card.Outcome = OutcomeFail
	card.Placement = StreamPlacement(card.Stream, CardStateDone)
	card.UpdatedAt = now
	m.addCellMember(TableStreams, card.Stream, string(CardStateDone), string(card.ID), float64(now.UnixMilli()))

	if card.LiveCopy != nil {
		if cp, ok := m.copies[card.LiveCopy.String()]; ok && cp.State.IsLive() {
			m.removeCellMember(TableFleet, string(cp.Consumer), string(cp.State), cp.ID.String())
			cp.State = CopyStateFail
			cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
			cp.UpdatedAt = now
			m.addCellMember(TableFleet, string(cp.Consumer), string(CopyStateFail), cp.ID.String(), float64(now.UnixMilli()))
		}
		card.LiveCopy = nil
	}
	for _, rID := range card.LiveReads {
		if cp, ok := m.copies[rID.String()]; ok && cp.State.IsLive() {
			m.removeCellMember(TableFleet, string(cp.Consumer), string(cp.State), cp.ID.String())
			cp.State = CopyStateFail
			cp.Placement = FleetPlacement(cp.Consumer, CopyStateFail)
			cp.UpdatedAt = now
			m.addCellMember(TableFleet, string(cp.Consumer), string(CopyStateFail), cp.ID.String(), float64(now.UnixMilli()))
		}
	}
	card.LiveReads = []CopyID{}

	return m.nextReceiptLocked(opt.Actor), nil
}

func (m *MemoryCardMachine) Work(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) Beat(ctx context.Context, i CopyID, leaseDuration time.Duration, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) GiveBack(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) Expire(ctx context.Context, i CopyID, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) EndFail(ctx context.Context, i CopyID, reason string, opts ...WriteOptions) (*Receipt, error) {
	return nil, nil
}

func (m *MemoryCardMachine) LandEvent(ctx context.Context, c CardID, opts ...WriteOptions) (*Receipt, error) {
	return m.Land(ctx, c, opts...)
}

func (m *MemoryCardMachine) GetCard(ctx context.Context, c CardID) (*CardRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	card, ok := m.cards[c]
	if !ok {
		return nil, ErrCardNotFound
	}
	return card, nil
}

func (m *MemoryCardMachine) GetCopy(ctx context.Context, i CopyID) (*CopyRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp, ok := m.copies[i.String()]
	if !ok {
		return nil, ErrCopyNotFound
	}
	return cp, nil
}

func (m *MemoryCardMachine) GetConsumerCapacity(ctx context.Context, k ConsumerID) (*ConsumerCapacity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cap, ok := m.consumers[k]
	if !ok {
		return nil, nil
	}
	return cap, nil
}

// ============================================================================
// Deterministic State Hash Calculation
// ============================================================================

// StateHash computes a canonical, byte-for-byte SHA-256 hash of the entire in-memory
// state machine (cards, copies, cell sets, consumers, epoch, and revisions).
func (m *MemoryCardMachine) StateHash() [32]byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	h := sha256.New()

	// 1. Revision & Active Epoch
	binary.Write(h, binary.BigEndian, m.revision)
	binary.Write(h, binary.BigEndian, uint64(m.activeEpoch))

	// 2. Sorted Cards
	cardIDs := make([]string, 0, len(m.cards))
	for id := range m.cards {
		cardIDs = append(cardIDs, string(id))
	}
	sort.Strings(cardIDs)
	for _, idStr := range cardIDs {
		c := m.cards[CardID(idStr)]
		h.Write([]byte(c.ID))
		binary.Write(h, binary.BigEndian, uint64(c.Epoch))
		h.Write([]byte(c.Stream))
		h.Write([]byte(c.State))
		h.Write([]byte(c.Placement.String()))
		h.Write([]byte(c.Outcome))
		if c.LiveCopy != nil {
			h.Write([]byte(c.LiveCopy.String()))
		}
		for _, r := range c.LiveReads {
			h.Write([]byte(r.String()))
		}
		binary.Write(h, binary.BigEndian, int32(c.NCut))
		binary.Write(h, binary.BigEndian, int32(c.Head))
		binary.Write(h, binary.BigEndian, int32(c.PRHead))
		h.Write([]byte(c.Author))
		h.Write([]byte(c.CI))
	}

	// 3. Sorted Copies
	copyIDs := make([]string, 0, len(m.copies))
	for id := range m.copies {
		copyIDs = append(copyIDs, id)
	}
	sort.Strings(copyIDs)
	for _, id := range copyIDs {
		cp := m.copies[id]
		h.Write([]byte(cp.ID.String()))
		binary.Write(h, binary.BigEndian, uint64(cp.Epoch))
		h.Write([]byte(cp.Primary))
		h.Write([]byte(cp.Consumer))
		h.Write([]byte(cp.Leg))
		h.Write([]byte(cp.State))
		h.Write([]byte(cp.Placement.String()))
		binary.Write(h, binary.BigEndian, int32(cp.Attempt))
	}

	// 4. Sorted Cell sets (streams and fleet)
	hashTable := func(table map[string]map[string]map[string]float64) {
		rows := make([]string, 0, len(table))
		for r := range table {
			rows = append(rows, r)
		}
		sort.Strings(rows)
		for _, r := range rows {
			h.Write([]byte(r))
			cols := make([]string, 0, len(table[r]))
			for col := range table[r] {
				cols = append(cols, col)
			}
			sort.Strings(cols)
			for _, col := range cols {
				h.Write([]byte(col))
				members := make([]string, 0, len(table[r][col]))
				for mem := range table[r][col] {
					members = append(members, mem)
				}
				sort.Strings(members)
				for _, mem := range members {
					h.Write([]byte(mem))
				}
			}
		}
	}
	hashTable(m.streamsCells)
	hashTable(m.fleetCells)

	// 5. Consumers
	consumerIDs := make([]string, 0, len(m.consumers))
	for id := range m.consumers {
		consumerIDs = append(consumerIDs, string(id))
	}
	sort.Strings(consumerIDs)
	for _, idStr := range consumerIDs {
		cap := m.consumers[ConsumerID(idStr)]
		h.Write([]byte(cap.Consumer))
		binary.Write(h, binary.BigEndian, int32(cap.Slots))
		binary.Write(h, binary.BigEndian, int32(cap.Working))
		binary.Write(h, binary.BigEndian, int32(cap.Ready))
	}

	var res [32]byte
	copy(res[:], h.Sum(nil))
	return res
}
