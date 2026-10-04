package tokens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckCoverReadNoSpendFileParsesDaysNotesAndSkips pins ReadNoSpendFile's
// main path: one YYYY-MM-DD per line becomes a set entry, the note after the day
// is dropped, a blank line and a `#` comment are skipped, a Windows line ending
// is read, and the same day twice is one entry.
func TestCheckCoverReadNoSpendFileParsesDaysNotesAndSkips(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		lines string
		want  map[string]bool
	}{
		{"plain day", "2026-03-01\n", map[string]bool{"2026-03-01": true}},
		{"note after a space", "2026-03-01 no work this day\n", map[string]bool{"2026-03-01": true}},
		{"note after a tab", "2026-03-02\tholiday\n", map[string]bool{"2026-03-02": true}},
		{"blank and comment lines are skipped", "\n# last week off\n   \n2026-03-03\n",
			map[string]bool{"2026-03-03": true}},
		{"windows line ending", "2026-03-04\r\n", map[string]bool{"2026-03-04": true}},
		{"duplicate day is one entry", "2026-03-05\n2026-03-05 twice\n", map[string]bool{"2026-03-05": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "no-spend")
			require.NoError(t, os.WriteFile(path, []byte(c.lines), 0o644))
			days, err := ReadNoSpendFile(path)
			require.NoError(t, err)
			assert.Equal(t, c.want, days)
		})
	}
}

// TestCheckCoverReadNoSpendFileRefusesNonDayLine pins the refusal: a line that
// is not a calendar day is an error naming its line number and its text, so a
// half-read list never half-shuts the gate, and no set comes back.
func TestCheckCoverReadNoSpendFileRefusesNonDayLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		lines    string
		wantLine string
		wantText string
	}{
		{"not a day at all", "hello\n", "line 1", `"hello"`},
		{"day off the calendar", "2026-02-30\n", "line 1", `"2026-02-30"`},
		{"bad line after a good one", "2026-03-01\nnope\n", "line 2", `"nope"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "no-spend")
			require.NoError(t, os.WriteFile(path, []byte(c.lines), 0o644))
			days, err := ReadNoSpendFile(path)
			require.Error(t, err)
			assert.Nil(t, days, "a list that cannot be read whole answers no set")
			assert.Contains(t, err.Error(), c.wantLine, "the refusal names the line")
			assert.Contains(t, err.Error(), c.wantText, "the refusal names the text")
			assert.Contains(t, err.Error(), "is not a day", "the refusal says what the line must be")
		})
	}
}

// TestCheckCoverReadNoSpendFileRefusesMissingFile pins the open refusal: a path
// that does not exist is an error, not an empty set, so a typo in --no-spend
// does not read as a claim that every gap is missing.
func TestCheckCoverReadNoSpendFileRefusesMissingFile(t *testing.T) {
	t.Parallel()
	days, err := ReadNoSpendFile(filepath.Join(t.TempDir(), "nowhere"))
	require.Error(t, err)
	assert.Nil(t, days, "a file that cannot be opened answers no set")
	assert.Contains(t, err.Error(), "nowhere", "the refusal names the path")
}

// TestCheckCoverReadNoSpendFileRefusesDirectory pins the read refusal: a
// directory opens and then does not read, so the scanner's error is surfaced
// and no empty set stands in for the list the caller meant.
func TestCheckCoverReadNoSpendFileRefusesDirectory(t *testing.T) {
	t.Parallel()
	days, err := ReadNoSpendFile(t.TempDir())
	require.Error(t, err)
	assert.Nil(t, days, "a path that does not read answers no set")
}
