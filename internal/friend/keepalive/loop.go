package keepalive

import (
	"context"
	"fmt"
	"time"
)

// Observation is daemon transport evidence, never native session proof.
// The authority service must atomically fence Seat and deduplicate Evidence.
type Observation struct {
	Friend         string
	Seat           Seat
	DaemonInstance string
	Evidence       uint64
	PongAge        time.Duration
	Asleep         bool
	Up             bool
}

// Loop runs the daemon-only protocol (SPEC-FRIEND); its Store has no ordinary
// bus delivery or acknowledgement methods. All state is volatile.
type Loop struct {
	Name, Role, Instance string
	Store                Store
	Authority            func(context.Context) (Seat, error)
	Peers                func(context.Context) ([]string, error) // configured friends, coordinator only
	Asleep               func(context.Context) (bool, error)     // local saved choice, never changed here
	HealthBatch          func(context.Context, []Observation) error
	Now                  func() time.Time
	Pause                func(context.Context, time.Duration)
	Record               func(string)
}

// Run stops cleanly on cancellation. Store and projection errors are reported
// and retried only on a later tick, with new frames rather than blind reappend.
func (l *Loop) Run(ctx context.Context) error {
	if l.Store == nil || l.Authority == nil || l.Now == nil || l.Pause == nil || l.Record == nil {
		return fmt.Errorf("keepalive loop needs store, authority, clock, pause and record callbacks")
	}
	if l.Role != "friend" && l.Role != "coordinator" {
		return fmt.Errorf("keepalive role wants friend or coordinator")
	}
	if l.Role == "coordinator" && l.Peers == nil {
		return fmt.Errorf("coordinator keepalive needs configured peers")
	}
	machines := map[string]*Machine{}
	cursor := ""
	headed := false
	reset := func() {
		for _, m := range machines {
			m.ResetProof()
		}
	}
	for ctx.Err() == nil {
		now := l.Now()
		deadline := now.Add(time.Second)
		pause := func() {
			if remaining := deadline.Sub(l.Now()); remaining > 0 {
				l.Pause(ctx, remaining)
			}
		}
		seat, err := l.Authority(ctx)
		if err != nil || seat.Holder == "" || seat.Generation == 0 || (l.Role == "coordinator" && seat.Holder != l.Name) {
			reset()
			l.Record(fmt.Sprintf("keepalive authority unavailable or not held: %v", err))
			pause()
			continue
		}
		peers := []string{seat.Holder}
		if l.Role == "coordinator" {
			peers, err = l.Peers(ctx)
			if err != nil {
				l.Record("keepalive configured peers: " + err.Error())
				pause()
				continue
			}
		}
		if len(peers) > BatchLimit {
			return fmt.Errorf("keepalive configured peers exceed %d", BatchLimit)
		}
		asleep := false
		if l.Asleep != nil {
			asleep, err = l.Asleep(ctx)
			if err != nil {
				l.Record("keepalive asleep observation: " + err.Error())
				pause()
				continue
			}
		}
		active := map[string]bool{}
		valid := true
		for _, peer := range peers {
			if peer == l.Name {
				continue
			}
			if active[peer] {
				continue
			}
			active[peer] = true
			if machines[peer] == nil {
				machines[peer], err = New(l.Name, peer, l.Role, l.Instance, seat)
			} else {
				err = machines[peer].SetSeat(seat)
			}
			if err != nil {
				l.Record("keepalive peer: " + err.Error())
				valid = false
				break
			}
		}
		if !valid {
			pause()
			continue
		}
		for peer := range machines {
			if !active[peer] {
				delete(machines, peer)
			}
		}
		if !headed {
			cursor, err = l.Store.Head(ctx, l.Name)
			if err != nil {
				l.Record("keepalive head: " + err.Error())
				pause()
				continue
			}
			headed = true
		}
		read, err := l.Store.ReadBatch(ctx, l.Name, cursor, ReadLimit)
		now = l.Now()
		if err != nil {
			l.Record("keepalive read: " + err.Error())
		} else {
			if read.Next != "" {
				cursor = read.Next
			}
			for _, entry := range read.Entries {
				if entry.DecodeError != "" {
					l.Record("keepalive decode: " + entry.DecodeError)
					continue
				}
				if m := machines[entry.Frame.From]; m != nil {
					if _, err := m.Observe(now, entry.Frame); err != nil {
						l.Record("keepalive observe: " + err.Error())
					}
				}
			}
		}
		// Recheck immediately before external writes; the health sink still needs
		// an atomic seat fence because a handover can race this check.
		current, err := l.Authority(ctx)
		if err != nil || current != seat {
			reset()
			l.Record("keepalive authority changed before writes")
			pause()
			continue
		}
		now = l.Now()
		var frames []Frame
		for _, peer := range peers {
			m := machines[peer]
			if m == nil {
				continue
			}
			f, send, err := m.Next(now, asleep)
			if err != nil {
				l.Record("keepalive next: " + err.Error())
				continue
			}
			if send {
				frames = append(frames, f)
			}
		}
		if len(frames) > 0 {
			receipts, err := l.Store.AppendBatch(ctx, frames)
			if err != nil {
				l.Record("keepalive append: " + err.Error())
			}
			for i, f := range frames {
				if i >= len(receipts) || receipts[i].Unknown || receipts[i].ID == "" {
					l.Record(fmt.Sprintf("keepalive append unknown peer=%s seq=%d", f.To, f.Seq))
				}
			}
		}
		if l.Role == "coordinator" && l.HealthBatch != nil {
			now = l.Now()
			var observations []Observation
			for peer, m := range machines {
				s := m.Status(now)
				age := time.Duration(0)
				if !s.LastPong.IsZero() {
					age = now.Sub(s.LastPong)
				}
				o := Observation{Friend: peer, Seat: seat, DaemonInstance: l.Instance, Evidence: s.LastAckSeq, PongAge: age, Asleep: s.Asleep, Up: s.Up}
				observations = append(observations, o)
			}
			if err := l.HealthBatch(ctx, observations); err != nil {
				l.Record("keepalive health: " + err.Error())
			}
		}
		pause()
	}
	return nil
}
