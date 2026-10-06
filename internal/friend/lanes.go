package friend

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// The delivery modes (internal/config FriendModes, the friend row's mode).
const (
	ModeBatch   = "batch"
	ModeOneShot = "one-shot"
)

// CardTurns is how many turns a one-shot lane gives one card: a turn that
// ends without the card's RESULT.md hands the same card again once, then the
// card is reported to the coordinator and set aside.
const CardTurns = 2

// LaneOpenRetry is how long a lane whose session could not be opened waits
// before it tries again.
const LaneOpenRetry = time.Minute

// LaneState is what the lanes keep across restarts, in the state directory
// (lanes.json): each lane's session, so a lane is the same friend's session
// for its life, the cards set aside after CardTurns, so a restart does
// not hand them again, and the cards a lane has begun and not ended, so a
// restart finishes each (a lane's end, lane_end.go).
type LaneState struct {
	Sessions map[int]string     `json:"sessions"`
	GivenUp  []string           `json:"given_up,omitempty"`
	Started  map[string]Started `json:"started,omitempty"`
}

// Card is one card a lane hands: its id (the queue file's), its brief, and
// the outbox directory its REPORT.md and RESULT.md go to.
type Card struct {
	ID     string `json:"id"`
	Brief  string `json:"brief"`
	Outbox string `json:"outbox"`
}

// Epoch is the sprint epoch alone, without the job's generation (docs/FRIENDS.md).
func (c Card) Epoch() string {
	_, epoch, found := strings.Cut(filepath.Base(c.Outbox), "~")
	if !found {
		return "0"
	}
	epoch, _, _ = strings.Cut(epoch, ".g")
	return epoch
}

// Gen is the card's generation, its directory's .g<gen> (friend sync names a card dealt
// again to the same friend <id>~<epoch>.g<gen>); 1 when it has none.
func (c Card) Gen() int {
	base := filepath.Base(c.Outbox)
	if i := strings.LastIndex(base, ".g"); i >= 0 && strings.Contains(base[:i], "~") {
		if n, err := strconv.Atoi(base[i+2:]); err == nil && n > 1 {
			return n
		}
	}
	return 1
}

// ParseJob reads a friend's job directory name, <id>~<epoch> with .g<gen> after it from
// the card's second generation (nova-sprint friendJobOf); gen is 1 when it has none.
func ParseJob(job string) (id string, epoch, gen int, ok bool) {
	id, rest, found := strings.Cut(job, "~")
	if !found || id == "" {
		return "", 0, 0, false
	}
	gen = 1
	if e, g, dotted := strings.Cut(rest, ".g"); dotted {
		n, err := strconv.Atoi(g)
		if err != nil || n < 1 {
			return "", 0, 0, false
		}
		rest, gen = e, n
	}
	epoch, err := strconv.Atoi(rest)
	if err != nil || epoch < 0 {
		return "", 0, 0, false
	}
	return id, epoch, gen, true
}

// ProgressEvery is how often the daemon stamps progress on a card whose lane turn prints:
// the sprint's own number (internal/sprint ProgressEvery, inside the late rule's ten-minute
// window; docs/SPEC-SPRINT.md section 8, the rules table's row late).
const ProgressEvery = 3 * time.Minute

// ProgressArgv is the sprint server's verbs that stamp progress on the cards, one for the
// cards of each epoch, in the order of the epochs: `progress --as friend.<friend> <card>...
// --epoch <n>`, her row as the holder, as FinishArgv names it. Only the holder's stamp is
// taken: the server refuses one for a card she does not work, and one sent as her bare name
// (held by friend.<name>, not <name>).
func ProgressArgv(friend string, cards []Card) [][]string {
	byEpoch := map[string][]string{}
	for _, c := range cards {
		byEpoch[c.Epoch()] = append(byEpoch[c.Epoch()], c.ID)
	}
	var out [][]string
	for _, epoch := range slices.Sorted(maps.Keys(byEpoch)) {
		argv := append([]string{"progress", "--as", "friend." + friend}, slices.Sorted(slices.Values(byEpoch[epoch]))...)
		out = append(out, append(argv, "--epoch", epoch))
	}
	return out
}

// Result is the card's RESULT.md, whose presence after a turn is the card done.
func (c Card) Result() string { return filepath.Join(c.Outbox, "RESULT.md") }

// Report is the card's REPORT.md, the one friend sync finishes the card from.
func (c Card) Report() string { return filepath.Join(c.Outbox, "REPORT.md") }

// cardDir selects the exact generation and recorded job when present, otherwise
// its highest epoch (docs/FRIENDS.md, generation-specific jobs).
func cardDir(root string, task Task) (string, bool) {
	gen := task.Gen
	if gen == 0 {
		gen = 1
	}
	if gen < 1 {
		return "", false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	best, bestEpoch := "", -1
	for _, e := range entries {
		epoch, ok := strings.CutPrefix(e.Name(), task.ID)
		if !ok {
			continue
		}
		if strings.HasPrefix(epoch, "~") {
			epoch = strings.TrimPrefix(epoch, "~")
		} else if epoch == "" || strings.HasPrefix(epoch, ".g") {
			epoch = "0" + epoch
		} else {
			continue
		}
		epoch, generation, hasGeneration := strings.Cut(epoch, ".g")
		g := 1
		if hasGeneration {
			var err error
			g, err = strconv.Atoi(generation)
			if err != nil || g <= 1 {
				continue
			}
		}
		n, err := strconv.Atoi(epoch)
		if !e.IsDir() || err != nil || n < 0 || g != gen || (task.Job != "" && task.Job != e.Name()) {
			continue
		}
		if n > bestEpoch {
			best, bestEpoch = e.Name(), n
		}
	}
	return best, best != ""
}

// NextCard is the first card of dir's queue file (inbox/QUEUE.json, in its
// order) that is queued, delivered (inbox/<id>~<epoch>/BRIEF.md), not done
// (no outbox/<id>~<epoch>/RESULT.md, and no REPORT.md: a card with a report
// is friend sync's to finish) and not skipped (held by another lane, or set
// aside); found is false when there is none.
func NextCard(dir string, skip func(Card) bool) (c Card, found bool, err error) {
	var q Queue
	path := filepath.Join(dir, filepath.FromSlash(QueueFile))
	if _, err := read(path, &q); err != nil {
		if _, err2 := read(path, &q.Tasks); err2 != nil { // the file may be a bare list of tasks
			return Card{}, false, err
		}
	}
	for _, t := range q.Tasks {
		if t.State != "queued" && t.State != "" {
			continue
		}
		base, ok := cardDir(filepath.Join(dir, "inbox"), t)
		if !ok {
			continue
		}
		c := Card{ID: t.ID, Brief: filepath.Join(dir, "inbox", base, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", base)}
		if skip(c) || !exists(c.Brief) || exists(c.Result()) || exists(c.Report()) {
			continue
		}
		return c, true, nil
	}
	return Card{}, false, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// lane is one one-shot lane: its session, the card in its hand and how many
// turns that card has had, and the turn under way.
type lane struct {
	n        int // from 1
	session  string
	opening  bool
	openAt   time.Time // when an open that failed is tried again
	openFrom time.Time // when the open under way started
	card     *Card
	attempts int
	t        *turn
	marked   time.Time // when the lane last wrote its running mark on its card's job (one_lane.go)
	ended    string    // the lane that finished the card this lane still runs: its run is being stopped
}

type laneResult struct {
	ln      *lane
	open    bool
	session string
	turn    LaneTurn
	err     error
	t       *turn
}

// laneSet is the daemon's lanes in one-shot mode.
type laneSet struct {
	lanes   []*lane
	given   map[string]bool
	state   LaneState
	loaded  bool
	results chan laneResult
	refused map[string]string // each job a lane was refused, and who held it, said once while it stands
	gov     LaneGovernor      // the live cap under rate limits, the hold when out of funds (ratelimit.go)
	pace    Pacer             // the effective width under the subscription windows (pacing.go)
	paced   int               // the paced width at the last step
	width   int               // the row's width at the last step
	now     time.Time         // the last step's clock
}

func (s *laneSet) running() bool {
	for _, ln := range s.lanes {
		if ln.t != nil || ln.opening {
			return true
		}
	}
	return false
}

// said is the lanes as the status says them: n:session:card/attempts, the
// lanes beyond the width the row now gives marked retired, those beyond the
// live cap a rate limit lowered marked capped, those beyond the width the
// subscription windows allow marked paced, and every lane marked paused in a
// backoff, held when out of funds.
func (s *laneSet) said(width int) string {
	var out []string
	limit := s.gov.Cap(width)
	paced := min(s.paced, width)
	for _, ln := range s.lanes {
		card := "-"
		if ln.card != nil {
			card = fmt.Sprintf("%s/%d", ln.card.ID, ln.attempts+1)
		}
		w := fmt.Sprintf("%d:%s:%s", ln.n, dash(ln.session), card)
		switch {
		case ln.n > width:
			w += ":retired"
		case ln.n > limit:
			w += ":capped"
		case ln.n > paced:
			w += ":paced"
		case s.gov.Held() != "":
			w += ":held"
		case s.gov.Paused(s.now):
			w += ":paused"
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// identity is the friend's own files a lane's session is seeded from:
// AGENTS.md and memory/, in the working directory or in its <friend>/.
func (d *Daemon) identity() (agents, memory string) {
	for _, root := range []string{d.Dir, filepath.Join(d.Dir, d.Friend)} {
		if agents == "" && exists(filepath.Join(root, "AGENTS.md")) {
			agents = filepath.Join(root, "AGENTS.md")
		}
		if memory == "" && exists(filepath.Join(root, "memory")) {
			memory = filepath.Join(root, "memory")
		}
	}
	return agents, memory
}

// LaneSeed is the first turn of a lane's new session: who the friend is,
// from her own files, and what each later turn will be.
func LaneSeed(friend string, n, width int, agents, memory string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s: one of %d one-shot lanes of %s, this is lane %d, a session of your own.\n", friend, width, friend, n)
	switch {
	case agents != "" && memory != "":
		fmt.Fprintf(&b, "Read %s and every file under %s/ first: they are who you are.\n", agents, memory)
	case agents != "":
		fmt.Fprintf(&b, "Read %s first: it is who you are.\n", agents)
	}
	b.WriteString("From the next turn on, each turn hands you exactly one card: do it, write its REPORT.md and RESULT.md, send one bus line, and stop.\nAnswer this turn with the one word: ready.\n")
	return b.String()
}

// CardText is one lane turn: the card and its three steps, then what else
// rides along (the pong line first, the word about the coordinator, the bus
// messages waiting).
func CardText(c Card, n, width int, sendLine, pong, notice string, msgs []bus.Message) string {
	var b strings.Builder
	if pong != "" {
		b.WriteString("Run this now, first, exactly as written: " + pong + "\nThen read on.\n\n")
	}
	fmt.Fprintf(&b, "nova-friend: lane %d of %d: one card this turn, %s. Do exactly these three things, then stop.\n", n, width, c.ID)
	fmt.Fprintf(&b, "1. Do the card. Its brief is %s; work as it says, only where it says.\n", c.Brief)
	fmt.Fprintf(&b, "2. Write %s/REPORT.md and %s/RESULT.md as the brief's END step says.\n", c.Outbox, c.Outbox)
	fmt.Fprintf(&b, "3. Send one bus line: %s\n", sendLine)
	if notice != "" {
		b.WriteString("\nnova-friend: " + notice + "\n")
	}
	if len(msgs) > 0 {
		b.WriteString("\nAlso for you, after the card:\n\n" + Batch(msgs, "", ""))
	}
	return b.String()
}

// laneStep starts what the lanes owe: a session for a lane that has none,
// and a card's turn for a lane that is free, the waiting messages riding
// along. A lane beyond width, or beyond the live cap a rate limit lowered,
// takes nothing new and hands back a card it holds between turns; while the
// lanes back off from a rate limit, or are held out of funds, no lane starts
// a turn or an open (ratelimit.go).
func (l *loop) laneStep(now time.Time, width int) {
	s := l.lanes
	d := l.d
	s.width, s.now = width, now
	for _, line := range s.gov.Step(now, width) {
		d.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
	l.paceStep(now, width)
	limit, paused := min(s.gov.Cap(width), s.paced), s.gov.Paused(now)
	if !s.loaded {
		s.loaded = true
		s.given = map[string]bool{}
		if d.LoadLanes != nil {
			st, err := d.LoadLanes()
			if err != nil {
				d.Record(now.UTC().Format(time.RFC3339) + " lanes: the lane state cannot be read: " + err.Error() + "; every lane opens a new session")
			}
			s.state = st
		}
		if s.state.Sessions == nil {
			s.state.Sessions = map[int]string{}
		}
		for _, id := range s.state.GivenUp {
			s.given[id] = true
		}
		if len(s.state.Started) > 0 {
			l.endStarted(now)
		}
		if s.state.Started == nil {
			s.state.Started = map[string]Started{}
		}
	}
	for len(s.lanes) < width {
		n := len(s.lanes) + 1
		s.lanes = append(s.lanes, &lane{n: n, session: s.state.Sessions[n]})
	}
	l.oneLaneStep(now)
	lh, _ := d.Deliver.(LaneHarness)
	runner, perCard := d.Deliver.(CardRunner)
	asking := 0 // the lane the hand is asked for
	held := func(c Card) bool {
		id := filepath.Base(c.Outbox)
		legacy := id == c.ID || id == c.ID+"~"+c.Epoch()
		if s.given[id] || (legacy && s.given[c.ID]) {
			return true
		}
		if slices.ContainsFunc(s.lanes, func(ln *lane) bool { return ln.card != nil && ln.card.Outbox == c.Outbox }) {
			return true
		}
		if exists(c.Result()) || exists(c.Report()) {
			return true // done: no lane is owed it, and none is refused it
		}
		if who, how := l.laneHolder(c, now); who != "" { // one live lane per card
			l.refuseLane(asking, c, who, how, now)
			return true
		}
		return false
	}
	for _, ln := range s.lanes {
		if ln.t != nil || ln.opening {
			continue
		}
		if ln.n > limit {
			if ln.card != nil { // between turns: the card goes back to the queue for a lane within the cap
				d.Record(fmt.Sprintf("%s lane %d: beyond the cap (%d of %d); card %s handed back for another lane", now.UTC().Format(time.RFC3339), ln.n, limit, width, ln.card.ID))
				ln.card, ln.attempts = nil, 0
			}
			continue
		}
		if paused {
			continue
		}
		if ln.session == "" && !perCard {
			if now.Before(ln.openAt) {
				continue
			}
			ln.opening, ln.openFrom = true, now
			agents, memory := d.identity()
			seed := LaneSeed(d.Friend, ln.n, width, agents, memory)
			go func(ln *lane) {
				id, err := lh.OpenSession(LaneContext(l.ctx), seed)
				s.results <- laneResult{ln: ln, open: true, session: id, err: err}
			}(ln)
			continue
		}
		if ln.card == nil {
			asking = ln.n
			c, found, err := d.nextCard(held)
			if err != nil {
				d.Record(now.UTC().Format(time.RFC3339) + " lanes: the queue file: " + err.Error())
				return
			}
			if !found {
				continue // messages wait: they ride only with a card
			}
			// the card's job claimed before its first turn: a lane that claimed it first runs it alone
			holder, err := ClaimLane(d.Dir, filepath.Base(c.Outbox), l.laneWho(ln.n), now)
			if err != nil {
				d.Record(fmt.Sprintf("%s lane %d: card %s not started: its lane mark cannot be written: %s", now.UTC().Format(time.RFC3339), ln.n, c.ID, oneLine(err.Error(), 300)))
				continue
			}
			if holder != "" {
				l.refuseLane(ln.n, c, holder, "runs it", now)
				continue
			}
			delete(s.refused, filepath.Base(c.Outbox))
			ln.card, ln.attempts, ln.marked = &c, 0, now
			s.state.Started[filepath.Base(c.Outbox)] = Started{Lane: ln.n, Card: c, At: now}
			l.saveLanes(now)
		}
		if perCard { // the brief alone: no message, pong or notice rides with it
			t, c := &turn{subjects: fmt.Sprintf("%q", "card "+ln.card.ID)}, *ln.card
			ln.t = t
			l.startTurn(t, now, func(ctx context.Context) laneResult {
				lt, err := runner.RunCard(LaneContext(ctx), c)
				return laneResult{ln: ln, turn: lt, err: err, t: t}
			})
			continue
		}
		t := &turn{}
		t.entries, t.msgs = l.take()
		var subjects []string
		for _, m := range t.msgs {
			subjects = append(subjects, m.Subject)
		}
		t.subjects = fmt.Sprintf("%q", strings.Join(append([]string{"card " + ln.card.ID}, subjects...), " | "))
		notice, pong := l.head()
		t.notice = l.noticeTaken
		send := ""
		if d.CardDone != nil {
			send = d.CardDone(ln.card.ID, l.coordinator())
		}
		t.text = CardText(*ln.card, ln.n, width, send, pong, notice, t.msgs)
		ln.t = t
		l.startTurn(t, now, func(ctx context.Context) laneResult {
			lt, err := lh.DeliverTo(LaneContext(ctx), ln.session, t.text)
			return laneResult{ln: ln, turn: lt, err: err, t: t}
		})
	}
}

// laneDone is a lane's open or turn ending: a session kept, or a card done,
// handed again, or set aside and reported.
func (l *loop) laneDone(r laneResult, now time.Time) {
	d, s, ln := l.d, l.lanes, r.ln
	at := now.UTC().Format(time.RFC3339)
	if r.open {
		ln.opening = false
		if l.providerLimit(r.err, ln.openFrom, now) {
			ln.openAt = now // the governor's pause or hold says when it is tried again
			d.Record(fmt.Sprintf("%s lane %d: no session: %s; tried again when the lanes resume", at, ln.n, oneLine(r.err.Error(), 300)))
			return
		}
		if r.err != nil {
			ln.openAt = now.Add(LaneOpenRetry)
			d.Record(fmt.Sprintf("%s lane %d: no session: %s; tried again in %s", at, ln.n, oneLine(r.err.Error(), 300), LaneOpenRetry))
			return
		}
		ln.session = r.session
		s.state.Sessions[ln.n] = r.session
		l.saveLanes(now)
		d.Record(fmt.Sprintf("%s lane %d: session %s opened, seeded from the friend's own files", at, ln.n, r.session))
		return
	}
	t := r.t
	t.running = false
	ln.t = nil
	if ln.ended != "" {
		// another lane finished the card: its messages go back pending, counted toward nothing
		for _, e := range t.entries {
			delete(l.inHand, e)
		}
		if t.notice != nil && l.notice == nil {
			l.notice = t.notice
			l.saidSilent = t.notice.Subject != "coordinator silent"
		}
		d.Record(fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s card=ended reason=%q", at, ln.n, ln.session, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), "card finished by "+ln.ended))
		l.setDown(ln, now)
		return
	}
	s.pace.Observe(r.turn.Windows)
	var rate RateLimited
	var funds OutOfFunds
	var usage UsageLimited
	limited := (errors.As(r.err, &rate) || errors.As(r.err, &funds) || errors.As(r.err, &usage)) && !t.stopped
	if limited && exists(ln.card.Result()) {
		r.err, limited = nil, false // the card is done: the words were the card's, not the provider's answer
	}
	if limited {
		l.limitedTurn(r, now)
		return
	}
	if !t.stopped {
		s.gov.Clean(now)
	}
	ok := r.err == nil && r.turn.Exit == 0 && !t.stopped
	line := fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s exit=%d", at, ln.n, ln.session, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), r.turn.Exit)
	if r.err != nil {
		line += fmt.Sprintf(" error=%q", r.err.Error())
	}
	if t.stopped {
		line += fmt.Sprintf(" stopped=%q", "no output for "+l.silentStop.String())
	}
	if r.turn.Rejected != "" {
		line += fmt.Sprintf(" rejected=%q", r.turn.Rejected)
	}
	line += l.settle(t, ok, r.err, now)
	card := *ln.card
	end := LaneEnd{Exit: r.turn.Exit, Wall: now.Sub(t.started), Rejected: r.turn.Rejected, Turns: ln.attempts + 1, Started: s.state.Started[filepath.Base(card.Outbox)].At}
	if r.err != nil {
		end.Err = oneLine(r.err.Error(), 300)
	}
	if t.stopped {
		end.Cap = "no output for " + l.silentStop.String()
	}
	if l.ctx.Err() != nil {
		// the daemon is stopping: the card stays started, and the next daemon to start finishes it
		d.Record(line + " card=started reason=\"the daemon stopped\"")
		ln.card, ln.attempts = nil, 0
		return
	}
	if exists(card.Result()) || exists(card.Report()) {
		end.NoReport = true
		line += " card=done " + l.endCard(ln.n, card, end, now)
		ln.card, ln.attempts = nil, 0
		d.Record(line)
		return
	}
	ln.attempts++
	why := "the turn ended with no RESULT.md"
	switch {
	case t.stopped:
		why = "the turn was stopped: no output for " + l.silentStop.String()
	case r.turn.Rejected != "":
		why = "the harness refused a permission: " + r.turn.Rejected
	case r.err != nil:
		why = oneLine(r.err.Error(), 300)
	case r.turn.Exit != 0:
		why = fmt.Sprintf("the turn exited %d with no RESULT.md", r.turn.Exit)
	}
	if ln.attempts < CardTurns {
		d.Record(line + fmt.Sprintf(" card=again turn=%d/%d reason=%q", ln.attempts, CardTurns, why))
		return
	}
	d.Record(line + fmt.Sprintf(" card=set_aside turn=%d/%d reason=%q ", ln.attempts, CardTurns, why) + l.endCard(ln.n, card, end, now))
	job := filepath.Base(card.Outbox)
	s.given[job] = true
	s.state.GivenUp = append(s.state.GivenUp, job)
	l.saveLanes(now)
	ln.card, ln.attempts = nil, 0
	l.tell(fmt.Sprintf("friend %s: card %s not finished after %d turns (lane %d): %s", d.Friend, card.ID, CardTurns, ln.n, oneLine(why, 200)),
		fmt.Sprintf("Lane %d of %s handed card %s (%s) %d times and no RESULT.md appeared in %s. The last turn: %s. The lane has set the card aside, finished it failed in the sprint (its REPORT.md says how the run ended) and takes the next.\n", ln.n, d.Friend, card.ID, card.Brief, CardTurns, card.Outbox, why), now)
}

// limitedTurn is a lane's turn the provider rate-limited or refused out of
// funds: the card stays in the lane's hand, counted toward nothing, never
// set aside; the messages it carried go back pending, counted toward
// nothing, and the word about the coordinator is owed again; the governor
// backs off or holds (providerLimit).
func (l *loop) limitedTurn(r laneResult, now time.Time) {
	d, ln, t := l.d, r.ln, r.t
	line := fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s exit=%d", now.UTC().Format(time.RFC3339), ln.n, ln.session, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), r.turn.Exit)
	var funds OutOfFunds
	var usage UsageLimited
	if errors.As(r.err, &funds) {
		line += fmt.Sprintf(" out_of_funds=%q", funds.Reason)
	} else if errors.As(r.err, &usage) {
		line += fmt.Sprintf(" usage_limited=%q until=%s", usage.Reason, usage.Until.UTC().Format(time.RFC3339))
	} else {
		line += fmt.Sprintf(" rate_limited=%q", oneLine(r.err.Error(), 300))
	}
	for _, e := range t.entries {
		delete(l.inHand, e) // pending: the claim hands them in again
	}
	if t.notice != nil && l.notice == nil {
		l.notice = t.notice
		l.saidSilent = t.notice.Subject != "coordinator silent"
	}
	d.Record(line + fmt.Sprintf(" card=kept turn=%d/%d", ln.attempts, CardTurns))
	l.providerLimit(r.err, t.started, now)
}

// paceStep is the pacer at now and the row's width: the paced width kept
// for the step and the status, a change of it on the record, and the
// judgment to the coordinator once when the lanes are paced below half the
// row (pacing.go).
func (l *loop) paceStep(now time.Time, width int) {
	d, s := l.d, l.lanes
	pacing := DefaultPacing
	if d.Pacing != nil {
		pacing = PacingOf(d.Pacing())
	}
	paced, line, judge := s.pace.Step(now, width, pacing)
	s.paced = paced
	d.status.Paced, d.status.Window, d.status.Pacing = &paced, s.pace.Use(now), PacingText(pacing)
	if line != "" {
		d.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
	if judge {
		subject, body := PacingJudgmentText(d.Friend, paced, width, pacing, s.pace.Use(now))
		l.tellKind(bus.KindBlocker, subject, body, now)
	}
}

// providerLimit is the governor's answer to a rate limit or out of funds met
// by a turn or an open started at started: a rate limit pauses and lowers
// the cap, its line on the record, and RateJudgeAfter lowerings within
// RateJudgeWithin are one judgment to the coordinator; out of funds holds
// the lanes, said once with one judgment. It answers whether err was either.
func (l *loop) providerLimit(err error, started, now time.Time) bool {
	d, s := l.d, l.lanes
	at := now.UTC().Format(time.RFC3339)
	var rate RateLimited
	var funds OutOfFunds
	var usage UsageLimited
	switch {
	case errors.As(err, &usage):
		if line := s.gov.PauseUntil(usage.Until, usage.Reason); line != "" {
			d.Record(at + " " + line)
		}
		return true
	case errors.As(err, &funds):
		if s.gov.Hold(funds.Reason) {
			d.Record(fmt.Sprintf("%s out of funds: lanes held until the daemon restarts, every card kept in hand: %s", at, oneLine(funds.Reason, 200)))
			subject, body := FundsJudgmentText(d.Friend, funds.Reason)
			l.tellKind(bus.KindBlocker, subject, body, now)
		}
		return true
	case errors.As(err, &rate):
		line, judge := s.gov.RateLimit(now, started, s.width, rate.Reason)
		if line != "" {
			d.Record(at + " " + line)
		}
		if judge {
			subject, body := RateJudgmentText(d.Friend, s.gov.Cap(s.width), s.width, rate.Reason)
			l.tellKind(bus.KindBlocker, subject, body, now)
		}
		return true
	}
	return false
}

func (l *loop) saveLanes(now time.Time) {
	if l.d.SaveLanes == nil {
		return
	}
	if err := l.d.SaveLanes(l.lanes.state); err != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " lanes: the lane state cannot be written: " + err.Error())
	}
}

// ReadLanes is the lanes' state in stateDir; none is an empty state.
func ReadLanes(stateDir string) (LaneState, error) {
	var s LaneState
	_, err := read(filepath.Join(stateDir, LanesFile), &s) // a file not there is found=false and no error
	return s, err
}

// WriteLanes is the lanes' state written whole to stateDir.
func WriteLanes(stateDir string, s LaneState) error {
	return write(filepath.Join(stateDir, LanesFile), s)
}

// LanesFile is the lanes' state in the state directory.
const LanesFile = "lanes.json"

// ParseRow reads the friend's row off her beat's answer (nova-sprint friend
// beat prints row_mode=<mode> row_width=<n>, as friend sync last wrote her
// nova-config row); ok is false when the answer carries none.
func ParseRow(answer string) (mode string, width int, ok bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_mode="); found {
			mode, ok = v, true
		}
		if v, found := strings.CutPrefix(w, "row_width="); found {
			if n, err := strconv.Atoi(v); err == nil {
				width = n
			}
		}
	}
	return mode, width, ok
}

// laneKey marks a context as a lane's: what a lane's harness runs under it runs inside
// the lane's wall (Wall.Exec).
type laneKey struct{}

// LaneContext is ctx marked as a lane's.
func LaneContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, laneKey{}, true)
}

// InLane says whether ctx is a lane's.
func InLane(ctx context.Context) bool {
	in, _ := ctx.Value(laneKey{}).(bool)
	return in
}

// WallVerb is nova-friend's verb that runs one command inside a lane's wall:
// `nova-friend wall --profile <p> --dir <d> --deny <self>... [--config-dir <c>] [--job <j>]... [--read <r>]... -- <command> <args>`
// (docs/SPEC-FRIEND.md, buds-in-the-wall-r.w5).
const WallVerb = "wall"

// Wall is the wall every child of a lane runs inside: the wall profile its friend row
// names (sandbox.LaneProfile), over the friend's directories.
type Wall struct {
	Profile   string   // the row's profile; "" is sandbox.ProfileFriend
	Self      []string // the program that runs the wall verb, and its arguments before the verb: this nova-friend
	Dir       string   // the friend's working directory
	ConfigDir string   // her CLAUDE_CONFIG_DIR; "" none
	Jobs      []string // her job directories outside Dir
	Reads     []string // what her harness reads beyond the system roots and its own directory
	Deny      []string // the coordinator's self, never written inside the wall (sandbox.LaneProfile.Deny)
}

// Args is the wall verb and its flags, up to and with the "--" the command follows.
func (w Wall) Args() []string {
	profile := w.Profile
	if profile == "" {
		profile = sandbox.ProfileFriend
	}
	args := []string{WallVerb, "--profile", profile, "--dir", w.Dir}
	for _, d := range w.Deny {
		args = append(args, "--deny", d)
	}
	if w.ConfigDir != "" {
		args = append(args, "--config-dir", w.ConfigDir)
	}
	for _, j := range w.Jobs {
		args = append(args, "--job", j)
	}
	for _, r := range w.Reads {
		args = append(args, "--read", r)
	}
	return append(args, "--")
}

// Exec is run with every lane's child inside the wall: a command whose context is a
// lane's (LaneContext) runs as `<Self> wall <flags> -- <name> <args>`, in the same
// directory and with the same stdin; any other runs as it was. With no Self the lane's
// command is refused, never run unwalled.
func (w Wall) Exec(run Exec) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		if !InLane(ctx) {
			return run(ctx, dir, name, args, stdin)
		}
		if len(w.Self) == 0 {
			return "", 0, fmt.Errorf("a lane's %s cannot be walled: no program runs the wall, and a lane child never runs outside it", name)
		}
		argv := append(append(append([]string{}, w.Self[1:]...), w.Args()...), name)
		return run(ctx, dir, w.Self[0], append(argv, args...), stdin)
	}
}

// listFlag is a flag given any number of times.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// RunWall is the wall verb: args are its flags, "--", and the command; env is the
// environment the command gets inside the wall (its HOME is the one the deny list is
// under). It prints nothing on stdout but the command's own, so a harness's answer is
// read through it as it is; a refusal is one WALL REFUSED line per problem on stderr
// and sandbox.ExitRefused. The answer is the command's exit.
func RunWall(args, env []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(WallVerb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", sandbox.ProfileFriend, "the wall profile: "+strings.Join(sandbox.LaneProfiles, ", "))
	dir := fs.String("dir", "", "the friend's working directory")
	configDir := fs.String("config-dir", "", "the friend's CLAUDE_CONFIG_DIR, and the HOME inside the wall")
	var jobs, reads, deny listFlag
	fs.Var(&deny, "deny", "a path no write inside the wall reaches, the coordinator's self; ~/ is under HOME (repeatable, at least one)")
	fs.Var(&jobs, "job", "a job directory outside --dir, writable inside the wall (repeatable)")
	fs.Var(&reads, "read", "a directory the harness reads, beyond the system roots (repeatable)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return sandbox.ExitRefused
	}
	refused := func(reason, text string) int {
		fmt.Fprintf(stderr, "WALL REFUSED reason=%s %s\n", reason, oneLine(text, 400))
		return sandbox.ExitRefused
	}
	if fs.NArg() == 0 {
		return refused("no_command", "nothing after --; usage: nova-friend wall --profile <p> --dir <d> --deny <self>... [--config-dir <c>] [--job <j>]... [--read <r>]... -- <command> <args>")
	}
	home := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	cwd, _ := os.Getwd() // ignored: no cwd is the profile's working directory
	lp := sandbox.LaneProfile{Name: *profile, Work: *dir, Jobs: jobs, ConfigDir: *configDir, Reads: reads, Deny: deny, Home: home}
	in, err := lp.Input(cwd, fs.Args())
	if err != nil {
		return refused("bad_profile", err.Error())
	}
	p, bad := sandbox.Build(in)
	if len(bad) > 0 {
		code := sandbox.ExitRefused
		for _, r := range bad {
			refused(r.Reason, r.Text)
			code = max(code, r.Code())
		}
		return code
	}
	child := append(sandbox.ChildEnv(env, p.Tmp), "HOME="+p.Home)
	if *configDir != "" {
		child = append(child, "CLAUDE_CONFIG_DIR="+*configDir)
	}
	code, err := sandbox.Run(p, child, stdin, stdout, stderr, nil)
	if err != nil {
		reason := "sandbox_failed"
		if r, ok := err.(sandbox.Refusal); ok {
			reason = r.Reason
		}
		refused(reason, err.Error())
	}
	return code
}

// ParseProfile reads the friend's wall profile off her beat's answer (row_profile=<name>,
// beside row_mode and row_width); ok is false when the answer carries none.
func ParseProfile(answer string) (profile string, ok bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_profile="); found {
			profile, ok = v, true
		}
	}
	return profile, ok
}

// RowConfigDir reads the friend row's config_dir off her beat's answer
// (row_config_dir=<dir>, the directory her claude lanes run with as
// CLAUDE_CONFIG_DIR); empty when the answer carries none.
func RowConfigDir(answer string) string {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_config_dir="); found {
			return v
		}
	}
	return ""
}
