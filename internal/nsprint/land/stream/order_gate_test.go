package stream

import (
	"strings"
	"testing"
)

// TestNeverFinishesIsRetiredFailNotDone is the never-finishes predicate: a
// predecessor can no longer be satisfied when it, or a record it reaches
// through DEPENDS-ON edges not yet met, ended done/fail (retired fail, or
// cancelled). A dependency done/ok or landed is satisfied, merely waiting on
// its predecessor; done/abstain may still become ok; a live or parked
// dependency is walked through; a satisfied one's own edges are not.
func TestNeverFinishesIsRetiredFailNotDone(t *testing.T) {
	t.Parallel()
	recs := map[string]DepRec{
		"p-ok":        {Where: "waiting", Deps: []string{"done-ok"}},
		"done-ok":     {Where: "done", OK: "ok", Deps: []string{"failed"}},
		"p-landed":    {Where: "waiting", Deps: []string{"landed"}},
		"landed":      {Where: "landed"},
		"p-abstain":   {Where: "waiting", Deps: []string{"abstained"}},
		"abstained":   {Where: "done", OK: "abstain"},
		"p-fail":      {Where: "waiting", Deps: []string{"failed"}},
		"failed":      {Where: "done", OK: "fail"},
		"p-cancel":    {Where: "ready", Deps: []string{"cancelled"}},
		"cancelled":   {Where: "done", State: "cancelled"},
		"p-deep":      {Where: "waiting", Deps: []string{"mid"}},
		"mid":         {Where: "parked", Deps: []string{"mid2"}},
		"mid2":        {Where: "working", Deps: []string{"failed"}},
		"p-live":      {Where: "working", Deps: []string{"mid2-ok"}},
		"mid2-ok":     {Where: "review", Deps: []string{"landed"}},
		"p-loop":      {Where: "waiting", Deps: []string{"p-loop2"}},
		"p-loop2":     {Where: "waiting", Deps: []string{"p-loop"}},
		"retired-now": {Where: "done", OK: "fail"},
	}
	reads := 0
	read := func(ids []string) (map[string]DepRec, error) {
		reads++
		out := map[string]DepRec{}
		for _, id := range ids {
			out[id] = recs[id]
		}
		return out, nil
	}
	var got []string
	for _, start := range []string{"p-ok", "p-landed", "p-abstain", "p-fail", "p-cancel", "p-deep", "p-live", "p-loop", "retired-now"} {
		dep, err := NeverFinishes(start, read)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, start+"="+dep)
	}
	want := "p-ok= p-landed= p-abstain= p-fail=failed p-cancel=cancelled p-deep=failed p-live= p-loop= retired-now=retired-now"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	t.Logf("%s (%d level reads)", want, reads)
}
