package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// answer (docs/SPEC-SPRINT.md section 8, answered by nova-decide; docs/SPEC-NOVA-DECIDE.md
// section 9): the routine judgments of the inbox answered by the judgment decision. For each
// card of each routine judgment it asks the decision (the judgment's kind and text, the card's
// log, the verbs the judgment prints), applies the verb chosen at or above the bar by the
// commands the inbox prints for that card, and lists the rest for the coordinator: every drop,
// everything under the bar, and a card a provider refused for want of payment, which is never
// asked (a payment is the owner's). Every decision goes to the record under its judgment's id,
// and the outcome (landed, dropped, came back) is attached to it once the card's state says.
//
// The record is the guard against applying a decision twice: "applying" and the decision's
// operation id are recorded before its first verb runs, every verb carries that id as its
// --op, and "applied" or "refused" is recorded after. A pass stopped between the two leaves
// "applying", which the next pass finishes through the same ids: a verb that ran replays its
// recorded result and changes nothing.
//
// It is a client of the sprint like the coordinator's shell: it reads and writes through the
// verbs (inbox, card, log, routes and the answer verbs), sent to the server when
// NOVA_SPRINT_SERVER names one, else run here on the store --redis names. Nothing it does is a
// move the coordinator could not type.

// answerRow is one card of one judgment, and what was done with it.
type answerRow struct {
	Judgment string  `json:"judgment"`
	Note     string  `json:"note,omitempty"`
	Card     string  `json:"card,omitempty"`
	Kind     string  `json:"kind"`
	Verb     string  `json:"verb,omitempty"`
	P        float64 `json:"p,omitempty"`
	Act      string  `json:"act"` // applied, would-apply, listed, refused, failed, left
	Why      string  `json:"why,omitempty"`
	Decision string  `json:"decision,omitempty"` // its id in the record
	Recorded string  `json:"recorded,omitempty"` // new, existing
}

// The acts a row reports; actApplying is the record's alone, a decision whose verbs began.
const (
	actApplying   = "applying"
	actApplied    = "applied"
	actWouldApply = "would-apply"
	actListed     = "listed"
	actRefused    = "refused"
	actFailed     = "failed"
	actLeft       = "left"
)

// kindOther is the kind column of a judgment left alone: not a routine kind, its type in the why.
const kindOther = "other"

// answerOutcome is an outcome attached to an earlier decision in this pass.
type answerOutcome struct {
	Decision string `json:"decision"`
	Card     string `json:"card"`
	Label    string `json:"label"`
}

// answerer is one answer: how it reaches the sprint (call), the backend and the record of
// the judgment decision, how long one ask may take, its bar, and the clock its record is
// stamped by.
type answerer struct {
	call    func(ctx context.Context, argv []string) (sprintwire.Result, error)
	backend decide.Backend
	timeout time.Duration
	record  string
	bar     string // --bar as given; "" reads the sprint row's
	barSet  bool   // this pass has a bar: with none, nothing is applied
	dry     bool
	now     func() time.Time
	done    map[string]bool // the command lines run in this pass: a note's verb runs once
}

// answerPass is what one pass did.
type answerPass struct {
	Rows     []answerRow     `json:"rows"`
	Outcomes []answerOutcome `json:"outcomes"`
	Bar      *float64        `json:"bar"` // null: no bar is set, and nothing is applied
	Machine  string          `json:"machine"`
	Record   string          `json:"record"`
	DryRun   bool            `json:"dry_run,omitempty"`
}

func (p answerPass) count(act string) int {
	n := 0
	for _, r := range p.Rows {
		if r.Act == act {
			n++
		}
	}
	return n
}

// stopped says the machine is not running (STOPPED, or DONE): the loop form ends.
func (p answerPass) stopped() bool {
	return strings.Contains(p.Machine, "STOPPED") || strings.Contains(p.Machine, store.DoneState)
}

func (a *app) cmdAnswer(args []string, stdout, stderr io.Writer) int {
	// no --op, --epoch or --max: each verb a pass applies carries its decision's own op
	// (decide.<decision id>), and a pass lists every row
	fs, c := verbflag.New("answer"), &common{verb: "answer"}
	c.registerStore(fs, a.getenv)
	dry := fs.Bool("dry-run", false, "ask the decision and print what would be applied; apply nothing and write no record")
	bar := fs.String("bar", "", "apply a verb whose probability is at or above this bar (else the sprint row's decide_judgment_bar; with neither, nothing is applied: every decision is recorded and what a bar would apply is listed)")
	every := fs.Duration("every", 0, "run a pass every duration until the machine is STOPPED (or DONE): the coordinator seat's loop; 0 is one pass")
	backend := fs.String("backend", "jev", "the decision's backend: jev (its key from JEV_API_KEY, which nova-secrets exec sets) or fixed (--answers)")
	answers := fs.String("answers", "", "the fixed backend's answers, a JSON file (--backend fixed)")
	timeout := fs.Duration("timeout", decide.JevTimeout, "how long one ask of the backend may take; an ask past it is that card's failed row, and nothing is applied for it")
	record := fs.String("record", "", "the judgment decisions' record, JSON lines (default ~/nova-sprint/decide/judgment.jsonl, its directory made 0700)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "answer", argErr("takes no words ", err, pos...))
	}
	var problems []string
	if *bar != "" {
		if _, err := decide.ParseBar(*bar); err != nil {
			problems = append(problems, "--bar: "+err.Error())
		}
	}
	if *every < 0 {
		problems = append(problems, "--every wants a duration above zero, or 0 for one pass")
	}
	if *timeout <= 0 {
		problems = append(problems, "--timeout wants a duration above zero")
	}
	if *backend != "jev" && *backend != "fixed" {
		problems = append(problems, "--backend "+*backend+" is not jev or fixed")
	}
	if (*backend == "fixed") != (*answers != "") {
		problems = append(problems, "--answers <file> goes with --backend fixed, and only with it")
	}
	if c.actor == "" {
		problems = append(problems, "--actor <name> is required (or NOVA_SPRINT_ACTOR): the answers are the coordinator's")
	}
	if len(problems) > 0 {
		return refuse(stderr, "answer", strings.Join(problems, "; "))
	}
	w := &answerer{bar: *bar, dry: *dry, now: a.now, record: *record, timeout: *timeout}
	if w.record == "" {
		home, err := a.home()
		if err != nil {
			return refuse(stderr, "answer", "no home directory for the default record ("+err.Error()+"); give --record <file>")
		}
		w.record = filepath.Join(home, "nova-sprint", "decide", "judgment.jsonl")
	}
	if w.backend, err = a.judgmentBackend(*backend, *answers, *timeout); err != nil {
		return refuse(stderr, "answer", err.Error())
	}
	server := a.server(fs)
	w.call = func(ctx context.Context, argv []string) (sprintwire.Result, error) {
		if server != "" {
			n := readVerb(argv).words
			return a.ask(ctx, server, argv[:n], argv[n:])
		}
		return a.runHere(argv, *c), nil
	}
	ctx, cancel := a.notify(context.Background())
	defer cancel()
	why, err := w.coordinator(ctx, c.actor)
	if err != nil {
		fmt.Fprintf(stderr, "%s answer: %s\n", prog, oneline.WithRemedy(err.Error(), "nova-sprint where"))
		return 2
	}
	if why != "" {
		return refuse(stderr, "answer", why)
	}
	if !w.dry {
		if err := os.MkdirAll(filepath.Dir(w.record), 0o700); err != nil {
			return refuse(stderr, "answer", "the record's directory: "+err.Error())
		}
	}
	for {
		p, err := w.pass(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s answer: %s\n", prog, oneline.WithRemedy(err.Error(), "nova-sprint where"))
			return 2
		}
		code := printPass(p, c.json, stdout)
		if *every == 0 {
			return code
		}
		if p.stopped() {
			fmt.Fprintf(stdout, "ANSWER STOPPED %s: the loop ends\n", strings.TrimPrefix(p.Machine, "machine: "))
			return 0
		}
		a.sleep(*every)
		if ctx.Err() != nil {
			return 0
		}
	}
}

// runHere runs one verb in this process, on the store the answer verb was given, as its
// actor: the answer verb with no server.
func (a *app) runHere(argv []string, c common) sprintwire.Result {
	n := readVerb(argv).words
	words := slices.Concat(argv[:n], []string{"--actor", c.actor}, argv[n:])
	if c.redis != "" {
		words = slices.Concat(argv[:n], []string{"--redis", c.redis, "--actor", c.actor}, argv[n:])
	}
	var out, errb strings.Builder
	code := a.run(words, &out, &errb)
	return sprintwire.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
}

// judgmentBackend is the judgment decision's backend: the test's, a fixed file, or Jev with
// the key from the environment (a missing key fails each ask, naming nova-secrets exec), its
// client bounded by timeout.
func (a *app) judgmentBackend(kind, answers string, timeout time.Duration) (decide.Backend, error) {
	if a.decider != nil {
		return a.decider, nil
	}
	if kind == "fixed" {
		raw, err := os.ReadFile(answers)
		if err != nil {
			return nil, fmt.Errorf("--answers: %w", err)
		}
		return decide.ParseFixed(raw)
	}
	key := a.getenv(decide.JevSecret)
	if key == "" {
		return noKey{}, nil
	}
	return decide.JevHTTP(key, timeout), nil
}

// noKey is the Jev backend with no key in the environment: every ask fails, saying how to
// give it one, so a pass that has nothing to ask needs none.
type noKey struct{}

func (noKey) Name() string { return "jev:" + decide.JevModel }
func (noKey) Ask(context.Context, decide.Schema, string) (map[string]decide.Answer, decide.Usage, error) {
	return nil, decide.Usage{}, errors.New(decide.JevSecret + " is absent from this environment; run under nova-secrets exec --only " + decide.JevSecret + " -- nova-sprint answer")
}

// get runs a read and decodes its JSON.
func (w *answerer) get(ctx context.Context, v any, argv ...string) error {
	res, err := w.call(ctx, argv)
	if err != nil {
		return fmt.Errorf("the sprint did not answer %s: %w", argv[0], err)
	}
	if res.Code != 0 {
		return fmt.Errorf("%s: %s", strings.Join(argv, " "), leadLine(res.Stderr+res.Stdout))
	}
	return json.Unmarshal([]byte(res.Stdout), v)
}

// coordinator is why the actor may not answer, "" when it may: the judgments are the
// coordinator's alone.
func (w *answerer) coordinator(ctx context.Context, actor string) (string, error) {
	var in struct {
		Coordinator string `json:"coordinator"`
	}
	if err := w.get(ctx, &in, "inbox", "--json"); err != nil {
		return "", err
	}
	if in.Coordinator != actor {
		return "answer is the coordinator's alone: " + in.Coordinator + ", not " + actor + "; nothing was changed", nil
	}
	return "", nil
}

// judgmentBar is the bar this pass applies at: --bar, else the sprint row's; set is false
// when neither gives one, and then nothing is applied (the row ships empty: the owner,
// 2026-10-03, "same rule as the other layers").
func (w *answerer) judgmentBar(ctx context.Context) (bar float64, set bool, err error) {
	raw := w.bar
	if raw == "" {
		var r struct {
			Bar string `json:"decide_judgment_bar"`
		}
		if err := w.get(ctx, &r, "routes", "--json"); err != nil {
			return 0, false, err
		}
		raw = r.Bar
	}
	if raw == "" {
		return 0, false, nil
	}
	bar, err = decide.ParseBar(raw)
	return bar, err == nil, err
}

// pass answers the inbox once and attaches the outcomes now known.
func (w *answerer) pass(ctx context.Context) (answerPass, error) {
	w.done = map[string]bool{}
	bar, set, err := w.judgmentBar(ctx)
	if err != nil {
		return answerPass{}, err
	}
	var in struct {
		Groups  []sprint.Group `json:"groups"`
		Machine string         `json:"machine"`
	}
	if err := w.get(ctx, &in, "inbox", "--json"); err != nil {
		return answerPass{}, err
	}
	w.barSet = set
	p := answerPass{Machine: in.Machine, Record: w.record, DryRun: w.dry, Rows: []answerRow{}, Outcomes: []answerOutcome{}}
	if set {
		p.Bar = &bar
	}
	// the outcomes first: a card that came back is seen before this pass answers it again
	if !w.dry {
		if p.Outcomes, err = w.outcomes(ctx); err != nil {
			return p, err
		}
	}
	for _, g := range in.Groups {
		if g.Kind != sprint.Judgment || g.Quiet {
			continue // a judgment the coordinator set a wait on is the coordinator's until it comes due
		}
		kind, routine := decide.Kinds[g.Type]
		if !routine {
			p.Rows = append(p.Rows, answerRow{Judgment: g.ID, Kind: kindOther, Act: actLeft, Why: "not a routine kind (" + g.Type + "): the coordinator's"})
			continue
		}
		rows, err := w.group(ctx, g, kind, bar)
		if err != nil {
			return p, err
		}
		p.Rows = append(p.Rows, rows...)
	}
	return p, nil
}

// group answers one routine judgment, card by card.
func (w *answerer) group(ctx context.Context, g sprint.Group, kind string, bar float64) ([]answerRow, error) {
	var open struct {
		Open []string `json:"open"`
	}
	if err := w.get(ctx, &open, "inbox", "--open", g.ID, "--json"); err != nil {
		return nil, err
	}
	members := open.Open
	if len(members) == 0 {
		members = g.Primaries
	}
	var rows []answerRow
	for _, card := range members {
		r, err := w.card(ctx, g, kind, card, len(members), bar)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// cardJSON is what answer reads of card --json.
type cardJSON struct {
	Primary *sprint.Card  `json:"primary"`
	Open    []sprint.Open `json:"open"`
}

// card asks the decision for one card of a judgment and does what it chose.
func (w *answerer) card(ctx context.Context, g sprint.Group, kind, card string, cards int, bar float64) (answerRow, error) {
	row := answerRow{Judgment: g.ID, Card: card, Kind: kind}
	var cv cardJSON
	if err := w.get(ctx, &cv, "card", card, "--json"); err != nil {
		return row, err
	}
	note, found := judgmentOn(cv.Open, g)
	if !found {
		note = sprint.Note{ID: g.ID, Kind: sprint.Judgment, Type: g.Type, Stream: g.Stream, Decisions: g.Decisions, What: g.What, Card: card, Primaries: []string{card}}
	}
	row.Note = note.ID
	// a judgment of one card is answered by the lines the inbox prints for it; one card of
	// several, by the lines the inbox prints for a group of that card alone
	cmds := g.Commands
	if cards > 1 {
		cmds = cardCommands(note, card)
	}
	allowed := allowedVerbs(cmds)
	res, err := w.call(ctx, []string{"log", "--card", card, "--max", "0"})
	if err != nil {
		return row, fmt.Errorf("the sprint did not answer log: %w", err)
	}
	history := decide.HistoryOf(res.Stdout)
	last := history[max(0, len(history)-10):]
	if decide.PaymentRefusal(append([]string{note.What}, last...)...) {
		row.Act, row.Why = actListed, "a provider refused it for want of payment (402, out of credit): a payment is the owner's"
		return row, nil
	}
	row.Decision = note.ID
	if len(note.Primaries) > 1 {
		row.Decision = note.ID + ":" + card
	}
	d, existing, err := w.decide(ctx, row.Decision, decide.JudgmentInput{Kind: g.Type, Text: note.What, Card: card, Cards: cards, History: history, Allowed: allowed})
	if err != nil {
		row.Act, row.Why = actFailed, err.Error()
		return row, nil
	}
	row.Recorded = map[bool]string{true: "existing", false: "new"}[existing]
	ch := decide.Choose(d.Answers, allowed, bar) // with no bar set, bar is 0: what any bar would allow
	row.Verb, row.P = ch.Verb, ch.P
	if !existing {
		d.Inputs = map[string]string{"judgment": g.ID, "note": note.ID, "kind": kind, "card": card, "verb": ch.Verb}
	}
	act, op, at := applyState(d)
	if act == actApplied {
		row.Act, row.Why = actListed, "applied at "+at+" and the judgment is still open: the coordinator's"
		return row, nil
	}
	// a decision whose verbs began and whose end is not recorded (a pass stopped between
	// them) is finished through the same op, past the guards it passed when it began
	resume := act == actApplying
	if op == "" {
		op = opOf(row.Decision)
	}
	lines, why := fill(commandOf(cmds, ch.Verb), ch)
	if why == "" {
		why = ackOfSeveral(ch.Verb, note)
	}
	if why == "" && ch.Verb == decide.VerbRework && !resume {
		if at, err := w.reworkedWithin(card, ReworkWindow); err != nil {
			return row, err
		} else if at != "" {
			why = "nova-decide reworked it at " + at + ", within the hour: the coordinator's"
		}
	}
	switch {
	case !resume && ch.Act != decide.ActApply:
		row.Act, row.Why = actListed, ch.Why
	case why != "":
		row.Act, row.Why = actListed, why
	case !w.barSet && resume:
		row.Act, row.Why = actListed, "no decide_judgment_bar is set, so nothing is applied; applying it began at "+at+" and its end is not recorded: a pass with a bar finishes it through --op "+op
	case !w.barSet:
		row.Act, row.Why = actListed, "no decide_judgment_bar is set, so nothing is applied; at a bar at or under "+strconv.FormatFloat(ch.P, 'f', 2, 64)+" it would apply: "+shown(lines)
	case w.dry:
		row.Act, row.Why = actWouldApply, shown(withOp(lines, op))
	default:
		var err error
		row.Act, row.Why, err = w.applyRecorded(ctx, d, existing, resume, op, lines)
		return row, err
	}
	if !existing && !w.dry {
		d.Inputs["act"] = row.Act
		if _, err := decide.Append(w.record, d); err != nil {
			return row, fmt.Errorf("the record %s: %w", w.record, err)
		}
	}
	return row, nil
}

// ackOfSeveral is why an ack chosen for one card is not applied: it names a note of
// several cards, and would answer the others too; "" for any other.
func ackOfSeveral(verb string, n sprint.Note) string {
	if verb == decide.VerbAck && len(n.Primaries) > 1 {
		return "the ack names a note of several cards: the coordinator's"
	}
	return ""
}

// applyState is where applying d stands: its last act line, else the act it was recorded
// with; the op its verbs carry; and when that was.
func applyState(d decide.Decision) (act, op, at string) {
	if n := len(d.Acts); n > 0 {
		a := d.Acts[n-1]
		return a.Act, a.Op, a.At
	}
	return d.Inputs["act"], d.Inputs["op"], d.At
}

// opOf is the operation id a decision's verbs carry: its record id, less the '~' the
// sprint keeps for its epochs.
func opOf(id string) string {
	return "decide." + strings.ReplaceAll(id, "~", "_")
}

// withOp is a decision's lines each carrying its op: the decision's own for one line,
// <op>.<n> for the n-th of several.
func withOp(lines [][]string, op string) [][]string {
	out := make([][]string, len(lines))
	for i, l := range lines {
		id := op
		if len(lines) > 1 {
			id = op + "." + strconv.Itoa(i+1)
		}
		out[i] = slices.Concat(l, []string{"--op", id})
	}
	return out
}

// applyRecorded applies a decision with the record as its guard: "applying" and the op
// are recorded before the first verb runs (on the decision's own line when it is new),
// every verb carries the op, and the end (applied, refused) is recorded after. A
// resumed decision has its "applying" already.
func (w *answerer) applyRecorded(ctx context.Context, d decide.Decision, existing, resume bool, op string, lines [][]string) (string, string, error) {
	stamp := func() string { return w.now().UTC().Format(time.RFC3339) }
	var err error
	switch {
	case !existing:
		d.Inputs["act"], d.Inputs["op"] = actApplying, op
		_, err = decide.Append(w.record, d)
	case !resume:
		err = decide.RecordAct(w.record, decide.Act{ID: d.ID, Act: actApplying, Op: op, At: stamp()})
	}
	if err != nil {
		return actFailed, "", fmt.Errorf("the record %s: %w", w.record, err)
	}
	act, why := w.apply(ctx, lines, op)
	if err := decide.RecordAct(w.record, decide.Act{ID: d.ID, Act: act, Op: op, At: stamp()}); err != nil {
		return act, why, fmt.Errorf("the record %s: %w", w.record, err)
	}
	return act, why, nil
}

// decide is the recorded decision under id, or a new one asked through the backend (not
// yet recorded: the record takes it with what was done).
func (w *answerer) decide(ctx context.Context, id string, in decide.JudgmentInput) (decide.Decision, bool, error) {
	ds, err := decide.Load(w.record)
	if err != nil {
		return decide.Decision{}, false, err
	}
	if have := decide.Find(ds, id); have != nil {
		return *have, true, nil // a judgment's card is decided once
	}
	s, state := decide.JudgmentSchema(), decide.JudgmentState(in)
	actx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	answers, usage, err := decide.Ask(actx, w.backend, s, state)
	if err != nil {
		if errors.Is(actx.Err(), context.DeadlineExceeded) {
			return decide.Decision{}, false, fmt.Errorf("backend %s did not answer within %s (--timeout): %w", w.backend.Name(), w.timeout, err)
		}
		return decide.Decision{}, false, fmt.Errorf("backend %s: %w", w.backend.Name(), err)
	}
	return decide.Decision{ID: id, Decision: s.Name, Schema: s.Hash(), Backend: w.backend.Name(), At: w.now().UTC().Format(time.RFC3339),
		State: state, Answers: answers, Usage: usage}, false, nil
}

// ReworkWindow is how long after the decision reworked a card it reworks it no more: a
// card that comes back within it is the coordinator's (the night of 2026-10-02: a card
// reworked again and again under a dead provider).
const ReworkWindow = time.Hour

// reworkedWithin is when the record says the decision last applied a rework to the card,
// or began to (applying, its end not recorded), within window of now; "" when it did not.
func (w *answerer) reworkedWithin(card string, window time.Duration) (string, error) {
	ds, err := decide.Load(w.record)
	if err != nil {
		return "", err
	}
	for i := len(ds) - 1; i >= 0; i-- {
		d := ds[i]
		if d.Decision != decide.JudgmentName || d.Inputs["card"] != card || d.Inputs["verb"] != decide.VerbRework {
			continue
		}
		act, _, at := applyState(d)
		if act != actApplied && act != actApplying {
			continue
		}
		if t, err := time.Parse(time.RFC3339, at); err == nil && w.now().Sub(t) < window {
			return at, nil
		}
	}
	return "", nil
}

// apply runs a decision's commands in order, each carrying its op and each once a pass
// (a note's line two of its cards chose); the first refused stops it.
func (w *answerer) apply(ctx context.Context, lines [][]string, op string) (string, string) {
	run := withOp(lines, op)
	for i, argv := range run {
		key := strings.Join(lines[i], "\x00")
		if w.done[key] {
			continue
		}
		w.done[key] = true
		res, err := w.call(ctx, argv)
		if err != nil {
			return actRefused, "the sprint did not answer " + argv[0] + ": " + err.Error()
		}
		if res.Code != 0 {
			return actRefused, leadLine(res.Stderr + res.Stdout)
		}
	}
	return actApplied, shown(run)
}

// shown is commands as the coordinator would type them, one after another.
func shown(lines [][]string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = prog + " " + quoteLine(l)
	}
	return strings.Join(out, "; ")
}

// judgmentOn is the card's open judgment of the group: a note of the group's type, one of
// the group's notes when the card names it.
func judgmentOn(open []sprint.Open, g sprint.Group) (sprint.Note, bool) {
	var typed *sprint.Note
	for i := range open {
		n := open[i].Note
		if n.Kind != sprint.Judgment || n.Type != g.Type {
			continue
		}
		if slices.Contains(g.Notes, n.ID) {
			return n, true
		}
		if typed == nil {
			typed = &open[i].Note
		}
	}
	if typed != nil {
		return *typed, true
	}
	return sprint.Note{}, false
}

// cardCommands is the lines the inbox prints for the note as a group of the card alone,
// with the card named where a group's form would name --group <note> --expect 1 (a note
// is no group's id when it is not its group's oldest).
func cardCommands(n sprint.Note, card string) []sprint.Command {
	cmds := sprint.NoteCommands(n, []string{card})
	for i := range cmds {
		for j, l := range cmds[i].Lines {
			cmds[i].Lines[j] = strings.Replace(l, " --group "+n.ID+" --expect 1", " "+card, 1)
		}
	}
	return cmds
}

// allowedVerbs is the verbs the printed decisions make, in their order.
func allowedVerbs(cmds []sprint.Command) []string {
	var out []string
	for _, c := range cmds {
		if v := decide.VerbOf(c.Decision); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// commandOf is the lines of the first printed decision that makes verb.
func commandOf(cmds []sprint.Command, verb string) []string {
	for _, c := range cmds {
		if decide.VerbOf(c.Decision) == verb {
			return c.Lines
		}
	}
	return nil
}

// fill is the printed lines as the verbs' words, their placeholders filled from the choice:
// '<fix>' the fix's text, '<why>' the reason's, '<why nothing is to be done>' the ack's. A
// line that wants anything else, or is no nova-sprint line, is why it cannot be applied.
func fill(lines []string, ch decide.Chosen) ([][]string, string) {
	if len(lines) == 0 {
		return nil, "the judgment prints no command for " + orDashStr(ch.Verb, "its choice")
	}
	var out [][]string
	for _, l := range lines {
		ws, err := words(l)
		if err != nil || len(ws) == 0 || ws[0] != prog {
			return nil, "the line " + l + " is no nova-sprint verb: the coordinator's"
		}
		for i, wd := range ws {
			if !strings.HasPrefix(wd, "<") || !strings.HasSuffix(wd, ">") {
				continue
			}
			text := ""
			switch wd {
			case "<fix>":
				text = decide.Fixes[ch.Fix]
			case "<why>":
				text = decide.Reasons[ch.Reason]
			case "<why nothing is to be done>":
				text = decide.AckReason(ch.P)
			}
			if text == "" {
				return nil, fmt.Sprintf("%s wants %s, which the decision did not give (fix=%s)", ch.Verb, wd, orDashStr(ch.Fix, "-"))
			}
			ws[i] = text
		}
		out = append(out, ws[1:])
	}
	return out, ""
}

// words splits a printed line as a shell does for the lines the inbox prints: words
// apart by spaces, a '...' one word with its quotes taken off.
func words(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, have := false, false
	for _, r := range line {
		switch {
		case r == '\'':
			in, have = !in, true
		case r == ' ' && !in:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if in {
		return nil, errors.New("an unmatched quote")
	}
	if have {
		out = append(out, cur.String())
	}
	return out, nil
}

// quoteLine is words as a shell takes them: a word holding a blank or a quote is quoted,
// a quote in it closed, escaped and opened again.
func quoteLine(ws []string) string {
	q := make([]string, len(ws))
	for i, w := range ws {
		if w == "" || strings.ContainsAny(w, " '") {
			w = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
		q[i] = w
	}
	return strings.Join(q, " ")
}

// MaxOutcomeLooks bounds the earlier decisions a pass looks up for their outcome.
const MaxOutcomeLooks = 25

// outcomes attaches to earlier judgment decisions the outcome their card's state now says,
// newest first, at most MaxOutcomeLooks a pass.
func (w *answerer) outcomes(ctx context.Context) ([]answerOutcome, error) {
	ds, err := decide.Load(w.record)
	if err != nil {
		return nil, err
	}
	out := []answerOutcome{}
	looks := 0
	for i := len(ds) - 1; i >= 0 && looks < MaxOutcomeLooks; i-- {
		d := ds[i]
		if d.Decision != decide.JudgmentName || d.Outcome != nil || d.Inputs["card"] == "" {
			continue
		}
		looks++
		var cv cardJSON
		if err := w.get(ctx, &cv, "card", d.Inputs["card"], "--json"); err != nil {
			continue // a card the sprint no longer knows (a clear) has no outcome to read
		}
		other := slices.ContainsFunc(cv.Open, func(o sprint.Open) bool {
			return o.Note.Kind == sprint.Judgment && o.Note.ID != d.Inputs["note"]
		})
		col := ""
		if cv.Primary != nil {
			col = cv.Primary.Col
		}
		label := decide.JudgmentOutcome(col, cv.Primary.Placed(), other)
		if label == "" {
			continue
		}
		act, _, _ := applyState(d)
		o := decide.Outcome{ID: d.ID, Label: label, Note: "the card is " + orDashStr(col, "off the table") + "; act=" + act, At: w.now().UTC().Format(time.RFC3339)}
		if _, _, err := decide.Attach(w.record, o); err != nil {
			return out, fmt.Errorf("the record %s: %w", w.record, err)
		}
		out = append(out, answerOutcome{Decision: d.ID, Card: d.Inputs["card"], Label: label})
	}
	return out, nil
}

// printPass prints one pass, the table and its summary line, or one JSON object; its
// exit code is 1 when a verb was refused or a decision failed.
func printPass(p answerPass, asJSON bool, stdout io.Writer) int {
	code := 0
	if p.count(actRefused)+p.count(actFailed) > 0 {
		code = 1
	}
	if asJSON {
		b, _ := json.Marshal(p) // ignored: plain fields always encode
		fmt.Fprintln(stdout, string(b))
		return code
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "judgment\tcard\tkind\tverb\tp\tact\twhy")
	for _, r := range p.Rows {
		p := "-"
		if r.Verb != "" {
			p = strconv.FormatFloat(r.P, 'f', 2, 64)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", oneline.Field(r.Judgment), orDashStr(oneline.Field(r.Card), "-"), oneline.Field(r.Kind),
			orDashStr(r.Verb, "-"), p, r.Act, oneline.Escape(r.Why))
	}
	_ = tw.Flush() // ignored: the caller's stdout
	for _, o := range p.Outcomes {
		fmt.Fprintf(stdout, "OUTCOME %s card=%s label=%s\n", oneline.Field(o.Decision), oneline.Field(o.Card), o.Label)
	}
	word := "OK"
	if p.DryRun {
		word = "DRY-RUN"
	}
	bar := "-"
	if p.Bar != nil {
		bar = strconv.FormatFloat(*p.Bar, 'f', 2, 64)
	}
	fmt.Fprintf(stdout, "ANSWER %s rows=%d applied=%d would_apply=%d listed=%d refused=%d failed=%d left=%d outcomes=%d bar=%s record=%s; run: nova-sprint inbox\n", word,
		len(p.Rows), p.count(actApplied), p.count(actWouldApply), p.count(actListed), p.count(actRefused), p.count(actFailed), p.count(actLeft),
		len(p.Outcomes), bar, oneline.Field(p.Record))
	return code
}

// leadLine is the first line of a verb's output, for a row's why.
func leadLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}
