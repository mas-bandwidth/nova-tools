package width

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FillstateKey returns the key for friend's fillstate hash.
func FillstateKey(friend string) string {
	return "friend:" + friend + ":fillstate"
}

// LogKey is the per-tick sample stream the fold integrates.
const LogKey = "width:log"

// FunctionWrite is the Redis Function name that writes friend fillstate.
const FunctionWrite = "ns_width_write"

// Policy is what one width tick reads from config.
type Policy struct {
	RebalanceTicks int
	Readers        []string
	Builders       []string
	Coordinator    string
}

// Fillstate represents friend:<f>:fillstate data.
type Fillstate struct {
	Friend        string
	Slots         int
	Starting      int
	Living        int
	Leased        int
	Working       int
	Deficit       int
	Eligible      int
	IdleNoReady   int
	IdleDeps      int
	IdleInput     int
	IdleUnfilled  int
	Peak          int
	PeakAt        int64
	UnfilledSince int64
	At            int64
}

// Line returns the WIDTH row string. If stale (> 3s old), prints "?" for all fields.
func (fs Fillstate) Line(nowMs int64) string {
	if fs.At == 0 || nowMs-fs.At > 3000 {
		return fmt.Sprintf("WIDTH %s slots=? starting=? living=? leased=? working=? deficit=? eligible=? idle=? peak=?@? at=?", fs.Friend)
	}
	return fmt.Sprintf("WIDTH %s slots=%d starting=%d living=%d leased=%d working=%d deficit=%d eligible=%d idle=%s peak=%d@%d at=%d",
		fs.Friend, fs.Slots, fs.Starting, fs.Living, fs.Leased, fs.Working, fs.Deficit, fs.Eligible, fs.IdleString(), fs.Peak, fs.PeakAt, fs.At)
}

// IdleString formats the typed idle counts into <reason>:<n>,...
func (fs Fillstate) IdleString() string {
	var parts []string
	if fs.IdleUnfilled > 0 {
		parts = append(parts, fmt.Sprintf("unfilled:%d", fs.IdleUnfilled))
	}
	if fs.IdleDeps > 0 {
		parts = append(parts, fmt.Sprintf("deps:%d", fs.IdleDeps))
	}
	if fs.IdleInput > 0 {
		parts = append(parts, fmt.Sprintf("input:%d", fs.IdleInput))
	}
	if fs.IdleNoReady > 0 {
		parts = append(parts, fmt.Sprintf("no_ready:%d", fs.IdleNoReady))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// FleetLine formats the WIDTH fleet line.
func FleetLine(working, slots, deficit int) string {
	return fmt.Sprintf("WIDTH fleet working=%d/%d deficit=%d", working, slots, deficit)
}

// ParseFillstate parses a Redis HGetAll map into a Fillstate struct.
func ParseFillstate(friend string, m map[string]string) Fillstate {
	slots, _ := strconv.Atoi(m["slots"])
	starting, _ := strconv.Atoi(m["starting"])
	living, _ := strconv.Atoi(m["living"])
	leased, _ := strconv.Atoi(m["leased"])
	working, _ := strconv.Atoi(m["working"])
	deficit, _ := strconv.Atoi(m["deficit"])
	eligible, _ := strconv.Atoi(m["eligible"])
	idleNoReady, _ := strconv.Atoi(m["idle_no_ready"])
	idleDeps, _ := strconv.Atoi(m["idle_deps"])
	idleInput, _ := strconv.Atoi(m["idle_input"])
	idleUnfilled, _ := strconv.Atoi(m["idle_unfilled"])
	peak, _ := strconv.Atoi(m["peak"])
	peakAt, _ := strconv.ParseInt(m["peak_at"], 10, 64)
	unfilledSince, _ := strconv.ParseInt(m["unfilled_since"], 10, 64)
	at, _ := strconv.ParseInt(m["at"], 10, 64)

	return Fillstate{
		Friend:        friend,
		Slots:         slots,
		Starting:      starting,
		Living:        living,
		Leased:        leased,
		Working:       working,
		Deficit:       deficit,
		Eligible:      eligible,
		IdleNoReady:   idleNoReady,
		IdleDeps:      idleDeps,
		IdleInput:     idleInput,
		IdleUnfilled:  idleUnfilled,
		Peak:          peak,
		PeakAt:        peakAt,
		UnfilledSince: unfilledSince,
		At:            at,
	}
}

// ReadFillstate reads friend:<f>:fillstate for one friend.
func ReadFillstate(ctx context.Context, st *store.Store, friend string) (Fillstate, bool, error) {
	if st == nil {
		return Fillstate{}, false, fmt.Errorf("width: nil store")
	}
	m, err := st.Client().HGetAll(ctx, FillstateKey(friend)).Result()
	if err != nil {
		return Fillstate{}, false, fmt.Errorf("width: %w", err)
	}
	if len(m) == 0 {
		return Fillstate{}, false, nil
	}
	return ParseFillstate(friend, m), true, nil
}
