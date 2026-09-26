// Package jev is the Jev decision ledger (nova-tools #4316, Glenn 2026-09-26
// ~11:05 AM ET: "we have Jev, it's cool. How can we include Jev into our
// processes more, using it everywhere that a decision is made? How can we
// train it? Measure it? Improve it?").
//
// Every decision the card model makes is ONE ROW:
//
//	jev:row:<type>:<subject>  HASH  type subject at state input_sha rules
//	                                [jev jev_conf prompt_version tokens cost ms jev_at]
//	                                [outcome outcome_by outcome_why outcome_at]
//
// indexed by jev:rows:<type> (ZSET scored by at) and jev:types (SET), with
// every decision, answer and outcome also appended to the stream
// jev:decisions (the training set a fine-tune reads later). state is the exact
// text Jev reads; rules is the answer the structure acts on today (a declared
// tier, the REVIEW-JEV suggest, the read rules); jev is TypeSafe Jev's own
// answer, asked in shadow (jev:pending, `nova-sprint jev ask`, one typed call
// per row, never a loop); outcome is what the system recorded later. `jev
// report` prints, per type, source and prompt version, how often each answer
// agreed with its outcome, so a prompt change is measured before it stays.
//
// The decision points that exist today are read off ws:log, the one move log
// every card and copy move already writes (`nova-sprint jev sync`): nothing
// on the copy model's live path writes a jev key, so a bench's or a friend's
// card end (whose Redis user may not touch jev:*) is never at risk.
//
//	worktype <primary>        a primary is pushed; outcome: the card's TYPE, when it declares one of Jev's types
//	tier <primary>            a primary is pushed (rules: the declared tier, else flash); outcome: the tier whose work read 8+
//	review <primary>@<ms>     a failed copy puts its primary in review (rules: REVIEW-JEV suggest=); outcome: the posted verdict
//	readsane <read copy>      a read copy ends with a score (rules: ReadRules); outcome at the head's fate:
//	                          trust when the read's pass or fail matched it, suspect when not
//
// A decision point another stream is building (the order-miss pairs of #4342,
// the sentinel fold of #4318) makes one call, Record, and its outcome one
// call, Join; a Decision carries its own Prompt when Jev has no built-in one
// for its type.
package jev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The decision types. Order and sentinel are hooks: their decision points are
// built by #4342 and #4318, which call Record and Join.
const (
	TypeWorkType = "worktype"
	TypeTier     = "tier"
	TypeReview   = "review"
	TypeReadSane = "readsane"
	TypeOrder    = "order"
	TypeSentinel = "sentinel"
)

// Types is the report's order; a type a hook records that is not here
// follows, sorted.
var Types = []string{TypeWorkType, TypeTier, TypeReview, TypeReadSane, TypeGate, TypeOrder, TypeSentinel}

// The ledger's keys.
const (
	KeyDecisions = "jev:decisions"
	KeyPending   = "jev:pending"
	KeyTypes     = "jev:types"
	KeyCursor    = "jev:cursor"
	// KeyAsking holds the rows an ask has claimed off jev:pending (SMOVE)
	// until their answers are written.
	KeyAsking = "jev:asking"
	// KeyOpenReview is each primary's review row a verdict will join.
	KeyOpenReview = "jev:review:open"
	// KeyBuilt is the tier each primary's last work or fix copy ran at:
	// "<tier> <copy> <model>", the tier row's outcome when it merges.
	KeyBuilt = "jev:built"
	// KeyMoves is the move log sync reads (TK.move and TM.log write it).
	KeyMoves = "ws:log"
	// LogMax trims jev:decisions like ws:log is trimmed.
	LogMax = 200000
	// StateMax is the most of a state the row keeps and Jev reads.
	StateMax = 6000
)

// RowKey is one decision's row.
func RowKey(t, subject string) string { return "jev:row:" + t + ":" + subject }

// IndexKey is one type's rows, scored by the decision's ms.
func IndexKey(t string) string { return "jev:rows:" + t }

// ReadsKey is a primary's read decisions still waiting for the head's fate:
// copy -> "<score> <head>".
func ReadsKey(primary string) string { return "jev:reads:" + primary }

// Tiers are the model types a tier decision answers, cheapest last.
var Tiers = []string{"frontier", "pro", "flash"}

// IsTier is t one of Tiers.
func IsTier(t string) bool {
	for _, x := range Tiers {
		if t == x {
			return true
		}
	}
	return false
}

// Prompt is one decision type's question to Jev, versioned: the report is per
// version, so a change is measured before it stays. The built-in prompts are
// docs/jev/<type>.<version>.txt (Render; a unit test holds them equal).
type Prompt struct {
	Version      string
	Instructions string
	Options      map[string]string
}

// Render is the prompt as its docs/jev file holds it.
func (p Prompt) Render() string {
	var b strings.Builder
	b.WriteString("VERSION: " + p.Version + "\n\n" + strings.TrimSpace(p.Instructions) + "\n\nOPTIONS:\n")
	for _, k := range sortedKeys(p.Options) {
		b.WriteString("- " + k + ": " + p.Options[k] + "\n")
	}
	return b.String()
}

// Has is o one of the prompt's options.
func (p Prompt) Has(o string) bool { _, ok := p.Options[o]; return ok && o != "" }

var prompts = map[string]Prompt{
	TypeWorkType: {Version: "worktype-v1",
		Instructions: "Classify the card into the one work type that best describes the change it asks for. " +
			"Read the title, the PATHS, the DONE-WHEN and the issue text; judge the work, not the words.",
		Options: map[string]string{
			"fixture":  "a test fixture, a test file or test data only; no production code changes",
			"test-fix": "a failing or flaky test repaired, with the smallest production change it needs",
			"verb":     "a command-line verb or flag added or changed in one package, with its tests",
			"lua":      "a Redis Function (Lua) changed with its Go caller: moves, sets, indexes",
			"refactor": "one change across several packages: renames, moves, dead code, a shared helper",
			"spec":     "a design or specification decided and written down; little or no code",
			"docs":     "documentation only",
			"read":     "read and judge existing work (a PR, a spec, a finding); no change produced",
		}},
	TypeTier: {Version: "tier-v1",
		Instructions: "Pick the cheapest model tier that will finish this card correctly on the first try: " +
			"its work read at 8/10 or better by a cold reader. Judge from the size (PATHS, packages), " +
			"the novelty and the risk of the change the card asks for.",
		Options: map[string]string{
			"frontier": "design across packages, novel mechanisms, concurrency or state machines; a wrong turn is expensive",
			"pro":      "a typical verb, fix or Lua change with its tests in one or two packages",
			"flash":    "small and mechanical: a fixture, a doc, a rename, a one-file fix with an obvious test",
		}},
	TypeReview: {Version: "review-v1",
		Instructions: "A copy of this card failed. From the evidence (the failure shape, how often the same shape " +
			"failed on this card and on this consumer, the model, the exit, the last typed line, the reads) pick " +
			"the verdict that gets the card landed soonest.",
		Options: map[string]string{
			"recut":    "the card itself is the problem (unclear, too big, wrong base): rewrite it and start again",
			"redeal":   "a transient failure (a crash, a lapsed lease, an outage): deal the same card again",
			"reassign": "this consumer or model keeps failing this shape: give the card to another consumer",
			"drop":     "the work is no longer needed or already done: close it",
		}},
	TypeReadSane: {Version: "readsane-v1",
		Instructions: "A cold reader scored this pull request. Judge whether the score can be trusted: does it " +
			"follow from the gates and the finding, judged against the DONE-WHEN? A pass (8 or more) with a " +
			"failing gate or a blocking finding, or a low score whose finding names nothing to fix, is suspect.",
		Options: map[string]string{
			"trust":   "the score follows from the evidence the reader gives",
			"suspect": "the score does not follow from the reader's own evidence",
		}},
}

// PromptFor is the built-in prompt for type t.
func PromptFor(t string) (Prompt, bool) {
	p, ok := prompts[t]
	return p, ok
}

// Decision is one decision point's answer as a row: what Jev reads (State),
// the answer the structure acts on today (Rules, "" when it has none), and
// whether Jev is asked in shadow (Ask). Prompt is a hook's own question; nil
// is the built-in one for Type. Fields are more row fields.
type Decision struct {
	Type, Subject string
	State         string
	Rules         string
	Ask           bool
	Prompt        *Prompt
	Fields        map[string]string
	// At is the decision's ms (the move's, from sync), the row's at and its
	// index score; 0 is when it is recorded.
	At int64
}

var (
	typeRx    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	versionRx = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,39}$`)
	// reserved are the row fields a Decision's Fields may not set.
	reserved = map[string]bool{"type": true, "subject": true, "at": true, "state": true, "input_sha": true,
		"rules": true, "jev": true, "jev_conf": true, "prompt_version": true, "tokens": true, "cost": true,
		"ms": true, "jev_at": true, "outcome": true, "outcome_by": true, "outcome_why": true, "outcome_at": true,
		"q_version": true, "q_instructions": true, "q_options": true}
)

// checkSubject refuses a subject a key or a pending member cannot carry.
func checkSubject(s string) error {
	if s == "" || len(s) > 200 || strings.ContainsAny(s, " \t\r\n") {
		return fmt.Errorf("subject %q is not one token of 1-200 bytes", s)
	}
	return nil
}

// Check refuses a decision that cannot be a row, or that asks Jev with no
// question to ask.
func (d Decision) Check() error {
	if !typeRx.MatchString(d.Type) {
		return fmt.Errorf("type %q is not [a-z][a-z0-9-]{0,31}", d.Type)
	}
	if err := checkSubject(d.Subject); err != nil {
		return err
	}
	if strings.TrimSpace(d.State) == "" {
		return errors.New("a decision carries the state Jev reads; it is empty")
	}
	for k := range d.Fields {
		if reserved[k] || k == "" {
			return fmt.Errorf("field %q is the ledger's own", k)
		}
	}
	p, builtin := PromptFor(d.Type)
	if d.Prompt != nil {
		p = *d.Prompt
		if !versionRx.MatchString(p.Version) || strings.TrimSpace(p.Instructions) == "" || len(p.Options) < 2 {
			return errors.New("a decision's own prompt needs a version, instructions and two options or more")
		}
	} else if d.Ask && !builtin {
		return fmt.Errorf("type %s has no built-in prompt; Ask needs the decision's own Prompt", d.Type)
	}
	if d.Rules != "" && (d.Prompt != nil || builtin) && !p.Has(d.Rules) {
		return fmt.Errorf("rules answer %q is not one of %s's options", d.Rules, d.Type)
	}
	return nil
}

// InputSHA is the 12-hex sha256 of the state: rows asked of the same input
// share it.
func InputSHA(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])[:12]
}

// capState keeps the row and the call bounded.
func capState(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > StateMax {
		s = s[:StateMax]
		for len(s) > 0 && s[len(s)-1]&0xC0 == 0x80 {
			s = s[:len(s)-1]
		}
		if len(s) > 0 && s[len(s)-1] >= 0xC0 {
			s = s[:len(s)-1]
		}
	}
	return s
}

// pendingMember is the jev:pending member of one row.
func pendingMember(t, subject string) string { return t + " " + subject }

// recordCmds queues one decision: the row (a new decision replaces the old
// row), its index, the type, the log entry and, when asked, the pending ask.
func recordCmds(ctx context.Context, p redis.Pipeliner, d Decision, at int64) {
	if d.At > 0 {
		at = d.At
	}
	state := capState(d.State)
	key := RowKey(d.Type, d.Subject)
	h := []any{"type", d.Type, "subject", d.Subject, "at", strconv.FormatInt(at, 10), "state", state,
		"input_sha", InputSHA(state), "rules", d.Rules}
	if d.Prompt != nil {
		opts, _ := json.Marshal(d.Prompt.Options)
		h = append(h, "q_version", d.Prompt.Version, "q_instructions", d.Prompt.Instructions, "q_options", string(opts))
	}
	for _, k := range sortedKeys(d.Fields) {
		h = append(h, k, d.Fields[k])
	}
	p.Del(ctx, key)
	p.HSet(ctx, key, h...)
	p.ZAdd(ctx, IndexKey(d.Type), redis.Z{Score: float64(at), Member: d.Subject})
	p.SAdd(ctx, KeyTypes, d.Type)
	if d.Ask {
		p.SAdd(ctx, KeyPending, pendingMember(d.Type, d.Subject))
	}
	p.XAdd(ctx, &redis.XAddArgs{Stream: KeyDecisions, MaxLen: LogMax, Approx: true, Values: []any{
		"event", "decision", "type", d.Type, "subject", d.Subject, "answer", d.Rules, "input_sha", InputSHA(state),
		"at", strconv.FormatInt(at, 10)}})
}

// Outcome is what the system recorded later for one row.
type Outcome struct {
	Type, Subject, Outcome, By, Why string
}

// joinCmds queues one outcome onto its row and the log.
func joinCmds(ctx context.Context, p redis.Pipeliner, o Outcome, at int64) {
	p.HSet(ctx, RowKey(o.Type, o.Subject), "outcome", o.Outcome, "outcome_by", o.By, "outcome_why", o.Why,
		"outcome_at", strconv.FormatInt(at, 10))
	p.XAdd(ctx, &redis.XAddArgs{Stream: KeyDecisions, MaxLen: LogMax, Approx: true, Values: []any{
		"event", "outcome", "type", o.Type, "subject", o.Subject, "answer", o.Outcome, "by", o.By,
		"at", strconv.FormatInt(at, 10)}})
}

// Record is the ONE CALL a decision point makes (the hook for #4342's
// order-miss pairs and #4318's sentinel fold): the row, its index, its log
// entry and, with Ask, Jev's shadow ask, in one MULTI.
func Record(ctx context.Context, c redis.Cmdable, d Decision) error {
	if err := d.Check(); err != nil {
		return fmt.Errorf("jev record %s %s: %w", d.Type, d.Subject, err)
	}
	at := time.Now().UnixMilli()
	_, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		recordCmds(ctx, p, d, at)
		return nil
	})
	if err != nil {
		return fmt.Errorf("jev record %s %s: %w", d.Type, d.Subject, err)
	}
	return nil
}

// ErrNoRow is Join's refusal when no decision was recorded for the subject:
// an outcome joins a decision, it never makes one.
var ErrNoRow = errors.New("no decision row")

// Join is the ONE CALL an outcome makes: it joins the outcome to the row the
// decision wrote (ErrNoRow when there is none).
func Join(ctx context.Context, c redis.Cmdable, o Outcome) error {
	if !typeRx.MatchString(o.Type) {
		return fmt.Errorf("jev join: type %q is not [a-z][a-z0-9-]{0,31}", o.Type)
	}
	if err := checkSubject(o.Subject); err != nil {
		return fmt.Errorf("jev join: %w", err)
	}
	if strings.TrimSpace(o.Outcome) == "" || strings.ContainsAny(o.Outcome, " \t\r\n") {
		return fmt.Errorf("jev join %s %s: outcome %q is not one word", o.Type, o.Subject, o.Outcome)
	}
	n, err := c.Exists(ctx, RowKey(o.Type, o.Subject)).Result()
	if err != nil {
		return fmt.Errorf("jev join %s %s: %w", o.Type, o.Subject, err)
	}
	if n == 0 {
		return fmt.Errorf("jev join %s %s: %w", o.Type, o.Subject, ErrNoRow)
	}
	at := time.Now().UnixMilli()
	if _, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		joinCmds(ctx, p, o, at)
		return nil
	}); err != nil {
		return fmt.Errorf("jev join %s %s: %w", o.Type, o.Subject, err)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
