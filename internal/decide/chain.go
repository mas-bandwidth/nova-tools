// The decider chain (docs/SPEC-DECIDE.md D1-D2, :719-756).
//
// A decider is anything that answers a typed question over framed evidence.
// Four ship: `rules`, a deterministic table that sees private evidence and
// makes no network call; `jev`, the remote provider, which must be assumed to
// train on what it sees; `local`, the same wire shape at a loopback address;
// and `none`. Nothing in this file is specific to us, and no question's text,
// option or rule row names a person, a bench, a repository or a model of ours.
//
// The walk is the part worth reading twice. `rules` is always consulted first,
// whether or not it is named, because a question a table can answer is a call
// not worth making. A STOPPING member ends the walk before the floor is looked
// at, at any confidence -- a first provider's stop is not undone by a second
// provider's permission, and that is the one deliberate exception to "below the
// floor is unknown". Private evidence offered to a sees=public decider is
// refused BEFORE the call and the question falls through to the next decider,
// so a private bus is triaged by the table or by a loopback model and by
// nothing else. When the chain is exhausted the answer is `unknown`, which is a
// member of no answer set: it is the absence of an answer, and every caller's
// behaviour for it is today's behaviour.
package decide

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide/questions"
)

// What a decider may see (S7, :696-716).
const (
	SeesPublic  = "public"
	SeesPrivate = "private"
)

// Where a piece of evidence came from.
const (
	EvidencePublic  = "public"
	EvidencePrivate = "private"
)

// The deciders that ship (D1).
const (
	DeciderLocal = "local"
)

// Why a line reads as it does (D3's `why=`).
const (
	WhyProviderError   = "provider-error"
	WhyPrivateEvidence = "private-evidence"
	WhyTamper          = "tamper"
	WhySecretShaped    = "secret-shaped"
)

// ChainDecider is one member of the walk.
//
// D1 names the interface `Decide(ctx, state, questions)`, and that interface
// ALREADY EXISTS in this package as `Decider` (ladder.go:821), where the route
// asks the provider through it. This is deliberately a second, narrower seam
// rather than a replacement: a chain member also has to say its NAME and what
// it may SEE (S7), which a wire-level interface has no business carrying, and
// displacing the route's seam to add two methods would change a call site this
// task has no reason to touch. JevDecider below wraps the existing `Decider`,
// so there is one wire client and not two.
//
// The state a member is handed is already framed, redacted and bounded; a chain
// member never sees an unframed byte.
type ChainDecider interface {
	Name() string
	// Sees is SeesPublic for anything that leaves the machine.
	Sees() string
	Ask(ctx context.Context, q questions.Question, state string) (answer string, confidence float64, usage Usage, err error)
}

// Evidence is one item's text and where it came from. The class is a fact about
// the SOURCE -- the forge reporting a repository public, a bus clone holding a
// marker -- and is never inferred from the text.
type Evidence struct {
	Text    string
	Class   string
	Pointer string
}

// Public reports whether this evidence may be shown to a decider that leaves
// the machine. Anything not positively known to be public is private: the
// boundary fails closed.
func (e Evidence) Public() bool { return e.Class == EvidencePublic }

// RulesDecider is the deterministic table. Confidence is 1.00 where a row
// matches and there is no answer where none does; it sees private evidence
// because it never leaves the machine, and it makes no call.
type RulesDecider struct{ rows map[string]string }

// NewRulesDecider builds the table from substring -> member rows. The rows are
// the caller's data; where a default ships it is a file of plain words a caller
// replaces whole (D1).
func NewRulesDecider(rows map[string]string) *RulesDecider {
	copied := make(map[string]string, len(rows))
	for k, v := range rows {
		copied[k] = v
	}
	return &RulesDecider{rows: copied}
}

func (r *RulesDecider) Name() string { return DeciderRules }
func (r *RulesDecider) Sees() string { return SeesPrivate }

// Ask matches the rows in a stable order, so two runs over one evidence give
// one answer even where two rows could match.
func (r *RulesDecider) Ask(_ context.Context, _ questions.Question, state string) (string, float64, Usage, error) {
	keys := make([]string, 0, len(r.rows))
	for k := range r.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k != "" && strings.Contains(state, k) {
			return r.rows[k], 1.00, Usage{}, nil
		}
	}
	return "", 0, Usage{}, nil
}

// Chain is the ordered walk. Escalate names the stronger reader an unknown or a
// tampered item goes to (D5); it is a name the caller supplies and never one
// this package knows.
type Chain struct {
	Deciders []ChainDecider
	Escalate string
	// Tamper is the pattern table; the shipped one is used when it is nil.
	Tamper []string
	// Now is a var so a test can pin a row's timestamp.
	Now func() time.Time
}

// Result is one classification, and the fields of D3's CLASSIFY line.
type Result struct {
	Question   string
	Version    int
	Answer     string
	Confidence float64
	Floor      float64
	Decider    string
	Stop       bool
	Below      string
	Tamper     bool
	Why        string
	Escalate   string
	Bytes      int
	Hash       string
	Usage      Usage

	// Skipped names the deciders that were not asked and why, so a line that
	// fell through to a weaker decider says so rather than looking like a
	// choice somebody made.
	Skipped string

	// refusal marks a result that is a refusal rather than an answer.
	refusal bool
}

// Exit is D3's exit code: 0 for an answer at or above the floor (and for a
// stop, which the caller acts on), 3 for unknown, 2 for a refusal.
func (r Result) Exit() int {
	switch {
	case r.refusal:
		return 2
	case r.Answer == questions.Unknown:
		return 3
	default:
		return 0
	}
}

// Classify walks the chain for ONE item. One item to a call, always: one item's
// text can never colour another item's answer (D4).
func (c Chain) Classify(ctx context.Context, q questions.Question, ev Evidence, floor float64) Result {
	res := Result{
		Question: q.Name, Version: q.Version, Floor: floor,
		Decider: DeciderNone, Why: WhyNone, Answer: questions.Unknown,
		Bytes: len(ev.Text), Hash: hashOf(ev.Text), Escalate: "",
	}

	// The screen runs before anything is asked of anybody (S5).
	table := c.Tamper
	if table == nil {
		table = questions.DefaultTamper()
	}
	if questions.Tampered(table, ev.Text) {
		res.Tamper = true
		res.Answer = q.TamperAnswer
		res.Why = WhyTamper
		res.Escalate = c.Escalate
		res.Decider = DeciderRules
		return res
	}
	if questions.SecretShaped(ev.Text) {
		res.refusal = true
		res.Why = WhySecretShaped
		return res
	}

	var skipped []string
	deciders := c.rulesFirst()
	for _, d := range deciders {
		// S7: private evidence never reaches a decider that leaves the machine,
		// and the refusal is BEFORE the call.
		if !ev.Public() && d.Sees() == SeesPublic {
			skipped = append(skipped, d.Name()+"="+WhyPrivateEvidence)
			continue
		}
		nonce, err := nonce16()
		if err != nil {
			res.refusal = true
			res.Why = WhyProviderError
			res.Skipped = strings.Join(skipped, ",")
			return res
		}
		state, err := questions.Frame(q, ev.Text, nonce)
		if err != nil {
			res.refusal = true
			res.Why = WhySecretShaped
			res.Skipped = strings.Join(skipped, ",")
			return res
		}
		res.Bytes = len(state)

		answer, conf, usage, err := d.Ask(ctx, q, state)
		res.Usage = usage
		if err != nil {
			skipped = append(skipped, d.Name()+"="+WhyProviderError)
			continue
		}
		if answer == "" {
			// A table with no matching row has said nothing, which is not an
			// answer and not an error. The walk goes on.
			continue
		}
		// S3: an answer outside the set is a provider error and never a
		// decision. Nothing free is parsed, stored or acted on.
		if !q.Member(answer) {
			res.refusal = true
			res.Why = WhyProviderError
			res.Decider = d.Name()
			res.Skipped = strings.Join(skipped, ",")
			return res
		}
		// D2's exception: a stopping member ends the walk BEFORE the floor is
		// looked at, at any confidence, and no later decider is asked.
		if q.Stops(answer) {
			res.Answer, res.Confidence, res.Decider, res.Stop = answer, conf, d.Name(), true
			res.Why = WhyNone
			res.Skipped = strings.Join(skipped, ",")
			return res
		}
		if conf >= floor {
			res.Answer, res.Confidence, res.Decider = answer, conf, d.Name()
			res.Why = WhyNone
			res.Skipped = strings.Join(skipped, ",")
			return res
		}
		// Below the floor: the answer is kept as `below=` and the walk goes on
		// (:100 -- a below-floor answer is logged, never dropped).
		res.Below, res.Confidence, res.Decider = answer, conf, d.Name()
		res.Why = WhyBelowFloor
	}

	res.Skipped = strings.Join(skipped, ",")
	res.Answer = questions.Unknown
	if res.Below == "" {
		res.Decider = DeciderNone
		res.Why = WhyNoDecider
	}
	res.Escalate = c.Escalate
	return res
}

// rulesFirst puts the table at the head of the walk whether or not the caller
// named it, and never twice.
func (c Chain) rulesFirst() []ChainDecider {
	var rules []ChainDecider
	var rest []ChainDecider
	for _, d := range c.Deciders {
		if d.Name() == DeciderRules && len(rules) == 0 {
			rules = append(rules, d)
			continue
		}
		rest = append(rest, d)
	}
	if len(rules) == 0 {
		rules = append(rules, NewRulesDecider(nil))
	}
	return append(rules, rest...)
}

// Line is D3's one CLASSIFY line.
func (r Result) Line(pointer string) string {
	conf := "-"
	if r.Confidence > 0 {
		conf = fmt.Sprintf("%.2f", r.Confidence)
	}
	below := r.Below
	if below == "" {
		below = "-"
	}
	escalate := r.Escalate
	if escalate == "" {
		escalate = "-"
	}
	return fmt.Sprintf("CLASSIFY question=%s/v%d answer=%s conf=%s floor=%.2f decider=%s stop=%s below=%s tamper=%s why=%s escalate=%s pointer=%s bytes=%d",
		r.Question, r.Version, r.Answer, conf, r.Floor, r.Decider,
		yesNoWord(r.Stop), below, yesNoWord(r.Tamper), r.Why, escalate, pointer, r.Bytes)
}

// Row is the classify log's decision row. THE EVIDENCE TEXT IS NEVER IN IT:
// what joins a row to an item is a hash and a size, because a log that held the
// text would be a second copy of everything anybody ever classified, kept
// forever, on a disk nobody audited.
func (r Result) Row(pointer string) string {
	row := map[string]any{
		"time":     time.Now().UTC().Format(time.RFC3339),
		"question": fmt.Sprintf("%s/v%d", r.Question, r.Version),
		"pointer":  pointer,
		"answer":   r.Answer,
		"conf":     r.Confidence,
		"floor":    r.Floor,
		"decider":  r.Decider,
		"stop":     r.Stop,
		"tamper":   r.Tamper,
		"why":      r.Why,
		"hash":     r.Hash,
		"bytes":    r.Bytes,
	}
	body, err := json.Marshal(row)
	if err != nil {
		return `{"error":"encode"}`
	}
	return string(body)
}

func yesNoWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// hashOf is what the log carries instead of the text.
func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// nonce16 draws the frame's sixteen hex digits. It is never derived from the
// evidence (S2).
func nonce16() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("decide: no nonce for the frame: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// JevDecider adapts the existing wire client -- D1's `Decider`, the one the
// route already asks through -- to a chain member. It is `sees=public`: assume
// a remote provider trains on what it sees (S7). LocalDecider is the same wire
// shape at a loopback address, and is `sees=private` for the same reason a
// loopback model is: nothing leaves the machine.
type JevDecider struct {
	Client Decider
	Local  bool
}

// Name is `jev` for the remote provider and `local` for a loopback one.
func (j JevDecider) Name() string {
	if j.Local {
		return DeciderLocal
	}
	return DeciderJev
}

// Sees is the whole point of the type: a remote provider is public, a loopback
// one is private, and the chain refuses private evidence to the first before
// any call is made.
func (j JevDecider) Sees() string {
	if j.Local {
		return SeesPrivate
	}
	return SeesPublic
}

// Ask asks the one question, one item to a call (D4), and returns the choice
// and its confidence. An answer of a type the question did not ask for is no
// answer: the chain's caller sees an empty string and walks on.
func (j JevDecider) Ask(ctx context.Context, q questions.Question, state string) (string, float64, Usage, error) {
	if j.Client == nil {
		return "", 0, Usage{}, fmt.Errorf("decide: %s has no client", j.Name())
	}
	choice := map[string]string{}
	for _, m := range q.Members {
		choice[m] = q.Criteria
	}
	answers, usage, err := j.Client.Decide(ctx, state, map[string]Question{
		q.Name: {Instructions: q.Instructions, Choice: choice},
	})
	if err != nil {
		return "", 0, usage, err
	}
	a, ok := answers[q.Name]
	if !ok {
		return "", 0, usage, nil
	}
	return a.Choice, a.Confidence, usage, nil
}
