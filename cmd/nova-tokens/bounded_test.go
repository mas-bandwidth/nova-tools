package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
)

// Rule 11: bounded output, MEASURED at the largest plausible state.
//
// The state the spec names is a month of 20 models and 10 repos -- 200 pairs, up to 6,200
// rows over 31 files -- folded from ten declared sources, with ninety days under --all.
// What the BOUNDS depend on is the number of items of each kind, and that is what these
// tests build in full: more than the ceiling of every listing, so that every cap and every
// MORE line is exercised at once.
//
// The one place it is deliberately smaller than the spec's sentence is the MESSAGE volume:
// the spec names 3,000 transcript files and 50,000 messages a day, which over ninety days
// is four and a half million messages to build and fold inside one test. The two-minute
// rule wins there, because the bound this table is about does not move with it: a message
// is a number added into a row, and a row's LINE is capped by --max whether it was fed by
// one message or fifty thousand. The file-count half of that claim is pinned separately,
// by TestEachDeclaredFileIsOpenedOncePerRun.

// overflow is one more than twice the default ceiling, so every listing has a prefix of 20
// and a MORE line standing for the rest.
const overflow = 25

const dayHeader = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"

func lines(s string) []string {
	if s = strings.TrimSuffix(s, "\n"); s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// largestFoldState builds ten declared sources, every listing overflowing, under dir, and
// returns their flags (everything but --out).
func largestFoldState(t *testing.T, dir string) []string {
	t.Helper()
	day := func(i int) string { return fmt.Sprintf("2026-09-%02d", (i%28)+1) }
	files := map[string]string{}
	// Three transcript directories; the first holds more unreadable files than the cap.
	for d := range 3 {
		var body []string
		for i := range overflow {
			body = append(body, msg(fmt.Sprintf("m%d-%d", d, i), day(i)+"T10:00:00Z",
				fmt.Sprintf("model-%d", i%20), map[string]int{"input_tokens": 10 + i, "output_tokens": i},
				fmt.Sprintf("/work/repo%d/a.go", i%10)))
		}
		files[fmt.Sprintf("tr%d/window.jsonl", d)] = strings.Join(body, "\n") + "\n"
	}
	// Two provider exports over the same days, one UTC and one zoned: every one of those
	// (day, model, repo) keys is a row of two bases.
	utc, zoned := []string{"timestamp,model,input_tokens"}, []string{"# timezone: America/Los_Angeles", "date,model,input"}
	for i := range overflow {
		utc = append(utc, fmt.Sprintf("%sT12:00:00Z,mixed-%d,%d", day(i), i, 100+i))
		zoned = append(zoned, fmt.Sprintf("%s,mixed-%d,%d", day(i), i, 200+i))
	}
	files["google.csv"], files["xai.csv"] = strings.Join(utc, "\n")+"\n", strings.Join(zoned, "\n")+"\n"
	testkit.Tree(t, dir, files)
	for i := range overflow {
		makeUnreadable(t, testkit.WriteFile(t, filepath.Join(dir, "tr0", fmt.Sprintf("locked-%02d.jsonl", i)), "{}\n"))
	}
	pool := filepath.Join(dir, "pool")
	for i := range overflow {
		job := fmt.Sprintf("j%02d", i)
		swarmUsage(t, pool, job, swarmRow(job, "1", "-", fmt.Sprintf("model-%d", i%20), fmt.Sprintf("repo%d", i%10), day(i)+"T11:00:00Z", "5", "6", "7", "8", "9"))
	}
	// Four lanes: one of conflicts, one long chain of corrections, one of comments and
	// unparsed lines, and one quiet.
	bus := busDir(t, filepath.Join(dir, "bus"), "emma", "bo", "cyd", "dee")
	line := func(d string) string { return d + "\temma\tbusmodel\trepo1\tinput\t5\n" }
	prev := ""
	for i := range overflow {
		d := day(i)
		busNote(t, bus, "emma", fmt.Sprintf("a%02d.md", i), fmt.Sprintf("emma-0000000%05d", i), "tokens "+d, busDate, line(d))
		busNote(t, bus, "emma", fmt.Sprintf("b%02d.md", i), fmt.Sprintf("emma-1000000%05d", i), "tokens "+d, busDate, line(d))
		busNote(t, bus, "cyd", fmt.Sprintf("d%02d.md", i), fmt.Sprintf("cyd-0000000%05d", i), "tokens "+d, busDate,
			line(d)+"# repos: schema, serialize\nthis line is prose and is not a body line\n")
	}
	for i := range overflow + 1 {
		id, subject := fmt.Sprintf("bo-0000000%05d", i), "tokens 2026-09-01"
		if prev != "" {
			subject += " at=2026-09-11T20:00:00Z build=b supersedes=" + prev
		}
		busNote(t, bus, "bo", fmt.Sprintf("c%02d.md", i), id, subject, busDate, "2026-09-01\tbo\tbusmodel\trepo1\tinput\t"+fmt.Sprint(10+i)+"\n")
		prev = id
	}
	return []string{"--all", "--repos", reposFile(t, dir),
		"--claude", "one=" + filepath.Join(dir, "tr0"), "--claude", "two=" + filepath.Join(dir, "tr1"), "--claude", "three=" + filepath.Join(dir, "tr2"),
		"--swarm", "pool=" + pool, "--provider", "google:emma=" + filepath.Join(dir, "google.csv"), "--provider", "xai:johnny=" + filepath.Join(dir, "xai.csv"), "--bus", bus}
}

// overflowingDays writes, under out, 25 day files with a row-level finding, 25 strays, and
// a gap of 25 days between the first day and the last.
func overflowingDays(t *testing.T, out string) string {
	t.Helper()
	files := map[string]string{"2026-11-30.tsv": "nova-tokens v1 day=2026-11-30 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n" + dayHeader +
		"2026-11-30\ta\tschema\t1\t2\t3\t4\t5\t0\tutc\tx\n"}
	for i := range overflow {
		d := fmt.Sprintf("2026-09-%02d", i+1)
		files[d+".tsv"] = "nova-tokens v1 day=" + d + " at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n" + dayHeader + d + "\ta\tschema\t1\t2\t3\t4\t5\t0\tutc\n"
		files[fmt.Sprintf("notes-%02d.txt", i)] = "a person's file\n"
	}
	return testkit.Tree(t, out, files)
}

// aFullMonth writes, under out, thirty day files of twenty-one models on ten repos: 210
// pairs. The spec's own example is twenty models, which does not overflow the model
// listing at all -- and its table still shows a MORE line under it. One more model is what
// makes both caps and both MORE lines real, and the pair count moves with it. September has
// THIRTY days: the fixture once wrote a 2026-09-31.tsv and the month read it as a day,
// because a day used to be a shape and not a date on the calendar.
func aFullMonth(t *testing.T, out string) string {
	t.Helper()
	files := map[string]string{}
	for d := 1; d <= 30; d++ {
		date := fmt.Sprintf("2026-09-%02d", d)
		rows := []string{"nova-tokens v1 day=" + date + " at=2026-09-11T23:55:02Z build=b turns=100 sources=x\n" + strings.TrimSuffix(dayHeader, "\n")}
		for m := range 21 {
			for repo := range 10 {
				rows = append(rows, fmt.Sprintf("%s\tmodel-%02d\trepo-%02d\t%d\t%d\t-\t-\t-\t0\tutc\tx", date, m, repo, 100+m, 10+repo))
			}
		}
		files[date+".tsv"] = strings.Join(rows, "\n") + "\n"
	}
	return testkit.Tree(t, out, files)
}

// Each verb that lists, at the largest plausible state, one row each: the item lines of each
// kind (the default ceiling's prefix of 20), the MORE lines (one per overflowing kind), the
// lines and the bytes of all its output, and the count lines, which are the truth about the
// STATE, never about the output. The bytes are measured as the TOOL's own text: a
// t.TempDir() path is about a hundred characters deep and appears on most lines, and the
// spec's byte ceilings are a claim about what this tool writes, so the bench's root is
// counted as the two bytes "/x".
func TestEveryListingIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name               string
		args               func(t *testing.T, dir, out string) []string
		exit               int
		kinds              map[string]int
		more               int
		maxLines, maxBytes int // 0: uncapped
		out, err           map[string][]string
	}{
		// Unreadable files, unparsed lines, mixed rows and conflicts, all at once; ten
		// declared sources: three transcript directories, one swarm pool, two exports and
		// four bus lanes; exactly one remedy line.
		{name: "fold", exit: 1, more: 7, maxLines: 160, maxBytes: 28 * 1024,
			args: func(t *testing.T, dir, out string) []string {
				return append([]string{"fold", "--out", out}, largestFoldState(t, dir)...)
			},
			kinds: map[string]int{"TOKENS SOURCE": 10, "TOKENS UNREADABLE": 20, "TOKENS UNPARSED": 20, "TOKENS SUPERSEDED": 20,
				"TOKENS CONFLICT": 20, "TOKENS TOUCHED": 20, "TOKENS MIXED": 20, "TOKENS DAY": 20, "TOKENS NOTE": 1},
			err: map[string][]string{"TOKENS FAIL": {"unreadable=25", "conflict=25", "mixed=25"}}},
		// A caller who asked for all of it gets all of it.
		{name: "fold --max 0 prints all", exit: 1, more: 0,
			args: func(t *testing.T, dir, out string) []string {
				return append([]string{"fold", "--out", out, "--max", "0"}, largestFoldState(t, dir)...)
			},
			kinds: map[string]int{"TOKENS UNREADABLE": overflow}},
		{name: "sources", more: 2, maxLines: 53, maxBytes: 8 * 1024,
			args: func(t *testing.T, dir, _ string) []string {
				return append([]string{"sources"}, largestFoldState(t, dir)...)
			},
			kinds: map[string]int{"SOURCES SOURCE": 10, "SOURCES UNREADABLE": 20, "SOURCES UNPARSED": 20}},
		// --strict is the reading that names every calendar gap and every non-day entry, and
		// it is the one this measurement is about: the longest listing check can print.
		// Twenty item lines and the count line are CHECK FAIL.
		{name: "check --strict", exit: 1, more: 3, maxLines: 64, maxBytes: 8 * 1024,
			args: func(t *testing.T, _, out string) []string {
				return []string{"check", "--out", overflowingDays(t, out), "--strict"}
			},
			kinds: map[string]int{"CHECK FAIL": 21, "CHECK MISSING": 20, "CHECK STRAY": 20},
			err:   map[string][]string{"CHECK FAIL files=": {"bad=25", "stray=25"}}},
		{name: "sum", more: 2, maxLines: 45, maxBytes: 10 * 1024,
			args: func(t *testing.T, _, out string) []string {
				return []string{"sum", "--out", aFullMonth(t, out), "--month", "2026-09"}
			},
			kinds: map[string]int{"SUM PAIR": 20, "SUM MODEL": 20},
			out:   map[string][]string{"SUM OK": {"pairs=210"}, "SUM MONTH": {"turns=3000"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			r := novaTokens.Do(t, c.args(t, b.dir, b.out)...).Exit(c.exit)
			linesHold(t, r.Stdout, c.out, r)
			linesHold(t, r.Stderr, c.err, r)
			all := r.Stdout + r.Stderr
			more, kinds := 0, map[string]int{}
			for _, line := range lines(all) {
				switch f := strings.Fields(line); {
				case len(f) < 2:
				case f[1] == "MORE":
					more++
				default:
					kinds[f[0]+" "+f[1]]++
				}
			}
			for kind, n := range c.kinds {
				assert.Equal(t, n, kinds[kind], "%s lines: %v", kind, kinds)
			}
			assert.Equal(t, c.more, more, "MORE lines, one per overflowing kind")
			n, size := len(lines(all)), len(strings.ReplaceAll(all, b.dir, "/x"))
			if c.maxLines > 0 {
				assert.LessOrEqual(t, n, c.maxLines, "lines at the largest plausible state")
				assert.LessOrEqual(t, size, c.maxBytes, "bytes at the largest plausible state")
			}
			t.Logf("measured: %d lines, %d bytes", n, size)
		})
	}
}

// report is UNCAPPED on purpose: the body is the artifact, and a capped report would be a
// count sent as a total. Two hundred pairs, four types each: a Claude transcript carries
// no reasoning count; stderr is 20 TOKENS AVG, the one TOKENS AVG-ALL and the one REPORT OK.
func TestReportIsUncappedBecauseTheBodyIsTheArtifact(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	var body, rules []string
	for m := range 20 {
		for repo := range 10 {
			body = append(body, msg(fmt.Sprintf("m%d-%d", m, repo), "2026-09-11T10:00:00Z", fmt.Sprintf("model-%02d", m),
				map[string]int{"input_tokens": 1, "output_tokens": 2, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 4},
				fmt.Sprintf("/work/repo-%02d/a.go", repo)))
		}
	}
	for repo := range 10 {
		rules = append(rules, fmt.Sprintf("repo-%02d\t(^|/)repo-%02d($|/)", repo, repo))
	}
	b.transcript("a.jsonl", body...)
	repos := testkit.WriteFile(t, filepath.Join(b.dir, "ten-repos.tsv"), strings.Join(rules, "\n")+"\n")
	r := novaTokens.Do(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+b.tr).Exit(0).NotOut("MORE")
	assert.Len(t, lines(r.Stdout), 800, "report lines: 200 pairs x the four types a transcript carries")
	assert.LessOrEqual(t, len(r.Stdout), 64*1024, "report bytes")
	assert.Len(t, lines(r.Stderr), 22, "stderr lines")
	t.Logf("measured: %d lines, %d bytes", len(lines(r.Stdout)), len(r.Stdout))
}
