package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rule 11: bounded output, MEASURED at the largest plausible state.
//
// The state the spec names is a month of 20 models and 10 repos -- 200 pairs, up to 6,200
// rows over 31 files -- folded from ten declared sources, with ninety days under --all.
// What the BOUNDS depend on is the number of items of each kind, and that is what this
// test builds in full: more than the ceiling of every listing, so that every cap and every
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

// ownBytes is the output measured as the TOOL's own text: a t.TempDir() path is about a
// hundred characters deep and appears on most lines, and the spec's byte ceilings are a
// claim about what this tool writes rather than about how deeply nested the caller's
// directory happens to be. The stand-in is short and the substitution is named here so a
// reader knows exactly what was discounted.
func ownBytes(out, tmp string) int { return len(strings.ReplaceAll(out, tmp, "/x")) }

func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// countKinds is how many item lines of each kind the output holds, and how many MORE lines.
func countKinds(out string) (more int, byToken map[string]int) {
	byToken = map[string]int{}
	for _, line := range lines(out) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if f[1] == "MORE" {
			more++
			continue
		}
		byToken[f[0]+" "+f[1]]++
	}
	return more, byToken
}

// largestFoldState builds ten declared sources, every listing overflowing.
func largestFoldState(t *testing.T) (tmp, out string, args []string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so the unreadable listing cannot overflow")
	}
	dir := t.TempDir()
	out = mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	day := func(i int) string { return fmt.Sprintf("2026-09-%02d", (i%28)+1) }

	// Three transcript directories; the first holds more unreadable files than the cap.
	var claudeDirs []string
	for d := range 3 {
		td := mkdir(t, filepath.Join(dir, fmt.Sprintf("tr%d", d)))
		claudeDirs = append(claudeDirs, td)
		var body []string
		for i := range overflow {
			body = append(body, msg(fmt.Sprintf("m%d-%d", d, i), day(i)+"T10:00:00Z",
				fmt.Sprintf("model-%d", i%20), map[string]int{"input_tokens": 10 + i, "output_tokens": i},
				fmt.Sprintf("/work/repo%d/a.go", i%10)))
		}
		write(t, filepath.Join(td, "window.jsonl"), strings.Join(body, "\n")+"\n")
		if d == 0 {
			for i := range overflow {
				bad := write(t, filepath.Join(td, fmt.Sprintf("locked-%02d.jsonl", i)), "{}\n")
				if err := os.Chmod(bad, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(bad, 0o644) })
			}
		}
	}

	pool := mkdir(t, filepath.Join(dir, "pool"))
	for i := range overflow {
		swarmUsage(t, pool, fmt.Sprintf("j%02d", i), swarmRow(fmt.Sprintf("j%02d", i), "1", "-",
			fmt.Sprintf("model-%d", i%20), fmt.Sprintf("repo%d", i%10), day(i)+"T11:00:00Z", "5", "6", "7", "8", "9"))
	}

	// Two provider exports over the same days, one UTC and one zoned: every one of those
	// (day, model, repo) keys is a row of two bases.
	var utc, zoned []string
	utc = append(utc, "timestamp,model,input_tokens")
	zoned = append(zoned, "# timezone: America/Los_Angeles", "date,model,input")
	for i := range overflow {
		utc = append(utc, fmt.Sprintf("%sT12:00:00Z,mixed-%d,%d", day(i), i, 100+i))
		zoned = append(zoned, fmt.Sprintf("%s,mixed-%d,%d", day(i), i, 200+i))
	}
	g := write(t, filepath.Join(dir, "google.csv"), strings.Join(utc, "\n")+"\n")
	x := write(t, filepath.Join(dir, "xai.csv"), strings.Join(zoned, "\n")+"\n")

	// Four lanes: one of conflicts, one long chain of corrections, one of comments and
	// unparsed lines, and one quiet.
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma", "bo", "cyd", "dee")
	line := func(d string) string { return d + "\temma\tbusmodel\trepo1\tinput\t5\n" }
	for i := range overflow {
		d := day(i)
		busNote(t, bus, "emma", fmt.Sprintf("a%02d.md", i), fmt.Sprintf("emma-0000000%05d", i), "tokens "+d, busDate, line(d))
		busNote(t, bus, "emma", fmt.Sprintf("b%02d.md", i), fmt.Sprintf("emma-1000000%05d", i), "tokens "+d, busDate, line(d))
	}
	prev := ""
	for i := range overflow + 1 {
		id := fmt.Sprintf("bo-0000000%05d", i)
		subject := "tokens 2026-09-01"
		if prev != "" {
			subject = "tokens 2026-09-01 at=2026-09-11T20:00:00Z build=b supersedes=" + prev
		}
		busNote(t, bus, "bo", fmt.Sprintf("c%02d.md", i), id, subject, busDate, "2026-09-01\tbo\tbusmodel\trepo1\tinput\t"+fmt.Sprint(10+i)+"\n")
		prev = id
	}
	for i := range overflow {
		d := day(i)
		busNote(t, bus, "cyd", fmt.Sprintf("d%02d.md", i), fmt.Sprintf("cyd-0000000%05d", i), "tokens "+d, busDate,
			line(d)+"# repos: schema, serialize\nthis line is prose and is not a body line\n")
	}

	args = []string{"--out", out, "--all", "--repos", repos,
		"--claude", "one=" + claudeDirs[0], "--claude", "two=" + claudeDirs[1], "--claude", "three=" + claudeDirs[2],
		"--swarm", "pool=" + pool, "--provider", "google:emma=" + g, "--provider", "xai:johnny=" + x, "--bus", bus}
	return dir, out, args
}

func TestFoldIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	tmp, _, args := largestFoldState(t)
	r := invoke(t, append([]string{"fold"}, args...)...)
	wantExit(t, r, 1) // unreadable files, unparsed lines, mixed rows and conflicts, all at once
	all := r.stdout + r.stderr
	more, byToken := countKinds(all)

	// Ten declared sources: three transcript directories, one swarm pool, two exports and
	// four bus lanes.
	if byToken["TOKENS SOURCE"] != 10 {
		t.Errorf("%d SOURCE lines, want 10", byToken["TOKENS SOURCE"])
	}
	for _, kind := range []string{"TOKENS UNREADABLE", "TOKENS UNPARSED", "TOKENS SUPERSEDED",
		"TOKENS CONFLICT", "TOKENS TOUCHED", "TOKENS MIXED", "TOKENS DAY"} {
		if byToken[kind] != 20 {
			t.Errorf("%d %s lines, want the prefix of 20 the default ceiling allows", byToken[kind], kind)
		}
	}
	if more != 7 {
		t.Errorf("%d MORE lines, want one per overflowing kind (7)", more)
	}
	if n := byToken["TOKENS NOTE"]; n != 1 {
		t.Errorf("%d TOKENS NOTE lines, want exactly one remedy line", n)
	}
	if n := len(lines(all)); n > 160 {
		t.Errorf("%d lines at the largest plausible state, want at most 160", n)
	}
	if n := ownBytes(all, tmp); n > 28*1024 {
		t.Errorf("%d bytes at the largest plausible state, want under 28 KB", n)
	}
	// The counts are the truth about the STATE, never about the output.
	fail := lineWith(r.stderr, "TOKENS FAIL")
	for _, want := range []string{"unreadable=25", "conflict=25", "mixed=25"} {
		wantContains(t, fail, want)
	}
	t.Logf("measured: %d lines, %d bytes", len(lines(all)), ownBytes(all, tmp))

	// --max 0 prints all of it.
	r = invoke(t, append(append([]string{"fold"}, args...), "--max", "0")...)
	more, byToken = countKinds(r.stdout + r.stderr)
	if more != 0 {
		t.Errorf("--max 0 printed %d MORE lines; a caller who asked for all of it gets all of it", more)
	}
	if byToken["TOKENS UNREADABLE"] != overflow {
		t.Errorf("--max 0 printed %d of %d unreadable lines", byToken["TOKENS UNREADABLE"], overflow)
	}
	// --max -1 is a typo with two readings, and is refused.
	r = invoke(t, append(append([]string{"fold"}, args...), "--max", "-1")...)
	wantExit(t, r, 2)
}

func TestSourcesIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	tmp, _, args := largestFoldState(t)
	var sourceArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--out" {
			i++
			continue
		}
		sourceArgs = append(sourceArgs, args[i])
	}
	r := invoke(t, append([]string{"sources"}, sourceArgs...)...)
	wantExit(t, r, 0)
	all := r.stdout + r.stderr
	more, byToken := countKinds(all)
	if byToken["SOURCES SOURCE"] != 10 || byToken["SOURCES UNREADABLE"] != 20 || byToken["SOURCES UNPARSED"] != 20 {
		t.Errorf("sources printed %v", byToken)
	}
	if more != 2 {
		t.Errorf("%d MORE lines, want 2", more)
	}
	if n := len(lines(all)); n > 53 {
		t.Errorf("%d lines, want at most 53", n)
	}
	if n := ownBytes(all, tmp); n > 8*1024 {
		t.Errorf("%d bytes, want under 8 KB", n)
	}
	t.Logf("measured: %d lines, %d bytes", len(lines(all)), ownBytes(all, tmp))
}

func TestCheckIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	// Twenty-five files with a row-level finding, twenty-five strays, and a gap of
	// twenty-five days between the first day and the last.
	for i := range overflow {
		d := fmt.Sprintf("2026-09-%02d", i+1)
		write(t, filepath.Join(out, d+".tsv"),
			"nova-tokens v1 day="+d+" at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+
				d+"\ta\tschema\t1\t2\t3\t4\t5\t0\tutc\n")
		write(t, filepath.Join(out, fmt.Sprintf("notes-%02d.txt", i)), "a person's file\n")
	}
	write(t, filepath.Join(out, "2026-11-30.tsv"),
		"nova-tokens v1 day=2026-11-30 at=2026-09-11T23:55:02Z build=b turns=1 sources=x\n"+hdr+
			"2026-11-30\ta\tschema\t1\t2\t3\t4\t5\t0\tutc\tx\n")
	r := invoke(t, "check", "--out", out)
	wantExit(t, r, 1)
	all := r.stdout + r.stderr
	more, byToken := countKinds(all)
	if byToken["CHECK FAIL"] != 21 { // twenty item lines and the count line
		t.Errorf("%d CHECK FAIL lines, want 20 findings and one count line", byToken["CHECK FAIL"])
	}
	if byToken["CHECK MISSING"] != 20 || byToken["CHECK STRAY"] != 20 {
		t.Errorf("check printed %v", byToken)
	}
	if more != 3 {
		t.Errorf("%d MORE lines, want 3", more)
	}
	if n := len(lines(all)); n > 64 {
		t.Errorf("%d lines, want at most 64", n)
	}
	if n := ownBytes(all, dir); n > 8*1024 {
		t.Errorf("%d bytes, want under 8 KB", n)
	}
	count := lineWith(r.stderr, "CHECK FAIL files=")
	wantContains(t, count, "bad=25")
	wantContains(t, count, "stray=25")
	t.Logf("measured: %d lines, %d bytes", len(lines(all)), ownBytes(all, dir))
}

func TestSumIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	// Thirty-one day files, twenty-one models on ten repos: 210 pairs. The spec's own
	// example is twenty models, which does not overflow the model listing at all -- and
	// its table still shows a MORE line under it. One more model is what makes both caps
	// and both MORE lines real, and the pair count moves with it.
	for d := 1; d <= 31; d++ {
		date := fmt.Sprintf("2026-09-%02d", d)
		var rows []string
		for m := range 21 {
			for repo := range 10 {
				rows = append(rows, fmt.Sprintf("%s\tmodel-%02d\trepo-%02d\t%d\t%d\t-\t-\t-\t0\tutc\tx",
					date, m, repo, 100+m, 10+repo))
			}
		}
		write(t, filepath.Join(out, date+".tsv"),
			"nova-tokens v1 day="+date+" at=2026-09-11T23:55:02Z build=b turns=100 sources=x\n"+hdr+
				strings.Join(rows, "\n")+"\n")
	}
	r := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, r, 0)
	all := r.stdout + r.stderr
	more, byToken := countKinds(all)
	if byToken["SUM PAIR"] != 20 || byToken["SUM MODEL"] != 20 {
		t.Errorf("sum printed %v", byToken)
	}
	if more != 2 {
		t.Errorf("%d MORE lines, want 2", more)
	}
	if n := len(lines(all)); n > 45 {
		t.Errorf("%d lines, want at most 45", n)
	}
	if n := ownBytes(all, dir); n > 10*1024 {
		t.Errorf("%d bytes, want under 10 KB", n)
	}
	wantContains(t, lineWith(r.stdout, "SUM OK"), "pairs=210")
	wantContains(t, lineWith(r.stdout, "SUM MONTH"), "turns=3100")
	t.Logf("measured: %d lines, %d bytes", len(lines(all)), ownBytes(all, dir))
}

// report is UNCAPPED on purpose: the body is the artifact, and a capped report would be a
// count sent as a total.
func TestReportIsUncappedBecauseTheBodyIsTheArtifact(t *testing.T) {
	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	var body []string
	for m := range 20 {
		for repo := range 10 {
			body = append(body, msg(fmt.Sprintf("m%d-%d", m, repo), "2026-09-11T10:00:00Z",
				fmt.Sprintf("model-%02d", m), map[string]int{"input_tokens": 1, "output_tokens": 2, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 4},
				fmt.Sprintf("/work/repo-%02d/a.go", repo)))
		}
	}
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join(body, "\n")+"\n")
	var rules []string
	for repo := range 10 {
		rules = append(rules, fmt.Sprintf("repo-%02d\t(^|/)repo-%02d($|/)", repo, repo))
	}
	repos := write(t, filepath.Join(dir, "ten-repos.tsv"), strings.Join(rules, "\n")+"\n")
	r := invoke(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr)
	wantExit(t, r, 0)
	// Two hundred pairs, four types each: a Claude transcript carries no reasoning count.
	if n := len(lines(r.stdout)); n != 800 {
		t.Errorf("%d report lines, want 800 (200 pairs x the four types a transcript carries)", n)
	}
	wantNotContains(t, r.stdout, "MORE")
	if n := len(r.stdout); n > 64*1024 {
		t.Errorf("%d bytes, want under 64 KB", n)
	}
	if n := len(lines(r.stderr)); n != 1 {
		t.Errorf("%d lines on stderr, want the one REPORT OK", n)
	}
	t.Logf("measured: %d lines, %d bytes", len(lines(r.stdout)), len(r.stdout))
}
