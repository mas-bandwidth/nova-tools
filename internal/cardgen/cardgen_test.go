package cardgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardlimits"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const serialFixture = `# The tests that do not open with t.Parallel()
# ceiling: 5
cmd/nova-bus/a_test.go:TestOne serial: t.Setenv
cmd/nova-bus/a_test.go:TestTwo serial: t.Chdir
cmd/nova-bus/b_test.go:TestThree serial: swaps package var main.x
internal/swarm/c_test.go:TestFour serial: os.Setenv
internal/swarm/d_test.go:TestFive serial: t.Setenv
this row has no colon
`

var header = Header{Repo: "example/repo", Base: "dev", Sha: "0123456789abcdef0123456789abcdef01234567"}

// A ledger's rows group by file, in ledger order; a row the parser cannot read is
// reported, not dropped silently.
func TestLedgerRowsGroupByFileInLedgerOrder(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, skipped := ParseLedger(l, serialFixture)
	require.Len(t, rows, 5)
	assert.Equal(t, []string{"internal/ci/testdata/serial-tests_allowlist.txt:8: this row has no colon"}, skipped)
	assert.Equal(t, Row{File: "cmd/nova-bus/a_test.go", Key: "TestOne", Rest: "serial: t.Setenv", Line: 3}, rows[0])
	p := PlanLedger(l, rows, "", "", 0)
	require.Len(t, p.Cards, 4)
	assert.Equal(t, "serial-tests-cmd-nova-bus-a", p.Cards[0].ID)
	assert.Len(t, p.Cards[0].Rows, 2)
	assert.Equal(t, "flash", p.Tier)
	assert.Equal(t, "fix-red", p.Cards[0].Kind)
}

// PATHS: the file, its package's test files, the ledger; never more than MaxPaths.
func TestLedgerPathsAreTheFileItsPackageTestsAndTheLedger(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	p := PlanLedger(l, rows, "", "", 0)
	assert.Equal(t, []string{"cmd/nova-bus/a_test.go", "cmd/nova-bus/*_test.go", l.File}, p.Cards[0].Paths)
	assert.Equal(t, "internal/ci TestEveryTestOpensWithTParallel", p.Cards[0].Test)
	// a package row (sleeps-skips) takes the package's go files
	pkg, _ := ParseLedger(Ledgers["sleeps-skips"], "internal/bus\tTestX\t#1 calls time.Sleep\n")
	pp := PlanLedger(Ledgers["sleeps-skips"], pkg, "", "", 0)
	assert.Equal(t, []string{"internal/bus/*.go", Ledgers["sleeps-skips"].File}, pp.Cards[0].Paths)
	// a bare name (transcripts) adds no package glob
	tr, _ := ParseLedger(Ledgers["transcripts"], "nova-bus      # #1654 -- collects\n")
	tp := PlanLedger(Ledgers["transcripts"], tr, "", "", 0)
	assert.Equal(t, []string{Ledgers["transcripts"].File}, tp.Cards[0].Paths)
	assert.Contains(t, tp.Cards[0].Task, "`## nova-bus` section")
}

func TestMergePathsFoldsPastTheCeiling(t *testing.T) {
	t.Parallel()
	var many []string
	for _, n := range strings.Fields("a b c d e f g h i j") {
		many = append(many, "pkg/"+n+".go")
	}
	many = append(many, "ledger.txt", "pkg/a.go")
	got := MergePaths(many)
	assert.LessOrEqual(t, len(got), MaxPaths)
	assert.Contains(t, got, "pkg/*")
	assert.Contains(t, got, "ledger.txt")
	for _, g := range got {
		assert.NotEqual(t, "pkg/a.go", g, "a file its directory's glob covers is dropped")
	}
	assert.Equal(t, []string{"x.go", "y.go"}, MergePaths([]string{"x.go", "y.go", "x.go"}))
}

// Adjacent cards of one ledger delete adjacent lines: odd cards wave 1, even cards
// wave 2 needing their neighbours; a generated ledger has one wave and no needs.
func TestWavesAlternateOnAnOrdinaryLedgerAndNotOnAGeneratedOne(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	p := PlanLedger(l, rows, "", "", 0)
	assert.Equal(t, 2, p.Waves)
	assert.Equal(t, []int{1, 2, 1, 2}, []int{p.Cards[0].Wave, p.Cards[1].Wave, p.Cards[2].Wave, p.Cards[3].Wave})
	assert.Empty(t, p.Cards[0].Deps)
	assert.Equal(t, []string{p.Cards[0].ID, p.Cards[2].ID}, p.Cards[1].Deps)
	assert.Equal(t, []string{p.Cards[2].ID}, p.Cards[3].Deps, "the last even card has one neighbour")
	assert.True(t, p.Shared, "wave-1 cards share the ledger with no need between them: the add wants --allow-shared-paths")

	g := Ledgers["generality-fixtures"]
	grows, _ := ParseLedger(g, "a/one.md recorded\nb/two.md recorded\nc/three.md recorded\n")
	gp := PlanLedger(g, grows, "", "", 0)
	assert.Equal(t, 1, gp.Waves)
	for _, c := range gp.Cards {
		assert.Equal(t, 1, c.Wave)
		assert.Empty(t, c.Deps)
	}
	assert.Equal(t, "pro", gp.Tier)
	assert.Equal(t, "flash", PlanLedger(g, grows, "", "flash", 0).Tier, "--tier overrides the ledger's default")
}

// Every rendered brief passes the add's lint and carries no placeholder.
func TestEveryRenderedBriefPassesTheLint(t *testing.T) {
	t.Parallel()
	for _, name := range LedgerNames() {
		l := Ledgers[name]
		var text string
		switch name {
		case "sleeps-skips":
			text = "internal/bus\tTestX\t#1 calls time.Sleep\n"
		case "dead-code":
			text = "# ceiling: 3\ninternal/bounded 3\n"
		case "fixed-waits":
			text = "cmd/nova-bus/t_test.go:12 sleep 2026-09-17 a fixed sleep\n"
		case "namedpaths":
			text = "docs/templates SPEC-CI cardtemplates: skipped\n"
		case "transcripts":
			text = "nova-bus      # #1654\n"
		case "generality-fixtures":
			text = "internal/cairn/testdata/x.md a captured cairn\n"
		default:
			text = serialFixture
		}
		rows, _ := ParseLedger(l, text)
		require.NotEmpty(t, rows, name)
		p := PlanLedger(l, rows, "", "", 0)
		for _, c := range p.Cards {
			brief := Render(header, c)
			assert.Empty(t, Lint(c.ID, brief), "%s: %s\n%s", name, c.ID, brief)
			assert.True(t, strings.HasPrefix(brief, "RESULT: "+c.ID+" sha=0123456789ab tier: "+c.Tier+"\n"), brief)
			assert.Contains(t, brief, "\nTEST: "+l.Test+"\n")
			assert.Contains(t, brief, "\nKIND: fix-red\n")
		}
	}
}

// The lint refuses what the add refuses: a placeholder, a missing rule, a bad tier.
func TestTheLintNamesWhatTheAddWouldRefuse(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	c := PlanLedger(l, rows, "", "", 0).Cards[0]
	good := Render(header, c)
	bad := strings.Replace(good, "Never kill a process you did not start.", "", 1)
	bad = strings.Replace(bad, "tier: flash", "tier: cheap", 1)
	bad = strings.Replace(bad, "THE TASK.", "THE TASK. <fill me>", 1)
	var checks []string
	for _, f := range Lint("x", bad) {
		checks = append(checks, f.Check)
	}
	assert.Contains(t, checks, "rule-no-kill")
	assert.Contains(t, checks, "model-lines")
	assert.Contains(t, checks, "placeholder")
	assert.Contains(t, LintFinding{ID: "x", Check: "placeholder", Line: 3, Excerpt: "<fill me>"}.String(), "LINT DRIFT card=x check=placeholder line=3")
}

func TestFindingsAreOneCardPerFile(t *testing.T) {
	t.Parallel()
	tsv := "file\tfinding\tremedy\ttest\n" +
		"internal/bus/send.go:12\tthe receipt is not fsynced\tcall f.Sync before close\tinternal/bus TestReceiptIsFsynced\n" +
		"internal/bus/send.go:40\tthe error is swallowed\treturn it\t\n" +
		"cmd/nova-bus/main.go:9\tthe banner names a verb that is gone\tdrop the line\n" +
		"short row\n"
	fs, skipped := ParseFindings(tsv)
	require.Len(t, fs, 3)
	assert.Equal(t, []string{"findings:5: short row"}, skipped)
	p := PlanFindings(fs, "", "", 0)
	require.Len(t, p.Cards, 2)
	assert.Equal(t, "finding-internal-bus-send", p.Cards[0].ID)
	assert.Equal(t, "internal/bus TestReceiptIsFsynced", p.Cards[0].Test)
	assert.Equal(t, []string{"internal/bus/send.go", "internal/bus/*_test.go"}, p.Cards[0].Paths)
	assert.Contains(t, p.Cards[0].Task, "2 of them")
	assert.Contains(t, p.Cards[0].Task, "At internal/bus/send.go:40: the error is swallowed. Remedy: return it.")
	assert.Equal(t, "cmd/nova-bus TestFindingMain", p.Cards[1].Test, "a finding with no test is given the one it must write")
	assert.Equal(t, "pro", p.Tier)
	for _, c := range p.Cards {
		assert.Empty(t, Lint(c.ID, Render(header, c)))
	}
	assert.Contains(t, Manifest(p), "finding-internal-bus-send\tinternal/bus/send.go\tinternal/bus TestReceiptIsFsynced\t1\t-\n")
}

func TestHelpCardListsTheLongLines(t *testing.T) {
	t.Parallel()
	help := "nova-x: a tool\n\n" + strings.Repeat("x", 120) + "\nshort\n"
	c := PlanHelp("nova-x", help, "", "", "")
	assert.Equal(t, "help-nova-x", c.ID)
	assert.Contains(t, c.Task, "1 line(s) run over 100 characters (help line 3 (120 chars))")
	assert.Equal(t, "cmd/nova-x TestHelpExampleLinesRunAsPrinted", c.Test)
	assert.Empty(t, Lint(c.ID, Render(header, c)))
	none := PlanHelp("nova-y", "short\n", "", "", "flash")
	assert.Contains(t, none.Task, "no line runs over 100 characters")
	assert.Equal(t, "flash", none.Tier)
}

func TestTheOKLineAndTheDeadline(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "CARDS OK dir=out cards=0 waves=2 tier=flash", OKLine("out", Plan{Waves: 2, Tier: "flash"}))
	assert.Equal(t, 45, Deadline("flash"))
	assert.Equal(t, 60, Deadline("pro"))
	c := Card{ID: "a", File: "x/y.go", Paths: []string{"x/y.go"}, Test: "x TestA", Tier: "pro", Kind: "fix-red", Task: "Do it."}
	assert.Contains(t, Render(Header{Repo: "o/r", Base: "dev", Sha: "abc", Minutes: 7}, c), "Deadline: finish within 7 minutes.")
	c.New = []string{"x/z_test.go"}
	assert.Contains(t, Render(Header{Repo: "o/r", Base: "dev", Sha: "abc"}, c), "\nNEW: x/z_test.go\n")
}

// --max cuts before the waves are assigned: the kept cards' needs name kept
// cards only, so the directory is admitted (the add refuses a need that is no card
// of the add), and the waves and the shared flag are those of the cut plan.
func TestMaxCutsBeforeTheWavesAreAssigned(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	p := PlanLedger(l, rows, "", "", 2)
	require.Len(t, p.Cards, 2)
	assert.Equal(t, []string{p.Cards[0].ID}, p.Cards[1].Deps, "the cut neighbour is not a need")
	assert.Equal(t, 2, p.Waves)
	assert.False(t, p.Shared, "one wave-1 card shares the ledger with nobody")
	assert.Len(t, PlanLedger(l, rows, "", "", 0).Cards, 4, "0 is all")
	assert.Len(t, PlanLedger(l, rows, "", "", 9).Cards, 4, "a max past the plan keeps every card")
	fs, _ := ParseFindings("a/x.go:1\twrong\tfix\ta TestA\nb/y.go:1\twrong\tfix\tb TestB\n")
	assert.Len(t, PlanFindings(fs, "", "", 1).Cards, 1)
}

// A tool named twice plans one id twice; the generator refuses rather than
// writing one file for two manifest rows.
func TestADuplicateIDIsNamed(t *testing.T) {
	t.Parallel()
	a := PlanHelp("nova-x", "x\n", "", "", "")
	b := PlanHelp("nova-x", "x\n", "", "", "")
	assert.Equal(t, "help-nova-x", DuplicateID([]Card{a, b}))
	assert.Equal(t, "", DuplicateID([]Card{a, PlanHelp("nova-y", "y\n", "", "", "")}))
}

// A help card's TEST is the test of the tool's package that runs the help's
// examples, read off the package's test files: the one by the conventional name
// when it exists, else the test that calls onboarding.ExampleLines, else the card
// writes the conventional one and says so.
func TestAHelpCardNamesTheTestItsPackageHas(t *testing.T) {
	t.Parallel()
	byName := "package main\n\nfunc TestOther(t *testing.T) {}\n\nfunc TestHelpExampleLinesRunAsPrinted(t *testing.T) {\n\tonboarding.ExampleLines(banner, \"nova-x\")\n}\n"
	byCall := "package main\n\nfunc TestUsageBannerExamplesRun(t *testing.T) {\n\texamples, err := onboarding.ExampleLines(banner, \"nova-x\")\n}\n"
	assert.Equal(t, "TestHelpExampleLinesRunAsPrinted", ExampleTest(byCall, byName), "the conventional name wins over the first caller")
	assert.Equal(t, "TestUsageBannerExamplesRun", ExampleTest(byCall))
	assert.Equal(t, "", ExampleTest("package main\n\nfunc TestNothing(t *testing.T) {}\n"))
	assert.Equal(t, "", ExampleTest())

	found := PlanHelp("nova-x", "x\n", "TestUsageBannerExamplesRun", "", "")
	assert.Equal(t, "cmd/nova-x TestUsageBannerExamplesRun", found.Test)
	assert.Contains(t, found.Task, "the test TestUsageBannerExamplesRun in the same package holds the examples to the binary")
	assert.NotContains(t, found.Task, "no test in cmd/nova-x")
	none := PlanHelp("nova-x", "x\n", "", "", "")
	assert.Equal(t, "cmd/nova-x "+HelpExampleTest, none.Test)
	assert.Contains(t, none.Task, "no test in cmd/nova-x runs the help's example lines through onboarding.ExampleLines: write TestHelpExampleLinesRunAsPrinted first")
	assert.Contains(t, none.Task, "the card's gate is go test ./cmd/nova-x/")
	for _, c := range []Card{found, none} {
		assert.Empty(t, Lint(c.ID, Render(header, c)))
	}
}

// A findings card on a package with no test file keeps its test glob in PATHS (the
// land holds the diff to PATHS) and names the test file it creates on NEW:, so the
// glob that matches nothing at the base is the card's to answer, not a red line.
func TestAPackageWithNoTestFileGetsItsTestOnTheNEWLine(t *testing.T) {
	t.Parallel()
	fs, _ := ParseFindings("internal/none/x.go:1\twrong\tfix\t\n")
	c := PlanFindings(fs, "", "", 0).Cards[0]
	NewTestFile(&c, func(glob string) bool { return glob == "internal/none/x.go" })
	assert.Equal(t, []string{"internal/none/x.go", "internal/none/*_test.go"}, c.Paths, "the glob stays")
	assert.Equal(t, []string{"internal/none/x_test.go"}, c.New)
	assert.True(t, c.Creates("internal/none/*_test.go"))
	assert.False(t, c.Creates("internal/none/x.go"))
	brief := Render(header, c)
	assert.Contains(t, brief, "\nPATHS: internal/none/x.go, internal/none/*_test.go\nNEW: internal/none/x_test.go\nTEST: internal/none TestFindingX\n")
	assert.Empty(t, Lint(c.ID, brief))
	has := c
	has.New = nil
	NewTestFile(&has, func(string) bool { return true })
	assert.Empty(t, has.New, "a package with tests creates none")
}

// The gate step of every card the generators write, and of the card template, tells the
// child what to do when the gate is red: name the failing test's file, say whether it is
// changed by the child's work (yours) or is unchanged (already red at BASE), and report that line first. The card
// stays under the lint's advisory size and still passes the lint.
func TestTheGateStepNamesWhoseFileFailed(t *testing.T) {
	t.Parallel()
	const sentence = "When a test fails, name its file and say whether that file was changed by your work (yours) or is unchanged (already red at BASE: run the same test on the unchanged base to say so), and report that line first."
	gateStep := func(brief string) string {
		for _, line := range strings.Split(brief, "\n") {
			if strings.HasPrefix(line, "STEP 4.") {
				return line
			}
		}
		return ""
	}

	rows, _ := ParseLedger(Ledgers["serial-tests"], serialFixture)
	require.NotEmpty(t, rows)
	fs, _ := ParseFindings("file\tfinding\tremedy\ttest\ninternal/bus/send.go:12\tthe receipt is not fsynced\tcall f.Sync before close\tinternal/bus TestReceiptIsFsynced\n")
	require.Len(t, fs, 1)
	cards := append(PlanLedger(Ledgers["serial-tests"], rows, "", "", 0).Cards, PlanFindings(fs, "", "", 0).Cards...)
	cards = append(cards, PlanHelp("nova-x", "x\n", "", "", ""))
	for _, c := range cards {
		brief := Render(header, c)
		assert.Contains(t, gateStep(brief), sentence, c.ID)
		assert.Empty(t, Lint(c.ID, brief), c.ID)
		assert.Less(t, len(brief), cardlimits.BriefAdvisoryBytes, c.ID)
	}

	tmpl, err := swarm.Template("card")
	require.NoError(t, err)
	assert.Contains(t, gateStep(tmpl), sentence, "the card template")
	assert.Less(t, len(tmpl), cardlimits.BriefAdvisoryBytes)
}
