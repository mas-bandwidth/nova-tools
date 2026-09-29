package sprint

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The steps that move primaries through the work table and the fleet: add,
// resolve, start, take, finish, and the fleet's own moves. Each is a pure
// function of an observed snapshot and a request; it returns the plan (the
// manifests' entries, per card, and the notifications the moves cause) or a
// refusal per card. Nothing here reads a store or a clock.

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// AddReq admits primaries into a stream.
type AddReq struct {
	Stream string
	IDs    []string
	Count  int // generate this many ids, <stream>-<n>
	Needs  []string
	Brief  string
	Score  *float64 // the first primary's score; the rest follow it
	Only   []string
	Who    string
}

// AddIDs is the ids an add admits: the named ones, or Count generated ones
// numbered after the highest the stream has.
func AddIDs(s *Snapshot, r AddReq) []string {
	if r.Only != nil {
		return r.Only
	}
	if len(r.IDs) > 0 || r.Count <= 0 {
		return r.IDs
	}
	high := 0
	prefix := r.Stream + "-"
	for id := range s.Work.Cards {
		if n, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && strings.HasPrefix(id, prefix) && n > high {
			high = n
		}
	}
	out := make([]string, r.Count)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%d", r.Stream, high+1+i)
	}
	return out
}

// Add admits primaries: waiting if they need something not landed, else ready.
func Add(s *Snapshot, r AddReq) Plan {
	var p Plan
	if !ValidID(r.Stream) {
		for _, id := range AddIDs(s, r) {
			p.refuse(id, fmt.Sprintf("stream %q wants letters, digits, _ and -", r.Stream))
		}
		return p
	}
	for _, t := range []string{Work, Merge} {
		if !s.T(t).HasRow(r.Stream) {
			p.Rows = append(p.Rows, RowAdd{t, r.Stream})
		}
	}
	ctl := s.Merge.Card(CtlID(r.Stream))
	var head []Change
	switch {
	case ctl == nil:
		head = append(head, change(Merge, createEntry(CtlID(r.Stream), r.Stream, Ctl, 0,
			map[string]string{"kind": "stream", "state": StreamWaiting, "since": stamp(s.Now)})))
	case ctl.F("state") == StreamLanded:
		head = append(head, change(Merge, setEntry(ctl, map[string]string{"state": StreamWaiting, "since": stamp(s.Now)})))
	}
	score := 1.0
	for _, c := range s.Work.Cards {
		if c.Score >= score {
			score = c.Score + 1
		}
	}
	if r.Score != nil {
		score = *r.Score
	}
	needs := strings.Join(r.Needs, ",")
	seen := map[string]bool{}
	for _, id := range AddIDs(s, r) {
		switch {
		case seen[id]:
			p.refuse(id, "named twice")
			continue
		case !ValidID(id) || strings.HasPrefix(id, "ctl-"):
			p.refuse(id, "an id wants letters, digits, _ and -, and does not start with ctl-")
			continue
		case s.Work.Card(id) != nil:
			p.refuse(id, "exists already ("+placeWord(s.Work.Card(id))+")")
			continue
		}
		seen[id] = true
		col, why := Ready, ""
		for _, n := range r.Needs {
			if n == id {
				col, why = "", "needs itself"
			} else if s.StateOf(n) != Landed && col != "" {
				col = Waiting
			}
		}
		if col == "" {
			p.refuse(id, why)
			continue
		}
		fields := map[string]string{"kind": "primary", "stream": r.Stream, "attempt": "0", "admitted": stamp(s.Now)}
		if r.Brief != "" {
			fields["brief"] = r.Brief
		}
		if needs != "" {
			fields["needs"] = needs
		}
		u := Unit{Key: id, Stream: r.Stream, Changes: append(head, change(Work, createEntry(id, r.Stream, col, score, fields))),
			Moved: fmt.Sprintf("%s -> %s stream=%s score=%s", id, col, r.Stream, fmtScore(score))}
		head = nil
		p.Units = append(p.Units, u)
		score++
	}
	if head != nil && len(p.Units) == 0 && len(p.Refused) == 0 {
		p.Units = append(p.Units, Unit{Key: CtlID(r.Stream), Changes: head, Moved: "stream " + r.Stream + " open"})
	}
	return p
}

func placeWord(c *Card) string {
	if c.Placed() {
		return c.Row + ":" + c.Col
	}
	return "kept, " + orDash(c.F("outcome"))
}

func fmtScore(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// ResolveReq moves waiting primaries whose needs have landed.
type ResolveReq struct {
	Sel
	Who string
}

// ResolveExtras is the needs a resolve must read as records: the ones not on
// the table, which may have been dropped.
func ResolveExtras(s *Snapshot) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range s.Work.Column(Waiting) {
		for _, n := range Split(c.F("needs")) {
			if s.Work.Placed(n) == nil && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// Resolve moves waiting -> ready where every need has landed. A need that was
// dropped is a judgment for the coordinator, once.
func Resolve(s *Snapshot, r ResolveReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Waiting), rowOf, func(c *Card) string { return inState(c, Waiting) }, s.primaryCard)
	for _, c := range chosen {
		var waits, dropped []string
		for _, n := range Split(c.F("needs")) {
			switch {
			case s.StateOf(n) == Landed:
			case s.Work.Card(n) != nil && !s.Work.Card(n).Placed() && s.Work.Card(n).F("outcome") == "dropped":
				dropped = append(dropped, n)
			default:
				waits = append(waits, n)
			}
		}
		if len(dropped) > 0 {
			if !hasOpen(s.Open, NBlocked, c.ID) {
				n := judgment(NBlocked, c.Row, s.Now, 0, c.ID)
				n.What = c.ID + " needs " + strings.Join(dropped, ",") + ", dropped"
				n.Who = r.Who
				p.Notes = append(p.Notes, n)
			}
			if len(r.IDs) > 0 {
				p.refuse(c.ID, "needs "+strings.Join(dropped, ",")+", which was dropped")
			}
			continue
		}
		if len(waits) > 0 {
			if len(r.IDs) > 0 {
				p.refuse(c.ID, "waits for "+strings.Join(waits, ","))
			}
			continue
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, nil))},
			Moved: c.ID + " waiting -> ready"})
	}
	return p
}

// StartReq cuts and deals work cards for ready primaries.
type StartReq struct {
	Sel
	Who string
}

// Start moves ready -> working: for each primary, in work order, its work
// card is dealt to the up member with the shortest ready queue. A card
// withdrawn because no member was up is the same card dealt again at a new
// generation, its attempt unchanged; otherwise the next attempt's card is cut.
func Start(s *Snapshot, r StartReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Ready), rowOf, func(c *Card) string { return inState(c, Ready) }, s.primaryCard)
	up := s.UpMembers()
	if len(up) == 0 {
		for _, c := range chosen {
			p.refuse(c.ID, "no fleet member is up; run: nova-sprint fleet up <member>")
		}
		return p
	}
	q := readyQueues(s, up)
	for _, c := range chosen {
		if wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt"))); wc != nil && wc.Col == Withdrawn {
			p.Units = append(p.Units, redeal(s, c, wc, up, q))
			continue
		}
		u, why := deal(s, c, c.F("fix"), up, q, nil)
		if why != "" {
			p.refuse(c.ID, why)
			continue
		}
		p.Units = append(p.Units, u)
	}
	return p
}

// readyQueues is the up members' ready queue lengths.
func readyQueues(s *Snapshot, up []string) map[string]int {
	q := map[string]int{}
	for _, m := range up {
		q[m] = s.Fleet.Count(m, Ready)
	}
	return q
}

// deal cuts the primary's next attempt's work card, carrying the fix and the
// primary's score, into the ready queue of the up member with the shortest
// queue, at generation 1, and moves the primary to working with set.
func deal(s *Snapshot, c *Card, fix string, up []string, q map[string]int, set map[string]string, unset ...string) (Unit, string) {
	attempt := c.Int("attempt") + 1
	card := WorkCardID(c.ID, attempt)
	if s.Fleet.Card(card) != nil {
		return Unit{}, "work card " + card + " exists already"
	}
	m := shortest(up, q)
	q[m]++
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": m}
	if fix != "" {
		fields["fix"] = fix
	}
	if set == nil {
		set = map[string]string{}
	}
	set["attempt"], set["work"] = itoa(attempt), card
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, m, Ready, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, set, append(unset, "result")...)),
	}, Moved: fmt.Sprintf("%s %s -> working card=%s member=%s", c.ID, c.Col, card, m)}, ""
}

// redeal deals a withdrawn work card again, into the ready queue of the up
// member with the shortest queue, at a new generation bound to that member,
// and moves its primary to working on it. The attempt, the fix and the score
// are the card's own, unchanged.
func redeal(s *Snapshot, c, wc *Card, up []string, q map[string]int) Unit {
	m := shortest(up, q)
	q[m]++
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, moveEntry(wc, m, Ready, nextGen(wc, m), "withdrawn")),
		change(Work, moveEntry(c, c.Row, Working, map[string]string{"work": wc.ID}, "result")),
	}, Moved: fmt.Sprintf("%s %s -> working card=%s member=%s gen=%d (dealt again)", c.ID, c.Col, wc.ID, m, wc.Int("gen")+1)}
}

// shortest is the name with the smallest count, the first in order on a tie.
func shortest(names []string, q map[string]int) string {
	best := names[0]
	for _, n := range names[1:] {
		if q[n] < q[best] {
			best = n
		}
	}
	return best
}

// TakeReq is a worker taking its work cards. Gens names the generation the
// worker holds for a named card; a take by id names one for every card, and
// a card whose live generation differs has been dealt again, so the take is
// refused as stale. A take by selection takes the member's oldest ready cards
// and reports each one's generation.
type TakeReq struct {
	Sel
	As   string
	Gens map[string]int
	Who  string
}

// named says the selection names its cards by id.
func named(sel Sel) bool { return len(sel.IDs) > 0 || sel.Only != nil }

// liveGen is the refusal of a card whose generation the request does not
// name, or names and is not the card's live one.
func liveGen(verb string, c *Card, gens map[string]int) string {
	g, ok := gens[c.ID]
	switch {
	case !ok:
		return fmt.Sprintf("names no generation; the live one is %d: %s %s@%d", c.Int("gen"), verb, c.ID, c.Int("gen"))
	case g != c.Int("gen"):
		return fmt.Sprintf("stale: generation %d is not the live one (%d): the card was dealt again to %s", g, c.Int("gen"), orDash(c.Row))
	}
	return ""
}

// Take moves the member's work cards fleet ready -> working.
func Take(s *Snapshot, r TakeReq) Plan {
	var p Plan
	sel := r.Sel
	if !named(sel) && sel.Limit == 0 {
		sel.Limit = 1
	}
	if !s.Fleet.HasRow(r.As) {
		for _, id := range sel.IDs {
			p.refuse(id, "no fleet member "+r.As)
		}
		return p
	}
	if st := s.MemberCtl(r.As).F("status"); st != Up {
		for _, id := range sel.IDs {
			p.refuse(id, "member "+r.As+" is "+orDash(st))
		}
		return p
	}
	byID := named(sel)
	chosen := pick(&p, sel, s.Fleet.Cell(r.As, Ready), fieldStream, func(c *Card) string {
		if byID {
			if why := liveGen("take", c, r.Gens); why != "" {
				return why
			}
		}
		if !c.Placed() || c.Row != r.As || c.Col != Ready {
			return "not in " + r.As + " ready (it is " + placeWord(c) + ")"
		}
		return ""
	}, s.Fleet.Card)
	for _, c := range chosen {
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Working, map[string]string{"taken": stamp(s.Now)}))},
			Moved: fmt.Sprintf("%s ready -> working member=%s gen=%s", c.ID, r.As, c.F("gen"))})
	}
	return p
}

// FinishReq is a worker finishing work cards, ok or failed.
type FinishReq struct {
	Sel
	As     string
	Gens   map[string]int // the generation held, per named card
	Failed bool
	Head   string
	Report string
	Who    string
}

// Finish moves work cards working -> done and their primaries working ->
// review. Fixed work that comes back ok is asked of the same readers again.
// A finish always names the generation it holds for every card it finishes:
// a card without one is refused, naming the live generation, and a finish
// by selection without --as is refused outright.
func Finish(s *Snapshot, r FinishReq) Plan {
	var p Plan
	if !named(r.Sel) && r.As == "" {
		p.refuse("finish", "a finish by selection names its member: --as <member>; better, name each card: finish <card>@<gen>")
		return p
	}
	var all []*Card
	if r.As != "" {
		all = s.Fleet.Cell(r.As, Working)
	} else {
		all = s.Fleet.Column(Working)
	}
	byID := named(r.Sel)
	picked := pick(&p, r.Sel, all, fieldStream, func(c *Card) string {
		if byID {
			if why := liveGen("finish", c, r.Gens); why != "" {
				return why
			}
		}
		if !c.Placed() || c.Col != Working {
			return "not working (it is " + placeWord(c) + ")"
		}
		if r.As != "" && c.Row != r.As {
			return "dealt to " + c.Row + ", not " + r.As
		}
		if pr := s.Work.Placed(c.F("primary")); pr == nil || pr.Col != Working || pr.F("work") != c.ID {
			return "its primary " + c.F("primary") + " is not working on it"
		}
		return ""
	}, s.Fleet.Card)
	var chosen []*Card
	for _, c := range picked {
		if why := liveGen("finish", c, r.Gens); why != "" {
			p.refuse(c.ID, why)
			continue
		}
		chosen = append(chosen, c)
	}
	who := r.As
	if who == "" {
		who = r.Who
	}
	for _, c := range chosen {
		pr := s.Work.Placed(c.F("primary"))
		head := r.Head
		if head == "" {
			head = c.ID
		}
		result, okWord, counter := "ok", "yes", "ok"
		if r.Failed {
			result, okWord, counter = "failed", "no", "failed"
		}
		cardSet := map[string]string{"ok": okWord, "head": head, "finished": stamp(s.Now)}
		if r.Report != "" {
			cardSet["report"] = r.Report
		}
		set := map[string]string{"head": head, "result": result}
		if r.Failed {
			set["failed"] = itoa(pr.Int("failed") + 1)
		}
		u := Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{change(Fleet, moveEntry(c, c.Row, Done, cardSet))},
			Moved: fmt.Sprintf("%s working -> done %s; %s working -> review", c.ID, result, pr.ID)}
		if s.MemberCtl(c.Row) != nil {
			u.Bumps = append(u.Bumps, Bump{Fleet, CtlID(c.Row), counter, 1})
		}
		attempt := pr.Int("attempt")
		if !r.Failed {
			var again []string
			for _, reader := range Split(pr.F("asked")) {
				id := ReadCardID(pr.ID, attempt, reader)
				if !s.Readers.HasRow(reader) || s.Readers.Card(id) != nil {
					continue
				}
				u.Changes = append(u.Changes, change(Readers, createEntry(id, reader, Asked, pr.Score,
					map[string]string{"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": reader, "attempt": itoa(attempt), "head": head})))
				again = append(again, reader)
			}
			if len(again) > 0 {
				u.Moved += "; asked again of " + strings.Join(again, ", ")
			}
			n := happened(NWorkOK, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt = who, attempt
			u.Notes = append(u.Notes, n)
		} else {
			n := judgment(NWorkFailed, pr.Row, s.Now, pr.Int("failed"), pr.ID)
			n.Who, n.Attempt, n.What = who, attempt, r.Report
			u.Notes = append(u.Notes, n)
		}
		u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Review, set)))
		p.Units = append(p.Units, u)
	}
	return p
}

// FleetReq is a fleet verb: a member up or down, or the ready queues levelled.
type FleetReq struct {
	Op     string // up, down, level
	Member string
	Who    string
}

// Fleet brings a member up (and levels the ready queues), takes one down
// (dealing its unfinished work cards to up members, or withdrawing them when
// none is up), or levels the ready queues.
func FleetStep(s *Snapshot, r FleetReq) Plan {
	var p Plan
	switch r.Op {
	case "up":
		if !ValidID(r.Member) {
			p.refuse(r.Member, "a member name wants letters, digits, _ and -")
			return p
		}
		if !s.Fleet.HasRow(r.Member) {
			p.Rows = append(p.Rows, RowAdd{Fleet, r.Member})
		}
		ctl := s.Fleet.Card(CtlID(r.Member))
		n := happened(NMemberUp, "", s.Now)
		n.What, n.Who = r.Member+" up", r.Who
		var head []Change
		switch {
		case ctl == nil:
			head = append(head, change(Fleet, createEntry(CtlID(r.Member), r.Member, Ctl, 0,
				map[string]string{"kind": "member", "status": Up, "since": stamp(s.Now), "ok": "0", "failed": "0"})))
		case ctl.F("status") != Up:
			head = append(head, change(Fleet, setEntry(ctl, map[string]string{"status": Up, "since": stamp(s.Now)})))
		default:
			n = Note{}
		}
		up := s.UpMembers()
		if !contains(up, r.Member) {
			up = append(up, r.Member)
		}
		level(s, &p, orderLike(s.Fleet.Rows, up, r.Member))
		if len(head) > 0 {
			if len(p.Units) == 0 {
				p.Units = append(p.Units, Unit{Key: CtlID(r.Member)})
			}
			p.Units[0].Changes = append(head, p.Units[0].Changes...)
			p.Units[0].Notes = append(p.Units[0].Notes, n)
			p.Units[0].Moved = strings.TrimPrefix(p.Units[0].Moved+"; "+r.Member+" up", "; ")
		}
	case "down":
		ctl := s.MemberCtl(r.Member)
		if ctl == nil {
			p.refuse(r.Member, "no fleet member "+r.Member)
			return p
		}
		var head []Change
		n := happened(NMemberDown, "", s.Now)
		n.What, n.Who = r.Member+" down", r.Who
		if ctl.F("status") != Down {
			head = append(head, change(Fleet, setEntry(ctl, map[string]string{"status": Down, "since": stamp(s.Now)})))
		}
		var up []string
		for _, m := range s.UpMembers() {
			if m != r.Member {
				up = append(up, m)
			}
		}
		cards := append(append([]*Card{}, s.Fleet.Cell(r.Member, Ready)...), s.Fleet.Cell(r.Member, Working)...)
		SortCards(cards)
		q := map[string]int{}
		for _, m := range up {
			q[m] = s.Fleet.Count(m, Ready)
		}
		var withdrawn []string
		for _, c := range cards {
			if len(up) > 0 {
				m := shortest(up, q)
				q[m]++
				p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, m, Ready, nextGen(c, m), "taken"))},
					Moved: fmt.Sprintf("%s %s:%s -> %s:ready gen=%d", c.ID, c.Row, c.Col, m, c.Int("gen")+1)})
				continue
			}
			set := nextGen(c, "")
			set["withdrawn"] = stamp(s.Now)
			u := Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Withdrawn, set, "taken"))},
				Moved: fmt.Sprintf("%s withdrawn gen=%d", c.ID, c.Int("gen")+1)}
			if pr := s.Work.Placed(c.F("primary")); pr != nil && pr.Col == Working && pr.F("work") == c.ID {
				u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")))
				u.Moved += "; " + pr.ID + " working -> ready"
				withdrawn = append(withdrawn, pr.ID)
				w := happened(NWithdrawn, pr.Row, s.Now, pr.ID)
				w.Who = r.Who
				u.Notes = append(u.Notes, w)
			}
			p.Units = append(p.Units, u)
		}
		if len(head) > 0 {
			if len(p.Units) == 0 {
				p.Units = append(p.Units, Unit{Key: CtlID(r.Member)})
			}
			p.Units[0].Changes = append(head, p.Units[0].Changes...)
			p.Units[0].Notes = append(p.Units[0].Notes, n)
			p.Units[0].Moved = strings.TrimPrefix(p.Units[0].Moved+"; "+r.Member+" down", "; ")
		}
	case "level":
		level(s, &p, s.UpMembers())
	default:
		p.refuse(r.Op, "fleet wants up, down or level")
	}
	return p
}

// level evens the up members' ready queues: the newest cards (the last in
// work order) of the longest queue move to the shortest, until no two differ
// by more than one.
func level(s *Snapshot, p *Plan, up []string) {
	if len(up) < 2 {
		return
	}
	queues := map[string][]*Card{}
	for _, m := range up {
		queues[m] = append([]*Card{}, s.Fleet.Cell(m, Ready)...)
	}
	for {
		long, short := up[0], up[0]
		for _, m := range up {
			if len(queues[m]) > len(queues[long]) {
				long = m
			}
			if len(queues[m]) < len(queues[short]) {
				short = m
			}
		}
		if len(queues[long])-len(queues[short]) <= 1 {
			return
		}
		q := queues[long]
		c := q[len(q)-1]
		queues[long] = q[:len(q)-1]
		queues[short] = append(queues[short], c)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, short, Ready, nextGen(c, short)))},
			Moved: fmt.Sprintf("%s %s:ready -> %s:ready gen=%d", c.ID, long, short, c.Int("gen")+1)})
	}
}

// nextGen is the fields of a work card dealt again: a new generation, bound
// to the member it is dealt to ("" when it is withdrawn).
func nextGen(c *Card, member string) map[string]string {
	set := map[string]string{"gen": itoa(c.Int("gen") + 1)}
	if member != "" {
		set["member"] = member
	}
	return set
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// orderLike orders names by the rows' order, a name with no row last.
func orderLike(rows, names []string, extra string) []string {
	var out []string
	for _, r := range rows {
		if contains(names, r) {
			out = append(out, r)
		}
	}
	if !contains(out, extra) && contains(names, extra) {
		out = append(out, extra)
	}
	return out
}
