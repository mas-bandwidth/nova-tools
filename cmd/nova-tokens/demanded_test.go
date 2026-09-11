package main

// The tests SPEC-TOKENS.md demands, one function per numbered rule. Each runs inside
// t.TempDir(), against a fake sqlite3 on PATH where the OpenCode source is involved, with
// no network, and each was seen red before the code under it existed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- rule 1: every path is a flag

func TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted(t *testing.T) {
	dir := t.TempDir()
	// A complete, valid set of sources sitting under every variable a tool might reach for.
	bait := mkdir(t, filepath.Join(dir, "bait"))
	write(t, filepath.Join(bait, "t", "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 5}, "/x/schema/a.go")+"\n")
	reposFile(t, bait)
	t.Setenv("HOME", bait)
	t.Setenv("TMPDIR", bait)
	t.Setenv("XDG_DATA_HOME", bait)

	r := invoke(t, "fold", "--day", "2026-09-11")
	wantExit(t, r, 2)
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want three refusal lines, one per independent problem, got %d:\n%s", len(lines), r.stderr)
	}
	for i, want := range []string{"--out", "--repos", "source"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("refusal line %d is %q, want the one about %s (the order is fixed)", i+1, lines[i], want)
		}
		if !strings.HasPrefix(lines[i], "TOKENS REFUSED: ") {
			t.Errorf("refusal line %d does not open TOKENS REFUSED: %q", i+1, lines[i])
		}
	}
	// What it WANTS, not only what was wrong.
	wantContains(t, r.stderr, "refusing to guess")
	wantContains(t, r.stderr, "--claude <label>=<dir>")
	// Nothing under the variables was opened: no file was written anywhere.
	if ents, _ := os.ReadDir(filepath.Join(bait, "t")); len(ents) != 1 {
		t.Errorf("the bait directory changed")
	}
	if r.stdout != "" {
		t.Errorf("a refusal wrote to stdout: %q", r.stdout)
	}
}

// ---------------------------------------------------------------- rule 2: sources are declared, rows name them

func TestRule2EveryRowNamesItsSources(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 10}, "/x/schema/a.go")+"\n")
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "n1.md", "emma-000000000001", "tokens 2026-09-11", busDate,
		"2026-09-11\temma\tfable\tschema\tinput\t5\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr, "--bus", bus)
	wantExit(t, r, 0)
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	row := lineWith(day, "fable\tschema")
	if row == "" {
		t.Fatalf("no row for fable/schema:\n%s", day)
	}
	cols := strings.Split(row, "\t")
	if got := cols[len(cols)-1]; got != "bus:emma,claude:glenn" {
		t.Errorf("sources column is %q, want both labels sorted", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(day), "\n")[2:] {
		if c := strings.Split(line, "\t"); c[len(c)-1] == "" {
			t.Errorf("a row has an empty sources column: %q", line)
		}
	}

	// A duplicate label across two flags is exit 2 naming it.
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr, "--swarm", "glenn="+tr)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "glenn")
	wantContains(t, r.stderr, "REFUSED")
}

// ---------------------------------------------------------------- rule 3: an unreadable source is counted, printed, exit 1

func TestRule3AnUnreadableSourceIsCountedAndPrintedAndExitsOne(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so the refusal this pins cannot happen")
	}
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "good.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go")+"\n")
	bad := write(t, filepath.Join(tr, "bad.jsonl"), "{}\n")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS UNREADABLE label=claude:glenn")
	wantContains(t, r.stderr, "bad.jsonl")
	wantContains(t, r.stdout, "files=2 unreadable=1")
	wantContains(t, r.stderr, "TOKENS FAIL")
	wantContains(t, lineWith(r.stderr, "TOKENS FAIL"), "unreadable=1")
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err != nil {
		t.Errorf("the day file from the readable file was not written: %v", err)
	}

	// Without the unreadable file the same run is TOKENS OK, exit 0.
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS OK")
}

// ---------------------------------------------------------------- rule 4: a message is counted once, by its id

func TestRule4AMessageIsCountedOnceByItsID(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	var lines []string
	for _, n := range []int{10, 20, 30, 40, 55} {
		lines = append(lines, msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": n, "output_tokens": n * 2}, "/x/schema/a.go"))
	}
	lines = append(lines, msg("", "2026-09-11T10:05:00Z", "fable", map[string]int{"input_tokens": 999}, "/x/schema/a.go"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join(lines, "\n")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "glenn="+tr)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "dup=4")
	wantContains(t, r.stdout, "noid=1")
	row := lineWith(read(t, filepath.Join(out, "2026-09-11.tsv")), "fable\tschema")
	cols := strings.Split(row, "\t")
	if cols[3] != "55" || cols[4] != "110" {
		t.Errorf("the row carries %q/%q, want the LAST line's usage 55/110", cols[3], cols[4])
	}
}

// ---------------------------------------------------------------- rule 5: repo attribution, unknown and other

func TestRule5RepoAttributionAndTheTwoNamedBuckets(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	// unknown first (no paths and nothing before it), then schema, then a no-path message
	// that inherits schema, then one whose paths match nothing.
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m0", "2026-09-11T09:00:00Z", "fable", map[string]int{"input_tokens": 1}),
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10}, "/x/schema/a.go"),
		msg("m2", "2026-09-11T10:01:00Z", "fable", map[string]int{"input_tokens": 20}),
		msg("m3", "2026-09-11T10:02:00Z", "fable", map[string]int{"input_tokens": 69}, "/x/elsewhere/b.go"),
	}, "\n")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "glenn="+tr)
	wantExit(t, r, 0)
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	for _, want := range []string{"fable\tschema\t30", "fable\tunknown\t1", "fable\tother\t69"} {
		wantContains(t, day, want)
	}
	// 1/100 unknown, 69/100 other.
	line := lineWith(r.stdout, "TOKENS DAY")
	wantContains(t, line, "unknown=1.0%")
	wantContains(t, line, "other=69.0%")

	// A fold with no --repos is exit 2.
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--claude", "glenn="+tr)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--repos")
}

// ---------------------------------------------------------------- rule 6: the bus note

func TestRule6TheSubjectIsExactAndAnUnparsedLineIsPrinted(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-00000000000a", "Tokens 2026-09-11 (rough)", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")
	busNote(t, bus, "emma", "b.md", "emma-00000000000b", "tokens 2026-09-11 at=x", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")
	body := strings.Join([]string{
		"2026-09-11\temma\tgemini\tschema\tinput\t100",
		"2026-09-11\temma\tgemini\tschema\toutput\t20",
		"",
		"2026-09-11\temma\tgemini\tserialize\tinput\t5",
		"# folded by hand",
		"# repos: schema, serialize",
		"",
		"this is prose",
		"2026-09-11\temma\tgemini\tschema\tinput",
		"2026-09-11\temma\tgemini\t\tinput\t3",
		"",
	}, "\n")
	busNote(t, bus, "emma", "c.md", "emma-00000000000c", "tokens 2026-09-11 at=2026-09-11T23:55:02Z build=abc123", busDate, body)

	r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
	wantExit(t, r, 1)
	wantContains(t, r.stdout, "comments=2")
	wantContains(t, r.stdout, "TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema,serialize")
	if n := strings.Count(r.stderr, "TOKENS UNPARSED"); n != 3 {
		t.Errorf("%d TOKENS UNPARSED lines, want 3:\n%s", n, r.stderr)
	}
	wantContains(t, r.stderr, "note=emma-00000000000c")
	wantContains(t, r.stdout, "unparsed=3")
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	if n := strings.Count(day, "\n"); n != 4 { // version, header, two rows
		t.Errorf("want three folded rows across two repos, got:\n%s", day)
	}
	// Neither the wrong-case subject nor the one with trailing text is a tokens note.
	wantNotContains(t, r.stdout+r.stderr, "emma-00000000000a")
	wantNotContains(t, r.stdout+r.stderr, "emma-00000000000b")
}

func TestRule6ABadDateRefusesTheWholeNote(t *testing.T) {
	for _, tc := range []struct{ name, date, want string }{
		{"missing", "", "line=0"},
		{"unparseable", "yesterday", "line="},
		{"later than the fold", "Sat Sep 12 00:55:02 UTC 2026", "line="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out := mkdir(t, filepath.Join(dir, "out"))
			bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
			header := "From: Emma\nTo: Rowan\nId: emma-00000000000d\nSubject: tokens 2026-09-11\n"
			if tc.date != "" {
				header = "From: Emma\nTo: Rowan\nDate: " + tc.date + "\nId: emma-00000000000d\nSubject: tokens 2026-09-11\n"
			}
			write(t, filepath.Join(bus, "from-emma", "d.md"), header+"\n2026-09-11\temma\tg\tschema\tinput\t7\n")
			r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
			wantExit(t, r, 1)
			if n := strings.Count(r.stderr, "TOKENS UNPARSED"); n != 1 {
				t.Errorf("%d UNPARSED lines, want exactly one for the whole note:\n%s", n, r.stderr)
			}
			wantContains(t, r.stderr, tc.want)
			wantContains(t, r.stdout, "unparsed=1")
			if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err == nil {
				t.Error("a row of a note with a bad Date was folded")
			}
		})
	}
}

func TestRule6SupersedesOrdersTwoNotesAndNothingElseDoes(t *testing.T) {
	newBus := func(t *testing.T) (dir, out, bus string) {
		dir = t.TempDir()
		out = mkdir(t, filepath.Join(dir, "out"))
		bus = busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
		return
	}
	line := func(model, repo, typ, n string) string {
		return "2026-09-11\temma\t" + model + "\t" + repo + "\t" + typ + "\t" + n + "\n"
	}

	t.Run("the successor is the day", func(t *testing.T) {
		dir, out, bus := newBus(t)
		// Filenames whose lexical order OPPOSES the send order, and one Date to the second.
		busNote(t, bus, "emma", "zzz-first.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "100"))
		busNote(t, bus, "emma", "aaa-second.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "250"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 0)
		wantContains(t, r.stdout, "TOKENS SUPERSEDED label=bus:emma note=emma-000000000001 by=emma-000000000002 day=2026-09-11")
		wantContains(t, r.stdout, "superseded=1")
		wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "\t250\t")
	})

	t.Run("a chain folds to its last link", func(t *testing.T) {
		dir, out, bus := newBus(t)
		busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "100"))
		busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "250"))
		busNote(t, bus, "emma", "c.md", "emma-000000000003", "tokens 2026-09-11 at=2026-09-11T21:00:00Z build=b supersedes=emma-000000000002", busDate, line("g", "schema", "input", "300"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 0)
		if n := strings.Count(r.stdout, "TOKENS SUPERSEDED"); n != 2 {
			t.Errorf("%d SUPERSEDED lines, want 2:\n%s", n, r.stdout)
		}
		wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "\t300\t")
	})

	t.Run("two roots are a conflict that folds nothing", func(t *testing.T) {
		dir, out, bus := newBus(t)
		// A day file already on disk, which the conflict must leave byte-identical.
		busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "100"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 0)
		before := read(t, filepath.Join(out, "2026-09-11.tsv"))

		busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11", busDate, line("g", "schema", "input", "250"))
		r = invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 1)
		wantContains(t, r.stderr, "TOKENS CONFLICT label=bus:emma day=2026-09-11")
		wantContains(t, r.stderr, "supersedes=")
		wantContains(t, r.stderr, "conflict=1")
		if got := read(t, filepath.Join(out, "2026-09-11.tsv")); got != before {
			t.Errorf("the day file changed under a conflict:\nbefore:\n%s\nafter:\n%s", before, got)
		}

		// A correction naming only one tip leaves the other, and the remedy names both.
		busNote(t, bus, "emma", "c.md", "emma-000000000003", "tokens 2026-09-11 at=2026-09-11T21:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "400"))
		r = invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 1)
		conflict := lineWith(r.stderr, "TOKENS CONFLICT")
		wantContains(t, conflict, "emma-000000000002")
		wantContains(t, conflict, "emma-000000000003")

		// The replacement snapshot: one note naming BOTH tips, sorted, clears it.
		busNote(t, bus, "emma", "d.md", "emma-000000000004", "tokens 2026-09-11 at=2026-09-11T22:00:00Z build=b supersedes=emma-000000000002,emma-000000000003", busDate, line("g", "schema", "input", "900"))
		r = invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 0)
		wantContains(t, r.stdout, "conflict=0")
		// Three: the two tips the snapshot named, and the note the first correction
		// had already superseded. Every predecessor of a valid successor is one line.
		if n := strings.Count(r.stdout, "TOKENS SUPERSEDED"); n != 3 {
			t.Errorf("%d SUPERSEDED lines, want 3 (the two tips and the note already superseded):\n%s", n, r.stdout)
		}
		for _, tip := range []string{"emma-000000000002", "emma-000000000003"} {
			wantContains(t, r.stdout, "note="+tip+" by=emma-000000000004")
		}
		wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "\t900\t")
	})

	t.Run("two successors of one predecessor are a conflict", func(t *testing.T) {
		dir, out, bus := newBus(t)
		busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "100"))
		busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "250"))
		busNote(t, bus, "emma", "c.md", "emma-000000000003", "tokens 2026-09-11 at=2026-09-11T21:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "300"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 1)
		wantContains(t, r.stderr, "TOKENS CONFLICT")
		if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err == nil {
			t.Error("a conflicted lane-day wrote a file")
		}
	})

	for _, tc := range []struct{ name, trailer, want string }{
		{"a duplicate id", "supersedes=emma-000000000001,emma-000000000001", "duplicate"},
		{"an unsorted set", "supersedes=emma-000000000002,emma-000000000001", "sorted"},
		{"a missing target", "supersedes=emma-0000000000ff", "no such note"},
		{"a target in another lane", "supersedes=bo-000000000001", "another lane"},
		{"a target for another day", "supersedes=emma-000000000009", "another day"},
	} {
		t.Run(tc.name+" refuses the whole successor", func(t *testing.T) {
			dir, out, bus := newBus(t)
			busDir(t, bus, "emma", "bo")
			busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "100"))
			busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "250"))
			busNote(t, bus, "emma", "old.md", "emma-000000000009", "tokens 2026-09-10", busDate, "2026-09-10\temma\tg\tschema\tinput\t1\n")
			busNote(t, bus, "bo", "x.md", "bo-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "1"))
			busNote(t, bus, "emma", "z.md", "emma-00000000000f", "tokens 2026-09-11 at=2026-09-11T22:00:00Z build=b "+tc.trailer, busDate, line("g", "schema", "input", "900"))
			r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
			wantExit(t, r, 1)
			wantContains(t, r.stderr, "note=emma-00000000000f")
			wantContains(t, strings.ToLower(r.stderr), tc.want)
			wantNotContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "\t900\t")
		})
	}

	t.Run("a cycle refuses both", func(t *testing.T) {
		dir, out, bus := newBus(t)
		busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000002", busDate, line("g", "schema", "input", "100"))
		busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T21:00:00Z build=b supersedes=emma-000000000001", busDate, line("g", "schema", "input", "250"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 1)
		wantContains(t, strings.ToLower(r.stderr), "cycle")
	})

	t.Run("a lone note folds", func(t *testing.T) {
		dir, out, bus := newBus(t)
		busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("g", "schema", "input", "100"))
		r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
		wantExit(t, r, 0)
		wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "\t100\t")
	})
}

// ---------------------------------------------------------------- rule 7: the rough mark

func TestRule7ARoughLineFoldsAsItsNumberAndIsCountedApart(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, strings.Join([]string{
		"2026-09-11\temma\tg\tschema\tinput\t~100000",
		"2026-09-11\temma\tg\tschema\toutput\t~3",
		"",
	}, "\n"))
	repos := reposFile(t, dir)
	r := invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--bus", bus)
	wantExit(t, r, 0)
	row := lineWith(read(t, filepath.Join(out, "2026-09-11.tsv")), "g\tschema")
	cols := strings.Split(row, "\t")
	if cols[3] != "100000" {
		t.Errorf("~100000 folded as %q, want 100000", cols[3])
	}
	if cols[8] != "2" {
		t.Errorf("rough column is %q, want 2", cols[8])
	}
	wantContains(t, lineWith(r.stdout, "TOKENS DAY"), "rough=2")

	s := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, s, 0)
	for _, tok := range []string{"SUM PAIR", "SUM MODEL", "SUM TOTAL"} {
		wantContains(t, lineWith(s.stdout, tok), "rough=2")
	}
}

// ---------------------------------------------------------------- rule 8: one temp name, one lock

func TestRule8TheTempNameIsFixedAndIsNotAStray(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go")+"\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr), 0)
	before := read(t, filepath.Join(out, "2026-09-11.tsv"))

	// What a fold killed between the write and the rename leaves behind.
	stranded := write(t, filepath.Join(out, "2026-09-11.tsv.tmp"), "half a file\n")
	if got := read(t, filepath.Join(out, "2026-09-11.tsv")); got != before {
		t.Error("the day file was not left entire")
	}
	c := invoke(t, "check", "--out", out)
	wantExit(t, c, 0)
	wantNotContains(t, c.all(), "CHECK STRAY")

	// The next fold writes over the temp and renames.
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr), 0)
	if _, err := os.Stat(stranded); err == nil {
		t.Error("the temp name survived the next fold")
	}
}

// ---------------------------------------------------------------- rule 9: one file per day, nothing removed

func TestRule9OneFilePerDayAndNothingIsRemoved(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m1", "2026-09-09T10:00:00Z", "fable", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-10T10:00:00Z", "fable", map[string]int{"input_tokens": 2}, "/x/schema/a.go"),
		msg("m3", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 3}, "/x/schema/a.go"),
	}, "\n")+"\n")
	old := write(t, filepath.Join(out, "daily-2026-09.tsv"), "a month file from the prototype\n")
	notes := write(t, filepath.Join(out, "notes.txt"), "a person's note\n")

	wantExit(t, invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--claude", "glenn="+tr), 0)
	ents, _ := os.ReadDir(out)
	days := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tsv") && !strings.HasPrefix(e.Name(), "daily") {
			days++
		}
	}
	if days != 3 {
		t.Errorf("%d day files, want 3", days)
	}
	s := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, s, 0)
	wantContains(t, s.stdout, "days=3")
	c := invoke(t, "check", "--out", out)
	wantExit(t, c, 1)
	wantContains(t, c.stderr, "CHECK STRAY")
	if n := strings.Count(c.stderr, "CHECK STRAY"); n != 2 {
		t.Errorf("%d stray lines, want 2", n)
	}
	if read(t, old) != "a month file from the prototype\n" || read(t, notes) != "a person's note\n" {
		t.Error("a file under --out was touched")
	}
}

// ---------------------------------------------------------------- rule 10: a day that would shrink is refused

func TestRule10ADayThatWouldShrinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	src := filepath.Join(tr, "a.jsonl")
	write(t, src, msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go")+"\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr), 0)
	before := read(t, filepath.Join(out, "2026-09-11.tsv"))

	write(t, src, msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 60}, "/x/schema/a.go")+"\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS SHRANK date=2026-09-11 type=input file=100 now=60 written=false")
	if read(t, filepath.Join(out, "2026-09-11.tsv")) != before {
		t.Error("a refused shrink rewrote the file")
	}
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr, "--allow-shrink")
	wantExit(t, r, 0)
	wantContains(t, r.stderr, "written=true")
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "\t60\t")
}

func TestRule10ANumberBecomingADashShrinksAndADashBecomingANumberDoesNot(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	note := filepath.Join(bus, "from-emma", "a.md")
	header := "From: Emma\nTo: Rowan\nDate: " + busDate + "\nId: emma-000000000001\nSubject: tokens 2026-09-11\n\n"
	write(t, note, header+"2026-09-11\temma\tg\tschema\tinput\t10\n2026-09-11\temma\tg\tschema\treasoning\t40\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--bus", bus), 0)

	write(t, note, header+"2026-09-11\temma\tg\tschema\tinput\t10\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--bus", bus)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "type=reasoning file=40 now=-")

	// The other direction: a dash in the file that is a number now is coverage arriving.
	write(t, note, header+"2026-09-11\temma\tg\tschema\tinput\t10\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--bus", bus, "--allow-shrink"), 0)
	write(t, note, header+"2026-09-11\temma\tg\tschema\tinput\t10\n2026-09-11\temma\tg\tschema\treasoning\t40\n")
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--bus", bus)
	wantExit(t, r, 0)
	wantNotContains(t, r.stderr, "SHRANK")
}

// ---------------------------------------------------------------- rule 12: the tool stamps

func TestRule12TheToolStampsAndNoFlagSetsIt(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"),
		msg("m2", "2026-09-11T11:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"),
	}, "\n")+"\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr)
	wantExit(t, r, 0)
	first := strings.Split(read(t, filepath.Join(out, "2026-09-11.tsv")), "\n")[0]
	for _, want := range []string{"nova-tokens v1 ", "day=2026-09-11", "at=2026-09-11T23:55:02Z", "build=", "turns=2", "sources=claude:glenn"} {
		if !strings.Contains(first, want) {
			t.Errorf("the version line %q lacks %q", first, want)
		}
	}
	wantContains(t, lineWith(r.stdout, "TOKENS FOLD"), "at=2026-09-11T23:55:02Z")
	wantContains(t, lineWith(r.stdout, "TOKENS DAY"), "turns=2")
	// No flag sets at=.
	bad := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr, "--at", "2020-01-01T00:00:00Z")
	wantExit(t, bad, 2)

	// A day fed by a bus note alone counts no turns.
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-10", busDate, "2026-09-10\temma\tg\tschema\tinput\t10\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-10", "--repos", repos, "--bus", bus), 0)
	wantContains(t, strings.Split(read(t, filepath.Join(out, "2026-09-10.tsv")), "\n")[0], "turns=-")

	s := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, s, 0)
	wantContains(t, lineWith(s.stdout, "SUM MONTH"), "turns=2")
	wantContains(t, lineWith(s.stdout, "SUM TOTAL"), "turns=2")
	wantContains(t, lineWith(s.stdout, "SUM MONTH"), "at=2026-09-11T23:55:02Z")
	c := invoke(t, "check", "--out", out)
	wantContains(t, lineWith(c.stdout, "CHECK OK"), "at=2026-09-11T23:55:02Z")
}

// ---------------------------------------------------------------- rule 13: check is the gate

func TestRule13CheckNamesEveryFindingAndFillsNoDay(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	const ver = "nova-tokens v1 day=%s at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"
	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	row := func(day, model, repo, in string) string {
		return day + "\t" + model + "\t" + repo + "\t" + in + "\t2\t3\t4\t5\t0\tutc\tx\n"
	}
	good := func(day string) string {
		return "nova-tokens v1 day=" + day + " at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n" + hdr + row(day, "a", "schema", "1")
	}

	// eight bad files, and a gap at 09-09 between 09-07 and 09-10
	write(t, filepath.Join(out, "2026-09-01.tsv"), "nova-tokens v1 day=2026-09-01 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+"2026-09-01\ta\tschema\t1\t2\t3\t4\t5\t0\tutc\n")
	write(t, filepath.Join(out, "2026-09-02.tsv"), "nova-tokens v1 day=2026-09-02 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+row("2026-09-03", "a", "schema", "1"))
	write(t, filepath.Join(out, "2026-09-03.tsv"), "nova-tokens v1 day=2026-09-03 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+row("2026-09-03", "a", "schema", "1")+row("2026-09-03", "a", "schema", "2"))
	write(t, filepath.Join(out, "2026-09-04.tsv"), "nova-tokens v1 day=2026-09-04 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+row("2026-09-04", "b", "schema", "1")+row("2026-09-04", "a", "schema", "2"))
	write(t, filepath.Join(out, "2026-09-05.tsv"), hdr+row("2026-09-05", "a", "schema", "1"))
	write(t, filepath.Join(out, "2026-09-06.tsv"), "nova-tokens v1 day=2026-09-06 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+"2026-09-06\ta\tschema\t\t2\t3\t4\t5\t0\tutc\tx\n")
	write(t, filepath.Join(out, "2026-09-07.tsv"), "nova-tokens v1 day=2026-09-07 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+"2026-09-07\ta\tschema\t1\t2\t3\t4\t5\t0\t\tx\n")
	write(t, filepath.Join(out, "2026-09-08.tsv"), "nova-tokens v1 day=2026-09-08 at=2026-09-11T23:55:02Z build=b sources=x\n"+hdr+row("2026-09-08", "a", "schema", "1"))
	write(t, filepath.Join(out, "2026-09-10.tsv"), good("2026-09-10"))

	r := invoke(t, "check", "--out", out, "--max", "0")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "CHECK MISSING date=2026-09-09")
	line := lineWith(r.stderr, "CHECK FAIL files=")
	wantContains(t, line, "bad=8")
	wantContains(t, line, "missing=1")

	// A clean set, with dashes and a zone, is CHECK OK.
	clean := mkdir(t, filepath.Join(dir, "clean"))
	write(t, filepath.Join(clean, "2026-09-11.tsv"), "nova-tokens v1 day=2026-09-11 at=2026-09-11T23:55:02Z build=b turns=- sources=x\n"+hdr+
		"2026-09-11\ta\tschema\t-\t-\t-\t-\t-\t0\tAmerica/Los_Angeles\tx\n")
	c := invoke(t, "check", "--out", clean)
	wantExit(t, c, 0)
	wantContains(t, c.stdout, "missing=0")

	// sum over the same gapped month answers rather than gating.
	s := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, s, 2) // a file whose stamp line is not nova-tokens v1 is refused by sum
	gapped := mkdir(t, filepath.Join(dir, "gapped"))
	write(t, filepath.Join(gapped, "2026-09-07.tsv"), good("2026-09-07"))
	write(t, filepath.Join(gapped, "2026-09-08.tsv"), good("2026-09-08"))
	write(t, filepath.Join(gapped, "2026-09-10.tsv"), good("2026-09-10"))
	s = invoke(t, "sum", "--out", gapped, "--month", "2026-09")
	wantExit(t, s, 0)
	wantContains(t, s.stdout, "missing=1")
}

// ---------------------------------------------------------------- rule 14: the swarm's usage files

func TestRule14TheSwarmUsageFilesAreASource(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	pool := mkdir(t, filepath.Join(dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRow("j1", "t1", "1", "deepseek-v3", "serialize", "2026-09-11T10:00:00Z", "1000", "20", "-", "5", "0"))
	swarmUsage(t, pool, "j2", swarmRow("j2", "t1", "2", "deepseek-v3", "serialize", "2026-09-11T11:00:00Z", "7", "8", "9", "10", "11"))
	swarmUsage(t, pool, "j3", swarmRow("j3", "t3", "1", "deepseek-v3", "cathedral", "2026-09-11T12:00:00Z", "1", "1", "1", "1", "1"))
	// reclaimed job directories, and one with no usage file
	mkdir(t, filepath.Join(pool, "done", "j4"))
	write(t, filepath.Join(pool, "done", "j4", "secret.txt"), "nothing here may be opened\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--swarm", "deepseek="+pool)
	wantExit(t, r, 0)
	src := lineWith(r.stdout, "TOKENS SOURCE")
	wantContains(t, src, "nousage=1")
	wantContains(t, src, "reports=input,output,cache_write,cache_read,reasoning")
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	// attempt 1 and attempt 2 are two rows folded into one (model, repo) key; the
	// unknown repo name goes to other.
	wantContains(t, day, "deepseek-v3\tother\t1\t1\t1\t1\t1\t0\tutc\tswarm:deepseek")
	row := lineWith(day, "deepseek-v3\tserialize")
	cols := strings.Split(row, "\t")
	if cols[3] != "1007" || cols[5] != "9" || cols[7] != "11" {
		t.Errorf("the two attempts did not both fold: %q", row)
	}

	// a fifteen-column header is refused by name
	short := mkdir(t, filepath.Join(dir, "short"))
	write(t, filepath.Join(short, "usage", "j9.tsv"), strings.Join(swarmHeader[:15], "\t")+"\n")
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--swarm", "d="+short)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "note")
	wantContains(t, r.stderr, "TOKENS UNPARSED")
}

// ---------------------------------------------------------------- rule 15: five types apart, a dash is not a zero

func TestRule15ATypeTheSourceDidNotReportIsADashAndNeverAZero(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10, "output_tokens": 1, "cache_creation_input_tokens": 2, "cache_read_input_tokens": 3}, "/x/schema/a.go")+"\n")
	pool := mkdir(t, filepath.Join(dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRow("j1", "t", "1", "dv3", "serialize", "2026-09-11T10:00:00Z", "5", "6", "-", "-", "7"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate,
		"2026-09-11\temma\tgem\tschema\tinput\t9\n2026-09-11\temma\tgem\tschema\toutput\t0\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos,
		"--claude", "glenn="+tr, "--swarm", "d="+pool, "--bus", bus)
	wantExit(t, r, 0)
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	wantContains(t, day, "fable\tschema\t10\t1\t2\t3\t-\t")
	wantContains(t, day, "dv3\tserialize\t5\t6\t-\t-\t7\t")
	wantContains(t, day, "gem\tschema\t9\t0\t-\t-\t-\t")
	// dashes counted on the day line and per column by sum
	wantContains(t, lineWith(r.stdout, "TOKENS DAY"), "dashes=6")
	s := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, s, 0)
	wantContains(t, lineWith(s.stdout, "SUM TOTAL"), "dashes=0,0,2,2,2")
	// neither who nor window is a column
	hdr := strings.Split(day, "\n")[1]
	for _, forbidden := range []string{"who", "window"} {
		if strings.Contains(hdr, forbidden) {
			t.Errorf("%q is a column: %q", forbidden, hdr)
		}
	}
}

func TestRule15AMixedRowSumsPerTypeOverTheSourcesThatReportedIt(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "m", map[string]int{"input_tokens": 10, "output_tokens": 1, "cache_creation_input_tokens": 2, "cache_read_input_tokens": 3}, "/x/schema/a.go")+"\n")
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate,
		"2026-09-11\temma\tm\tschema\tinput\t5\n2026-09-11\temma\tm\tschema\treasoning\t77\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr, "--bus", bus)
	wantExit(t, r, 0)
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "m\tschema\t15\t1\t2\t3\t77\t")
}

// ---------------------------------------------------------------- rule 16 and 19: sources are read-only, one subprocess

func TestRule16And19TheDatabaseIsCopiedAndQueriedReadOnlyUnderATimeout(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	scratch := mkdir(t, filepath.Join(dir, "scratch"))
	db := write(t, filepath.Join(dir, "opencode.db"), "SQLite format 3\x00 not really\n")
	write(t, db+"-wal", "wal\n")
	logPath := fakeSqlite3(t,
		"s1\t\t/x/schema\n",
		"msg1\ts1\t2026-09-11T10:00:00Z\tanthropic\tmercury-2.5\t10\t20\t30\t40\t50\t/x/schema\n",
		"msg1\ts1\t/x/schema/a.go\n")

	before, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir),
		"--opencode", "bench="+db, "--scratch", scratch)
	wantExit(t, r, 0)
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "mercury-2.5\tschema\t10\t20\t30\t40\t50\t")

	argv := read(t, logPath)
	if !strings.Contains(argv, "-readonly") {
		t.Errorf("an invocation carried no -readonly:\n%s", argv)
	}
	for _, line := range strings.Split(strings.TrimSpace(argv), "\n") {
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "/") && !strings.HasPrefix(tok, scratch) {
				t.Errorf("sqlite3 was pointed at %q, outside --scratch", tok)
			}
		}
	}
	after, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if after.ModTime() != before.ModTime() || after.Size() != before.Size() {
		t.Error("the live database changed")
	}
	// --scratch is required with --opencode and refused without it.
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--opencode", "bench="+db), 2)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1})+"\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr, "--scratch", scratch), 2)
}

func TestRule19ASubprocessPastTheTimeoutIsUnreadableAndTheFoldGoesOn(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	scratch := mkdir(t, filepath.Join(dir, "scratch"))
	db := write(t, filepath.Join(dir, "opencode.db"), "SQLite format 3\x00\n")
	bin := mkdir(t, filepath.Join(dir, "bin"))
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 3}, "/x/schema/a.go")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir),
		"--opencode", "bench="+db, "--scratch", scratch, "--claude", "g="+tr, "--timeout", "1")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "timeout after 1s")
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err != nil {
		t.Error("the fold did not continue over the other sources")
	}
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr, "--timeout", "0"), 2)
}

// ---------------------------------------------------------------- rule 17: a day is a UTC day

func TestRule17TheDayComesFromTheMessageStamp(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m1", "2026-09-11T23:59:59Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-12T00:00:01Z", "f", map[string]int{"input_tokens": 2}, "/x/schema/a.go"),
	}, "\n")+"\n")
	r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 0)
	for _, d := range []string{"2026-09-11", "2026-09-12"} {
		if _, err := os.Stat(filepath.Join(out, d+".tsv")); err != nil {
			t.Errorf("no file for %s", d)
		}
	}
}

func TestRule17ABusLineDatedAnotherDayIsRedatedAndCounted(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate,
		"2026-09-10\temma\tg\tschema\tinput\t5\n")
	r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "redated=1")
	wantContains(t, read(t, filepath.Join(out, "2026-09-10.tsv")), "\t5\t")
}

// ---------------------------------------------------------------- rule 18: two folds, same rows

func TestRule18TwoFoldsDifferInNothingButTheStamp(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m1", "2026-09-11T10:00:00Z", "z", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-11T10:01:00Z", "a", map[string]int{"input_tokens": 2}, "/x/serialize/a.go"),
	}, "\n")+"\n")
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000001", busDate, "2026-09-11\temma\tg\tschema\tinput\t2\n")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")

	wantExit(t, invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--claude", "g="+tr, "--bus", bus), 0)
	first := read(t, filepath.Join(out, "2026-09-11.tsv"))
	later := foldStamp.Add(time.Hour)
	wantExit(t, invokeAt(t, later, "fold", "--out", out, "--all", "--repos", repos, "--claude", "g="+tr, "--bus", bus), 0)
	second := read(t, filepath.Join(out, "2026-09-11.tsv"))
	a, b := strings.SplitN(first, "\n", 2), strings.SplitN(second, "\n", 2)
	if a[1] != b[1] {
		t.Errorf("the rows differ between two folds:\n%s\n%s", a[1], b[1])
	}
	if a[0] == b[0] {
		t.Error("the stamp line did not change with the clock")
	}
	if strings.ReplaceAll(a[0], "at=2026-09-11T23:55:02Z", "") != strings.ReplaceAll(b[0], "at=2026-09-12T00:55:02Z", "") {
		t.Errorf("the stamp lines differ in more than at=:\n%s\n%s", a[0], b[0])
	}
	// rows sorted by (model, repo)
	rows := strings.Split(strings.TrimSpace(first), "\n")[2:]
	if !strings.HasPrefix(rows[0], "2026-09-11\ta\t") {
		t.Errorf("rows are not sorted by (model, repo): %q", rows[0])
	}
}

// ---------------------------------------------------------------- rule 20: report

func TestRule20ReportPrintsTheBodyAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "gemini", map[string]int{"input_tokens": 100, "output_tokens": 20, "cache_creation_input_tokens": 0}, "/x/schema/a.go")+"\n")
	note := filepath.Join(dir, "note-body.txt")

	r := invoke(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr, "--note", note)
	wantExit(t, r, 0)
	for _, line := range strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 6 {
			t.Errorf("a report line has %d fields, want six: %q", len(f), line)
		}
		if strings.ContainsAny(line, "~#") {
			t.Errorf("a report line carries ~ or #: %q", line)
		}
	}
	wantNotContains(t, r.stdout, "reasoning")
	wantContains(t, r.stdout, "2026-09-11\temma\tgemini\tschema\tinput\t100")
	wantContains(t, r.stdout, "2026-09-11\temma\tgemini\tschema\tcache_write\t0")
	wantContains(t, r.stderr, "REPORT OK who=emma day=2026-09-11")
	wantContains(t, r.stderr, "subject=tokens 2026-09-11 at=2026-09-11T23:55:02Z build=")
	if read(t, note) != r.stdout {
		t.Error("--note is not exactly the stdout bytes")
	}

	// The note folds back as the same rows, through the bus, with one hand-added comment.
	out := mkdir(t, filepath.Join(dir, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	subject := strings.TrimPrefix(lineWith(r.stderr, "REPORT OK"), "")
	subject = subject[strings.Index(subject, "subject=")+len("subject="):]
	busNote(t, bus, "emma", "n.md", "emma-000000000001", subject, busDate, r.stdout+"# repos: schema\n")
	f := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--bus", bus)
	wantExit(t, f, 0)
	wantContains(t, f.stdout, "unparsed=0")
	wantContains(t, f.stdout, "TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema")
	viaBus := read(t, filepath.Join(out, "2026-09-11.tsv"))
	out2 := mkdir(t, filepath.Join(dir, "out2"))
	wantExit(t, invoke(t, "fold", "--out", out2, "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr), 0)
	direct := read(t, filepath.Join(out2, "2026-09-11.tsv"))
	stripRow := func(s string) string {
		var keep []string
		for _, l := range strings.Split(strings.TrimSpace(s), "\n")[2:] {
			c := strings.Split(l, "\t")
			keep = append(keep, strings.Join(c[:8], "\t"))
		}
		return strings.Join(keep, "\n")
	}
	if stripRow(viaBus) != stripRow(direct) {
		t.Errorf("the note's rows are not the transcript's rows:\nbus:\n%s\ndirect:\n%s", stripRow(viaBus), stripRow(direct))
	}
	wantContains(t, viaBus, "\t-\t0\tutc\tbus:emma")
}

func TestRule20ReportRefusesAndSupersedes(t *testing.T) {
	dir := t.TempDir()
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "gemini", map[string]int{"input_tokens": 1}, "/x/schema/a.go")+"\n")
	note := write(t, filepath.Join(dir, "note.txt"), "what was there before\n")

	// A report whose every source is unreadable prints nothing and says so.
	if os.Geteuid() != 0 {
		bad := mkdir(t, filepath.Join(dir, "bad"))
		f := write(t, filepath.Join(bad, "x.jsonl"), "{}\n")
		if err := os.Chmod(f, 0o000); err != nil {
			t.Fatal(err)
		}
		r := invoke(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+bad, "--note", note)
		wantExit(t, r, 1)
		if r.stdout != "" {
			t.Errorf("a failed report wrote to stdout: %q", r.stdout)
		}
		wantContains(t, r.stderr, "REPORT FAIL")
		wantContains(t, r.stderr, "TOKENS UNREADABLE")
		if read(t, note) != "what was there before\n" {
			t.Error("a failed report replaced the --note file")
		}
		if _, err := os.Stat(note + ".tmp"); err == nil {
			t.Error("a failed report left a .tmp beside the note")
		}
		os.Chmod(f, 0o644)
	}

	r := invoke(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr,
		"--supersedes", "emma-000000000002", "--supersedes", "emma-000000000001")
	wantExit(t, r, 0)
	wantContains(t, r.stderr, "supersedes=emma-000000000001,emma-000000000002")
	r = invoke(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr,
		"--supersedes", "emma-000000000001", "--supersedes", "emma-000000000001")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "REPORT REFUSED")
	wantContains(t, r.stderr, "emma-000000000001")
}

// ---------------------------------------------------------------- rule 21: the provider export

func TestRule21AProviderExportIsUnattributedAndNeverSplit(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	// Google: per-row timestamps, folded to UTC days.
	g := write(t, filepath.Join(dir, "google.csv"), strings.Join([]string{
		"timestamp,model,input_tokens,output_tokens",
		"2026-09-11T20:30:00-07:00,gemini-2.5-pro,100,10",
		"2026-09-11T10:30:00-07:00,gemini-2.5-pro,200,20",
		"",
	}, "\n"))
	r := invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--provider", "google="+g)
	wantExit(t, r, 0)
	wantContains(t, read(t, filepath.Join(out, "2026-09-12.tsv")), "gemini-2.5-pro\tunattributed\t100\t10\t-\t-\t-\t0\tutc\tprovider:google")
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "gemini-2.5-pro\tunattributed\t200\t20\t-\t-\t-\t0\tutc\tprovider:google")
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "reports=input,output")

	// xAI: per-day totals in a declared zone.
	out2 := mkdir(t, filepath.Join(dir, "out2"))
	x := write(t, filepath.Join(dir, "xai.csv"), strings.Join([]string{
		"# timezone: America/Los_Angeles",
		"date,model,input_tokens,output_tokens,reasoning_tokens",
		"2026-09-11,grok-4,9912340,301122,55",
		"",
	}, "\n"))
	r = invoke(t, "fold", "--out", out2, "--all", "--repos", repos, "--provider", "xai="+x)
	wantExit(t, r, 0)
	wantContains(t, read(t, filepath.Join(out2, "2026-09-11.tsv")), "grok-4\tunattributed\t9912340\t301122\t-\t-\t55\t0\tAmerica/Los_Angeles\tprovider:xai")
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "day_basis=America/Los_Angeles")
	wantContains(t, lineWith(r.stdout, "TOKENS DAY"), "nonutc=1")
	s := invoke(t, "sum", "--out", out2, "--month", "2026-09")
	wantExit(t, s, 0)
	wantContains(t, lineWith(s.stdout, "SUM TOTAL"), "nonutc=1")

	// An export with neither timestamps nor a zone.
	out3 := mkdir(t, filepath.Join(dir, "out3"))
	n := write(t, filepath.Join(dir, "nozone.csv"), "date,model,input_tokens\n2026-09-11,grok-4,5\n")
	r = invoke(t, "fold", "--out", out3, "--all", "--repos", repos, "--provider", "xai="+n)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS UNREADABLE")

	// An unknown column is unreadable, quoting the line.
	u := write(t, filepath.Join(dir, "odd.csv"), "date,model,widgets\n2026-09-11,grok-4,5\n")
	r = invoke(t, "fold", "--out", out3, "--all", "--repos", repos, "--provider", "xai="+u)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "widgets")
}

func TestRule17AMixedRowIsRefused(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	g := write(t, filepath.Join(dir, "google.csv"), "timestamp,model,input_tokens\n2026-09-11T10:00:00Z,m,5\n")
	x := write(t, filepath.Join(dir, "xai.csv"), "# timezone: America/Los_Angeles\ndate,model,input_tokens\n2026-09-11,m,7\n")
	r := invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--provider", "google="+g, "--provider", "xai="+x)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS MIXED date=2026-09-11 model=m repo=unattributed")
	wantContains(t, r.stderr, "mixed=1")
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err == nil {
		t.Error("a mixed row was written")
	}
}

func TestRule21ANoteOfOneReposCommentIsValidWithZeroRows(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, "# repos: schema, serialize\n\n")
	r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--bus", bus)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema,serialize")
	wantContains(t, r.stdout, "unparsed=0")
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "rows=0")
}
