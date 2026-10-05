package friend

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FriendsTableRow is one row of the friends table as read by the coordinator's
// wake ping loop.
type FriendsTableRow struct {
	Name          string `json:"name"`
	Status        string `json:"status"` // "up", "held", "down"
	NeverWake     bool   `json:"never_wake,omitempty"`
	NeverWakeDash bool   `json:"never-wake,omitempty"`
	Class         string `json:"class,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Mode          string `json:"mode,omitempty"`
}

// IsNeverWake reports whether the friend is marked never-wake on its row.
func (r FriendsTableRow) IsNeverWake() bool {
	if r.NeverWake || r.NeverWakeDash {
		return true
	}
	for _, s := range []string{r.Class, r.Reason, r.Mode} {
		low := strings.ToLower(s)
		if strings.Contains(low, "never-wake") || strings.Contains(low, "never_wake") {
			return true
		}
	}
	return false
}

// Skipped reports whether this row should be skipped by the wake ping loop
// (a friend held, down, or with never-wake on its row).
func (r FriendsTableRow) Skipped() bool {
	return r.Status == "held" || r.Status == "down" || r.IsNeverWake()
}

// ParseFriendsTable parses JSON representing the friends table: either a slice
// of FriendsTableRow or an object containing a "friends" array (such as where --json).
func ParseFriendsTable(data []byte) ([]FriendsTableRow, error) {
	var list []FriendsTableRow
	if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	var wrapper struct {
		Friends []FriendsTableRow          `json:"friends"`
		Tables  map[string]json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil {
		if len(wrapper.Friends) > 0 {
			return wrapper.Friends, nil
		}
		if raw, ok := wrapper.Tables["friends"]; ok {
			var tblList []FriendsTableRow
			if err := json.Unmarshal(raw, &tblList); err == nil {
				return tblList, nil
			}
		}
	}
	return nil, fmt.Errorf("the friends table cannot be parsed")
}
