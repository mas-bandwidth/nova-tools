package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tokens dayfile records — a day file (dayfile.go) and the bus roster
// (bus.go) — are read alike from their fixed binary image, the padded
// standard base64 of that image and the legacy form the writers write today
// (S2), refused by name when malformed, counted per record and per form
// (C13). Every writer still writes the legacy form.

const sampleDayJSON = `{"day":"2026-10-16","at":"2026-10-16T23:00:00Z","build":"v1.5.0-example","turns":42,"sources":[{"text":"claude"},{"text":"opencode"}],"rows":[{"date":"2026-10-16","model":"example-model-000","repo":"example/repo","input":1000,"output":0,"cache_read":9223372036854775807,"reasoning":7,"rough":3,"basis":"America/New_York","sources":[{"text":"claude"},{"text":"opencode"}]}]}`

const sampleDayLegacy = `nova-tokens v1 day=2026-10-16 at=2026-10-16T23:00:00Z build=v1.5.0-example turns=42 sources=claude,opencode
date	model	repo	input	output	cache_write	cache_read	reasoning	rough	day_basis	sources
2026-10-16	example-model-000	example/repo	1000	0	-	9223372036854775807	7	3	America/New_York	claude,opencode
`

const sampleRosterJSON = `{"participants":[{"name":"Example 000","lane":"from-example-000"},{"name":"Example 001","lane":"from-example-001"}]}`

func TestTokensDayfileDualRead(t *testing.T) {
	t.Parallel()

	// A file in each legacy form on disk, under a directory the test writes.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "day.tsv"), []byte(sampleDayLegacy), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(sampleRosterJSON), 0o644))
	legacy, err := os.ReadFile(filepath.Join(dir, "day.tsv"))
	require.NoError(t, err)
	rosterLegacy, err := os.ReadFile(filepath.Join(dir, "participants.json"))
	require.NoError(t, err)

	// The day file: one value, read alike from every form.
	day := NewDayFileReader()
	expectDay, err := day.ReadDayFile([]byte(sampleDayJSON))
	require.NoError(t, err)
	doc, _, err := day.r.Read([]byte(sampleDayJSON))
	require.NoError(t, err)
	binary, err := day.r.Write(&doc, schemaread.FormBinary)
	require.NoError(t, err)
	base64, err := day.r.Write(&doc, schemaread.FormBase64)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"binary", binary}, {"base64", base64}, {"legacy", legacy},
	} {
		got, err := day.ReadDayFile(tc.in)
		require.NoError(t, err, tc.name)
		assert.Equal(t, expectDay, got, "%s: reads as one equal value", tc.name)
	}

	// The roster: one value, read alike from every form (the legacy form is
	// the JSON form, the same shape).
	re := NewBusRosterReader()
	expectRoster, err := re.ReadRoster([]byte(sampleRosterJSON))
	require.NoError(t, err)
	rdoc, _, err := re.r.Read([]byte(sampleRosterJSON))
	require.NoError(t, err)
	rosterBinary, err := re.r.Write(&rdoc, schemaread.FormBinary)
	require.NoError(t, err)
	rosterB64, err := re.r.Write(&rdoc, schemaread.FormBase64)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"binary", rosterBinary}, {"base64", rosterB64}, {"legacy", rosterLegacy},
	} {
		got, err := re.ReadRoster(tc.in)
		require.NoError(t, err, tc.name)
		assert.Equal(t, expectRoster, got, "%s: reads as one equal value", tc.name)
	}

	// Reads are counted per record and per form.
	assert.Positive(t, day.Counts().Reads(schemaread.FormBinary))
	assert.Positive(t, day.Counts().Reads(schemaread.FormBase64))
	assert.Positive(t, day.Counts().Reads(schemaread.FormLegacy))
	assert.Positive(t, re.Counts().Reads(schemaread.FormBinary))
	assert.Positive(t, re.Counts().Reads(schemaread.FormBase64))
	assert.Positive(t, re.Counts().Reads(schemaread.FormLegacy))

	// A malformed value of each form is refused by name, never read as absent,
	// empty or zero.
	bad := []struct {
		name string
		in   []byte
		kind string
	}{
		{"day binary truncated", binary[:len(binary)-1], "DAYFILE"},
		{"day base64 truncated", base64[:len(base64)-3], "DAYFILE"},
		{"day binary flipped", flip(binary), "DAYFILE"},
		{"day base64 mangled", append([]byte("!"), base64[1:]...), "DAYFILE"},
		{"day legacy bad header", []byte("not a day file\n"), "DAYFILE"},
		{"roster binary truncated", rosterBinary[:len(rosterBinary)-1], "ROSTER"},
		{"roster base64 truncated", rosterB64[:len(rosterB64)-3], "ROSTER"},
	}
	for _, tc := range bad {
		var r, ref *schemaread.Refusal
		if tc.kind == "DAYFILE" {
			_, _, err := day.r.Read(tc.in)
			r = ref
			require.True(t, asRefusal(err, &ref), "%s: refuses by name, got %v", tc.name, err)
			assert.True(t, strings.HasPrefix(err.Error(), "DAYFILE REFUSED: "), tc.name)
		} else {
			_, _, err := re.r.Read(tc.in)
			r = ref
			require.True(t, asRefusal(err, &ref), "%s: refuses by name, got %v", tc.name, err)
			assert.True(t, strings.HasPrefix(err.Error(), "ROSTER REFUSED: "), tc.name)
		}
		_ = r
	}

	// A clamped value is refused by name: a day of eleven bytes.
	clamped := strings.Replace(sampleDayJSON, `"day":"2026-10-16"`, `"day":"2026-10-166"`, 1)
	_, _, err = day.r.Read([]byte(clamped))
	var ref *schemaread.Refusal
	require.True(t, asRefusal(err, &ref), "clamped: refuses by name, got %v", err)
	assert.Contains(t, err.Error(), "clamped")
	assert.Positive(t, day.Counts().Refused(schemaread.ReasonClamped))
}

func flip(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[len(out)/2] ^= 0xff
	return out
}

func asRefusal(err error, ref **schemaread.Refusal) bool {
	if err == nil {
		return false
	}
	r, ok := err.(*schemaread.Refusal)
	if ok {
		*ref = r
	}
	return ok
}
