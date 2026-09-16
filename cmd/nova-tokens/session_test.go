package main

// The session verb, end to end: one SESSION line, and a day file holding the coordinator's
// own row beside whatever the other sources already wrote.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"timestamp":"2026-09-11T09:00:00Z","message":{"id":"a","model":"claude-opus-5","usage":{"input_tokens":12,"cache_creation_input_tokens":2000,"cache_read_input_tokens":0,"output_tokens":300}}}
{"timestamp":"2026-09-11T09:01:00Z","message":{"id":"b","model":"claude-opus-5","usage":{"input_tokens":4,"cache_creation_input_tokens":500,"cache_read_input_tokens":20000,"output_tokens":120}}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSessionPrintsOneLineAndFoldsNothingWithoutOut: reading is not writing. With no --out
// the verb measures and says so, and the ledger is untouched.
func TestSessionPrintsOneLineAndFoldsNothingWithoutOut(t *testing.T) {
	r := invoke(t, "session", "--claude-session", writeSession(t))
	wantExit(t, r, 0)
	// By hand: 16 + 1.25 x 2500 + 0.1 x 20000 + 5 x 420 = 16 + 3125 + 2000 + 2100 = 7241,
	// and the context a turn carried is (16 + 2500 + 20000) / 2 = 11258.
	want := "SESSION turns=2 input=16 cache_write=2500 cache_read=20000 output=420 weighted=7241 avg_context=11258\n"
	if r.stdout != want {
		t.Errorf("stdout is\n  %q\nwant\n  %q", r.stdout, want)
	}
}

// TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows: the coordinator arrives as its own
// model line, and the row another source wrote is still there, cell for cell. The mutation
// that matters: a fold that recomputes the file whole and erases what it did not read.
func TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows(t *testing.T) {
	out := t.TempDir()
	existing := "nova-tokens v1 day=2026-09-11 at=2026-09-11T00:00:00Z build=test turns=- sources=emma\n" +
		"date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n" +
		"2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\temma\n"
	if err := os.WriteFile(filepath.Join(out, "2026-09-11.tsv"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "session", "--claude-session", writeSession(t), "--out", out)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS DAY day=2026-09-11 written=true rows=2 retained=1")
	wantContains(t, r.stdout, "model=claude-fable-5-1/coordinator")

	raw, err := os.ReadFile(filepath.Join(out, "2026-09-11.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	wantContains(t, body, "2026-09-11\tclaude-fable-5-1/coordinator\tcoordinator\t16\t420\t2500\t20000\t-\t0\tutc\tclaude-session")
	wantContains(t, body, "2026-09-11\tdeepseek-v4-flash\tnova-tools\t100\t10\t-\t-\t-\t0\tutc\temma")
	wantContains(t, body, "turns=2")
	if !strings.Contains(body, "sources=claude-session,emma") {
		t.Errorf("the version line does not name both sources:\n%s", strings.SplitN(body, "\n", 2)[0])
	}

	// Folding the same session again is the same file: a fold replaces its own rows and
	// never adds to them.
	again := invoke(t, "session", "--claude-session", writeSession(t), "--out", out)
	wantExit(t, again, 0)
	raw2, _ := os.ReadFile(filepath.Join(out, "2026-09-11.tsv"))
	if strings.Count(string(raw2), "claude-fable-5-1/coordinator") != 1 {
		t.Errorf("a second fold wrote a second coordinator row:\n%s", raw2)
	}
}

// TestSessionRefusesWithoutTheSessionAndOnABadDay: no path is guessed, and a day this tool
// cannot read is a refusal and not a guess.
func TestSessionRefusesWithoutTheSessionAndOnABadDay(t *testing.T) {
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
	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "nova-tokens session --claude-session <jsonl>")
	wantContains(t, r.stdout, "claude-fable-5-1/coordinator")
}
