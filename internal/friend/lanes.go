package friend

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
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
// for its life, and the cards set aside after CardTurns, so a restart does
// not hand them again.
type LaneState struct {
	Sessions map[int]string `json:"sessions"`
	GivenUp  []string       `json:"given_up,omitempty"`
}

// Card is one card a lane hands: its id (the queue file's), its brief, and
// the outbox directory its REPORT.md and RESULT.md go to.
type Card struct {
	ID, Brief, Outbox string
}

// Epoch is the sprint epoch the card was delivered at, its directory's <id>~<epoch>.
func (c Card) Epoch() string {
	_, epoch, _ := strings.Cut(filepath.Base(c.Outbox), "~")
	return epoch
}

// ProgressEvery is how often the daemon stamps progress on a card whose lane turn prints:
// the sprint's own number (internal/sprint ProgressEvery, inside the late rule's ten-minute
// window; docs/SPEC-SPRINT.md section 8, the rules table's row late).
const ProgressEvery = 3 * time.Minute

// ProgressArgv is the sprint server's verbs that stamp progress on the cards, one for the
// cards of each epoch, in the order of the epochs: `progress --as <friend> <card>... --epoch
// <n>`. Only the holder's stamp is taken: the server refuses one for a card she does not
// work.
func ProgressArgv(friend string, cards []Card) [][]string {
	byEpoch := map[string][]string{}
	for _, c := range cards {
		byEpoch[c.Epoch()] = append(byEpoch[c.Epoch()], c.ID)
	}
	var out [][]string
	for _, epoch := range slices.Sorted(maps.Keys(byEpoch)) {
		argv := append([]string{"progress", "--as", friend}, slices.Sorted(slices.Values(byEpoch[epoch]))...)
		out = append(out, append(argv, "--epoch", epoch))
	}
	return out
}

// Result is the card's RESULT.md, whose presence after a turn is the card done.
func (c Card) Result() string { return filepath.Join(c.Outbox, "RESULT.md") }

// cardDir is the card's directory under root (inbox or outbox): <id>~<epoch>,
// the highest epoch when friend sync delivered more than one.
func cardDir(root, id string) (string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	best, bestEpoch := "", -1
	for _, e := range entries {
		name, epoch, ok := strings.Cut(e.Name(), "~")
		n, err := strconv.Atoi(epoch)
		if !ok || name != id || !e.IsDir() || err != nil {
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
// (no outbox/<id>~<epoch>/RESULT.md) and not skipped (held by another lane,
// or set aside); found is false when there is none.
func NextCard(dir string, skip func(id string) bool) (c Card, found bool, err error) {
	var q Queue
	path := filepath.Join(dir, filepath.FromSlash(QueueFile))
	if _, err := read(path, &q); err != nil {
		if _, err2 := read(path, &q.Tasks); err2 != nil { // the file may be a bare list of tasks
			return Card{}, false, err
		}
	}
	for _, t := range q.Tasks {
		if (t.State != "queued" && t.State != "") || skip(t.ID) {
			continue
		}
		base, ok := cardDir(filepath.Join(dir, "inbox"), t.ID)
		if !ok {
			continue
		}
		c := Card{ID: t.ID, Brief: filepath.Join(dir, "inbox", base, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", base)}
		if !exists(c.Brief) || exists(c.Result()) {
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
	card     *Card
	attempts int
	t        *turn
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
// lanes beyond the width the row now gives marked retired.
func (s *laneSet) said(width int) string {
	var out []string
	for _, ln := range s.lanes {
		card := "-"
		if ln.card != nil {
			card = fmt.Sprintf("%s/%d", ln.card.ID, ln.attempts+1)
		}
		w := fmt.Sprintf("%d:%s:%s", ln.n, dash(ln.session), card)
		if ln.n > width {
			w += ":retired"
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
// along. A lane beyond width takes nothing new.
func (l *loop) laneStep(now time.Time, width int) {
	s := l.lanes
	d := l.d
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
	}
	for len(s.lanes) < width {
		n := len(s.lanes) + 1
		s.lanes = append(s.lanes, &lane{n: n, session: s.state.Sessions[n]})
	}
	lh := d.Deliver.(LaneHarness)
	held := func(id string) bool {
		if s.given[id] {
			return true
		}
		return slices.ContainsFunc(s.lanes, func(ln *lane) bool { return ln.card != nil && ln.card.ID == id })
	}
	for _, ln := range s.lanes {
		if ln.t != nil || ln.opening || ln.n > width {
			continue
		}
		if ln.session == "" {
			if now.Before(ln.openAt) {
				continue
			}
			ln.opening = true
			agents, memory := d.identity()
			seed := LaneSeed(d.Friend, ln.n, width, agents, memory)
			go func(ln *lane) {
				id, err := lh.OpenSession(l.ctx, seed)
				s.results <- laneResult{ln: ln, open: true, session: id, err: err}
			}(ln)
			continue
		}
		if ln.card == nil {
			c, found, err := NextCard(d.Dir, held)
			if err != nil {
				d.Record(now.UTC().Format(time.RFC3339) + " lanes: the queue file: " + err.Error())
				return
			}
			if !found {
				continue // messages wait: they ride only with a card
			}
			ln.card, ln.attempts = &c, 0
		}
		t := &turn{}
		if !l.reserve(t) {
			return
		}
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
			lt, err := lh.DeliverTo(ctx, ln.session, t.text)
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
	if exists(card.Result()) {
		line += " card=done"
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
	d.Record(line + fmt.Sprintf(" card=set_aside turn=%d/%d reason=%q", ln.attempts, CardTurns, why))
	s.given[card.ID] = true
	s.state.GivenUp = append(s.state.GivenUp, card.ID)
	l.saveLanes(now)
	ln.card, ln.attempts = nil, 0
	l.tell(fmt.Sprintf("friend %s: card %s not finished after %d turns (lane %d): %s", d.Friend, card.ID, CardTurns, ln.n, oneLine(why, 200)),
		fmt.Sprintf("Lane %d of %s handed card %s (%s) %d times and no RESULT.md appeared in %s. The last turn: %s. The lane has set the card aside and takes the next; hand it again by removing it from given_up in the lane state (lanes.json), or deal it elsewhere.\n", ln.n, d.Friend, card.ID, card.Brief, CardTurns, card.Outbox, why), now)
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
