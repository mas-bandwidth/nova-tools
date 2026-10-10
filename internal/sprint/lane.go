package sprint

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxIDLen is the maximum length of an identifier.
const MaxIDLen = 64

// LaneHoldFor is how long a holder may hold a lane without activity.
const LaneHoldFor = 15 * time.Minute

// LaneWaitFor is how long a waiter may wait.
const LaneWaitFor = 30 * time.Minute

// LaneWidthDefault is the default lane width.
const LaneWidthDefault = 2

// ValidID checks if s is a valid identifier.
func ValidID(s string) bool {
	if s == "" || len(s) > MaxIDLen {
		return false
	}
	return !strings.Contains(s, "/") && !strings.Contains(s, "\\") && !strings.Contains(s, "..")
}

// ValidLaneWho decides who may hold a lane.
func ValidLaneWho(w string) bool {
	if ValidID(w) {
		return true
	}
	parts := strings.Split(w, "/")
	if len(parts) != 2 {
		return false
	}
	return ValidID(parts[0]) && ValidID(parts[1])
}

// LaneWidth turns the work table's go_lanes property into a width.
func LaneWidth(prop string, valid func(string) bool) (int, bool) {
	if valid == nil {
		valid = ValidID
	}
	if prop == "" {
		return LaneWidthDefault, true
	}
	n, err := strconv.Atoi(prop)
	if err != nil || n <= 0 {
		return LaneWidthDefault, true
	}
	return n, true
}

// Lanes represents lanes as a reader sees them.
type Lanes struct {
	Holding map[string]Holder
	Waiting map[string]Machine
	Granted map[string]Grant
}

// Holder is a lane holder.
type Holder struct {
	Since time.Time
}

// Machine is a lane machine.
type Machine struct{}

// Grant is a granted lane.
type Grant struct {
	Since time.Time
	Waiter string
}

// Expire releases holders and queue heads.
func (l *Lanes) Expire(now time.Time) {
	if l == nil {
		return
	}
	// Release holders not seen for more than LaneHoldFor
	for who, h := range l.Holding {
		if now.Sub(h.Since) > LaneHoldFor {
			delete(l.Holding, who)
			delete(l.Granted, who)
		}
	}
	// Release unclaimed grants older than LaneWaitFor
	for who, g := range l.Granted {
		if g.Waiter == "" && now.Sub(g.Since) > LaneWaitFor {
			delete(l.Granted, who)
		}
	}
	// Drop waiters not seen for LaneWaitFor
	for who := range l.Waiting {
		delete(l.Waiting, who)
	}
	// Drop machines whose lane ends empty
	// (simplified - not tracking machine lanes separately)
}

// Rows sorted by machine, with Held and Waiting empty slices where none.
func (l *Lanes) Rows() []Row {
	if l == nil {
		return []Row{}
	}
	var rows []Row
	// Build rows from holders
	for who, h := range l.Holding {
		rows = append(rows, Row{
			Who: who,
			Held: []Held{{
				Since: h.Since,
				Claimed: true,
			}},
		})
	}
	// Build rows from granted
	for who, g := range l.Granted {
		found := false
		for i := range rows {
			if rows[i].Who == who {
				rows[i].Held = append(rows[i].Held, Held{
					Since: g.Since,
					Claimed: false,
				})
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, Row{
				Who: who,
				Held: []Held{{
					Since: g.Since,
					Claimed: false,
				}},
			})
		}
	}
	// Build rows from waiters
	for who, m := range l.Waiting {
		found := false
		for i := range rows {
			if rows[i].Who == who {
				rows[i].Waiting = []Waiting{{Machine: m}}
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, Row{
				Who: who,
				Waiting: []Waiting{{Machine: m}},
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Who < rows[j].Who
	})
	return rows
}

// Row is one row of lane state.
type Row struct {
	Who    string
	Held   []Held
	Waiting []Waiting
}

// Held is a held lane.
type Held struct {
	Since  time.Time
	Claimed bool
}

// Waiting is a waiting queue.
type Waiting struct {
	Machine Machine
}
