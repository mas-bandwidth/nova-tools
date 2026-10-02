package tokens

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"time"
)

// The package's own tests: the day file's round trip and its strict parse, the shrink
// comparison with a dash on either side, the attribution ladder, and the lock.

func TestTheDayFileRoundTripsByteIdentically(t *testing.T) {
	t.Parallel()

	var c Counts
	c.Set(Input, 100)
	c.Set(CacheRead, 0)
	f := &DayFile{
		Day: "2026-09-11", At: "2026-09-11T23:55:02Z", Build: "abc", Turns: "42",
		Sources: []string{"bus:emma", "claude:glenn"},
		Rows: []DayRow{
			{Date: "2026-09-11", Model: "a", Repo: "schema", Counts: c, Rough: 2, Basis: UTC, Sources: []string{"claude:glenn"}},
		},
	}
	text := f.Render()
	got, findings := ParseDayFile("2026-09-11", text)
	require.Lenf(t, findings, 0, "a file this package wrote does not parse: %v", findings)
	{
		again := (&DayFile{Day: got.Day, At: got.At, Build: got.Build, Turns: got.Turns, Sources: got.Sources, Rows: got.Rows}).Render()
		assert.EqualValuesf(t, text, again, "the round trip is not byte-identical:\n%q\n%q", text, again)
	}
	// A zero is written as a zero and an absence as a dash, and the two are different.
	assert.Truef(t, strings.Contains(text, "\t100\t-\t-\t0\t-\t"), "the cells are not `100 - - 0 -`:\n%s", text)
}

func TestDayWritersRefuseSymlinkedOutputDirectory(t *testing.T) {
	t.Parallel()
	day := &DayFile{Day: "2026-09-11", Build: "fixture"}
	operations := map[string]func(string) error{
		"save": day.Save,
		"lock": func(out string) error {
			release, err := TakeFoldLock(out, 0)
			if release != nil {
				release()
			}
			return err
		},
	}
	for name, write := range operations {
		for _, suffix := range []string{"", string(os.PathSeparator), string(os.PathSeparator) + "."} {
			t.Run(name+"/"+fmt.Sprintf("suffix=%q", suffix), func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				target, link := filepath.Join(root, "target"), filepath.Join(root, "out")
				require.NoError(t, os.Mkdir(target, 0o700))
				for _, file := range []string{day.Day + FileSuffix, LockName} {
					require.NoError(t, os.WriteFile(filepath.Join(target, file), []byte("unchanged:"+file), 0o600))
				}
				require.NoError(t, os.Symlink(target, link))
				{
					err := write(link + suffix)
					assert.Falsef(t, err == nil || !strings.Contains(err.Error(), "symlink"), "write through symlink error=%v, want symlink refusal", err)
				}
				info, err := os.Lstat(link)
				require.Falsef(t, err != nil || info.Mode()&os.ModeSymlink == 0, "output link changed: %v / %v", info, err)
				entries, err := os.ReadDir(target)
				require.Falsef(t, err != nil || len(entries) != 2, "target directory changed: %v / %v", entries, err)
				for _, file := range []string{day.Day + FileSuffix, LockName} {
					got, err := os.ReadFile(filepath.Join(target, file))
					assert.Falsef(t, err != nil || string(got) != "unchanged:"+file, "target %s changed: %q / %v", file, got, err)
				}
			})
		}
	}
}

func TestDayWritersRefuseParentSymlinkedOutputDirectory(t *testing.T) {
	t.Parallel()
	day := &DayFile{Day: "2026-09-11", Build: "fixture"}
	operations := map[string]func(string) error{
		"save": day.Save,
		"lock": func(out string) error {
			release, err := TakeFoldLock(out, 0)
			if release != nil {
				release()
			}
			return err
		},
	}
	for name, write := range operations {
		for _, suffix := range []string{"", string(os.PathSeparator), string(os.PathSeparator) + "."} {
			t.Run(name+"/"+fmt.Sprintf("suffix=%q", suffix), func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				target := filepath.Join(root, "target")
				child := filepath.Join(target, "child")
				link := filepath.Join(root, "parent_link")
				require.NoError(t, os.MkdirAll(child, 0o700))
				require.NoError(t, os.Symlink(target, link))
				out := filepath.Join(link, "child") + suffix
				err := write(out)
				require.Falsef(t, err == nil || !strings.Contains(err.Error(), "symlink"), "write through parent symlink error=%v, want symlink refusal", err)
				require.Falsef(t, name == "lock" && !strings.Contains(err.Error(), "lock"), "lock error=%v, want mention of lock", err)
				info, err := os.Lstat(link)
				require.Falsef(t, err != nil || info.Mode()&os.ModeSymlink == 0, "parent link changed: %v / %v", info, err)
				entries, err := os.ReadDir(child)
				require.NoError(t, err)
				require.Lenf(t, entries, 0, "referent child directory changed: %d entries, want 0: %v", len(entries), entries)
				{
					_, err := os.Stat(filepath.Join(child, LockName))
					assert.Truef(t, os.IsNotExist(err), "lock file created in referent child: %v", err)
				}
				{
					_, err := os.Stat(filepath.Join(child, day.Day+FileSuffix))
					assert.Truef(t, os.IsNotExist(err), "day file created in referent child: %v", err)
				}
			})
		}
	}
}

func TestDayWritersAcceptRealOutputDirectorySpellings(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"", string(os.PathSeparator), string(os.PathSeparator) + "."} {
		t.Run(fmt.Sprintf("suffix=%q", suffix), func(t *testing.T) {
			t.Parallel()
			out := t.TempDir() + suffix
			release, err := TakeFoldLock(out, 0)
			require.NoError(t, err)
			defer release()
			day := &DayFile{Day: "2026-09-11", Build: "fixture"}
			require.NoError(t, day.Save(out))
			got, err := os.ReadFile(Path(out, day.Day))
			require.Falsef(t, err != nil || string(got) != day.Render(), "saved day=%q (%v), want %q", got, err, day.Render())
		})
	}
}

func TestAnUnversionedFileRefuses(t *testing.T) {
	t.Parallel()

	_, findings := ParseDayFile("2026-09-11", "date\tmodel\trepo\n")
	assert.Falsef(t, len(findings) != 1 || !strings.Contains(findings[0].Reason, Version), "an unversioned file gives %v; it wants one finding naming the version line", findings)
}

func TestTheShrinkComparisonWithADashOnEitherSide(t *testing.T) {
	t.Parallel()

	var was, now Counts
	was.Set(Input, 100)
	was.Set(Reasoning, 40)
	now.Set(Input, 60)
	// reasoning: a number in the file and a dash now -- the source went quiet.
	got := Shrinks(was, now, "2026-09-11")
	require.Lenf(t, got, 2, "%d shrinks, want two (input lower, reasoning gone): %v", len(got), got)
	assert.Falsef(t, got[0].Now != "60" || got[1].Now != Dash, "the two shrinks are %v", got)
	// The other direction is coverage arriving, not a shrink.
	var thin, fat Counts
	thin.Set(Input, 10)
	fat.Set(Input, 10)
	fat.Set(Reasoning, 40)
	{
		s := Shrinks(thin, fat, "2026-09-11")
		assert.Lenf(t, s, 0, "a dash becoming a number is a shrink: %v", s)
	}
}

// R5 (issue #268): the merge itself -- retained, replaced, blended, collision. A retained
// row comes back byte for byte, because the fold that wrote it is the only run that could
// compute it and this one must not touch it.
func TestMergeDayRetainsReplacesAndRefuses(t *testing.T) {
	t.Parallel()

	row := func(model, repo string, in int64, sources ...string) DayRow {
		var c Counts
		c.Set(Input, in)
		return DayRow{Date: "2026-09-14", Model: model, Repo: repo, Counts: c, Basis: UTC, Sources: sources}
	}

	// Retained and replaced, in one merge.
	old := []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn"), row("mercury-2.5", "serialize.rs", 1, "swarm:freddy")}
	fresh := []DayRow{row("mercury-2.5", "serialize.rs", 2000, "swarm:freddy")}
	merged, retained, partials := MergeDay(old, fresh, []string{"swarm:freddy"})
	require.Lenf(t, partials, 0, "partials on a clean merge: %v", partials)
	assert.EqualValuesf(t, 1, retained, "retained=%d, want 1", retained)
	require.Falsef(t, len(merged) != 2 || merged[0].Model != "claude-x" || merged[1].Model != "mercury-2.5", "merged rows are not the two, sorted by (model, repo): %v", merged)
	{
		got, _ := merged[0].Counts.Get(Input)
		assert.EqualValuesf(t, 410, got, "the retained row's input is %d, want 410 -- byte for byte is the promise", got)
	}
	{
		got, _ := merged[1].Counts.Get(Input)
		assert.EqualValuesf(t, 2000, got, "the replaced row's input is %d, want 2000 (replaced, never summed with the file's 1)", got)
	}

	// A blended row: one row's sources name a declared label and an undeclared one.
	old = []DayRow{row("claude-x", "serialize.rs", 410, "swarm:freddy", "swarm:glenn")}
	fresh = []DayRow{row("mercury-2.5", "serialize.rs", 2000, "swarm:freddy")}
	_, _, partials = MergeDay(old, fresh, []string{"swarm:freddy"})
	require.Falsef(t, len(partials) != 1 || partials[0].Model != "claude-x", "a blended row was not refused: %v", partials)
	assert.EqualValuesf(t, PartialBlended, partials[0].Why, "why=%q, want %q", partials[0].Why, PartialBlended)

	// A collision: a retained row and a recomputed row with the same (model, repo).
	old = []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn")}
	fresh = []DayRow{row("claude-x", "serialize.rs", 2000, "swarm:freddy")}
	_, _, partials = MergeDay(old, fresh, []string{"swarm:freddy"})
	require.Falsef(t, len(partials) != 1 || partials[0].Why != PartialCollision, "a collision was not refused: %v", partials)

	// Full replacement: nothing retained, and the merge is exactly this run's rows.
	old = []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn")}
	fresh = []DayRow{row("claude-x", "serialize.rs", 900, "swarm:glenn")}
	merged, retained, partials = MergeDay(old, fresh, []string{"swarm:glenn"})
	require.Falsef(t, len(partials) != 0 || retained != 0 || len(merged) != 1, "full replacement is not today's behaviour: %d rows, retained=%d, %v", len(merged), retained, partials)
	{
		got, _ := merged[0].Counts.Get(Input)
		assert.EqualValuesf(t, 900, got, "input %d, want 900 (replaced, not summed)", got)
	}

	// Byte for byte: the retained row renders exactly the line it was parsed from.
	before := (&DayFile{Day: "2026-09-14", At: "s", Build: "b", Turns: Dash,
		Sources: []string{"swarm:glenn"}, Rows: []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn")}}).Render()
	parsed, findings := ParseDayFile("2026-09-14", before)
	require.Lenf(t, findings, 0, "the fixture file does not parse: %v", findings)
	merged, _, _ = MergeDay(parsed.Rows, []DayRow{row("mercury-2.5", "serialize.rs", 2000, "swarm:freddy")}, []string{"swarm:freddy"})
	after := (&DayFile{Day: "2026-09-14", At: "s", Build: "b", Turns: Dash,
		Sources: []string{"swarm:glenn"}, Rows: []DayRow{merged[0]}}).Render()
	assert.EqualValuesf(t, before, after, "a retained row did not come back byte-identical:\nwas:  %q\nnow:  %q", before, after)
}

func TestTheAttributionLadder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	os.WriteFile(path, []byte("# a comment\n\nschema\t(^|/)schema($|/)\n"), 0o644)
	rules, err := LoadRules(path)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, prev, want string
		inputs           []string
	}{
		{name: "the first rule that matches names the repo", inputs: []string{"/w/schema/a.go"}, want: "schema"},
		{name: "a token that matches nothing is seen", inputs: []string{"/w/elsewhere/a.go"}, want: Other},
		{name: "no token at all inherits the stream's previous repo", inputs: []string{"nothing path-like here"}, prev: "schema", want: "schema"},
		{name: "no token and nothing before it is unknown", inputs: []string{""}, want: Unknown},
		{name: "a remote is a path-like token too", inputs: []string{"github.com:mas-bandwidth/schema"}, want: "schema"},
	} {
		{
			got := rules.AttributeInputs(tc.inputs, tc.prev)
			assert.EqualValuesf(t, tc.want, got, "%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The tally behind `sources --unattributed`: only the `other` arm feeds it, only when a
// caller asked for it, and the key is the tree rather than the file.
func TestTheUnattributedTallyCountsOnlyWhatFellToOther(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	// Off by default: an ordinary fold pays nothing for the tally and keeps none of it.
	rules.AttributeInputs([]string{"/w/elsewhere/a.go"}, "")
	{
		n := rules.TotalUnattributed()
		require.Falsef(t, n != 0 || len(rules.Unattributed()) != 0, "the tally ran without being asked for: total=%d stems=%v", n, rules.Unattributed())
	}
	rules.WatchUnattributed()
	rules.AttributeInputs([]string{"/w/schema/a.go"}, "")               // named: not tallied
	rules.AttributeInputs([]string{"nothing path-like here"}, "schema") // no token: not tallied
	rules.AttributeInputs([]string{"/home/nova/tree/a.go"}, "")         // other
	rules.AttributeInputs([]string{"/home/nova/tree/deeper/b.go"}, "")  // other, same tree
	rules.AttributeInputs([]string{"/home/nova/other-tree/c.go"}, "")   // other
	rules.AttributeInputs([]string{Unattributed}, "")                   // a bucket name passes through
	{
		n := rules.TotalUnattributed()
		assert.EqualValuesf(t, 3, n, "unattributed total = %d, want 3", n)
	}
	got := rules.Unattributed()
	assert.Falsef(t, len(got) != 2 || got[0].Stem != "/home/nova/tree" || got[0].Count != 2 || got[1].Stem != "/home/nova/other-tree", "the tally is %v; it wants the heaviest tree first, keyed by the tree", got)
}

// TestTheUnattributedTallyScopesToFilteredDay: when FilterDay is set, only messages
// attributed under that day are tallied.
func TestTheUnattributedTallyScopesToFilteredDay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	rules.WatchUnattributed()
	rules.FilterDay("2026-09-11")

	// Message on 2026-09-11: should be tallied
	rules.SetDay("2026-09-11")
	rules.AttributeInputs([]string{"/home/nova/day1/a.go"}, "")

	// Message on 2026-09-12: should be ignored
	rules.SetDay("2026-09-12")
	rules.AttributeInputs([]string{"/home/nova/day2/b.go"}, "")

	n := rules.TotalUnattributed()
	assert.Equal(t, 1, n, "unattributed total = %d, want 1", n)
	got := rules.Unattributed()
	assert.Equal(t, []UnattributedStem{{Stem: "/home/nova/day1", Count: 1}}, got, "got %v, want 1 stem for day 1", got)
}

// The tally is bounded, and past the ceiling it still counts every token: a listing whose
// memory grows with the tree is the unbounded read this repo's caps exist to end, and a
// total that stopped at the ceiling would be a number nobody could use.
func TestTheUnattributedTallyIsBoundedAndKeepsItsTotal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	rules.WatchUnattributed()
	const n = stemLimit + 1000
	for i := 0; i < n; i++ {
		rules.AttributeInputs([]string{fmt.Sprintf("/home/nova/t%06d/a.go", i)}, "")
	}
	// The one stem admitted first keeps counting after the ceiling is reached.
	for i := 0; i < 5; i++ {
		rules.AttributeInputs([]string{"/home/nova/t000000/b.go"}, "")
	}
	{
		got := len(rules.Unattributed())
		assert.EqualValuesf(t, stemLimit, got, "%d stems held, want the ceiling of %d", got, stemLimit)
	}
	{
		got := rules.TotalUnattributed()
		assert.EqualValuesf(t, n+5, got, "total = %d, want every token counted (%d)", got, n+5)
	}
	{
		top := rules.Unattributed()[0]
		assert.Falsef(t, top.Stem != "/home/nova/t000000" || top.Count != 6, "the heaviest stem is %v; a stem already held keeps counting past the ceiling", top)
	}
}

// PathStem is the key, and it is one function so that the listing and the rule a person
// writes from it are cut from the same string.
func TestPathStemKeepsTheTree(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"/Users/glenn/deepseek-working-3/cmd/a.go", "/Users/glenn/deepseek-working-3"},
		{"/Users/glenn/deepseek-working-3", "/Users/glenn/deepseek-working-3"},
		{"/x/y", "/x/y"},
		{"/x", "/x"},
		{"~/rowan-working/nova-tools/cmd/a.go", "~/rowan-working/nova-tools"},
		{"~", "~"},
		{"//double//slash//and//more", "/double/slash/and"},
		{"github.com:mas-bandwidth/nova-tools", "github.com:mas-bandwidth/nova-tools"},
	} {
		{
			got := PathStem(tc.in)
			assert.EqualValuesf(t, tc.want, got, "PathStem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAMalformedRulesLineIsNamed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\nthis line has no tab\n"), 0o644)
	_, err := LoadRules(path)
	assert.Falsef(t, err == nil || !strings.Contains(err.Error(), "line 2"), "a malformed rules line gives %v; it wants the line number", err)
}

func TestMissingDaysAreNamedAndNeverFilled(t *testing.T) {
	t.Parallel()

	got := MissingDays([]string{"2026-09-07", "2026-09-08", "2026-09-10"})
	assert.Falsef(t, len(got) != 1 || got[0] != "2026-09-09", "missing days are %v, want [2026-09-09]", got)
	{
		n := MissingDays([]string{"2026-02-27", "2026-03-02"})
		assert.Falsef(t, len(n) != 2 || n[0] != "2026-02-28" || n[1] != "2026-03-01", "across a month end the missing days are %v", n)
	}
	{
		n := MissingDays([]string{"2026-09-11"})
		assert.Nilf(t, n, "one day has no gaps, got %v", n)
	}
}

func TestTheFoldLockIsExclusiveAndNamesItsHolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	release, err := TakeFoldLock(dir, LockWait)
	require.NoError(t, err)
	{
		pid := HolderPID(filepath.Join(dir, LockName))
		assert.False(t, pid == Dash, "the lock file holds no pid, so a waiter could not name the holder")
	}
	_, err = TakeFoldLock(dir, 50*time.Millisecond)
	require.Error(t, err, "a second fold took the lock")
	assert.Truef(t, strings.Contains(err.Error(), LockName), "the refusal does not name the lock: %v", err)
	release()
	release() // safe more than once
	again, err := TakeFoldLock(dir, LockWait)
	require.NoErrorf(t, err, "the lock was not released: %v", err)
	again()
}

func TestTheBusGrammarIsOneGrammar(t *testing.T) {
	t.Parallel()

	// The parser is the serializer's inverse, which is the only way the two stay one
	// grammar: report writes this and fold --bus reads it.
	line := BodyLine("2026-09-11", "emma", "gemini", "schema", Input, 1234, UTC)
	{
		f := strings.Split(line, "\t")
		assert.Lenf(t, f, 6, "a UTC line has %d fields, want six: %q", len(f), line)
	}
	zoned := BodyLine("2026-09-11", "emma", "gemini", "unattributed", Output, 7, "America/Los_Angeles")
	assert.Truef(t, strings.HasSuffix(zoned, "\tday_basis=America/Los_Angeles"), "a zoned line does not carry its basis: %q", zoned)
	subject := Subject("2026-09-11", "2026-09-11T23:55:02Z", "b", []string{"emma-000000000001", "emma-000000000002"})
	p, ok := ParseSubject(subject)
	assert.Falsef(t, !ok || p.day != "2026-09-11" || len(p.supersedes) != 2 || p.badSet != "", "the subject %q does not parse back: %+v ok=%v", subject, p, ok)
	// `at=` is an RFC 3339 UTC stamp: `at=garbage build=b` was taken for a tokens note,
	// and the fold validates every note's Date: against that stamp.
	// Rule 6 names ONE trailer in ONE order, `at=<stamp> build=<id>[ supersedes=<set>]`,
	// and says any other text after the date is not a tokens note. The keys used to be
	// accepted in any order and any position, so three arrangements nobody wrote were
	// tokens notes. A day is a date on the calendar too: 2026-02-30 is not one.
	for _, bad := range []string{"Tokens 2026-09-11", "tokens 2026-09-11 (rough)", "tokens 2026-09-11 at=x",
		"tokens 11-09-2026", "tokens 2026-09-11 at=garbage build=b", "tokens 2026-09-11 at=2026-09-11T23:55:02-07:00 build=b",
		"tokens 2026-09-11 build=b at=2026-09-11T23:55:02Z",
		"tokens 2026-09-11 supersedes=emma-000000000001 at=2026-09-11T23:55:02Z build=b",
		"tokens 2026-09-11 at=2026-09-11T23:55:02Z supersedes=emma-000000000001 build=b",
		"tokens 2026-09-11 at=2026-09-11T23:55:02Z build=b supersedes=emma-000000000001 at=2026-09-11T23:55:02Z",
		"tokens 2026-02-30", "tokens 2026-13-40"} {
		{
			_, ok := ParseSubject(bad)
			assert.Falsef(t, ok, "%q was taken for a tokens note's subject", bad)
		}
	}
	{
		p, _ := ParseSubject("tokens 2026-09-11 at=2026-09-11T23:55:02Z build=b supersedes=emma-000000000002,emma-000000000001")
		assert.False(t, p.badSet == "", "an unsorted predecessor set was accepted")
	}
}

// TestValidDayIsACalendarCheck: a day is a date, not a ten-character shape. `--day
// 2026-13-40` was accepted, wrote a day file, passed check, and left MissingDays walking
// from a day that does not exist.
func TestValidDayIsACalendarCheck(t *testing.T) {
	t.Parallel()

	for _, good := range []string{"2026-09-11", "2024-02-29", "2026-01-01", "2026-12-31"} {
		assert.Truef(t, ValidDay(good), "%s is a day", good)
	}
	for _, bad := range []string{"2026-13-40", "2026-02-30", "2026-00-10", "2026-09-31", "2026-09-00", "2026-9-11", "not-a-day!"} {
		assert.Falsef(t, ValidDay(bad), "%s is not a day", bad)
	}
}

// TestValidMonthIsACalendarCheck: a month is a calendar month (01-12), not just seven characters
// with a hyphen.
func TestValidMonthIsACalendarCheck(t *testing.T) {
	t.Parallel()

	for _, good := range []string{"2026-01", "2026-09", "2026-12", "1999-02"} {
		assert.True(t, ValidMonth(good), "%s is a month", good)
	}
	for _, bad := range []string{"2026-13", "2026-00", "2026-9", "2026-99", "not-a-month", "2026/01", "2026-a1"} {
		assert.False(t, ValidMonth(bad), "%s is not a month", bad)
	}
}

func TestASourceLineFieldIsADashWhereItIsNotAMeasurement(t *testing.T) {
	t.Parallel()

	s := &Source{Kind: KindClaude}
	assert.EqualValues(t, Dash, s.StatField("nousage"), "a transcript has no job directories, so nousage is a dash and not a zero")
	assert.EqualValues(t, "0", s.StatField("dup"), "a transcript CAN have duplicate ids, so zero of them is a measurement")
	// SPEC-TOKENS' TOKENS SOURCE paragraph says "a transcript has no unparsed lines",
	// so the column is a dash there and the code follows the spec rather than arguing
	// with it in the output. The lines themselves are not lost: a transcript line whose
	// stamp does not parse is one TOKENS UNPARSED line naming the label and is in the
	// unparsed= total on TOKENS FAIL (rule 3). Striking the spec's clause is a spec
	// decision, and the PR body carries it as one; this assertion moves with the spec.
	assert.EqualValues(t, Dash, s.StatField("unparsed"), "the spec says a transcript has no unparsed lines, so the column is a dash")
	b := &Source{Kind: KindBus}
	assert.False(t, b.StatField("dup") != Dash || b.StatField("comments") != "0", "a bus lane has no duplicate ids and does have comments")
}

// L10a: readSource is the one whole-file read, and it had no ceiling, so one oversized
// ledger or bus file took the process's memory. A file over the cap must be refused by
// name rather than read.
func TestReadSourceRefusesAnOversizedFile(t *testing.T) {
	t.Parallel()

	const capInTest = 64 << 20
	path := filepath.Join(t.TempDir(), "huge.csv")
	f, err := os.Create(path)
	require.NoError(t, err)
	if err := f.Truncate(capInTest + 1); err != nil {
		f.Close()
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	_, err = readSource(path)
	require.Errorf(t, err, "readSource read %d bytes with no cap", capInTest+1)
	assert.Truef(t, strings.Contains(err.Error(), path), "the refusal must name the path: %v", err)
	assert.Truef(t, strings.Contains(err.Error(), fmt.Sprint(capInTest)), "the refusal must name the cap %d: %v", capInTest, err)
}

// L10b: onCycle marked a node seen, walked its predecessors, then deleted it on the way
// out, so a diamond was re-walked once per path. A node already proven acyclic must stay
// memoized, which the walk's own seen map must show; the answer is unchanged.
func TestOnCycleDoesNotRewalkAProvenAcyclicDiamond(t *testing.T) {
	t.Parallel()

	bottom := &note{id: "d"}
	left := &note{id: "b", subject: parsedSubject{supersedes: []string{"d"}}}
	right := &note{id: "c", subject: parsedSubject{supersedes: []string{"d"}}}
	top := &note{id: "a", subject: parsedSubject{supersedes: []string{"b", "c"}}}
	all := map[string]*note{"a": top, "b": left, "c": right, "d": bottom}
	seen := map[string]bool{}
	require.False(t, onCycle(top, all, seen), "onCycle reported a cycle in an acyclic diamond")
	assert.Lenf(t, seen, len(all), "onCycle left %d of %d nodes proven: it re-walked an acyclic node", len(seen), len(all))
}

// L10b: the memo must not hide a cycle. A diamond with a back edge still reports true.
func TestOnCycleStillSeesARealCycle(t *testing.T) {
	t.Parallel()

	bottom := &note{id: "d"}
	left := &note{id: "b", subject: parsedSubject{supersedes: []string{"d"}}}
	right := &note{id: "c", subject: parsedSubject{supersedes: []string{"d", "a"}}}
	top := &note{id: "a", subject: parsedSubject{supersedes: []string{"b", "c"}}}
	all := map[string]*note{"a": top, "b": left, "c": right, "d": bottom}
	require.True(t, onCycle(top, all, map[string]bool{}), "onCycle missed a cycle through a supersedes back edge")
}

func TestCheckThroughStale(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hdr := "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	row := "2026-09-18\tm\tr\t10\t10\t-\t-\t-\t0\tUTC\tx\n"
	content := "nova-tokens v1 day=2026-09-18 at=2026-09-18T23:55:00Z build=b turns=1 sources=x\n" + hdr + row
	require.NoError(t, os.WriteFile(filepath.Join(dir, "2026-09-18.tsv"), []byte(content), 0o600))

	res, err := Check(dir, CheckOptions{Through: "2026-09-20"})
	require.NoError(t, err)
	assert.Truef(t, res.Stale, "Check with Through=2026-09-20 when last=2026-09-18 want Stale=true, got false")

	res, err = Check(dir, CheckOptions{Through: "2026-09-18"})
	require.NoError(t, err)
	assert.Falsef(t, res.Stale, "Check with Through=2026-09-18 when last=2026-09-18 want Stale=false, got true")

	res, err = Check(dir, CheckOptions{Through: "2026-09-15"})
	require.NoError(t, err)
	assert.Falsef(t, res.Stale, "Check with Through=2026-09-15 when last=2026-09-18 want Stale=false, got true")
}

// isAtomicTemp grammar: only .<YYYY-MM-DD>.tsv.tmp-<8hex> with a valid calendar day
// is recognized; unrelated dotfiles or malformed names are rejected.
func TestIsAtomicTempGrammar(t *testing.T) {
	t.Parallel()

	valid := []string{
		".2026-09-11.tsv.tmp-1a2b3c4d",
		".2026-01-01.tsv.tmp-00000000",
		".2026-12-31.tsv.tmp-DEADBEEF",
		".2026-02-28.tsv.tmp-abcdef12",
	}
	for _, name := range valid {
		assert.Truef(t, isAtomicTemp(name), "isAtomicTemp(%q) = false, want true", name)
	}

	invalid := []string{
		".unrelated.txt.tmp-12345678",
		".2026-09-11.txt.tmp-12345678",
		"2026-09-11.tsv.tmp-1a2b3c4d",
		".2026-02-31.tsv.tmp-1a2b3c4d", // invalid day
		".2026-99-99.tsv.tmp-12345678",
		".2026-09-11.tsv.tmp-1234",
		".2026-09-11.tsv.tmp-123456789",
		".2026-09-11.tsv.tmp-1234567g", // non-hex
		".2026-09-11.tsv.tmp-",
		".2026-09-11.tsv",
		"2026-09-11.tsv.tmp",
	}
	for _, name := range invalid {
		assert.Falsef(t, isAtomicTemp(name), "isAtomicTemp(%q) = true, want false", name)
	}
}

// Check must report unrelated temporary files as strays, while recognizing
// valid dayfile atomic temporary files.
func TestCheckReportsUnrelatedTempAsStray(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(out, 0o755))

	// Valid day file
	df := &DayFile{
		Day: "2026-09-11", At: "2026-09-11T23:55:02Z", Build: "abc",
		Rows: []DayRow{{Date: "2026-09-11", Model: "m1", Repo: "r1", Basis: UTC}},
	}
	require.NoError(t, df.Save(out))

	// Valid atomic temp: not a stray
	require.NoError(t, os.WriteFile(filepath.Join(out, ".2026-09-11.tsv.tmp-1a2b3c4d"), []byte("partial\n"), 0o644))

	res, err := Check(out, CheckOptions{})
	require.NoErrorf(t, err, "Check failed: %v", err)
	assert.Lenf(t, res.Strays, 0, "valid atomic temp was reported as stray: %v", res.Strays)

	// Unrelated dotfile temp: MUST be reported as a stray
	unrelatedPath := filepath.Join(out, ".unrelated.txt.tmp-12345678")
	require.NoError(t, os.WriteFile(unrelatedPath, []byte("foreign\n"), 0o644))

	res2, err := Check(out, CheckOptions{})
	require.NoErrorf(t, err, "Check failed: %v", err)
	assert.Falsef(t, len(res2.Strays) != 1 || res2.Strays[0] != unrelatedPath, "Check strays = %v, want [%s]", res2.Strays, unrelatedPath)
}

// A day file written while the `units` column existed still reads: the twelfth cell is
// ignored, the rows that column alone kept apart are the one (model, repo) row they add up
// to, and the writer puts out eleven columns.
func TestALegacyTwelveColumnDayFileReadsWithTheUnitsColumnIgnored(t *testing.T) {
	t.Parallel()

	legacy := "nova-tokens v1 day=2026-09-21 at=2026-09-22T00:00:00Z build=x turns=3 sources=bus:a,claude:b\n" +
		LegacyHeaderLine + "\n" +
		"2026-09-21\tm1\tschema\t10\t20\t-\t-\t-\t1\tutc\tclaude:b\tu1\n" +
		"2026-09-21\tm1\tschema\t5\t-\t3\t-\t-\t0\tutc\tbus:a\tu2\n" +
		"2026-09-21\tm1\tserialize\t1\t1\t-\t-\t-\t0\tutc\tclaude:b\t-\n"
	d, findings := ParseDayFile("2026-09-21", legacy)
	require.Lenf(t, findings, 0, "a legacy file was refused: %v", findings)
	require.Lenf(t, d.Rows, 2, "rows = %d, want 2 (the two unit rows of m1/schema are one)", len(d.Rows))
	r := d.Rows[0]
	{
		in, _ := r.Counts.Get(Input)
		assert.EqualValuesf(t, 15, in, "input = %d, want 15", in)
	}
	{
		cw, ok := r.Counts.Get(CacheWrite)
		assert.Falsef(t, !ok || cw != 3, "cache_write = %d,%v, want 3,true", cw, ok)
	}
	assert.Falsef(t, r.Rough != 1 || strings.Join(r.Sources, ",") != "bus:a,claude:b", "rough %d sources %v", r.Rough, r.Sources)

	out := d.Render()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.EqualValuesf(t, HeaderLine, lines[1], "the writer's header is %q, want the eleven names", lines[1])
	for _, l := range lines[2:] {
		{
			n := len(strings.Split(l, "\t"))
			assert.EqualValuesf(t, 11, n, "a written row has %d cells, want 11: %q", n, l)
		}
	}

	// Eleven-column files do not merge: a second row for one (model, repo) is an error.
	dup := "nova-tokens v1 day=2026-09-21 at=x build=x turns=- sources=a\n" + HeaderLine + "\n" +
		"2026-09-21\tm1\tschema\t1\t1\t-\t-\t-\t0\tutc\ta\n2026-09-21\tm1\tschema\t1\t1\t-\t-\t-\t0\tutc\ta\n"
	{
		_, f := ParseDayFile("2026-09-21", dup)
		assert.False(t, len(f) == 0, "a second (model, repo) row in an eleven-column file was accepted")
	}
}
