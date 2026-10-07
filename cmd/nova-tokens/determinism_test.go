package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// fakeGit puts a git on PATH that records every invocation, so a test can assert that this
// tool ran none. A fold that fetched would be a fold whose numbers depend on a network call.
// fakeGit puts a git on PATH that records every invocation, so a test can assert that this
// tool ran none. A fold that fetched would be a fold whose numbers depend on a network call.
// It returns the log path and the environment (PATH) the run under test needs; the caller
// hands the environment to runToolChild, so the fake is on the child's PATH rather than this
// process's, which its parallel neighbours share.
func fakeGit(t *testing.T) (logPath string, env []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "git-argv.log")
	bin := mkdir(t, filepath.Join(dir, "bin"))
	{
		err := testbin.WriteExecutable(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> "+logPath+"\nexit 0\n"), 0o755)
		require.NoError(t, err, err)
	}
	return logPath, []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
}

// (The name is the git bus's, kept because the serial ledger lists it: the checkout is gone, and
// what this pins is that the order of the log decides nothing.)
func TestNothingAboutTheCheckoutDecidesWhichNoteIsTheDay(t *testing.T) {
	t.Parallel()

	// The tool runs no git, whatever PATH holds; a fake one records any run.
	gitLog, _ := fakeGit(t)
	dir := t.TempDir()
	repos := reposFile(t, dir)
	line := func(n string) string { return "2026-09-11\temma\tg\tschema\tinput\t" + n + "\n" }
	const first = "01EMMA00000000000000000001"
	// The successor is on the log BEFORE its predecessor, and its id sorts after: the
	// order of two competing notes is in the notes (supersedes=) and nowhere else.
	later := func(addr string) {
		busNote(t, addr, "emma", "", "01EMMA00000000000000000002",
			"tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes="+first, busDate, line("250"))
		busNote(t, addr, "emma", "", first, "tokens 2026-09-11", busDate, line("100"))
	}
	inOrder := func(addr string) {
		busNote(t, addr, "emma", "", first, "tokens 2026-09-11", busDate, line("100"))
		busNote(t, addr, "emma", "", "01EMMA00000000000000000002",
			"tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes="+first, busDate, line("250"))
	}
	fold := func(t *testing.T, name string, put func(string)) string {
		t.Helper()
		addr := busDir(t, mkdir(t, filepath.Join(dir, name+"-bus")), "emma")
		put(addr)
		out := mkdir(t, filepath.Join(dir, name+"-out"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--bus", addr)
		wantExit(t, r, 0)
		wantContains(t, r.stdout, "TOKENS SUPERSEDED")
		return read(t, filepath.Join(out, "2026-09-11.tsv"))
	}
	before := fold(t, "a", inOrder)
	wantContains(t, before, "\t250\t")
	after := fold(t, "b", later)
	a := strings.SplitN(before, "\n", 2)[1]
	b := strings.SplitN(after, "\n", 2)[1]
	assert.Equal(t, b, a, "the rows moved when the log's order was reversed:\n%s\n%s", a, b)
	{
		_, err := os.Stat(gitLog)
		if err == nil {
			assert.Failf(t, "git was invoked", "git was invoked: %s", read(t, gitLog))
		}
	}
}

// Rule 20 and demanded test 20's last third: a provider's local-day total crosses the bus
// as a seventh field, and folds back to the same row it would have folded from the export.
func TestAZonedReportCrossesTheBusWithoutBeingCalledUTC(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := reposFile(t, dir)
	export := write(t, filepath.Join(dir, "xai.csv"), strings.Join([]string{
		"# timezone: America/Los_Angeles",
		"date,model,input,output",
		"2026-09-11,grok-4,900,80",
		"",
	}, "\n"))
	note := filepath.Join(dir, "note.txt")
	r := invoke(t, "report", "--who", "johnny", "--day", "2026-09-11", "--repos", repos,
		"--provider", "xai:johnny="+export, "--note", note)
	wantExit(t, r, 0)
	for _, line := range lines(r.stdout) {
		{
			f := strings.Split(line, "\t")
			assert.True(t, len(f) == 7 && f[6] == "day_basis=America/Los_Angeles", "a zoned report line is %q; it wants seven fields ending day_basis=<zone>", line)
		}
	}
	subject := subjectOf(t, r)

	// The note folds to the same rows the export folds to directly.
	viaBus := mkdir(t, filepath.Join(dir, "out-bus"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "johnny")
	busNote(t, bus, "johnny", "n.md", "01J0HN00000000000000000001", subject, busDate, read(t, note))
	f := invoke(t, "fold", "--out", viaBus, "--day", "2026-09-11", "--repos", repos, "--bus", bus)
	wantExit(t, f, 0)
	wantContains(t, lineWith(f.stdout, "TOKENS SOURCE"), "day_basis=America/Los_Angeles")
	wantContains(t, lineWith(f.stdout, "TOKENS DAY"), "nonutc=1")

	direct := mkdir(t, filepath.Join(dir, "out-direct"))
	wantExit(t, invoke(t, "fold", "--out", direct, "--day", "2026-09-11", "--repos", repos, "--provider", "xai:johnny="+export), 0)

	cells := func(text string) string {
		var keep []string
		for _, l := range strings.Split(strings.TrimSpace(text), "\n")[2:] {
			c := strings.Split(l, "\t")
			keep = append(keep, strings.Join(c[1:10], "\t")) // everything but date and sources
		}
		return strings.Join(keep, "\n")
	}
	got, want := cells(read(t, filepath.Join(viaBus, "2026-09-11.tsv"))), cells(read(t, filepath.Join(direct, "2026-09-11.tsv")))
	assert.Equal(t, want, got, "the note's row is not the export's row:\nbus:    %s\ndirect: %s", got, want)
	// A hand-written seventh field that spells utc, one that is not day_basis=, and an
	// eighth field are each unparsed: two spellings of one fact would be two grammars.
	for _, bad := range []string{
		"2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tday_basis=utc",
		"2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tzone=America/Los_Angeles",
		"2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tday_basis=X\textra",
	} {
		one := mkdir(t, filepath.Join(dir, "b"+string(rune('a'+len(bad)%26))))
		lane := busDir(t, mkdir(t, filepath.Join(one, "bus")), "johnny")
		busNote(t, lane, "johnny", "n.md", "01J0HN00000000000000000002", "tokens 2026-09-11", busDate, bad+"\n")
		o := mkdir(t, filepath.Join(one, "out"))
		r := invoke(t, "fold", "--out", o, "--day", "2026-09-11", "--repos", repos, "--bus", lane)
		wantExit(t, r, 1)
		wantContains(t, r.stderr, "TOKENS UNPARSED")
	}
}

// Rule 5's refusal half at the binary: a malformed rules line is exit 2 naming the line.
func TestAMalformedRulesFileIsRefusedByLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1})+"\n")
	bad := write(t, filepath.Join(dir, "bad-repos.tsv"), "schema\t(^|/)schema($|/)\nthis line has no tab\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", bad, "--claude", "g="+tr)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "line 2")
	wantContains(t, r.stderr, "TOKENS REFUSED")
	missing := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", filepath.Join(dir, "nope.tsv"), "--claude", "g="+tr)
	wantExit(t, missing, 2)
	wantContains(t, missing.stderr, "it wants a file of")
}

// Rule 12's other half: the build id is compiled in, and `version` says which build wrote
// a day file. A day file whose stamp a caller could set would be a stamp nobody could trust.
func TestVersionSaysWhichBuildIsRunning(t *testing.T) {
	t.Parallel()

	r := invoke(t, "version")
	wantExit(t, r, 0)
	{
		n := len(strings.Fields(strings.TrimSpace(r.stdout)))
		assert.Equal(t, 4, n, "`nova-tokens version` printed %q, want four tokens", r.stdout)
	}
	wantContains(t, r.stdout, "nova-tokens ")
	wantExit(t, invoke(t, "version", "--anything"), 2)
}

// Demanded test 3's other half: a line that is not JSON is counted inside this file's
// unreadable accounting, and the file continues.
func TestANonJSONLineIsCountedAndTheFileContinues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 10}, "/x/schema/a.go"),
		"this line is not JSON at all",
		msg("m2", "2026-09-11T10:01:00Z", "f", map[string]int{"input_tokens": 5}, "/x/schema/a.go"),
		// A synthetic model is not a message: the harness talking to itself.
		msg("m3", "2026-09-11T10:02:00Z", "<synthetic>", map[string]int{"input_tokens": 999}, "/x/schema/a.go"),
	}, "\n")+"\n")
	// The other transcript shape, read the same way.
	write(t, filepath.Join(tr, "child.output"), msg("m4", "2026-09-11T10:03:00Z", "f", map[string]int{"input_tokens": 7}, "/x/schema/a.go")+"\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "badline=1")
	wantContains(t, r.stdout, "files=2 unreadable=1")
	wantContains(t, r.stdout, "messages=3")
	// Both files were read whole and the synthetic line was not counted: 10+5+7.
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "f\tschema\t22\t")
}
