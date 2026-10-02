package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The answers a cold AI reader acts on in one turn (docs/STANDARD.md sections 2
// and 3; the tool ledger's rows P4, P5, P7, P8, P9, X2, X3, X4, X9, X11): every
// refusal carries the REFUSED word and says what it found, a misspelled flag
// names the nearest and the verb's flags, a group's -h is help, a verb's -h
// carries its own exit codes, and a verb that takes --json prints JSON.

func TestARefusalCarriesTheRefusedWordAndWhatItFound(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, tc := range []struct{ line, want string }{
		{"check foo", "nova-sprint check REFUSED: takes no words, found foo; run: nova-sprint check -h\n"},
		{"card", "nova-sprint card REFUSED: wants one primary id, found none; run: nova-sprint card -h\n"},
		{"card a b", "nova-sprint card REFUSED: wants one primary id, found a b; run: nova-sprint card -h\n"},
	} {
		t.Run(tc.line, func(t *testing.T) {
			code, out, errs := ta.do(tc.line)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Equal(t, tc.want, errs)
			assert.NotContains(t, errs, "<nil>")
		})
	}
	for _, line := range []string{"", "zz-no-such-verb"} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 2, code, line)
		assert.True(t, strings.HasPrefix(errs, "nova-sprint REFUSED: "), errs)
		assert.Contains(t, errs, "init, add, quack", line)
		assert.Equal(t, 1, strings.Count(errs, "\n"), "one line: %q", errs)
	}
}

func TestAMisspelledFlagNamesTheNearestAndTheVerbsFlags(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	code, _, errs := ta.do("add --strem s1 --count 1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nova-sprint add REFUSED: unknown flag --strem; did you mean --stream? add takes --actor, --after")
	assert.Contains(t, errs, "; run: nova-sprint help add\n")
	assert.NotContains(t, errs, "provided but not defined")
	code, _, errs = ta.do("fleet up m1 --wdth 3")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "did you mean --width? fleet up takes")
	assert.Contains(t, errs, "; run: nova-sprint help fleet up\n", "a two-word verb's help is its own")
}

func TestAGroupsHelpIsHelpAndABareGroupNamesItsVerbs(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, g := range []string{"fleet", "reader", "goal", "stream"} {
		t.Run(g, func(t *testing.T) {
			code, out, errs := ta.do(g + " -h")
			assert.Equal(t, 0, code)
			assert.Empty(t, errs)
			assert.True(t, strings.HasPrefix(out, "usage:\n  nova-sprint "+g+" "), out)
			assert.Contains(t, out, "nova-sprint help "+g+" <verb>")

			code, out, errs = ta.do(g)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Contains(t, errs, "nova-sprint "+g+" REFUSED: "+g+" wants one of its verbs; its verbs are "+strings.Join(groupVerbs(g), ", "))
		})
	}
	_, _, errs := ta.do("stream bogus")
	assert.Contains(t, errs, "unknown verb stream bogus; its verbs are stream remove; run: nova-sprint help stream")
}

func TestAVerbsHelpCarriesItsOwnExitCodes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	last := func(line string) string {
		code, out, _ := ta.do(line)
		require.Equal(t, 0, code, line)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		return lines[len(lines)-1]
	}
	assert.Equal(t, commonExit, last("take -h"))
	assert.NotContains(t, last("take -h"), "fleet sync", "a worker's verb is not told of fleet sync's codes")
	assert.Equal(t, verbExit["run"], last("run -h"))
	assert.Equal(t, verbExit["fleet sync"], last("fleet sync -h"))
	assert.Equal(t, verbExit["land"], last("help land"))
	assert.Contains(t, banner(), "\n"+exitLine+"\n", "nova-sprint help keeps every code at once")
}

func TestTheStoreRefusalNamesTheTwin(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	var out, errb strings.Builder
	code := a.run([]string{"where"}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "nova-sprint where REFUSED: --redis <addr> is required")
	assert.Contains(t, errb.String(), "--redis mem:<file>")
}

func TestTheVerbsThatTookJSONPrintJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, tc := range []struct{ line, verb string }{
		{"init --readers reader-a,reader-b --members m1", "init"},
		{"reader add reader-c", "reader add"},
		{"reader away reader-c", "reader away"},
		{"reader up reader-c", "reader up"},
		{"reader remove reader-c", "reader remove"},
		{"clear --confirm sprint", "clear"},
		{"teardown --confirm sprint", "teardown"},
	} {
		code, out, errs := ta.do(tc.line + " --json")
		require.Equal(t, 0, code, "%s: %s", tc.line, errs)
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &got), "%s printed %q", tc.line, out)
		assert.Equal(t, tc.verb, got["verb"], tc.line)
		assert.Equal(t, "ok", got["status"], tc.line)
		assert.Equal(t, 1, strings.Count(out, "\n"), "%s: one object: %q", tc.line, out)
	}
}

// rework of a primary that is not in review says what to run instead, by its
// state, as brief does.
func TestReworkOfACardNotInReviewSaysWhatToRunInstead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	code, _, errs := ta.do("rework s1-2 --fix x")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "not review (it is ready); no attempt has run, so there is nothing to rework: change its task instead: nova-sprint brief s1-2 --brief-file <path>")
	ta.ok("start")
	ta.deal(1)
	ta.ok("take --as m1")
	code, _, errs = ta.do("rework s1-1 --fix x")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "not review (it is working); its attempt is still running: once it finishes (review), run: nova-sprint rework s1-1 --fix '<what changes>', or now: nova-sprint drop s1-1 --reason '<why>'")
}

func TestAnEmptyBriefFileIsRefusedNamingIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	empty := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(empty, []byte("\n"), 0o600))
	before := ta.applies()
	for _, line := range []string{"add --stream s1 s1-x --brief-file " + empty, "add --stream s1 --count 1 --brief-file " + empty} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 2, code, line)
		assert.Contains(t, errs, "nova-sprint add REFUSED: --brief-file: "+empty+" holds no brief (it is empty)", line)
	}
	assert.Equal(t, before, ta.applies(), "a refused add wrote")
}

func TestOneLintFindingIsSaidInTheSingular(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	card, err := swarm.Template("card")
	require.NoError(t, err)
	brief := filepath.Join(t.TempDir(), "brief.md")
	require.NoError(t, os.WriteFile(brief, []byte(strings.Replace(card, "Report what was not done.\n", "", 1)), 0o600))
	code, _, errs := ta.do("add --stream s1 s1-1 --brief-file " + brief)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "fails the card lint (1 finding);")
	assert.Equal(t, "1 finding", findingsCount(1))
	assert.Equal(t, "2 findings", findingsCount(2))
}

// land's refusal of a head that is not a commit ends with the resume a land
// that met it owes: the conflict fact stopped the stream.
func TestTheHeadRefusalSaysTheStreamThenWantsResume(t *testing.T) {
	t.Parallel()
	why := headNotCommit("s1", landCard{id: "s1-2", head: "s1-2.w1"})
	assert.Contains(t, why, "run: nova-sprint return s1-2 --reason 'its head is not a commit', then nova-sprint rework s1-2 --fix 'finish with --head <commit>', then (a land that met it stopped the stream) nova-sprint resume --stream s1 --did 'returned s1-2 for rework'")
	assert.Empty(t, headNotCommit("s1", landCard{id: "s1-2", head: "9f3c2e1"}))
}

// A dry run names the fact land would record as would_record, never as fact,
// so a reader of the JSON need not check dry_run to know what the store holds.
func TestADryRunSaysWouldRecordNotFact(t *testing.T) {
	t.Parallel()
	b := landBatch{Stream: "s1", Status: "refused", Cards: 1, IDs: []string{"s1-2"}, WouldRecord: "conflict", DryRun: true}
	assert.Contains(t, b.line(), " would_record=conflict dry_run=yes")
	assert.NotContains(t, b.line(), "fact=")
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"would_record":"conflict"`)
	assert.NotContains(t, string(raw), `"fact"`)
}

// The help's first screen says where the rest is, and the finish it shows is
// the one land can merge.
func TestTheHelpSaysWhereTheRestIsAndFinishNamesItsHead(t *testing.T) {
	t.Parallel()
	first := strings.Join(strings.SplitN(banner(), "\n", 16)[:15], "\n")
	assert.Contains(t, first, "nova-sprint help <verb>")
	assert.Contains(t, banner(), "  nova-sprint finish --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed)")
	assert.Contains(t, banner(), "\n  nova-sprint land --stream s1 --check 'make test'\n", "the coordinator's day lands with land")
	assert.NotContains(t, banner(), "\n\nmachine has no ETA", "the ETA paragraph is one paragraph")
}
