package fleet

// THE CERTIFICATION COLUMN. The record exists, the loop writes it, and nobody looking at the
// fleet could see it: `fleet certify --status` is a verb somebody has to remember to run,
// and the page everybody already has open said nothing about whether the machines it lists
// can still do the work their roles imply.
//
// A summary is read from the certificates file ALONE. No ssh, no forge, no registry: the
// page says what the RECORD says, which is the only thing it can say honestly about a
// machine it has not asked.
//
// "Current" here is the record's own rule, narrowed to what a reader can check without
// leaving the file: the newest row per class, held against the (build, standard) pair of the
// machine's OWN newest row. A certificate written before an adopt, or under a standard that
// has moved, does not count -- exactly as `Certified` does not count it -- and that is why
// the pair is read off the machine rather than assumed to be one fleet-wide pair. The
// machines do not all take a release at the same minute.

import (
	"fmt"
	"sort"
	"time"
)

// Certification is one machine's column: how many of its recorded classes are current, how
// many there are, and how many are not. `Known` is false for a machine with no row at all,
// and the page prints a dash for it -- a zero would read as "nothing is certified", which is
// a claim, and "nobody has ever run it here" is not that claim.
type Certification struct {
	Machine   string
	Certified int
	Total     int
	Stale     int
	Known     bool
	// Build and Hash are the pair the current rows were written under, for the page to show
	// beside the count: two machines both saying 11/14 under different builds is a fleet
	// mid-release, and a reader who cannot see that reads it as two machines agreeing.
	Build string
	Hash  string
}

// Summarize reads one machine's column out of the whole certificates file.
func Summarize(certs []Certificate, machine string, now time.Time, maxAge time.Duration) Certification {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	out := Certification{Machine: machine}
	newest := map[string]Certificate{}
	var latest *Certificate
	for i := range certs {
		c := certs[i]
		if c.Machine != machine {
			continue
		}
		if held, ok := newest[c.Class]; !ok || !c.At.Before(held.At) {
			newest[c.Class] = c
		}
		if latest == nil || !c.At.Before(latest.At) {
			latest = &certs[i]
		}
	}
	if latest == nil {
		return out
	}
	out.Known = true
	out.Build, out.Hash = latest.Build, latest.Hash
	classes := make([]string, 0, len(newest))
	for class := range newest {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		c := newest[class]
		out.Total++
		current := c.Build == out.Build && c.Hash == out.Hash &&
			(c.Verdict == VerdictOK || c.Verdict == VerdictWarn) &&
			now.Sub(c.At) <= maxAge
		if current {
			out.Certified++
			continue
		}
		out.Stale++
	}
	return out
}

// SummarizeAll is one column per named machine, in the order given, so a page's rows keep
// the order its own fleet file has.
func SummarizeAll(certs []Certificate, machines []string, now time.Time, maxAge time.Duration) []Certification {
	out := make([]Certification, 0, len(machines))
	for _, m := range machines {
		out = append(out, Summarize(certs, m, now, maxAge))
	}
	return out
}

// Column is the cell the page prints: `certified=<k>/<n> stale=<m>`, or `-` for a machine
// nobody has ever certified.
func (c Certification) Column() string {
	if !c.Known {
		return "-"
	}
	return fmt.Sprintf("certified=%d/%d stale=%d", c.Certified, c.Total, c.Stale)
}
