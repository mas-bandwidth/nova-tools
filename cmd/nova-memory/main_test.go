package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"no subcommand", nil, "run: nova-memory help"},
		{"unknown verb", []string{"frobnicate"}, `unknown verb "frobnicate"; the verbs are quickstart, stats, search, check, verify, eval, boot, version`},

		{"stats without root", []string{"stats"}, "--root is required"},
		{"stats stray argument", []string{"stats", "--root", corpus, "extra"}, `takes no positional arguments, got "extra"`},
		{"quickstart stray argument", []string{"quickstart", "--root", corpus, "extra"}, `takes no positional arguments, got "extra"`},

		{"search without root", []string{"search", "--channels", "bm25", "--k", "3", "x"}, "--root is required"},
		{"search without channels", []string{"search", "--root", corpus, "--k", "3", "x"}, "--channels is required"},
		{"search without k", []string{"search", "--root", corpus, "--channels", "bm25", "x"}, "--k is required"},
		{"search with zero k", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "0", "x"}, "--k must be a positive"},
		{"search with negative k", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "-2", "x"}, "--k must be a positive"},
		{"search without a query", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "3"}, "takes <words>..., at least 1 argument, got 0"},
		{"search with unknown channel", []string{"search", "--root", corpus, "--channels", "semantic", "--k", "3", "x"}, `unknown channel "semantic"`},
		{"search with empty channels", []string{"search", "--root", corpus, "--channels", "", "--k", "3", "x"}, "named no channels"},
		{"search with a stray comma in channels", []string{"search", "--root", corpus, "--channels", "bm25,", "--k", "3", "x"}, "empty entry"},

		{"check without root", []string{"check", "--channels", "bm25", "--k", "3", "-"}, "--root is required"},
		{"check without channels", []string{"check", "--root", corpus, "--k", "3", "-"}, "--channels is required"},
		{"check without k", []string{"check", "--root", corpus, "--channels", "bm25", "-"}, "--k is required"},
		{"check without a named input", []string{"check", "--root", corpus, "--channels", "bm25", "--k", "3"}, "takes <file|->, exactly 1 argument, got 0"},
		{"check with two inputs", []string{"check", "--root", corpus, "--channels", "bm25", "--k", "3", "a", "b"}, "takes <file|->, exactly 1 argument, got 2"},

		{"verify without root", []string{"verify", "--links", "info", "--coverage", "a:b"}, "--root is required"},
		{"verify without links", []string{"verify", "--root", corpus, "--coverage", "a:b"}, "--links is required"},
		{"verify with a bad links value", []string{"verify", "--root", corpus, "--links", "maybe"}, "--links must be gate or info"},
		{"verify with no gating check", []string{"verify", "--root", corpus, "--links", "info"}, "a run that cannot fail is not a verification"},
		{"verify exempt without frontmatter", []string{"verify", "--root", corpus, "--links", "gate", "--exempt", "index-"}, "--exempt only applies"},
		{"verify with a malformed coverage pair", []string{"verify", "--root", corpus, "--links", "info", "--coverage", "notes"}, "--coverage wants A:B"},
		{"verify stray argument", []string{"verify", "--root", corpus, "--links", "gate", "extra"}, `takes no positional arguments, got "extra"`},

		{"eval without root", []string{"eval", "--channels", "bm25", "--k", "3", "--floor", "0.8", exampleGold}, "--root is required"},
		{"eval without channels", []string{"eval", "--root", corpus, "--k", "3", "--floor", "0.8", exampleGold}, "--channels is required"},
		{"eval without k", []string{"eval", "--root", corpus, "--channels", "bm25", "--floor", "0.8", exampleGold}, "--k is required"},
		{"eval without floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", exampleGold}, "--floor is required"},
		{"eval with a zero floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0", exampleGold}, "a harness that cannot fail is not a measurement"},
		{"eval with a negative floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "-1", exampleGold}, "--floor must be in (0,1]"},
		{"eval with a floor above one", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "1.5", exampleGold}, "--floor must be in (0,1]"},
		{"eval with a NaN floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "NaN", exampleGold}, "--floor must be in (0,1]"},
		{"eval with an Inf floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "+Inf", exampleGold}, "--floor must be in (0,1]"},
		{"eval without a gold file", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8"}, "takes <gold.tsv>, exactly 1 argument, got 0"},
		{"eval with a missing gold file", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", "testdata/no-such-gold.tsv"}, "no-such-gold.tsv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tt.args...)
			require.Equalf(t, 2, exit, "exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			assert.Containsf(t, stderr, tt.wantStderr, "stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			assert.Equalf(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
		})
	}
}

// Missing flags must be reported in the same order every run: the order the verb
// declares its rules in, never map order.
func TestRequiredFlagErrorOrderDeterministic(t *testing.T) {
	t.Parallel()

	for i := 0; i < 20; i++ {
		exit, _, stderr := runCLI(t, "", "eval")
		require.Equalf(t, 2, exit, "exit = %d, want 2", exit)
		want := []string{"--root is required", "--k is required", "--channels is required", "--floor is required"}
		at := -1
		for _, w := range want {
			idx := strings.Index(stderr, w)
			require.GreaterOrEqualf(t, idx, 0, "stderr must name every missing flag, got %q", stderr)
			require.GreaterOrEqualf(t, idx, at, "flag errors out of sorted order (run %d): %q", i, stderr)
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
	require.Equalf(t, 2, exit, "exit = %d, want 2 — an environment variable must not supply the root; stdout: %s", exit, stdout)
	assert.Containsf(t, stderr, "--root is required", "stderr = %q, want the refusal to name --root", stderr)
}

func TestRefusesAnUnusableRoot(t *testing.T) {
	t.Parallel()

	empty := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-dir.md")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	cases := []struct{ name, root, want string }{
		{"missing directory", filepath.Join(empty, "nope"), "not a readable directory"},
		{"a file, not a directory", file, "not a readable directory"},
		{"a directory holding no markdown", empty, "no markdown files found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := runCLI(t, "", "stats", "--root", tc.root)
			require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
			assert.Containsf(t, stderr, tc.want, "stderr = %q, want it to contain %q", stderr, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// The verbs, on the fixture corpus

func TestStats(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "stats", "--root", corpus)
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	for _, want := range []string{
		"STATS OK schema=nova-memory/2 files=6",
		"STATS OK class=. chunks=",
		"STATS OK class=log chunks=",
		"STATS OK class=notes chunks=",
	} {
		assert.Containsf(t, stdout, want, "stdout = %q, want it to contain %q", stdout, want)
	}
}

func TestStatsHonoursExclude(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "stats", "--root", corpus, "--exclude", "log")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	assert.NotContainsf(t, stdout, "class=log", "an excluded class was indexed anyway: %q", stdout)
	assert.Containsf(t, stdout, "class=notes", "--exclude removed more than it was given: %q", stdout)
}

func TestSearch(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3",
		"when", "can", "the", "relief", "boat", "land", "at", "the", "jetty")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	for _, want := range []string{
		"SEARCH OK hits=", "hits=3", "channels=bm25",
		"SEARCH CAL score=", "probe=unrelated-control",
		"SEARCH HIT rank=1", "class=notes", "name=tide-tables", "type=reference", "notes/tides.md:",
		"SEARCH NOTE lexical only",
	} {
		assert.Containsf(t, stdout, want, "stdout = %q, want it to contain %q", stdout, want)
	}
}

// Every receipt carries its class: "already written down in a dated log" and
// "already distilled into a note" are different answers to "do I know this?",
// and a receipt that hid the difference would hand the mind the wrong verdict.
func TestSearchReceiptsCarryClassAndFrontmatter(t *testing.T) {
	t.Parallel()

	_, stdout, _ := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "6",
		"washing", "the", "glazing", "before", "an", "onshore", "gale")
	assert.Containsf(t, stdout, "class=log", "no log-class receipt in %q", stdout)
	assert.Containsf(t, stdout, "class=notes name=lantern-care type=measured", "no classed, frontmattered note receipt in %q", stdout)
	assert.Containsf(t, stdout, "name=- type=-", "a file without frontmatter must still print stable fields, got %q", stdout)
}

// Two seed memories under two roots must both be reachable by one search: the
// cairn besided memory/ is a second root, not part of the first, and a query
// that spans both must hand back a hit from each, with every hit naming the
// root it came from. #488 lives in the cairn and can never surface from
// memory/ alone, however lexical the index.
func TestSearchSpansMultipleRoots(t *testing.T) {
	t.Parallel()

	memdir := t.TempDir()
	cairn := t.TempDir()
	writeUnder(t, memdir, "compressor.md",
		"the compressor belt hardens with age and the blast runs a half second short.\n")
	writeUnder(t, cairn, "cairns-compressor.md",
		"the compressor held pressure through the long gale, a fact worth banking.\n")
	exit, stdout, stderr := runCLI(t, "",
		"search", "--root", memdir, "--root", cairn, "--channels", "bm25", "--k", "2", "compressor")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	for _, want := range []string{
		"hits=2",
		"compressor.md:",
		"cairns-compressor.md:",
		"root=" + memdir,
		"root=" + cairn,
	} {
		assert.Containsf(t, stdout, want, "stdout = %q, want it to contain %q", stdout, want)
	}
}

// The calibration probe is part of what the schema version names — the spec
// says changing it IS a schema change. Nothing enforced that: the probe lives
// in this package, SchemaVersion lives in internal/memindex, and each could
// move without the other. This is the third copy that makes the parity a
// tripwire instead of a remembered rule, the same pattern the floors registry
// uses. Change one of these and this test goes red until you change both.
func TestCalibrationProbeAndSchemaVersionMoveTogether(t *testing.T) {
	t.Parallel()

	const wantProbe = "the quarterly marketing budget for the regional office needs revised headcount projections before the fiscal deadline"
	const wantSchema = "nova-memory/2"
	assert.Equalf(t, wantProbe, calibrationProbe, "the calibration probe changed:\n got: %q\nwant: %q\n"+
		"The probe defines the negative-control band, so every band printed under the old probe is incomparable "+
		"with every band printed under the new one. If the change is intended, bump memindex.SchemaVersion in the "+
		"same commit and update BOTH constants here.", calibrationProbe, wantProbe)
	assert.Equalf(t, wantSchema, memindex.SchemaVersion, "the schema version changed:\n got: %q\nwant: %q\n"+
		"Update this test and confirm the calibration probe above is still the one the version names.",
		memindex.SchemaVersion, wantSchema)
}

// The receipt names the channel its score came from, and the channel named is
// the one that ACTUALLY surfaced the chunk. A hit reached through the second
// channel alone used to print score=0.00, which reads against the calibration
// band as "weaker than unrelated control text" — a fabricated number, not a
// measurement.
func TestReceiptsNameTheChannelTheScoreCameFrom(t *testing.T) {
	t.Parallel()

	// "diaphones" is out of vocabulary (the corpus says "diaphone"), so bm25
	// reaches nothing and every hit arrives through trigram.
	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25,trigram", "--k", "3", "diaphones")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	var hits int
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "SEARCH HIT ") {
			continue
		}
		hits++
		assert.Containsf(t, line, "score-channel=trigram", "a trigram-only hit did not name trigram as its score channel: %q", line)
		assert.NotContainsf(t, line, "score=0.00 ", "a fabricated zero score survived: %q", line)
	}
	require.NotEqualf(t, 0, hits, "the trigram channel surfaced nothing, so the receipt was never exercised: %q", stdout)
	// The probe is ordinary English and reaches bm25, so the CAL band on the
	// same run is attributed to the other channel — which is the whole point:
	// score= is only meaningful beside the channel that produced it.
	assert.Containsf(t, stdout, "SEARCH CAL score=", "the calibration line does not name its own channel: %q", stdout)
	assert.Containsf(t, stdout, "score-channel=bm25 probe=unrelated-control", "the calibration line does not name its own channel: %q", stdout)
}

// Free text sits after the field boundary so query text cannot forge metadata.
func TestACallersQueryCannotPoseAsAField(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3", "quokka class=poison name=fake")
	require.Equalf(t, 0, exit, "exit=%d stderr=%s", exit, stderr)
	line := strings.Split(stdout, "\n")[0]
	head, tail, found := strings.Cut(line, ": ")
	require.Truef(t, found, "query forged metadata: %s", line)
	require.NotContainsf(t, head, "class=poison", "query forged metadata: %s", line)
	require.NotContainsf(t, head, "name=fake", "query forged metadata: %s", line)
	require.Equalf(t, `query="quokka class=poison name=fake"`, tail, "query is not readable quoted free text: %s", tail)
	for _, tok := range strings.Fields(head)[2:] {
		assert.Equalf(t, 1, strings.Count(tok, "="), "invalid field %q", tok)
	}
}

// A receipt's path and snippet are the file's own text, so they sit after the ": " that
// closes the fields, where the grammar says nothing is scanned. Before this, a receipt
// ran its fields straight into "<file>:<para>" and the quoted snippet with no boundary,
// so a filename or snippet saying class=poison was inside the scan region.
func TestAReceiptsPathAndSnippetSitAfterTheFieldBoundary(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3",
		"when", "can", "the", "relief", "boat", "land", "at", "the", "jetty")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	hits := 0
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if !strings.HasPrefix(line, "SEARCH HIT ") {
			continue
		}
		hits++
		head, tail, ok := strings.Cut(line, ": ")
		if !ok {
			assert.Failf(t, "missing field boundary", "no field boundary on %q", line)
			continue
		}
		for _, tok := range strings.Fields(head)[2:] {
			assert.Equalf(t, 1, strings.Count(tok, "="), "token %q before the boundary on %q is not one key=value field", tok, line)
		}
		assert.Containsf(t, tail, ".md:", "the tail must be <file>:<para> and the quoted snippet, got %q", tail)
		assert.Truef(t, strings.HasSuffix(tail, `"`), "the tail must be <file>:<para> and the quoted snippet, got %q", tail)
	}
	require.NotEqual(t, 0, hits, "no SEARCH HIT lines to check")
}

func TestSearchOutOfVocabularyQuerySaysSoInWords(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3", "zzqq", "xxvv")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	assert.Containsf(t, stdout, "hits=0", "a zero must be explained, never bare: %q", stdout)
	assert.Containsf(t, stdout, "SEARCH MISS every query term is out of vocabulary", "a zero must be explained, never bare: %q", stdout)
}

func TestCheckFromStdin(t *testing.T) {
	t.Parallel()

	in := "The compressor belt hardens with age and the blast runs a half second short.\n\n" +
		"The relief boat should not try the jetty steps near low water on a spring tide.\n"
	exit, stdout, stderr := runCLI(t, in, "check", "--root", corpus, "--channels", "bm25", "--k", "2", "-")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
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
		assert.Containsf(t, stdout, want, "stdout = %q, want it to contain %q", stdout, want)
	}
	// k is the mind's budget: exactly k receipts per candidate, never more.
	{
		got := strings.Count(stdout, "MEMORY HIT cand=1 ")
		assert.Equalf(t, 2, got, "candidate 1 got %d receipts, want exactly k=2", got)
	}
}

func TestCheckFromANamedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cand := filepath.Join(dir, "candidate.md")
	require.NoError(t, os.WriteFile(cand, []byte("Salt haze on the glazing has to be washed off in daylight before it etches the glass.\n"), 0o644))
	exit, stdout, stderr := runCLI(t, "", "check", "--root", corpus, "--channels", "bm25", "--k", "3", cand)
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	assert.Containsf(t, stdout, "source="+cand, "stdout = %q, want it to name the source file", stdout)
	assert.Containsf(t, stdout, "notes/lantern.md:", "the candidate's own subject did not surface: %q", stdout)
}

// A CRLF candidate file must split into the same candidates as its LF twin.
// The split is on the literal "\n\n", so before line endings were normalized
// a CRLF input arrived as ONE candidate — the whole file queried as a single
// blob against a corpus indexed paragraph by paragraph, silently, with a
// green MEMORY OK line.
func TestCheckCandidateSplittingIsLineEndingAgnostic(t *testing.T) {
	t.Parallel()

	lfBody := "The compressor belt hardens with age and the blast runs a half second short.\n\n" +
		"The relief boat should not try the jetty steps near low water on a spring tide.\n\n" +
		"Salt haze on the glazing has to be washed off in daylight before it etches the glass.\n"
	run := func(name, body string) string {
		p := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		exit, stdout, stderr := runCLI(t, "", "check", "--root", corpus, "--channels", "bm25", "--k", "2", p)
		require.Equalf(t, 0, exit, "%s: exit = %d, want 0; stderr: %s", name, exit, stderr)
		return stdout
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
	lf := run("lf.md", lfBody)
	// Both twins, because a normalization that only folds "\r\n" leaves a
	// lone-CR file exactly as broken as an unfixed CRLF one.
	for _, tw := range []struct{ name, ending string }{
		{"CRLF", "\r\n"},
		{"CR", "\r"},
	} {
		t.Run(tw.name, func(t *testing.T) {
			twin := run("twin.md", strings.ReplaceAll(lfBody, "\n", tw.ending))
			for _, want := range []string{"MEMORY OK candidates=3", "MEMORY CAND n=3"} {
				assert.Containsf(t, twin, want, "%s input: stdout = %q, want it to contain %q — the file arrived as one giant candidate", tw.name, twin, want)
			}
			assert.Equalf(t, strip(lf), strip(twin), "the twins produced different receipts:\n--- lf ---\n%s--- %s ---\n%s", lf, tw.name, twin)
		})
	}
}

func TestCheckRefusesUnusableInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	thin := filepath.Join(dir, "thin.md")
	require.NoError(t, os.WriteFile(thin, []byte("# h\n\nok\n"), 0o644))
	cases := []struct{ name, stdin, arg, want string }{
		{"a file with no candidate paragraph", "", thin, "no candidate paragraph"},
		{"empty stdin", "", "-", "no candidate paragraph"},
		{"a file that does not exist", "", filepath.Join(dir, "nope.md"), "nope.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, tc.stdin, "check", "--root", corpus, "--channels", "bm25", "--k", "3", tc.arg)
			require.Equalf(t, 2, exit, "exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			assert.Containsf(t, stderr, tc.want, "stderr = %q, want it to contain %q", stderr, tc.want)
			assert.Equalf(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
		})
	}
}

// Two runs over the same corpus and the same input must produce identical
// bytes. Go randomizes map iteration, and stats' build= duration is the one
// field deliberately excluded from this promise (it is a measurement, and it
// is labelled as one).
func TestRetrievalOutputIsByteIdentical(t *testing.T) {
	t.Parallel()

	in := "The relief boat should not try the jetty steps near low water on a spring tide.\n"
	for _, args := range [][]string{
		{"check", "--root", corpus, "--channels", "bm25,trigram", "--k", "5", "-"},
		{"search", "--root", corpus, "--channels", "bm25,trigram", "--k", "5", "salt", "haze", "glazing"},
		{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.5", exampleGold},
	} {
		_, a, _ := runCLI(t, in, args...)
		_, b, _ := runCLI(t, in, args...)
		require.Equalf(t, a, b, "%v produced different bytes on two runs:\n--- a ---\n%s--- b ---\n%s", args[0], a, b)
	}
}

// ---------------------------------------------------------------------------
// verify — and the ruling that unresolved wikilinks have no default

func TestVerifyPassesOnTheFixtureCorpus(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "verify", "--root", corpus, "--links", "info",
		"--coverage", "notes/*.md:notes/index-*.md", "--frontmatter", "notes/*.md", "--exempt", "index-")
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	assert.Containsf(t, stdout, "VERIFY OK gating=0 info=1", "stdout = %q, want the clean OK line", stdout)
	assert.Containsf(t, stdout, "VERIFY INFO wikilink: [[storm-glass]]", "the informational wikilink finding is missing: %q", stdout)
}

// The exit contract has no default: the same corpus, the same findings, and
// two different exits, chosen by the caller and by nobody else. The tool this
// was ported from defaulted this to informational while its own spec promised
// a nonzero exit on findings, and a script trusting the spec passed dangling
// links silently.
func TestVerifyLinksRulingIsTheCallersBothWays(t *testing.T) {
	t.Parallel()

	base := []string{"verify", "--root", corpus, "--coverage", "notes/*.md:notes/index-*.md"}
	exit, stdout, _ := runCLI(t, "", append(append([]string{}, base...), "--links", "info")...)
	require.Equalf(t, 0, exit, "--links info: exit = %d, want 0", exit)
	assert.Containsf(t, stdout, "VERIFY INFO wikilink", "--links info must still report the finding, got %q", stdout)

	exit, stdout, stderr := runCLI(t, "", append(append([]string{}, base...), "--links", "gate")...)
	require.Equalf(t, 1, exit, "--links gate: exit = %d, want 1; stderr: %s", exit, stderr)
	assert.Containsf(t, stderr, "VERIFY FAIL wikilink [[storm-glass]]", "stderr = %q, want the gating wikilink failure", stderr)
	assert.NotContainsf(t, stdout, "VERIFY OK", "a failing verify must not print an OK line, got %q", stdout)
}

// Planted faults, one per gating check, each observed failing. A check never
// seen failing is not a check.
func TestVerifySaysNoOnPlantedFaults(t *testing.T) {
	t.Parallel()

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
			require.Equalf(t, 1, exit, "exit = %d, want 1; stdout: %s stderr: %s", exit, stdout, stderr)
			assert.Containsf(t, stderr, tc.want, "stderr = %q, want it to contain %q", stderr, tc.want)
			assert.NotContainsf(t, stdout, "VERIFY OK", "a failing verify must not print an OK line, got %q", stdout)
		})
	}
}

// The other arm of the widened link grammars: a link that RESOLVES through an
// alias or a heading must not be reported. Widening a regex until it catches
// the dangling case is only half the repair — a gate at a high false-positive
// rate trains a reader to wave findings through, which is the failure the
// --links ruling exists to prevent.
func TestVerifyDoesNotFlagLinksThatResolve(t *testing.T) {
	t.Parallel()

	dir := copyCorpus(t)
	writeUnder(t, dir, "notes/aliased.md", "---\nname: aliased\n---\n"+
		"\nsee [[tide-tables|the jetty timing]] and [[lantern-care#the-brass]] and\n"+
		"[the tides](tides.md#the-sandbar) and [the lantern](lantern.md?raw=1) for the rest\n")
	// --links gate, so a wikilink false positive would show up as a FAIL line.
	// The fixture's own deliberate [[storm-glass]] still gates, so the exit is
	// 1 either way; what is under test is WHICH findings appear.
	exit, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate",
		"--coverage", "notes/*.md:notes/index-*.md")
	require.Equalf(t, 1, exit, "exit = %d, want 1 (the fixture's deliberate [[storm-glass]] gates); stderr: %s", exit, stderr)
	for _, resolves := range []string{"tide-tables", "lantern-care", "tides.md", "lantern.md"} {
		assert.NotContainsf(t, stderr, resolves, "a link that resolves was reported: %q appears in %q", resolves, stderr)
	}
	assert.Containsf(t, stderr, "VERIFY FAIL wikilink [[storm-glass]]", "the fixture's known-dangling link stopped being found: %q", stderr)
}

// A glob that matches nothing is a broken check, not a pass — refused (2),
// never reported green.
func TestVerifyRefusesAnEmptyCheck(t *testing.T) {
	t.Parallel()

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
			require.Equalf(t, 2, exit, "exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			assert.Containsf(t, stderr, tc.want, "stderr = %q, want it to contain %q", stderr, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// eval — the harness ships, and it is proven able to fail

func TestEvalOnTheShippedExampleGold(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", exampleGold)
	require.Equalf(t, 0, exit, "exit = %d, want 0; stdout: %s stderr: %s", exit, stdout, stderr)
	assert.Containsf(t, stdout, "EVAL OK recall@3=1.000 floor=0.800 rows=7 hits=7", "stdout = %q, want the measured OK line", stdout)
	// THE HIT IS NOT LISTED. It was: seven rows, seven EVAL HIT lines, on the run where
	// every one of them said the same thing the OK line already says. At five hundred
	// rows that was 36 KB of "this worked". The hits are a count now, and a run with no
	// misses is one line.
	assert.NotContainsf(t, stdout, "EVAL HIT", "a passing row is listed rather than counted: %q", stdout)
	assert.Containsf(t, stdout, "misses=0 shown=0", "the OK line does not carry the miss count: %q", stdout)
	{
		n := strings.Count(stdout, "\n")
		assert.Equalf(t, 1, n, "a clean seven-row eval printed %d lines, want 1:\n%s", n, stdout)
	}
}

// TestExampleGoldHeaderCommentMatchesRowCount asserts that the header comment
// stating the evaluation row count matches the actual count of evaluation rows
// in testdata/example-gold.tsv (7 rows), so any future discrepancy fails.
func TestExampleGoldHeaderCommentMatchesRowCount(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(exampleGold)
	require.NoError(t, err)
	rows, err := readGold(exampleGold)
	require.NoErrorf(t, err, "readGold: %v", err)
	actualCount := len(rows)
	require.Equalf(t, 7, actualCount, "actual evaluation rows = %d, want 7", actualCount)

	wordToNum := map[string]int{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
		"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}

	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(trimmed, "benchmark:"); idx != -1 {
			rest := strings.TrimSpace(trimmed[idx+len("benchmark:"):])
			fields := strings.Fields(rest)
			if len(fields) >= 2 && strings.HasPrefix(fields[1], "row") {
				found = true
				word := strings.ToLower(fields[0])
				statedCount, ok := wordToNum[word]
				if !ok {
					if n, err := strconv.Atoi(word); err == nil {
						statedCount = n
						ok = true
					}
				}
				require.Truef(t, ok, "could not parse number of rows from word %q in header comment: %s", fields[0], trimmed)
				assert.Equalf(t, actualCount, statedCount, "header comment states %d rows (%q), but file contains %d evaluation rows: %s",
					statedCount, word, actualCount, trimmed)
			}
		}
	}
	require.Truef(t, found, "did not find '# benchmark: <N> rows' comment in header of %s", exampleGold)
}

// The point of shipping the harness: a channel set is a measurement, not a
// taste. Here the second channel measurably costs ranking quality on this
// fixture — the same finding that keeps trigram off unless a caller names it.
func TestEvalMeasuresChannelSetsAgainstEachOther(t *testing.T) {
	t.Parallel()

	mrr := func(channels string) string {
		exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", channels, "--k", "3", "--floor", "0.8", exampleGold)
		require.Equalf(t, 0, exit, "channels %s: exit = %d; stderr: %s", channels, exit, stderr)
		for _, line := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(line, "EVAL OK") {
				return line[strings.Index(line, "mrr="):]
			}
		}
		require.FailNowf(t, "missing evaluation result", "no EVAL OK line for channels %s", channels)
		return ""
	}
	{
		a, b := mrr("bm25"), mrr("bm25,trigram")
		assert.NotEqualf(t, b, a, "the two channel sets measured identically (%s) — the harness is not discriminating", a)
	}
}

// The arm the source tool never had a test for: a gold expectation that is
// wrong must drive the exit code. Without this, "run the eval" is a ritual,
// not a property.
func TestEvalSaysNoBelowTheFloor(t *testing.T) {
	t.Parallel()

	gold := filepath.Join(t.TempDir(), "wrong-gold.tsv")
	content := "# every expectation here names the wrong file on purpose\n" +
		"how often should the lantern glazing be washed\tnotes/fog-signal.md\n" +
		"when can the relief boat land at the jetty steps\tnotes/lantern.md\n" +
		"what does a short blast mean about the drive belt\tnotes/tides.md\n"
	require.NoError(t, os.WriteFile(gold, []byte(content), 0o644))
	exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "1", "--floor", "0.8", gold)
	require.Equalf(t, 1, exit, "exit = %d, want 1; stdout: %s stderr: %s", exit, stdout, stderr)
	assert.Containsf(t, stderr, "EVAL FAIL recall@1=", "stderr = %q, want the below-floor failure naming the measurement", stderr)
	assert.Containsf(t, stderr, "below floor 0.800", "stderr = %q, want the below-floor failure naming the measurement", stderr)
	assert.NotContainsf(t, stdout, "EVAL OK", "a failing eval must not print an OK line, got %q", stdout)
	assert.Containsf(t, stdout, "EVAL MISS query=", "the failing rows must be named, got %q", stdout)
}

// A floor of exactly the measured recall passes: a floor is a floor, not a
// fence to stay clear of — the same posture as the kernel budget.
func TestEvalFloorIsInclusive(t *testing.T) {
	t.Parallel()

	exit, _, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "1", exampleGold)
	require.Equalf(t, 0, exit, "exit = %d, want 0 at a floor equal to the measured recall; stderr: %s", exit, stderr)
}

// A broken gold file is a refusal, never a measurement. Each of these shapes
// would otherwise move the reported recall without moving anything a reader
// can see.
func TestEvalRefusesABrokenGoldFile(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, content, want string }{
		{"a row with no TAB", "how often should the glazing be washed notes/lantern.md\n", "has no TAB"},
		{"a row with an empty query", "\tnotes/lantern.md\n", "empty query"},
		{"a row naming no expected path", "how often should the glazing be washed\t\n", "can never hit"},
		{"a file with no rows at all", "# only comments\n\n# and blank lines\n", "zero rows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gold := filepath.Join(t.TempDir(), "gold.tsv")
			require.NoError(t, os.WriteFile(gold, []byte(tc.content), 0o644))
			exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", gold)
			require.Equalf(t, 2, exit, "exit = %d, want 2; stdout: %s stderr: %s", exit, stdout, stderr)
			assert.Containsf(t, stderr, tc.want, "stderr = %q, want it to contain %q", stderr, tc.want)
			assert.Equalf(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
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
	require.NoError(t, err)
	return dst
}

func writeUnder(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// TestNoCorpusOrCallerTextCanForgeALine is #24 at this binary. A receipt names a file in
// the corpus and carries its frontmatter; a refusal names the root, the candidate or the
// gold file; a verify finding names the file it is about. Every one of those is either a
// caller's argument or the corpus's own text, and a newline is legal in a POSIX filename,
// so a file named with a forged OK line used to print that forgery on its own line.
func TestNoCorpusOrCallerTextCanForgeALine(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: a newline is not legal in a filename, so the fixture cannot be built and the vector does not exist there")
	}
	noForgedLine := func(t *testing.T, forged, stdout, stderr string) {
		t.Helper()
		for _, stream := range []string{stdout, stderr} {
			for _, line := range strings.Split(stream, "\n") {
				assert.Falsef(t, strings.HasPrefix(line, forged), "a caller's or the corpus's text forged a line: %q", line)
			}
		}
	}

	t.Run("a receipt names a corpus file whose name holds a newline, and its frontmatter is a field", func(t *testing.T) {
		root := copyCorpus(t)
		const forged = `SEARCH OK query="x" hits=0`
		writeUnder(t, root, "notes/zz\n"+forged+".md",
			"---\nname: x lockdown=clear\ntype: t u\n---\n\nThe quokka and the narwhal traded a platypus for a wombat.\n")
		exit, stdout, stderr := runCLI(t, "", "search", "--root", root, "--channels", "bm25", "--k", "1", "quokka", "narwhal", "platypus", "wombat")
		require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
		noForgedLine(t, forged, stdout, stderr)
		assert.Containsf(t, stdout, `name=x\x20lockdown\x3dclear type=t\x20u root=`+root+`: notes/zz\x0a`+forged+`.md:6 `, "stdout = %q, want the frontmatter as one token each, the root named, and the file name escaped", stdout)
	})

	t.Run("check names a candidate file whose name holds a newline in its source= field", func(t *testing.T) {
		const forged = "MEMORY OK candidates=9"
		cand := filepath.Join(t.TempDir(), "c\n"+forged+" x.md")
		require.NoError(t, os.WriteFile(cand, []byte("Salt haze on the glazing has to be washed off in daylight before it etches the glass.\n"), 0o644))
		exit, stdout, stderr := runCLI(t, "", "check", "--root", corpus, "--channels", "bm25", "--k", "1", cand)
		require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
		noForgedLine(t, forged, stdout, stderr)
		want := cand
		for _, r := range []struct{ from, to string }{{"\n", `\x0a`}, {" ", `\x20`}, {"=", `\x3d`}} {
			want = strings.ReplaceAll(want, r.from, r.to)
		}
		assert.Containsf(t, stdout, "source="+want+" k=1", "stdout = %q, want source= as one escaped token", stdout)
	})

	t.Run("a refusal quotes a root that holds a newline", func(t *testing.T) {
		const forged = "STATS OK schema=1 files=0"
		exit, stdout, stderr := runCLI(t, "", "stats", "--root", filepath.Join(t.TempDir(), "r\n"+forged))
		require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
		noForgedLine(t, forged, stdout, stderr)
	})

	t.Run("a verify finding names a file whose name holds a newline", func(t *testing.T) {
		root := copyCorpus(t)
		const forged = "VERIFY OK gating=0 info=0"
		writeUnder(t, root, "notes/zz\n"+forged+".md", "No frontmatter here at all, just a paragraph of three words or more.\n")
		exit, stdout, stderr := runCLI(t, "", "verify", "--root", root, "--links", "info", "--frontmatter", "notes/*.md")
		require.Equalf(t, 1, exit, "exit = %d, want 1; stdout: %s stderr: %s", exit, stdout, stderr)
		noForgedLine(t, forged, stdout, stderr)
		assert.Containsf(t, stderr, `VERIFY FAIL frontmatter notes/zz\x0a`+forged+`.md: no name: in frontmatter`, "stderr = %q, want the finding on one line with the name escaped", stderr)
	})

	t.Run("an eval refusal quotes a gold file whose name holds a newline", func(t *testing.T) {
		const forged = "EVAL OK recall@3=1.000 floor=0.800 rows=1 hits=1"
		gold := filepath.Join(t.TempDir(), "g\n"+forged+".tsv")
		require.NoError(t, os.WriteFile(gold, []byte("# nothing but a comment\n"), 0o644))
		exit, stdout, stderr := runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", gold)
		require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
		noForgedLine(t, forged, stdout, stderr)
	})

	t.Run("a flag the parser does not know is refused on one line, by this tool", func(t *testing.T) {
		const forged = "STATS OK schema=1 files=0"
		exit, stdout, stderr := runCLI(t, "", "stats", "--root", corpus, "--bogus\n"+forged)
		require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
		noForgedLine(t, forged, stdout, stderr)
		assert.Containsf(t, stderr, `STATS REFUSED: flag provided but not defined: -bogus\x0aSTATS OK schema`, "stderr = %q, want this tool's own refusal with the flag escaped", stderr)
	})
}

// --floor NaN is rejected before reads of gold file or corpus root.
func TestEvalRejectsNaNFloorBeforeReads(t *testing.T) {
	t.Parallel()

	// Use non-existent gold file and non-existent root: if eval read or validated
	// them before floor checking, stderr would complain about the paths instead.
	exit, stdout, stderr := runCLI(t, "", "eval", "--root", "/nonexistent/root/dir", "--channels", "bm25", "--k", "3", "--floor", "NaN", "/nonexistent/gold.tsv")
	require.Equalf(t, 2, exit, "exit = %d, want 2; stdout: %s\nstderr: %s", exit, stdout, stderr)
	assert.Containsf(t, stderr, "--floor must be in (0,1] (got NaN)", "stderr does not name the rejected floor: %q", stderr)
	assert.NotContainsf(t, stderr, "/nonexistent", "rejection happened after reading or attempting to read paths: %q", stderr)
	assert.Equalf(t, "", stdout, "expected empty stdout on refusal, got %q", stdout)
}
