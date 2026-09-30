package sprint

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The position rules of the event-driven tick (the upper design, version 2.1,
// IT08 of section 8.1): the rules that move a card by where it stands in its
// stream's line, and by what it waits for. R3 resolve releases what is before
// the first sentinel and says when it is reached; R4 needs lowers a waiter's
// count when a need lands or goes, and closes a missing need when it is made;
// R5 cross resumes a stream stopped for a card of another stream once that
// card has landed; R15 done says the sprint is done; R19 pullback puts ready
// cards behind a sentinel back to waiting. Each is a Rule of 8.0: a Read that
// sizes a read plan to layer 1's bounds from the queries' declared costs, and
// a Plan that plans once over every key it was given on the partial snapshot
// the read gave (T4, batch always), never scanning a cell.
//
// Every rule is idempotent (E7, 1.3.3): it plans from the state as read, its
// moves guard the place they were planned from, so a rule run twice on the same
// keys with nothing changed in between plans nothing the second time. Every
// rule names, in its doc, what it would be O(n) without.
//
// A plan reads only what its read asked for (1.5.2): the partial snapshot
// refuses any other read, and rules_position_read.go is where the answers the
// snapshot holds of these rules' queries are taken from.
//
// Where the design is silent, or its shapes do not reach, the narrower reading
// is taken and is listed here; the pull request repeats them as questions.
//
//	Set guards. R3 and R15 must guard the position of whole sets: an rcount of
//	the open cells before a sentinel (reach and unreach), an S.zguard on sent:s
//	(release), an rcount over every open cell of every stream (done).
//	RulePlan (8.0) names no field that can carry a layer 1 count guard: XGuard
//	is a guard on a sprint key with an int64 score, and a score bound is a float
//	written in the store's grammar. SetGuard is this file's shape for one, and
//	it rides in RulePlan.Guards as an XGuard of Kind posSetGuard whose Key is
//	its JSON, so that no guard is dropped on the way to the builder;
//	SetGuardsOf reads them back.
//	The counter guard of R15 (COUNTER on {p}next@e.streams) is an XGuard of Kind
//	posCounter: the kinds 8.0 lists do not include it.
//	The made intent. R4 for a made key "closes the missing judgment on each
//	waiter and takes n out of missing" (2.3), and no intent of 1.3.3 takes n out
//	of missing: this file plans the close as a NoteReq and the removal as an
//	Intent of Kind posMade (Need n, Waiters the waiters of the last head).
//	A made need with more waiters than one read's head. The waiters stay in
//	wait:n, so a second read of the same head would find the same waiters: the
//	key carries a cursor (made:n+w, w the last waiter closed, by id), the need
//	stays in missing, and n leaves it in the read that reaches the end of wait:n.
//	The cursor is a place in the order of wait:n's members and not a count of the
//	waiters closed, so a waiter that leaves wait:n between two heads, the one the
//	cursor names included, skips nobody: n leaves missing only when the cursor
//	has passed every waiter. A head whose members were all left out, being
//	quarantined, with more beyond them moves the cursor past them, in a key of
//	either rule (needs:n+w or made:n+w, w the last member the head read,
//	NeedAnswer.Last): 2.3 R4 has the offset move past quarantined ids, which it
//	leaves out, and the same head read again would find the same ids. A need
//	with a waiter of a dropping stream in its
//	head is left out whole (no close, no removal from missing) and its key stays
//	held, since a close for some of them would be planned again by every run.
//	A key that names a line cannot carry a cursor: a need of a line that
//	is not finished, and cannot move on in place, is carried by a key of its own
//	(needs:n, needs:n+w, made:n or made:n+w) and the line's offset moves past it. A need
//	whose only remaining waiters are in dropping streams is carried the same
//	way, held back until the mark clears (2.3 R4, 1.3.5), so the waiters of the
//	needs after it are served. The key it puts in the agenda for it is in both
//	Requeue and HeldBack: put there, and not planned again until the mark
//	clears.
//	A needs key whose whole head is in dropping streams, with waiters beyond
//	it, cannot reach them: the head of wait:n is the only place a waiter is read
//	from, and a position is not stable while waiters leave. The design does not
//	say how the head moves past frozen waiters.
//	R5 reads the need a cross stop waits for on the stream's control card. The
//	present merge step writes it as the field other (with card, the stuck
//	card), and the design calls it need_card (2.3 R5): posCrossNeedField is the
//	one row that names it. A stopped stream whose stuck cell is cut resumes its
//	stuck cards a chunk at a time and flips the stream to merging in the last
//	chunk only, so a partly resumed stream is still stopped, and the key is
//	requeued only for progress.
//	R5 sizes the ids of a stuck cell for the most streams a sprint may have,
//	since a read plan cannot know how many streams there are: min(2,000, range
//	ids / MaxStreams), not the design's min(2,000, range ids / s).
//	R15 raises "the sprint is done" only when at least one card has landed or
//	been dropped (the spec: written by the step that takes the last open card
//	out), not for a sprint that never had a card; the design says "no open
//	card". Its close is guarded atleast 1, the mirror of R3's unreach; the
//	design gives only the done guard.
//	R19 plans nothing behind a quarantined sentinel: no entry may name a
//	quarantined card (1.3.5).
//	A reached sentinel that gains a need of its own while nothing is before it
//	keeps its judgment: R3's unreach names only n_before (2.3).
//	The causes of the judgments raised: sentinel reached and the sprint is done
//	have none, a missing need's is the need. The decisions a judgment offers are
//	IT06's table's; a NoteReq here names none.
//	The subjects are the tree's: StreamSubject for a stream and SprintSubject
//	for the sprint, where 8.0 writes jopen:sprint.
//	The priorities are IT05's rows (RulePriorities), by name.
//	AgendaKey is the tree's {Key, Seq}, not 8.0's {Rule, Subject, Line, Offset,
//	Order}: posSplitKey, posLineKey and posNeedKey are the places that take a
//	key apart and put one together.
//	The reads take the sprint keys and the answers of the queries from
//	SprintQ.Keys, SprintQ.Counts, SprintQ.WaiterAfter, SprintQ.Missing and the
//	fields of Answer that rules_position_read.go names; IT05's shapes have none
//	of them.

// The numbers of this file, each with the section that gives it. They are this
// file's own: today's machine keeps its constants (DeadlineMergeIdle), and the
// switch (IT23) decides whether the two sets are one.
const (
	// positionChunk is StepChunk: the changed members of one request, layer 1's
	// ceiling (1.0). A read is planned at this chunk halved once for each
	// halving after a BUDGET or a LIMIT, down to one (1.3.5, 1.4.2).
	positionChunk = 2000
	// pullbackMaxSteps is the steps of a tick R19 may send (1.4.2, 8.0).
	pullbackMaxSteps = 1
	// posCrossStuckIDs is the most ids of a stuck cell R5 reads a stream (2.3).
	posCrossStuckIDs = 2000
	// posMergeIdleSpan is the span a stream with cards to merge may go without a
	// merge step: R5 gives a resumed stream its due time (1.2: last merge step or
	// state change + 30 min).
	posMergeIdleSpan = 30 * time.Minute
	// needsLineWindow is the ids of a line R4 reads from a key's offset in one
	// read, and needsMinWaiters the fewest waiters it reads a need for once the
	// ids of a read are counted. The design gives neither (2.3 says "for each n
	// of the key from its offset" and "up to the chunk"): they are this item's
	// choice, so that a landing of a merge batch is read whole in a tick and a
	// need with many waiters is served a chunk a run.
	needsLineWindow = 100
	needsMinWaiters = 20
)

// The names this file adds to the rules of ingest.go: the keys of R19 and the
// second key of R4 (2.1). Both are served by rules of this file (ServingRule).
const (
	posPullbackRule = "pullback"
	posMadeRule     = "made"
)

// The kinds of an intent, of a note request and of a guard that this file
// plans, prefixed so that the constants of the items built beside it do not
// collide. The four kinds of intent of 1.3.3 are needmet and needgone here,
// and posMade is the fifth this file adds (see above).
const (
	posNeedmet  = "needmet"
	posNeedgone = "needgone"
	posMade     = "made"

	posOpen  = "open"
	posClose = "close"
	posKnow  = "know"

	posCounter  = "counter"
	posSetGuard = "setguard"
)

// The types of the two notices of 2.5 that no constant names yet: the count and
// the stream are in the note's text, and IT06's notice table will own the
// words.
const (
	posNoticeMadeReady  = "cards made ready"                 // 2.5: k cards of s made ready
	posNoticePulledBack = "ready cards went back to waiting" // 2.5: k ready cards of s went back to waiting behind G
)

// posCrossNeedField is the field of a stopped stream's control card that names
// the card its cross stop waits for (see above).
const posCrossNeedField = "other"

// posResumeDid is what a resume by the machine says was done, as the present
// tick's does.
const posResumeDid = "the card it needed landed"

// posOpenColumns are the five open cells of a stream in the work table
// (1.3.1, 2.3): a card in one of them has not landed.
var posOpenColumns = []State{Waiting, Ready, Working, Review, Merging}

// posCountColumns are the cells R15 counts on every row: the five open cells
// and the landed one.
var posCountColumns = func() []string {
	cols := make([]string, 0, len(posOpenColumns)+1)
	for _, c := range posOpenColumns {
		cols = append(cols, string(c))
	}
	return append(cols, Landed)
}()

// positionKeyWords are the words of the keys each rule of this file serves, the
// words ServingRule routes to it (2.1): R4 serves the keys of made too.
var positionKeyWords = map[string][]string{
	ruleNeeds:       {ruleNeeds, posMadeRule},
	ruleResolve:     {ruleResolve},
	ruleCross:       {ruleCross},
	ruleDone:        {ruleDone},
	posPullbackRule: {posPullbackRule},
}

// posPriority is a rule's priority, IT05's row for its name: a rules file takes
// its priority from RulePriorities, so a change to the order is a changed row.
func posPriority(name string) int {
	p, ok := PriorityOf(name)
	if !ok {
		panic("sprint: the rule " + name + " has no row in RulePriorities")
	}
	return p
}

// positionRuleRows is the table of the rules of this file: a rule is a row, so
// a change to the design's order or its steps is a changed row.
var positionRuleRows = []Rule{
	{Name: ruleNeeds, Priority: posPriority(ruleNeeds), Read: readNeeds, Plan: planNeeds},
	{Name: ruleResolve, Priority: posPriority(ruleResolve), Read: readResolve, Plan: planResolve},
	{Name: ruleCross, Priority: posPriority(ruleCross), Read: readCross, Plan: planCross},
	{Name: ruleDone, Priority: posPriority(ruleDone), Read: readDone, Plan: planDone},
	{Name: posPullbackRule, Priority: posPriority(posPullbackRule), MaxSteps: pullbackMaxSteps, Read: readPullback, Plan: planPullback},
}

func init() {
	for _, r := range positionRuleRows {
		RegisterRule(r)
	}
}

// A key of the rules of this file taken apart, and put together.

// posKey is an agenda key of the form rule, rule:subject, rule:subject+w,
// rule@seq or rule@seq+offset (E6, 2.1).
type posKey struct {
	rule, subject string
	// line is the seq of a key that names a line, offset the place among the
	// line's ids it resumes from.
	line   uint64
	offset int
	byLine bool
	// after is the cursor of a key of one need: the last waiter of wait:n a
	// made key has closed, or the last member of a head whose members were all
	// left out (needs:n+w or made:n+w, w the id). It reads the head of wait:n
	// after that id. No other key has one.
	after string
}

// posSplitKey takes a key apart; false when it is none of the forms.
func posSplitKey(k AgendaKey) (posKey, bool) {
	i := strings.IndexAny(k.Key, ":@")
	if i < 0 {
		return posKey{rule: k.Key}, k.Key != ""
	}
	p := posKey{rule: k.Key[:i]}
	rest := k.Key[i+1:]
	if k.Key[i] == ':' {
		if p.rule == posMadeRule || p.rule == ruleNeeds {
			if subject, after, has := strings.Cut(rest, "+"); has {
				// no id has a '+', a ':' or an '@': the cursor is one id
				if subject == "" || after == "" || strings.ContainsAny(after, "+:@") {
					return posKey{}, false
				}
				p.subject, p.after = subject, after
				return p, true
			}
		}
		p.subject = rest
		return p, rest != "" && !strings.Contains(rest, "+") // no id has a '+': a cursor is a key of one need's
	}
	num, off, hasOffset := strings.Cut(rest, "+")
	seq, err := strconv.ParseUint(num, 10, 64)
	if err != nil || seq == 0 {
		return posKey{}, false
	}
	p.byLine, p.line = true, seq
	if hasOffset {
		o, err := strconv.Atoi(off)
		if err != nil || o < 0 {
			return posKey{}, false
		}
		p.offset = o
	}
	return p, true
}

// posLineKey is the key of a rule for a line from an offset, with the order of
// the key it stands for: rule@seq, or rule@seq+offset past the first id.
func posLineKey(rule string, line uint64, offset int, order uint64) AgendaKey {
	key := rule + "@" + strconv.FormatUint(line, 10)
	if offset > 0 {
		key += "+" + strconv.Itoa(offset)
	}
	return AgendaKey{Key: key, Seq: order}
}

// posNeedKey is the key of a rule for one need from a waiter: rule:n, or
// rule:n+w past the member w of wait:n (the last one served, or the last one
// a head left out). Only a key of one need has a cursor.
func posNeedKey(rule, need, after string, order uint64) AgendaKey {
	key := rule + ":" + need
	if after != "" && (rule == posMadeRule || rule == ruleNeeds) {
		key += "+" + after
	}
	return AgendaKey{Key: key, Seq: order}
}

// posServes says the rule serves keys of the word (positionKeyWords).
func posServes(rule, word string) bool { return slices.Contains(positionKeyWords[rule], word) }

// posOwnKeys splits keys into those of the rule, in order, and the others,
// which include a key of another form that no rule of this file can read: they
// are left alone.
func posOwnKeys(keys []AgendaKey, rule string) (mine, others []AgendaKey) {
	for _, k := range keys {
		if p, ok := posSplitKey(k); ok && posServes(rule, p.rule) {
			mine = append(mine, k)
		} else {
			others = append(others, k)
		}
	}
	return mine, others
}

// A read is cut to layer 1's bounds by the queries' declared costs.

// posFits says the cost, added to what the plan has already asked, and one
// query more, is within the bounds.
func posFits(b ReadBounds, used, c Cost, queries int) bool {
	return queries <= b.Queries && used.Records+c.Records <= b.Records &&
		used.RangeIDs+c.RangeIDs <= b.RangeIDs && used.Bytes+c.Bytes <= b.Bytes
}

// posSharedFronts is the front(s) queries of the streams of one read that
// share one chunk (the step of R3 moves at most one chunk in all, and R19 sends
// at most one step a tick): n streams read chunk / n ids of their head each, and
// n is cut until the queries' declared cost and their number fit layer 1's
// bounds (posFits). The streams that do not fit are left for the next tick, and
// the first always goes. query makes the query of a stream with that head.
func posSharedFronts(streams []string, chunk int, b ReadBounds, query func(stream string, head int) SprintQ) []SprintQ {
	n := min(len(streams), chunk)
	for n > 1 {
		c := QueryCost(query("", max(1, chunk/n)))
		if posFits(b, Cost{}, Cost{Records: n * c.Records, RangeIDs: n * c.RangeIDs, Bytes: n * c.Bytes}, n) {
			break
		}
		n /= 2
	}
	out := make([]SprintQ, 0, n)
	for _, stream := range streams[:n] {
		out = append(out, query(stream, max(1, chunk/n)))
	}
	return out
}

// Layer 1's guards, and the entries of a plan.

// SetGuard is a guard over a set, checked by layer 1 at apply and refusing the
// whole step when it does not hold (L1 3): an rcount entry (the sum of the
// counts of Cells, in Table, of the members scored within Min and Max, within
// AtLeast and AtMost), or an S.zguard on a sprint index key (Kind zguard: the
// same over the members of Key). Min and Max are score bounds in the store's
// grammar ("-inf", "+inf", a number, "(" and a number for an open bound); an
// absent count bound is not checked.
type SetGuard struct {
	// Kind is GuardRCount or GuardZGuard.
	Kind string `json:"kind"`
	// Table and Cells are an rcount's cells, each "<row>:<col>".
	Table string   `json:"table,omitempty"`
	Cells []string `json:"cells,omitempty"`
	// Key is a zguard's sorted set, named without the deployment prefix and
	// epoch: sent:<stream>.
	Key string `json:"key,omitempty"`
	// Min and Max are the score bounds.
	Min string `json:"min"`
	Max string `json:"max"`
	// AtLeast and AtMost are the inclusive bounds of the count; nil is none.
	AtLeast *int `json:"atleast,omitempty"`
	AtMost  *int `json:"atmost,omitempty"`
}

// The kinds of a SetGuard.
const (
	// GuardRCount is layer 1's rcount entry over cells of a table.
	GuardRCount = "rcount"
	// GuardZGuard is layer 1's S.zguard, an rcount over a sprint index key.
	GuardZGuard = "zguard"
)

// XGuard is the guard as a plan carries it in RulePlan.Guards: Kind posSetGuard
// and its JSON as the key.
func (g SetGuard) XGuard() XGuard {
	b, err := json.Marshal(g)
	if err != nil {
		panic("sprint: a set guard did not encode: " + err.Error())
	}
	return XGuard{Kind: posSetGuard, Key: string(b)}
}

// SetGuardsOf is the set guards a rule plan carries, in order; the guards of
// other kinds are not its. A guard that does not decode is an error, never
// dropped.
func SetGuardsOf(p RulePlan) ([]SetGuard, error) {
	var out []SetGuard
	for _, x := range p.Guards {
		if x.Kind != posSetGuard {
			continue
		}
		var g SetGuard
		if err := json.Unmarshal([]byte(x.Key), &g); err != nil {
			return nil, fmt.Errorf("a set guard of the plan does not decode: %w", err)
		}
		out = append(out, g)
	}
	return out, nil
}

func posInt(n int) *int { return &n }

// posOpenCells is the five open cells of each row, row by row.
func posOpenCells(rows ...string) []string {
	cells := make([]string, 0, len(rows)*len(posOpenColumns))
	for _, r := range rows {
		for _, c := range posOpenColumns {
			cells = append(cells, r+":"+string(c))
		}
	}
	return cells
}

// posBefore is the rcount of the open cells of a stream below σ: none of them
// before it (reach), or at least one (unreach).
func posBefore(stream string, sigma float64, atLeast, atMost *int) SetGuard {
	return SetGuard{Kind: GuardRCount, Table: Work, Cells: posOpenCells(stream),
		Min: "-inf", Max: "(" + fmtScore(sigma), AtLeast: atLeast, AtMost: atMost}
}

// posNoSentinelUpTo is the S.zguard that no sentinel of the stream has been
// placed at or before a score since the read (release, R3).
func posNoSentinelUpTo(stream string, score float64) SetGuard {
	return SetGuard{Kind: GuardZGuard, Key: "sent:" + stream, Min: "-inf", Max: fmtScore(score), AtMost: posInt(0)}
}

// posPlaceOnly guards a card at the place it was read at, and its revision not
// at all: a release reads only place and index (1.3.3, the guard rule for plain
// entries).
func posPlaceOnly(c *Card) *ntable.MemberExpect {
	return &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}
}

// posMove moves a card, guarded by its place only, to a column of its own row,
// keeping its score.
func posMove(c *Card, col string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Expect: posPlaceOnly(c), Move: &ntable.MemberMoveOp{Row: c.Row, Col: col}}
}

// posGuard is a guard-only entry on a card at the place it was read at.
func posGuard(c *Card) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Expect: posPlaceOnly(c)}
}

func posIDs(cards []*Card) []string {
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	return ids
}

// posPlaceWord is where a card is, for a refusal's words: the record's place,
// or that it is kept off the table. It reads no field: a projection need not
// carry one for a word.
func posPlaceWord(c *Card) string {
	if c.Placed() {
		return c.Row + ":" + c.Col
	}
	return "kept, off the table"
}

// posDropSplit is the waiters of streams not being dropped, and those that are:
// X refuses a card of a dropping stream, so a plan leaves it out and its key
// stays (1.3.5).
func posDropSplit(s *Snapshot, cards []*Card) (kept, out []*Card) {
	for _, c := range cards {
		if posDropping(s, c.Row) {
			out = append(out, c)
		} else {
			kept = append(kept, c)
		}
	}
	return kept, out
}

// R3 resolve.

// resolveFields are the fields of the records R3's read names: what its plan
// reads of a head card and of G.
var resolveFields = []string{"kind", "open", "refused"}

func posResolveQuery(stream string, head int) SprintQ {
	return SprintQ{Kind: QueryFront, Stream: stream, Fields: resolveFields,
		Heads: []HeadQ{{Index: HeadEligBelow, Limit: head}},
		Keys:  []string{KeyJOpenG, KeyDropping}}
}

// readResolve is R3's read (2.3): front(s) for each stream of a key, with jopen
// of G and the dropping mark, and its head of elig:s below σ. The step of R3 moves
// at most one chunk in all, so the streams of a read share it (posSharedFronts):
// a key that does not fit is left for the next tick, and the first always goes.
func readResolve(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	own, left := posOwnKeys(keys, ruleResolve)
	var mine []AgendaKey
	var streams []string
	for _, k := range own {
		if p, _ := posSplitKey(k); p.subject != "" && !p.byLine {
			mine, streams = append(mine, k), append(streams, p.subject)
		} else {
			left = append(left, k)
		}
	}
	rp := ReadPlan{Sprint: posSharedFronts(streams, Halved(positionChunk, halvings), b, posResolveQuery)}
	return rp, append(left, mine[len(rp.Sprint):]...)
}

// ReachedCause is the cause of "sentinel reached" (the model's "-": a sentinel
// is reached for no cause beyond itself; tla/SprintEvents.tla, VEff "release"
// and ReachedPassed close JClose(@, "reached", CS(a), "-")). J refuses a state
// op with an empty cause (REQUEST), so R3's reach, its unreach, release and a
// rank or insertion that passes the sentinel all name this one.
const ReachedCause = "-"

// planResolve is R3, resolve:s, on the partial snapshot: for each stream, in
// this order and at most one chunk in all,
//
//  1. release: the cards of elig:s below σ (all of elig:s when the stream has no
//     sentinel) go waiting to ready, into fresh:s; guarded by each card at
//     waiting (place only) and by S.zguard(sent:s, rcount, -inf, the highest
//     score released, atmost 0), so no sentinel has been placed before a
//     released card since the read;
//  2. reach: nothing of the stream's five open cells is before σ, G counts no
//     need (open 0) and is not quarantined, and no "sentinel reached" is open
//     or held on it (jopen:G): the judgment is raised. It guards G at waiting
//     with its revision and rcount of the five open cells in (-inf, σ) at most
//     0;
//  3. unreach: the judgment is open or held and something is before σ: it
//     closes, with the reason, guarded by the same rcount at least 1.
//
// Both name the judgment's cause ReachedCause, so the close meets the field the
// open wrote.
//
// A quarantined sentinel is σ still, so nothing behind it is released, and R3
// plans no reach for it (1.0). A dropping stream plans nothing and its key
// stays, held back (1.3.5). The key is requeued while the head of elig:s was
// cut at the chunk and removed otherwise. Raises KNOW "k cards of s made ready",
// one a step for each stream, and the reach judgment. A card of the head that is
// not free to go is refused (1.3.5): the answer must not lead the rule to move
// what its definition excludes.
//
// O(k) moved plus front(s): a release of 2,000 is about 21 ms of store time
// (2.3), and each stream costs the plan a pass over its head. Without it,
// release scans waiting in s and reach counts every open card of s.
func planResolve(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	for _, k := range keys {
		p, ok := posSplitKey(k)
		if !ok || p.rule != ruleResolve || p.subject == "" || p.byLine {
			continue
		}
		f, ok := posFrontOf(s, p.subject)
		if !ok {
			continue
		}
		if posDropping(s, p.subject) {
			rp.HeldBack = append(rp.HeldBack, k)
			continue
		}
		head, more, ok := posHeadOf(s, p.subject, HeadEligBelow)
		if !ok {
			continue
		}
		moved := rp.resolveStream(s, p.subject, f, head)
		if more && moved {
			rp.Requeue = append(rp.Requeue, k)
		} else {
			rp.Done = append(rp.Done, k)
		}
	}
	return rp
}

// resolveStream plans release, reach and unreach for one stream; moved says
// the plan moved or refused a card of the head, which is the progress that
// lets the key be requeued (ChunkProgress, 5).
func (rp *RulePlan) resolveStream(s *Snapshot, stream string, f posFront, head []*Card) (moved bool) {
	var released []*Card
	var top float64
	for _, c := range head {
		if why := posReleasable(c, stream); why != "" {
			rp.Plan.refuse(c.ID, why)
			moved = true
			continue
		}
		if len(released) == 0 || c.Score > top {
			top = c.Score
		}
		released = append(released, c)
		rp.Plan.Units = append(rp.Plan.Units, Unit{Key: c.ID, Stream: stream,
			Changes: []Change{change(Work, posMove(c, Ready))}, Moved: c.ID + " waiting -> ready"})
	}
	if len(released) > 0 {
		moved = true
		rp.Guards = append(rp.Guards, posNoSentinelUpTo(stream, top).XGuard())
		rp.Notes = append(rp.Notes, NoteReq{Op: posKnow, Type: posNoticeMadeReady, Subjects: []string{StreamSubject(stream)},
			Text: fmt.Sprintf("%d cards of %s made ready", len(released), stream)})
	}
	g := f.G
	if g == nil {
		return moved
	}
	judged := posJudgedG(s, stream, NSentinelReached)
	switch {
	case !f.GQuarantined && f.NBefore == 0 && g.Int("open") == 0 && !judged:
		rp.Plan.Units = append(rp.Plan.Units, Unit{Key: g.ID, Stream: stream,
			Changes: []Change{change(Work, guardEntry(g))}, Moved: "sentinel " + g.ID + " reached"})
		rp.Guards = append(rp.Guards, posBefore(stream, g.Score, nil, posInt(0)).XGuard())
		rp.Notes = append(rp.Notes, NoteReq{Op: posOpen, Type: NSentinelReached, Cause: ReachedCause, Subjects: []string{g.ID},
			Text: fmt.Sprintf("sentinel %s reached: nothing of %s is open before it", g.ID, stream)})
	case judged && f.NBefore > 0:
		rp.Guards = append(rp.Guards, posBefore(stream, g.Score, posInt(1), nil).XGuard())
		rp.Notes = append(rp.Notes, NoteReq{Op: posClose, Type: NSentinelReached, Cause: ReachedCause, Subjects: []string{g.ID},
			Text: fmt.Sprintf("%d cards now before it", f.NBefore)})
	}
	return moved
}

// posReleasable is why a card of the head of elig:s is not free to go, "" when
// it is: elig is the primaries of the stream in waiting that name no need left
// and are not refused (1.3.1).
func posReleasable(c *Card, stream string) string {
	switch {
	case c.Row != stream || c.Col != Waiting:
		return "not waiting in " + stream + " (it is " + posPlaceWord(c) + ")"
	case IsSentinel(c):
		return "a sentinel is released by the coordinator, never by the machine"
	case c.Int("open") != 0:
		return "it still counts " + strconv.Itoa(c.Int("open")) + " needs"
	case c.F("refused") != "":
		return "it is refused: " + c.F("refused")
	}
	return ""
}

// R4 needs and made.

// needsFields are the fields R4's read names of a waiter. A plan reads a waiter's
// place and nothing of its fields; IT05's shapes cannot say "no field" (an empty
// list is whole records, and costs 976 bytes), so the read names the one the
// intents change, the count of needs it waits for.
var needsFields = []string{"open"}

// posNeedsHead is the most waiters a read of ids ids can take the head of wait:n
// at, within layer 1's records, range ids and bytes by the declared cost of
// waiters (1 + the head an id): 0 when not even one fits.
func posNeedsHead(b ReadBounds, ids, chunk int) int {
	rec := recordBytes(needsFields)
	head := min(chunk, b.Records/ids-1, b.RangeIDs/ids)
	if b.Bytes > 0 {
		head = min(head, (b.Bytes/ids-rec)/(rec+RangeIDBytes))
	}
	return head
}

// readNeeds is R4's read (2.3): waiters for the needs of each key. A key that
// names one need (needs:n, needs:n+w, made:n, made:n+w) reads that one, after its
// cursor (the last waiter a made key served, or the last member of a head whose
// members were all left out); a key that names a line (needs@seq, made@seq, with
// an offset) names the line by its seq as the id source and reads the next needsLineWindow of its ids
// from the offset. Every need is read for the head of wait:n up to the chunk,
// cut so that the ids of the read at their head fit layer 1's records, range
// ids and bytes (declared cost 1 + the head an id), and no lower than
// needsMinWaiters unless the chunk is; a key that would take the head below
// that is left for the next tick, and the first always goes. A made key reads
// the head only for the needs that have a score in {p}missing@e. Every query
// reads the dropping marks, for the streams of its waiters.
func readNeeds(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	chunk := Halved(positionChunk, halvings)
	floor := min(needsMinWaiters, chunk)
	var picked []posKey
	var left []AgendaKey
	ids := 0
	for _, k := range keys {
		p, ok := posSplitKey(k)
		if !ok || !posServes(ruleNeeds, p.rule) {
			left = append(left, k)
			continue
		}
		w := 1
		if p.byLine {
			w = needsLineWindow
		}
		afford := posNeedsHead(b, ids+w, chunk)
		if len(picked) > 0 && (afford < floor || len(picked)+1 > b.Queries) {
			left = append(left, k)
			continue
		}
		picked, ids = append(picked, p), ids+w
	}
	var rp ReadPlan
	if len(picked) == 0 {
		return rp, left
	}
	build := func(head int) []SprintQ {
		qs := make([]SprintQ, 0, len(picked))
		for _, p := range picked {
			q := SprintQ{Kind: QueryWaiters, Limit: head, Fields: needsFields, Missing: p.rule == posMadeRule, Keys: []string{KeyDropping}}
			if p.byLine {
				q.Source = IDSource{Kind: SourceLine, Seq: p.line, Offset: p.offset, Limit: needsLineWindow}
			} else {
				q.Source, q.WaiterAfter = IDSource{Kind: SourceIDs, IDs: []string{p.subject}}, p.after
			}
			qs = append(qs, q)
		}
		return qs
	}
	// the declared cost, dropping marks and all, of the queries at that head
	cost := func(qs []SprintQ) (c Cost) {
		for _, q := range qs {
			c = c.Add(QueryCost(q))
		}
		return c
	}
	head := max(1, posNeedsHead(b, ids, chunk))
	qs := build(head)
	for head > 1 && !posFits(b, Cost{}, cost(qs), len(qs)) {
		head = max(1, head-max(1, head/10))
		qs = build(head)
	}
	rp.Sprint = qs
	return rp, left
}

// needStatus is where the work owed for one need stands after a run.
type needStatus int

const (
	// needFinished: nothing more is owed for the need.
	needFinished needStatus = iota
	// needProgress: waiters were served and others stay in the head of wait:n
	// or beyond it: the same key again finds the rest, and the head strictly
	// moves (ChunkProgress).
	needProgress
	// needHeld: what is owed is a waiter of a dropping stream, and nothing
	// could be planned: the work stays, held back until the mark clears.
	needHeld
	// needContinue: a made need's head was closed and its waiters stay in wait:n,
	// or a head's members were all left out (quarantined) with more beyond them:
	// the work resumes after the last member the head read (next), a place in
	// wait:n that a waiter leaving it does not move (2.3 R4).
	needContinue
)

// needResult is a need's status and, for needContinue, the last waiter served:
// the key resumes after it.
type needResult struct {
	status needStatus
	next   string
}

// planNeeds is R4 on the partial snapshot, for keys of two names, needs and
// made (2.1). For each need of a key, in order:
//
//   - needs, the need landed: Intent needmet (n, the waiters); the need was
//     removed (no place): Intent needgone (n, the waiters). Lua decides at
//     apply, from the real before-state, which waiters are still in wait:n
//     (1.3.3), so two needs of one waiter landing in one tick lower its open
//     twice. A need that is still open plans nothing: its landing or removal
//     will queue the key again.
//   - made, the need has a score in {p}missing@e: J closes "blocked on something
//     missing: n" on the waiters of the head (NoteReq close). The waiters stay in
//     wait:n with open unchanged, so each now waits for n to land; n leaves
//     missing (Intent made) when the head reaches the end of wait:n, and while
//     more waiters are beyond it the key resumes after the last one closed, by
//     id (made:n+w).
//
// A waiter of a dropping stream is left out of the plan and the key stays for
// it, held back when that was all its work (1.3.5). The key is requeued at the
// same offset while wait:n held a full chunk and the head moved; a need of a
// line that cannot move on in place is carried by a key of its own and the
// line's offset moves past it (a need whose only remaining waiters are in
// dropping streams: 2.3 says the offset moves past n once its set is empty of
// waiters outside dropping streams); the key is removed at the end of its line.
// No guard is put on the waiters: the intents and J decide in Lua.
//
// O(w) a chunk: 2,000 waiters about 20 ms of store time (2.3); a made line of
// 2,000 ids is one ZMSCORE in two pieces. Without it, who needs n is a scan of
// waiting in every stream.
func planNeeds(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	carried := map[string]bool{}
	for _, k := range keys {
		p, ok := posSplitKey(k)
		if !ok || !posServes(ruleNeeds, p.rule) {
			continue
		}
		kv, ok := posKeyViewOf(s, k)
		if !ok {
			continue
		}
		rp.needsKey(s, k, p, kv, carried)
	}
	return rp
}

// needsKey plans one key of R4 and settles it: where the key resumes, whether
// it is done, requeued or held back. carried are the texts of the keys this plan
// already put in the agenda for a need of its own.
func (rp *RulePlan) needsKey(s *Snapshot, k AgendaKey, p posKey, kv posKeyView, carried map[string]bool) {
	made := p.rule == posMadeRule
	res := make([]needResult, len(kv.Needs))
	for j, nv := range kv.Needs {
		res[j] = rp.planNeed(s, made, p, nv)
	}
	if !p.byLine {
		if len(res) == 0 {
			rp.Done = append(rp.Done, k)
			return
		}
		switch r := res[0]; r.status {
		case needProgress:
			rp.Requeue = append(rp.Requeue, k)
		case needHeld:
			rp.HeldBack = append(rp.HeldBack, k)
		case needContinue:
			rp.Done = append(rp.Done, k)
			rp.Requeue = append(rp.Requeue, posNeedKey(p.rule, kv.Needs[0].Need, r.next, k.Seq))
		default:
			rp.Done = append(rp.Done, k)
		}
		return
	}
	// A line: it resumes at the first need whose work is served in place, so that
	// its waiters are read again from the same head; the needs before it that
	// are not finished are carried by keys of their own.
	resume := len(res)
	for j, r := range res {
		if r.status == needProgress {
			resume = j
			break
		}
	}
	for j, r := range res[:resume] {
		var key AgendaKey
		switch r.status {
		case needHeld:
			key = posNeedKey(p.rule, kv.Needs[j].Need, "", k.Seq)
		case needContinue:
			key = posNeedKey(p.rule, kv.Needs[j].Need, r.next, k.Seq)
		default:
			continue
		}
		if carried[key.Key] {
			continue
		}
		carried[key.Key] = true
		rp.Requeue = append(rp.Requeue, key)
		if r.status == needHeld {
			rp.HeldBack = append(rp.HeldBack, key)
		}
	}
	at := p.offset + resume
	switch {
	case resume == len(res) && !kv.MoreIDs:
		rp.Done = append(rp.Done, k)
	case at == p.offset:
		rp.Requeue = append(rp.Requeue, k)
	default:
		rp.Done = append(rp.Done, k)
		rp.Requeue = append(rp.Requeue, posLineKey(p.rule, p.line, at, k.Seq))
	}
}

// planNeed plans the work of one need of a key and says where it stands.
func (rp *RulePlan) planNeed(s *Snapshot, made bool, p posKey, nv posNeed) needResult {
	ws, frozen := posDropSplit(s, nv.Waiters)
	if made {
		switch {
		case !nv.Missing:
			return needResult{}
		case len(frozen) > 0:
			// all or nothing for the head: its waiters stay in wait:n, so a close
			// planned for some of them would be planned again by every run while
			// the others are frozen
			return needResult{status: needHeld}
		}
		if len(ws) > 0 {
			rp.Notes = append(rp.Notes, NoteReq{Op: posClose, Type: NMissingNeed, Cause: nv.Need, Subjects: posIDs(ws),
				Text: nv.Need + " was created; its waiters now wait for it to land"})
		}
		if nv.More {
			return needResult{status: needContinue, next: posCursor(nv)}
		}
		rp.Intents = append(rp.Intents, Intent{Kind: posMade, Need: nv.Need, Waiters: posIDs(ws)})
		return needResult{}
	}
	if nv.Place != Landed && nv.Place != "" {
		return needResult{} // still open: its landing or removal queues the key again
	}
	kind := posNeedmet
	if nv.Place == "" {
		kind = posNeedgone
	}
	switch {
	case len(ws) > 0:
		rp.Intents = append(rp.Intents, Intent{Kind: kind, Need: nv.Need, Waiters: posIDs(ws)})
		if len(frozen) > 0 || nv.More {
			return needResult{status: needProgress}
		}
	case len(frozen) > 0:
		return needResult{status: needHeld}
	case nv.More:
		// every member the head read was left out (quarantined), and wait:n has
		// more: the same head would find them again, so the key moves past them
		// (2.3 R4: the offset moves past quarantined ids, which it leaves out)
		return needResult{status: needContinue, next: posCursor(nv)}
	}
	return needResult{}
}

// posCursor is where a need's next head starts: after the last member the head
// read (NeedAnswer.Last), which is at or after its last waiter, or after that
// waiter when the answer does not name one.
func posCursor(nv posNeed) string {
	if nv.Last != "" {
		return nv.Last
	}
	return nv.Waiters[len(nv.Waiters)-1].ID
}

// R5 cross.

// crossFields are the fields R5's read names of a control card: what its plan
// reads of one.
var crossFields = []string{"state", "cause", posCrossNeedField, "card"}

// readCross is R5's read (2.3): the one streams query, every stream's control
// card and for each stopped on a cross need its need's place and the first ids
// of its stuck cell, with the dropping marks. The ids are sized for the most
// streams a sprint may have (see above), min(2,000, range ids / MaxStreams) and
// halved after a BUDGET or a LIMIT, so that the read fits whatever the number of
// stopped streams.
func readCross(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	mine, left := posOwnKeys(keys, ruleCross)
	var rp ReadPlan
	if len(mine) == 0 {
		return rp, left
	}
	stuck := Halved(max(1, min(posCrossStuckIDs, b.RangeIDs/MaxStreams)), halvings)
	rp.Sprint = []SprintQ{{Kind: QueryStreams, Limit: stuck, Fields: crossFields, Keys: []string{KeyDropping}}}
	return rp, left
}

// planCross is R5, cross, on the partial snapshot: for each stream stopped with
// cause cross whose need card has landed, today's Resume (2.3): its stuck cards
// go back to queued at their unchanged scores, the stream to merging (with
// since, did and due_mergeidle, from which the merge-idle entry is derived),
// its cause, card and other unset, its cross judgment closed, and KNOW "stream
// resumed: the card it needed landed". A stuck cell cut at the read resumes
// its first ids, keeps the stream stopped and requeues the key; only the last
// chunk flips the stream. It guards each resumed stream's control card with its
// revision and each stuck card at stuck (place only). A dropping stream is
// left out and the key stays for it.
//
// O(s + stuck): ten streams about 0.3 ms (2.3). Without it, an index from
// each needed card to its stopped streams.
func planCross(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	mine, _ := posOwnKeys(keys, ruleCross)
	if len(mine) == 0 || !posStreamsRead(s) {
		return rp
	}
	cut, leftOut := false, false
	for _, stream := range s.Streams() {
		ctl := s.StreamCtl(stream)
		if ctl == nil || ctl.F("state") != StreamStopped || ctl.F("cause") != "cross" {
			continue
		}
		need := ctl.F(posCrossNeedField)
		if need == "" || s.StateOf(need) != Landed {
			continue
		}
		stuck, ok := posStuckOf(s, stream)
		if !ok || len(stuck.IDs) == 0 {
			continue
		}
		if posDropping(s, stream) {
			leftOut = true
			continue
		}
		cut = cut || stuck.More
		rp.resumeStream(stream, ctl, stuck, now)
	}
	moved := len(rp.Plan.Units) > 0
	for _, k := range mine {
		switch {
		case (cut || leftOut) && moved:
			rp.Requeue = append(rp.Requeue, k)
		case leftOut:
			rp.HeldBack = append(rp.HeldBack, k)
		default:
			rp.Done = append(rp.Done, k)
		}
	}
	return rp
}

// resumeStream plans the resume of one stopped stream over the stuck ids read.
func (rp *RulePlan) resumeStream(stream string, ctl *Card, stuck StuckAnswer, now Now) {
	u := Unit{Key: ctl.ID, Stream: stream}
	if stuck.More {
		u.Changes = append(u.Changes, change(Merge, guardEntry(ctl)))
		u.Moved = fmt.Sprintf("stream %s stays stopped; %d stuck -> queued, more to resume", stream, len(stuck.IDs))
	} else {
		set := map[string]string{
			"state":         StreamMerging,
			"since":         stamp(time.UnixMilli(now.Wall)),
			"did":           posResumeDid,
			"due_mergeidle": strconv.FormatInt(now.R+posMergeIdleSpan.Milliseconds(), 10),
		}
		u.Changes = append(u.Changes, change(Merge, setEntry(ctl, set, "cause", "card", "other")))
		u.Moved = fmt.Sprintf("stream %s stopped -> merging; %d stuck -> queued", stream, len(stuck.IDs))
		rp.Notes = append(rp.Notes,
			NoteReq{Op: posClose, Type: NCross, Subjects: []string{StreamSubject(stream)}, Text: posResumeDid},
			NoteReq{Op: posKnow, Type: NResumed, Subjects: []string{StreamSubject(stream)}, Text: "stream " + stream + " resumed: " + posResumeDid})
	}
	for _, id := range stuck.IDs {
		u.Changes = append(u.Changes, change(Merge, ntable.BatchMemberEntry{
			ID:     id,
			Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: stream, Col: Stuck}},
			Move:   &ntable.MemberMoveOp{Row: stream, Col: Queued},
			Unset:  []string{"need_card", "need_stream"},
		}))
	}
	rp.Plan.Units = append(rp.Plan.Units, u)
}

// R15 done.

// readDone is R15's read (2.3): the control card of every stream, for its
// dropped count, the stream set's version and jopen:sprint, and the counts of
// every stream's five open cells and its landed cell. It is one streams query,
// whatever the number of streams.
func readDone(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	mine, left := posOwnKeys(keys, ruleDone)
	var rp ReadPlan
	if len(mine) > 0 {
		rp.Sprint = []SprintQ{{Kind: QueryStreams, Fields: []string{"dropped"}, Counts: posCountColumns,
			Keys: []string{KeyNextStreams, KeyJOpenSprint}}}
	}
	return rp, left
}

// planDone is R15, done, on the partial snapshot. No open card in any stream
// (and a card landed or dropped: see above) and no "the sprint is done" open:
// the judgment is raised once, "n landed, m dropped". Open cards and the
// judgment open: it closes, "work was added". It guards with one rcount entry
// over the five open cells of every stream, scores -inf to +inf, at most 0 for
// the decision and at least 1 for the close, however many streams there are,
// and with COUNTER on {p}next@e.streams as read, so a stream added or removed
// since the read refuses the step (a race, 1.3.5). The key is removed.
//
// O(s) cells in one entry and one read. Without it, nothing: the counts are
// cells.
func planDone(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	mine, _ := posOwnKeys(keys, ruleDone)
	if len(mine) == 0 || !posStreamsRead(s) {
		return rp
	}
	version, ok := posNextStreams(s)
	if !ok {
		return rp
	}
	rows := s.Streams()
	open, landed, dropped := 0, 0, 0
	for _, row := range rows {
		for _, col := range posOpenColumns {
			open += s.Work.Count(row, string(col))
		}
		landed += s.Work.Count(row, Landed)
		dropped += s.StreamCtl(row).Int("dropped")
	}
	judged := posJudgedSprint(s, NSprintDone)
	counter := XGuard{Kind: posCounter, Key: "streams", Score: int64(version)}
	all := func(atLeast, atMost *int) SetGuard {
		return SetGuard{Kind: GuardRCount, Table: Work, Cells: posOpenCells(rows...), Min: "-inf", Max: "+inf", AtLeast: atLeast, AtMost: atMost}
	}
	switch {
	case len(rows) > 0 && open == 0 && landed+dropped > 0 && !judged:
		rp.Guards = append(rp.Guards, all(nil, posInt(0)).XGuard(), counter)
		rp.Notes = append(rp.Notes, NoteReq{Op: posOpen, Type: NSprintDone, Subjects: []string{SprintSubject},
			Text: fmt.Sprintf("%d landed, %d dropped", landed, dropped)})
	case open > 0 && judged:
		rp.Guards = append(rp.Guards, all(posInt(1), nil).XGuard(), counter)
		rp.Notes = append(rp.Notes, NoteReq{Op: posClose, Type: NSprintDone, Subjects: []string{SprintSubject},
			Text: "work was added"})
	}
	rp.Done = append(rp.Done, mine...)
	return rp
}

// R19 pullback.

// pullbackFields are the fields R19's read names: what its plan reads of a card
// of the head.
var pullbackFields = []string{"kind"}

func posPullbackQuery(stream string, head int) SprintQ {
	return SprintQ{Kind: QueryFront, Stream: stream, Fields: pullbackFields,
		Heads: []HeadQ{{Index: HeadFreshAbove, Limit: head}},
		Keys:  []string{KeyDropping}}
}

// readPullback is R19's read (2.3): front(s) for the streams of the keys, the
// head of fresh:s above σ, with the dropping mark. One tick sends at most one
// step of this rule (MaxSteps 1), so the streams of a read share one chunk
// (posSharedFronts): a key that does not fit is left for the next tick, and the
// first always goes.
func readPullback(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	own, left := posOwnKeys(keys, posPullbackRule)
	var mine []AgendaKey
	var streams []string
	for _, k := range own {
		if p, _ := posSplitKey(k); p.subject != "" && !p.byLine {
			mine, streams = append(mine, k), append(streams, p.subject)
		} else {
			left = append(left, k)
		}
	}
	rp := ReadPlan{Sprint: posSharedFronts(streams, Halved(positionChunk, halvings), b, posPullbackQuery)}
	return rp, append(left, mine[len(rp.Sprint):]...)
}

// planPullback is R19, pullback:s, on the partial snapshot: the cards of fresh:s
// above σ go ready to waiting (the lifecycle row "a sentinel placed in front of
// it", its cause naming the sentinel G) and join elig:s by derivation. It
// guards each pulled card at ready (place only) and G at waiting. Deal guards σ
// itself, so the pull back is never on the path of correctness: it keeps the
// table showing the order the owner set. A stream with no sentinel, or with a
// quarantined one, has nothing to pull back behind; a dropping stream is held
// back (1.3.5). The key is requeued while the head was cut and removed
// otherwise; the rule sends at most one step a tick (MaxSteps 1), after every
// other rule. Raises KNOW "k ready cards of s went back to waiting behind G",
// one a step for each stream.
//
// O(k) moved: 2,000 about 21 ms of store time (2.3). Without it, nothing: a
// card above σ is never dealt either way.
func planPullback(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	for _, k := range keys {
		p, ok := posSplitKey(k)
		if !ok || p.rule != posPullbackRule || p.subject == "" || p.byLine {
			continue
		}
		f, ok := posFrontOf(s, p.subject)
		if !ok {
			continue
		}
		if posDropping(s, p.subject) {
			rp.HeldBack = append(rp.HeldBack, k)
			continue
		}
		head, more, ok := posHeadOf(s, p.subject, HeadFreshAbove)
		if !ok {
			continue
		}
		moved := rp.pullStream(p.subject, f, head)
		if more && moved {
			rp.Requeue = append(rp.Requeue, k)
		} else {
			rp.Done = append(rp.Done, k)
		}
	}
	return rp
}

// pullStream plans the pull back of one stream; moved says it moved or refused
// a card.
func (rp *RulePlan) pullStream(stream string, f posFront, head []*Card) (moved bool) {
	g := f.G
	if g == nil || f.GQuarantined || len(head) == 0 {
		return false
	}
	var pulled []*Card
	for _, c := range head {
		switch {
		case c.Row != stream || c.Col != Ready:
			rp.Plan.refuse(c.ID, "not ready in "+stream+" (it is "+posPlaceWord(c)+")")
			moved = true
		case IsSentinel(c):
			rp.Plan.refuse(c.ID, "a sentinel never goes back to waiting")
			moved = true
		default:
			pulled = append(pulled, c)
		}
	}
	if len(pulled) == 0 {
		return moved
	}
	rp.Plan.inserting = true
	rp.Plan.Units = append(rp.Plan.Units, Unit{Key: g.ID, Stream: stream, Changes: []Change{change(Work, posGuard(g))},
		Moved: "sentinel " + g.ID + " still waiting"})
	for _, c := range pulled {
		rp.Plan.Units = append(rp.Plan.Units, Unit{Key: c.ID, Stream: stream,
			Changes: []Change{change(Work, posMove(c, Waiting))},
			Moved:   c.ID + " ready -> waiting behind sentinel " + g.ID})
	}
	rp.Notes = append(rp.Notes, NoteReq{Op: posKnow, Type: posNoticePulledBack, Subjects: []string{StreamSubject(stream)},
		Text: fmt.Sprintf("%d ready cards of %s went back to waiting behind %s", len(pulled), stream, g.ID)})
	return true
}
