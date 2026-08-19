package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
)

// corpus is the fixture corpus that ships with the tool: a small invented
// lighthouse station, self-contained under testdata, referenced by nothing
// outside it. Every functional test runs against it, and the induced-failure
// tests plant their faults in copies under t.TempDir().
const corpus = "testdata/corpus"

const exampleGold = "testdata/example-gold.tsv"

func runCLI(t *testing.T, stdin string, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, strings.NewReader(stdin), &out, &errb)
	return exit, out.String(), errb.String()
}

// ---------------------------------------------------------------------------
// The no-guessing law: every missing flag is a refusal that names the flag.

func TestRefusesToGuess(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"no subcommand", nil, "usage"},
		{"unknown subcommand", []string{"frobnicate"}, "unknown subcommand"},

		{"stats without root", []string{"stats"}, "--root is required"},
		{"stats stray argument", []string{"stats", "--root", corpus, "extra"}, "unexpected argument"},

		{"search without root", []string{"search", "--channels", "bm25", "--k", "3", "x"}, "--root is required"},
		{"search without channels", []string{"search", "--root", corpus, "--k", "3", "x"}, "--channels is required"},
		{"search without k", []string{"search", "--root", corpus, "--channels", "bm25", "x"}, "--k is required"},
		{"search with zero k", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "0", "x"}, "--k must be a positive"},
		{"search with negative k", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "-2", "x"}, "--k must be a positive"},
		{"search without a query", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "3"}, "no query words"},
		{"search with unknown channel", []string{"search", "--root", corpus, "--channels", "semantic", "--k", "3", "x"}, `unknown channel "semantic"`},
		{"search with empty channels", []string{"search", "--root", corpus, "--channels", "", "--k", "3", "x"}, "named no channels"},
		{"search with a stray comma in channels", []string{"search", "--root", corpus, "--channels", "bm25,", "--k", "3", "x"}, "empty entry"},

		{"check without root", []string{"check", "--channels", "bm25", "--k", "3", "-"}, "--root is required"},
		{"check without channels", []string{"check", "--root", corpus, "--k", "3", "-"}, "--channels is required"},
		{"check without k", []string{"check", "--root", corpus, "--channels", "bm25", "-"}, "--k is required"},
		{"check without a named input", []string{"check", "--root", corpus, "--channels", "bm25", "--k", "3"}, "exactly one candidate file"},
		{"check with two inputs", []string{"check", "--root", corpus, "--channels", "bm25", "--k", "3", "a", "b"}, "exactly one candidate file"},

		{"verify without root", []string{"verify", "--links", "info", "--coverage", "a:b"}, "--root is required"},
		{"verify without links", []string{"verify", "--root", corpus, "--coverage", "a:b"}, "--links is required"},
		{"verify with a bad links value", []string{"verify", "--root", corpus, "--links", "maybe"}, "--links must be gate or info"},
		{"verify with no gating check", []string{"verify", "--root", corpus, "--links", "info"}, "a run that cannot fail is not a verification"},
		{"verify exempt without frontmatter", []string{"verify", "--root", corpus, "--links", "gate", "--exempt", "index-"}, "--exempt only applies"},
		{"verify with a malformed coverage pair", []string{"verify", "--root", corpus, "--links", "info", "--coverage", "notes"}, "--coverage wants A:B"},
		{"verify stray argument", []string{"verify", "--root", corpus, "--links", "gate", "extra"}, "unexpected argument"},

		{"eval without root", []string{"eval", "--channels", "bm25", "--k", "3", "--floor", "0.8", exampleGold}, "--root is required"},
		{"eval without channels", []string{"eval", "--root", corpus, "--k", "3", "--floor", "0.8", exampleGold}, "--channels is required"},
		{"eval without k", []string{"eval", "--root", corpus, "--channels", "bm25", "--floor", "0.8", exampleGold}, "--k is required"},
		{"eval without floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", exampleGold}, "--floor is required"},
		{"eval with a zero floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0", exampleGold}, "a harness that cannot fail is not a measurement"},
		{"eval with a negative floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "-1", exampleGold}, "--floor must be in (0,1]"},
		{"eval with a floor above one", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "1.5", exampleGold}, "--floor must be in (0,1]"},
		{"eval without a gold file", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8"}, "exactly one gold file"},
		{"eval with a missing gold file", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", "testdata/no-such-gold.tsv"}, "no-such-gold.tsv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tt.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

// Two missing flags must be reported in the same order every run: the check
// is sorted, never map order.
func TestRequiredFlagErrorOrderDeterministic(t *testing.T) {
	for i := 0; i < 20; i++ {
		exit, _, stderr := runCLI(t, "", "eval")
		if exit != 2 {
			t.Fatalf("exit = %d, want 2", exit)
		}
		want := []string{"--channels is required", "--floor is required", "--k is required", "--root is required"}
		at := -1
		for _, w := range want {
			idx := strings.Index(stderr, w)
			if idx < 0 {
				t.Fatalf("stderr must name every missing flag, got %q", stderr)
			}
			if idx < at {
				t.Fatalf("flag errors out of sorted order (run %d): %q", i, stderr)
			}
			at = idx
		}
	}
}

// The corpus root is never inherited from the environment. The tool this was
// ported from resolved --root, then $NOVA_MEMORY_ROOT, then the enclosing git
// worktree; under this repo's law a corpus you did not name is a corpus you
// did not mean, and answering "you already know this" about someone else's
// memory is the worst possible way to be wrong.
func TestRootIsNeverTakenFromTheEnvironment(t *testing.T) {
	t.Setenv("NOVA_MEMORY_ROOT", corpus)
	exit, stdout, stderr := runCLI(t, "", "stats")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2 — an environment variable must not supply the root; stdout: %s", exit, stdout)
	}
	if !strings.Contains(stderr, "--root is required") {
		t.Errorf("stderr = %q, want the refusal to name --root", stderr)
	}
}

func TestRefusesAnUnusableRoot(t *testing.T) {
	empty := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-dir.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, root, want string }{
		{"missing directory", filepath.Join(empty, "nope"), "not a readable directory"},
		{"a file, not a directory", file, "not a readable directory"},
		{"a directory holding no markdown", empty, "no markdown files found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := runCLI(t, "", "stats", "--root", tc.root)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The verbs, on the fixture corpus

func TestStats(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "stats", "--root", corpus)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	for _, want := range []string{
		"STATS OK schema=nova-memory/1 files=6",
		"STATS OK class=. chunks=",
		"STATS OK class=log chunks=",
		"STATS OK class=notes chunks=",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestStatsHonoursExclude(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "stats", "--root", corpus, "--exclude", "log")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	if strings.Contains(stdout, "class=log") {
		t.Errorf("an excluded class was indexed anyway: %q", stdout)
	}
	if !strings.Contains(stdout, "class=notes") {
		t.Errorf("--exclude removed more than it was given: %q", stdout)
	}
}

func TestSearch(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3",
		"when", "can", "the", "relief", "boat", "land", "at", "the", "jetty")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	for _, want := range []string{
		"SEARCH OK query=", "hits=3", "channels=bm25",
		"SEARCH CAL score=", "probe=unrelated-control",
		"SEARCH HIT rank=1", "class=notes", "name=tide-tables", "type=reference", "notes/tides.md:",
		"SEARCH NOTE lexical only",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

// Every receipt carries its class: "already written down in a dated log" and
// "already distilled into a note" are different answers to "do I know this?",
// and a receipt that hid the difference would hand the mind the wrong verdict.
func TestSearchReceiptsCarryClassAndFrontmatter(t *testing.T) {
	_, stdout, _ := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "6",
		"washing", "the", "glazing", "before", "an", "onshore", "gale")
	if !strings.Contains(stdout, "class=log") {
		t.Errorf("no log-class receipt in %q", stdout)
	}
	if !strings.Contains(stdout, "class=notes name=lantern-care type=measured") {
		t.Errorf("no classed, frontmattered note receipt in %q", stdout)
	}
	if !strings.Contains(stdout, "name=- type=-") {
		t.Errorf("a file without frontmatter must still print stable fields, got %q", stdout)
	}
}

// The calibration probe is part of what the schema version names — the spec
// says changing it IS a schema change. Nothing enforced that: the probe lives
// in this package, SchemaVersion lives in internal/memindex, and each could
// move without the other. This is the third copy that makes the parity a
// tripwire instead of a remembered rule, the same pattern the floors registry
// uses. Change one of these and this test goes red until you change both.
func TestCalibrationProbeAndSchemaVersionMoveTogether(t *testing.T) {
	const wantProbe = "the quarterly marketing budget for the regional office needs revised headcount projections before the fiscal deadline"
	const wantSchema = "nova-memory/1"
	if calibrationProbe != wantProbe {
		t.Errorf("the calibration probe changed:\n got: %q\nwant: %q\n"+
			"The probe defines the negative-control band, so every band printed under the old probe is incomparable "+
			"with every band printed under the new one. If the change is intended, bump memindex.SchemaVersion in the "+
			"same commit and update BOTH constants here.", calibrationProbe, wantProbe)
	}
	if memindex.SchemaVersion != wantSchema {
		t.Errorf("the schema version changed:\n got: %q\nwant: %q\n"+
			"Update this test and confirm the calibration probe above is still the one the version names.",
			memindex.SchemaVersion, wantSchema)
	}
}

// The receipt names the channel its score came from, and the channel named is
// the one that ACTUALLY surfaced the chunk. A hit reached through the second
// channel alone used to print score=0.00, which reads against the calibration
// band as "weaker than unrelated control text" — a fabricated number, not a
// measurement.
func TestReceiptsNameTheChannelTheScoreCameFrom(t *testing.T) {
	// "diaphones" is out of vocabulary (the corpus says "diaphone"), so bm25
	// reaches nothing and every hit arrives through trigram.
	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25,trigram", "--k", "3", "diaphones")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	var hits int
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "SEARCH HIT ") {
			continue
		}
		hits++
		if !strings.Contains(line, "score-channel=trigram") {
			t.Errorf("a trigram-only hit did not name trigram as its score channel: %q", line)
		}
		if strings.Contains(line, "score=0.00 ") {
			t.Errorf("a fabricated zero score survived: %q", line)
		}
	}
	if hits == 0 {
		t.Fatalf("the trigram channel surfaced nothing, so the receipt was never exercised: %q", stdout)
	}
	// The probe is ordinary English and reaches bm25, so the CAL band on the
	// same run is attributed to the other channel — which is the whole point:
	// score= is only meaningful beside the channel that produced it.
	if !strings.Contains(stdout, "SEARCH CAL score=") || !strings.Contains(stdout, "score-channel=bm25 probe=unrelated-control") {
		t.Errorf("the calibration line does not name its own channel: %q", stdout)
	}
}

func TestSearchOutOfVocabularyQuerySaysSoInWords(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3", "zzqq", "xxvv")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "hits=0") || !strings.Contains(stdout, "SEARCH MISS every query term is out of vocabulary") {
		t.Errorf("a zero must be explained, never bare: %q", stdout)
	}
}

func TestCheckFromStdin(t *testing.T) {
	in := "The compressor belt hardens with age and the blast runs a half second short.\n\n" +
		"The relief boat should not try the jetty steps near low water on a spring tide.\n"
	exit, stdout, stderr := runCLI(t, in, "check", "--root", corpus, "--channels", "bm25", "--k", "2", "-")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	for _, want := range []string{
		"MEMORY OK candidates=2 source=- k=2 channels=bm25",
		"MEMORY CAL score=",
		"MEMORY CAND n=1", "MEMORY CAND n=2",
		"MEMORY HIT cand=1 rank=1", "notes/fog-signal.md:",
		"MEMORY HIT cand=2 rank=1", "notes/tides.md:",
		"MEMORY NOTE lexical only",
		"MEMORY NOTE this verb asserts nothing and never exits 1",
		// Class-relative, never layout-presuming: the corpus classifies itself,
		// so the NOTE must not talk as if every adopter's tree has one
		// canonical memory directory.
		"MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// k is the mind's budget: exactly k receipts per candidate, never more.
	if got := strings.Count(stdout, "MEMORY HIT cand=1 "); got != 2 {
		t.Errorf("candidate 1 got %d receipts, want exactly k=2", got)
	}
}

func TestCheckFromANamedFile(t *testing.T) {
	dir := t.TempDir()
	cand := filepath.Join(dir, "candidate.md")
	if err := os.WriteFile(cand, []byte("Salt haze on the glazing has to be washed off in daylight before it etches the glass.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runCLI(t, "", "check", "--root", corpus, "--channels", "bm25", "--k", "3", cand)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "source="+cand) {
		t.Errorf("stdout = %q, want it to name the source file", stdout)
	}
	if !strings.Contains(stdout, "notes/lantern.md:") {
		t.Errorf("the candidate's own subject did not surface: %q", stdout)
	}
}

// A CRLF candidate file must split into the same candidates as its LF twin.
// The split is on the literal "\n\n", so before line endings were normalized
// a CRLF input arrived as ONE candidate — the whole file queried as a single
// blob against a corpus indexed paragraph by paragraph, silently, with a
// green MEMORY OK line.
func TestCheckCandidateSplittingIsLineEndingAgnostic(t *testing.T) {
	lfBody := "The compressor belt hardens with age and the blast runs a half second short.\n\n" +
		"The relief boat should not try the jetty steps near low water on a spring tide.\n\n" +
		"Salt haze on the glazing has to be washed off in daylight before it etches the glass.\n"
	crlfBody := strings.ReplaceAll(lfBody, "\n", "\r\n")

	run := func(name, body string) string {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		exit, stdout, stderr := runCLI(t, "", "check", "--root", corpus, "--channels", "bm25", "--k", "2", p)
		if exit != 0 {
			t.Fatalf("%s: exit = %d, want 0; stderr: %s", name, exit, stderr)
		}
		return stdout
	}
	lf, crlf := run("lf.md", lfBody), run("crlf.md", crlfBody)
	for _, want := range []string{"MEMORY OK candidates=3", "MEMORY CAND n=3"} {
		if !strings.Contains(crlf, want) {
			t.Errorf("CRLF input: stdout = %q, want it to contain %q — a CRLF file arrived as one giant candidate", crlf, want)
		}
	}
	strip := func(s string) string {
		var keep []string
		for _, line := range strings.Split(s, "\n") {
			if !strings.HasPrefix(line, "MEMORY OK ") { // holds the source path, which differs by design
				keep = append(keep, line)
			}
		}
		return strings.Join(keep, "\n")
	}
	if strip(lf) != strip(crlf) {
		t.Errorf("the twins produced different receipts:\n--- lf ---\n%s--- crlf ---\n%s", lf, crlf)
	}
}

func TestCheckRefusesUnusableInput(t *testing.T) {
	dir := t.TempDir()
	thin := filepath.Join(dir, "thin.md")
	if err := os.WriteFile(thin, []byte("# h\n\nok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, stdin, arg, want string }{
		{"a file with no candidate paragraph", "", thin, "no candidate paragraph"},
		{"empty stdin", "", "-", "no candidate paragraph"},
		{"a file that does not exist", "", filepath.Join(dir, "nope.md"), "nope.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, tc.stdin, "check", "--root", corpus, "--channels", "bm25", "--k", "3", tc.arg)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

// Two runs over the same corpus and the same input must produce identical
// bytes. Go randomizes map iteration, and stats' build= duration is the one
// field deliberately excluded from this promise (it is a measurement, and it
// is labelled as one).
func TestRetrievalOutputIsByteIdentical(t *testing.T) {
	in := "The relief boat should not try the jetty steps near low water on a spring tide.\n"
	for _, args := range [][]string{
		{"check", "--root", corpus, "--channels", "bm25,trigram", "--k", "5", "-"},
		{"search", "--root", corpus, "--channels", "bm25,trigram", "--k", "5", "salt", "haze", "glazing"},
		{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.5", exampleGold},
	} {
		_, a, _ := runCLI(t, in, args...)
		_, b, _ := runCLI(t, in, args...)
		if a != b {
			t.Fatalf("%v produced different bytes on two runs:\n--- a ---\n%s--- b ---\n%s", args[0], a, b)
		}
	}
}

// ---------------------------------------------------------------------------
// verify — and the ruling that unresolved wikilinks have no default

func TestVerifyPassesOnTheFixtureCorpus(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "verify", "--root", corpus, "--links", "info",
		"--coverage", "notes/*.md:notes/index-*.md", "--frontmatter", "notes/*.md", "--exempt", "index-")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "VERIFY OK gating=0 info=1") {
		t.Errorf("stdout = %q, want the clean OK line", stdout)
	}
	if !strings.Contains(stdout, "VERIFY INFO wikilink [[storm-glass]]") {
		t.Errorf("the informational wikilink finding is missing: %q", stdout)
	}
}

// The exit contract has no default: the same corpus, the same findings, and
// two different exits, chosen by the caller and by nobody else. The tool this
// was ported from defaulted this to informational while its own spec promised
// a nonzero exit on findings, and a script trusting the spec passed dangling
// links silently.
func TestVerifyLinksRulingIsTheCallersBothWays(t *testing.T) {
	base := []string{"verify", "--root", corpus, "--coverage", "notes/*.md:notes/index-*.md"}
	exit, stdout, _ := runCLI(t, "", append(append([]string{}, base...), "--links", "info")...)
	if exit != 0 {
		t.Fatalf("--links info: exit = %d, want 0", exit)
	}
	if !strings.Contains(stdout, "VERIFY INFO wikilink") {
		t.Errorf("--links info must still report the finding, got %q", stdout)
	}

	exit, stdout, stderr := runCLI(t, "", append(append([]string{}, base...), "--links", "gate")...)
	if exit != 1 {
		t.Fatalf("--links gate: exit = %d, want 1; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "VERIFY FAIL wikilink [[storm-glass]]") {
		t.Errorf("stderr = %q, want the gating wikilink failure", stderr)
	}
	if strings.Contains(stdout, "VERIFY OK") {
		t.Errorf("a failing verify must not print an OK line, got %q", stdout)
	}
}

// Planted faults, one per gating check, each observed failing. A check never
// seen failing is not a check.
func TestVerifySaysNoOnPlantedFaults(t *testing.T) {
	cases := []struct {
		name  string
		plant map[string]string
		links string // "" means info: the wikilink findings must not do the gating
		args  []string
		want  string
	}{
		{
			name:  "an orphan note that no index names",
			plant: map[string]string{"notes/orphan.md": "---\nname: orphan\n---\n\nan undistilled lesson that no index line names at all\n"},
			args:  []string{"--coverage", "notes/*.md:notes/index-*.md"},
			want:  "VERIFY FAIL coverage notes/orphan.md",
		},
		{
			name:  "an index line pointing at nothing",
			plant: map[string]string{"notes/index-a.md": "# Index\n\n- [gone](gone.md)\n"},
			args:  []string{"--coverage", "notes/*.md:notes/index-*.md"},
			want:  "VERIFY FAIL backlink",
		},
		{
			// The fault the wall used to pass green. The target regex
			// excluded '#' and '?' from the whole target, so an anchored
			// link never matched and was never resolved: this exact plant
			// printed "VERIFY OK gating=0" and exited 0. Backlink findings
			// gate regardless of --links, so it was a wall waving a planted
			// fault through.
			name:  "an index line pointing at nothing through an anchor",
			plant: map[string]string{"notes/index-a.md": "# Index\n\n- [gone anchored](gone.md#top)\n"},
			args:  []string{"--coverage", "notes/*.md:notes/index-*.md"},
			want:  "VERIFY FAIL backlink",
		},
		{
			name:  "an index line pointing at nothing through a query string",
			plant: map[string]string{"notes/index-a.md": "# Index\n\n- [gone queried](gone.md?raw=1)\n"},
			args:  []string{"--coverage", "notes/*.md:notes/index-*.md"},
			want:  "VERIFY FAIL backlink",
		},
		{
			// Same hole in the other link grammar: the wikilink regex
			// excluded '|' and '#' from the body, so the aliased and heading
			// forms were never scanned and --links=gate waved them through.
			name:  "a dangling aliased wikilink under the gate",
			plant: map[string]string{"notes/aliased.md": "---\nname: aliased\n---\n\nsee [[nowhere-page|the missing page]] for the part nobody wrote down yet\n"},
			links: "gate",
			want:  "VERIFY FAIL wikilink [[nowhere-page]]",
		},
		{
			name:  "a dangling heading wikilink under the gate",
			plant: map[string]string{"notes/heading.md": "---\nname: heading\n---\n\nsee [[nowhere-page#the-readings]] for the part nobody wrote down yet\n"},
			links: "gate",
			want:  "VERIFY FAIL wikilink [[nowhere-page]]",
		},
		{
			name:  "a note with no frontmatter name",
			plant: map[string]string{"notes/bare.md": "just a paragraph of prose with no frontmatter above it\n"},
			args:  []string{"--frontmatter", "notes/*.md", "--exempt", "index-"},
			want:  "VERIFY FAIL frontmatter notes/bare.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyCorpus(t)
			for rel, content := range tc.plant {
				writeUnder(t, dir, rel, content)
			}
			links := tc.links
			if links == "" {
				links = "info"
			}
			args := append([]string{"verify", "--root", dir, "--links", links}, tc.args...)
			exit, stdout, stderr := runCLI(t, "", args...)
			if exit != 1 {
				t.Fatalf("exit = %d, want 1; stdout: %s stderr: %s", exit, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
			if strings.Contains(stdout, "VERIFY OK") {
				t.Errorf("a failing verify must not print an OK line, got %q", stdout)
			}
		})
	}
}

// The other arm of the widened link grammars: a link that RESOLVES through an
// alias or a heading must not be reported. Widening a regex until it catches
// the dangling case is only half the repair — a gate at a high false-positive
// rate trains a reader to wave findings through, which is the failure the
// --links ruling exists to prevent.
func TestVerifyDoesNotFlagLinksThatResolve(t *testing.T) {
	dir := copyCorpus(t)
	writeUnder(t, dir, "notes/aliased.md", "---\nname: aliased\n---\n"+
		"\nsee [[tide-tables|the jetty timing]] and [[lantern-care#the-brass]] and\n"+
		"[the tides](tides.md#the-sandbar) and [the lantern](lantern.md?raw=1) for the rest\n")
	// --links gate, so a wikilink false positive would show up as a FAIL line.
	// The fixture's own deliberate [[storm-glass]] still gates, so the exit is
	// 1 either way; what is under test is WHICH findings appear.
	exit, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate",
		"--coverage", "notes/*.md:notes/index-*.md")
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (the fixture's deliberate [[storm-glass]] gates); stderr: %s", exit, stderr)
	}
	for _, resolves := range []string{"tide-tables", "lantern-care", "tides.md", "lantern.md"} {
		if strings.Contains(stderr, resolves) {
			t.Errorf("a link that resolves was reported: %q appears in %q", resolves, stderr)
		}
	}
	if !strings.Contains(stderr, "VERIFY FAIL wikilink [[storm-glass]]") {
		t.Errorf("the fixture's known-dangling link stopped being found: %q", stderr)
	}
}

// A glob that matches nothing is a broken check, not a pass — refused (2),
// never reported green.
func TestVerifyRefusesAnEmptyCheck(t *testing.T) {
	cases := []struct {
		name, want string
		args       []string
	}{
		{"coverage A side matches nothing", "an empty side is a broken check", []string{"--coverage", "nowhere/*.md:notes/index-*.md"}},
		{"coverage B side matches nothing", "an empty side is a broken check", []string{"--coverage", "notes/*.md:nowhere/index-*.md"}},
		{"frontmatter glob matches nothing", "matched nothing", []string{"--frontmatter", "nowhere/*.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"verify", "--root", corpus, "--links", "info"}, tc.args...)
			exit, stdout, stderr := runCLI(t, "", args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// eval — the harness ships, and it is proven able to fail

func TestEvalOnTheShippedExampleGold(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", exampleGold)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout: %s stderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "EVAL OK recall@3=1.000 floor=0.800 rows=7 hits=7") {
		t.Errorf("stdout = %q, want the measured OK line", stdout)
	}
	if !strings.Contains(stdout, "EVAL HIT rank=") {
		t.Errorf("per-row receipts are missing from %q", stdout)
	}
}

// The point of shipping the harness: a channel set is a measurement, not a
// taste. Here the second channel measurably costs ranking quality on this
// fixture — the same finding that keeps trigram off unless a caller names it.
func TestEvalMeasuresChannelSetsAgainstEachOther(t *testing.T) {
	mrr := func(channels string) string {
		exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", channels, "--k", "3", "--floor", "0.8", exampleGold)
		if exit != 0 {
			t.Fatalf("channels %s: exit = %d; stderr: %s", channels, exit, stderr)
		}
		for _, line := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(line, "EVAL OK") {
				return line[strings.Index(line, "mrr="):]
			}
		}
		t.Fatalf("no EVAL OK line for channels %s", channels)
		return ""
	}
	if a, b := mrr("bm25"), mrr("bm25,trigram"); a == b {
		t.Errorf("the two channel sets measured identically (%s) — the harness is not discriminating", a)
	}
}

// The arm the source tool never had a test for: a gold expectation that is
// wrong must drive the exit code. Without this, "run the eval" is a ritual,
// not a property.
func TestEvalSaysNoBelowTheFloor(t *testing.T) {
	gold := filepath.Join(t.TempDir(), "wrong-gold.tsv")
	content := "# every expectation here names the wrong file on purpose\n" +
		"how often should the lantern glazing be washed\tnotes/fog-signal.md\n" +
		"when can the relief boat land at the jetty steps\tnotes/lantern.md\n" +
		"what does a short blast mean about the drive belt\tnotes/tides.md\n"
	if err := os.WriteFile(gold, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "1", "--floor", "0.8", gold)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout: %s stderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "EVAL FAIL recall@1=") || !strings.Contains(stderr, "below floor 0.800") {
		t.Errorf("stderr = %q, want the below-floor failure naming the measurement", stderr)
	}
	if strings.Contains(stdout, "EVAL OK") {
		t.Errorf("a failing eval must not print an OK line, got %q", stdout)
	}
	if !strings.Contains(stdout, "EVAL MISS query=") {
		t.Errorf("the failing rows must be named, got %q", stdout)
	}
}

// A floor of exactly the measured recall passes: a floor is a floor, not a
// fence to stay clear of — the same posture as the kernel budget.
func TestEvalFloorIsInclusive(t *testing.T) {
	exit, _, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "1", exampleGold)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 at a floor equal to the measured recall; stderr: %s", exit, stderr)
	}
}

// A broken gold file is a refusal, never a measurement. Each of these shapes
// would otherwise move the reported recall without moving anything a reader
// can see.
func TestEvalRefusesABrokenGoldFile(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"a row with no TAB", "how often should the glazing be washed notes/lantern.md\n", "has no TAB"},
		{"a row with an empty query", "\tnotes/lantern.md\n", "empty query"},
		{"a row naming no expected path", "how often should the glazing be washed\t\n", "can never hit"},
		{"a file with no rows at all", "# only comments\n\n# and blank lines\n", "zero rows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gold := filepath.Join(t.TempDir(), "gold.tsv")
			if err := os.WriteFile(gold, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", gold)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers

// copyCorpus materializes a writable copy of the fixture corpus so a test can
// plant a fault in it without touching what ships.
func copyCorpus(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(corpus, p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func writeUnder(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
