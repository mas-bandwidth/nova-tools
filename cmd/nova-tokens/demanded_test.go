package main

// The tests SPEC-TOKENS.md demands, one function per numbered rule. Each runs inside
// t.TempDir(), against a fake sqlite3 on PATH where the OpenCode source is involved, with
// no network, and each was seen red before the code under it existed.

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// foldAll is `nova-tokens fold --out <out> --all --repos <repos>` and then args.
func (b bench) foldAll(args ...string) testkit.Ran {
	b.t.Helper()
	return novaTokens.Do(b.t, append([]string{"fold", "--out", b.out, "--all", "--repos", b.repos}, args...)...)
}

// report is `nova-tokens report --who emma --day 2026-09-11 --repos <repos>` and then args.
func (b bench) report(args ...string) testkit.Ran {
	b.t.Helper()
	return novaTokens.Do(b.t, append([]string{"report", "--who", "emma", "--day", "2026-09-11", "--repos", b.repos}, args...)...)
}

// sum is `nova-tokens sum --out <out> --month 2026-09`.
func (b bench) sum() testkit.Ran {
	b.t.Helper()
	return novaTokens.Do(b.t, "sum", "--out", b.out, "--month", "2026-09")
}

// check is `nova-tokens check --out <out>` and then args.
func (b bench) check(args ...string) testkit.Ran {
	b.t.Helper()
	return novaTokens.Do(b.t, append([]string{"check", "--out", b.out}, args...)...)
}

// oneNote is emma's bus lane under b holding the one note emma-000000000001, subject
// `tokens 2026-09-11`, with body; it returns the bus directory.
func oneNote(b bench, body string) string {
	b.t.Helper()
	bus := busDir(b.t, filepath.Join(b.dir, "bus"), "emma")
	busNote(b.t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, body)
	return bus
}

func TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted(t *testing.T) {
	b := newBench(t)
	// A complete, valid set of sources sitting under every variable a tool might reach for.
	bait := filepath.Join(b.dir, "bait")
	testkit.WriteFile(t, filepath.Join(bait, "t", "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 5}, "/x/schema/a.go")+"\n")
	reposFile(t, bait)
	for _, v := range []string{"HOME", "TMPDIR", "XDG_DATA_HOME"} {
		t.Setenv(v, bait)
	}

	// Rule 1: "$HOME, $TMPDIR, $XDG_DATA_HOME and every other variable are ignored, and a
	// test sets them and proves it." The proof is the count of source files this process
	// has opened: a read does not change the number of entries in a directory, so
	// counting entries proved nothing, and the refusal returns before any source is read.
	opens := tokens.Opens()
	r := novaTokens.Do(t, "fold", "--day", "2026-09-11").Exit(2)
	assert.Zero(t, tokens.Opens()-opens, "the refusal opened source files; nothing under $HOME, $TMPDIR or $XDG_DATA_HOME may be opened")

	// And a fold that DOES run opens only the source its flags name -- the one transcript
	// under --claude -- never the identical tree the variables point at, which holds one
	// transcript of its own. (The rules file is a flag's value, not a source.)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 5}, "/x/schema/a.go"))
	opens = tokens.Opens()
	b.fold("--claude", "g="+b.tr).Exit(0)
	assert.EqualValues(t, 1, tokens.Opens()-opens, "the fold opened other than the 1 source file its --claude names; the bait tree under $HOME, $TMPDIR and $XDG_DATA_HOME holds one more")

	lines := strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")
	require.Len(t, lines, 3, "want three refusal lines, one per independent problem: %s", r)
	for i, want := range []string{"--out", "--repos", "source"} {
		assert.Contains(t, lines[i], want, "refusal line %d is not the one about %s (the order is fixed)", i+1, want)
		assert.Regexp(t, "^TOKENS REFUSED: ", lines[i])
	}
	// What it WANTS, not only what was wrong.
	r.Err("refusing to guess", "--claude <label>=<dir>")
	assert.Empty(t, r.Stdout, r)
}

// A symlinked --out, or a symlinked parent of it, is refused before the lock is taken:
// whatever the referent held is unchanged, and an empty referent stays empty.
func TestFoldRefusesASymlinkedOutputBeforeWritingAnything(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name           string
		parent, seeded bool // the link is --out's parent; the referent holds a lock and a day
	}{
		{"FoldRefusesSymlinkedOutputBeforeWritingLock", false, true},
		{"FoldRefusesParentSymlinkedOutputBeforeWritingLock", true, true},
		{"FoldRefusesParentSymlinkCreatesNoFiles", true, false},
	} {
		for _, suffix := range []string{"", string(os.PathSeparator), string(os.PathSeparator) + "."} {
			t.Run(c.name+"/out"+suffix, func(t *testing.T) {
				t.Parallel()
				b := newBench(t)
				target, link := filepath.Join(b.dir, "target"), filepath.Join(b.dir, "link")
				referent, out := target, link
				if c.parent {
					referent, out = filepath.Join(target, "child"), filepath.Join(link, "child")
				}
				held := map[string]string{}
				if c.seeded {
					held = map[string]string{tokens.LockName: "original lock\n", "2026-09-11.tsv": "original day\n"}
				}
				testkit.Tree(t, referent, held)
				require.NoError(t, os.Symlink(target, link))
				novaTokens.Do(t, "fold", "--out", out+suffix, "--day", "2026-09-11", "--repos", b.repos, "--claude", "fixture="+b.tr).ExitErr(2, "symlink")
				entries, err := os.ReadDir(referent)
				require.NoError(t, err)
				after := map[string]string{}
				for _, e := range entries {
					after[e.Name()] = testkit.ReadFile(t, filepath.Join(referent, e.Name()))
				}
				assert.Equal(t, held, after, "the referent changed")
			})
		}
	}
}

func TestRule2EveryRowNamesItsSources(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 10}, "/x/schema/a.go"))
	bus := oneNote(b, "2026-09-11\temma\tfable\tschema\tinput\t5\n")

	b.fold("--claude", "glenn="+b.tr, "--bus", bus).Exit(0)
	day := b.day()
	cols := strings.Split(lineWith(day, "fable\tschema"), "\t")
	require.Len(t, cols, len(tokens.Columns), "no full row for fable/schema:\n%s", day)
	// The sources column is the ELEVENTH, read by position.
	assert.Equal(t, "bus:emma,claude:glenn", cols[10], "want both labels sorted")
	for _, line := range strings.Split(strings.TrimSpace(day), "\n")[2:] {
		c := strings.Split(line, "\t")
		if assert.Len(t, c, len(tokens.Columns), "a row is not every column: %q", line) {
			assert.NotEmpty(t, c[10], "a row has an empty sources column: %q", line)
		}
	}

	// A duplicate label across two flags is exit 2 naming it.
	b.fold("--claude", "glenn="+b.tr, "--swarm", "glenn="+b.tr).ExitErr(2, "glenn").Err("REFUSED")
}

func TestRule3AnUnreadableSourceIsCountedAndPrintedAndExitsOne(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("good.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"))
	bad := b.transcript("bad.jsonl", "{}")
	release := makeUnreadable(t, bad)

	r := b.fold("--claude", "glenn="+b.tr).ExitErr(1, "TOKENS UNREADABLE label=claude:glenn").Err("bad.jsonl").Out("files=2 unreadable=1")
	assert.Contains(t, lineWith(r.Stderr, "TOKENS FAIL"), "unreadable=1")
	assert.FileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "the day file from the readable file was not written")

	// Without the unreadable file the same run is TOKENS OK, exit 0.
	release() // windows holds the file open to make it unreadable, and an open file is undeletable
	require.NoError(t, os.Remove(bad))
	b.fold("--claude", "glenn="+b.tr).Exit(0).Out("TOKENS OK")
}

// TestRule3AValidLineIsNeverCountedAsNotJSON pins rule 3's word: "A source that cannot be
// read is counted and printed, never skipped silently." The count is for lines that do
// not parse as JSON. A Claude Code user turn writes
// `"message":{"role":"user","content":"<a string>"}` -- valid JSON, and a type mismatch
// against a reader that declares content an array of blocks. Such a reader flags nearly
// every transcript file TOKENS UNREADABLE, exit 1, with a remedy ("open those files to this
// group") nobody can act on.
func TestRule3AValidLineIsNeverCountedAsNotJSON(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		// A user turn whose content is a STRING, one whose content is an array of blocks and
		// no usage, and an assistant turn: content an ARRAY, and it carries the usage.
		`{"type":"user","timestamp":"2026-09-11T09:59:00Z","message":{"role":"user","content":"hello, read /x/schema/a.go"}}`,
		`{"type":"user","timestamp":"2026-09-11T09:59:30Z","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 10}, "/x/schema/a.go"))

	b.fold("--claude", "glenn="+b.tr).Exit(0).Out("TOKENS OK", "unreadable=0").NotErr("badline", "UNREADABLE")
	assert.Contains(t, b.day(), "fable\tschema\t100\t10\t")

	// And a line that really is not JSON is still counted, printed, and exit 1.
	b.transcript("b.jsonl", "{not json")
	b.fold("--claude", "glenn="+b.tr).ExitErr(1, "badline=1").Err("b.jsonl")
}

func TestRule4AMessageIsCountedOnceByItsID(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	var lines []string
	for _, n := range []int{10, 20, 30, 40, 55} {
		lines = append(lines, msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": n, "output_tokens": n * 2}, "/x/schema/a.go"))
	}
	b.transcript("a.jsonl", append(lines, msg("", "2026-09-11T10:05:00Z", "fable", map[string]int{"input_tokens": 999}, "/x/schema/a.go"))...)

	b.fold("--claude", "glenn="+b.tr).Exit(0).Out("dup=4", "noid=1")
	cols := strings.Split(lineWith(b.day(), "fable\tschema"), "\t")
	assert.Equal(t, []string{"55", "110"}, cols[3:5], "want the LAST line's usage")
}

func TestRule5RepoAttributionAndTheTwoNamedBuckets(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	// unknown first (no paths and nothing before it), then schema, then a no-path message
	// that inherits schema, then one whose paths match nothing.
	b.transcript("a.jsonl",
		msg("m0", "2026-09-11T09:00:00Z", "fable", map[string]int{"input_tokens": 1}),
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10}, "/x/schema/a.go"),
		msg("m2", "2026-09-11T10:01:00Z", "fable", map[string]int{"input_tokens": 20}),
		msg("m3", "2026-09-11T10:02:00Z", "fable", map[string]int{"input_tokens": 69}, "/x/elsewhere/b.go"))

	r := b.fold("--claude", "glenn="+b.tr).Exit(0)
	holds(t, b.day(), "fable\tschema\t30", "fable\tunknown\t1", "fable\tother\t69")
	// 1/100 unknown, 69/100 other.
	holds(t, lineWith(r.Stdout, "TOKENS DAY"), "unknown=1.0%", "other=69.0%")

	// A fold with no --repos is exit 2.
	novaTokens.Do(t, "fold", "--out", b.out, "--day", "2026-09-11", "--claude", "glenn="+b.tr).ExitErr(2, "--repos")
}

func TestRule6TheSubjectIsExactAndAnUnparsedLineIsPrinted(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "a.md", "emma-00000000000a", "Tokens 2026-09-11 (rough)", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")
	busNote(t, bus, "emma", "b.md", "emma-00000000000b", "tokens 2026-09-11 at=x", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")
	busNote(t, bus, "emma", "c.md", "emma-00000000000c", "tokens 2026-09-11 at=2026-09-11T23:55:02Z build=abc123", busDate, strings.Join([]string{
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
	}, "\n"))

	r := b.foldAll("--bus", bus).Exit(1).Out("comments=2", "TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema,serialize", "unparsed=5").
		Err("note=emma-00000000000c")
	assert.Equal(t, 5, strings.Count(r.Stderr, "TOKENS UNPARSED"), "want three body lines and the two near-miss subjects: %s", r)
	day := b.day()
	assert.Equal(t, 4, strings.Count(day, "\n"), "want the version, the header and two rows (three folded lines on two repos):\n%s", day)
	// Neither the wrong-case subject nor the one with trailing text is a tokens note:
	// each is named, counted, and folds nothing (its 1 input is in no row).
	r.Err("TOKENS UNPARSED label=bus:emma note=emma-00000000000a", "TOKENS UNPARSED label=bus:emma note=emma-00000000000b")
	assert.NotContains(t, day, "\t1\t", "a near-miss note's numbers were folded")
}

// TestANearMissSubjectIsNamedAndNeverVanishes pins the near miss on the whole run rather
// than on a lane that also holds a real note: a note whose subject is one token short must
// not get TOKENS OK, days=0, "nothing was wrong" -- byte-identical to a lane holding nothing
// at all -- and counting it in a field nobody prints is the same silence. It is an unparsed
// note: named with its id, counted, exit 1, and the one remedy line is about the subject.
//
// TestALaneThatFoldedNothingPrintsADashForReports, on the same lane: every field of the
// source line has a value. A lane whose only note is a near miss, or whose day is a
// conflict, reports no type at all, and its reports= is a dash, never empty.
func TestANearMissSubjectIsNamedAndNeverVanishes(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "near.md", "emma-00000000000a", "tokens 2026-09-11 (rough)", busDate, "2026-09-11\temma\tg\tschema\tinput\t100\n")
	// An ordinary note of the lane is not this tool's business and stays silent: it is a
	// file this lane opened and nothing more.
	busNote(t, bus, "emma", "talk.md", "emma-00000000000b", "the build is red", busDate, "prose\n")

	r := b.foldAll("--bus", bus).ExitErr(1, "TOKENS UNPARSED label=bus:emma note=emma-00000000000a").Err("tokens 2026-09-11 (rough)").
		NotOut("emma-00000000000b").NotErr("emma-00000000000b")
	source := lineWith(r.Stdout, "TOKENS SOURCE")
	holds(t, source, "files=2", "unparsed=1", "reports=-")
	assert.NotContains(t, source, "reports= ")
	assert.Contains(t, lineWith(r.Stderr, "TOKENS FAIL"), "unparsed=1")
	note := lineWith(r.Stdout, "TOKENS NOTE")
	assert.Contains(t, note, "emma-00000000000a")
	assert.NotContains(t, note, "nothing was wrong")
	assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "a near-miss note was folded into a day file")
}

func TestRule6ABadDateRefusesTheWholeNote(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, date, want string }{
		{"missing", "", "line=0"},
		{"unparseable", "Date: yesterday\n", "line="},
		{"later than the fold", "Date: Sat Sep 12 00:55:02 UTC 2026\n", "line="},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
			testkit.WriteFile(t, filepath.Join(bus, "from-emma", "d.md"), "From: Emma\nTo: Rowan\n"+c.date+"Id: emma-00000000000d\nSubject: tokens 2026-09-11\n\n2026-09-11\temma\tg\tschema\tinput\t7\n")
			r := b.foldAll("--bus", bus).ExitErr(1, c.want).Out("unparsed=1")
			assert.Equal(t, 1, strings.Count(r.Stderr, "TOKENS UNPARSED"), "want exactly one for the whole note: %s", r)
			assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "a row of a note with a bad Date was folded")
		})
	}
}

func TestRule6SupersedesOrdersTwoNotesAndNothingElseDoes(t *testing.T) {
	t.Parallel()
	const n1, n2, n3, n4 = "emma-000000000001", "emma-000000000002", "emma-000000000003", "emma-000000000004"
	// lane is a bench with emma's bus lane; put writes her note for 2026-09-11 whose subject
	// ends with trailer and whose one line is n input on g/schema.
	lane := func(t *testing.T) (bench, string, func(file, id, trailer, n string)) {
		b := newBench(t)
		bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
		return b, bus, func(file, id, trailer, n string) {
			busNote(t, bus, "emma", file, id, "tokens 2026-09-11"+trailer, busDate, "2026-09-11\temma\tg\tschema\tinput\t"+n+"\n")
		}
	}
	// by is the trailer of a correction sent at hour naming its predecessors.
	by := func(hour, ids string) string { return " at=2026-09-11T" + hour + ":00:00Z build=b supersedes=" + ids }

	for _, c := range []struct {
		name       string
		notes      [][4]string // file, id, subject trailer, input
		code       int
		says       []string // on stdout when the fold exits 0, on stderr (and no day file) when not
		superseded int      // TOKENS SUPERSEDED lines when the fold exits 0
		day        string   // in the day file when the fold exits 0
	}{
		// Filenames whose lexical order OPPOSES the send order, and one Date to the second.
		{"the successor is the day", [][4]string{{"zzz-first.md", n1, "", "100"}, {"aaa-second.md", n2, by("20", n1), "250"}}, 0,
			[]string{"TOKENS SUPERSEDED label=bus:emma note=" + n1 + " by=" + n2 + " day=2026-09-11", "superseded=1"}, 1, "\t250\t"},
		{"a chain folds to its last link", [][4]string{{"a.md", n1, "", "100"}, {"b.md", n2, by("20", n1), "250"}, {"c.md", n3, by("21", n2), "300"}}, 0, nil, 2, "\t300\t"},
		{"a lone note folds", [][4]string{{"a.md", n1, "", "100"}}, 0, nil, 0, "\t100\t"},
		{"two successors of one predecessor are a conflict", [][4]string{{"a.md", n1, "", "100"}, {"b.md", n2, by("20", n1), "250"}, {"c.md", n3, by("21", n1), "300"}}, 1,
			[]string{"TOKENS CONFLICT"}, 0, ""},
		{"a cycle refuses both", [][4]string{{"a.md", n1, by("20", n2), "100"}, {"b.md", n2, by("21", n1), "250"}}, 1, []string{"cycle"}, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b, bus, put := lane(t)
			for _, n := range c.notes {
				put(n[0], n[1], n[2], n[3])
			}
			r := b.foldAll("--bus", bus).Exit(c.code)
			if c.code != 0 {
				r.Err(c.says...)
				assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "a refused lane-day wrote a file")
				return
			}
			r.Out(c.says...)
			assert.Equal(t, c.superseded, strings.Count(r.Stdout, "TOKENS SUPERSEDED"), r)
			assert.Contains(t, b.day(), c.day)
		})
	}

	t.Run("two roots are a conflict that folds nothing", func(t *testing.T) {
		t.Parallel()
		b, bus, put := lane(t)
		// A day file already on disk, which the conflict must leave byte-identical.
		put("a.md", n1, "", "100")
		b.foldAll("--bus", bus).Exit(0)
		before := b.day()

		put("b.md", n2, "", "250")
		b.foldAll("--bus", bus).ExitErr(1, "TOKENS CONFLICT label=bus:emma day=2026-09-11").Err("supersedes=", "conflict=1")
		assert.Equal(t, before, b.day(), "the day file changed under a conflict")

		// A correction naming only one tip leaves the other, and the remedy names both.
		put("c.md", n3, by("21", n1), "400")
		r := b.foldAll("--bus", bus).Exit(1)
		holds(t, lineWith(r.Stderr, "TOKENS CONFLICT"), n2, n3)

		// The replacement snapshot: one note naming BOTH tips, sorted, clears it. Three
		// SUPERSEDED lines: the two tips the snapshot named, and the note the first
		// correction had already superseded. Every predecessor of a valid successor is one line.
		put("d.md", n4, by("22", n2+","+n3), "900")
		r = b.foldAll("--bus", bus).Exit(0).Out("conflict=0", "note="+n2+" by="+n4, "note="+n3+" by="+n4)
		assert.Equal(t, 3, strings.Count(r.Stdout, "TOKENS SUPERSEDED"), r)
		assert.Contains(t, b.day(), "\t900\t")
	})

	for _, c := range []struct{ name, ids, want string }{
		{"a duplicate id", n1 + "," + n1, "duplicate"},
		{"an unsorted set", n2 + "," + n1, "sorted"},
		{"a missing target", "emma-0000000000ff", "no such note"},
		{"a target in another lane", "bo-000000000001", "another lane"},
		{"a target for another day", "emma-000000000009", "another day"},
		// SPEC-TOKENS' demanded test names five refusals, and this is the fifth: a
		// predecessor that is a note of this lane and this day and did NOT parse. The
		// successor is refused whole rather than replacing a note nobody could read.
		{"a target that did not parse", "emma-00000000000e", "did not parse"},
	} {
		t.Run(c.name+" refuses the whole successor", func(t *testing.T) {
			t.Parallel()
			b, bus, put := lane(t)
			busDir(t, bus, "emma", "bo")
			put("a.md", n1, "", "100")
			put("b.md", n2, by("20", n1), "250")
			busNote(t, bus, "emma", "old.md", "emma-000000000009", "tokens 2026-09-10", busDate, "2026-09-10\temma\tg\tschema\tinput\t1\n")
			busNote(t, bus, "bo", "x.md", "bo-000000000001", "tokens 2026-09-11", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")
			// A note of this lane and this day whose Date: nobody can parse: it is in the
			// lane, it is dead, and naming it as a predecessor is the fifth refusal.
			busNote(t, bus, "emma", "dead.md", "emma-00000000000e", "tokens 2026-09-11", "yesterday", "2026-09-11\temma\tg\tschema\tinput\t7\n")
			put("z.md", "emma-00000000000f", by("22", c.ids), "900")
			r := b.foldAll("--bus", bus).ExitErr(1, "note=emma-00000000000f")
			assert.Contains(t, strings.ToLower(r.Stderr), c.want)
			assert.NotContains(t, b.day(), "\t900\t")
		})
	}
}

func TestRule7ARoughLineFoldsAsItsNumberAndIsCountedApart(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := oneNote(b, "2026-09-11\temma\tg\tschema\tinput\t~100000\n2026-09-11\temma\tg\tschema\toutput\t~3\n")
	r := b.foldAll("--bus", bus).Exit(0)
	cols := strings.Split(lineWith(b.day(), "g\tschema"), "\t")
	assert.Equal(t, "100000", cols[3], "~100000 folded as another number")
	assert.Equal(t, "2", cols[8], "the rough column")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS DAY"), "rough=2")

	s := b.sum().Exit(0)
	for _, line := range []string{"SUM PAIR", "SUM MODEL", "SUM TOTAL"} {
		assert.Contains(t, lineWith(s.Stdout, line), "rough=2", line)
	}
}

func TestRule8RandomSiblingTempIsNotAStrayAndIsPreserved(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"))
	b.fold("--claude", "glenn="+b.tr).Exit(0)
	before := b.day()

	// What a fold killed between the write and the rename leaves behind:
	// either a legacy .tsv.tmp or an atomicfile .<day>.tsv.tmp-%08x. Neither is a stray.
	stranded := testkit.WriteFile(t, filepath.Join(b.out, "2026-09-11.tsv.tmp"), "half a file\n")
	strandedAtomic := testkit.WriteFile(t, filepath.Join(b.out, ".2026-09-11.tsv.tmp-1a2b3c4d"), "partial atomic file\n")
	assert.Equal(t, before, b.day(), "the day file was not left entire")
	b.check().Exit(0).NotOut("CHECK STRAY").NotErr("CHECK STRAY")

	// An unrelated dotfile is not recognized as this day's temp; check reports it as a stray.
	unrelated := testkit.WriteFile(t, filepath.Join(b.out, ".unrelated.txt.tmp-12345678"), "foreign temp\n")
	r := b.check()
	holds(t, r.Stdout+r.Stderr, "CHECK STRAY", ".unrelated.txt.tmp-12345678")
	require.NoError(t, os.Remove(unrelated))

	// The next fold writes the day file atomically via internal/atomicfile.
	// Stale random-sibling temporaries from an interrupted run are preserved.
	b.fold("--claude", "glenn="+b.tr).Exit(0)
	assert.FileExists(t, stranded, "legacy stranded temp was removed")
	assert.FileExists(t, strandedAtomic, "stranded atomic temp was removed")
}

func TestRule9OneFilePerDayAndNothingIsRemoved(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-09T10:00:00Z", "fable", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-10T10:00:00Z", "fable", map[string]int{"input_tokens": 2}, "/x/schema/a.go"),
		msg("m3", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 3}, "/x/schema/a.go"))
	old := testkit.WriteFile(t, filepath.Join(b.out, "daily-2026-09.tsv"), "a month file from the prototype\n")
	notes := testkit.WriteFile(t, filepath.Join(b.out, "notes.txt"), "a person's note\n")

	b.foldAll("--claude", "glenn="+b.tr).Exit(0)
	ents, err := os.ReadDir(b.out)
	require.NoError(t, err)
	days := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tsv") && !strings.HasPrefix(e.Name(), "daily") {
			days++
		}
	}
	assert.Equal(t, 3, days, "day files")
	b.sum().Exit(0).Out("days=3")
	c := b.check().Exit(1)
	assert.Equal(t, 2, strings.Count(c.Stderr, "CHECK STRAY"), c)
	assert.Equal(t, "a month file from the prototype\n", testkit.ReadFile(t, old), "a file under --out was touched")
	assert.Equal(t, "a person's note\n", testkit.ReadFile(t, notes), "a file under --out was touched")
}

func TestRule10ADayThatWouldShrinkIsRefused(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"))
	b.fold("--claude", "glenn="+b.tr).Exit(0)
	before := b.day()

	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 60}, "/x/schema/a.go"))
	b.fold("--claude", "glenn="+b.tr).ExitErr(1, "TOKENS SHRANK date=2026-09-11 type=input file=100 now=60 written=false")
	assert.Equal(t, before, b.day(), "a refused shrink rewrote the file")
	b.fold("--claude", "glenn="+b.tr, "--allow-shrink").ExitErr(0, "written=true")
	assert.Contains(t, b.day(), "\t60\t")
}

func TestRule10ANumberBecomingADashShrinksAndADashBecomingANumberDoesNot(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	const input, reasoning = "2026-09-11\temma\tg\tschema\tinput\t10\n", "2026-09-11\temma\tg\tschema\treasoning\t40\n"
	bus := oneNote(b, input+reasoning)
	b.fold("--bus", bus).Exit(0)
	oneNote(b, input)
	b.fold("--bus", bus).ExitErr(1, "type=reasoning file=40 now=-")

	// The other direction: a dash in the file that is a number now is coverage arriving.
	b.fold("--bus", bus, "--allow-shrink").Exit(0)
	oneNote(b, input+reasoning)
	b.fold("--bus", bus).Exit(0).NotErr("SHRANK")
}

// foldPools writes two swarm pools, one row each, and returns out, repos, poolA, poolB.
// poolA is claude-x on serialize; poolB is mercury-2.5 on serialize: the pair on which a
// swarm-only fold that recomputed the file whole would erase the other source's row.
func foldPools(t *testing.T, aIn, aOut, bIn, bOut string) (out, repos, poolA, poolB string) {
	t.Helper()
	dir := t.TempDir()
	poolA, poolB = filepath.Join(dir, "poolA"), filepath.Join(dir, "poolB")
	swarmUsage(t, poolA, "j1", swarmRow("j1", "1", "-", "claude-x", "serialize", "2026-09-14T01:00:00Z", aIn, aOut, "0", "0", "-"))
	swarmUsage(t, poolB, "j2", swarmRow("j2", "1", "-", "mercury-2.5", "serialize", "2026-09-14T02:00:00Z", bIn, bOut, "0", "81000", "50"))
	return testkit.Mkdir(t, filepath.Join(dir, "out")), reposFile(t, dir), poolA, poolB
}

// pooled is foldPools' bench (poolA 410/100 as glenn, poolB 2000/420 as freddy): fold is
// on the pools' day, 2026-09-14, and day reads that day's file.
type pooled struct {
	t                         *testing.T
	out, repos, glenn, freddy string // glenn and freddy are the pool paths
}

func newPooled(t *testing.T) pooled {
	out, repos, a, b := foldPools(t, "410", "100", "2000", "420")
	return pooled{t, out, repos, a, b}
}

func (p pooled) fold(args ...string) testkit.Ran {
	p.t.Helper()
	return novaTokens.Do(p.t, append([]string{"fold", "--out", p.out, "--day", "2026-09-14", "--repos", p.repos}, args...)...)
}

func (p pooled) day() string {
	p.t.Helper()
	return testkit.ReadFile(p.t, filepath.Join(p.out, "2026-09-14.tsv"))
}

// A swarm-only fold into a day file that holds another source's row keeps that row. A fold
// that recomputed the file whole would exit 0, written=true, no SHRANK, with the claude-x
// row simply gone -- because the totals ROSE, so rule 10's day-total comparison sees
// nothing.
func TestAFoldKeepsARowNoDeclaredSourceWrote(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	p.fold("--swarm", "glenn="+p.glenn).Exit(0)
	assert.Contains(t, p.day(), "claude-x")

	r := p.fold("--swarm", "freddy="+p.freddy).Exit(0).NotOut("SHRANK", "PARTIAL").NotErr("SHRANK", "PARTIAL")
	holds(t, p.day(), "claude-x", "\t410\t", "mercury-2.5", "\t2000\t", "sources=swarm:freddy,swarm:glenn")
	// TestIssue268CoherentDaySummaryScopeReflectsMergedFile: TOKENS DAY summarizes the
	// merged day file (two rows), so its fields are coherent with the file on disk, while
	// TOKENS OK rows= is this run's folded rows.
	holds(t, lineWith(r.Stdout, "TOKENS DAY"), "rows=2", "models=2", "repos=1", "sources=swarm:freddy,swarm:glenn", "written=true")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS OK"), "rows=1")
}

// R2: full replacement -- every source in the file is declared -- is exactly what it was.
// The one row is REPLACED by this run's arithmetic, never summed with the file's.
func TestAFullReplacementIsUnchanged(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	p.fold("--swarm", "glenn="+p.glenn).Exit(0)
	swarmUsage(t, p.glenn, "j1", swarmRow("j1", "1", "-", "claude-x", "serialize", "2026-09-14T01:00:00Z", "900", "100", "0", "0", "-"))
	p.fold("--swarm", "glenn="+p.glenn).Exit(0).NotOut("SHRANK", "PARTIAL").NotErr("SHRANK", "PARTIAL")
	got := p.day()
	holds(t, got, "\t900\t", "sources=swarm:glenn")
	lacks(t, got, "\t410\t", "\t1310\t") // replaced, neither kept nor summed
}

// R3: a BLENDED row -- one row whose sources cell names a label this run declared and one
// it did not. Its cells are already a sum over both and nothing on disk takes them apart,
// so the fold refuses the day rather than guessing.
func TestABlendedRowIsRefusedAndNothingIsWritten(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	// Both pools on the SAME model and repo, so one row carries both labels.
	swarmUsage(t, p.freddy, "j2", swarmRow("j2", "1", "-", "claude-x", "serialize", "2026-09-14T02:00:00Z", "2000", "420", "0", "0", "-"))
	p.fold("--swarm", "glenn="+p.glenn, "--swarm", "freddy="+p.freddy).Exit(0)
	before := p.day()
	assert.Contains(t, before, "swarm:freddy,swarm:glenn")

	p.fold("--swarm", "freddy="+p.freddy).
		ExitErr(1, "TOKENS PARTIAL date=2026-09-14 model=claude-x repo=serialize sources=swarm:freddy,swarm:glenn folded=swarm:freddy written=false").Err("partial=1")
	assert.Equal(t, before, p.day(), "a refused partial fold rewrote the file")
	// --allow-shrink is about a shrink, not about a row this fold cannot compute.
	p.fold("--swarm", "freddy="+p.freddy, "--allow-shrink").Exit(1)
	assert.Equal(t, before, p.day(), "--allow-shrink wrote a row the fold could not compute")
}

// R4: rule 10 still fires on a real shrink, now compared against the MERGED file, and
// --allow-shrink still writes it with the retained row still there.
func TestShrinkCheckUsesMergedTotalsAndKeepsRetainedRows(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	p.fold("--swarm", "glenn="+p.glenn, "--swarm", "freddy="+p.freddy).Exit(0)
	before := p.day()

	// poolB's numbers LOWERED, folded alone: the claude-x row is retained, and the one
	// source this run read went quiet by 1000 input. That is rule 10's day.
	swarmUsage(t, p.freddy, "j2", swarmRow("j2", "1", "-", "mercury-2.5", "serialize", "2026-09-14T02:00:00Z", "1000", "420", "0", "81000", "50"))
	p.fold("--swarm", "freddy="+p.freddy).ExitErr(1, "TOKENS SHRANK date=2026-09-14 type=input file=2410 now=1410 written=false")
	assert.Equal(t, before, p.day(), "a refused shrink rewrote the file")

	p.fold("--swarm", "freddy="+p.freddy, "--allow-shrink").ExitErr(0, "written=true")
	holds(t, p.day(), "claude-x", "\t410\t", "\t1000\t", "turns=-")
}

// A malformed existing day row fails closed before replacement: fold refuses the day,
// reports TOKENS UNREADABLE label=out with the finding reason, and leaves the raw
// malformed file on disk byte-identical.
func TestMalformedExistingDayRowFailsClosedAndPreservesRawFile(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	// The claude-x row with its sources cell blank.
	const malformed = "nova-tokens v1 day=2026-09-14 at=2026-09-14T02:00:00Z build=test turns=1 sources=swarm:glenn\n" +
		"date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n" +
		"2026-09-14\tclaude-x\tserialize\t410\t100\t0\t0\t-\t0\tutc\t\n"
	testkit.WriteFile(t, filepath.Join(p.out, "2026-09-14.tsv"), malformed)

	p.fold("--swarm", "freddy="+p.freddy).ExitErr(1, "TOKENS UNREADABLE label=out").Err("the sources cell is empty", "unreadable=1")
	assert.Equal(t, malformed, p.day(), "a malformed existing day file was modified or overwritten")
}

// An explicitly selected day whose declared source becomes empty / goes quiet is detected
// and refused under rule 10 rather than silently skipping with exit 0.
func TestExplicitDayQuietSourceDetectedAndRefused(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	p.fold("--swarm", "glenn="+p.glenn).Exit(0)
	before := p.day()
	assert.Contains(t, before, "\t410\t")

	// The declared source now has zero rows for that day: input fell from 410 to unknown
	// (now=-), and the refusal leaves the file on disk untouched.
	require.NoError(t, os.Remove(filepath.Join(p.glenn, "usage", "j1.tsv")))
	p.fold("--swarm", "glenn="+p.glenn).ExitErr(1, "TOKENS SHRANK date=2026-09-14 type=input file=410 now=- written=false").Err("shrank=4")
	assert.Equal(t, before, p.day(), "a refused quiet source on explicit day modified the day file")

	// With --allow-shrink on a day that shrank to 0 rows, fold does not write an empty day
	// file: SHRANK and DAY agree that written=false, and the file on disk is unchanged.
	p.fold("--swarm", "glenn="+p.glenn, "--allow-shrink").ExitErr(0, "TOKENS SHRANK date=2026-09-14 type=input file=410 now=- written=false").
		Out("TOKENS DAY date=2026-09-14", "written=false")
	assert.Equal(t, before, p.day(), "an empty day write modified the existing day file")
}

// When one source of a multi-source day goes quiet, shrinking under --allow-shrink preserves
// the other source's rows.
func TestExplicitDayQuietSourcePreservesOtherSources(t *testing.T) {
	t.Parallel()
	p := newPooled(t)
	both := []string{"--swarm", "glenn=" + p.glenn, "--swarm", "freddy=" + p.freddy}
	p.fold(both...).Exit(0)
	before := p.day()

	require.NoError(t, os.Remove(filepath.Join(p.glenn, "usage", "j1.tsv")))
	r := p.fold(both...).ExitErr(1, "TOKENS SHRANK date=2026-09-14 type=input file=2410 now=2000 written=false")
	assert.Equal(t, before, p.day(), "refused shrink modified file")
	// The fold refuses the day under rule 10, and the quiet source itself is named, not only
	// the totals.
	assert.Contains(t, r.Stdout+r.Stderr, "TOKENS QUIET label=swarm:glenn day=2026-09-14")

	// With --allow-shrink: the second pool's rows (2000) are written and the first pool's row is removed;
	// SHRANK and DAY agree that written=true.
	p.fold(append(both, "--allow-shrink")...).ExitErr(0, "TOKENS SHRANK date=2026-09-14 type=input file=2410 now=2000 written=true").
		Out("TOKENS DAY date=2026-09-14", "written=true")
	got := p.day()
	assert.NotContains(t, got, "claude-x")
	holds(t, got, "mercury-2.5", "\t2000\t")
}

func TestRule12TheToolStampsAndNoFlagSetsIt(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"),
		msg("m2", "2026-09-11T11:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"))
	r := b.fold("--claude", "glenn="+b.tr).Exit(0)
	holds(t, strings.Split(b.day(), "\n")[0], "nova-tokens v1 ", "day=2026-09-11", "at=2026-09-11T23:55:02Z", "build=", "turns=2", "sources=claude:glenn")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS FOLD"), "at=2026-09-11T23:55:02Z")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS DAY"), "turns=2")
	// No flag sets at=.
	b.fold("--claude", "glenn="+b.tr, "--at", "2020-01-01T00:00:00Z").Exit(2)

	// A day fed by a bus note alone counts no turns.
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-10", busDate, "2026-09-10\temma\tg\tschema\tinput\t10\n")
	novaTokens.Do(t, "fold", "--out", b.out, "--day", "2026-09-10", "--repos", b.repos, "--bus", bus).Exit(0)
	assert.Contains(t, strings.Split(testkit.ReadFile(t, filepath.Join(b.out, "2026-09-10.tsv")), "\n")[0], "turns=-")

	s := b.sum().Exit(0)
	holds(t, lineWith(s.Stdout, "SUM MONTH"), "turns=2", "at=2026-09-11T23:55:02Z")
	assert.Contains(t, lineWith(s.Stdout, "SUM TOTAL"), "turns=2")
	assert.Contains(t, lineWith(b.check().Stdout, "CHECK OK"), "at=2026-09-11T23:55:02Z")
}

func TestRule13CheckNamesEveryFindingAndFillsNoDay(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	// v is a version line and the header for 2026-09-<d>; row is one good row of it.
	v := func(d, turns string) string {
		return "nova-tokens v1 day=2026-09-" + d + " at=2026-09-11T23:55:02Z build=b" + turns + " sources=x\n" + hdr
	}
	row := func(d, model, in string) string {
		return "2026-09-" + d + "\t" + model + "\tschema\t" + in + "\t2\t3\t4\t5\t0\tutc\tx\n"
	}
	good := func(d string) string { return v(d, " turns=1") + row(d, "a", "1") }

	// ten bad files, and a gap at 09-09 between 09-08 and 09-10
	testkit.Tree(t, b.out, map[string]string{
		"2026-09-01.tsv": v("01", " turns=1") + "2026-09-01\ta\tschema\t1\t2\t3\t4\t5\t0\tutc\n",
		"2026-09-02.tsv": v("02", " turns=1") + row("03", "a", "1"),
		"2026-09-03.tsv": v("03", " turns=1") + row("03", "a", "1") + row("03", "a", "2"),
		"2026-09-04.tsv": v("04", " turns=1") + row("04", "b", "1") + row("04", "a", "2"),
		"2026-09-05.tsv": hdr + row("05", "a", "1"),
		"2026-09-06.tsv": v("06", " turns=1") + "2026-09-06\ta\tschema\t\t2\t3\t4\t5\t0\tutc\tx\n",
		"2026-09-07.tsv": v("07", " turns=1") + "2026-09-07\ta\tschema\t1\t2\t3\t4\t5\t0\t\tx\n",
		"2026-09-08.tsv": v("08", "") + row("08", "a", "1"),
		"2026-09-10.tsv": good("10"),
		// Rule 13: "the version line carries `turns=` as an integer or `-`". An EMPTY value
		// and a NEGATIVE one both passed: turns= and turns=-5 were CHECK OK, exit 0.
		"2026-09-11.tsv": v("11", " turns=") + row("11", "a", "1"),
		"2026-09-12.tsv": v("12", " turns=-5") + row("12", "a", "1"),
	})

	r := b.check("--max", "0").Exit(1).Err("2026-09-11.tsv", "2026-09-12.tsv").NotErr("CHECK MISSING date=2026-09-09")
	// The gap at 09-09 is COUNTED by default and named only when something says there
	// was spend on it. Nobody folded that day, and the day file nobody wrote is not
	// evidence that anybody worked.
	holds(t, lineWith(r.Stderr, "CHECK FAIL files="), "bad=10", "gap=1", "missing=0")
	strict := b.check("--max", "0", "--strict").ExitErr(1, "CHECK MISSING date=2026-09-09")
	assert.Contains(t, lineWith(strict.Stderr, "CHECK FAIL files="), "missing=1")

	// A --no-spend list is the other door, and it is the one a person keeps: the days it
	// does not name are the days nobody folded.
	blank := testkit.WriteFile(t, filepath.Join(b.dir, "no-spend-blank.txt"), "# nothing declared\n")
	b.check("--max", "0", "--no-spend", blank).ExitErr(1, "CHECK MISSING date=2026-09-09")
	named := testkit.WriteFile(t, filepath.Join(b.dir, "no-spend.txt"), "# the day nobody worked\n2026-09-09\tnobody was at the bench\n")
	accounted := b.check("--max", "0", "--no-spend", named).Exit(1).NotErr("CHECK MISSING") // the ten bad files are still findings
	assert.Contains(t, lineWith(accounted.Stderr, "CHECK FAIL files="), "missing=0")

	// Two answers to one question is a refusal, not a silent precedence.
	b.check("--strict", "--no-spend", named).ExitErr(2, "CHECK REFUSED")
	// And a --no-spend line that is not a day is named by its line number.
	b.check("--no-spend", testkit.WriteFile(t, filepath.Join(b.dir, "no-spend-bad.txt"), "2026-09-09\nyesterday\n")).Exit(2)

	// A clean set, with dashes and a zone, is CHECK OK.
	clean := testkit.Tree(t, filepath.Join(b.dir, "clean"), map[string]string{"2026-09-11.tsv": v("11", " turns=-") +
		"2026-09-11\ta\tschema\t-\t-\t-\t-\t-\t0\tAmerica/Los_Angeles\tx\n"})
	novaTokens.Do(t, "check", "--out", clean).Exit(0).Out("missing=0")

	// sum over the same gapped month answers rather than gating.
	b.sum().Exit(2) // a file whose stamp line is not nova-tokens v1 is refused by sum
	gapped := testkit.Tree(t, filepath.Join(b.dir, "gapped"), map[string]string{"2026-09-07.tsv": good("07"), "2026-09-08.tsv": good("08"), "2026-09-10.tsv": good("10")})
	novaTokens.Do(t, "sum", "--out", gapped, "--month", "2026-09").Exit(0).Out("missing=1")
}

func TestRule14TheSwarmUsageFilesAreASource(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	pool := filepath.Join(b.dir, "pool")
	// The reader's own sixteen names are SPEC-SWARM's, so a file the swarm writes reads.
	require.Equal(t, swarmHeader, tokens.SwarmColumns, "SwarmColumns is not SPEC-SWARM rule 12's sixteen names in order")
	swarmUsage(t, pool, "j1", swarmRow("j1", "1", "-", "deepseek-v3", "serialize", "2026-09-11T10:00:00Z", "1000", "20", "-", "5", "0"))
	swarmUsage(t, pool, "j2", swarmRow("j2", "2", "j1", "deepseek-v3", "serialize", "2026-09-11T11:00:00Z", "7", "8", "9", "10", "11"))
	swarmUsage(t, pool, "j3", swarmRow("j3", "1", "-", "deepseek-v3", "cathedral", "2026-09-11T12:00:00Z", "1", "1", "1", "1", "1"))
	// a reclaimed job directory with no usage file
	testkit.WriteFile(t, filepath.Join(pool, "done", "j4", "secret.txt"), "nothing here may be opened\n")

	r := b.fold("--swarm", "deepseek="+pool).Exit(0)
	holds(t, lineWith(r.Stdout, "TOKENS SOURCE"), "nousage=1", "reports=input,output,cache_write,cache_read,reasoning")
	day := b.day()
	// attempt 1 and attempt 2 are two rows folded into one (model, repo) key; the
	// unknown repo name goes to other.
	assert.Contains(t, day, "deepseek-v3\tother\t1\t1\t1\t1\t1\t0\tutc\tswarm:deepseek")
	row := lineWith(day, "deepseek-v3\tserialize")
	cols := strings.Split(row, "\t")
	assert.Equal(t, [3]string{"1007", "9", "11"}, [3]string{cols[3], cols[5], cols[7]}, "the two attempts did not both fold: %q", row)

	// a fifteen-column header is refused by name
	short := filepath.Join(b.dir, "short")
	testkit.WriteFile(t, filepath.Join(short, "usage", "j9.tsv"), strings.Join(swarmHeader[:15], "\t")+"\n")
	r = b.fold("--swarm", "d="+short).ExitErr(1, swarmHeader[15]).Err("TOKENS UNPARSED")
	// TOKENS NOTE is ONE remedy line, and it is the remedy for the kind that failed: a
	// swarm usage file's header wants SPEC-SWARM's sixteen columns, not a bus body line.
	note := lineWith(r.Stdout, "TOKENS NOTE")
	assert.Contains(t, note, "SPEC-SWARM rule 12")
	assert.NotContains(t, note, "date<TAB>who<TAB>", "the remedy for a swarm header refusal is the bus body-line shape")
}

func TestRule15ATypeTheSourceDidNotReportIsADashAndNeverAZero(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10, "output_tokens": 1, "cache_creation_input_tokens": 2, "cache_read_input_tokens": 3}, "/x/schema/a.go"))
	pool := filepath.Join(b.dir, "pool")
	swarmUsage(t, pool, "j1", swarmRow("j1", "t", "1", "dv3", "serialize", "2026-09-11T10:00:00Z", "5", "6", "-", "-", "7"))
	bus := oneNote(b, "2026-09-11\temma\tgem\tschema\tinput\t9\n2026-09-11\temma\tgem\tschema\toutput\t0\n")

	r := b.fold("--claude", "glenn="+b.tr, "--swarm", "d="+pool, "--bus", bus).Exit(0)
	day := b.day()
	holds(t, day, "fable\tschema\t10\t1\t2\t3\t-\t", "dv3\tserialize\t5\t6\t-\t-\t7\t", "gem\tschema\t9\t0\t-\t-\t-\t")
	// dashes counted on the day line and per column by sum
	assert.Contains(t, lineWith(r.Stdout, "TOKENS DAY"), "dashes=6")
	assert.Contains(t, lineWith(b.sum().Exit(0).Stdout, "SUM TOTAL"), "dashes=0,0,2,2,2")
	// neither who nor window is a column
	lacks(t, strings.Split(day, "\n")[1], "who", "window")
}

func TestRule15AMixedRowSumsPerTypeOverTheSourcesThatReportedIt(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "m", map[string]int{"input_tokens": 10, "output_tokens": 1, "cache_creation_input_tokens": 2, "cache_read_input_tokens": 3}, "/x/schema/a.go"))
	bus := oneNote(b, "2026-09-11\temma\tm\tschema\tinput\t5\n2026-09-11\temma\tm\tschema\treasoning\t77\n")
	b.fold("--claude", "g="+b.tr, "--bus", bus).Exit(0)
	assert.Contains(t, b.day(), "m\tschema\t15\t1\t2\t3\t77\t")
}

func TestRule16And19TheDatabaseIsCopiedAndQueriedReadOnlyUnderATimeout(t *testing.T) {
	b := newBench(t)
	scratch := testkit.Mkdir(t, filepath.Join(b.dir, "scratch"))
	db := testkit.WriteFile(t, filepath.Join(b.dir, "opencode.db"), "SQLite format 3\x00 not really\n")
	testkit.WriteFile(t, db+"-wal", "wal\n")
	logPath := fakeSqlite3(t,
		ocRows(ocSession("s1", "", "/x/schema")),
		ocRows(ocMessage("msg1", "s1", "2026-09-11T10:00:00Z", "anthropic", "mercury-2.5", "10", "20", "30", "40", "50", "/x/schema")),
		ocRows(
			ocPart("msg1", "s1", "", "/x/schema/a.go", "", ""),
			// A tool part's command holds tabs and newlines. It is the reason the code
			// asks sqlite3 for -json: under -tabs this row splits on the data inside it.
			ocPart("msg1", "s1", "cat /x/schema/b.go\tand\nmore", "", "", "")))

	// A dry run reads the database exactly as the real run does, and leaves --scratch,
	// --out and the database's own directory as they were: a file already at the path the
	// real run copies to is neither truncated nor replaced, and nothing is left behind.
	testkit.WriteFile(t, filepath.Join(scratch, "opencode-bench", "opencode.db"), "an earlier copy\n")
	for _, args := range [][]string{
		{"fold", "--out", b.out, "--day", "2026-09-11", "--repos", b.repos, "--opencode", "bench=" + db, "--scratch", scratch, "--dry-run"},
		{"report", "--who", "ada", "--day", "2026-09-11", "--repos", b.repos, "--opencode", "bench=" + db, "--scratch", scratch, "--dry-run"},
		{"sources", "--day", "2026-09-11", "--repos", b.repos, "--opencode", "bench=" + db, "--scratch", scratch},
	} {
		tree := testkit.Snapshot(t, b.dir)
		r := novaTokens.Do(t, args...).Exit(0)
		switch args[0] {
		case "sources":
			holds(t, r.Stdout, "messages=1")
		case "fold":
			holds(t, r.Stdout+r.Stderr, "dry_run=true")
			holds(t, r.Stdout, "kind=opencode", "messages=1", "would_write=true")
		default:
			holds(t, r.Stdout+r.Stderr, "dry_run=true")
			holds(t, r.Stdout, "2026-09-11\tada\tmercury-2.5\tschema\tinput\t10")
		}
		assert.Equal(t, tree, testkit.Snapshot(t, b.dir), "%s, which writes nothing, changed --scratch, --out or the database's directory", args[0])
	}

	before, err := os.Stat(db)
	require.NoError(t, err)
	b.fold("--opencode", "bench="+db, "--scratch", scratch).Exit(0)
	assert.Contains(t, b.day(), "mercury-2.5\tschema\t10\t20\t30\t40\t50\t")

	argv := testkit.ReadFile(t, logPath)
	// Every invocation is -readonly. The spec's --opencode section names providerID, the
	// five tokens.* counts, path.cwd and session.directory: JSON paths, because OpenCode
	// keeps the row in a `data` column. A query that names bare columns is `no such
	// column: providerID`.
	holds(t, argv, "-readonly", "-json", "$.providerID", "$.modelID", "$.tokens.input", "$.tokens.cache.write", "$.tokens.reasoning", "$.path.cwd", "directory FROM session")
	for _, tok := range strings.Fields(argv) {
		// filepath.IsAbs, not a leading slash: on windows an absolute path starts
		// with a drive letter, and a leading-slash reading would make this clause
		// vacuous there.
		if filepath.IsAbs(tok) {
			assert.True(t, strings.HasPrefix(tok, scratch), "sqlite3 was pointed at %q, outside --scratch", tok)
		}
	}
	after, err := os.Stat(db)
	require.NoError(t, err)
	assert.True(t, after.ModTime().Equal(before.ModTime()), "the live database changed: its modification time moved")
	assert.Equal(t, before.Size(), after.Size(), "the live database changed: its size moved")
	// --scratch is required with --opencode and refused without it.
	b.fold("--opencode", "bench="+db).Exit(2)
	b.transcript("a.jsonl", msg("m", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}))
	b.fold("--claude", "g="+b.tr, "--scratch", scratch).Exit(2)
}

func TestRule17TheDayComesFromTheMessageStamp(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-11T23:59:59Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-12T00:00:01Z", "f", map[string]int{"input_tokens": 2}, "/x/schema/a.go"))
	b.foldAll("--claude", "g="+b.tr).Exit(0)
	assert.FileExists(t, filepath.Join(b.out, "2026-09-11.tsv"))
	assert.FileExists(t, filepath.Join(b.out, "2026-09-12.tsv"))
}

// TestRule17AZonedStampFoldsOnItsUTCDayAndAnUnreadableStampIsCounted pins rule 17's own
// sentence -- "A day is a UTC day, from the message's own stamp, and a row that is not
// says so" -- on the two readers that took the stamp's first ten characters instead of
// parsing it: a transcript line stamped 2026-09-11T20:30:00-07:00 is 03:30Z on the 12th,
// and it landed in 2026-09-11.tsv with day_basis=utc. A stamp this tool cannot read is
// rule 3's business: counted and printed, never skipped silently -- it vanished.
func TestRule17AZonedStampFoldsOnItsUTCDayAndAnUnreadableStampIsCounted(t *testing.T) {
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-11T20:30:00-07:00", "f", map[string]int{"input_tokens": 7}, "/x/schema/a.go"),
		`{"type":"assistant","timestamp":"","message":{"id":"m2","model":"f","usage":{"input_tokens":9}}}`,
		`{"type":"assistant","timestamp":"the eleventh","message":{"id":"m3","model":"f","usage":{"input_tokens":9}}}`)

	r := b.foldAll("--claude", "g="+b.tr).Exit(1).Err("the eleventh")
	// 20:30 on the 11th at -07:00 is 03:30Z on the TWELFTH.
	assert.Contains(t, testkit.ReadFile(t, filepath.Join(b.out, "2026-09-12.tsv")), "f\tschema\t7\t")
	assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "the zoned stamp folded on the local day, not on its UTC day")
	// Two stamps this tool cannot read: counted, printed, and named. The count is on
	// TOKENS FAIL and the lines name the label; the transcript's own unparsed= column is
	// a dash, which is what the spec's TOKENS SOURCE paragraph says it is.
	assert.Contains(t, lineWith(r.Stdout, "TOKENS SOURCE"), "unparsed=-")
	assert.Equal(t, 2, strings.Count(r.Stderr, "TOKENS UNPARSED label=claude:g"), r)
	assert.Contains(t, lineWith(r.Stderr, "TOKENS FAIL"), "unparsed=2")

	// The same, for the OpenCode reader.
	o := newBench(t)
	scratch := testkit.Mkdir(t, filepath.Join(o.dir, "scratch"))
	db := testkit.WriteFile(t, filepath.Join(o.dir, "opencode.db"), "SQLite format 3\x00\n")
	fakeSqlite3(t,
		ocRows(ocSession("s1", "", "/x/schema")),
		ocRows(
			ocMessage("k1", "s1", "2026-09-11T20:30:00-07:00", "p", "m", "3", "", "", "", "", "/x/schema"),
			ocMessage("k2", "s1", "", "p", "m", "4", "", "", "", "", "/x/schema")),
		ocRows(ocPart("k1", "s1", "", "/x/schema/a.go", "", "")))
	r = o.foldAll("--opencode", "b="+db, "--scratch", scratch).ExitErr(1, "TOKENS UNPARSED label=opencode:b")
	assert.Contains(t, testkit.ReadFile(t, filepath.Join(o.out, "2026-09-12.tsv")), "m\tschema\t3\t")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS SOURCE"), "unparsed=-")
	assert.Contains(t, lineWith(r.Stderr, "TOKENS FAIL"), "unparsed=1")
}

func TestRule17ABusLineDatedAnotherDayIsRedatedAndCounted(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := oneNote(b, "2026-09-10\temma\tg\tschema\tinput\t5\n")
	b.foldAll("--bus", bus).Exit(0).Out("redated=1")
	assert.Contains(t, testkit.ReadFile(t, filepath.Join(b.out, "2026-09-10.tsv")), "\t5\t")
}

func TestRule18TwoFoldsDifferInNothingButTheStamp(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-11T10:00:00Z", "z", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-11T10:01:00Z", "a", map[string]int{"input_tokens": 2}, "/x/serialize/a.go"))
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "b.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes=emma-000000000001", busDate, "2026-09-11\temma\tg\tschema\tinput\t2\n")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, "2026-09-11\temma\tg\tschema\tinput\t1\n")

	b.foldAll("--claude", "g="+b.tr, "--bus", bus).Exit(0)
	first := strings.SplitN(b.day(), "\n", 2)
	at(foldStamp.Add(time.Hour)).Do(t, "fold", "--out", b.out, "--all", "--repos", b.repos, "--claude", "g="+b.tr, "--bus", bus).Exit(0)
	second := strings.SplitN(b.day(), "\n", 2)
	assert.Equal(t, first[1], second[1], "the rows differ between two folds")
	assert.NotEqual(t, first[0], second[0], "the stamp line did not change with the clock")
	assert.Equal(t, strings.ReplaceAll(first[0], "at=2026-09-11T23:55:02Z", ""), strings.ReplaceAll(second[0], "at=2026-09-12T00:55:02Z", ""), "the stamp lines differ in more than at=")
	assert.Regexp(t, "^2026-09-11\ta\t", strings.Split(first[1], "\n")[1], "rows are not sorted by (model, repo)")
}

func TestRule20ReportPrintsTheBodyAndNothingElse(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "gemini", map[string]int{"input_tokens": 100, "output_tokens": 20, "cache_creation_input_tokens": 0}, "/x/schema/a.go"))
	note := filepath.Join(b.dir, "note-body.txt")

	r := b.report("--claude", "g="+b.tr, "--note", note).Exit(0)
	for _, line := range strings.Split(strings.TrimSuffix(r.Stdout, "\n"), "\n") {
		assert.Len(t, strings.Split(line, "\t"), 6, "a report line is six fields: %q", line)
		assert.NotContains(t, line, "~", "a report line carries ~: %q", line)
		assert.NotContains(t, line, "#", "a report line carries #: %q", line)
	}
	r.NotOut("reasoning").Out("2026-09-11\temma\tgemini\tschema\tinput\t100", "2026-09-11\temma\tgemini\tschema\tcache_write\t0").
		Err("REPORT OK who=emma day=2026-09-11", "subject=tokens 2026-09-11 at=2026-09-11T23:55:02Z build=")
	assert.Equal(t, r.Stdout, testkit.ReadFile(t, note), "--note is not exactly the stdout bytes")

	// The note folds back as the same rows, through the bus, with one hand-added comment.
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "n.md", "emma-000000000001", subjectOf(t, r), busDate, r.Stdout+"# repos: schema\n")
	b.fold("--bus", bus).Exit(0).Out("unparsed=0", "TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema")
	direct := newBench(t)
	direct.fold("--claude", "g="+b.tr).Exit(0)
	stripRows := func(s string) (keep []string) {
		for _, l := range strings.Split(strings.TrimSpace(s), "\n")[2:] {
			keep = append(keep, strings.Join(strings.Split(l, "\t")[:8], "\t"))
		}
		return keep
	}
	assert.Equal(t, stripRows(direct.day()), stripRows(b.day()), "the note's rows are not the transcript's rows")
	assert.Contains(t, b.day(), "\t-\t0\tutc\tbus:emma")
}

func TestRule20ReportRefusesAndSupersedes(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "gemini", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
	note := testkit.WriteFile(t, filepath.Join(b.dir, "note.txt"), "what was there before\n")

	// A report whose every source is unreadable prints nothing and says so.
	bad := filepath.Join(b.dir, "bad")
	release := makeUnreadable(t, testkit.WriteFile(t, filepath.Join(bad, "x.jsonl"), "{}\n"))
	r := b.report("--claude", "g="+bad, "--note", note).ExitErr(1, "REPORT FAIL").Err("TOKENS UNREADABLE")
	assert.Empty(t, r.Stdout, r)
	assert.Equal(t, "what was there before\n", testkit.ReadFile(t, note), "a failed report replaced the --note file")
	assert.NoFileExists(t, note+".tmp", "a failed report left a .tmp beside the note")
	release()

	b.report("--claude", "g="+b.tr, "--supersedes", "emma-000000000002", "--supersedes", "emma-000000000001").ExitErr(0, "supersedes=emma-000000000001,emma-000000000002")
	b.report("--claude", "g="+b.tr, "--supersedes", "emma-000000000001", "--supersedes", "emma-000000000001").ExitErr(2, "REPORT REFUSED").Err("emma-000000000001")

	// Demanded test 20's last clause, which had no test: "a note built from it folds as
	// the successor of <id> (two sequential `report`s, the second superseding the first,
	// fold to the second's rows and one SUPERSEDED line)."
	first := b.report("--claude", "g="+b.tr).Exit(0)
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "n1.md", "emma-000000000001", subjectOf(t, first), busDate, first.Stdout)
	// The friend folds again -- the transcript grew -- and corrects the day by name.
	b.transcript("b.jsonl", msg("m2", "2026-09-11T11:00:00Z", "gemini", map[string]int{"input_tokens": 40}, "/x/schema/a.go"))
	second := b.report("--claude", "g="+b.tr, "--supersedes", "emma-000000000001").Exit(0)
	busNote(t, bus, "emma", "n2.md", "emma-000000000002", subjectOf(t, second), busDate, second.Stdout)

	f := b.fold("--bus", bus).Exit(0).Out("TOKENS SUPERSEDED label=bus:emma note=emma-000000000001 by=emma-000000000002 day=2026-09-11")
	// The successor is the day: 41, not 1 and not 42.
	assert.Contains(t, b.day(), "gemini\tschema\t41\t")
	assert.Equal(t, 1, strings.Count(f.Stdout, "TOKENS SUPERSEDED"), "SUPERSEDED lines for two sequential reports: %s", f)
	assert.Contains(t, lineWith(f.Stdout, "TOKENS SOURCE"), "superseded=1")
}

// subjectOf is the subject a `report` says it built, as REPORT OK prints it.
func subjectOf(t *testing.T, r testkit.Ran) string {
	t.Helper()
	_, subject, ok := strings.Cut(lineWith(r.Stderr, "REPORT OK"), "subject=")
	require.True(t, ok, "no subject= on REPORT OK: %s", r)
	return subject
}

func TestRule21AProviderExportIsUnattributedAndNeverSplit(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	// Google: per-row timestamps, folded to UTC days.
	g := testkit.WriteFile(t, filepath.Join(b.dir, "google.csv"), "timestamp,model,input_tokens,output_tokens\n2026-09-11T20:30:00-07:00,gemini-2.5-pro,100,10\n2026-09-11T10:30:00-07:00,gemini-2.5-pro,200,20\n")
	r := b.foldAll("--provider", "google:emma="+g).Exit(0)
	assert.Contains(t, testkit.ReadFile(t, filepath.Join(b.out, "2026-09-12.tsv")), "gemini-2.5-pro\tunattributed\t100\t10\t-\t-\t-\t0\tutc\tgoogle:emma")
	assert.Contains(t, b.day(), "gemini-2.5-pro\tunattributed\t200\t20\t-\t-\t-\t0\tutc\tgoogle:emma")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS SOURCE"), "reports=input,output")

	// xAI: per-day totals in a declared zone.
	x := newBench(t)
	r = x.foldAll("--provider", "xai:johnny="+testkit.WriteFile(t, filepath.Join(x.dir, "xai.csv"), "# timezone: America/Los_Angeles\ndate,model,input,output,reasoning\n2026-09-11,grok-4,9912340,301122,55\n")).Exit(0)
	assert.Contains(t, x.day(), "grok-4\tunattributed\t9912340\t301122\t-\t-\t55\t0\tAmerica/Los_Angeles\txai:johnny")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS SOURCE"), "day_basis=America/Los_Angeles")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS DAY"), "nonutc=1")
	assert.Contains(t, lineWith(x.sum().Exit(0).Stdout, "SUM TOTAL"), "nonutc=1")

	file := func(name, body string) string { return testkit.WriteFile(t, filepath.Join(b.dir, name), body) }
	g2 := file("google2.csv", "timestamp,model,input_tokens\n2026-09-11T10:00:00Z,gemini-2.5-pro,4\n")
	for _, c := range []struct {
		name     string
		provider []string
		code     int
		says     []string // on stderr
		day      string   // in the 2026-09-11 day file; "" is no day file
	}{
		{"an export with neither timestamps nor a zone", []string{"xai:johnny=" + file("nozone.csv", "date,model,input\n2026-09-11,grok-4,5\n")}, 1, []string{"TOKENS UNREADABLE"}, ""},
		{"an unknown column is unreadable, quoting the line", []string{"xai:johnny=" + file("odd.csv", "date,model,widgets\n2026-09-11,grok-4,5\n")}, 1, []string{"widgets"}, ""},
		// ONE PARSER PER EXPORT SHAPE, chosen by the label's kind. The spec's --provider
		// section: "the label names the provider and the parser (google, xai); an export
		// whose shape the parser does not know is TOKENS UNREADABLE with the first unparsed
		// line quoted, never a guess." A union of every provider's column names folded the
		// Google export above under xai's name, green -- the column that makes a number
		// traceable naming a parser that did not read it.
		{"a google export under the xai parser", []string{"xai:johnny=" + g}, 1, []string{"TOKENS UNREADABLE", "the xai parser does not know the column input_tokens"}, ""},
		// And the third parser the work list names reads its own shape.
		{"openai", []string{"openai:stella=" + file("openai.csv", "timestamp,model,prompt_tokens,completion_tokens,cached_tokens\n2026-09-11T10:00:00Z,gpt-5,11,22,33\n")}, 0, nil,
			"gpt-5\tunattributed\t11\t22\t-\t33\t-\t0\tutc\topenai:stella"},
		// Two friends' exports from ONE provider are two sources, which a label that WAS the
		// parser name could not express.
		{"two friends of one provider", []string{"google:emma=" + g2, "google:freddy=" + g2}, 0, nil, "google:emma,google:freddy"},
		// A --provider with no kind, and one whose kind names no parser, are refusals.
		{"no kind", []string{"emma=" + g2}, 2, nil, ""},
		{"a kind that names no parser", []string{"gerbil:emma=" + g2}, 2, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			o := newBench(t)
			var args []string
			for _, p := range c.provider {
				args = append(args, "--provider", p)
			}
			o.foldAll(args...).Exit(c.code).Err(c.says...)
			if c.day == "" {
				assert.NoFileExists(t, filepath.Join(o.out, "2026-09-11.tsv"))
				return
			}
			assert.Contains(t, o.day(), c.day)
		})
	}
}

// Rule 17's mixed row, from two provider exports; and TestTheMixedRemedyNamesTheTwoLabels,
// the TOKENS NOTE sentence "if a row mixed two day bases it names the two labels": the
// mixed remedy told the caller to declare one export for that day without saying which two
// were competing.
func TestRule17AMixedRowIsRefused(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	g := testkit.WriteFile(t, filepath.Join(b.dir, "google.csv"), "timestamp,model,input_tokens\n2026-09-11T10:00:00Z,m,5\n")
	x := testkit.WriteFile(t, filepath.Join(b.dir, "xai.csv"), "# timezone: America/Los_Angeles\ndate,model,input\n2026-09-11,m,7\n")
	r := b.foldAll("--provider", "google:emma="+g, "--provider", "xai:johnny="+x).ExitErr(1, "TOKENS MIXED date=2026-09-11 model=m repo=unattributed").Err("mixed=1")
	assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "a mixed row was written")
	holds(t, lineWith(r.Stdout, "TOKENS NOTE"), "google:emma", "xai:johnny")
}

func TestRule21ANoteOfOneReposCommentIsValidWithZeroRows(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := oneNote(b, "# repos: schema, serialize\n\n")
	r := b.foldAll("--bus", bus).Exit(0).Out("TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema,serialize", "unparsed=0")
	assert.Contains(t, lineWith(r.Stdout, "TOKENS SOURCE"), "rows=0")
}

// TestRule19TheTimeoutDefaultIsTwoMinutes pins SPEC-TOKENS' own sentence, "`--timeout`
// unset is 120 and a test asserts it", on the flag the verbs actually declare rather than
// on the constant alone: a default is a promise about the unset flag, and the two could
// drift. A run cannot be the test here -- the assertion is that nothing waits two minutes.
func TestRule19TheTimeoutDefaultIsTwoMinutes(t *testing.T) {
	t.Parallel()
	fs := &tool.Flags{FlagSet: flag.NewFlagSet("fold", flag.ContinueOnError)}
	declareSources(fs)
	require.NoError(t, fs.Parse(nil))
	assert.Equal(t, 120, fs.Lookup("timeout").Value.(flag.Getter).Get(), "--timeout unset")
	assert.Equal(t, "120", fs.Lookup("timeout").DefValue, "the flag's declared default")
	assert.Equal(t, 120*time.Second, tokens.DefaultTimeout)
}

// TestRule20ABusNoteWithSixAndSevenFieldLinesForOneKeyIsMixed pins rule 20's own clause:
// "a note with a six-field and a seven-field line for one `(date, model, repo)` is
// `TOKENS MIXED` for that row." The MIXED path was only ever exercised through two
// PROVIDER exports; a single note can do it alone, because the seventh field is the
// line's day basis and a six-field line is UTC.
func TestRule20ABusNoteWithSixAndSevenFieldLinesForOneKeyIsMixed(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := oneNote(b, "2026-09-11\temma\tg\tschema\tinput\t100\n2026-09-11\temma\tg\tschema\tinput\t5\tday_basis=America/Los_Angeles\n")
	r := b.foldAll("--bus", bus).ExitErr(1, "TOKENS MIXED").Err("2026-09-11")
	assert.Contains(t, lineWith(r.Stderr, "TOKENS FAIL"), "mixed=1")
	// A row fed by two bases is not written, and the one remedy line is about the bases.
	if day, err := os.ReadFile(filepath.Join(b.out, "2026-09-11.tsv")); err == nil {
		lacks(t, string(day), "\t100\t", "\t105\t")
	}
}

// TestReportCountsAndPrintsEverythingItDropped pins rule 20's "the same sources and the
// same attribution as fold" on the part that is not a row: a transcript line whose stamp
// this tool cannot read, and a message with no id. Both are counted by the reader and
// dropped before the body, and `report` prints both: otherwise REPORT OK, exit 0, and a
// short day posted to the bus with nothing anywhere saying so.
func TestReportCountsAndPrintsEverythingItDropped(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 3}, "/x/schema/a.go"),
		msg("m2", "yesterday", "f", map[string]int{"input_tokens": 5}, "/x/schema/a.go"),
		msg("", "2026-09-11T11:00:00Z", "f", map[string]int{"input_tokens": 7}, "/x/schema/a.go"))

	// The OK line is the grammar's, so what says the day is short is the line above it,
	// the note, and the exit code -- and the body still printed.
	r := b.report("--claude", "g="+b.tr).ExitErr(1, "TOKENS UNPARSED label=claude:g").Err("yesterday", "REPORT OK who=emma day=2026-09-11 rows=1")
	assert.Equal(t, 1, strings.Count(r.Stderr, "TOKENS UNPARSED"), r)
	// The no-id message is spend that was read and dropped, and it is named.
	holds(t, lineWith(r.Stderr, "TOKENS NOTE"), "no id", "claude:g")
	// Exit 1 still writes: the body is what it could compute, and it is only the 3.
	r.Out("2026-09-11\temma\tf\tschema\tinput\t3").NotOut("\t15")
}

// TestABusLaneWithASixFieldAndASevenFieldLineIsMixed pins the TOKENS SOURCE paragraph:
// day_basis is "`mixed` when one lane's lines carry more than one". A six-field line means
// UTC (rule 6), so a lane with one of each carries two bases -- and the field named the
// zone, because the utc member was dropped before the merge.
func TestABusLaneWithASixFieldAndASevenFieldLineIsMixed(t *testing.T) {
	t.Parallel()
	const zoned = "2026-09-11\temma\tzonemodel\tschema\tinput\t5\tday_basis=America/Los_Angeles\n"
	// Two different models, so the two bases are two rows and not one mixed row. A lane
	// whose lines all carry the one zone names that zone, and a lane of six-field lines
	// alone is utc: mixed is a fact about the lane, not a default.
	for body, want := range map[string]string{"2026-09-11\temma\tutcmodel\tschema\tinput\t100\n" + zoned: "day_basis=mixed", zoned: "day_basis=America/Los_Angeles"} {
		b := newBench(t)
		bus := oneNote(b, body)
		assert.Contains(t, lineWith(b.foldAll("--bus", bus).Exit(0).Stdout, "TOKENS SOURCE"), want)
	}
}

// TestADayIsADateOnTheCalendar pins rule 6's "exactly `tokens YYYY-MM-DD`" and rule 17's
// "each names the day its tokens count to" as what they say: a DAY. The shape alone was
// the test, so --day 2026-13-40 was accepted and wrote 2026-13-40.tsv.
//
// TestSumRejectsMonth13AndInvalidMonths: a month is a calendar month (01-12), not just
// seven characters with a hyphen, rejected with exit 2 before the output directory is read.
func TestADayIsADateOnTheCalendar(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
	fold := func(day string) testkit.Ran {
		return novaTokens.Do(t, "fold", "--out", b.out, "--day", day, "--repos", b.repos, "--claude", "g="+b.tr)
	}
	for _, bad := range []string{"2026-13-40", "2026-02-30", "2026-00-10", "2026-09-31"} {
		fold(bad).ExitErr(2, "--day is not a day: "+bad)
		assert.NoFileExists(t, filepath.Join(b.out, bad+".tsv"))
	}
	for _, bad := range []string{"2026-13", "2026-00", "2026-99", "2026-1", "bad-month"} {
		novaTokens.Do(t, "sum", "--out", b.out, "--month", bad).ExitErr(2, "SUM REFUSED: --month is not a month: "+bad)
	}
	// A leap day that exists is a day.
	fold("2024-02-29").Exit(0)
}

// TestASwarmFileWithALeadingBlankLineStillValidatesItsHeader pins the --swarm section: "A
// file whose header is not the sixteen names in order is TOKENS UNPARSED naming the file
// and the first wrong column." The header was whatever line 1 was, so one blank line at
// the top meant the header was never checked at all: every row after it was read against
// an empty column map -- every lookup column 0 -- and the named refusal never came. The
// same file without the blank line refuses in exactly the same words.
func TestASwarmFileWithALeadingBlankLineStillValidatesItsHeader(t *testing.T) {
	t.Parallel()
	wrong := slices.Clone(swarmHeader)
	wrong[2] = "nonsense"
	file := strings.Join(wrong, "\t") + "\n" + swarmRow("j1", "1", "-", "m", "schema", "2026-09-11T10:00:00Z", "1", "2", "3", "4", "5") + "\n"
	for _, body := range []string{"\n" + file, file} {
		b := newBench(t)
		pool := filepath.Join(b.dir, "pool")
		testkit.WriteFile(t, filepath.Join(pool, "usage", "j1.tsv"), body)
		b.fold("--swarm", "d="+pool).ExitErr(1, "TOKENS UNPARSED label=swarm:d").Err("nonsense", swarmHeader[2])
	}
}

// TestABusNoteWithAHeadingReportsTheFileLineNumber pins the grammar's `line=<n>`: it is
// the line in the FILE, never a count of parsed header KEYS, which would report every body
// line early for a note with the leading `# heading` nova-bus writes -- or a repeated or
// malformed header line. Every other fixture in this package writes a plain five-key
// header with no heading, the one shape that count would get right.
func TestABusNoteWithAHeadingReportsTheFileLineNumber(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	// 1 heading, 2 blank, 3-7 header (one bulleted), 8 blank, 9 good, 10 prose.
	testkit.WriteFile(t, filepath.Join(bus, "from-emma", "a.md"), strings.Join([]string{
		"# tokens 2026-09-11", "",
		"From: Emma", "To: Rowan", "- Date: " + busDate, "Id: emma-000000000001", "Subject: tokens 2026-09-11", "",
		"2026-09-11\temma\tg\tschema\tinput\t100", "this line is prose", "",
	}, "\n"))
	b.foldAll("--bus", bus).ExitErr(1, "line=10: this line is prose")
	// And the row from line 9 still folded.
	assert.Contains(t, b.day(), "\t100\t")
}

// TestAFailedDayWriteIsInsideTheUnreadableCap pins rule 11's contract on the one listing
// that can grow late: "every listing is capped at --max ... one MORE line naming the
// remedy; every count is uncapped". A day file this run cannot write is an unreadable, and
// it arrives after every source's unreadables, so the listing's MORE line is printed only
// after the last line either can get.
func TestAFailedDayWriteIsInsideTheUnreadableCap(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl",
		msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
		msg("m2", "2026-09-12T10:00:00Z", "f", map[string]int{"input_tokens": 2}, "/x/schema/a.go"))
	// A DIRECTORY where each day file goes: the rename onto it fails on every OS.
	testkit.Mkdir(t, filepath.Join(b.out, "2026-09-11.tsv"))
	testkit.Mkdir(t, filepath.Join(b.out, "2026-09-12.tsv"))
	r := b.foldAll("--claude", "g="+b.tr, "--max", "1").Exit(1)
	assert.Equal(t, 1, strings.Count(r.Stderr, "TOKENS UNREADABLE"), "TOKENS UNREADABLE lines under --max 1: %s", r)
	assert.Contains(t, r.Stdout+r.Stderr, "TOKENS MORE kind=unreadable shown=1 total=2")
	assert.Contains(t, lineWith(r.Stderr, "TOKENS FAIL"), "unreadable=2")
}

// TestReportKeepsTheLinesForEveryKeyThatIsNotMixed pins rule 20's own words: a report
// whose sources give one (model, repo) two bases prints TOKENS MIXED, "no line for that
// key", REPORT FAIL, exit 1. The verb threw away the whole body instead, so one mixed key
// hid every other key the day had. A FAIL still writes nothing: the note file is unchanged.
func TestReportKeepsTheLinesForEveryKeyThatIsNotMixed(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	note := testkit.WriteFile(t, filepath.Join(b.dir, "note.txt"), "what was there before\n")
	g := testkit.WriteFile(t, filepath.Join(b.dir, "google.csv"), "timestamp,model,input_tokens\n2026-09-11T12:00:00Z,mixed-model,100\n2026-09-11T12:00:00Z,clean-model,7\n")
	x := testkit.WriteFile(t, filepath.Join(b.dir, "xai.csv"), "# timezone: America/Los_Angeles\ndate,model,input\n2026-09-11,mixed-model,5\n")

	b.report("--note", note, "--provider", "google:emma="+g, "--provider", "xai:johnny="+x).ExitErr(1, "TOKENS MIXED").Err("REPORT FAIL").
		Out("clean-model").NotOut("mixed-model")
	assert.Equal(t, "what was there before\n", testkit.ReadFile(t, note), "a failed report replaced the --note file")
}

// TestEveryVerbRefusesAPositionalArgument: every verb's shape in the usage block is flags
// only. Four of the five parsed the extra word and dropped it, so `nova-tokens sum --out X
// --month Y extra` answered about something the caller did not ask about.
func TestEveryVerbRefusesAPositionalArgument(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
	for _, args := range [][]string{
		{"fold", "--out", b.out, "--day", "2026-09-11", "--repos", b.repos, "--claude", "g=" + b.tr},
		{"sources", "--repos", b.repos, "--claude", "g=" + b.tr},
		{"report", "--who", "emma", "--day", "2026-09-11", "--repos", b.repos, "--claude", "g=" + b.tr},
		{"sum", "--out", b.out, "--month", "2026-09"},
		{"check", "--out", b.out},
	} {
		novaTokens.Do(t, append(args, "extra")...).ExitErr(2, "takes no positional arguments").Err("extra")
	}
}

// TestAWholeNoteRefusalCarriesTheSubjectsFileLine finishes what the body-line fix started:
// `line=` is the line in the FILE for every UNPARSED line, not only the ones that come
// from a body. A refused trailer, a refused predecessor set and a cycle each refuse the
// note at its Subject:, and all three printed line=1 while the same run numbered a prose
// line in the same file correctly.
func TestAWholeNoteRefusalCarriesTheSubjectsFileLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, supersedes, want string }{
		{"a duplicate predecessor", "emma-000000000001,emma-000000000001", "twice"},
		{"a predecessor in another lane", "bo-000000000001", "another lane"},
		{"a cycle", "emma-00000000000f", "cycle"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			bus := busDir(t, filepath.Join(b.dir, "bus"), "emma", "bo")
			// 1 heading, 2 blank, 3 From, 4 To, 5 Date, 6 Id, 7 Subject, 8 blank, 9 body.
			testkit.WriteFile(t, filepath.Join(bus, "from-emma", "z.md"), strings.Join([]string{
				"# tokens 2026-09-11", "",
				"From: Emma", "To: Rowan", "Date: " + busDate, "Id: emma-00000000000f",
				"Subject: tokens 2026-09-11 at=2026-09-11T22:00:00Z build=b supersedes=" + c.supersedes, "",
				"2026-09-11\temma\tg\tschema\tinput\t900", "",
			}, "\n"))
			busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, "2026-09-11\temma\tg\tschema\tinput\t100\n")
			busNote(t, bus, "bo", "x.md", "bo-000000000001", "tokens 2026-09-11", busDate, "2026-09-11\tbo\tg\tschema\tinput\t1\n")
			r := b.foldAll("--bus", bus).ExitErr(1, "note=emma-00000000000f line=7")
			assert.Contains(t, strings.ToLower(r.Stderr), c.want)
			assert.NotContains(t, b.day(), "\t900\t")
		})
	}
}

// TestAProviderUnparsedLineIsTheLineInTheFile: the comment-stripped view is not the file.
// The `# timezone:` declaration every zoned export must carry is dropped before the CSV
// reader sees it, so the record index was one short from the first row onward -- and a
// quoted field holding a newline made it drift further with every one.
func TestAProviderUnparsedLineIsTheLineInTheFile(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	x := testkit.WriteFile(t, filepath.Join(b.dir, "xai.csv"), strings.Join([]string{
		"# timezone: America/Los_Angeles", // 1
		"date,model,input",                // 2
		"2026-09-11,grok-4,5",             // 3
		"not-a-day,grok-4,6",              // 4
		"",
	}, "\n"))
	b.foldAll("--provider", "xai:johnny="+x).ExitErr(1, "line=4").Err("not-a-day")

	// A field carrying a newline is one record over two file lines, and the record after
	// it is still numbered by the file.
	g := testkit.WriteFile(t, filepath.Join(b.dir, "google.csv"), strings.Join([]string{
		"timestamp,model,input_tokens",           // 1
		"2026-09-11T10:00:00Z,\"gemini\n2.5\",1", // 2-3
		"nope,gemini,2",                          // 4
		"",
	}, "\n"))
	newBench(t).foldAll("--provider", "google:emma="+g).ExitErr(1, "line=4")
}
