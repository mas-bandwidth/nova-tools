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
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// world is what the tool reaches outside itself: the clock every record stamp
// reads, the environment the backend's key comes from, the Jev transport (its
// client bounded by --timeout, as the ask's context is), and the deadline of a
// brief batch. main passes the real one; a test passes its own, so no test opens
// a socket, reads the real clock or needs a key.
type world struct {
	now      func() time.Time
	getenv   func(string) string
	send     func(key string, timeout time.Duration) decide.Send
	deadline time.Duration // brief's whole batch (decide.BriefDeadline, as nova-sprint add's)
}

func realWorld() world {
	return world{now: time.Now, getenv: os.Getenv, deadline: decide.BriefDeadline,
		send: func(key string, timeout time.Duration) decide.Send { return decide.JevHTTP(key, timeout).Send }}
}

func main() { os.Exit(decideTool(realWorld()).Main()) }

const fixture = "./cmd/nova-decide/testdata/"

func decideTool(w world) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-decide",
		What:  "typed decisions with probabilities, recorded so each one can be calibrated against its outcome",
		Stamp: version,
		How: `noul: a yes-or-no question answered with a probability of yes; choice: one option.
a decision is a named schema of choice or noul questions over a state; jev or fixed answers:
schema {"name":"q","questions":{"ok":{"type":"noul","instructions":"It asks."}}}
state R? fixed answers {"ok":{"noul":0.9}} print ASK OK id=f decision=q backend=fixed recorded=new
ASK ANSWER question=ok type=noul value=yes p=yes:0.9; exit 0 means recorded, never approved.`,
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
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
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
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
				},
				Run: w.read,
			},
			{
				Name:    "score",
				Usage:   "score --card <file> --diff <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "score --card " + fixture + "card.md --diff " + fixture + "card.diff --backend fixed --answers " + fixture + "score-answers.json --record ./decisions.jsonl --op card-1@landed@0123456789ab",
				Effect:  tool.Delivery + "; with --backend jev it sends the card and diff to the backend, and it appends to --record",
				Detail: `The score decision: a landed diff against its card, the read's five questions and one noul
per escalation class the reviews found (stranded_fragment, cut_citation, renamed_file_assumed,
ledger_ceiling, comment_contradicts_code, test_weakened, record_made_claim, invented_reason,
fenced_block_edit, asserted_data_cut, load_bearing_word_cut; outside_paths is 1 - inside_paths).
The line names the top class and its p; nova-sprint land asks it as <card>@landed@<head>.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("card", "the card the worker was given, a file")
					f.Required("diff", "the landed unified diff, a file")
					w.asking(f)
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
				},
				Run: w.score,
			},
			{
				Name:    "attempt",
				Usage:   "attempt --brief <file> [--result <file>] --reason <line> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "attempt --brief " + fixture + "card.md --result " + fixture + "result.md --reason \"verdict not-done: tests red in internal/decide\" --backend fixed --answers " + fixture + "attempt-answers.json --record ./decisions.jsonl --op c1@1",
				Effect:  tool.Delivery + "; with --backend jev it sends the brief, result and reason to the backend, and it appends to --record",
				Detail: `The attempt decision: how a work take ended, one choice, class: done, nothing-to-do,
wrong-scope, no-result, needs-pro or provider-failure, each with its p. The state is the brief,
the child's RESULT.md (none when --result is not given) and the member's reason line.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("brief", "the card's brief the worker was given, a file")
					f.String("result", "", "the child's RESULT.md, a file; absent when the child wrote none")
					f.Required("reason", "the member's reason line for the take's end, as text")
					w.asking(f)
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
				},
				Run: w.attempt,
			},
			{
				Name:    "grade",
				Usage:   "grade --brief <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "grade --brief " + fixture + "card.md --backend fixed --answers " + fixture + "grade-answers.json --record ./decisions.jsonl --op c1@grade",
				Effect:  tool.Delivery + "; with --backend jev it sends the brief to the backend, and it appends to --record",
				Detail: `The grade decision: a card's convergence before its first deal, one choice, grade: script
(no model), flash or pro, each with its p. The state is the brief alone, or with --examples
ten landed cards per class (flash, pro, heavy) ahead of it, picked by --seed outside --held-out.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("brief", "the card's brief, a file")
					f.String("examples", "", "few-shot examples from the sprint record, a JSON-lines file of {card, heading, paths, kind, label} (SPEC-NOVA-DECIDE section 11)")
					f.String("held-out", "", "cards left out of the example pool, a file of card ids one per line (with --examples)")
					f.String("seed", "0", "the seed that picks the examples, so a run reproduces (with --examples)")
					w.asking(f)
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
				},
				Run: w.grade,
			},
			{
				Name:    "gate",
				Usage:   "gate --output <file> --card <file> [--diff <file>] [--base-red <test,...>] [--bars <flaky,pre-existing>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "gate --output " + fixture + "gate-output.txt --card " + fixture + "card.md --diff " + fixture + "card.diff --base-red TestPortInUse --backend fixed --answers " + fixture + "gate-answers.json --record ./decisions.jsonl --op c1@1@gate",
				Effect:  tool.Delivery + "; with --backend jev it sends each failure, the card's PATHS and the diff's summary to the backend, and it appends to --record",
				Detail: `The gate decision: a red gate's go test output, read failure by failure; each failing test
is classed flaky, caused or pre-existing (one choice, class, with a p per class) over its first
lines, whether it is red at the base (--base-red names those; without it the base is "not run"),
the gate's other failures, the card's PATHS and the diff's files. Each failure is a decision,
<op>/<pkg>.<Test>; a build failure is caused, unasked. A failure goes flaky at or above the first
--bars value (rerun it once), pre-existing at or above the second, else caused; an unset bar (the
default) routes no failure. The gate's route is caused when one failure is, else flaky when one
is, else pre-existing.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("output", "the gate's output, a file of go test's output (plain or -v)")
					f.Required("card", "the card the worker was given, a file: its PATHS line is read")
					f.String("diff", "", "the card's unified diff, a file: its files and line counts are summarised")
					f.String("base-red", "", "the failing tests red at the card's base, comma-separated (<Test> or <pkg>.<Test>); given empty, none is")
					f.String("bars", "", "the flaky and the pre-existing bars, <flaky>,<pre-existing>: each a probability or empty (that route taken by no failure), two set ones summing above 1; empty (the default) routes none, as the sprint row's defaults do; 0.8,0.8 is the starting point")
					w.asking(f)
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
					f.Check(func(c *tool.Call) {
						if _, err := gateBars(c.Str("bars")); err != nil {
							c.Problem(err.Error())
						}
					})
				},
				Run: w.gate,
			},
			{
				Name:    "brief",
				Usage:   "brief --card <file|dir> --backend <jev|fixed> [--answers <file>] --record <file> [--width <n>] [--timeout <d>] [--max <n>] [--dry-run]",
				Example: "brief --card " + fixture + "greet.md --backend fixed --answers " + fixture + "brief-answers.json --record ./decisions.jsonl",
				Effect:  tool.Delivery + "; with --backend jev it sends each card to the backend, and it appends to --record",
				Detail: `The brief decision: a card's text alone, as a flash child with no memory reads it, before
the card is added. Six nouls (repo_branch, files_named, gate_stated, commit_stated,
report_stated, one_thing), ambiguous_step (none, step-<n> or unnumbered), minutes, and
converges, p that the child lands it on its first attempt: a rank, uncalibrated. The batch has
one deadline, a minute, as nova-sprint add's; --timeout bounds each card. A directory is
its *.md files as nova-sprint add --brief-dir reads them (none below it), each card's id its
file's name without .md; each decision's id is <card>@brief-<hex>. One BRIEF CARD item per card,
in id order; a card the backend failed is named, the rest are recorded.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("card", "a card file, or a directory of *.md card files (as add --brief-dir reads it)")
					w.asking(f)
					f.Max()
					f.Int("width", decide.BriefWidth, "how many cards are asked at once")
					f.Check(func(c *tool.Call) {
						if c.Int("width") < 1 {
							c.Problem("--width must be at least 1")
						}
					})
				},
				Run: w.brief,
			},
			{
				Name:    "hold",
				Usage:   "hold --report <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]",
				Example: "hold --report " + fixture + "hold-reports/paths-too-narrow.txt --backend fixed --answers " + fixture + "hold-answers.json --record ./decisions.jsonl --op holdreport",
				Effect:  tool.Delivery + "; with --backend jev it sends the report to the backend, and it appends to --record",
				Detail: `The hold decision: a HOLD report's classification, over the report text alone.
Two questions: class (choice) with p per option (paths-too-narrow, missing-dependency,
already-done, work-defect, harness-failure), and proposed_paths (noul) p that PATHS-PROPOSED
line is present. ExtractProposedPaths reads PATHS-PROPOSED if present, else paths named as needed.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("report", "the HOLD report to classify, a file")
					w.asking(f)
					f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
				},
				Run: w.hold,
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
			{
				Name:   "import",
				Usage:  "import --record <file> [--verdicts <glob>] [--judgments <dir> --log <file>] [--reports <glob>] [--dry-run]",
				Effect: tool.LocalWrite + "; it appends to --record one labelled decision per item read, and leaves an item recorded before; --dry-run writes nothing",
				DryRun: true,
				Detail: `Loads finished decisions as labelled records, so they can be read, calibrated and trained on:
--verdicts: heavy-read VERDICT.md files, the first word of the first line is the label (ACCEPT, REWORK, ...);
--judgments with --log: a directory of judgment files (<judgment id>.md) labelled by the verb of the line a
nova-sprint log --json export holds that answers it; one nothing answers is counted as unanswered, not recorded;
--reports: REPORT.md files whose first line is "Verdict: HOLD", labelled HOLD.
An id comes from the source path and content, so a second import adds nothing. Prints IMPORT OK with
<kind>_new and <kind>_existing for verdict, judgment and report. The decisions are named import-<kind>.`,
				Flags: func(f *tool.Flags) {
					f.Required("record", "the record file the labelled decisions are appended to")
					f.String("verdicts", "", "a glob of heavy-read VERDICT.md files")
					f.String("judgments", "", "a directory of judgment files, <judgment id>.md; needs --log")
					f.String("log", "", "a nova-sprint log --json export holding the answers to the judgments")
					f.String("reports", "", "a glob of REPORT.md files; those beginning Verdict: HOLD are imported")
					f.Check(func(c *tool.Call) {
						if c.Str("verdicts") == "" && c.Str("judgments") == "" && c.Str("reports") == "" {
							c.Problem("no source: give --verdicts, --judgments with --log, or --reports")
						}
						if (c.Str("judgments") == "") != (c.Str("log") == "") {
							c.Problem("--judgments and --log go together: a judgment file holds no answer, the log export does")
						}
					})
				},
				Run: w.importRecords,
			},
			{
				Name:   "score-grades",
				Usage:  "score-grades --record <file> --log <file> [--day <date>]",
				Effect: tool.Inspection,
				Detail: `Grades Jev's grades against the sprint log, for the grade decisions made in one UTC day
(--day; default: the day before now). Each card is scored by its newest grade of the day against its
cost records in a nova-sprint log --json export: GRADE lines are Jev's grade by the tier the card was
first dealt on (n, landed by attempt 2, landed, escalated to pro, dropped, open); BUCKET lines are the
p of a flash or pro grade by whether the flash first attempt failed. A card the log lacks is counted
as no_log; a card never dealt to the fleet is left out. See docs/SPEC-NOVA-DECIDE.md section 11.`,
				Flags: func(f *tool.Flags) {
					f.Required("record", "the record file holding the grade decisions")
					f.Required("log", "a nova-sprint log --json export, the outcomes")
					f.String("day", "", "the UTC day scored, 2006-01-02; default: the day before now")
					f.Check(func(c *tool.Call) {
						if _, err := time.Parse(time.DateOnly, c.Str("day")); c.Str("day") != "" && err != nil {
							c.Problem(fmt.Sprintf("--day %q is not a date (2006-01-02)", c.Str("day")))
						}
					})
				},
				Run: w.scoreGrades,
			},
			{
				Name:    "findings",
				Usage:   "findings --record <file> [--since <time>] [--bar <p>] [--shadow <file> --real <file>] [--read-shadow <file> [--heavy <file>]]",
				Example: "findings --record " + fixture + "record.jsonl --since 2026-10-01",
				Effect:  tool.Inspection,
				Detail: `Clusters the score decisions made since --since by every class each gives a p at or above
--bar: one FINDING line per class (count, cards), most cards first; unnamed is p(defect) at or
above the bar with no class there. A class that keeps coming back is a finder rule or class test owed. With --shadow (the
judgment-shadow record) and --real (the judgment-answer record) it also joins each shadow answer
to the real one by note and card and prints one SHADOW line per judgment kind: pairs, agreement
percent, and agreement at p 0.95 and above. With --read-shadow (the read-shadow record) it prints
one SHADOW_READ line per gold, the readers' outcome and, with --heavy (a record import --verdicts
made), the heavy-read verdicts: cards joined, tp, fp, fn, tn, and the precision and recall of the
shadow read's BOUNCE (a shadow read is never a read).`,
				Flags: func(f *tool.Flags) {
					f.Required("record", "the record file")
					f.String("since", "", "the window's start, RFC 3339 or a date (2006-01-02, UTC); default: seven days before now")
					f.String("bar", "0.5", "the p at or above which a class counts, a probability")
					f.String("shadow", "", "the judgment-shadow record, to score Jev's shadow answers per judgment kind")
					f.String("real", "", "the judgment-answer record the shadow answers are joined to")
					f.String("read-shadow", "", "the read-shadow record, to score Jev's shadow reads against the readers' outcome")
					f.String("heavy", "", "a record imported from heavy-read verdicts (import --verdicts), to score the shadow reads against them too")
					f.Check(func(c *tool.Call) {
						if (c.Str("shadow") == "") != (c.Str("real") == "") {
							c.Problem("--shadow and --real go together: a shadow answer is scored against the real one")
						}
						if c.Given("heavy") && !c.Given("read-shadow") {
							c.Problem("--heavy goes with --read-shadow: the heavy verdicts score the shadow reads")
						}
						if _, err := since(c.Str("since"), time.Time{}); err != nil {
							c.Problem(err.Error())
						}
						if _, err := bars(c.Str("bar")); err != nil || len(list(c.Str("bar"))) != 1 {
							c.Problem(fmt.Sprintf("--bar %q wants one probability from 0 to 1", c.Str("bar")))
						}
					})
				},
				Run: w.findings,
			},
		},
	}
}

// asking declares what the deciding verbs (ask, read, score, attempt, grade, gate
// and brief) share: the backend, the record and the deadline, with the rules
// between them; each but brief declares its --op itself.
func (w world) asking(f *tool.Flags) {
	f.Required("backend", "the backend that answers: jev or fixed")
	f.String("answers", "", "the fixed backend's answers, a JSON file (--backend fixed only)")
	f.Required("record", "the record file every decision is appended to (JSON lines; created if absent)")
	f.Duration("timeout", decide.JevTimeout, "how long the backend may take to answer")
	f.Check(func(c *tool.Call) {
		switch b := c.Str("backend"); {
		case b == "fixed" && !c.Given("answers"):
			c.Problem("--backend fixed answers from --answers <file>; name the file")
		case b == "jev" && c.Given("answers"):
			c.Problem("--answers is the fixed backend's; --backend jev asks the model")
		case b == "jev" && w.getenv(decide.JevSecret) == "" && !c.DryRun():
			c.ProblemAs("key_absent", decide.JevSecret+" is absent from this environment; run under `nova-secrets exec --only "+decide.JevSecret+" -- nova-decide ...` (the key is never a flag or a file)")
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
	texts, inputs, refused := readFiles(c, "card", "diff", "rule")
	if refused != nil {
		return refused
	}
	return w.decision(c, decide.ReadSchema(), decide.ReadState(texts["card"], texts["diff"], texts["rule"]), inputs)
}

// score reads the card and the landed diff and makes the score decision.
func (w world) score(c *tool.Call) *tool.Out {
	texts, inputs, refused := readFiles(c, "card", "diff")
	if refused != nil {
		return refused
	}
	return w.decision(c, decide.ScoreSchema(), decide.ReadState(texts["card"], texts["diff"], ""), inputs)
}

// attempt asks the attempt decision over a brief, a result and a reason line.
func (w world) attempt(c *tool.Call) *tool.Out {
	texts, inputs, refused := readFiles(c, "brief", "result")
	if refused != nil {
		return refused
	}
	inputs["reason"] = c.Str("reason")
	return w.decision(c, decide.AttemptSchema(), decide.AttemptState(texts["brief"], texts["result"], c.Str("reason")), inputs)
}

// grade asks the grade decision over a brief.
func (w world) grade(c *tool.Call) *tool.Out {
	texts, inputs, refused := readFiles(c, "brief")
	if refused != nil {
		return refused
	}
	state := decide.GradeState(texts["brief"])
	if c.Given("examples") {
		raw, err := os.ReadFile(c.Str("examples"))
		var pool []decide.Example
		if err == nil {
			pool, err = decide.ParseExamples(raw)
		}
		held := map[string]bool{}
		if err == nil && c.Given("held-out") {
			var ids []byte
			if ids, err = os.ReadFile(c.Str("held-out")); err == nil {
				for _, id := range strings.Fields(string(ids)) {
					held[id] = true
				}
			}
		}
		var shots []decide.Example
		if err == nil {
			shots, err = decide.PickExamples(pool, held, c.Str("seed"), decide.GradeExamplesPerClass)
		}
		if err != nil {
			return tool.Refuse(err.Error())
		}
		state = decide.GradeStateWith(texts["brief"], shots)
		inputs["examples"], inputs["seed"] = c.Str("examples"), c.Str("seed")
	}
	return w.decision(c, decide.GradeSchema(), state, inputs)
}

// readFiles reads each named file flag that is given: its text, and the record's inputs
// (the path and its SHA-256); every file it cannot read is named in one refusal.
func readFiles(c *tool.Call, names ...string) (texts, inputs map[string]string, refused *tool.Out) {
	texts, inputs = map[string]string{}, map[string]string{}
	var problems []string
	for _, name := range names {
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
		return nil, nil, tool.Refuse(problems...)
	}
	return texts, inputs, nil
}

// scoreGrades prints the day's grade decisions scored against the sprint log
// (docs/SPEC-NOVA-DECIDE.md section 11, scoring the grades).
func (w world) scoreGrades(c *tool.Call) *tool.Out {
	ds, err := decide.Load(c.Str("record"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	f, err := os.Open(c.Str("log"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	defer f.Close() // ignored: the log is only read; its close can lose nothing the read returned
	facts, err := decide.ReadLog(f)
	if err != nil {
		return tool.Refuse(c.Str("log") + ": " + err.Error())
	}
	day := w.now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	if c.Given("day") {
		day, _ = time.Parse(time.DateOnly, c.Str("day")) // checked by the verb's flag rule
	}
	s := decide.ScoreGrades(ds, facts, day, day.Add(24*time.Hour))
	o := tool.Done().Fact("day", day.Format(time.DateOnly)).Fact("decisions", s.Decisions).Fact("cards", s.Cards).Fact("no_log", s.NoLog)
	for _, r := range s.Rows {
		o.Item("grade", "jev", r.Grade, "dealt", r.Dealt, "n", r.N, "landed2", r.Landed2, "landed", r.Landed, "to_pro", r.ToPro, "dropped", r.Dropped, "open", r.Open)
	}
	for _, b := range s.Buckets {
		o.Item("bucket", "jev", b.Grade, "p", b.Bucket, "n", b.N, "att1_failed", b.FirstFailed, "landed2", b.Landed2)
	}
	return o
}

// findings prints the record's score decisions in the window clustered by class;
// card values follow the one-token field model of internal/oneline.Field.
func (w world) findings(c *tool.Call) *tool.Out {
	ds, err := decide.Load(c.Str("record"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	bar, _ := strconv.ParseFloat(c.Str("bar"), 64) // checked by the verb's flag rule
	from, _ := since(c.Str("since"), w.now())      // checked by the verb's flag rule
	clusters, scored, skipped := decide.FindingsSkipped(ds, from, bar)
	o := tool.Done().Fact("scored", scored).Fact("classes", len(clusters)).Fact("bar", round(bar)).Fact("since", from.Format(time.RFC3339))
	for _, cl := range clusters {
		cards := make([]string, len(cl.Cards))
		for i, card := range cl.Cards {
			cards[i] = strings.ReplaceAll(oneline.Field(card), ",", `\x2c`)
		}
		o.Item("finding", "class", cl.Class, "count", cl.Count, "cards", strings.Join(cards, ","))
	}
	if len(skipped) > 0 {
		named := skipped[:min(len(skipped), 3)]
		for i, id := range named {
			named[i] = oneline.Field(id)
		}
		o.Note(fmt.Sprintf("%d score decisions skipped: at is not RFC 3339: %s", len(skipped), strings.Join(named, " ")))
	}
	if c.Str("read-shadow") != "" {
		reads, err := decide.Load(c.Str("read-shadow"))
		if err != nil {
			return tool.Refuse(err.Error())
		}
		var heavy []decide.Decision
		if c.Str("heavy") != "" {
			if heavy, err = decide.Load(c.Str("heavy")); err != nil {
				return tool.Refuse(err.Error())
			}
		}
		for _, s := range decide.ShadowReadScores(reads, heavy) {
			o.Item("shadow_read", "against", s.Against, "count", s.Count, "tp", s.TP, "fp", s.FP, "fn", s.FN, "tn", s.TN, "precision_pct", s.Precision, "recall_pct", s.Recall)
		}
	}
	if c.Str("shadow") == "" {
		return o
	}
	shadows, err := decide.Load(c.Str("shadow"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	real, err := decide.Load(c.Str("real"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o.Fact("shadow_pending", decide.ShadowPending(shadows, real))
	for _, a := range decide.ShadowAgreement(shadows, real) {
		o.Item("shadow", "kind", a.Kind, "count", a.Count, "agree_pct", a.Pct, "high_count", a.HighCount, "high_agree_pct", a.HighPct)
	}
	return o
}

// gate reads the gate's output, the card and the diff, names every input it cannot read in
// one refusal, and asks the gate decision of each failure (decide.Gate).
func (w world) gate(c *tool.Call) *tool.Out {
	texts := map[string]string{}
	var problems []string
	for _, name := range []string{"output", "card", "diff"} {
		if !c.Given(name) {
			continue
		}
		raw, err := os.ReadFile(c.Str(name))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		texts[name] = string(raw)
	}
	failures := decide.ParseGateOutput(texts["output"])
	if len(problems) == 0 && len(failures) == 0 {
		problems = append(problems, "--output "+c.Str("output")+" holds no go test failure (no `--- FAIL:` or `FAIL <pkg>` line); the gate decision reads a red go test run")
	}
	if len(problems) > 0 {
		return tool.Refuse(problems...)
	}
	in := decide.GateInput{Failures: failures, Paths: decide.CardPaths(texts["card"]), Diff: decide.DiffSummary(texts["diff"])}
	if c.Given("base-red") {
		in.BaseRed = map[string]bool{}
		named := list(c.Str("base-red"))
		for _, f := range failures {
			in.BaseRed[f.Key()] = slices.Contains(named, f.Key()) || f.Test != "" && slices.Contains(named, f.Test)
		}
	}
	bars, _ := gateBars(c.Str("bars")) // checked by the verb's flag rule
	op := c.Str("op")
	if op == "" {
		op = decide.GateName + "-" + decide.Sum([]byte(texts["output"] + "\n" + texts["card"] + "\n" + texts["diff"]))[:12]
	}
	dry := c.DryRun() // read before any refusal below, so a refused dry run says so
	b, err := w.backend(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if dry {
		return gateDryRun(c.Str("record"), op, b, in)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	res, err := decide.Gate(ctx, b, bars, in, c.Str("record"), op, w.now())
	var failed *decide.BackendError
	switch {
	case errors.As(err, &failed):
		return backendFailed(err, "op", op, b, "make --answers answer the class question")
	case err != nil:
		return tool.Refuse(err.Error())
	}
	o := tool.Done().Fact("op", op).Fact("decision", decide.GateName).Fact("backend", b.Name()).Fact("failures", len(res.Calls)).Fact("route", res.Route)
	for _, call := range res.Calls {
		id, class, p, recorded := "-", "-", "-", "unasked"
		if d := call.Decision; d != nil {
			a := d.Answers["class"]
			id, class, p, recorded = d.ID, a.Value, probs(a.P), "new"
			if call.Existing {
				recorded = "existing"
			}
		}
		o.Item("failure", "key", call.Failure.Key(), "id", id, "class", class, "p", p, "route", call.Route, "recorded", recorded)
	}
	return o
}

// gateDryRun is the gate verb's dry run: each failure's id and state size, and whether the
// record holds its decision already (recorded=existing, with its class: the run would ask
// nothing for it), asks nothing (a build failure, one past decide.MaxGateFailures: unasked),
// or would ask (no). An id the record holds over another state is refused, as the run would
// refuse it. The top-level recorded is existing when every failure asked is in the record.
func gateDryRun(record, op string, b decide.Backend, in decide.GateInput) *tool.Out {
	ds, err := decide.Load(record)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o := tool.Done().Fact("op", op).Fact("decision", decide.GateName).Fact("backend", b.Name()).Fact("failures", len(in.Failures))
	all := "existing"
	for i, f := range in.Failures {
		id, state, class, recorded := decide.GateOp(op, f), decide.GateState(in, i), "-", "no"
		switch have := decide.Find(ds, id); {
		case f.Test == "" || i >= decide.MaxGateFailures:
			recorded = "unasked"
		case have != nil:
			if err := decide.Replays(*have, decide.GateSchema(), state); err != nil {
				return tool.Refuse(err.Error())
			}
			class, recorded = have.Answers["class"].Value, "existing"
		default:
			all = "no"
		}
		o.Item("failure", "key", f.Key(), "id", id, "state_bytes", len(state), "class", class, "recorded", recorded)
	}
	return o.Fact("recorded", all)
}

// backendFailed is a decision the backend gave no answer to: exit 2, the id it was asked
// under (named key), and the remedy: fixed's (what --answers must answer), or Jev's.
func backendFailed(err error, key, id string, b decide.Backend, fixed string) *tool.Out {
	o := tool.Fail(err.Error()).Fact(key, id).Fact("backend", b.Name())
	o.Exit, o.Remedy = 2, fixed
	if _, isJev := b.(decide.Jev); isJev {
		o.Remedy = "check the backend's account and the key nova-secrets delivers, then run the same line again"
	}
	return o
}

// gateBars parses --bars: the flaky and the pre-existing bar (decide.ParseGateBars), either
// empty for unset; all of it empty is both unset.
func gateBars(s string) (decide.GateBars, error) {
	if strings.TrimSpace(s) == "" {
		return decide.ParseGateBars("", "")
	}
	f := strings.Split(s, ",")
	if len(f) != 2 {
		return decide.GateBars{}, fmt.Errorf("--bars %q wants two probabilities, the flaky and the pre-existing bar: 0.8,0.8", s)
	}
	b, err := decide.ParseGateBars(f[0], f[1])
	if err != nil {
		return decide.GateBars{}, fmt.Errorf("--bars %q: %v", s, err)
	}
	return b, nil
}

// hold makes the hold decision of a single report: its class and proposed_paths
// answers, and the paths the report proposes (one PROPOSED line per path).
func (w world) hold(c *tool.Call) *tool.Out {
	raw, err := os.ReadFile(c.Str("report"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o := w.decision(c, decide.HoldSchema(), decide.HoldState(string(raw)), map[string]string{
		"report": c.Str("report"), "report_sha256": decide.Sum(raw),
	})
	if o.Status == tool.OK && !c.DryRun() {
		for _, p := range strings.Fields(decide.ExtractProposedPaths(string(raw))) {
			o.Item("proposed", "path", p)
		}
	}
	return o
}

// brief makes the brief decision of every card --card names, as one batch.
func (w world) brief(c *tool.Call) *tool.Out {
	dry := c.DryRun() // read first: a refusal under --dry-run is still a refusal
	cards, err := decide.CardFiles(c.Str("card"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	b, err := w.backend(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	record := c.Str("record")
	if dry {
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
	ctx, cancel := context.WithTimeout(context.Background(), w.deadline) // one deadline for the batch, as add's
	defer cancel()
	made, err := decide.Briefs(ctx, b, cards, record, w.now(), c.Int("width"), c.Dur("timeout"))
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
			"failed", strings.Join(br.Failed, ","), "uncalibrated", true, "recorded", map[bool]string{true: "existing", false: "new"}[m.Existing])
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
	dry := c.DryRun() // read before any refusal below, so a refused dry run says so
	b, err := w.backend(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if dry {
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
		return backendFailed(err, "id", id, b, "make --answers answer every question of the schema")
	case err != nil:
		return tool.Refuse(err.Error())
	case existing:
		return answered(d, "existing")
	}
	return answered(d, "new")
}

// answered is a decision's result: its id and backend, the headline choice when the
// schema has one (a read's verdict, an attempt's class, a grade's grade), and one
// ANSWER item per question in name order. A choice with an empty P prints p=-.
// A wire confidence is printed beside it as confidence=<x> method=wire, and is
// not a probability of correctness (SPEC-NOVA-DECIDE section 3). Top and Prob
// read P only.
func answered(d decide.Decision, recorded string) *tool.Out {
	o := tool.Done().Fact("id", d.ID).Fact("decision", d.Decision).Fact("backend", d.Backend)
	if d.Decision == decide.ScoreName {
		top, p := decide.Top(d)
		o.Fact("top", top).Fact("p", round(p))
	} else {
		for _, head := range []string{"verdict", decide.AttemptQuestion, decide.GradeQuestion} {
			if v, ok := d.Answers[head]; ok && v.Type == decide.Choice {
				o.Fact(head, v.Value).Fact("p", shownP(v))
				if v.Method != "" || v.Confidence != 0 {
					o.Fact("confidence", v.Confidence).Fact("method", v.Method)
				}
			}
		}
	}
	o.Fact("tokens_in", d.Usage.InputTokens).Fact("tokens_out", d.Usage.OutputTokens).Fact("recorded", recorded)
	for _, name := range slices.Sorted(maps.Keys(d.Answers)) {
		a := d.Answers[name]
		if a.Method != "" || a.Confidence != 0 {
			o.Item("answer", "question", name, "type", a.Type, "value", a.Value, "p", probs(a.P), "confidence", a.Confidence, "method", a.Method)
			continue
		}
		o.Item("answer", "question", name, "type", a.Type, "value", a.Value, "p", probs(a.P))
	}
	return o
}

// shownP is the headline probability: empty, printed as p=-, when P is empty,
// else the chosen option's probability. It reads P only (SPEC-NOVA-DECIDE section 3).
func shownP(a decide.Answer) any {
	if len(a.P) == 0 {
		return ""
	}
	return round(a.Prob(a.Value))
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
	return decide.Jev{Model: decide.JevModel, Send: w.send(w.getenv(decide.JevSecret), c.Dur("timeout"))}, nil
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

// importRecords is import: decide.Import over the sources named, counted per kind.
func (w world) importRecords(c *tool.Call) *tool.Out {
	record := c.Str("record")
	if c.DryRun() {
		// the plan from the same code path: import into a copy of the record, report, discard it
		b, err := os.ReadFile(record)
		if err != nil && !os.IsNotExist(err) {
			return tool.Refuse(err.Error())
		}
		f, err := os.CreateTemp("", "nova-decide-import-*.record")
		if err != nil {
			return tool.Refuse(err.Error())
		}
		defer func() { _ = os.Remove(f.Name()) }() // ignored: a temp copy that nothing reads after
		_, werr := f.Write(b)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return tool.Refuse(werr.Error())
		}
		record = f.Name()
	}
	res, err := decide.Import(record, decide.ImportSources{Verdicts: c.Str("verdicts"), Judgments: c.Str("judgments"),
		Log: c.Str("log"), Reports: c.Str("reports")}, w.now())
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o := tool.Done()
	for _, k := range decide.ImportKinds {
		o.Fact(k+"_new", res.Kinds[k].New).Fact(k+"_existing", res.Kinds[k].Existing)
	}
	return o.Fact("unanswered", res.Unanswered)
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

// since parses --since: an RFC 3339 time or a date (UTC midnight); "" is seven days
// before now.
func since(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return now.Add(-7 * 24 * time.Hour).UTC().Truncate(time.Second), nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("--since %q is not an RFC 3339 time or a date (2006-01-02)", s)
}

// readInput reads a file, or stdin when the name is -.
func readInput(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}
