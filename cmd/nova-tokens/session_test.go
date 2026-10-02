package main

// The session verb, end to end: one SESSION line, and a day file holding the coordinator's
// own row beside whatever the other sources already wrote.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// twoTurns is a coordinator's transcript of two turns on 2026-09-11.
const twoTurns = `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","model":"claude-opus-5","usage":{"input_tokens":12,"cache_creation_input_tokens":2000,"cache_read_input_tokens":0,"output_tokens":300}}}
{"timestamp":"2026-09-11T09:01:00Z","message":{"id":"b","model":"claude-opus-5","usage":{"input_tokens":4,"cache_creation_input_tokens":500,"cache_read_input_tokens":20000,"output_tokens":120}}}
`

// otherDay is a day file another source (emma) wrote for 2026-09-11.
const otherDay = "nova-tokens v1 day=2026-09-11 at=2026-09-11T00:00:00Z build=test turns=- sources=emma\n" +
	"date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n" +
	"2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\temma\n"

// One session run per row, on a transcript of its own ($session) and an --out that does
// not exist yet ($out): a row that names no day-file text leaves no --out behind.
func TestSessionMeasuresAndRefuses(t *testing.T) {
	t.Parallel()

	const noModel = `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","usage":{"input_tokens":5,"output_tokens":6}}}` + "\n"
	for _, c := range []struct {
		name, transcript string
		args             []string
		exit             int
		stdout           string   // whole, when set
		out, err         []string // on stdout, on stderr
		day, notDay      []string // in the day file written, and not in it
	}{
		// Reading is not writing. By hand: 16 + 1.25 x 2500 + 0.1 x 20000 + 5 x 420 =
		// 16 + 3125 + 2000 + 2100 = 7241, and the context a turn carried is
		// (16 + 2500 + 20000) / 2 = 11258.
		{name: "one line and nothing folded without --out", transcript: twoTurns, args: []string{"--claude-session", "$session"},
			stdout: "SESSION turns=2 input=16 cache_write=2500 cache_read=20000 output=420 weighted=7241 avg_context=11258\n"},
		// No path is guessed, and a day this tool cannot read is a refusal and not a guess.
		{name: "refuses without the session", exit: 2, err: []string{"--claude-session is required", "refusing to guess"}},
		{name: "refuses a bad day", transcript: twoTurns, args: []string{"--claude-session", "$session", "--day", "yesterday"}, exit: 2, err: []string{"--day wants one UTC day"}},
		{name: "refuses a transcript it cannot read", args: []string{"--claude-session", "$session"}, exit: 2, err: []string{"cannot read"}},
		// A transcript of another model is booked as that model's coordinator row, never
		// under a fixed label (a cold rating of the tools, 2026-09-30: `session --out` folded
		// a transcript naming one model under another's coordinator label).
		{name: "books the model the transcript names",
			transcript: `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","model":"some-other-model-7","usage":{"input_tokens":5,"output_tokens":6}}}` + "\n",
			args:       []string{"--claude-session", "$session", "--out", "$out"}, out: []string{"model=some-other-model-7/coordinator"},
			day: []string{"2026-09-11\tsome-other-model-7/coordinator\tcoordinator\t5\t6\t0\t0"}, notDay: []string{"fable"}},
		// A transcript that names no model: with --out the verb refuses, names the reason and
		// writes nothing; without --out it still measures.
		{name: "refuses to fold a transcript naming no model", transcript: noModel, args: []string{"--claude-session", "$session", "--out", "$out"}, exit: 2, err: []string{"names no model"}},
		{name: "measures a transcript naming no model", transcript: noModel, args: []string{"--claude-session", "$session"}, out: []string{"SESSION turns=1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			out := filepath.Join(dir, "out")
			fill := strings.NewReplacer("$session", filepath.Join(dir, "session.jsonl"), "$out", out)
			if c.transcript != "" {
				testkit.WriteFile(t, filepath.Join(dir, "session.jsonl"), c.transcript)
			}
			args := []string{"session"}
			for _, a := range c.args {
				args = append(args, fill.Replace(a))
			}
			r := novaTokens.Do(t, args...).Exit(c.exit).Out(c.out...).Err(c.err...)
			if c.stdout != "" {
				assert.Equal(t, c.stdout, r.Stdout)
			}
			if len(c.day) == 0 {
				assert.NoDirExists(t, out, "a run with nothing to fold made its --out")
				return
			}
			day := testkit.ReadFile(t, filepath.Join(out, "2026-09-11.tsv"))
			for _, s := range c.day {
				assert.Contains(t, day, s)
			}
			for _, s := range c.notDay {
				assert.NotContains(t, day, s, "a fixed coordinator label was booked")
			}
		})
	}
}

// TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows: the coordinator arrives as its own
// model line, and the row another source wrote is still there, cell for cell. The mutation
// that matters: a fold that recomputes the file whole and erases what it did not read.
func TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows(t *testing.T) {
	t.Parallel()

	t.Run("beside another source's row", func(t *testing.T) {
		t.Parallel()
		out := t.TempDir()
		testkit.WriteFile(t, filepath.Join(out, "2026-09-11.tsv"), otherDay)
		session := testkit.WriteFile(t, filepath.Join(t.TempDir(), "session.jsonl"), twoTurns)
		novaTokens.Do(t, "session", "--claude-session", session, "--out", out).Exit(0).Out("TOKENS DAY day=2026-09-11 written=true rows=2 retained=1", "model=claude-opus-5/coordinator")
		body := testkit.ReadFile(t, filepath.Join(out, "2026-09-11.tsv"))
		assert.Contains(t, body, "2026-09-11\tclaude-opus-5/coordinator\tcoordinator\t16\t420\t2500\t20000\t-\t0\tutc\tclaude-session")
		assert.Contains(t, body, "2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\temma")
		assert.Contains(t, body, "turns=2")
		assert.Contains(t, strings.SplitN(body, "\n", 2)[0], "sources=claude-session,emma", "the version line names both sources")

		// Folding the same session again is the same file: a fold replaces its own rows and
		// never adds to them.
		novaTokens.Do(t, "session", "--claude-session", session, "--out", out).Exit(0)
		again := testkit.ReadFile(t, filepath.Join(out, "2026-09-11.tsv"))
		assert.Equal(t, 1, strings.Count(again, "claude-opus-5/coordinator"), "a second fold wrote a second coordinator row:\n%s", again)
	})
	// An unreadable existing day file in a writable output directory must not be merged
	// against empty state or overwritten: session refuses and writes nothing. Absence
	// (ENOENT) is intentionally allowed as a fresh day; an unreadable existing file is not.
	t.Run("refuses an unreadable day file", func(t *testing.T) {
		t.Parallel()
		out := t.TempDir()
		day := testkit.WriteFile(t, filepath.Join(out, "2026-09-11.tsv"), otherDay)
		session := testkit.WriteFile(t, filepath.Join(t.TempDir(), "session.jsonl"), twoTurns)
		release := makeUnreadable(t, day)
		novaTokens.Do(t, "session", "--claude-session", session, "--out", out).Exit(1).Err("TOKENS REFUSED: cannot read", "2026-09-11.tsv")
		release()
		require.Equal(t, otherDay, testkit.ReadFile(t, day), "the unreadable day file was modified")
	})
}

// TestTheHelpSessionExampleIsWhatItPrints runs the help's session line as a reader pastes
// it after the setup line (./session.jsonl is the bench's own window, ./out its day files)
// and compares it, line for line, with what the tool prints.
func TestTheHelpSessionExampleIsWhatItPrints(t *testing.T) {
	t.Parallel()

	line := "nova-tokens session --claude-session ./session.jsonl --out ./out"
	examples, err := onboarding.ExampleLines(usage, "nova-tokens")
	require.NoError(t, err)
	assert.Contains(t, examples, line)
	bench := exampleBench(t)
	testkit.WriteFile(t, filepath.Join(bench, "session.jsonl"), testkit.ReadFile(t, filepath.Join(bench, "transcripts", "window.jsonl")))
	r := novaTokens.Do(t, pasted(bench, line)...)
	step := onboarding.Step{Line: "$ " + line, Want: []string{
		"SESSION turns=3 input=1338 cache_write=1200 cache_read=246000 output=1593 weighted=35403 avg_context=82846",
		"TOKENS DAY day=2026-09-11 written=true rows=1 retained=0 model=claude-fable-5-1/coordinator weighted=35403",
	}}
	assert.Empty(t, onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{onboarding.Result(r.Result)}, nil), "the help's session example")
}
