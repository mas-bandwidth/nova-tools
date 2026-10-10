package card

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

var header = cardgen.Header{Repo: "example/repo", Base: "dev", Sha: "0123456789abcdef0123456789abcdef01234567"}

// A generated card's PATHS are computed from the START line its brief carries, never
// typed: every directory a START file lives in, as its Go files and its tests, and the
// docs the planner named. A planner's narrow list (one file and the test glob) comes
// out as the packages, and every brief so computed passes the card checks as written:
// its TEST package is in its PATHS and line 1 names its tier.
func TestGeneratedPathsAreThePackagesOfStart(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"pkg/bus/*.go", "pkg/bus/*_test.go", "docs/X.md"},
		PackagePaths([]string{"pkg/bus/send.go", "./pkg/bus/recv.go", "pkg/bus"}, []string{"docs/X.md"}),
		"files of one package and the package itself are one pair of globs")
	assert.Equal(t, []string{"internal/ci/testdata/x.txt"}, PackagePaths([]string{"internal/ci/testdata/x.txt", "nova-bus"}, nil),
		"a non-Go file is itself and a bare word names no path")
	assert.Equal(t, []string{"docs/CLI.md", "internal/ci/testdata/l.txt"},
		Docs([]string{"cmd/x/main.go", "cmd/x/*_test.go", "docs/CLI.md", "internal/y", "internal/ci/testdata/l.txt"}))

	fs, _ := cardgen.ParseFindings("pkg/bus/send.go:12\tlost\tkeep it\tinternal/ci TestX\nfleet/roles/a.yml:3\twrong\tfix it\tinternal/fleet TestRole\n")
	fp := cardgen.PlanFindings(fs, "", "", 0)
	require.Len(t, fp.Cards, 2)
	help := cardgen.PlanHelp("nova-x", "x\n", "TestExamples", "", "")
	want := [][]string{
		{"pkg/bus/*.go", "pkg/bus/*_test.go", "internal/ci/*.go", "internal/ci/*_test.go"},
		{"fleet/roles/a.yml", "pkg/fleet/*.go", "pkg/fleet/*_test.go"},
		{"cmd/nova-x/*.go", "cmd/nova-x/*_test.go", "docs/CLI.md"},
	}
	for i, c := range append(fp.Cards, help) {
		assert.Equal(t, want[i], Paths(header, c), c.ID)
		c.Paths = Paths(header, c)
		brief := cardgen.Render(header, c)
		assert.Contains(t, brief, "\nPATHS: "+strings.Join(c.Paths, ", ")+"\n", c.ID)
		assert.Empty(t, Lint(c.ID, brief, Options{}), c.ID)
		for _, s := range Start(brief) {
			if strings.Contains(s, "/") && !strings.Contains(s, ".") {
				assert.Contains(t, c.Paths, s+"/*.go", "%s: START %s is a package of PATHS", c.ID, s)
			}
		}
	}
}

// Two cards that share a package glob and neither needs the other are said, so the
// add is given --allow-shared-paths; a card that needs the other is not.
func TestCardsSharingAPackageAreShared(t *testing.T) {
	t.Parallel()
	a := cardgen.Card{ID: "a", Paths: []string{"p/*.go"}}
	b := cardgen.Card{ID: "b", Paths: []string{"p/*.go"}, Deps: []string{"a"}}
	assert.False(t, Shared([]cardgen.Card{a, b}))
	b.Deps = nil
	assert.True(t, Shared([]cardgen.Card{a, b}))
	b.Paths = []string{"q/*.go"}
	assert.False(t, Shared([]cardgen.Card{a, b}))
}

// A package's Go files are not answered by a test file the card creates; the test
// glob is.
func TestATestFileTheCardCreatesIsNoPackage(t *testing.T) {
	t.Parallel()
	c := cardgen.Card{New: []string{"internal/x/y_test.go"}}
	assert.False(t, Answered(c, "internal/x/*.go"))
	assert.True(t, Answered(c, "internal/x/*_test.go"))
}

// The card checks refuse a brief with the finding: no tier on line 1, a TEST outside
// PATHS, a name outside the owner's quoted words, a dropped card named; a clean brief,
// the owner quoted by name, and the card's own id that carries a name all pass.
func TestChecksRefuseWithTheFinding(t *testing.T) {
	t.Parallel()
	opts := Options{Names: []string{"Ada", "bench7"}, Dropped: []string{"old-card.w1"}}
	clean := "RESULT: rate-ada-x.w2 sha=abc tier: pro\nREPO: o/r\nPATHS: internal/x/*.go, internal/x/*_test.go\nTEST: ./internal/x TestY\n\nTHE TASK. The owner said \"Ada wants this\" and the card does it.\n"
	assert.Empty(t, Checks("rate-ada-x.w2", clean, opts))

	checks := func(id, brief string) []string {
		var out []string
		for _, f := range Checks(id, brief, opts) {
			out = append(out, f.Check)
		}
		return out
	}
	assert.Equal(t, []string{"tier-line"}, checks("rate-ada-x.w2", strings.Replace(clean, " tier: pro", "", 1)))
	assert.Equal(t, []string{"test-outside-paths"}, checks("rate-ada-x.w2", strings.Replace(clean, "TEST: ./internal/x TestY", "TEST: internal/other TestY", 1)))
	assert.Equal(t, []string{"personal-name"}, checks("rate-ada-x.w2", clean+"Run it on bench7.\n"))
	assert.Equal(t, []string{"personal-name"}, checks("c", clean), "outside its own id the name is a name")
	assert.Equal(t, []string{"dropped-card"}, checks("rate-ada-x.w2", clean+"Carry the work of old-card.w1.\n"))
	assert.Empty(t, checks("old-card.w1", strings.Replace(clean, "rate-ada-x.w2", "old-card.w1", 1)), "a card may name itself")
	assert.Empty(t, checks("rate-ada-x.w2", clean+"Adapt the bench7x shim.\n"), "a name inside another word is no name")
	assert.Empty(t, checks("rate-ada-x.w2", clean+"WHO: ada\n"), "the WHO: line pins a friend by name")
	assert.Empty(t, Checks("c", "Do the thing.\n", opts), "a brief with no PATHS line is a free task: no tier or TEST check")

	f := Checks("c", "RESULT: c tier: pro\nPATHS: a/*.go\nTEST: ./b TestZ\n", Options{})
	require.Len(t, f, 1)
	assert.Equal(t, "LINT DRIFT card=c check=test-outside-paths line=3: the TEST package b is no directory PATHS names (a/*.go); a card lands with a test it may edit", f[0].String())
}

func TestTestPackage(t *testing.T) {
	t.Parallel()
	for brief, want := range map[string]string{
		"TEST: internal/x TestY\n":                          "internal/x",
		"TEST: ./internal/x/ TestY\n":                       "internal/x",
		"TEST: go test ./internal/x/... -run TestY\n":       "internal/x",
		"TEST: none (a read card)\n":                        "",
		"PATHS: a/*.go\n":                                   "",
		"TEST: cmd/nova-x TestHelpExampleLinesRunAsPrinted": "cmd/nova-x",
	} {
		assert.Equal(t, want, TestPackage(brief), brief)
	}
}

// No generated brief names a friend as the author: it carries the ATTRIBUTION line
// (By: your own name, the worker who does this attempt) and an AS A READ section that judges
// a By: trailer only for being present and true, and passes the checks with friends
// configured. A brief that writes By: and a configured friend name, in any of the
// shapes a generator stamped it, is refused with author-name; a Co-Authored-By trailer,
// a name that is no friend and the WHO: preference line are no author.
func TestABriefNamesNoFriendAsAuthor(t *testing.T) {
	t.Parallel()
	opts := Options{Names: []string{"Ada", "bench7"}}
	c := cardgen.PlanHelp("nova-x", "x\n", "TestExamples", "", "")
	c.Paths = Paths(header, c)
	brief := cardgen.Render(header, c)
	assert.Contains(t, brief, "\nATTRIBUTION: By: your own name, the worker who does this attempt")
	assert.Contains(t, brief, "\nAS A READ\nA By: trailer is judged only for being present and true: it names the worker who pushed")
	assert.Empty(t, Lint(c.ID, brief, opts))
	assert.Empty(t, Lint(c.ID, brief+"WHO: friend ada\n", opts), "WHO stays a preference line")

	authors := func(b string) []string {
		var out []string
		for _, f := range Checks(c.ID, b, opts) {
			if f.Check == "author-name" {
				out = append(out, f.String())
			}
		}
		return out
	}
	for _, line := range []string{
		"By: Ada",
		"ATTRIBUTION: By: ada",
		"STEP 5. Commit on your own branch; the trailer is By: ada.",
		"Every commit body carries `By: ada` above the trailer.",
		"The owner said \"sign it By: <ada>\".",
		"By: friend.ada",
	} {
		got := authors(brief + line + "\n")
		require.Len(t, got, 1, line)
		assert.Contains(t, got[0], "check=author-name", line)
		assert.Contains(t, got[0], "names ada as the author", line)
	}
	for _, line := range []string{
		"Co-Authored-By: Ada <ada@example.com>",
		"By: your own name",
		"By: bob",
		"Standby: ada",
	} {
		assert.Empty(t, authors(brief+line+"\n"), line)
	}
}

// The brief a worker is handed for a card pinned to a friend (the generated brief, its
// WHO pin, and the held rules the member appends at stage time) signs the worker's own
// name and nothing else: the deal may hand a pinned card to any worker, so the brief
// carries no By: of the pinned friend, no fill-in Claude trailer a worker of another
// model would complete with its own model's name, and says that a model name is never a
// By:, that a Claude worker adds its true Co-Authored-By trailer and that any other
// worker adds none (cardgen.Attribution, fleet/child-rules.txt commit-trailer).
func TestAStagedBriefSignsTheWorkersOwnName(t *testing.T) {
	t.Parallel()
	opts := Options{Names: []string{"Ada", "bench7"}}
	h := header
	h.Repo = "mas-bandwidth/" + swarm.HomeRepo
	c := cardgen.PlanHelp("nova-x", "x\n", "TestExamples", "", "")
	c.Paths = Paths(h, c)
	rules, err := swarm.HeldRules(swarm.OwnRulesName(cardgen.Render(h, c), ""))
	require.NoError(t, err)
	staged := swarm.StagedBrief(cardgen.Render(h, c)+"WHO: friend ada\n", rules)

	assert.Contains(t, staged, "\nWHO: friend ada\n", "the pin is the deal's and stays")
	assert.Empty(t, Lint(c.ID, staged, opts), "no line names the pinned friend as the author")
	assert.NotContains(t, staged, "Claude <", "no fill-in Claude trailer: a worker of another model completes it with its own")
	for _, want := range []string{
		"By: your own name, the worker who does this attempt",
		"a model name is never a By:",
		"a Claude worker adds its true Co-Authored-By trailer",
		"any other worker adds no Co-Authored-By",
	} {
		assert.Contains(t, staged, want)
	}
	assert.NotContains(t, staged, "the friend doing this work", "a fleet machine is dealt pinned cards too")
}
