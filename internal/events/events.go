// Package events is the card event stream of nova-tools #2563 and the SQLite fold that is
// its record: every card transition XADDs one entry to `cards:done`, and one consumer folds
// the stream into a SQLite file whose views answer "how many, how much, per model, per
// route, per bench, per day".
//
// THERE IS ONE STREAM (Rowan's ruling on the bus 2026-09-22, accepted by Johnny 17:16Z).
// `cards:done` is the stream internal/record and internal/ci already read; this package does
// not mint a second one beside it, it adds the event fields to the entries of that stream.
// The SQLite file is a fold of `cards:done` and nothing else: a view that `fold --rebuild`
// can recompute from the stream at any moment, never a second source of truth. An entry
// written before the fields were added carries no `event` field; the fold counts it as
// skipped rather than guessing it into `ok` or `fail`.
//
// The division of labour is Johnny's (reports/redis-for-nova-tools-2026-09-21.md section 8,
// adopted): Redis holds ids, counts and event ids and NOTHING else -- never a diff, a test,
// a prompt, a transcript or a disposition -- and every query lives in the fold, over core
// Redis types only, so Valkey stays a drop-in and the hot store never becomes the database.
// Validate enforces that literally: a field over maxFieldBytes or carrying a control
// character is refused at the door rather than trusted to the caller's good manners.
//
// Streams deliver at least once, so the event id is the primary key of every fold table and
// a redelivery is a no-op. That is what makes Johnny's bar 3 -- a hundred DONEs, kill the
// fold, restart, the count is a hundred -- a property of the schema rather than of luck.
package events

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Stream is the one stream every card transition writes to, Group the consumer group the
// fold reads under, and MaxLen the approximate cap XADD trims to (MAXLEN ~ 1e6): a stream
// that grows without a bound is a store that fills a disk no one is watching.
const (
	Stream = "cards:done"
	Group  = "fold"
	MaxLen = 1_000_000
)

// maxFieldBytes is how long any one id-shaped field may be. It is the mechanical half of
// "nothing but ids and counts in Redis": a diff, a prompt or a transcript does not fit, and
// a caller that tries is refused by name instead of quietly filling the stream.
const maxFieldBytes = 200

// Kind is the transition one entry records.
type Kind string

// The kinds, in the order a card walks them. `read` is a friend's read of a pull request,
// `landed` the lander's merge, `jev` a Jev decision; the rest are the card's own life.
// `decide` (decide.go) is a routing decision carrying the decide_log fields: the record
// that was the decide_log table until #2623.
const (
	Queued    Kind = "queued"
	Leased    Kind = "leased"
	Started   Kind = "started"
	Turn      Kind = "turn"
	OK        Kind = "ok"
	Fail      Kind = "fail"
	Asked     Kind = "asked"
	Harvested Kind = "harvested"
	PullReq   Kind = "pr"
	Read      Kind = "read"
	Landed    Kind = "landed"
	Jev       Kind = "jev"
)

// Kinds is every kind the stream accepts, in banner order.
var Kinds = []Kind{Queued, Leased, Started, Turn, OK, Fail, Asked, Harvested, PullReq, Read, Landed, Jev, Decide}

// KindList is the kinds as the refusal and the usage banner spell them.
func KindList() string {
	names := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		names = append(names, string(k))
	}
	return strings.Join(names, "|")
}

// Known reports whether k is a kind the stream accepts.
func (k Kind) Known() bool {
	for _, want := range Kinds {
		if k == want {
			return true
		}
	}
	return false
}

// Event is one card transition. Every string field is an id or a name; every number is a
// count or a price. There is no field for prose and that is the point.
//
// TokensIn, TokensOut and USD are pointers because an absent cost is not a zero cost (no
// evidence is not negative evidence): nil means nobody reported the number, the entry then
// carries no such field at all, and the fold stores NULL. A zero is a writer saying zero.
type Event struct {
	Label     string
	Attempt   int
	Bench     string
	Model     string
	Route     string
	Kind      Kind
	TokensIn  *int64
	TokensOut *int64
	USD       *float64
	PR        string
	Head      string
	At        time.Time

	// Decision is the decide_log row a `decide` entry carries, and nil on every other kind.
	Decision *Decision
}

// Int64 and Float64 are the one-line way to fill a reported number: Event{USD: Float64(0.11)}.
func Int64(n int64) *int64       { return &n }
func Float64(f float64) *float64 { return &f }

// fieldNames are the stream's field names, in the order XADD writes them. They are the
// names nova-tools #2563 spells, so a bash writer and this package agree on the wire.
var fieldNames = []string{
	"label", "attempt", "bench", "model", "route", "event",
	"tokens_in", "tokens_out", "usd", "pr", "head", "at",
}

// Validate is the door. It refuses what must never reach Redis rather than trusting the
// caller: an unknown kind, a negative count, a field long enough to be a payload, and any
// control character, which would also break the one-line output grammar on the way back.
func (e Event) Validate() error {
	if strings.TrimSpace(e.Label) == "" {
		return fmt.Errorf("label is required; it wants the card label, the id the whole stream joins on")
	}
	if !e.Kind.Known() {
		return fmt.Errorf("event %q is not one of %s", string(e.Kind), KindList())
	}
	for _, f := range []struct{ name, value string }{
		{"label", e.Label},
		{"bench", e.Bench},
		{"model", e.Model},
		{"route", e.Route},
		{"pr", e.PR},
		{"head", e.Head},
	} {
		if err := idShaped(f.name, f.value); err != nil {
			return err
		}
	}
	if e.Attempt < 0 {
		return fmt.Errorf("attempt is a count, got %d", e.Attempt)
	}
	if e.TokensIn != nil && *e.TokensIn < 0 {
		return fmt.Errorf("tokens_in and tokens_out are counts, got tokens_in %d", *e.TokensIn)
	}
	if e.TokensOut != nil && *e.TokensOut < 0 {
		return fmt.Errorf("tokens_in and tokens_out are counts, got tokens_out %d", *e.TokensOut)
	}
	if e.USD != nil && (math.IsNaN(*e.USD) || math.IsInf(*e.USD, 0) || *e.USD < 0) {
		return fmt.Errorf("usd is a price in dollars, got %v", *e.USD)
	}
	switch {
	case e.Kind == Decide && e.Decision == nil:
		return fmt.Errorf("a decide event carries the decision's fields; nova-decide route --store writes it, not a hand")
	case e.Kind != Decide && e.Decision != nil:
		return fmt.Errorf("only a decide event carries a decision, and this one is %q", string(e.Kind))
	case e.Decision != nil:
		return e.Decision.validate()
	}
	return nil
}

// idShaped holds one string field to the id rule: short, printable, one line.
func idShaped(name, value string) error {
	if len(value) > maxFieldBytes {
		return fmt.Errorf("%s is %d bytes; the stream carries ids and counts only, and a field is at most %d bytes (the diff, the test, the prompt and the transcript live in git)", name, len(value), maxFieldBytes)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s carries a control character; the stream carries ids and counts only, one line each", name)
		}
	}
	return nil
}

// Stamp returns the event with At filled from now when the caller left it zero, so the
// emitter stamps exactly once and a replay never restamps.
func (e Event) Stamp(now time.Time) Event {
	if e.At.IsZero() {
		e.At = now.UTC()
	} else {
		e.At = e.At.UTC()
	}
	return e
}

// Values is the flat field/value list XADD writes, in fieldNames order. A number nobody
// reported is left out, so the entry has no such field rather than a zero.
func (e Event) Values() []any {
	f := e.Fields()
	out := make([]any, 0, 2*(len(fieldNames)+len(decisionFieldNames)))
	for _, names := range [][]string{fieldNames, decisionFieldNames} {
		for _, name := range names {
			if v, ok := f[name]; ok {
				out = append(out, name, v)
			}
		}
	}
	return out
}

// Fields is the entry as Redis stores it: every value a string, every name the one #2563
// spells. tokens_in, tokens_out and usd are present only when the event carries them.
func (e Event) Fields() map[string]string {
	at := ""
	if !e.At.IsZero() {
		at = e.At.UTC().Format(time.RFC3339)
	}
	f := map[string]string{
		"label":   e.Label,
		"attempt": strconv.Itoa(e.Attempt),
		"bench":   e.Bench,
		"model":   e.Model,
		"route":   e.Route,
		"event":   string(e.Kind),
		"pr":      e.PR,
		"head":    e.Head,
		"at":      at,
	}
	if e.TokensIn != nil {
		f["tokens_in"] = strconv.FormatInt(*e.TokensIn, 10)
	}
	if e.TokensOut != nil {
		f["tokens_out"] = strconv.FormatInt(*e.TokensOut, 10)
	}
	if e.USD != nil {
		f["usd"] = strconv.FormatFloat(*e.USD, 'f', -1, 64)
	}
	if e.Decision != nil {
		e.Decision.fields(f)
	}
	return f
}

// FromFields reads one stream entry back. A missing tokens_in, tokens_out or usd stays
// ABSENT (nil), because a writer that did not know the cost has not reported a zero cost; a
// number that is present and unreadable is an error, because that is a writer with a bug.
func FromFields(f map[string]string) (Event, error) {
	e := Event{
		Label: f["label"],
		Bench: f["bench"],
		Model: f["model"],
		Route: f["route"],
		Kind:  Kind(f["event"]),
		PR:    f["pr"],
		Head:  f["head"],
	}
	var err error
	if e.Attempt, err = atoiField(f, "attempt"); err != nil {
		return Event{}, err
	}
	if e.TokensIn, err = optInt64Field(f, "tokens_in"); err != nil {
		return Event{}, err
	}
	if e.TokensOut, err = optInt64Field(f, "tokens_out"); err != nil {
		return Event{}, err
	}
	if raw := strings.TrimSpace(f["usd"]); raw != "" {
		usd, perr := strconv.ParseFloat(raw, 64)
		if perr != nil {
			return Event{}, fmt.Errorf("usd %q is not a number", raw)
		}
		e.USD = &usd
	}
	if raw := strings.TrimSpace(f["at"]); raw != "" {
		when, perr := time.Parse(time.RFC3339, raw)
		if perr != nil {
			return Event{}, fmt.Errorf("at %q is not an RFC3339 stamp", raw)
		}
		e.At = when.UTC()
	}
	if e.Kind == Decide {
		if e.Decision, err = decisionFromFields(f); err != nil {
			return Event{}, err
		}
	}
	if err := e.Validate(); err != nil {
		return Event{}, err
	}
	return e, nil
}

// atoiField reads attempt, the one count whose absence is its zero: an entry that names no
// attempt is the card's first.
func atoiField(f map[string]string, name string) (int, error) {
	n, err := optInt64Field(f, name)
	if n == nil {
		return 0, err
	}
	return int(*n), err
}

// optInt64Field is nil for an absent (or empty) field and an error for an unreadable one.
func optInt64Field(f map[string]string, name string) (*int64, error) {
	raw := strings.TrimSpace(f[name])
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s %q is not a whole number", name, raw)
	}
	return &n, nil
}

// Day is the UTC date the event belongs to, or "" when it carries no stamp. It is computed
// once at fold time so the per-day view is a column and not a string function in a query.
func (e Event) Day() string {
	if e.At.IsZero() {
		return ""
	}
	return e.At.UTC().Format("2006-01-02")
}

// Line is the one-line receipt `nova-pulse event` prints.
func (e Event) Line(stream, id string) string {
	return fmt.Sprintf("EVENT %s %s label=%s event=%s attempt=%d bench=%s model=%s route=%s usd=%s",
		stream, id, e.Label, string(e.Kind), e.Attempt, dash(e.Bench), dash(e.Model), dash(e.Route),
		usdText(e.USD))
}

// usdText is the price as the receipt prints it: a dash when nobody reported one.
func usdText(usd *float64) string {
	if usd == nil {
		return "-"
	}
	return strconv.FormatFloat(*usd, 'f', -1, 64)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
