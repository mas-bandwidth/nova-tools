package cardgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generated card's PATHS are computed from what it starts from, never typed: every
// directory a START file lives in, as its Go files and its tests, and the docs it
// names; a test package the card lands with is a START directory too. Every brief the
// three planners write passes the card checks as written: its TEST package is in its
// PATHS and line 1 names its tier.
func TestGeneratedPathsAreThePackagesOfStart(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"internal/bus/*.go", "internal/bus/*_test.go", "docs/X.md"},
		PackagePaths([]string{"internal/bus/send.go", "./internal/bus/recv.go", "internal/bus"}, []string{"docs/X.md"}),
		"files of one package and the package itself are one pair of globs")
	assert.Equal(t, []string{"internal/ci/testdata/x.txt"}, PackagePaths([]string{"internal/ci/testdata/x.txt", "nova-bus"}, nil),
		"a non-Go file is itself and a bare word names no path")

	fs, _ := ParseFindings("internal/bus/send.go:12\tlost\tkeep it\tinternal/ci TestX\nfleet/roles/a.yml:3\twrong\tfix it\tinternal/fleet TestRole\n")
	fp := PlanFindings(fs, "", "", 0)
	require.Len(t, fp.Cards, 2)
	assert.Equal(t, []string{"internal/bus/*.go", "internal/bus/*_test.go", "internal/ci/*.go", "internal/ci/*_test.go"}, fp.Cards[0].Paths,
		"the file's package and the test's package")
	assert.Equal(t, []string{"fleet/roles/a.yml", "internal/fleet/*.go", "internal/fleet/*_test.go"}, fp.Cards[1].Paths)

	rows, _ := ParseLedger(Ledgers["serial-tests"], serialFixture)
	lp := PlanLedger(Ledgers["serial-tests"], rows, "", "", 0)
	assert.Equal(t, []string{"internal/swarm/*.go", "internal/swarm/*_test.go", Ledgers["serial-tests"].File}, lp.Cards[2].Paths)

	help := PlanHelp("nova-x", "x\n", "", "", "")
	assert.Equal(t, []string{"cmd/nova-x/*.go", "cmd/nova-x/*_test.go", "docs/CLI.md"}, help.Paths)

	for _, c := range append(append(fp.Cards, lp.Cards...), help) {
		brief := Render(header, c)
		assert.Contains(t, brief, "\nPATHS: "+strings.Join(c.Paths, ", ")+"\n", c.ID)
		assert.Empty(t, LintWith(c.ID, brief, LintOptions{}), c.ID)
	}
}

// Two cards that share a package glob and neither needs the other are said, so the
// add is given --allow-shared-paths; a card that needs the other is not.
func TestCardsSharingAPackageAreShared(t *testing.T) {
	t.Parallel()
	fs, _ := ParseFindings("internal/bus/a.go:1\tx\ty\t\ninternal/bus/b.go:1\tx\ty\t\n")
	assert.True(t, PlanFindings(fs, "", "", 0).Shared)
	one, _ := ParseFindings("internal/bus/a.go:1\tx\ty\t\ninternal/swarm/b.go:1\tx\ty\t\n")
	assert.False(t, PlanFindings(one, "", "", 0).Shared)
	a := Card{ID: "a", Paths: []string{"p/*.go"}}
	b := Card{ID: "b", Paths: []string{"p/*.go"}, Deps: []string{"a"}}
	assert.False(t, SharedPaths([]Card{a, b}))
	b.Deps = nil
	assert.True(t, SharedPaths([]Card{a, b}))
}

// The card checks refuse a brief with the finding: no tier on line 1, a TEST outside
// PATHS, a name outside the owner's quoted words, a dropped card named; a clean brief,
// the owner quoted by name, and the card's own id that carries a name all pass.
func TestCardChecksRefuseWithTheFinding(t *testing.T) {
	t.Parallel()
	opts := LintOptions{Names: []string{"Ada", "bench7"}, Dropped: []string{"old-card.w1"}}
	clean := "RESULT: rate-ada-x.w2 sha=abc tier: pro\nREPO: o/r\nPATHS: internal/x/*.go, internal/x/*_test.go\nTEST: ./internal/x TestY\n\nTHE TASK. The owner said \"Ada wants this\" and the card does it.\n"
	assert.Empty(t, CardChecks("rate-ada-x.w2", clean, opts))

	checks := func(id, brief string) []string {
		var out []string
		for _, f := range CardChecks(id, brief, opts) {
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

	free := "Do the thing.\n"
	assert.Empty(t, CardChecks("c", free, opts), "a brief with no PATHS line is a free task: no tier or TEST check")

	f := CardChecks("c", "RESULT: c tier: pro\nPATHS: a/*.go\nTEST: ./b TestZ\n", LintOptions{})
	require.Len(t, f, 1)
	assert.Equal(t, "LINT DRIFT card=c check=test-outside-paths line=3: the TEST package b is no directory PATHS names (a/*.go); a card lands with a test it may edit", f[0].String())
}

func TestTestPackageOf(t *testing.T) {
	t.Parallel()
	for brief, want := range map[string]string{
		"TEST: internal/x TestY\n":                          "internal/x",
		"TEST: ./internal/x/ TestY\n":                       "internal/x",
		"TEST: go test ./internal/x/... -run TestY\n":       "internal/x",
		"TEST: none (a read card)\n":                        "",
		"PATHS: a/*.go\n":                                   "",
		"TEST: cmd/nova-x TestHelpExampleLinesRunAsPrinted": "cmd/nova-x",
	} {
		assert.Equal(t, want, TestPackageOf(brief), brief)
	}
}
