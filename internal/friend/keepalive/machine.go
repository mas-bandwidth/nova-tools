package keepalive

import (
	"fmt"
	"math"
	"time"
)

const (
	Every            = time.Second
	Window           = 10 * time.Second
	OutstandingLimit = 11
)

type issued struct {
	seq uint64
	at  time.Time
}

// Machine owns one peer's volatile proof. Its caller serializes access and
// supplies one local monotonic clock; transport success is never proof.
type Machine struct {
	Every                       time.Duration // default1s; supported range1s..5s
	local, peer, role, instance string
	seat                        Seat
	seq, consumed               uint64
	issued                      []issued
	lastIssue                   time.Time
	hasIssued                   bool
	ackInstance                 string
	ackSeq                      uint64
	peerInstance                string
	peerSeq                     uint64
	lastPong                    time.Time
	proved, asleep              bool
}

type Status struct {
	LastAckSeq   uint64
	Up           bool
	Asleep       bool
	LastPong     time.Time
	PeerInstance string
	Outstanding  int
}

func New(local, peer, role, instance string, seat Seat) (*Machine, error) {
	m := &Machine{Every: Every, local: local, peer: peer, role: role, instance: instance, seat: seat}
	if err := m.frame(1, false).Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Machine) frame(seq uint64, asleep bool) Frame {
	return Frame{Version: Version, From: m.local, To: m.peer, Role: m.role, Seat: m.seat,
		Instance: m.instance, Seq: seq, AckInstance: m.ackInstance, AckSeq: m.ackSeq, Asleep: asleep}
}

// ResetProof fails closed on lost authority. Sequences never reset within an
// invocation, so a later restored seat cannot accept an earlier challenge.
func (m *Machine) ResetProof() {
	m.issued = nil
	m.proved, m.asleep = false, false
	m.peerInstance, m.ackInstance = "", ""
	m.peerSeq, m.ackSeq = 0, 0
	m.lastPong = time.Time{}
	// Permit immediate emission after authority is restored, not catch-up.
	m.hasIssued = false
}

func (m *Machine) SetSeat(seat Seat) error {
	peer := m.peer
	if m.role == "friend" {
		peer = seat.Holder
	}
	trial := m.frame(1, false)
	trial.Seat, trial.To = seat, peer
	if err := trial.Validate(); err != nil {
		return err
	}
	if seat != m.seat {
		m.ResetProof()
		m.seat, m.peer = seat, peer
	}
	return nil
}

func (m *Machine) expire(now time.Time) {
	kept := m.issued[:0]
	for _, challenge := range m.issued {
		age := now.Sub(challenge.at)
		if age >= 0 && age < Window {
			kept = append(kept, challenge)
		}
	}
	m.issued = kept
}

// Next emits at most once each second. Missed ticks are skipped; a challenge
// is registered before AppendBatch so an uncertain send can still be answered.
func (m *Machine) Next(now time.Time, asleep bool) (Frame, bool, error) {
	if m.Every < Every || m.Every > 5*time.Second {
		return Frame{}, false, fmt.Errorf("keepalive cadence wants 1s..5s")
	}
	if m.hasIssued {
		age := now.Sub(m.lastIssue)
		if age < 0 {
			return Frame{}, false, fmt.Errorf("keepalive local clock moved backwards")
		}
		if age < m.Every {
			return Frame{}, false, nil
		}
	}
	m.expire(now)
	if m.seq == math.MaxUint64 {
		return Frame{}, false, fmt.Errorf("keepalive sequence exhausted; start a new invocation")
	}
	if len(m.issued) >= OutstandingLimit {
		return Frame{}, false, fmt.Errorf("keepalive outstanding challenge limit reached")
	}
	m.seq++
	m.issued = append(m.issued, issued{m.seq, now})
	m.lastIssue, m.hasIssued = now, true
	return m.frame(m.seq, asleep), true, nil
}

// Observe learns a challenge for the next piggyback ACK without granting up.
// Only a fresh, unconsumed acknowledgement of our own invocation proves life.
// Routing metadata is trusted convention, not cryptographic authentication.
func (m *Machine) Observe(now time.Time, f Frame) (bool, error) {
	if err := f.Validate(); err != nil {
		return false, err
	}
	wantRole := "friend"
	if m.role == "friend" {
		wantRole = "coordinator"
	}
	if f.From != m.peer || f.To != m.local || f.Role != wantRole || f.Seat != m.seat {
		return false, fmt.Errorf("keepalive frame does not match peer, role or current seat")
	}
	if f.Instance != m.ackInstance || f.Seq > m.ackSeq {
		m.ackInstance, m.ackSeq = f.Instance, f.Seq
	}
	if f.AckInstance != m.instance || f.AckSeq <= m.consumed {
		return false, nil
	}
	var match *issued
	for i := range m.issued {
		if m.issued[i].seq == f.AckSeq {
			match = &m.issued[i]
			break
		}
	}
	if match == nil {
		return false, nil
	}
	age := now.Sub(match.at)
	if age < 0 || age >= Window {
		return false, nil
	}
	if f.Instance == m.peerInstance && f.Seq <= m.peerSeq {
		return false, nil
	}
	m.consumed = f.AckSeq
	kept := m.issued[:0]
	for _, challenge := range m.issued {
		if challenge.seq > m.consumed {
			kept = append(kept, challenge)
		}
	}
	m.issued = kept
	m.peerInstance, m.peerSeq = f.Instance, f.Seq
	m.lastPong, m.proved, m.asleep = now, true, f.Asleep
	return true, nil
}

func (m *Machine) Status(now time.Time) Status {
	m.expire(now)
	age := now.Sub(m.lastPong)
	up := m.proved && age >= 0 && age < Window
	return Status{LastAckSeq: m.consumed, Up: up, Asleep: up && m.asleep, LastPong: m.lastPong,
		PeerInstance: m.peerInstance, Outstanding: len(m.issued)}
}
