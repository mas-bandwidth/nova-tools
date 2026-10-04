package refmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCompareCoverSig covers Sig (compare.go): the difference's table and
// field joined by a dot (the main path), and an empty Difference whose Sig
// is just the dot (the refusal).
func TestCompareCoverSig(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		d    Difference
		want string
	}{
		"main path": {
			d:    Difference{Table: "primary", ID: "p1", Field: "state"},
			want: "primary.state",
		},
		"empty fields refusal": {
			d:    Difference{},
			want: ".",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.d.Sig())
		})
	}
}

// TestCompareCoverString covers String (compare.go): a fully populated
// Difference renders its table, id, field and engine/model values (the main
// path), and a zero Difference still formats (the refusal).
func TestCompareCoverString(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		d    Difference
		want string
	}{
		"main path": {
			d:    Difference{Table: "primary", ID: "p1", Field: "state", Engine: "waiting", Model: "ready"},
			want: `primary p1 state: engine "waiting", model "ready"`,
		},
		"zero difference refusal": {
			d:    Difference{},
			want: `  : engine "", model ""`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.d.String())
		})
	}
}

// TestCompareCoverShow covers show (compare.go): the []string branch joins
// with commas (the main path), the bool branch maps to "yes"/"no", and a
// value that is neither a []string nor a bool falls through to fmt.Sprint
// (the refusal).
func TestCompareCoverShow(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		v    any
		want string
	}{
		"string slice main path": {
			v:    []string{"a", "b", "c"},
			want: "a,b,c",
		},
		"bool true": {
			v:    true,
			want: "yes",
		},
		"bool false refusal branch": {
			v:    false,
			want: "no",
		},
		"int falls through refusal": {
			v:    42,
			want: "42",
		},
		"nil falls through refusal": {
			v:    nil,
			want: "<nil>",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, show(tc.v))
		})
	}
}

// TestCompareCoverUnion covers union (compare.go): two slices with overlap
// become one sorted, de-duplicated slice (the main path), and empty inputs
// yield nil (the refusal).
func TestCompareCoverUnion(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		a, b []string
		want []string
	}{
		"overlapping main path": {
			a:    []string{"b", "a"},
			b:    []string{"b", "c"},
			want: []string{"a", "b", "c"},
		},
		"disjoint sets": {
			a:    []string{"a"},
			b:    []string{"c"},
			want: []string{"a", "c"},
		},
		"all duplicates refusal": {
			a:    []string{"x", "x"},
			b:    []string{"x"},
			want: []string{"x"},
		},
		"empty refusal": {
			a:    nil,
			b:    nil,
			want: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, union(tc.a, tc.b))
		})
	}
}

// TestCompareCoverUnionJ covers unionJ (compare.go): two judgment maps with
// overlap become one sorted, de-duplicated slice (the main path), and empty
// maps yield nil (the refusal).
func TestCompareCoverUnionJ(t *testing.T) {
	t.Parallel()
	jp := func(typ, subj string) Judgment { return Judgment{Type: typ, Subject: subj} }
	for name, tc := range map[string]struct {
		a, b map[Judgment]bool
		want []Judgment
	}{
		"overlapping main path": {
			a:    map[Judgment]bool{jp(JFailed, "p1"): true, jp(JBound, "p2"): true},
			b:    map[Judgment]bool{jp(JBound, "p2"): true, jp(JCI, "p3"): true},
			want: []Judgment{jp(JBound, "p2"), jp(JCI, "p3"), jp(JFailed, "p1")},
		},
		"empty refusal": {
			a:    nil,
			b:    nil,
			want: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, unionJ(tc.a, tc.b))
		})
	}
}

// TestCompareCoverCompare covers Compare (compare.go): two states that differ
// in a primary's state yield one Difference (the main path); identical states
// yield none (the refusal); and a primary present on only one side yields an
// "exists" Difference (a second refusal through the !eok || !mok guard).
func TestCompareCoverCompare(t *testing.T) {
	t.Parallel()
	identical := State{
		Primaries: map[string]Primary{
			"p1": {Stream: "s1", Kind: KindPrimary, State: Waiting, Score: 1.0},
		},
		Work:    map[string]WorkCard{},
		Reads:   map[string]ReadCard{},
		Merge:   map[string]MergeCard{},
		Streams: map[string]Stream{"s1": {State: SWaiting}},
		Members: map[string]string{"m1": Up},
		Open:    map[Judgment]bool{},
		Acked:   map[Judgment]bool{},
		Machine: Stopped,
		Epoch:   1,
	}
	differ := State{
		Primaries: map[string]Primary{
			"p1": {Stream: "s1", Kind: KindPrimary, State: Ready, Score: 1.0},
		},
		Work:    map[string]WorkCard{},
		Reads:   map[string]ReadCard{},
		Merge:   map[string]MergeCard{},
		Streams: map[string]Stream{"s1": {State: SWaiting}},
		Members: map[string]string{"m1": Up},
		Open:    map[Judgment]bool{},
		Acked:   map[Judgment]bool{},
		Machine: Stopped,
		Epoch:   1,
	}
	oneSide := State{
		Primaries: map[string]Primary{
			"p1": {Stream: "s1", Kind: KindPrimary, State: Waiting, Score: 1.0},
			"p2": {Stream: "s1", Kind: KindPrimary, State: Waiting, Score: 2.0},
		},
		Machine: Stopped,
		Epoch:   1,
	}
	oneSideOther := State{
		Primaries: map[string]Primary{
			"p1": {Stream: "s1", Kind: KindPrimary, State: Waiting, Score: 1.0},
		},
		Machine: Stopped,
		Epoch:   1,
	}
	for name, tc := range map[string]struct {
		e    State
		m    State
		want []Difference
	}{
		"main path differs in state": {
			e:    identical,
			m:    differ,
			want: []Difference{{Table: "primary", ID: "p1", Field: "state", Engine: "waiting", Model: "ready"}},
		},
		"refusal equal states": {
			e:    identical,
			m:    identical,
			want: nil,
		},
		"refusal exists on one side only": {
			e:    oneSide,
			m:    oneSideOther,
			want: []Difference{{Table: "primary", ID: "p2", Field: "exists", Engine: "yes", Model: "no"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Compare(tc.e, tc.m))
		})
	}
}
