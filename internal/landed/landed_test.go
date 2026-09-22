package landed

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// forge is a canned gh: one stdout per argument line, every line counted. Nothing
// here reaches a network.
type forge struct {
	answers map[string]string
	calls   map[string]int
}

func (f *forge) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[key]++
	out, ok := f.answers[key]
	if !ok {
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}
	return []byte(out), nil
}

const tree = `{"truncated":false,"tree":[{"path":"a.go","type":"blob","sha":"A"},{"path":"old.go","type":"blob","sha":"O"}]}`

func closedPR(head string) string {
	return `{"state":"closed","merged_at":null,"head":{"sha":"` + head + `"}}`
}

func TestLanded(t *testing.T) {
	f := &forge{answers: map[string]string{
		"api repos/o/r/git/trees/dev?recursive=1": tree,

		"api repos/o/r/pulls/1":                               closedPR("h1"),
		"api --paginate repos/o/r/pulls/1/files?per_page=100": `[{"filename":"a.go","status":"renamed","sha":"A","previous_filename":"old.go"}]`,
		"api repos/o/r/pulls/2":                               closedPR("h2"),
		"api --paginate repos/o/r/pulls/2/files?per_page=100": `[{"filename":"a.go","status":"added","sha":"A"},{"filename":"new.go","status":"added","sha":"N"}]`,
		"api repos/o/r/pulls/3":                               closedPR("h3"),
		"api --paginate repos/o/r/pulls/3/files?per_page=100": `[]`,
		"api repos/o/r/pulls/4":                               closedPR("h4"),
		"api --paginate repos/o/r/pulls/4/files?per_page=100": `[{"filename":"old.go","status":"removed","sha":"O"}]`,
		"api repos/o/r/pulls/5":                               closedPR("h5"),
		"api --paginate repos/o/r/pulls/5/files?per_page=100": `[{"filename":"a.go","status":"modified","sha":"A"}]`,
		"api repos/o/r/commits?sha=dev&per_page=100":          `[{"sha":"c1","commit":{"message":"land: (#1 #22)"}}]`,
		"api repos/o/r/git/trees/c1?recursive=1":              `{"truncated":false,"tree":[{"path":"a.go","type":"blob","sha":"A"},{"path":"old.go","type":"blob","sha":"O"}]}`,
	}}
	e := New(f.run, "o/r", "dev")
	ctx := context.Background()
	for _, c := range []struct {
		subject, word, why string
	}{
		// a rename whose old path is still on the base has not landed whole
		{"pr:o/r#1", "no", "differs:old.go"},
		// an added file the base does not hold
		{"pr:o/r#2", "no", "differs:new.go"},
		// a closed PR that touched nothing landed nothing
		{"pr:o/r#3", "no", "no-files"},
		// a removal the base still carries
		{"pr:o/r#4", "no", "differs:old.go"},
		{"pr:o/r#5", "yes", "content-in-base"},
		{"pr:o/r#6", "unknown", "error:gh:-Not-Found-(HTTP-404)"},
		{"pr:o/r", "unknown", `error:subject-"pr:o/r"-is-not-pr:<owner/repo>#<n>`},
		{"pr:o/r/../x#1", "unknown", `error:subject-"pr:o/r/../x#1"-is-not-pr:<owner/repo>#<n>`},
		{"commit:xyz", "unknown", "error:commit:xyz-is-not-a-sha"},
	} {
		v := e.Landed(ctx, c.subject)
		if v.Word() != c.word || v.Why != c.why {
			t.Errorf("Landed(%q) = %s why=%s, want %s why=%s", c.subject, v.Word(), v.Why, c.word, c.why)
		}
	}
	if n := f.calls["api repos/o/r/git/trees/dev?recursive=1"]; n != 1 {
		t.Errorf("the base tree was fetched %d times, want once per run", n)
	}
	// asked again: every answer comes from the run's cache
	before := len(f.calls)
	total := 0
	for _, n := range f.calls {
		total += n
	}
	e.Landed(ctx, "pr:o/r#5")
	e.Landed(ctx, "pr:o/r#1")
	after := 0
	for _, n := range f.calls {
		after += n
	}
	if len(f.calls) != before || after != total {
		t.Errorf("a repeated question reached gh: %d calls -> %d", total, after)
	}
}

func TestLandedTruncatedTreeIsUnknown(t *testing.T) {
	f := &forge{answers: map[string]string{
		"api repos/o/r/git/trees/dev?recursive=1":             `{"truncated":true,"tree":[]}`,
		"api repos/o/r/pulls/5":                               closedPR("h5"),
		"api --paginate repos/o/r/pulls/5/files?per_page=100": `[{"filename":"a.go","status":"modified","sha":"A"}]`,
	}}
	v := New(f.run, "o/r", "dev").Landed(context.Background(), "pr:o/r#5")
	if v.Known || v.Holds {
		t.Errorf("a truncated tree cannot say a file is absent, yet the verdict is %s why=%s", v.Word(), v.Why)
	}
}

func TestMergedAt(t *testing.T) {
	f := &forge{answers: map[string]string{
		"api repos/o/r/pulls/1": `{"state":"closed","merged_at":"2026-09-22T00:00:00Z","merge_commit_sha":"abc123","head":{"sha":"def456"}}`,
		"api repos/o/r/pulls/2": closedPR("h2"),
	}}
	e := New(f.run, "o/r", "dev")
	ctx := context.Background()
	for _, c := range []struct{ subject, word string }{
		{"pr:o/r#1", "yes"},
		{"pr:o/r#1@def456", "yes"},
		{"pr:o/r#1@abc1", "yes"},
		{"pr:o/r#1@999999", "no"},
		{"pr:o/r#2", "no"},
	} {
		if v := e.MergedAt(ctx, c.subject); v.Word() != c.word {
			t.Errorf("MergedAt(%q) = %s why=%s, want %s", c.subject, v.Word(), v.Why, c.word)
		}
	}
	if f.calls["api repos/o/r/pulls/1"] != 1 {
		t.Errorf("pull 1 was fetched %d times, want once", f.calls["api repos/o/r/pulls/1"])
	}
}

func TestCommitReachable(t *testing.T) {
	f := &forge{answers: map[string]string{
		"api repos/o/r/compare/dev...aaaaaaa": `{"status":"identical"}`,
		"api repos/p/q/compare/dev...bbbbbbb": `{"status":"behind"}`,
		"api repos/o/r/compare/dev...ccccccc": `{"status":"ahead"}`,
	}}
	e := New(f.run, "o/r", "dev")
	ctx := context.Background()
	for _, c := range []struct{ subject, word string }{
		{"commit:aaaaaaa", "yes"},
		{"commit:p/q@bbbbbbb", "yes"},
		{"commit:ccccccc", "no"},
	} {
		if v := e.Landed(ctx, c.subject); v.Word() != c.word {
			t.Errorf("Landed(%q) = %s why=%s, want %s", c.subject, v.Word(), v.Why, c.word)
		}
	}
	if v := New(f.run, "", "dev").Landed(ctx, "commit:aaaaaaa"); v.Known {
		t.Errorf("a bare commit with no repo anywhere was answered: %s", v.Word())
	}
}

func TestNamesPR(t *testing.T) {
	for _, c := range []struct {
		msg  string
		n    int
		want bool
	}{
		{"land-1600: 2 approved PRs (#2614 #2607) — gated", 2614, true},
		{"land-1600: 2 approved PRs (#2614 #2607) — gated", 2607, true},
		{"land-1600: 2 approved PRs (#2614 #2607) — gated", 26, false},
		{"fix (#26140) and (#2614)", 2614, true},
		{"subject\n\nbody names #2614", 2614, false},
		{"#7", 7, true},
	} {
		if got := namesPR(c.msg, c.n); got != c.want {
			t.Errorf("namesPR(%q, %d) = %v, want %v", c.msg, c.n, got, c.want)
		}
	}
}
