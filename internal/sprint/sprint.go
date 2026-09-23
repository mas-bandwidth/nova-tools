// Package sprint is the bounded set of work and the four questions asked of it: how far
// along is it (x/y z%), how long until it is done (the WALL, not the sum), where does each
// task go (the router, over internal/deal), and how good were the estimates (calibration).
//
// A SPRINT is any current bounded set of tasks toward a goal -- friends, friends and swarm,
// or swarm only (Glenn, 2026-09-22): "we can widen the idea of a sprint to be any work that
// we are doing towards a goal. A current bounded set of tasks that we can go x/y z% on."
// Work run on the swarm is SWARM WORK, never "a sprint"; the per-bench table is the SWARM
// TABLE. As above so below: the same shape holds a week of sprints, a sprint, and a card's
// steps.
//
// WHAT THIS PACKAGE OWNS, and nothing else: the bounded set (open, add, close), the x/y z%
// line, the wall computation, the route rule and split-first, and the calibration. Queues,
// leases, widths, presence and the refill RULE belong to internal/deal, which a bench's
// dealer calls with benches and this package calls with friends -- one mechanism, because
// a friend is a consumer like a bench and not a second system (Glenn, 2026-09-22: "there is
// no reason for these things to be apart").
//
// THE STORE IS REDIS, core types only, ids and counts and nothing else: `sprint:<name>` a
// hash, `sprint:<name>:tasks` a set, `task:<id>` a hash. The history lives in the fold over
// ev:cards (#2587), never in the hot store. Every state flip comes from a PRIMARY RECORD --
// a PR merged, an issue closed, a typed line at head, a landed card event -- and never from
// a hand: that is the Records seam at the bottom of this file.
package sprint

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The three states of COWS (Glenn, 2026-09-22: "we have the COW method, closed, open,
// working -- we now add the final piece. S = sprint"). Working means leased with a live
// heartbeat; the S is the sprint that bounds them, and only inside one is C/(C+O+W) a
// fraction that means anything.
const (
	StateOpen    = "open"
	StateWorking = "working"
	StateClosed  = "closed"
)

// States is every state a task may hold, in COWS order.
var States = []string{StateClosed, StateOpen, StateWorking}

// The kinds a task may be. A card is a task whose route is a bench consumer; a friend's fix
// or read is a task whose route is a friend. One shape, two consumer kinds.
const (
	KindCard       = "card"
	KindRead       = "read"
	KindFix        = "fix"
	KindReview     = "review"
	KindRepair     = "repair"
	KindRuling     = "ruling"
	KindDecision   = "decision"
	KindIssue      = "issue"
	KindScript     = "script"
	KindEvaluation = "evaluation"
)

// Kinds is every kind, in banner order.
var Kinds = []string{KindCard, KindRead, KindFix, KindReview, KindRepair, KindRuling, KindDecision, KindIssue, KindScript, KindEvaluation}

// DefaultEstimate is the estimate a kind carries when the caller gives none, in minutes
// (Glenn, 2026-09-22: "read 10, flash card 30, pro card 90, friend fix 120, decision 30;
// overridable"). They are DEFAULTS, not truths: `sprint calibration` prints the error
// distribution so they are tuned from measurement, never from taste (ruling: do not guess,
// measure). A card's default is the cheaper rung's, because a card that needs the dearer one
// is a card whose route says so.
var DefaultEstimate = map[string]int{
	KindRead:       10,
	KindCard:       30,
	KindFix:        120,
	KindDecision:   30,
	KindReview:     30,
	KindRepair:     90,
	KindRuling:     30,
	KindIssue:      90,
	KindScript:     30,
	KindEvaluation: 120,
}

// SplitBound is the estimate above which a task is returned SPLIT-FIRST (Glenn: "Large tasks
// run serial. Split work can run parallel, possibly"), in minutes. Ninety is as much about
// observability as wall clock: ASLEEP is a fact on a thirty minute task and meaningless on a
// four hour one.
const SplitBound = 90

// Sprint is the bounded set itself. Nothing but ids, times and one goal sentence.
type Sprint struct {
	Name           string
	Goal           string
	OpenedAt       time.Time
	ClosedAt       time.Time
	PlannedCloseAt time.Time
}

// Active reports whether the sprint is still the bounded set of something.
func (s Sprint) Active() bool { return s.ClosedAt.IsZero() }

// Task is one unit of work in a sprint -- a card, a read, a fix, a decision -- in the ONE
// shape every consumer takes. The requirement fields (Paths, Leg, Locality, Isolation,
// Routes, CostCeilingUSD) are what the router matches against a consumer's capabilities;
// the rest is the record.
type Task struct {
	ID    string
	Kind  string
	Ref   string // nova-tools#2549 | card label@attempt | repo#pr@sha | a memory slug
	Owner string
	// Route is the consumer the router chose, as `friend:<name>` or `bench:<name>`, and
	// RouteReason is why. A route with no reason is a guess and is refused.
	Route       string
	RouteReason string
	State       string
	EstMinutes  int
	LeasedAt    time.Time
	DoneAt      time.Time
	// ActualMinutes is measured from LeasedAt to DoneAt at close; it is the calibration's
	// only input, and an estimate with no actual beside it is the TELL the ruling names.
	Actual   int
	Evidence string // the merge sha, the landed sha, the typed line id, the issue close
	// DependsOn are task ids this one truly waits for. TRUE dependencies only: a shared
	// path, or a symbol one task produces and another consumes. Two tasks with the same
	// owner and disjoint paths are PARALLEL, whatever name is on them.
	DependsOn []string
	Paths     []string
	// The rest of the requirement fields; empty means "no requirement".
	Leg            string
	Locality       string
	Isolation      string
	Routes         []string
	CostCeilingUSD float64
	Repo           string
	Base           string
	Reader         string // the required reader: split moves the typing, never the verdict
	Priority       bool
	CreatedAt      time.Time
}

// Open reports whether the task still counts against the sprint's remaining work.
func (t Task) Open() bool { return t.State != StateClosed }

// Package is the package a task's paths sit in: the first two path elements under internal/
// or cmd/, or the first element otherwise. It is what "spans more than one package" means.
func (t Task) Packages() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range t.Paths {
		pkg := packageOf(p)
		if pkg == "" || seen[pkg] {
			continue
		}
		seen[pkg] = true
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}

func packageOf(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "/"))
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		return parts[0]
	}
	if parts[0] == "internal" || parts[0] == "cmd" || parts[0] == "tools" {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// SharesPaths reports whether two tasks touch a file in common: the one thing (with a
// produced symbol) that makes a lane truly serial.
func SharesPaths(a, b Task) bool {
	for _, p := range a.Paths {
		for _, q := range b.Paths {
			if strings.TrimSpace(p) != "" && strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(q)) {
				return true
			}
		}
	}
	return false
}

// ValidateName holds a sprint or task name to what the store can carry. `: ` is refused
// because the one-line grammar uses it as the separator, and a name carrying one would make
// a status line unparseable by the thing that reads it.
func ValidateName(kind, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("the %s name is required; it wants a short id like fixes-2026-09-22; refusing to guess", kind)
	}
	if strings.Contains(name, ": ") {
		return fmt.Errorf("the %s name %q carries \": \", which is the one-line grammar's separator; name it without one", kind, name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("the %s name carries a control character", kind)
		}
	}
	if len(name) > 120 {
		return fmt.Errorf("the %s name is %d bytes; it wants an id, not a sentence", kind, len(name))
	}
	return nil
}

// ValidateKind refuses a kind the router has no rule for, by name.
func ValidateKind(kind string) error {
	for _, k := range Kinds {
		if k == kind {
			return nil
		}
	}
	return fmt.Errorf("kind %q is not one of %s", kind, strings.Join(Kinds, "|"))
}

// ValidateState refuses a state outside COWS.
func ValidateState(state string) error {
	for _, s := range States {
		if s == state {
			return nil
		}
	}
	return fmt.Errorf("state %q is not one of %s", state, strings.Join(States, "|"))
}

// Store is the sprint's half of the fleet Redis, and the seam every test runs against. It
// holds ids and counts: no diff, no prompt, no transcript (Johnny's division of labour,
// reports/redis-for-nova-tools-2026-09-21.md section 8, adopted).
type Store interface {
	PutSprint(ctx context.Context, s Sprint) error
	GetSprint(ctx context.Context, name string) (Sprint, error)
	Sprints(ctx context.Context) ([]Sprint, error)
	PutTask(ctx context.Context, t Task) error
	GetTask(ctx context.Context, id string) (Task, error)
	AddTask(ctx context.Context, sprintName, taskID string) error
	Tasks(ctx context.Context, sprintName string) ([]Task, error)
	// Presence is the measured heartbeat of every consumer: the `friend:<name>` keys
	// (#2612) and the `bench:<name>` rows, lowercased, live ones only.
	Presence(ctx context.Context) (map[string]bool, error)
	// QueueDepths is how deep each consumer's stream is right now, by consumer name.
	QueueDepths(ctx context.Context, consumers []string) (map[string]int, error)
	// Place appends one task to a consumer's stream and returns the entry id. It is the
	// same XADD a card takes: a friend's task is not a different kind of message.
	Place(ctx context.Context, queue string, t Task) (string, error)
	Close() error
}

// Minutes renders a whole number of minutes as the hours the status line carries: `~4h`,
// `~1.5h`, `~45m`. The arrow is the caller's.
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

// Percent is x/y as the whole percent the line carries. An empty sprint is 0%, never a
// division by zero and never 100%: nothing done out of nothing is not done.
func Percent(x, y int) int {
	if y <= 0 {
		return 0
	}
	return int(float64(x)/float64(y)*100 + 0.5)
}
