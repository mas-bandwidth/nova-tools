package friend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
)

// The friend's reader row (docs/SPEC-FRIEND.md, the reader row; the owner, 2026-10-05: "Make
// this part of the friend's lane daemon"): reader-<friend> is beaten by her daemon, which
// begins every asked read up to her read slots, runs each as a one-shot of her harness and
// records the verdict. Read slots are their own number beside width: a read never takes a
// card lane and a card never takes a read slot ("Read slots are different from worker cards").

// ReaderOf is the name of a friend's reader row.
func ReaderOf(friend string) string { return "reader-" + friend }

// DefaultReadSlots is how many reads a friend runs at once when her row says none.
const DefaultReadSlots = 2

// ReadAskEvery is how often the daemon asks its reader queue, which is the reader's beat.
const ReadAskEvery = 10 * time.Second

// ReadBodyKept bounds the body of a read's RESULT.md recorded as its finding.
const ReadBodyKept = 3500

// ReadHarness is a harness that runs one read as a one-shot of its own: a new session with
// the prompt as its only turn, on model ("" is the harness's own), blocking until it ends.
// A harness without it runs the prompt as the seed of a new lane session (LaneHarness).
type ReadHarness interface {
	RunRead(ctx context.Context, model, prompt string) (LaneTurn, error)
}

// ReadModels is the model of each tier for a claude account's reads, the table the bud
// runners carried.
var ReadModels = map[string]string{
	"frontier": "claude-fable-5-1",
	"heavy":    "claude-opus-5-5",
	"pro":      "claude-sonnet-5-5",
	"flash":    "claude-haiku-4-5-20251001",
}

// ParseReadSlots reads the friend's read slots off her beat's answer (row_read_slots=<n>,
// beside row_mode and row_width); ok is false when the answer carries none.
func ParseReadSlots(answer string) (n int, ok bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_read_slots="); found {
			if k, err := strconv.Atoi(v); err == nil && k >= 0 {
				n, ok = k, true
			}
		}
	}
	return n, ok
}

// ReadPacket is what the reader queue says of one asked read.
type ReadPacket struct {
	Tier       string `json:"tier"`
	Head       string `json:"head"`
	WorkBranch string `json:"work_branch"`
	Attempt    int    `json:"attempt"`
	Brief      string `json:"brief"`
	Report     string `json:"report"`
}

// AskedRead is one card of the reader queue in the asked column.
type AskedRead struct {
	ID     string
	Epoch  string
	Col    string
	Gen    int // the read card's generation as the queue carries it (0: a queue before it); a stop-return names it
	Packet ReadPacket
}

// ParseReadQueue reads `queue --as reader-<friend> --json`: the asked reads, in the queue's
// order, each with the queue's epoch.
func ParseReadQueue(out string) ([]AskedRead, error) {
	var q struct {
		Epoch any `json:"epoch"`
		Cards []struct {
			ID     string     `json:"id"`
			Col    string     `json:"col"`
			Gen    int        `json:"gen"`
			Packet ReadPacket `json:"packet"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		return nil, fmt.Errorf("the reader queue is not JSON: %v", err)
	}
	epoch := ""
	switch e := q.Epoch.(type) {
	case string:
		epoch = e
	case float64:
		epoch = strconv.Itoa(int(e))
	}
	if epoch == "" {
		return nil, errors.New("the reader queue names no epoch")
	}
	var asked []AskedRead
	for _, c := range q.Cards {
		if c.Col == "asked" || c.Col == "reading" {
			asked = append(asked, AskedRead{ID: c.ID, Epoch: epoch, Col: c.Col, Gen: c.Gen, Packet: c.Packet})
		}
	}
	return asked, nil
}

// ReadQueueArgv is the sprint verb that is the reader's beat and answers its queue.
func ReadQueueArgv(friend string) []string {
	return []string{"queue", "--as", ReaderOf(friend), "--json"}
}

// ReadBeginArgv begins an asked read.
func ReadBeginArgv(friend string, r AskedRead) []string {
	return []string{"read", "--as", ReaderOf(friend), "--begin", readSelector(r), "--epoch", r.Epoch, "--op", readOp(friend, r, "begin")}
}

// ReadVerdictArgv records a read's verdict (ok or broken) with its finding and usage.
func ReadVerdictArgv(friend string, r AskedRead, verdict, finding, usage string) []string {
	return []string{"read", "--as", ReaderOf(friend), "--" + verdict, readSelector(r), "--epoch", r.Epoch, "--finding", finding, "--usage", usage, "--op", readOp(friend, r, "verdict")}
}

// ReadReturnArgv returns a read that has no verdict, with why.
func ReadReturnArgv(friend string, r AskedRead, reason, usage string) []string {
	return []string{"read", "--as", ReaderOf(friend), "--return", readSelector(r), "--reason", reason, "--epoch", r.Epoch, "--usage", usage, "--op", readOp(friend, r, "return")}
}

func readSelector(r AskedRead) string { return r.ID + "@" + strconv.Itoa(max(r.Gen, 1)) }

// Each phase has one replay token derived from the immutable queue lease.
// The completed settlement freezes the verdict's remaining arguments.
func readOp(friend string, r AskedRead, phase string) string {
	sum := sha256.Sum256([]byte(ReaderOf(friend) + "\x00" + r.ID + "\x00" + r.Epoch + "\x00" + strconv.Itoa(max(r.Gen, 1)) + "\x00" + phase))
	return "friend-read-" + hex.EncodeToString(sum[:16])
}

func readMovedOne(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "READ" && fields[1] == "OK" && fields[2] == "moved=1" {
			return true
		}
	}
	return false
}

var repoLine = regexp.MustCompile(`(?m)^REPO:\s*(\S+)`)
var baseLine = regexp.MustCompile(`(?m)^BASE:\s*(\S+)`)

func briefField(re *regexp.Regexp, brief string) string {
	if m := re.FindStringSubmatch(brief); m != nil {
		return m[1]
	}
	return ""
}

// BenchRule is the sentence every read prompt carries: the machine the friend runs on runs no go command, a Linux
// bench does.
func BenchRule(friend, card string) string {
	d := "~/nova-bench/buds/" + friend + "/reads/" + card
	return fmt.Sprintf("BENCH RULE, over any GOCACHE or go command the brief gives: this machine (the one you are on) runs no go build, go test or go vet, ever. Read the checkout here (change nothing), then copy the tree to a Linux bench and run every go command there: ssh <bench> 'mkdir -p %s' && rsync -a --delete <your repo dir>/ <bench>:%s/repo/ && ssh <bench> 'cd %s/repo && export GOCACHE=~/nova-bench/buds/%s/cache/go-build GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 && nice -n 19 go ...' (<bench> is the Linux bench your AGENTS.md names; the next one it names only when that one does not answer). Re-sync after each edit. When you are done, remove that bench directory: ssh <bench> 'rm -rf %s'. Report the gate lines as the bench printed them, naming the bench.", d, d, d, friend, d)
}

// ReadText is READ.md: the read's job, as the reader loops wrote it.
func ReadText(friend, jobDir string, r AskedRead) string {
	p := r.Packet
	repo, base := briefField(repoLine, p.Brief), briefField(baseLine, p.Brief)
	me := ReaderOf(friend)
	return fmt.Sprintf(`# JOB: read %[1]s, attempt %[2]d

You are %[3]s, a reader of %[4]s reading one card. Work only in %[5]s. Change nothing in the work, commit nothing, push nothing.

1. Clone: cd %[5]s && git clone -q https://github.com/%[6]s.git repo && cd repo && git fetch -q origin %[7]s && git checkout -q --detach %[8]s (verify git rev-parse HEAD is %[8]s; if the head cannot be had, write RESULT.md with verdict: none and say why).
2. The work's change is exactly $(git merge-base %[8]s origin/%[9]s)..%[8]s, on branch %[7]s against %[9]s. %[9]s may have moved since the work began; a diff against its tip shows every change landed since as a deletion, and those are never the work's and never a finding. Judge the work by the merge-base diff alone.
3. The card under review is BRIEF.md beside this file; the worker's own report is WORKER-REPORT.txt. Judge: does the change do the brief's task, touch only its PATHS, add the test the TEST line names (red before, green after, pinning the behaviour), and keep the brief's RULES.
4. The read's gate, in place of the card's: the packages the change touches and the tests that read a doc it changes, each as nice -n 19 go vet <pkg> and nice -n 19 go test -count=1 -timeout 600s <pkg>. Never ./... . %[10]s
5. End by writing %[5]s/RESULT.md in exactly this shape:
    head: <the commit, full sha>
    branch: %[7]s
    verdict: ok | broken
    gate: <the gate commands you ran, or ->
    report: <one line>
    ## Body
    <your findings, each with file:line>
A broken verdict tells the worker what to do: at least one finding names the file (file:line), the line, or the brief's STEP or RULE the work breaks, and says what to change. A broken verdict that names no file, line or rule is not a verdict.
`, r.ID, p.Attempt, me, friend, jobDir, repo, p.WorkBranch, p.Head, base, BenchRule(friend, r.ID))
}

// ReadPrompt is the one turn a read runs: do READ.md, stop when RESULT.md is written.
func ReadPrompt(friend, jobDir string) string {
	return fmt.Sprintf("You are %s, a reader of %s. Do the read in %s/READ.md exactly, and stop when RESULT.md is written.", ReaderOf(friend), friend, jobDir)
}

// ReadVerdict reads a read's RESULT.md: its verdict (ok or broken; "" when there is none),
// and the finding recorded with it, the report line then the body.
func ReadVerdict(result string) (verdict, finding string) {
	var report string
	for _, line := range strings.Split(result, "\n") {
		l := strings.TrimSpace(line)
		low := strings.ToLower(l)
		switch {
		case verdict == "" && strings.HasPrefix(low, "verdict:"):
			if f := strings.Fields(l[len("verdict:"):]); len(f) > 0 {
				verdict = strings.ToLower(f[0])
			}
		case report == "" && strings.HasPrefix(low, "report:"):
			report = strings.TrimSpace(l[len("report:"):])
		}
	}
	if verdict != "ok" && verdict != "broken" {
		return "", ""
	}
	body := ""
	if _, after, found := strings.Cut(result, "## Body\n"); found {
		body = after
	}
	if len(body) > ReadBodyKept {
		body = body[:ReadBodyKept]
	}
	return verdict, strings.TrimSpace(report + " " + body)
}

type readResult struct {
	read       AskedRead
	model      string
	dir        string
	start      time.Time
	turn       LaneTurn
	err        error
	gateCalled bool // the native process callback ran before harness execution
}

// readSet is the daemon's reads at once.
type readSet struct {
	results chan readResult
	running map[string]bool
	begun   map[string]bool // asked reads this daemon began: the queue shows them asked until the verdict lands
	asked   []AskedRead
	askedAt time.Time
	known   bool                          // false until a canonical reader queue answers; failures hold shared capacity
	cancel  map[string]context.CancelFunc // each read under way: the machine's stop ends it (stop.go)
	stopped map[string]bool               // reads the stop cancelled: no verdict, a stop-return
	active  map[string]AskedRead          // each read under way as it was begun: its generation and epoch, which the queue's refresh no longer lists
	persist func(string, readSettlement) error
}

// A settlement is written before its RPC. A lost response or daemon exit must
// never turn an already completed read into another model run.
type readSettlement struct {
	Read    AskedRead `json:"read"`
	Verdict string    `json:"verdict,omitempty"`
	Finding string    `json:"finding,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Usage   string    `json:"usage"`
}

const readSettlementFile = "SETTLEMENT.json"
const readActiveFile = "ACTIVE.json"
const readStopNoLaunchFile = "STOP-NO-LAUNCH.json"

type readActive struct {
	Read     AskedRead `json:"read"`
	PID      int       `json:"pid"`
	Identity string    `json:"identity"`
}

func readActivePath(dir string) string       { return filepath.Join(dir, readActiveFile) }
func readAnchorPath(dir string) string       { return readActivePath(dir) + ".anchor" }
func readStopNoLaunchPath(dir string) string { return filepath.Join(dir, readStopNoLaunchFile) }

func saveReadStopNoLaunch(dir string, debt StopReturn) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(debt)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(readStopNoLaunchPath(dir), b, 0o600)
}

func readStopNoLaunchMatches(dir string, debt StopReturn) bool {
	b, err := os.ReadFile(readStopNoLaunchPath(dir))
	if err != nil {
		return false
	}
	var saved StopReturn
	if json.Unmarshal(b, &saved) != nil {
		return false
	}
	return saved.Card == debt.Card && saved.Row == debt.Row && saved.Epoch == debt.Epoch && max(saved.Gen, 1) == max(debt.Gen, 1)
}

func sameReadLease(a, b AskedRead) bool {
	return a.ID == b.ID && a.Epoch == b.Epoch && max(a.Gen, 1) == max(b.Gen, 1) && a.Packet.Head == b.Packet.Head
}

func saveReadActive(dir string, a readActive) error {
	if a.PID <= 0 || a.Identity == "" {
		return errors.New("read process identity unavailable")
	}
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(readActivePath(dir), b, 0o600)
}

func loadReadActive(dir string) (readActive, bool, error) {
	b, err := os.ReadFile(readActivePath(dir))
	if os.IsNotExist(err) {
		return readActive{}, false, nil
	}
	if err != nil {
		return readActive{}, false, err
	}
	var a readActive
	if err := json.Unmarshal(b, &a); err != nil {
		return a, false, err
	}
	if a.Read.ID == "" || a.Read.Epoch == "" || a.PID <= 0 || a.Identity == "" {
		return a, false, errors.New("incomplete active read identity")
	}
	return a, true, nil
}

func readSettlementPath(dir string) string { return filepath.Join(dir, readSettlementFile) }

func saveReadSettlement(dir string, p readSettlement) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(readSettlementPath(dir), b, 0o600)
}

func loadReadSettlement(dir string) (readSettlement, bool, error) {
	b, err := os.ReadFile(readSettlementPath(dir))
	if os.IsNotExist(err) {
		return readSettlement{}, false, nil
	}
	if err != nil {
		return readSettlement{}, false, err
	}
	var p readSettlement
	if err := json.Unmarshal(b, &p); err != nil {
		return p, false, err
	}
	if p.Read.ID == "" || p.Read.Epoch == "" || p.Usage == "" || (p.Verdict == "" && p.Reason == "") {
		return p, false, errors.New("incomplete read settlement")
	}
	return p, true, nil
}

func newReadSet() *readSet {
	return &readSet{results: make(chan readResult, 64), running: map[string]bool{}, begun: map[string]bool{}, cancel: map[string]context.CancelFunc{}, stopped: map[string]bool{}, active: map[string]AskedRead{}, persist: saveReadSettlement}
}

func (d *Daemon) readSlots() int {
	if d.ReadSlots != nil {
		return d.ReadSlots()
	}
	return DefaultReadSlots
}

func (d *Daemon) readModel(tier string) string {
	if d.ReadModel != nil {
		return d.ReadModel(tier)
	}
	return ""
}

// readQueueStep refreshes native read ownership before either admission path.
func (l *loop) readQueueStep(now time.Time) {
	d, s := l.d, l.reads
	if d.Sprint == nil {
		return
	}
	at := now.UTC().Format(time.RFC3339)
	if s.askedAt.IsZero() || now.Sub(s.askedAt) >= ReadAskEvery {
		s.askedAt = now
		out, err := d.Sprint(l.ctx, ReadQueueArgv(d.Friend))
		if err != nil {
			d.Record(fmt.Sprintf("%s reads: the reader queue: %s", at, oneLine(err.Error(), 300)))
			s.known = false
		} else if asked, parseErr := ParseReadQueue(out); parseErr != nil {
			d.Record(fmt.Sprintf("%s reads: %s", at, parseErr))
			s.known = false
		} else {
			s.asked, s.known = asked, true
			l.retryReadSettlements(now)
		}
	}
}

// childRoom counts each owned job once, including reservations between turns
// and recovered children whose exit is not proved. Control turns keep their own path.
func (l *loop) childRoom(width int) bool {
	if l.lanes.stateUnknown {
		return false
	}
	jobs := map[string]bool{}
	for job := range l.lanes.state.Started {
		jobs["work:"+job] = true
	}
	for _, st := range l.lanes.adopt {
		jobs["work:"+filepath.Base(st.Card.Outbox)] = true
	}
	for _, ln := range l.lanes.lanes {
		if ln.card != nil {
			jobs["work:"+filepath.Base(ln.card.Outbox)] = true
		}
	}
	if l.d.Sprint != nil && !l.reads.known {
		return false
	}
	for id := range l.reads.running {
		jobs["read:"+id] = true
	}
	for _, r := range l.reads.asked {
		if r.Col == "reading" {
			jobs["read:"+r.ID] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(l.d.Dir, "reads"))
	if err != nil && !os.IsNotExist(err) {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			_, found, err := loadReadActive(filepath.Join(l.d.Dir, "reads", e.Name()))
			if err != nil {
				return false
			}
			if found {
				jobs["read:"+e.Name()] = true
			}
		}
	}
	return len(jobs) < width
}

// readStep is the reader row's step in one-shot mode: its queue asked once a ReadAskEvery,
// and each asked read begun and run while a read slot is free and the lanes are neither
// backing off from a rate limit nor held out of funds. Work and reads share physical width.
func (l *loop) readStep(now time.Time) {
	d, s := l.d, l.reads
	if d.Sprint == nil {
		return
	}
	at := now.UTC().Format(time.RFC3339)
	l.readQueueStep(now)
	// Settlement is independent of read slots and provider availability. In
	// particular, a down key must not strand a result already produced.
	settled := map[string]bool{}
	for _, r := range s.asked {
		if l.readStopOwns(r) {
			continue // a STOP return owns this exact lease; never publish a verdict behind it
		}
		if s.running[r.ID] {
			continue
		}
		dir := filepath.Join(d.Dir, "reads", r.ID)
		quiescent := false
		active, activeFound, activeErr := loadReadActive(dir)
		if activeErr != nil {
			d.Record(fmt.Sprintf("%s read %s: active identity held: %s", at, r.ID, activeErr))
			continue
		}
		if activeFound {
			if !sameReadLease(active.Read, r) {
				d.Record(fmt.Sprintf("%s read %s: active identity differs from queue; held", at, r.ID))
				continue
			}
			if !l.readGroupQuiescent(dir, active) {
				d.Record(fmt.Sprintf("%s read %s: active process group still alive; held", at, r.ID))
				continue
			}
			quiescent = true
		}
		p, found, err := loadReadSettlement(dir)
		if err != nil {
			d.Record(fmt.Sprintf("%s read %s: settlement held: %s", at, r.ID, err))
			continue
		}
		if found && !sameReadLease(p.Read, r) {
			d.Record(fmt.Sprintf("%s read %s: settlement identity differs from queue; held", at, r.ID))
			continue
		}
		if !found && r.Col == "reading" && quiescent {
			// A previous process may have exited after RESULT.md was written.
			// Recover its exact verdict without running the model again.
			if raw, err := os.ReadFile(filepath.Join(dir, "RESULT.md")); err == nil {
				if verdict, finding := ReadVerdict(string(raw)); verdict != "" && readResultHead(string(raw)) == r.Packet.Head {
					p = readSettlement{Read: r, Verdict: verdict, Finding: finding, Usage: "model=unknown wall=0s harness=" + d.Harness + " account=" + d.Friend}
					if err = s.persist(dir, p); err != nil {
						d.Record(fmt.Sprintf("%s read %s: settlement held: %s", at, r.ID, err))
						continue
					}
					found = true
				}
			}
		}
		if found {
			if activeFound && !l.clearReadActive(dir, r.ID, at) {
				continue
			}
			l.sendReadSettlement(dir, p, now)
			settled[r.ID] = true
		} else if r.Col == "reading" && quiescent {
			// The daemon owns no harness for this reading row. Return it so
			// the coordinator can route the interrupted read again.
			p = readSettlement{Read: r, Reason: "reader daemon stopped before a verdict was recorded", Usage: "model=unknown wall=0s harness=" + d.Harness + " account=" + d.Friend}
			if err := s.persist(dir, p); err != nil {
				d.Record(fmt.Sprintf("%s read %s: settlement held: %s", at, r.ID, err))
				continue
			}
			if activeFound && !l.clearReadActive(dir, r.ID, at) {
				continue
			}
			l.sendReadSettlement(dir, p, now)
			settled[r.ID] = true
		} else if r.Col == "reading" {
			d.Record(fmt.Sprintf("%s read %s: no process exit proof or verdict; held", at, r.ID))
		}
	}
	if d.readSlots() <= 0 || l.lanes.envHeld || l.lanes.gov.Held() != "" || l.lanes.gov.Paused(now) || !d.machineMayLaunch() {
		return
	}
	occupied := len(s.running)
	for _, r := range s.asked {
		if r.Col == "reading" && !s.running[r.ID] && !settled[r.ID] {
			occupied++
		}
	}
	for _, r := range s.asked {
		if r.Col != "asked" {
			continue
		}
		if l.readStopOwns(r) {
			continue
		}
		if settled[r.ID] {
			continue
		}
		if occupied >= d.readSlots() || !l.childRoom(l.lanes.width) {
			return
		}
		if s.running[r.ID] || s.begun[r.ID] || !d.machineMayLaunch() {
			continue
		}
		dir := filepath.Join(d.Dir, "reads", r.ID)
		if _, found, err := loadReadActive(dir); found || err != nil {
			continue // a prior native group owns this path until its exit and debt are settled
		}
		if _, found, err := loadReadSettlement(dir); found || err != nil {
			continue
		}
		out, err := d.Sprint(l.ctx, ReadBeginArgv(d.Friend, r))
		if err != nil || !readMovedOne(out) {
			why := out
			if err != nil {
				why = err.Error()
			}
			d.Record(fmt.Sprintf("%s read %s: begin refused: %s", at, r.ID, oneLine(why, 300)))
			s.begun[r.ID] = true // not begun again by this daemon until the queue stops asking it
			continue
		}
		s.running[r.ID], s.begun[r.ID] = true, true
		occupied++
		l.startRead(r, now)
	}
	// a read the queue no longer asks is forgotten, so a read asked again is begun again
	for id := range s.begun {
		if !s.running[id] && !containsRead(s.asked, id) {
			delete(s.begun, id)
		}
	}
}

func (l *loop) readStopOwns(r AskedRead) bool {
	for _, debt := range l.lanes.state.StopReturns {
		if debt.Job == "read:"+r.ID && debt.Row == ReaderOf(l.d.Friend) && debt.Card == r.ID && debt.Epoch == r.Epoch && max(debt.Gen, 1) == max(r.Gen, 1) {
			return true
		}
	}
	return false
}

func (l *loop) clearReadActive(dir, id, at string) bool {
	if err := os.Remove(readAnchorPath(dir)); err != nil && !os.IsNotExist(err) {
		l.d.Record(fmt.Sprintf("%s read %s: anchor cleanup: %s", at, id, err))
		return false
	}
	if err := os.Remove(readActivePath(dir)); err != nil && !os.IsNotExist(err) {
		l.d.Record(fmt.Sprintf("%s read %s: active identity cleanup: %s", at, id, err))
		return false
	}
	return true
}

// A response can be lost after the server commits and the queue omits the
// read. Replay the durable, byte-identical argv under its stable operation;
// the server's recorded result, not an absent queue row, acknowledges it.
func (l *loop) retryReadSettlements(now time.Time) {
	root := filepath.Join(l.d.Dir, "reads")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		l.d.Record("reads: settlement scan: " + err.Error())
		return
	}
	for _, e := range entries {
		if !e.IsDir() || containsRead(l.reads.asked, e.Name()) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		p, found, err := loadReadSettlement(dir)
		if err != nil {
			l.d.Record("read " + e.Name() + ": settlement held: " + err.Error())
			continue
		}
		if !found || p.Read.ID != e.Name() || l.readStopOwns(p.Read) {
			continue
		}
		a, active, err := loadReadActive(dir)
		if err != nil {
			continue
		}
		if active {
			if !sameReadLease(a.Read, p.Read) || !l.readGroupQuiescent(dir, a) || !l.clearReadActive(dir, p.Read.ID, now.UTC().Format(time.RFC3339)) {
				continue
			}
		}
		l.sendReadSettlement(dir, p, now)
	}
}

var readHeadLine = regexp.MustCompile(`(?m)^head:\s*(\S+)`)

func readResultHead(raw string) string { return briefField(readHeadLine, raw) }

func (l *loop) sendReadSettlement(dir string, p readSettlement, now time.Time) {
	d := l.d
	var argv []string
	if p.Verdict != "" {
		argv = ReadVerdictArgv(d.Friend, p.Read, p.Verdict, p.Finding, p.Usage)
	} else {
		argv = ReadReturnArgv(d.Friend, p.Read, p.Reason, p.Usage)
	}
	out, err := d.Sprint(l.ctx, argv)
	d.Record(fmt.Sprintf("%s read %s: settlement: %s", now.UTC().Format(time.RFC3339), p.Read.ID, recorded(out, err)))
	if err == nil && readMovedOne(out) {
		if err := os.Remove(readSettlementPath(dir)); err != nil && !os.IsNotExist(err) {
			d.Record(fmt.Sprintf("read %s: settlement cleanup: %s", p.Read.ID, err))
		}
		delete(l.reads.begun, p.Read.ID)
		l.reads.askedAt = time.Time{} // re-read the canonical queue before another begin
	}
}

func containsRead(asked []AskedRead, id string) bool {
	for _, r := range asked {
		if r.ID == id {
			return true
		}
	}
	return false
}

// startRead writes the read's job (BRIEF.md, WORKER-REPORT.txt, READ.md) under the friend's
// directory and runs it as a one-shot of her harness in a goroutine.
func (l *loop) startRead(r AskedRead, now time.Time) {
	d, s := l.d, l.reads
	dir := filepath.Join(d.Dir, "reads", r.ID)
	model := d.readModel(r.Packet.Tier)
	d.Record(fmt.Sprintf("%s read %s: begun tier=%s model=%s", now.UTC().Format(time.RFC3339), r.ID, dash(r.Packet.Tier), dash(model)))
	rctx, cancel := context.WithCancel(l.ctx)
	s.cancel[r.ID], s.active[r.ID] = cancel, r
	go func() {
		defer cancel()
		res := readResult{read: r, model: model, dir: dir, start: now}
		defer func() { s.results <- res }()
		_ = os.Remove(filepath.Join(dir, "RESULT.md")) // ignored: a result of an earlier attempt is not this read's
		if res.err = os.MkdirAll(dir, 0o755); res.err != nil {
			return
		}
		for name, text := range map[string]string{"BRIEF.md": r.Packet.Brief, "WORKER-REPORT.txt": r.Packet.Report, "READ.md": ReadText(d.Friend, dir, r)} {
			if res.err = os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); res.err != nil {
				return
			}
		}
		ctx, prompt := LaneContext(rctx), ReadPrompt(d.Friend, dir)
		ctx = WithProcessGroupAnchor(ctx, readAnchorPath(dir))
		ctx = WithProcessStarted(ctx, func(pid int) error {
			res.gateCalled = true
			return saveReadActive(dir, readActive{Read: r, PID: pid, Identity: ProcessIdentity(pid)})
		})
		switch h := d.Deliver.(type) {
		case ReadHarness:
			res.turn, res.err = h.RunRead(ctx, model, prompt)
		case LaneHarness:
			_, res.err = h.OpenSession(ctx, prompt)
		default:
			res.err = errors.New("the harness runs no one-shot")
		}
	}()
}

// readDone records a read that ended: the verdict its RESULT.md names, else a return with
// why there is none; a rate limit or out of funds goes to the governor as a card's turn's
// does (providerLimit), and a read cut off by the daemon stopping is left begun.
func (l *loop) readDone(r readResult, now time.Time) {
	d, s := l.d, l.reads
	delete(s.running, r.read.ID)
	delete(s.begun, r.read.ID) // the next ask says whether it is asked again; the one in hand is stale
	delete(s.cancel, r.read.ID)
	delete(s.active, r.read.ID)
	s.asked = slices.DeleteFunc(s.asked, func(a AskedRead) bool { return a.ID == r.read.ID })
	if s.stopped[r.read.ID] {
		delete(s.stopped, r.read.ID)
		l.stopReadDone(r, now) // cancelled by the machine's stop: no verdict, a stop-return
		return
	}
	if l.ctx.Err() != nil {
		d.Record(fmt.Sprintf("%s read %s: left begun: the daemon stopped", now.UTC().Format(time.RFC3339), r.read.ID))
		return
	}
	at := now.UTC().Format(time.RFC3339)
	usage := fmt.Sprintf("model=%s wall=%ds harness=%s account=%s", dash(r.model), int(now.Sub(r.start).Seconds()), d.Harness, d.Friend)
	raw, _ := os.ReadFile(filepath.Join(r.dir, "RESULT.md"))
	verdict, finding := ReadVerdict(string(raw))
	if verdict != "" && readResultHead(string(raw)) != r.read.Packet.Head {
		verdict = "" // a result for another head cannot close this read
	}
	p := readSettlement{Read: r.read, Usage: usage}
	if verdict != "" {
		p.Verdict, p.Finding = verdict, finding
		if err := s.persist(r.dir, p); err != nil {
			d.Record(fmt.Sprintf("%s read %s: settlement held: %s", at, r.read.ID, err))
			return
		}
		if !l.readReadyToSettle(r.dir, r.read, at) {
			return
		}
		l.sendReadSettlement(r.dir, p, now)
		return
	}
	why := fmt.Sprintf("no verdict from the %s run (exit %d)", d.Harness, r.turn.Exit)
	if r.err != nil {
		var rate RateLimited
		var funds OutOfFunds
		switch {
		case errors.As(r.err, &funds):
			why = "usage limit on " + d.Friend + ": " + oneLine(funds.Reason, 200)
		case errors.As(r.err, &rate):
			why = "usage limit on " + d.Friend + ": " + oneLine(rate.Reason, 200)
		default:
			why += ": " + oneLine(r.err.Error(), 300)
		}
		l.providerLimit(r.err, r.start, now)
	}
	p.Reason = why
	if err := s.persist(r.dir, p); err != nil {
		d.Record(fmt.Sprintf("%s read %s: settlement held: %s", at, r.read.ID, err))
		return
	}
	if !l.readReadyToSettle(r.dir, r.read, at) {
		return
	}
	l.sendReadSettlement(r.dir, p, now)
}

func (l *loop) readReadyToSettle(dir string, read AskedRead, at string) bool {
	a, found, err := loadReadActive(dir)
	if err != nil {
		l.d.Record(fmt.Sprintf("%s read %s: active identity held: %s", at, read.ID, err))
		return false
	}
	if !found {
		return true
	} // a harness without a native child
	if !sameReadLease(a.Read, read) || procgroup.GroupRunnable(a.PID) {
		l.d.Record(fmt.Sprintf("%s read %s: process group not quiescent; settlement held", at, read.ID))
		return false
	}
	if err := os.Remove(readAnchorPath(dir)); err != nil && !os.IsNotExist(err) {
		l.d.Record(fmt.Sprintf("%s read %s: anchor cleanup: %s", at, read.ID, err))
		return false
	}
	if err := os.Remove(readActivePath(dir)); err != nil && !os.IsNotExist(err) {
		l.d.Record(fmt.Sprintf("%s read %s: active identity cleanup: %s", at, read.ID, err))
		return false
	}
	return true
}

// readStopQuiescent is the current turn's proof before stopReadDone may mark
// its stop-return Ended. The native callback runs behind RealExec's launch
// gate; an absent ACTIVE is an unspawned turn only when that callback never
// ran. Keep ACTIVE until the stop-return acknowledgement is durable.
func (l *loop) readStopQuiescent(r readResult, now time.Time) bool {
	debt := StopReturn{Job: "read:" + r.read.ID, Row: ReaderOf(l.d.Friend), Card: r.read.ID, Gen: r.read.Gen, Epoch: r.read.Epoch}
	if _, found, err := loadReadActive(r.dir); err != nil {
		l.d.Record(fmt.Sprintf("%s read %s: stop identity held: %s", now.UTC().Format(time.RFC3339), r.read.ID, err))
		return false
	} else if !found {
		if r.gateCalled {
			return false
		}
		return saveReadStopNoLaunch(r.dir, debt) == nil
	}
	return l.readStopDebtQuiescent(debt, now)
}

// readStopDebtQuiescent is also called after restart. A durable debt alone
// cannot prove the old harness exited: its matching ACTIVE must say which
// kernel process group to verify, at this read's generation and epoch.
func (l *loop) readStopDebtQuiescent(debt StopReturn, now time.Time) bool {
	dir := filepath.Join(l.d.Dir, "reads", debt.Card)
	a, found, err := loadReadActive(dir)
	if err != nil || !found {
		return err == nil && readStopNoLaunchMatches(dir, debt)
	}
	if debt.Row != ReaderOf(l.d.Friend) || a.Read.ID != debt.Card || a.Read.Epoch != debt.Epoch || max(a.Read.Gen, 1) != max(debt.Gen, 1) {
		return false
	}
	return l.readGroupQuiescent(dir, a)
}

func (l *loop) readGroupQuiescent(dir string, a readActive) bool {
	if current := ProcessIdentity(a.PID); current != "" && current != a.Identity {
		return false // a reused leader is not authority over the saved read
	}
	if !procgroup.GroupRunnable(a.PID) {
		return true
	}
	anchor := readAnchorPath(dir)
	if !procgroup.AnchorPinned(a.PID, anchor) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), time.Second)
	defer cancel()
	return procgroup.ReapVerified(ctx, a.PID, a.Identity, anchor, 100*time.Millisecond)
}

// clearStoppedReadActive runs only after stop.go has saved the server's ack.
// The generation check prevents a late cleanup from erasing the next read.
func (l *loop) clearStoppedReadActive(debt StopReturn) bool {
	dir := filepath.Join(l.d.Dir, "reads", debt.Card)
	a, found, err := loadReadActive(dir)
	if err != nil {
		return false
	}
	if found {
		if a.Read.ID != debt.Card || a.Read.Epoch != debt.Epoch || max(a.Read.Gen, 1) != max(debt.Gen, 1) {
			return false
		}
		if current := ProcessIdentity(a.PID); current != "" && current != a.Identity {
			return false
		}
		if procgroup.GroupRunnable(a.PID) {
			return false
		}
		if err := os.Remove(readAnchorPath(dir)); err != nil && !os.IsNotExist(err) {
			return false
		}
		if err := os.Remove(readActivePath(dir)); err != nil && !os.IsNotExist(err) {
			return false
		}
	}
	if readStopNoLaunchMatches(dir, debt) {
		if err := os.Remove(readStopNoLaunchPath(dir)); err != nil && !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func recorded(out string, err error) string {
	if err != nil {
		return "not recorded: " + oneLine(err.Error(), 200)
	}
	return oneLine(out, 120)
}
