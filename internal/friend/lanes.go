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

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
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
	// StopReturns is every card the machine's stop took out of a lane and the stop-return
	// owed or taken for it (stop.go): a daemon starting up sends what is owed first.
	StopReturns []StopReturn `json:"stop_returns,omitempty"`
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
	tier     string        // the card's tier as her row said it when the lane took it
	cap      time.Duration // the card's wall cap by its tier (lane_cap.go)
	t        *turn
	marked   time.Time  // when the lane last wrote its running mark on its card's job (one_lane.go)
	ended    string     // the lane that finished the card this lane still runs: its run is being stopped
	base     LaneTokens // the session's tokens when the card began: the card's are the rest
	baseOK   bool
	polled   time.Time // when the card's tokens were last read for the cap
	capped   bool      // stopped at the row's token cap
	job      LaneJob   // the card as the harness is handed it: every path absolute, the brief inline, the job directory
	// baseUsage is the session's usage when the card began, read from the harness's own
	// session record (LaneTokens, tokencap.go); baseUsageOK says the read succeeded. The
	// finish's tokens are the record at the end less this (finishNote), never the sqlite
	// database.
	baseUsage   LaneTokens
	baseUsageOK bool
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
	stopped bool // the machine's word is STOPPED (stopStep): said once each way
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
	marked  bool              // the hold in force is the pause marker's (a person lifts it)
	loaded1 bool              // lanes are held to the load width
	took    map[string]bool
	faults  FaultWatch // the row's harness faults: the third alike within ten minutes marks her down (lane_parity.go)
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

// CardText is one lane turn: the card and its three steps, every path absolute, the
// brief's text inline (a model that reads a path relative still has it), then what else
// rides along (the pong line first, the word about the coordinator, the bus messages
// waiting, each labelled by its sender's authority against seat).
func CardText(job LaneJob, n, width int, sendLine, pong, notice string, seat string, msgs []bus.Message) string {
	c := job.Card
	var b strings.Builder
	if pong != "" {
		b.WriteString("Run this now, first, exactly as written: " + pong + "\nThen read on.\n\n")
	}
	fmt.Fprintf(&b, "nova-friend: lane %d of %d: one card this turn, %s. Do exactly these three things, then stop.\n", n, width, c.ID)
	if job.Dir != "" {
		fmt.Fprintf(&b, "Your working directory is the card's job directory, %s. Every path here is absolute: use it exactly as written, leading slash and all.\n", job.Dir)
	}
	if job.Brief != "" {
		fmt.Fprintf(&b, "1. Do the card. Its brief is %s, and its whole text is below; work as it says, only where it says.\n", c.Brief)
	} else {
		fmt.Fprintf(&b, "1. Do the card. Its brief is %s; work as it says, only where it says.\n", c.Brief)
	}
	fmt.Fprintf(&b, "2. Write %s/REPORT.md and %s/RESULT.md as the brief's END step says.\n", c.Outbox, c.Outbox)
	fmt.Fprintf(&b, "3. Send one bus line: %s\n", sendLine)
	if job.Brief != "" {
		fmt.Fprintf(&b, "\nTHE BRIEF (%s):\n\n%s\nEND OF THE BRIEF\n", c.Brief, strings.TrimRight(job.Brief, "\n"))
	}
	if notice != "" {
		b.WriteString("\nnova-friend: " + notice + "\n")
	}
	if len(msgs) > 0 {
		b.WriteString("\nAlso for you, after the card:\n\n" + BatchFor(seat, msgs, "", ""))
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
	rules := d.laneRules()
	l.markerStep(now)
	limit, paused := min(s.gov.Cap(width), s.paced), s.gov.Paused(now)
	limit = l.loadLimit(rules, limit, now)
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
		if s.state.Started == nil {
			s.state.Started = map[string]Started{}
		}
		for i, r := range s.state.StopReturns {
			// a run the stop cancelled before this daemon started is gone with it: its
			// stop-return is owed first, never a FAIL (StopReturnsSurviveRestart); a taken
			// record is the past, and the card's later run under the same job name is a
			// started one like any other
			if !r.Owed() {
				continue
			}
			delete(s.state.Started, r.Job)
			if !r.Ended {
				s.state.StopReturns[i].Ended, s.state.StopReturns[i].Exit = true, -1
			}
		}
		d.owed.Store(int64(s.state.owed()))
		if len(s.state.Started) > 0 {
			l.endStarted(now)
		}
	}
	if l.stopStep(now) {
		paused = true // NoLaunchAfterStop: no open, no turn, no card taken while STOPPED
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
		if v, _ := l.judge(rules, id); v != LaneRun {
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
	l.takeBack(rules, now)
	for _, ln := range s.lanes {
		if ln.t != nil && ln.t.running {
			l.capStep(rules, ln, now)
		}
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
			if d.machineStopped() {
				continue // the word read right before the take, before any claim (NoLaunchAfterStop)
			}
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
			// every path the harness is handed absolute, the brief inline, run in the job
			// directory; a path that cannot be made absolute refuses the lane (lane_parity.go)
			job, err := LaneJobOf(d.Dir, c, filepath.Abs)
			if err == nil {
				var raw []byte
				if raw, err = os.ReadFile(job.Card.Brief); err == nil {
					job.Brief = string(raw)
				}
			}
			if err != nil {
				jobName := filepath.Base(c.Outbox)
				if why := "not started: " + err.Error(); s.refused[jobName] != why { // said once while it stands
					s.refused[jobName] = why
					d.Record(fmt.Sprintf("%s lane %d: card %s %s", now.UTC().Format(time.RFC3339), ln.n, c.ID, oneLine(why, 400)))
				}
				_ = os.Remove(laneMarkPath(d.Dir, jobName)) // ignored: the claim just made is this lane's; one left stands until stale
				continue
			}
			ln.card, ln.attempts, ln.marked, ln.job = &c, 0, now, job
			ln.tier = d.cardTier(c)
			ln.cap = d.laneCap(ln.tier)
			ln.capped = false
			ln.baseOK = false
			if base, err := l.tokens(ln.session); err == nil {
				ln.base, ln.baseOK = base, true
			}
			ln.baseUsageOK = false
			if d.SessionUsage != nil {
				if u, _, err := d.SessionUsage(ln.session); err == nil {
					ln.baseUsage, ln.baseUsageOK = u, true
				} else if errors.Is(err, errEmptySession) {
					// a session just opened has spent nothing: its baseline is zero,
					// never cardcost.None() (-1), which would overcount every class by one
					ln.baseUsage, ln.baseUsageOK = LaneTokens{}, true
				}
			}
			s.state.Started[filepath.Base(c.Outbox)] = Started{Lane: ln.n, Card: c, At: now}
			l.saveLanes(now)
		}
		if d.machineStopped() {
			// the word turned between the claim and the start: the card is given up, owed
			// its stop-return, never held with no turn (NoLaunchAfterStop)
			l.releaseHeld(ln, now)
			continue
		}
		if perCard { // the brief alone: no message, pong or notice rides with it
			t, c, dir := &turn{subjects: fmt.Sprintf("%q", "card "+ln.card.ID)}, ln.job.Card, ln.job.Dir
			ln.t = t
			l.startTurn(t, now, func(ctx context.Context) laneResult {
				lt, err := runner.RunCard(WithLaneDir(LaneContext(ctx), dir), c)
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
		t.text = CardText(ln.job, ln.n, width, send, pong, notice, l.seat(now), t.msgs)
		ln.t = t
		dir := ln.job.Dir
		l.startTurn(t, now, func(ctx context.Context) laneResult {
			lt, err := lh.DeliverTo(WithLaneDir(LaneContext(ctx), dir), ln.session, t.text)
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
	if t.byStop && ln.card != nil {
		l.stopDone(ln, t, r, now) // cancelled by the machine's stop: never finished
		return
	}
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
	if t.held && !exists(ln.card.Result()) && !exists(ln.card.Report()) {
		l.heldTurn(r, now) // a provider failure stopped it: kept for after the resume
		return
	}
	s.pace.Observe(r.turn.Windows)
	var rate RateLimited
	var funds OutOfFunds
	var usage UsageLimited
	limited := (errors.As(r.err, &rate) || errors.As(r.err, &funds) || errors.As(r.err, &usage)) && !t.stopped && !t.capped
	if limited && exists(ln.card.Result()) {
		r.err, limited = nil, false // the card is done: the words were the card's, not the provider's answer
	}
	if limited {
		l.limitedTurn(r, now)
		return
	}
	if !t.stopped && !t.capped {
		s.gov.Clean(now)
	}
	ok := r.err == nil && r.turn.Exit == 0 && !t.stopped && !t.capped
	line := fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s exit=%d", at, ln.n, ln.session, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), r.turn.Exit)
	if r.err != nil {
		line += fmt.Sprintf(" error=%q", r.err.Error())
	}
	if t.stopped {
		line += fmt.Sprintf(" stopped=%q", "no output for "+l.silentStop.String())
	}
	if t.capped {
		line += fmt.Sprintf(" capped=%q", ln.cap.String()+" tier "+dash(ln.tier))
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
	if !exists(card.Result()) && !exists(card.Report()) {
		// a report written under a relative spelling of the outbox, inside the job: moved home
		if moved := RescueStray(ln.job); len(moved) > 0 {
			line += fmt.Sprintf(" stray=%q", strings.Join(moved, ","))
		}
	}
	if exists(card.Result()) || exists(card.Report()) {
		end.NoReport = true
		line += " card=done " + l.endCard(ln.n, card, end, now)
		l.finishNote(ln, card, now.Sub(t.started), now)
		ln.card, ln.attempts = nil, 0
		d.Record(line)
		return
	}
	// the card's wall at or past its cap, the turn ended by the cap or by itself: the card
	// ends now, a HOLD naming the cap, never handed again in the lane (lane_cap.go)
	if wall := now.Sub(end.Started); ln.cap > 0 && !end.Started.IsZero() && (t.capped || wall >= ln.cap) {
		end.Capped, end.Tier, end.Overrun, end.Tail = ln.cap, ln.tier, wall-ln.cap, LastLines(t.tail.String(), CapTailLines)
		end.Cap = ""
		ln.attempts++
		job := filepath.Base(card.Outbox)
		d.Record(line + fmt.Sprintf(" card=capped turn=%d/%d reason=%q ", ln.attempts, CardTurns, CappedWords(end.Capped, end.Tier, end.Overrun)) + l.endCard(ln.n, card, end, now))
		s.given[job] = true
		s.state.GivenUp = append(s.state.GivenUp, job)
		l.saveLanes(now)
		ln.card, ln.attempts = nil, 0
		return
	}
	// exit 0 and no report: the harness's fault, never the worker's failed attempt; the card
	// stays in the lane's hand, its attempt not counted, and nothing goes to the sprint server
	if first, fault := l.harnessFault(r, t, card); fault {
		l.faultTurn(ln, line, HarnessFaultNoReport, first, now)
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
	l.finishNote(ln, card, now.Sub(t.started), now)
	job := filepath.Base(card.Outbox)
	s.given[job] = true
	s.state.GivenUp = append(s.state.GivenUp, job)
	l.saveLanes(now)
	ln.card, ln.attempts = nil, 0
	l.tell(fmt.Sprintf("friend %s: card %s not finished after %d turns (lane %d): %s", d.Friend, card.ID, CardTurns, ln.n, oneLine(why, 200)),
		fmt.Sprintf("Lane %d of %s handed card %s (%s) %d times and no RESULT.md appeared in %s. The last turn: %s. The lane has set the card aside, finished it failed in the sprint (its REPORT.md says how the run ended) and takes the next.\n", ln.n, d.Friend, card.ID, card.Brief, CardTurns, card.Outbox, why), now)
}

// harnessFault says a lane turn that ended on its own is a harness fault: it exited 0 with no
// refused permission and no error but its outbox's lack (NoReport), and its card has neither
// RESULT.md nor REPORT.md. first is the harness's first error line, from the turn's own word,
// else its output's tail.
func (l *loop) harnessFault(r laneResult, t *turn, card Card) (first string, fault bool) {
	var none NoReport
	if t.stopped || t.capped || r.turn.Exit != 0 || r.turn.Rejected != "" || (r.err != nil && !errors.As(r.err, &none)) {
		return "", false
	}
	if exists(card.Result()) || exists(card.Report()) {
		return "", false
	}
	first = r.turn.FirstError
	if first == "" {
		first = HarnessFirstError(t.tail.String())
	}
	return first, true
}

// faultTurn is a lane's turn that ended in a harness fault: said on the record with the
// harness's first error line, the card kept in the lane's hand with its attempt not counted
// and no finish sent, so it is not a reader's finding; the same fault FaultRepeats times
// within FaultWithin on her row marks her down until FaultDownFor later (her lanes held, her
// beat down with the reason), with one judgment to the seat, not one per card.
func (l *loop) faultTurn(ln *lane, line, fault, first string, now time.Time) {
	d, s := l.d, l.lanes
	reason := FaultWords(fault, first)
	d.Record(line + fmt.Sprintf(" card=kept turn=%d/%d reason=%q", ln.attempts, CardTurns, reason))
	until, down := s.faults.Observe(fault, now)
	if !down {
		return
	}
	s.gov.PauseUntil(until, reason)
	d.Record(fmt.Sprintf("%s harness fault: %d alike within %s: her row down until %s, her lanes held: %s", now.UTC().Format(time.RFC3339), FaultRepeats, FaultWithin, until.UTC().Format(time.RFC3339), oneLine(reason, 300)))
	if d.FaultDown != nil {
		d.FaultDown(until, reason)
	}
	subject, body := FaultDownText(d.Friend, reason, until)
	l.tellKind(bus.KindBlocker, subject, body, now)
}

// heldTurn is a lane's turn a provider failure stopped (stopEvery) before its card had a
// report: the card stays in the lane's hand, counted toward nothing, never set aside, and runs
// again once a person clears the pause; the messages it carried go back pending.
func (l *loop) heldTurn(r laneResult, now time.Time) {
	d, ln, t := l.d, r.ln, r.t
	for _, e := range t.entries {
		delete(l.inHand, e)
	}
	if t.notice != nil && l.notice == nil {
		l.notice = t.notice
		l.saidSilent = t.notice.Subject != "coordinator silent"
	}
	d.Record(fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s card=kept turn=%d/%d reason=%q", now.UTC().Format(time.RFC3339), ln.n, ln.session, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), ln.attempts, CardTurns, "a provider failure stopped every lane"))
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
	if t.notice != nil {
		l.owedAgain(t.notice, now)
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
	stop, stopped := d.laneRules().ProviderStop(err)
	switch {
	case stopped && errors.As(err, &rate):
		// the row says a rate limit holds her down too (row_pause_on=any): as out of funds does
		if s.gov.Hold(rate.Reason) {
			d.Record(fmt.Sprintf("%s provider failure: lanes held, the friend held down, nothing resumes until a person clears %s: %s", at, PauseFile, oneLine(rate.Reason, 200)))
			l.holdDown(stop, now)
			l.tellKind(bus.KindBlocker, fmt.Sprintf("friend %s: provider failure: %s", d.Friend, oneLine(rate.Reason, 120)), fmt.Sprintf("The row says a rate limit holds the lanes (pause_on=any). The provider said: %s. Every lane is stopped and the friend is held down; clear the pause with nova-friend resume --as %s, then bring her up.\n", rate.Reason, d.Friend), now)
		}
		return true
	case errors.As(err, &usage):
		if line := s.gov.PauseUntil(usage.Until, usage.Reason); line != "" {
			d.Record(at + " " + line)
		}
		return true
	case errors.As(err, &funds):
		if s.gov.Hold(funds.Reason) {
			l.holdDown(stop, now)
			subject, body := FundsJudgmentText(d.Friend, funds.Reason)
			if s.marked { // the pause marker holds them: a person's resume, not a restart, lifts it
				d.Record(fmt.Sprintf("%s provider failure: out of funds: lanes held, the friend held down, nothing resumes until a person clears %s, every card kept in hand: %s", at, PauseFile, oneLine(funds.Reason, 200)))
				body += fmt.Sprintf("Her lanes' pause marker (%s) holds them across a restart and her beat says her down with the provider's message: once paid, run nova-friend resume --as %s.\n", PauseFile, d.Friend)
			} else {
				d.Record(fmt.Sprintf("%s out of funds: lanes held until the daemon restarts, every card kept in hand: %s", at, oneLine(funds.Reason, 200)))
			}
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

// laneRules is her row's lane rules; none when the parity is not wired.
func (d *Daemon) laneRules() LaneRules {
	if d.Rules == nil {
		return LaneRules{}
	}
	return d.Rules()
}

// markerStep makes the pause marker the hold's truth: a marker the lanes find (a provider
// failure held them before this daemon started, or another hand wrote it) holds them and
// stops every lane running, and a marker a person cleared lifts the hold it made.
func (l *loop) markerStep(now time.Time) {
	d, s := l.d, l.lanes
	if d.LaneHold == nil {
		return
	}
	msg := d.LaneHold()
	switch {
	case msg != "" && s.gov.Held() == "":
		s.gov.Hold(msg)
		s.marked = true
		d.Record(fmt.Sprintf("%s provider failure: lanes held, nothing resumes until a person clears %s: %s", now.UTC().Format(time.RFC3339), PauseFile, oneLine(msg, 200)))
		l.stopEvery(now)
	case msg == "" && s.marked && s.gov.Held() != "":
		s.gov.Release()
		s.marked = false
		d.Record(now.UTC().Format(time.RFC3339) + " provider failure: the pause is cleared by a person; lanes resume")
	}
}

// holdDown stops every lane running and records the provider's exact message as the pause
// marker (LaneHoldDown), which her beat then carries as down (PauseBeat); the marker is the
// hold's truth only once it is written: a marker that cannot be written leaves the hold in
// this daemon alone, until it restarts, and says so.
func (l *loop) holdDown(message string, now time.Time) {
	d := l.d
	if d.Rules != nil { // the parity wired: a lane running on into the failure spends for nothing
		l.stopEvery(now)
	}
	if d.LaneHoldDown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
	defer cancel()
	if err := d.LaneHoldDown(ctx, message); err != nil {
		d.Record(fmt.Sprintf("%s provider failure: the pause marker cannot be written, so the friend is not beaten down and the lanes stay held until this daemon restarts: %s", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
		return
	}
	l.lanes.marked = true
}

// stopEvery ends every lane turn under way, as the runner's pause killed every lane: each
// turn's card stays in its lane's hand, counted toward nothing, and runs again when a person
// clears the pause (laneDone, heldTurn); one line per lane stopped.
func (l *loop) stopEvery(now time.Time) {
	for _, ln := range l.lanes.lanes {
		if ln.t == nil || !ln.t.running || ln.t.held || ln.card == nil {
			continue
		}
		ln.t.held = true
		if ln.t.cancel != nil {
			ln.t.cancel()
		}
		l.d.Record(fmt.Sprintf("%s lane %d: card %s stopped: a provider failure stops every lane; the card is kept and runs again after a person resumes", now.UTC().Format(time.RFC3339), ln.n, ln.card.ID))
	}
}

// loadLimit is the live cap held to the row's load width while the machine's load is above the
// row's bound, each change said once.
func (l *loop) loadLimit(r LaneRules, limit int, now time.Time) int {
	d, s := l.d, l.lanes
	if d.Load == nil {
		return limit
	}
	n, held := r.LaneWidthUnderLoad(limit, d.Load())
	switch {
	case held && !s.loaded1:
		s.loaded1 = true
		d.Record(fmt.Sprintf("%s load above %.0f: lanes held at %d of %d", now.UTC().Format(time.RFC3339), r.LoadMax, n, limit))
	case !held && s.loaded1:
		s.loaded1 = false
		d.Record(fmt.Sprintf("%s load at or below %.0f: lanes back to %d", now.UTC().Format(time.RFC3339), r.LoadMax, limit))
	}
	return n
}

// heldOf is the held card whose job is job.
func (d *Daemon) heldOf(job string) (HeldCard, bool) {
	for _, h := range d.heldCards {
		if h.Job == job {
			return h, true
		}
	}
	return HeldCard{}, false
}

// judge is the row's card filter on a job: the tier and stream come from her held cards when
// the server said them; a job the row does not hold is judged by its id alone.
func (l *loop) judge(r LaneRules, job string) (LaneVerdict, string) {
	h, ok := l.d.heldOf(job)
	if !ok {
		id, _, _, _ := ParseJob(job)
		if id == "" {
			id = job
		}
		r.Tiers = nil // no tier is known for a job the row does not hold
		return r.Judge(id, "", "")
	}
	return r.Judge(h.Card, h.Stream, h.Tier)
}

// takeBack asks the coordinator, once, to take back for the dealer each dealt card the row's
// tiers do not cover, when no lane has begun it and no job directory or report exists for it:
// a bus note naming the verb (TakeBackNote), since the sprint server serves no friend's take
// back. The lanes never run the card meanwhile (judge).
func (l *loop) takeBack(r LaneRules, now time.Time) {
	d, s := l.d, l.lanes
	if len(r.Tiers) == 0 {
		return
	}
	for _, h := range d.heldCards {
		if h.Col != "working" || s.took[h.Job] || s.given[h.Job] || !validJob(h.Job) {
			continue
		}
		if _, started := s.state.Started[h.Job]; started || exists(filepath.Join(d.Dir, "jobs", h.Job)) || exists(filepath.Join(d.Dir, "outbox", h.Job, "REPORT.md")) {
			continue
		}
		if slices.ContainsFunc(s.lanes, func(ln *lane) bool { return ln.card != nil && filepath.Base(ln.card.Outbox) == h.Job }) {
			continue
		}
		v, why := r.Judge(h.Card, h.Stream, h.Tier)
		if v != LaneTake {
			continue
		}
		if s.took == nil {
			s.took = map[string]bool{}
		}
		s.took[h.Job] = true
		subject, body := TakeBackNote(d.Friend, h.Card, h.Job, why)
		l.tellKind(bus.KindRequest, subject, body, now)
		d.Record(fmt.Sprintf("%s take %s back: asked the coordinator: %s", now.UTC().Format(time.RFC3339), h.Card, why))
	}
}

// TokenPollEvery is how often a running card's tokens are read for the cap.
const TokenPollEvery = 15 * time.Second

// tokens reads a session's tokens; the error is why they could not be read.
func (l *loop) tokens(session string) (LaneTokens, error) {
	d := l.d
	if d.Tokens == nil || session == "" {
		return LaneTokens{}, errors.New("the friend's tokens are not wired (no token source)")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
	defer cancel()
	t, err := d.Tokens(ctx, session)
	if err != nil {
		d.Record(l.d.Now().UTC().Format(time.RFC3339) + " tokens: " + oneLine(err.Error(), 300))
		return LaneTokens{}, err
	}
	return t, nil
}

// capStep stops a running card that has spent the row's token cap: a HOLD REPORT.md naming the
// cap, then the turn is stopped (its end finds the report and finishes the card).
func (l *loop) capStep(r LaneRules, ln *lane, now time.Time) {
	if r.TokenCap == 0 || ln.capped || ln.card == nil || !ln.baseOK || now.Sub(ln.polled) < TokenPollEvery {
		return
	}
	ln.polled = now
	cur, err := l.tokens(ln.session)
	// ignored: a token read that fails stops no cap check; the next poll reads again, and the finish prices the card unknown
	if err != nil {
		return
	}
	spent := cur.Sub(ln.base)
	if !r.OverTokenCap(spent.Total()) {
		return
	}
	ln.capped = true
	if !exists(ln.card.Report()) {
		if err := os.MkdirAll(ln.card.Outbox, 0o755); err == nil {
			// ignored: a report that cannot be written leaves the lane's end to write its own failed one
			_ = atomicfile.WriteFile(ln.card.Report(), []byte(TokenCapReport(l.d.Friend, r.TokenCap, spent.Total(), ln.attempts+1, "")), 0o644)
		}
	}
	l.d.Record(fmt.Sprintf("%s TOKEN CAP lane=%d card=%s: %d tokens of %d; lane stopped", now.UTC().Format(time.RFC3339), ln.n, ln.card.ID, spent.Total(), r.TokenCap))
	ln.t.cancel()
}

// finishNote publishes the card's cost on its REPORT.md and RESULT.md and sends the bus note
// to the coordinator, at each finish; off when the parity is not wired (Rules nil). A finish
// whose usage could not be read is unpriced, never $0.00, and one judgment tells the seat
// (usage unknown for <card>: <why>); a route found with no counted token is unpriced too
// (usage_opencode.go). The tokens come from the harness's own session record (SessionUsage),
// never from the model's report, where one is wired; else from the sqlite database (Tokens).
func (l *loop) finishNote(ln *lane, card Card, wall time.Duration, now time.Time) {
	d := l.d
	if d.Rules == nil {
		return
	}
	spent := LaneTokens{Tokens: cardcost.None()}
	model := d.Model
	unknown := ""
	switch {
	case d.SessionUsage != nil:
		if cur, m, err := d.SessionUsage(ln.session); err == nil && ln.baseUsageOK {
			spent = cur.Sub(ln.baseUsage)
			if m != "" {
				model = m
			}
		} else if err == nil {
			unknown = "session usage at card start was not read"
		} else {
			unknown = oneLine(err.Error(), 300)
		}
	case d.Tokens != nil:
		if cur, err := l.tokens(ln.session); err == nil && ln.baseOK {
			spent = cur.Sub(ln.base)
		} else if err != nil {
			unknown = oneLine(err.Error(), 300)
		}
	}
	var rp RoutePrice
	if d.Route != nil {
		rp = d.Route()
	}
	if d.SessionUsage != nil || d.Tokens != nil {
		if err := PublishFinishCost(card.Outbox, spent, rp, model, unknown); err != nil {
			d.Record(fmt.Sprintf("%s cost: %s: %s", now.UTC().Format(time.RFC3339), card.ID, oneLine(err.Error(), 300)))
		}
	}
	verdict := ""
	if raw, err := os.ReadFile(card.Report()); err == nil {
		verdict, _ = reportLine(string(raw), "Verdict")
	}
	cost := FinishCost(spent.Tokens, rp, model)
	if unknown != "" {
		cost = "unpriced (usage unknown: " + unknown + ")"
	}
	subject, body := FinishNote(d.Friend, filepath.Base(card.Outbox), verdict, cost, wall)
	l.tell(subject, body, now)
	if unknown != "" {
		s, b := UsageUnknownNote(d.Friend, card.ID, unknown)
		l.tell(s, b, now)
	}
}
