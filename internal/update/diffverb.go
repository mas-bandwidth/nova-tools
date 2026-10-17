package update

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"unsafe"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The version snapshot (the four-column TSV the `snapshot` verb writes) reads
// through pkg/schemaread and the generated releasewire VersionSnapshot table
// (S1, S2): the fixed binary image, the padded standard base64 of that image,
// the schema JSON text and the legacy TSV all decode to one equal map, damage
// is refused by name, and reads are counted per record and per form (C13).
// Writers stay legacy here; the writer switch is the release writers card.

// srDoc is one value and the region that holds it: text and lists are
// self-relative references into that region, so the value is never copied out
// of it.
type srDoc[T any] struct {
	v      *T
	region []byte
}

func srRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

type srBuilder[T any] interface {
	Init(...wire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Region() []byte
}

func srViaJSON[T any, B any, PB interface {
	*B
	srBuilder[T]
}](from func(*B, []byte, *wire.TableReport) bool) func([]byte, *wire.TableReport) (*T, []byte, bool) {
	return func(text []byte, r *wire.TableReport) (*T, []byte, bool) {
		b := PB(new(B))
		if !b.Init() || !from((*B)(b), text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	}
}

func srReport(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
}

type srTable[T any] struct {
	name          string
	help          string
	measureReason func([]byte) (int64, error)
	load          func(region, w []byte, r *wire.TableReport) *T
	fromJSON      func([]byte, *wire.TableReport) (*T, []byte, bool)
	measure       func(*T, ...wire.TableWriteContext) int64
	save          func(*T, []byte, ...wire.TableWriteContext) int64
	jsonMeasure   func(*T, ...wire.TableAllocator) int64
	toJSON        func(*T, []byte, ...wire.TableAllocator) int64
	constraints   func(*T, *schemaread.Checker)
}

func (tb srTable[T]) saveWire(v *T) []byte {
	n := tb.measure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if tb.save(v, buf) != n {
		return nil
	}
	return buf
}

func (tb srTable[T]) codec() *schemaread.Codec[srDoc[T]] {
	return &schemaread.Codec[srDoc[T]]{
		Name:  tb.name,
		Help:  tb.help,
		Reset: func(d *srDoc[T]) { *d = srDoc[T]{} },
		Measure: func(w []byte) error {
			_, err := tb.measureReason(w)
			return err
		},
		LoadWire: func(d *srDoc[T], w []byte) schemaread.Report {
			n, err := tb.measureReason(w)
			if err != nil || n < 0 {
				return schemaread.Report{Malformed: true}
			}
			var r wire.TableReport
			d.region = srRegion(n)
			if d.v = tb.load(d.region, w, &r); d.v == nil {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			return srReport(&r)
		},
		LoadJSON: func(d *srDoc[T], text []byte) schemaread.Report {
			var r wire.TableReport
			v, reg, ok := tb.fromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, reg
			return srReport(&r)
		},
		Constraints: func(d *srDoc[T], c *schemaread.Checker) {
			if d.v != nil {
				tb.constraints(d.v, c)
			}
		},
		SaveWire: func(d *srDoc[T]) []byte { return tb.saveWire(d.v) },
		SaveJSON: func(d *srDoc[T]) []byte {
			n := tb.jsonMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.toJSON(d.v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

var versionSnapshotTable = srTable[wire.VersionSnapshot]{
	name: "VERSNAPSHOT", help: "nova-version snapshot --bin <dir> --out <file.tsv>",
	measureReason: wire.VersionSnapshotLoadMeasureReason,
	load:         wire.VersionSnapshotLoad, fromJSON: srViaJSON[wire.VersionSnapshot, wire.VersionSnapshotBuilder](wire.VersionSnapshotFromJson),
	measure: wire.VersionSnapshotMeasure, save: wire.VersionSnapshotSave,
	jsonMeasure: wire.VersionSnapshotToJsonMeasure, toJSON: wire.VersionSnapshotToJson,
	constraints: versionSnapshotConstraints,
}

// versionSnapshotConstraints are the table's own bounds. VersionSnapshot is a
// VARIABLE table (S1): its rows are an open list and its text is uncapped by
// the code, so no bound is declared; reading an absent field as zero is the
// table's read, and the map key the read builds is the caller's.
func versionSnapshotConstraints(*wire.VersionSnapshot, *schemaread.Checker) {}

// srstr is a table text field as a Go string.
func srstr(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// snapshotOf is the table value as the map the diff verb reads; it holds one
// entry per row, keyed by the row's name, the map `snapshot` reads today.
func snapshotOf(v *wire.VersionSnapshot) map[string]snapRow {
	rows := map[string]snapRow{}
	for i := int32(0); i < v.Rows.Count; i++ {
		e := v.Rows.At(i)
		name, stamp, revision, platform := srstr(&e.Name), srstr(&e.Stamp), srstr(&e.Revision), srstr(&e.Platform)
		rows[name] = snapRow{name: name, stamp: stamp, revision: revision, platform: platform}
	}
	return rows
}

// snapshotTable is true when the input is one of the table forms: the binary
// wire (led by a control byte, the form byte), the schema JSON text, or one
// unbroken run of padded standard base64. Everything else is the legacy TSV
// the snapshot writers write today, damaged or not, so a damaged legacy file
// is still named by parseSnapshotTSV's own findings.
func snapshotTable(in []byte) bool {
	t := bytes.TrimSpace(in)
	if len(t) == 0 {
		return false
	}
	if b := t[0]; b < 0x20 || b >= 0x80 || b == '{' {
		return true
	}
	for _, b := range t {
		ok := b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '/' || b == '='
		if !ok {
			return false
		}
	}
	return true
}

// parseSnapshotTSV parses the legacy four-column TSV `snapshot` writes: the
// header line, then one row per line, # comment lines and blank lines skipped,
// a row of the wrong arity refused. It stays readable, since people write
// these files by hand (C13).
func parseSnapshotTSV(in []byte) (map[string]snapRow, error) {
	sc := bufio.NewScanner(bytes.NewReader(in))
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != snapshotHeader {
		return nil, fmt.Errorf("its header is not %s", tabbed(snapshotHeader))
	}
	rows := map[string]snapRow{}
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("a row has %d fields, not 4", len(f))
		}
		rows[f[0]] = snapRow{name: f[0], stamp: f[1], revision: f[2], platform: f[3]}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cannot read")
	}
	return rows, nil
}

// snapshotReader reads a version snapshot in every form and counts the reads
// per form (S2, C13). One read at a time: the file's name is the read's own
// state.
type snapshotReader struct {
	r *schemaread.Reader[srDoc[wire.VersionSnapshot]]

	mu   sync.Mutex
	rows map[string]snapRow
}

func newSnapshotReader() *snapshotReader {
	s := &snapshotReader{}
	c := versionSnapshotTable.codec()
	loadWire, loadJSON := c.LoadWire, c.LoadJSON
	c.LoadWire = func(v *srDoc[wire.VersionSnapshot], in []byte) schemaread.Report { return s.structure(v, loadWire(v, in)) }
	c.LoadJSON = func(v *srDoc[wire.VersionSnapshot], in []byte) schemaread.Report { return s.structure(v, loadJSON(v, in)) }
	c.Legacy = func(in []byte) bool { return !snapshotTable(in) }
	c.LoadLegacy = func(_ *srDoc[wire.VersionSnapshot], in []byte) schemaread.Report {
		rows, err := parseSnapshotTSV(in)
		if err != nil {
			return schemaread.Report{Reason: err.Error()}
		}
		s.rows = rows
		return schemaread.Report{}
	}
	s.r = schemaread.NewReader(c)
	return s
}

// structure holds a cleanly loaded table value as the same map the legacy TSV
// parses to, so every form reads as one equal value (C13). The rows list has
// no bound the code enforces, so nothing else is refused here.
func (s *snapshotReader) structure(v *srDoc[wire.VersionSnapshot], rep schemaread.Report) schemaread.Report {
	if rep.Malformed || rep.Reason != "" || rep.KindMismatch > 0 || rep.Clamped > 0 || v.v == nil {
		return rep
	}
	s.rows = snapshotOf(v.v)
	return rep
}

// Read reads a version snapshot in any form. A legacy file reads as
// parseSnapshotTSV reads it; a table form reads as the same map. A refused
// read returns the named refusal.
func (s *snapshotReader) Read(in []byte) (map[string]snapRow, schemaread.Form, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = nil
	_, res, err := s.r.Read(in)
	form := res.Form
	if form == schemaread.FormNone {
		form = s.r.Detect(in)
	}
	if err != nil && form != schemaread.FormLegacy {
		return nil, form, err
	}
	return s.rows, form, err
}

// snapshotRead is the one version-snapshot reader of the package; its counters
// are the per-form read counts (S2, C13).
var snapshotRead = newSnapshotReader()

// readSnapshotFile reads the snapshot the path names in every form. A missing
// file names the file; damage names the record and its reason (C13).
func readSnapshotFile(path string) (map[string]snapRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open")
	}
	defer func() { _ = f.Close() }() // ignored: read-only file
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("cannot read")
	}
	rows, _, err := snapshotRead.Read(data)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// diffVerb compares two snapshots: one item per changed binary, and a DIFF OK
// line that counts the state -- every name on either side -- not the output.
func diffVerb(c *tool.Call) *tool.Out {
	from, to := c.Str("from"), c.Str("to")
	// Both files are read before either is refused, so one run names both.
	before, errFrom := readSnapshotFile(from)
	after, errTo := readSnapshotFile(to)
	if errFrom != nil || errTo != nil {
		var why []string
		for _, f := range []struct {
			path string
			err  error
		}{{from, errFrom}, {to, errTo}} {
			if f.err != nil {
				why = append(why, fmt.Sprintf("cannot read %s as a snapshot (%s)", f.path, f.err))
			}
		}
		o := tool.Refuse(strings.Join(why, "; ") + " (write one with nova-version snapshot --bin <dir> --out <file.tsv>)")
		o.Remedy = "nova-version snapshot -h"
		return o
	}
	names := map[string]bool{}
	for n := range before {
		names[n] = true
	}
	for n := range after {
		names[n] = true
	}
	ordered := slices.Sorted(maps.Keys(names))
	o := tool.Done().Fact("from", from).Fact("to", to).Fact("tools", len(ordered))
	changed := 0
	for _, n := range ordered {
		a, inA := before[n]
		b, inB := after[n]
		if inA && inB && a == b {
			continue
		}
		o.Item("changed", "name", n, "from", stampOrDash(a.stamp, inA), "to", stampOrDash(b.stamp, inB))
		changed++
	}
	return o.Fact("changed", changed)
}

// stampOrDash is a snapshot row's stamp, or "-" for a name absent on that side:
// the same value in the lines and in the JSON.
func stampOrDash(stamp string, present bool) string {
	if !present {
		return "-"
	}
	return stamp
}
