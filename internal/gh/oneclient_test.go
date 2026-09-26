package gh_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ghUse is what a GitHub call outside this package looks like in Go source:
// gh as an exec argv, the go-github client, or the REST root named for a
// hand-rolled net/http call.
var ghUse = regexp.MustCompile(`Command(Context)?\(\s*(ctx,\s*)?"gh"|\{"gh",|google/go-github|https://api\.github\.com|api\.github\.com/`)

// ghAllowed are the files outside nova-sprint (cmd/nova-sprint,
// internal/nsprint) that still talk to GitHub on their own, each with its
// reason. They are not nova-sprint verbs and are outside #4343's PATHS; a
// follow-up moves each onto internal/gh or retires it. Inside nova-sprint
// there is no allowlist: every verb uses the one client.
var ghAllowed = map[string]string{
	"cmd/nova-review/main.go":      "nova-review, not a nova-sprint verb",
	"cmd/nova-review/reads.go":     "nova-review, not a nova-sprint verb",
	"cmd/nova-sandbox/worktree.go": "nova-sandbox, not a nova-sprint verb",
	"internal/ci/events_gh.go":     "the old ci events forge, not a nova-sprint verb",
	"internal/ghcapture/record.go": "the read-only GraphQL recorder, not a nova-sprint verb",
	"internal/landed/landed.go":    "nova-work landed criterion, not a nova-sprint verb",
	"internal/update/latest.go":    "the self-update's releases read, not a nova-sprint verb",
	"internal/wake/entry.go":       "nova-wake, not a nova-sprint verb",
	"internal/wake/forge.go":       "nova-wake, not a nova-sprint verb",
}

// TestOneGitHubClient (#4343 BUILD 1): every GitHub call in nova-sprint
// goes through internal/gh. No file under cmd/nova-sprint or
// internal/nsprint shells out to gh, imports go-github or names the REST
// root; elsewhere in the module only the named files do, and a row whose
// file no longer does is dropped.
func TestOneGitHubClient(t *testing.T) {
	t.Parallel()
	for _, sample := range []string{
		`c := exec.CommandContext(ctx, "gh", "pr", "view")`,
		`out, err := exec.Command("gh", "auth", "token").Output()`,
		`api := fs.String("api", "https://api.github.com", "")`,
		`"github.com/google/go-github/v60/github"`,
	} {
		if !ghUse.MatchString(sample) {
			t.Fatalf("the pattern misses %q; the test would pass on nothing", sample)
		}
	}
	root := moduleRoot(t)
	hits := map[string][]string{}
	n := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/gh/") {
				return nil
			}
			n++
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if ghUse.MatchString(line) {
					hits[rel] = append(hits[rel], rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 100 {
		t.Fatalf("read %d files; the walk is broken", n)
	}
	for rel, lines := range hits {
		inSprint := strings.HasPrefix(rel, "cmd/nova-sprint/") || strings.HasPrefix(rel, "internal/nsprint/")
		if _, ok := ghAllowed[rel]; ok && !inSprint {
			continue
		}
		for _, l := range lines {
			t.Errorf("GitHub call outside internal/gh: %s", l)
		}
	}
	for rel := range ghAllowed {
		if _, ok := hits[rel]; !ok {
			t.Errorf("allowed file %s no longer calls GitHub on its own (or is gone): drop its row", rel)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = up
	}
}

// bodyRead is a read of an issue, PR or comment body from GitHub.
var bodyRead = regexp.MustCompile(`\.(PRBody|IssueBody|CommentBody)\(`)

// bodyReaders are the two readers of a body (#4343 BUILD 4): the landed
// record (a member whose record does not say which issues it closes) and
// the file verb's byte-for-byte read-back of what it just posted (control
// 50); the import (issue -> card) reads through file. Every other read is
// from the Redis copy.
var bodyReaders = map[string]string{
	"internal/nsprint/land/stream/land.go": "the landed record's one read of a member body",
	"internal/nsprint/file/file.go":        "the file verb's read-back of its own post",
}

// TestBodyReadersAreImportAndLanded: no other file reads a body from
// GitHub; a reader row whose file no longer reads one is dropped.
func TestBodyReadersAreImportAndLanded(t *testing.T) {
	t.Parallel()
	if !bodyRead.MatchString(`b, err := o.GH.PRBody(ctx, o.Repo, r.N)`) {
		t.Fatal("the pattern misses a known body read; the test would pass on nothing")
	}
	root := moduleRoot(t)
	hits := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/gh/") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if bodyRead.MatchString(line) {
					hits[rel] = true
					if _, ok := bodyReaders[rel]; !ok {
						t.Errorf("body read outside the import and the landed record: %s:%d: %s", rel, i+1, strings.TrimSpace(line))
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for rel := range bodyReaders {
		if !hits[rel] {
			t.Errorf("reader %s no longer reads a body (or is gone): drop its row", rel)
		}
	}
}
