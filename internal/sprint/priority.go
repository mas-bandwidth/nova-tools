package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// A priority is a group's, never a card's (docs/SPEC-SPRINT.md, the deal: priority; the
// owner, 2026-10-06: "Priority should not be able to change work order within a stream,
// but it should be able to make sure that certain groups of cards get dealt first"). The
// group is a stream, or every stream of a release (stream set --release), and its level a
// field of each stream's control card (FieldLevel): a whole number, absent at 0. The deal
// takes the groups in descending level and, inside a level, the stream turns as before
// (byLevel over streamTurns); within a stream the order stays the stream's own (rank). A
// group with nothing eligible costs no turn: the next level is dealt. The model is
// tla/Deal.tla: LevelFirst, a card of a higher level is never passed over for a lower one
// while a worker eligible for it has room.

// FieldLevel is a stream's control card's field: its priority level, absent at 0.
const FieldLevel = "level"

// StreamLevel is the stream's priority level: its control card's, 0 when none.
func StreamLevel(s *Snapshot, stream string) int {
	if s.Merge == nil {
		return 0
	}
	return LevelOfField(s.StreamCtl(stream).F(FieldLevel))
}

// LevelOfField is a control card's level field as a level: 0 when absent or not a number.
func LevelOfField(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}

// PriorityView is the deal's groups as where and the dashboard show them: each stream at
// a level above 0, the deal's current group (the highest level with a ready primary) and
// its streams with one; nil when no stream has a level.
type PriorityView struct {
	Levels  map[string]int `json:"levels"`
	Group   int            `json:"group"`
	Streams []string       `json:"streams"`
}

// PriorityOf is the view from the stream clocks and each stream's ready count.
func PriorityOf(clocks []StreamClock, ready map[string]int64) *PriorityView {
	v := &PriorityView{Levels: map[string]int{}, Group: -1}
	for _, c := range clocks {
		if c.Level > 0 {
			v.Levels[c.Stream] = c.Level
		}
		switch {
		case ready[c.Stream] == 0 || c.Level < v.Group:
		case c.Level > v.Group:
			v.Group, v.Streams = c.Level, []string{c.Stream}
		default:
			v.Streams = append(v.Streams, c.Stream)
		}
	}
	if len(v.Levels) == 0 {
		return nil
	}
	v.Group = max(v.Group, 0)
	slices.Sort(v.Streams)
	return v
}

// PriorityLine is where's line of it: "priority: dealing level <n> (<streams>); levels
// <stream> <n>, ..., the rest 0"; "" for nil.
func PriorityLine(v *PriorityView) string {
	if v == nil {
		return ""
	}
	var lv []string
	for _, st := range slices.Sorted(maps.Keys(v.Levels)) {
		lv = append(lv, fmt.Sprintf("%s %d", st, v.Levels[st]))
	}
	group := "nothing ready"
	if len(v.Streams) > 0 {
		group = strings.Join(v.Streams, ",")
	}
	return fmt.Sprintf("priority: dealing level %d (%s); levels %s, the rest 0", v.Group, group, strings.Join(lv, ", "))
}

// PriorityReq is one priority set: the streams named, or every stream of the release, to
// the level.
type PriorityReq struct {
	Streams []string `json:",omitempty"`
	Release string   `json:",omitempty"`
	Level   int
	Who     string
}

// PriorityStreams is the streams a priority set names: Streams, or every stream whose
// release is Release, in name order; with every problem refused at once.
func PriorityStreams(s *Snapshot, r PriorityReq) ([]string, []string) {
	var why []string
	switch {
	case len(r.Streams) == 0 && r.Release == "":
		why = append(why, "names no group: name streams (nova-sprint priority set <stream>... --level <n>) or a release (--release <name> --level <n>)")
	case len(r.Streams) > 0 && r.Release != "":
		why = append(why, "names streams and a release: a priority set names one group; name the streams, or the release alone")
	}
	if r.Level < 0 {
		why = append(why, fmt.Sprintf("level %d is below 0: a level is a whole number, 0 the default", r.Level))
	}
	var out []string
	for _, st := range r.Streams {
		if s.Merge == nil || s.StreamCtl(st) == nil {
			why = append(why, fmt.Sprintf("no stream %s (streams: %s)", st, strings.Join(s.Streams(), ",")))
			continue
		}
		if !slices.Contains(out, st) {
			out = append(out, st)
		}
	}
	if r.Release != "" && len(r.Streams) == 0 {
		for _, st := range s.Streams() {
			if s.StreamCtl(st).F(FieldRelease) == r.Release {
				out = append(out, st)
			}
		}
		if len(out) == 0 {
			why = append(why, "no stream is of release "+r.Release+": nova-sprint stream set <s> --release "+r.Release+" names one")
		}
	}
	slices.Sort(out)
	return out, why
}

// PrioritySet writes the level on each stream of the group (FieldLevel), level 0 taking
// it off; all or none, every problem named at once.
func PrioritySet(s *Snapshot, r PriorityReq) Plan {
	var p Plan
	streams, why := PriorityStreams(s, r)
	if len(why) > 0 {
		p.refuse("priority", strings.Join(why, "; "))
		return p
	}
	for _, st := range streams {
		ctl := s.StreamCtl(st)
		set, unset := map[string]string{FieldLevel: itoa(r.Level)}, []string(nil)
		if r.Level == 0 {
			set, unset = nil, []string{FieldLevel}
		}
		p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: st, Changes: []Change{change(Merge, setEntry(ctl, set, unset...))},
			Moved: fmt.Sprintf("stream %s level %d (was %d)", st, r.Level, StreamLevel(s, st))})
	}
	return p
}

// byLevel is the cards with the higher level's first, stable: the cards of one level keep
// the order given (the stream turns, and within a stream its own order).
func byLevel(s *Snapshot, cards []*Card) []*Card {
	out := slices.Clone(cards)
	level := map[string]int{}
	for _, c := range out {
		if _, ok := level[c.Row]; !ok {
			level[c.Row] = StreamLevel(s, c.Row)
		}
	}
	slices.SortStableFunc(out, func(a, b *Card) int { return level[b.Row] - level[a.Row] })
	return out
}

// dealOrder is the order the deal offers ready primaries in: the stream turns from the
// deal's stream index (streamTurns), the higher level's first (byLevel).
func dealOrder(s *Snapshot, cards []*Card) []*Card {
	return byLevel(s, streamTurns(cards, streamRound(s, PropStreamIndex)))
}

// WhyLine is one line of why: what it is about (group, order, hold, who, tier, friends,
// machines, bound) and what holds or frees the card there.
type WhyLine struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// WhyNotDealt is, for a ready primary, what keeps it from dealing this tick, one line each
// (docs/SPEC-SPRINT.md, the deal: priority, and nova-sprint why): its group's level and the
// groups ahead of it with a ready card, its place in the deal's order, its stream's hold,
// its WHO line, its tier and route, the friends up and their room, the machines up and
// theirs, and a bound that keeps it back. A card not ready gets the one line that says so.
// It reads the snapshot and the friends' seats and writes nothing.
func WhyNotDealt(s *Snapshot, id string, seats []FriendSeat) ([]WhyLine, string) {
	c := s.Work.Placed(id)
	switch {
	case c == nil:
		return nil, "no card " + id + " on the work table: nova-sprint card " + id + " shows its record"
	case IsSentinel(c):
		return []WhyLine{{"state", "a sentinel: never dealt; release lets what waits behind it go"}}, ""
	case c.Col != Ready:
		return []WhyLine{{"state", "it is " + c.Col + ", not ready: the deal deals ready primaries only"}}, ""
	}
	var out []WhyLine
	add := func(k, f string, a ...any) { out = append(out, WhyLine{k, fmt.Sprintf(f, a...)}) }
	var ready []*Card
	for _, x := range s.Work.Column(Ready) {
		if !IsSentinel(x) && !StreamHeld(s, x.Row) {
			ready = append(ready, x)
		}
	}
	level := StreamLevel(s, c.Row)
	aheadBy := map[string]int{}
	for _, x := range ready {
		if StreamLevel(s, x.Row) > level {
			aheadBy[x.Row]++
		}
	}
	if len(aheadBy) == 0 {
		add("group", "level %d (stream %s): no group of a higher level has a ready card", level, c.Row)
	} else {
		var parts []string
		for _, st := range slices.Sorted(maps.Keys(aheadBy)) {
			parts = append(parts, fmt.Sprintf("%s level %d (%d ready)", st, StreamLevel(s, st), aheadBy[st]))
		}
		add("group", "level %d (stream %s): the groups ahead of it are dealt first: %s", level, c.Row, strings.Join(parts, ", "))
	}
	if i := slices.Index(dealOrder(s, ready), c); i >= 0 {
		add("order", "%d ready ahead of it in the deal's order (by level, then stream turns, then its stream's own order)", i)
	} else {
		add("order", "not in the deal's order: its stream is held")
	}
	if StreamHeld(s, c.Row) {
		add("hold", "its stream %s is held (%s): dealt nowhere until nova-sprint unhold %s", c.Row, orDash(s.StreamCtl(c.Row).F(FieldHeldReason)), c.Row)
	} else {
		add("hold", "its stream %s is not held", c.Row)
	}
	name, named := FriendCard(c)
	switch {
	case OnlyFriend(c):
		add("who", "WHO: only friend %s: it waits for that friend, never a machine or another friend", name)
	case named && name != "":
		add("who", "WHO: friend %s: offered to that friend first, then any friend, then the machines", name)
	case named:
		add("who", "WHO: friend: offered to any friend first, then the machines")
	default:
		add("who", "no WHO line: offered to the friends first, then the machines")
	}
	tier := cardTierOf(escalating(s, c))
	if _, why := s.noRoute(escalating(s, c)); why != "" {
		add("tier", "%s: no route serves it: %s", tier, why)
	} else {
		add("tier", "%s", tier)
	}
	var fl []string
	left := friendsLeft(s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt"))))
	for _, f := range seats {
		room, _ := friendRoom(f)
		free := room - friendLoad(s, f.Name)
		why := "eligible"
		switch {
		case f.Status != Up:
			why = f.Status
		case !friendTakes(f, tier):
			why = "tier not taken"
		case slices.Contains(left, f.Name):
			why = "it has left this friend"
		case free <= 0:
			why = "full"
		}
		fl = append(fl, fmt.Sprintf("%s %s, room %d of %d", f.Name, why, max(free, 0), room))
	}
	if len(fl) == 0 {
		fl = []string{"none on the roster"}
	}
	add("friends", "%s", strings.Join(fl, "; "))
	up := s.UpMembers()
	members := onlyBench(up, Bench(c))
	total := 0
	for _, m := range members {
		total += DealAhead * s.Width(m)
	}
	switch {
	case len(Bench(c)) > 0 && len(members) == 0:
		add("machines", "its bench %s has no member up: it waits ready for one", strings.Join(Bench(c), ","))
	case len(up) == 0:
		add("machines", "no fleet member is up")
	default:
		add("machines", "%d up, room %d free of %d", len(members), widthRoom(s, members), total)
	}
	switch wc, _ := AtStagingBound(s, c, up); {
	case AtRedealBound(s, c) != nil:
		add("bound", "its attempt is at its redeal bound: the coordinator's judgment, not the deal, moves it")
	case wc != nil:
		add("bound", "every member up refused it at staging: the coordinator's judgment, not the deal, moves it")
	default:
		add("bound", "none")
	}
	return out, ""
}
