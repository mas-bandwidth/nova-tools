package pulse

// Triage is G2 of the pit-stop's class G (#828): when a rule cannot decide, the decision
// does not come back to the coordinator's window as a transcript -- it goes out as a
// bounded packet on the cheapest text route, and it comes back as ONE line.
//
// The packet holds three things and nothing else: the RESULT lines the case is about, the
// refusal line the harness printed, and the candidate rows of <queue>/RULES.tsv (kind,
// condition, verdict). No spec, no log, no transcript, no history. That is what keeps it
// under PacketMax bytes at the largest plausible state, and what makes the decision cost
// about a third of a cent instead of a coordinator turn.
//
// The verdict shape the card demands is one line:
//
//	TRIAGE <case> <verdict> <rule-row-or-NEW>
//
// ParseTriage reads exactly that line and nothing else. A verdict naming NEW is a rule the
// table does not have yet: AppendRule writes it to RULES.tsv marked `pending`, so the day's
// lesson becomes a row the same hour (Glenn 2026-09-15, "lessons become spec the same
// hour") and the next case of the same kind is decided by the rule and never by a model.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PacketMax is the ceiling on a triage card, in bytes. Five thousand is about 1.2k tokens
// on the text route: enough for the evidence and the rule table, few enough that the
// decision is cheaper than the coordinator reading the same lines once.
const PacketMax = 5000

// TriageRoute is the model a triage card is cut for: the cheapest route that can hold a
// text decision (SPEC-PULSE rule 7's cost table gives the same answer; this is the name
// the card carries so a hand reading the card knows what it was written for).
const TriageRoute = "deepseek-v4-flash"

// TriageKinds are the seven cases that took a coordinator turn each on 2026-09-16 and
// which a rule row plus a packet decides instead. A kind outside this list is refused:
// a case nobody named is a case with no rule rows to offer, and a packet with no candidate
// rows is a packet asking a model to invent policy.
var TriageKinds = []string{"signature", "scope", "docs-only", "nosha", "orphan", "fence", "hold-line"}

// TriageVerdicts are the words the third field may be. They are the dispositions the loop
// already has: admit the card, refuse it, hold it for a person, retry it once, or escalate.
var TriageVerdicts = []string{"ADMIT", "REFUSE", "HOLD", "RETRY", "ESCALATE"}

// NewRule is the third field of a verdict that no row in the table decided.
const NewRule = "NEW"

// RuleRow is one row of <queue>/RULES.tsv: the case kind it decides, the condition in the
// evidence that selects it, the verdict it gives, and its state (`pending` until a person
// has read it).
type RuleRow struct {
	Kind      string
	Condition string
	Verdict   string
	State     string
}

// Packet is the decision packet: everything the triage route is given.
type Packet struct {
	Case    string
	Ref     string
	Result  []string // the RESULT lines of the card the case is about
	Refusal string   // the harness's refusal line, one line
	Rules   []RuleRow
	Link    string // the LINKS line when a typed decision matched an open issue, else ""
}

// KnownTriageKind reports whether kind is one of the seven.
func KnownTriageKind(kind string) bool {
	for _, k := range TriageKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func knownVerdict(v string) bool {
	for _, k := range TriageVerdicts {
		if k == v {
			return true
		}
	}
	return false
}

// ReadRules reads <queue>/RULES.tsv. A missing file is no rows and not an error: the first
// day of a queue has no table yet, and every verdict that day is NEW.
func ReadRules(queue string) []RuleRow {
	var out []RuleRow
	for _, l := range readLines(filepath.Join(queue, "RULES.tsv")) {
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 3 {
			continue
		}
		r := RuleRow{Kind: strings.TrimSpace(f[0]), Condition: strings.TrimSpace(f[1]), Verdict: strings.TrimSpace(f[2])}
		if len(f) >= 4 {
			r.State = strings.TrimSpace(f[3])
		}
		out = append(out, r)
	}
	return out
}

// CandidateRules are the rows that could decide this case: the rows of its own kind, and
// the rows written for every kind (`*`). Nothing else is put in front of the route, because
// a row of another kind is a row that can only mislead it.
func CandidateRules(rules []RuleRow, kind string) []RuleRow {
	var out []RuleRow
	for _, r := range rules {
		if r.Kind == kind || r.Kind == "*" {
			out = append(out, r)
		}
	}
	return out
}

// AppendRule writes one row to <queue>/RULES.tsv marked pending. It is what a NEW verdict
// costs: the decision is recorded as policy the same hour, and a person reading the table
// sees which rows have not been read yet.
func AppendRule(queue string, r RuleRow) error {
	if r.State == "" {
		r.State = "pending"
	}
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(queue, "RULES.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\t%s\n",
		oneline.Field(r.Kind), oneline.Field(r.Condition), oneline.Field(r.Verdict), oneline.Field(r.State))
	return err
}

// BuildPacket assembles a packet for one case from the queue's rule table.
func BuildPacket(queue, kind, ref string, result []string, refusal string) Packet {
	return Packet{
		Case:    kind,
		Ref:     ref,
		Result:  result,
		Refusal: refusal,
		Rules:   CandidateRules(ReadRules(queue), kind),
	}
}

// evidenceMax is the ceiling on any one evidence line. A RESULT line is a one-line record
// already; a line longer than this is a card that pasted a file into its result, and the
// packet carries its head and says how much it dropped.
const evidenceMax = 400

// Card renders the packet as the card the route is handed: line 1 the RESULT contract line
// (`RESULT <label> sha=<sha12 of everything below>`, SPEC-PULSE "The card"), then the wall,
// the evidence, the candidate rows, and the one line the verdict must be.
//
// It never exceeds PacketMax bytes. Over the ceiling it drops EVIDENCE first, from the end,
// and says how many lines it dropped: the rule rows and the verdict shape are the decision
// and are never the thing cut.
func (p Packet) Card() string {
	result := make([]string, 0, len(p.Result))
	for _, l := range p.Result {
		if t := strings.TrimSpace(l); t != "" {
			result = append(result, oneline.Cap(oneline.Escape(t), evidenceMax))
		}
	}
	dropped := 0
	card := p.card(result, dropped)
	for len(card) > PacketMax && len(result) > 0 {
		result = result[:len(result)-1]
		dropped++
		card = p.card(result, dropped)
	}
	// Still over with no evidence left: the rule table itself is the weight. The rows go
	// the same way, from the end, so the card is always a card and never a truncation.
	for len(card) > PacketMax && len(p.Rules) > 0 {
		p.Rules = p.Rules[:len(p.Rules)-1]
		card = p.card(result, dropped)
	}
	return card
}

// card is line 1 and the body it binds.
func (p Packet) card(result []string, dropped int) string {
	body := p.body(result, dropped)
	return contractLine(p.label(), body) + body
}

// label is the card's label: the case and its ref, one token each.
func (p Packet) label() string {
	return "triage-" + oneline.Field(p.Case) + "-" + oneline.Field(nonEmpty(p.Ref, "none"))
}

// body is everything below line 1.
func (p Packet) body(result []string, dropped int) string {
	var b strings.Builder
	b.WriteString("You are a triage reader on the text route. Read only what is below this line: no repo, no spec, no log, no transcript. Do not run go build, go test or any toolchain; read and write only. Answer with one line and stop.\n")
	fmt.Fprintf(&b, "MODEL: %s\n", TriageRoute)
	fmt.Fprintf(&b, "CASE %s ref=%s\n", oneline.Field(p.Case), field(p.Ref))
	if p.Link != "" {
		fmt.Fprintf(&b, "%s\n", p.Link)
	}
	fmt.Fprintf(&b, "RESULT LINES %d\n", len(result))
	for _, l := range result {
		b.WriteString("  " + l + "\n")
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "  ...+%d result lines dropped to hold this card under %d bytes\n", dropped, PacketMax)
	}
	// The refusal is a SENTENCE and stands alone on its line, so it is escaped and not
	// fielded: a refusal whose spaces came back as \x20 is the one line in the packet the
	// route most needs to read, rendered unreadable to save a token it was not spending.
	fmt.Fprintf(&b, "REFUSAL %s\n", nonEmpty(oneline.Escape(oneline.Cap(strings.TrimSpace(p.Refusal), evidenceMax)), "-"))
	fmt.Fprintf(&b, "RULES %d (kind, condition, verdict)\n", len(p.Rules))
	for _, r := range p.Rules {
		fmt.Fprintf(&b, "  %s\t%s\t%s\n", oneline.Field(r.Kind),
			oneline.Cap(oneline.Field(r.Condition), evidenceMax), oneline.Field(r.Verdict))
	}
	b.WriteString("VERDICT\n")
	fmt.Fprintf(&b, "Write exactly one line, nothing before it and nothing after it:\n")
	fmt.Fprintf(&b, "TRIAGE %s <verdict> <rule-row-or-%s>\n", oneline.Field(p.Case), NewRule)
	fmt.Fprintf(&b, "<verdict> is one of %s. The third field is the condition of the rule row\n", strings.Join(TriageVerdicts, ", "))
	fmt.Fprintf(&b, "above that decided it, as one token, or %s when no row fits.\n", NewRule)
	return b.String()
}

// contractLine is line 1: the label and the first twelve hex of the SHA-256 of the bytes
// below it, so the line binds the card it heads (SPEC-PULSE "The card").
func contractLine(label, body string) string {
	sum := sha256.Sum256([]byte(body))
	return fmt.Sprintf("RESULT %s sha=%s\n", label, hex.EncodeToString(sum[:])[:12])
}

// Verdict is the one line a triage comes back as.
type Verdict struct {
	Case    string
	Verdict string
	Rule    string // the condition of the row that decided it, or NEW
}

// Line renders a verdict in the shape the card demands, so that a test can round-trip a
// packet through the shape it asked for without a model in the loop.
func (v Verdict) Line() string {
	return fmt.Sprintf("TRIAGE %s %s %s", oneline.Field(v.Case), v.Verdict, oneline.Field(nonEmpty(v.Rule, NewRule)))
}

// ParseTriage reads the one line a triage card demands. Anything else -- a preamble, a
// second line, a verdict that is not one of the five, a kind that is not one of the seven
// -- is an error naming what was wrong, because a verdict this tool half-read is a verdict
// nobody approved.
func ParseTriage(s string) (Verdict, error) {
	var v Verdict
	lines := 0
	pick := ""
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		lines++
		if strings.HasPrefix(t, "TRIAGE ") {
			if pick != "" {
				return v, fmt.Errorf("two TRIAGE lines; the card demands exactly one (send the card again)")
			}
			pick = t
		}
	}
	if pick == "" {
		return v, fmt.Errorf("no TRIAGE line in %d lines; it wants `TRIAGE <case> <verdict> <rule-row-or-%s>` (send the card again)", lines, NewRule)
	}
	f := strings.Fields(pick)
	if len(f) != 4 {
		return v, fmt.Errorf("the TRIAGE line has %d fields, want 4: TRIAGE <case> <verdict> <rule-row-or-%s> (send the card again)", len(f), NewRule)
	}
	v = Verdict{Case: f[1], Verdict: f[2], Rule: f[3]}
	if !KnownTriageKind(v.Case) {
		return v, fmt.Errorf("case %s is not one of %s (name the case the packet was cut for)", oneline.Field(v.Case), strings.Join(TriageKinds, ", "))
	}
	if !knownVerdict(v.Verdict) {
		return v, fmt.Errorf("verdict %s is not one of %s (answer with one of them)", oneline.Field(v.Verdict), strings.Join(TriageVerdicts, ", "))
	}
	return v, nil
}

// Apply records a verdict: a NEW rule becomes a pending row of the table, and anything else
// is already policy and writes nothing. It returns whether a row was written.
func (v Verdict) Apply(queue, condition string) (bool, error) {
	if v.Rule != NewRule {
		return false, nil
	}
	return true, AppendRule(queue, RuleRow{Kind: v.Case, Condition: nonEmpty(condition, v.Rule), Verdict: v.Verdict, State: "pending"})
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// DefaultDedupeFloor is the confidence floor triage --dedupe starts at.
const DefaultDedupeFloor = 0.9

// TriageDecider is the typed decision triage --dedupe asks: one call, a same_class choice.
// The shipped one is *decide.Client; a test passes a fake, so no test reaches the network.
type TriageDecider interface {
	Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)
}

// IssueRow is one open issue from --issues: its number and its title, one per line, tab
// separated. The loop writes the file from the API; this verb only reads it.
type IssueRow struct {
	Number string
	Title  string
}

// TriageInput is the triage verb, apart from flag parsing.
type TriageInput struct {
	Case     string
	Queue    string
	Out      string
	Ref      string
	Evidence string // a file of the RESULT lines and the refusal line; default <queue>/UNDECIDED/<case>.txt
	Stdout   io.Writer
	Stderr   io.Writer

	// Decide turns on the verb's typed decision: the bounded packet is put to the provider
	// as one verdict question behind Floor, and the answer is printed as one advisory line
	// beside the verb's own. A decision below the floor is a suggestion, never an
	// authorization: the packet is cut and written exactly as it was before the flag.
	Decide bool
	// Dedupe turns on the typed decision: before the card is cut, a same_class
	// choice over the open issues in Issues asks whether this new case is the same
	// class as one of them. At or above Floor the card gains the LINKS line and the
	// TRIAGE line reads dedupe=#<n>; on `none`, on a provider error, or below the
	// floor the card is cut unchanged and the line reads dedupe=?. A decision below
	// the floor is a suggestion, never an authorization.
	Dedupe  bool
	Issues  string
	Floor   float64
	Decider TriageDecider
}

// ReadIssues reads the --issues file, one `number<TAB>title` per line. A blank line is
// skipped. An unreadable file is an error, never an empty list: a decision over no issues
// would link nothing and read as "no duplicate", which is the guess this refuses.
func ReadIssues(path string) ([]IssueRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []IssueRow
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.SplitN(l, "\t", 2)
		number := strings.TrimSpace(f[0])
		if number == "" {
			continue
		}
		title := ""
		if len(f) == 2 {
			title = strings.TrimSpace(f[1])
		}
		out = append(out, IssueRow{Number: number, Title: title})
	}
	return out, nil
}

// sameClassQuestions is the one typed decision --dedupe asks: a choice whose options are
// the open issue numbers and `none`.
func sameClassQuestions(issues []IssueRow) map[string]decide.Question {
	options := map[string]string{"none": "a new class of finding, not the same as any open issue"}
	for _, iss := range issues {
		options[iss.Number] = "the open issue #" + iss.Number + ": " + nonEmpty(iss.Title, "untitled")
	}
	return map[string]decide.Question{
		"same_class": {
			Instructions: "Is this new case the same class of finding as one of the open issues below? Answer with exactly the number of the issue whose class matches, or none.",
			Choice:       options,
		},
	}
}

// dedupeState is what the typed decision is given and nothing else: the new case's title
// (its case and ref) and its evidence, each line escaped and capped as the packet escapes
// evidence, so the state is a bounded packet and never a transcript.
func dedupeState(caseName, ref string, result []string, refusal string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CASE %s ref=%s\n", oneline.Field(caseName), field(ref))
	b.WriteString("EVIDENCE\n")
	for _, l := range result {
		if t := strings.TrimSpace(l); t != "" {
			b.WriteString(oneline.Cap(oneline.Escape(t), evidenceMax))
			b.WriteByte('\n')
		}
	}
	if r := strings.TrimSpace(refusal); r != "" {
		b.WriteString(oneline.Escape(oneline.Cap(r, evidenceMax)))
		b.WriteByte('\n')
	}
	return b.String()
}

// link is the one typed decision, asked before the card is cut. It returns the card's
// LINKS line (empty when there is none) and the dedupe field for the TRIAGE line. On
// `none`, on an answer that names no open issue, on a provider error and below the floor
// the field is `?` and the card is cut unchanged.
func (in TriageInput) link(caseName, ref string, result []string, refusal string) (string, string, error) {
	if in.Decider == nil {
		return "", "", fmt.Errorf("--dedupe needs a decider; refusing to guess (set --key-env, or drop --dedupe)")
	}
	issues, err := ReadIssues(in.Issues)
	if err != nil {
		return "", "", fmt.Errorf("--issues %s could not be read: %s (name a file of number<TAB>title, one open issue per line)", oneline.Field(in.Issues), oneline.Err(err))
	}
	floor := in.Floor
	if floor <= 0 {
		floor = DefaultDedupeFloor
	}
	answers, _, derr := in.Decider.Decide(context.Background(), dedupeState(caseName, ref, result, refusal), sameClassQuestions(issues))
	if derr != nil {
		return "", "dedupe=?", nil
	}
	a, ok := answers["same_class"]
	if !ok || a.Choice == "" || a.Choice == "none" || a.Confidence < floor {
		return "", "dedupe=?", nil
	}
	for _, iss := range issues {
		if iss.Number == a.Choice {
			return fmt.Sprintf("LINKS #%s (same class, conf=%.2f)", iss.Number, a.Confidence), "dedupe=#" + iss.Number, nil
		}
	}
	return "", "dedupe=?", nil
}

// Triage cuts one packet to a card file and prints one line.
func Triage(in TriageInput) int {
	stdout, stderr := in.Stdout, in.Stderr
	if !KnownTriageKind(in.Case) {
		return refusal(stderr, "TRIAGE", fmt.Errorf("--case %s is not one of %s (name the case this packet is cut for)",
			oneline.Field(in.Case), strings.Join(TriageKinds, ", ")))
	}
	path := in.Evidence
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(in.Queue, "UNDECIDED", in.Case+".txt")
	}
	lines := readLines(path)
	result, refuse := splitEvidence(lines)
	p := BuildPacket(in.Queue, in.Case, in.Ref, result, refuse)
	dedupeField := ""
	if in.Dedupe {
		link, f, err := in.link(in.Case, in.Ref, result, refuse)
		if err != nil {
			return refusal(stderr, "TRIAGE", err)
		}
		p.Link, dedupeField = link, f
	}
	card := p.Card()
	if err := os.MkdirAll(filepath.Dir(in.Out), 0o755); err != nil {
		return refusal(stderr, "TRIAGE", fmt.Errorf("cannot make the card's directory: %s (name a writable --out)", oneline.Err(err)))
	}
	if err := os.WriteFile(in.Out, []byte(card), 0o644); err != nil {
		return refusal(stderr, "TRIAGE", fmt.Errorf("cannot write the card: %s (name a writable --out)", oneline.Err(err)))
	}
	if dedupeField != "" {
		dedupeField = " " + dedupeField
	}
	fmt.Fprintf(stdout, "TRIAGE OK case=%s ref=%s route=%s rules=%d result=%d bytes=%d out=%s%s\n",
		oneline.Field(in.Case), field(in.Ref), TriageRoute, len(p.Rules), len(result), len(card), field(in.Out), dedupeField)
	// The typed decision, when asked for, is one more advisory line: the answer at or above
	// the floor, `?` below it, and always the evidence pointer the state came from. It
	// changes no verdict and writes nothing (SPEC-DECIDE rules 5, 7 and 10).
	if in.Decide {
		fmt.Fprintln(stdout, in.decideLine(card, path))
	}
	return 0
}

// triageDecideQuestions is the one typed question the triage verb asks: which of the five
// dispositions the bounded packet names. It advises; the rule table and a person still
// decide.
func triageDecideQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"verdict": {
			Instructions: "Read the bounded triage packet and choose the one disposition: " +
				strings.Join(TriageVerdicts, ", ") +
				". ADMIT admits the card, REFUSE rejects it, HOLD asks a person, RETRY tries the same card once more, ESCALATE hands it to a person.",
			Choice: map[string]string{
				"ADMIT":    "the card is sound and may be admitted",
				"REFUSE":   "the card is unsound and must not be admitted",
				"HOLD":     "hold the card for a person",
				"RETRY":    "retry the same card once more",
				"ESCALATE": "escalate the case to a person",
			},
		},
	}
}

// decideLine renders the one advisory line the triage verb appends when --decide is set.
// The typed verdict is shown only at or above the floor; below it the answer is `?` with
// its confidence (rule 11). The evidence pointer is always present (rule 10), and the line
// is a suggestion that never changes what the verb does.
func (in TriageInput) decideLine(state, evidence string) string {
	value, conf := "?", 0.0
	if in.Decider != nil {
		if answers, _, err := in.Decider.Decide(context.Background(), state, triageDecideQuestions()); err == nil {
			if a, ok := answers["verdict"]; ok {
				conf = a.Confidence
				if a.Confidence >= in.Floor && a.Choice != "" {
					value = a.Choice
				}
			}
		}
	}
	return fmt.Sprintf("TRIAGE DECIDE verdict=%s conf=%.2f floor=%.2f evidence=%s",
		oneline.Field(value), conf, in.Floor, oneline.Field(evidence))
}

// splitEvidence divides an evidence file into the RESULT lines and the one refusal line:
// the last line carrying a refusal word is the refusal, and every other line is evidence.
func splitEvidence(lines []string) (result []string, refusal string) {
	for _, l := range lines {
		if isRefusalLine(l) {
			refusal = strings.TrimSpace(l)
			continue
		}
		result = append(result, l)
	}
	return result, refusal
}

func isRefusalLine(l string) bool {
	low := strings.ToLower(l)
	for _, w := range []string{"refused", "refusing", "denied", "permission", "abstain"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}
