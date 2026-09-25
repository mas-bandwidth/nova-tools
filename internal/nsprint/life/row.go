package life

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// RowStamp is a table row's at: RFC 3339, UTC, to the second (the stamp the
// bash row loops wrote and the live table parses).
func RowStamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// Load1Now is this machine's one-minute load average with two decimals, the
// host row's load cell; "" when it cannot be measured (the cell prints "-").
func Load1Now() string {
	if load, ok := loadavg1(); ok {
		return strconv.FormatFloat(load, 'f', 2, 64)
	}
	return ""
}

// FriendRowRequest is one pass of `nova-sprint friend row` (#3440).
type FriendRowRequest struct {
	Friend string
	Sprint string
	// At is the row's stamp; zero means now.
	At time.Time
}

// FriendRowResult is the row one pass wrote.
type FriendRowResult struct {
	Friend                        string
	Up                            bool
	Ready, Working, Waiting, Done int
	Slots                         string // "" when friend:<f>:desired has no whole-number slots
	At                            string
}

// FriendRow writes friend:<f>, the friend's sprint-table row, in one Redis
// Function call (ns_friend_row): the counts are the SCARDs of the friend-queue
// index sets sprint:<S>:idx:<f>:open|working|waiting|closed, up is the seat's
// own beat, and the whole row is one HSET with one at (#3281). It replaces
// the bash row loop rowan-tools bin/friend-row.
func FriendRow(ctx context.Context, st *store.Store, req FriendRowRequest) (FriendRowResult, error) {
	friend := strings.ToLower(strings.TrimSpace(req.Friend))
	if st == nil || friend == "" || req.Sprint == "" {
		return FriendRowResult{}, fmt.Errorf("friend row: store, friend and sprint are required")
	}
	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	stamp := RowStamp(at)
	reply, err := st.Client().FCall(ctx, FunctionFriendRow, nil, friend, req.Sprint, stamp).Result()
	if err != nil {
		return FriendRowResult{}, fmt.Errorf("friend row %s: %w", friend, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return FriendRowResult{}, fmt.Errorf("friend row %s: unexpected reply %T", friend, reply)
	}
	if status := fmt.Sprint(values[0]); status != "OK" || len(values) < 7 {
		return FriendRowResult{}, fmt.Errorf("friend row %s: %s", friend, status)
	}
	n := func(i int) int {
		v, _ := strconv.Atoi(fmt.Sprint(values[i]))
		return v
	}
	return FriendRowResult{
		Friend: friend, Up: fmt.Sprint(values[1]) == "1",
		Ready: n(2), Working: n(3), Waiting: n(4), Done: n(5),
		Slots: fmt.Sprint(values[6]), At: stamp,
	}, nil
}
