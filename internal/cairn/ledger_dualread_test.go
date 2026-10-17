package cairn_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	_ "github.com/mas-bandwidth/nova-tools/internal/record/ledgerread"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordLedgerAndCairnDualRead is the reader half of the record ledger and
// cairn records' table adoption (v1.5 aspect 13, part 1; the owner's S2 and
// S4, readers first). The ledger half, the hash fields and values that
// record.ReadLedgerDay reads, is internal/record TestRecordLedgerDualRead: its
// counters and table-form writer are test-build doors of package record
// (internal/record/export_test.go), so its test lives there. This half: the
// cairn log lines (cairn.ReadOpen) read alike from the table wire, the padded
// standard base64 of it, the schema JSON text and the legacy form the writer
// writes today; every cut of every form, and a seeded run of random images,
// is refused by name (`CAIRN_LOG_RECORD REFUSED: <reason>; run: <help>`),
// never read as an open record, an empty value or zero; a clamped value is
// refused; and each read is counted under its form.
//
// Unit tier: no clock (the test's clock starts at 1000 s), no socket, no
// store; the log files are written into a t.TempDir().
func TestRecordLedgerAndCairnDualRead(t *testing.T) {
	t.Parallel()
	cairnDualRead(t)
}

var testClock = time.Unix(1000, 0).UTC()

var tableForms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

// refusedByName asserts err is the named refusal of record pointing at help.
func refusedByName(t *testing.T, err error, record, help, what string) {
	t.Helper()
	require.Error(t, err, "%s: want a refusal, got a read", what)
	assert.Contains(t, err.Error(), record+" REFUSED: ", "%s", what)
	assert.Contains(t, err.Error(), "; run: "+help, "%s", what)
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
