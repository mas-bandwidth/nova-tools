package life

import (
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The CI legs on a bench (nova-tools#4293, Glenn 2026-09-26 ~12:00 PM ET:
// "CI over work is a permanent setting"): a bench's free slots are its
// declared slots minus the CI legs running on it, so the deal never puts a
// copy beside a leg it would slow. The beat counts the legs and carries the
// count as ci; the two deal passes and the in-Redis fill read it there.
//
// A leg is one running GitHub Actions job, and the runner starts exactly
// one Runner.Worker process per job for as long as it runs (Runner.Listener
// is the idle runner, not a leg). Counting those processes needs nothing
// from the runner's own files and is the same on darwin and Linux.

// CIWorkerProcess is the runner's per-job process, one per running leg.
const CIWorkerProcess = "Runner.Worker"

// CountCILegs counts the CI legs in one `ps -axo command=` listing: the
// lines whose program (argv[0], by base name) is CIWorkerProcess.
func CountCILegs(ps string) int {
	n := 0
	for _, line := range strings.Split(ps, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && path.Base(f[0]) == CIWorkerProcess {
			n++
		}
	}
	return n
}

// CILegsNow is the count of CI legs on this machine as the beat's ci field:
// a number, or "" when ps could not be read (the deal then subtracts
// nothing, as before). ps is a process of its own, so the listing is taken
// at most every psEvery and served from the cache between (a leg lives
// minutes; a beat asks once a second).
func CILegsNow() string {
	psMu.Lock()
	defer psMu.Unlock()
	if time.Since(psAt) < psEvery {
		return psLast
	}
	out, err := exec.Command("ps", "-axo", "command=").Output()
	psAt = time.Now()
	if err != nil {
		psLast = ""
		return psLast
	}
	psLast = strconv.Itoa(CountCILegs(string(out)))
	return psLast
}

const psEvery = 3 * time.Second

var (
	psMu   sync.Mutex
	psAt   time.Time
	psLast string
)
