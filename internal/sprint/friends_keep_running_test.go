package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An out-of-credit fleet stops the machine only when no friend is up: with one up, the
// all-out judgment and the stop go and each provider's own judgment stays.
func TestTheMachineKeepsRunningWhileAFriendIsUp(t *testing.T) {
	t.Parallel()
	conds := []cond{{typ: NProviderFunds, what: "provider p is out of credit"}, {typ: NAllOutOfCredit, what: FundsCause}}
	for _, tc := range []struct {
		name    string
		friends []FriendSeat
		stop    bool
	}{
		{"a friend up keeps it running", []FriendSeat{{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"}}, false},
		{"no friend up stops it", []FriendSeat{{Name: "amy", Width: 8, Status: "down"}}, true},
		{"a friend up with no width stops it", []FriendSeat{{Name: "amy", Width: 0, Status: Up, Class: "flash,pro"}}, true},
		{"no roster stops it", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, stop := friendsKeepRunning(append([]cond(nil), conds...), FundsCause, tc.friends)
			if tc.stop {
				assert.Equal(t, FundsCause, stop)
				assert.Len(t, got, 2)
				return
			}
			assert.Empty(t, stop)
			if assert.Len(t, got, 1) {
				assert.Equal(t, NProviderFunds, got[0].typ, "the provider's own judgment stays")
			}
		})
	}
}
