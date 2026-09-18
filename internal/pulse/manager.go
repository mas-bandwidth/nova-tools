package pulse

// manager is SPEC-PULSE's manager tier: the shift a cheap model used to hold by hand, run as a
// verb, with NO model call. A cycle is wait, notes, harvest, triage, merge, refill, one line.
// Quiet time makes no call and sends nothing; an unknown policy key is a refusal, because a
// policy the tool half-understands is a policy nobody approved.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ManagerInput is the verb's input, held apart from flag parsing so a test can drive a whole
// shift against a fake queue and fake gh, git, nova-bus and nova-swarm on PATH.
type ManagerInput struct {
	Policy string // the finite policy this shift executes
	Queue  string // the queue directory: pending, launched, done, failed and the state files
	Roots  string // comma-separated swarm roots, the benches this shift harvests
	Bus    string // the nova-bus clone this shift is the single waiter on
	As     string // the name this shift waits and receipts as
	Hours  float64
	Max    int
	// Once runs exactly one cycle and ends the shift. --hours 0 was the only one-cycle
	// door and it is a DURATION, so a reader had to know that 0 hours means one cycle
	// rather than none (the manager dogfood, edge 6).
	Once bool
	// DryRun reads everything and changes nothing: no bus advance, no receipt, no push,
	// no PR, no lane, no card moved, cut or released. The counts are what the cycle WOULD
	// have done, and the line carries dry-run=yes.
	DryRun bool
	// Locked says this shift runs inside a caller already holding the queue's lock.
	Locked bool
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

// policy is the approved finite policy; every key it may carry is named here.
type policy struct {
	WaitTimeout string
	Floor       int
	Scope       *regexp.Regexp
	Sources     []string
	KnownFlakes []string
	MaxAttempts int
	Lane        string
}

var policyKeys = []string{"wait-timeout", "floor", "scope-regex", "sources", "known-flakes", "max-attempts", "lane"}

// noteID matches a bus note id as it appears on an inbox display line.
var noteID = regexp.MustCompile(`\bbo-[0-9a-f]{6,}\b`)

// gateLine matches the one gate a card may carry: AFTER: PR<n> merged.
var gateLine = regexp.MustCompile(`AFTER: PR([0-9]+) merged`)

// contractPR and contractIssue are the two numeric dedup keys read off a candidate.
var (
	contractPR    = regexp.MustCompile(`(?i)\bPR\s*#?([0-9]+)\b`)
	contractIssue = regexp.MustCompile(`#([0-9]+)\b`)
)

func readPolicy(path string) (policy, error) {
	p := policy{WaitTimeout: "3m", MaxAttempts: 1}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, fmt.Errorf("cannot read --policy %s (the shift executes a policy file; write one with the keys %s)", path, strings.Join(policyKeys, ", "))
	}
	for n, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		key, value, ok := strings.Cut(t, "=")
		if !ok {
			return p, fmt.Errorf("%s line %d is not key=value (the policy is key=value lines; the keys are %s)", path, n+1, strings.Join(policyKeys, ", "))
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "wait-timeout":
			if _, err := time.ParseDuration(value); err != nil {
				return p, fmt.Errorf("%s: wait-timeout wants a duration such as 3m, got %q", path, value)
			}
			p.WaitTimeout = value
		case "floor":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return p, fmt.Errorf("%s: floor wants a whole number of ready cards, got %q", path, value)
			}
			p.Floor = n
		case "scope-regex":
			re, err := regexp.Compile(value)
			if err != nil {
				return p, fmt.Errorf("%s: scope-regex does not compile: %s", path, oneline.Err(err))
			}
			p.Scope = re
		case "sources":
			p.Sources = splitList(value)
		case "known-flakes":
			p.KnownFlakes = splitList(value)
		case "max-attempts":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return p, fmt.Errorf("%s: max-attempts wants 1 or more, got %q", path, value)
			}
			p.MaxAttempts = n
		case "lane":
			p.Lane = value
		default:
			return p, fmt.Errorf("%s line %d: unknown policy key %q; the manager never expands its policy (the keys are %s)", path, n+1, key, strings.Join(policyKeys, ", "))
		}
	}
	return p, nil
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if t := strings.TrimSpace(f); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// manager is one shift's state: the policy, the benches, the counters the SHIFT END line ends with.
type manager struct {
	in    ManagerInput
	pol   policy
	roots []string
	now   func() time.Time

	// per-shift
	cycles      int
	decisions   int
	escalations int

	// per-cycle
	out                                                         *boundedList
	notes, receipts, harvested, prs, merged, requeued, refilled int
	released, gated                                             int
	escalatedNow                                                int
	called                                                      bool // any child ran this cycle beyond the bus wait
}

// writes says whether this cycle may change anything. Under --dry-run it never may, and
// every door a change goes through asks here first.
func (m *manager) writes() bool { return !m.in.DryRun }

// Manager runs one bounded shift: 0 when it ended by itself, 2 on a refusal that never started.
func Manager(in ManagerInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Max == 0 {
		in.Max = 20
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Policy, "policy", "the approved policy file this shift executes"},
		{in.Queue, "queue", "the queue directory holding pending, launched, done and the state files"},
		{in.Roots, "roots", "the benches to harvest, comma separated"},
		{in.Bus, "bus", "the nova-bus clone this shift waits on"},
		{in.As, "as", "the name this shift waits and receipts as"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "MANAGER", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	if in.Hours < 0 {
		return refusal(in.Stderr, "MANAGER", fmt.Errorf("--hours is 0 or more, got %v (0 runs exactly one cycle, as --once does)", in.Hours))
	}
	pol, err := readPolicy(in.Policy)
	if err != nil {
		return refusal(in.Stderr, "MANAGER", err)
	}
	m := &manager{in: in, pol: pol, roots: splitList(in.Roots), now: in.Now}
	for _, d := range []string{"pending", "launched", "done", "failed"} {
		if err := os.MkdirAll(filepath.Join(in.Queue, d), 0o755); err != nil {
			return refusal(in.Stderr, "MANAGER", fmt.Errorf("cannot make %s: %s", filepath.Join(in.Queue, d), oneline.Err(err)))
		}
	}
	// ONE WRITER PER QUEUE (queuelock.go). The shift hands out card numbers from
	// <queue>/NEXT with a read-modify-write and moves cards between the directories; two
	// shifts on one queue hand the same number to two cards.
	if !in.Locked && m.writes() {
		lock, err := LockQueue(in.Queue, "manager")
		if err != nil {
			return refusal(in.Stderr, "MANAGER", err)
		}
		defer lock.Release()
	}
	return m.shift()
}

func (m *manager) shift() int {
	end := m.now().Add(time.Duration(m.in.Hours * float64(time.Hour)))
	failures := 0
	for {
		m.cycles++
		if ok := m.cycle(); !ok {
			failures++
			if failures >= 3 {
				fmt.Fprintf(m.in.Stderr, "MANAGER REFUSED: nova-bus wait failed %d times in a row (check the bus clone %s and the name %s, then start the shift again)\n", failures, oneline.Field(m.in.Bus), oneline.Field(m.in.As))
				break
			}
		} else {
			failures = 0
		}
		if m.in.Once || !m.now().Before(end) {
			break
		}
	}
	line := fmt.Sprintf("SHIFT END cycles=%d decisions=%d escalations=%d", m.cycles, m.decisions, m.escalations)
	fmt.Fprintln(m.in.Stdout, line)
	m.log(line)
	return 0
}

// cycle is one turn: wait, notes, harvest, triage, merge, refill, one line. False = the wait failed.
func (m *manager) cycle() bool {
	m.out = bound(m.in.Stdout, m.in.Max)
	m.notes, m.receipts, m.harvested, m.prs, m.merged, m.requeued, m.refilled = 0, 0, 0, 0, 0, 0, 0
	m.released, m.gated = 0, 0
	m.escalatedNow, m.called = 0, false

	waited := m.waitBus()
	m.handleNotes(waited)
	m.harvest()
	m.mergeApproved()
	// The gate is released BEFORE the refill: a card whose PR landed this cycle is ready
	// this cycle, so the floor counts it and the refill does not cut a neighbour for the
	// slot it already has (SPEC-PULSE rule 1, a card never waits for a tick to start).
	m.releaseGates()
	m.refill()

	m.out.More()
	pending := len(m.cardsIn("pending"))
	line := fmt.Sprintf("MANAGER cycle=%d notes=%d receipts=%d harvested=%d prs=%d merged=%d requeued=%d refilled=%d released=%d gated=%d escalated=%d pending=%d quiet=%t",
		m.cycles, m.notes, m.receipts, m.harvested, m.prs, m.merged, m.requeued, m.refilled, m.released, m.gated, m.escalatedNow, pending, !m.called && m.notes == 0)
	if m.in.DryRun {
		line += " dry-run=yes"
	}
	fmt.Fprintln(m.in.Stdout, line)
	m.log(line)
	return waited != nil
}

// releaseGates is the other half of SPEC-PULSE rule 4, and the half nobody had written: a
// pending card carrying `AFTER: PR<n> merged` has its gate taken off by the first cycle in
// which the forge says that PR is merged, and is ready from that moment -- never by a person
// noticing. `fill` holds the card while the line is on it (cardgate.go); this is what takes
// the line off. One gh call per DISTINCT pull request, however many cards wait on it.
func (m *manager) releaseGates() {
	repo := m.repo()
	merged := map[int]bool{}
	state := map[int]string{}
	for _, card := range m.cardsIn("pending") {
		path := filepath.Join(m.in.Queue, "pending", card)
		text := readCard(path)
		pr := cardGateOf(text)
		if pr == 0 {
			continue
		}
		if repo == "" {
			m.gated++
			m.event("MANAGER NOTE card=%s after=PR%d: no %s, so nobody can be asked whether it merged (write owner/name there)",
				oneline.Field(card), pr, oneline.Field(filepath.Join(m.in.Queue, "REPO")))
			continue
		}
		if _, asked := merged[pr]; !asked {
			merged[pr], state[pr] = m.prMerged(repo, pr)
			m.called = true
		}
		if !merged[pr] {
			m.gated++
			continue
		}
		if m.writes() {
			if err := os.WriteFile(path, []byte(releaseCardGate(text)), 0o644); err != nil {
				m.event("MANAGER NOTE card=%s: the gate could not be taken off: %s", oneline.Field(card), oneline.Err(err))
				m.gated++
				continue
			}
		}
		m.released++
		m.decisions++
		m.event("MANAGER RELEASED card=%s after=PR%d state=%s", oneline.Field(card), pr, oneline.Field(state[pr]))
	}
}

// prMerged asks the forge whether one pull request is merged, and names the state it gave.
// Anything it cannot read is NOT merged: a gate whose answer is unknown stays shut, which
// is what the card was gated for.
func (m *manager) prMerged(repo string, pr int) (bool, string) {
	out, err := m.sh("", 60*time.Second, "gh", "pr", "view", strconv.Itoa(pr), "-R", repo, "--json", "state")
	if err != nil {
		return false, "unread"
	}
	var v prView
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &v) != nil || strings.TrimSpace(v.State) == "" {
		return false, "unread"
	}
	return strings.EqualFold(v.State, "MERGED"), strings.ToUpper(strings.TrimSpace(v.State))
}

// repo is the repository this queue is about: <queue>/REPO, the same file `status` reads.
func (m *manager) repo() string { return firstLine(filepath.Join(m.in.Queue, "REPO")) }

// waitBus blocks on nova-bus wait in the foreground: the one call a quiet cycle makes.
func (m *manager) waitBus() []string {
	// A minute of slack, so this tool never cuts the wait off before nova-bus's own bound does.
	d, err := time.ParseDuration(m.pol.WaitTimeout)
	if err != nil {
		d = 3 * time.Minute
	}
	// The wait ADVANCES the bus cursor, which is a change, so a dry run does not wait at
	// all: it reads no notes rather than reading them and forgetting where it got to.
	out, err := m.change("wait on the bus", "", d+time.Minute, "nova-bus", "wait", "--bus", m.in.Bus, "--as", m.in.As,
		"--timeout", m.pol.WaitTimeout, "--advance")
	if err != nil {
		m.event("MANAGER NOTE bus wait failed: %s", oneline.Cap(strings.TrimSpace(out), 120))
		return nil
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if lines == nil {
		lines = []string{}
	}
	return lines
}

// handleNotes receipts a START or DONE note and escalates every other note one line. It never
// composes a reply: composing is the planning tier's, and this tier makes no model call.
func (m *manager) handleNotes(lines []string) {
	for _, l := range lines {
		id := noteID.FindString(l)
		if id == "" {
			continue
		}
		m.notes++
		if u := strings.ToUpper(l); strings.Contains(u, "START") || strings.Contains(u, "DONE") {
			if _, err := m.change("receipt the note", "", 60*time.Second, "nova-bus", "receipt", "--bus", m.in.Bus, "--as", m.in.As, "--note", id); err != nil {
				m.event("MANAGER NOTE receipt failed note=%s", oneline.Field(id))
				continue
			}
			m.called = true
			m.receipts++
			m.decisions++
			continue
		}
		m.escalate("note", id, oneline.Cap(l, 160))
	}
}

// harvest disposes every launched card whose job has a RESULT.md: a read records its verdict,
// a work card is pushed and opened as a PR, an abstain is triaged.
func (m *manager) harvest() {
	done := map[string][]string{} // root -> finished jobs, appended to the status index once
	for _, card := range m.cardsIn("launched") {
		job, root := m.jobFor(card)
		if job == "" {
			continue
		}
		result, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
		if err != nil {
			continue // still in flight; launch owns the slot, not this tier
		}
		m.harvested++
		lines := strings.Split(strings.ReplaceAll(string(result), "\r\n", "\n"), "\n")
		switch {
		case abstainReason(lines) != "":
			m.triageAbstain(card, root, abstainReason(lines))
		case readVerdict(lines) != nil:
			m.recordVerdict(card, readVerdict(lines))
		default:
			m.openPR(card, job, lines)
		}
		done[root] = append(done[root], job)
	}
	// The finished jobs join their roots' status indexes, so status answers the next tick
	// without opening a job's usage.tsv (#1088).
	for root, jobs := range done {
		appendStatusIndex(root, jobs)
	}
}

// abstainReason is an abstain's reason token, "" when the card did not abstain: triage routes on it.
func abstainReason(lines []string) string {
	for i, l := range lines {
		if i > 1 {
			break
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(l), "ABSTAIN")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		for _, f := range fields {
			if v, ok := strings.CutPrefix(f, "reason="); ok {
				return strings.Trim(v, ":,")
			}
		}
		if len(fields) > 0 {
			return strings.Trim(fields[0], ":,")
		}
		return "unsaid"
	}
	return ""
}

// verdict is one read card's answer about one PR.
type verdict struct {
	Repo string
	PR   int
	Say  string // APPROVE or HOLD
	Head string
}

// readVerdict reads a read card's PR<n>: APPROVE|HOLD line, with the head it read.
func readVerdict(lines []string) *verdict {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "PR") {
			continue
		}
		num, rest, ok := strings.Cut(strings.TrimPrefix(t, "PR"), ":")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil {
			continue
		}
		v := &verdict{PR: n}
		for _, f := range strings.Fields(rest) {
			switch {
			case f == "APPROVE" || f == "HOLD":
				v.Say = f
			default:
				if s, ok := strings.CutPrefix(f, "head="); ok {
					v.Head = s
				}
				if s, ok := strings.CutPrefix(f, "repo="); ok {
					v.Repo = strings.TrimPrefix(s, "github.com/")
				}
			}
		}
		if v.Say == "" {
			continue
		}
		return v
	}
	return nil
}

// recordVerdict files an APPROVE for the merge step and a HOLD as a hold plus one escalation.
func (m *manager) recordVerdict(card string, v *verdict) {
	m.move(card, "done")
	ref := fmt.Sprintf("%s#%d", v.Repo, v.PR)
	if v.Say == "HOLD" {
		m.appendQueue("HOLD", ref)
		m.escalate("HOLD", ref, "a read held this PR; a repair card must quote the HOLD lines")
		return
	}
	if v.Head == "" {
		m.escalate("READ-NO-HEAD", ref, "the read named no head=<sha>; nothing is merged on a read that cannot be revalidated")
		return
	}
	m.appendQueue("APPROVED", fmt.Sprintf("%s %d %s", oneline.Field(v.Repo), v.PR, oneline.Field(v.Head)))
	m.decisions++
}

// openPR pushes the branch by explicit refspec and opens or updates its PR. A fix with neither a
// red: line nor a test file in its diff is refused, never admitted.
func (m *manager) openPR(card, job string, lines []string) {
	branch, repo := branchAndRepo(lines)
	dir := filepath.Join(job, "repo")
	m.called = true
	switch {
	case branch == "" || branch == "main" || branch == "master":
		m.move(card, "failed")
		m.event("MANAGER REFUSED card=%s branch=%s: a card never pushes a base branch (name a working branch on the BRANCH line)", oneline.Field(card), oneline.Field(branch))
		return
	case repo == "":
		m.move(card, "failed")
		m.event("MANAGER REFUSED card=%s: no REPO line in RESULT.md (name owner/repo on the REPO line)", oneline.Field(card))
		return
	case isFixBranch(branch) && !hasRedLine(lines) && !m.hasTestInDiff(dir):
		m.move(card, "failed")
		m.event("MANAGER REFUSED card=%s branch=%s: a fix without its reproducing test is not admitted (add the red: line or a test file to the diff)", oneline.Field(card), oneline.Field(branch))
		return
	}
	if out, err := m.change("push the branch", dir, 120*time.Second, "git", "push", pushURL(repo), "+"+branch+":"+branch); err != nil {
		m.event("MANAGER NOTE push failed card=%s branch=%s: %s", oneline.Field(card), oneline.Field(branch), oneline.Cap(strings.TrimSpace(out), 120))
		return
	}
	pr := m.prNumber(repo, branch)
	if pr == 0 {
		title := oneline.Cap(strings.TrimSpace(firstNonEmpty(lines)), 110)
		body := strings.Join(lines, "\n")
		if len(body) > 4096 {
			body = body[:4096]
		}
		out, err := m.change("open the pull request", dir, 120*time.Second, "gh", "pr", "create", "-R", repo, "--head", branch, "--base", "main", "--title", title, "--body", body)
		if err != nil {
			m.event("MANAGER NOTE pr failed card=%s: %s", oneline.Field(card), oneline.Cap(strings.TrimSpace(out), 120))
			return
		}
		pr = parsePRNumber(out)
	}
	m.move(card, "done")
	m.prs++
	m.decisions++
	m.event("MANAGER PR repo=%s pr=%d card=%s branch=%s", oneline.Field(repo), pr, oneline.Field(card), oneline.Field(branch))
	m.cutCard(Candidate{Source: repo, ID: fmt.Sprintf("%s#%d", repo, pr), Kind: "read", Title: fmt.Sprintf("read of %s PR%d and post the verdict line PR%d: APPROVE|HOLD head=<sha>", repo, pr, pr), Template: "read"}, 0)
}

func (m *manager) prNumber(repo, branch string) int {
	out, err := m.sh("", 60*time.Second, "gh", "pr", "list", "-R", repo, "--head", branch, "--state", "open", "--json", "number")
	if err != nil {
		return 0
	}
	var rows []struct {
		Number int `json:"number"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &rows) == nil && len(rows) > 0 {
		return rows[0].Number
	}
	return 0
}

func branchAndRepo(lines []string) (branch, repo string) {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		for _, p := range []string{"BRANCH:", "BRANCH"} {
			if v, ok := strings.CutPrefix(t, p); ok && branch == "" {
				branch = strings.TrimSpace(v)
			}
		}
		for _, p := range []string{"REPO:", "REPO"} {
			if v, ok := strings.CutPrefix(t, p); ok && repo == "" {
				repo = strings.TrimPrefix(strings.TrimSpace(v), "github.com/")
			}
		}
	}
	return branch, repo
}

func isFixBranch(branch string) bool {
	if _, rest, ok := strings.Cut(branch, "/"); ok {
		branch = rest
	}
	for _, p := range []string{"fix-", "repair-", "flakes-", "drift-"} {
		if strings.HasPrefix(branch, p) {
			return true
		}
	}
	return false
}

// hasTestInDiff asks git whether the branch touches a test file; a fix's proof is a red test.
func (m *manager) hasTestInDiff(dir string) bool {
	base, err := m.sh(dir, 30*time.Second, "git", "merge-base", "origin/main", "HEAD")
	if err != nil {
		return false
	}
	out, err := m.sh(dir, 30*time.Second, "git", "diff", "--name-only", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasSuffix(t, "_test.go") || strings.HasSuffix(t, "_test.lisp") || strings.Contains(t, "/tests/") {
			return true
		}
	}
	return false
}

// triageAbstain requeues a card once under a new number on the other bench and escalates the
// second abstain: the attempt count rides on the card, so the record is the card itself.
func (m *manager) triageAbstain(card, root, reason string) {
	ref := strings.TrimSuffix(card, ".md")
	body, err := os.ReadFile(filepath.Join(m.in.Queue, "launched", card))
	if err != nil {
		m.move(card, "failed")
		m.escalate("ABSTAIN", ref, "the launched card cannot be read; requeue it by hand")
		return
	}
	attempts := attemptOf(string(body)) + 1
	if attempts > m.pol.MaxAttempts {
		m.move(card, "failed")
		m.escalate("ABSTAIN", ref, fmt.Sprintf("reason=%s attempts=%d; the card is a prompt defect, rewrite it rather than retry it", reason, attempts))
		return
	}
	bench := m.otherBench(root)
	name := fmt.Sprintf("card-%d.md", m.nextNumber())
	if err := m.writeCard(name, reissue(string(body), bench, attempts)); err != nil {
		m.escalate("ABSTAIN", ref, "the requeued card could not be written; requeue it by hand")
		return
	}
	m.move(card, "done")
	m.requeued++
	m.decisions++
	m.event("MANAGER REQUEUE card=%s new=%s bench=%s reason=%s", oneline.Field(card), oneline.Field(name), oneline.Field(bench), oneline.Field(reason))
}

// otherBench is the bench a requeue goes to: never the one that abstained, when there is one.
func (m *manager) otherBench(root string) string {
	for _, r := range m.roots {
		if r != root {
			return filepath.Base(r)
		}
	}
	if len(m.roots) > 0 {
		return filepath.Base(m.roots[0])
	}
	return "-"
}

// attemptOf is the ATTEMPT: line a requeued card carries; a card out for the first time has none.
func attemptOf(card string) int {
	for _, l := range strings.Split(card, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "ATTEMPT:"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return 0
}

// reissue is the requeued card: the same contract line, the other bench, one higher attempt.
func reissue(card, bench string, attempt int) string {
	out := []string{""}
	for i, l := range strings.Split(card, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BENCH:") || strings.HasPrefix(t, "ATTEMPT:") {
			continue
		}
		if i == 0 {
			out[0] = l
			continue
		}
		out = append(out, l)
	}
	return strings.Join(append([]string{out[0], "BENCH: " + bench, fmt.Sprintf("ATTEMPT: %d", attempt)}, out[1:]...), "\n")
}

// mergeApproved merges every PR an approving read named, after revalidating its head: never a
// draft, never one on HOLD, never one whose checks are not all SUCCESS.
func (m *manager) mergeApproved() {
	path := filepath.Join(m.in.Queue, "APPROVED")
	rows := readLines(path)
	if len(rows) == 0 {
		return
	}
	held := map[string]bool{}
	for _, h := range readLines(filepath.Join(m.in.Queue, "HOLD")) {
		held[strings.TrimSpace(h)] = true
	}
	var keep []string
	for _, row := range rows {
		f := strings.Fields(row)
		if len(f) != 3 {
			continue
		}
		repo, head := f[0], f[2]
		pr, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		ref := fmt.Sprintf("%s#%d", repo, pr)
		if held[ref] {
			m.event("MANAGER NOTE held ref=%s: a HOLD read stands, nothing is merged", oneline.Field(ref))
			continue
		}
		m.called = true
		if m.mergeOne(repo, pr, head, ref) {
			keep = append(keep, row)
		}
	}
	if m.writes() {
		writeLines(path, keep)
	}
}

type prView struct {
	IsDraft    bool   `json:"isDraft"`
	State      string `json:"state"`
	HeadRefOid string `json:"headRefOid"`
}

// mergeOne is the merge condition for one PR; it returns whether the approval stays on file.
func (m *manager) mergeOne(repo string, pr int, head, ref string) (keep bool) {
	out, err := m.sh("", 60*time.Second, "gh", "pr", "view", strconv.Itoa(pr), "-R", repo, "--json", "isDraft,state,headRefOid")
	if err != nil {
		m.event("MANAGER NOTE view failed ref=%s: %s", oneline.Field(ref), oneline.Cap(strings.TrimSpace(out), 100))
		return true
	}
	var v prView
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		m.escalate("VIEW-UNREADABLE", ref, "gh pr view did not answer JSON; nothing is merged on an unread head")
		return false
	}
	if v.HeadRefOid != head {
		m.escalate("STALE-READ", ref, fmt.Sprintf("the head moved since the read (read=%s now=%s); cut a fresh read card", sha12(head), sha12(v.HeadRefOid)))
		return false
	}
	if v.IsDraft {
		m.escalate("DRAFT-READY", ref, "approved and revalidated, but the PR is a draft; a person marks it ready")
		return false
	}
	if v.State != "" && v.State != "OPEN" {
		m.event("MANAGER NOTE ref=%s state=%s: nothing to merge", oneline.Field(ref), oneline.Field(v.State))
		return false
	}
	green, why := m.checksGreen(repo, pr, ref)
	if !green {
		m.event("MANAGER NOTE ref=%s not merged: %s", oneline.Field(ref), why)
		return why == "a check is still running"
	}
	if m.pol.Lane == "" {
		m.event("MANAGER NOTE ref=%s: no lane in the policy; nothing is merged (set lane=<dir> to make nova-merge the merge queue)", oneline.Field(ref))
		return true
	}
	if out, err := m.change("hand it to the merge lane", "", 120*time.Second, "nova-merge", "add", "--lane", m.pol.Lane, "--pr", strconv.Itoa(pr)); err != nil {
		m.event("MANAGER NOTE add failed ref=%s: %s", oneline.Field(ref), oneline.Cap(strings.TrimSpace(out), 100))
		return true
	}
	m.merged++
	m.decisions++
	m.event("MANAGER LANE ref=%s head=%s", oneline.Field(ref), oneline.Field(sha12(head)))
	return false
}

// checksGreen is the merge condition's second half: every check SUCCESS, none pending. A failure
// the policy calls a known flake is named and left for the next cycle.
func (m *manager) checksGreen(repo string, pr int, ref string) (bool, string) {
	out, err := m.sh("", 60*time.Second, "gh", "pr", "checks", strconv.Itoa(pr), "-R", repo, "--json", "name,state")
	if err != nil {
		return false, "gh pr checks failed"
	}
	var rows []struct{ Name, State string }
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &rows) != nil {
		return false, "gh pr checks did not answer JSON"
	}
	if len(rows) == 0 {
		return false, "no check has reported yet"
	}
	for _, c := range rows {
		switch c.State {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED":
			return false, "a check is still running"
		default:
			if slices.Contains(m.pol.KnownFlakes, c.Name) {
				m.event("MANAGER FLAKE ref=%s check=%s: a known flake, rerun it and the next cycle re-reads the checks", oneline.Field(ref), oneline.Field(c.Name))
				return false, "a known flake is red"
			}
			m.escalate("RED", ref, fmt.Sprintf("check %s is %s; a repair card must quote the failing tail", c.Name, c.State))
			return false, "a check is red"
		}
	}
	return true, ""
}

// refill tops the queue up to the floor from the policy's sources, deduplicated on PR number,
// issue number and contract sentence, in scope only, leaving gated cards gated.
func (m *manager) refill() {
	ready, seen := m.queueState()
	if ready >= m.pol.Floor || len(m.pol.Sources) == 0 {
		return
	}
	for _, src := range m.pol.Sources {
		for _, c := range readCandidates(src) {
			if ready >= m.pol.Floor {
				return
			}
			subject := c.ID + " " + c.Title
			if m.pol.Scope != nil && !m.pol.Scope.MatchString(subject) {
				m.event("MANAGER SCOPE-DEFERRED id=%s: outside the policy's scope, it stays an issue and never a card", oneline.Field(c.ID))
				continue
			}
			dup := false
			for _, k := range keysOf(c.ID, c.Title) {
				if seen[k] {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			gate := 0
			if g := gateLine.FindStringSubmatch(c.Title); g != nil {
				gate, _ = strconv.Atoi(g[1])
			}
			name := m.cutCard(c, gate)
			if name == "" {
				continue
			}
			for _, k := range keysOf(c.ID, c.Title) {
				seen[k] = true
			}
			m.refilled++
			m.decisions++
			if gate == 0 {
				ready++
			}
		}
	}
}

// cutCard writes one card into pending under the next number; a gate stays on the card, and a
// gated card is never counted ready.
func (m *manager) cutCard(c Candidate, gate int) string {
	n := m.nextNumber()
	name := fmt.Sprintf("card-%d.md", n)
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: CARD-%d %s\n", n, strings.TrimSpace(gateLine.ReplaceAllString(c.Title, "")))
	if gate > 0 {
		fmt.Fprintf(&b, "AFTER: PR%d merged\n", gate)
	}
	fmt.Fprintf(&b, "SOURCE: %s %s\n", c.Source, c.ID)
	if c.Template != "" {
		if raw, err := os.ReadFile(filepath.Join(m.in.Queue, "templates", c.Template+".md")); err == nil {
			b.WriteString(strings.TrimRight(string(raw), "\n") + "\n")
		}
	}
	if err := m.writeCard(name, b.String()); err != nil {
		m.event("MANAGER NOTE cannot cut %s: %s", oneline.Field(name), oneline.Err(err))
		return ""
	}
	m.event("MANAGER CARD card=%s id=%s", oneline.Field(name), oneline.Field(c.ID))
	return name
}

// writeCard writes one card into pending. It is the manager's only door to a new card, so
// --dry-run closes it and the cycle still counts the card it would have cut.
func (m *manager) writeCard(name, body string) error {
	if !m.writes() {
		return nil
	}
	return os.WriteFile(filepath.Join(m.in.Queue, "pending", name), []byte(body), 0o644)
}

// queueState counts the ready (ungated) pending cards and indexes every card by its dedup keys.
func (m *manager) queueState() (int, map[string]bool) {
	seen := map[string]bool{}
	ready := 0
	for _, dir := range []string{"pending", "launched", "done"} {
		for _, card := range m.cardsIn(dir) {
			raw, err := os.ReadFile(filepath.Join(m.in.Queue, dir, card))
			if err != nil {
				continue
			}
			text := string(raw)
			contract := strings.TrimSpace(firstNonEmpty(strings.Split(text, "\n")))
			id := ""
			for _, l := range strings.Split(text, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(l), "SOURCE:"); ok {
					f := strings.Fields(v)
					if len(f) > 1 {
						id = f[1]
					}
				}
			}
			for _, k := range keysOf(id, contract) {
				seen[k] = true
			}
			if dir == "pending" && !gateLine.MatchString(text) {
				ready++
			}
		}
	}
	return ready, seen
}

// keysOf is a candidate's dedup keys: PR number, issue number, contract sentence normalised.
func keysOf(id, title string) []string {
	var keys []string
	if s := contractPR.FindStringSubmatch(title + " " + id); s != nil {
		keys = append(keys, "pr:"+s[1])
	}
	if s := contractIssue.FindStringSubmatch(id); s != nil {
		keys = append(keys, "issue:"+s[1])
	}
	if c := normalizeContract(title); c != "" {
		keys = append(keys, "contract:"+c)
	}
	return keys
}

// normalizeContract strips the card number and collapses case and whitespace to one key.
func normalizeContract(s string) string {
	t := strings.TrimSpace(s)
	if v, ok := strings.CutPrefix(t, "RESULT:"); ok {
		t = strings.TrimSpace(v)
		if f := strings.Fields(t); len(f) > 0 && strings.HasPrefix(strings.ToUpper(f[0]), "CARD-") {
			t = strings.TrimSpace(strings.TrimPrefix(t, f[0]))
		}
	}
	t = gateLine.ReplaceAllString(t, "")
	return strings.ToLower(strings.Join(strings.Fields(t), " "))
}

// escalate is the one line a decision outside the policy costs: no reply, no model call.
func (m *manager) escalate(kind, ref, line string) {
	stamp := m.now().UTC().Format("2006-01-02T15:04:05Z")
	m.appendQueue("ESCALATE", fmt.Sprintf("ESCALATE %s %s %s: %s", stamp, kind, ref, line))
	m.escalations++
	m.escalatedNow++
	m.event("MANAGER ESCALATE %s %s: %s", oneline.Field(kind), oneline.Field(ref), line)
}

func (m *manager) event(format string, a ...any) { m.out.Line(fmt.Sprintf(format, a...)) }

func (m *manager) log(line string) {
	m.appendQueue("MANAGER.log", m.now().UTC().Format("15:04:05Z")+" "+line)
}

// sha12 is a head as a line carries it: twelve characters, enough to tell two heads apart.
func sha12(s string) string { return s[:min(12, len(s))] }

// change runs one bounded child that CHANGES something outside this process -- the bus
// cursor, a branch, a pull request, the merge lane. Under --dry-run it runs nothing, names
// what it would have run on one line, and answers as an empty success, so the rest of the
// cycle reads on and counts what the shift would have done.
func (m *manager) change(what, dir string, timeout time.Duration, name string, args ...string) (string, error) {
	if !m.writes() {
		m.event("MANAGER DRY-RUN would %s: %s", what, oneline.Cap(name+" "+strings.Join(args, " "), 160))
		return "", nil
	}
	return m.sh(dir, timeout, name, args...)
}

// appendQueue appends one line to a file in the queue. It is the manager's only door to
// the record files, so --dry-run closes all of them at once.
func (m *manager) appendQueue(name, line string) {
	if !m.writes() {
		return
	}
	appendLine(filepath.Join(m.in.Queue, name), line)
}

// sh runs one bounded child and returns its combined output. Nothing here runs a shell.
func (m *manager) sh(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cardsIn lists the card files in one queue directory, in name order.
func (m *manager) cardsIn(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(m.in.Queue, dir, "card-*.md"))
	for i, p := range matches {
		matches[i] = filepath.Base(p)
	}
	return matches
}

// jobFor finds a card's job directory on any bench: <root>/<slot>/jobs/<label>.
func (m *manager) jobFor(card string) (job, root string) {
	label := strings.TrimSuffix(card, ".md")
	for _, r := range m.roots {
		matches, _ := filepath.Glob(filepath.Join(r, "*", "jobs", label))
		if len(matches) > 0 {
			return matches[0], r
		}
	}
	return "", ""
}

func (m *manager) move(card, to string) {
	if !m.writes() {
		return
	}
	_ = os.Rename(filepath.Join(m.in.Queue, "launched", card), filepath.Join(m.in.Queue, to, card))
}

// nextNumber reads <queue>/NEXT, uses it and writes it back plus one: card numbers are
// never reused, so a requeue is always a new number and the record stays readable.
func (m *manager) nextNumber() int {
	path := filepath.Join(m.in.Queue, "NEXT")
	n := 1
	if raw, err := os.ReadFile(path); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && v > 0 {
			n = v
		}
	}
	if m.writes() {
		_ = os.WriteFile(path, []byte(strconv.Itoa(n+1)+"\n"), 0o644)
	}
	return n
}

func readLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func writeLines(path string, lines []string) {
	if len(lines) == 0 {
		_ = os.Remove(path)
		return
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, oneline.Escape(line))
}
