package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Reminders (the tick's one more duty): the people who work on a sprint each
// have a goal, a text of what to keep doing, and a route to reach them. While
// the machine is RUNNING the tick pushes each person's goal down its route
// once every RemindEvery of running time, and once at once when the machine
// starts. The logic here is pure; the store binding keeps the record and does
// the delivery.

// RemindEvery is the running time between two pushes to one person.
const RemindEvery = 5 * time.Minute

// NRemindFailed is the judgment written once for a person whose reminder
// cannot be delivered.
const NRemindFailed = "a reminder could not be delivered"

// Route kinds.
const (
	RouteFile = "file"
	RouteBus  = "bus"
)

// Goal is one person's goal and where they stand: the text, the route, the
// last push and how many there have been, the last attempt and the error of
// the delivery that is failing now.
type Goal struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Route string `json:"route"`
	// Last is the last push that arrived, Count how many have.
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
	// Tried is the last delivery that failed; Fail is its error while the
	// route fails, and "" once a delivery arrives.
	Tried time.Time `json:"tried"`
	Fail  string    `json:"fail,omitempty"`
	// Pending says the goal or its route was just set: the next tick pushes
	// it, whatever the last push was.
	Pending bool `json:"pending,omitempty"`
}

// Goals is the sprint's people, by name, and the judgments written for
// failing routes: person to the words of the judgment.
type Goals struct {
	People []Goal            `json:"people,omitempty"`
	Noted  map[string]string `json:"noted,omitempty"`
}

// Find is the person's index, -1 when there is none.
func (g Goals) Find(name string) int {
	for i, p := range g.People {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Sort keeps the people in name order.
func (g *Goals) Sort() {
	sort.Slice(g.People, func(i, j int) bool { return g.People[i].Name < g.People[j].Name })
}

// ValidGoalName is nil for a name that is safe in a file name and a route:
// lower-case letters, digits, dot, dash and underscore, at most 64, starting
// with a letter or digit.
func ValidGoalName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("a name is 1 to 64 characters")
	}
	for i, r := range name {
		letter := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if !letter && (i == 0 || r != '.' && r != '-' && r != '_') {
			return fmt.Errorf("the name %q holds anything but lower-case letters, digits, dot, dash and underscore, and starts with a letter or digit", name)
		}
	}
	return nil
}

// ValidGoalText is nil for text a reminder can carry: not blank, valid UTF-8,
// no more than max bytes.
func ValidGoalText(text string, max int) error {
	switch {
	case strings.TrimSpace(text) == "":
		return fmt.Errorf("the goal text is empty")
	case !utf8.ValidString(text) || strings.ContainsRune(text, 0):
		return fmt.Errorf("the goal text is not text (invalid UTF-8 or a NUL byte)")
	case len(text) > max:
		return fmt.Errorf("the goal text is %d bytes; the bound is %d", len(text), max)
	}
	return nil
}

// ParseRoute splits a route into its kind and target: file:<absolute path>
// or bus:<bus directory>.
func ParseRoute(route string) (kind, target string, err error) {
	kind, target, ok := strings.Cut(route, ":")
	if !ok || strings.TrimSpace(target) == "" {
		return "", "", fmt.Errorf("the route %q is not file:<path> or bus:<bus-dir>", route)
	}
	if kind != RouteFile && kind != RouteBus {
		return "", "", fmt.Errorf("the route kind %q is not file or bus", kind)
	}
	return kind, target, nil
}

// Attempt is the last time a push was tried, whether or not it arrived.
func (g Goal) Attempt() time.Time {
	if g.Tried.After(g.Last) {
		return g.Tried
	}
	return g.Last
}

// Due says the person is to be pushed to now: just set; never pushed to; the last
// attempt was before this run of the machine began (since: the first tick
// after start pushes to everyone); or RemindEvery of running time has passed
// since it, time STOPPED not counting.
func (g Goal) Due(now, since time.Time, stopped func(from, to time.Time) time.Duration) bool {
	at := g.Attempt()
	if g.Pending || at.IsZero() || at.Before(since) {
		return true
	}
	d := now.Sub(at)
	if stopped != nil {
		d -= stopped(at, now)
	}
	return d >= RemindEvery
}

// FailureWhat is the words of the judgment for a person whose route fails.
func FailureWhat(g Goal) string {
	return fmt.Sprintf("the reminder to %s over %s failed: %s", g.Name, g.Route, g.Fail)
}

// Failing is the judgments the failing routes call for, person to words.
func (g Goals) Failing() map[string]string {
	out := map[string]string{}
	for _, p := range g.People {
		if p.Fail != "" {
			out[p.Name] = FailureWhat(p)
		}
	}
	return out
}

// RemindNotes is the plan that makes the open judgments the failing routes:
// one for each person whose route fails and has none open, each closed once
// its person is reached, or dropped. It writes nothing when they agree.
func RemindNotes(s *Snapshot, g Goals, who string) Plan {
	var conds []cond
	for _, p := range g.People {
		if p.Fail == "" {
			continue
		}
		conds = append(conds, cond{typ: NRemindFailed, streamLevel: true, what: FailureWhat(p),
			decisions: []string{"goal set " + p.Name + " --to <route>", "goal drop " + p.Name}})
	}
	var p Plan
	notify(&p, s, conds, []string{NRemindFailed}, who)
	return p
}
