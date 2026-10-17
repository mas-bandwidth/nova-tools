package release

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"math"
	"slices"
	"time"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// The record readers of the release cycle: the build arguments the cycle
// hands the tools play (cycle.go), the journey evidence header and each event
// line (journeygate.go readJourneyEvidence) and the spend receipts file
// (spendcheck_sources.go FileReceipts.Tokens). Each is read through
// pkg/schemaread with its generated table of pkg/releasewire in every form it
// may arrive in: the binary wire, the padded standard base64 of that wire,
// the schema JSON text form, and the legacy form its writer writes today
// (the owner's rule S2: a reader accepts each form, refuses damage by name and
// counts reads per form). A damaged value is refused as
// `<RECORD> REFUSED: <reason>; run: <help>`, never read as absent, empty or
// zero, and every read is counted under its record and its form: the
// cutover's evidence (S4, readers first, UNION.md C13). Every writer still
// writes the legacy form.
//
// The tables are VARIABLE (S1, schemas/release/release.schema): a friend's
// tools may meet these records from another version, and none of them holds
// a bound the code enforces, so the schema declares none and the generated
// load clamps nothing. The one bound here is the receipts' token count, which
// is never negative (receiptsConstraints), checked on every form.

// Records is one set of the release cycle's record readers, each counting its
// own reads per form and its refusals by reason. The cycle and the gates read
// through the process's one set; a test makes its own to count.
type Records struct {
	BuildArgs *schemaread.Reader[[]string]       // CYCLE_BUILD_ARGS: the play's nova_release_build_args
	Header    *schemaread.Reader[EvidenceHeader] // JOURNEY_HEADER: the evidence file's first line
	Event     *schemaread.Reader[testEvent]      // JOURNEY_EVENT: each later line of the evidence file
	Receipts  *schemaread.Reader[spendReceipts]  // SPEND_RECEIPTS: the subscription friends' receipts file
}

var records = NewRecords()

// NewRecords is a set of record readers with empty counters.
func NewRecords() *Records {
	return &Records{
		BuildArgs: schemaread.NewReader(buildArgsCodec()),
		Header:    schemaread.NewReader(headerCodec()),
		Event:     schemaread.NewReader(eventCodec()),
		Receipts:  schemaread.NewReader(receiptsCodec()),
	}
}

// ReadBuildArgs reads the play's build arguments (cycle.go).
func (r *Records) ReadBuildArgs(raw []byte) ([]string, error) {
	v, _, err := r.BuildArgs.Read(raw)
	return v, err
}

// ReadHeader reads the journey evidence header line (journeygate.go).
func (r *Records) ReadHeader(raw []byte) (EvidenceHeader, error) {
	v, _, err := r.Header.Read(raw)
	return v, err
}

// ReadEvent reads one journey evidence event line (journeygate.go).
func (r *Records) ReadEvent(raw []byte) (testEvent, error) {
	v, _, err := r.Event.Read(raw)
	return v, err
}

// ReadReceipts reads the spend receipts file (spendcheck_sources.go).
func (r *Records) ReadReceipts(raw []byte) (spendReceipts, error) {
	v, _, err := r.Receipts.Read(raw)
	return v, err
}

// spendReceipts is the receipts file's one record (spendcheck_sources.go
// FileReceipts): {"evidence":"spend-receipts","from":<RFC3339>,
// "to":<RFC3339>,"friends":{"<friend>":<tokens>}}.
type spendReceipts struct {
	Evidence string           `json:"evidence"`
	From     time.Time        `json:"from"`
	To       time.Time        `json:"to"`
	Friends  map[string]int64 `json:"friends"`
}

// ---------------------------------------------------------------------------
// The generated table, read and written through its builder.

// builder is the generated builder of one table: W is the table's row type.
type builder[W, B any] interface {
	*B
	Init(...wire.TableAllocator) bool
	Lock() bool
	AsConst() *W
	Shutdown()
}

// table is the generated functions of one table the codec needs.
type table[W, B any, PB builder[W, B]] struct {
	measureReason func([]byte) (int64, error)
	loadBuilder   func(*B, []byte, *wire.TableReport) bool
	fromJSON      func(*B, []byte, *wire.TableReport) bool
	measure       func(*W, ...wire.TableWriteContext) int64
	save          func(*W, []byte, ...wire.TableWriteContext) int64
}

// report is the generated TableReport reduced to what the strict read decides
// on; a repeated text key is damage, never a silent last-one-wins.
func report(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0 || r.MissingRequired > 0,
		Reason:       r.Reason,
	}
}

// open loads in with load (the wire or the JSON text form) into a builder and
// hands the locked value to from, which copies it out. A value from cannot
// read (a time that is not RFC 3339, a friend named twice) is malformed.
func open[T, W, B any, PB builder[W, B]](load func(*B, []byte, *wire.TableReport) bool, in []byte, v *T, from func(*W) (T, bool)) schemaread.Report {
	b := PB(new(B))
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var r wire.TableReport
	ok := load((*B)(b), in, &r)
	rep := report(&r)
	if !ok {
		rep.Malformed = rep.Reason == "" || rep.Malformed
		return rep
	}
	if !b.Lock() {
		return schemaread.Report{Malformed: true}
	}
	out, good := from(b.AsConst())
	if !good {
		return schemaread.Report{Malformed: true}
	}
	*v = out
	return rep
}

// typedCodec is one record's codec: the generated table for the wire, its
// base64 and the JSON text form; the legacy load for the form its writer
// writes today; and the record's constraints, run on every form and before
// every write. to is the record as the schema JSON text form spells it, the
// one writer of the typed forms (the wire is built from that text by the
// generated reader, so the two never disagree).
func typedCodec[T, W, B any, PB builder[W, B]](name, help string, tb table[W, B, PB],
	from func(*W) (T, bool), to func(*T) any,
	legacy func([]byte) bool, loadLegacy func(*T, []byte) schemaread.Report,
	constraints func(*T, *schemaread.Checker)) *schemaread.Codec[T] {
	saveJSON := func(v *T) []byte {
		out, err := json.Marshal(to(v))
		if err != nil {
			return nil
		}
		return out
	}
	return &schemaread.Codec[T]{
		Name:  name,
		Help:  help,
		Reset: func(v *T) { var zero T; *v = zero },
		Measure: func(w []byte) error {
			_, err := tb.measureReason(w)
			return err
		},
		LoadWire: func(v *T, w []byte) schemaread.Report {
			return open[T, W, B, PB](tb.loadBuilder, w, v, from)
		},
		LoadJSON: func(v *T, text []byte) schemaread.Report {
			return open[T, W, B, PB](tb.fromJSON, text, v, from)
		},
		Legacy:      legacy,
		LoadLegacy:  loadLegacy,
		Constraints: constraints,
		SaveJSON:    saveJSON,
		SaveWire: func(v *T) []byte {
			text := saveJSON(v)
			if text == nil {
				return nil
			}
			b := PB(new(B))
			if !b.Init() {
				return nil
			}
			defer b.Shutdown()
			var r wire.TableReport
			if !tb.fromJSON((*B)(b), text, &r) || !b.Lock() {
				return nil
			}
			n := tb.measure(b.AsConst())
			if n <= 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.save(b.AsConst(), buf) != n {
				return nil
			}
			return buf
		},
	}
}

// text is a loaded string slot of a generated value.
func text(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// decodeOne reads exactly one JSON value into v: an unreadable value is
// malformed, and one followed by more than white space is malformed too, so
// two records glued together are never read as the first.
func decodeOne[T any](v *T, in []byte) schemaread.Report {
	d := json.NewDecoder(bytes.NewReader(in))
	if err := d.Decode(v); err != nil {
		return schemaread.Report{Malformed: true}
	}
	if _, err := d.Token(); err != io.EOF {
		return schemaread.Report{Malformed: true}
	}
	return schemaread.Report{}
}

// keys is the top-level keys of a JSON object, or false when in is none.
func keys(in []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if len(in) == 0 || in[0] != '{' || json.Unmarshal(in, &m) != nil {
		return nil, false
	}
	return m, true
}

// ---------------------------------------------------------------------------
// CYCLE_BUILD_ARGS: the -e extra variable the cycle hands the tools play.

const buildArgsKey = "nova_release_build_args"

type wordText struct {
	Text string `json:"text"`
}

// buildArgsCodec reads the build arguments. The legacy form is cycle.go's
// {"nova_release_build_args":["--incremental",...]}, a list of strings; the
// schema JSON text form wraps each as {"text":...} (release.schema Word), so
// a list whose elements are objects is the JSON text form and any other
// object is legacy. An empty list reads the same either way.
func buildArgsCodec() *schemaread.Codec[[]string] {
	return typedCodec[[]string, wire.CycleBuildArgs, wire.CycleBuildArgsBuilder]("CYCLE_BUILD_ARGS", "nova-update release cycle -h",
		table[wire.CycleBuildArgs, wire.CycleBuildArgsBuilder, *wire.CycleBuildArgsBuilder]{
			measureReason: wire.CycleBuildArgsLoadMeasureReason,
			loadBuilder:   wire.CycleBuildArgsLoadBuilder,
			fromJSON:      wire.CycleBuildArgsFromJson,
			measure:       wire.CycleBuildArgsMeasure,
			save:          wire.CycleBuildArgsSave,
		},
		func(w *wire.CycleBuildArgs) ([]string, bool) {
			out := make([]string, 0, w.NovaReleaseBuildArgs.Count)
			for i := int32(0); i < w.NovaReleaseBuildArgs.Count; i++ {
				out = append(out, text(&w.NovaReleaseBuildArgs.At(i).Text))
			}
			return out, true
		},
		func(v *[]string) any {
			words := make([]wordText, len(*v))
			for i, s := range *v {
				words[i] = wordText{s}
			}
			return map[string][]wordText{buildArgsKey: words}
		},
		func(in []byte) bool {
			m, ok := keys(in)
			if !ok {
				return in[0] == '{' // unreadable JSON: the legacy load refuses it as malformed
			}
			var elems []json.RawMessage
			if json.Unmarshal(m[buildArgsKey], &elems) != nil {
				return true
			}
			for _, e := range elems {
				if len(e) > 0 && e[0] == '{' {
					return false
				}
			}
			return true
		},
		func(v *[]string, in []byte) schemaread.Report {
			var m map[string][]string
			if rep := decodeOne(&m, in); rep != (schemaread.Report{}) {
				return rep
			}
			args, ok := m[buildArgsKey]
			if !ok {
				return schemaread.Report{Reason: schemaread.ReasonAbsentRequired}
			}
			*v = args
			return schemaread.Report{}
		},
		// Each argument reaches the play's command line: exec refuses a NUL
		// in an argument, so a value that holds one is refused here by name.
		func(v *[]string, c *schemaread.Checker) {
			c.Required(buildArgsKey, len(*v) > 0)
			for _, a := range *v {
				c.Text(buildArgsKey, a, math.MaxInt)
			}
		})
}

// ---------------------------------------------------------------------------
// JOURNEY_HEADER and JOURNEY_EVENT: the journey evidence file's lines.

// headerJSON is the header as the schema JSON text form writes it: an empty
// installed list is left out, as the table's writer leaves it.
type headerJSON struct {
	Evidence  string             `json:"evidence"`
	Revision  string             `json:"revision"`
	Functions string             `json:"functions"`
	Schema    string             `json:"schema"`
	Installed []InstalledReceipt `json:"installed,omitempty"`
}

// headerCodec reads the evidence header. Its legacy text is the schema JSON
// text form byte for byte (EvidenceHeader's keys are the table's), so it reads,
// and is counted, as json: there is no separate legacy form to tell apart.
// What bindEvidence requires stays its own refusal; the reader requires only
// what makes the line a header at all, the evidence kind (journeygate.go).
func headerCodec() *schemaread.Codec[EvidenceHeader] {
	return typedCodec[EvidenceHeader, wire.JourneyHeader, wire.JourneyHeaderBuilder]("JOURNEY_HEADER", "nova-update release cut -h",
		table[wire.JourneyHeader, wire.JourneyHeaderBuilder, *wire.JourneyHeaderBuilder]{
			measureReason: wire.JourneyHeaderLoadMeasureReason,
			loadBuilder:   wire.JourneyHeaderLoadBuilder,
			fromJSON:      wire.JourneyHeaderFromJson,
			measure:       wire.JourneyHeaderMeasure,
			save:          wire.JourneyHeaderSave,
		},
		func(w *wire.JourneyHeader) (EvidenceHeader, bool) {
			h := EvidenceHeader{Evidence: text(&w.Evidence), Revision: text(&w.Revision), Functions: text(&w.Functions), Schema: text(&w.Schema)}
			for i := int32(0); i < w.Installed.Count; i++ {
				r := w.Installed.At(i)
				h.Installed = append(h.Installed, InstalledReceipt{Machine: text(&r.Machine), Build: text(&r.Build), Revision: text(&r.Revision)})
			}
			return h, true
		},
		func(v *EvidenceHeader) any { return headerJSON(*v) },
		nil, nil,
		func(v *EvidenceHeader, c *schemaread.Checker) {
			c.Required("evidence", v.Evidence == EvidenceKind)
		})
}

// eventJSON is a journey event as the schema JSON text form spells it
// (release.schema JourneyEvent): lower-case keys.
type eventJSON struct {
	Action  string `json:"action,omitempty"`
	Package string `json:"package,omitempty"`
	Test    string `json:"test,omitempty"`
	Output  string `json:"output,omitempty"`
}

// eventCodec reads one event line. The legacy form is the line as `go test
// -json` writes it, with capitalised keys ("Action", "Package", ...); the
// schema JSON text form spells them in lower case, so an object with the key
// "action" is the JSON text form and any other object is legacy. Every event
// has an action (eventCodec's constraints).
func eventCodec() *schemaread.Codec[testEvent] {
	return typedCodec[testEvent, wire.JourneyEvent, wire.JourneyEventBuilder]("JOURNEY_EVENT", "nova-update release cut -h",
		table[wire.JourneyEvent, wire.JourneyEventBuilder, *wire.JourneyEventBuilder]{
			measureReason: wire.JourneyEventLoadMeasureReason,
			loadBuilder:   wire.JourneyEventLoadBuilder,
			fromJSON:      wire.JourneyEventFromJson,
			measure:       wire.JourneyEventMeasure,
			save:          wire.JourneyEventSave,
		},
		func(w *wire.JourneyEvent) (testEvent, bool) {
			return testEvent{Action: text(&w.Action), Package: text(&w.Pkg), Test: text(&w.Test), Output: text(&w.Output)}, true
		},
		func(v *testEvent) any { return eventJSON(*v) },
		func(in []byte) bool {
			m, ok := keys(in)
			if !ok {
				return in[0] == '{'
			}
			_, lower := m["action"]
			return !lower
		},
		func(v *testEvent, in []byte) schemaread.Report { return decodeOne(v, in) },
		// `go test -json` writes an action on every event; a line with none
		// is no event, and a damaged image whose fields all read as unknown
		// is refused here rather than read as an empty event.
		func(v *testEvent, c *schemaread.Checker) {
			c.Required("action", v.Action != "")
		})
}

// ---------------------------------------------------------------------------
// SPEND_RECEIPTS: the subscription friends' harness receipts file.

type receiptFriendJSON struct {
	Friend string `json:"friend"`
	Tokens int64  `json:"tokens"`
}

type receiptsJSON struct {
	Evidence string              `json:"evidence"`
	From     string              `json:"from"`
	To       string              `json:"to"`
	Friends  []receiptFriendJSON `json:"friends"`
}

// receiptsCodec reads the receipts file. The legacy form holds the friends
// as a JSON object of tokens by name; the schema JSON text form holds them as
// a list of {"friend","tokens"} (release.schema SpendReceiptFriend), so an
// object whose friends is a list is the JSON text form and any other object
// is legacy. A typed form holds no absent-or-empty distinction: zero friends
// read as present and empty, and a friend named twice is malformed.
func receiptsCodec() *schemaread.Codec[spendReceipts] {
	return typedCodec[spendReceipts, wire.SpendReceipts, wire.SpendReceiptsBuilder]("SPEND_RECEIPTS", "nova-update release cut -h",
		table[wire.SpendReceipts, wire.SpendReceiptsBuilder, *wire.SpendReceiptsBuilder]{
			measureReason: wire.SpendReceiptsLoadMeasureReason,
			loadBuilder:   wire.SpendReceiptsLoadBuilder,
			fromJSON:      wire.SpendReceiptsFromJson,
			measure:       wire.SpendReceiptsMeasure,
			save:          wire.SpendReceiptsSave,
		},
		func(w *wire.SpendReceipts) (spendReceipts, bool) {
			from, err1 := time.Parse(time.RFC3339, text(&w.FromTime))
			to, err2 := time.Parse(time.RFC3339, text(&w.ToTime))
			if err1 != nil || err2 != nil {
				return spendReceipts{}, false
			}
			out := spendReceipts{Evidence: text(&w.Evidence), From: from, To: to, Friends: map[string]int64{}}
			for i := int32(0); i < w.Friends.Count; i++ {
				f := w.Friends.At(i)
				name := text(&f.FriendName)
				if _, twice := out.Friends[name]; twice {
					return spendReceipts{}, false
				}
				out.Friends[name] = f.Tokens
			}
			return out, true
		},
		func(v *spendReceipts) any {
			out := receiptsJSON{Evidence: v.Evidence, From: v.From.Format(time.RFC3339Nano), To: v.To.Format(time.RFC3339Nano), Friends: []receiptFriendJSON{}}
			for _, name := range slices.Sorted(maps.Keys(v.Friends)) {
				out.Friends = append(out.Friends, receiptFriendJSON{name, v.Friends[name]})
			}
			return out
		},
		func(in []byte) bool {
			m, ok := keys(in)
			if !ok {
				return in[0] == '{'
			}
			f := bytes.TrimSpace(m["friends"])
			return len(f) == 0 || f[0] != '['
		},
		func(v *spendReceipts, in []byte) schemaread.Report { return decodeOne(v, in) },
		receiptsConstraints)
}

// receiptsConstraints holds the receipts to FileReceipts.Tokens: the evidence
// kind, a friends record present, and token counts that are never negative
// (a count is a harness's tally; a negative one would lower the recorded
// spend the gate compares, so it is refused as clamped on every form).
func receiptsConstraints(v *spendReceipts, c *schemaread.Checker) {
	c.Required("evidence", v.Evidence == ReceiptsKind)
	c.Required("friends", v.Friends != nil)
	for _, name := range slices.Sorted(maps.Keys(v.Friends)) {
		c.Bound("friends.tokens", v.Friends[name], 0, math.MaxInt64)
	}
}
