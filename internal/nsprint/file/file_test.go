package file

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// goodBody is an issue body carrying every required line, well over 200 bytes.
const goodBody = `Evidence: three batches of issues were posted with a literal @path body today.

What: file issues through one verb that reads the file, lints it and reads it back.

DONE-WHEN: ` + "`go test ./internal/nsprint/file -run TestControl50File`" + ` passes.

PATHS: nova-tools: cmd/nova-sprint/file.go internal/nsprint/file/
BASE: dev
DEPENDS-ON: none
owner: rowan   reader: johnny   est: 30 min
`

// fakeGitHub is a local stand-in for the issues REST API. literal makes it
// store the literal string "@<path>" in place of the body it was sent, which
// is exactly what `gh api -f body=@file` did three times on 2026-09-23.
type fakeGitHub struct {
	mu       sync.Mutex
	literal  string
	posts    int
	issues   map[int]string
	comments map[int64]string
	lastAuth string
}

func newFake() *fakeGitHub {
	return &fakeGitHub{issues: map[int]string{}, comments: map[int64]string{}}
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAuth = r.Header.Get("Authorization")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// repos/<o>/<r>/issues[/<n>[/comments]] or repos/<o>/<r>/issues/comments/<id>
	if len(parts) < 4 || parts[0] != "repos" || parts[3] != "issues" {
		http.NotFound(w, r)
		return
	}
	rest := parts[4:]
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.posts++
		if f.literal != "" {
			in.Body = f.literal
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && len(rest) == 0:
		n := 100 + len(f.issues)
		f.issues[n] = in.Body
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"number":%d,"html_url":"http://%s/o/r/issues/%d"}`, n, r.Host, n)
	case r.Method == http.MethodGet && len(rest) == 1 && rest[0] != "comments":
		n, _ := strconv.Atoi(rest[0])
		body, ok := f.issues[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": n, "body": body})
	case r.Method == http.MethodPost && len(rest) == 2 && rest[1] == "comments":
		id := int64(9000 + len(f.comments))
		f.comments[id] = in.Body
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d,"html_url":"http://%s/o/r/issues/%s#issuecomment-%d"}`, id, r.Host, rest[0], id)
	case r.Method == http.MethodGet && len(rest) == 2 && rest[0] == "comments":
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		body, ok := f.comments[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "body": body})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

type run struct {
	code   int
	stdout string
	stderr string
}

func runFile(t *testing.T, fake *fakeGitHub, d Deps, args ...string) run {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	d.API = srv.URL
	d.HTTP = srv.Client()
	if d.Token == nil {
		d.Token = func() (string, error) { return "test-token", nil }
	}
	var out, errOut bytes.Buffer
	code := Main(context.Background(), args, &out, &errOut, d)
	return run{code: code, stdout: out.String(), stderr: errOut.String()}
}

func writeBody(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestControl50File is control 50 of #2756 (spec 4.11, nova-tools #3089): a
// body file whose text is a literal @path and a body with no DONE-WHEN are
// refused with nothing posted; a good body posts and reads back equal; a
// GitHub that stores the literal @path makes `file` exit 1.
func TestControl50File(t *testing.T) {
	t.Parallel()

	t.Run("literal @path body is refused, nothing posted", func(t *testing.T) {
		fake := newFake()
		p := writeBody(t, "@/private/tmp/claude-501/scratchpad/jev-issues/3.md\n"+strings.Repeat("x", 300))
		r := runFile(t, fake, Deps{}, "--repo", "o/r", "--title", "t", "--body-file", p)
		if r.code != 2 || fake.postCount() != 0 {
			t.Fatalf("code=%d posts=%d stderr=%q; want exit 2 and nothing posted", r.code, fake.postCount(), r.stderr)
		}
		if !strings.Contains(r.stderr, "starts with @") {
			t.Fatalf("stderr %q does not name the @ rule", r.stderr)
		}
	})

	t.Run("body with no DONE-WHEN is refused, nothing posted", func(t *testing.T) {
		fake := newFake()
		p := writeBody(t, strings.Replace(goodBody, "DONE-WHEN:", "Done when", 1))
		r := runFile(t, fake, Deps{}, "--repo", "o/r", "--title", "t", "--body-file", p)
		if r.code != 2 || fake.postCount() != 0 {
			t.Fatalf("code=%d posts=%d stderr=%q; want exit 2 and nothing posted", r.code, fake.postCount(), r.stderr)
		}
		if !strings.Contains(r.stderr, "DONE-WHEN") {
			t.Fatalf("stderr %q does not name the missing DONE-WHEN line", r.stderr)
		}
	})

	t.Run("good body posts and reads back equal", func(t *testing.T) {
		fake := newFake()
		p := writeBody(t, goodBody)
		r := runFile(t, fake, Deps{}, "--repo", "o/r", "--title", "nova-sprint file", "--body-file", p)
		if r.code != 0 {
			t.Fatalf("code=%d stderr=%q; want 0", r.code, r.stderr)
		}
		want := fmt.Sprintf("FILED o/r#100 len=%d\n", len(goodBody))
		if r.stdout != want {
			t.Fatalf("stdout %q, want %q", r.stdout, want)
		}
		if got := fake.issues[100]; got != goodBody {
			t.Fatalf("stored body differs from the file")
		}
		if fake.lastAuth != "Bearer test-token" {
			t.Fatalf("auth header %q", fake.lastAuth)
		}
	})

	t.Run("GitHub storing the literal @path exits 1", func(t *testing.T) {
		fake := newFake()
		p := writeBody(t, goodBody)
		fake.literal = "@" + p
		r := runFile(t, fake, Deps{}, "--repo", "o/r", "--title", "t", "--body-file", p)
		if r.code != 1 {
			t.Fatalf("code=%d stdout=%q stderr=%q; want exit 1 on a read-back that differs", r.code, r.stdout, r.stderr)
		}
		if !strings.Contains(r.stderr, "READBACK DIFFERS o/r#100") {
			t.Fatalf("stderr %q does not name the issue that differs", r.stderr)
		}
	})
}

func TestLintIssueRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"short", "What: x\nDONE-WHEN: y\n", "under 200"},
		{"leading space then @", "  \n@/tmp/x.md " + strings.Repeat("y", 300), "starts with @"},
		{"no PATHS", strings.Replace(goodBody, "PATHS:", "Paths are", 1), "PATHS:"},
		{"no BASE", strings.Replace(goodBody, "BASE: dev", "", 1), "BASE:"},
		{"no DEPENDS-ON", strings.Replace(goodBody, "DEPENDS-ON: none", "", 1), "DEPENDS-ON:"},
		{"no What", strings.Replace(goodBody, "What:", "So", 1), "What:"},
		{"empty DONE-WHEN", strings.Replace(goodBody, "DONE-WHEN: `go test", "DONE-WHEN:\n`go test", 1), "DONE-WHEN:"},
		{"no owner line", strings.Replace(goodBody, "owner: rowan   reader: johnny   est: 30 min", "", 1), "owner:"},
		{"no est", strings.Replace(goodBody, "   est: 30 min", "", 1), "est:"},
		{"reader is owner", strings.Replace(goodBody, "reader: johnny", "reader: rowan", 1), "reader must not be the owner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(LintIssue([]byte(c.body)), "; ")
			if !strings.Contains(got, c.want) {
				t.Fatalf("lint %q does not contain %q", got, c.want)
			}
		})
	}
	if got := LintIssue([]byte(goodBody)); len(got) != 0 {
		t.Fatalf("good body refused: %v", got)
	}
}

func TestCommentPostsAndReadsBack(t *testing.T) {
	t.Parallel()

	fake := newFake()
	body := "Read at head abc123: the change does what the DONE-WHEN says. " + strings.Repeat("Evidence line. ", 20)
	p := writeBody(t, body)
	r := runFile(t, fake, Deps{}, "--repo", "o/r", "--comment", "42", "--body-file", p)
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if want := fmt.Sprintf("COMMENTED o/r#42 comment=9000 len=%d\n", len(body)); r.stdout != want {
		t.Fatalf("stdout %q, want %q", r.stdout, want)
	}
	fake2 := newFake()
	fake2.literal = "@" + p
	r = runFile(t, fake2, Deps{}, "--repo", "o/r", "--comment", "42", "--body-file", p)
	if r.code != 1 {
		t.Fatalf("literal comment: code=%d; want 1", r.code)
	}
	fake3 := newFake()
	r = runFile(t, fake3, Deps{}, "--repo", "o/r", "--comment", "42", "--body-file", writeBody(t, "@"+p))
	if r.code != 2 || fake3.postCount() != 0 {
		t.Fatalf("@ comment: code=%d posts=%d; want 2 and nothing posted", r.code, fake3.postCount())
	}
}

// pushBody is goodBody made one invariant (#4396): the card --push-to queues.
const pushBody = goodBody + "INVARIANT: every issue is filed through one verb that reads it back.\nCLASS-TEST: TestControl50File\n"

func TestPushToQueuesTheBuildTask(t *testing.T) {
	t.Parallel()

	fake := newFake()
	var got task.PushRequest
	d := Deps{Push: func(_ context.Context, _ string, req task.PushRequest) (task.PushResult, error) {
		got = req
		return task.PushResult{Status: task.PushCreated}, nil
	}}
	p := writeBody(t, pushBody)
	r := runFile(t, fake, d, "--repo", "o/r", "--title", "nova-sprint file", "--body-file", p,
		"--push-to", "rowan", "--front", "--sprint", "v6", "--redis", "127.0.0.1:1")
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "PUSH CREATED id=build-r-100 to=rowan") {
		t.Fatalf("stdout %q has no PUSH line", r.stdout)
	}
	if got.To != "rowan" || !got.Front || got.Sprint != "v6" || got.Kind != task.KindWork || got.ID != "build-r-100" {
		t.Fatalf("push request %+v", got)
	}
	for _, want := range []string{"o/r#100 nova-sprint file", "| DONE-WHEN: `go test", "| PATHS: nova-tools: cmd/nova-sprint/file.go internal/nsprint/file/", "| DEPENDS-ON: none", "| reader: johnny"} {
		if !strings.Contains(got.Title, want) {
			t.Fatalf("title %q lacks %q", got.Title, want)
		}
	}
	// A push-to of a body that is not one invariant (no INVARIANT, no
	// CLASS-TEST) is refused before anything is posted or pushed (#4396).
	fake4, pushed := newFake(), 0
	d4 := Deps{Push: func(context.Context, string, task.PushRequest) (task.PushResult, error) {
		pushed++
		return task.PushResult{Status: task.PushCreated}, nil
	}}
	p4 := writeBody(t, goodBody)
	r = runFile(t, fake4, d4, "--repo", "o/r", "--title", "t", "--body-file", p4, "--push-to", "rowan", "--sprint", "v6", "--redis", "127.0.0.1:1")
	wantErr := `REFUSED card-lint rule=invariant-missing line="" remedy="add INVARIANT: <the one sentence the class test proves>" card=` + strconv.Quote(p4) + "\n" +
		`REFUSED card-lint rule=class-test-missing line="" remedy="add CLASS-TEST: Test<Name>, the one Go test that proves the invariant" card=` + strconv.Quote(p4) + "\n"
	if r.code != 2 || fake4.postCount() != 0 || pushed != 0 || r.stdout != "" || r.stderr != wantErr {
		t.Fatalf("push-to of a card that is not one invariant: code=%d posts=%d pushed=%d stdout=%q stderr\n%s\nwant\n%s", r.code, fake4.postCount(), pushed, r.stdout, r.stderr, wantErr)
	}
	// A push-to without a sprint is refused before anything is posted.
	fake2 := newFake()
	r = runFile(t, fake2, d, "--repo", "o/r", "--title", "t", "--body-file", p, "--push-to", "rowan")
	if r.code != 2 || fake2.postCount() != 0 {
		t.Fatalf("push-to without sprint: code=%d posts=%d", r.code, fake2.postCount())
	}
}

func TestFlagRefusals(t *testing.T) {
	t.Parallel()

	p := writeBody(t, goodBody)
	for _, args := range [][]string{
		{"--title", "t", "--body-file", p},
		{"--repo", "o/r", "--body-file", p},
		{"--repo", "o/r", "--title", "t"},
		{"--repo", "o/r", "--title", "t", "--body-file", "@" + p},
		{"--repo", "o/r", "--title", "@x", "--body-file", p},
		{"--repo", "bad repo", "--title", "t", "--body-file", p},
		{"--repo", "o/r", "--title", "t", "--comment", "5", "--body-file", p},
	} {
		fake := newFake()
		r := runFile(t, fake, Deps{}, args...)
		if r.code != 2 || fake.postCount() != 0 {
			t.Fatalf("args %q: code=%d posts=%d; want 2 and nothing posted", args, r.code, fake.postCount())
		}
	}
}
