package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// THE DECIDE LANE (run --decide <dir>; docs/SPEC-SPRINT.md section 2, nova-decide's layer 2;
// the owner, 2026-10-02: "the nova-decide is both sides"). The server keeps the record of the
// sprint's attempt and grade decisions, <dir>/attempt.jsonl and <dir>/grade.jsonl, and is
// its one writer. The lane runs beside the tick, every DecideEvery, apart from the server's
// line of control but for two short holds (a read of the work table, a grade's write), so no
// backend's answer and no record write ever holds a tick or a worker's verb:
//
//   - the attempt decisions the finishes carried (cmdFinish hands each to put) are appended
//     to attempt.jsonl;
//   - every card waiting or ready that was never dealt and has no grade is graded through the
//     lane's backend (Jev, with the key JEV_API_KEY holds in the run loop's environment),
//     recorded in grade.jsonl, and written on the card (store.GradeStep);
//   - the outcome of every decision of a card that landed or was dropped is attached
//     (sprint.DecideDue), once;
//   - the coordinator's own judgment answers, each appended to judgment-answer.jsonl by the
//     verb that gave it (recordAnswer), have their outcome attached when their card lands,
//     is dropped or bounces again (answerOutcomes; SPEC-NOVA-DECIDE section 13);
//   - every routine judgment as it is raised is answered in shadow through the lane's backend,
//     recorded in judgment-shadow.jsonl and never applied (shadowRound; jev-shadow-judgments.w1);
//   - every card in review is read in shadow through the lane's backend over its brief and diff,
//     recorded in read-shadow.jsonl, never counted as a read, and given the readers' outcome when
//     its card lands or a later read finds it broken (shadowReadRound; jev-shadow-heavy-read.w1).

// DecideEvery is how often the decide lane runs a round.
const DecideEvery = 5 * time.Second

// GradeWidth is the most grade asks one round makes at once.
const GradeWidth = 8

// GradeWait is the default bound on one grade's answer (newDecideLane's wait).
const GradeWait = time.Minute

// decideLane is the lane's state: where the record is, the backend that grades (nil: no
// grading, no key), how long one grade's answer may take, the decisions the finishes handed
// it, and what it has done this process.
type decideLane struct {
	dir     string
	backend decide.Backend
	now     func() time.Time
	wait    time.Duration
	// diffOf is the diff of a card in review as the workers' head left it; nil reads nothing
	// in shadow (the run loop holds no checkout to take one from).
	diffOf func(ctx context.Context, c *sprint.Card) (string, error)

	mu     sync.Mutex
	queued []decide.Decision

	attached  map[string]bool // ops whose outcome is in the record, or that it cannot take
	shadowed  map[string]bool // open-judgment keys asked in shadow this process; a failed ask is asked again the next round
	graded    map[string]bool // cards graded this process; a card whose ask failed is asked again the next round
	watched   map[string]bool // primaries with a decision on them, read placed or not (a drop unplaces them)
	answering map[string]bool // cards with a judgment answer whose outcome is not attached: read placed or not
	readSeen  map[string]bool // card@head pairs shadow-read this process; a failed ask is asked again the next round
	readOpen  map[string]bool // cards with a shadow read whose outcome is not attached: read placed or not
	loaded    bool
	said      string // the last failure said, said once until it changes
}

// newDecideLane is the lane over dir, grading through b (nil grades nothing), each grade's
// answer bounded by wait (GradeWait in the run loop).
func newDecideLane(dir string, b decide.Backend, now func() time.Time, wait time.Duration) *decideLane {
	return &decideLane{dir: dir, backend: b, now: now, wait: wait, attached: map[string]bool{}, shadowed: map[string]bool{}, graded: map[string]bool{}, watched: map[string]bool{}, answering: map[string]bool{}, readSeen: map[string]bool{}, readOpen: map[string]bool{}}
}

func (l *decideLane) record(decision string) string {
	return filepath.Join(l.dir, decision+".jsonl")
}

// put queues an attempt decision a finish carried, for the next round to record.
func (l *decideLane) put(d decide.Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queued = append(l.queued, d)
}

// recordAnswer appends a judgment answer to judgment-answer.jsonl at once, by the verb that
// gave it (a record write is short and under the record's own lock; the answer's card is
// watched from now so its outcome is read even once a drop unplaces it).
func (l *decideLane) recordAnswer(d decide.Decision) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	if _, err := decide.Append(l.record(decide.JudgmentAnswerName), d); err != nil {
		return err
	}
	l.mu.Lock()
	l.answering[d.Inputs["card"]] = true
	l.mu.Unlock()
	return nil
}

// markOf is what an outcome reads of a card of the work table: nil is a card the sprint no
// longer holds (a clear), which has no outcome.
func markOf(c *sprint.Card) decide.CardMark {
	if c == nil {
		return decide.CardMark{}
	}
	return decide.CardMark{Placed: c.Placed(), Landed: c.Placed() && c.Col == sprint.Landed, Dropped: !c.Placed() && c.F("outcome") == "dropped",
		Broken: c.Int("broken_reads"), Failed: c.Int("failed")}
}

// answerOutcomes attaches to each judgment answer without one the outcome its card's state
// now says (decide.AnswerOutcome), once: how many it attached, and every record failure. The
// cards of the answers still without one are the ones the lane watches.
func (l *decideLane) answerOutcomes(s *sprint.Snapshot) (int, []string) {
	ds, err := decide.Load(l.record(decide.JudgmentAnswerName))
	if err != nil {
		return 0, []string{"the judgment answers: " + err.Error()}
	}
	n := 0
	var problems []string
	open := map[string]bool{}
	for _, d := range ds {
		if d.Outcome != nil {
			continue
		}
		card := d.Inputs["card"]
		label, note := decide.AnswerOutcome(d, markOf(s.Work.Card(card)))
		if label == "" {
			open[card] = true
			continue
		}
		_, changed, err := decide.Attach(l.record(decide.JudgmentAnswerName), decide.Outcome{ID: d.ID, Label: label, Note: card + ": " + note, At: l.now().UTC().Format(time.RFC3339)})
		if err != nil {
			problems = append(problems, fmt.Sprintf("the outcome of %s: %v", d.ID, err))
			open[card] = true
			continue
		}
		if changed {
			n++
		}
	}
	l.mu.Lock()
	l.answering = open
	l.mu.Unlock()
	return n, problems
}

// decideLoop runs decideRound every DecideEvery until ctx ends.
func (a *app) decideLoop(ctx context.Context, addr string, stdout io.Writer) {
	grading := "no grading: " + decide.JevSecret + " is absent from this environment"
	if a.decide.backend != nil {
		grading = "grading with " + a.decide.backend.Name()
	}
	fmt.Fprintf(stdout, "DECIDE every %s: attempt and grade decisions recorded under %s, outcomes attached on landing and drop; %s\n", DecideEvery, a.decide.dir, grading)
	for ctx.Err() == nil {
		a.decideRound(ctx, addr, stdout)
		a.sleep(DecideEvery)
	}
}

// decideRound is one round of the lane: the queued attempt decisions recorded, the cards to
// grade graded and their grades written, the outcomes due attached. It prints one DECIDE line
// for what it did, nothing for a round with nothing to do, and a failure once until it
// changes (the next round tries again).
func (a *app) decideRound(ctx context.Context, addr string, stdout io.Writer) {
	l := a.decide
	at := oneline.Field(a.now().Format("15:04:05"))
	recorded, problems := l.recordQueued()
	if !l.loaded {
		if err := l.load(); err != nil {
			problems = append(problems, err.Error())
		}
	}
	a.serial.Lock()
	s, err := a.decideSnapshot(ctx, addr)
	a.serial.Unlock()
	if err != nil {
		l.fail(stdout, at, "the work table could not be read: "+err.Error())
		return
	}
	grades, outcomes := sprint.DecideDue(s)
	shadowed, sp := a.shadowRound(ctx, addr)
	problems = append(problems, sp...)
	attached, ap := l.attach(outcomes)
	problems = append(problems, ap...)
	answered, np := l.answerOutcomes(s)
	attached += answered
	problems = append(problems, np...)
	read, rp := l.shadowReadRound(ctx, s)
	problems = append(problems, rp...)
	got, gp := l.grade(ctx, grades)
	problems = append(problems, gp...)
	written := 0
	if len(got) > 0 {
		a.serial.Lock()
		res, err := a.decideStore(ctx, addr, func(st *store.Store) (store.Result, error) {
			return st.Run(ctx, store.GradeStep(sprint.GradeReq{Grades: got, Who: sprint.MachineActor}))
		})
		a.serial.Unlock()
		if err != nil {
			problems = append(problems, "the grades could not be written: "+err.Error())
			for card := range got {
				delete(l.graded, card) // asked again next round, answered from the record
			}
		}
		written = len(res.Moved)
	}
	l.watch(s)
	if recorded+attached+len(got)+shadowed+read > 0 {
		fmt.Fprintf(stdout, "%s DECIDE recorded=%d graded=%d written=%d attached=%d", at, recorded, len(got), written, attached)
		if shadowed > 0 {
			fmt.Fprintf(stdout, " shadowed=%d", shadowed)
		}
		if read > 0 {
			fmt.Fprintf(stdout, " shadow_read=%d", read)
		}
		fmt.Fprintln(stdout)
	}
	if len(problems) > 0 {
		l.fail(stdout, at, strings.Join(problems, "; "))
		return
	}
	l.said = ""
}

// shadowRound asks the judgment decision, in shadow, of each open routine judgment not yet
// asked, at most GradeWidth a round, and records the answer in judgment-shadow.jsonl
// (decide.ShadowAsk): how many it recorded, and why each one that failed did. Nothing is
// applied and nothing is written on the work table. With no backend nothing is asked.
func (a *app) shadowRound(ctx context.Context, addr string) (int, []string) {
	l := a.decide
	if l.backend == nil {
		return 0, nil
	}
	a.serial.Lock()
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	var open []sprint.Open
	if err == nil {
		open, err = st.B.OpenNotes(ctx)
	}
	a.serial.Unlock()
	if err != nil {
		return 0, []string{"the open judgments could not be read: " + err.Error()}
	}
	judgments, _ := sprint.SplitOpen(open)
	n := 0
	var problems []string
	for _, o := range judgments {
		_, card, _ := strings.Cut(o.Key, "|")
		_, routine := decide.Kinds[o.Note.Type]
		if !routine || card == "" || strings.Contains(card, ":") || l.shadowed[o.Key] || n+len(problems) >= GradeWidth {
			continue
		}
		l.shadowed[o.Key] = true
		if decide.PaymentRefusal(o.Note.What) {
			continue // a payment is the owner's; the real decision is never asked for it either
		}
		allowed := decide.Verbs
		if v := verbsOf(o.Note.Decisions); len(v) > 0 {
			allowed = v
		}
		if err := os.MkdirAll(l.dir, 0o755); err != nil {
			delete(l.shadowed, o.Key)
			return n, append(problems, "the record's directory: "+err.Error())
		}
		actx, cancel := context.WithTimeout(ctx, l.wait)
		_, existing, err := decide.ShadowAsk(actx, l.backend, decide.JudgmentInput{Kind: o.Note.Type, Text: o.Note.What, Card: card,
			Cards: max(len(o.Note.Primaries), 1), Allowed: allowed}, o.Note.ID, l.record(decide.ShadowName), l.now())
		cancel()
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s not shadowed: %v", card, err))
			delete(l.shadowed, o.Key) // asked again on the next round
		case !existing:
			n++
		}
	}
	return n, problems
}

// shadowReadRound reads in shadow each card in review not yet read at its head (decide.ShadowReadAsk),
// at most GradeWidth a round, and attaches the readers' outcome (decide.ShadowReadOutcome) to each
// shadow read whose card has one: how many it recorded, and why each failure failed. A shadow read is
// never a read: nothing is applied or written on the work table. With no backend or no diff source
// nothing is asked; outcomes are still attached.
func (l *decideLane) shadowReadRound(ctx context.Context, s *sprint.Snapshot) (int, []string) {
	var problems []string
	n := 0
	if l.backend != nil && l.diffOf != nil {
		for _, c := range s.Work.Column(sprint.Review) {
			key := c.ID + "@" + c.F("head")
			if !c.Placed() || sprint.IsSentinel(c) || c.F("brief") == "" || l.readSeen[key] || n+len(problems) >= GradeWidth {
				continue
			}
			l.readSeen[key] = true
			diff, err := l.diffOf(ctx, c)
			if err == nil {
				err = os.MkdirAll(l.dir, 0o755)
			}
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s not shadow-read: %v", c.ID, err))
				delete(l.readSeen, key)
				continue
			}
			actx, cancel := context.WithTimeout(ctx, l.wait)
			_, existing, err := decide.ShadowReadAsk(actx, l.backend, c.ID, c.F("brief"), diff, c.Int("broken_reads"), l.record(decide.ShadowReadName), l.now())
			cancel()
			switch {
			case err != nil:
				problems = append(problems, fmt.Sprintf("%s not shadow-read: %v", c.ID, err))
				delete(l.readSeen, key) // asked again on the next round
			case !existing:
				n++
			}
		}
	}
	ds, err := decide.Load(l.record(decide.ShadowReadName))
	if err != nil {
		return n, append(problems, "the shadow reads: "+err.Error())
	}
	open := map[string]bool{}
	for _, d := range ds {
		if d.Outcome != nil {
			continue
		}
		card := d.Inputs["card"]
		label, note := decide.ShadowReadOutcome(d, markOf(s.Work.Card(card)))
		if label == "" {
			open[card] = true
			continue
		}
		if _, _, err := decide.Attach(l.record(decide.ShadowReadName), decide.Outcome{ID: d.ID, Label: label, Note: card + ": " + note, At: l.now().UTC().Format(time.RFC3339)}); err != nil {
			problems = append(problems, fmt.Sprintf("the outcome of %s: %v", d.ID, err))
			open[card] = true
		}
	}
	l.mu.Lock()
	l.readOpen = open
	l.mu.Unlock()
	return n, problems
}

// verbsOf is the verbs a judgment's printed decisions make, each once.
func verbsOf(decisions []string) []string {
	var out []string
	for _, d := range decisions {
		if v := decide.VerbOf(d); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// fail says a round's failure once until it changes.
func (l *decideLane) fail(stdout io.Writer, at, why string) {
	if why != l.said {
		fmt.Fprintf(stdout, "%s DECIDE FAILED %s; the next round tries again\n", at, oneline.Escape(why))
	}
	l.said = why
}

// decideStore runs fn on the lane's store, the machine's (the run loop's address).
func (a *app) decideStore(ctx context.Context, addr string, fn func(*store.Store) (store.Result, error)) (store.Result, error) {
	st, err := a.storeCtx(ctx, common{verb: "grade", redis: addr, actor: sprint.MachineActor})
	if err != nil {
		return store.Result{}, err
	}
	return fn(st)
}

// decideSnapshot is the work table as the lane reads it: every card placed, and every
// primary it watches placed or not (a dropped card is unplaced, and its outcome is due).
func (a *app) decideSnapshot(ctx context.Context, addr string) (*sprint.Snapshot, error) {
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	if err != nil {
		return nil, err
	}
	watching := maps.Clone(a.decide.watched)
	a.decide.mu.Lock()
	maps.Copy(watching, a.decide.answering)
	maps.Copy(watching, a.decide.readOpen)
	a.decide.mu.Unlock()
	watched := slices.Sorted(maps.Keys(watching))
	return st.Load(ctx, []string{sprint.Work}, func(*sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Work: watched}
	})
}

// recordQueued appends the queued attempt decisions to attempt.jsonl: how many it recorded,
// and every one the record refused (an op id recorded over another state).
func (l *decideLane) recordQueued() (int, []string) {
	l.mu.Lock()
	queued := l.queued
	l.queued = nil
	l.mu.Unlock()
	if len(queued) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		l.mu.Lock()
		l.queued = append(queued, l.queued...)
		l.mu.Unlock()
		return 0, []string{"the record's directory: " + err.Error()}
	}
	n := 0
	var problems []string
	for _, d := range queued {
		have, err := decide.Append(l.record(decide.AttemptName), d)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("attempt %s not recorded: %v", d.ID, err))
		case have == nil:
			n++
		}
	}
	return n, problems
}

// load reads which decisions the records hold an outcome for: they are attached already.
func (l *decideLane) load() error {
	for _, name := range []string{decide.AttemptName, decide.GradeName} {
		ds, err := decide.Load(l.record(name))
		if err != nil {
			return err
		}
		for _, d := range ds {
			if d.Outcome != nil {
				l.attached[d.ID] = true
			}
		}
	}
	answers, err := decide.Load(l.record(decide.JudgmentAnswerName))
	if err != nil {
		return err
	}
	for _, d := range answers {
		if d.Outcome == nil {
			l.mu.Lock()
			l.answering[d.Inputs["card"]] = true
			l.mu.Unlock()
		}
	}
	l.loaded = true
	return nil
}

// attach attaches each outcome due whose decision has none yet: how many it attached, and
// every one the record refused but for a decision it does not hold (the lane began after it,
// or a finish's decision was not recorded), which it attaches nothing to and stops asking.
func (l *decideLane) attach(outcomes []sprint.DecideOutcome) (int, []string) {
	n := 0
	var problems []string
	for _, o := range outcomes {
		if l.attached[o.Op] {
			continue
		}
		_, changed, err := decide.Attach(l.record(o.Decision), decide.Outcome{ID: o.Op, Label: o.Label, Note: o.Note, At: l.now().UTC().Format(time.RFC3339)})
		var conflict *decide.ConflictError
		switch {
		case errors.Is(err, decide.ErrUnknown), errors.As(err, &conflict), errors.Is(err, os.ErrNotExist):
			// a decision the record does not hold: nothing attached, and never asked again
		case err != nil:
			problems = append(problems, fmt.Sprintf("the outcome of %s: %v", o.Op, err))
			continue
		}
		l.attached[o.Op] = true
		if changed {
			n++
		}
	}
	return n, problems
}

// grade asks the grade of each card to grade, GradeWidth at a time, each bounded by the
// lane's wait, recording each in grade.jsonl (decide.Make: a brief graded before is answered
// from the record): the grades made by card, and why each card that was not graded was not.
// A card graded is not asked again this process; a card whose ask failed is asked again on
// the next round, at most once a round; with no backend nothing is asked.
func (l *decideLane) grade(ctx context.Context, asks []sprint.GradeAsk) (map[string]decide.Decided, []string) {
	var todo []sprint.GradeAsk
	for _, g := range asks {
		if l.backend != nil && !l.graded[g.Card] {
			todo = append(todo, g)
			l.graded[g.Card] = true
		}
	}
	if len(todo) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return nil, []string{"the record's directory: " + err.Error()}
	}
	var mu sync.Mutex
	got := map[string]decide.Decided{}
	var problems []string
	gate := make(chan struct{}, GradeWidth)
	var wg sync.WaitGroup
	for _, g := range todo {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-gate }()
			dec, err := l.gradeOne(ctx, g)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s not graded: %v", g.Card, err))
				delete(l.graded, g.Card) // asked again on the next round
				return
			}
			got[g.Card] = dec
		}()
	}
	wg.Wait()
	slices.Sort(problems)
	return got, problems
}

// gradeOne is one card's grade decision, made and recorded (decide.Make).
func (l *decideLane) gradeOne(ctx context.Context, g sprint.GradeAsk) (decide.Decided, error) {
	ctx, cancel := context.WithTimeout(ctx, l.wait)
	defer cancel()
	state := decide.GradeState(g.Brief)
	d, _, err := decide.Make(ctx, l.backend, decide.GradeSchema(), state, l.record(decide.GradeName), decide.GradeOp(g.Card, state),
		map[string]string{"card": g.Card, "brief_sha256": decide.Sum([]byte(g.Brief))}, l.now())
	if err != nil {
		return decide.Decided{}, err
	}
	value, p := decide.ChoiceOf(d, decide.GradeQuestion)
	return decide.Decided{Value: value, P: p, Op: d.ID}, nil
}

// watch keeps every primary with a decision on it, so a round after its drop reads it
// unplaced (decideSnapshot); a primary whose outcomes are all attached is let go.
func (l *decideLane) watch(s *sprint.Snapshot) {
	for _, c := range s.Work.Cards() {
		open := false
		for k := range c.Fields {
			if op, ok := strings.CutPrefix(k, sprint.PrefixDecided); ok && !l.attached[op] {
				open = true
			}
		}
		if g, ok := decide.ParseDecided(c.F(sprint.FieldGrade)); ok && !l.attached[g.Op] {
			open = true
		}
		l.mu.Lock()
		open = open || l.answering[c.ID]
		l.mu.Unlock()
		if open {
			l.watched[c.ID] = true
		} else {
			delete(l.watched, c.ID)
		}
	}
}
