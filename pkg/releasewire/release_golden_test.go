package releasewire_test

// TestReleaseRecordsGolden is the golden test of the release definitions
// (schemas/release/release.schema, schemas/release/tables.baseline and the
// generated pkg/releasewire): every table of the area round-trips through
// pkg/schemaread as the binary wire, padded standard base64 of that wire and
// the schema JSON text form (S1, S2); a value past each declared bound is
// refused by name on read and on write, never clamped; a damaged image of
// each form is refused by name; and generate.go pins the repository's schema
// pin.
//
// The split it pins (S1, by the coordinator's form ruling of 2026-10-17): a
// record is a FIXED table only where every one of its bounds is a bound the
// code enforces. Every record of this area holds text or a list the code
// does not bound, so every table is VARIABLE: text with no bound in the code
// is *string, a list is []T, and the only bounds declared are the ones the
// code enforces (the two closed lists, ManifestKind and AdoptionState, and
// the 64-byte sha256 digest). So a 200-entry manifest, ledger, snapshot,
// checks file, machines file, evidence file, receipts file, path list and
// build record, and text of a megabyte, round-trip.
//
// The instances are written as JSON text and read by the generated reader;
// the wire is the generated writer's. The test uses no clock, no socket, no
// network and no program; it reads only files of the repository.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unsafe"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// help is the command every refusal points at.
const help = "go doc github.com/mas-bandwidth/nova-tools/pkg/releasewire"

// ---------------------------------------------------------------------------
// Reading a loaded value.

func str(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

func blank(slot *int64) bool { return strings.TrimSpace(str(slot)) == "" }

func fixedText(b []byte, n int32) string { return string(b[:n]) }

// ---------------------------------------------------------------------------
// The constraints: what the Go code requires of each record, stated once for
// the reader and the writer. A variable table reads an absent field as zero,
// so a field the code requires is Required here; a declared bound is checked
// again here so the writer refuses it by name before a byte is written. Each
// function names the code's rule.

var (
	// machineName is pkg/release/adopt.go:27.
	machineName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`)
	// sha256Hex is pkg/release/adopt.go:115.
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// argvWords holds a list of words to internal/update/manifest.go argv (:29):
// the words are non-empty and carry no blank, quote or line break (the
// single-spaced string it splits has none).
func argvWords(c *schemaread.Checker, field string, l *wire.TableList[wire.Word], required bool) {
	if required {
		c.Required(field, l.Count > 0)
	}
	for i := int32(0); i < l.Count; i++ {
		w := str(&l.At(i).Text)
		c.Required(field+" word", w != "" && !strings.ContainsAny(w, " \r\n\t\"'"))
	}
}

func constraintsWord(*wire.Word, *schemaread.Checker)                     {}
func constraintsCycleBuildArgs(*wire.CycleBuildArgs, *schemaread.Checker) {}
func constraintsJourneyEvent(*wire.JourneyEvent, *schemaread.Checker)     {}
func constraintsSpendReceiptFriend(*wire.SpendReceiptFriend, *schemaread.Checker) {
}
func constraintsSnapshotObserved(*wire.SnapshotObserved, *schemaread.Checker) {}
func constraintsSnapshotObservedEntry(*wire.SnapshotObservedEntry, *schemaread.Checker) {
}
func constraintsSnapshotDelivery(v *wire.SnapshotDelivery, c *schemaread.Checker) {
	distinctNames(c, "delivery.observed.name", &v.Observed)
}
func constraintsSnapshotDeliveryEntry(v *wire.SnapshotDeliveryEntry, c *schemaread.Checker) {
	constraintsSnapshotDelivery(&v.Delivery, c)
}
func constraintsSnapshotScope(*wire.SnapshotScope, *schemaread.Checker)       {}
func constraintsVersionSnapEntry(*wire.VersionSnapEntry, *schemaread.Checker) {}
func constraintsVersionSnapshot(*wire.VersionSnapshot, *schemaread.Checker)   {}
func constraintsBuildRecord(*wire.BuildRecord, *schemaread.Checker)           {}

// distinctNames refuses an observed map that names a key twice
// (internal/update/snapshot.go:124, "two keys naming one field").
func distinctNames(c *schemaread.Checker, field string, l *wire.TableList[wire.SnapshotObservedEntry]) {
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < l.Count; i++ {
		n := str(&l.At(i).Name)
		ok = ok && !seen[n]
		seen[n] = true
	}
	c.Required(field+" distinct", ok)
}

func constraintsUpdateSnapshot(v *wire.UpdateSnapshot, c *schemaread.Checker) {
	distinctNames(c, "observed.name", &v.Observed)
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < v.Delivered.Count; i++ {
		e := v.Delivered.At(i)
		s := str(&e.Scope)
		ok = ok && !seen[s]
		seen[s] = true
		distinctNames(c, "delivered.observed.name", &e.Delivery.Observed)
	}
	c.Required("delivered.scope distinct", ok)
}

// journeyKind is pkg/release/journeygate.go:49 (EvidenceKind).
const journeyKind = "release-journeys"

func installedOf(c *schemaread.Checker, v *wire.JourneyInstalled) {
	// bindEvidence requires a non-blank machine, build and revision
	// (journeygate.go:210).
	c.Required("installed.machine", !blank(&v.Machine))
	c.Required("installed.build", !blank(&v.Build))
	c.Required("installed.revision", !blank(&v.Revision))
}

func constraintsJourneyInstalled(v *wire.JourneyInstalled, c *schemaread.Checker) { installedOf(c, v) }

func headerOf(c *schemaread.Checker, v *wire.JourneyHeader) {
	// The evidence kind (journeygate.go:174) and bindEvidence (:197-215).
	c.Required("evidence", str(&v.Evidence) == journeyKind)
	c.Required("revision", !blank(&v.Revision))
	c.Required("functions", !blank(&v.Functions))
	c.Required("schema", !blank(&v.Schema))
	c.Required("installed", v.Installed.Count > 0)
	for i := int32(0); i < v.Installed.Count; i++ {
		installedOf(c, v.Installed.At(i))
	}
}

func constraintsJourneyHeader(v *wire.JourneyHeader, c *schemaread.Checker) { headerOf(c, v) }
func constraintsJourneyEvidence(v *wire.JourneyEvidence, c *schemaread.Checker) {
	headerOf(c, &v.Header)
}

// spendKind is pkg/release/spendcheck_sources.go:172 (ReceiptsKind).
const spendKind = "spend-receipts"

func constraintsSpendReceipts(v *wire.SpendReceipts, c *schemaread.Checker) {
	// FileReceipts.Tokens (spendcheck_sources.go:192): the evidence kind and
	// the two RFC3339 times json.Unmarshal parses into a time.Time. The
	// present-and-empty friends map the Go code tells from an absent one is
	// one empty list here (the PR names it).
	c.Required("evidence", str(&v.Evidence) == spendKind)
	for _, f := range []struct {
		name string
		slot *int64
	}{{"from", &v.FromTime}, {"to", &v.ToTime}} {
		_, err := time.Parse(time.RFC3339, str(f.slot))
		c.Required(f.name, err == nil)
	}
}

func entryOf(c *schemaread.Checker, v *wire.UpdateManifestEntry) {
	// Load (internal/update/manifest.go:86-98): six non-empty fields (apply
	// may be none), a kind of the closed list, argv words.
	c.Required("name", str(&v.Name) != "")
	c.Required("kind", v.Kind != wire.ManifestKindNone)
	c.Enum("kind", int(v.Kind)-1, int(wire.ManifestKindCount))
	argvWords(c, "installed", &v.Installed, true)
	c.Required("latest", str(&v.Latest) != "")
	argvWords(c, "apply", &v.Apply, false)
	c.Required("owner", str(&v.Owner) != "")
}

func constraintsUpdateManifestEntry(v *wire.UpdateManifestEntry, c *schemaread.Checker) {
	entryOf(c, v)
}

func constraintsUpdateManifest(v *wire.UpdateManifest, c *schemaread.Checker) {
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < v.Entries.Count; i++ {
		e := v.Entries.At(i)
		entryOf(c, e)
		n := str(&e.Name)
		ok = ok && !seen[n] // manifest.go:95
		seen[n] = true
	}
	c.Required("entries.name distinct", ok)
}

func adoptionOf(c *schemaread.Checker, v *wire.AdoptionChoice) {
	// loadAdoption (internal/update/adoption.go:47-50): five non-empty
	// fields, a state of the closed list.
	c.Required("tool", str(&v.Tool) != "")
	c.Required("friend", str(&v.FriendName) != "")
	c.Required("state", v.State != wire.AdoptionStateNone)
	c.Enum("state", int(v.State)-1, int(wire.AdoptionStateCount))
	c.Required("version", str(&v.Version) != "")
	c.Required("detail", str(&v.Detail) != "")
}

func constraintsAdoptionChoice(v *wire.AdoptionChoice, c *schemaread.Checker) { adoptionOf(c, v) }
func constraintsAdoptionLedger(v *wire.AdoptionLedger, c *schemaread.Checker) {
	for i := int32(0); i < v.Rows.Count; i++ {
		adoptionOf(c, v.Rows.At(i))
	}
}

func checkOf(c *schemaread.Checker, v *wire.AdoptCheckEntry) {
	// LoadAdopt (internal/update/adopt.go:55-62): three non-empty fields and
	// an argv command.
	c.Required("check", str(&v.CheckName) != "")
	argvWords(c, "command", &v.Command, true)
	c.Required("owner", str(&v.Owner) != "")
}

func constraintsAdoptCheckEntry(v *wire.AdoptCheckEntry, c *schemaread.Checker) { checkOf(c, v) }
func constraintsAdoptChecks(v *wire.AdoptChecks, c *schemaread.Checker) {
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < v.Checks.Count; i++ {
		e := v.Checks.At(i)
		checkOf(c, e)
		n := str(&e.CheckName)
		ok = ok && !seen[n] // adopt.go:58
		seen[n] = true
	}
	c.Required("checks.check distinct", ok)
}

func machineOf(c *schemaread.Checker, v *wire.MachineEntry) {
	// Machines (pkg/release/adopt.go:65-78): a machine name, and an
	// override that is either absent or non-blank.
	c.Required("name", machineName.MatchString(str(&v.Name)))
	if v.Bin != 0 {
		c.Required("bin", !blank(&v.Bin))
	}
	if v.Dest != 0 {
		c.Required("dest", !blank(&v.Dest))
	}
}

func constraintsMachineEntry(v *wire.MachineEntry, c *schemaread.Checker) { machineOf(c, v) }
func constraintsMachineList(v *wire.MachineList, c *schemaread.Checker) {
	// A list naming no machine is refused (adopt.go:86).
	c.Required("machines", v.Machines.Count > 0)
	for i := int32(0); i < v.Machines.Count; i++ {
		machineOf(c, v.Machines.At(i))
	}
}

func digestText(c *schemaread.Checker, digest []byte, length int32) {
	// The sha256 is 64 bytes, lowercase hex (sumOf, incremental.go:278;
	// sha256Hex, adopt.go:115, held at :696 and by cut.go:654).
	n := min(length, int32(len(digest)))
	s := fixedText(digest, n)
	c.Text("digest", s, 64)
	if length > 64 {
		c.Bound("digest", int64(length), 0, 64)
	}
	c.Required("digest", sha256Hex.MatchString(s))
}

func constraintsPathsFile(v *wire.PathsFile, c *schemaread.Checker) {
	// ReadPathsFile (cut.go:628, :650-654): the range and the digest.
	c.Required("range", !blank(&v.RangeName))
	digestText(c, v.Digest[:], v.DigestLength)
}

func constraintsReleaseDigest(v *wire.ReleaseDigest, c *schemaread.Checker) {
	digestText(c, v.Digest[:], v.DigestLength)
}

// ---------------------------------------------------------------------------
// The harness: one generated table behind pkg/schemaread.

// doc is one value and the region that holds it: text and lists are
// self-relative references into that region, so the value is never copied
// out of it.
type doc[T any] struct {
	v      *T
	region []byte
}

type table[T any] struct {
	name          string
	info          func() *wire.TableTypeInfo
	measureReason func([]byte) (int64, error)
	load          func(region, w []byte, r *wire.TableReport) *T
	fromJSON      func([]byte, *wire.TableReport) (*T, []byte, bool)
	measure       func(*T, ...wire.TableWriteContext) int64
	save          func(*T, []byte, ...wire.TableWriteContext) int64
	jsonMeasure   func(*T, ...wire.TableAllocator) int64
	toJSON        func(*T, []byte, ...wire.TableAllocator) int64
	saveText      func(*T, ...wire.TableWriteContext) (string, error)
	loadText      func(string, *wire.TableReport) (*T, error)
	constraints   func(*T, *schemaread.Checker)
}

type builder[T any] interface {
	Init(...wire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Region() []byte
}

// viaJSON is a generated builder filled from JSON text by the generated
// reader, then locked into its region.
func viaJSON[T any, B any, PB interface {
	*B
	builder[T]
}](from func(*B, []byte, *wire.TableReport) bool) func([]byte, *wire.TableReport) (*T, []byte, bool) {
	return func(text []byte, r *wire.TableReport) (*T, []byte, bool) {
		b := PB(new(B))
		if !b.Init() || !from((*B)(b), text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	}
}

// region is an aligned buffer of n bytes for a region load.
func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func report(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		// a key given twice in the JSON text is ambiguous: refused, never last-wins
		Malformed: r.Malformed || r.Duplicate > 0,
		Reason:    r.Reason,
	}
}

func (tb table[T]) saveWire(v *T) []byte {
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

func (tb table[T]) codec() *schemaread.Codec[doc[T]] {
	return &schemaread.Codec[doc[T]]{
		Name:  tb.name,
		Help:  help,
		Reset: func(d *doc[T]) { *d = doc[T]{} },
		Measure: func(w []byte) error {
			_, err := tb.measureReason(w)
			return err
		},
		LoadWire: func(d *doc[T], w []byte) schemaread.Report {
			n, err := tb.measureReason(w)
			if err != nil || n < 0 {
				return schemaread.Report{Malformed: true}
			}
			var r wire.TableReport
			d.region = region(n)
			if d.v = tb.load(d.region, w, &r); d.v == nil {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			return report(&r)
		},
		LoadJSON: func(d *doc[T], text []byte) schemaread.Report {
			var r wire.TableReport
			v, reg, ok := tb.fromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, reg
			return report(&r)
		},
		Constraints: func(d *doc[T], c *schemaread.Checker) {
			c.Required("record", d.v != nil)
			if d.v != nil {
				tb.constraints(d.v, c)
			}
		},
		SaveWire: func(d *doc[T]) []byte { return tb.saveWire(d.v) },
		SaveJSON: func(d *doc[T]) []byte {
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

var forms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

func b64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func write[T any](t *testing.T, r *schemaread.Reader[doc[T]], d doc[T], form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(&d, form)
	require.NoError(t, err, form)
	require.NotEmpty(t, out, "%s: the generated writer wrote it", form)
	return out
}

// canon is JSON text as a value, with the absent and the empty dropped, so
// two texts that say the same thing compare equal; a present empty string
// is kept apart from an absent one, and numbers keep their digits.
func canon(t *testing.T, text []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v), "%.200s", text)
	return clean(v)
}

func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if c := clean(e); c != nil {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			if out[i] = clean(e); out[i] == nil {
				out[i] = map[string]any{}
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return v
}

// runner is a table behind the harness, whatever its type.
type runner interface {
	label() string
	roundTrip(t *testing.T, sample []byte)
	damage(t *testing.T, sample []byte)
	typeInfo() *wire.TableTypeInfo
	jsonText(t *testing.T, in []byte) []byte
	refuses(t *testing.T, in []byte) string
}

func (tb table[T]) label() string                 { return tb.name }
func (tb table[T]) typeInfo() *wire.TableTypeInfo { return tb.info() }

// roundTrip writes the sample in every form and reads each back as the
// sample (S2), and pins the wire as canonical and the base64 text as padded
// standard base64 of it.
func (tb table[T]) roundTrip(t *testing.T, sample []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, res, err := r.Read(sample)
	require.NoError(t, err, "%s: the sample is read", tb.name)
	require.Equal(t, schemaread.FormJSON, res.Form)
	want := canon(t, sample)
	require.Equal(t, want, canon(t, write(t, r, d, schemaread.FormJSON)), "%s: the JSON text reads as written", tb.name)
	for _, form := range forms {
		out := write(t, r, d, form)
		got, res, err := r.Read(out)
		require.NoError(t, err, "%s %s", tb.name, form)
		assert.Equal(t, form, res.Form)
		assert.Zero(t, res.Unknown)
		assert.Equal(t, want, canon(t, write(t, r, got, schemaread.FormJSON)), "%s %s", tb.name, form)
	}
	w := write(t, r, d, schemaread.FormBinary)
	assert.Equal(t, byte(1), w[0], "%s: the wire leads with its form byte", tb.name)
	again, _, err := r.Read(w)
	require.NoError(t, err)
	assert.Equal(t, w, write(t, r, again, schemaread.FormBinary), "%s: a saved wire re-saves to the same bytes", tb.name)
	assert.Equal(t, b64(w), write(t, r, d, schemaread.FormBase64), "%s: base64 is padded standard base64 of the wire", tb.name)
	// The generated text transport is the same base64, and loads back to the
	// same wire.
	text, err := tb.saveText(d.v)
	require.NoError(t, err)
	assert.Equal(t, string(b64(w)), text, tb.name)
	back, err := tb.loadText(text, nil)
	require.NoError(t, err)
	assert.Equal(t, w, tb.saveWire(back), tb.name)
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBinary), "%s: the per-form counter moves on the form read", tb.name)
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormJSON))
}

// refusal is the reason a read or a write refused with, by name; "" when it
// did not refuse.
func refusal(t *testing.T, name string, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), name+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+help), err.Error())
	if ref.Field != "" {
		return ref.Reason + " (" + ref.Field + ")"
	}
	return ref.Reason
}

func readRefusal[T any](t *testing.T, r *schemaread.Reader[doc[T]], name string, in []byte) string {
	t.Helper()
	got, _, err := r.Read(in)
	if err != nil {
		assert.Nil(t, got.v, "a refused read returns nothing")
	}
	return refusal(t, name, err)
}

// damage reads the damaged images of the sample's good wire and JSON text
// and asserts each is refused by name.
func (tb table[T]) damage(t *testing.T, sample []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, _, err := r.Read(sample)
	require.NoError(t, err)
	good, text := write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON)
	r = schemaread.NewReader(tb.codec())
	newer := bytes.Clone(good)
	newer[0] = 9
	for _, tc := range []struct {
		name   string
		in     []byte
		reason string
	}{
		{"truncated wire", good[:len(good)-1], schemaread.ReasonWireDamaged},
		{"truncated to the form byte", good[:1], schemaread.ReasonWireDamaged},
		{"trailing junk", append(bytes.Clone(good), 0, 1), schemaread.ReasonWireDamaged},
		{"a form this reader does not know", newer, schemaread.ReasonUnknownForm},
		{"the same as base64", b64(newer), schemaread.ReasonUnknownForm},
		{"truncated base64", b64(good)[:len(b64(good))-3], schemaread.ReasonMalformed},
		{"bad base64", append([]byte("!"), b64(good)[1:]...), schemaread.ReasonUnknownForm},
		{"empty", nil, schemaread.ReasonUnknownForm},
		{"truncated JSON text", bytes.TrimSpace(text)[:len(bytes.TrimSpace(text))-1], schemaread.ReasonMalformed},
		{"JSON text that does not parse", []byte(`{"x": `), schemaread.ReasonMalformed},
	} {
		assert.Equal(t, tc.reason, readRefusal(t, r, tb.name, tc.in), "%s: %s", tb.name, tc.name)
	}
	for i := range len(good) {
		assert.NotEmpty(t, readRefusal(t, r, tb.name, good[:i]), "%s: every truncation is refused, never read as a shorter record: cut at %d", tb.name, i)
	}
	assert.Positive(t, r.Counts().Refused(schemaread.ReasonWireDamaged))
}

// duplicated is JSON text with its first key given twice.
func duplicated(text []byte) []byte {
	i := bytes.IndexByte(text, ',')
	return append(append(bytes.Clone(text[:i]), ','), text[1:]...)
}

// pastBound is the wire image good holds with its string of exactly the n
// bytes s grown by one byte, its length prefix rewritten: the image of a
// writer that ignored the bound. The table wire writes a string(N) as kind
// 12, the LEB128 length and the bytes.
func pastBound(t *testing.T, good []byte, s string) []byte {
	t.Helper()
	enc := func(s string) []byte { return append(binary.AppendUvarint([]byte{12}, uint64(len(s))), s...) }
	require.Equal(t, 1, bytes.Count(good, enc(s)), "the at-bound string %q is in the image once", s)
	return bytes.Replace(good, enc(s), enc(s+"9"), 1)
}

// edited is JSON text with one value replaced.
func edited(t *testing.T, text []byte, from, to string) []byte {
	t.Helper()
	require.Equal(t, 1, bytes.Count(text, []byte(from)), "%q is in the text once", from)
	return bytes.Replace(text, []byte(from), []byte(to), 1)
}

// jsonText is the schema JSON text the generated writer makes of an instance.
func (tb table[T]) jsonText(t *testing.T, in []byte) []byte {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, _, err := r.Read(in)
	require.NoError(t, err)
	return write(t, r, d, schemaread.FormJSON)
}

var (
	wordTable = table[wire.Word]{
		name: "Word", info: wire.WordTableType, measureReason: wire.WordLoadMeasureReason,
		load: wire.WordLoad, fromJSON: viaJSON[wire.Word, wire.WordBuilder](wire.WordFromJson),
		measure: wire.WordMeasure, save: wire.WordSave,
		jsonMeasure: wire.WordToJsonMeasure, toJSON: wire.WordToJson,
		saveText: wire.WordSaveText, loadText: wire.WordLoadText, constraints: constraintsWord,
	}
	cycleBuildArgsTable = table[wire.CycleBuildArgs]{
		name: "CycleBuildArgs", info: wire.CycleBuildArgsTableType, measureReason: wire.CycleBuildArgsLoadMeasureReason,
		load: wire.CycleBuildArgsLoad, fromJSON: viaJSON[wire.CycleBuildArgs, wire.CycleBuildArgsBuilder](wire.CycleBuildArgsFromJson),
		measure: wire.CycleBuildArgsMeasure, save: wire.CycleBuildArgsSave,
		jsonMeasure: wire.CycleBuildArgsToJsonMeasure, toJSON: wire.CycleBuildArgsToJson,
		saveText: wire.CycleBuildArgsSaveText, loadText: wire.CycleBuildArgsLoadText, constraints: constraintsCycleBuildArgs,
	}
	journeyInstalledTable = table[wire.JourneyInstalled]{
		name: "JourneyInstalled", info: wire.JourneyInstalledTableType, measureReason: wire.JourneyInstalledLoadMeasureReason,
		load: wire.JourneyInstalledLoad, fromJSON: viaJSON[wire.JourneyInstalled, wire.JourneyInstalledBuilder](wire.JourneyInstalledFromJson),
		measure: wire.JourneyInstalledMeasure, save: wire.JourneyInstalledSave,
		jsonMeasure: wire.JourneyInstalledToJsonMeasure, toJSON: wire.JourneyInstalledToJson,
		saveText: wire.JourneyInstalledSaveText, loadText: wire.JourneyInstalledLoadText, constraints: constraintsJourneyInstalled,
	}
	journeyHeaderTable = table[wire.JourneyHeader]{
		name: "JourneyHeader", info: wire.JourneyHeaderTableType, measureReason: wire.JourneyHeaderLoadMeasureReason,
		load: wire.JourneyHeaderLoad, fromJSON: viaJSON[wire.JourneyHeader, wire.JourneyHeaderBuilder](wire.JourneyHeaderFromJson),
		measure: wire.JourneyHeaderMeasure, save: wire.JourneyHeaderSave,
		jsonMeasure: wire.JourneyHeaderToJsonMeasure, toJSON: wire.JourneyHeaderToJson,
		saveText: wire.JourneyHeaderSaveText, loadText: wire.JourneyHeaderLoadText, constraints: constraintsJourneyHeader,
	}
	journeyEventTable = table[wire.JourneyEvent]{
		name: "JourneyEvent", info: wire.JourneyEventTableType, measureReason: wire.JourneyEventLoadMeasureReason,
		load: wire.JourneyEventLoad, fromJSON: viaJSON[wire.JourneyEvent, wire.JourneyEventBuilder](wire.JourneyEventFromJson),
		measure: wire.JourneyEventMeasure, save: wire.JourneyEventSave,
		jsonMeasure: wire.JourneyEventToJsonMeasure, toJSON: wire.JourneyEventToJson,
		saveText: wire.JourneyEventSaveText, loadText: wire.JourneyEventLoadText, constraints: constraintsJourneyEvent,
	}
	journeyEvidenceTable = table[wire.JourneyEvidence]{
		name: "JourneyEvidence", info: wire.JourneyEvidenceTableType, measureReason: wire.JourneyEvidenceLoadMeasureReason,
		load: wire.JourneyEvidenceLoad, fromJSON: viaJSON[wire.JourneyEvidence, wire.JourneyEvidenceBuilder](wire.JourneyEvidenceFromJson),
		measure: wire.JourneyEvidenceMeasure, save: wire.JourneyEvidenceSave,
		jsonMeasure: wire.JourneyEvidenceToJsonMeasure, toJSON: wire.JourneyEvidenceToJson,
		saveText: wire.JourneyEvidenceSaveText, loadText: wire.JourneyEvidenceLoadText, constraints: constraintsJourneyEvidence,
	}
	spendReceiptFriendTable = table[wire.SpendReceiptFriend]{
		name: "SpendReceiptFriend", info: wire.SpendReceiptFriendTableType, measureReason: wire.SpendReceiptFriendLoadMeasureReason,
		load: wire.SpendReceiptFriendLoad, fromJSON: viaJSON[wire.SpendReceiptFriend, wire.SpendReceiptFriendBuilder](wire.SpendReceiptFriendFromJson),
		measure: wire.SpendReceiptFriendMeasure, save: wire.SpendReceiptFriendSave,
		jsonMeasure: wire.SpendReceiptFriendToJsonMeasure, toJSON: wire.SpendReceiptFriendToJson,
		saveText: wire.SpendReceiptFriendSaveText, loadText: wire.SpendReceiptFriendLoadText, constraints: constraintsSpendReceiptFriend,
	}
	spendReceiptsTable = table[wire.SpendReceipts]{
		name: "SpendReceipts", info: wire.SpendReceiptsTableType, measureReason: wire.SpendReceiptsLoadMeasureReason,
		load: wire.SpendReceiptsLoad, fromJSON: viaJSON[wire.SpendReceipts, wire.SpendReceiptsBuilder](wire.SpendReceiptsFromJson),
		measure: wire.SpendReceiptsMeasure, save: wire.SpendReceiptsSave,
		jsonMeasure: wire.SpendReceiptsToJsonMeasure, toJSON: wire.SpendReceiptsToJson,
		saveText: wire.SpendReceiptsSaveText, loadText: wire.SpendReceiptsLoadText, constraints: constraintsSpendReceipts,
	}
	snapshotObservedTable = table[wire.SnapshotObserved]{
		name: "SnapshotObserved", info: wire.SnapshotObservedTableType, measureReason: wire.SnapshotObservedLoadMeasureReason,
		load: wire.SnapshotObservedLoad, fromJSON: viaJSON[wire.SnapshotObserved, wire.SnapshotObservedBuilder](wire.SnapshotObservedFromJson),
		measure: wire.SnapshotObservedMeasure, save: wire.SnapshotObservedSave,
		jsonMeasure: wire.SnapshotObservedToJsonMeasure, toJSON: wire.SnapshotObservedToJson,
		saveText: wire.SnapshotObservedSaveText, loadText: wire.SnapshotObservedLoadText, constraints: constraintsSnapshotObserved,
	}
	snapshotObservedEntryTable = table[wire.SnapshotObservedEntry]{
		name: "SnapshotObservedEntry", info: wire.SnapshotObservedEntryTableType, measureReason: wire.SnapshotObservedEntryLoadMeasureReason,
		load: wire.SnapshotObservedEntryLoad, fromJSON: viaJSON[wire.SnapshotObservedEntry, wire.SnapshotObservedEntryBuilder](wire.SnapshotObservedEntryFromJson),
		measure: wire.SnapshotObservedEntryMeasure, save: wire.SnapshotObservedEntrySave,
		jsonMeasure: wire.SnapshotObservedEntryToJsonMeasure, toJSON: wire.SnapshotObservedEntryToJson,
		saveText: wire.SnapshotObservedEntrySaveText, loadText: wire.SnapshotObservedEntryLoadText, constraints: constraintsSnapshotObservedEntry,
	}
	snapshotDeliveryTable = table[wire.SnapshotDelivery]{
		name: "SnapshotDelivery", info: wire.SnapshotDeliveryTableType, measureReason: wire.SnapshotDeliveryLoadMeasureReason,
		load: wire.SnapshotDeliveryLoad, fromJSON: viaJSON[wire.SnapshotDelivery, wire.SnapshotDeliveryBuilder](wire.SnapshotDeliveryFromJson),
		measure: wire.SnapshotDeliveryMeasure, save: wire.SnapshotDeliverySave,
		jsonMeasure: wire.SnapshotDeliveryToJsonMeasure, toJSON: wire.SnapshotDeliveryToJson,
		saveText: wire.SnapshotDeliverySaveText, loadText: wire.SnapshotDeliveryLoadText, constraints: constraintsSnapshotDelivery,
	}
	snapshotDeliveryEntryTable = table[wire.SnapshotDeliveryEntry]{
		name: "SnapshotDeliveryEntry", info: wire.SnapshotDeliveryEntryTableType, measureReason: wire.SnapshotDeliveryEntryLoadMeasureReason,
		load: wire.SnapshotDeliveryEntryLoad, fromJSON: viaJSON[wire.SnapshotDeliveryEntry, wire.SnapshotDeliveryEntryBuilder](wire.SnapshotDeliveryEntryFromJson),
		measure: wire.SnapshotDeliveryEntryMeasure, save: wire.SnapshotDeliveryEntrySave,
		jsonMeasure: wire.SnapshotDeliveryEntryToJsonMeasure, toJSON: wire.SnapshotDeliveryEntryToJson,
		saveText: wire.SnapshotDeliveryEntrySaveText, loadText: wire.SnapshotDeliveryEntryLoadText, constraints: constraintsSnapshotDeliveryEntry,
	}
	updateSnapshotTable = table[wire.UpdateSnapshot]{
		name: "UpdateSnapshot", info: wire.UpdateSnapshotTableType, measureReason: wire.UpdateSnapshotLoadMeasureReason,
		load: wire.UpdateSnapshotLoad, fromJSON: viaJSON[wire.UpdateSnapshot, wire.UpdateSnapshotBuilder](wire.UpdateSnapshotFromJson),
		measure: wire.UpdateSnapshotMeasure, save: wire.UpdateSnapshotSave,
		jsonMeasure: wire.UpdateSnapshotToJsonMeasure, toJSON: wire.UpdateSnapshotToJson,
		saveText: wire.UpdateSnapshotSaveText, loadText: wire.UpdateSnapshotLoadText, constraints: constraintsUpdateSnapshot,
	}
	snapshotScopeTable = table[wire.SnapshotScope]{
		name: "SnapshotScope", info: wire.SnapshotScopeTableType, measureReason: wire.SnapshotScopeLoadMeasureReason,
		load: wire.SnapshotScopeLoad, fromJSON: viaJSON[wire.SnapshotScope, wire.SnapshotScopeBuilder](wire.SnapshotScopeFromJson),
		measure: wire.SnapshotScopeMeasure, save: wire.SnapshotScopeSave,
		jsonMeasure: wire.SnapshotScopeToJsonMeasure, toJSON: wire.SnapshotScopeToJson,
		saveText: wire.SnapshotScopeSaveText, loadText: wire.SnapshotScopeLoadText, constraints: constraintsSnapshotScope,
	}
	updateManifestEntryTable = table[wire.UpdateManifestEntry]{
		name: "UpdateManifestEntry", info: wire.UpdateManifestEntryTableType, measureReason: wire.UpdateManifestEntryLoadMeasureReason,
		load: wire.UpdateManifestEntryLoad, fromJSON: viaJSON[wire.UpdateManifestEntry, wire.UpdateManifestEntryBuilder](wire.UpdateManifestEntryFromJson),
		measure: wire.UpdateManifestEntryMeasure, save: wire.UpdateManifestEntrySave,
		jsonMeasure: wire.UpdateManifestEntryToJsonMeasure, toJSON: wire.UpdateManifestEntryToJson,
		saveText: wire.UpdateManifestEntrySaveText, loadText: wire.UpdateManifestEntryLoadText, constraints: constraintsUpdateManifestEntry,
	}
	updateManifestTable = table[wire.UpdateManifest]{
		name: "UpdateManifest", info: wire.UpdateManifestTableType, measureReason: wire.UpdateManifestLoadMeasureReason,
		load: wire.UpdateManifestLoad, fromJSON: viaJSON[wire.UpdateManifest, wire.UpdateManifestBuilder](wire.UpdateManifestFromJson),
		measure: wire.UpdateManifestMeasure, save: wire.UpdateManifestSave,
		jsonMeasure: wire.UpdateManifestToJsonMeasure, toJSON: wire.UpdateManifestToJson,
		saveText: wire.UpdateManifestSaveText, loadText: wire.UpdateManifestLoadText, constraints: constraintsUpdateManifest,
	}
	versionSnapEntryTable = table[wire.VersionSnapEntry]{
		name: "VersionSnapEntry", info: wire.VersionSnapEntryTableType, measureReason: wire.VersionSnapEntryLoadMeasureReason,
		load: wire.VersionSnapEntryLoad, fromJSON: viaJSON[wire.VersionSnapEntry, wire.VersionSnapEntryBuilder](wire.VersionSnapEntryFromJson),
		measure: wire.VersionSnapEntryMeasure, save: wire.VersionSnapEntrySave,
		jsonMeasure: wire.VersionSnapEntryToJsonMeasure, toJSON: wire.VersionSnapEntryToJson,
		saveText: wire.VersionSnapEntrySaveText, loadText: wire.VersionSnapEntryLoadText, constraints: constraintsVersionSnapEntry,
	}
	versionSnapshotTable = table[wire.VersionSnapshot]{
		name: "VersionSnapshot", info: wire.VersionSnapshotTableType, measureReason: wire.VersionSnapshotLoadMeasureReason,
		load: wire.VersionSnapshotLoad, fromJSON: viaJSON[wire.VersionSnapshot, wire.VersionSnapshotBuilder](wire.VersionSnapshotFromJson),
		measure: wire.VersionSnapshotMeasure, save: wire.VersionSnapshotSave,
		jsonMeasure: wire.VersionSnapshotToJsonMeasure, toJSON: wire.VersionSnapshotToJson,
		saveText: wire.VersionSnapshotSaveText, loadText: wire.VersionSnapshotLoadText, constraints: constraintsVersionSnapshot,
	}
	adoptionChoiceTable = table[wire.AdoptionChoice]{
		name: "AdoptionChoice", info: wire.AdoptionChoiceTableType, measureReason: wire.AdoptionChoiceLoadMeasureReason,
		load: wire.AdoptionChoiceLoad, fromJSON: viaJSON[wire.AdoptionChoice, wire.AdoptionChoiceBuilder](wire.AdoptionChoiceFromJson),
		measure: wire.AdoptionChoiceMeasure, save: wire.AdoptionChoiceSave,
		jsonMeasure: wire.AdoptionChoiceToJsonMeasure, toJSON: wire.AdoptionChoiceToJson,
		saveText: wire.AdoptionChoiceSaveText, loadText: wire.AdoptionChoiceLoadText, constraints: constraintsAdoptionChoice,
	}
	adoptionLedgerTable = table[wire.AdoptionLedger]{
		name: "AdoptionLedger", info: wire.AdoptionLedgerTableType, measureReason: wire.AdoptionLedgerLoadMeasureReason,
		load: wire.AdoptionLedgerLoad, fromJSON: viaJSON[wire.AdoptionLedger, wire.AdoptionLedgerBuilder](wire.AdoptionLedgerFromJson),
		measure: wire.AdoptionLedgerMeasure, save: wire.AdoptionLedgerSave,
		jsonMeasure: wire.AdoptionLedgerToJsonMeasure, toJSON: wire.AdoptionLedgerToJson,
		saveText: wire.AdoptionLedgerSaveText, loadText: wire.AdoptionLedgerLoadText, constraints: constraintsAdoptionLedger,
	}
	adoptCheckEntryTable = table[wire.AdoptCheckEntry]{
		name: "AdoptCheckEntry", info: wire.AdoptCheckEntryTableType, measureReason: wire.AdoptCheckEntryLoadMeasureReason,
		load: wire.AdoptCheckEntryLoad, fromJSON: viaJSON[wire.AdoptCheckEntry, wire.AdoptCheckEntryBuilder](wire.AdoptCheckEntryFromJson),
		measure: wire.AdoptCheckEntryMeasure, save: wire.AdoptCheckEntrySave,
		jsonMeasure: wire.AdoptCheckEntryToJsonMeasure, toJSON: wire.AdoptCheckEntryToJson,
		saveText: wire.AdoptCheckEntrySaveText, loadText: wire.AdoptCheckEntryLoadText, constraints: constraintsAdoptCheckEntry,
	}
	adoptChecksTable = table[wire.AdoptChecks]{
		name: "AdoptChecks", info: wire.AdoptChecksTableType, measureReason: wire.AdoptChecksLoadMeasureReason,
		load: wire.AdoptChecksLoad, fromJSON: viaJSON[wire.AdoptChecks, wire.AdoptChecksBuilder](wire.AdoptChecksFromJson),
		measure: wire.AdoptChecksMeasure, save: wire.AdoptChecksSave,
		jsonMeasure: wire.AdoptChecksToJsonMeasure, toJSON: wire.AdoptChecksToJson,
		saveText: wire.AdoptChecksSaveText, loadText: wire.AdoptChecksLoadText, constraints: constraintsAdoptChecks,
	}
	machineEntryTable = table[wire.MachineEntry]{
		name: "MachineEntry", info: wire.MachineEntryTableType, measureReason: wire.MachineEntryLoadMeasureReason,
		load: wire.MachineEntryLoad, fromJSON: viaJSON[wire.MachineEntry, wire.MachineEntryBuilder](wire.MachineEntryFromJson),
		measure: wire.MachineEntryMeasure, save: wire.MachineEntrySave,
		jsonMeasure: wire.MachineEntryToJsonMeasure, toJSON: wire.MachineEntryToJson,
		saveText: wire.MachineEntrySaveText, loadText: wire.MachineEntryLoadText, constraints: constraintsMachineEntry,
	}
	machineListTable = table[wire.MachineList]{
		name: "MachineList", info: wire.MachineListTableType, measureReason: wire.MachineListLoadMeasureReason,
		load: wire.MachineListLoad, fromJSON: viaJSON[wire.MachineList, wire.MachineListBuilder](wire.MachineListFromJson),
		measure: wire.MachineListMeasure, save: wire.MachineListSave,
		jsonMeasure: wire.MachineListToJsonMeasure, toJSON: wire.MachineListToJson,
		saveText: wire.MachineListSaveText, loadText: wire.MachineListLoadText, constraints: constraintsMachineList,
	}
	buildRecordTable = table[wire.BuildRecord]{
		name: "BuildRecord", info: wire.BuildRecordTableType, measureReason: wire.BuildRecordLoadMeasureReason,
		load: wire.BuildRecordLoad, fromJSON: viaJSON[wire.BuildRecord, wire.BuildRecordBuilder](wire.BuildRecordFromJson),
		measure: wire.BuildRecordMeasure, save: wire.BuildRecordSave,
		jsonMeasure: wire.BuildRecordToJsonMeasure, toJSON: wire.BuildRecordToJson,
		saveText: wire.BuildRecordSaveText, loadText: wire.BuildRecordLoadText, constraints: constraintsBuildRecord,
	}
	pathsFileTable = table[wire.PathsFile]{
		name: "PathsFile", info: wire.PathsFileTableType, measureReason: wire.PathsFileLoadMeasureReason,
		load: wire.PathsFileLoad, fromJSON: viaJSON[wire.PathsFile, wire.PathsFileBuilder](wire.PathsFileFromJson),
		measure: wire.PathsFileMeasure, save: wire.PathsFileSave,
		jsonMeasure: wire.PathsFileToJsonMeasure, toJSON: wire.PathsFileToJson,
		saveText: wire.PathsFileSaveText, loadText: wire.PathsFileLoadText, constraints: constraintsPathsFile,
	}
	releaseDigestTable = table[wire.ReleaseDigest]{
		name: "ReleaseDigest", info: wire.ReleaseDigestTableType, measureReason: wire.ReleaseDigestLoadMeasureReason,
		load: wire.ReleaseDigestLoad, fromJSON: viaJSON[wire.ReleaseDigest, wire.ReleaseDigestBuilder](wire.ReleaseDigestFromJson),
		measure: wire.ReleaseDigestMeasure, save: wire.ReleaseDigestSave,
		jsonMeasure: wire.ReleaseDigestToJsonMeasure, toJSON: wire.ReleaseDigestToJson,
		saveText: wire.ReleaseDigestSaveText, loadText: wire.ReleaseDigestLoadText, constraints: constraintsReleaseDigest,
	}
)

// allTables is every table of the area, by name.
var allTables = []runner{
	wordTable,
	cycleBuildArgsTable,
	journeyInstalledTable,
	journeyHeaderTable,
	journeyEventTable,
	journeyEvidenceTable,
	spendReceiptFriendTable,
	spendReceiptsTable,
	snapshotObservedTable,
	snapshotObservedEntryTable,
	snapshotDeliveryTable,
	snapshotDeliveryEntryTable,
	updateSnapshotTable,
	snapshotScopeTable,
	updateManifestEntryTable,
	updateManifestTable,
	versionSnapEntryTable,
	versionSnapshotTable,
	adoptionChoiceTable,
	adoptionLedgerTable,
	adoptCheckEntryTable,
	adoptChecksTable,
	machineEntryTable,
	machineListTable,
	buildRecordTable,
	pathsFileTable,
	releaseDigestTable,
}

func (tb table[T]) refuses(t *testing.T, in []byte) string {
	t.Helper()
	return readRefusal(t, schemaread.NewReader(tb.codec()), tb.name, in)
}

// ---------------------------------------------------------------------------
// The samples. Every name is obviously fake, and no secret value appears.

type J = map[string]any
type L = []any

func jsonOf(t *testing.T, m any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func words(ss ...string) L {
	out := L{}
	for _, s := range ss {
		out = append(out, J{"text": s})
	}
	return out
}

func nWords(n int, format string) L {
	out := L{}
	for i := range n {
		out = append(out, J{"text": fmt.Sprintf(format, i)})
	}
	return out
}

func long(c string, n int) string { return strings.Repeat(c, n) }

var digest64 = strings.Repeat("0123456789abcdef", 4)

var kinds = []string{"harness", "engine", "model", "tool", "pin"}

var states = []string{"evaluated", "useful_now", "tried", "adopted", "declined", "deferred", "unknown", "equivalent"}

func manifestEntry(i int) J {
	e := J{"name": fmt.Sprintf("tool-%03d", i), "kind": kinds[i%len(kinds)], "installed": words("example", "--version"),
		"latest": "local:example --version", "owner": "caller"}
	if i%7 != 0 {
		e["apply"] = words("example", "update", fmt.Sprint(i))
	}
	return e
}

func observed(i int) J {
	return J{"name": fmt.Sprintf("tool-%03d", i), "observed": J{"raw": fmt.Sprintf("1.%d.0", i), "status": "ok", "at": "2026-10-16T23:00:00Z"}}
}

func delivered(i int) J {
	return J{"scope": fmt.Sprintf(`["example","a,b","host-%03d"]`, i), "delivery": J{
		"observed": L{observed(i), observed(i + 1)}, "id": fmt.Sprintf("delivery-%03d", i), "at": "2026-10-16T23:00:01Z"}}
}

func header(installed int) J {
	in := L{}
	for i := range installed {
		in = append(in, J{"machine": fmt.Sprintf("example-bench-%02d", i), "build": "v1.5.0-example", "revision": "0123abc"})
	}
	return J{"evidence": journeyKind, "revision": "0123abc", "functions": "f1", "schema": "s1", "installed": in}
}

func event(i int) J {
	return J{"action": "pass", "package": "hook", "test": fmt.Sprintf("TestExample%03d", i), "output": "--- PASS\n"}
}

// samples are the instances of every table: typical, minimal and sized by
// content (200-entry lists, megabyte text).
func samples() map[string][]J {
	var bigEvents, bigObserved, bigDelivered, bigManifest, bigFriends, bigRows, bigLedger, bigChecks, bigMachines L
	var bigPaths = L{}
	for i := range 200 {
		bigEvents = append(bigEvents, event(i))
		bigObserved = append(bigObserved, observed(i))
		bigDelivered = append(bigDelivered, delivered(i))
		bigManifest = append(bigManifest, manifestEntry(i))
		bigFriends = append(bigFriends, J{"friend": fmt.Sprintf("example-%03d", i), "tokens": int64(i) * 1000003})
		bigRows = append(bigRows, J{"name": fmt.Sprintf("nova-example-%03d", i), "stamp": "nova-example v1", "revision": "0123abc", "platform": "linux/amd64"})
		bigLedger = append(bigLedger, J{"tool": fmt.Sprintf("tool-%03d", i), "friend": "example", "state": states[i%len(states)], "version": "-", "detail": "-"})
		bigChecks = append(bigChecks, J{"check": fmt.Sprintf("check-%03d", i), "command": words("example", "check", fmt.Sprint(i)), "owner": "caller"})
		bigMachines = append(bigMachines, J{"name": fmt.Sprintf("bench-%03d", i), "bin": "/opt/example/bin", "dest": "/opt/example/dest"})
		bigPaths = append(bigPaths, J{"text": fmt.Sprintf("pkg/example/file_%03d.go", i)})
	}
	return map[string][]J{
		"Word":                  {{"text": "--incremental"}, {}, {"text": ""}},
		"CycleBuildArgs":        {{"nova_release_build_args": words("--incremental", "--gate", "report", "--reason", "example reason")}, {}, {"nova_release_build_args": append(words("--incremental", "--gate", "report", "--reason", long("r", 1<<20)), nWords(200, "arg-%03d")...)}},
		"JourneyInstalled":      {{"machine": "example-bench", "build": "v1.5.0-example", "revision": "0123abc"}, {"machine": long("m", 4096), "build": long("b", 4096), "revision": long("r", 4096)}},
		"JourneyHeader":         {header(1), header(200)},
		"JourneyEvent":          {event(1), {"action": "run"}, {"action": "output", "package": "cmd/nova-hook", "test": "TestX/sub", "output": long("o", 1<<20)}},
		"JourneyEvidence":       {{"header": header(2), "events": L{event(1), event(2)}}, {"header": header(1)}, {"header": header(200), "events": bigEvents}},
		"SpendReceiptFriend":    {{"friend": "example", "tokens": 123}, {"friend": "", "tokens": 0}, {"friend": long("f", 4096), "tokens": int64(9223372036854775807)}, {"friend": "low", "tokens": int64(-9223372036854775807) - 1}},
		"SpendReceipts":         {{"evidence": spendKind, "from": "2026-10-16T00:00:00Z", "to": "2026-10-17T00:00:00Z", "friends": L{J{"friend": "example", "tokens": 5}}}, {"evidence": spendKind, "from": "2026-10-16T00:00:00Z", "to": "2026-10-17T00:00:00Z"}, {"evidence": spendKind, "from": "2026-10-16T00:00:00Z", "to": "2026-10-17T00:00:00Z", "friends": bigFriends}},
		"SnapshotObserved":      {{"raw": "1.2.3", "status": "ok", "at": "2026-10-16T23:00:00Z"}, {}, {"raw": long("r", 1<<20), "status": "", "at": long("a", 4096)}},
		"SnapshotObservedEntry": {observed(1), {"name": "outil-é", "observed": J{"status": "stale"}}, {"name": long("n", 1<<20), "observed": J{"raw": "x"}}},
		"SnapshotDelivery":      {delivered(1)["delivery"].(J), {}, {"observed": bigObserved, "id": "big"}},
		"SnapshotDeliveryEntry": {delivered(1), {"scope": long("s", 1<<20), "delivery": J{"id": "d"}}},
		"UpdateSnapshot":        {{"observed": L{observed(1), J{"name": "Tool", "observed": J{"raw": "1"}}, J{"name": "tool", "observed": J{"raw": "2"}}}, "delivered": L{delivered(1)}}, {}, {"observed": bigObserved, "delivered": bigDelivered}},
		"SnapshotScope":         {{"as_name": "example", "to": "a,b", "host": "example-host"}, {}, {"as_name": long("a", 4096), "to": long("t", 1<<20), "host": ""}},
		"UpdateManifestEntry":   {manifestEntry(0), manifestEntry(1), manifestEntry(2), manifestEntry(3), manifestEntry(4), {"name": long("n", 1<<20), "kind": "tool", "installed": words("example"), "latest": "-", "owner": long("o", 4096)}},
		"UpdateManifest":        {{"entries": L{manifestEntry(1), manifestEntry(2)}}, {}, {"entries": bigManifest}},
		"VersionSnapEntry":      {{"name": "nova-example", "stamp": "nova-example v1", "revision": "0123abc", "platform": "linux/amd64"}, {}, {"name": long("n", 1<<20), "stamp": "-"}},
		"VersionSnapshot":       {{"rows": L{bigRows[0], bigRows[1]}}, {}, {"rows": bigRows}},
		"AdoptionChoice":        adoptionRows(),
		"AdoptionLedger":        {{"rows": L{bigLedger[0], bigLedger[1]}}, {}, {"rows": bigLedger}},
		"AdoptCheckEntry":       {{"check": "go", "command": words("go", "version"), "owner": "caller"}, {"check": long("c", 1<<20), "command": words(long("w", 4096)), "owner": "x"}},
		"AdoptChecks":           {{"checks": L{bigChecks[0], bigChecks[1]}}, {}, {"checks": bigChecks}},
		"MachineEntry":          {{"name": "bench-1"}, {"name": "bench-2", "bin": "/opt/example/bin", "dest": "/opt/example/dest"}, {"name": "a" + long("b", 4095), "bin": "/" + long("p", 1<<20), "dest": "/" + long("d", 4096)}},
		"MachineList":           {{"machines": L{J{"name": "bench-1"}, J{"name": "bench-2", "bin": "/opt/bin"}}}, {"machines": bigMachines}},
		"BuildRecord":           {{"commit": "0123abc", "go": "go1.26.6", "ldflags": "-X main.version={version}", "base": "v1.4.0", "gate": "report", "reason": "example reason", "rebuilt": words("nova-a", "nova-b"), "reused": words("nova-c")}, {}, {"commit": "", "go": long("g", 4096), "reason": long("r", 1<<20), "rebuilt": nWords(200, "nova-r-%03d"), "reused": nWords(200, "nova-u-%03d")}},
		"PathsFile":             {{"range_name": "v1.4.0...0123abc", "digest": digest64, "files": words("a.go", "b/c.go")}, {"range_name": "v1.4.0...0123abc", "digest": digest64}, {"range_name": long("r", 4096), "digest": digest64, "files": bigPaths}, {"range_name": "v1.4.0...0123abc", "digest": digest64, "files": words("/" + long("p", 1<<20))}},
		"ReleaseDigest":         {{"digest": digest64}, {"digest": strings.Repeat("f", 64)}},
	}
}

func adoptionRows() []J {
	var out []J
	for i, s := range states {
		out = append(out, J{"tool": fmt.Sprintf("tool-%d", i), "friend": "example", "state": s, "version": "1.0.0", "detail": "a detail of the choice"})
	}
	return append(out, J{"tool": long("t", 1<<20), "friend": "example", "state": "adopted", "version": "-", "detail": long("d", 1<<20)})
}

// The small sample of each table is the first one, the one a damage test
// truncates byte by byte.
func small(s map[string][]J, label string) J { return s[label][0] }

// firstDiff is the first byte at which two images of one record differ: the
// start of the variant id an enum rides as.
func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------

func TestReleaseRecordsGolden(t *testing.T) {
	t.Parallel()
	all := samples()

	t.Run("generate.go pins the repository's schema pin", func(t *testing.T) {
		t.Parallel()
		pin := regexp.MustCompile(`(?m)^//go:generate go run github\.com/mas-bandwidth/schema/v2/cmd/schema@(\S+) generate --lang go --out \. \.\./\.\./schemas/(\S+)$`)
		own, err := os.ReadFile("generate.go")
		require.NoError(t, err)
		m := pin.FindSubmatch(own)
		require.NotNil(t, m, "generate.go holds the pin line")
		assert.Equal(t, "release", string(m[2]))
		pong, err := os.ReadFile(filepath.Join("..", "pongwire", "generate.go"))
		require.NoError(t, err)
		pm := pin.FindSubmatch(pong)
		require.NotNil(t, pm)
		assert.Equal(t, string(pm[1]), string(m[1]), "one compiler writes the whole tree (schemas/README.md, the pin)")
	})

	t.Run("every table has samples and every sample is a table", func(t *testing.T) {
		t.Parallel()
		assert.Len(t, allTables, 27)
		have := map[string]bool{}
		for _, tb := range allTables {
			have[tb.label()] = true
			assert.NotEmpty(t, all[tb.label()], "%s has samples", tb.label())
		}
		for label := range all {
			assert.True(t, have[label], "%s is a table of the area", label)
		}
	})

	t.Run("the area is variable tables with the code's bounds", func(t *testing.T) {
		t.Parallel()
		// The bounds declared are the ones the code enforces: the two sha256
		// digests (64 bytes) and the two closed lists. Every other text is
		// *string and every list is open ([]T).
		bounds := map[string]string{"PathsFile.digest": "string(64)", "ReleaseDigest.digest": "string(64)"}
		enums := map[string]string{"UpdateManifestEntry.kind": "ManifestKind", "AdoptionChoice.state": "AdoptionState"}
		seenBound, seenEnum := map[string]bool{}, map[string]bool{}
		for _, tb := range allTables {
			info := tb.typeInfo()
			assert.True(t, info.Variable, "%s is a variable table (S1): its text or lists are not bounded by the code", info.Name)
			for _, f := range info.Fields {
				key := info.Name + "." + f.Name
				switch {
				case f.List:
				case f.Kind == 12 && f.ArrayBound > 0:
					assert.Equal(t, int32(64), f.ArrayBound, key)
					assert.Contains(t, bounds, key, "%s declares a bound the code does not enforce", key)
					seenBound[key] = true
				case enums[key] != "":
					assert.Equal(t, enums[key], f.DeclaredTypeName, key)
					seenEnum[key] = true
				case f.TypeName == "string":
					assert.True(t, f.Pointer, "%s: text the code does not bound is *string", key)
				case f.IsArray || f.HasRange:
					t.Errorf("%s declares a bound the code does not enforce: %s", key, f.DeclaredTypeName)
				}
			}
		}
		assert.Len(t, seenBound, len(bounds))
		assert.Len(t, seenEnum, len(enums))
		_, err := os.Stat(filepath.Join("..", "..", "schemas", "release", "schema.lock"))
		assert.True(t, errors.Is(err, os.ErrNotExist), "no fixed layout is locked: the area has no fixed table")
		baseline, err := os.ReadFile(filepath.Join("..", "..", "schemas", "release", "tables.baseline"))
		require.NoError(t, err)
		for _, tb := range allTables {
			assert.Contains(t, string(baseline), "table "+tb.label()+"\n", "tables.baseline records %s", tb.label())
		}
	})

	t.Run("round trip in every form", func(t *testing.T) {
		t.Parallel()
		for _, tb := range allTables {
			for i, s := range all[tb.label()] {
				t.Run(fmt.Sprintf("%s/%d", tb.label(), i), func(t *testing.T) {
					t.Parallel()
					tb.roundTrip(t, jsonOf(t, s))
				})
			}
		}
	})

	t.Run("every kind and every state is accepted in every form", func(t *testing.T) {
		t.Parallel()
		for i, k := range kinds {
			e := manifestEntry(i)
			e["kind"] = k
			updateManifestEntryTable.roundTrip(t, jsonOf(t, e))
		}
		for _, s := range states {
			adoptionChoiceTable.roundTrip(t, jsonOf(t, J{"tool": "t", "friend": "f", "state": s, "version": "-", "detail": "-"}))
		}
	})

	t.Run("the JSON text is keyed by the schema's names", func(t *testing.T) {
		t.Parallel()
		check := func(tb runner, in J, keys ...string) {
			got := tb.jsonText(t, jsonOf(t, in))
			for _, k := range keys {
				assert.Contains(t, string(got), `"`+k+`"`)
			}
		}
		check(cycleBuildArgsTable, all["CycleBuildArgs"][0], "nova_release_build_args")
		check(spendReceiptsTable, all["SpendReceipts"][0], "evidence", "from", "to", "friends", "friend", "tokens")
		check(journeyEventTable, all["JourneyEvent"][0], "action", "package", "test", "output")
		check(buildRecordTable, all["BuildRecord"][0], "commit", "go", "ldflags", "base", "gate", "reason", "rebuilt", "reused")
		check(adoptCheckEntryTable, all["AdoptCheckEntry"][0], "check", "command", "owner")
		check(adoptionChoiceTable, all["AdoptionChoice"][0], "tool", "friend", "state", "version", "detail")
	})

	t.Run("damage is refused by name in every form", func(t *testing.T) {
		t.Parallel()
		for _, tb := range allTables {
			t.Run(tb.label(), func(t *testing.T) {
				t.Parallel()
				tb.damage(t, jsonOf(t, small(all, tb.label())))
			})
		}
	})

	t.Run("the reader refuses a value past each declared bound", func(t *testing.T) {
		t.Parallel()
		// The closed lists: a sixth manifest kind and a ninth adoption
		// state. The wire carries an enum as the id of its variant, so a
		// writer that ignored the list is a variant id this reader does not
		// know: it reads as none and counts one unknown field (a newer
		// writer's variant is tolerated that way), and the code's rule that
		// a kind or a state is one of the list refuses it by name. The same
		// holds for the JSON text, where the name is unknown.
		kind := manifestEntry(3)
		kindText := jsonOf(t, kind)
		assert.Equal(t, "absent_required (kind)", updateManifestEntryTable.refuses(t, edited(t, kindText, `"kind":"tool"`, `"kind":"sixth"`)), "a sixth kind as JSON text")
		r := schemaread.NewReader(updateManifestEntryTable.codec())
		d, _, err := r.Read(kindText)
		require.NoError(t, err)
		kindWire := write(t, r, d, schemaread.FormBinary)
		pin := manifestEntry(3)
		pin["kind"] = "pin"
		dp, _, err := r.Read(jsonOf(t, pin))
		require.NoError(t, err)
		pinWire := write(t, r, dp, schemaread.FormBinary)
		at := firstDiff(kindWire, pinWire)
		sixth := bytes.Clone(pinWire)
		sixth[at] ^= 0xff
		assert.Equal(t, "absent_required (kind)", updateManifestEntryTable.refuses(t, sixth), "a sixth kind as binary")
		assert.Equal(t, "absent_required (kind)", updateManifestEntryTable.refuses(t, b64(sixth)), "a sixth kind as base64")

		state := J{"tool": "t", "friend": "f", "state": "equivalent", "version": "-", "detail": "-"}
		assert.Equal(t, "absent_required (state)", adoptionChoiceTable.refuses(t, edited(t, jsonOf(t, state), `"state":"equivalent"`, `"state":"ninth"`)), "a ninth state as JSON text")
		ra := schemaread.NewReader(adoptionChoiceTable.codec())
		da, _, err := ra.Read(jsonOf(t, state))
		require.NoError(t, err)
		eqWire := write(t, ra, da, schemaread.FormBinary)
		state["state"] = "deferred"
		dd, _, err := ra.Read(jsonOf(t, state))
		require.NoError(t, err)
		ninth := bytes.Clone(eqWire)
		ninth[firstDiff(eqWire, write(t, ra, dd, schemaread.FormBinary))] ^= 0xff
		assert.Equal(t, "absent_required (state)", adoptionChoiceTable.refuses(t, ninth), "a ninth state as binary")
		assert.Equal(t, "absent_required (state)", adoptionChoiceTable.refuses(t, b64(ninth)), "a ninth state as base64")

		// The digest: 64 bytes are accepted (the round trips above), 65 are
		// refused in all three forms.
		for _, c := range []struct {
			name string
			tb   runner
			in   J
		}{
			{"ReleaseDigest", releaseDigestTable, J{"digest": digest64}},
			{"PathsFile", pathsFileTable, J{"range_name": "v1.4.0...0123abc", "digest": digest64, "files": words("a.go")}},
		} {
			text := jsonOf(t, c.in)
			assert.Equal(t, "clamped", c.tb.refuses(t, edited(t, text, digest64, digest64+"9")), "%s: a digest of 65 bytes as JSON text", c.name)
			var good []byte
			switch c.name {
			case "ReleaseDigest":
				rr := schemaread.NewReader(releaseDigestTable.codec())
				dd, _, err := rr.Read(text)
				require.NoError(t, err)
				good = write(t, rr, dd, schemaread.FormBinary)
			default:
				rr := schemaread.NewReader(pathsFileTable.codec())
				dd, _, err := rr.Read(text)
				require.NoError(t, err)
				good = write(t, rr, dd, schemaread.FormBinary)
			}
			past := pastBound(t, good, digest64)
			assert.Equal(t, "clamped", c.tb.refuses(t, past), "%s: a digest of 65 bytes as binary", c.name)
			assert.Equal(t, "clamped", c.tb.refuses(t, b64(past)), "%s: a digest of 65 bytes as base64", c.name)
			// and 63 bytes is a digest the code refuses (the reader's rule)
			assert.Equal(t, "absent_required (digest)", c.tb.refuses(t, edited(t, text, digest64, digest64[:63])), "%s: a digest of 63 bytes", c.name)
			assert.Equal(t, "absent_required (digest)", c.tb.refuses(t, edited(t, text, digest64, strings.ToUpper(digest64))), "%s: an upper-case digest", c.name)
		}
		// a key given twice is ambiguous
		assert.Equal(t, schemaread.ReasonMalformed, releaseDigestTable.refuses(t, duplicated(jsonOf(t, J{"digest": digest64, "x": 1}))))
	})

	t.Run("the reader refuses what the code refuses", func(t *testing.T) {
		t.Parallel()
		type neg struct {
			tb   runner
			in   J
			want string
		}
		hdr := header(1)
		noEvidence := header(1)
		noEvidence["evidence"] = "other"
		noInstalled := header(1)
		delete(noInstalled, "installed")
		blankBuild := header(1)
		blankBuild["installed"] = L{J{"machine": "m", "build": " ", "revision": "0123abc"}}
		dupe := L{manifestEntry(1), manifestEntry(1)}
		emptyOwner := manifestEntry(1)
		emptyOwner["owner"] = ""
		noInstall := manifestEntry(1)
		delete(noInstall, "installed")
		quoted := manifestEntry(1)
		quoted["installed"] = words("a b")
		noKind := manifestEntry(1)
		delete(noKind, "kind")
		for _, c := range []neg{
			{journeyHeaderTable, noEvidence, "absent_required (evidence)"},
			{journeyHeaderTable, noInstalled, "absent_required (installed)"},
			{journeyEvidenceTable, J{"header": blankBuild}, "absent_required (installed.build)"},
			{journeyEvidenceTable, J{"header": hdr}, ""},
			{spendReceiptsTable, J{"evidence": "other", "from": "2026-10-16T00:00:00Z", "to": "2026-10-17T00:00:00Z"}, "absent_required (evidence)"},
			{spendReceiptsTable, J{"evidence": spendKind, "from": "yesterday", "to": "2026-10-17T00:00:00Z"}, "absent_required (from)"},
			{updateManifestTable, J{"entries": dupe}, "absent_required (entries.name distinct)"},
			{updateManifestEntryTable, emptyOwner, "absent_required (owner)"},
			{updateManifestEntryTable, noInstall, "absent_required (installed)"},
			{updateManifestEntryTable, quoted, "absent_required (installed word)"},
			{updateManifestEntryTable, noKind, "absent_required (kind)"},
			{updateSnapshotTable, J{"observed": L{observed(1), observed(1)}}, "absent_required (observed.name distinct)"},
			{updateSnapshotTable, J{"delivered": L{delivered(1), delivered(1)}}, "absent_required (delivered.scope distinct)"},
			{adoptionChoiceTable, J{"tool": "t", "friend": "f", "state": "tried", "version": "-"}, "absent_required (detail)"},
			{adoptCheckEntryTable, J{"check": "c", "owner": "o"}, "absent_required (command)"},
			{adoptChecksTable, J{"checks": L{all["AdoptCheckEntry"][0], all["AdoptCheckEntry"][0]}}, "absent_required (checks.check distinct)"},
			{machineListTable, J{}, "absent_required (machines)"},
			{machineEntryTable, J{"name": "-bad"}, "absent_required (name)"},
			{machineEntryTable, J{"name": "bench-1", "bin": " "}, "absent_required (bin)"},
			{pathsFileTable, J{"digest": digest64}, "absent_required (range)"},
			{pathsFileTable, J{"range_name": "r"}, "absent_required (digest)"},
		} {
			assert.Equal(t, c.want, c.tb.refuses(t, jsonOf(t, c.in)), "%s: %v", c.tb.label(), c.in)
		}
	})

	t.Run("the writer refuses past each bound and writes nothing", func(t *testing.T) {
		t.Parallel()
		// A kind or a state past the closed list: the generated writer
		// itself writes nothing for it.
		re := schemaread.NewReader(updateManifestEntryTable.codec())
		d, _, err := re.Read(jsonOf(t, manifestEntry(3)))
		require.NoError(t, err)
		d.v.Kind = wire.ManifestKindCount + 1
		for _, form := range forms {
			out, err := re.Write(&d, form)
			assert.Nil(t, out, "a sixth kind %s: nothing is written", form)
			assert.Equal(t, "clamped (kind)", refusal(t, "UpdateManifestEntry", err), "a sixth kind %s", form)
		}
		d.v.Kind = wire.ManifestKindNone
		_, err = re.Write(&d, schemaread.FormBinary)
		assert.Equal(t, "absent_required (kind)", refusal(t, "UpdateManifestEntry", err))

		ra := schemaread.NewReader(adoptionChoiceTable.codec())
		da, _, err := ra.Read(jsonOf(t, all["AdoptionChoice"][0]))
		require.NoError(t, err)
		da.v.State = wire.AdoptionStateCount + 1
		for _, form := range forms {
			out, err := ra.Write(&da, form)
			assert.Nil(t, out, "a ninth state %s: nothing is written", form)
			assert.Equal(t, "clamped (state)", refusal(t, "AdoptionChoice", err), "a ninth state %s", form)
		}

		// A digest of 65 bytes.
		rd := schemaread.NewReader(releaseDigestTable.codec())
		dd, _, err := rd.Read(jsonOf(t, J{"digest": digest64}))
		require.NoError(t, err)
		dd.v.Digest[64], dd.v.DigestLength = '9', 65
		for _, form := range forms {
			out, err := rd.Write(&dd, form)
			assert.Nil(t, out, "a digest of 65 bytes %s: nothing is written", form)
			assert.Equal(t, "clamped (digest)", refusal(t, "ReleaseDigest", err), "a digest of 65 bytes %s", form)
		}
		rp := schemaread.NewReader(pathsFileTable.codec())
		dp, _, err := rp.Read(jsonOf(t, all["PathsFile"][0]))
		require.NoError(t, err)
		dp.v.Digest[64], dp.v.DigestLength = '9', 65
		for _, form := range forms {
			out, err := rp.Write(&dp, form)
			assert.Nil(t, out)
			assert.Equal(t, "clamped (digest)", refusal(t, "PathsFile", err), "%s", form)
		}
	})
}
