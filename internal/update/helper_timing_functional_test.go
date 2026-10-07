//go:build functional || slow

package update

import (
	"os"
	"strconv"
	"time"
)

func init() {
	fakeBusHang = hangUntilTheCallerIsGone
	helperTiming = func(a []string) bool {
		switch a[0] {
		case "hang":
			hangUntilTheCallerIsGone()
		case "hold":
			if len(a) > 2 && a[2] != "" {
				_ = os.WriteFile(a[2], []byte(strconv.Itoa(os.Getpid())), 0600)
			}
			d, err := time.ParseDuration(a[1])
			if err != nil {
				os.Exit(6)
			}
			time.Sleep(d)
		case "escaped":
			// Leave a grandchild holding stdout open in its OWN process group, then
			// hang, so the grandchild survives this process's group kill: the
			// escaped-pipe case a deadline must still close.
			readyFile := ""
			if len(a) > 2 {
				readyFile = a[2]
			}
			if err := spawnEscapedHolder(a[1], readyFile); err != nil {
				os.Exit(5)
			}
			hangUntilTheCallerIsGone()
		default:
			return false
		}
		return true
	}
}

// hangUntilTheCallerIsGone is the hang these helpers owe: a child that never
// answers, so the deadline under test is what ends it. That deadline kills with
// a signal this process cannot catch, so the one event a hanging child sees is
// its caller's death -- it is orphaned the moment the caller that has to close
// it is gone -- and the wait is a poll for that event up to NOVA_TEST_WAIT, not
// a fixed sleep (docs/SPEC-CI.md, `waits`; internal/ci/testdata/fixed-waits-allowlist.txt,
// "The allowed shape is a poll up to NOVA_TEST_WAIT (default 30s) or a fake
// clock"). The bound is generous because the event arrives long before it: what
// the poll changes is the case no caller kills, where a child used to outlive
// the run by a fixed thirty seconds and now ends with it.
func hangUntilTheCallerIsGone() {
	caller := os.Getppid()
	if caller <= 1 {
		return
	}
	limit := time.Now().Add(testWait())
	for time.Now().Before(limit) {
		if os.Getppid() != caller {
			return
		}
		time.Sleep(orphanPoll)
	}
}

// orphanPoll is how often the hang looks for its caller's death: the poll's own
// step, under the hundred milliseconds the `waits` rule reads as a fixed sleep.
const orphanPoll = 5 * time.Millisecond

// testWait is the generous poll bound the `waits` rule names: NOVA_TEST_WAIT
// when it is set, thirty seconds otherwise, read at the wait so a loaded
// machine lengthens the wait rather than ending it early.
func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}
