package member

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The merger is the mechanical caller of the sprint's merge step
// (docs/SPEC-SPRINT.md section 7: "The merge step is mechanical and is given
// its facts by the caller"; docs/SPEC-SWARM.md, `member --merger`). One runs
// per sprint store. Every pass, for each stream the merge table says is
// merging, one batch at a time: the head of the stream's queue in work order
// (never past a stuck card), each card's head merged into the stream branch
// sprint/<stream>.e<epoch>, the branch pushed and proved by the repository's
// checks, a green branch landed on the development branch by a fast-forward,
// and each outcome fed as one of the merge verb's facts: --batch n once the
// development branch holds the batch, --conflict <card>, --red with the batch
// as suspects, --rejected. It decides nothing the spec gives the coordinator: a
// stream that is not merging (stopped, waiting, landed) is left alone. The model
// is tla/Merger.tla.

// MergeGit is the merger's hands on a stream's repository: cmd/nova-swarm's
// git, a test's fake. Nothing it does rewrites a branch: no rebase, no force.
type MergeGit interface {
	// Landed says whether the development branch on origin holds every card's head.
	Landed(b Batch) (bool, error)
	// Build makes the stream branch for the batch: each card's head merged in
	// order onto the development branch (or onto origin's stream branch when it
	// holds a part of this batch, a batch rejected before). conflict is the card
	// whose head did not merge, "" when every one did; head is what was built.
	Build(b Batch) (head, conflict string, err error)
	// Push puts the built head on origin's stream branch, never forced.
	Push(b Batch, head string) error
	// Land pushes the head to origin's development branch, a fast-forward only:
	// rejected is git's line when origin refused it as none.
	Land(b Batch, head string) (rejected string, err error)
}

// The states of a head's checks (Checks.State).
const (
	CheckPending = "pending" // a check run has not concluded
	CheckGreen   = "green"   // every check run concluded, none failed
	CheckRed     = "red"     // a check run failed
	CheckNone    = "none"    // no check run on the head
)

// Checks is the proof of a pushed stream branch: the repository's CI on its head.
type Checks interface {
	// State is the checks on the head now, with a note: the runs that failed or
	// are pending, by name.
	State(repo, head string) (state, note string, err error)
}

// Batch is one stream's batch: the head n queued cards in work order, the
// repository and development branch their briefs name, and the stream branch.
type Batch struct {
	Stream string
	Epoch  uint64
	Repo   string // the clone URL (or a local path) the cards' briefs name
	Base   string // the development branch
	Branch string // the stream branch: sprint/<stream>.e<epoch>
	Cards  []BatchCard
}

// BatchCard is a card of a batch: the primary and the head its accepted
// attempt pushed, on the branch it was pushed to.
type BatchCard struct {
	ID, Head, Branch string
}

// IDs is the batch's cards, in order.
func (b Batch) IDs() []string {
	out := make([]string, len(b.Cards))
	for i, c := range b.Cards {
		out[i] = c.ID
	}
	return out
}

// StreamBranch is a stream's branch at an epoch: sprint/<stream>.e<epoch>.
func StreamBranch(stream string, epoch uint64) string {
	return "sprint/" + stream + ".e" + strconv.FormatUint(epoch, 10)
}

// MergerConfig is a merger's standing.
type MergerConfig struct {
	As       string        // the actor its facts are fed as
	Batch    int           // the most cards of a batch (default 1)
	Deadline time.Duration // the longest a pushed batch waits for its checks: past it, red
	Grace    time.Duration // how long a head with no check run waits before it is taken as proved by none
	// RepoOf is the repository and development branch a card's brief names (its
	// header's base-repo: or REPO:, and BASE:).
	RepoOf func(brief string) (repo, base string)
}

// pending is a stream's batch pushed and waiting for its checks.
type pending struct {
	b      Batch
	head   string
	pushed time.Time
}

// Merger is the merger loop's state: the batch each stream waits on.
type Merger struct {
	cfg     MergerConfig
	sprint  Sprint
	git     MergeGit
	checks  Checks
	out     io.Writer
	pending map[string]*pending // by stream: one batch at a time (tla/Merger.tla, OneBatchAtATime)
	said    map[string]string   // by stream: the last NOTE said, so a standing problem is said once
}

// NewMerger is a merger with no batch in flight.
func NewMerger(cfg MergerConfig, s Sprint, g MergeGit, c Checks, out io.Writer) *Merger {
	if cfg.Batch <= 0 {
		cfg.Batch = 1
	}
	return &Merger{cfg: cfg, sprint: s, git: g, checks: c, out: out, pending: map[string]*pending{}, said: map[string]string{}}
}

// Running is the batches pushed and waiting for their checks.
func (m *Merger) Running() int { return len(m.pending) }

// Drain is a no-op: a merger holds no child, and a batch it was waiting on is
// built again (and found pushed) by the next merger from origin.
func (m *Merger) Drain() {}

// mergeWhere is the part of `nova-sprint where --json` the merger reads: the
// epoch and the merge table's rows (a stream's state and its queued count).
type mergeWhere struct {
	Epoch  uint64                                  `json:"epoch"`
	Tables map[string]map[string]map[string]string `json:"tables"`
}

// mergeQueue is `nova-sprint queue --stream <s> --json`: the stream's merge
// queue, queued then stuck, each with its score.
type mergeQueue struct {
	Cards []struct {
		ID    string  `json:"id"`
		Col   string  `json:"col"`
		Score float64 `json:"score"`
	} `json:"cards"`
}

// mergeCard is the part of `nova-sprint card <id> --json` the merger reads: the
// primary's head and brief, and its attempts' work cards (the branch).
type mergeCard struct {
	Primary struct {
		Fields map[string]string `json:"Fields"`
	} `json:"primary"`
	Work []struct {
		Fields map[string]string `json:"Fields"`
	} `json:"work_cards"`
}

// Tick is one pass: read the merge table, then for each merging stream either
// prove the batch it waits on or start the next one. It returns the facts it
// fed and the first error that stopped it (a store that does not answer); a
// problem of the merger's own (a stage, a push, the checks) is said as a NOTE
// line and the stream is tried again next pass.
func (m *Merger) Tick(now time.Time) (acted int, err error) {
	code, out := m.sprint.Run("where", "--json")
	if code != 0 {
		return 0, fmt.Errorf("where: exit %d: %s", code, strings.TrimSpace(string(out)))
	}
	var w mergeWhere
	if err := json.Unmarshal(out, &w); err != nil {
		return 0, fmt.Errorf("where: not JSON: %w", err)
	}
	rows := w.Tables["merge"]
	streams := make([]string, 0, len(rows))
	for s := range rows {
		streams = append(streams, s)
	}
	sort.Strings(streams)
	for _, s := range streams {
		row := rows[s]
		if row["state"] != "merging" {
			// stopped, waiting or landed: the coordinator's, or nothing to do; a
			// batch it waited on is not landed (tla/Merger.tla: only a merging
			// stream starts a batch)
			delete(m.pending, s)
			continue
		}
		var n int
		var ferr error
		if p := m.pending[s]; p != nil {
			n, ferr = m.prove(s, w.Epoch, p, now)
		} else if q, _ := strconv.Atoi(strings.TrimSpace(row["queued"])); q > 0 {
			n, ferr = m.start(s, w.Epoch, now)
		}
		acted += n
		if ferr != nil {
			return acted, ferr
		}
	}
	return acted, nil
}

// note says a problem of the merger's own with a stream, once while it stands.
func (m *Merger) note(stream, what string) {
	if m.said[stream] == what {
		return
	}
	m.said[stream] = what
	fmt.Fprintf(m.out, "NOTE merge %s: %s\n", stream, oneLine(what))
}

// batchOf is the stream's batch: the first n queued cards by score (then id),
// none at or past the first stuck card (a stuck card is a barrier,
// docs/SPEC-SPRINT.md section 7), each with its head, branch and repository.
func (m *Merger) batchOf(stream string, epoch uint64) (Batch, string, error) {
	b := Batch{Stream: stream, Epoch: epoch, Branch: StreamBranch(stream, epoch)}
	ids, problem, err := m.queueHead(stream, m.cfg.Batch)
	if err != nil || problem != "" || len(ids) == 0 {
		return b, problem, err
	}
	for _, id := range ids {
		code, out := m.sprint.Run("card", id, "--json")
		if code == 2 {
			return b, "", fmt.Errorf("card %s: the store did not answer: %s", id, strings.TrimSpace(string(out)))
		}
		if code != 0 {
			return b, "card " + id + " refused: " + strings.TrimSpace(string(out)), nil
		}
		var c mergeCard
		if err := json.Unmarshal(out, &c); err != nil {
			return b, "", fmt.Errorf("card %s: not JSON: %w", id, err)
		}
		head, attempt := c.Primary.Fields["head"], c.Primary.Fields["attempt"]
		branch := ""
		for _, w := range c.Work {
			if w.Fields["attempt"] == attempt && w.Fields["head"] == head {
				branch = w.Fields["branch"]
			}
		}
		repo, base := "", ""
		if m.cfg.RepoOf != nil {
			repo, base = m.cfg.RepoOf(c.Primary.Fields["brief"])
		}
		switch {
		case len(head) != 40:
			return b, "card " + id + " has no full head to merge (head " + strconv.Quote(head) + ")", nil
		case branch == "":
			return b, "card " + id + "'s attempt " + attempt + " names no branch its head " + head + " was pushed to", nil
		case repo == "" || base == "":
			return b, "card " + id + "'s brief names no repository and base (base-repo: or REPO:, and BASE:)", nil
		case b.Repo != "" && (repo != b.Repo || base != b.Base):
			return b, "the batch's cards name two repositories or bases: " + b.Repo + " " + b.Base + " and " + repo + " " + base + " (card " + id + ")", nil
		}
		b.Repo, b.Base = repo, base
		b.Cards = append(b.Cards, BatchCard{ID: id, Head: head, Branch: branch})
	}
	return b, "", nil
}

// queueHead is the first n queued cards of the stream by score (then id), none
// at or past the first stuck card: the batch the merge step takes
// (docs/SPEC-SPRINT.md section 7).
func (m *Merger) queueHead(stream string, n int) (ids []string, problem string, err error) {
	code, out := m.sprint.Run("queue", "--stream", stream, "--json")
	if code == 2 {
		return nil, "", fmt.Errorf("queue --stream %s: the store did not answer: %s", stream, strings.TrimSpace(string(out)))
	}
	if code != 0 {
		return nil, "queue --stream refused: " + strings.TrimSpace(string(out)), nil
	}
	var q mergeQueue
	if err := json.Unmarshal(out, &q); err != nil {
		return nil, "", fmt.Errorf("queue --stream %s: not JSON: %w", stream, err)
	}
	cards := q.Cards
	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].Score != cards[j].Score {
			return cards[i].Score < cards[j].Score
		}
		return cards[i].ID < cards[j].ID
	})
	for _, c := range cards {
		if c.Col == "stuck" {
			break
		}
		if c.Col == "queued" && len(ids) < n {
			ids = append(ids, c.ID)
		}
	}
	return ids, "", nil
}

// start takes the stream's next batch: fed --batch at once when the development
// branch holds it already (a landing whose fact was not fed), else built (a
// conflict fed), pushed, and kept to be proved.
func (m *Merger) start(stream string, epoch uint64, now time.Time) (int, error) {
	b, problem, err := m.batchOf(stream, epoch)
	if err != nil {
		return 0, err
	}
	if problem != "" {
		m.note(stream, problem)
		return 0, nil
	}
	if len(b.Cards) == 0 {
		return 0, nil
	}
	if landed, err := m.git.Landed(b); err != nil {
		m.note(stream, "reading origin: "+err.Error())
		return 0, nil
	} else if landed {
		// the development branch holds every head: the fact is fed now (tla/Merger.tla, FactsMatchBranch)
		return m.feed(b, "the development branch "+b.Base+" holds the batch already")
	}
	head, conflict, err := m.git.Build(b)
	if err != nil {
		m.note(stream, "building "+b.Branch+": "+err.Error())
		return 0, nil
	}
	if conflict != "" {
		return m.feed(b, "", "--conflict", conflict, "--note", "the head of "+conflict+" did not merge into "+b.Branch)
	}
	if err := m.git.Push(b, head); err != nil {
		m.note(stream, "pushing "+b.Branch+": "+err.Error())
		return 0, nil
	}
	delete(m.said, stream)
	m.pending[stream] = &pending{b: b, head: head, pushed: now}
	fmt.Fprintf(m.out, "merge %s batch=%s branch=%s head=%s pushed\n", stream, strings.Join(b.IDs(), ","), b.Branch, head)
	return 0, nil
}

// prove reads the checks on the pushed batch: green lands it (a fast-forward of
// the development branch, --batch fed; not a fast-forward, --rejected), red
// feeds --red with the batch as suspects, and a batch past the deadline is red;
// a head with no check run within the grace is proved by none, said in the note.
func (m *Merger) prove(stream string, epoch uint64, p *pending, now time.Time) (int, error) {
	state, note, err := m.checks.State(p.b.Repo, p.head)
	if err != nil {
		m.note(stream, "reading the checks on "+p.head+": "+err.Error())
		return 0, nil
	}
	waited := now.Sub(p.pushed)
	switch {
	case state == CheckNone && waited >= m.cfg.Grace:
		state, note = CheckGreen, "no check run on "+p.head+" within "+m.cfg.Grace.String()+": proved by none"
	case (state == CheckPending || state == CheckNone) && waited > m.cfg.Deadline:
		state, note = CheckRed, "the checks did not conclude within "+m.cfg.Deadline.String()+": "+note
	}
	switch state {
	case CheckRed:
		delete(m.pending, stream)
		return m.feed(p.b, "", "--red", "--suspect", strings.Join(p.b.IDs(), ","), "--note", "checks red on "+p.b.Branch+" at "+p.head+": "+note)
	case CheckGreen:
		rejected, err := m.git.Land(p.b, p.head)
		if err != nil {
			m.note(stream, "landing "+p.head+" on "+p.b.Base+": "+err.Error())
			return 0, nil
		}
		delete(m.pending, stream)
		if rejected != "" {
			return m.feed(p.b, "", "--rejected", "--note", "the push of "+p.head+" to "+p.b.Base+" is not a fast-forward: "+rejected)
		}
		why := "landed " + p.b.Base + " at " + p.head
		if note != "" {
			why += "; " + note
		}
		return m.feed(p.b, why)
	}
	return 0, nil
}

// feed feeds one fact of the merge verb for the batch (fact empty: --batch n,
// the batch landed, with the note), at the epoch the batch was read at.
func (m *Merger) feed(b Batch, landed string, fact ...string) (int, error) {
	if len(fact) == 0 {
		// --batch n lands the head n of the queue: only when they are still this
		// batch (a card ranked first since is merged by a later pass, and this
		// batch's fact fed when its turn comes: tla/Merger.tla, FactsMatchBranch)
		ids, problem, err := m.queueHead(b.Stream, len(b.Cards))
		if err != nil {
			return 0, err
		}
		if problem != "" || strings.Join(ids, ",") != strings.Join(b.IDs(), ",") {
			m.note(b.Stream, "the batch "+strings.Join(b.IDs(), ",")+" is on "+b.Base+" and the queue's head is now "+strings.Join(ids, ",")+problem+"; its fact waits for its turn")
			return 0, nil
		}
	}
	args := []string{"merge", "--stream", b.Stream, "--batch", strconv.Itoa(len(b.Cards))}
	if len(fact) == 0 {
		args = append(args, "--note", landed)
	} else {
		args = append(args, fact...)
	}
	args = append(args, "--epoch", strconv.FormatUint(b.Epoch, 10))
	code, out := m.sprint.Run(args...)
	word := "batch"
	if len(fact) > 0 {
		word = strings.TrimPrefix(fact[0], "--")
	}
	fmt.Fprintf(m.out, "merge %s %s batch=%s exit=%d\n", b.Stream, word, strings.Join(b.IDs(), ","), code)
	if code == 2 {
		return 0, fmt.Errorf("merge --stream %s: the store did not answer: %s", b.Stream, strings.TrimSpace(string(out)))
	}
	if code != 0 {
		m.note(b.Stream, "merge --"+word+" refused: "+strings.TrimSpace(string(out)))
		return 0, nil
	}
	delete(m.said, b.Stream)
	return 1, nil
}
