package record_test

import (
	"bytes"
	"encoding/base64"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	_ "github.com/mas-bandwidth/nova-tools/internal/record/ledgerread"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordLedgerDualRead is the ledger half of the record ledger and cairn
// records' table adoption (v1.5 aspect 13, part 1; the owner's S2 and S4,
// readers first; the cairn half is internal/cairn
// TestRecordLedgerAndCairnDualRead): the ledger's hash fields and values
// (record.ReadLedgerDay, the read LedgerReport makes of each day hash) read
// alike from the table wire, the padded standard base64 of it, the schema JSON
// text and the legacy form the writers write today; every cut of every form,
// and a seeded run of random images, is refused by name
// (`<RECORD> REFUSED: <reason>; run: <help>`), never read as a row, an empty
// value or zero; a clamped value is refused; and each read is counted under
// its record and its form.
//
// It counts through its own record.NewLedgerReaders, so the package's other
// parallel tests that read ledger days never move its counters.
//
// Unit tier: no clock, no socket, no store; every value is in memory.
func TestRecordLedgerDualRead(t *testing.T) {
	t.Parallel()
	ledgerDualRead(t)
}

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
	r := record.NewLedgerReaders()
	counts := func(rec, form string) uint64 { return r.Counts()[rec][form] }

	// The process's reader, the one LedgerReport reads through, reads the
	// writer's own legacy row too.
	rows, err := record.ReadLedgerDay(day, map[string]string{legacyField: legacyValue})
	require.NoError(t, err, "ReadLedgerDay reads the legacy row")
	assert.Equal(t, []record.LedgerEntry{ledgerRow}, rows)

	// Legacy: the writer's own field and value.
	rows, err = r.ReadDay(day, map[string]string{legacyField: legacyValue})
	require.NoError(t, err, "the legacy row reads")
	assert.Equal(t, []record.LedgerEntry{ledgerRow}, rows)
	assert.Equal(t, uint64(1), counts("LEDGER_KEY", "legacy"))
	assert.Equal(t, uint64(1), counts("LEDGER_VALUE", "legacy"))

	// Each table form reads to the same row, counted under its form.
	images := map[schemaread.Form][2][]byte{}
	for _, form := range tableForms {
		f, v, err := r.FieldText(ledgerRow, form)
		require.NoError(t, err, "write %s", form)
		images[form] = [2][]byte{f, v}
		rows, err := r.ReadDay(day, map[string]string{string(f): string(v)})
		require.NoError(t, err, "read %s", form)
		assert.Equal(t, []record.LedgerEntry{ledgerRow}, rows, "%s reads as the legacy row reads", form)
		assert.Equal(t, uint64(1), counts("LEDGER_KEY", form.String()), "%s key counted", form)
		assert.Equal(t, uint64(1), counts("LEDGER_VALUE", form.String()), "%s value counted", form)
	}

	// One hash holding every form at once (a day mid-cutover) reads every row.
	other := ledgerRow
	other.Card = "card-2"
	mixed := map[string]string{legacyField: legacyValue}
	of, ov, err := r.FieldText(other, schemaread.FormBase64)
	require.NoError(t, err)
	mixed[string(of)] = string(images[schemaread.FormBinary][1])
	rows, err = r.ReadDay(day, mixed)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	mixed[string(of)] = string(ov)
	rows, err = r.ReadDay(day, mixed)
	require.NoError(t, err)
	assert.ElementsMatch(t, []record.LedgerEntry{ledgerRow, other}, rows)

	// Every cut of every form of the field, and of the value, is refused by
	// name; nothing reads as a row.
	images[schemaread.FormLegacy] = [2][]byte{[]byte(legacyField), []byte(legacyValue)}
	for form, img := range images {
		for n := 0; n < len(img[0]); n++ {
			_, err := r.ReadDay(day, map[string]string{string(img[0][:n]): string(img[1])})
			refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, form.String()+" field cut at "+itoa(n))
		}
		for n := 0; n < len(img[1]); n++ {
			_, err := r.ReadDay(day, map[string]string{string(img[0]): string(img[1][:n])})
			refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, form.String()+" value cut at "+itoa(n))
		}
	}
	// A wire led by a form byte this reader does not know, and its base64.
	bad := append([]byte{0x7f}, images[schemaread.FormBinary][1][1:]...)
	for _, image := range []string{string(bad), base64.StdEncoding.EncodeToString(bad)} {
		_, err := r.ReadDay(day, map[string]string{legacyField: image})
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
				rows, err := r.ReadDay(day, map[string]string{kv[0]: kv[1]})
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
		rows, err := r.ReadDay(day, map[string]string{legacyField: string(mut)})
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
	_, err = r.ReadDay(day, map[string]string{legacyField: strings.Replace(legacyValue, `"provider"`, `"prnvider"`, 1)})
	refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, "an unknown legacy value key")
	assert.Contains(t, err.Error(), "malformed (unknown_field)")
	// So is a key's (here a damaged "day" in the table's JSON text).
	jf, _, err := r.FieldText(ledgerRow, schemaread.FormJSON)
	require.NoError(t, err)
	_, err = r.ReadDay(day, map[string]string{strings.Replace(string(jf), `"day"`, `"dax"`, 1): legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "an unknown table key field")
	assert.Contains(t, err.Error(), "malformed (unknown_field)")

	// Clamped: a day past string(10), six token slots past [..5], a legacy key
	// of four parts, a table key naming another day than its hash.
	clampedKey := `{"day":"2026-10-170","card":"c","model":"m","repo":"r"}`
	_, err = r.ReadDay(day, map[string]string{clampedKey: legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a day past its bound")
	clampedValue := `{"provider":"p","tokens":[{"amount":1},{"amount":1},{"amount":1},{"amount":1},{"amount":1},{"amount":1}],"rough":1,"sources":"s"}`
	_, err = r.ReadDay(day, map[string]string{legacyField: clampedValue})
	refusedByName(t, err, "LEDGER_VALUE", record.LedgerHelp, "six token slots")
	_, err = r.ReadDay(day, map[string]string{`["c","m","r","x"]`: legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a legacy key of four parts")
	_, err = r.ReadDay(day, map[string]string{`["c","","r"]`: legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a legacy key with no model")
	elsewhere := ledgerRow
	elsewhere.Day = "2026-10-18"
	ef, _, err := r.FieldText(elsewhere, schemaread.FormJSON)
	require.NoError(t, err)
	_, err = r.ReadDay(day, map[string]string{string(ef): legacyValue})
	refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, "a key of another day")

	// Clamped in the table forms: the wire's day patched to 11 bytes (past
	// string(10)), and its token slots patched to six (past [..5]), each in the
	// binary wire and its base64.
	wf, wv, err := r.FieldText(ledgerRow, schemaread.FormBinary)
	require.NoError(t, err)
	longDay := patch(t, wf, append([]byte{0x0c, 0x0a}, day...), append([]byte{0x0c, 0x0b}, day+"0"...))
	sparse := ledgerRow
	sparse.Known = [5]bool{true, false, false, false, false}
	_, sv, err := r.FieldText(sparse, schemaread.FormBinary)
	require.NoError(t, err)
	// The tokens body: its length, the slot count, then one known slot and
	// four empty ones (0x01 0x00 each); a sixth empty slot grows the body by two.
	sixSlots := patch(t, patch(t, sv, []byte{0x0e, 0x16, 0x0d, 0x05}, []byte{0x0e, 0x18, 0x0d, 0x06}),
		[]byte{0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x04}, []byte{0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x04})
	for _, enc := range []struct {
		name string
		text func([]byte) string
	}{{"binary", func(b []byte) string { return string(b) }}, {"base64", base64.StdEncoding.EncodeToString}} {
		_, err = r.ReadDay(day, map[string]string{enc.text(longDay): string(wv)})
		refusedByName(t, err, "LEDGER_KEY", record.LedgerHelp, enc.name+" day past its bound")
		assert.Contains(t, err.Error(), "clamped", enc.name+" day past its bound")
		_, err = r.ReadDay(day, map[string]string{string(wf): enc.text(sixSlots)})
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

func itoa(n int) string { return strconv.Itoa(n) }
