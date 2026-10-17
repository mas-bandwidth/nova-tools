package swarmwire

// TestSwarmRecordsGolden is the golden test of the swarm definitions
// (schemas/swarm): every table of the area (the exit record, the worker
// description, the harness config, the fence config, the declared model and
// the member carry) round-trips as the binary wire, as padded standard base64
// of that wire and as the JSON text form, each read strictly through
// pkg/schemaread (owner's rules S1 and S2); a value past each declared bound
// and a damaged image of each form are refused by name, never clamped.
//
// Every table is a VARIABLE table (schemas/swarm/swarm.schema, the area's
// bound rule): the code bounds none of the text these records hold, so text
// is held at its used size and lists are unbounded. The proof is that long
// text, long lists and integers across int64's range round-trip, that a value
// at each declared bound is accepted and one past it is refused by name in
// every form, and that damaged images of each form are refused by name.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unbounded is the text bound of a field no code bounds: the checker still
// refuses a NUL and invalid UTF-8 (pkg/schemaread Checker.Text), never a
// length.
const unbounded = math.MaxInt

// refusal checks the house style of a refusal and returns its reason.
func refusal(t *testing.T, name string, err error) string {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), strings.ToUpper(name)+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: nova-swarm -h"), err.Error())
	if r.Field != "" {
		return r.Reason + " (" + r.Field + ")"
	}
	return r.Reason
}

var b64 = base64.StdEncoding.EncodeToString

func mustWrite[T any](t *testing.T, r *schemaread.Reader[T], v *T, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(v, form)
	require.NoError(t, err)
	return out
}

func le64(v int64) []byte { return binary.LittleEndian.AppendUint64(nil, uint64(v)) }

// marker is an in-range integer whose little-endian bytes occur once in a
// small document's wire, so a test can move exactly that value past its
// bound: what a broken or older writer could put on the wire.
const marker int64 = 0x0123456789abcdef

// ---- the plain documents -------------------------------------------------------
//
// The generated variable Go is region based, so the test works on a plain Go
// value of each document: a plain value is written through the generated
// builder (by the schema JSON text, the generated reader) and read back out
// of the generated region.

type exitDoc struct {
	Rc                 int64
	Signal, Ended      string
	Survivors          int64
	Nonce, Attest, End string
	Spent              int64
	Observed, Partial  bool
	Reason             string
}

type workerDoc struct {
	Name, Provider, Model, BaseURL, EnvVar, KeyFile, Secret string
	Usage, WorkerClass                                      uint8
	Harness                                                 string
	HarnessArgs                                             []string
	WorkerDir, Deadline, Board                              string
	ReadRoots                                               []string
	MaxTurns                                                int64
	MaxCacheRead                                            int64
	InputLimitPhrases                                       []string
	LaunchGrace                                             string
	KeepData                                                bool
}

type harnessDoc struct{ Provider, Model, EnvVar, BaseURL string }

type ruleDoc struct {
	Pattern string
	Action  uint8
}

type fenceDoc struct {
	JobDir   string
	Reads    []string
	Rules    []ruleDoc
	Webfetch uint8
}

type modelDoc struct{ Provider, Model string }

type carryDoc struct {
	Card         string
	Attempt      int64
	Head, Branch string
}

func entries(v []string) []any {
	out := make([]any, 0, len(v))
	for _, s := range v {
		out = append(out, map[string]any{"value": s})
	}
	return out
}

// jsonOf is the schema's name-keyed text of a document; an empty text is
// absent, as a pointer field is.
func jsonOf(m map[string]any) []byte {
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
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(err)
	}
	return out
}

func (d exitDoc) json() []byte {
	return jsonOf(map[string]any{"rc": d.Rc, "signal": d.Signal, "ended": d.Ended, "survivors": d.Survivors, "nonce": d.Nonce,
		"attest": d.Attest, "end": d.End, "spent": d.Spent, "observed": d.Observed, "partial": d.Partial, "reason": d.Reason})
}

func (d workerDoc) json() []byte {
	return jsonOf(map[string]any{"name": d.Name, "provider": d.Provider, "model": d.Model, "base_url": d.BaseURL,
		"env_var": d.EnvVar, "key_file": d.KeyFile, "secret": d.Secret, "usage": d.Usage, "worker_class": d.WorkerClass,
		"harness": d.Harness, "harness_args": entries(d.HarnessArgs), "worker_dir": d.WorkerDir, "deadline": d.Deadline,
		"board": d.Board, "read_roots": entries(d.ReadRoots), "max_turns": d.MaxTurns, "max_cache_read": d.MaxCacheRead,
		"input_limit_phrases": entries(d.InputLimitPhrases), "launch_grace": d.LaunchGrace, "keep_data": d.KeepData})
}

func (d harnessDoc) json() []byte {
	return jsonOf(map[string]any{"provider": d.Provider, "model": d.Model, "env_var": d.EnvVar, "base_url": d.BaseURL})
}

func (d fenceDoc) json() []byte {
	rules := make([]any, 0, len(d.Rules))
	for _, r := range d.Rules {
		m := map[string]any{"action": r.Action}
		if r.Pattern != "" {
			m["pattern"] = r.Pattern
		}
		rules = append(rules, m)
	}
	return jsonOf(map[string]any{"job_dir": d.JobDir, "reads": entries(d.Reads), "rules": rules, "webfetch": d.Webfetch})
}

func (d modelDoc) json() []byte {
	return jsonOf(map[string]any{"provider": d.Provider, "model": d.Model})
}

func (d carryDoc) json() []byte {
	return jsonOf(map[string]any{"card": d.Card, "attempt": d.Attempt, "head": d.Head, "branch": d.Branch})
}

func text(ref *TableRef) string {
	v := TableStringAt(ref)
	return string(v.Data[:v.Length])
}

func list(l *TableList[EntryRow]) []string {
	var out []string
	for i := int32(0); i < l.Count; i++ {
		out = append(out, text(&l.At(i).Value))
	}
	return out
}

func exitOf(e *ExitRecord) exitDoc {
	return exitDoc{e.Rc, text(&e.Signal), text(&e.Ended), e.Survivors, text(&e.Nonce), text(&e.Attest), text(&e.End),
		e.Spent, e.Observed, e.Partial, text(&e.Reason)}
}

func workerOf(w *WorkerRecord) workerDoc {
	return workerDoc{text(&w.Name), text(&w.Provider), text(&w.Model), text(&w.BaseUrl), text(&w.EnvVar), text(&w.KeyFile), text(&w.Secret),
		w.Usage, w.WorkerClass, text(&w.Harness), list(&w.HarnessArgs), text(&w.WorkerDir), text(&w.Deadline), text(&w.Board),
		list(&w.ReadRoots), w.MaxTurns, w.MaxCacheRead, list(&w.InputLimitPhrases), text(&w.LaunchGrace), w.KeepData}
}

func harnessOf(h *HarnessConfigRecord) harnessDoc {
	return harnessDoc{text(&h.Provider), text(&h.Model), text(&h.EnvVar), text(&h.BaseUrl)}
}

func fenceOf(f *FenceConfig) fenceDoc {
	d := fenceDoc{JobDir: text(&f.JobDir), Reads: list(&f.Reads), Webfetch: f.Webfetch}
	for i := int32(0); i < f.Rules.Count; i++ {
		r := f.Rules.At(i)
		d.Rules = append(d.Rules, ruleDoc{text(&r.Pattern), r.Action})
	}
	return d
}

func modelOf(m *DeclaredModel) modelDoc { return modelDoc{text(&m.Provider), text(&m.Model)} }

func carryOf(c *MemberCarry) carryDoc {
	return carryDoc{text(&c.Card), c.Attempt, text(&c.Head), text(&c.Branch)}
}

// ---- the generated functions of one variable table ---------------------------

// builder is the generated builder of the root R.
type builder[R, B any] interface {
	*B
	Init(...TableAllocator) bool
	Lock() bool
	AsConst() *R
	Shutdown()
}

// gen is one variable table's generated functions.
type gen[R, B any] struct {
	fromJSON    func(*B, []byte, *TableReport) bool
	loadMeasure func([]byte) int64
	load        func(region, wire []byte, report *TableReport) *R
	measure     func(*R, ...TableWriteContext) int64
	save        func(*R, []byte, ...TableWriteContext) int64
	jsonMeasure func(*R, ...TableAllocator) int64
	toJSON      func(*R, []byte, ...TableAllocator) int64
	saveText    func(*R, ...TableWriteContext) (string, error)
	loadText    func(string, *TableReport) (*R, error)
}

// docTable is one variable table's strict codec on the plain value P, and the
// generated text transport it is checked against.
type docTable[P any] struct {
	name  string
	codec *schemaread.Codec[P]
	// saveText and loadText are the generated SaveText and LoadText, on P.
	saveText func(P) (string, error)
	loadText func(string) (P, error)
}

func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func strictReport(r TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed || !ok || r.Duplicate > 0}
	if r.Verdict == TableOpenRefused || r.Verdict == TableOpenDamaged {
		out.Reason = r.Err().Error()
	}
	return out
}

// docTableOf is the strict codec over a table's generated functions. The
// facade rules are UNION.md section 4.6: refuse every clamp, refuse absence
// where empty is no value, and validate before writing. check states the
// bounds the code enforces, and only those (the area's bound rule).
func docTableOf[P, R, B any, PB builder[R, B]](name string, g gen[R, B], toJSON func(P) []byte, of func(*R) P, check func(P, *schemaread.Checker)) docTable[P] {
	build := func(d P) (*R, func()) {
		var b B
		pb := PB(&b)
		if !pb.Init() {
			return nil, func() {}
		}
		var r TableReport
		if !g.fromJSON(&b, toJSON(d), &r) || !pb.Lock() {
			return nil, pb.Shutdown
		}
		return pb.AsConst(), pb.Shutdown
	}
	var zero P
	return docTable[P]{
		name: name,
		codec: &schemaread.Codec[P]{
			Name: strings.ToUpper(name), Help: "nova-swarm -h",
			LoadWire: func(v *P, wire []byte) schemaread.Report {
				*v = zero
				n := g.loadMeasure(wire)
				if n < 0 {
					return strictReport(TableReport{Malformed: true}, false)
				}
				var r TableReport
				root := g.load(region(n), wire, &r)
				if root == nil {
					return strictReport(r, false)
				}
				*v = of(root)
				return strictReport(r, true)
			},
			LoadJSON: func(v *P, text []byte) schemaread.Report {
				*v = zero
				var b B
				pb := PB(&b)
				if !pb.Init() {
					return strictReport(TableReport{}, false)
				}
				defer pb.Shutdown()
				var r TableReport
				if !g.fromJSON(&b, text, &r) || !pb.Lock() {
					return strictReport(r, false)
				}
				*v = of(pb.AsConst())
				return strictReport(r, true)
			},
			Constraints: func(v *P, c *schemaread.Checker) { check(*v, c) },
			SaveWire: func(v *P) []byte {
				root, done := build(*v)
				defer done()
				if root == nil {
					return nil
				}
				buf := make([]byte, g.measure(root))
				if g.save(root, buf) != int64(len(buf)) {
					return nil
				}
				return buf
			},
			SaveJSON: func(v *P) []byte {
				root, done := build(*v)
				defer done()
				if root == nil {
					return nil
				}
				buf := make([]byte, g.jsonMeasure(root))
				if g.toJSON(root, buf) != int64(len(buf)) {
					return nil
				}
				return buf
			},
		},
		saveText: func(d P) (string, error) {
			root, done := build(d)
			defer done()
			if root == nil {
				return "", errors.New("the document does not build")
			}
			return g.saveText(root)
		},
		loadText: func(text string) (P, error) {
			root, err := g.loadText(text, nil)
			if err != nil {
				return zero, err
			}
			return of(root), nil
		},
	}
}

// ---- the six tables, each with the bounds its code enforces ------------------

// exitTable: swarm.ReadJSON reads exit.json with no bound and no required
// field (pkg/swarm/slot.go ReadJSON), so the check bounds nothing but the
// text's encoding.
func exitTable() docTable[exitDoc] {
	return docTableOf[exitDoc, ExitRecord, ExitRecordBuilder]("ExitRecord",
		gen[ExitRecord, ExitRecordBuilder]{ExitRecordFromJson, ExitRecordLoadMeasure, ExitRecordLoad, ExitRecordMeasure, ExitRecordSave,
			ExitRecordToJsonMeasure, ExitRecordToJson, ExitRecordSaveText, ExitRecordLoadText},
		exitDoc.json, exitOf,
		func(d exitDoc, c *schemaread.Checker) {
			for _, f := range [][2]string{{"signal", d.Signal}, {"ended", d.Ended}, {"nonce", d.Nonce}, {"attest", d.Attest}, {"end", d.End}, {"reason", d.Reason}} {
				c.Text(f[0], f[1], unbounded)
			}
		})
}

// workerTable: LoadWorker's own checks (pkg/swarm/worker.go LoadWorker): the
// required fields, usage and class from their closed sets, harness_args
// non-empty, and the budgets non-negative.
func workerTable() docTable[workerDoc] {
	return docTableOf[workerDoc, WorkerRecord, WorkerRecordBuilder]("WorkerRecord",
		gen[WorkerRecord, WorkerRecordBuilder]{WorkerRecordFromJson, WorkerRecordLoadMeasure, WorkerRecordLoad, WorkerRecordMeasure, WorkerRecordSave,
			WorkerRecordToJsonMeasure, WorkerRecordToJson, WorkerRecordSaveText, WorkerRecordLoadText},
		workerDoc.json, workerOf,
		func(d workerDoc, c *schemaread.Checker) {
			for _, f := range []struct {
				name, v  string
				required bool
			}{{"name", d.Name, true}, {"provider", d.Provider, true}, {"model", d.Model, true}, {"base_url", d.BaseURL, false},
				{"env_var", d.EnvVar, true}, {"key_file", d.KeyFile, false}, {"secret", d.Secret, false}, {"harness", d.Harness, true},
				{"worker_dir", d.WorkerDir, true}, {"deadline", d.Deadline, true}, {"board", d.Board, false}, {"launch_grace", d.LaunchGrace, false}} {
				c.Required(f.name, !f.required || f.v != "")
				c.Text(f.name, f.v, unbounded)
			}
			c.Required("usage", d.Usage != 0)
			c.Bound("usage", int64(d.Usage), 0, 2)
			c.Bound("worker_class", int64(d.WorkerClass), 0, 2)
			c.Required("harness_args", len(d.HarnessArgs) > 0)
			for _, l := range [][]string{d.HarnessArgs, d.ReadRoots, d.InputLimitPhrases} {
				for _, s := range l {
					c.Required("entry", s != "")
					c.Text("entry", s, unbounded)
				}
			}
			c.Bound("max_turns", d.MaxTurns, 0, math.MaxInt64)
			c.Bound("max_cache_read", d.MaxCacheRead, 0, math.MaxInt64)
		})
}

// harnessTable: the three fields LoadWorker requires (want) before
// HarnessConfig writes them; base_url is optional.
func harnessTable() docTable[harnessDoc] {
	return docTableOf[harnessDoc, HarnessConfigRecord, HarnessConfigRecordBuilder]("HarnessConfigRecord",
		gen[HarnessConfigRecord, HarnessConfigRecordBuilder]{HarnessConfigRecordFromJson, HarnessConfigRecordLoadMeasure, HarnessConfigRecordLoad,
			HarnessConfigRecordMeasure, HarnessConfigRecordSave, HarnessConfigRecordToJsonMeasure, HarnessConfigRecordToJson,
			HarnessConfigRecordSaveText, HarnessConfigRecordLoadText},
		harnessDoc.json, harnessOf,
		func(d harnessDoc, c *schemaread.Checker) {
			for _, f := range []struct {
				name, v  string
				required bool
			}{{"provider", d.Provider, true}, {"model", d.Model, true}, {"env_var", d.EnvVar, true}, {"base_url", d.BaseURL, false}} {
				c.Required(f.name, !f.required || f.v != "")
				c.Text(f.name, f.v, unbounded)
			}
		})
}

// fenceTable: FencePermission writes allow or deny and denies webfetch
// (pkg/swarm/fence.go FencePermission).
func fenceTable() docTable[fenceDoc] {
	return docTableOf[fenceDoc, FenceConfig, FenceConfigBuilder]("FenceConfig",
		gen[FenceConfig, FenceConfigBuilder]{FenceConfigFromJson, FenceConfigLoadMeasure, FenceConfigLoad, FenceConfigMeasure, FenceConfigSave,
			FenceConfigToJsonMeasure, FenceConfigToJson, FenceConfigSaveText, FenceConfigLoadText},
		fenceDoc.json, fenceOf,
		func(d fenceDoc, c *schemaread.Checker) {
			c.Required("job_dir", d.JobDir != "")
			c.Text("job_dir", d.JobDir, unbounded)
			for _, s := range d.Reads {
				c.Required("reads", s != "")
				c.Text("reads", s, unbounded)
			}
			for _, r := range d.Rules {
				c.Required("rules", r.Pattern != "" && r.Action != 0)
				c.Text("rules", r.Pattern, unbounded)
				c.Bound("rules", int64(r.Action), 0, 2)
			}
			c.Required("webfetch", d.Webfetch != 0)
			c.Bound("webfetch", int64(d.Webfetch), 0, 2)
		})
}

// modelTable: DeclareRouteModel declares nothing for an empty provider or
// model (pkg/swarm/declaremodel.go DeclareRouteModel), and bounds no length.
func modelTable() docTable[modelDoc] {
	return docTableOf[modelDoc, DeclaredModel, DeclaredModelBuilder]("DeclaredModel",
		gen[DeclaredModel, DeclaredModelBuilder]{DeclaredModelFromJson, DeclaredModelLoadMeasure, DeclaredModelLoad, DeclaredModelMeasure, DeclaredModelSave,
			DeclaredModelToJsonMeasure, DeclaredModelToJson, DeclaredModelSaveText, DeclaredModelLoadText},
		modelDoc.json, modelOf,
		func(d modelDoc, c *schemaread.Checker) {
			c.Required("provider", d.Provider != "")
			c.Text("provider", d.Provider, unbounded)
			c.Required("model", d.Model != "")
			c.Text("model", d.Model, unbounded)
		})
}

// carryTable: parseCarry's own checks (pkg/member/member.go parseCarry): a
// head is required, an attempt is never under 1 when written (0 is absent),
// and an attempt needs a card.
func carryTable() docTable[carryDoc] {
	return docTableOf[carryDoc, MemberCarry, MemberCarryBuilder]("MemberCarry",
		gen[MemberCarry, MemberCarryBuilder]{MemberCarryFromJson, MemberCarryLoadMeasure, MemberCarryLoad, MemberCarryMeasure, MemberCarrySave,
			MemberCarryToJsonMeasure, MemberCarryToJson, MemberCarrySaveText, MemberCarryLoadText},
		carryDoc.json, carryOf,
		func(d carryDoc, c *schemaread.Checker) {
			c.Text("card", d.Card, unbounded)
			c.Bound("attempt", d.Attempt, 0, math.MaxInt64)
			c.Required("card", d.Attempt == 0 || d.Card != "")
			c.Required("head", d.Head != "")
			c.Text("head", d.Head, unbounded)
			c.Text("branch", d.Branch, unbounded)
		})
}

// ---- the proof of one table ----------------------------------------------------

// probe is one bound: inside is a document at the bound and is accepted.
// The binary wire moves the bound's value one past it: with step (the same
// document a step inside the bound) the one byte that differs between the
// two is moved on past the bound; otherwise from (the value's little-endian
// bytes, occurring once) is replaced by to. jsonFrom and jsonTo do the same
// in the JSON text form. Each is refused by name, as binary, base64 and JSON.
type probe[P any] struct {
	inside, step P
	stepped      bool
	from, to     []byte
	jsonFrom     string
	jsonTo       string
}

// runDoc is the whole proof of one variable table over its documents.
func runDoc[P any](t *testing.T, tb docTable[P], docs map[string]P, over map[string]P, probes map[string]probe[P]) {
	t.Helper()
	r := schemaread.NewReader(tb.codec)
	n := 0
	for name, d := range docs {
		wire, err := r.Write(&d, schemaread.FormBinary)
		require.NoError(t, err, "%s %s writes", tb.name, name)
		text64, err := r.Write(&d, schemaread.FormBase64)
		require.NoError(t, err)
		assert.Equal(t, b64(wire), string(text64), "%s %s: the text is the padded standard base64 of the whole wire", tb.name, name)
		js, err := r.Write(&d, schemaread.FormJSON)
		require.NoError(t, err)
		for _, in := range []struct {
			form schemaread.Form
			data []byte
		}{{schemaread.FormBinary, wire}, {schemaread.FormBase64, text64}, {schemaread.FormJSON, js}} {
			got, res, err := r.Read(in.data)
			require.NoError(t, err, "%s %s as %s", tb.name, name, in.form)
			assert.Equal(t, in.form, res.Form)
			assert.Equal(t, d, got, "%s %s as %s reads the document", tb.name, name, in.form)
		}
		// the generated SaveText and LoadText are the same text transport
		gen, err := tb.saveText(d)
		require.NoError(t, err)
		assert.Equal(t, b64(wire), gen, "%s %s: SaveText is the padded standard base64 of the wire", tb.name, name)
		back, err := tb.loadText(gen)
		require.NoError(t, err)
		assert.Equal(t, d, back, "%s %s: LoadText reads what SaveText wrote", tb.name, name)
		n++
	}
	c := r.Counts()
	assert.Equal(t, uint64(n), c.Reads(schemaread.FormBinary), "%s: each read is counted once, on its form", tb.name)
	assert.Equal(t, uint64(n), c.Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(n), c.Reads(schemaread.FormJSON))

	// a value past its bound (or an empty required field) is refused by name before a byte is written
	for name, d := range over {
		out, err := r.Write(&d, schemaread.FormBinary)
		assert.Nil(t, out, "%s %s writes nothing", tb.name, name)
		assert.Contains(t, []string{"clamped", "absent_required", "malformed"}, strings.Fields(refusal(t, tb.name, err))[0], "%s %s", tb.name, name)
	}

	// a document at each bound is accepted; one past it, as a writer could put
	// it on the wire or in the text, is refused by name in every form, never
	// clamped
	for name, p := range probes {
		atBound, err := r.Write(&p.inside, schemaread.FormBinary)
		require.NoError(t, err, "%s %s: at the bound writes", tb.name, name)
		_, _, err = r.Read(atBound)
		require.NoError(t, err, "%s %s: at the bound reads", tb.name, name)
		var bad []byte
		if p.stepped {
			inside, err := r.Write(&p.step, schemaread.FormBinary)
			require.NoError(t, err)
			require.Equal(t, len(inside), len(atBound))
			at := -1
			for i := range inside {
				if inside[i] != atBound[i] {
					require.Equal(t, -1, at, "%s %s differ at one byte", tb.name, name)
					at = i
				}
			}
			require.GreaterOrEqual(t, at, 0)
			bad = append([]byte{}, atBound...)
			bad[at]++
		} else {
			require.Equal(t, 1, bytes.Count(atBound, p.from), "%s %s: the bound's bytes occur once", tb.name, name)
			bad = bytes.Replace(atBound, p.from, p.to, 1)
		}
		js := string(mustWrite(t, r, &p.inside, schemaread.FormJSON))
		require.Equal(t, 1, strings.Count(js, p.jsonFrom), "%s %s: %q occurs once in %s", tb.name, name, p.jsonFrom, js)
		badJSON := strings.Replace(js, p.jsonFrom, p.jsonTo, 1)
		for form, in := range map[string][]byte{"binary": bad, "base64": []byte(b64(bad)), "json": []byte(badJSON)} {
			got, _, err := r.Read(in)
			assert.Equal(t, "clamped", refusal(t, tb.name, err), "%s %s as %s", tb.name, name, form)
			var zero P
			assert.Equal(t, zero, got, "%s %s as %s: a refused read returns nothing, never a partial value", tb.name, name, form)
		}
	}

	// damaged images, by name; the typical document, never the long one: the
	// mutation pass reads every byte of the image
	d, ok := docs["typical"]
	require.True(t, ok, "%s: the documents name a typical one", tb.name)
	wire, err := r.Write(&d, schemaread.FormBinary)
	require.NoError(t, err)
	newer := append([]byte{}, wire...)
	newer[0] = 9
	text := b64(wire)
	js := mustWrite(t, r, &d, schemaread.FormJSON)
	for _, tc := range []struct {
		name   string
		in     []byte
		reason string
	}{
		{"an unknown newer form", newer, "unknown_form"},
		{"base64 that does not decode", []byte(text[:len(text)-3]), "malformed"},
		{"base64 with trailing junk", []byte(text + " junk"), "malformed"},
		{"damaged JSON text", []byte(strings.Replace(string(js), `": `, `" `, 1)), "malformed"},
		{"empty", nil, "unknown_form"},
		{"no record at all", []byte("hello swarm"), "malformed"},
	} {
		_, _, err := r.Read(tc.in)
		assert.Equal(t, tc.reason, refusal(t, tb.name, err), "%s: %s", tb.name, tc.name)
	}
	for i := range len(wire) {
		_, _, err := r.Read(wire[:i])
		assert.Error(t, err, "%s cut at byte %d", tb.name, i)
	}
	mutants := 0
	for _, in := range [][]byte{wire, []byte(text)} {
		for i := range in {
			for _, b := range []byte{0x00, in[i] ^ 0x01, in[i] ^ 0x80} {
				m := append([]byte{}, in...)
				m[i] = b
				mutants++
				got, _, err := r.Read(m)
				if err != nil {
					var ref *schemaread.Refusal
					require.True(t, errors.As(err, &ref), "%s mutant %d: %v", tb.name, mutants, err)
					continue
				}
				require.NoError(t, r.Check(&got), "%s: a mutant that reads passes every constraint", tb.name)
			}
		}
	}
	assert.Greater(t, mutants, 100)
}

func many(n int, f func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

// topProbe is a uint8 closed set at its top (2, inside) moved to 3, with
// step the same document at 1; underZero is a non-negative int64 at the
// marker moved to -1. Both move the value in the wire and in the JSON text.
func topProbe[P any](inside, step P, field string) probe[P] {
	return probe[P]{inside: inside, step: step, stepped: true, jsonFrom: fmt.Sprintf(`"%s": 2`, field), jsonTo: fmt.Sprintf(`"%s": 3`, field)}
}

func underZero[P any](inside P, field string) probe[P] {
	return probe[P]{inside: inside, from: le64(marker), to: le64(-1), jsonFrom: fmt.Sprintf(`"%s": %d`, field, marker), jsonTo: fmt.Sprintf(`"%s": -1`, field)}
}

const (
	kib  = 1024
	sha1 = "0123456789abcdef0123456789abcdef01234567"
)

func TestSwarmRecordsGolden(t *testing.T) {
	t.Parallel()

	t.Run("the exit record", func(t *testing.T) {
		t.Parallel()
		typical := exitDoc{Rc: 2, Ended: "2026-10-17T04:00:00Z", Survivors: 1, Nonce: "0123456789ab", Reason: "deadline"}
		// no text or integer is bounded: int64's whole range and long text round-trip
		long := exitDoc{Rc: math.MinInt64, Signal: strings.Repeat("S", 10*kib), Ended: strings.Repeat("e", 10*kib), Survivors: math.MaxInt64,
			Nonce: strings.Repeat("n", 10*kib), Attest: strings.Repeat("a", 10*kib), End: strings.Repeat("d", 10*kib), Spent: math.MaxInt64,
			Observed: true, Partial: true, Reason: strings.Repeat("r", 100*kib)}
		high := long
		high.Rc, high.Survivors, high.Spent = math.MaxInt64, math.MinInt64, math.MinInt64
		runDoc(t, exitTable(),
			map[string]exitDoc{"typical": typical, "long": long, "int64 top and bottom": high, "empty": {}},
			map[string]exitDoc{"a NUL in a reason": {Reason: "a\x00b"}, "invalid UTF-8 in a signal": {Signal: "\xff"}},
			nil)
	})

	t.Run("the worker description", func(t *testing.T) {
		t.Parallel()
		typical := workerDoc{Name: "deepseek-1", Provider: "deepseek", Model: "deepseek-chat", EnvVar: "DEEPSEEK_API_KEY", Secret: "DEEPSEEK_API_KEY",
			Usage: 1, Harness: "opencode", HarnessArgs: []string{"run", "--model", "{model}", "--", "{prompt}"}, WorkerDir: "/home/nova/worker",
			Deadline: "20m", ReadRoots: []string{"/home/nova/go"}, MaxTurns: 80}
		// no list or text is bounded: 200 entries, entries of 5000 bytes, a
		// 70000-byte path, a 10 KB provider and model, and the budgets at
		// int64's top
		long := typical
		long.Provider, long.Model = strings.Repeat("p", 10*kib), strings.Repeat("m", 10*kib)
		long.HarnessArgs = many(200, func(i int) string { return fmt.Sprintf("--arg-%d", i) })
		long.ReadRoots = many(200, func(i int) string { return fmt.Sprintf("/read/root/%d/%s", i, strings.Repeat("d", 5000)) })
		long.InputLimitPhrases = many(200, func(i int) string { return fmt.Sprintf("phrase %d", i) })
		long.WorkerDir = "/" + strings.Repeat("w", 70000)
		long.MaxTurns, long.MaxCacheRead, long.Usage, long.WorkerClass, long.KeepData = math.MaxInt64, math.MaxInt64, 2, 2, true
		w := func(edit func(*workerDoc)) workerDoc { d := typical; edit(&d); return d }
		runDoc(t, workerTable(),
			map[string]workerDoc{"typical": typical, "long": long},
			map[string]workerDoc{
				"usage 3":           w(func(d *workerDoc) { d.Usage = 3 }),
				"class 3":           w(func(d *workerDoc) { d.WorkerClass = 3 }),
				"max_turns -1":      w(func(d *workerDoc) { d.MaxTurns = -1 }),
				"max_cache_read -1": w(func(d *workerDoc) { d.MaxCacheRead = -1 }),
				"no name":           w(func(d *workerDoc) { d.Name = "" }),
				"no usage":          w(func(d *workerDoc) { d.Usage = 0 }),
				"no harness args":   w(func(d *workerDoc) { d.HarnessArgs = nil }),
				"a NUL in a model":  w(func(d *workerDoc) { d.Model = "a\x00b" }),
				"an empty arg":      w(func(d *workerDoc) { d.HarnessArgs = []string{""} }),
			},
			map[string]probe[workerDoc]{
				"usage over 2":           topProbe(w(func(d *workerDoc) { d.Usage = 2 }), w(func(d *workerDoc) { d.Usage = 1 }), "usage"),
				"worker_class over 2":    topProbe(w(func(d *workerDoc) { d.WorkerClass = 2 }), w(func(d *workerDoc) { d.WorkerClass = 1 }), "worker_class"),
				"max_turns under 0":      underZero(w(func(d *workerDoc) { d.MaxTurns = marker }), "max_turns"),
				"max_cache_read under 0": underZero(w(func(d *workerDoc) { d.MaxCacheRead = marker }), "max_cache_read"),
			})
	})

	// int64's top is accepted and a JSON number past it is refused by name,
	// never wrapped or clamped to the top (the wire cannot carry one past it)
	t.Run("past int64 in the JSON text", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(carryTable().codec)
		at := carryDoc{Card: "c", Attempt: math.MaxInt64, Head: sha1}
		js := string(mustWrite(t, r, &at, schemaread.FormJSON))
		got, _, err := r.Read([]byte(js))
		require.NoError(t, err)
		assert.Equal(t, at, got)
		for _, past := range []string{"9223372036854775808", "18446744073709551616"} {
			bad := strings.Replace(js, `"attempt": 9223372036854775807`, `"attempt": `+past, 1)
			require.NotEqual(t, js, bad)
			_, _, err := r.Read([]byte(bad))
			assert.Equal(t, "clamped", refusal(t, "MemberCarry", err), "attempt %s", past)
		}
	})

	t.Run("the harness config", func(t *testing.T) {
		t.Parallel()
		typical := harnessDoc{Provider: "deepseek", Model: "deepseek-chat", EnvVar: "DEEPSEEK_API_KEY"}
		long := harnessDoc{Provider: strings.Repeat("p", 10*kib), Model: strings.Repeat("m", 10*kib), EnvVar: strings.Repeat("E", 10*kib),
			BaseURL: "https://" + strings.Repeat("u", 10*kib)}
		h := func(edit func(*harnessDoc)) harnessDoc { d := typical; edit(&d); return d }
		runDoc(t, harnessTable(),
			map[string]harnessDoc{"typical": typical, "long": long},
			map[string]harnessDoc{
				"no provider":      h(func(d *harnessDoc) { d.Provider = "" }),
				"no model":         h(func(d *harnessDoc) { d.Model = "" }),
				"no env_var":       h(func(d *harnessDoc) { d.EnvVar = "" }),
				"a NUL in a model": h(func(d *harnessDoc) { d.Model = "a\x00" }),
			},
			nil)
	})

	t.Run("the fence config", func(t *testing.T) {
		t.Parallel()
		typical := fenceDoc{JobDir: "/home/nova/jobs/a", Reads: []string{"/sys/kernel/security/lsm"},
			Rules:    []ruleDoc{{"/home/nova/jobs/a/*", 1}, {"/home/nova/jobs/a/**", 1}},
			Webfetch: 2}
		long := typical
		long.JobDir = "/" + strings.Repeat("j", 10*kib)
		long.Reads = many(200, func(i int) string { return fmt.Sprintf("/read/%d/%s", i, strings.Repeat("r", 3000)) })
		long.Rules = nil
		for i := range 2 + 4*200 {
			long.Rules = append(long.Rules, ruleDoc{fmt.Sprintf("/rule/%d/%s", i, strings.Repeat("p", 2000)), uint8(1 + i%2)})
		}
		f := func(edit func(*fenceDoc)) fenceDoc {
			d := typical
			d.Rules = append([]ruleDoc{}, typical.Rules...)
			edit(&d)
			return d
		}
		runDoc(t, fenceTable(),
			map[string]fenceDoc{"typical": typical, "long": long},
			map[string]fenceDoc{
				"webfetch 3":  f(func(d *fenceDoc) { d.Webfetch = 3 }),
				"action 3":    f(func(d *fenceDoc) { d.Rules[0].Action = 3 }),
				"no job dir":  f(func(d *fenceDoc) { d.JobDir = "" }),
				"no webfetch": f(func(d *fenceDoc) { d.Webfetch = 0 }),
				"a NUL path":  f(func(d *fenceDoc) { d.Reads = []string{"/a\x00"} }),
			},
			map[string]probe[fenceDoc]{
				"webfetch over 2": topProbe(f(func(d *fenceDoc) { d.Webfetch = 2 }), f(func(d *fenceDoc) { d.Webfetch = 1 }), "webfetch"),
				"action over 2": topProbe(fenceDoc{JobDir: "/j", Rules: []ruleDoc{{"/j/*", 2}}, Webfetch: 1},
					fenceDoc{JobDir: "/j", Rules: []ruleDoc{{"/j/*", 1}}, Webfetch: 1}, "action"),
			})
	})

	t.Run("the declared model", func(t *testing.T) {
		t.Parallel()
		typical := modelDoc{Provider: "deepseek", Model: "deepseek-chat"}
		// DeclareRouteModel accepts a provider and a model of any length
		long := modelDoc{Provider: strings.Repeat("p", 10*kib), Model: strings.Repeat("m", 20*kib)}
		runDoc(t, modelTable(),
			map[string]modelDoc{"typical": typical, "long": long},
			map[string]modelDoc{"no provider": {Model: "m"}, "no model": {Provider: "p"}, "a NUL in a provider": {Provider: "p\x00", Model: "m"}},
			nil)
	})

	t.Run("the member carry", func(t *testing.T) {
		t.Parallel()
		typical := carryDoc{Card: "recut-widen-r.w1", Attempt: 2, Head: sha1}
		// CarryLine writes a card and a branch of any length, and parseCarry
		// reads an attempt across int's range
		long := carryDoc{Card: strings.Repeat("c", 10*kib), Attempt: math.MaxInt64, Head: sha1 + sha1[:24], Branch: "sprint/" + strings.Repeat("b", 5*kib)}
		pr := carryDoc{Head: sha1, Branch: "sprint/a13-defs-swarm.w4.g1.e21"}
		runDoc(t, carryTable(),
			map[string]carryDoc{"typical": typical, "long": long, "a finish-PR carry": pr},
			map[string]carryDoc{
				"attempt -1":          {Card: "c", Attempt: -1, Head: sha1},
				"no head":             {Card: "c", Attempt: 1},
				"an attempt, no card": {Attempt: 1, Head: sha1},
				"a NUL in a branch":   {Head: sha1, Branch: "b\x00"},
			},
			map[string]probe[carryDoc]{
				"attempt under 0": underZero(carryDoc{Card: "c", Attempt: marker, Head: sha1}, "attempt"),
			})
	})
}
