package friend

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Pacing a subscription friend by measurement (docs/SPEC-FRIEND.md, subscription pacing).
// The owner, 2026-10-05 ~10:45 PM: "Please try to go easy on <friend>
// (<machine>) and this session until 11PM, or you will run out of credits."
// The coordinator cut a width by hand, paused a reader and parked jobs
// until eleven. A subscription has a 5-hour and a 7-day window, and a Claude Code headless
// run reports its use of each in a rate_limit_event; pacing is a setting on
// the row (the fraction of each window the sprint may spend, DefaultPacing
// when the row names none), and the daemon reads the harness's report after
// every lane turn, lowers the lanes' effective width as a window fills,
// raises it as the window resets, starts nothing at the setting (so no run
// meets the hard limit), and tells the coordinator once, as a judgment, when
// the friend is paced below half her row.

// DefaultPacing is the fraction of each subscription window the sprint may
// spend when the row names no pacing.
const DefaultPacing = 0.80

// The windows a Claude subscription reports, in its own spelling.
const (
	WindowFiveHour = "five_hour"
	WindowSevenDay = "seven_day"
)

// WindowUse is one subscription window as the harness last reported it:
// its name, the fraction used (0 when the report named none: under the
// harness's warning threshold), the harness's status (allowed,
// allowed_warning, rejected), when it resets (zero: not said) and when it
// was read.
type WindowUse struct {
	Window string    `json:"window"`
	Used   float64   `json:"used"`
	Status string    `json:"status,omitempty"`
	Resets time.Time `json:"resets,omitzero"`
	At     time.Time `json:"at,omitzero"`
}

// ReadRateLimitEvents is the windows a headless run's stream-json output
// reported in its rate_limit_event lines (rateLimitEvent, limit.go: the
// unifiedWindows of each window, or the older shape's one window), the
// last word of each window, in the order the windows were first named, each
// read at at. A rejected event marks rejected the windows it spent (at 1),
// or every window it names when none is at 1. A line that is not such an
// event is skipped.
func ReadRateLimitEvents(out string, at time.Time) []WindowUse {
	var uses []WindowUse
	put := func(w WindowUse) {
		if i := slices.IndexFunc(uses, func(u WindowUse) bool { return u.Window == w.Window }); i >= 0 {
			uses[i] = w
			return
		}
		uses = append(uses, w)
	}
	reset := func(epoch int64) time.Time {
		if epoch > 0 {
			return time.Unix(epoch, 0).UTC()
		}
		return time.Time{}
	}
	for _, line := range strings.Split(out, "\n") {
		var ev rateLimitEvent
		if !strings.Contains(line, `"rate_limit_event"`) || json.Unmarshal([]byte(strings.TrimSpace(line)), &ev) != nil || ev.Type != "rate_limit_event" {
			continue
		}
		in := ev.Info
		if len(in.Windows) == 0 {
			if in.RateLimitType == "" {
				continue
			}
			w := WindowUse{Window: in.RateLimitType, Status: in.Status, Resets: reset(in.ResetsAt), At: at}
			if in.Utilization != nil {
				w.Used = *in.Utilization
			}
			put(w)
			continue
		}
		names := slices.Sorted(maps.Keys(in.Windows))
		spent := slices.ContainsFunc(names, func(n string) bool { return in.Windows[n].Utilization >= 1 })
		for _, n := range names {
			w := WindowUse{Window: n, Used: in.Windows[n].Utilization, Status: in.Status, Resets: reset(in.Windows[n].ResetsAt), At: at}
			if in.Status == "rejected" && spent && w.Used < 1 {
				w.Status = "allowed"
			}
			put(w)
		}
	}
	return uses
}

// windowSpan is how long a window lasts, for a report that names no reset.
func windowSpan(window string) time.Duration {
	if strings.HasPrefix(window, WindowSevenDay) {
		return 7 * 24 * time.Hour
	}
	return 5 * time.Hour
}

// live says the window's report still holds at now: before its reset, or,
// with none said, within one span of the report.
func (w WindowUse) live(now time.Time) bool {
	if !w.Resets.IsZero() {
		return now.Before(w.Resets)
	}
	return now.Before(w.At.Add(windowSpan(w.Window)))
}

// short is a window's name as the table says it: 5h, 7d, else its own.
func (w WindowUse) short() string {
	switch w.Window {
	case WindowFiveHour:
		return "5h"
	case WindowSevenDay:
		return "7d"
	}
	return w.Window
}

// Pacer is a friend's lanes paced by her subscription windows. Its zero
// value knows no window and paces nothing.
type Pacer struct {
	windows []WindowUse // the last report of each window
	paced   int         // the effective width last said; 0 before the first step
	stepped bool
	judged  bool // the coordinator was told she is paced below half; told again only after she rose to half
}

// Observe takes a turn's report: each window replaces its last report. A
// report with no utilization (under the harness's warning threshold) keeps
// the use last read for the same reset, since use does not fall within a
// window.
func (p *Pacer) Observe(uses []WindowUse) {
	for _, u := range uses {
		i := slices.IndexFunc(p.windows, func(w WindowUse) bool { return w.Window == u.Window })
		if i < 0 {
			p.windows = append(p.windows, u)
			continue
		}
		if u.Used == 0 && u.Status != "rejected" && p.windows[i].Resets.Equal(u.Resets) && p.windows[i].live(u.At) {
			u.Used = p.windows[i].Used
		}
		p.windows[i] = u
	}
}

// Width is the lanes' effective width at now for the row's width and the
// pacing fraction, and the window that decided it ("" when none did). A
// window at or past the pacing, or that the harness rejected, allows none
// until it resets; below it, the width is the row's scaled by the share of
// the paced budget left, rounded up: the lanes thin as the window fills.
func (p *Pacer) Width(width int, pacing float64, now time.Time) (int, string) {
	n, why := width, ""
	for _, w := range p.windows {
		if !w.live(now) {
			continue
		}
		m := 0
		if w.Status != "rejected" && w.Used < pacing {
			m = int(math.Ceil(float64(width)*(pacing-w.Used)/pacing - 1e-9))
		}
		if m < n {
			n, why = m, w.Window
		}
	}
	return n, why
}

// held is why one lane is held at now under pacing, empty when it is not:
// the window that allows none, its use against the pacing, and its reset.
func (p *Pacer) held(now time.Time, pacing float64) string {
	n, why := p.Width(1, pacing, now)
	if n > 0 || why == "" {
		return ""
	}
	w := p.windows[slices.IndexFunc(p.windows, func(w WindowUse) bool { return w.Window == why })]
	line := fmt.Sprintf("paced: the subscription window %s at %d%% of %s", w.Window, int(math.Round(w.Used*100)), PacingText(pacing))
	if w.Status == "rejected" {
		line += ", rejected by the harness"
	}
	if !w.Resets.IsZero() {
		line += "; held until it resets " + w.Resets.In(now.Location()).Format(time.RFC3339)
	}
	return line
}

// Use is the live windows' use as the table says it: "5h 62% 7d 31%",
// empty when none is live.
func (p *Pacer) Use(now time.Time) string {
	var out []string
	for _, w := range p.windows {
		if w.live(now) {
			out = append(out, fmt.Sprintf("%s %d%%", w.short(), int(math.Round(w.Used*100))))
		}
	}
	return strings.Join(out, " ")
}

// Step is the pacer at now: the effective width, the line that says a
// change of it (empty when none), and whether this step owes the
// coordinator the judgment (paced below half her row, said once until she
// is back at half or more).
func (p *Pacer) Step(now time.Time, width int, pacing float64) (paced int, line string, judge bool) {
	paced, why := p.Width(width, pacing, now)
	if p.stepped && paced != p.paced {
		switch {
		case why == "":
			line = fmt.Sprintf("pacing: width %d -> %d of %d, no window over the pacing (%s)", p.paced, paced, width, PacingText(pacing))
		default:
			w := p.windows[slices.IndexFunc(p.windows, func(w WindowUse) bool { return w.Window == why })]
			line = fmt.Sprintf("pacing: width %d -> %d of %d, %s at %d%% of %s", p.paced, paced, width, w.Window, int(math.Round(w.Used*100)), PacingText(pacing))
			if w.Status == "rejected" {
				line += ", rejected by the harness"
			}
			if !w.Resets.IsZero() {
				line += ", resets " + w.Resets.UTC().Format(time.RFC3339)
			}
		}
	}
	p.paced, p.stepped = paced, true
	switch {
	case 2*paced < width && !p.judged:
		p.judged, judge = true, true
	case 2*paced >= width:
		p.judged = false
	}
	return paced, line, judge
}

// PacingText is a pacing fraction as a percent: "80%".
func PacingText(pacing float64) string {
	return strconv.Itoa(int(math.Round(pacing*100))) + "%"
}

// PacingOf is the pacing a row's setting gives: the setting when it is a
// fraction in (0, 1], else DefaultPacing.
func PacingOf(setting float64) float64 {
	if setting > 0 && setting <= 1 {
		return setting
	}
	return DefaultPacing
}

// PacingJudgmentText is what the coordinator is told when a friend's lanes
// are paced below half her row's width: one judgment.
func PacingJudgmentText(friend string, paced, width int, pacing float64, use string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: paced to %d of %d lanes by the subscription windows (%s; pacing %s)", friend, paced, width, use, PacingText(pacing))
	body = fmt.Sprintf("The harness's rate_limit_event says the subscription windows of %s are at %s, against a pacing of %s. The daemon lowers the lanes as a window fills and raises them as it resets, by itself; at the pacing it starts nothing, so no run meets the hard limit. A judgment: keep the pacing, change it (the row's pacing setting), or move the cards to another route until the window resets.\n",
		friend, use, PacingText(pacing))
	return subject, body
}
