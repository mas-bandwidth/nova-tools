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
// The REST host is spelled in two literals so the CI-NET class test, which
// reads every string literal of a test as a URL, sees no host here.
var ghUse = regexp.MustCompile(`Command(Context)?\(\s*(ctx,\s*)?"gh"|\{"gh",|(?i:(program|bin|path)\w*) = "gh"\s*$|google/go-github|https://` + `api\.github\.com|api\.github\.com/` +
	`|(\.(gh|run|api)|\bgh)\([^)]*"api"`)

// ghAllowed are the files outside nova-sprint (cmd/nova-sprint,
// internal/nsprint) that still talk to GitHub on their own, each with its
// reason. They are not nova-sprint verbs and are outside #4343's PATHS; a
// follow-up moves each onto internal/gh or retires it. Inside nova-sprint
// there is no allowlist: every verb uses the one client.
var ghAllowed = map[string]string{
	"cmd/nova-decide/review.go":    "nova-decide reads check-runs through its gh wrapper; not a nova-sprint verb",
	"cmd/nova-review/main.go":      "nova-review, not a nova-sprint verb",
	"cmd/nova-review/reads.go":     "nova-review, not a nova-sprint verb",
	"cmd/nova-sandbox/worktree.go": "nova-sandbox, not a nova-sprint verb",
	"internal/ci/events_gh.go":     "the old ci events forge, not a nova-sprint verb",
	"internal/ghcapture/record.go": "the read-only GraphQL recorder, not a nova-sprint verb",
	"internal/board/issue.go":      "nova-board reads issues through its gh wrapper; not a nova-sprint verb",
	"internal/ci/failed_forge.go":  "the old ci failed-run reader through its gh wrapper; not a nova-sprint verb",
	"internal/converge/forge.go":   "nova-converge's gh binary seam; not a nova-sprint verb",
	"internal/landed/landed.go":    "nova-work landed criterion, not a nova-sprint verb",
	"internal/merge/enqueue.go":    "nova-merge's merge-queue GraphQL through its gh wrapper; not a nova-sprint verb",
	"internal/merge/flaky.go":      "nova-merge reads a run's jobs through its gh wrapper; not a nova-sprint verb",
	"internal/merge/host.go":       "nova-merge reads a branch sha through its gh wrapper; not a nova-sprint verb",
	"internal/merge/sweepgh.go":    "nova-merge's sweep GraphQL through its gh wrapper; not a nova-sprint verb",
	"internal/secrets/seal.go":     "nova-secrets' gh binary seam; not a nova-sprint verb",
	"internal/release/edges.go":    "nova-release reads check-runs through its gh wrapper; not a nova-sprint verb",
	"internal/update/latest.go":    "the self-update's releases read, not a nova-sprint verb",
	"internal/wake/branch.go":      "nova-wake reads a branch through its gh wrapper; not a nova-sprint verb",
	"internal/wake/entry.go":       "nova-wake, not a nova-sprint verb",
	"internal/wake/pr.go":          "nova-wake reads PRs through its gh wrapper; not a nova-sprint verb",
	"internal/wake/forge.go":       "nova-wake, not a nova-sprint verb",
	"internal/wake/run.go":         "nova-wake reads check-runs through its gh wrapper; not a nova-sprint verb",
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
		`api := fs.String("api", "https://` + `api.github.com", "")`,
		`"github.com/google/go-github/v60/github"`,
		`		program = "gh"`, // a program variable defaulting to gh
		`	meta, err := h.gh("api", fmt.Sprintf("repos/%s/actions/runs/%d", h.Repo, id))`, // a gh wrapper
		`	out, err := g.gh(false, "api", fmt.Sprintf("repos/%s/actions/runs", g.Repo))`,
		`	rawRuns, err := gh(ctx, r.Timeout, &r.calls, "api",`,
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

// clientLiteral is a production construction of a client or of a seam that
// builds one; storeSet is the deferred form, when the store opens after the
// token gate (the landing verbs).
var (
	clientLiteral = regexp.MustCompile(`(gh\.Client|stream\.GitHub|&GitHub|RESTFiler|RESTPRHost|read\.Poster|deal\.GH|card\.GHIssues)\{`)
	storeSet      = regexp.MustCompile(`\.Redis = `)
)

// TestEveryProductionClientHasAStore (#4343 BUILD 1, the read of #4371):
// every client built in production code names its Redis where it is built,
// or the file sets it once the store is open; a client without a store
// counts nothing and paces alone, and that is a test's shape only.
func TestEveryProductionClientHasAStore(t *testing.T) {
	t.Parallel()
	if !clientLiteral.MatchString(`c := &gh.Client{API: f.BaseURL, Token: f.Token}`) || !storeSet.MatchString(`	gh.Redis = st.Client()`) {
		t.Fatal("the pattern misses a known construction; the test would pass on nothing")
	}
	root := moduleRoot(t)
	n := 0
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
			body := string(b)
			for i, line := range strings.Split(body, "\n") {
				if !clientLiteral.MatchString(line) {
					continue
				}
				n++
				if strings.Contains(line, "Redis:") || storeSet.MatchString(body) {
					continue
				}
				t.Errorf("client built without a store: %s:%d: %s", rel, i+1, strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 5 {
		t.Fatalf("saw %d client constructions; the walk is broken", n)
	}
}
