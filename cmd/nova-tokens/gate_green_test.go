package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
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
// The fixture is cut from that directory's own file names, so each row runs on the
// directory: a well-formed day file for each day, and the four non-day entries exactly as
// they are on disk. The summary line is on stdout when green and on stderr when red.
func TestCheckIsGreenOnTheReportsTokensDirectoryAsItIs(t *testing.T) {
	t.Parallel()

	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	dir := map[string]string{
		"README.md":                         "what a person put beside the day files\n",
		"collate.log":                       "what a person put beside the day files\n",
		"session-151250bd-2026-09-14.md":    "what a person put beside the day files\n",
		"pre-nova-tokens/daily-2026-08.tsv": "the table the day files replaced\n",
	}
	for _, d := range []string{"2026-07-29", "2026-07-30", "2026-08-15", "2026-09-06", "2026-09-07", "2026-09-08", "2026-09-09", "2026-09-10",
		"2026-09-11", "2026-09-12", "2026-09-13", "2026-09-14", "2026-09-15", "2026-09-16", "2026-09-17", "2026-09-18"} {
		dir[d+".tsv"] = "nova-tokens v1 day=" + d + " at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n" + hdr + d + "\tclaude-fable-5-1\tschema\t1\t2\t3\t4\t-\t0\tutc\tx\n"
	}
	for _, c := range []struct {
		name   string
		extra  map[string]string
		args   []string
		exit   int
		fields []string       // on the summary line
		counts map[string]int // finding lines on stderr
		named  []string       // on stderr
		absent []string       // on either stream
	}{
		// CHECK OK IS REACHABLE ON THAT DIRECTORY AS IT IS. No file moved, nothing removed,
		// no list maintained: NOTHING WAS HIDDEN TO MAKE IT GREEN, the 36 calendar days with
		// no file and the 4 entries that are not day files are on the OK line, counted, and
		// neither is a finding, because nothing in that directory says anybody worked on
		// 2026-08-03.
		{"as it is", nil, nil, 0, []string{"CHECK OK", "files=16", "first=2026-07-29", "last=2026-09-18", "missing=0", "stray=0", "gap=36", "notes=4"},
			nil, nil, []string{"CHECK MISSING", "CHECK STRAY"}},
		// --strict names every gap and every note: the same forty findings, so a person who wants
		// them has them and nobody had to argue about which ones to keep. The strays are
		// named by path, and the archive DIRECTORY is one of them.
		{"--strict restores every finding", nil, []string{"--strict", "--max", "0"}, 1, []string{"CHECK FAIL files=", "missing=36", "stray=4", "bad=0"},
			map[string]int{"CHECK MISSING ": 36, "CHECK STRAY ": 4}, []string{"README.md", "collate.log", "pre-nova-tokens", "session-151250bd-2026-09-14.md"}, nil},
		// A .tsv that is not a day, and a file with no extension at all, are strays under
		// BOTH readings: the allowlist is three shapes, not "anything that is not a day file".
		{"a real stray is not swallowed", map[string]string{"daily-2026-09.tsv": "a month file from the prototype\n", "scratch": "no extension\n", "working/.keep": ""},
			nil, 1, []string{"CHECK FAIL files=", "stray=3", "notes=4"}, nil, []string{"daily-2026-09.tsv", "scratch", "working"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := testkit.Tree(t, filepath.Join(t.TempDir(), "tokens"), dir)
			testkit.Tree(t, out, c.extra)
			r := novaTokens.Do(t, append([]string{"check", "--out", out}, c.args...)...).Exit(c.exit).Err(c.named...).NotOut(c.absent...).NotErr(c.absent...)
			summary := r.Stdout
			if c.exit != 0 {
				summary = r.Stderr
			}
			linesHold(t, summary, map[string][]string{c.fields[0]: c.fields[1:]}, summary)
			for line, n := range c.counts {
				assert.Equal(t, n, strings.Count(r.Stderr, line), "%q lines", line)
			}
		})
	}
}

// `other=81%` on a day line (measured on this bench, 2026-09-18) is a diagnosis with no
// remedy: it says the rules file is not good enough and nothing about which line to add.
// `sources --unattributed` is the evidence — the path stems that were seen and matched no
// rule, heaviest first, in the shape a rule matches. Each row is one transcript and the
// runs of `sources --repos <schema, serialize> --claude g=<it>` over it.
func TestSourcesUnattributedNamesThePathsThatFellToOther(t *testing.T) {
	t.Parallel()

	onePerPath := func(paths ...string) []string {
		var lines []string
		for i, p := range paths {
			lines = append(lines, msg(fmt.Sprintf("m%d", i), "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10}, p))
		}
		return lines
	}
	var trees []string
	for i := 0; i < 25; i++ {
		trees = append(trees, fmt.Sprintf("/home/nova/tree-%02d/a.go", i))
	}
	type run struct {
		args            []string
		source, ok, out []string // fields on the SOURCES SOURCE and SOURCES OK lines; text on stdout
		not             []string // on neither stream
		unattributed    int      // SOURCES UNATTRIBUTED lines
	}
	for _, c := range []struct {
		name  string
		lines []string
		runs  []run
	}{
		// Three messages under one unnamed tree, two under another, one under a repo the
		// rules DO name: only the five unattributed ones are tallied, heaviest first, keyed by
		// the leading directory rather than by the file, because a list of files is not a
		// list of candidate rules. One unnamed tree is ONE stem however many directories
		// inside it were touched. Without the flag nothing is tallied and the field is a
		// dash: an absence, where a zero is a measurement (rule 15's reading).
		{"the paths that fell to other", onePerPath("/Users/glenn/deepseek-working-3/cmd/a.go", "/Users/glenn/deepseek-working-3/cmd/b.go",
			"/Users/glenn/deepseek-working-3/internal/c.go", "/Users/glenn/rowan-working/nova-tools/cmd/d.go",
			"/Users/glenn/rowan-working/nova-tools/internal/e.go", "/x/schema/f.go"), []run{
			{args: []string{"--all", "--unattributed", "--max", "20"}, ok: []string{"unattributed=5"}, not: []string{"schema"}, unattributed: 2,
				out: []string{"SOURCES UNATTRIBUTED stem=/Users/glenn/deepseek-working-3 tokens=3\nSOURCES UNATTRIBUTED stem=/Users/glenn/rowan-working tokens=2\n"}},
			{args: []string{"--all"}, ok: []string{"unattributed=-"}, not: []string{"SOURCES UNATTRIBUTED"}},
		}},
		// The listing is capped like every other listing here, with the one MORE line that
		// says what was not shown and how to see it.
		{"capped with a remedy", onePerPath(trees...), []run{
			{args: []string{"--all", "--unattributed", "--max", "20"}, unattributed: 20, out: []string{"SOURCES MORE kind=unattributed shown=20 total=25", "--max 0"}},
			{args: []string{"--all", "--unattributed", "--max", "0"}, unattributed: 25, not: []string{"SOURCES MORE kind=unattributed"}},
		}},
		// One message, two path-like inputs, two different trees: the row is one `other` and
		// the tally is two, which is the whole difference between a repo and a path.
		{"every path of a message", []string{msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10}, "/home/nova/tree/a.go", "/home/nova/elsewhere/c.go")}, []run{
			{args: []string{"--all", "--unattributed"}, ok: []string{"unattributed=2"},
				out: []string{"SOURCES UNATTRIBUTED stem=/home/nova/tree tokens=1", "SOURCES UNATTRIBUTED stem=/home/nova/elsewhere tokens=1"}},
		}},
		// --day filters the day-scoped tallies (messages, rows and unattributed stems) to the
		// requested day, while the inventory (files, unreadables, unparsed) stays source-wide;
		// --all covers every day. Each day has one message on a named repo and one on an
		// unmatched stem: two rows.
		{"--day scopes the tallies", []string{
			msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10}, "/x/schema/a.go"),
			msg("m2", "2026-09-11T11:00:00Z", "fable", map[string]int{"input_tokens": 20}, "/x/unmatched1/b.go"),
			msg("m3", "2026-09-12T10:00:00Z", "fable", map[string]int{"input_tokens": 30}, "/x/serialize/c.go"),
			msg("m4", "2026-09-12T11:00:00Z", "fable", map[string]int{"input_tokens": 40}, "/x/unmatched2/d.go"),
		}, []run{
			{args: []string{"--day", "2026-09-11", "--unattributed"}, source: []string{"files=1", "messages=2", "rows=2"}, ok: []string{"files=1", "messages=2", "rows=2", "unattributed=1"},
				out: []string{"SOURCES UNATTRIBUTED stem=/x/unmatched1/b.go tokens=1"}, not: []string{"SOURCES UNATTRIBUTED stem=/x/unmatched2"}},
			{args: []string{"--day", "2026-09-12", "--unattributed"}, source: []string{"files=1", "messages=2", "rows=2"}, ok: []string{"files=1", "messages=2", "rows=2", "unattributed=1"},
				out: []string{"SOURCES UNATTRIBUTED stem=/x/unmatched2/d.go tokens=1"}, not: []string{"SOURCES UNATTRIBUTED stem=/x/unmatched1"}},
			{args: []string{"--day", "2026-09-13", "--unattributed"}, source: []string{"files=1", "messages=0", "rows=0"}, ok: []string{"files=1", "messages=0", "rows=0", "unattributed=0"},
				not: []string{"SOURCES UNATTRIBUTED"}},
			{args: []string{"--all", "--unattributed"}, source: []string{"files=1", "messages=4", "rows=4"}, ok: []string{"files=1", "messages=4", "rows=4", "unattributed=2"},
				out: []string{"SOURCES UNATTRIBUTED stem=/x/unmatched1/b.go tokens=1", "SOURCES UNATTRIBUTED stem=/x/unmatched2/d.go tokens=1"}},
			{args: []string{"--day", "2026-09-11"}, source: []string{"messages=2", "rows=2"}, ok: []string{"unattributed=-"}},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			b.transcript("a.jsonl", c.lines...)
			for _, s := range c.runs {
				r := novaTokens.Do(t, append([]string{"sources", "--repos", b.repos, "--claude", "g=" + b.tr}, s.args...)...).Exit(0).Out(s.out...).NotOut(s.not...).NotErr(s.not...)
				linesHold(t, r.Stdout, map[string][]string{"SOURCES SOURCE label=claude:g": s.source, "SOURCES OK": s.ok}, r)
				if s.unattributed > 0 {
					assert.Equal(t, s.unattributed, strings.Count(r.Stdout, "SOURCES UNATTRIBUTED "), "%q", s.args)
				}
			}
		})
	}
}
