package swarm

import (
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
func ReadJSON(path string, v any) error {
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
	d, res, err := exitReader().Read(raw)
	if err != nil {
		return ExitRecord{}, res.Form, err
	}
	return ExitRecord{RC: int(d.Rc), Signal: d.Signal, Ended: d.Ended, Survivors: int(d.Survivors), Nonce: d.Nonce, Attest: d.Attest, End: d.End, Spent: int(d.Spent), Observed: d.Observed, Partial: d.Partial, Reason: d.Reason}, res.Form, nil
}
