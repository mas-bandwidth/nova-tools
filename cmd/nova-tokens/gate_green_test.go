package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// THE GATE ON THE ONLY DIRECTORY ANYBODY POINTS IT AT.
//
// Dogfood, 2026-09-18: `nova-tokens check --out reports/tokens` was red, and had been red
// since the day the directory was first folded. Forty findings, every one of them correct:
// 36 `missing` for the calendar days between 2026-07-30 and 2026-09-06 that nobody worked,
// and 4 `stray` for the README that explains the directory, the collator's log, the
// `pre-nova-tokens/` archive of what the day files replaced, and a session note. None of
// the forty was work anybody would ever do, so the gate had become a line people skipped —
// which is worse than no gate, because a red that means nothing hides a red that means
// something.
//
// The fixture is cut from that directory's own file names, so this test is the directory.
func reportsTokensNames() (days []string, others []string, dirs []string) {
	days = []string{
		"2026-07-29", "2026-07-30", "2026-08-15",
		"2026-09-06", "2026-09-07", "2026-09-08", "2026-09-09", "2026-09-10",
		"2026-09-11", "2026-09-12", "2026-09-13", "2026-09-14", "2026-09-15",
		"2026-09-16", "2026-09-17", "2026-09-18",
	}
	others = []string{"README.md", "collate.log", "session-151250bd-2026-09-14.md"}
	dirs = []string{"pre-nova-tokens"}
	return days, others, dirs
}

// reportsTokensFixture writes a directory with those names: a well-formed day file for
// each day, and the four non-day entries exactly as they are on disk.
func reportsTokensFixture(t *testing.T) string {
	t.Helper()
	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	out := mkdir(t, filepath.Join(t.TempDir(), "tokens"))
	days, others, dirs := reportsTokensNames()
	for _, d := range days {
		write(t, filepath.Join(out, d+".tsv"),
			"nova-tokens v1 day="+d+" at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+
				d+"\tclaude-fable-5-1\tschema\t1\t2\t3\t4\t-\t0\tutc\tx\n")
	}
	for _, name := range others {
		write(t, filepath.Join(out, name), "what a person put beside the day files\n")
	}
	for _, name := range dirs {
		mkdir(t, filepath.Join(out, name))
		write(t, filepath.Join(out, name, "daily-2026-08.tsv"), "the table the day files replaced\n")
	}
	return out
}

// CHECK OK IS REACHABLE ON THAT DIRECTORY AS IT IS. No file moved, nothing removed, no
// list maintained: the gaps and the notes are counted on the line, and neither is a
// finding, because nothing in that directory says anybody worked on 2026-08-03.
func TestCheckIsGreenOnTheReportsTokensDirectoryAsItIs(t *testing.T) {
	out := reportsTokensFixture(t)
	r := invoke(t, "check", "--out", out)
	wantExit(t, r, 0)
	line := lineWith(r.stdout, "CHECK OK")
	wantContains(t, line, "files=16")
	wantContains(t, line, "first=2026-07-29")
	wantContains(t, line, "last=2026-09-18")
	wantContains(t, line, "missing=0")
	wantContains(t, line, "stray=0")
	// NOTHING WAS HIDDEN TO MAKE IT GREEN. The 36 calendar days with no file and the 4
	// entries that are not day files are both on the OK line, counted.
	wantContains(t, line, "gap=36")
	wantContains(t, line, "notes=4")
	wantNotContains(t, r.all(), "CHECK MISSING")
	wantNotContains(t, r.all(), "CHECK STRAY")
}

// And --strict is the old reading, whole: the same forty findings, so a person who wants
// them has them and nobody had to argue about which ones to keep.
func TestCheckStrictRestoresEveryFindingTheGateUsedToMake(t *testing.T) {
	out := reportsTokensFixture(t)
	r := invoke(t, "check", "--out", out, "--strict", "--max", "0")
	wantExit(t, r, 1)
	line := lineWith(r.stderr, "CHECK FAIL files=")
	wantContains(t, line, "missing=36")
	wantContains(t, line, "stray=4")
	wantContains(t, line, "bad=0")
	if n := strings.Count(r.stderr, "CHECK MISSING "); n != 36 {
		t.Errorf("%d CHECK MISSING lines under --strict, want 36", n)
	}
	if n := strings.Count(r.stderr, "CHECK STRAY "); n != 4 {
		t.Errorf("%d CHECK STRAY lines under --strict, want 4", n)
	}
	// The strays are named by path, and the archive DIRECTORY is one of them.
	for _, want := range []string{"README.md", "collate.log", "pre-nova-tokens", "session-151250bd-2026-09-14.md"} {
		wantContains(t, r.stderr, want)
	}
}

// A .tsv that is not a day, and a file with no extension at all, are strays under BOTH
// readings: the allowlist is three shapes, not "anything that is not a day file".
func TestTheAllowlistDoesNotSwallowARealStray(t *testing.T) {
	out := reportsTokensFixture(t)
	write(t, filepath.Join(out, "daily-2026-09.tsv"), "a month file from the prototype\n")
	write(t, filepath.Join(out, "scratch"), "no extension\n")
	mkdir(t, filepath.Join(out, "working"))
	r := invoke(t, "check", "--out", out)
	wantExit(t, r, 1)
	line := lineWith(r.stderr, "CHECK FAIL files=")
	wantContains(t, line, "stray=3")
	wantContains(t, line, "notes=4")
	for _, want := range []string{"daily-2026-09.tsv", "scratch", "working"} {
		wantContains(t, r.stderr, want)
	}
}

// ------------------------------------------------------------ sources --unattributed

// `other=81%` on a day line (measured on this bench, 2026-09-18) is a diagnosis with no
// remedy: it says the rules file is not good enough and nothing about which line to add.
// `sources --unattributed` is the evidence — the path stems that were seen and matched no
// rule, heaviest first, in the shape a rule matches.
func TestSourcesUnattributedNamesThePathsThatFellToOther(t *testing.T) {
	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir) // names `schema` and `serialize`, and nothing else

	var lines []string
	// Three messages under one unnamed tree, two under another, one under a repo the
	// rules DO name: only the five unattributed ones are tallied.
	for i, p := range []string{
		"/Users/glenn/deepseek-working-3/cmd/a.go",
		"/Users/glenn/deepseek-working-3/cmd/b.go",
		"/Users/glenn/deepseek-working-3/internal/c.go",
		"/Users/glenn/rowan-working/nova-tools/cmd/d.go",
		"/Users/glenn/rowan-working/nova-tools/internal/e.go",
		"/x/schema/f.go",
	} {
		lines = append(lines, msg(fmt.Sprintf("m%d", i), "2026-09-11T10:00:00Z", "fable",
			map[string]int{"input_tokens": 10}, p))
	}
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join(lines, "\n")+"\n")

	r := invoke(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr, "--unattributed", "--max", "20")
	wantExit(t, r, 0)
	// Heaviest first, keyed by the leading directory rather than by the file, because a
	// list of files is not a list of candidate rules.
	first := strings.Split(strings.TrimSpace(r.stdout), "\n")
	var stems []string
	for _, line := range first {
		if strings.HasPrefix(line, "SOURCES UNATTRIBUTED ") {
			stems = append(stems, line)
		}
	}
	if len(stems) != 2 {
		t.Fatalf("%d SOURCES UNATTRIBUTED lines, want 2:\n%s", len(stems), r.stdout)
	}
	// One unnamed tree is ONE stem however many directories inside it were touched: the
	// three `deepseek-working-3` paths sit in two directories and arrive as one line.
	wantContains(t, stems[0], "stem=/Users/glenn/deepseek-working-3 tokens=3")
	wantContains(t, stems[1], "stem=/Users/glenn/rowan-working tokens=2")
	// The path the rules DO name never reaches the tally.
	wantNotContains(t, r.all(), "schema")
	wantContains(t, lineWith(r.stdout, "SOURCES OK"), "unattributed=5")

	// Without the flag nothing is tallied, and the field is a dash: a dash is an absence
	// where a zero is a measurement (rule 15's reading, applied to this count).
	plain := invoke(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr)
	wantExit(t, plain, 0)
	wantNotContains(t, plain.all(), "SOURCES UNATTRIBUTED")
	wantContains(t, lineWith(plain.stdout, "SOURCES OK"), "unattributed=-")
}

// The listing is capped like every other listing here, with the one MORE line that says
// what was not shown and how to see it.
func TestSourcesUnattributedIsCappedWithARemedy(t *testing.T) {
	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, msg(fmt.Sprintf("m%d", i), "2026-09-11T10:00:00Z", "fable",
			map[string]int{"input_tokens": 10}, fmt.Sprintf("/home/nova/tree-%02d/a.go", i)))
	}
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join(lines, "\n")+"\n")

	r := invoke(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr, "--unattributed", "--max", "20")
	wantExit(t, r, 0)
	if n := strings.Count(r.stdout, "SOURCES UNATTRIBUTED "); n != 20 {
		t.Errorf("%d unattributed lines at --max 20, want 20", n)
	}
	wantContains(t, r.stdout, "SOURCES MORE kind=unattributed shown=20 total=25")
	wantContains(t, r.stdout, "--max 0")

	all := invoke(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr, "--unattributed", "--max", "0")
	wantExit(t, all, 0)
	if n := strings.Count(all.stdout, "SOURCES UNATTRIBUTED "); n != 25 {
		t.Errorf("%d unattributed lines at --max 0, want all 25", n)
	}
	wantNotContains(t, all.stdout, "SOURCES MORE kind=unattributed")
}

// A message naming several unattributed paths counts each of them: `other` is one repo for
// the row, and the tally is about the PATHS, which is what a rules file matches.
func TestSourcesUnattributedCountsEveryTokenOfAMessage(t *testing.T) {
	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	// One message, two path-like inputs, two different trees: the row is one `other` and
	// the tally is two, which is the whole difference between a repo and a path.
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable",
		map[string]int{"input_tokens": 10},
		"/home/nova/tree/a.go", "/home/nova/elsewhere/c.go")+"\n")
	r := invoke(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr, "--unattributed")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "SOURCES UNATTRIBUTED stem=/home/nova/tree tokens=1")
	wantContains(t, r.stdout, "SOURCES UNATTRIBUTED stem=/home/nova/elsewhere tokens=1")
	wantContains(t, lineWith(r.stdout, "SOURCES OK"), "unattributed=2")
}
