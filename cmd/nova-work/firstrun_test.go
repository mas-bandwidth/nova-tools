package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannerFirstRunExamples extracts the commands under the "first run" section in banner.
func bannerFirstRunExamples(t *testing.T) []string {
	t.Helper()
	const heading = "first run (a login gh can use, and a scratch directory):\n"
	_, tail, ok := strings.Cut(banner, heading)
	if !ok {
		t.Fatalf("banner missing expected heading %q", heading)
	}
	var lines []string
	for _, l := range strings.Split(tail, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "nova-work ") {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("banner has no nova-work commands under %q", heading)
	}
	return lines
}

// TestUsageBannerFirstRunExamples verifies that the usage banner's first run example lines
// are present and executable against the recorded replay fixture.
func TestUsageBannerFirstRunExamples(t *testing.T) {
	t.Parallel()

	examples := bannerFirstRunExamples(t)
	wantExamples := []string{
		"nova-work import --org <org> --repo <org>/<repo> --out ./scratch/tree.lisp",
		"nova-work verify --tree ./scratch/tree.lisp --repo <org>/<repo>",
	}
	if len(examples) != len(wantExamples) {
		t.Fatalf("banner examples count = %d, want %d: %v", len(examples), len(wantExamples), examples)
	}
	for i, want := range wantExamples {
		if examples[i] != want {
			t.Errorf("example %d = %q, want %q", i, examples[i], want)
		}
	}

	const org = "mas-bandwidth"
	const repo = "reliable"
	tree := filepath.Join(t.TempDir(), "tree.lisp")

	// Execute Step 1: import
	code, out, errs := do(t, replay(t), "import", "--org", org, "--repo", org+"/"+repo, "--out", tree, "--page-size", "15")
	if code != 0 {
		t.Fatalf("import failed: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	if !strings.Contains(out, "PLAN OK") || !strings.Contains(out, "REPO OK") || !strings.Contains(out, "IMPORT OK") {
		t.Fatalf("import stdout missing expected OK lines:\n%s", out)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("import did not write tree file: %v", err)
	}

	// Execute Step 2: verify
	code, out, errs = do(t, replay(t), "verify", "--tree", tree, "--repo", org+"/"+repo, "--page-size", "15")
	if code != 0 {
		t.Fatalf("verify failed: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	if !strings.Contains(out, "VERIFY OK") || !strings.Contains(out, "differences=0") {
		t.Fatalf("verify stdout missing VERIFY OK differences=0:\n%s", out)
	}
}

// TestFirstRunWorkflowUnderJSON verifies that both first-run verbs emit the standard
// JSON envelope under --json:
//  1. import: {"result":{"verb":"import","status":"ok","exit":0},"facts":{"org":"...","repo":"...","issues":<n>,"out":"..."}}
//  2. verify: {"result":{"verb":"verify","status":"ok","exit":0},"facts":{"differences":0},"items":[]}
func TestFirstRunWorkflowUnderJSON(t *testing.T) {
	t.Parallel()

	const org = "mas-bandwidth"
	const repo = "reliable"
	tree := filepath.Join(t.TempDir(), "tree.lisp")

	// 1. import --json
	code, out, errs := do(t, replay(t), "import", "--org", org, "--repo", org+"/"+repo, "--out", tree, "--page-size", "15", "--json")
	if code != 0 {
		t.Fatalf("import --json exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	if errs != "" {
		t.Fatalf("import --json wrote to stderr:\n%s", errs)
	}

	var importEnv importEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &importEnv); err != nil {
		t.Fatalf("import --json emitted invalid JSON: %v\noutput:\n%s", err, out)
	}
	if importEnv.Result.Verb != "import" || importEnv.Result.Status != "ok" || importEnv.Result.Exit != 0 {
		t.Errorf("unexpected import result: %+v", importEnv.Result)
	}
	if gotOrg, _ := importEnv.Facts["org"].(string); gotOrg != org {
		t.Errorf("facts[org] = %q, want %q", gotOrg, org)
	}
	if gotRepo, _ := importEnv.Facts["repo"].(string); gotRepo != org+"/"+repo {
		t.Errorf("facts[repo] = %q, want %q", gotRepo, org+"/"+repo)
	}
	if gotIssues, _ := importEnv.Facts["issues"].(float64); int(gotIssues) != 20 {
		t.Errorf("facts[issues] = %v, want 20", importEnv.Facts["issues"])
	}
	if gotOut, _ := importEnv.Facts["out"].(string); gotOut != tree {
		t.Errorf("facts[out] = %q, want %q", gotOut, tree)
	}

	// 2. verify --json (0 differences)
	code, out, errs = do(t, replay(t), "verify", "--tree", tree, "--repo", org+"/"+repo, "--page-size", "15", "--json")
	if code != 0 {
		t.Fatalf("verify --json exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	if errs != "" {
		t.Fatalf("verify --json wrote to stderr:\n%s", errs)
	}

	var verifyEnv verifyEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &verifyEnv); err != nil {
		t.Fatalf("verify --json emitted invalid JSON: %v\noutput:\n%s", err, out)
	}
	if verifyEnv.Result.Verb != "verify" || verifyEnv.Result.Status != "ok" || verifyEnv.Result.Exit != 0 {
		t.Errorf("unexpected verify result: %+v", verifyEnv.Result)
	}
	if gotDiffs, _ := verifyEnv.Facts["differences"].(float64); int(gotDiffs) != 0 {
		t.Errorf("facts[differences] = %v, want 0", verifyEnv.Facts["differences"])
	}
	if len(verifyEnv.Items) != 0 {
		t.Errorf("verify items = %v, want empty", verifyEnv.Items)
	}
}

// TestVerifyDifferenceUnderJSON verifies that when differences exist, verify --json
// emits {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"differences":<n>},"items":[...]}
// with exit code 1.
func TestVerifyDifferenceUnderJSON(t *testing.T) {
	t.Parallel()

	const org = "mas-bandwidth"
	const repo = "reliable"
	tree := filepath.Join(t.TempDir(), "tree.lisp")

	code, out, errs := do(t, replay(t), "import", "--org", org, "--repo", org+"/"+repo, "--out", tree, "--page-size", "15")
	if code != 0 {
		t.Fatalf("import exit %d: %s%s", code, out, errs)
	}

	data, err := os.ReadFile(tree)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), `:title "`, `:title "changed `, 1)
	if err := os.WriteFile(tree, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errs = do(t, replay(t), "verify", "--tree", tree, "--repo", org+"/"+repo, "--page-size", "15", "--json")
	if code != 1 {
		t.Fatalf("verify exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}

	var verifyEnv verifyEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &verifyEnv); err != nil {
		t.Fatalf("verify --json emitted invalid JSON: %v\noutput:\n%s", err, out)
	}
	if verifyEnv.Result.Verb != "verify" || verifyEnv.Result.Status != "failed" || verifyEnv.Result.Exit != 1 {
		t.Errorf("unexpected verify result: %+v", verifyEnv.Result)
	}
	if gotDiffs, _ := verifyEnv.Facts["differences"].(float64); int(gotDiffs) != 1 {
		t.Errorf("facts[differences] = %v, want 1", verifyEnv.Facts["differences"])
	}
	if len(verifyEnv.Items) != 1 {
		t.Fatalf("items count = %d, want 1", len(verifyEnv.Items))
	}
	if verifyEnv.Items[0].Kind != "DRIFT" {
		t.Errorf("item kind = %q, want DRIFT", verifyEnv.Items[0].Kind)
	}
	if verifyEnv.Items[0].Field != "title" {
		t.Errorf("item field = %q, want title", verifyEnv.Items[0].Field)
	}
}

// TestJSONRefusals verifies that flag or input refusals under --json emit
// structured JSON with status "refused" and exit 2 on stdout.
func TestJSONRefusals(t *testing.T) {
	t.Parallel()

	// import missing --org
	code, out, _ := do(t, nil, "import", "--json", "--dry-run")
	if code != 2 {
		t.Errorf("import without --org exit = %d, want 2", code)
	}
	var env importEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("invalid json on import refusal: %v\n%s", err, out)
	}
	if env.Result.Status != "refused" || env.Result.Exit != 2 {
		t.Errorf("expected refused exit 2, got: %+v", env.Result)
	}

	// verify missing --tree
	code, out, _ = do(t, nil, "verify", "--json")
	if code != 2 {
		t.Errorf("verify without --tree exit = %d, want 2", code)
	}
	var vEnv verifyEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &vEnv); err != nil {
		t.Fatalf("invalid json on verify refusal: %v\n%s", err, out)
	}
	if vEnv.Result.Status != "refused" || vEnv.Result.Exit != 2 {
		t.Errorf("expected refused exit 2, got: %+v", vEnv.Result)
	}
}

// TestVerifyMoreLineWithRunKeyword verifies that text mode MORE lines have the run: keyword.
func TestVerifyMoreLineWithRunKeyword(t *testing.T) {
	t.Parallel()

	tree := filepath.Join(t.TempDir(), "tree.lisp")
	repo := []string{"--repo", "mas-bandwidth/reliable", "--page-size", "15"}
	code, out, errs := do(t, replay(t), append([]string{"import", "--org", "mas-bandwidth", "--out", tree}, repo...)...)
	if code != 0 {
		t.Fatalf("import exit %d\n%s%s", code, out, errs)
	}
	data, err := os.ReadFile(tree)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(data), `:title "`, `:title "changed `)
	if err := os.WriteFile(tree, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs = do(t, replay(t), append([]string{"verify", "--tree", tree, "--max", "1"}, repo...)...)
	if code != 1 {
		t.Fatalf("verify exit %d, want 1\n%s%s", code, out, errs)
	}
	wantMore := "VERIFY MORE kind=difference shown=1 total=20 run: nova-work verify --tree " + tree + " --max 0\n"
	if !strings.Contains(out, wantMore) {
		t.Fatalf("stdout lacking expected MORE line with run: keyword.\nwant:\n%s\ngot:\n%s", wantMore, out)
	}
}
