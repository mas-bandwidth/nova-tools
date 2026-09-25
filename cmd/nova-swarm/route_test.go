package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRouteServer runs the handler over httptest, skipping where the sandbox
// forbids listening sockets. The refusal test below runs socket-free
// everywhere, so a skip loses no signal here.
func fakeRouteServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); refusal test pins the verb", err)
	}
	ln.Close()
	return httptest.NewServer(handler)
}

func writeRouteFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func routeHandler(t *testing.T, answers string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body struct {
			State     string                    `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %v", err)
		} else {
			if len(body.State) != 1500 {
				t.Errorf("state length = %d, want 1500 (the card's first 1500 characters)", len(body.State))
			}
			for _, q := range []string{"kind", "complexity", "needs_strong", "touches_private"} {
				if body.Questions[q] == nil {
					t.Errorf("questions missing %q", q)
				}
			}
			if body.Questions["kind"]["type"] != "choice" ||
				body.Questions["complexity"]["type"] != "score" ||
				body.Questions["needs_strong"]["type"] != "noul" ||
				body.Questions["touches_private"]["type"] != "noul" {
				t.Errorf("question types wrong: kind=%v complexity=%v needs_strong=%v touches_private=%v",
					body.Questions["kind"]["type"], body.Questions["complexity"]["type"],
					body.Questions["needs_strong"]["type"], body.Questions["touches_private"]["type"])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(answers))
	}
}

// A fix/complexity-1 card routes to the muse row.
func TestRouteFixComplexity1PicksMuse(t *testing.T) {
	srv := fakeRouteServer(t, routeHandler(t, `{"answers": {
		"kind": {"type":"choice","choice":"fix","probabilities":{"fix":0.95},"confidence":0.95},
		"complexity": {"type":"score","score":1.0,"confidence":0.9},
		"needs_strong": {"type":"noul","noul":0.1},
		"touches_private": {"type":"noul","noul":0.1}
	}, "usage": {"input_tokens": 1, "output_tokens": 1}}`))
	defer srv.Close()

	dir := t.TempDir()
	muse := filepath.Join(dir, "muse.json")
	os.WriteFile(muse, []byte(`{}`), 0o600)
	other := filepath.Join(dir, "other.json")
	os.WriteFile(other, []byte(`{}`), 0o600)
	card := writeRouteFile(t, "card.md", strings.Repeat("fix the named bug with a red test first. ", 60))
	routes := writeRouteFile(t, "routes.tsv", "fix\t1\t"+muse+"\tpublic\nfix\t2\t"+other+"\tpublic\n")
	t.Setenv("JEV_API_KEY", "sekret")
	exit, stdout, stderr := runSwarm(t, "route", "--card", card, "--routes", routes, "--base-url", srv.URL)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "kind=fix") {
		t.Fatalf("output missing kind=fix: %q", stdout)
	}
	if !strings.Contains(stdout, "worker="+muse) {
		t.Fatalf("output missing worker=%s: %q", muse, stdout)
	}
	if !strings.Contains(stdout, "ROUTE card=") {
		t.Fatalf("output missing ROUTE card=: %q", stdout)
	}
}

// The same card with touches_private 0.9 skips the public row and lands on
// the paid row: contributor routes are forbidden for private material.
func TestRoutePrivateSkipsPublic(t *testing.T) {
	srv := fakeRouteServer(t, routeHandler(t, `{"answers": {
		"kind": {"type":"choice","choice":"fix","probabilities":{"fix":0.95},"confidence":0.95},
		"complexity": {"type":"score","score":1.0,"confidence":0.9},
		"needs_strong": {"type":"noul","noul":0.1},
		"touches_private": {"type":"noul","noul":0.9}
	}, "usage": {"input_tokens": 1, "output_tokens": 1}}`))
	defer srv.Close()

	dir := t.TempDir()
	muse := filepath.Join(dir, "muse.json")
	os.WriteFile(muse, []byte(`{}`), 0o600)
	paid := filepath.Join(dir, "paid.json")
	os.WriteFile(paid, []byte(`{}`), 0o600)
	card := writeRouteFile(t, "card.md", strings.Repeat("fix the named bug with a red test first. ", 60))
	routes := writeRouteFile(t, "routes.tsv", "fix\t1\t"+muse+"\tpublic\nfix\t1\t"+paid+"\tpaid\n")
	t.Setenv("JEV_API_KEY", "sekret")
	exit, stdout, stderr := runSwarm(t, "route", "--card", card, "--routes", routes, "--base-url", srv.URL)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if strings.Contains(stdout, "worker="+muse) {
		t.Fatalf("private card must skip the public row: %q", stdout)
	}
	if !strings.Contains(stdout, "worker="+paid) {
		t.Fatalf("output missing worker=%s: %q", paid, stdout)
	}
}

// Kind confidence below the floor yields worker=default and exit 3.
func TestRouteBelowFloorExits3(t *testing.T) {
	srv := fakeRouteServer(t, routeHandler(t, `{"answers": {
		"kind": {"type":"choice","choice":"fix","probabilities":{"fix":0.5},"confidence":0.5},
		"complexity": {"type":"score","score":1.0,"confidence":0.9},
		"needs_strong": {"type":"noul","noul":0.1},
		"touches_private": {"type":"noul","noul":0.1}
	}, "usage": {"input_tokens": 1, "output_tokens": 1}}`))
	defer srv.Close()

	dir := t.TempDir()
	muse := filepath.Join(dir, "muse.json")
	os.WriteFile(muse, []byte(`{}`), 0o600)
	def := filepath.Join(dir, "default.json")
	os.WriteFile(def, []byte(`{}`), 0o600)
	card := writeRouteFile(t, "card.md", strings.Repeat("fix the named bug with a red test first. ", 60))
	routes := writeRouteFile(t, "routes.tsv", "fix\t1\t"+muse+"\tpublic\n")
	t.Setenv("JEV_API_KEY", "sekret")
	exit, stdout, stderr := runSwarm(t, "route", "--card", card, "--routes", routes, "--default", def, "--base-url", srv.URL)
	if exit != 3 {
		t.Fatalf("exit = %d, want 3 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "worker="+def) {
		t.Fatalf("below-floor output missing worker=%s: %q", def, stdout)
	}
	if !strings.Contains(stdout, "below=kind") && !strings.Contains(stdout, "below=kind,") && !strings.Contains(stdout, ",kind") {
		t.Fatalf("below-floor output must name kind: %q", stdout)
	}
}

// A missing routes row falls to the nearest lower complexity for that kind.
func TestRouteFallsToLowerComplexity(t *testing.T) {
	srv := fakeRouteServer(t, routeHandler(t, `{"answers": {
		"kind": {"type":"choice","choice":"feat","probabilities":{"feat":0.95},"confidence":0.95},
		"complexity": {"type":"score","score":3.0,"confidence":0.9},
		"needs_strong": {"type":"noul","noul":0.1},
		"touches_private": {"type":"noul","noul":0.1}
	}, "usage": {"input_tokens": 1, "output_tokens": 1}}`))
	defer srv.Close()

	dir := t.TempDir()
	low := filepath.Join(dir, "low.json")
	os.WriteFile(low, []byte(`{}`), 0o600)
	mid := filepath.Join(dir, "mid.json")
	os.WriteFile(mid, []byte(`{}`), 0o600)
	card := writeRouteFile(t, "card.md", strings.Repeat("add a verb with tests. ", 100))
	routes := writeRouteFile(t, "routes.tsv", "feat\t1\t"+low+"\tpublic\nfeat\t2\t"+mid+"\tpublic\n")
	t.Setenv("JEV_API_KEY", "sekret")
	exit, stdout, stderr := runSwarm(t, "route", "--card", card, "--routes", routes, "--base-url", srv.URL)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "worker="+mid) {
		t.Fatalf("complexity 3 with no row must fall to the feat/2 row %s: %q", mid, stdout)
	}
}

// Socket-free: the routes table picks exact, skips public for private
// material, falls to the nearest lower complexity, else the default.
func TestRouteTablePicksExactAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	muse := filepath.Join(dir, "muse.json")
	paid := filepath.Join(dir, "paid.json")
	mid := filepath.Join(dir, "mid.json")
	def := filepath.Join(dir, "default.json")
	routes := writeRouteFile(t, "routes.tsv",
		"fix\t1\t"+muse+"\tpublic\nfix\t1\t"+paid+"\tpaid\nfeat\t1\t"+muse+"\tpublic\nfeat\t2\t"+mid+"\tpublic\n")
	rows, err := parseRoutes(routes)
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	if got := pickRoute(rows, "fix", 1, 0.1, def); got != muse {
		t.Fatalf("fix/1 public card picked %q, want %q", got, muse)
	}
	if got := pickRoute(rows, "fix", 1, 0.9, def); got != paid {
		t.Fatalf("private fix/1 card picked %q, want paid %q", got, paid)
	}
	if got := pickRoute(rows, "feat", 3, 0.1, def); got != mid {
		t.Fatalf("feat/3 with no row picked %q, want lower %q", got, mid)
	}
	if got := pickRoute(rows, "port", 4, 0.1, def); got != def {
		t.Fatalf("unknown kind picked %q, want default %q", got, def)
	}
}

// Socket-free: the verb asks the four documented questions.
func TestRouteQuestionsHaveFourKinds(t *testing.T) {
	qs := routeQuestions()
	kind, ok := qs["kind"]
	if !ok || kind.Choice["fix"] == "" || kind.Choice["probe"] == "" || kind.Choice["feat"] == "" || kind.Choice["port"] == "" {
		t.Fatalf("kind question must be a choice with probe/read/spec/fix/feat/port: %+v", kind)
	}
	comp, ok := qs["complexity"]
	if !ok || len(comp.Score) != 4 {
		t.Fatalf("complexity question must be a score of 4 levels: %+v", comp)
	}
	if !qs["needs_strong"].Noul || !qs["touches_private"].Noul {
		t.Fatalf("needs_strong and touches_private must be noul questions")
	}
}
func TestRouteRefusesWithoutRoutes(t *testing.T) {
	card := writeRouteFile(t, "card.md", "fix the named bug\n")
	exit, _, stderr := runSwarm(t, "route", "--card", card)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", exit, stderr)
	}
	if !strings.Contains(stderr, "--routes is required") {
		t.Fatalf("the refusal does not name --routes: %q", stderr)
	}
}
