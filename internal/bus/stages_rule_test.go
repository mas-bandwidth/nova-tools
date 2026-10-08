package bus

import (
	"strconv"
	"strings"
	"time"
)

// Forward is the receipt rule, the one the store's script keeps (forwardLua,
// redis.go) and this package's Fake calls: the value a receipt holding cur
// moves to when state is asked at now, and whether it moves. It moves only
// forward, and only delivered starts one: a message is never read or acted
// before it is delivered. (tla/Bus2Receipts.tla: ReceiptNeverMovesBack,
// ActedImpliesDelivered) bustest/fake.go keeps the same rule for the tests
// outside this package; production runs it only as the script.
func Forward(cur, state string, now time.Time) (string, bool) {
	have, want := rank(stageState(cur)), rank(state)
	if want <= have || (have == 0 && want != 1) {
		return cur, false
	}
	return state + " " + strconv.FormatInt(now.Unix(), 10), true
}

// stageState is the state word of a receipt's value.
func stageState(v string) string {
	s, _, _ := strings.Cut(v, " ")
	return s
}
