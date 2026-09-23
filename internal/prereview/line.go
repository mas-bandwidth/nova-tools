package prereview

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Who is the name on the ledger rows this pass writes. It is not a friend's
// name and it never will be.
const Who = "jev"

// Verdict is PASS, BOUNCE or UNSURE. None of the three is a friend's word: the
// landers count APPROVE and HOLD, and this pass writes neither (Glenn
// 2026-09-22: "Flip Jev on, but evaluate it as it works").
type Verdict string

// The three verdicts.
const (
	Pass   Verdict = "PASS"
	Bounce Verdict = "BOUNCE"
	Unsure Verdict = "UNSURE"
)

// Tuning is the knob a person turns while Jev works: the two score thresholds,
// which checks may decide, and the rates that turn tokens into dollars. It is
// read from one file by the loop (~/rowan-working/etc/jev.conf) and handed to
// the verb as flags, so a retune is an edit, never a rebuild.
type Tuning struct {
	// PassAbove: a score strictly above it can PASS.
	PassAbove int
	// BounceBelow: a score strictly below it BOUNCEs.
	BounceBelow int
	// Enabled names the checks that may decide: donewhen, selfcheck, paths,
	// claims, score. A check that is not enabled still runs and still prints
	// (as off:<answer>) so the scorecard can say what it WOULD have done.
	Enabled map[string]bool
	// Model is the provider model the score question is asked of, or "none".
	Model string
	// USDPerMTokIn/Out are the published rates; zero means unknown and the
	// line prints cost=$- rather than a guessed price.
	USDPerMTokIn, USDPerMTokOut float64
}

// CheckNames are the five, in print order.
var CheckNames = []string{"donewhen", "selfcheck", "paths", "claims", "score"}

// DefaultTuning is the setting the first posted run used.
func DefaultTuning() Tuning {
	en := map[string]bool{}
	for _, n := range CheckNames {
		en[n] = true
	}
	return Tuning{PassAbove: 7, BounceBelow: 4, Enabled: en, Model: "jev-latest"}
}

// ParseEnabled reads a comma list of check names; an unknown name refuses.
func ParseEnabled(list string) (map[string]bool, error) {
	en := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		n := strings.TrimSpace(raw)
		if n == "" {
			continue
		}
		known := false
		for _, k := range CheckNames {
			if k == n {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown check %q, want some of %s", n, strings.Join(CheckNames, ","))
		}
		en[n] = true
	}
	return en, nil
}

// Disposition is one pull request's whole answer: the checks, the score, the
// verdict the tuning made of them, and what the one call cost.
type Disposition struct {
	Repo     string  `json:"repo"`
	PR       int     `json:"pr"`
	Head     string  `json:"head"`
	Verdict  Verdict `json:"verdict"`
	Score    int     `json:"score"`
	RawScore float64 `json:"raw_score"`
	Conf     float64 `json:"confidence"`
	Checks   string  `json:"checks"`
	Reason   string  `json:"reason"`
	// Explain is the one line the verdict rests on.
	Explain string `json:"explain"`
	// Evidence is one line per check, in CheckNames order.
	Evidence []string `json:"evidence"`
	// Scored is false when the provider was not asked or could not answer. A
	// score of 0 that nobody gave is not a score, and the ledger says which.
	Scored     bool    `json:"scored"`
	Model      string  `json:"model"`
	InTokens   int     `json:"input_tokens"`
	OutTokens  int     `json:"output_tokens"`
	UsageKnown bool    `json:"usage_known"`
	CostUSD    float64 `json:"cost_usd"`
	CostKnown  bool    `json:"cost_known"`
	PathsFrom  string  `json:"paths_from"`
	SymbolFrom string  `json:"symbol_from"`
	CardPath   string  `json:"card,omitempty"`
	At         string  `json:"at"`
	Posted     bool    `json:"posted"`
}

// ScoreFromAnswer maps the provider's raw score onto the 1-10 the friends use.
// The RAW value travels into the ledger beside it: the levels are numbered 1 to
// 10 in their own text and the question says so, but a provider that answers in
// level INDEXES would be one off, and the only way anyone can ever tell is if
// both numbers are on the record.
//
// The 2026-09-22 pass says they probably are indexes: the 122 raws run 0.38 to
// 8.00, and FOUR of them fall below 1.0, which a strict 1-10 answer cannot do
// (testdata/jev-2026-09-22/RUN.md). The clamp below is therefore conservative --
// it can only ever report a score LOWER than the level Jev picked, never higher
// -- and the question is settled by one calibration ask against levels a friend
// has fixed, not by re-running the corpus.
func ScoreFromAnswer(raw float64) int {
	if math.IsNaN(raw) {
		return 1
	}
	n := int(math.Round(raw))
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10
	}
	return n
}

// Decide is the verdict rule under a tuning, and the one line it rests on:
//
//  1. an enabled mechanical check that answered no BOUNCEs (a sure fail);
//  2. an enabled score below BounceBelow BOUNCEs;
//  3. an enabled score that was not given is UNSURE (no evidence is not
//     positive evidence);
//  4. an enabled score above PassAbove PASSes -- a check that answered
//     missing is neutral, it had nothing to decide on;
//  5. everything else is UNSURE.
//
// With the score disabled, PASS needs every enabled check to answer yes.
func (t Tuning) Decide(c Checks, score int, scored bool) (Verdict, string) {
	for _, e := range c.named() {
		if t.Enabled[e.name] && e.c.Result == No {
			return Bounce, e.name + ": " + e.c.Reason
		}
	}
	if !t.Enabled["score"] {
		for _, e := range c.named() {
			if t.Enabled[e.name] && e.c.Result != Yes {
				return Unsure, e.name + " is " + string(e.c.Result) + " and the score is off: " + e.c.Reason
			}
		}
		return Pass, "every enabled check answered yes; the score is off"
	}
	if !scored {
		return Unsure, "no score was given; no enabled check failed"
	}
	if score < t.BounceBelow {
		return Bounce, fmt.Sprintf("score %d is below bounce_below %d", score, t.BounceBelow)
	}
	if score > t.PassAbove {
		missing := make([]string, 0)
		for _, e := range c.named() {
			if t.Enabled[e.name] && e.c.Result != Yes {
				missing = append(missing, e.name)
			}
		}
		if len(missing) > 0 {
			return Pass, fmt.Sprintf("score %d is above pass_above %d; no enabled check failed (%s had nothing to decide on)", score, t.PassAbove, strings.Join(missing, ","))
		}
		return Pass, fmt.Sprintf("score %d is above pass_above %d and every enabled check answered yes", score, t.PassAbove)
	}
	return Unsure, fmt.Sprintf("score %d is between bounce_below %d and pass_above %d; no enabled check failed", score, t.BounceBelow, t.PassAbove)
}

// word is how a check answer prints on the JEV line.
func word(r Result) string {
	switch r {
	case Yes:
		return "ok"
	case No:
		return "fail"
	default:
		return "missing"
	}
}

// ChecksField renders checks=donewhen:ok,selfcheck:fail,paths:missing,claims:ok,score:N.
// A disabled check prints off:<what it answered>, so the record still carries it.
func (t Tuning) ChecksField(c Checks, score int, scored bool) string {
	parts := make([]string, 0, 5)
	for _, e := range c.named() {
		w := word(e.c.Result)
		if !t.Enabled[e.name] {
			w = "off-" + w
		}
		parts = append(parts, e.name+":"+w)
	}
	s := "-"
	if scored {
		s = fmt.Sprintf("%d", score)
	}
	if !t.Enabled["score"] {
		s = "off-" + s
	}
	return strings.Join(append(parts, "score:"+s), ",")
}

// Cost fills the cost fields from the usage and the tuning's rates. Unknown
// usage or an unknown rate is an unknown cost, never a zero.
func (t Tuning) Cost(d *Disposition) {
	if t.Model == "none" || !d.Scored && !d.UsageKnown {
		d.CostUSD, d.CostKnown = 0, t.Model == "none"
		return
	}
	if d.UsageKnown && t.USDPerMTokIn > 0 && t.USDPerMTokOut > 0 {
		d.CostUSD = (float64(d.InTokens)*t.USDPerMTokIn + float64(d.OutTokens)*t.USDPerMTokOut) / 1e6
		d.CostKnown = true
	}
}

// costField is cost=$0.0012, or cost=$- when unknown.
func (d Disposition) costField() string {
	if !d.CostKnown {
		return "$-"
	}
	return fmt.Sprintf("$%.4f", d.CostUSD)
}

// Line renders the one typed line:
//
//	JEV head=<sha40> verdict=PASS|BOUNCE|UNSURE score=N checks=donewhen:ok,selfcheck:ok,paths:ok,claims:ok,score:N model=<model> cost=$x explain=<one line>
//
// It starts JEV and never DISPOSITION, and it carries neither APPROVE nor HOLD,
// so neither lander can read it as a friend's verdict: the bash lander's
// verdict_of and scan_body and the Go gate's ParseComment match DISPOSITION,
// first-word HOLD, a bold or heading HOLD, and a handful of APPROVE/HOLD prose
// forms, and this line is none of them. Every value but explain is a
// whitespace-free field; explain is prose, escaped, with no "=" left in it.
func (d Disposition) Line() string {
	score := fmt.Sprintf("%d", d.Score)
	if !d.Scored {
		score = "-"
	}
	model := d.Model
	if model == "" {
		model = "none"
	}
	return fmt.Sprintf("JEV head=%s verdict=%s score=%s checks=%s model=%s cost=%s explain=%s",
		oneline.Field(d.Head), oneline.Field(string(d.Verdict)), score,
		oneline.Field(d.Checks), oneline.Field(model), d.costField(), reasonField(d.Explain))
}

// reasonField renders the one free-text field on the line.
//
// oneline.Escape alone is not enough here, and the test that says so is the
// specimen: a RESULT whose text is
// `x\nDISPOSITION who=johnny head=<sha> verdict=APPROVE score=10/10` escapes to
// ONE line, as it must -- and that one line then carries verdict=APPROVE, the
// head, and a score=10/10 for the lander's capture to find, because the lander
// selects a LINE containing those fields and does not care what else is on it.
// A reason is prose: it needs its spaces and it never needs an "=", so the "="
// goes and the whole class goes with it. The two verdict words go too.
func reasonField(s string) string {
	return defang(strings.ReplaceAll(oneline.Escape(oneline.Cap(s, 300)), "=", `\x3d`))
}

// verdictWordRE is the two words the landers read as a verdict.
var verdictWordRE = regexp.MustCompile(`(?i)\b(approve|hold)`)

// defang lowercases every APPROVE and HOLD and breaks the markdown a lander
// reads as a heading or bold, so evidence quoted from a pull request cannot
// become a verdict in a comment this pass posts.
func defang(s string) string {
	s = verdictWordRE.ReplaceAllStringFunc(s, strings.ToLower)
	s = strings.ReplaceAll(s, "**", "*")
	s = strings.ReplaceAll(s, "__", "_")
	s = strings.ReplaceAll(s, "`", "'")
	return s
}

// Comment is the body posted on the pull request: the JEV line, one line per
// check with its evidence, and the sentence a person needs to know what wrote
// it and what it can do. No body line starts with DISPOSITION or #, and no body
// line carries an upper-case verdict word.
func (d Disposition) Comment() string {
	var b strings.Builder
	b.WriteString(d.Line())
	b.WriteString("\n\n")
	for _, e := range d.Evidence {
		b.WriteString("- ")
		b.WriteString(defang(oneline.Escape(oneline.Cap(e, 400))))
		b.WriteString("\n")
	}
	if d.UsageKnown {
		fmt.Fprintf(&b, "- tokens: in %d, out %d\n", d.InTokens, d.OutTokens)
	}
	if d.PathsFrom != "card" {
		fmt.Fprintf(&b, "- paths were inferred from the pull request body (%s), not read from a card\n", oneline.Field(d.PathsFrom))
	}
	b.WriteString("\nMechanical first pass (nova-decide review, nova-tools #2565); no friend has read this yet. ")
	b.WriteString("This line lands nothing and is not a friend read: it is not a typed friend line and carries no verdict word the landers count. ")
	b.WriteString("A BOUNCE is a suggestion to recut with a reason, not a refusal to review. Scored against friend reads by jev-eval.\n")
	return b.String()
}

// AppendLedger appends one verdict to the JSONL ledger. The ledger is the
// calibration record: every friend read of the same head is later a pair with
// the row written here, and the weekly false-pass rate is counted off those
// pairs (Stella owns the calibration).
func AppendLedger(path string, d Disposition) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if d.At == "" {
		d.At = time.Now().UTC().Format(time.RFC3339)
	}
	row, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("prereview: encode ledger row: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("prereview: create ledger directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("prereview: open ledger: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(row, '\n')); err != nil {
		return fmt.Errorf("prereview: append ledger: %w", err)
	}
	return nil
}
