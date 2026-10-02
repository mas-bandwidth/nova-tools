package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rules 16 and 18, the halves that are about what does NOT decide an answer: the checkout's
// mtimes, its directory order, its INDEX and its git history. The order of two competing
// notes is in the notes themselves and nowhere else, so a fold must be byte-identical
// under every one of those rearranged, and the tool runs no git (a fake one on PATH records
// any call): a fold that fetched would be a fold whose numbers depend on a network call.
func TestNothingAboutTheCheckoutDecidesWhichNoteIsTheDay(t *testing.T) {
	// The real git, resolved before the fake one goes on PATH: the fixture's history is
	// built with it, and the tool must still never run git.
	realGit, _ := exec.LookPath("git")
	if realGit == "" {
		t.Skip("no git on PATH; the reversed-history fixture wants one")
	}
	b := newBench(t)
	gitLog := fakeGit(t)
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	line := func(n string) string { return "2026-09-11\temma\tg\tschema\tinput\t" + n + "\n" }
	// The successor's filename sorts BEFORE its predecessor's, and (below) its mtime is
	// older and its position in a rebuilt INDEX is earlier. None of that may matter.
	first := busNote(t, bus, "emma", "zzz-first.md", "emma-000000000001", "tokens 2026-09-11", busDate, line("100"))
	busNote(t, bus, "emma", "aaa-second.md", "emma-000000000002", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes="+first, busDate, line("250"))
	rows := func(out string) string {
		novaTokens.Do(t, "fold", "--out", out, "--all", "--repos", b.repos, "--bus", bus).Exit(0).Out("TOKENS SUPERSEDED")
		_, rows, _ := strings.Cut(testkit.ReadFile(t, filepath.Join(out, "2026-09-11.tsv")), "\n")
		return rows
	}
	before := rows(b.out)
	require.Contains(t, before, "\t250\t")

	// The mtimes swapped, so the successor looks older than what it replaces.
	old, newer := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(filepath.Join(bus, "from-emma", "aaa-second.md"), old, old))
	require.NoError(t, os.Chtimes(filepath.Join(bus, "from-emma", "zzz-first.md"), newer, newer))
	// An INDEX sorted by path, the way `nova-bus check --rebuild-index` writes it: a
	// derived catalogue, and never a statement about which number the friend meant.
	testkit.WriteFile(t, filepath.Join(bus, "INDEX.md"), "- from-emma/aaa-second.md\n- from-emma/zzz-first.md\n")
	// And a REAL git history in the checkout, whose commit order is the opposite of the
	// send order: the successor is committed first. Demanded test 6 asks for "the notes'
	// commit order reversed in a fixture git history"; an empty .git directory was not one.
	gitRun(t, realGit, bus, "init", "-q")
	for _, f := range []string{"from-emma/aaa-second.md", "from-emma/zzz-first.md"} {
		gitRun(t, realGit, bus, "add", "--", f)
		gitRun(t, realGit, bus, "commit", "-q", "-m", "commit "+f)
	}

	assert.Equal(t, before, rows(testkit.Mkdir(t, filepath.Join(b.dir, "out2"))), "the rows moved when the checkout was rearranged")
	assert.NoFileExists(t, gitLog, "git was invoked")
}

// Rule 20 and demanded test 20's last third: a provider's local-day total crosses the bus
// as a seventh field, and folds back to the same row it would have folded from the export.
func TestAZonedReportCrossesTheBusWithoutBeingCalledUTC(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	export := testkit.WriteFile(t, filepath.Join(b.dir, "xai.csv"), "# timezone: America/Los_Angeles\ndate,model,input,output\n2026-09-11,grok-4,900,80\n")
	note := filepath.Join(b.dir, "note.txt")
	r := novaTokens.Do(t, "report", "--who", "johnny", "--day", "2026-09-11", "--repos", b.repos, "--provider", "xai:johnny="+export, "--note", note).Exit(0)
	for _, line := range lines(r.Stdout) {
		f := strings.Split(line, "\t")
		assert.Len(t, f, 7, "a zoned report line is %q; it wants seven fields ending day_basis=<zone>", line)
		assert.Equal(t, "day_basis=America/Los_Angeles", f[len(f)-1], line)
	}
	_, subject, _ := strings.Cut(lineWith(r.Stderr, "REPORT OK"), "subject=")

	// The note folds to the same rows the export folds to directly: every cell but the
	// date and the sources.
	cells := func(out string) (keep []string) {
		for _, l := range lines(testkit.ReadFile(t, filepath.Join(out, "2026-09-11.tsv")))[2:] {
			keep = append(keep, strings.Join(strings.Split(l, "\t")[1:10], "\t"))
		}
		return keep
	}
	bus := busDir(t, filepath.Join(b.dir, "bus"), "johnny")
	busNote(t, bus, "johnny", "n.md", "johnny-000000000001", subject, busDate, testkit.ReadFile(t, note))
	viaBus := b.fold("--bus", bus).Exit(0)
	linesHold(t, viaBus.Stdout, map[string][]string{"TOKENS SOURCE": {"day_basis=America/Los_Angeles"}, "TOKENS DAY": {"nonutc=1"}}, viaBus)
	direct := testkit.Mkdir(t, filepath.Join(b.dir, "out-direct"))
	novaTokens.Do(t, "fold", "--out", direct, "--day", "2026-09-11", "--repos", b.repos, "--provider", "xai:johnny="+export).Exit(0)
	assert.Equal(t, cells(direct), cells(b.out), "the note's row is not the export's row")

	// A hand-written seventh field that spells utc, one that is not day_basis=, and an
	// eighth field are each unparsed: two spellings of one fact would be two grammars.
	for _, c := range []struct{ name, line string }{
		{"a seventh field spelling utc", "2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tday_basis=utc"},
		{"a seventh field that is not day_basis=", "2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tzone=America/Los_Angeles"},
		{"an eighth field", "2026-09-11\tjohnny\tgrok-4\tunattributed\tinput\t1\tday_basis=X\textra"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			lane := busDir(t, filepath.Join(b.dir, "bus"), "johnny")
			busNote(t, lane, "johnny", "n.md", "johnny-000000000002", "tokens 2026-09-11", busDate, c.line+"\n")
			b.fold("--bus", lane).Exit(1).Err("TOKENS UNPARSED")
		})
	}
}

// Rule 12's other half: the build id is compiled in, and `version` says which build wrote
// a day file, in exactly the four tokens. A day file whose stamp a caller could set would
// be a stamp nobody could trust.
func TestVersionSaysWhichBuildIsRunning(t *testing.T) {
	t.Parallel()

	assert.Empty(t, novaTokens.Version(t, "nova-tokens").Extras)
	novaTokens.Do(t, "version", "--anything").Exit(2)
}
