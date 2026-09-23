package swarm_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// nova-tools#1728: SPEC-TOOLWORK section 5's card header and WORKER-CARDS practice 17
// contradicted each other, so no card in the spec's shape could be admitted by a swarm.
// Stella's ruling keeps the admission bound (STEP 1 in the first fifteen lines) and moves
// the fix to the header's order -- contract, optional role, the hashed typed lines, then
// STEP 1 -- and requires the cutter's output to pass the same production shape check
// before any card is written. These are that ruling's controls, end to end: the real
// cutter (pulse.Cut, the `nova-pulse cut --pool` path) renders the shipped fix-red
// template and the real admission (readCards, through swarm.AdmitCards) reads its output.

// cutTemplates is the shipped testdata templates directory the cut verb's own help names.
const cutTemplates = "../../cmd/nova-pulse/testdata/templates"

// publicRepos stands an in-process host in for github's, so admission's repository probe
// answers "public" without a real host (CI-NET).
func publicRepos(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NOVA_SWARM_PROBE_BASE", srv.URL)
}

// cutOne runs the cutter over one pool row against a templates directory and returns its
// exit code, its stderr, and the out directory.
func cutOne(t *testing.T, templates, template string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool.tsv")
	row := "mas-bandwidth/nova-tools\tfixred-1728\tfix\treconcile the card header\t" + template + "\n"
	if err := os.WriteFile(pool, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "cards")
	var stdout, stderr bytes.Buffer
	code := pulse.Cut(pulse.CutInput{
		Pool: pool, Templates: templates, Out: out, Root: filepath.Join(dir, "root"),
		Stdout: &stdout, Stderr: &stderr,
	})
	return code, stderr.String(), out
}

// TestIssue1728ACardCutRendersIsAdmitted is SPEC-TOOLWORK section 5's class test
// `a-card-cut-renders-is-admitted`: a section-5 shaped fix-red card (eight STEP lines,
// MODE: explore with a TURNS budget in the hashed header) rendered by the cutter is
// admitted by readCards on a DeepSeek-family route, and the fifteen-line bound is unchanged.
func TestIssue1728ACardCutRendersIsAdmitted(t *testing.T) {
	publicRepos(t)
	code, stderr, out := cutOne(t, cutTemplates, "fix-red")
	if code != 0 {
		t.Fatalf("the cutter renders the shipped fix-red template, exit %d: %s", code, stderr)
	}
	card, err := os.ReadFile(filepath.Join(out, "fixred-1728.md"))
	if err != nil {
		t.Fatalf("the cutter wrote the card: %v", err)
	}
	lines := strings.Split(string(card), "\n")
	step1 := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "STEP 1") {
			step1 = i + 1
			break
		}
	}
	if step1 < 1 || step1 > 15 {
		t.Fatalf("STEP 1 sits inside the first fifteen lines, got line %d", step1)
	}
	for _, key := range []string{"KIND: ", "PATHS: ", "TEST: ", "LEGS: ", "SOURCE: ", "MODE: explore", "TURNS: "} {
		at := -1
		for i, ln := range lines[:step1-1] {
			if strings.HasPrefix(ln, key) {
				at = i
			}
		}
		if at < 1 {
			t.Fatalf("the typed header line %q sits between line 1 and STEP 1 (inside the hash)", key)
		}
	}
	for _, key := range []string{"LANE: ", "ACCEPT: ", "CERT: "} {
		for i, ln := range lines {
			if strings.HasPrefix(ln, key) && i+1 < step1 {
				t.Fatalf("%q is prose for the reader and sits below STEP 1, got line %d", key, i+1)
			}
		}
	}

	got, err := swarm.AdmitCards(filepath.Join(out, "cards.tsv"))
	if err != nil {
		t.Fatalf("admission reads the cutter's cards.tsv: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("admission read %d cards, want 1", len(got))
	}
	if !strings.HasPrefix(got[0].Model, "opencode/") && !strings.HasPrefix(got[0].Model, "deepseek/") {
		t.Fatalf("the card routes to a DeepSeek-family model, so the shape check applies; got %q", got[0].Model)
	}
	if got[0].Why != "" {
		t.Fatalf("a SPEC-TOOLWORK section 5 shaped fix-red card rendered by the cutter is admitted, got %q", got[0].Why)
	}

	// The bound is unchanged: the same card with its STEP lines pushed to line 16 is refused.
	pushed := lines[0] + "\n" + strings.Repeat("LANE: work\n", 15-step1+1) + strings.Join(lines[1:], "\n")
	if why := swarm.CardShapeRefusal(pushed); !strings.Contains(why, "first 15 lines") {
		t.Fatalf("a card whose STEP 1 is line 16 is still refused under the fifteen-line bound, got %q", why)
	}
}

// TestIssue1728CutRefusesACardAdmissionWouldRefuse: the cutter runs admission's shape check
// on every card it renders before writing it. Each template here passes the cutter's own
// rules on dev (STEP 1 within its twenty lines; eight steps under its twenty-turn budget)
// and is refused at admission, so dev cut them and the swarm abstained them at in=0.
func TestIssue1728CutRefusesACardAdmissionWouldRefuse(t *testing.T) {
	head := "RESULT: <label> sha=<sha12>\nYou are a worker. The deadline is the machinery's.\n"
	step1 := "STEP 1. mkdir -p scratch && git clone -q https://example.com/<source>.git . && git checkout -b <branch>\n"
	tail := "STEP 2. Fix it: report the red line, then the green line.\nSTEP 3. Write RESULT.md with line 1 equal to this card's line 1.\n"
	eight := "STEP 2. Read.\nSTEP 3. Red test: the red line.\nSTEP 4. Fix: the green line.\nSTEP 5. Revert.\nSTEP 6. Gates.\nSTEP 7. Commit.\nSTEP 8. Write RESULT.md.\n"
	cases := []struct{ name, tmpl, want string }{
		{
			// Draft 5's shape: a coordinator's header above the steps, STEP 1 at line 17.
			name: "draft5-header",
			tmpl: head + "KIND: fix-red\nPATHS: a.go\nTEST: ./x TestX\nLEGS: go\nSOURCE: <source>#1728\n" +
				"BASE: dev\nSPEC: docs/SPEC-TOOLWORK.md\nROUTE: pro\nACCEPT: gate\nCERT: standard\nLANE: work\n" +
				"RULES: unattended.\nNever ask a question.\nOne command per line.\n" + step1 + tail,
			want: "first 15 lines",
		},
		{
			// A fix-red card's eight steps with no MODE: explore in its header.
			name: "eight-steps-no-mode",
			tmpl: head + "KIND: fix-red\n" + step1 + eight,
			want: "MODE: explore",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range []string{"benches.tsv"} {
				raw, err := os.ReadFile(filepath.Join(cutTemplates, f))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, f), raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte(tc.tmpl), 0o644); err != nil {
				t.Fatal(err)
			}
			code, stderr, out := cutOne(t, dir, "bad")
			if code != 2 {
				t.Fatalf("the cutter refuses a card admission would refuse, exit %d, stderr %q", code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("the refusal names admission's reason %q, got %q", tc.want, stderr)
			}
			if _, err := os.Stat(filepath.Join(out, "fixred-1728.md")); err == nil {
				t.Fatal("no card is written before the shape check passes")
			}
		})
	}
}
