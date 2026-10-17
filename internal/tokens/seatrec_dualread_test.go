package tokens

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tokens dayfile records, a day file (dayfile.go ReadDayFile) and the bus
// roster (bus.go laneNames), are read alike from their fixed binary image, the
// padded standard base64 of that image and the legacy form the writers write
// today (S2), refused by name when damaged, counted per record and per form
// (C13). Every writer still writes the legacy form.

const sampleDayName = "2026-10-16"

const sampleDayJSON = `{"day":"2026-10-16","at":"2026-10-16T23:00:00Z","build":"v1.5.0-example","turns":42,"sources":[{"text":"claude"},{"text":"opencode"}],"rows":[{"date":"2026-10-16","model":"example-model-000","repo":"example/repo","input":1000,"output":0,"cache_read":9223372036854775807,"reasoning":7,"rough":3,"basis":"America/New_York","sources":[{"text":"claude"},{"text":"opencode"}]},{"date":"2026-10-16","model":"example-model-001","repo":"example/repo","output":5,"rough":0,"basis":"utc","sources":[{"text":"claude"}]}]}`

const sampleDayLegacy = `nova-tokens v1 day=2026-10-16 at=2026-10-16T23:00:00Z build=v1.5.0-example turns=42 sources=claude,opencode
date	model	repo	input	output	cache_write	cache_read	reasoning	rough	day_basis	sources
2026-10-16	example-model-000	example/repo	1000	0	-	9223372036854775807	7	3	America/New_York	claude,opencode
2026-10-16	example-model-001	example/repo	-	5	-	-	-	0	utc	claude
`

const sampleRosterJSON = `{"participants":[{"name":"Example 000","lane":"from-example-000"},{"name":"Example 001","lane":"from-example-001"}]}`

// dayForms are the binary image and its base64 of the sample day, written by
// the table's own writer through a plain reader (no file name to hold it to).
func dayForms(t testing.TB) (image, b64 []byte, doc srDoc[wire.DayFile]) {
	plain := schemaread.NewReader(dayTable.codec())
	doc, _, err := plain.Read([]byte(sampleDayJSON))
	require.NoError(t, err)
	image, err = plain.Write(&doc, schemaread.FormBinary)
	require.NoError(t, err)
	b64, err = plain.Write(&doc, schemaread.FormBase64)
	require.NoError(t, err)
	return image, b64, doc
}

func rosterForms(t testing.TB) (image, b64 []byte) {
	plain := schemaread.NewReader(rosterTable.codec())
	doc, _, err := plain.Read([]byte(sampleRosterJSON))
	require.NoError(t, err)
	image, err = plain.Write(&doc, schemaread.FormBinary)
	require.NoError(t, err)
	b64, err = plain.Write(&doc, schemaread.FormBase64)
	require.NoError(t, err)
	return image, b64
}

// refusedAs is true when err is the named refusal of record name.
func refusedAs(err error, name string) bool {
	var r *schemaread.Refusal
	return errors.As(err, &r) && r.Name == name && strings.HasPrefix(err.Error(), name+" REFUSED: ")
}

// dayCanonical is true when a read day file is no absent, empty or zero value
// and is exactly what its own legacy rendering reads back as: every rule
// ParseDayFile holds the legacy form to holds for it.
func dayCanonical(d DayFile) bool {
	if d.Day != sampleDayName || len(d.Rows) == 0 {
		return false
	}
	back, f := ParseDayFile(sampleDayName, d.Render())
	return len(f) == 0 && assert.ObjectsAreEqual(d, back)
}

// damage is the outcome tally of one form's damaged reads.
type damage struct{ refused, same, silent int }

// dayDamage reads one damaged day file. A table form is refused by name with
// no value, or decoded canonically. A legacy form is refused by name with
// ParseDayFile's findings, or decoded canonically.
func dayDamage(t *testing.T, r *DayFileReader, label string, in []byte, want DayFile, tally *damage) {
	t.Helper()
	got, f, form, err := r.Read(sampleDayName, in)
	if err != nil {
		require.True(t, refusedAs(err, "DAYFILE"), "%s: damage %x is refused by name: %v", label, in, err)
		if form == schemaread.FormLegacy {
			require.NotEmpty(t, f, "%s: a refused legacy read names its findings", label)
		} else {
			require.Equal(t, DayFile{}, got, "%s: a refused table read returns no value", label)
		}
		tally.refused++
		return
	}
	// A legacy read is ParseDayFile's, unchanged: damage it does not catch
	// reads as it always has, never as an empty day. A table read is held to
	// the legacy rules and to one equal value besides.
	require.Equal(t, sampleDayName, got.Day, "%s: damage %x", label, in)
	require.NotEmpty(t, got.Rows, "%s: damage %x", label, in)
	if form != schemaread.FormLegacy {
		require.True(t, dayCanonical(got), "%s: damage %x reads canonically: %+v", label, in, got)
	}
	if assert.ObjectsAreEqual(want, got) {
		tally.same++
		return
	}
	tally.silent++
}

// rosterDamage reads one damaged roster through laneNames's reader: refused by
// name, or decoded to slugs that re-read alike (a roster left with no lane is
// laneNames's own refusal).
func rosterDamage(t *testing.T, r *BusRosterReader, label string, in []byte, want []string, tally *damage) {
	t.Helper()
	got, err := r.ReadRoster(in)
	if err != nil {
		require.True(t, refusedAs(err, "ROSTER"), "%s: damage %x is refused by name: %v", label, in, err)
		tally.refused++
		return
	}
	again, err := r.ReadRoster(in)
	require.NoError(t, err, label)
	require.Equal(t, got, again, label)
	if assert.ObjectsAreEqual(want, got) {
		tally.same++
		return
	}
	tally.silent++
}

func TestTokensDayfileDualRead(t *testing.T) {
	t.Parallel()

	wireImage, b64, _ := dayForms(t)
	want, findings := ParseDayFile(sampleDayName, sampleDayLegacy)
	require.Empty(t, findings)
	rosterBinary, rosterB64 := rosterForms(t)

	// The read sites, over files the test writes: ReadDayFile reads every form
	// to one equal value, and laneNames every roster form to the same slugs.
	dir := t.TempDir()
	before := dayReader.r.Counts().Snapshot()
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"binary", wireImage}, {"base64", b64}, {"legacy", []byte(sampleDayLegacy)},
	} {
		sub := filepath.Join(dir, "day-"+tc.name)
		require.NoError(t, os.Mkdir(sub, 0o755))
		require.NoError(t, os.WriteFile(Path(sub, sampleDayName), tc.in, 0o644))
		got, f, err := ReadDayFile(Path(sub, sampleDayName))
		require.NoError(t, err, tc.name)
		require.Empty(t, f, tc.name)
		assert.Equal(t, want, got, "%s: reads as one equal value", tc.name)
	}
	after := dayReader.r.Counts().Snapshot()
	for _, form := range []string{"binary", "base64", "legacy"} {
		assert.GreaterOrEqual(t, after[form], before[form]+1, "ReadDayFile counts the %s read", form)
	}

	rBefore := rosterReader.r.Counts().Snapshot()
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"binary", rosterBinary}, {"base64", rosterB64}, {"legacy", []byte(sampleRosterJSON)},
	} {
		sub := filepath.Join(dir, "roster-"+tc.name)
		require.NoError(t, os.Mkdir(sub, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "participants.json"), tc.in, 0o644))
		got, err := laneNames(os.DirFS(sub))
		require.NoError(t, err, tc.name)
		assert.Equal(t, []string{"example-000", "example-001"}, got, "%s: reads as one equal value", tc.name)
	}
	rAfter := rosterReader.r.Counts().Snapshot()
	for _, form := range []string{"binary", "base64", "legacy"} {
		assert.GreaterOrEqual(t, rAfter[form], rBefore[form]+1, "laneNames counts the %s read", form)
	}

	// A damaged table file at the read site is a line-1 finding carrying the
	// named refusal (the place sum and check refuse), never a day with no rows.
	bad := filepath.Join(dir, "bad")
	require.NoError(t, os.Mkdir(bad, 0o755))
	require.NoError(t, os.WriteFile(Path(bad, sampleDayName), wireImage[:len(wireImage)-1], 0o644))
	got, f, err := ReadDayFile(Path(bad, sampleDayName))
	require.NoError(t, err)
	require.NotEmpty(t, f)
	assert.Equal(t, 1, f[0].Line)
	assert.True(t, strings.HasPrefix(f[0].Reason, "DAYFILE REFUSED: "), f[0].Reason)
	assert.Equal(t, DayFile{}, got)
	// A table image under another day's name is refused as the legacy file is.
	require.NoError(t, os.WriteFile(Path(bad, "2026-10-17"), wireImage, 0o644))
	_, f, err = ReadDayFile(Path(bad, "2026-10-17"))
	require.NoError(t, err)
	require.NotEmpty(t, f)
	assert.Contains(t, f[0].Reason, "DAYFILE REFUSED: malformed")
	assert.Contains(t, f[0].Reason, "the file is named 2026-10-17")
	// A damaged roster at the read site is the named refusal, naming the file.
	require.NoError(t, os.WriteFile(filepath.Join(bad, "participants.json"), rosterBinary[:len(rosterBinary)-1], 0o644))
	_, err = laneNames(os.DirFS(bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "participants.json: ROSTER REFUSED: ")

	// Named damage of each form, on readers of their own so the counts are
	// this test's.
	day := NewDayFileReader()
	roster := NewBusRosterReader()
	dayFlip := append([]byte(nil), wireImage...)
	dayFlip[4] ^= 0x01 // a byte of the day: "3026-10-16" is not the file's name
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"wireImage truncated", wireImage[:len(wireImage)-1]},
		{"wireImage flipped day", dayFlip},
		{"wireImage flipped middle", flipAt(wireImage, len(wireImage)/2, 0xff)},
		{"base64 truncated", b64[:len(b64)-3]},
		{"base64 of the flipped day", []byte(base64.StdEncoding.EncodeToString(dayFlip))},
		{"base64 mangled", append([]byte("!"), b64[1:]...)},
		{"legacy bad header", []byte("not a day file\n")},
		{"legacy truncated", []byte(sampleDayLegacy[:len(sampleDayLegacy)-20])},
		{"legacy flipped day", []byte(strings.Replace(sampleDayLegacy, "day=2026", "day=3026", 1))},
		{"legacy rows out of order", []byte(strings.Replace(strings.Replace(sampleDayLegacy, "model-000", "model-00X", 1), "model-001", "model-000", 1))},
		{"empty", nil},
	} {
		got, _, _, err := day.Read(sampleDayName, tc.in)
		require.True(t, refusedAs(err, "DAYFILE"), "%s: refused by name, got %v", tc.name, err)
		assert.False(t, dayCanonical(got) && assert.ObjectsAreEqual(want, got), "%s: never read as the value", tc.name)
	}
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"wireImage truncated", rosterBinary[:len(rosterBinary)-1]},
		{"wireImage flipped middle", flipAt(rosterBinary, len(rosterBinary)/2, 0xff)},
		{"base64 truncated", rosterB64[:len(rosterB64)-3]},
		{"legacy damaged", []byte("{oops")},
		{"legacy truncated", []byte(sampleRosterJSON[:len(sampleRosterJSON)-2])},
	} {
		_, err := roster.ReadRoster(tc.in)
		require.True(t, refusedAs(err, "ROSTER"), "%s: refused by name, got %v", tc.name, err)
	}

	// A clamped value of each day form is refused by name. The roster
	// declares no bound (an open list of uncapped text), so it has no clamped
	// value to refuse.
	clampJSON := strings.Replace(sampleDayJSON, `"day":"2026-10-16"`, `"day":"2026-10-166"`, 1)
	_, _, _, err = day.Read(sampleDayName, []byte(clampJSON))
	require.True(t, refusedAs(err, "DAYFILE"), "json clamped: %v", err)
	assert.Contains(t, err.Error(), schemaread.ReasonClamped)
	// The table writer refuses to write a count below zero, so the image is
	// patched: the first row's input, 1000 as eight little-endian bytes,
	// becomes -1.
	var le1000, leNeg [8]byte
	binary.LittleEndian.PutUint64(le1000[:], 1000)
	binary.LittleEndian.PutUint64(leNeg[:], math.MaxUint64)
	require.Equal(t, 1, bytes.Count(wireImage, le1000[:]))
	clampBin := bytes.Replace(wireImage, le1000[:], leNeg[:], 1)
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"wireImage clamped", clampBin}, {"base64 clamped", []byte(base64.StdEncoding.EncodeToString(clampBin))},
	} {
		_, _, _, err := day.Read(sampleDayName, tc.in)
		require.True(t, refusedAs(err, "DAYFILE"), "%s: %v", tc.name, err)
		assert.Contains(t, err.Error(), schemaread.ReasonClamped, tc.name)
	}
	_, _, _, err = day.Read(sampleDayName, []byte(strings.Replace(sampleDayLegacy, "\t1000\t", "\t-1000\t", 1)))
	require.True(t, refusedAs(err, "DAYFILE"), "legacy negative count: %v", err)

	// Reads are counted per record and per form, and refusals by reason: one
	// good and one damaged read of each form on fresh readers.
	cd, cr := NewDayFileReader(), NewBusRosterReader()
	for _, in := range [][]byte{wireImage, b64, []byte(sampleDayLegacy), []byte(sampleDayJSON)} {
		_, _, _, err := cd.Read(sampleDayName, in)
		require.NoError(t, err)
	}
	for _, in := range [][]byte{wireImage[:len(wireImage)-1], b64[:len(b64)-4], []byte(sampleDayLegacy[:40]), []byte(clampJSON)} {
		_, _, _, err := cd.Read(sampleDayName, in)
		require.True(t, refusedAs(err, "DAYFILE"), "%v", err)
	}
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormLegacy, schemaread.FormJSON} {
		assert.Equal(t, uint64(2), cd.r.Counts().Reads(form), "day %s reads", form)
	}
	assert.Equal(t, uint64(1), cd.r.Counts().Refused(schemaread.ReasonClamped))
	assert.Equal(t, uint64(4), refusedTotal(cd.r.Counts()))
	for _, in := range [][]byte{rosterBinary, rosterB64, []byte(sampleRosterJSON)} {
		_, err := cr.ReadRoster(in)
		require.NoError(t, err)
	}
	for _, in := range [][]byte{rosterBinary[:len(rosterBinary)-1], rosterB64[:len(rosterB64)-4], []byte("{oops")} {
		_, err := cr.ReadRoster(in)
		require.True(t, refusedAs(err, "ROSTER"), "%v", err)
	}
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormLegacy} {
		assert.Equal(t, uint64(2), cr.r.Counts().Reads(form), "roster %s reads", form)
	}
	assert.Equal(t, uint64(3), refusedTotal(cr.r.Counts()))

	// Every single-byte flip and every truncation of every form, then seeded
	// multi-byte damage: each is refused by name or decoded canonically, and
	// never read as absent, empty or zero. A flip that changes a value in
	// place without breaking the structure is the wire's limit (no checksum):
	// counted here as silent and named in the card's report.
	forms := []struct {
		name string
		in   []byte
	}{
		{"day binary", wireImage}, {"day base64", b64}, {"day legacy", []byte(sampleDayLegacy)},
		{"roster binary", rosterBinary}, {"roster base64", rosterB64}, {"roster legacy", []byte(sampleRosterJSON)},
	}
	rng := rand.New(rand.NewPCG(1000, 1000))
	for _, fm := range forms {
		var tally damage
		read := func(in []byte) {
			if strings.HasPrefix(fm.name, "day") {
				dayDamage(t, day, fm.name, in, want, &tally)
			} else {
				rosterDamage(t, roster, fm.name, in, []string{"example-000", "example-001"}, &tally)
			}
		}
		for i := range fm.in {
			for _, mask := range []byte{0x01, 0x80, 0xff} {
				read(flipAt(fm.in, i, mask))
			}
			read(fm.in[:i])
		}
		for range 2000 {
			in := append([]byte(nil), fm.in...)
			for range 1 + rng.IntN(3) {
				in[rng.IntN(len(in))] ^= byte(1 + rng.IntN(255))
			}
			if rng.IntN(4) == 0 {
				in = in[:rng.IntN(len(in))]
			}
			read(in)
		}
		assert.Positive(t, tally.refused, fm.name)
		t.Logf("%s: damaged reads refused=%d unchanged=%d silent-value-change=%d", fm.name, tally.refused, tally.same, tally.silent)
	}
}

// FuzzTokensDayfileDualRead: any input, in any form, is refused by name or
// read as a canonical, non-empty day file.
func FuzzTokensDayfileDualRead(f *testing.F) {
	image, b64, _ := dayForms(f)
	for _, seed := range [][]byte{image, b64, []byte(sampleDayLegacy), []byte(sampleDayJSON), nil} {
		f.Add(seed)
	}
	r := NewDayFileReader()
	f.Fuzz(func(t *testing.T, in []byte) {
		got, _, form, err := r.Read(sampleDayName, in)
		if err != nil {
			require.True(t, refusedAs(err, "DAYFILE"), "%v", err)
			if form != schemaread.FormLegacy {
				require.Equal(t, DayFile{}, got)
			}
			return
		}
		if form == schemaread.FormLegacy {
			require.Equal(t, sampleDayName, got.Day)
			require.NotEmpty(t, got.Rows)
			return
		}
		require.True(t, dayCanonical(got), "%+v", got)
	})
}

// FuzzTokensRosterDualRead: any roster input is refused by name or read alike
// twice.
func FuzzTokensRosterDualRead(f *testing.F) {
	image, b64 := rosterForms(f)
	for _, seed := range [][]byte{image, b64, []byte(sampleRosterJSON), []byte("{oops"), nil} {
		f.Add(seed)
	}
	r := NewBusRosterReader()
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := r.ReadRoster(in)
		if err != nil {
			require.True(t, refusedAs(err, "ROSTER"), "%v", err)
			return
		}
		again, err := r.ReadRoster(in)
		require.NoError(t, err)
		require.Equal(t, got, again)
	})
}

func refusedTotal(c *schemaread.Counts) uint64 {
	var n uint64
	for k, v := range c.Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			n += v
		}
	}
	return n
}

func flipAt(b []byte, i int, mask byte) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= mask
	return out
}
