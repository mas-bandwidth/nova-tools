package main

// The session verb, end to end: one SESSION line, and a day file holding the window's own
// row beside whatever the other sources already wrote.

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeSession(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","model":"claude-opus-5","usage":{"input_tokens":12,"cache_creation_input_tokens":2000,"cache_read_input_tokens":0,"output_tokens":300}}}
{"timestamp":"2026-09-11T09:01:00Z","message":{"id":"b","model":"claude-opus-5","usage":{"input_tokens":4,"cache_creation_input_tokens":500,"cache_read_input_tokens":20000,"output_tokens":120}}}
`
	{
		err := os.WriteFile(path, []byte(body), 0o644)
		require.NoError(t, err, err)
	}
	return path
}

// TestSessionPrintsOneLineAndFoldsNothingWithoutOut: reading is not writing. With no --out
// the verb measures and says so, and the ledger is untouched.
func TestSessionPrintsOneLineAndFoldsNothingWithoutOut(t *testing.T) {
	t.Parallel()

	r := invoke(t, "session", "--claude-session", writeSession(t))
	wantExit(t, r, 0)
	// By hand: 16 + 1.25 x 2500 + 0.1 x 20000 + 5 x 420 = 16 + 3125 + 2000 + 2100 = 7241,
	// and the context a turn carried is (16 + 2500 + 20000) / 2 = 11258.
	want := "SESSION turns=2 input=16 cache_write=2500 cache_read=20000 output=420 weighted=7241 avg_context=11258\n"
	assert.Equal(t, want, r.stdout, "stdout is\n  %q\nwant\n  %q", r.stdout, want)
}

// TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows: the window arrives as its own model
// line, and the row another source wrote is still there, cell for cell. The mutation that
// matters: a fold that recomputes the file whole and erases what it did not read.
func TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	existing := "nova-tokens v1 day=2026-09-11 at=2026-09-11T00:00:00Z build=test turns=- sources=emma\n" +
		"date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n" +
		"2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\temma\n"
	{
		err := os.WriteFile(filepath.Join(out, "2026-09-11.tsv"), []byte(existing), 0o644)
		require.NoError(t, err, err)
	}

	r := invoke(t, "session", "--claude-session", writeSession(t), "--out", out)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS DAY day=2026-09-11 written=true rows=2 retained=1")
	wantContains(t, r.stdout, "model=claude-opus-5 weighted=7241")

	raw, err := os.ReadFile(filepath.Join(out, "2026-09-11.tsv"))
	require.NoError(t, err, err)
	body := string(raw)
	wantContains(t, body, "2026-09-11\tclaude-opus-5\tunattributed\t16\t420\t2500\t20000\t-\t0\tutc\tclaude-session")
	wantContains(t, body, "2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\temma")
	wantContains(t, body, "turns=2")
	assert.True(t, strings.Contains(body, "sources=claude-session,emma"), "the version line does not name both sources:\n%s", strings.SplitN(body, "\n", 2)[0])

	// Folding the same session again is the same file: a fold replaces its own rows and
	// never adds to them.
	again := invoke(t, "session", "--claude-session", writeSession(t), "--out", out)
	wantExit(t, again, 0)
	raw2, _ := os.ReadFile(filepath.Join(out, "2026-09-11.tsv"))
	assert.Equal(t, 1, strings.Count(string(raw2), "claude-opus-5\tunattributed"), "a second fold wrote a second session row:\n%s", raw2)
}

// TestSessionRefusesWithoutTheSessionAndOnABadDay: no path is guessed, and a day this tool
// cannot read is a refusal and not a guess.
func TestSessionRefusesWithoutTheSessionAndOnABadDay(t *testing.T) {
	t.Parallel()

	r := invoke(t, "session")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--claude-session is required")
	wantContains(t, r.stderr, "refusing to guess")

	r = invoke(t, "session", "--claude-session", writeSession(t), "--day", "yesterday")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--day wants one UTC day")

	r = invoke(t, "session", "--claude-session", filepath.Join(t.TempDir(), "nope.jsonl"))
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "cannot read")
}

// TestHelpNamesTheSessionVerb: a verb a reader cannot find is a verb behind the source.
func TestHelpNamesTheSessionVerb(t *testing.T) {
	t.Parallel()

	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "nova-tokens session --claude-session <jsonl>")

	h := invoke(t, "session", "-h")
	wantExit(t, h, 0)
	wantContains(t, h.stdout, "a comparison, not a price: the defaults are the ratios of one vendor's published list prices; set your own")
}

// TestSessionBooksTheModelTheTranscriptNames: a transcript of another model is booked as that
// model's row, never under a fixed label (a cold rating of the tools, 2026-09-30:
// `session --out` folded a transcript naming one model under another's label).
func TestSessionBooksTheModelTheTranscriptNames(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","model":"some-other-model-7","usage":{"input_tokens":5,"output_tokens":6}}}
`
	{
		err := os.WriteFile(path, []byte(body), 0o644)
		require.NoError(t, err, err)
	}
	out := t.TempDir()
	r := invoke(t, "session", "--claude-session", path, "--out", out)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "model=some-other-model-7")
	raw, err := os.ReadFile(filepath.Join(out, "2026-09-11.tsv"))
	require.NoError(t, err, err)
	wantContains(t, string(raw), "2026-09-11\tsome-other-model-7\tunattributed\t5\t6\t0\t0")
	assert.False(t, strings.Contains(string(raw), "fable"), "a fixed label was booked:\n%s", raw)
}

// TestSessionRefusesATranscriptThatNamesNoModel: with --out the verb refuses, names the
// reason and writes nothing; without --out it still measures.
func TestSessionRefusesATranscriptThatNamesNoModel(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","usage":{"input_tokens":5,"output_tokens":6}}}
`
	{
		err := os.WriteFile(path, []byte(body), 0o644)
		require.NoError(t, err, err)
	}
	out := filepath.Join(t.TempDir(), "out")
	r := invoke(t, "session", "--claude-session", path, "--out", out)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "names no model")
	{
		_, err := os.Stat(out)
		assert.False(t, err == nil, "a refused fold created %s", out)
	}

	r = invoke(t, "session", "--claude-session", path)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "SESSION turns=1")
}

// TestSessionRefusesUnreadableDayFile: an unreadable existing day file in a writable output
// directory must not be merged against empty state or overwritten; session must refuse and
// write nothing. Absence (ENOENT) is intentionally allowed as a fresh day; an unreadable
// existing file must be refused.
func TestSessionRefusesUnreadableDayFile(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	existing := "nova-tokens v1 day=2026-09-11 at=2026-09-11T00:00:00Z build=test turns=- sources=other-source\n" +
		"date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n" +
		"2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\tother-source\n"
	dayPath := filepath.Join(out, "2026-09-11.tsv")
	require.NoError(t, os.WriteFile(dayPath, []byte(existing), 0o644))

	release := makeUnreadable(t, dayPath)
	r := invoke(t, "session", "--claude-session", writeSession(t), "--out", out)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS REFUSED: cannot read")
	wantContains(t, r.stderr, "2026-09-11.tsv")

	release()
	raw, err := os.ReadFile(dayPath)
	require.NoError(t, err)
	require.Equal(t, existing, string(raw), "unreadable day file was modified:\ngot:\n%s\nwant:\n%s", string(raw), existing)
}

// TestSessionRoleAndWeightsAreFlags: the ledger row and the WEIGHTED ratios are the
// caller's flags and no fleet of the tool's (docs/STANDARD.md section 4). The default
// books the bare model at the ratios the four constants carried before they became
// --weights; --role books <model>/<role>; --weights takes four numbers in,cw,cr,out and
// refuses a value that is not.
func TestSessionRoleAndWeightsAreFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		args     []string
		wantExit int
		wantOut  string
		wantRow  string
	}{
		{
			// 16 + 1.25 x 2500 + 0.1 x 20000 + 5 x 420 = 7241, the number the
			// constants carried, from the flag's default and not from a constant.
			name:    "no role books the bare model at the default weights",
			wantOut: "SESSION turns=2 input=16 cache_write=2500 cache_read=20000 output=420 weighted=7241 avg_context=11258",
			wantRow: "2026-09-11\tclaude-opus-5\tunattributed\t16\t420\t2500\t20000\t-\t0\tutc\tclaude-session",
		},
		{
			name:    "a role books <model>/<role> with the role in the repo cell",
			args:    []string{"--role", "keeper"},
			wantOut: "model=claude-opus-5/keeper weighted=7241",
			wantRow: "2026-09-11\tclaude-opus-5/keeper\tkeeper\t16\t420\t2500\t20000\t-\t0\tutc\tclaude-session",
		},
		{
			name:    "the default weights reproduce the number the constants carried",
			wantOut: "weighted=7241",
		},
		{
			// 16 + 2500 + 20000 + 420 = 22936: the counts themselves, so a
			// weight the flag did not carry into the sum turns this row red.
			name:    "custom weights change the number",
			args:    []string{"--weights", "1,1,1,1"},
			wantOut: "weighted=22936",
		},
		{
			name:     "weights that are not four numbers are refused",
			args:     []string{"--weights", "1,2,3"},
			wantExit: 2,
			wantOut:  "--weights wants four comma-separated numbers in,cw,cr,out",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			args := append([]string{"session", "--claude-session", writeSession(t), "--out", out}, tc.args...)
			r := invoke(t, args...)
			wantExit(t, r, tc.wantExit)
			if tc.wantOut != "" {
				wantContains(t, r.all(), tc.wantOut)
			}
			if tc.wantRow != "" {
				raw, err := os.ReadFile(filepath.Join(out, "2026-09-11.tsv"))
				require.NoError(t, err, err)
				wantContains(t, string(raw), tc.wantRow)
			}
		})
	}
}

// TestNoFleetNameInHelpOrOutput: the help this tool prints names no machine, person,
// organisation or home path of one fleet (docs/STANDARD.md section 4). The list is the
// generality class test's token inventory (internal/ci/generality_class_test.go); a word
// is a finding whole, the way that class test scans, so an English word that merely holds
// a name ("whitespace", "minimum") is not one.
func TestNoFleetNameInHelpOrOutput(t *testing.T) {
	t.Parallel()

	fleet := map[string]bool{
		"alex": true, "antman": true, "batman": true, "captainamerica": true,
		"emma": true, "freddy": true, "glenn": true, "hetzner": true, "hulk": true,
		"johnny": true, "macbook": true, "mini": true, "rowan": true, "space": true,
		"spacegame": true, "stella": true, "studio": true, "superman": true, "vision": true,
	}
	words := regexp.MustCompile(`[a-zA-Z0-9]+`)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"help", []string{"help"}},
		{"fold -h", []string{"fold", "-h"}},
		{"report -h", []string{"report", "-h"}},
		{"ledger -h", []string{"ledger", "-h"}},
		{"sum -h", []string{"sum", "-h"}},
		{"check -h", []string{"check", "-h"}},
		{"sources -h", []string{"sources", "-h"}},
		{"profiles -h", []string{"profiles", "-h"}},
		{"session -h", []string{"session", "-h"}},
		{"version -h", []string{"version", "-h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := invoke(t, tc.args...)
			wantExit(t, r, 0)
			text := r.all()
			assert.NotContains(t, strings.ToLower(text), "mas-bandwidth", "the help names the organisation:\n%s", text)
			for _, w := range words.FindAllString(text, -1) {
				assert.Falsef(t, fleet[strings.ToLower(w)], "the help names the fleet word %q (docs/STANDARD.md section 4):\n%s", w, text)
			}
		})
	}
}
