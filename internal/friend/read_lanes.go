package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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

// ReadBenchBudget bounds one removal of a read's bench copy, so a bench that does not answer
// does not hold the loop; the owed cleanup is tried again at the next step.
const ReadBenchBudget = 10 * time.Second

// ReadBenchRetry is how long before an owed bench removal is tried again.
const ReadBenchRetry = 30 * time.Second

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
		if c.Col == "asked" {
			asked = append(asked, AskedRead{ID: c.ID, Epoch: epoch, Gen: c.Gen, Packet: c.Packet})
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
	return []string{"read", "--as", ReaderOf(friend), "--begin", r.ID, "--epoch", r.Epoch}
}

// ReadVerdictArgv records a read's verdict (ok or broken) with its finding and usage.
func ReadVerdictArgv(friend string, r AskedRead, verdict, finding, usage string) []string {
	return []string{"read", "--as", ReaderOf(friend), "--" + verdict, r.ID, "--epoch", r.Epoch, "--finding", finding, "--usage", usage}
}

// ReadReturnArgv returns a read that has no verdict, with why.
func ReadReturnArgv(friend string, r AskedRead, reason, usage string) []string {
	return []string{"read", "--as", ReaderOf(friend), "--return", r.ID, "--reason", reason, "--epoch", r.Epoch, "--usage", usage}
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
	return fmt.Sprintf("BENCH RULE, over any GOCACHE or go command the brief gives: this machine (the one you are on) runs no go build, go test or go vet, ever. Read the checkout here (change nothing), then copy the tree to a Linux bench and run every go command there: ssh <bench> 'mkdir -p %s' && rsync -a --delete <your repo dir>/ <bench>:%s/repo/ && ssh <bench> 'cd %s/repo && export GOCACHE=~/nova-bench/buds/%s/cache/go-build GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 && nice -n 19 go ...' (<bench> is the Linux bench your AGENTS.md names; the next one it names only when that one does not answer). Re-sync after each edit. When the finding is recorded, this lane removes that bench directory %s and the local checkout. Report the gate lines as the bench printed them, naming the bench in a `bench:` line.", d, d, d, friend, d)
}

// readBenchDir is the read's bench directory as the reader's prompt names it, under the
// bench login's home (`~`), which the login shell on the bench expands.
func readBenchDir(friend, id string) string {
	return "~/nova-bench/buds/" + friend + "/reads/" + id
}

// readBenchRemoval is the remote line that removes a read's bench copy: the `~` is
// expanded by the bench's login shell, and friend and id are validJob-safe, so the words
// need no quoting beyond the path's fixed slashes. It removes only a path it built from a
// validated friend and id, never a bare variable.
func readBenchRemoval(friend, id string) string {
	return "rm -rf -- " + readBenchDir(friend, id)
}

// benchKey is a field of the read's RESULT.md: `bench: <host>` names the Linux bench the
// reader copied its tree to, which the lane's remote cleanup removes from after the finding
// is recorded.
var benchKey = regexp.MustCompile(`(?m)^bench:\s*(\S+)`)

// ReadBenchHost reads the bench a read named off its RESULT.md (`bench: <host>`); "" when
// there is none or the name is not a host.
func ReadBenchHost(result string) string {
	m := benchKey.FindStringSubmatch(result)
	if m == nil {
		return ""
	}
	host := strings.TrimSpace(m[1])
	if bench.CheckHost(host) != nil {
		return ""
	}
	return host
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
    bench: <the Linux bench that ran them>
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
	read  AskedRead
	model string
	dir   string
	start time.Time
	turn  LaneTurn
	err   error
}

// readBenchCleanup is a read's bench copy owed removal: the host the reader named and the
// directory on it (readBenchDir), tried again at next until it succeeds.
type readBenchCleanup struct {
	host, path string
	next       time.Time
}

// readSet is the daemon's reads at once.
type readSet struct {
	results   chan readResult
	running   map[string]bool
	begun     map[string]bool // asked reads this daemon began: the queue shows them asked until the verdict lands
	asked     []AskedRead
	askedAt   time.Time
	epoch     string                        // the latest epoch seen on the reader queue
	cancel    map[string]context.CancelFunc // each read under way: the machine's stop ends it (stop.go)
	stopped   map[string]bool               // reads the stop cancelled: no verdict, a stop-return
	active    map[string]AskedRead          // each read under way as it was begun: its generation and epoch, which the queue's refresh no longer lists
	owedBench map[string]readBenchCleanup   // reads whose remote bench copy is owed removal, tried until it succeeds
}

func newReadSet() *readSet {
	return &readSet{results: make(chan readResult, 64), running: map[string]bool{}, begun: map[string]bool{}, cancel: map[string]context.CancelFunc{}, stopped: map[string]bool{}, active: map[string]AskedRead{}, owedBench: map[string]readBenchCleanup{}}
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

// readStep is the reader row's step in one-shot mode: its queue asked once a ReadAskEvery,
// and each asked read begun and run while a read slot is free and the lanes are neither
// backing off from a rate limit nor held out of funds. It takes nothing from the card lanes.
func (l *loop) readStep(now time.Time) {
	d, s := l.d, l.reads
	if d.Sprint == nil || d.readSlots() <= 0 {
		return
	}
	at := now.UTC().Format(time.RFC3339)
	if s.askedAt.IsZero() || now.Sub(s.askedAt) >= ReadAskEvery {
		s.askedAt = now
		out, err := d.Sprint(l.ctx, ReadQueueArgv(d.Friend))
		if err != nil {
			d.Record(fmt.Sprintf("%s reads: the reader queue: %s", at, oneLine(err.Error(), 300)))
			s.asked = nil
		} else if s.asked, err = ParseReadQueue(out); err != nil {
			d.Record(fmt.Sprintf("%s reads: %s", at, err))
		} else if len(s.asked) > 0 {
			s.epoch = s.asked[0].Epoch
		}
	}
	if l.lanes.gov.Held() != "" || l.lanes.gov.Paused(now) || d.machineStopped() {
		return // nothing begins while STOPPED (NoLaunchAfterStop)
	}
	for _, r := range s.asked {
		if len(s.running) >= d.readSlots() {
			return
		}
		if s.running[r.ID] || s.begun[r.ID] || d.machineStopped() {
			continue
		}
		out, err := d.Sprint(l.ctx, ReadBeginArgv(d.Friend, r))
		if err != nil || !strings.Contains(out, "OK") {
			why := out
			if err != nil {
				why = err.Error()
			}
			d.Record(fmt.Sprintf("%s read %s: begin refused: %s", at, r.ID, oneLine(why, 300)))
			s.begun[r.ID] = true // not begun again by this daemon until the queue stops asking it
			continue
		}
		s.running[r.ID], s.begun[r.ID] = true, true
		l.startRead(r, now)
	}
	// a read the queue no longer asks is forgotten, so a read asked again is begun again
	for id := range s.begun {
		if !s.running[id] && !containsRead(s.asked, id) {
			delete(s.begun, id)
		}
	}
	// a bench copy owed removal is tried until it succeeds, whatever the lanes do
	if l.ctx.Err() == nil {
		l.retryBenchCleanup(now)
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
	if verdict != "" {
		out, err := d.Sprint(l.ctx, ReadVerdictArgv(d.Friend, r.read, verdict, finding, usage))
		d.Record(fmt.Sprintf("%s read %s: verdict=%s wall=%s: %s", at, r.read.ID, verdict, now.Sub(r.start).Round(time.Second), recorded(out, err)))
		if err == nil {
			l.releaseRead(r.read.ID, now)
		}
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
	out, err := d.Sprint(l.ctx, ReadReturnArgv(d.Friend, r.read, why, usage))
	d.Record(fmt.Sprintf("%s read %s: returned: %s: %s", at, r.read.ID, oneLine(why, 200), recorded(out, err)))
}

// releaseRead removes the read once its finding is recorded, whatever the finding: the
// local checkout, the bench copy under BenchRoot when that root is set, and the bench copy
// on the Linux bench the read's RESULT.md names (readBenchDir on the `bench:` host), reached
// over Bench (ssh) and removed with the same authenticated line the reader ran (docs/SPEC-
// FRIEND.md, what is scratch). The read's own files (READ.md, the brief, the worker's
// report, RESULT.md) stay, so the bench it names stays recorded until it is gone. A path
// that is not there is nothing to remove. A bench the daemon cannot reach yet is not
// dropped: it is enqueued and tried again each step until it succeeds.
func (l *loop) releaseRead(id string, now time.Time) {
	d := l.d
	if !validJob(id) {
		return
	}
	dir := filepath.Join(d.Dir, "reads", id)
	if err := safepath.RemoveUnder(dir, filepath.Join(dir, "repo")); err != nil {
		d.Record(fmt.Sprintf("%s read %s: not removed reads/%s/repo: %s", now.UTC().Format(time.RFC3339), id, id, oneLine(err.Error(), 200)))
	}
	if d.BenchRoot != "" && validJob(d.Friend) {
		bench := filepath.Join(d.BenchRoot, "buds", d.Friend, "reads", id)
		if err := safepath.RemoveUnder(d.BenchRoot, bench); err != nil {
			d.Record(fmt.Sprintf("%s read %s: not removed bench reads/%s: %s", now.UTC().Format(time.RFC3339), id, id, oneLine(err.Error(), 200)))
		}
	}
	if d.Bench != nil && validJob(d.Friend) && l.readBenchHost(id) != "" {
		l.removeBenchCopy(id, now)
	}
}

// readBenchHost is the host the read's RESULT.md names (`bench:` line), "" when it names
// none or no host. The read's own files stay after the finding is recorded, so the host the
// reader actually copied to is what is removed, never a guess.
func (l *loop) readBenchHost(id string) string {
	raw, _ := os.ReadFile(filepath.Join(l.d.Dir, "reads", id, "RESULT.md"))
	return ReadBenchHost(string(raw))
}

// removeBenchCopy removes the read's bench copy on the bench its RESULT.md named, enqueuing
// the owed cleanup when the bench does not answer so the next step tries again until it
// succeeds. A removal that reports no bench, or no transport, has nothing to reach.
func (l *loop) removeBenchCopy(id string, now time.Time) {
	d, s := l.d, l.reads
	host, path := l.readBenchHost(id), readBenchDir(d.Friend, id)
	ctx, cancel := context.WithTimeout(l.ctx, ReadBenchBudget)
	defer cancel()
	code, err := d.Bench.Shell(ctx, host, readBenchRemoval(d.Friend, id), io.Discard, io.Discard)
	if err == nil && code == 0 {
		delete(s.owedBench, id)
		d.Record(fmt.Sprintf("%s read %s: removed bench %s", now.UTC().Format(time.RFC3339), id, host))
		return
	}
	s.owedBench[id] = readBenchCleanup{host: host, path: path, next: now.Add(ReadBenchRetry)}
	why := err
	if err == nil {
		why = fmt.Errorf("rm exit %d", code)
	}
	d.Record(fmt.Sprintf("%s read %s: bench %s not reached, owed removal of %s: %s", now.UTC().Format(time.RFC3339), id, host, path, oneLine(why.Error(), 120)))
}

// retryBenchCleanup tries each owed bench removal whose time has come, dropping the ones
// that succeed. It runs each step, so a bench that answers again is cleaned without a
// person.
func (l *loop) retryBenchCleanup(now time.Time) {
	d, s := l.d, l.reads
	if len(s.owedBench) == 0 || d.Bench == nil {
		return
	}
	for id, c := range s.owedBench {
		if now.Before(c.next) {
			continue
		}
		ctx, cancel := context.WithTimeout(l.ctx, ReadBenchBudget)
		code, err := d.Bench.Shell(ctx, c.host, readBenchRemoval(d.Friend, id), io.Discard, io.Discard)
		cancel()
		if err == nil && code == 0 {
			delete(s.owedBench, id)
			d.Record(fmt.Sprintf("%s read %s: removed bench %s", now.UTC().Format(time.RFC3339), id, c.host))
			continue
		}
		c.next = now.Add(ReadBenchRetry)
		s.owedBench[id] = c
	}
}

func recorded(out string, err error) string {
	if err != nil {
		return "not recorded: " + oneLine(err.Error(), 200)
	}
	return oneLine(out, 120)
}
