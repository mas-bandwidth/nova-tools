package sprint

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The tick's gc (docs/SPEC-SPRINT.md section 1, "gc"): the sprint's run loop runs nova-sprint
// gc on every machine, its own and each machine row of the config through the fleet runner,
// once every GCEvery and, between, as soon as a machine's fullest volume is at
// GCVolumeAlarm or above, at most once every GCVolumeRetry while it stays there (a volume
// gc cannot bring down is not walked every minute). GCDue is that rule over what the loop
// last read; the loop is cmd/nova-sprint/gc.go.

// GCEvery is how often gc runs on every machine.
const GCEvery = time.Hour

// GCVolumeAlarm is the use, in percent, of a machine's fullest volume at which gc runs on
// it before its hour is up.
const GCVolumeAlarm = 80

// GCVolumeRetry is the least time between two runs a full volume starts.
const GCVolumeRetry = 10 * time.Minute

// GCProbeEvery is how often the loop reads each machine's volume between its runs.
const GCProbeEvery = 5 * time.Minute

// GCMachine is what the loop knows of one machine: when gc last ran there (zero: never)
// and its fullest volume's use as last read (-1: not read).
type GCMachine struct {
	Name   string
	Last   time.Time
	Volume int
}

// GCRun is one gc the loop owes: the machine and why.
type GCRun struct {
	Machine, Why string
}

// GCDue is each machine gc is due on at now, in the order given: never run, or its hour is
// up; or its volume is at the alarm and its last run is GCVolumeRetry old.
func GCDue(ms []GCMachine, now time.Time) []GCRun {
	var out []GCRun
	for _, m := range ms {
		since := now.Sub(m.Last)
		switch {
		case m.Last.IsZero():
			out = append(out, GCRun{m.Name, "first"})
		case since >= GCEvery:
			out = append(out, GCRun{m.Name, "hourly"})
		case m.Volume >= GCVolumeAlarm && since >= GCVolumeRetry:
			out = append(out, GCRun{m.Name, fmt.Sprintf("volume %d%% >= %d%%", m.Volume, GCVolumeAlarm)})
		}
	}
	return out
}

// gcVolumeRE is the volume a gc's summary line says (GC OK|INCOMPLETE freed=<n> volume=<p>%).
var gcVolumeRE = regexp.MustCompile(`(?m)^GC (?:OK|INCOMPLETE) freed=\d+ volume=(\d+)%`)

// GCVolumeIn is the fullest use, in percent, that out says: a gc's summary line, else
// the Capacity column of df -P's lines (the probe GCProbeLine runs); ok is false when it
// says none.
func GCVolumeIn(out string) (use int, ok bool) {
	if m := gcVolumeRE.FindAllStringSubmatch(out, -1); len(m) > 0 {
		n, err := strconv.Atoi(m[len(m)-1][1])
		return n, err == nil
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.HasSuffix(f[4], "%") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSuffix(f[4], "%")); err == nil && n >= use {
			use, ok = n, true
		}
	}
	return use, ok
}

// GCProbeLine is the remote line that reads a machine's volumes between its runs: df -P of
// the home, the bench root and the AI root (those that exist).
const GCProbeLine = `df -Pk "$HOME" "$HOME/nova-bench" "${NOVA_AI_ROOT:-$HOME/ai}" 2>/dev/null; true`
