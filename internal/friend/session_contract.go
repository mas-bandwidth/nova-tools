package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// The session contract (docs/SPEC-FRIEND.md, the session contract). The finding of
// 2026-10-07: a session friend's daemon was reinstalled and began waking her through a new
// wake file; it said "the session runs no monitor over <file>" only in its own log, so her
// presence fell, the bus refused her as deaf, and the dashboard listed a working friend down.
// The same day the sprint took hundreds of her cards back as "not started", because a card
// counts as started only once it is stamped with `nova-sprint progress`, and no session had
// ever been told so. Now the daemon tells the session everything the machine expects of it:
// the wake file and the monitor line, the pong line, the start stamp, the finish form. It is
// carried by every session check until the session answers one, pushed as one message titled
// "your contract" on the daemon's start and on a reinstall, and again whenever the wake path,
// the server or the epoch changes (the first epoch included), written to <state>/CONTRACT.md,
// and printed by `nova-friend contract --as <me>`. A check deferred because the session runs
// no monitor is pushed as a message that still carries that contract.

// ContractTitle heads the contract's message: the subject it is pushed under and its first line.
const ContractTitle = "your contract"

// ContractFile is the contract as the daemon last told it, in its state directory.
const ContractFile = "CONTRACT.md"

// NoMonitorChecks is how many session checks in a row may be deferred because the session
// runs no monitor over its wake file before the daemon says the friend down with that reason.
const NoMonitorChecks = 3

// StartRetry is how long a start stamp the sprint server did not take waits before it is sent again.
const StartRetry = time.Minute

// Contract is what the machine expects of one session friend. Wake is the wake file the
// session's monitor tails ("" when the harness has none, or the session chooses it), Monitor
// the exact line the session runs ("" when deliveries are turns the daemon runs), Pong the
// line that answers a nonce (with <nonce> in it), Server the sprint server, Epoch the sprint
// epoch of the cards on her row ("" until the server has said one), Reports her outbox.
type Contract struct {
	Friend, Harness string
	Wake, Monitor   string
	Pong            string
	Server, Epoch   string
	Reports         string
}

// Key is what a change of retells the contract: the wake path, the server and the epoch.
func (c Contract) Key() string { return c.Wake + "\n" + c.Server + "\n" + c.Epoch }

// StartStamp is the line that stamps a card started: the sprint counts a card of hers as
// working only once it is stamped, and takes back one past its bound that is not.
func StartStamp(friend, server, card string, gen int, epoch string) string {
	if card == "" {
		card = "<card>"
	}
	g := "<gen>"
	if gen > 0 {
		g = strconv.Itoa(gen)
	}
	if epoch == "" {
		epoch = "<n>"
	}
	line := fmt.Sprintf("nova-sprint progress --as friend.%s %s@%s --epoch %s", friend, card, g, epoch)
	if server != "" {
		line = "NOVA_SPRINT_SERVER=" + server + " " + line
	}
	return line
}

// StartArgv is the progress verb that stamps one held card started, as the daemon sends it:
// <card>@<gen> when the server named the generation, else the bare card (the verb takes both).
func StartArgv(friend string, h HeldCard) []string {
	card := h.Card
	if h.Gen > 0 {
		card += "@" + strconv.Itoa(h.Gen)
	}
	return []string{"progress", "--as", "friend." + friend, card, "--epoch", strconv.FormatUint(h.Epoch, 10)}
}

// The contract's lines, each a key the daemon reads back from CONTRACT.md (ContractKeyOf).
const (
	contractWake   = "Wake file: "
	contractServer = "Server: "
	contractEpoch  = "Epoch: "
)

// Text is the contract as the session reads it: one message, its first line ContractTitle.
func (c Contract) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (nova-friend, %s): what the machine expects of this session. Keep it; it is told again whenever it changes.\n", ContractTitle, c.Friend)
	wake, monitor := c.Wake, c.Monitor
	if wake == "" {
		wake = "none"
	}
	if monitor == "" {
		monitor = "none: every delivery is a turn the daemon runs into this session by " + c.Harness + "'s deliver command"
	}
	b.WriteString(contractWake + wake + "\n")
	b.WriteString("Monitor: " + monitor + "\n")
	b.WriteString("Pong: answer a SESSION CHECK <nonce> at once, with this one line and nothing to fill in but the nonce: " + c.Pong + "\n")
	b.WriteString("Start: when you or a child begin a card, stamp it started before anything else: " + StartStamp(c.Friend, c.Server, "", 0, c.Epoch) +
		"; a card not stamped is counted not started and is taken back past its bound\n")
	reports := c.Reports
	if reports == "" {
		reports = "outbox"
	}
	b.WriteString("Finish: write " + filepath.Join(reports, "<job>", "REPORT.md") + ": first line exactly `Verdict: LAND|HOLD|FAIL`, second line exactly `Head: <40-hex>` (the sha you pushed; blank for HOLD and FAIL)\n")
	server, epoch := c.Server, c.Epoch
	if server == "" {
		server = "none"
	}
	if epoch == "" {
		epoch = "not yet said by the server"
	}
	b.WriteString(contractServer + server + "\n")
	b.WriteString(contractEpoch + epoch + "\n")
	b.WriteString("Read it again: nova-friend contract --as " + c.Friend + "\n")
	return b.String()
}

// ContractKeyOf is the Key of a contract's text as Text wrote it ("" when it carries none):
// what the last run told, read back at the start to say a reinstall that changed it.
func ContractKeyOf(text string) string {
	var wake, server, epoch string
	found := false
	for line := range strings.SplitSeq(text, "\n") {
		switch {
		case strings.HasPrefix(line, contractWake):
			wake, found = strings.TrimPrefix(line, contractWake), true
		case strings.HasPrefix(line, contractServer):
			server = strings.TrimPrefix(line, contractServer)
		case strings.HasPrefix(line, contractEpoch):
			epoch = strings.TrimPrefix(line, contractEpoch)
		}
	}
	if !found {
		return ""
	}
	if wake == "none" {
		wake = ""
	}
	if server == "none" {
		server = ""
	}
	if epoch == "not yet said by the server" {
		epoch = ""
	}
	return Contract{Wake: wake, Server: server, Epoch: epoch}.Key()
}

// ContractPath is the contract's file in the state directory.
func ContractPath(stateDir string) string { return filepath.Join(stateDir, ContractFile) }

// WriteContract writes the contract's file, whole.
func WriteContract(stateDir, text string) error {
	return atomicfile.WriteFile(ContractPath(stateDir), []byte(text), 0o644)
}

// ReadContract is the contract's file; found is false when no daemon wrote one.
func ReadContract(stateDir string) (text string, found bool, err error) {
	raw, err := os.ReadFile(ContractPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

// changed says what differs between two contracts' keys, as the record says it.
func changed(old, now Contract) string {
	var parts []string
	if old.Wake != now.Wake {
		parts = append(parts, fmt.Sprintf("the wake path (%s -> %s)", dash(old.Wake), dash(now.Wake)))
	}
	if old.Server != now.Server {
		parts = append(parts, fmt.Sprintf("the server (%s -> %s)", dash(old.Server), dash(now.Server)))
	}
	if old.Epoch != now.Epoch {
		parts = append(parts, fmt.Sprintf("the epoch (%s -> %s)", dash(old.Epoch), dash(now.Epoch)))
	}
	return strings.Join(parts, ", ")
}

// ContractTeller tells the session its contract. Start is the daemon's start (a restart, a
// reinstall): it pushes one message, subject ContractTitle, body Contract.Text, on Push (the
// daemon's own delivery path, the one a card's deal takes), and every session check carries it
// (CheckText) until the session answers one (Answered). Update is the contract as it stands at
// a step: a change of the wake path, the server or the epoch, the epoch first said by the
// server among them, pushes it the same way and owes it to the checks again. Every change is
// written (Write: CONTRACT.md) and said on the record.
type ContractTeller struct {
	Push   func(ctx context.Context, subject, body string) error
	Write  func(text string) error
	Record func(line string)
	Now    func() time.Time

	mu      sync.Mutex
	c       Contract
	started bool
	owed    bool
}

// Start is the daemon's start with contract c; prior is CONTRACT.md as the last run wrote it
// ("" when there is none): one that differs is a reinstall, said with what changed. Start and
// a reinstall each push one message, subject ContractTitle, body the contract's text. pushed
// is false when there is no push path or the push fails; the checks still carry it.
func (t *ContractTeller) Start(ctx context.Context, c Contract, prior string) (pushed bool) {
	why := "start"
	if pk := ContractKeyOf(prior); pk != "" && pk != c.Key() {
		parts := strings.SplitN(pk, "\n", 3)
		why = "reinstall: " + changed(Contract{Wake: parts[0], Server: parts[1], Epoch: parts[2]}, c) + " since the last run"
	}
	t.mu.Lock()
	t.c, t.started, t.owed = c, true, true
	t.mu.Unlock()
	t.write(c)
	line := "contract: " + why + ": owed to the session; every session check carries it until the session answers one, and " + ContractFile + " holds it"
	switch {
	case t.Push == nil:
		line += "; no push path"
	default:
		if err := t.Push(ctx, ContractTitle, c.Text()); err != nil {
			line += "; the push failed: " + oneLine(err.Error(), 300)
		} else {
			pushed = true
			line += "; pushed into the session as a message titled \"" + ContractTitle + "\""
		}
	}
	t.record(line)
	return pushed
}

// Update is the contract c at a step; pushed says it went to the session as a message.
func (t *ContractTeller) Update(ctx context.Context, c Contract) (pushed bool) {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return t.Start(ctx, c, "")
	}
	old := t.c
	if old.Key() == c.Key() {
		t.c = c
		t.mu.Unlock()
		return false
	}
	t.c = c
	t.owed = true
	t.mu.Unlock()
	t.write(c)
	why := changed(old, c)
	if t.Push == nil {
		t.record("contract: " + why + " changed; no push path: the next session check carries it")
		return false
	}
	if err := t.Push(ctx, ContractTitle, c.Text()); err != nil {
		t.record("contract: " + why + " changed; the push failed: " + oneLine(err.Error(), 300) + "; the next session check carries it")
		return false
	}
	t.record("contract: " + why + " changed; pushed into the session as a message, and the next session check carries it")
	return true
}

// CheckText is a session check's text with the contract after it while it is owed.
func (t *ContractTeller) CheckText(check string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.owed {
		return check
	}
	return strings.TrimRight(check, "\n") + "\n\n" + t.c.Text()
}

// Answered is the session's answer to a check: it has read the contract the check carried.
func (t *ContractTeller) Answered() {
	t.mu.Lock()
	t.owed = false
	t.mu.Unlock()
}

// Owed says the next session check carries the contract.
func (t *ContractTeller) Owed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.owed
}

// Contract is the contract as last told.
func (t *ContractTeller) Contract() Contract {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.c
}

func (t *ContractTeller) write(c Contract) {
	if t.Write == nil {
		return
	}
	if err := t.Write(c.Text()); err != nil {
		t.record("contract: " + ContractFile + " not written: " + err.Error())
	}
}

func (t *ContractTeller) record(line string) {
	if t.Record == nil {
		return
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	t.Record(now().UTC().Format(time.RFC3339) + " " + line)
}

// DeferredCheckOf reads one of the session check's record lines: a check deferred
// because the session runs no monitor over its wake file ("presence: session check <nonce>
// deferred: ... runs no monitor over <file>; ..."), its nonce and the file. ok is false for
// every other line, a check deferred for any other reason among them.
func DeferredCheckOf(line string) (nonce, file string, ok bool) {
	const head, mid, mark = "presence: session check ", " deferred: ", "runs no monitor over "
	i := strings.Index(line, head)
	if i < 0 {
		return "", "", false
	}
	rest := line[i+len(head):]
	nonce, reason, found := strings.Cut(rest, mid)
	if !found || nonce == "" || strings.ContainsAny(nonce, " \t") {
		return "", "", false
	}
	j := strings.Index(reason, mark)
	if j < 0 {
		return "", "", false
	}
	file, _, _ = strings.Cut(reason[j+len(mark):], ";")
	file = strings.TrimSpace(file)
	if file == "" {
		return "", "", false
	}
	return nonce, file, true
}

// NoteNoMonitor is a session check the session did not read because no monitor runs over its
// wake file (DeferredCheckOf). The check wrote nothing and queued nothing, so it is marked
// read: the next check is owed on the check cadence (ProveEvery while she is up, SessionQuiet
// while she is down), not after ReaskAfter, which is only for a check a queueing session still
// holds. Three such checks then say her down on that cadence (MonitorWatch), and her down beat
// can say the reason as soon as the third is unanswered.
func (s *SessionCheck) NoteNoMonitor(line string) {
	if s == nil {
		return
	}
	if _, _, ok := DeferredCheckOf(line); !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m != nil {
		s.m.Read = true
	}
}

// IsSessionAnswer says a record line is the session check's word that the session answered
// or wrote on the bus ("presence: up: ...").
func IsSessionAnswer(line string) bool { return strings.Contains(line, "presence: up: ") }

// MonitorWatch is the daemon's answer to a session check deferred because the session runs no
// monitor over its wake file: the check is pushed as a message on her stream ("answer <nonce>:
// <pong line>", then the contract, Post) instead of being left in the log, and NoMonitorChecks
// of them in a row with no answer is the friend down with the reason "session runs no monitor
// over <file>" (Down), which her beat and her presence file say. Any answer from the session
// clears it. A grok deliver with no monitor writes nothing, so this message is what the session
// is pushed, and it carries the contract.
type MonitorWatch struct {
	Post func(ctx context.Context, subject, body string) error
	Pong func(nonce string) string
	// Contract is the contract text the deferred message carries (the wake file, the monitor
	// command, the start stamp, the finish form). Nil carries the answer line only.
	Contract func() string
	Record   func(line string)
	Now      func() time.Time

	mu     sync.Mutex
	misses int
	file   string
}

// Line reads one record line of the session check: a deferral for no monitor is pushed and
// counted, an answer clears the count.
func (m *MonitorWatch) Line(ctx context.Context, line string) {
	if IsSessionAnswer(line) {
		m.Answered()
		return
	}
	if nonce, file, ok := DeferredCheckOf(line); ok {
		m.Deferred(ctx, nonce, file)
	}
}

// Deferred is the check with nonce deferred because the session runs no monitor over file.
func (m *MonitorWatch) Deferred(ctx context.Context, nonce, file string) {
	m.mu.Lock()
	m.misses++
	m.file = file
	n := m.misses
	m.mu.Unlock()
	pong := "<pong line>"
	if m.Pong != nil {
		pong = m.Pong(nonce)
	}
	body := fmt.Sprintf("answer %s: %s\n", nonce, pong)
	if m.Contract != nil {
		if text := m.Contract(); text != "" {
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			body += "\n" + text
		}
	}
	line := fmt.Sprintf("presence: session check %s pushed as a message: the session runs no monitor over %s (%d of %d unanswered)", nonce, file, n, NoMonitorChecks)
	if m.Post == nil {
		line += "; no push path"
	} else if err := m.Post(ctx, SessionCheckPrefix+nonce, body); err != nil {
		line += "; the push failed: " + oneLine(err.Error(), 300)
	}
	m.record(line)
	if n == NoMonitorChecks {
		m.record("presence: down: " + NoMonitorReason(file))
	}
}

// Answered is any answer from the session: the count starts again.
func (m *MonitorWatch) Answered() {
	m.mu.Lock()
	m.misses, m.file = 0, ""
	m.mu.Unlock()
}

// Down says the friend is down for no monitor, and the reason her beat says.
func (m *MonitorWatch) Down() (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.misses < NoMonitorChecks {
		return false, ""
	}
	return true, NoMonitorReason(m.file)
}

// NoMonitorReason is the down reason of a session that runs no monitor over file.
func NoMonitorReason(file string) string { return "session runs no monitor over " + file }

func (m *MonitorWatch) record(line string) {
	if m.Record == nil {
		return
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	m.Record(now().UTC().Format(time.RFC3339) + " " + line)
}

// JobSigns is what a held card's job directory shows of work begun: a worktree the friend
// made, or one whose index was written after the stage, and a push of the card's branch.
type JobSigns struct {
	Worktree, Pushed bool
}

// Started says the job shows work begun.
func (s JobSigns) Started() bool { return s.Worktree || s.Pushed }

// StartStampOwed is one start stamp the inbox pass owes: the card, why, and the verb.
type StartStampOwed struct {
	Card HeldCard
	Why  string
	Argv []string
}

// StartWatch is the inbox pass's start stamps for a session friend whose children may never
// run the verb: each work card on her row whose job directory shows work begun (Look: a
// worktree, a branch push) is stamped started once this run (Pass), and one the server did not
// take is sent again after StartRetry (Failed). A card that leaves her row is forgotten.
type StartWatch struct {
	Friend string
	Look   func(h HeldCard) JobSigns

	mu      sync.Mutex
	stamped map[string]bool
	retry   map[string]time.Time
}

// Pass is one inbox pass over the cards on her row at now: the start stamps owed.
func (s *StartWatch) Pass(now time.Time, cards []HeldCard) []StartStampOwed {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stamped == nil {
		s.stamped, s.retry = map[string]bool{}, map[string]time.Time{}
	}
	held := map[string]bool{}
	var owed []StartStampOwed
	for _, h := range cards {
		if h.Job == "" || (h.Kind != "" && h.Kind != "work") {
			continue
		}
		held[h.Job] = true
		if s.stamped[h.Job] || now.Before(s.retry[h.Job]) || s.Look == nil {
			continue
		}
		signs := s.Look(h)
		if !signs.Started() {
			continue
		}
		why := "a branch push"
		if signs.Worktree {
			why = "a worktree"
			if signs.Pushed {
				why += " and a branch push"
			}
		}
		s.stamped[h.Job] = true
		owed = append(owed, StartStampOwed{Card: h, Why: "its job " + h.Job + " shows " + why, Argv: StartArgv(s.Friend, h)})
	}
	for job := range s.stamped {
		if !held[job] {
			delete(s.stamped, job)
			delete(s.retry, job)
		}
	}
	return owed
}

// Failed is a start stamp the server did not take: the job is stamped again after StartRetry.
func (s *StartWatch) Failed(job string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stamped == nil {
		s.stamped, s.retry = map[string]bool{}, map[string]time.Time{}
	}
	delete(s.stamped, job)
	s.retry[job] = now.Add(StartRetry)
}

// RowEpoch is the epoch of the cards on her row: the highest, "" when she holds none.
func RowEpoch(cards []HeldCard) string {
	var top uint64
	found := false
	for _, h := range cards {
		if !found || h.Epoch > top {
			top, found = h.Epoch, true
		}
	}
	if !found {
		return ""
	}
	return strconv.FormatUint(top, 10)
}
