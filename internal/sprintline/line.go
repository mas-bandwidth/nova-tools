// Package sprintline is the sprint table's x/y z% -> ~eta line, and nothing else.
//
// x, y and the percent are nova-work's, from `set check --evaluate`:
//
//	SET OK units=<y> ...
//	SET DONE done=<x> percent=<p>
//
// The percent is printed as that tool printed it. It is not recomputed, and a
// work-set's :status "done" or :status "landed" is not an input. Those marks are
// the hand-counted receipts the old sprint-xy bash counted; they do not move
// this line.
//
// eta is the sprint verb's wall after each still-open task's estimate is
// replaced by its kind's SUGGEST minutes from `nova-pulse sprint calibration`
// (the mean lease-to-done actual). A kind with no measurement keeps the task's
// own estimate. The hand formula (open*10/3+12) is not used.
package sprintline

import (
	"fmt"
	"strconv"
	"strings"
)

// Counts is x/y and the tool's percent.
type Counts struct {
	Done    int
	Units   int
	Percent int
}

// ParseEvaluate reads nova-work set check stdout. SET OK supplies units (y).
// SET DONE supplies done (x) and percent. Anything else in the stdout, including
// a copy of the work-set, is not a count.
func ParseEvaluate(stdout string) (Counts, error) {
	var c Counts
	var sawUnits, sawDone, sawPercent bool
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "SET OK" || strings.HasPrefix(line, "SET OK "):
			n, ok, err := fieldInt(line, "units")
			if err != nil {
				return Counts{}, err
			}
			if !ok {
				return Counts{}, fmt.Errorf("SET OK wants units=; refusing to guess y")
			}
			if err := one(sawUnits, c.Units, n, "units"); err != nil {
				return Counts{}, err
			}
			c.Units, sawUnits = n, true
		case line == "SET DONE" || strings.HasPrefix(line, "SET DONE "):
			done, ok, err := fieldInt(line, "done")
			if err != nil {
				return Counts{}, err
			}
			if !ok {
				return Counts{}, fmt.Errorf("SET DONE wants done=; refusing to count :status")
			}
			pct, pok, err := fieldInt(line, "percent")
			if err != nil {
				return Counts{}, err
			}
			if !pok {
				return Counts{}, fmt.Errorf("SET DONE wants percent=; the percent is the tool's, refusing to recompute")
			}
			if err := one(sawDone, c.Done, done, "done"); err != nil {
				return Counts{}, err
			}
			if err := one(sawPercent, c.Percent, pct, "percent"); err != nil {
				return Counts{}, err
			}
			c.Done, sawDone = done, true
			c.Percent, sawPercent = pct, true
		}
	}
	if !sawUnits {
		return Counts{}, fmt.Errorf("set check did not print SET OK units=; refusing to guess y")
	}
	if !sawDone || !sawPercent {
		return Counts{}, fmt.Errorf("set check did not print SET DONE; refusing to count :status")
	}
	return c, nil
}

func one(saw bool, prev, next int, name string) error {
	if saw && prev != next {
		return fmt.Errorf("%s is given twice and they disagree (%d and %d)", name, prev, next)
	}
	return nil
}

func fieldInt(line, key string) (int, bool, error) {
	for _, tok := range strings.Fields(line) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || k != key {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, true, fmt.Errorf("%s=%s is not a non-negative integer", key, v)
		}
		return n, true, nil
	}
	return 0, false, nil
}

// ParseSuggest reads `nova-pulse sprint calibration` stdout and returns the
// minute count each kind's measurement argues for. KIND and OWNER lines are
// the report around those counts. A report with no SUGGEST line has no
// measurement; the caller keeps each task's own estimate.
func ParseSuggest(stdout string) (map[string]int, error) {
	if strings.TrimSpace(stdout) == "" {
		return nil, fmt.Errorf("calibration printed nothing")
	}
	suggest := map[string]int{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "KIND ") || strings.HasPrefix(line, "OWNER ") {
			continue
		}
		kind, minutes, ok, err := suggestOf(line)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("calibration line %q is not KIND, OWNER, or SUGGEST", line)
		}
		if _, dup := suggest[kind]; dup {
			return nil, fmt.Errorf("SUGGEST %s is given twice", kind)
		}
		suggest[kind] = minutes
	}
	return suggest, nil
}

// suggestOf parses `SUGGEST fix 90m (default 120m)`, the line calibration prints.
func suggestOf(line string) (kind string, minutes int, ok bool, err error) {
	if !strings.HasPrefix(line, "SUGGEST ") {
		return "", 0, false, nil
	}
	rest := strings.TrimPrefix(line, "SUGGEST ")
	kind, rest, found := strings.Cut(rest, " ")
	if !found || kind == "" {
		return "", 0, true, fmt.Errorf("SUGGEST wants a kind and its minutes: %q", line)
	}
	mins, rest, found := strings.Cut(rest, " ")
	if !strings.HasSuffix(mins, "m") || !found {
		return "", 0, true, fmt.Errorf("SUGGEST %s wants <n>m, the lease-to-done actual: %q", kind, line)
	}
	n, conv := strconv.Atoi(strings.TrimSuffix(mins, "m"))
	if conv != nil || n < 0 {
		return "", 0, true, fmt.Errorf("SUGGEST %s minutes %q are not a non-negative integer", kind, mins)
	}
	if !strings.HasPrefix(rest, "(default ") || !strings.HasSuffix(rest, "m)") {
		return "", 0, true, fmt.Errorf("SUGGEST %s wants `(default <n>m)` beside the measurement: %q", kind, line)
	}
	return kind, n, true, nil
}

// Minutes renders a whole number of minutes the way the sprint status line does:
// `~45m`, `~1.5h`, `~3h`.
func Minutes(m int) string {
	if m <= 0 {
		return "~0m"
	}
	if m < 60 {
		return fmt.Sprintf("~%dm", m)
	}
	h := float64(m) / 60
	s := strconv.FormatFloat(h, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return "~" + s + "h"
}

// Format is `26/42 61% -> ~3h`. A wall of zero minutes prints the fraction
// alone: an estimate nobody measured is not printed as a made-up hour.
func Format(c Counts, etaMinutes int) string {
	head := fmt.Sprintf("%d/%d %d%%", c.Done, c.Units, c.Percent)
	if etaMinutes <= 0 {
		return head
	}
	return head + " -> " + Minutes(etaMinutes)
}

// Compose is the line. evaluate is nova-work's stdout, calibration is the
// sprint verb's, tasks are the still-open sprint tasks. The work-set's text is
// not a parameter: a hand-marked receipt has nowhere to enter.
func Compose(evaluate, calibration string, tasks []Task) (string, error) {
	c, err := ParseEvaluate(evaluate)
	if err != nil {
		return "", err
	}
	suggest, err := ParseSuggest(calibration)
	if err != nil {
		return "", err
	}
	eta, err := WallMinutes(tasks, suggest)
	if err != nil {
		return "", err
	}
	return Format(c, eta), nil
}
