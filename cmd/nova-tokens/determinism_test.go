package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Rules 16 and 18, the halves that are about what does NOT decide an answer: the checkout's
// mtimes, its directory order, its INDEX and its git history. The order of two competing
// notes is in the notes themselves and nowhere else, so a fold must be byte-identical
// under every one of those rearranged.

// fakeGit puts a git on PATH that records every invocation, so a test can assert that this
// tool ran none. A fold that fetched would be a fold whose numbers depend on a network call.
func fakeGit(t *testing.T) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "git-argv.log")
	bin := mkdir(t, filepath.Join(dir, "bin"))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> "+logPath+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// reversedHistory makes dir a git repository whose commits land in the order given, using
// the real git found BEFORE the fake one went on PATH. It runs entirely inside t.TempDir()
// with no network and no global config: -c flags carry the identity, and the fixture never
// touches the caller's git.
func reversedHistory(t *testing.T, git, dir string, files ...string) {
	t.Helper()
	if git == "" {
		t.Skip("no git on PATH; the reversed-history fixture wants one")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{
			"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
			"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-C", dir,
		}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_DATE=2026-09-11T20:00:00Z", "GIT_COMMITTER_DATE=2026-09-11T20:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	for i, f := range files {
		run("add", "--", f)
		run("commit", "-q", "-m", fmt.Sprintf("commit %d: %s", i+1, f))
	}
}

func TestNothingAboutTheCheckoutDecidesWhichNoteIsTheDay(t *testing.T) {
	// The real git, resolved before the fake one goes on PATH: the fixture's history is
	// built with it, and the tool must still never run git.
	realGit, _ := exec.LookPath("git")
	gitLog := fakeGit(t)
	dir := t.TempDir()
	repos := reposFile(t, dir)
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	line := func(n string) string { return "2026-09-11\temma\tg\tschema\tinput\t" + n + "\n" }
	// The successor's filename sorts BEFORE its predecessor's, and (below) its mtime is
	// older and its position in a rebuilt INDEX is earlier. None of that may matter.
	first := busNote(t, bus, "emma", "zzz-first.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("100"))
	second := busNote(t, bus, "emma", "aaa-second.md", "emma-000000000002",
		"tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes="+first, busDate, line("250"))
	_ = second

	fold := func(t *testing.T, out string) string {
		t.Helper()
		r := invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--bus", bus)
		wantExit(t, r, 0)
		wantContains(t, r.stdout, "TOKENS SUPERSEDED")
		return read(t, filepath.Join(out, "2026-09-11.tsv"))
	}
	before := fold(t, mkdir(t, filepath.Join(dir, "out1")))
	wantContains(t, before, "\t250\t")

	// The mtimes swapped, so the successor looks older than what it replaces.
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(bus, "from-emma", "aaa-second.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(bus, "from-emma", "zzz-first.md"), newer, newer); err != nil {
		t.Fatal(err)
	}
	// An INDEX sorted by path, the way `nova-bus check --rebuild-index` writes it: a
	// derived catalogue, and never a statement about which number the friend meant.
	write(t, filepath.Join(bus, "INDEX.md"), "- from-emma/aaa-second.md\n- from-emma/zzz-first.md\n")
	// And a REAL git history in the checkout, whose commit order is the opposite of the
	// send order: the successor is committed first. Demanded test 6 asks for "the notes'
	// commit order reversed in a fixture git history"; an empty .git directory was not
	// one, and nothing about a checkout may decide which note is the day.
	reversedHistory(t, realGit, bus, "from-emma/aaa-second.md", "from-emma/zzz-first.md")

	after := fold(t, mkdir(t, filepath.Join(dir, "out2")))
	a := strings.SplitN(before, "\n", 2)[1]
	b := strings.SplitN(after, "\n", 2)[1]
	if a != b {
		t.Errorf("the rows moved when the checkout was rearranged:\n%s\n%s", a, b)
	}
	if _, err := os.Stat(gitLog); err == nil {
		t.Errorf("git was invoked: %s", read(t, gitLog))
	}
}

// Rule 20 and demanded test 20's last third: a provider's local-day total crosses the bus
// as a seventh field, and folds back to the same row it would have folded from the export.
func TestAZonedReportCrossesTheBusWithoutBeingCalledUTC(t *testing.T) {
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
		if f := strings.Split(line, "\t"); len(f) != 7 || f[6] != "day_basis=America/Los_Angeles" {
			t.Errorf("a zoned report line is %q; it wants seven fields ending day_basis=<zone>", line)
		}
	}
	subject := lineWith(r.stderr, "REPORT OK")
	subject = subject[strings.Index(subject, "subject=")+len("subject="):]

	// The note folds to the same rows the export folds to directly.
	viaBus := mkdir(t, filepath.Join(dir, "out-bus"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "johnny")
	busNote(t, bus, "johnny", "n.md", "johnny-000000000001", subject, busDate, read(t, note))
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
	if got != want {
		t.Errorf("the note's row is not the export's row:\nbus:    %s\ndirect: %s", got, want)
	}
	// A hand-written seventh field that spells utc, one that is not day_basis=, and an
	// eighth field are each unparsed: two spellings of one fact would be two grammars.
	for _, bad := range []string{
		"2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tday_basis=utc",
		"2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tzone=America/Los_Angeles",
		"2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tday_basis=X\textra",
	} {
		one := mkdir(t, filepath.Join(dir, "b"+string(rune('a'+len(bad)%26))))
		lane := busDir(t, mkdir(t, filepath.Join(one, "bus")), "johnny")
		busNote(t, lane, "johnny", "n.md", "johnny-000000000002", "tokens 2026-09-11", busDate, bad+"\n")
		o := mkdir(t, filepath.Join(one, "out"))
		r := invoke(t, "fold", "--out", o, "--day", "2026-09-11", "--repos", repos, "--bus", lane)
		wantExit(t, r, 1)
		wantContains(t, r.stderr, "TOKENS UNPARSED")
	}
}

// Rule 5's refusal half at the binary: a malformed rules line is exit 2 naming the line.
func TestAMalformedRulesFileIsRefusedByLine(t *testing.T) {
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
	r := invoke(t, "version")
	wantExit(t, r, 0)
	if n := len(strings.Fields(strings.TrimSpace(r.stdout))); n != 4 {
		t.Errorf("`nova-tokens version` printed %q, want four tokens", r.stdout)
	}
	wantContains(t, r.stdout, "nova-tokens ")
	wantExit(t, invoke(t, "version", "--anything"), 2)
}

// Demanded test 3's other half: a line that is not JSON is counted inside this file's
// unreadable accounting, and the file continues.
func TestANonJSONLineIsCountedAndTheFileContinues(t *testing.T) {
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
