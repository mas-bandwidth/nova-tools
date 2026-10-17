package dogfood_test

// TestDogfoodReceiptsDualRead is the reader half of the dogfood receipts'
// table adoption (v1.5 aspect 13, part 2; the owner's S2 and S4, readers
// first): the receipt lines ReadReceipts reads read alike from the table wire,
// the padded standard base64 of that wire and the legacy JSON form the writer
// writes today (Record); a damaged value of each form is refused by name
// (`DOGFOOD_RECEIPT REFUSED: <reason>; run: <help>`) and never read as
// absent, empty or zero; a clamped value is refused; and each read is counted
// under its form.
//
// Unit tier: no clock, no socket, no store; the files are written into a
// t.TempDir(). The generated DogfoodReceipt table (pkg/ledgerwire) is wired
// into the package's slot here, as an installer wires it for a binary.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/record/tablejson"
	"github.com/mas-bandwidth/nova-tools/pkg/dogfood"
	"github.com/mas-bandwidth/nova-tools/pkg/ledgerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDogfoodReceiptsDualRead(t *testing.T) {
	t.Parallel()
	wire := receiptWire()
	load := receiptLoadOf(wire)
	dogfood.ReceiptWire.Store(&load)

	want := dogfood.Receipt{Tool: "nova-tools", Verb: "test", By: "fixture",
		At: "2026-10-17T12:00:00Z", OK: true, Notes: "fixture", Issue: 12, Closes: "deadbeef"}
	legacy, err := json.Marshal(want)
	require.NoError(t, err)

	bin := receiptBinary(t, want)
	b64 := []byte(base64.StdEncoding.EncodeToString(bin))

	dir := t.TempDir()
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"legacy.json", legacy}, {"bin.json", bin}, {"b64.json", b64},
	} {
		line := make([]byte, 0, len(f.data)+1)
		line = append(line, f.data...)
		line = append(line, '\n')
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.name), line, 0o600))
	}

	receipts, failures, err := dogfood.ReadReceipts(dir)
	require.NoError(t, err)
	assert.Empty(t, failures, "every form reads")
	require.Len(t, receipts, 3)
	for _, got := range receipts {
		got.File = ""
		assert.Equal(t, want, got, "each form reads as the legacy receipt reads")
	}

	// Each read is counted once, under the form it arrived in, by the same
	// reader shape ReadReceipts uses.
	counts := schemaread.NewReader(&schemaread.Codec[dogfood.Receipt]{
		Name:  "DOGFOOD_RECEIPT",
		Help:  dogfood.ReceiptHelp,
		Forms: []byte{1, 3},
		Reset: func(r *dogfood.Receipt) { *r = dogfood.Receipt{} },
		LoadWire: func(v *dogfood.Receipt, in []byte) schemaread.Report {
			load := dogfood.ReceiptWire.Load()
			out, rep := (*load)(in)
			*v = out
			return rep
		},
		Legacy:      receiptIsLegacy,
		LoadLegacy:  receiptLoadLegacy,
		Constraints: receiptConstraints,
	})
	for _, in := range [][]byte{bin, b64, legacy} {
		_, _, err := counts.Read(in)
		require.NoError(t, err)
	}
	assert.Equal(t, uint64(1), counts.Counts().Reads(schemaread.FormBinary), "binary counted")
	assert.Equal(t, uint64(1), counts.Counts().Reads(schemaread.FormBase64), "base64 counted")
	assert.Equal(t, uint64(1), counts.Counts().Reads(schemaread.FormLegacy), "legacy counted")
	assert.Zero(t, counts.Counts().Reads(schemaread.FormJSON), "no schema JSON reads")

	// A damaged value of each form is refused by name, never read as a receipt.
	for n := 1; n < len(bin); n++ {
		fails := readOne(t, bin[:n])
		require.Len(t, fails, 1, "truncated binary cut at %d", n)
		assert.Contains(t, fails[0].Reason, "DOGFOOD_RECEIPT REFUSED:", "cut at %d", n)
	}
	for at := range bin {
		mut := append([]byte(nil), bin...)
		mut[at] ^= 0x01
		recs, fails := readBoth(t, mut)
		assert.Equal(t, 1, len(recs)+len(fails), "flip at %d is read or refused by name", at)
		for _, f := range fails {
			assert.Contains(t, f.Reason, "DOGFOOD_RECEIPT REFUSED:", "flip at %d", at)
		}
	}
	fails := readOne(t, []byte("not a receipt at all"))
	require.Len(t, fails, 1)
	assert.Contains(t, fails[0].Reason, "DOGFOOD_RECEIPT REFUSED:")

	// A clamped value is refused by name.
	clamped := []byte(`{"tool":"nova-tools","verb":"test","by":"fixture","at":"2026-10-17T12:00:00Z","ok":true,"notes":"fixture","issue":-1}`)
	fails = readOne(t, clamped)
	require.Len(t, fails, 1)
	assert.Contains(t, fails[0].Reason, "DOGFOOD_RECEIPT REFUSED:")
	assert.Contains(t, fails[0].Reason, "clamped")
}

func readOne(t *testing.T, line []byte) []dogfood.Failure {
	t.Helper()
	recs, fails := readBoth(t, line)
	require.Len(t, recs, 0)
	return fails
}

func readBoth(t *testing.T, line []byte) ([]dogfood.Receipt, []dogfood.Failure) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one.json"), append(append([]byte(nil), line...), '\n'), 0o600))
	recs, failures, err := dogfood.ReadReceipts(dir)
	require.NoError(t, err)
	return recs, failures
}

func receiptBinary(t *testing.T, w dogfood.Receipt) []byte {
	t.Helper()
	text, err := json.Marshal(receiptText{Tool: w.Tool, Verb: w.Verb, By: w.By, At: w.At,
		OK: w.OK, Notes: w.Notes, Issue: int64(w.Issue), Closes: w.Closes})
	require.NoError(t, err)
	wire, err := receiptWire().JSONWire(text)
	require.NoError(t, err)
	return wire
}

func receiptWire() tablejson.Wire {
	return wireOf(ledgerwire.DogfoodReceiptLoadMeasureReason, ledgerwire.DogfoodReceiptLoadBuilder,
		ledgerwire.DogfoodReceiptFromJson, ledgerwire.DogfoodReceiptMeasure, ledgerwire.DogfoodReceiptSave,
		ledgerwire.DogfoodReceiptToJsonMeasure, ledgerwire.DogfoodReceiptToJson)
}

type receiptText struct {
	Tool   string `json:"tool"`
	Verb   string `json:"verb"`
	By     string `json:"by"`
	At     string `json:"at"`
	OK     bool   `json:"ok"`
	Notes  string `json:"notes"`
	Issue  int64  `json:"issue,omitempty"`
	Closes string `json:"closes,omitempty"`
}

func receiptIsLegacy(in []byte) bool {
	t := strings.TrimSpace(string(in))
	return len(t) > 0 && t[0] == '{'
}

func receiptLoadLegacy(v *dogfood.Receipt, in []byte) schemaread.Report {
	d := json.NewDecoder(strings.NewReader(string(in)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return schemaread.Report{Malformed: true}
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); err != io.EOF {
		return schemaread.Report{Malformed: true}
	}
	return schemaread.Report{}
}

func receiptConstraints(v *dogfood.Receipt, c *schemaread.Checker) {
	c.Required("tool", strings.TrimSpace(v.Tool) != "")
	c.Required("verb", strings.TrimSpace(v.Verb) != "")
	c.Required("by", strings.TrimSpace(v.By) != "")
	c.Required("at", strings.TrimSpace(v.At) != "")
	c.Required("notes", strings.TrimSpace(v.Notes) != "")
	c.Bound("issue", int64(v.Issue), 0, 9223372036854775807)
}

// receiptLoadOf is a DogfoodReceipt table load built from the table's Wire: it
// reads a wire image into the receipt it carries, from the table's JSON text.
func receiptLoadOf(w tablejson.Wire) dogfood.ReceiptLoad {
	return func(wire []byte) (dogfood.Receipt, schemaread.Report) {
		text, rep := w.WireJSON(wire)
		if refused(rep) {
			return dogfood.Receipt{}, rep
		}
		var rec dogfood.Receipt
		if err := json.Unmarshal(text, &rec); err != nil {
			return dogfood.Receipt{}, schemaread.Report{Malformed: true}
		}
		return rec, rep
	}
}

func refused(r schemaread.Report) bool {
	return r.Reason != "" || r.Malformed || r.KindMismatch > 0 || r.Clamped > 0
}

type builder[T, B any] interface {
	*B
	Init(...ledgerwire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Shutdown()
}

var errWrite = errors.New(schemaread.ReasonMalformed)

func reportOf(rep *ledgerwire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch),
		Clamped: int(rep.Clamped), Malformed: rep.Malformed || rep.Duplicate > 0 || !ok}
	if rep.Verdict == ledgerwire.TableOpenRefused || rep.Verdict == ledgerwire.TableOpenDamaged || rep.MissingRequired > 0 {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

func wireOf[T, B any, PB builder[T, B]](
	measure func([]byte) (int64, error),
	loadWire, fromJSON func(PB, []byte, *ledgerwire.TableReport) bool,
	wireMeasure func(*T, ...ledgerwire.TableWriteContext) int64, wireSave func(*T, []byte, ...ledgerwire.TableWriteContext) int64,
	jsonMeasure func(*T, ...ledgerwire.TableAllocator) int64, jsonSave func(*T, []byte, ...ledgerwire.TableAllocator) int64,
) tablejson.Wire {
	load := func(in []byte, load func(PB, []byte, *ledgerwire.TableReport) bool, out func(*T) []byte) ([]byte, schemaread.Report) {
		b := PB(new(B))
		if !b.Init() {
			return nil, schemaread.Report{Malformed: true}
		}
		defer b.Shutdown()
		var rep ledgerwire.TableReport
		ok := load(b, in, &rep)
		r := reportOf(&rep, ok)
		if !ok || r.Reason != "" || r.Malformed || !b.Lock() {
			r.Malformed = r.Malformed || r.Reason == ""
			return nil, r
		}
		v := b.AsConst()
		if v == nil {
			return nil, schemaread.Report{Malformed: true}
		}
		text := out(v)
		if text == nil {
			r.Malformed = true
		}
		return text, r
	}
	asJSON := func(v *T) []byte {
		n := jsonMeasure(v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if jsonSave(v, buf) != n {
			return nil
		}
		return buf
	}
	asWire := func(v *T) []byte {
		n := wireMeasure(v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if wireSave(v, buf) != n {
			return nil
		}
		return buf
	}
	return tablejson.Wire{
		Measure:  func(wire []byte) error { _, err := measure(wire); return err },
		WireJSON: func(wire []byte) ([]byte, schemaread.Report) { return load(wire, loadWire, asJSON) },
		JSON:     func(text []byte) ([]byte, schemaread.Report) { return load(text, fromJSON, asJSON) },
		JSONWire: func(text []byte) ([]byte, error) {
			wire, rep := load(text, fromJSON, asWire)
			if wire == nil || rep.Reason != "" || rep.Malformed || rep.Clamped > 0 || rep.KindMismatch > 0 {
				return nil, errWrite
			}
			return wire, nil
		},
	}
}
