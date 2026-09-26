package life

import (
	"os/exec"
	"sync"
	"time"
)

// cpuRead asks top for one CPU usage line ("CPU usage: 13.22% user, 37.47%
// sys, 49.31% idle"); busy is 100 minus idle. top is a process of its own,
// so the sample is taken at most every topEvery and served from the cache
// between (the beat asks once a second; the table's cell needs no better).
func cpuRead() cpuSample {
	topMu.Lock()
	defer topMu.Unlock()
	if time.Since(topAt) < topEvery && topLast.ok {
		return topLast
	}
	out, err := exec.Command("/usr/bin/top", "-l", "1", "-n", "0", "-s", "0").Output()
	topAt = time.Now()
	if err != nil {
		topLast = cpuSample{}
		return topLast
	}
	topLast = parseTopCPU(string(out))
	return topLast
}

const topEvery = 10 * time.Second

var (
	topMu   sync.Mutex
	topAt   time.Time
	topLast cpuSample
)
