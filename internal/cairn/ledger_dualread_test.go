package cairn_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	_ "github.com/mas-bandwidth/nova-tools/internal/record/ledgerread"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordLedgerAndCairnDualRead is the reader half of the record ledger and
// cairn records' table adoption (v1.5 aspect 13, part 1; the owner's S2 and
// S4, readers first): the ledger's hash fields and values (record.ReadLedgerDay,
// the read LedgerReport makes of each day hash) and the cairn log lines
// (cairn.ReadOpen) read alike from the table wire, the padded standard base64
// of it, the schema JSON text and the legacy form the writers write today;
// every cut of every form, and a seeded run of random images, is refused by
// name (`<RECORD> REFUSED: <reason>; run: <help>`), never read as a row, an
// empty value or zero; a clamped value is refused; and each read is counted
// under its record and its form.
//
// Unit tier: no clock (the test's clock starts at 1000 s), no socket, no
// store; the log files are written into a t.TempDir().
func TestRecordLedgerAndCairnDualRead(t *testing.T) {
	t.Parallel()
	t.Run("ledger", func(t *testing.T) { ledgerDualRead(t) })
	t.Run("cairn log", func(t *testing.T) { cairnDualRead(t) })
}

var testClock = time.Unix(1000, 0).UTC()

const day = "2026-10-17"

var ledgerRow = record.LedgerEntry{
	Day: day, Card: "a13-ledgers.w3", Model: "model-1", Repo: "mas-bandwidth/nova-tools", Provider: "provider-1",
	Tokens: [5]int64{100, 0, 3, 0, 5}, Known: [5]bool{true, false, true, false, true}, Rough: 132, Sources: "fixture",
}

// The legacy field and value exactly as ReplaceLedgerDays writes them.
const (
	legacyField = `["a13-ledgers.w3","model-1","mas-bandwidth/nova-tools"]`
	legacyValue = `{"provider":"provider-1","tokens":[100,null,3,null,5],"rough":132,"sources":"fixture"}`
)

var tableForms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

// refusedByName asserts err is the named refusal of record pointing at help.
func refusedByName(t *testing.T, err error, record, help, what string) {
	t.Helper()
	require.Error(t, err, "%s: want a refusal, got a read", what)
	assert.Contains(t, err.Error(), record+" REFUSED: ", "%s", what)
	assert.Contains(t, err.Error(), "; run: "+help, "%s", what)
}

func ledgerDualRead(t *testing.T) {
	counts := func(rec, form string) uint64 { return record.LedgerReadCounts()[rec][form] }

	// Legacy: the writer's own field and value.
	rows, err := record.ReadLedgerDay(day, map[string]string{legacyField: legacyValue})
	require.NoError(t, err, "the legacy row reads")
	assert.Equal(t, []record.LedgerEntry{ledgerRow}, rows)
	assert.Equal(t, uint64(1), counts("LEDGER_KEY", "legacy"))
	assert.Equal(t, uint64(1), counts("LEDGER_VALUE", "legacy"))

	// Each table form reads to the same row, counted under its form.
	images := map[schemaread.Form][2][]byte{}
	for _, form := range tableForms {
		f, v, err := record.LedgerFieldText(ledgerRow, form)
		require.NoError(t, err, "write %s", form)
		images[form] = [2][]byte{f, v}
		rows, err := record.ReadLedgerDay(day, map[string]string{string(f): string(v)})
		require.NoError(t, err, "read %s", form)
		assert.Equal(t, []record.LedgerEntry{ledgerRow}, rows, "%s reads as the legacy row reads", form)
		assert.Equal(t, uint64(1), counts("LEDGER_KEY", form.String()), "%s key counted", form)
		assert.Equal(t, uint64(1), counts("LEDGER_VALUE", form.String()), "%s value counted", form)
	}

	// One hash holding every form at once (a day mid-cutover) reads every row.
	other := ledgerRow
	other.Card = "card-2"
	mixed := map[string]string{legacyField: legacyValue}
	of, ov, err := record.LedgerFieldText(other, schemaread.FormBase64)
	require.NoError(t, err)
	mixed[string(of)] = string(images[schemaread.FormBinary][1])
	rows, err = record.ReadLedgerDay(day, mixed)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	mixed[string(of)] = string(ov)
	rows, err = record.ReadLedgerDay(day, mixed)
	require.NoError(t, err)
	assert.ElementsMatch(t, []record.LedgerEntry{ledgerRow, other}, rows)

	// Every cut of every form of the field, and of the value, is refused by
	// name; nothing reads as a row.
	images[schemaread.FormLegacy] = [2][]byte{[]byte(legacyField), []byte(legacyValue)}
	for form, img := range images {
		for n := 0; n < len(img[0]); n++ {
			_, err := record.ReadLedgerDay(day, map[string]string{string(img[0][:n]): string(img[1])})
			refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, form.String()+" field cut at "+itoa(n))
		}
		for n := 0; n < len(img[1]); n++ {
			_, err := record.ReadLedgerDay(day, map[string]string{string(img[0]): string(img[1][:n])})
			refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, form.String()+" value cut at "+itoa(n))
		}
	}
	// A wire led by a form byte this reader does not know, and its base64.
	bad := append([]byte{0x7f}, images[schemaread.FormBinary][1][1:]...)
	for _, image := range []string{string(bad), base64.StdEncoding.EncodeToString(bad)} {
		_, err := record.ReadLedgerDay(day, map[string]string{legacyField: image})
		refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, "an unknown form byte")
	}
	// Every single-byte flip of every form of the field, and of the value, is
	// refused by name or reads as a row of the same shape (each field the
	// fixture holds still held, the same token slots known); never a short
	// row, an empty field or a zero.
	for form, img := range images {
		for side, rec := range []string{"LEDGER_KEY", "LEDGER_VALUE"} {
			flips(img[side], func(at int, mask byte, mut []byte) {
				kv := [2]string{string(img[0]), string(img[1])}
				kv[side] = string(mut)
				rows, err := record.ReadLedgerDay(day, map[string]string{kv[0]: kv[1]})
				what := form.String() + " " + rec + " flip at " + itoa(at) + " ^" + itoa(int(mask))
				if err != nil {
					refusedByName(t, err, rec, record.LedgerHelp, what)
					return
				}
				if assert.Len(t, rows, 1, "%s", what) {
					ledgerShaped(t, rows[0], what)
				}
			})
		}
	}
	// A seeded run of random images (no clock): each is refused by name or,
	// when it happens to be a whole record, reads as a row of the same shape;
	// never a panic or a silent row.
	rnd := rand.New(rand.NewPCG(1000, 17))
	for i := range 2000 {
		img := images[tableForms[i%len(tableForms)]][1]
		mut := append([]byte(nil), img...)
		mut[rnd.IntN(len(mut))] ^= byte(1 + rnd.IntN(255))
		rows, err := record.ReadLedgerDay(day, map[string]string{legacyField: string(mut)})
		if err != nil {
			refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, "random image "+itoa(i))
			continue
		}
		if assert.Len(t, rows, 1, "random image %d", i) {
			ledgerShaped(t, rows[0], "random image "+itoa(i))
		}
	}
	// A value key no reader names (here a damaged "provider") is refused, never
	// read as a row with no provider.
	_, err = record.ReadLedgerDay(day, map[string]string{legacyField: strings.Replace(legacyValue, `"provider"`, `"prnvider"`, 1)})
	refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, "an unknown legacy value key")
	assert.Contains(t, err.Error(), "malformed (unknown_field)")
	// So is a key's (here a damaged "day" in the table's JSON text).
	jf, _, err := record.LedgerFieldText(ledgerRow, schemaread.FormJSON)
	require.NoError(t, err)
	_, err = record.ReadLedgerDay(day, map[string]string{strings.Replace(string(jf), `"day"`, `"dax"`, 1): legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "an unknown table key field")
	assert.Contains(t, err.Error(), "malformed (unknown_field)")

	// Clamped: a day past string(10), six token slots past [..5], a legacy key
	// of four parts, a table key naming another day than its hash.
	clampedKey := `{"day":"2026-10-170","card":"c","model":"m","repo":"r"}`
	_, err = record.ReadLedgerDay(day, map[string]string{clampedKey: legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a day past its bound")
	clampedValue := `{"provider":"p","tokens":[{"amount":1},{"amount":1},{"amount":1},{"amount":1},{"amount":1},{"amount":1}],"rough":1,"sources":"s"}`
	_, err = record.ReadLedgerDay(day, map[string]string{legacyField: clampedValue})
	refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, "six token slots")
	_, err = record.ReadLedgerDay(day, map[string]string{`["c","m","r","x"]`: legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a legacy key of four parts")
	_, err = record.ReadLedgerDay(day, map[string]string{`["c","","r"]`: legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a legacy key with no model")
	elsewhere := ledgerRow
	elsewhere.Day = "2026-10-18"
	ef, _, err := record.LedgerFieldText(elsewhere, schemaread.FormJSON)
	require.NoError(t, err)
	_, err = record.ReadLedgerDay(day, map[string]string{string(ef): legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a key of another day")

	// Clamped in the table forms: the wire's day patched to 11 bytes (past
	// string(10)), and its token slots patched to six (past [..5]), each in the
	// binary wire and its base64.
	wf, wv, err := record.LedgerFieldText(ledgerRow, schemaread.FormBinary)
	require.NoError(t, err)
	longDay := patch(t, wf, append([]byte{0x0c, 0x0a}, day...), append([]byte{0x0c, 0x0b}, day+"0"...))
	sparse := ledgerRow
	sparse.Known = [5]bool{true, false, false, false, false}
	_, sv, err := record.LedgerFieldText(sparse, schemaread.FormBinary)
	require.NoError(t, err)
	// The tokens body: its length, the slot count, then one known slot and
	// four empty ones (0x01 0x00 each); a sixth empty slot grows the body by two.
	sixSlots := patch(t, patch(t, sv, []byte{0x0e, 0x16, 0x0d, 0x05}, []byte{0x0e, 0x18, 0x0d, 0x06}),
		[]byte{0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x04}, []byte{0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x04})
	for _, enc := range []struct {
		name string
		text func([]byte) string
	}{{"binary", func(b []byte) string { return string(b) }}, {"base64", base64.StdEncoding.EncodeToString}} {
		_, err = record.ReadLedgerDay(day, map[string]string{enc.text(longDay): string(wv)})
		refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, enc.name+" day past its bound")
		assert.Contains(t, err.Error(), "clamped", enc.name+" day past its bound")
		_, err = record.ReadLedgerDay(day, map[string]string{string(wf): enc.text(sixSlots)})
		refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, enc.name+" six token slots")
		assert.Contains(t, err.Error(), "clamped", enc.name+" six token slots")
	}
	assert.Positive(t, counts("LEDGER_KEY", "refused_clamped"), "the clamp is counted")
	assert.Positive(t, counts("LEDGER_VALUE", "refused_clamped"), "the clamp is counted")
	assert.Positive(t, counts("LEDGER_VALUE", "refused_malformed"), "the cuts are counted")
}

// ledgerShaped asserts a row read from a damaged image has the fixture's
// shape: the hash's day, every text field held, the same token slots known
// and a rough count; damage never reads as an empty field or a zero.
func ledgerShaped(t *testing.T, r record.LedgerEntry, what string) {
	t.Helper()
	assert.Equal(t, day, r.Day, "%s: the day", what)
	for name, f := range map[string]string{"card": r.Card, "model": r.Model, "repo": r.Repo, "provider": r.Provider, "sources": r.Sources} {
		assert.NotEmpty(t, f, "%s: %s read as empty", what, name)
	}
	assert.Equal(t, ledgerRow.Known, r.Known, "%s: the known token slots", what)
	assert.NotZero(t, r.Rough, "%s: rough read as zero", what)
}

// flips calls each with every single-byte flip of img under four masks.
func flips(img []byte, each func(at int, mask byte, mut []byte)) {
	for at := range img {
		for _, mask := range []byte{0x01, 0x20, 0x80, 0xff} {
			mut := append([]byte(nil), img...)
			mut[at] ^= mask
			each(at, mask, mut)
		}
	}
}

// patch replaces the one occurrence of old in img with new; a fixture that
// holds old other than once fails, so a regenerated wire fails loudly.
func patch(t *testing.T, img, old, new []byte) []byte {
	t.Helper()
	require.Equal(t, 1, bytes.Count(img, old), "the image holds % x once", old)
	return bytes.Replace(img, old, new, 1)
}

// patchID replaces the one occurrence of a generated id (little-endian, as
// the wire's id table holds it) with another.
func patchID(t *testing.T, img []byte, old, new uint64) []byte {
	t.Helper()
	var o, n [8]byte
	binary.LittleEndian.PutUint64(o[:], old)
	binary.LittleEndian.PutUint64(n[:], new)
	return patch(t, img, o[:], n[:])
}

func itoa(n int) string { return strconv.Itoa(n) }

// writeLog makes a store whose log.jsonl holds lines.
func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	store := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(store, "log.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return store
}

func cairnDualRead(t *testing.T) {
	counts := func(k string) uint64 { return cairn.LogReadCounts()[k] }
	stamp := testClock.Format(time.RFC3339Nano)
	want := cairn.OpenRecord{Source: "session:x", Publish: cairn.PublishManual, Opened: testClock, Found: true}

	// Legacy: the line Open writes.
	store := t.TempDir()
	legacyBefore := counts("legacy")
	require.NoError(t, cairn.Open(store, "s1", "session:x", testClock, cairn.PublishManual))
	got, err := cairn.ReadOpen(store, "s1")
	require.NoError(t, err)
	assert.Equal(t, want, got, "the legacy open line reads")
	assert.GreaterOrEqual(t, counts("legacy")-legacyBefore, uint64(1))
	legacyLine := strings.TrimSpace(readFile(t, filepath.Join(store, "log.jsonl")))

	// Each table form of the same record reads to the same open record. The
	// wire rides a line only when it holds no newline byte, so the fixture is
	// checked for one.
	images := map[schemaread.Form]string{schemaread.FormLegacy: legacyLine}
	for _, form := range tableForms {
		line, err := cairn.LogLineText("open", "s1", "", stamp, cairn.PublishManual, "session:x", form)
		require.NoError(t, err, "write %s", form)
		require.NotContains(t, string(line), "\n", "%s rides one line", form)
		images[form] = string(line)
		before := counts(form.String())
		if form == schemaread.FormJSON {
			before = counts("legacy") // the record's JSON text is the legacy line's shape
		}
		got, err := cairn.ReadOpen(writeLog(t, string(line)), "s1")
		require.NoError(t, err, "read %s", form)
		assert.Equal(t, want, got, "%s reads as the legacy line reads", form)
		if form == schemaread.FormJSON {
			assert.GreaterOrEqual(t, counts("legacy")-before, uint64(1))
		} else {
			assert.Equal(t, uint64(1), counts(form.String())-before, "%s counted", form)
		}
	}

	// Every cut of the session's open line, in every form, is refused by name
	// naming the log. The one exception is SPEC-CAIRN line 30's: a cut legacy
	// line that no longer names this session and the open event could be
	// another session's, so it is skipped and counted, never read as this
	// session's open.
	for form, line := range images {
		for n := 1; n < len(line); n++ {
			cut := line[:n]
			store := writeLog(t, cut)
			refusedBefore := counts("refused_malformed") + counts("refused_unknown_form") + counts("refused_wire_damaged") + counts("refused_clamped") + counts("refused_absent_required")
			got, err := cairn.ReadOpen(store, "s1")
			what := form.String() + " line cut at " + itoa(n)
			if err != nil {
				assert.Contains(t, err.Error(), filepath.Join(store, "log.jsonl"), "%s: the refusal names the log", what)
				refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, what)
				continue
			}
			// The record's JSON text reads as the legacy line it shares a shape with.
			assert.Contains(t, []schemaread.Form{schemaread.FormLegacy, schemaread.FormJSON}, form, "%s: only a text cut may be another session's", what)
			assert.False(t, strings.Contains(cut, `"s1"`) && strings.Contains(cut, `"open"`), "%s: a cut naming this session's open is refused", what)
			assert.False(t, got.Found, "%s: never read as an open", what)
			refusedAfter := counts("refused_malformed") + counts("refused_unknown_form") + counts("refused_wire_damaged") + counts("refused_clamped") + counts("refused_absent_required")
			assert.Greater(t, refusedAfter, refusedBefore, "%s: the skipped line is counted", what)
		}
	}

	// A table image of this session's open whose first byte is damaged to
	// printable punctuation is in no form a reader knows, and is refused by
	// name for every session, never skipped as no record.
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64} {
		for _, lead := range []byte("@!#-") {
			line := []byte(images[form])
			line[0] = lead
			store := writeLog(t, string(line))
			got, err := cairn.ReadOpen(store, "s1")
			what := form.String() + " line led by " + string(lead)
			refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, what)
			assert.Contains(t, err.Error(), "unknown_form", what)
			assert.Contains(t, err.Error(), filepath.Join(store, "log.jsonl"), what)
			assert.False(t, got.Found, what)
		}
	}

	// Every single-byte flip of the session's open line, in every form, is
	// refused by name naming the log, or reads as an open of the same shape
	// (a source, a policy and a stamp), or reads whole as another record (a
	// flipped session or event names another session's record, and a table
	// form carries no check that could tell); a text line is skipped only
	// under SPEC-CAIRN line 30's rule (it no longer names this session's
	// open), counted. Never a silent none.
	refusals := func() uint64 {
		return counts("refused_malformed") + counts("refused_unknown_form") + counts("refused_wire_damaged") + counts("refused_clamped") + counts("refused_absent_required") + counts("refused_kind_mismatch")
	}
	for form, line := range images {
		text := form == schemaread.FormLegacy || form == schemaread.FormJSON
		flips([]byte(line), func(at int, mask byte, mut []byte) {
			store := writeLog(t, string(mut))
			before := refusals()
			got, err := cairn.ReadOpen(store, "s1")
			what := form.String() + " line flip at " + itoa(at) + " ^" + itoa(int(mask))
			switch {
			case err != nil:
				assert.Contains(t, err.Error(), filepath.Join(store, "log.jsonl"), "%s: the refusal names the log", what)
				refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, what)
			case got.Found:
				assert.NotEmpty(t, got.Source, "%s: source read as none", what)
				assert.NotEmpty(t, got.Publish, "%s: policy read as none", what)
				assert.False(t, got.Opened.IsZero(), "%s: stamp read as none", what)
			case text && refusals() > before:
				assert.False(t, strings.Contains(string(mut), `"s1"`) && strings.Contains(string(mut), `"open"`), "%s: a line naming this session's open is refused", what)
			default:
				assert.Equal(t, before, refusals(), "%s: a table image is refused or read whole, never skipped", what)
			}
		})
	}
	// A damaged key on the session's open (an unknown field) and a damaged
	// stamp are refused by name, never read as an open with no policy or no
	// stamp.
	malformedBefore := counts("refused_malformed")
	_, err = cairn.ReadOpen(writeLog(t, strings.Replace(legacyLine, `"publish"`, `"pubmish"`, 1)), "s1")
	refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, "an unknown key on the open")
	assert.Contains(t, err.Error(), "malformed (unknown_field)")
	_, err = cairn.ReadOpen(writeLog(t, strings.Replace(legacyLine, stamp, strings.Replace(stamp, ":", ";", 1), 1)), "s1")
	refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, "a damaged stamp")
	assert.Contains(t, err.Error(), "invalid stamp")
	assert.Contains(t, err.Error(), "malformed (stamp)")
	assert.Equal(t, uint64(2), counts("refused_malformed")-malformedBefore, "both refusals are counted")

	// Clamped in the table forms, each in the binary wire and its base64: a
	// session patched to 129 bytes (past string(128)), and an event and a
	// policy whose id no enum names. The ids are the generated ones
	// (pkg/ledgerwire CairnLogRecordSaveBodyFields: open, manual).
	const idOpen, idManual, idNone = 0xf84f97b4633670e9, 0x567e8790c5b15043, 0x1111111111111111
	long128 := strings.Repeat("x", 128)
	wire128, err := cairn.LogLineText("open", long128, "", stamp, cairn.PublishManual, "session:x", schemaread.FormBinary)
	require.NoError(t, err)
	wire, err := cairn.LogLineText("open", "s1", "", stamp, cairn.PublishManual, "session:x", schemaread.FormBinary)
	require.NoError(t, err)
	for name, c := range map[string]struct {
		img     []byte
		session string
	}{
		"session past 128":     {patch(t, wire128, append([]byte{0x0c, 0x80, 0x01}, long128...), append([]byte{0x0c, 0x81, 0x01}, long128+"x"...)), long128},
		"event no enum names":  {patchID(t, wire, idOpen, idNone), "s1"},
		"policy no enum names": {patchID(t, wire, idManual, idNone), "s1"},
	} {
		for _, line := range []string{string(c.img), base64.StdEncoding.EncodeToString(c.img)} {
			_, err := cairn.ReadOpen(writeLog(t, line), c.session)
			refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, name)
			assert.Contains(t, err.Error(), "clamped", name)
		}
	}

	// Clamped: a policy outside Policies, an event outside open and append,
	// and a session past 128 bytes, read; and the writer refuses the long id.
	long := "s1" + strings.Repeat("x", 128)
	for name, c := range map[string]struct{ line, session string }{
		"policy":  {`{"entry":"","event":"open","publish":"bogus","session":"s1","source":"x","stamp":""}`, "s1"},
		"event":   {`{"entry":"","event":"close","publish":"manual","session":"s1","source":"x","stamp":"","note":"open"}`, "s1"},
		"session": {`{"entry":"","event":"open","publish":"manual","session":"` + long + `","source":"x","stamp":""}`, long},
	} {
		_, err := cairn.ReadOpen(writeLog(t, c.line), c.session)
		refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, name)
		assert.Contains(t, err.Error(), "clamped", name)
	}
	_, err = cairn.LogLineText("open", strings.Repeat("s", 129), "", stamp, cairn.PublishManual, "x", schemaread.FormBase64)
	refusedByName(t, err, "CAIRN_LOG_RECORD", cairn.LogHelp, "a writer's long session")

	// Another session's open, in a table form, is not this one's.
	other, err := cairn.LogLineText("open", "s2", "", stamp, cairn.PublishNever, "session:y", schemaread.FormBase64)
	require.NoError(t, err)
	got, err = cairn.ReadOpen(writeLog(t, string(other), legacyLine), "s1")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	require.NoError(t, err)
	return string(b)
}
