package pkgselect

import (
	"go/build"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deal_tags_test.go pins TaggedPackages, the pure selection behind `ci deal
// --tags`: the live packages holding a _test.go whose //go:build expression
// holds when the leg's tag is set and fails when it is not. The tests are pure
// over synthesized (package, constraint) pairs: no `go list`, no files.

// TestDealTagsSelectsOnlyTheTaggedFiles is the table: each package's constraint
// decides whether it is dealt for the tag, the way nightly-slow.yml's legs read
// it. A package with only untagged tests is dealt for no tag; a `slow` file is
// dealt for slow and not for functional; `functional && unix` is dealt for
// functional on a unix host; `!slow` is dealt for nothing.
func TestDealTagsSelectsOnlyTheTaggedFiles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		pairs []TaggedPackage
		tags  []string
		base  []string
		want  []string
	}{
		{
			name:  "a package with only untagged tests is not selected",
			pairs: []TaggedPackage{{Package: "a", Constraint: ""}},
			tags:  []string{"slow"},
		},
		{
			name:  "a slow file selects its package for slow",
			pairs: []TaggedPackage{{Package: "a", Constraint: "slow"}},
			tags:  []string{"slow"},
			want:  []string{"a"},
		},
		{
			name:  "a slow file does not select its package for functional",
			pairs: []TaggedPackage{{Package: "a", Constraint: "slow"}},
			tags:  []string{"functional"},
		},
		{
			name:  "functional and unix is selected for functional on a unix host",
			pairs: []TaggedPackage{{Package: "a", Constraint: "functional && unix"}},
			tags:  []string{"functional"},
			base:  []string{"unix"},
			want:  []string{"a"},
		},
		{
			name:  "functional and unix is not selected for functional without unix",
			pairs: []TaggedPackage{{Package: "a", Constraint: "functional && unix"}},
			tags:  []string{"functional"},
		},
		{
			name:  "a negated opt-in is never selected",
			pairs: []TaggedPackage{{Package: "a", Constraint: "!slow"}},
			tags:  []string{"slow"},
		},
		{
			name:  "a negated opt-in is not selected for another tag either",
			pairs: []TaggedPackage{{Package: "a", Constraint: "!slow"}},
			tags:  []string{"functional"},
		},
		{
			name:  "a file satisfied either way is not selected",
			pairs: []TaggedPackage{{Package: "a", Constraint: "functional || unix"}},
			tags:  []string{"functional"},
			base:  []string{"unix"},
		},
		{
			name:  "an either tag file is selected for both tags",
			pairs: []TaggedPackage{{Package: "a", Constraint: "slow || functional"}},
			tags:  []string{"slow", "functional"},
			want:  []string{"a"},
		},
		{
			name: "an untagged file beside a slow one still selects the package",
			pairs: []TaggedPackage{
				{Package: "a", Constraint: ""},
				{Package: "a", Constraint: "slow"},
			},
			tags: []string{"slow"},
			want: []string{"a"},
		},
		{
			name: "a package is selected once however many of its files carry the tag",
			pairs: []TaggedPackage{
				{Package: "a", Constraint: "slow"},
				{Package: "a", Constraint: "slow && unix"},
			},
			tags: []string{"slow"},
			base: []string{"unix"},
			want: []string{"a"},
		},
		{
			name: "the selected packages keep their first-seen order",
			pairs: []TaggedPackage{
				{Package: "b", Constraint: "slow"},
				{Package: "a", Constraint: "slow"},
				{Package: "b", Constraint: "slow"},
			},
			tags: []string{"slow"},
			want: []string{"b", "a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := TaggedPackages(tc.pairs, tc.tags, tc.base)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDealTagsBaseTagsHoldTheToolchainTags pins BaseTags, the host half of the
// selection: the tags go/build treats as satisfied without a -tags line. A
// context with cgo enabled and a go1.NN release must hand both to
// TaggedPackages — otherwise a `slow && cgo` or `slow && go1.NN` test file is
// dropped from its nightly leg though the toolchain compiles and runs it.
func TestDealTagsBaseTagsHoldTheToolchainTags(t *testing.T) {
	t.Parallel()

	ctxt := build.Context{
		GOOS:        "linux",
		GOARCH:      "amd64",
		Compiler:    "gc",
		CgoEnabled:  true,
		ToolTags:    []string{"goexperiment.regabiargs"},
		ReleaseTags: []string{"go1.24", "go1.25"},
	}
	base := BaseTags(ctxt)
	for _, want := range []string{"linux", "amd64", "gc", "cgo", "unix", "goexperiment.regabiargs", "go1.24", "go1.25"} {
		assert.Contains(t, base, want)
	}

	selected, err := TaggedPackages([]TaggedPackage{
		{Package: "cgo", Constraint: "slow && cgo"},
		{Package: "release", Constraint: "slow && go1.24"},
		{Package: "tool", Constraint: "slow && goexperiment.regabiargs"},
	}, []string{"slow"}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"cgo", "release", "tool"}, selected)
}

// TestDealTagsBaseTagsWithoutCgoDoNotSelect: cgo is a host base tag only when
// the toolchain enables it, so a `slow && cgo` file is dealt for slow on a cgo
// host and nothing on a pure one.
func TestDealTagsBaseTagsWithoutCgoDoNotSelect(t *testing.T) {
	t.Parallel()

	base := BaseTags(build.Context{GOOS: "linux", GOARCH: "amd64", Compiler: "gc", ReleaseTags: []string{"go1.26"}})
	assert.NotContains(t, base, "cgo")
	selected, err := TaggedPackages([]TaggedPackage{{Package: "a", Constraint: "slow && cgo"}}, []string{"slow"}, base)
	require.NoError(t, err)
	assert.Empty(t, selected)
}

// TestDealTagsBaseTagsCarryTheGOOSAliases: the toolchain sets the portability
// aliases go/build derives from GOOS (linux on android, solaris on illumos,
// darwin on ios), so a `slow && linux` file is dealt on android.
func TestDealTagsBaseTagsCarryTheGOOSAliases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ goos, alias string }{
		{"android", "linux"},
		{"illumos", "solaris"},
		{"ios", "darwin"},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()
			base := BaseTags(build.Context{GOOS: tc.goos, GOARCH: "amd64", Compiler: "gc"})
			assert.Contains(t, base, tc.alias)
		})
	}
}

// TestDealTagsRefusesAMalformedConstraint: a constraint the parser cannot read
// is an error naming the package, never a silent skip that would drop its tests
// from every leg.
func TestDealTagsRefusesAMalformedConstraint(t *testing.T) {
	t.Parallel()

	_, err := TaggedPackages([]TaggedPackage{{Package: "a", Constraint: "slow &&"}}, []string{"slow"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a")
}

// TestDealTagsEmptySelectionDealsNothingToEveryShard: when the tagged selection
// is empty (the soak and nightly tags carry no file yet), every shard of every
// deal is dealt nothing — the shard prints one line and passes.
func TestDealTagsEmptySelectionDealsNothingToEveryShard(t *testing.T) {
	t.Parallel()

	selected, err := TaggedPackages(
		[]TaggedPackage{{Package: "a", Constraint: "slow"}, {Package: "b", Constraint: ""}},
		[]string{"soak"}, nil,
	)
	require.NoError(t, err)
	require.Empty(t, selected)

	for _, shards := range []int{1, 2, 7} {
		for shard := 1; shard <= shards; shard++ {
			mine, err := Deal(selected, nil, shards, shard)
			require.NoError(t, err)
			assert.Empty(t, mine, "shard %d of %d", shard, shards)
		}
	}
}
