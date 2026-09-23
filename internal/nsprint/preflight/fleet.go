// Package preflight is `nova-sprint preflight` (#2756 section 7): one line per
// check, GREEN or RED with its number, and any RED refuses the sprint.
//
// This file holds the fleet checks (#2948): 7.4 the batch launcher, 7.5 bench
// beats, 7.6 one dry batch per bench over one session, 7.8 harvest leases and
// consumer groups, 7.12 table --check, 7.14 the GitHub REST budget, 7.16 two
// schedulers and 7.17 orphan effects. The store checks (7.1, 7.2, 7.3, 7.7,
// 7.10, 7.15) are #2947's.
//
// Every check reads a FleetInput the verb gathers from Redis, the workflow
// files at the dev tip and the configured launcher, so the checks never ssh,
// read GitHub or read a state file themselves; the tests hand them fakes.
// No evidence is not negative evidence: an input the check needs and does not
// have prints MISSING and is RED, never GREEN.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// BatchLauncherName is the one launcher a bench beat may name (7.4).
const BatchLauncherName = "nova-sprint card launch"

// Bounds from #2756 section 7.
const (
	DefaultAckWindow = 5 * time.Second  // 7.6: every dry child acks within 5 s
	BeatFresh        = 2 * time.Second  // 7.5, 7.12: a beat or table file older than 2 s is stale
	PendingMax       = 60 * time.Second // 7.8: consumer group pending entries older than 60 s
)

// FleetLine is one preflight line. Check is the section 7 number ("7.6").
type FleetLine struct {
	Check string
	Name  string
	Red   bool
	What  string // the green summary, or every red reason joined by "; "
}

func (l FleetLine) String() string {
	state := "GREEN"
	if l.Red {
		state = "RED"
	}
	s := state + " " + l.Check + " " + l.Name
	if l.What != "" {
		s += ": " + l.What
	}
	return s
}

// FleetRed reports whether any line is red; the verb exits 1 on it.
func FleetRed(lines []FleetLine) bool {
	for _, l := range lines {
		if l.Red {
			return true
		}
	}
	return false
}

// BenchState is a registered bench as the store holds it: its desired slots,
// pause, and the fields of its beat.
type BenchState struct {
	Name         string
	Desired      int           // bench:<b>:desired slots, written by capacity
	Paused       bool          // an explicit pause; a paused bench may have no beat
	BeatPresent  bool          // bench:<b>:beat exists
	BeatAge      time.Duration // now - beat at
	Launcher     string        // the beat's launcher field
	HarvestLease bool          // the bench's harvest worker lease is held
}

// Up is a bench the dealer would deal to: a fresh beat and no pause.
func (b BenchState) Up() bool { return !b.Paused && b.BeatPresent && b.BeatAge <= BeatFresh }

// Dialer opens one ssh session to a bench. The deal pass is the only opener,
// and preflight hands the launcher a counting Dialer so the count is
// preflight's, not the launcher's own report.
type Dialer interface {
	Dial(ctx context.Context, bench string) (Session, error)
}

// Session is one open session; LaunchDry starts one dry child on it and
// returns when the child acks.
type Session interface {
	LaunchDry(ctx context.Context, card string) error
	Close() error
}

// BatchLauncher is the configured launcher in dry mode (`card launch --dry`).
type BatchLauncher interface {
	LaunchBatch(ctx context.Context, bench string, cards []string, d Dialer) error
}

// ConsumerGroup is one consumer group's oldest pending entry (XPENDING).
type ConsumerGroup struct {
	Name          string
	OldestPending time.Duration
}

// RESTBudget is the GitHub REST rate budget and pr-to-read's use of it.
type RESTBudget struct {
	Known        bool // the budget was read; false prints MISSING
	Remaining    int
	CallsPerPass int           // REST calls one pr-to-read pass makes
	Cadence      time.Duration // pr-to-read's pass interval
}

// BenchProfile is a registered bench profile: its OS and the legs it runs.
type BenchProfile struct {
	Bench string
	OS    string // linux or darwin
	Legs  []string
}

// Workflow is one .github/workflows file of a sprint repo at the dev tip.
type Workflow struct {
	Repo string
	Path string
	Body string
}

// HeadRecord is a review-ready PR at its head and what vouches for its CI.
type HeadRecord struct {
	Repo       string
	PR         int
	Head       string
	CICard     bool // a ci card exists for ci:<repo>:<head>
	RunnerOnly bool // a runner-only record exists for the head
}

// LandReceipt is a live PR's land-ready receipt and the source it names.
type LandReceipt struct {
	Repo   string
	PR     int
	Head   string
	Source string // must be the ci:<repo>:<head> key
}

// OrphanCard is a card in orphan-effect.
type OrphanCard struct {
	ID         string
	Age        time.Duration
	Unresolved int // unresolved items the card still names
}

// EndedCard is a card in ended(DONE) and its receipt reason.
type EndedCard struct {
	ID     string
	Reason string
}

// FleetInput is everything the fleet checks read.
type FleetInput struct {
	Benches []BenchState

	Dialer    Dialer
	Launcher  BatchLauncher
	AckWindow time.Duration // 0 means DefaultAckWindow

	Consumers []ConsumerGroup

	TableCheck       func(ctx context.Context) error // table --check on its fixture
	TableFilePresent bool
	TableFileAge     time.Duration

	REST RESTBudget

	Profiles    []BenchProfile
	Workflows   []Workflow
	RunnerRows  []string // policy runner_rows: "<repo>:<job>" or "<job>"
	ReviewReady []HeadRecord
	LandReady   []LandReceipt

	Orphans     []OrphanCard
	OrphanGrace time.Duration
	EndedDone   []EndedCard
}

// FleetChecks runs every fleet check and returns the lines in check order.
func FleetChecks(ctx context.Context, in FleetInput) []FleetLine {
	return []FleetLine{
		CheckBatchLauncher(in),
		CheckBeats(in),
		CheckOneSessionPerBatch(ctx, in),
		CheckHarvestAndConsumers(in),
		CheckTable(ctx, in),
		CheckRESTBudget(in),
		CheckTwoSchedulers(in),
		CheckOrphanEffects(in),
	}
}

func line(check, name string, reds []string, green string) FleetLine {
	if len(reds) > 0 {
		return FleetLine{Check: check, Name: name, Red: true, What: strings.Join(reds, "; ")}
	}
	return FleetLine{Check: check, Name: name, What: green}
}

func secs(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

func sortedBenches(in []BenchState) []BenchState {
	out := append([]BenchState(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CheckBatchLauncher is 7.4: every beat names the batch launcher.
func CheckBatchLauncher(in FleetInput) FleetLine {
	var reds []string
	n := 0
	for _, b := range sortedBenches(in.Benches) {
		if !b.BeatPresent {
			continue // 7.5 reds a missing beat
		}
		n++
		switch b.Launcher {
		case BatchLauncherName:
		case "":
			reds = append(reds, oneline.Escape(b.Name)+" launcher MISSING")
		default:
			reds = append(reds, oneline.Escape(b.Name)+" launcher "+oneline.Escape(b.Launcher))
		}
	}
	return line("7.4", "batch launcher", reds, fmt.Sprintf("%d beats name %s", n, BatchLauncherName))
}

// CheckBeats is 7.5: a registered bench's beat is fresh, or it is paused.
func CheckBeats(in FleetInput) FleetLine {
	var reds []string
	up := 0
	for _, b := range sortedBenches(in.Benches) {
		switch {
		case b.Paused:
		case !b.BeatPresent:
			reds = append(reds, oneline.Escape(b.Name)+" beat missing without a pause")
		case b.BeatAge > BeatFresh:
			reds = append(reds, oneline.Escape(b.Name)+" beat "+secs(b.BeatAge)+" old")
		default:
			up++
		}
	}
	return line("7.5", "bench beats", reds, fmt.Sprintf("%d of %d benches up, the rest paused", up, len(in.Benches)))
}

// CheckOneSessionPerBatch is 7.6: one dry batch per UP bench, as wide as its
// desired slots, through the configured launcher. Green when exactly one
// session carries each batch and every dry child acks inside the window; red
// when the launcher opened more than one session, or sshd refused or timed
// out the one session, or a child did not ack.
func CheckOneSessionPerBatch(ctx context.Context, in FleetInput) FleetLine {
	const check, name = "7.6", "one session per batch"
	if in.Launcher == nil || in.Dialer == nil {
		return line(check, name, []string{"launcher or dialer MISSING; no dry batch ran"}, "")
	}
	window := in.AckWindow
	if window <= 0 {
		window = DefaultAckWindow
	}
	var benches []BenchState
	for _, b := range sortedBenches(in.Benches) {
		if b.Up() && b.Desired > 0 {
			benches = append(benches, b)
		}
	}
	results := make([]dryResult, len(benches))
	var wg sync.WaitGroup
	for i, b := range benches {
		wg.Add(1)
		go func(i int, b BenchState) {
			defer wg.Done()
			results[i] = dryBatch(ctx, in.Launcher, in.Dialer, b, window)
		}(i, b)
	}
	wg.Wait()

	var reds []string
	cards, sessions := 0, 0
	for i, r := range results {
		cards += r.cards
		sessions += r.sessions
		if why := r.red(window); why != "" {
			reds = append(reds, oneline.Escape(benches[i].Name)+": "+why)
		}
	}
	return line(check, name, reds, fmt.Sprintf("%d benches, %d dry cards over %d sessions", len(benches), cards, sessions))
}

type dryResult struct {
	cards    int
	sessions int
	acked    int
	dialErr  error
	err      error
	timedOut bool
}

func (r dryResult) red(window time.Duration) string {
	var why []string
	if r.sessions > 1 {
		why = append(why, fmt.Sprintf("%d sessions for %d cards", r.sessions, r.cards))
	}
	switch {
	case r.dialErr != nil:
		why = append(why, oneline.Err(r.dialErr))
	case r.timedOut:
		why = append(why, fmt.Sprintf("timed out after %s with %d of %d acked", window, r.acked, r.cards))
	case r.err != nil:
		why = append(why, "launcher: "+oneline.Err(r.err))
	}
	if len(why) == 0 && r.acked < r.cards {
		why = append(why, fmt.Sprintf("%d of %d dry children acked", r.acked, r.cards))
	}
	return strings.Join(why, ", ")
}

func dryBatch(parent context.Context, l BatchLauncher, d Dialer, b BenchState, window time.Duration) dryResult {
	ctx, cancel := context.WithTimeout(parent, window)
	defer cancel()
	cards := make([]string, b.Desired)
	for i := range cards {
		cards[i] = fmt.Sprintf("dry-%s-%d", b.Name, i+1)
	}
	cd := &countingDialer{d: d, acked: map[string]bool{}}
	done := make(chan error, 1)
	go func() { done <- l.LaunchBatch(ctx, b.Name, cards, cd) }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err() // a launcher that ignores the deadline is not waited for
	}
	cd.mu.Lock()
	defer cd.mu.Unlock()
	r := dryResult{cards: len(cards), sessions: cd.sessions, acked: len(cd.acked), dialErr: cd.dialErr, err: err}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) && r.acked < r.cards {
		r.timedOut = true
	}
	return r
}

// countingDialer counts every session the launcher opens and every dry child
// that acks, whatever the launcher reports about itself.
type countingDialer struct {
	d        Dialer
	mu       sync.Mutex
	sessions int
	acked    map[string]bool
	dialErr  error
}

func (c *countingDialer) Dial(ctx context.Context, bench string) (Session, error) {
	c.mu.Lock()
	c.sessions++
	c.mu.Unlock()
	s, err := c.d.Dial(ctx, bench)
	if err != nil {
		c.mu.Lock()
		if c.dialErr == nil {
			c.dialErr = err
		}
		c.mu.Unlock()
		return nil, err
	}
	return &countingSession{s: s, c: c}, nil
}

type countingSession struct {
	s Session
	c *countingDialer
}

func (s *countingSession) LaunchDry(ctx context.Context, card string) error {
	err := s.s.LaunchDry(ctx, card)
	if err == nil && ctx.Err() == nil {
		s.c.mu.Lock()
		s.c.acked[card] = true
		s.c.mu.Unlock()
	}
	return err
}

func (s *countingSession) Close() error { return s.s.Close() }

// CheckHarvestAndConsumers is 7.8: every UP bench holds a harvest worker
// lease and no consumer group has an entry pending past 60 s.
func CheckHarvestAndConsumers(in FleetInput) FleetLine {
	var reds []string
	for _, b := range sortedBenches(in.Benches) {
		if b.Up() && !b.HarvestLease {
			reds = append(reds, oneline.Escape(b.Name)+" has no harvest worker lease")
		}
	}
	groups := append([]ConsumerGroup(nil), in.Consumers...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	for _, g := range groups {
		if g.OldestPending > PendingMax {
			reds = append(reds, fmt.Sprintf("%s pending %ds", oneline.Escape(g.Name), int(g.OldestPending.Seconds())))
		}
	}
	return line("7.8", "harvest and consumers", reds, fmt.Sprintf("%d consumer groups under %s", len(groups), PendingMax))
}

// CheckTable is 7.12: table --check passes and the renderer's file is fresh.
func CheckTable(ctx context.Context, in FleetInput) FleetLine {
	var reds []string
	if in.TableCheck == nil {
		reds = append(reds, "table --check MISSING")
	} else if err := in.TableCheck(ctx); err != nil {
		reds = append(reds, "table --check: "+oneline.Err(err))
	}
	switch {
	case !in.TableFilePresent:
		reds = append(reds, "table file MISSING")
	case in.TableFileAge > BeatFresh:
		reds = append(reds, "table file "+secs(in.TableFileAge)+" old")
	}
	return line("7.12", "table", reds, "table --check passes, file "+secs(in.TableFileAge)+" old")
}

// CheckRESTBudget is 7.14: the REST budget covers one hour of pr-to-read.
func CheckRESTBudget(in FleetInput) FleetLine {
	const check, name = "7.14", "REST budget"
	r := in.REST
	if !r.Known {
		return line(check, name, []string{"REST budget MISSING"}, "")
	}
	if r.CallsPerPass <= 0 || r.Cadence <= 0 {
		return line(check, name, []string{"pr-to-read calls per pass or cadence MISSING"}, "")
	}
	need := r.CallsPerPass * int(math.Ceil(float64(time.Hour)/float64(r.Cadence)))
	if r.Remaining < need {
		return line(check, name, []string{fmt.Sprintf("REST remaining %d < %d (one hour of pr-to-read at %d calls per %s)",
			r.Remaining, need, r.CallsPerPass, r.Cadence)}, "")
	}
	return line(check, name, nil, fmt.Sprintf("REST remaining %d >= %d for one hour of pr-to-read", r.Remaining, need))
}

// CheckTwoSchedulers is 7.16 (#2756 10.8): no workflow row runs what a
// registered bench profile can run, every review-ready head has a ci card or
// a runner-only record, and no land-ready came from a check-run.
func CheckTwoSchedulers(in FleetInput) FleetLine {
	var reds []string
	carriers := map[string][]string{} // os -> benches carrying the go leg
	for _, p := range in.Profiles {
		for _, leg := range p.Legs {
			if leg == "go" {
				carriers[p.OS] = append(carriers[p.OS], p.Bench)
			}
		}
	}
	for os := range carriers {
		sort.Strings(carriers[os])
	}
	exempt := map[string]bool{}
	for _, r := range in.RunnerRows {
		exempt[r] = true
	}
	rows := 0
	for _, w := range in.Workflows {
		for _, j := range scanWorkflow(w.Body) {
			if !j.goRow || exempt[j.id] || exempt[w.Repo+":"+j.id] {
				continue
			}
			rows++
			where := oneline.Escape(w.Repo) + " " + oneline.Escape(w.Path) + " job " + oneline.Escape(j.id)
			if j.unresolved {
				reds = append(reds, where+" runs-on unresolved; make its labels literal or name it in runner_rows")
				continue
			}
			for _, os := range j.oses {
				if bs := carriers[os]; len(bs) > 0 {
					reds = append(reds, where+" runs "+os+" go on a runner while "+strings.Join(bs, ",")+" carries the leg")
				}
			}
		}
	}
	for _, h := range in.ReviewReady {
		if !h.CICard && !h.RunnerOnly {
			reds = append(reds, fmt.Sprintf("%s#%d at %s has no ci card and no runner-only record",
				oneline.Escape(h.Repo), h.PR, oneline.Escape(h.Head)))
		}
	}
	for _, l := range in.LandReady {
		if l.Source != "ci:"+l.Repo+":"+l.Head {
			src := l.Source
			if src == "" {
				src = "MISSING"
			}
			reds = append(reds, fmt.Sprintf("%s#%d at %s land-ready from %s, not ci:%s:%s",
				oneline.Escape(l.Repo), l.PR, oneline.Escape(l.Head), oneline.Escape(src), oneline.Escape(l.Repo), oneline.Escape(l.Head)))
		}
	}
	return line("7.16", "two schedulers", reds, fmt.Sprintf("%d go rows on runners, none a bench leg; %d review-ready, %d land-ready from ci keys",
		rows, len(in.ReviewReady), len(in.LandReady)))
}

// CheckOrphanEffects is 7.17: no card sits in orphan-effect past the grace
// with nothing unresolved, and no ended(DONE) came from `reconciled`.
func CheckOrphanEffects(in FleetInput) FleetLine {
	var reds []string
	if len(in.Orphans) > 0 && in.OrphanGrace <= 0 {
		reds = append(reds, "orphan_grace MISSING")
	}
	for _, o := range in.Orphans {
		if in.OrphanGrace > 0 && o.Age > in.OrphanGrace && o.Unresolved == 0 {
			reds = append(reds, fmt.Sprintf("%s orphan-effect %ds with no unresolved item", oneline.Escape(o.ID), int(o.Age.Seconds())))
		}
	}
	for _, e := range in.EndedDone {
		if e.Reason == "reconciled" {
			reds = append(reds, oneline.Escape(e.ID)+" ended(DONE) by reconciled")
		}
	}
	return line("7.17", "orphan effects", reds, fmt.Sprintf("%d in orphan-effect inside grace or with items", len(in.Orphans)))
}

// workflowJob is one job of a workflow as the scanner reads it.
type workflowJob struct {
	id         string
	goRow      bool     // a step runs the go toolchain
	oses       []string // linux, darwin: the bench-runnable rows it runs on
	unresolved bool     // runs-on is an expression whose OS cannot be read
}

var (
	goStep  = regexp.MustCompile(`\bgo (test|build|vet|run|install|version|env|list|mod|generate)\b|actions/setup-go@`)
	osToken = regexp.MustCompile(`(?i)\b(ubuntu-[\w.]+|macos-[\w.]+|windows-[\w.]+)\b`)
	keyLine = regexp.MustCompile(`^( *)([A-Za-z0-9_.-]+):(.*)$`)
)

// scanWorkflow reads the jobs of a workflow file with an indentation scan:
// no YAML library is in the module, and the check needs only each job's id,
// its runs-on labels (or the matrix's literal runner names when runs-on is an
// expression) and whether a step runs go.
func scanWorkflow(body string) []workflowJob {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var jobs []workflowJob
	inJobs, jobIndent := false, -1
	var cur []string
	var id string
	flush := func() {
		if id != "" {
			jobs = append(jobs, readJob(id, cur))
		}
		id, cur = "", nil
	}
	for _, raw := range lines {
		text := raw
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(text) - len(strings.TrimLeft(text, " "))
		if indent == 0 {
			flush()
			inJobs = strings.HasPrefix(text, "jobs:")
			jobIndent = -1
			continue
		}
		if !inJobs {
			continue
		}
		if jobIndent < 0 {
			jobIndent = indent
		}
		if indent == jobIndent {
			if m := keyLine.FindStringSubmatch(text); m != nil {
				flush()
				id = m[2]
				continue
			}
		}
		if id != "" {
			cur = append(cur, text)
		}
	}
	flush()
	return jobs
}

func readJob(id string, block []string) workflowJob {
	j := workflowJob{id: id}
	var runsOn []string
	for i := 0; i < len(block); i++ {
		t := stripComment(block[i])
		if goStep.MatchString(t) {
			j.goRow = true
		}
		trimmed := strings.TrimSpace(t)
		if !strings.HasPrefix(trimmed, "runs-on:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(trimmed, "runs-on:"))
		if val != "" {
			runsOn = append(runsOn, splitLabels(val)...)
			continue
		}
		base := len(t) - len(strings.TrimLeft(t, " "))
		for i+1 < len(block) {
			next := stripComment(block[i+1])
			ind := len(next) - len(strings.TrimLeft(next, " "))
			nt := strings.TrimSpace(next)
			if ind <= base || !strings.HasPrefix(nt, "- ") {
				break
			}
			runsOn = append(runsOn, splitLabels(strings.TrimPrefix(nt, "- "))...)
			i++
		}
	}
	oses := map[string]bool{}
	expr := false
	runnerOnly := false
	for _, l := range runsOn {
		if strings.Contains(l, "${{") {
			expr = true
			continue
		}
		switch os := labelOS(l); os {
		case "":
		case "runner-only":
			runnerOnly = true
		default:
			oses[os] = true
		}
	}
	if expr && len(oses) == 0 && !runnerOnly {
		for _, t := range block {
			for _, m := range osToken.FindAllString(stripComment(t), -1) {
				if os := labelOS(m); os == "linux" || os == "darwin" {
					oses[os] = true
				}
			}
		}
		if len(oses) == 0 {
			j.unresolved = true
		}
	}
	if runnerOnly {
		oses = map[string]bool{}
	}
	for os := range oses {
		j.oses = append(j.oses, os)
	}
	sort.Strings(j.oses)
	return j
}

// labelOS maps a runs-on label to the bench OS it names, "runner-only" for a
// row no bench runs (s390x), or "" for a label that names no OS.
func labelOS(label string) string {
	l := strings.ToLower(strings.Trim(label, `"' `))
	switch {
	case l == "s390x":
		return "runner-only"
	case l == "linux" || strings.HasPrefix(l, "ubuntu"):
		return "linux"
	case l == "macos" || l == "darwin" || strings.HasPrefix(l, "macos-"):
		return "darwin"
	case l == "windows" || strings.HasPrefix(l, "windows"):
		return "windows"
	}
	return ""
}

func splitLabels(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${{") {
		return []string{v}
	}
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return s[:i]
	}
	return s
}
