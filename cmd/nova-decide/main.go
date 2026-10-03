// nova-decide is a decision system trained from its own record
// (docs/SPEC-NOVA-DECIDE.md). The decide side asks a named, typed question set
// (a schema) over one state through a backend and prints every answer with its
// probabilities; the train side records each decision, attaches its outcome
// when it is known, and calibrates the bar a decision is trusted at from the
// decisions whose outcome is known. The dispatch, the banner, the help, the
// version verb, the refusals and the output envelope are internal/tool's; the
// decisions, the backends and the record are internal/decide's.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// world is what the tool reaches outside itself: the clock every record stamp
// reads, the environment the backend's key comes from, and the Jev transport.
// main passes the real one; a test passes its own, so no test opens a socket,
// reads the real clock or needs a key.
type world struct {
	now    func() time.Time
	getenv func(string) string
	send   func(key string) decide.Send
}

func realWorld() world {
	return world{now: time.Now, getenv: os.Getenv,
		send: func(key string) decide.Send { return decide.HTTPSend(http.DefaultClient, decide.JevURL, key) }}
}

func main() { os.Exit(decideTool(realWorld()).Main()) }

const fixture = "./cmd/nova-decide/testdata/"

func decideTool(w world) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-decide",
		What:  "typed decisions with probabilities, recorded so each one can be calibrated against its outcome",
		Stamp: version,
		How: `a decision is a named schema of typed questions (choice or noul) asked over a state.
A backend answers it: jev (TypeSafe's System One model, key from JEV_API_KEY) or fixed (a file).
Every decision is appended to the record (--record, JSON lines), with its state and answers.
outcome attaches what turned out true; calibrate reads the record and prints the bar it supports.
first run: from a checkout root (the examples read its testdata); fixed backend, no network or key.`,
		ExitTable: "0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).",
		Verbs: []tool.Verb{
			{
				Name:    "ask",
				Usage:   "ask --schema <file> --state <file|-> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "ask --schema " + fixture + "schema.json --state " + fixture + "state.txt --backend fixed --answers " + fixture + "answers.json --record ./decisions.jsonl --op first",
				Effect:  tool.Delivery + "; with --backend jev it sends the state to the backend, and it appends to --record",
				Detail: `A schema is {"name": <decision>, "questions": {<name>: {"type": "choice"|"noul",
"instructions": <text>, "criteria": {<option>: <meaning>}}}}; criteria is for a choice only.
Each answer prints as one ANSWER line: a choice's value and every option's p, a noul's p of yes.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("schema", "the decision's schema, a JSON file")
					f.Required("state", "the text the decision is made over, a file, or - for stdin")
					w.asking(f)
					f.Op()
				},
				Run: w.ask,
			},
			{
				Name:    "read",
				Usage:   "read --card <file> --diff <file> [--rule <file>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "read --card " + fixture + "card.md --diff " + fixture + "card.diff --backend fixed --answers " + fixture + "read-answers.json --record ./decisions.jsonl --op card-1",
				Effect:  tool.Delivery + "; with --backend jev it sends the card and diff to the backend, and it appends to --record",
				Detail: `The read decision: a worker's diff against the card that asked for it, five questions:
does_task, lines_changed, inside_paths and defect (nouls, p of yes) and verdict (LAND, BOUNCE
or UNSURE). The state is the card, the rule when --rule names one, and the diff, nothing else.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("card", "the card the worker was given, a file")
					f.Required("diff", "the worker's unified diff, a file")
					f.String("rule", "", "a rule text the read holds the diff to as well, a file")
					w.asking(f)
					f.Op()
				},
				Run: w.read,
			},
			{
				Name:    "brief",
				Usage:   "brief --card <file|dir> --backend <jev|fixed> [--answers <file>] --record <file> [--width <n>] [--timeout <d>] [--max <n>] [--dry-run]",
				Example: "brief --card " + fixture + "greet.md --backend fixed --answers " + fixture + "brief-answers.json --record ./decisions.jsonl",
				Effect:  tool.Delivery + "; with --backend jev it sends each card to the backend, and it appends to --record",
				Detail: `The brief decision: a card's text alone, as a flash child with no memory reads it, before
the card is added. Six nouls (repo_branch, files_named, gate_stated, commit_stated,
report_stated, one_thing), ambiguous_step (none, step-<n> or unnumbered), minutes, and
converges, p that the child lands it on its first attempt. A directory is every *.md under it,
each card's id its file's name without .md; each decision's id is <card>@brief-<hex>. One BRIEF
CARD item per card, in id order; a card the backend failed is named, the rest are recorded.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("card", "a card file, or a directory of *.md card files")
					w.asking(f)
					f.Max()
					f.Int("width", 8, "how many cards are asked at once")
					f.Check(func(c *tool.Call) {
						if c.Int("width") < 1 {
							c.Problem("--width must be at least 1")
						}
					})
				},
				Run: w.brief,
			},
			{
				Name:    "outcome",
				Usage:   "outcome --record <file> --id <decision-id> --label <word> [--note <text>] [--dry-run]",
				Example: "outcome --record ./decisions.jsonl --id card-1 --label ok --note \"the review found nothing\"",
				Effect:  tool.LocalWrite + ": appends one outcome line to --record",
				Detail:  "The same label again changes nothing; another label for a labelled decision is exit 1.",
				DryRun:  true,
				Flags: func(f *tool.Flags) {
					f.Required("record", "the record file the decision is in")
					f.Required("id", "the decision's id, as ask or read printed it")
					f.Required("label", "what turned out true, one word (ok, wrong, pass, fail)")
					f.String("note", "", "why, in a sentence: the finding behind the label")
				},
				Run: w.outcome,
			},
			{
				Name:    "calibrate",
				Usage:   "calibrate --record <file> --decision <name> --question <name[=option]> --positive <label,...> --negative <label,...> [--bars <p,...>]",
				Example: "calibrate --record " + fixture + "record.jsonl --decision read --question defect --positive wrong --negative ok",
				Effect:  tool.Inspection,
				Detail: `Scores each labelled decision by the p its answer gave (a noul's yes, or a choice's
named option: verdict=BOUNCE), and prints the AUC, one BAR line per --bars value (positives
caught, negatives bounced), and the CATCH-ALL bar: the highest that still flags every positive.`,
				Flags: func(f *tool.Flags) {
					f.Required("record", "the record file")
					f.Required("decision", "the decision's name, as its schema names it (read)")
					f.Required("question", "the answer scored: a noul's name, or <choice>=<option>")
					f.Required("positive", "the outcome labels the answer should flag, comma-separated")
					f.Required("negative", "the outcome labels it should pass, comma-separated")
					f.String("bars", "0.5,0.7,0.9", "the thresholds to report, comma-separated probabilities")
					f.Check(func(c *tool.Call) {
						if _, err := bars(c.Str("bars")); err != nil {
							c.Problem(err.Error())
						}
					})
				},
				Run: calibrate,
			},
		},
	}
}

// asking declares what ask, read and brief share: the backend, the record and
// the deadline, with the rules between them.
func (w world) asking(f *tool.Flags) {
	f.Required("backend", "the backend that answers: jev or fixed")
	f.String("answers", "", "the fixed backend's answers, a JSON file (--backend fixed only)")
	f.Required("record", "the record file every decision is appended to (JSON lines; created if absent)")
	f.Duration("timeout", time.Minute, "how long the backend may take to answer")
	f.Check(func(c *tool.Call) {
		switch b := c.Str("backend"); {
		case b == "fixed" && !c.Given("answers"):
			c.Problem("--backend fixed answers from --answers <file>; name the file")
		case b == "jev" && c.Given("answers"):
			c.Problem("--answers is the fixed backend's; --backend jev asks the model")
		case b == "jev" && w.getenv(decide.JevSecret) == "" && !c.DryRun():
			c.Problem(decide.JevSecret + " is absent from this environment; run under `nova-secrets exec --only " + decide.JevSecret + " -- nova-decide ...` (the key is never a flag or a file)")
		case b != "" && b != "jev" && b != "fixed":
			c.Problem(fmt.Sprintf("--backend %q is no backend; it wants jev or fixed", b))
		}
		if c.Dur("timeout") <= 0 {
			c.Problem("--timeout must be above zero")
		}
	})
}

// ask names every input it cannot read or parse in one refusal (ONBOARDING point 2).
func (w world) ask(c *tool.Call) *tool.Out {
	var problems []string
	raw, err := os.ReadFile(c.Str("schema"))
	var s decide.Schema
	if err == nil {
		s, err = decide.ParseSchema(raw)
	}
	if err != nil {
		problems = append(problems, err.Error())
	}
	state, err := readInput(c.Str("state"), c.Stdin)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return tool.Refuse(problems...)
	}
	return w.decision(c, s, string(state), map[string]string{"state": c.Str("state"), "state_sha256": decide.Sum(state)})
}

func (w world) read(c *tool.Call) *tool.Out {
	inputs, texts := map[string]string{}, map[string]string{}
	var problems []string
	for _, name := range []string{"card", "diff", "rule"} {
		if !c.Given(name) {
			continue
		}
		raw, err := os.ReadFile(c.Str(name))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		texts[name] = string(raw)
		inputs[name], inputs[name+"_sha256"] = c.Str(name), decide.Sum(raw)
	}
	if len(problems) > 0 {
		return tool.Refuse(problems...)
	}
	return w.decision(c, decide.ReadSchema(), decide.ReadState(texts["card"], texts["diff"], texts["rule"]), inputs)
}

// brief makes the brief decision of every card --card names, as one batch.
func (w world) brief(c *tool.Call) *tool.Out {
	cards, err := decide.CardFiles(c.Str("card"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	b, err := w.backend(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	record := c.Str("record")
	if c.DryRun() {
		ds, err := decide.Load(record)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		held := 0
		for id, text := range cards {
			if decide.Find(ds, decide.BriefOp(id, text)) != nil {
				held++
			}
		}
		return tool.Done().Fact("decision", decide.BriefName).Fact("backend", b.Name()).Fact("cards", len(cards)).
			Fact("recorded", held).Fact("to_ask", len(cards)-held)
	}
	made, err := decide.Briefs(context.Background(), b, cards, record, w.now(), c.Int("width"), c.Dur("timeout"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o, failed, asked := tool.Done(), 0, 0
	for _, m := range made {
		id := m.Inputs["card"]
		switch {
		case m.Err != nil:
			failed++
			o.Item("card", "id", id, "op", m.ID, "recorded", "no", "error", tool.Text(m.Err.Error()))
			continue
		case !m.Existing:
			asked++
		}
		br := decide.BriefOf(m.Decision)
		o.Item("card", "id", id, "op", m.ID, "p_converges", round(br.Converges), "minutes", br.Minutes,
			"failed", strings.Join(br.Failed, ","), "recorded", map[bool]string{true: "existing", false: "new"}[m.Existing])
	}
	if failed > 0 {
		o.Status, o.Exit, o.Why = tool.Failed, 2, []string{fmt.Sprintf("the backend answered %d of %d cards; each one it failed is named on its BRIEF CARD line, and nothing was recorded for it", len(made)-failed, len(made))}
		o.Remedy = "run the same line again: a recorded card is answered from the record and asks nothing"
	}
	return o.Fact("decision", decide.BriefName).Fact("backend", b.Name()).Fact("cards", len(made)).Fact("asked", asked).
		Fact("existing", len(made)-asked-failed).Fact("failed", failed)
}

// decision makes one decision and records it (decide.Make): an op id already
// recorded over the same state returns the recorded decision and asks nothing.
func (w world) decision(c *tool.Call, s decide.Schema, state string, inputs map[string]string) *tool.Out {
	record, now := c.Str("record"), w.now()
	id := c.Str("op")
	if id == "" {
		id = s.Name + "-" + decide.Sum([]byte(s.Hash() + "\n" + now.UTC().Format(time.RFC3339) + "\n" + state))[:12]
	}
	b, err := w.backend(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if c.DryRun() {
		ds, err := decide.Load(record)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		if have := decide.Find(ds, id); have != nil {
			if err := decide.Replays(*have, s, state); err != nil {
				return tool.Refuse(err.Error())
			}
			return answered(*have, "existing")
		}
		return tool.Done().Fact("id", id).Fact("decision", s.Name).Fact("backend", b.Name()).
			Fact("questions", len(s.Questions)).Fact("state_bytes", len(state)).Fact("recorded", "no")
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	d, existing, err := decide.Make(ctx, b, s, state, record, id, inputs, now)
	var failed *decide.BackendError
	switch {
	case errors.As(err, &failed):
		o := tool.Fail(err.Error()).Fact("id", id).Fact("backend", b.Name())
		o.Exit, o.Remedy = 2, "make --answers answer every question of the schema"
		if _, isJev := b.(decide.Jev); isJev {
			o.Remedy = "check the backend's account and the key nova-secrets delivers, then run the same line again"
		}
		return o
	case err != nil:
		return tool.Refuse(err.Error())
	case existing:
		return answered(d, "existing")
	}
	return answered(d, "new")
}

// answered is a decision's result: its id and backend, the verdict when the
// schema has one, and one ANSWER item per question in name order.
func answered(d decide.Decision, recorded string) *tool.Out {
	o := tool.Done().Fact("id", d.ID).Fact("decision", d.Decision).Fact("backend", d.Backend)
	if v, ok := d.Answers["verdict"]; ok && v.Type == decide.Choice {
		o.Fact("verdict", v.Value).Fact("p", round(v.Prob(v.Value)))
	}
	o.Fact("tokens_in", d.Usage.InputTokens).Fact("tokens_out", d.Usage.OutputTokens).Fact("recorded", recorded)
	for _, name := range slices.Sorted(maps.Keys(d.Answers)) {
		a := d.Answers[name]
		o.Item("answer", "question", name, "type", a.Type, "value", a.Value, "p", probs(a.P))
	}
	return o
}

// backend is the one --backend names.
func (w world) backend(c *tool.Call) (decide.Backend, error) {
	if c.Str("backend") == "fixed" {
		raw, err := os.ReadFile(c.Str("answers"))
		if err != nil {
			return nil, err
		}
		return decide.ParseFixed(raw)
	}
	return decide.Jev{Model: decide.JevModel, Send: w.send(w.getenv(decide.JevSecret))}, nil
}

func (w world) outcome(c *tool.Call) *tool.Out {
	record, id := c.Str("record"), c.Str("id")
	o := decide.Outcome{ID: id, Label: c.Str("label"), Note: c.Str("note"), At: w.now().UTC().Format(time.RFC3339)}
	if strings.ContainsAny(o.Label, " \t\n,=") {
		return tool.Refuse(fmt.Sprintf("--label %q is not one word; it wants a label such as ok or wrong", o.Label))
	}
	if c.DryRun() {
		ds, err := decide.Load(record)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		if decide.Find(ds, id) == nil {
			return tool.Refuse(fmt.Sprintf("%s: %s", id, decide.ErrUnknown))
		}
		return tool.Done().Fact("id", id).Fact("label", o.Label).Fact("recorded", "no")
	}
	d, changed, err := decide.Attach(record, o)
	var conflict *decide.ConflictError
	switch {
	case errors.As(err, &conflict):
		return tool.Fail(err.Error()).Fact("id", id)
	case err != nil:
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("id", id).Fact("decision", d.Decision).Fact("label", d.Outcome.Label).Fact("changed", changed)
}

func calibrate(c *tool.Call) *tool.Out {
	ds, err := decide.Load(c.Str("record"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	cal, err := decide.Calibrate(ds, c.Str("decision"), c.Str("question"), list(c.Str("positive")), list(c.Str("negative")))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o := tool.Done().Fact("decision", cal.Decision).Fact("schema", cal.Schema).Fact("question", cal.Question).
		Fact("option", cal.Option).Fact("positives", len(cal.Positives)).Fact("negatives", len(cal.Negatives)).
		Fact("skipped", cal.Skipped).Fact("auc", round(cal.AUC()))
	at, _ := bars(c.Str("bars")) // checked by the verb's flag rule
	for _, p := range at {
		row(o, "bar", cal, cal.At(p))
	}
	row(o, "catch-all", cal, cal.CatchAll())
	return o
}

func row(o *tool.Out, kind string, cal decide.Calibration, b decide.Bar) {
	o.Item(kind, "at", round(b.At), "caught", b.Caught, "of", len(cal.Positives), "bounced", b.Bounced, "of_negatives", len(cal.Negatives))
}

// bars parses --bars: probabilities in [0, 1], at least one.
func bars(s string) ([]float64, error) {
	var out []float64
	for _, f := range list(s) {
		p, err := strconv.ParseFloat(f, 64)
		if err != nil || p < 0 || p > 1 {
			return nil, fmt.Errorf("--bars %q holds %q; it wants probabilities from 0 to 1, comma-separated", s, f)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--bars is empty; it wants probabilities from 0 to 1, comma-separated")
	}
	return out, nil
}

func list(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// probs is an answer's probabilities as one field: option:p,... in option order.
func probs(p map[string]float64) string {
	parts := make([]string, 0, len(p))
	for _, k := range slices.Sorted(maps.Keys(p)) {
		parts = append(parts, k+":"+strconv.FormatFloat(round(p[k]), 'f', -1, 64))
	}
	return strings.Join(parts, ",")
}

func round(p float64) float64 { return float64(int(p*1000+0.5)) / 1000 }

// readInput reads a file, or stdin when the name is -.
func readInput(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}
