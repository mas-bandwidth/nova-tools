package sprint

// Shape is what a sprint keeps across a clear: its streams, its readers, and
// its fleet members with their status and the coordinator's holds. The tables, their columns and the view
// are the table layer's and carry across by themselves.
type Shape struct {
	Streams []string          `json:"streams"`
	Readers []string          `json:"readers"`
	Members []string          `json:"members"`
	Status  map[string]string `json:"status"` // member -> up or down
	Held    []string          `json:"held,omitempty"`
	// HeldBy is who made a member's hold when it was not the coordinator
	// (held_by, fleet_sync.go).
	HeldBy map[string]string `json:"held_by,omitempty"`
	// Width is each member's width its control card names (width.go); a
	// member with the default is not in it.
	Width map[string]string `json:"width,omitempty"`
}

// ShapeOf is a snapshot's shape.
func ShapeOf(s *Snapshot) Shape {
	sh := Shape{Readers: append([]string(nil), s.Readers.Rows()...), Members: append([]string(nil), s.Fleet.Rows()...), Status: map[string]string{}}
	for _, r := range append(append([]string(nil), s.Work.Rows()...), s.Merge.Rows()...) {
		if !contains(sh.Streams, r) {
			sh.Streams = append(sh.Streams, r)
		}
	}
	for _, m := range sh.Members {
		st := s.MemberCtl(m).F("status")
		if st == "" {
			st = Down
		}
		sh.Status[m] = st
		if s.MemberCtl(m).F("held") != "" {
			sh.Held = append(sh.Held, m)
			if by := s.MemberCtl(m).F(FieldHeldBy); by != "" {
				if sh.HeldBy == nil {
					sh.HeldBy = map[string]string{}
				}
				sh.HeldBy[m] = by
			}
		}
		if w := s.MemberCtl(m).F(FieldWidth); w != "" {
			if sh.Width == nil {
				sh.Width = map[string]string{}
			}
			sh.Width[m] = w
		}
	}
	return sh
}

// RestoreShape is the plan that gives a new epoch its control cards: each
// stream waiting, each member with its status and no work counted. A control
// card the epoch has already is left as it is.
func RestoreShape(s *Snapshot, sh Shape) Plan {
	var p Plan
	now := stamp(s.Now)
	for _, st := range sh.Streams {
		if s.Merge.Card(CtlID(st)) != nil {
			continue
		}
		p.Units = append(p.Units, Unit{Key: CtlID(st), Stream: st, Changes: []Change{change(Merge, createEntry(CtlID(st), st, Ctl, 0,
			map[string]string{"kind": "stream", "state": StreamWaiting}))}, Moved: "stream " + st + " waiting"})
	}
	for _, m := range sh.Members {
		if s.Fleet.Card(CtlID(m)) != nil {
			continue
		}
		fields := map[string]string{"kind": "member", "status": sh.Status[m], "since": now}
		if contains(sh.Held, m) {
			fields["held"] = now
			if by := sh.HeldBy[m]; by != "" {
				fields[FieldHeldBy] = by
			}
		}
		if w := sh.Width[m]; w != "" {
			fields[FieldWidth] = w
		}
		p.Units = append(p.Units, Unit{Key: CtlID(m), Changes: []Change{change(Fleet, createEntry(CtlID(m), m, Ctl, 0, fields))}, Moved: "member " + m + " " + sh.Status[m]})
	}
	return p
}
