package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/verbout"
)

func parseJSON(t *testing.T, raw string) *verbout.Value {
	t.Helper()
	var v verbout.Value
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("cannot unmarshal json: %s\nraw output:\n%s", err, raw)
	}
	return &v
}

func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "example-bench")
	root, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestTokensFoldJSON(t *testing.T) {
	t.Parallel()
	dir := copyFixture(t)
	out := filepath.Join(dir, "out")

	r := invoke(t, "fold", "--json", "--out", out, "--day", "2026-09-11",
		"--repos", filepath.Join(dir, "repos.tsv"), "--claude", "bench="+filepath.Join(dir, "transcripts"), "--bus", filepath.Join(dir, "bus"))
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "fold" {
		t.Errorf("verb = %q, want %q", v.Result.Verb, "fold")
	}
	if v.Result.Status != "ok" {
		t.Errorf("status = %q, want %q", v.Result.Status, "ok")
	}
	if v.Result.Exit != 0 {
		t.Errorf("exit = %d, want 0", v.Result.Exit)
	}
	if got, _ := v.Facts.Get("out"); got != out {
		t.Errorf("fact out = %q, want %q", got, out)
	}
	if got, _ := v.Facts.Get("days"); got != "1" {
		t.Errorf("fact days = %q, want %q", got, "1")
	}
	if got, _ := v.Facts.Get("rows"); got != "3" {
		t.Errorf("fact rows = %q, want %q", got, "3")
	}
	if len(v.Items) == 0 {
		t.Errorf("items is empty, want folded day and source items")
	}
	// Verify day file was actually written to disk
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err != nil {
		t.Errorf("expected 2026-09-11.tsv to exist: %s", err)
	}
}

func TestTokensCheckJSON(t *testing.T) {
	t.Parallel()
	dir := copyFixture(t)
	out := filepath.Join(dir, "out")

	// First fold so we have valid data
	rFold := invoke(t, "fold", "--out", out, "--day", "2026-09-11",
		"--repos", filepath.Join(dir, "repos.tsv"), "--claude", "bench="+filepath.Join(dir, "transcripts"), "--bus", filepath.Join(dir, "bus"))
	wantExit(t, rFold, 0)

	// Clean check
	r := invoke(t, "check", "--json", "--out", out)
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "check" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("files"); got != "1" {
		t.Errorf("files = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("bad"); got != "0" {
		t.Errorf("bad = %q, want 0", got)
	}

	// Dirty check: add a stray file
	write(t, filepath.Join(out, "stray.txt"), "hello")
	rFail := invoke(t, "check", "--json", "--out", out)
	wantExit(t, rFail, 1)

	vFail := parseJSON(t, rFail.stdout)
	if vFail.Result.Verb != "check" || vFail.Result.Status != "failed" || vFail.Result.Exit != 1 {
		t.Errorf("unexpected failure result: %+v", vFail.Result)
	}
	if got, _ := vFail.Facts.Get("stray"); got != "1" {
		t.Errorf("stray = %q, want 1", got)
	}
	foundStray := false
	for _, item := range vFail.Items {
		if item.Kind == "stray" {
			foundStray = true
			break
		}
	}
	if !foundStray {
		t.Errorf("missing stray item in items: %+v", vFail.Items)
	}
}

func TestTokensSumJSON(t *testing.T) {
	t.Parallel()
	dir := copyFixture(t)
	out := filepath.Join(dir, "out")

	rFold := invoke(t, "fold", "--out", out, "--day", "2026-09-11",
		"--repos", filepath.Join(dir, "repos.tsv"), "--claude", "bench="+filepath.Join(dir, "transcripts"), "--bus", filepath.Join(dir, "bus"))
	wantExit(t, rFold, 0)

	r := invoke(t, "sum", "--json", "--out", out, "--month", "2026-09")
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "sum" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("month"); got != "2026-09" {
		t.Errorf("month = %q, want 2026-09", got)
	}
	if got, _ := v.Facts.Get("days"); got != "1" {
		t.Errorf("days = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("rows"); got != "3" {
		t.Errorf("rows = %q, want 3", got)
	}
	hasPair, hasModel, hasTotal := false, false, false
	for _, it := range v.Items {
		switch it.Kind {
		case "pair":
			hasPair = true
		case "model":
			hasModel = true
		case "total":
			hasTotal = true
		}
	}
	if !hasPair || !hasModel || !hasTotal {
		t.Errorf("expected pair, model, total items, got %+v", v.Items)
	}
}

func TestTokensSourcesJSON(t *testing.T) {
	t.Parallel()
	dir := copyFixture(t)

	r := invoke(t, "sources", "--json", "--repos", filepath.Join(dir, "repos.tsv"), "--all", "--claude", "bench="+filepath.Join(dir, "transcripts"))
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "sources" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("sources"); got != "1" {
		t.Errorf("sources = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("messages"); got != "3" {
		t.Errorf("messages = %q, want 3", got)
	}
	if got, _ := v.Facts.Get("rows"); got != "2" {
		t.Errorf("rows = %q, want 2", got)
	}
	if len(v.Items) == 0 {
		t.Errorf("expected source items, got 0")
	}

	// With unattributed
	rUnattr := invoke(t, "sources", "--json", "--repos", filepath.Join(dir, "repos.tsv"), "--all", "--claude", "bench="+filepath.Join(dir, "transcripts"), "--unattributed")
	wantExit(t, rUnattr, 0)
	vUnattr := parseJSON(t, rUnattr.stdout)
	if _, ok := vUnattr.Facts.Get("unattributed"); !ok {
		t.Errorf("expected unattributed fact in %+v", vUnattr.Facts)
	}
}

func TestTokensReportJSON(t *testing.T) {
	t.Parallel()
	dir := copyFixture(t)

	r := invoke(t, "report", "--json", "--who", "emma", "--day", "2026-09-11",
		"--repos", filepath.Join(dir, "repos.tsv"), "--claude", "bench="+filepath.Join(dir, "transcripts"))
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "report" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("who"); got != "emma" {
		t.Errorf("who = %q, want emma", got)
	}
	if got, _ := v.Facts.Get("day"); got != "2026-09-11" {
		t.Errorf("day = %q, want 2026-09-11", got)
	}
	if got, _ := v.Facts.Get("rows"); got != "7" {
		t.Errorf("rows = %q, want 7", got)
	}
	hasBody, hasAvg := false, false
	for _, it := range v.Items {
		if it.Kind == "body" {
			hasBody = true
		}
		if it.Kind == "avg" {
			hasAvg = true
		}
	}
	if !hasBody || !hasAvg {
		t.Errorf("expected body and avg items, got %+v", v.Items)
	}
}

func TestTokensSessionJSON(t *testing.T) {
	t.Parallel()
	dir := copyFixture(t)
	out := filepath.Join(dir, "out")
	sessionFile := filepath.Join(dir, "transcripts", "window.jsonl")

	r := invoke(t, "session", "--json", "--claude-session", sessionFile)
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "session" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("turns"); got != "3" {
		t.Errorf("turns = %q, want 3", got)
	}
	for _, f := range []string{"input", "cache_write", "cache_read", "output", "weighted", "avg_context"} {
		if _, ok := v.Facts.Get(f); !ok {
			t.Errorf("expected fact %q in %+v", f, v.Facts)
		}
	}

	// Session with --out
	rOut := invoke(t, "session", "--json", "--claude-session", sessionFile, "--out", out)
	wantExit(t, rOut, 0)
	vOut := parseJSON(t, rOut.stdout)
	if got, _ := vOut.Facts.Get("out"); got != out {
		t.Errorf("fact out = %q, want %q", got, out)
	}
	if len(vOut.Items) == 0 {
		t.Errorf("expected day items when --out is given, got 0")
	}
}

func TestTokensProfilesJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))

	cardPrompt(t, filepath.Join(root, "batch-a", "jobs", "j1"), "500")
	cardUsageFile(t, filepath.Join(root, "batch-a", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-15T10:00:00Z", "1000", "100", "0.0100")

	r := invoke(t, "profiles", "--json", "--swarm-root", root)
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "profiles" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("models"); got != "1" {
		t.Errorf("models = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("cards"); got != "1" {
		t.Errorf("cards = %q, want 1", got)
	}
	if len(v.Items) != 1 {
		t.Errorf("items len = %d, want 1", len(v.Items))
	}
}

func TestTokensFoldPoolJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pool := mkdir(t, filepath.Join(dir, "pool", "usage"))
	ledger := filepath.Join(dir, "ledger.tsv")

	writePoolFile(t, filepath.Join(pool, "a.tsv"),
		poolRow("t1", "2026-09-11T10:00:00Z", "deepseek", "m1", "r1", "100", "50", "10", "20", "5", "0.010000"),
		poolRow("t2", "2026-09-11T11:00:00Z", "deepseek", "m1", "r1", "200", "100", "20", "40", "10", "0.020000"),
	)

	r := invoke(t, "fold-pool", "--json", "--pool", filepath.Join(dir, "pool"), "--ledger", ledger)
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "fold-pool" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("rows"); got != "1" {
		t.Errorf("rows = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("tasks"); got != "2" {
		t.Errorf("tasks = %q, want 2", got)
	}
	if got, _ := v.Facts.Get("days"); got != "1" {
		t.Errorf("days = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("ledger"); got != ledger {
		t.Errorf("ledger = %q, want %q", got, ledger)
	}
}

func TestTokensReportLedgerJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.tsv")
	writePoolLedgerFixture(t, ledger)

	r := invoke(t, "report", "--json", "--ledger", ledger, "--month", "2026-09")
	wantExit(t, r, 0)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "report" || v.Result.Status != "ok" || v.Result.Exit != 0 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("month"); got != "2026-09" {
		t.Errorf("month = %q, want 2026-09", got)
	}
	if got, _ := v.Facts.Get("source"); got != "ledger" {
		t.Errorf("source = %q, want ledger", got)
	}
	if got, _ := v.Facts.Get("groups"); got != "2" {
		t.Errorf("groups = %q, want 2", got)
	}
	if got, _ := v.Facts.Get("rows"); got != "3" {
		t.Errorf("rows = %q, want 3", got)
	}
	if got, _ := v.Facts.Get("usd"); got != "0.09" {
		t.Errorf("usd = %q, want 0.09", got)
	}
	if len(v.Items) != 2 {
		t.Errorf("items count = %d, want 2", len(v.Items))
	}
}

func TestTokensLedgerJSONError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := invoke(t, "ledger", "--json", "--out", dir, "--day", "2026-09-11", "--redis", "127.0.0.1:1")
	wantExit(t, r, 1)

	v := parseJSON(t, r.stdout)
	if v.Result.Verb != "ledger" || v.Result.Status != "failed" || v.Result.Exit != 1 {
		t.Errorf("unexpected result: %+v", v.Result)
	}
	if got, _ := v.Facts.Get("bad"); got != "1" {
		t.Errorf("bad = %q, want 1", got)
	}
	if got, _ := v.Facts.Get("days"); got != "0" {
		t.Errorf("days = %q, want 0", got)
	}
	if len(v.Items) != 1 || v.Items[0].Kind != "bad" {
		t.Errorf("expected 1 bad item, got %+v", v.Items)
	}
}
