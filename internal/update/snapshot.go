package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

type observed struct {
	Raw    string `json:"raw"`
	Status string `json:"status"`
	At     string `json:"at"`
}
type delivery struct {
	Observed map[string]observed `json:"observed"`
	ID       string              `json:"id"`
	At       string              `json:"at"`
}
type snapshot struct {
	Observed  map[string]observed `json:"observed"`
	Delivered map[string]delivery `json:"delivered"`
}

// foldKey renders a key the way the decoder will match it. ASCII letters fold by
// case; anything outside ASCII is refused, because the decoder's fold reaches
// beyond ASCII and a key this tool supports never needs to.
func foldKey(name string) (string, error) {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 0x80 {
			return "", fmt.Errorf("a key outside ASCII")
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// maxJSONDepth bounds the reader the same way every other reader here is
// bounded. The shapes this tool decodes are three deep; anything far past that
// is not a snapshot or an artifact, and is refused rather than descended.
const maxJSONDepth = 32

// validateSnapshot walks the snapshot's known shape and refuses exactly the two
// ambiguities a snapshot reader must refuse, without reaching into data it did
// not choose. A typed object -- the snapshot itself, an observed value, a
// and a delivered value --
// names its schema members, so a member must be spelled exactly and a member
// holding a byte outside ASCII is refused (the decoder's fold would otherwise
// let "ſha256" name the digest field), and two members that fold to one name are
// two values for one field. A data map -- `observed` and `delivered`
// -- is keyed by a manifest name or a delivery scope, content this tool did not
// choose: "Tool" and "tool" are distinct tools and "outil-é" is a legal name, so
// there only an exact duplicate key is ambiguity and a lone upper-case or
// Unicode name is preserved, not refused.
func validateSnapshot(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return walkSnapshot(d)
}

func walkSnapshot(d *json.Decoder) error {
	return walkTyped(d, map[string]func(*json.Decoder) error{
		"observed":  func(d *json.Decoder) error { return walkDataMap(d, walkObservedValue) },
		"delivered": func(d *json.Decoder) error { return walkDataMap(d, walkDeliveryValue) },
	})
}

func walkObservedValue(d *json.Decoder) error {
	return walkTyped(d, map[string]func(*json.Decoder) error{
		"raw":    skipValue,
		"status": skipValue,
		"at":     skipValue,
	})
}

func walkDeliveryValue(d *json.Decoder) error {
	return walkTyped(d, map[string]func(*json.Decoder) error{
		"observed": func(d *json.Decoder) error { return walkDataMap(d, walkObservedValue) },
		"id":       skipValue,
		"at":       skipValue,
	})
}

// walkTyped consumes one object whose members must be spelled exactly as the
// names in dispatch. A member that is not one of those names is refused even
// when it has no duplicate (a lone "Raw" or a Unicode alias is not a second
// spelling of "raw"), and two members that fold to one name are refused.
func walkTyped(d *json.Decoder, dispatch map[string]func(*json.Decoder) error) error {
	open, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON")
	}
	if delim, ok := open.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("a schema object is not an object")
	}
	seen := map[string]bool{}
	for {
		k, err := d.Token()
		if err != nil {
			return fmt.Errorf("malformed JSON")
		}
		if end, ok := k.(json.Delim); ok && end == '}' {
			return nil
		}
		name, ok := k.(string)
		if !ok {
			return fmt.Errorf("malformed JSON")
		}
		folded, err := foldKey(name)
		if err != nil {
			return err
		}
		if seen[folded] {
			return fmt.Errorf("two keys naming one field")
		}
		seen[folded] = true
		walk, ok := dispatch[name]
		if !ok {
			return fmt.Errorf("a field is missing or spelled otherwise")
		}
		if err := walk(d); err != nil {
			return err
		}
	}
}

// walkDataMap consumes one object whose keys are names this tool did not choose.
// Case-distinct and non-ASCII keys are legal; only an exact duplicate key is
// ambiguity about an identity.
func walkDataMap(d *json.Decoder, walkValue func(*json.Decoder) error) error {
	open, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON")
	}
	if delim, ok := open.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("a data map is not an object")
	}
	seen := map[string]bool{}
	for {
		k, err := d.Token()
		if err != nil {
			return fmt.Errorf("malformed JSON")
		}
		if end, ok := k.(json.Delim); ok && end == '}' {
			return nil
		}
		name, ok := k.(string)
		if !ok {
			return fmt.Errorf("malformed JSON")
		}
		if seen[name] {
			return fmt.Errorf("two entries naming one name")
		}
		seen[name] = true
		if err := walkValue(d); err != nil {
			return err
		}
	}
}

// skipValue consumes one JSON value of any shape without judging its contents;
// the decoder that follows walks it against the typed struct and its depth
// bound is kept here so a maliciously deep value is refused before recursion.
func skipValue(d *json.Decoder) error {
	return skipValueAtDepth(d, 0)
}

func skipValueAtDepth(d *json.Decoder, depth int) error {
	t, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON")
	}
	return skipAfter(d, t, depth)
}

func skipAfter(d *json.Decoder, t json.Token, depth int) error {
	if depth >= maxJSONDepth {
		return fmt.Errorf("JSON nested deeper than %d", maxJSONDepth)
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '[':
		for {
			v, err := d.Token()
			if err != nil {
				return fmt.Errorf("malformed JSON")
			}
			if end, ok := v.(json.Delim); ok && end == ']' {
				return nil
			}
			if err := skipAfter(d, v, depth+1); err != nil {
				return err
			}
		}
	case '{':
		for {
			k, err := d.Token()
			if err != nil {
				return fmt.Errorf("malformed JSON")
			}
			if end, ok := k.(json.Delim); ok && end == '}' {
				return nil
			}
			if _, ok := k.(string); !ok {
				return fmt.Errorf("malformed JSON")
			}
			v, err := d.Token()
			if err != nil {
				return fmt.Errorf("malformed JSON")
			}
			if err := skipAfter(d, v, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func emptySnapshot() *snapshot {
	return &snapshot{map[string]observed{}, map[string]delivery{}}
}

// snapDoc is one UpdateSnapshot read through pkg/schemaread. A typed form
// (the table wire, its padded standard base64, its schema JSON text) keeps
// the generated value and the region its text references point into; the
// legacy form keeps the maps the snapshot reader has always produced.
type snapDoc struct {
	v      *wire.UpdateSnapshot
	region []byte
	legacy *snapshot
}

// snapshotRead is the one strict reader of the update snapshot (S2, UNION.md
// section 6): the generated UpdateSnapshot table's binary wire, its padded
// standard base64, its schema JSON text and the legacy JSON its writer writes
// today. It refuses damage by name and counts reads per form, the evidence
// the reader-first cutover needs (S4, C13).
var snapshotRead = schemaread.NewReader(snapshotCodec())

// snapshotRegion is an aligned buffer of n bytes for a region load.
func snapshotRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// snapshotStr is a *string field's text; a zero slot is absent, not a value.
func snapshotStr(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// snapshotReport reduces a generated TableReport to the strict read's report.
// A repeated entry name or scope is ambiguity about an identity, so it is
// malformed here rather than silently last-one-wins.
func snapshotReport(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0 || r.MissingRequired > 0,
		Reason:       r.Reason,
	}
}

// snapshotLegacy recognises the JSON object the snapshot writer writes today:
// its observed and delivered members are objects keyed by name, where the
// schema JSON text form spells both as lists of entries. Text that is not an
// object, or not JSON at all, is left to the binary/base64 detector or to the
// legacy reader, which refuses it as it always has.
func snapshotLegacy(in []byte) bool {
	t := bytes.TrimLeft(in, " \t\r\n")
	if len(t) == 0 || t[0] != '{' {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(t, &m) != nil {
		return true // unreadable JSON is the legacy reader's to refuse
	}
	for _, key := range []string{"observed", "delivered"} {
		v := bytes.TrimSpace(m[key])
		if len(v) > 0 && v[0] == '[' {
			return false // a list is the schema JSON text form
		}
	}
	return true
}

// loadSnapshotLegacy reads the legacy form exactly as readSnapshot always
// read it: the strict shape walk (validateSnapshot) refuses an ambiguous or
// misspelled member, and a snapshot lacking either map is refused rather than
// read as an empty one (snapshot.go, snapshot struct).
func loadSnapshotLegacy(d *snapDoc, in []byte) schemaread.Report {
	if err := validateSnapshot(in); err != nil {
		return schemaread.Report{Reason: err.Error()}
	}
	var s snapshot
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	if dec.Decode(new(any)) != io.EOF {
		return schemaread.Report{Reason: "trailing snapshot data"}
	}
	if s.Observed == nil || s.Delivered == nil {
		return schemaread.Report{Reason: schemaread.ReasonAbsentRequired}
	}
	d.legacy = &s
	return schemaread.Report{}
}

// loadSnapshotWire reads the table wire and keeps the region the value's text
// points into, as the generated load returns a value that borrows it.
func loadSnapshotWire(d *snapDoc, w []byte) schemaread.Report {
	n, err := wire.UpdateSnapshotLoadMeasureReason(w)
	if err != nil || n < 0 {
		rep := schemaread.Report{Malformed: true}
		if err != nil {
			rep.Reason = err.Error()
		}
		return rep
	}
	var r wire.TableReport
	d.region = snapshotRegion(n)
	if d.v = wire.UpdateSnapshotLoad(d.region, w, &r); d.v == nil {
		return schemaread.Report{Malformed: true, Reason: r.Reason}
	}
	return snapshotReport(&r)
}

// loadSnapshotJSON builds the table from its schema JSON text form and locks
// it; the builder's region backs every string the value references.
func loadSnapshotJSON(d *snapDoc, text []byte) schemaread.Report {
	var r wire.TableReport
	b := &wire.UpdateSnapshotBuilder{}
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	if !wire.UpdateSnapshotFromJson(b, text, &r) {
		b.Shutdown()
		return schemaread.Report{Malformed: true, Reason: r.Reason}
	}
	if !b.Lock() {
		b.Shutdown()
		return schemaread.Report{Malformed: true}
	}
	d.v, d.region = b.AsConst(), b.Region()
	return snapshotReport(&r)
}

// snapshotConstraints holds the record to the snapshot reader's rules: a
// record must be present, and a name or scope given twice is not one identity
// (snapshot.go, walkDataMap; UPDATE_SNAPSHOT's own rule). The table declares
// no bound: every text is uncapped and every list is open, so nothing here
// can clamp (schemas/release/release.schema, the area's variability rule).
func snapshotConstraints(d *snapDoc, c *schemaread.Checker) {
	c.Required("record", d.v != nil || d.legacy != nil)
	if d.v == nil {
		return
	}
	seenObserved, seenDelivered := map[string]bool{}, map[string]bool{}
	distinct := true
	for i := int32(0); i < d.v.Observed.Count; i++ {
		n := snapshotStr(&d.v.Observed.At(i).Name)
		distinct = distinct && !seenObserved[n]
		seenObserved[n] = true
	}
	for i := int32(0); i < d.v.Delivered.Count; i++ {
		n := snapshotStr(&d.v.Delivered.At(i).Scope)
		distinct = distinct && !seenDelivered[n]
		seenDelivered[n] = true
	}
	c.Required("entries named distinctly", distinct)
}

// snapshotCodec is the update snapshot's Codec for pkg/schemaread: the
// generated UpdateSnapshot table's wire, padded base64 and JSON text, the
// legacy maps, and the constraints the read enforces.
func snapshotCodec() *schemaread.Codec[snapDoc] {
	return &schemaread.Codec[snapDoc]{
		Name:  "UpdateSnapshot",
		Help:  "nova-update report -h",
		Reset: func(d *snapDoc) { *d = snapDoc{} },
		Measure: func(w []byte) error {
			_, err := wire.UpdateSnapshotLoadMeasureReason(w)
			return err
		},
		LoadWire:    loadSnapshotWire,
		LoadJSON:    loadSnapshotJSON,
		Legacy:      snapshotLegacy,
		LoadLegacy:  loadSnapshotLegacy,
		Constraints: snapshotConstraints,
		SaveWire: func(d *snapDoc) []byte {
			b, ok := snapshotBuilder(d)
			if !ok {
				return nil
			}
			defer b.Shutdown()
			n, err := wire.UpdateSnapshotMeasureReason(b.AsConst())
			if err != nil || n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if wire.UpdateSnapshotSave(b.AsConst(), buf) != n {
				return nil
			}
			return buf
		},
		SaveJSON: func(d *snapDoc) []byte {
			b, ok := snapshotBuilder(d)
			if !ok {
				return nil
			}
			defer b.Shutdown()
			n := wire.UpdateSnapshotToJsonMeasure(b.AsConst())
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if wire.UpdateSnapshotToJson(b.AsConst(), buf) != n {
				return nil
			}
			return buf
		},
	}
}

// snapshotBuilder builds the generated table from the maps the snapshot
// reader holds, in name order so two builds of one value are one image. A
// *string is written only when it has text; an absent and an empty string
// read the same.
func snapshotBuilder(d *snapDoc) (*wire.UpdateSnapshotBuilder, bool) {
	s, ok := d.value()
	if !ok {
		return nil, false
	}
	b := &wire.UpdateSnapshotBuilder{}
	if !b.Init() {
		return nil, false
	}
	root := b.GetRoot()
	names := make([]string, 0, len(s.Observed))
	for name := range s.Observed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := wire.UpdateSnapshotObservedAdd(&b.Main, &root.Observed)
		if e == nil {
			b.Shutdown()
			return nil, false
		}
		snapshotEmplace(&b.Main, &e.Name, name)
		snapshotEmplace(&b.Main, &e.Observed.Raw, s.Observed[name].Raw)
		snapshotEmplace(&b.Main, &e.Observed.Status, s.Observed[name].Status)
		snapshotEmplace(&b.Main, &e.Observed.At, s.Observed[name].At)
	}
	scopes := make([]string, 0, len(s.Delivered))
	for scope := range s.Delivered {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		e := wire.UpdateSnapshotDeliveredAdd(&b.Main, &root.Delivered)
		if e == nil {
			b.Shutdown()
			return nil, false
		}
		snapshotEmplace(&b.Main, &e.Scope, scope)
		snapshotEmplace(&b.Main, &e.Delivery.Id, s.Delivered[scope].ID)
		snapshotEmplace(&b.Main, &e.Delivery.At, s.Delivered[scope].At)
		obs := make([]string, 0, len(s.Delivered[scope].Observed))
		for name := range s.Delivered[scope].Observed {
			obs = append(obs, name)
		}
		sort.Strings(obs)
		for _, name := range obs {
			o := wire.SnapshotDeliveryObservedAdd(&b.Main, &e.Delivery.Observed)
			if o == nil {
				b.Shutdown()
				return nil, false
			}
			snapshotEmplace(&b.Main, &o.Name, name)
			snapshotEmplace(&b.Main, &o.Observed.Raw, s.Delivered[scope].Observed[name].Raw)
			snapshotEmplace(&b.Main, &o.Observed.Status, s.Delivered[scope].Observed[name].Status)
			snapshotEmplace(&b.Main, &o.Observed.At, s.Delivered[scope].Observed[name].At)
		}
	}
	if !b.Lock() {
		b.Shutdown()
		return nil, false
	}
	return b, true
}

// snapshotEmplace writes one *string slot, leaving an empty text absent.
func snapshotEmplace(w *wire.TableWorker, slot *wire.TableRef, text string) {
	if text != "" {
		wire.TableStringEmplace(w, slot, []byte(text))
	}
}

// value is the snapshot the read produced: the legacy maps as they were read,
// or the generated table copied into the maps the rest of the tool uses. A
// repeated name (which the constraints already refuse) is not a value.
func (d *snapDoc) value() (*snapshot, bool) {
	if d.legacy != nil {
		return d.legacy, true
	}
	if d.v == nil {
		return nil, false
	}
	s := emptySnapshot()
	for i := int32(0); i < d.v.Observed.Count; i++ {
		e := d.v.Observed.At(i)
		name := snapshotStr(&e.Name)
		if _, twice := s.Observed[name]; twice {
			return nil, false
		}
		s.Observed[name] = observed{Raw: snapshotStr(&e.Observed.Raw), Status: snapshotStr(&e.Observed.Status), At: snapshotStr(&e.Observed.At)}
	}
	for i := int32(0); i < d.v.Delivered.Count; i++ {
		e := d.v.Delivered.At(i)
		scope := snapshotStr(&e.Scope)
		if _, twice := s.Delivered[scope]; twice {
			return nil, false
		}
		dl := delivery{Observed: map[string]observed{}, ID: snapshotStr(&e.Delivery.Id), At: snapshotStr(&e.Delivery.At)}
		for j := int32(0); j < e.Delivery.Observed.Count; j++ {
			o := e.Delivery.Observed.At(j)
			name := snapshotStr(&o.Name)
			if _, twice := dl.Observed[name]; twice {
				return nil, false
			}
			dl.Observed[name] = observed{Raw: snapshotStr(&o.Observed.Raw), Status: snapshotStr(&o.Observed.Status), At: snapshotStr(&o.Observed.At)}
		}
		s.Delivered[scope] = dl
	}
	return s, true
}

// readSnapshot reads the update snapshot from path in any of its four forms,
// through the one strict reader. A missing file is the empty snapshot, as it
// always was; damage is refused by name, never read as absent, empty or zero.
func readSnapshot(path string) (*snapshot, error) {
	s := emptySnapshot()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read snapshot (supply a readable --snapshot)")
	}
	doc, _, err := snapshotRead.Read(b)
	if err != nil {
		return nil, err
	}
	out, ok := doc.value()
	if !ok {
		return nil, &schemaread.Refusal{Name: "UpdateSnapshot", Reason: schemaread.ReasonMalformed, Help: "nova-update report -h"}
	}
	return out, nil
}

// writeSnapshotWith commits with rename. The command passes Environment.Rename,
// which a helper process sets to hold the real writer before committing; shipped
// commands leave it nil and get os.Rename.
func writeSnapshotWith(path string, s *snapshot, rename func(oldPath, newPath string) error) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), snapshotTempPrefix+"*")
	if err != nil {
		return fmt.Errorf("cannot create snapshot temporary file (create its parent directory)")
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }() // ignored: temp file cleanup
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot write snapshot (check room and permissions)")
	}
	if err = rename(temp, path); err != nil {
		return fmt.Errorf("cannot replace snapshot atomically (check destination permissions)")
	}
	return nil
}

// snapshotTempPrefix is this tool's own name for its half-written snapshots.
const snapshotTempPrefix = ".nova-version-snapshot-"

func sameObserved(a, b map[string]observed) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y, ok := b[k]
		if !ok || x.Raw != y.Raw || x.Status != y.Status {
			return false
		}
	}
	return true
}
func snapshotScope(o options) string {
	to := strings.Split(o.to, ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	sort.Strings(to)
	b, _ := json.Marshal([]string{o.as, strings.Join(to, ","), o.host})
	return string(b)
}

func cloneObserved(m map[string]observed) map[string]observed {
	n := map[string]observed{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
