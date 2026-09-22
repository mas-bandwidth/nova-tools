// Package receipt turns the three bench checks -- canary, conform and adopt -- into events
// on the card stream instead of files on disk.
//
// DONE-WHEN is the contract, and it has three halves that are one story: canary, conform and
// adopt each APPEND one kind=receipt event carrying pass/fail and the sha; the lineup READS
// them from the stream; and NONE OF THE SIX FILES (CANARY-RECEIPT.txt, BENCH-CONFORM.txt,
// ADOPT-<sha>.txt, ESCALATE, SPRINT-OK-HISTORY.tsv, SPRINT-MODELS.txt) IS WRITTEN. A bench
// result used to be a text file a coordinator had to go and open, one per bench, scattered
// across the tree; here it is one entry on the one stream internal/record and internal/ci
// already read, so a hundred benches fold into one view the same way a hundred DONEs do.
//
// A receipt event is an id and a verdict and nothing else. The field grammar matches
// internal/events -- lower snake case, one machine-scannable line, an RFC3339 stamp -- so the
// fold reads it as just another kind, and the diff, the test log and the transcript stay in
// git rather than reaching the store. The verbs are the writers (Append is the one call each
// makes); the lineup is the reader (Read hands it back every receipt in stream order). This
// package holds no Redis handle of its own: it is written against a Store seam so the verbs
// running in the pulse loop hand it the live stream and the tests hand it NewMemStream.
package receipt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is the one event kind this package writes. Every entry a receipt Append makes carries
// it, and it is what the lineup filters the stream on.
const Kind = "receipt"

// maxFieldBytes bounds every id-shaped field, copied from the stream door so a receipt cannot
// smuggle a payload onto cards:done any wider than a card transition already can.
const maxFieldBytes = 200

// Verb is which bench check a receipt records. The three verbs of DONE-WHEN are the only
// writers, so an unknown verb is refused rather than folded as if it were one of them.
type Verb string

// The three bench checks that append a receipt, in the order a bench walks them.
const (
	Canary  Verb = "canary"
	Conform Verb = "conform"
	Adopt   Verb = "adopt"
)

// Verbs is every writer this package accepts.
var Verbs = []Verb{Canary, Conform, Adopt}

// Known reports whether v is one of the three writers.
func (v Verb) Known() bool {
	for _, w := range Verbs {
		if v == w {
			return true
		}
	}
	return false
}

// Result is pass or fail spelled the way the stream stores it: the verdict DONE-WHEN calls
// "pass/fail", on one line, in lower case so a grep for `result=fail` finds it.
type Result string

const (
	ResultPass Result = "pass"
	ResultFail Result = "fail"
)

// SixFiles are the files that used to be the bench receipts and are now forbidden: a receipt
// is an event, not a file, and the fold is its record. Exposed so a caller and a test can
// assert that none of them was written.
var SixFiles = []string{
	"CANARY-RECEIPT.txt",
	"BENCH-CONFORM.txt",
	"ESCALATE",
	"SPRINT-OK-HISTORY.tsv",
	"SPRINT-MODELS.txt",
}

// Entry is one stream entry: the id the store minted and the fields the writer set. It mirrors
// the shape internal/events reads so the fold treats a receipt as just another kind.
type Entry struct {
	ID     string
	Fields map[string]string
}

// Store is the seam to the live stream: Append writes one entry, Range reads them back in
// order. The pulse loop supplies the Redis-backed stream the DEPENDS-ON card
// redis-event-stream-and-fold opens; the in-memory MemStream below stands in for it offline.
type Store interface {
	XAdd(ctx context.Context, fields map[string]string) (id string, err error)
	XRangeAll(ctx context.Context) ([]Entry, error)
}

// Receipt is one bench check's verdict, read back off the stream.
type Receipt struct {
	Verb    Verb
	Pass    bool
	SHA     string
	Kind    string
	At      time.Time
	EntryID string
}

// Line is the one-line receipt an Append prints, in the same grammar the stream's other kinds
// use: an id, a verb, a verdict and the sha, nothing else.
func (r Receipt) Line(stream string) string {
	return fmt.Sprintf("RECEIPT %s %s label=%s event=%s result=%s head=%s",
		stream, dash(r.EntryID), r.Verb, Kind, resultText(r.Pass), dash(r.SHA))
}

func resultText(pass bool) string {
	if pass {
		return string(ResultPass)
	}
	return string(ResultFail)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Append writes exactly ONE kind=receipt event for one verb: the pass/fail verdict and the sha
// the check was run at, stamped at now. It is the whole of a bench check's record -- it never
// opens a file, so there is no path by which any of the six could reappear. Append returns the
// event id and the receipt as it wrote it, so a caller can print the line without re-reading
// the stream.
func Append(ctx context.Context, store Store, verb Verb, pass bool, sha string, now time.Time) (id string, _ Receipt, err error) {
	r := Receipt{Verb: verb, Pass: pass, SHA: sha, Kind: Kind, At: now.UTC()}
	if err := r.validate(); err != nil {
		return "", Receipt{}, err
	}
	fields := map[string]string{
		"label":   string(r.Verb),
		"event":   Kind,
		"result":  resultText(r.Pass),
		"head":    r.SHA,
		"at":      r.At.Format(time.RFC3339),
		"attempt": "0",
	}
	id, err = store.XAdd(ctx, fields)
	if err != nil {
		return "", Receipt{}, err
	}
	r.EntryID = id
	return id, r, nil
}

// validate is the door: an unknown writer, a missing sha, or a field wide enough to be a
// payload is refused before it reaches the stream. A control character is refused too, because
// it would break the one-line grammar on the way back.
func (r Receipt) validate() error {
	if !r.Verb.Known() {
		return fmt.Errorf("verb %q is not one of %s", string(r.Verb), verbList())
	}
	if err := idShaped("head", r.SHA); err != nil {
		return err
	}
	return nil
}

func verbList() string {
	names := make([]string, 0, len(Verbs))
	for _, v := range Verbs {
		names = append(names, string(v))
	}
	return strings.Join(names, "|")
}

func idShaped(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required; a receipt names the sha it was taken at", name)
	}
	if len(value) > maxFieldBytes {
		return fmt.Errorf("%s is %d bytes; the stream carries ids and counts only, and a field is at most %d bytes", name, len(value), maxFieldBytes)
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("%s carries a control character; the stream carries ids and counts only, one line each", name)
		}
	}
	return nil
}

// Read is what the lineup does: it folds every kind=receipt entry back into Receipts, in
// stream order, oldest first. Entries that are not receipts -- a card's own OK or FAIL -- are
// skipped, and a receipt the reader cannot parse is skipped rather than fatal, the way the
// fold counts a bad writer: one buggy entry must not blind the lineup to every other bench.
func Read(ctx context.Context, store Store) ([]Receipt, error) {
	entries, err := store.XRangeAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []Receipt
	for _, e := range entries {
		if e.Fields["event"] != Kind {
			continue
		}
		r, err := fromFields(e)
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func fromFields(e Entry) (Receipt, error) {
	verb := Verb(e.Fields["label"])
	if !verb.Known() {
		return Receipt{}, fmt.Errorf("verb %q is not one of %s", string(verb), verbList())
	}
	result := Result(e.Fields["result"])
	var pass bool
	switch result {
	case ResultPass:
		pass = true
	case ResultFail:
		pass = false
	default:
		return Receipt{}, fmt.Errorf("result %q is not pass or fail", string(result))
	}
	r := Receipt{Verb: verb, Pass: pass, SHA: e.Fields["head"], Kind: Kind, EntryID: e.ID}
	if raw := strings.TrimSpace(e.Fields["at"]); raw != "" {
		when, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return Receipt{}, fmt.Errorf("at %q is not an RFC3339 stamp", raw)
		}
		r.At = when.UTC()
	}
	if err := r.validate(); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

// MemStream is the in-memory Store the tests run against and the offline path the verbs use
// when no Redis is in reach: the same append and read the live stream gives, over a slice.
type MemStream struct {
	mu      sync.Mutex
	entries []Entry
	seq     int64
}

// NewMemStream returns an empty stream.
func NewMemStream() *MemStream { return &MemStream{} }

// XAdd appends one entry and mints an id in Redis's `<ms>-<seq>` shape, so the entry an
// Append returns reads exactly like one a live XADD would.
func (m *MemStream) XAdd(_ context.Context, fields map[string]string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := strconv.FormatInt(m.seq, 10) + "-0"
	cp := make(map[string]string, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	m.entries = append(m.entries, Entry{ID: id, Fields: cp})
	return id, nil
}

// XRangeAll returns every entry in the order it was written.
func (m *MemStream) XRangeAll(_ context.Context) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out, nil
}
