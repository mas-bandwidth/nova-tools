package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/deal"
)

// THE ROUTER. Every task in a sprint is routed, not assigned by conversation (Glenn,
// 2026-09-22: "you will decide on a per-card basis, should this card run on friends, should
// this run on a swarm. it should be mechanical. unifying all work"). The rule table is:
//
//   - a live or security path            -> a friend, always, whatever it costs
//   - one file and a test a model can make red -> a bench consumer, cheap routes allowed
//   - across packages, or judgement      -> a bench consumer on the dearer routes, AND a
//     friend as the required reader
//   - a read                             -> the friend who owns that package, then by age
//   - a decision or a ruling             -> a friend; nothing else decides
//
// SPLIT-FIRST comes before all of it: a task whose paths span more than one package, or
// whose estimate is over ninety minutes, is returned with its seams named by file and is not
// routed until it is split or the seams are refused (a split is real only when the halves'
// files are disjoint; otherwise it is one task with two names and a rebase).
//
// And the rule that is not about the work at all: NEVER ROUTE TO A FRIEND WHOSE PRESENCE IS
// NOT UP. Presence is measured from the heartbeat key, never assumed; the router simply does
// not see an away consumer, which is why the table it is handed carries Present.

// LivePathPrefixes are the paths where a mistake reaches something running. A task touching
// one goes to a friend. The list is data a caller may replace; it is deliberately short,
// because a list that names everything routes everything to a friend and the swarm stops.
var LivePathPrefixes = []string{
	"internal/secrets/", // secrets
	"cmd/nova-secrets/",
	"internal/safepath/", // the path guard behind every delete
	"infra/",             // what converges the benches, live, on every machine
	"fleet/",             // the bench definition and the store's ACL
	"tools/deploy/",
}

// A source file is NOT a live path just because the thing it builds matters. The lander's
// own code (internal/merge) is protected by the reader rule -- a friend's typed line at head
// before it lands -- not by keeping the typing off the swarm; that is what let the day's
// #2550 and #2548 be handed to workers at all. Live means live: what is already running on a
// machine when it is changed.

// The route prefixes. A route is `<kind>:<consumer>`: the consumer is the match's output and
// the kind says which sort of consumer it is, so a reader of the line knows whether a person
// or a machine holds it.
const (
	RouteFriend = "friend:"
	RouteBench  = "bench:"
)

// Decision is one routing answer, with its reason. A route with no reason is a guess and
// this type cannot hold one.
type Decision struct {
	TaskID string
	// Split is true when the task must be split before it is routed; Seams are the
	// proposed children, one per package, by file.
	Split bool
	Seams []Seam
	// Consumer, Route and Reason are the answer when Split is false.
	Consumer string
	Route    string
	Reason   string
	// Reader is the friend who must read the result at head. It is set whenever the
	// typing moved off its owner, so ownership of the VERDICT never moves.
	Reader string
	// Routes is the tier the launcher reads: the model routes this task may run on. It is
	// a FIELD, never a queue (there is no evidence that a tier is the meaningful split of
	// the fleet; nova-tools #2564).
	Routes []string
}

// Line is the decision as one line of output.
func (d Decision) Line() string {
	if d.Split {
		var seams []string
		for _, s := range d.Seams {
			seams = append(seams, s.Package+"="+strings.Join(s.Paths, ","))
		}
		return fmt.Sprintf("SPLIT-FIRST %s seams: %s", d.TaskID, strings.Join(seams, " "))
	}
	line := fmt.Sprintf("ROUTE %s -> %s (%s)", d.TaskID, d.Route, d.Reason)
	if d.Reader != "" {
		line += " reader=" + d.Reader
	}
	if len(d.Routes) > 0 {
		line += " tier=" + strings.Join(d.Routes, ",")
	}
	return line
}

// Seam is one proposed child of a split: a package and the files that sit in it.
type Seam struct {
	Package string
	Paths   []string
}

// Requirements is the task as the dealer sees it: what it needs, with nothing about who
// happens to hold it today except the ownership the ordering respects.
func Requirements(t Task, now time.Time) deal.Requirements {
	req := deal.Requirements{
		Leg:            t.Leg,
		Locality:       t.Locality,
		Repo:           t.Repo,
		Base:           t.Base,
		WallMinutes:    t.EstMinutes,
		Isolation:      t.Isolation,
		Kind:           t.Kind,
		CostCeilingUSD: t.CostCeilingUSD,
		Routes:         t.Routes,
		Owner:          t.Owner,
		Age:            Age(t, now),
		Priority:       t.Priority,
	}
	if req.Locality == "" {
		req.Locality = deal.LocalityAny
	}
	if friend, _ := NeedsFriend(t); friend {
		req.ConsumerKind = deal.KindFriend
	}
	return req
}

// NeedsFriend reports whether the rule table sends this task to a person, and why.
func NeedsFriend(t Task) (bool, string) {
	for _, p := range t.Paths {
		p = strings.TrimSpace(strings.Trim(p, "/"))
		for _, live := range LivePathPrefixes {
			if strings.HasPrefix(p+"/", live) || strings.HasPrefix(p, live) {
				return true, "live or security path " + p
			}
		}
	}
	switch t.Kind {
	case KindDecision, KindRuling, KindReview:
		return true, "a " + t.Kind + " is a person's"
	case KindRead:
		return true, "a read is a friend's typed line"
	}
	return false, ""
}

// SplitFirst reports whether the task must be split before routing, with the seams.
func SplitFirst(t Task) (bool, []Seam) {
	pkgs := t.Packages()
	if len(pkgs) < 2 {
		// One package: there is no seam by file to name, so the task stays one and runs
		// serial even when it is over the bound. Saying so is the point -- a split that
		// is not real is a rebase with two names.
		return false, nil
	}
	by := map[string][]string{}
	for _, p := range t.Paths {
		pkg := packageOf(p)
		by[pkg] = append(by[pkg], strings.TrimSpace(p))
	}
	var seams []Seam
	for _, pkg := range pkgs {
		paths := by[pkg]
		sort.Strings(paths)
		seams = append(seams, Seam{Package: pkg, Paths: paths})
	}
	return true, seams
}

// Route applies the rule table to one task against the consumers table, and returns the
// decision with its reason. load is the open minutes each consumer already holds, so the
// router places a handed-over task on the emptiest lane that can take it rather than on the
// first name in the alphabet. It writes nothing: the caller records the answer on the task, so
// a dry run and a real run differ only in whether the store is written.
func Route(t Task, cs []deal.Capabilities, load map[string]int, now time.Time) (Decision, error) {
	if split, seams := SplitFirst(t); split {
		return Decision{TaskID: t.ID, Split: true, Seams: seams}, nil
	}
	req := Requirements(t, now)
	friendOnly, why := NeedsFriend(t)

	table := cs
	if friendOnly {
		table = onlyKind(cs, deal.KindFriend)
		if len(table) == 0 {
			return Decision{}, fmt.Errorf("task %s wants a friend (%s) and no friend is present; it waits rather than going to a bench", t.ID, why)
		}
	}
	c, reason, err := deal.Match(req, table, load)
	if err != nil {
		return Decision{}, fmt.Errorf("task %s: %w", t.ID, err)
	}
	d := Decision{TaskID: t.ID, Consumer: c.Name, Reason: reason}
	if friendOnly {
		d.Reason = why
	}
	if c.Kind == deal.KindFriend {
		d.Route = RouteFriend + c.Name
	} else {
		d.Route = RouteBench + c.Name
	}
	d.Routes = t.Routes
	// The judgement half of the rule table: a task that is not one file, or that a person
	// must stand behind, keeps a friend as the required reader at head even when a bench
	// does the typing.
	if c.Kind == deal.KindBench && (len(t.Paths) > 1 || len(t.Packages()) > 1 || t.Kind == KindRepair) {
		d.Reader = readerFor(t)
	}
	if t.Reader != "" {
		d.Reader = t.Reader
	}
	return d, nil
}

func readerFor(t Task) string {
	if t.Owner != "" {
		return strings.ToLower(t.Owner)
	}
	return ""
}

func onlyKind(cs []deal.Capabilities, kind string) []deal.Capabilities {
	var out []deal.Capabilities
	for _, c := range cs {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// Split turns one task into its per-file children, with the original owner as the required
// reader on every child. It refuses a split whose children share a file: that is one task
// with two names and a rebase, and it stays serial.
func Split(t Task, seams []Seam) ([]Task, error) {
	if len(seams) < 2 {
		return nil, fmt.Errorf("task %s has no seam to split on: its paths sit in one package", t.ID)
	}
	seen := map[string]string{}
	for _, s := range seams {
		for _, p := range s.Paths {
			p = strings.TrimSpace(p)
			if other, dup := seen[p]; dup {
				return nil, fmt.Errorf("task %s cannot be split: %s is in both %s and %s; the halves must be disjoint or the task stays one", t.ID, p, other, s.Package)
			}
			seen[p] = s.Package
		}
	}
	reader := readerFor(t)
	share := t.EstMinutes / len(seams)
	var out []Task
	for i, s := range seams {
		child := t
		child.ID = fmt.Sprintf("%s-%d", t.ID, i+1)
		child.Paths = append([]string(nil), s.Paths...)
		child.DependsOn = append([]string(nil), t.DependsOn...)
		child.Routes = append([]string(nil), t.Routes...)
		child.Owner = ""
		child.Route = ""
		child.RouteReason = ""
		child.Reader = reader
		child.State = StateOpen
		child.EstMinutes = share
		if i == 0 {
			child.EstMinutes = t.EstMinutes - share*(len(seams)-1)
		}
		child.Ref = t.Ref
		out = append(out, child)
	}
	return out, nil
}
