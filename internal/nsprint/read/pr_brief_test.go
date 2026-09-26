package read_test

// Unit tests of `read brief --pr` and `read post --file` (nova-tools #4335,
// #4315): the parsing and the rendering, no Redis. The Redis-backed ends are
// in pr_brief_functional_test.go (-tags functional).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

func TestParseNumstat(t *testing.T) {
	t.Parallel()
	got := read.ParseNumstat("12\t3\tz/b.go\n-\t-\tassets/logo.png\n0\t9\ta.md\n\n")
	want := []read.FileStat{{Path: "a.md", Del: 9}, {Path: "assets/logo.png", Binary: true}, {Path: "z/b.go", Add: 12, Del: 3}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestIssueRefEveryShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, def, want string
	}{
		{"4315", "mas-bandwidth/nova-tools", "mas-bandwidth/nova-tools#4315"},
		{"#4315", "nova-tools", "mas-bandwidth/nova-tools#4315"},
		{"nova-tools#4321", "mas-bandwidth/nova-tools", "mas-bandwidth/nova-tools#4321"},
		{"rowan-tools#12", "someone/nova-tools", "mas-bandwidth/rowan-tools#12"},
		{"acme/widgets#7", "nova-tools", "acme/widgets#7"},
		{"https://github.com/acme/widgets/issues/9", "nova-tools", "acme/widgets#9"},
		{"issue:nova-tools#4227", "nova-tools", "mas-bandwidth/nova-tools#4227"}, // a card's ORIGIN
		{"https://github.com/acme/widgets/pull/9", "nova-tools", ""},
		{"issue", "nova-tools", ""},
		{"0", "nova-tools", ""},
		{"", "nova-tools", ""},
	} {
		o, n, num, ok := read.IssueRef(tc.in, tc.def)
		got := ""
		if ok {
			got = o + "/" + n + "#" + num
		}
		if got != tc.want {
			t.Errorf("IssueRef(%q, %q) = %q, want %q", tc.in, tc.def, got, tc.want)
		}
	}
}

func TestSpecLines(t *testing.T) {
	t.Parallel()
	body := "LABEL: x\nDO: build the brief\n  EVIDENCE: indented is prose\nEVIDENCE: readf.sh ran four gh calls\nRECEIPTS:\nKEEP: read post --line\n"
	got := strings.Join(read.SpecLines(body, read.CardSpecKeys), "|")
	if want := "DO: build the brief|EVIDENCE: readf.sh ran four gh calls|KEEP: read post --line"; got != want {
		t.Fatalf("SpecLines = %q, want %q", got, want)
	}
}

// briefFixture is a PRBrief as LoadPRBrief fills it for a card's PR with a
// red check on our CI and a red run on the GitHub leg.
func briefFixture() read.PRBrief {
	head := strings.Repeat("c", 40)
	return read.PRBrief{
		Owner: "mas-bandwidth", Repo: "nova-tools", N: "7",
		Record: map[string]string{"head": head, "base": "dev", "base_sha": strings.Repeat("b", 40), "state": "open",
			"stream": "swarm: cards", "task": "work-brief", "paths": "internal/x"},
		Lines:   []string{"JEV who=jev head=" + head + " score=9/10"},
		CardKey: "task:work-brief",
		Card: map[string]string{"title": "read brief in one call", "paths": "internal/x", "done_when": "the card's DONE-WHEN",
			"depends_on": "none", "source": "nova-tools#4335", "body": "DO: build it\nEVIDENCE: readf.sh\n"},
		IssueRef: "mas-bandwidth/nova-tools#4335", IssueTitle: "read brief: issue, PR body, diff stat and CI state",
		IssueBody: "BUILD: one verb\n\nDONE-WHEN: a cold reader scores from one command's output",
		PRTitle:   "read brief --pr", PRBody: "STREAM: swarm: cards\nDONE-WHEN: the card's DONE-WHEN\n",
		Mirror: "/m/nova-tools.git", DiffBase: strings.Repeat("b", 40),
		Files:   []read.FileStat{{Path: "README.md", Add: 1}, {Path: "internal/x/x.go", Add: 40, Del: 2}},
		Outside: []string{"README.md"},
		CI: ci.Rows{Key: "ci:nova-tools:" + head, Found: true, Fields: map[string]string{"ci": "red", "attempt": "1", "bench": "studio"},
			Rows: []ci.Row{{Check: "go-build", State: "green", RC: "0", WallMS: "900"},
				{Check: "go-test-internal", State: "red", RC: "1", WallMS: "40000", Log: "/logs/t.log", Fail: "--- FAIL: TestX (0.01s)"}},
			GH: webhook.Record{Found: true, Word: "red", Fail: "check:lint", Runs: []webhook.Run{{Kind: "check", Name: "lint", Word: "red"}, {Kind: "wf", Name: "ci", Word: "green"}}}},
		Sources: []string{"record=redis:pr:nova-tools:7"},
	}
}

// TestRenderPRCarriesEverySection is #4335's DONE-WHEN on the screen: the
// issue, the card, DONE-WHEN from each source, the PR body, the files with
// +/-, the files outside PATHS, the check rollup with the failing names and
// the rubric, all in the one output.
func TestRenderPRCarriesEverySection(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	read.RenderPR(&b, briefFixture())
	out := b.String()
	for _, want := range []string{
		"READ BRIEF mas-bandwidth/nova-tools#7 head=" + strings.Repeat("c", 40) + " base=dev",
		"SOURCES record=redis:pr:nova-tools:7",
		"== ISSUE mas-bandwidth/nova-tools#4335: read brief: issue, PR body, diff stat and CI state\nBUILD: one verb",
		"== CARD task:work-brief\nTITLE: read brief in one call\n",
		"SOURCE: nova-tools#4335", "PATHS: internal/x", "DO: build it", "EVIDENCE: readf.sh",
		"issue: a cold reader scores from one command's output", "card: the card's DONE-WHEN",
		"== PR #7: read brief --pr\nSTREAM: swarm: cards",
		"== FILES 2 (+41 -2) bbbbbbbb..cccccccc outside PATHS 1",
		"    +1     -0  README.md", "   +40     -2  internal/x/x.go", "OUTSIDE PATHS: README.md",
		"patch: git -C /m/nova-tools.git diff ",
		"ours red attempt=1 bench=studio",
		"  go-build green rc=0 wall_ms=900\n",
		"  go-test-internal red rc=1 wall_ms=40000 fail=--- FAIL: TestX (0.01s) log=/logs/t.log",
		"github red fail=check:lint", "  check:lint red", "FAILING: go-test-internal check:lint",
		"== LINES 1\n- JEV who=jev",
		"== RUBRIC", "only tens land on product code", "nova-sprint read post --repo nova-tools --n 7",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("brief lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "READ BRIEF GAP") {
		t.Fatalf("a complete brief printed a GAP:\n%s", out)
	}
}

// TestRenderPRPrintsEveryGap: a section that could not be read is a GAP
// line, never an empty section passed off as complete.
func TestRenderPRPrintsEveryGap(t *testing.T) {
	t.Parallel()
	b := briefFixture()
	b.Gaps = []string{"issue: GET /repos/mas-bandwidth/nova-tools/issues/4335: github 404", "files: mirror /m has no commit c"}
	var w strings.Builder
	read.RenderPR(&w, b)
	for _, g := range b.Gaps {
		if !strings.Contains(w.String(), "READ BRIEF GAP mas-bandwidth/nova-tools#7 "+g+"\n") {
			t.Fatalf("gap %q not printed:\n%s", g, w.String())
		}
	}
}

// TestGitHubTitleBodyIsOneGet: the REST read is one GET with the bearer
// token and counts itself; a non-200 is an error naming the path.
func TestGitHubTitleBodyIsOneGet(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer t-1" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/repos/mas-bandwidth/nova-tools/issues/4335":
			_, _ = io.WriteString(w, `{"title":"read brief","body":"DONE-WHEN: one command"}`)
		case "/repos/mas-bandwidth/nova-tools/pulls/7":
			_, _ = io.WriteString(w, `{"title":"a hand PR","body":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	g := &read.GitHub{BaseURL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	ctx := context.Background()
	title, body, err := g.TitleBody(ctx, read.Endpoint("issues", "mas-bandwidth", "nova-tools", "4335"))
	if err != nil || title != "read brief" || body != "DONE-WHEN: one command" {
		t.Fatalf("issue = %q %q %v", title, body, err)
	}
	if title, body, err := g.TitleBody(ctx, read.Endpoint("pulls", "mas-bandwidth", "nova-tools", "7")); err != nil || title != "a hand PR" || body != "" {
		t.Fatalf("pull = %q %q %v", title, body, err)
	}
	if _, _, err := g.TitleBody(ctx, read.Endpoint("issues", "mas-bandwidth", "nova-tools", "1")); err == nil || !strings.Contains(err.Error(), "/issues/1: github 404") {
		t.Fatalf("404 err = %v", err)
	}
	if g.Calls() != 3 || calls.Load() != 3 {
		t.Fatalf("calls counted %d, served %d, want 3", g.Calls(), calls.Load())
	}
	var none *read.GitHub
	if none.Calls() != 0 {
		t.Fatal("a nil GitHub (--no-github) counts calls")
	}
}

func TestParseRowsRefusesEveryBadRow(t *testing.T) {
	t.Parallel()
	in := "# scores\n\nnova-tools\t7\tSCORE who=a head=abcdef1 score=9/10\\n1. item\n" +
		"nova-tools\t7\n" +
		"no such/repo/x\t7\tSCORE who=a head=abcdef1 score=9/10\n" +
		"mas-bandwidth/rowan-tools\tx\tSCORE who=a head=abcdef1 score=9/10\n" +
		"rowan-tools\t3\t   \n" +
		"acme/widgets\t12\tHOLD who=b head=1234567 score=5/10\n"
	rows, bad, err := read.ParseRows(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Num != 3 || rows[0].Repo != "nova-tools" || rows[0].Owner != "mas-bandwidth" || rows[0].N != "7" ||
		rows[0].Line != "SCORE who=a head=abcdef1 score=9/10\n1. item" || rows[1].Num != 8 || rows[1].Owner != "acme" || rows[1].Repo != "widgets" {
		t.Fatalf("rows %+v", rows)
	}
	want := []string{"ROW 4 READ POST REFUSED why=want <repo>\\t<n>\\t<typed line>, got 2", "ROW 5 READ POST REFUSED why=repo",
		"ROW 6 READ POST REFUSED repo=rowan-tools why=n \"x\"", "ROW 7 READ POST REFUSED repo=rowan-tools n=3 why=empty line"}
	if len(bad) != len(want) {
		t.Fatalf("bad %q", bad)
	}
	for i, w := range want {
		if !strings.HasPrefix(bad[i], w) {
			t.Fatalf("bad[%d] = %q, want prefix %q", i, bad[i], w)
		}
	}
}

// TestPostFileOneReceiptPerRowNeverSilent is #4335's DONE-WHEN for the
// batch: every row prints its receipt or its refusal under ROW <i>, a post
// that prints nothing is refused, and the tally counts both.
func TestPostFileOneReceiptPerRowNeverSilent(t *testing.T) {
	t.Parallel()
	in := "nova-tools\t1\tSCORE who=a head=abcdef1 score=9/10\n" +
		"nova-tools\t2\tSCORE who=a head=abcdef2 score=4/10\n" +
		"nova-tools\t3\tSCORE who=a head=abcdef3 score=8/10\n" +
		"nova-tools\tbad\tSCORE who=a head=abcdef4 score=8/10\n"
	var posted []string
	post := func(row read.Row, stdout, stderr io.Writer) int {
		posted = append(posted, row.N)
		switch row.N {
		case "1":
			_, _ = io.WriteString(stdout, "READ POST repo=nova-tools n=1 kind=SCORE lines=1 github_calls=1 comment=9\n")
			return 0
		case "2":
			_, _ = io.WriteString(stderr, "READ POST REFUSED repo=nova-tools n=2 kind=SCORE why=gate ci typed ok measured red; nothing stored\n")
			return 1
		}
		return 0 // row 3: exit 0 and no receipt
	}
	var out, errb strings.Builder
	code := read.PostFile(strings.NewReader(in), "scores.tsv", post, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1 (rows refused)", code)
	}
	if strings.Join(posted, ",") != "1,2,3" {
		t.Fatalf("posted rows %v, want 1,2,3 (the bad row never reaches the store)", posted)
	}
	for _, want := range []string{"ROW 1 READ POST repo=nova-tools n=1 kind=SCORE", "READ POST FILE file=scores.tsv rows=4 posted=1 refused=3 github_calls=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out.String())
		}
	}
	for _, want := range []string{"ROW 2 READ POST REFUSED repo=nova-tools n=2", "ROW 3 READ POST REFUSED repo=nova-tools n=3 why=the post printed no receipt", "ROW 4 READ POST REFUSED"} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errb.String())
		}
	}
}
