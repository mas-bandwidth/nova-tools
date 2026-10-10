package pkgselect

import (
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
