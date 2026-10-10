package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// The process edges around Next. Each one shells out or starts a child.
// docs/SPEC-RUNNER.md.

const reportLimit = 2000

var (
	sha40        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,80}$`)
	statusBranch = regexp.MustCompile(`push your work to the branch ([^\s;]+)`)
)

// Row is the friend's nova-config row, the fields the runner reads.
type Row struct {
	Name          string   `json:"name,omitempty"`
	Width         Width    `json:"width,omitempty"`
	Tiers         []string `json:"tiers,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	RunnerVersion string   `json:"runner_version,omitempty"`
}

// Proc is a lane's harness. Exited is non-blocking.
type Proc interface {
	Exited() (exited bool, errLine string)
	PID() int
	LogPath() string
}

// Edges are the process. Tests pass fakes; main wires the commands.
type Edges struct {
	Show    func(ctx context.Context, friend string) (Row, error)
	Queue   func(ctx context.Context, friend string) ([]Card, error)
	Claim   func(ctx context.Context, card Card) error
	Start   func(ctx context.Context, card Card) (Proc, error)
	Report  func(card Card) (string, bool)
	Finish  func(ctx context.Context, card Card, failed bool, head, report string) error
	Read    func(ctx context.Context, card Card, verdict, finding string) error
	Beat    func(ctx context.Context, friend string, beat Beat) error
	Judge   func(ctx context.Context, subject, body string) error
	Install func(ctx context.Context, version string) error
	Exec    func(version string) error
}

// Runner is one friend's loop. Dir empty skips the state file, which is how
// the tests drive a tick.
type Runner struct {
	Friend  string
	Dir     string
	Harness string
	Seat    string
	Version string
	Models  map[string]string
	Edges   Edges
	Out     io.Writer
	Err     io.Writer

	lanes        Lanes
	mem          Memory
	row          Row
	rowAt        time.Time
	procs        map[string]procSlot
	cards        map[string]Card
	pending      []pendingFinish
	pendingReads []pendingRead
	startErr     map[string]string
	drained      string
	loaded       bool
}

type procSlot struct {
	proc Proc
}

type pendingFinish struct {
	Card   Card   `json:"card"`
	Failed bool   `json:"failed"`
	Head   string `json:"head,omitempty"`
	Report string `json:"report"`
}

type pendingRead struct {
	Card    Card   `json:"card"`
	Verdict string `json:"verdict"`
	Finding string `json:"finding"`
	Report  string `json:"report"`
}

type diskLane struct {
	Card Card   `json:"card"`
	PID  int    `json:"pid"`
	Log  string `json:"log,omitempty"`
}

type diskState struct {
	Lanes        []diskLane      `json:"lanes,omitempty"`
	Pending      []pendingFinish `json:"pending,omitempty"`
	PendingReads []pendingRead   `json:"pending_reads,omitempty"`
	Fault        string          `json:"fault,omitempty"`
	Count        int             `json:"fault_count,omitempty"`
	Judged       bool            `json:"judged,omitempty"`
	Row          Row             `json:"row"`
	RowAt        time.Time       `json:"row_at,omitempty"`
}

// Run ticks immediately and then every second until ctx ends. Children are
// left running: each is its own session leader.
func (r *Runner) Run(ctx context.Context) error {
	now := time.Now
	if err := r.Tick(ctx, now()); err != nil {
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case t := <-tick.C:
			if err := r.Tick(ctx, t); err != nil {
				r.fail("tick", err)
			}
		}
	}
}

// Tick is one pass: the row at most every RowEvery, then reap, decide, finish,
// start, beat, and follow. now is the caller's clock.
func (r *Runner) Tick(ctx context.Context, now time.Time) error {
	r.boot()
	if RowDue(r.rowAt, now) && r.Edges.Show != nil {
		row, err := r.Edges.Show(ctx, r.Friend)
		if err != nil {
			r.fail("show", err)
		} else {
			r.row = row
			r.rowAt = now
		}
	}
	var queue []Card
	if r.Edges.Queue != nil {
		cards, err := r.Edges.Queue(ctx, r.Friend)
		if err != nil {
			r.fail("queue", err)
		} else {
			queue = cards
		}
	}
	ends := r.reap()
	step, mem := Next(World{
		Friend:        r.Friend,
		Lanes:         r.lanes,
		Width:         r.row.Width,
		Queue:         queue,
		Block:         r.blockIDs(),
		BinaryVersion: r.Version,
		RunnerVersion: r.row.RunnerVersion,
		Ended:         ends,
		Fault:         r.mem.Fault,
		FaultCount:    r.mem.FaultCount,
		Judged:        r.mem.Judged,
	})
	r.mem = mem
	for _, e := range ends {
		card := r.cards[e.ID]
		if card.ID == "" {
			card.ID = e.ID
		}
		if e.HasReport {
			if card.Kind == KindRead {
				r.enqueueRead(card, e.Verdict, e.Finding, e.Report)
			} else {
				failed, head := landOf(e.Verdict, e.Head)
				r.enqueueFinish(card, failed, head, e.Report)
			}
			continue
		}
	}
	for _, f := range step.Fail {
		card := r.cards[f.ID]
		if card.ID == "" {
			card.ID = f.ID
		}
		if card.Kind == KindRead {
			r.enqueueRead(card, "return", f.Report, "")
		} else {
			r.enqueueFinish(card, true, "", f.Report)
		}
	}
	r.flushPending(ctx)
	r.flushReads(ctx)
	if step.Judgment != nil && r.Edges.Judge != nil {
		if err := r.Edges.Judge(ctx, step.Judgment.Subject, step.Judgment.Body); err != nil {
			r.mem.Judged = false
			r.fail("judgment", err)
		} else {
			r.ok("RUNNER JUDGMENT subject=%s", oneLine(step.Judgment.Subject, 200))
		}
	}
	var dropped []string
	for _, c := range step.Start {
		if r.Edges.Start == nil {
			dropped = append(dropped, c.ID)
			r.fail("start "+c.ID, fmt.Errorf("no start edge"))
			continue
		}
		claimed := false
		if r.Edges.Claim != nil {
			if err := r.Edges.Claim(ctx, c); err != nil {
				r.fail("claim "+c.ID, err)
				dropped = append(dropped, c.ID)
				continue
			}
			claimed = true
			r.ok("RUNNER CLAIM card=%s kind=%s", c.ID, c.Kind)
		}
		proc, err := r.Edges.Start(ctx, c)
		if err != nil {
			if r.startErr == nil {
				r.startErr = map[string]string{}
			}
			if r.startErr[c.ID] != err.Error() {
				r.startErr[c.ID] = err.Error()
				r.fail("start "+c.ID, err)
			}
			if claimed {
				fault := HarnessFault(err.Error())
				if c.Kind == KindRead {
					r.enqueueRead(c, "return", fault, "")
				} else {
					r.enqueueFinish(c, true, "", fault)
				}
			}
			dropped = append(dropped, c.ID)
			continue
		}
		delete(r.startErr, c.ID)
		r.procs[c.ID] = procSlot{proc: proc}
		r.cards[c.ID] = c
		r.ok("RUNNER START card=%s kind=%s", c.ID, c.Kind)
	}
	r.flushPending(ctx)
	r.flushReads(ctx)
	r.lanes = step.Lanes.Without(dropped...)
	beat := BeatOf(r.lanes, queue, r.row.Width)
	if r.Edges.Beat != nil {
		if err := r.Edges.Beat(ctx, r.Friend, beat); err != nil {
			r.fail("beat", err)
		} else {
			r.ok("RUNNER OK beat working=%d queue=%d width=%d", beat.Working, beat.Queue, beat.Width)
		}
	}
	if step.Drain && r.drained != r.row.RunnerVersion {
		r.drained = r.row.RunnerVersion
		r.ok("RUNNER DRAIN version=%s", r.row.RunnerVersion)
	}
	if !step.Drain {
		r.drained = ""
	}
	r.save()
	if step.Exec != "" && r.lanes.Len() == 0 {
		if r.Edges.Install != nil {
			if err := r.Edges.Install(ctx, step.Exec); err != nil {
				r.fail("install", err)
				return nil
			}
		}
		r.ok("RUNNER EXEC version=%s", step.Exec)
		if r.Edges.Exec != nil {
			if err := r.Edges.Exec(step.Exec); err != nil {
				r.fail("exec", err)
			}
		}
	}
	return nil
}

func (r *Runner) blockIDs() []string {
	out := make([]string, 0, len(r.pending)+len(r.pendingReads))
	for _, p := range r.pending {
		out = append(out, p.Card.ID)
	}
	for _, p := range r.pendingReads {
		out = append(out, p.Card.ID)
	}
	return out
}

func (r *Runner) enqueueFinish(card Card, failed bool, head, report string) {
	if card.ID == "" {
		return
	}
	item := pendingFinish{Card: card, Failed: failed, Head: head, Report: report}
	for i, p := range r.pending {
		if p.Card.ID == card.ID {
			r.pending[i] = item
			return
		}
	}
	r.pending = append(r.pending, item)
}

func (r *Runner) flushPending(ctx context.Context) {
	if len(r.pending) == 0 {
		return
	}
	var left []pendingFinish
	for _, p := range r.pending {
		if r.Edges.Finish == nil {
			left = append(left, p)
			continue
		}
		if err := r.Edges.Finish(ctx, p.Card, p.Failed, p.Head, p.Report); err != nil {
			r.fail("finish "+p.Card.ID, err)
			left = append(left, p)
			continue
		}
		r.ok("RUNNER FINISH card=%s failed=%t", p.Card.ID, p.Failed)
	}
	r.pending = left
}

func (r *Runner) enqueueRead(card Card, verdict, finding, report string) {
	if card.ID == "" {
		return
	}
	item := pendingRead{Card: card, Verdict: verdict, Finding: finding, Report: report}
	for i, p := range r.pendingReads {
		if p.Card.ID == card.ID {
			r.pendingReads[i] = item
			return
		}
	}
	r.pendingReads = append(r.pendingReads, item)
}

func (r *Runner) flushReads(ctx context.Context) {
	if len(r.pendingReads) == 0 {
		return
	}
	var left []pendingRead
	for _, p := range r.pendingReads {
		if r.Edges.Read == nil {
			left = append(left, p)
			continue
		}
		if err := r.Edges.Read(ctx, p.Card, p.Verdict, p.Finding); err != nil {
			r.fail("read "+p.Card.ID, err)
			left = append(left, p)
			continue
		}
		r.ok("RUNNER READ card=%s verdict=%s", p.Card.ID, p.Verdict)
	}
	r.pendingReads = left
}

func (r *Runner) reap() []End {
	if len(r.procs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.procs))
	for id := range r.procs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var ends []End
	for _, id := range ids {
		slot := r.procs[id]
		if slot.proc == nil {
			delete(r.procs, id)
			continue
		}
		exited, line := slot.proc.Exited()
		if !exited {
			continue
		}
		delete(r.procs, id)
		card := r.cards[id]
		if r.Edges.Report != nil {
			if text, ok := r.Edges.Report(card); ok && strings.TrimSpace(text) != "" {
				if card.Kind == KindRead {
					if verdict, finding, parsed := parseReadReport(text); parsed {
						ends = append(ends, End{ID: id, HasReport: true, Verdict: verdict, Finding: finding, Report: text})
						continue
					}
					line = "read report has no verdict"
				} else if verdict, head, parsed := parseReport(text); parsed {
					ends = append(ends, End{ID: id, HasReport: true, Verdict: verdict, Head: head, Report: text})
					continue
				} else {
					line = "report has no verdict"
				}
			}
		}
		ends = append(ends, End{ID: id, ErrLine: line})
	}
	return ends
}

func (r *Runner) ok(format string, args ...any) {
	if r.Out == nil {
		return
	}
	fmt.Fprintf(r.Out, format+"\n", args...)
}

func (r *Runner) fail(what string, err error) {
	if r.Err == nil {
		return
	}
	fmt.Fprintf(r.Err, "RUNNER FAIL %s: %s\n", what, oneLine(err.Error(), 300))
}

func (r *Runner) statePath() string {
	return filepath.Join(r.Dir, "runner", "state.json")
}

func (r *Runner) boot() {
	if r.loaded {
		return
	}
	r.loaded = true
	if r.procs == nil {
		r.procs = map[string]procSlot{}
	}
	if r.cards == nil {
		r.cards = map[string]Card{}
	}
	if r.Dir == "" {
		return
	}
	raw, err := os.ReadFile(r.statePath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.fail("state", err)
		}
		return
	}
	var st diskState
	if err := json.Unmarshal(raw, &st); err != nil {
		r.fail("state", err)
		return
	}
	r.mem = Memory{Fault: st.Fault, FaultCount: st.Count, Judged: st.Judged}
	r.row = st.Row
	r.rowAt = st.RowAt
	r.pending = st.Pending
	r.pendingReads = st.PendingReads
	for _, n := range st.Lanes {
		if n.Card.ID == "" || n.PID <= 0 {
			continue
		}
		kind := n.Card.Kind
		if kind != KindRead {
			kind = KindWork
		}
		r.lanes = r.lanes.Add(Lane{ID: n.Card.ID, Kind: kind})
		r.cards[n.Card.ID] = n.Card
		r.procs[n.Card.ID] = procSlot{proc: pidProc{pid: n.PID, log: n.Log}}
	}
}

func (r *Runner) save() {
	if r.Dir == "" {
		return
	}
	st := diskState{
		Pending:      r.pending,
		PendingReads: r.pendingReads,
		Fault:        r.mem.Fault,
		Count:        r.mem.FaultCount,
		Judged:       r.mem.Judged,
		Row:          r.row,
		RowAt:        r.rowAt,
	}
	for _, n := range r.lanes.List() {
		card := r.cards[n.ID]
		if card.ID == "" {
			card = Card{ID: n.ID, Kind: n.Kind}
		}
		lane := diskLane{Card: card}
		if slot, ok := r.procs[n.ID]; ok && slot.proc != nil {
			lane.PID = slot.proc.PID()
			lane.Log = slot.proc.LogPath()
		}
		if lane.PID <= 0 {
			continue
		}
		st.Lanes = append(st.Lanes, lane)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		r.fail("state", err)
		return
	}
	dir := filepath.Join(r.Dir, "runner")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.fail("state", err)
		return
	}
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		r.fail("state", err)
		return
	}
	if err := os.Rename(tmp, r.statePath()); err != nil {
		r.fail("state", err)
	}
}

func showArgv(friend string) []string {
	return []string{"friend", "show", friend}
}

func queueArgv(friend string) []string {
	return []string{"queue", "--as", "friend." + friend, "--json"}
}

func beatArgv(friend string, b Beat) []string {
	args := []string{"friend", "beat", friend, "--working", strconv.Itoa(b.Working), "--queue", strconv.Itoa(b.Queue)}
	if b.Width >= 1 {
		args = append(args, "--width", strconv.Itoa(b.Width))
	}
	if len(b.Running) > 0 {
		args = append(args, "--running", strings.Join(b.Running, ","))
	}
	return args
}

func finishArgv(friend string, card Card, failed bool, head, report string) []string {
	gen := card.Gen
	if gen < 1 {
		gen = 1
	}
	args := []string{"finish", "--as", "friend." + friend, fmt.Sprintf("%s@%d", card.ID, gen), "--epoch", strconv.FormatUint(card.Epoch, 10)}
	if failed {
		args = append(args, "--failed")
	}
	if head != "" {
		args = append(args, "--head", head)
	}
	args = append(args, "--report", oneLine(report, reportLimit))
	if card.Branch != "" {
		args = append(args, "--branch", card.Branch)
	}
	return args
}

func installArgv(version string) []string {
	return []string{"install", "nova-runner@" + version}
}

func claimArgv(friend string, card Card) []string {
	gen := card.Gen
	if gen < 1 {
		gen = 1
	}
	return []string{"take", "--as", "friend." + friend, fmt.Sprintf("%s@%d", card.ID, gen), "--epoch", strconv.FormatUint(card.Epoch, 10)}
}

func readArgv(friend string, card Card, verdict, finding string) []string {
	gen := card.Gen
	if gen < 1 {
		gen = 1
	}
	args := []string{"read", "--as", "friend." + friend, "--" + verdict, fmt.Sprintf("%s@%d", card.ID, gen), "--epoch", strconv.FormatUint(card.Epoch, 10)}
	if finding != "" {
		if verdict == "return" {
			args = append(args, "--reason", finding)
		} else {
			args = append(args, "--finding", finding)
		}
	}
	return args
}

func judgeArgv(friend, seat, subject, body string) []string {
	return []string{"send", "--as", friend, "--to", seat, "--subject", subject, "--body", body}
}

// parseRow reads `nova-config friend show` text. The row's width is required.
func parseRow(friendName, out string) (Row, error) {
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "FRIEND ") {
			line = l
			break
		}
	}
	if line == "" {
		return Row{}, fmt.Errorf("nova-config friend show %s: no FRIEND line", friendName)
	}
	fields := map[string]string{}
	for _, w := range strings.Fields(line) {
		k, v, ok := strings.Cut(w, "=")
		if ok {
			fields[k] = v
		}
	}
	if name := fields["name"]; name != "" && name != friendName {
		return Row{}, fmt.Errorf("nova-config friend show %s: showed %s", friendName, name)
	}
	row := Row{Name: friendName, Mode: blank(fields["mode"]), Tiers: splitList(fields["tiers"])}
	if v := fields["runner_version"]; v != "" && v != "-" {
		row.RunnerVersion = v
	}
	w := fields["width"]
	if w == "" || w == "-" {
		return Row{}, fmt.Errorf("nova-config friend show %s: the row names no width", friendName)
	}
	n, err := strconv.Atoi(w)
	if err != nil || n < 1 {
		return Row{}, fmt.Errorf("nova-config friend show %s: width %q", friendName, w)
	}
	row.Width = Width(n)
	return row, nil
}

func blank(v string) string {
	if v == "-" {
		return ""
	}
	return v
}

func splitList(v string) []string {
	if v == "" || v == "-" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s != "" && s != "-" {
			out = append(out, s)
		}
	}
	return out
}

// parseQueue reads `nova-sprint queue --as friend.<f> --json`. Cards stay in
// the sprint's order. Only a card in the ready column may be claimed; a working
// card is already claimed by a lane, so the runner does not start it twice.
func parseQueue(out string) ([]Card, error) {
	var body struct {
		Cards []struct {
			ID      string `json:"id"`
			Col     string `json:"col"`
			Gen     int    `json:"gen"`
			Attempt int    `json:"attempt"`
			Packet  *struct {
				Card       string `json:"card"`
				Kind       string `json:"kind"`
				Attempt    int    `json:"attempt"`
				Gen        int    `json:"gen"`
				Epoch      uint64 `json:"epoch"`
				Brief      string `json:"brief"`
				Branch     string `json:"branch"`
				Base       string `json:"base"`
				Repo       string `json:"repo"`
				Model      string `json:"model"`
				Deadline   int    `json:"deadline"`
				Tier       string `json:"tier"`
				Stream     string `json:"stream"`
				Primary    string `json:"primary"`
				Head       string `json:"head"`
				WorkBranch string `json:"work_branch"`
				WorkBase   string `json:"work_base"`
				Report     string `json:"report"`
				ReadJob    string `json:"read_job"`
			} `json:"packet"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("queue: not JSON: %v", err)
	}
	var cards []Card
	for _, c := range body.Cards {
		if c.Col != "ready" || c.ID == "" || c.Packet == nil {
			continue
		}
		p := c.Packet
		gen := c.Gen
		if p.Gen > gen {
			gen = p.Gen
		}
		attempt := c.Attempt
		if p.Attempt > attempt {
			attempt = p.Attempt
		}
		kind := p.Kind
		if kind != KindRead {
			kind = KindWork
		}
		base, sha := splitBase(p.Base)
		if base == "" {
			base, sha = splitBase(headerValue(p.Brief, "BASE"))
		}
		repo := p.Repo
		if repo == "" {
			repo = headerValue(p.Brief, "REPO")
		}
		branch := p.Branch
		if branch == "" {
			if m := statusBranch.FindStringSubmatch(p.Brief); m != nil {
				branch = m[1]
			}
		}
		tier := p.Tier
		if tier == "" {
			tier = headerValue(p.Brief, "tier")
		}
		job := jobNameOf(c.ID, p.Epoch, gen)
		if kind == KindRead && p.ReadJob != "" {
			job = p.ReadJob
			if gen > 1 {
				job += ".g" + strconv.Itoa(gen)
			}
		}
		cards = append(cards, Card{
			ID: c.ID, Kind: kind, Tier: tier, Model: p.Model, Deadline: p.Deadline,
			Job: job, Epoch: p.Epoch, Attempt: attempt, Gen: gen, Brief: p.Brief,
			Repo: repo, Base: base, BaseSha: sha, Branch: branch, Stream: p.Stream, Col: c.Col,
			Primary: p.Primary, Head: p.Head, WorkBranch: p.WorkBranch, WorkBase: p.WorkBase, Report: p.Report,
		})
	}
	return cards, nil
}

func headerValue(brief, key string) string {
	for _, line := range strings.Split(brief, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, key+" :"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func splitBase(base string) (ref, sha string) {
	ref, sha, ok := strings.Cut(base, "@")
	if !ok || !sha40.MatchString(sha) {
		return base, ""
	}
	return ref, sha
}

func parseReport(text string) (verdict, head string, ok bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return "", "", false
	}
	v, found := strings.CutPrefix(strings.TrimSpace(lines[0]), "Verdict:")
	if !found {
		return "", "", false
	}
	verdict = strings.TrimSpace(v)
	if verdict == "" {
		return "", "", false
	}
	if len(lines) > 1 {
		if h, found := strings.CutPrefix(strings.TrimSpace(lines[1]), "Head:"); found {
			head = strings.TrimSpace(h)
		}
	}
	return verdict, head, true
}

// parseReadReport reads a read lane's REPORT.md as the sprint does
// (sprint.ParseFriendReadReport): LAND closes it ok, HOLD closes it broken with
// the finding, and anything else is no verdict.
func parseReadReport(text string) (verdict, finding string, ok bool) {
	v, f, why := sprint.ParseFriendReadReport(text)
	return v, f, why == ""
}

func landOf(verdict, head string) (failed bool, headOut string) {
	if verdict == "LAND" && sha40.MatchString(head) {
		return false, head
	}
	return true, ""
}

func modelOf(card Card, models map[string]string) string {
	if card.Model != "" {
		return card.Model
	}
	if models == nil {
		return ""
	}
	return models[card.Tier]
}

func harnessArgv(harness, model, prompt string) (string, []string) {
	if harness == "claude" {
		args := []string{"-p", prompt}
		if model != "" {
			args = append(args, "--model", model)
		}
		return "claude", args
	}
	args := []string{"run"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)
	return harness, args
}

func promptOf(friendName string, card Card) string {
	job := card.Job
	if job == "" {
		job = card.ID
	}
	if card.Kind == KindRead {
		return fmt.Sprintf("You are %s, a reader, one read only. Read inbox/%s/BRIEF.md and do the read exactly; stop when outbox/%s/REPORT.md is written. Change nothing, commit nothing, push nothing.\n", friendName, job, job)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, one-shot, this card only.\n", friendName)
	fmt.Fprintf(&b, "Read jobs/%s/JOB.md first, then inbox/%s/BRIEF.md.\n", job, job)
	fmt.Fprintf(&b, "Work only in jobs/%s/repo. Write outbox/%s/REPORT.md and outbox/%s/RESULT.md.\n", job, job, job)
	b.WriteString("REPORT.md line 1 is `Verdict: LAND|HOLD|FAIL`. Line 2 is `Head: <40-hex or none>`.\n")
	if card.Deadline > 0 {
		fmt.Fprintf(&b, "This lane is capped at %d seconds.\n", card.Deadline)
	}
	b.WriteString("Do not take any card except this one.\n")
	return b.String()
}

func jobOf(card Card) string {
	if card.Job != "" {
		return card.Job
	}
	return jobNameOf(card.ID, card.Epoch, card.Gen)
}

// jobNameOf is a card's job directory, the name nova-sprint delivers it as
// (friendJobOf): its stored id, .g<gen> from the second generation.
func jobNameOf(id string, epoch uint64, gen int) string {
	job := sprint.StoredID(id, epoch)
	if gen > 1 {
		job += ".g" + strconv.Itoa(gen)
	}
	return job
}

func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if n > 0 && len(s) > n {
		return s[:n]
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

func outputOf(ctx context.Context, name string, args ...string) (string, error) {
	cmd := subproc.Context(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = firstLine(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}

// realEdges wires the commands the loop shells out to.
func realEdges(friendName, dir, harness, seat string, models map[string]string) Edges {
	return Edges{
		Show: func(ctx context.Context, name string) (Row, error) {
			out, err := outputOf(ctx, "nova-config", showArgv(name)...)
			if err != nil {
				return Row{}, err
			}
			return parseRow(name, out)
		},
		Queue: func(ctx context.Context, name string) ([]Card, error) {
			out, err := outputOf(ctx, "nova-sprint", queueArgv(name)...)
			if err != nil {
				return nil, err
			}
			return parseQueue(out)
		},
		Claim: func(ctx context.Context, card Card) error {
			_, err := outputOf(ctx, "nova-sprint", claimArgv(friendName, card)...)
			return err
		},
		Start: func(ctx context.Context, card Card) (Proc, error) {
			return startLane(ctx, friendName, dir, harness, models, card)
		},
		Report: func(card Card) (string, bool) { return reportText(dir, card) },
		Finish: func(ctx context.Context, card Card, failed bool, head, report string) error {
			_, err := outputOf(ctx, "nova-sprint", finishArgv(friendName, card, failed, head, report)...)
			return err
		},
		Read: func(ctx context.Context, card Card, verdict, finding string) error {
			_, err := outputOf(ctx, "nova-sprint", readArgv(friendName, card, verdict, finding)...)
			return err
		},
		Beat: func(ctx context.Context, name string, beat Beat) error {
			_, err := outputOf(ctx, "nova-sprint", beatArgv(name, beat)...)
			return err
		},
		Judge: func(ctx context.Context, subject, body string) error {
			if seat == "" {
				return fmt.Errorf("no --seat to hear the judgment")
			}
			_, err := outputOf(ctx, "nova-bus", judgeArgv(friendName, seat, subject, body)...)
			return err
		},
		Install: func(ctx context.Context, version string) error {
			if !versionRE.MatchString(version) {
				return fmt.Errorf("runner_version %q is not a version", version)
			}
			_, err := outputOf(ctx, "nova-update", installArgv(version)...)
			return err
		},
		Exec: execInstalled,
	}
}

func execInstalled(version string) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("runner_version %q is not a version", version)
	}
	path, err := exec.LookPath("nova-runner")
	if err != nil {
		path, err = os.Executable()
		if err != nil {
			return err
		}
	}
	return syscall.Exec(path, os.Args, os.Environ())
}

func startLane(ctx context.Context, friendName, dir, harness string, models map[string]string, card Card) (Proc, error) {
	job := jobOf(card)
	if card.Kind == KindRead {
		if err := writeReadBrief(friendName, dir, card); err != nil {
			return nil, err
		}
	} else {
		if err := writeBrief(dir, card); err != nil {
			return nil, err
		}
		packet := friend.Packet{
			Card: card.ID, Job: job, Repo: card.Repo, Base: card.Base,
			BaseSha: card.BaseSha, Branch: card.Branch, Attempt: card.Attempt,
		}
		if _, err := (&friend.Stager{Dir: dir}).Stage(ctx, packet); err != nil {
			return nil, err
		}
	}
	logPath := filepath.Join(friend.JobDir(dir, job), "harness.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	bin, args := harnessArgv(harness, modelOf(card, models), promptOf(friendName, card))
	// The harness has its own session and deadline; runner shutdown does not kill it.
	cmd := subproc.Long(context.Background(), bin, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, err
	}
	proc := &cmdProc{pid: cmd.Process.Pid, log: logPath, done: make(chan struct{})}
	go capLane(card.Deadline, proc.pid, proc.done)
	go func() {
		err := cmd.Wait()
		logf.Close()
		proc.line = firstLine(readTrim(logPath))
		if proc.line == "" && err != nil {
			proc.line = firstLine(err.Error())
		}
		close(proc.done)
	}()
	return proc, nil
}

// writeReadBrief writes a read lane's inbox/<job>/BRIEF.md from the sprint's own
// read-card brief (sprint.ReadCardBrief): the work under review at its pinned
// branch and head, no checkout. The harness reads it and writes the report.
func writeReadBrief(friendName, dir string, card Card) error {
	p := sprint.Packet{
		Card: card.ID, Kind: KindRead, Primary: card.Primary, Attempt: card.Attempt,
		Gen: card.Gen, Epoch: card.Epoch, Brief: card.Brief, Report: card.Report,
		Head: card.Head, WorkBranch: card.WorkBranch, WorkBase: card.WorkBase, Tier: card.Tier,
	}
	body := sprint.ReadCardBrief(friendName, jobOf(card), p, "", time.Time{})
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	job := jobOf(card)
	for _, p := range []string{
		filepath.Join(dir, "inbox", job, "BRIEF.md"),
	} {
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// capLane is the card's own deadline, not a width kill. SIGTERM goes to the
// lane's session and to no other process.
func capLane(deadline, pid int, done <-chan struct{}) {
	if deadline <= 0 || pid <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(deadline) * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			log.Printf("RUNNER FAIL deadline pid=%d: %v", pid, err)
		}
	}
}

func writeBrief(dir string, card Card) error {
	if strings.TrimSpace(card.Brief) == "" {
		return nil
	}
	body := card.Brief
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	job := jobOf(card)
	for _, p := range []string{
		filepath.Join(dir, "inbox", job, "BRIEF.md"),
		filepath.Join(dir, "jobs", job, "BRIEF.md"),
	} {
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func reportText(dir string, card Card) (string, bool) {
	job := jobOf(card)
	for _, p := range []string{
		filepath.Join(dir, "outbox", job, "REPORT.md"),
		filepath.Join(dir, "jobs", job, "REPORT.md"),
	} {
		raw, err := os.ReadFile(p)
		if err != nil || strings.TrimSpace(string(raw)) == "" {
			continue
		}
		return string(raw), true
	}
	return "", false
}

func readTrim(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > 4096 {
		raw = raw[:4096]
	}
	return string(raw)
}

type cmdProc struct {
	pid  int
	log  string
	line string
	done chan struct{}
}

func (p *cmdProc) Exited() (bool, string) {
	select {
	case <-p.done:
		return true, p.line
	default:
		return false, ""
	}
}

func (p *cmdProc) PID() int        { return p.pid }
func (p *cmdProc) LogPath() string { return p.log }

// pidProc is a lane adopted from the state file. It is not this process's child
// unless the runner was re-exec'd, and a re-exec waits until none are running.
type pidProc struct {
	pid int
	log string
}

func (p pidProc) Exited() (bool, string) {
	if p.pid > 0 && syscall.Kill(p.pid, 0) == nil {
		return false, ""
	}
	line := firstLine(readTrim(p.log))
	if line == "" {
		line = "process exited"
	}
	return true, line
}

func (p pidProc) PID() int        { return p.pid }
func (p pidProc) LogPath() string { return p.log }
