package harvestcopy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
)

const branch = "nova/copies/quack-1-c2-a2"

// git runs one git command in dir with a fixed identity and no hooks.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// repos makes a bare remote and a work clone with one commit on branch in
// t.TempDir(); it returns both and the commit's sha.
func repos(t *testing.T) (bare, work, sha string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "remote.git")
	work = filepath.Join(root, "repo")
	git(t, root, "init", "-q", "--bare", bare)
	git(t, root, "init", "-q", "-b", "dev", work)
	if err := os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "base.txt")
	git(t, work, "commit", "-q", "-m", "base")
	git(t, work, "push", "-q", bare, "dev:refs/heads/dev")
	git(t, work, "checkout", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(work, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "x.go")
	git(t, work, "commit", "-q", "-m", "the copy's commit")
	return bare, work, git(t, work, "rev-parse", "HEAD")
}

// forge is a fake GitHub pulls endpoint: GET lists the open PRs for a head,
// POST opens one (or refuses with refuse), and every request's bearer token
// and body are kept.
type forge struct {
	mu     sync.Mutex
	next   int
	open   map[string]int // owner:branch -> number
	auth   []string
	bodies []map[string]string
	refuse int // a POST status to refuse with, 0 none
	msg    string
	gets   int
}

func newForge(t *testing.T) (*forge, *httptest.Server) {
	t.Helper()
	f := &forge{next: 4321, open: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		if !strings.HasPrefix(r.URL.Path, "/repos/mas-bandwidth/nova-tools/pulls") {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.gets++
			head := r.URL.Query().Get("head")
			var out []map[string]any
			if n, ok := f.open[head]; ok && r.URL.Query().Get("state") == "open" {
				_, ref, _ := strings.Cut(head, ":")
				out = append(out, map[string]any{"number": n, "html_url": fmt.Sprintf("http://127.0.0.1/pr/%d", n),
					"state": "open", "head": map[string]string{"ref": ref, "sha": "0000"}})
			}
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodPost:
			var body map[string]string
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			f.bodies = append(f.bodies, body)
			if f.refuse != 0 {
				w.WriteHeader(f.refuse)
				fmt.Fprintf(w, `{"message":%q}`, f.msg)
				return
			}
			n := f.next
			f.next++
			f.open["mas-bandwidth:"+body["head"]] = n
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": n, "html_url": fmt.Sprintf("http://127.0.0.1/pr/%d", n),
				"state": "open", "head": map[string]string{"ref": body["head"], "sha": "0000"}})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func request(bare, work, sha, api string) harvestcopy.Request {
	return harvestcopy.Request{
		RepoDir: work, SHA: sha, Branch: branch, Repo: "nova-tools", Base: "dev",
		Title: "copy model: push and open the PR at the boundary", Stream: "swarm: cards",
		Origin: "issue:nova-tools#4227", DoneWhen: "a work copy reaches ok with a PR open",
		Token: "t-4227", Remote: bare, API: api,
	}
}

// TestHarvestPushesTheBranchAndOpensThePR is the #4227 boundary on a
// throwaway bare remote and a fake forge: the commit lands as
// refs/heads/<branch>, one PR is opened against BASE with the primary's
// title and the typed body, the bearer token is on every REST call and
// nowhere else, and a second harvest of the same commit pushes nothing and
// opens nothing (already, already).
func TestHarvestPushesTheBranchAndOpensThePR(t *testing.T) {
	t.Parallel()
	bare, work, sha := repos(t)
	f, srv := newForge(t)
	ctx := context.Background()

	res, err := harvestcopy.Harvest(ctx, request(bare, work, sha, srv.URL))
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if res.Repo != "mas-bandwidth/nova-tools" || res.PR != 4321 || res.Head != sha || res.Branch != branch ||
		res.Push != "pushed" || res.Open != "opened" {
		t.Fatalf("result %+v", res)
	}
	if tip := git(t, bare, "rev-parse", "refs/heads/"+branch); tip != sha {
		t.Fatalf("remote branch at %s, want %s", tip, sha)
	}
	f.mu.Lock()
	bodies, auth := f.bodies, f.auth
	f.mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("%d PRs opened, want one", len(bodies))
	}
	pr := bodies[0]
	if pr["head"] != branch || pr["base"] != "dev" || pr["title"] != "copy model: push and open the PR at the boundary" {
		t.Fatalf("PR %v", pr)
	}
	body := pr["body"]
	for _, want := range []string{"STREAM: swarm: cards\n", "ORIGIN: issue:nova-tools#4227\n", "DONE-WHEN: a work copy reaches ok with a PR open\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR body lacks %q:\n%s", want, body)
		}
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), harvestcopy.ClaudeLine) {
		t.Fatalf("PR body does not end with the Claude Code line:\n%s", body)
	}
	for _, a := range auth {
		if a != "Bearer t-4227" {
			t.Fatalf("REST call authorization %q", a)
		}
	}
	if strings.Contains(res.Line(), "t-4227") {
		t.Fatalf("the token reached the receipt %q", res.Line())
	}

	again, err := harvestcopy.Harvest(ctx, request(bare, work, sha, srv.URL))
	if err != nil {
		t.Fatalf("second harvest: %v", err)
	}
	if again.PR != 4321 || again.Head != sha || again.Push != "already" || again.Open != "already" {
		t.Fatalf("second harvest %+v, want the same PR, already pushed and already open", again)
	}
	f.mu.Lock()
	n := len(f.bodies)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("the second harvest opened a PR: %d POSTs", n)
	}
}

// TestHarvestNeverForcesAMovedBranch: the remote branch at another sha is
// ErrBranchMoved (an ErrPushRefused, reason push-refused), the remote is
// left as it was and no REST call is made.
func TestHarvestNeverForcesAMovedBranch(t *testing.T) {
	t.Parallel()
	bare, work, sha := repos(t)
	f, srv := newForge(t)
	other := git(t, work, "rev-parse", "HEAD~1")
	git(t, work, "push", "-q", bare, other+":refs/heads/"+branch)

	_, err := harvestcopy.Harvest(context.Background(), request(bare, work, sha, srv.URL))
	if !errors.Is(err, harvestcopy.ErrBranchMoved) || !errors.Is(err, harvestcopy.ErrPushRefused) {
		t.Fatalf("err %v, want branch-moved and push-refused", err)
	}
	if harvestcopy.Reason(err) != "push-refused" {
		t.Fatalf("reason %q", harvestcopy.Reason(err))
	}
	if tip := git(t, bare, "rev-parse", "refs/heads/"+branch); tip != other {
		t.Fatalf("remote branch moved to %s", tip)
	}
	f.mu.Lock()
	calls := len(f.auth)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatalf("%d REST calls after a refused push, want none", calls)
	}
}

// TestHarvestRefusals: no token is no-token before anything runs; a branch
// outside nova/ and a sha the repo does not hold are push-refused.
func TestHarvestRefusals(t *testing.T) {
	t.Parallel()
	bare, work, sha := repos(t)
	_, srv := newForge(t)
	ctx := context.Background()

	req := request(bare, work, sha, srv.URL)
	req.Token = ""
	_, err := harvestcopy.Harvest(ctx, req)
	if !errors.Is(err, harvestcopy.ErrNoToken) || harvestcopy.Reason(err) != "no-token" || !strings.Contains(err.Error(), harvestcopy.TokenEnv) {
		t.Fatalf("no token: %v", err)
	}
	req = request(bare, work, sha, srv.URL)
	req.Branch = "feature/x"
	if _, err := harvestcopy.Harvest(ctx, req); !errors.Is(err, harvestcopy.ErrPushRefused) {
		t.Fatalf("branch outside nova/: %v", err)
	}
	req = request(bare, work, sha, srv.URL)
	req.SHA = strings.Repeat("ab", 20)
	if _, err := harvestcopy.Harvest(ctx, req); !errors.Is(err, harvestcopy.ErrPushRefused) {
		t.Fatalf("unknown sha: %v", err)
	}
	if tip, _ := exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output(); len(tip) != 0 {
		t.Fatalf("a refused harvest pushed %s", tip)
	}
}

// TestHarvestPROpenRefusedAndAlreadyOpen: a refused POST is pr-refused with
// GitHub's message (the push stands, so a retry finds the branch already
// there); a PR already open for the head is found by the lookup and none is
// opened; GitHub's 422 "already exists" after a lost reply is one more
// lookup, never a second PR.
func TestHarvestPROpenRefusedAndAlreadyOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		bare, work, sha := repos(t)
		f, srv := newForge(t)
		f.refuse, f.msg = http.StatusForbidden, "Resource not accessible by integration"
		res, err := harvestcopy.Harvest(ctx, request(bare, work, sha, srv.URL))
		if !errors.Is(err, harvestcopy.ErrPRRefused) || harvestcopy.Reason(err) != "pr-refused" ||
			!strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "Resource not accessible") {
			t.Fatalf("refused POST: %v", err)
		}
		if res.Push != "pushed" {
			t.Fatalf("the push before the refused PR: %+v", res)
		}
		if tip := git(t, bare, "rev-parse", "refs/heads/"+branch); tip != sha {
			t.Fatalf("remote branch at %s, want %s", tip, sha)
		}
	})
	t.Run("already open", func(t *testing.T) {
		t.Parallel()
		bare, work, sha := repos(t)
		f, srv := newForge(t)
		f.open["mas-bandwidth:"+branch] = 77
		res, err := harvestcopy.Harvest(ctx, request(bare, work, sha, srv.URL))
		if err != nil || res.PR != 77 || res.Open != "already" || res.Push != "pushed" || res.Head != sha {
			t.Fatalf("already open: %+v %v", res, err)
		}
		f.mu.Lock()
		n := len(f.bodies)
		f.mu.Unlock()
		if n != 0 {
			t.Fatalf("%d POSTs with a PR already open", n)
		}
	})
	t.Run("422 already exists", func(t *testing.T) {
		t.Parallel()
		bare, work, sha := repos(t)
		f, srv := newForge(t)
		f.refuse, f.msg = http.StatusUnprocessableEntity, "Validation Failed: A pull request already exists for mas-bandwidth:"+branch
		// the PR exists once the POST is refused: the lost-reply shape
		f.open["mas-bandwidth:"+branch] = 78
		res, err := harvestcopy.Harvest(ctx, harvestcopy.Request{
			RepoDir: work, SHA: sha, Branch: branch, Repo: "mas-bandwidth/nova-tools", Base: "dev",
			DoneWhen: "x", Token: "t", Remote: bare, API: srv.URL,
			HTTP: &http.Client{Transport: &skipFirstGet{next: http.DefaultTransport}},
		})
		if err != nil || res.PR != 78 || res.Open != "already" {
			t.Fatalf("422 already exists: %+v %v", res, err)
		}
	})
}

// skipFirstGet answers the first GET with an empty list so the harvest goes
// on to POST; every other request reaches the forge.
type skipFirstGet struct {
	mu   sync.Mutex
	done bool
	next http.RoundTripper
}

func (s *skipFirstGet) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	first := !s.done && r.Method == http.MethodGet
	if first {
		s.done = true
	}
	s.mu.Unlock()
	if !first {
		return s.next.RoundTrip(r)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}, Request: r}, nil
}
