package pulse

// Red tests for the launched route (SPEC-DECIDE rule 8, issue #896): with --routes,
// nova-pulse launch picks each card's worker by the shared typed decision, groups the cards
// by worker and runs one batch per worker, and logs one ROUTE line per card in
// <queue>/ROUTES.log beside the label and the time. Below the floor the card keeps its own
// model as the default worker and the line says so. The decision is a fake httptest server,
// never the network.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// routeFake answers the four route questions from the card's state: a card whose text
// carries MARK-feat is a feat, MARK-lowconf is a fix under the floor, everything else is a
// confident fix.
func routeFake(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(raw, &body)
		kind, conf := "fix", 0.95
		switch {
		case strings.Contains(body.State, "MARK-feat"):
			kind = "feat"
		case strings.Contains(body.State, "MARK-lowconf"):
			kind, conf = "fix", 0.5
		}
		resp := map[string]any{
			"answers": map[string]any{
				"kind":            map[string]any{"type": "choice", "choice": kind, "probabilities": map[string]float64{kind: conf}, "confidence": conf},
				"complexity":      map[string]any{"type": "score", "score": 1.0, "confidence": 0.9},
				"needs_strong":    map[string]any{"type": "noul", "noul": 0.1},
				"touches_private": map[string]any{"type": "noul", "noul": 0.1},
			},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// writeRoutedCards writes a cards.tsv whose cards all carry the same model column (the
// below-floor default), one marker per card, and returns the cards.tsv path and labels.
func writeRoutedCards(t *testing.T, root string, models, markers []string) (string, []string) {
	t.Helper()
	dir := filepath.Join(root, "src")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	labels := make([]string, len(markers))
	var sb strings.Builder
	for i, marker := range markers {
		labels[i] = "card-" + string(rune('a'+i))
		path := filepath.Join(dir, labels[i])
		text := "RESULT " + labels[i] + " sha=000000000000\nfix the named bug with a red test first " + marker + "\n"
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		sb.WriteString(labels[i] + "\t-\t" + models[i] + "\t" + path + "\n")
	}
	cards := filepath.Join(root, "cards.tsv")
	if err := os.WriteFile(cards, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return cards, labels
}

// writeRoutes writes a routes.tsv with one fix/1 and one feat/1 row and returns the path and
// the two worker paths.
func writeRoutes(t *testing.T, root, fixW, featW string) string {
	t.Helper()
	for _, w := range []string{fixW, featW} {
		if err := os.WriteFile(w, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	routes := filepath.Join(root, "routes.tsv")
	body := "fix\t1\t" + fixW + "\tpublic\nfeat\t1\t" + featW + "\tpublic\n"
	if err := os.WriteFile(routes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return routes
}

func routeTime() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }

// tsvArg returns the value of a flag's argument from one recorded argv line.
func tsvArg(t *testing.T, line, flag string) string {
	t.Helper()
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == flag && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	t.Fatalf("no %s in %q", flag, line)
	return ""
}

// two cards routed to two workers land in the one card-form batch and make two ROUTE lines.
func TestLaunchRoutesCardsToTwoWorkersTwoBatches(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	srv := routeFake(t)
	defer srv.Close()
	t.Setenv("JEV_API_KEY", "sekret")

	workers := filepath.Join(root, "workers")
	if err := os.MkdirAll(workers, 0o755); err != nil {
		t.Fatal(err)
	}
	fixW := filepath.Join(workers, "fix.json")
	featW := filepath.Join(workers, "feat.json")
	routes := writeRoutes(t, root, fixW, featW)
	cards, _ := writeRoutedCards(t, root, []string{"default-worker", "default-worker"}, []string{"", "MARK-feat"})

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Routes: routes, Floor: 0.9, KeyEnv: "JEV_API_KEY", BaseURL: srv.URL,
		Now: routeTime,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	id := pulseID(t, out)

	// The card form admits every routed card in one batch, and the admitted cards.tsv
	// names the worker each card was routed to.
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("one card-form batch carries both routed cards, got %d:\n%s", len(lines), raw)
	}
	admitted, err := os.ReadFile(tsvArg(t, lines[0], "--cards"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{fixW, featW} {
		if !strings.Contains(string(admitted), "\t"+w+"\t") {
			t.Fatalf("the admitted cards.tsv does not name worker %s:\n%s", w, admitted)
		}
	}
	// pulses/<id>.tsv records the admission.
	if _, err := os.Stat(filepath.Join(root, "pulses", id+".tsv")); err != nil {
		t.Fatalf("the pulse record was not written: %v", err)
	}

	// ROUTES.log: one line per card, worker= the chosen route.
	logRaw, err := os.ReadFile(filepath.Join(root, RoutesLogFile))
	if err != nil {
		t.Fatal(err)
	}
	logLines := strings.Split(strings.TrimSpace(string(logRaw)), "\n")
	if len(logLines) != 2 {
		t.Fatalf("ROUTES.log wants one line per card, got %d:\n%s", len(logLines), logRaw)
	}
	for _, w := range []string{fixW, featW} {
		if !strings.Contains(string(logRaw), "worker="+w) {
			t.Fatalf("ROUTES.log does not name worker %s:\n%s", w, string(logRaw))
		}
	}
	if !strings.Contains(string(logRaw), "card=card-a") || !strings.Contains(string(logRaw), "card=card-b") {
		t.Fatalf("ROUTES.log does not name both card labels:\n%s", string(logRaw))
	}
	if !strings.Contains(string(logRaw), routeTime().Format(time.RFC3339)) {
		t.Fatalf("ROUTES.log does not carry the time:\n%s", string(logRaw))
	}
}

// a below-floor card lands on its own model, the default, and the line says so.
func TestLaunchBelowFloorLandsOnDefaultWorker(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	srv := routeFake(t)
	defer srv.Close()
	t.Setenv("JEV_API_KEY", "sekret")

	workers := filepath.Join(root, "workers")
	if err := os.MkdirAll(workers, 0o755); err != nil {
		t.Fatal(err)
	}
	fixW := filepath.Join(workers, "fix.json")
	featW := filepath.Join(workers, "feat.json")
	routes := writeRoutes(t, root, fixW, featW)
	defaultW := filepath.Join(workers, "default.json")
	cards, _ := writeRoutedCards(t, root, []string{defaultW}, []string{"MARK-lowconf"})

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Routes: routes, Floor: 0.9, KeyEnv: "JEV_API_KEY", BaseURL: srv.URL,
		Now: routeTime,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	id := pulseID(t, out)

	// The admitted card carries the default worker as its model column, never the fix
	// row's worker.
	if _, err := os.Stat(filepath.Join(root, "pulses", id+".tsv")); err != nil {
		t.Fatalf("the pulse record was not written: %v", err)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := os.ReadFile(tsvArg(t, strings.TrimSpace(string(raw)), "--cards"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(admitted), "\t"+defaultW+"\t") {
		t.Fatalf("the below-floor card must land on the default worker %s:\n%s", defaultW, admitted)
	}
	if strings.Contains(string(admitted), fixW) {
		t.Fatalf("the below-floor card must not take the routed worker %s:\n%s", fixW, admitted)
	}

	logRaw, err := os.ReadFile(filepath.Join(root, RoutesLogFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logRaw), "worker="+defaultW) {
		t.Fatalf("ROUTES.log must name the default worker:\n%s", logRaw)
	}
	if !strings.Contains(string(logRaw), "below=kind") {
		t.Fatalf("the below-floor line must say so (below=kind):\n%s", logRaw)
	}
}
