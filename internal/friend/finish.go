package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// A friend's finish is read from what the lane did, never from the essay the model
// wrote about it (the owner, 2026-10-07: "I want everything that is not the model's
// fault fixed"). On that day one flash friend ended 144 of 286 attempts failed, 88 of
// them the machine's: lanes that committed and wrote no report, HOLDs whose only note
// was a cost line of tokens input=0 and output=0, finishes refused at land because the
// report named no head or no verdict. DecideFinish is that contract as a pure function
// of what the lane left. The daemon applies it from a lane's end (branchFinish); a turn
// the contract does not take still ends as it did (a lane's end, the outbox pass).
//
// Libraries considered: none decide a finish. git is internal/gitrun, the tree's one
// runner. The strings are the report the lane wrote.

// FinishFaults is how many harness faults on one card in a row finish it FAIL.
const FinishFaults = 3

// FinishForm is the report the recovery turn is asked to write, when the card's brief
// does not state its own.
const FinishForm = "Verdict: LAND|HOLD|FAIL\nHead: <sha>"

// How a finish comes out.
const (
	ActToday   = "today"   // the lane's end as it stood: this contract does not take the turn
	ActRecover = "recover" // one more harness turn in the same attempt
	ActFault   = "fault"   // not a verdict: no finish, the attempt is not spent
	ActFinish  = "finish"  // a verdict, the head taken from the branch
	ActFail    = "fail"    // a failed finish the contract itself writes
)

// UsageFact is what the daemon could read of the turn's usage. Zero and Absent are the
// two ways a finish ran no model. Measured is set when a token source was actually read;
// a lane with no token source leaves it false and is not, by that alone, this fault.
type UsageFact struct {
	Measured bool
	Zero     bool
	Absent   bool
}

// ZeroOrAbsent is rule 3's usage condition: the harness reported zeros, or reported nothing.
func (u UsageFact) ZeroOrAbsent() bool { return u.Zero || u.Absent }

// FinishInput is what the lane did at finish time. Tip and Commits come from git in the
// job checkout (the lane's own commits, not the base's history). Report is REPORT.md,
// empty when it is absent. Faults is how many of this contract's faults the card has
// already had in a row. RecoveryDone is the one recovery turn of this attempt already
// started. Form is the card's finish form, empty for FinishForm. GitUnreadable means a
// checkout was there and git could not be read: that is not evidence of no commits.
type FinishInput struct {
	Friend, Card, Branch string
	Report               string
	ReportPresent        bool
	Tip                  string
	Commits              []string
	Usage                UsageFact
	Exit                 int
	Stderr               string
	Faults               int
	RecoveryDone         bool
	Form                 string
	GitUnreadable        bool
}

// FinishDecision is what the daemon does with one finish. OK is a finish that is not
// --failed. Head is the branch tip, never a sha parsed out of the report; empty when
// the lane made no commit. AttemptSpent is false for a fault and for the recovery turn.
type FinishDecision struct {
	Act          string
	OK           bool
	Failed       bool
	Head         string
	Verdict      string
	Judgment     string
	Reason       string
	Prompt       string
	UsageUnknown bool
	AttemptSpent bool
	BackToQueue  bool
	SendsFinish  bool
}

func (in FinishInput) hasCommit() bool { return in.Tip != "" || len(in.Commits) > 0 }

// DecideFinish is the finish contract.
func DecideFinish(in FinishInput) FinishDecision {
	if in.GitUnreadable {
		return FinishDecision{Act: ActToday}
	}
	usage := in.Usage
	if costLineZero(in.Report) {
		usage.Zero = true
	}
	verdict, reportHead := "", ""
	if in.ReportPresent {
		verdict, reportHead = reportVerdict(in.Report)
	}
	sentence := ""
	if in.ReportPresent {
		sentence = modelSentence(in.Report)
	}
	hasCommit := in.hasCommit()
	noSentence := !in.ReportPresent || sentence == ""

	// A harness fault, never a verdict: zero or absent usage, no commit, and no sentence
	// from the model. A HOLD or FAIL whose note is only the cost line is that case.
	if usage.ZeroOrAbsent() && !hasCommit && noSentence {
		judgment := faultJudgment(in)
		if in.Faults+1 >= FinishFaults {
			return FinishDecision{
				Act: ActFail, Failed: true, Verdict: "FAIL",
				Reason: judgment, Judgment: judgment,
				AttemptSpent: true, SendsFinish: true,
			}
		}
		return FinishDecision{
			Act: ActFault, Judgment: judgment, Reason: judgment,
			BackToQueue: true,
		}
	}

	// Commits and no report: one recovery turn in this attempt, then a fail that names it.
	if !in.ReportPresent && hasCommit {
		if !in.RecoveryDone {
			return FinishDecision{
				Act: ActRecover, Head: in.Tip,
				Prompt: RecoveryPrompt(in.Branch, in.Tip, in.Commits, in.Form),
			}
		}
		return FinishDecision{
			Act: ActFail, Failed: true, Verdict: "FAIL", Head: in.Tip,
			Reason:       "no report after one recovery turn",
			Judgment:     "no report after one recovery turn",
			AttemptSpent: true, SendsFinish: true,
			UsageUnknown: usage.ZeroOrAbsent(),
		}
	}
	if !in.ReportPresent {
		return FinishDecision{Act: ActToday}
	}

	head := ""
	if hasCommit {
		head = in.Tip
	}
	var notes []string
	if verdict == "" && hasCommit {
		notes = append(notes, "verdict line missing, inferred ok from the report")
	}
	if fullSha.MatchString(reportHead) && head != "" && !strings.EqualFold(reportHead, head) {
		notes = append(notes, fmt.Sprintf("report named %s, branch is %s", reportHead, head))
	}
	if verdict == "" && !hasCommit {
		return FinishDecision{Act: ActToday}
	}
	dec := FinishDecision{
		Act: ActFinish, Head: head, Judgment: strings.Join(notes, "; "),
		AttemptSpent: true, SendsFinish: hasCommit,
	}
	switch {
	case verdict == "":
		dec.Verdict, dec.OK = "ok", true
	case verdict == "LAND" && head != "":
		dec.Verdict, dec.OK = "LAND", true
	default:
		dec.Verdict, dec.Failed = verdict, true
	}
	if usage.ZeroOrAbsent() && (hasCommit || sentence != "") {
		dec.UsageUnknown = true
	}
	return dec
}

// applyInLane is whether the daemon takes the decision instead of the lane's end.
// A measured zero (or a failed token read) and a cost-only report are taken. A finish
// is taken when the lane committed, so the head is the branch tip. An unmeasured
// exit with no report is not: that remains the lane's harness fault.
func (d FinishDecision) applyInLane(in FinishInput) bool {
	switch d.Act {
	case ActRecover, ActFail:
		return true
	case ActFault:
		return in.Usage.Measured || strings.Contains(in.Report, "Cost:")
	case ActFinish:
		return in.hasCommit()
	default:
		return false
	}
}

// RecoveryPrompt is the one recovery turn: the branch, its tip, the commits, and the
// card's finish form.
func RecoveryPrompt(branch, tip string, commits []string, form string) string {
	if strings.TrimSpace(form) == "" {
		form = FinishForm
	}
	return fmt.Sprintf("Your branch %s at %s has these commits: %s. Write REPORT.md in the form below and stop.\n\n%s\n",
		branch, tip, strings.Join(commits, ", "), strings.TrimRight(form, "\n"))
}

func faultJudgment(in FinishInput) string {
	exit := fmt.Sprintf("exit %d", in.Exit)
	if s := oneLine(in.Stderr, 200); s != "" {
		exit += ": " + s
	}
	return fmt.Sprintf("fault: %s lane for %s ran no model: %s", in.Friend, in.Card, exit)
}

// modelSentence is the report's first sentence from the model: not a verdict, a head,
// a cost line, or a blank. Empty when the report carries none.
func modelSentence(report string) string {
	for _, l := range strings.Split(report, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		key, _ := reportKey(l)
		switch key {
		case "verdict", "head", "cost", "tokens", "reader", "result":
			continue
		}
		if strings.Contains(l, "tokens input=") || strings.HasPrefix(l, "Cost:") {
			continue
		}
		return l
	}
	return ""
}

// modelSaid is a sentence the model wrote, not one this daemon writes when it finishes.
func modelSaid(report string) bool {
	s := modelSentence(report)
	switch {
	case s == "":
		return false
	case strings.HasPrefix(s, "nova-friend lane "), strings.HasPrefix(s, "fault: "), strings.HasPrefix(s, "no report after "):
		return false
	default:
		return true
	}
}

// costLineZero is a cost line whose token counts say the harness ran no model.
func costLineZero(report string) bool {
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, "input=0") && strings.Contains(l, "output=0") {
			return true
		}
	}
	return false
}

// measuredZero is a token read that reported counts and every one of them is zero.
// Unreported (no token source) is not zero: it was not measured.
func measuredZero(t cardcost.Tokens) bool {
	if !t.Reported() {
		return false
	}
	for _, n := range []int64{t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning} {
		if n > 0 {
			return false
		}
	}
	return true
}

func finishFormOf(brief string) string {
	var lines []string
	for _, l := range strings.Split(brief, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if (strings.Contains(l, "Verdict:") && strings.Contains(l, "LAND")) || (strings.Contains(l, "Head:") && strings.Contains(l, "sha")) {
			lines = append(lines, oneLine(l, 200))
		}
		if len(lines) == 4 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

var (
	briefBaseRE = regexp.MustCompile(`(?m)^BASE:\s*(\S+)`)
	stagedSHA   = regexp.MustCompile(`, ([0-9a-f]{40}), on branch`)
)

// branchFinishArgv is the sprint finish of a decision this contract sends. The head is
// the branch tip. OK is not --failed. usage unknown rides on the attempt, never priced
// as free.
func branchFinishArgv(friend string, c Card, branch string, d FinishDecision, note string) []string {
	argv := []string{"finish", "--as", "friend." + friend, c.ID + "@" + fmt.Sprint(c.Gen()), "--epoch", c.Epoch()}
	if !d.OK {
		argv = append(argv, "--failed")
	}
	if fullSha.MatchString(d.Head) {
		argv = append(argv, "--head", d.Head)
	}
	if branch != "" {
		argv = append(argv, "--branch", branch)
	}
	words := strings.TrimSpace(d.Verdict + ": " + note)
	if d.UsageUnknown && !strings.Contains(words, "usage unknown") {
		words += " usage unknown"
	}
	return append(argv, "--report", "friend "+friend+" "+oneLine(words, ReportChars))
}

// usageUnknownCost is the Cost: line of a finish whose work was real and whose usage
// the harness did not report. The ledger counts it unpriced, never as free.
func usageUnknownCost(model, route string) string {
	if model == "" {
		model = "-"
	}
	if route == "" {
		route = "-"
	}
	return fmt.Sprintf("Cost: unpriced (usage unknown) tokens input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=%s harness=- price_route=%s", model, route)
}

func withUsageUnknown(report, line string) string {
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for i, l := range lines {
		key, _ := reportKey(l)
		if key == "cost" || strings.HasPrefix(strings.TrimSpace(l), "Cost:") {
			lines[i] = line
			return strings.Join(lines, "\n") + "\n"
		}
	}
	return WithCost(report, line)
}

func withBranchHead(report, tip string) string {
	if !fullSha.MatchString(tip) {
		return report
	}
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for i, l := range lines {
		if key, _ := reportKey(l); key == "head" {
			lines[i] = "Head: " + tip
			return strings.Join(lines, "\n") + "\n"
		}
	}
	out := make([]string, 0, len(lines)+1)
	inserted := false
	for _, l := range lines {
		out = append(out, l)
		if !inserted {
			if key, _ := reportKey(l); key == "verdict" {
				out = append(out, "Head: "+tip)
				inserted = true
			}
		}
	}
	if !inserted {
		out = append([]string{"Head: " + tip}, out...)
	}
	return strings.Join(out, "\n") + "\n"
}

// withInferredOK puts the inferred verdict on a report that had none, and keeps the
// model's text under the judgment, so a later read is not refused for a missing line.
func withInferredOK(report, head, judgment string) string {
	var b strings.Builder
	b.WriteString("Verdict: LAND\n")
	if fullSha.MatchString(head) {
		fmt.Fprintf(&b, "Head: %s\n", head)
	}
	if judgment != "" {
		fmt.Fprintf(&b, "\n%s\n", judgment)
	}
	if report != "" {
		if !strings.HasSuffix(b.String(), "\n\n") {
			b.WriteString("\n")
		}
		b.WriteString(report)
		if !strings.HasSuffix(report, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func failReport(verdict, head, paragraph string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Verdict: %s\n", verdict)
	if fullSha.MatchString(head) {
		fmt.Fprintf(&b, "Head: %s\n", head)
	}
	fmt.Fprintf(&b, "\n%s\n", paragraph)
	return b.String()
}

func writeUsageUnknown(outbox, model, route string) error {
	line := usageUnknownCost(model, route)
	reportPath := filepath.Join(outbox, "REPORT.md")
	if raw, err := os.ReadFile(reportPath); err == nil {
		if err := atomicfile.WriteFile(reportPath, []byte(withUsageUnknown(string(raw), line)), 0o644); err != nil {
			return err
		}
	}
	resultPath := filepath.Join(outbox, "RESULT.md")
	raw, err := os.ReadFile(resultPath)
	if err != nil || strings.Contains(string(raw), "usage unknown") {
		return nil
	}
	out := strings.TrimRight(string(raw), "\n") + "\ncost: unpriced (usage unknown)\n"
	return atomicfile.WriteFile(resultPath, []byte(out), 0o644)
}

// checkoutHasHEAD is a checkout whose git HEAD is a file, a directory .git or a
// worktree's .git file. A ref dropped under .git with no HEAD is not one (a test's
// packed ref is not a checkout this contract reads).
func checkoutHasHEAD(dir string) bool {
	dot := filepath.Join(dir, ".git")
	if _, err := os.Stat(dot); err != nil {
		return false
	}
	st, err := os.Stat(filepath.Join(gitDir(dot), "HEAD"))
	return err == nil && st.Mode().IsRegular()
}

// findCheckout is the job's git checkout: jobs/<job>/repo when that is one, else the
// first directory under the job whose .git has a HEAD, three levels down at most.
func findCheckout(dir, job string) string {
	root := JobDir(dir, job)
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return ""
	}
	if checkoutHasHEAD(filepath.Join(root, "repo")) {
		return filepath.Join(root, "repo")
	}
	if checkoutHasHEAD(root) {
		return root
	}
	found := ""
	// ignored: a walk error means no checkout is found, and the lane reads no commits
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if found != "" {
			return filepath.SkipAll
		}
		if err != nil || e == nil || !e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel != "." && strings.Count(rel, string(filepath.Separator)) > 2 {
			return filepath.SkipDir
		}
		if checkoutHasHEAD(path) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func laneBaseOf(dir, job, brief string) string {
	if raw, err := os.ReadFile(filepath.Join(JobDir(dir, job), JobFile)); err == nil {
		if m := stagedSHA.FindSubmatch(raw); m != nil {
			return string(m[1])
		}
	}
	if m := briefBaseRE.FindStringSubmatch(brief); m != nil {
		return m[1]
	}
	return ""
}

// readLaneCommits is the lane's own commits on the checkout: git log of base..HEAD,
// the base the job was staged at or the brief's BASE. Empty when the lane committed
// nothing, or when there is no base to tell the lane's commits from the history.
// err is set only when git itself could not be read.
func readLaneCommits(ctx context.Context, checkout, base string) (tip string, commits []string, err error) {
	if checkout == "" || !checkoutHasHEAD(checkout) {
		return "", nil, nil
	}
	opt := gitrun.Options{C: checkout, OwnRepo: true}
	tip, err = gitrun.Output(ctx, opt, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", nil, err
	}
	tip = strings.ToLower(strings.TrimSpace(tip))
	if !fullSha.MatchString(tip) {
		return "", nil, fmt.Errorf("HEAD %q is not a full sha", tip)
	}
	spec := ""
	base = strings.TrimSpace(base)
	switch {
	case fullSha.MatchString(strings.ToLower(base)):
		spec = strings.ToLower(base) + "..HEAD"
	case base != "":
		for _, c := range []string{"origin/" + base, base} {
			if _, e := gitrun.Output(ctx, opt, "rev-parse", "--verify", "--quiet", c); e == nil {
				spec = c + "..HEAD"
				break
			}
		}
	}
	if spec == "" {
		return "", nil, nil
	}
	out, err := gitrun.Output(ctx, opt, "log", "--oneline", "--max-count=20", spec)
	if err != nil {
		return "", nil, err
	}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			commits = append(commits, l)
		}
	}
	if len(commits) == 0 {
		return "", nil, nil
	}
	return tip, commits, nil
}

// laneCommitted says the lane's checkout has commits of its own. A checkout with
// no HEAD is not one: a ref dropped under .git is not read, and git is not run.
func (l *loop) laneCommitted(card Card) bool {
	if l == nil || l.d == nil {
		return false
	}
	job := filepath.Base(card.Outbox)
	checkout := findCheckout(l.d.Dir, job)
	if !checkoutHasHEAD(checkout) {
		return false
	}
	brief := ""
	if raw, err := os.ReadFile(card.Brief); err == nil {
		brief = string(raw)
	}
	_, commits, err := readLaneCommits(context.WithoutCancel(l.ctx), checkout, laneBaseOf(l.d.Dir, job, brief))
	return err == nil && len(commits) > 0
}

func (ln *lane) dropCard() {
	ln.card = nil
	ln.attempts = 0
	ln.recoverText = ""
	ln.recoveryDone = false
}

// leaveToday is a turn the lane's end already owns: a cap, a silent stop, a restart.
func leaveToday(t *turn, end LaneEnd, ln *lane, now time.Time) bool {
	if t == nil {
		return true
	}
	if t.stopped || t.capped || end.Capped > 0 || !end.Restart.IsZero() {
		return true
	}
	if ln != nil && ln.cap > 0 && !end.Started.IsZero() && now.Sub(end.Started) >= ln.cap {
		return true
	}
	return false
}

// finishInput reads the lane's report, usage and branch. ok is false when the report
// is there and cannot be read: the lane's end says so, and this contract does not guess.
func (l *loop) finishInput(ln *lane, card Card, r laneResult, now time.Time) (FinishInput, bool) {
	d := l.d
	job := filepath.Base(card.Outbox)
	report, there, err := readReport(filepath.Join(d.Dir, "outbox"), job)
	if err != nil {
		return FinishInput{}, false
	}
	brief := ""
	if raw, e := os.ReadFile(card.Brief); e == nil {
		brief = string(raw)
	}
	branch, _ := "", ""
	if m := statusBranch.FindStringSubmatch(brief); m != nil {
		branch = m[1]
	}
	in := FinishInput{
		Friend: d.Friend, Card: card.ID, Branch: branch,
		Report: report, ReportPresent: there,
		Exit: r.turn.Exit, Form: finishFormOf(brief),
		RecoveryDone: ln.recoveryDone,
	}
	if l.lanes.cardFaults != nil {
		in.Faults = l.lanes.cardFaults[job]
	}
	if r.t != nil && r.t.tail != nil {
		in.Stderr = LastLines(r.t.tail.String(), 1)
	}
	if in.Stderr == "" {
		in.Stderr = r.turn.FirstError
	}
	if in.Stderr == "" && r.err != nil {
		in.Stderr = r.err.Error()
	}
	if d.Tokens != nil && ln.session != "" {
		if cur, ok := l.tokens(ln.session); ok && ln.baseOK {
			spent := cur.Sub(ln.base)
			in.Usage.Measured = true
			switch {
			case measuredZero(spent.Tokens):
				in.Usage.Zero = true
			case !spent.Tokens.Reported():
				in.Usage.Absent = true
			}
		} else {
			in.Usage.Measured = true
			in.Usage.Absent = true
		}
	}
	tip, commits, gitErr := readLaneCommits(context.WithoutCancel(l.ctx), findCheckout(d.Dir, job), laneBaseOf(d.Dir, job, brief))
	if gitErr != nil {
		in.GitUnreadable = true
		d.Record(now.UTC().Format(time.RFC3339) + " finish: the branch tip of " + job + " cannot be read: " + oneLine(gitErr.Error(), 200))
	}
	in.Tip, in.Commits = tip, commits
	return in, true
}

// branchFinish applies the finish contract to a lane turn that has ended. False leaves
// the turn to the lane's end.
func (l *loop) branchFinish(r laneResult, card Card, end LaneEnd, line string, now time.Time) bool {
	ln := r.ln
	if ln == nil || leaveToday(r.t, end, ln, now) {
		return false
	}
	in, ok := l.finishInput(ln, card, r, now)
	if !ok {
		return false
	}
	dec := DecideFinish(in)
	if !dec.applyInLane(in) {
		return false
	}
	l.applyBranchFinish(ln, card, in, dec, line, end.Wall, now)
	return true
}

func (l *loop) applyBranchFinish(ln *lane, card Card, in FinishInput, dec FinishDecision, line string, wall time.Duration, now time.Time) {
	d, s := l.d, l.lanes
	job := filepath.Base(card.Outbox)
	at := now.UTC().Format(time.RFC3339)
	switch dec.Act {
	case ActRecover:
		ln.recoverText = dec.Prompt
		d.Record(line + fmt.Sprintf(" card=recover reason=%q", "no report, commits on "+dash(in.Branch)+"; one recovery turn"))
	case ActFault:
		l.handBack(ln, card, job, now)
		if s.cardFaults == nil {
			s.cardFaults = map[string]int{}
		}
		s.cardFaults[job] = in.Faults + 1
		d.Record(line + fmt.Sprintf(" card=fault turn=%d reason=%q", s.cardFaults[job], dec.Judgment))
		l.tellKind(bus.KindBlocker, oneLine(dec.Judgment, 300), "The attempt was not spent. The card is back in its queue.\n", now)
	case ActFail:
		l.failFromBranch(ln, card, in, dec, line, wall, now)
	case ActFinish:
		l.finishFromBranch(ln, card, in, dec, line, wall, now)
	default:
		d.Record(at + " finish: " + job + " not taken (" + dec.Act + ")")
	}
}

// handBack returns a fault's card to the queue: no report left for the outbox to finish,
// the lane mark lifted, the attempt not spent.
func (l *loop) handBack(ln *lane, card Card, job string, now time.Time) {
	_ = os.Remove(card.Report()) // ignored: a report that is already gone is the fault's end
	_ = os.Remove(card.Result()) // ignored: a result left behind would keep the card out of the queue
	delete(l.lanes.state.Started, job)
	_ = os.Remove(laneMarkPath(l.d.Dir, job)) // ignored: a mark already gone leaves the card free to claim
	l.saveLanes(now)
	ln.dropCard()
}

func (l *loop) failFromBranch(ln *lane, card Card, in FinishInput, dec FinishDecision, line string, wall time.Duration, now time.Time) {
	d := l.d
	job := filepath.Base(card.Outbox)
	paragraph := dec.Reason
	if paragraph == "" {
		paragraph = dec.Judgment
	}
	report := failReport(dec.Verdict, dec.Head, paragraph)
	words := "finish=failed"
	if dec.Head != "" {
		words += " head=" + dec.Head
	}
	if err := os.MkdirAll(card.Outbox, 0o755); err != nil {
		words += fmt.Sprintf(" report_error=%q", err.Error())
	} else if err := atomicfile.WriteFile(card.Report(), []byte(report), 0o644); err != nil {
		words += fmt.Sprintf(" report_error=%q", err.Error())
	}
	if d.Finish != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
		err := d.Finish(ctx, branchFinishArgv(d.Friend, card, in.Branch, dec, paragraph))
		cancel()
		if err != nil {
			words += fmt.Sprintf(" sent=sync finish_error=%q", oneLine(err.Error(), 300))
		} else {
			words += " sent=server"
			l.markOutboxFinished(job)
		}
	} else {
		words += " sent=sync"
	}
	if err := endLaneMark(d.Dir, job, l.laneWho(ln.n)); err != nil {
		words += fmt.Sprintf(" mark_error=%q", oneLine(err.Error(), 300))
	}
	delete(l.lanes.state.Started, job)
	delete(l.lanes.cardFaults, job)
	l.lanes.given[job] = true
	l.lanes.state.GivenUp = append(l.lanes.state.GivenUp, job)
	l.saveLanes(now)
	l.finishNote(ln, card, wall, now)
	ln.dropCard()
	d.Record(line + fmt.Sprintf(" card=failed %s reason=%q", words, paragraph))
}

func (l *loop) finishFromBranch(ln *lane, card Card, in FinishInput, dec FinishDecision, line string, wall time.Duration, now time.Time) {
	d := l.d
	job := filepath.Base(card.Outbox)
	report := in.Report
	verdictWas := dec.Verdict
	if verdictWas == "LAND" || verdictWas == "ok" {
		if held := unaddressed(HeldByDaemon, report, jobFix(card.Brief, "")); held != "" {
			dec.OK, dec.Failed, dec.Verdict = false, true, "HOLD"
			if dec.Judgment != "" {
				dec.Judgment = held + "; " + dec.Judgment
			} else {
				dec.Judgment = held
			}
		}
	}
	switch {
	case verdictWas == "ok" && dec.Verdict == "HOLD":
		report = failReport("HOLD", dec.Head, dec.Judgment) + in.Report
		if in.Report != "" && !strings.HasSuffix(in.Report, "\n") {
			report += "\n"
		}
	case verdictWas == "ok":
		report = withInferredOK(in.Report, dec.Head, dec.Judgment)
	case dec.Head != "":
		report = withBranchHead(report, dec.Head)
		if dec.Judgment != "" && !strings.Contains(report, dec.Judgment) {
			report = strings.TrimRight(report, "\n") + "\n\n" + dec.Judgment + "\n"
		}
	}
	if err := os.MkdirAll(card.Outbox, 0o755); err == nil && report != in.Report {
		// ignored: a report that cannot be rewritten is still finished from the branch tip
		_ = atomicfile.WriteFile(card.Report(), []byte(report), 0o644)
	}
	if dec.UsageUnknown {
		route := ""
		if d.Route != nil {
			route = d.Route().Name
		}
		if err := writeUsageUnknown(card.Outbox, d.Model, route); err != nil {
			d.Record(fmt.Sprintf("%s cost: %s: %s", now.UTC().Format(time.RFC3339), card.ID, oneLine(err.Error(), 300)))
		}
	}
	note := dec.Judgment
	if note == "" {
		note = reportPara(report)
	}
	words := "finish=ok"
	if !dec.OK {
		words = "finish=failed"
	}
	if dec.Head != "" {
		words += " head=" + dec.Head
	}
	if dec.UsageUnknown {
		words += " usage=unknown"
	}
	if d.Finish != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
		err := d.Finish(ctx, branchFinishArgv(d.Friend, card, in.Branch, dec, note))
		cancel()
		if err != nil {
			words += fmt.Sprintf(" sent=sync finish_error=%q", oneLine(err.Error(), 300))
		} else {
			words += " sent=server"
			l.markOutboxFinished(job)
		}
	} else {
		words += " sent=sync"
	}
	if err := endLaneMark(d.Dir, job, l.laneWho(ln.n)); err != nil {
		words += fmt.Sprintf(" mark_error=%q", oneLine(err.Error(), 300))
	}
	delete(l.lanes.state.Started, job)
	delete(l.lanes.cardFaults, job)
	l.saveLanes(now)
	l.finishNote(ln, card, wall, now)
	ln.dropCard()
	extra := ""
	if dec.Judgment != "" {
		extra = fmt.Sprintf(" judgment=%q", dec.Judgment)
	}
	d.Record(line + " card=done " + words + extra)
}

func (l *loop) markOutboxFinished(job string) {
	o := &l.d.outbox
	if o.finished == nil {
		o.finished = map[string]bool{}
		o.tried = map[string]time.Time{}
		o.said = map[string]bool{}
	}
	o.finished[job] = true
}

// takeRecovery is the recovery prompt, and marks the recovery turn started. The second
// result is false when this turn is not that recovery.
func (ln *lane) takeRecovery() (string, bool) {
	if ln == nil || ln.recoverText == "" {
		return "", false
	}
	text := ln.recoverText
	ln.recoverText = ""
	ln.recoveryDone = true
	return text, true
}
