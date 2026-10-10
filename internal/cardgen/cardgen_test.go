package cardgen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardlimits"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
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
	assert.Equal(t, swarm.LedgerKind, p.Cards[0].Kind)
}

// A ledger card is KIND: ledger and its STOP is the ledger shrinking with the class test
// green, since that test is green at the base by construction: the rows for its file, or
// a counted row's count, shrink to 0. Any other card's STOP is its test red then green.
func TestALedgerCardStopsWhenItsLedgerEntryShrinks(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	p := PlanLedger(l, rows, "", "", 0)
	two, one := Render(header, p.Cards[0]), Render(header, p.Cards[1])
	assert.Contains(t, two, "\nKIND: ledger\n")
	assert.Contains(t, two, "\nSTOP: the ledger rows for cmd/nova-bus/a_test.go in internal/ci/testdata/serial-tests_allowlist.txt shrink from 2 to 0 and the class test TestEveryTestOpensWithTParallel stays green, and the STEP 4 gate passes\n")
	assert.Contains(t, one, "\nSTOP: the ledger row for cmd/nova-bus/b_test.go in internal/ci/testdata/serial-tests_allowlist.txt shrinks from 1 to 0 and the class test TestEveryTestOpensWithTParallel stays green, and the STEP 4 gate passes\n")

	d := Ledgers["dead-code"]
	drows, _ := ParseLedger(d, "# ceiling: 3\ninternal/bounded 3\n")
	dc := PlanLedger(d, drows, "", "", 0).Cards[0]
	assert.Contains(t, Render(header, dc), "\nSTOP: the count on the ledger row for internal/bounded in internal/ci/testdata/dead_code_allowlist.txt shrinks from 3 to 0 and the class test TestDeadCode stays green, and the STEP 4 gate passes\n")

	other := Render(header, Card{ID: "a", File: "x/y.go", Paths: []string{"x/y.go"}, Test: "x TestA", Tier: "pro", Kind: "fix-red", Task: "Do it."})
	assert.Contains(t, other, "\nSTOP: the test TestA is red before the change and green after it, and the STEP 4 gate passes\n")
	assert.Contains(t, other, "\nKIND: fix-red\n")
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

// The lander unions every generated ledger conflict, so adjacent deletions of one
// file no longer conflict at land: every ledger plans one wave, every card carries
// DEPENDS-ON: -, and the cards share the ledger's path with no need between them,
// so the CARDS line says shared-paths=yes and the add wants --allow-shared-paths.
func TestALedgerPlanIsOneWaveWithSharedPathsAndNoDependencyChain(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, "# ceiling: 5\n"+
		"cmd/nova-bus/a_test.go:TestOne serial: t.Setenv\n"+
		"cmd/nova-bus/b_test.go:TestTwo serial: t.Chdir\n"+
		"internal/swarm/c_test.go:TestThree serial: os.Setenv\n"+
		"internal/swarm/d_test.go:TestFour serial: t.Setenv\n"+
		"internal/bus/e_test.go:TestFive serial: os.Setenv\n")
	require.Len(t, rows, 5, "the plan under test covers a ledger of five rows")
	p := PlanLedger(l, rows, "", "", 0)
	assert.Equal(t, 1, p.Waves, "a ledger plan is one wave")
	assert.True(t, p.Shared, "the cards share the ledger's path with no need between them: the add wants --allow-shared-paths")
	for _, c := range p.Cards {
		assert.Equal(t, 1, c.Wave, c.ID)
		assert.Empty(t, c.Deps, c.ID)
		assert.Contains(t, Render(header, c), "\nDEPENDS-ON: -\n", c.ID)
	}

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
			assert.Contains(t, brief, "\nKIND: ledger\n")
			_, read, ok := strings.Cut(brief, "\nAS A READ\n")
			assert.True(t, ok, "the brief has an AS A READ section")
			assert.Contains(t, "\n"+read, "\nThe scope of this change is its PATHS line. "+AlwaysInPathsRule+"\n", "the reader is handed the scope rule")
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
	assert.Contains(t, Render(Header{Repo: "o/r", Base: "dev", Sha: "abc", Minutes: 7}, c), "Deadline: finish within 7 minutes; the judgment of a card that runs past it is the coordinator's, so report what you have with the verdict not-done rather than push past it.")
	c.New = []string{"x/z_test.go"}
	assert.Contains(t, Render(Header{Repo: "o/r", Base: "dev", Sha: "abc"}, c), "\nNEW: x/z_test.go\n")
}

// --max cuts before the plan is rendered: the kept cards are the whole plan, so no
// card needs a cut one (the add refuses a need that is no card of the add), the
// plan is one wave, and the kept cards share the ledger with no need between them.
func TestMaxCutsBeforeThePlanIsRendered(t *testing.T) {
	t.Parallel()
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	p := PlanLedger(l, rows, "", "", 2)
	require.Len(t, p.Cards, 2)
	assert.Empty(t, p.Cards[1].Deps, "no card needs another, cut or kept")
	assert.Equal(t, 1, p.Waves)
	assert.True(t, p.Shared, "the two kept cards share the ledger with no need between them")
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

// A card whose PATHS reach tla/ runs the model in its own gate: STEP 4 names make tlc for
// the groups of the cases it touched and the merge into tla/RUNS.tsv, the PATHS line
// carries tla/RUNS.tsv so the record can be committed, and the brief still passes the
// lint; a card that touches no model has neither.
func TestACardThatTouchesAModelRunsItInItsGate(t *testing.T) {
	t.Parallel()
	gateStep := func(brief string) string {
		for _, line := range strings.Split(brief, "\n") {
			if strings.HasPrefix(line, "STEP 4.") {
				return line
			}
		}
		return ""
	}
	for _, paths := range [][]string{{"tla/Land.tla", "tla/MCLand.cfg"}, {"tla/*", "internal/sprint/land*.go"}, {"tla/**"}} {
		c := Card{ID: "model-land", File: paths[0], Paths: paths, Test: "internal/tlc TestTLCRecordsCoverCurrentModels", Tier: "pro", Wave: 1, Kind: "fix-red", Task: "Fix the model."}
		brief := Render(header, c)
		step := gateStep(brief)
		assert.Contains(t, step, "make tlc TLC_JAR=/opt/tla/tla2tools.jar TLC_OUT=$JOB/scratch/tlc-$g TLC_GROUP=$g", paths)
		assert.Contains(t, step, "go run ./tools/tlacheck groups --root . --stale", paths)
		assert.Contains(t, step, "go run ./tools/tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv", paths)
		assert.Contains(t, step, swarm.GateNamesWhoseFile, paths)
		var line []string
		for _, l := range strings.Split(brief, "\n") {
			if rest, ok := strings.CutPrefix(l, "PATHS: "); ok {
				line = strings.Split(rest, ", ")
			}
		}
		assert.True(t, slices.ContainsFunc(line, func(g string) bool { return hygiene.MatchGlob(g, "tla/RUNS.tsv") }), "PATHS covers the records: %v", line)
		assert.LessOrEqual(t, strings.Count(strings.Join(line, " ")+" ", "tla/RUNS.tsv "), 1, "named at most once: %v", line)
		assert.Empty(t, Lint(c.ID, brief), paths)
		assert.Less(t, len(brief), cardlimits.BriefAdvisoryBytes, paths)
		assert.Equal(t, paths, c.Paths, "the card's own PATHS are not changed")
	}
	plain := Render(header, Card{ID: "go-only", File: "internal/x/x.go", Paths: []string{"internal/x/x.go", "docs/tla.md"}, Test: "internal/x TestX", Tier: "pro", Wave: 1, Kind: "fix-red", Task: "Fix x."})
	assert.NotContains(t, plain, "make tlc")
	assert.NotContains(t, strings.ReplaceAll(plain, AlwaysInPathsRule, ""), "tla/RUNS.tsv", "only the rule names the ledger")
}

// Every place the repository writes a card's GOCACHE sentence says the one thing JOB.md
// says (docs/SPEC-CARD-CONTRACT.md section 2, the staged environment): the machine's
// shared, warm build cache is already set and the child keeps it. Each card the
// generators write and the card template carry swarm.GoCacheLine, once, with no
// instruction to create or choose a cache of its own; the `gocache` rule the member
// injects from fleet/child-rules.txt quotes that same sentence, and the step-go-clean
// remedy says to keep the shared cache. A friend's card (WHO: friend) is the exception:
// it names the friend's own cache and says so (swarm.FriendGoCacheLine, written into
// the working-directory line by cmd/nova-sprint's friendBrief), so the `GOCACHE=`
// refusal holds only the child cards. cardgen.Render, the card template, the rules
// file and the remedy reach the sentence through swarm.GoCacheLine, so none can drift.
func TestAGeneratedCardNamesNoStaleGoCacheLine(t *testing.T) {
	t.Parallel()
	createOrChoose := []string{"private GOCACHE", "Export GOCACHE", "own GOCACHE", "choose a GOCACHE", "GOCACHE path"}
	cards := map[string]string{}

	rows, _ := ParseLedger(Ledgers["serial-tests"], serialFixture)
	require.NotEmpty(t, rows)
	for _, c := range PlanLedger(Ledgers["serial-tests"], rows, "", "", 0).Cards {
		cards[c.ID] = Render(header, c)
	}

	fs, _ := ParseFindings("file\tfinding\tremedy\ttest\ninternal/bus/send.go:12\tthe receipt is not fsynced\tcall f.Sync before close\tinternal/bus TestReceiptIsFsynced\n")
	require.NotEmpty(t, fs)
	for _, c := range PlanFindings(fs, "", "", 0).Cards {
		cards[c.ID] = Render(header, c)
	}
	cards["help-nova-x"] = Render(header, PlanHelp("nova-x", "x\n", "", "", ""))

	tmpl, err := swarm.Template("card")
	require.NoError(t, err)
	cards["card-template"] = tmpl

	rules, err := swarm.HeldRules(swarm.DefaultRulesName)
	require.NoError(t, err)
	var gocache swarm.ChildRule
	for _, r := range rules {
		if r.Name == "gocache" {
			gocache = r
		}
	}
	require.NotEmpty(t, gocache.Name, "fleet/child-rules.txt carries no [gocache] rule")
	cards["rule-gocache"] = gocache.Sentence
	cards["remedy-step-go-clean"] = swarm.ChildRemedy(rules, "step-go-clean")

	for name, card := range cards {
		assert.Contains(t, card, swarm.GoCacheLine, name)
		assert.Equal(t, 1, strings.Count(card, swarm.GoCacheLine), "%s: the one sentence, once", name)
		assert.NotContains(t, card, "GOCACHE=", "%s: a child card assigns no GOCACHE of its own", name)
		for _, s := range createOrChoose {
			assert.NotContains(t, card, s, "%s: no instruction to create or choose a GOCACHE", name)
		}
	}

	// A friend's card (WHO: friend) names the friend's own cache and says so: her
	// working directory holds her own build cache, warm across her cards. `GOCACHE=`
	// is legitimate there, which is why the refusal above holds only the child cards.
	friend := swarm.FriendGoCacheLine("amy")
	assert.Contains(t, friend, "GOCACHE=~/amy-working/.cache/go-build", "a friend card names the friend's own cache")
	assert.Contains(t, friend, "your own", "and says so")
	for _, s := range createOrChoose {
		assert.NotContains(t, friend, s, "a friend card gives no instruction to create or choose a cache")
	}
	// friendBrief (cmd/nova-sprint/friendcards.go) writes the friend's own cache line
	// through the one helper, so a friend card cannot carry a stale cache path.
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "nova-sprint", "friendcards.go"))
	require.NoError(t, err)
	writes := string(src)
	assert.True(t, strings.Contains(writes, "swarm.FriendGoCacheLine("), "friendBrief names the friend's own cache through the one line")
	assert.False(t, strings.Contains(writes, "GOCACHE=~/"), "the friend's cache path lives in the one line, never hand-written in friendBrief")
}

// docs/FRIENDS.md: all generated briefs teach the friend report's pinned shape.
func TestGeneratedBriefPinsTheFriendReportFirstTwoLines(t *testing.T) {
	t.Parallel()
	brief := Render(header, Card{ID: "shape", File: "internal/x/x.go", Paths: []string{"internal/x/x.go"}, Test: "internal/x TestX", Tier: "pro", Kind: "fix-red", Task: "Fix x."})
	assert.Contains(t, brief, "first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex>")
	assert.Contains(t, brief, "for HOLD and FAIL omit Head: and leave line 2 blank")
}

func TestTheDeadlineLineSaysTheJudgmentIsTheCoordinators(t *testing.T) {
	t.Parallel()
	const deadlineTail = "; the judgment of a card that runs past it is the coordinator's, so report what you have with the verdict not-done rather than push past it."

	// 1. Template card
	tmpl, err := swarm.Template("card")
	require.NoError(t, err)
	assert.Contains(t, tmpl, "Deadline: finish within <n> minutes"+deadlineTail)
	assert.Empty(t, swarm.LintCardChildWith([]byte(tmpl), swarm.DefaultChildRules))

	// 2. Ledger generator
	l := Ledgers["serial-tests"]
	rows, _ := ParseLedger(l, serialFixture)
	lp := PlanLedger(l, rows, "", "", 1)
	require.NotEmpty(t, lp.Cards)
	ledgerBrief := Render(header, lp.Cards[0])
	assert.Contains(t, ledgerBrief, "Deadline: finish within 45 minutes"+deadlineTail)
	assert.Empty(t, Lint(lp.Cards[0].ID, ledgerBrief))

	// 3. Findings generator
	findings, _ := ParseFindings("internal/bus/send.go:12\tthe receipt is not fsynced\tcall f.Sync before close\tinternal/bus TestReceiptIsFsynced\n")
	fp := PlanFindings(findings, "", "", 1)
	require.NotEmpty(t, fp.Cards)
	findingsBrief := Render(header, fp.Cards[0])
	assert.Contains(t, findingsBrief, "Deadline: finish within 60 minutes"+deadlineTail)
	assert.Empty(t, Lint(fp.Cards[0].ID, findingsBrief))

	// 4. Help generator
	hc := PlanHelp("nova-x", "help text\n", "", "", "")
	helpBrief := Render(header, hc)
	assert.Contains(t, helpBrief, "Deadline: finish within 60 minutes"+deadlineTail)
	assert.Empty(t, Lint(hc.ID, helpBrief))

	// Custom deadline minutes
	customBrief := Render(Header{Repo: "example/repo", Base: "dev", Sha: "0123456789abcdef0123456789abcdef01234567", Minutes: 20}, hc)
	assert.Contains(t, customBrief, "Deadline: finish within 20 minutes"+deadlineTail)
	assert.Empty(t, Lint(hc.ID, customBrief))
}

// The fix-red card stamped for a red class names the class, the base and the files, and
// its brief passes the add's lint whether the class has a test or is a bare run (then the
// card writes the class test).
func TestAClassRedCardPassesTheLint(t *testing.T) {
	t.Parallel()
	for _, r := range []ClassRed{
		{Class: "staticcheck", Run: "go test -tags functional ./internal/ci/", Test: "internal/ci TestStaticcheckFindings",
			Files: []string{"cmd/nova-swarm/x_test.go"}, Finding: "exit status 1: cmd/nova-swarm/x_test.go:12:6: func helper is unused (U1000)"},
		{Class: "gofmt", Run: "gofmt -l .", Files: []string{"cmd/nova-secrets/main.go"}, Finding: "it printed: cmd/nova-secrets/main.go"},
		{Class: "class-tests", Run: "go test ./internal/ci/ ./internal/docs/", Test: "internal/docs TestNovaToolsIsEveryCommand", Finding: "exit status 1: --- FAIL: TestNovaToolsIsEveryCommand"},
	} {
		c := PlanClassRed(r, "sprint/mechanical-2026-10-02", "")
		assert.Equal(t, "fix-red-"+Slug(r.Class)+"-sprint-mechanical-2026-10-02", c.ID)
		assert.Equal(t, "fix-red", c.Kind)
		assert.Contains(t, c.Task, "red on its class "+r.Class)
		for _, f := range r.Files {
			assert.Contains(t, c.Paths, f)
		}
		if r.Test == "" {
			assert.Equal(t, "internal/ci "+ClassTestName(r.Class), c.Test)
			assert.Contains(t, c.Task, "write "+ClassTestName(r.Class)+" in internal/ci first")
		}
		brief := Render(header, c)
		assert.Empty(t, Lint(c.ID, brief), "%s\n%s", r.Class, brief)
	}
}

// A generated brief does not ask the worker to set TMPDIR or GOTMPDIR by hand: the
// runner sets both outside the source checkout (docs/SPEC-SPRINT.md section 18).
func TestAGeneratedBriefDoesNotAskTheWorkerToSetTmp(t *testing.T) {
	t.Parallel()
	brief := Render(header, Card{ID: "tmp", File: "internal/x/x.go", Paths: []string{"internal/x/x.go"}, Test: "internal/x TestX", Tier: "pro", Kind: "fix-red", Task: "Fix x."})
	assert.Contains(t, brief, "TMPDIR and GOTMPDIR are already set by the bench runner outside the source checkout")
	assert.Contains(t, brief, "Set neither by hand")
	assert.NotContains(t, brief, "set TMPDIR and GOTMPDIR inside the job directory")
}
