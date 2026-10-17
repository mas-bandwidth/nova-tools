package swarm

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/swarmwire"
)

// ExitRecord is <job>/exit.json: the supervisor's durable completion evidence (rule 18,
// step 6). An outcome with none is `unknown`, never guessed. `Attest` is the per-launch
// secret the supervisor minted in its own memory, written here only inside endWith after the
// job's whole process group is dead; its hash lives in the slot file, and a reader accepts
// this record as the supervisor's own word only when both the nonce AND the attestation
// match.
type ExitRecord struct {
	RC        int    `json:"rc"`
	Signal    string `json:"signal,omitempty"`
	Ended     string `json:"ended"`
	Survivors int    `json:"survivors"`
	Nonce     string `json:"nonce"`
	Attest    string `json:"attest,omitempty"`
	End       string `json:"end,omitempty"`
	Spent     int    `json:"spent,omitempty"`
	Observed  bool   `json:"observed,omitempty"`
	Partial   bool   `json:"partial,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Nonce is twelve hex characters from the OS random source, drawn once per launch and never
// reused. It is what makes a stale supervisor's identify fail against a reservation that
// has moved on.
func Nonce() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the OS random source would not supply a launch nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ReadJSON reads one of this package's small records back.
// The exit record is read through its strict reader in every form (owner's
// rules S2 and S4, readers first), so a damaged exit.json is refused by name
// and never read as a zero record; every other record reads as JSON.
func ReadJSON(path string, v any) error {
	if rec, ok := v.(*ExitRecord); ok {
		got, _, err := ReadExitFile(path)
		if err != nil {
			return err
		}
		*rec = got
		return nil
	}
	raw, err := readFileSteady(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// The job directory's durable records.
func ExitPath(jobDir string) string   { return filepath.Join(jobDir, "exit.json") }
func ResultPath(jobDir string) string { return filepath.Join(jobDir, "RESULT.md") }

// exitDoc is the exit record as the schema reads it: the plain value the
// generated table's strict codec reads and writes (schema section 6, the
// schema JSON text form). The legacy form is the JSON the writer writes today
// (ExitRecord's json tags), which reads into the same value.
type exitDoc struct {
	Rc                 int64
	Signal, Ended      string
	Survivors          int64
	Nonce, Attest, End string
	Spent              int64
	Observed, Partial  bool
	Reason             string
}

func (d exitDoc) json() []byte {
	m := map[string]any{"rc": d.Rc, "signal": d.Signal, "ended": d.Ended, "survivors": d.Survivors, "nonce": d.Nonce,
		"attest": d.Attest, "end": d.End, "spent": d.Spent, "observed": d.Observed, "partial": d.Partial, "reason": d.Reason}
	for k, v := range m {
		if s, ok := v.(string); ok && s == "" {
			delete(m, k)
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return b
}

func exitText(ref *swarmwire.TableRef) string {
	v := swarmwire.TableStringAt(ref)
	return string(v.Data[:v.Length])
}

func exitOf(e *swarmwire.ExitRecord) exitDoc {
	return exitDoc{e.Rc, exitText(&e.Signal), exitText(&e.Ended), e.Survivors, exitText(&e.Nonce), exitText(&e.Attest), exitText(&e.End),
		e.Spent, e.Observed, e.Partial, exitText(&e.Reason)}
}

func exitRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func exitStrict(r swarmwire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed || !ok || r.Duplicate > 0}
	if r.Verdict == swarmwire.TableOpenRefused || r.Verdict == swarmwire.TableOpenDamaged {
		out.Reason = r.Err().Error()
	}
	return out
}

func exitBuild(d exitDoc) (*swarmwire.ExitRecord, func()) {
	var b swarmwire.ExitRecordBuilder
	if !b.Init() {
		return nil, func() {}
	}
	var r swarmwire.TableReport
	if !swarmwire.ExitRecordFromJson(&b, d.json(), &r) || !b.Lock() {
		return nil, b.Shutdown
	}
	return b.AsConst(), b.Shutdown
}

// exitReader is the exit record's strict reader (owner's rules S1 and S2):
// it reads the table wire, the padded standard base64 of that wire, the
// schema JSON text and the legacy JSON the writer writes today, refuses a
// damaged image by name and counts each read under its form. Writers stay
// legacy (ExitPath), so the read counters are the cutover's evidence.
func exitReader() *schemaread.Reader[exitDoc] {
	var zero exitDoc
	c := &schemaread.Codec[exitDoc]{
		Name: "EXIT_RECORD", Help: "nova-swarm -h",
		LoadWire: func(v *exitDoc, wire []byte) schemaread.Report {
			*v = zero
			n := swarmwire.ExitRecordLoadMeasure(wire)
			if n < 0 {
				return exitStrict(swarmwire.TableReport{Malformed: true}, false)
			}
			var r swarmwire.TableReport
			root := swarmwire.ExitRecordLoad(exitRegion(n), wire, &r)
			if root == nil {
				return exitStrict(r, false)
			}
			*v = exitOf(root)
			return exitStrict(r, true)
		},
		LoadJSON: func(v *exitDoc, text []byte) schemaread.Report {
			*v = zero
			var b swarmwire.ExitRecordBuilder
			if !b.Init() {
				return exitStrict(swarmwire.TableReport{}, false)
			}
			defer b.Shutdown()
			var r swarmwire.TableReport
			if !swarmwire.ExitRecordFromJson(&b, text, &r) || !b.Lock() {
				return exitStrict(r, false)
			}
			*v = exitOf(b.AsConst())
			return exitStrict(r, true)
		},
		Legacy: func(in []byte) bool { return len(in) > 0 && in[0] == '{' },
		LoadLegacy: func(v *exitDoc, in []byte) schemaread.Report {
			if err := json.Unmarshal(in, v); err != nil {
				return schemaread.Report{Malformed: true}
			}
			return schemaread.Report{}
		},
		Constraints: func(v *exitDoc, c *schemaread.Checker) {
			for _, f := range [][2]string{{"signal", v.Signal}, {"ended", v.Ended}, {"nonce", v.Nonce}, {"attest", v.Attest}, {"end", v.End}, {"reason", v.Reason}} {
				c.Text(f[0], f[1], math.MaxInt)
			}
		},
		SaveWire: func(v *exitDoc) []byte {
			root, done := exitBuild(*v)
			defer done()
			if root == nil {
				return nil
			}
			buf := make([]byte, swarmwire.ExitRecordMeasure(root))
			if swarmwire.ExitRecordSave(root, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
		SaveJSON: func(v *exitDoc) []byte {
			root, done := exitBuild(*v)
			defer done()
			if root == nil {
				return nil
			}
			buf := make([]byte, swarmwire.ExitRecordToJsonMeasure(root))
			if swarmwire.ExitRecordToJson(root, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
	}
	return schemaread.NewReader(c)
}

// ReadExitFile reads the job's exit record from path in any form and hands
// the refusal to the caller by name; a malformed value is never read as
// absent, empty or zero.
func ReadExitFile(path string) (ExitRecord, schemaread.Form, error) {
	raw, err := readFileSteady(path)
	if err != nil {
		return ExitRecord{}, schemaread.FormNone, err
	}
	d, res, err := exitReads.Read(raw)
	if err != nil {
		return ExitRecord{}, res.Form, err
	}
	return ExitRecord{RC: int(d.Rc), Signal: d.Signal, Ended: d.Ended, Survivors: int(d.Survivors), Nonce: d.Nonce, Attest: d.Attest, End: d.End, Spent: int(d.Spent), Observed: d.Observed, Partial: d.Partial, Reason: d.Reason}, res.Form, nil
}

// swarmBuilder is the generated builder of one variable table's root R.
type swarmBuilder[R, B any] interface {
	*B
	Init(...swarmwire.TableAllocator) bool
	Lock() bool
	AsConst() *R
	Shutdown()
}

// swarmTable is one variable table's generated functions: the shape every
// reader in this package hands pkg/schemaread (schema section 6, the strict
// read, and the owner's rule S2).
type swarmTable[R, B any, PB swarmBuilder[R, B]] struct {
	loadMeasure   func([]byte) int64
	load          func(region, wire []byte, report *swarmwire.TableReport) *R
	fromJSON      func(*B, []byte, *swarmwire.TableReport) bool
	measure       func(*R, ...swarmwire.TableWriteContext) int64
	save          func(*R, []byte, ...swarmwire.TableWriteContext) int64
	toJSONMeasure func(*R, ...swarmwire.TableAllocator) int64
	toJSON        func(*R, []byte, ...swarmwire.TableAllocator) int64
}

// loadWire loads the whole wire into a fresh region and hands its root to use
// while the region is alive.
func (t swarmTable[R, B, PB]) loadWire(wire []byte, use func(*R)) schemaread.Report {
	n := t.loadMeasure(wire)
	if n < 0 {
		return exitStrict(swarmwire.TableReport{Malformed: true}, false)
	}
	var r swarmwire.TableReport
	root := t.load(exitRegion(n), wire, &r)
	if root == nil {
		return exitStrict(r, false)
	}
	use(root)
	return exitStrict(r, true)
}

// loadJSON builds the schema JSON text into a builder and hands its root to
// use while the builder is alive.
func (t swarmTable[R, B, PB]) loadJSON(text []byte, use func(*R)) schemaread.Report {
	var b B
	pb := PB(&b)
	if !pb.Init() {
		return exitStrict(swarmwire.TableReport{}, false)
	}
	defer pb.Shutdown()
	var r swarmwire.TableReport
	if !t.fromJSON(&b, text, &r) || !pb.Lock() {
		return exitStrict(r, false)
	}
	use(pb.AsConst())
	return exitStrict(r, true)
}

// saveWire is the table wire of the schema JSON text, nil when it does not build.
func (t swarmTable[R, B, PB]) saveWire(text []byte) []byte {
	var out []byte
	t.loadJSON(text, func(root *R) {
		buf := make([]byte, t.measure(root))
		if t.save(root, buf) == int64(len(buf)) {
			out = buf
		}
	})
	return out
}

// saveJSON is the generated JSON text of the schema JSON text, nil when it does not build.
func (t swarmTable[R, B, PB]) saveJSON(text []byte) []byte {
	var out []byte
	t.loadJSON(text, func(root *R) {
		buf := make([]byte, t.toJSONMeasure(root))
		if t.toJSON(root, buf) == int64(len(buf)) {
			out = buf
		}
	})
	return out
}

// swarmCodec is one record's strict codec over its generated table: of reads
// a loaded root into the value, text writes the value as the schema JSON text
// the generated builder reads, and the legacy pair reads the form the record's
// writer writes today.
func swarmCodec[T, R, B any, PB swarmBuilder[R, B]](name, help string, t swarmTable[R, B, PB], of func(*R) T, text func(*T) []byte,
	legacy func([]byte) bool, loadLegacy func(*T, []byte) schemaread.Report, constraints func(*T, *schemaread.Checker)) *schemaread.Codec[T] {
	var zero T
	return &schemaread.Codec[T]{
		Name: name, Help: help,
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			*v = zero
			return t.loadWire(wire, func(root *R) { *v = of(root) })
		},
		LoadJSON: func(v *T, in []byte) schemaread.Report {
			*v = zero
			return t.loadJSON(in, func(root *R) { *v = of(root) })
		},
		Legacy:      legacy,
		LoadLegacy:  loadLegacy,
		Constraints: constraints,
		SaveWire:    func(v *T) []byte { return t.saveWire(text(v)) },
		SaveJSON:    func(v *T) []byte { return t.saveJSON(text(v)) },
	}
}

// swarmList is a table list of text as a Go slice; an empty list is nil, as
// the legacy JSON's omitted list is.
func swarmList(l *swarmwire.TableList[swarmwire.EntryRow]) []string {
	var out []string
	for i := int32(0); i < l.Count; i++ {
		out = append(out, exitText(&l.At(i).Value))
	}
	return out
}

// swarmEntries is a slice of text as the schema JSON text of a list of Entry.
func swarmEntries(v []string) []any {
	out := make([]any, 0, len(v))
	for _, s := range v {
		out = append(out, map[string]any{"value": s})
	}
	return out
}

// swarmJSON is the schema's name-keyed JSON text of a document: an empty text
// or list is absent, as a pointer field is (schemas/swarm/swarm.schema, Absence).
func swarmJSON(m map[string]any) []byte {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x == "" {
				delete(m, k)
			}
		case []any:
			if len(x) == 0 {
				delete(m, k)
			}
		}
	}
	b, _ := json.Marshal(m)
	return b
}

// jsonLegacy recognises the legacy JSON object every swarm record's writer
// writes today. The schema JSON text opens with `{` as well; a reader whose
// legacy keys and types are the schema's reads the schema JSON text as legacy
// into the same value.
func jsonLegacy(in []byte) bool {
	t := bytes.TrimLeft(in, " \t\r\n")
	return len(t) > 0 && t[0] == '{'
}

// The swarm records' readers, one per record, so the per-form read counters
// accumulate across reads (owner's rule S4: the counters are the cutover's
// evidence that every reader meets the new forms before a writer switches).
var (
	exitReads    = exitReader()
	modelReads   = declaredModelReader()
	workerReads  = workerReader()
	harnessReads = harnessConfigReader()
	fenceReads   = fenceConfigReader()
)

// SwarmReadCounts is every swarm record's read counters, by the record's
// refusal name: reads per form, unknown fields and refusals per reason.
func SwarmReadCounts() map[string]*schemaread.Counts {
	return map[string]*schemaread.Counts{
		"EXIT_RECORD":           exitReads.Counts(),
		"DECLARED_MODEL":        modelReads.Counts(),
		"WORKER_RECORD":         workerReads.Counts(),
		"HARNESS_CONFIG_RECORD": harnessReads.Counts(),
		"FENCE_CONFIG":          fenceReads.Counts(),
	}
}
