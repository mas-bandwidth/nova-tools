package landed

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// origin builds the forge's repository on disk, the shape of 2026-09-22:
//
//	M        a.go = lines 1..9, b.go = x
//	pull/1   M + a.go line 1 -> 1pr
//	pull/2   M + a.go line 9 -> 9pr
//	dev      M -> land (pulls 1, 2 COMBINED, and 5) -> later (a.go line 5, c.go rewritten)
//	pull/3   M + b.go -> y                       (never landed)
//	pull/4   M + a.go line 1 -> 1other           (conflicts with dev)
//	pull/5   M + c.go = c1                       (landed; "later" rewrote c.go)
//
// No dev commit carries pull/1's or pull/2's a.go byte for byte -- the #2544 shape
// -- and merging either into dev still changes nothing, so both landed.
func origin(t *testing.T) (url string, heads map[int]string) {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "uploadpack.allowFilter", "true")
	put := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) string {
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", msg)
		return gitIn(t, dir, "rev-parse", "HEAD")
	}
	put("a.go", lines(nil))
	put("b.go", "x\n")
	m := commit("M")
	heads = map[int]string{}
	branch := func(n int, name, body string) {
		gitIn(t, dir, "checkout", "-q", "--detach", m)
		put(name, body)
		heads[n] = commit("pull")
		gitIn(t, dir, "update-ref", "refs/pull/"+itoa(n)+"/head", heads[n])
	}
	branch(1, "a.go", lines(map[int]string{1: "1pr"}))
	branch(2, "a.go", lines(map[int]string{9: "9pr"}))
	branch(3, "b.go", "y\n")
	branch(4, "a.go", lines(map[int]string{1: "1other"}))
	branch(5, "c.go", "c1\n")
	gitIn(t, dir, "update-ref", "refs/pull/7/head", heads[5])
	gitIn(t, dir, "checkout", "-q", "-b", "dev", m)
	put("a.go", lines(map[int]string{1: "1pr", 9: "9pr"}))
	put("c.go", "c1\n")
	heads[0] = commit("land-1: 3 approved PRs (#1 #2 #5)")
	put("a.go", lines(map[int]string{1: "1pr", 5: "5later", 9: "9pr"}))
	put("c.go", "c-rewritten\n")
	commit("later")
	gitIn(t, dir, "checkout", "-q", "--detach")
	return "file://" + dir, heads
}

// lines is a.go: the numbers 1 to 9, one per line, with the edits given.
func lines(edit map[int]string) string {
	var b strings.Builder
	for i := 1; i <= 9; i++ {
		line, ok := edit[i]
		if !ok {
			line = strconv.Itoa(i)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }

func closedAt(head string, at time.Time) string {
	return `{"state":"closed","merged_at":null,"closed_at":"` + at.Format(time.RFC3339) + `","head":{"sha":"` + head + `"}}`
}

func closedPR(head string) string {
	return `{"state":"closed","merged_at":null,"head":{"sha":"` + head + `"}}`
}

func TestLandedIsTheLandersMergeRule(t *testing.T) {
	url, heads := origin(t)
	f := &forge{answers: map[string]string{
		"api repos/o/r/pulls/1": closedPR(heads[1]),
		"api repos/o/r/pulls/2": closedPR(heads[2]),
		"api repos/o/r/pulls/3": closedPR(heads[3]),
		"api repos/o/r/pulls/4": closedPR(heads[4]),
		"api repos/o/r/pulls/5": closedAt(heads[5], time.Now().UTC().Add(time.Minute)),
		"api repos/o/r/pulls/7": closedAt(heads[5], time.Now().UTC().Add(-48*time.Hour)),
		"api repos/o/r/pulls/8": `{"state":"closed","merged_at":"2026-09-22T00:00:00Z","head":{"sha":"abc"}}`,
		"api repos/o/r/pulls/9": `{"state":"open","merged_at":null,"head":{"sha":"abc"}}`,
	}}
	e := New(f.run, "o/r", "dev", t.TempDir())
	e.GitURL = func(string) string { return url }
	ctx := context.Background()
	for _, c := range []struct {
		subject, word, why string
	}{
		// the #2544 control: combined with another PR, carried by no single commit
		{"pr:o/r#1", "yes", "merge-changes-nothing"},
		{"pr:o/r#2", "yes", "merge-changes-nothing"},
		{"pr:o/r#3", "no", "merge-changes-base"},
		{"pr:o/r#4", "no", "merge-conflicts"},
		// dev rewrote what #5 added: the tip conflicts, the lander's commit in
		// the landing window is where merging changed nothing
		{"pr:o/r#5", "yes", "merge-changes-nothing-at:" + heads[0][:12]},
		// the same head closed long before any base commit: no window, the tip's no
		{"pr:o/r#7", "no", "merge-conflicts"},
		{"pr:o/r#8", "yes", "merged"},
		{"pr:o/r#9", "no", "open"},
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
	// asked again: every answer comes from the run's cache, and gh is not asked
	total := 0
	for _, n := range f.calls {
		total += n
	}
	e.Landed(ctx, "pr:o/r#1")
	e.Landed(ctx, "pr:o/r#3")
	after := 0
	for _, n := range f.calls {
		after += n
	}
	if after != total {
		t.Errorf("a repeated question reached gh: %d calls -> %d", total, after)
	}
	// A second run reuses the bare repository the first one made.
	e2 := New(f.run, "o/r", "dev", e.Cache)
	e2.GitURL = e.GitURL
	if v := e2.Landed(ctx, "pr:o/r#2"); !v.Holds {
		t.Errorf("second run over the same cache: %s why=%s", v.Word(), v.Why)
	}
}

func TestLandedWithoutACacheIsUnknown(t *testing.T) {
	_, heads := origin(t)
	f := &forge{answers: map[string]string{"api repos/o/r/pulls/1": closedPR(heads[1])}}
	v := New(f.run, "o/r", "dev", "").Landed(context.Background(), "pr:o/r#1")
	if v.Known {
		t.Errorf("no cache to merge in, yet the verdict is %s why=%s", v.Word(), v.Why)
	}
}

func TestMergedAt(t *testing.T) {
	f := &forge{answers: map[string]string{
		"api repos/o/r/pulls/1": `{"state":"closed","merged_at":"2026-09-22T00:00:00Z","merge_commit_sha":"abc123","head":{"sha":"def456"}}`,
		"api repos/o/r/pulls/2": closedPR("h2"),
	}}
	e := New(f.run, "o/r", "dev", "")
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
	e := New(f.run, "o/r", "dev", "")
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
	if v := New(f.run, "", "dev", "").Landed(ctx, "commit:aaaaaaa"); v.Known {
		t.Errorf("a bare commit with no repo anywhere was answered: %s", v.Word())
	}
}
