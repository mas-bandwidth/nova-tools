package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConstructPRBody_PreservesContractLineDoneRedGreen(t *testing.T) {
	resultLines := []string{
		"RESULT fix-widget-123 sha=abcdef123456 — fix widget crash on null pointer",
		"DONE",
		"BRANCH emma/fix-widget-123",
		"REPO mas-bandwidth/nova-tools",
		"red: TestWidgetCrash: nil pointer dereference at widget.go:42",
		"green: TestWidgetCrash PASS: handles nil widget gracefully",
		"prior: #1230 @ 99887766",
		"files: internal/widget/widget.go internal/widget/widget_test.go",
	}

	report := `CLAIM: widget nil pointer crash is fixed
CHANGED: internal/widget/widget.go: add nil check
DECISIONS: return early on nil rather than allocate default
NOT DONE: none
UNSURE: none`

	prov := Provenance{
		Model: "claude-3-5-sonnet",
		Route: "anthropic/sonnet",
		Bench: "studio",
		Cost:  "$0.0450",
	}

	body := constructPRBody(resultLines, report, prov)

	// Invariant 1: Line 1 must be the contract line
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		t.Fatalf("body has fewer than 2 lines:\n%s", body)
	}
	if lines[0] != "RESULT fix-widget-123 sha=abcdef123456 — fix widget crash on null pointer" {
		t.Fatalf("line 1 is not the contract line, got: %q", lines[0])
	}

	// Invariant 2: Line 2 must be DONE
	if lines[1] != "DONE" {
		t.Fatalf("line 2 is not DONE, got: %q", lines[1])
	}

	// Invariant 3: Verbatim red line preserved
	if !strings.Contains(body, "red: TestWidgetCrash: nil pointer dereference at widget.go:42") {
		t.Fatalf("verbatim red line missing from body:\n%s", body)
	}

	// Invariant 4: Verbatim green line preserved
	if !strings.Contains(body, "green: TestWidgetCrash PASS: handles nil widget gracefully") {
		t.Fatalf("verbatim green line missing from body:\n%s", body)
	}

	// Invariant 5: Report is included
	if !strings.Contains(body, "CLAIM: widget nil pointer crash is fixed") {
		t.Fatalf("REPORT content missing from body:\n%s", body)
	}

	// Invariant 6: Provenance table is included
	wantTable := "| model | route | bench | cost |\n| --- | --- | --- | --- |\n| claude-3-5-sonnet | anthropic/sonnet | studio | $0.0450 |"
	if !strings.Contains(body, wantTable) {
		t.Fatalf("provenance metadata table missing or wrong:\n%s\nwant:\n%s", body, wantTable)
	}
}

func TestConstructPRBody_RedGreenExtractedFromReport(t *testing.T) {
	resultLines := []string{
		"RESULT feat-thing-456 sha=112233445566 — add thing feature",
		"DONE",
		"BRANCH emma/feat-thing-456",
		"REPO mas-bandwidth/nova-tools",
	}

	report := `CLAIM: thing feature added
CHANGED: thing.go: implement thing
EVIDENCE: red: TestThingFeature: not implemented | green: TestThingFeature PASS | control: verified
DECISIONS: none
NOT DONE: none
UNSURE: none`

	prov := Provenance{
		Model: "gemini-2.5-pro",
		Route: "google/gemini",
		Bench: "hulk",
		Cost:  "$0.0120",
	}

	body := constructPRBody(resultLines, report, prov)

	// Contract line and DONE preserved
	lines := strings.Split(body, "\n")
	if lines[0] != "RESULT feat-thing-456 sha=112233445566 — add thing feature" {
		t.Fatalf("line 1 is not contract line, got: %q", lines[0])
	}
	if lines[1] != "DONE" {
		t.Fatalf("line 2 is not DONE, got: %q", lines[1])
	}

	// Verbatim red and green lines extracted from report and present in result header
	if !strings.Contains(body, "red: TestThingFeature: not implemented") {
		t.Fatalf("extracted verbatim red line missing from body:\n%s", body)
	}
	if !strings.Contains(body, "green: TestThingFeature PASS") {
		t.Fatalf("extracted verbatim green line missing from body:\n%s", body)
	}

	// Provenance table present
	wantTable := "| model | route | bench | cost |\n| --- | --- | --- | --- |\n| gemini-2.5-pro | google/gemini | hulk | $0.0120 |"
	if !strings.Contains(body, wantTable) {
		t.Fatalf("provenance table missing or wrong:\n%s", body)
	}
}

func TestConstructPRBody_MissingReportAndEmptyProvenance(t *testing.T) {
	resultLines := []string{
		"RESULT bare-card sha=000000000000 — bare card without report",
		"DONE",
		"BRANCH emma/bare-card",
		"REPO mas-bandwidth/nova-tools",
		"red: TestBare: fail",
		"green: TestBare: pass",
	}

	prov := Provenance{}
	body := constructPRBody(resultLines, "", prov)

	lines := strings.Split(body, "\n")
	if lines[0] != "RESULT bare-card sha=000000000000 — bare card without report" {
		t.Fatalf("line 1 not contract line: %q", lines[0])
	}
	if lines[1] != "DONE" {
		t.Fatalf("line 2 not DONE: %q", lines[1])
	}
	if !strings.Contains(body, "red: TestBare: fail") {
		t.Fatal("red line missing")
	}
	if !strings.Contains(body, "green: TestBare: pass") {
		t.Fatal("green line missing")
	}

	wantTable := "| model | route | bench | cost |\n| --- | --- | --- | --- |\n| - | - | - | - |"
	if !strings.Contains(body, wantTable) {
		t.Fatalf("empty provenance table should have dashes:\n%s", body)
	}
}

func TestBoundPRBody_MaxBodyBytesRespect(t *testing.T) {
	resultLines := []string{
		"RESULT bounds-test sha=aabbccddeeff — check MaxBodyBytes bounding",
		"DONE",
		"BRANCH emma/bounds-test",
		"REPO mas-bandwidth/nova-tools",
		"red: TestBound: red-line-verbatim",
		"green: TestBound: green-line-verbatim",
		strings.Repeat("long noise text line\n", 50),
	}

	report := "CLAIM: tested\n" + strings.Repeat("report details\n", 50)
	prov := Provenance{Model: "flash", Route: "google/flash", Bench: "studio", Cost: "$0.001"}

	fullBody := constructPRBody(resultLines, report, prov)

	for _, max := range []int{100, 200, 300, 500, 1000, 4096} {
		bounded := boundPRBody(fullBody, max)
		if len(bounded) > max {
			t.Fatalf("bounded body length %d exceeds max %d", len(bounded), max)
		}
		if idx := strings.LastIndex(bounded, "| model | route | bench | cost |"); idx != -1 {
			prefix := strings.TrimRight(bounded[:idx], "\n")
			if !strings.HasPrefix(fullBody, prefix) {
				t.Fatalf("bounded body prefix at max %d is not a prefix of fullBody", max)
			}
		} else if !strings.HasPrefix(fullBody, bounded) {
			t.Fatalf("bounded body at max %d is not a prefix of fullBody", max)
		}
		// For max >= 300, essential core (line 1, DONE, red) and provenance table are preserved
		if max >= 300 {
			if !strings.Contains(bounded, "RESULT bounds-test") {
				t.Fatalf("contract line missing at max %d", max)
			}
			if !strings.Contains(bounded, "DONE") {
				t.Fatalf("DONE missing at max %d", max)
			}
			if !strings.Contains(bounded, "red: TestBound: red-line-verbatim") {
				t.Fatalf("verbatim red line missing at max %d", max)
			}
			if !strings.Contains(bounded, prov.Table()) {
				t.Fatalf("provenance table missing at max %d", max)
			}
		}
		// For max >= 500, green line is also fully accommodated alongside the table
		if max >= 500 {
			if !strings.Contains(bounded, "green: TestBound: green-line-verbatim") {
				t.Fatalf("verbatim green line missing at max %d", max)
			}
		}
	}

	// max <= 0 defaults to 4096
	boundedDefault := boundPRBody(fullBody, 0)
	if len(boundedDefault) > 4096 {
		t.Fatalf("default max body length %d exceeds 4096", len(boundedDefault))
	}
}

func TestBoundPRBody_OversizedReportPreservesProvenanceTable(t *testing.T) {
	contract := "RESULT test-oversized sha=998877665544 — test oversized report preserves provenance"
	red := "red: TestOversized: fail"
	green := "green: TestOversized PASS: pass"
	resultLines := []string{
		contract,
		"DONE",
		"BRANCH emma/oversized",
		"REPO mas-bandwidth/nova-tools",
		red,
		green,
	}

	// 100KB report to far exceed 4096 bytes and default 60KB
	oversizedReport := "CLAIM: big report\n" + strings.Repeat("detail line in long report\n", 4000)
	prov := Provenance{
		Model: "gemini-2.5-pro",
		Route: "google/gemini",
		Bench: "studio",
		Cost:  "$0.0500",
	}

	fullBody := constructPRBody(resultLines, oversizedReport, prov)
	if len(fullBody) < 100000 {
		t.Fatalf("fullBody length %d expected > 100000", len(fullBody))
	}

	// Test with 4096 limit
	bounded := boundPRBody(fullBody, 4096)
	if len(bounded) > 4096 {
		t.Fatalf("bounded length %d exceeds max 4096", len(bounded))
	}

	wantTable := prov.Table()
	if !strings.HasSuffix(bounded, wantTable) {
		t.Fatalf("bounded body does not have provenance table at the end:\n%s", bounded)
	}

	if !strings.Contains(bounded, contract) {
		t.Fatalf("contract line missing in bounded body")
	}
	if !strings.Contains(bounded, "DONE") {
		t.Fatalf("DONE missing in bounded body")
	}
	if !strings.Contains(bounded, red) {
		t.Fatalf("red line missing in bounded body")
	}
	if !strings.Contains(bounded, green) {
		t.Fatalf("green line missing in bounded body")
	}

	// Invariant check passes
	if err := checkPRBodyInvariants(bounded, fullBody, 4096, contract, red, green); err != nil {
		t.Fatalf("checkPRBodyInvariants failed: %v", err)
	}

	// Also test with default max (0 -> 4096)
	boundedDefault := boundPRBody(fullBody, 0)
	if len(boundedDefault) > 4096 {
		t.Fatalf("boundedDefault length %d exceeds 4096", len(boundedDefault))
	}
	if !strings.HasSuffix(boundedDefault, wantTable) {
		t.Fatalf("boundedDefault does not have provenance table at the end")
	}

	// Also test with a larger max like 60KB (61440)
	bounded60k := boundPRBody(fullBody, 60*1024)
	if len(bounded60k) > 60*1024 {
		t.Fatalf("bounded60k length %d exceeds 60KB", len(bounded60k))
	}
	if !strings.HasSuffix(bounded60k, wantTable) {
		t.Fatalf("bounded60k does not have provenance table at the end")
	}
}

func TestSecretScan_RefusesSecretInReport(t *testing.T) {
	root := t.TempDir()
	cleanResult := []string{
		"RESULT clean-result sha=000000000000",
		"DONE",
		"BRANCH emma/clean",
		"REPO mas-bandwidth/nova-tools",
		"red: TestThing: fail",
		"green: TestThing: pass",
	}

	// Report contains a leaked key
	leakyReport := "CLAIM: all good\nEVIDENCE: red: fail | green: pass\nTOKEN: " + secretFixture()
	prov := Provenance{Model: "sonnet", Route: "anthropic/sonnet", Bench: "studio", Cost: "$0.05"}

	fullBody := constructPRBody(cleanResult, leakyReport, prov)
	bodyLines := strings.Split(fullBody, "\n")

	// Scan must catch the key shape in the report
	findings, err := secretFindings(root, "", "", bodyLines)
	if err != nil {
		t.Fatalf("secretFindings failed: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("secretFindings reported 0 findings for key in REPORT.md; MUST refuse and quarantine")
	}
}

func TestHarvestBench_PRBodyHasReportAndProvenance(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)

	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-prbody"
	line1 := "RESULT card-prbody sha=abcdef123456 — verify PR body content from bench"
	result := []string{
		line1,
		"DONE",
		"BRANCH rowan/card-prbody",
		"REPO mas-bandwidth/nova-tools",
		"red: TestBenchPR: fail line 5",
		"green: TestBenchPR: pass line 5",
	}
	report := []string{
		"CLAIM: feature works",
		"CHANGED: bench.go: fix",
		"DECISIONS: none",
	}
	usage := []string{
		"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd",
		"card-prbody\t1\t2026-09-21T10:00:00Z\t2026-09-21T10:05:00Z\t0\tanthropic\tclaude-3-5-sonnet\t100\t200\t0\t0\t0\t0.0350",
	}

	var b strings.Builder
	fmt.Fprintf(&b, "JOB\t%s\n", job)
	fmt.Fprintf(&b, "MTIME\t1234567890\n")
	for _, l := range result {
		fmt.Fprintf(&b, "R\t%s\n", l)
	}
	for _, l := range report {
		fmt.Fprintf(&b, "REP\t%s\n", l)
	}
	for _, l := range usage {
		fmt.Fprintf(&b, "U\t%s\n", l)
	}
	b.WriteString("END\n")

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return b.String(), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Bench = "studio"

	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, out, errb)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1", len(forge.opened))
	}

	pr := forge.opened[0]
	// Assert line 1 of PR body is contract line
	bodyLines := strings.Split(pr.body, "\n")
	if bodyLines[0] != line1 {
		t.Fatalf("PR body line 1 = %q, want %q", bodyLines[0], line1)
	}
	// Assert line 2 is DONE
	if bodyLines[1] != "DONE" {
		t.Fatalf("PR body line 2 = %q, want DONE", bodyLines[1])
	}
	// Assert verbatim red and green lines
	if !strings.Contains(pr.body, "red: TestBenchPR: fail line 5") {
		t.Fatalf("verbatim red line missing from PR body:\n%s", pr.body)
	}
	if !strings.Contains(pr.body, "green: TestBenchPR: pass line 5") {
		t.Fatalf("verbatim green line missing from PR body:\n%s", pr.body)
	}
	// Assert REPORT is present
	if !strings.Contains(pr.body, "CLAIM: feature works") {
		t.Fatalf("REPORT content missing from PR body:\n%s", pr.body)
	}
	// Assert Provenance table is present with correct model, route, bench, cost
	wantTable := "| model | route | bench | cost |\n| --- | --- | --- | --- |\n| claude-3-5-sonnet | anthropic/claude-3-5-sonnet | studio | $0.0350 |"
	if !strings.Contains(pr.body, wantTable) {
		t.Fatalf("provenance metadata table missing from PR body:\n%s\nwant:\n%s", pr.body, wantTable)
	}
}

func TestHarvestBench_RefusesSecretInReportOnBench(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)

	job := "/home/gaffer/rowan-swarm-root/0/jobs/leaky-report"
	line1 := "RESULT leaky-report sha=abcdef123456 — clean result but leaky report"
	result := []string{
		line1,
		"DONE",
		"BRANCH rowan/leaky-report",
		"REPO mas-bandwidth/nova-tools",
		"red: TestBenchPR: fail",
		"green: TestBenchPR: pass",
	}
	report := []string{
		"CLAIM: done",
		"LEAK: " + secretFixture(),
	}

	var b strings.Builder
	fmt.Fprintf(&b, "JOB\t%s\n", job)
	fmt.Fprintf(&b, "MTIME\t1234567890\n")
	for _, l := range result {
		fmt.Fprintf(&b, "R\t%s\n", l)
	}
	for _, l := range report {
		fmt.Fprintf(&b, "REP\t%s\n", l)
	}
	b.WriteString("END\n")

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") || strings.Contains(script, "mv ") {
			return "", nil
		}
		return b.String(), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Bench = "studio"

	code, out, errb := runBenchHarvest(t, in)
	if !strings.Contains(errb, "HARVEST REFUSED secret-shape") {
		t.Fatalf("no secret-shape refusal line:\nstdout=%s\nstderr=%s", out, errb)
	}
	if len(forge.opened) != 0 {
		t.Fatalf("a PR was opened despite key in REPORT.md: %+v", forge.opened)
	}
	if !shell.ran("mv '" + job + "'") {
		t.Error("leaky job was not quarantined on the bench")
	}
	_ = code
}

func TestHarvestWorking_RefusesSecretInReport(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "abc1234567890abc 2026-09-19T18:00:00Z\n"},
		{Arg: 1, Equals: "ls-remote", Stdout: "abc1234567890abc\trefs/heads/rowan/leaky-rep\n"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 1, Equals: "pr", Stdout: "[]"}}})

	job := filepath.Join(root, "working", "jobs", "leaky-rep")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	result := "RESULT leaky-rep sha=abc — clean result with secret in report\nDONE\nBRANCH rowan/leaky-rep\nREPO owner/repo\nred: TestX: fail\ngreen: TestX: pass\n"
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
	report := "CLAIM: all good\nTOKEN: " + secretFixture() + "\n"
	if err := os.WriteFile(filepath.Join(job, "REPORT.md"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	var errb bytes.Buffer
	r := &workingRun{in: HarvestInput{Stdout: &bytes.Buffer{}, Stderr: &errb}, base12: "origin/dev"}
	outcome := r.one(harvestJob{dir: job, label: "leaky-rep"})

	if outcome.pushed != 0 || outcome.prs != 0 {
		t.Fatalf("the card was published despite secret in REPORT.md: %+v", outcome)
	}
	if !strings.Contains(errb.String(), "HARVEST REFUSED secret-shape") || !strings.Contains(errb.String(), "site=harvest-working") {
		t.Fatalf("no working refusal line:\n%s", errb.String())
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.Contains(l, "push") || strings.Contains(l, "pr create") || strings.Contains(l, "pr edit") {
			t.Fatalf("the quarantined card reached the forge: %s", l)
		}
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("the job was not quarantined: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "working", "quarantine", "leaky-rep", "RESULT.md")); err != nil {
		t.Fatalf("the quarantined RESULT.md is gone: %v", err)
	}
	if strings.Contains(errb.String(), secretFixture()) {
		t.Fatal("the refusal printed the matched text")
	}
}

func TestProvenance_ExtractionVariants(t *testing.T) {
	dir := t.TempDir()

	// 1. Usage TSV parsing
	tsv := "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n" +
		"card-test\t1\t2026-09-21T10:00:00Z\t2026-09-21T10:05:00Z\t0\tgoogle\tgemini-2.5-pro\t100\t200\t0\t0\t0\t0.0150\n"
	if err := os.WriteFile(filepath.Join(dir, "usage.tsv"), []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}

	resLines := []string{
		"RESULT card-test sha=0123456789ab — test extraction",
		"DONE",
		"BRANCH emma/card-test",
		"REPO mas-bandwidth/nova-tools",
	}

	prov := extractJobProvenance(dir, "studio", resLines, "")
	if prov.Model != "gemini-2.5-pro" {
		t.Errorf("Model = %q, want gemini-2.5-pro", prov.Model)
	}
	if prov.Route != "google/gemini-2.5-pro" {
		t.Errorf("Route = %q, want google/gemini-2.5-pro", prov.Route)
	}
	if prov.Bench != "studio" {
		t.Errorf("Bench = %q, want studio", prov.Bench)
	}
	if prov.Cost != "$0.0150" {
		t.Errorf("Cost = %q, want $0.0150", prov.Cost)
	}

	// 2. Explicit tokens in RESULT.md override TSV
	resWithTokens := []string{
		"RESULT card-override sha=0123456789ab",
		"DONE",
		"model: custom-model",
		"route: custom-route",
		"bench: custom-bench",
		"cost: $0.99",
	}
	provOverride := extractJobProvenance(dir, "studio", resWithTokens, "")
	if provOverride.Model != "custom-model" || provOverride.Route != "custom-route" ||
		provOverride.Bench != "custom-bench" || provOverride.Cost != "$0.99" {
		t.Errorf("explicit tokens in RESULT.md not respected: %+v", provOverride)
	}

	// 3. Card file parsing with MODEL:
	cardPath := filepath.Join(dir, "card.txt")
	if err := os.WriteFile(cardPath, []byte("DISPATCH: 1\nMODEL: anthropic/claude-3-5-sonnet\nPROMPT: foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyDir := t.TempDir()
	provCard := extractProvenanceFromDir(emptyDir, cardPath, "hulk", []string{"RESULT card", "DONE"})
	if provCard.Route != "anthropic/claude-3-5-sonnet" {
		t.Errorf("Route from card = %q, want anthropic/claude-3-5-sonnet", provCard.Route)
	}
	if provCard.Bench != "hulk" {
		t.Errorf("Bench = %q, want hulk", provCard.Bench)
	}

	// 4. CardRow fallback in Harvest
	cRow := CardRow{Model: "gpt-4o", Card: cardPath}
	provHarvest := extractHarvestProvenance(emptyDir, cRow, "titan", []string{"RESULT card", "DONE"})
	if provHarvest.Bench != "titan" {
		t.Errorf("Harvest bench = %q, want titan", provHarvest.Bench)
	}
	if provHarvest.Route != "anthropic/claude-3-5-sonnet" {
		t.Errorf("Harvest route = %q, want anthropic/claude-3-5-sonnet", provHarvest.Route)
	}

	// 5. Cost formatting
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", "-"},
		{"-", "-"},
		{"0.05", "$0.05"},
		{"$0.05", "$0.05"},
		{"$1.2345", "$1.2345"},
		{"free", "free"},
	} {
		if got := formatCost(tc.in); got != tc.want {
			t.Errorf("formatCost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanResultSection_EdgeCases(t *testing.T) {
	// 1. Empty input
	if got := cleanResultSection(nil, ""); got != nil {
		t.Errorf("cleanResultSection(nil) = %v, want nil", got)
	}

	// 2. Missing red/green in RESULT but present in REPORT EVIDENCE
	res := []string{
		"RESULT my-feature sha=123456 — do something",
		"DONE",
		"BRANCH emma/my-feature",
	}
	rep := "CLAIM: feature done\nEVIDENCE: red: TestFail: missing | green: TestPass: ok\n"
	cleaned := cleanResultSection(res, rep)
	joined := strings.Join(cleaned, "\n")
	if !strings.Contains(joined, "red: TestFail: missing") {
		t.Fatalf("red line from EVIDENCE missing from cleaned result: %s", joined)
	}
	if !strings.Contains(joined, "green: TestPass: ok") {
		t.Fatalf("green line from EVIDENCE missing from cleaned result: %s", joined)
	}

	// 3. Leading whitespace on line 1 or DONE
	resWithSpaces := []string{
		"   RESULT spaced sha=123 — title   ",
		"   DONE   ",
		"BRANCH emma/spaced",
	}
	cleanedSpaces := cleanResultSection(resWithSpaces, "")
	if len(cleanedSpaces) < 2 || cleanedSpaces[0] != "RESULT spaced sha=123 — title" || cleanedSpaces[1] != "DONE" {
		t.Errorf("whitespace handling failed: %v", cleanedSpaces)
	}
}

// checkPRBodyInvariants validates that a generated PR body satisfies the essential invariants:
// - Bounded by maxBytes (when maxBytes > 0)
// - Strictly a prefix of the full scanned text (no post-scan additions)
// - Line 1 is the verbatim contract line
// - Line 2 is DONE
// - Verbatim red and green lines are preserved
// - Provenance metadata table is present and correctly formatted
func checkPRBodyInvariants(body string, scannedText string, maxBytes int, wantContract, wantRed, wantGreen string) error {
	if maxBytes > 0 && len(body) > maxBytes {
		return fmt.Errorf("length %d exceeds MaxBodyBytes %d", len(body), maxBytes)
	}
	if !strings.HasPrefix(scannedText, body) {
		idx := strings.LastIndex(body, "| model | route | bench | cost |")
		if idx == -1 {
			idx = strings.LastIndex(body, "| Model | Route | Bench | Cost |")
		}
		tableIdx := strings.LastIndex(scannedText, "| model | route | bench | cost |")
		if tableIdx == -1 {
			tableIdx = strings.LastIndex(scannedText, "| Model | Route | Bench | Cost |")
		}
		if idx == -1 || tableIdx == -1 {
			return fmt.Errorf("PR body is not a prefix of scanned text")
		}
		prefix := strings.TrimRight(body[:idx], "\n")
		if !strings.HasPrefix(scannedText, prefix) {
			return fmt.Errorf("PR body is not a prefix of scanned text")
		}
		if body[idx:] != scannedText[tableIdx:] {
			return fmt.Errorf("PR body is not a prefix of scanned text")
		}
	}
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		return fmt.Errorf("PR body has fewer than 2 lines")
	}
	if lines[0] != wantContract {
		return fmt.Errorf("line 1 %q != contract line %q", lines[0], wantContract)
	}
	if lines[1] != "DONE" {
		return fmt.Errorf("line 2 %q != DONE", lines[1])
	}
	if wantRed != "" && !strings.Contains(body, wantRed) {
		return fmt.Errorf("verbatim red line %q missing from body", wantRed)
	}
	if wantGreen != "" && !strings.Contains(body, wantGreen) {
		return fmt.Errorf("verbatim green line %q missing from body", wantGreen)
	}
	if !strings.Contains(body, "| model | route | bench | cost |") {
		return fmt.Errorf("provenance table header missing")
	}
	return nil
}

func TestMutation_PRBodyInvariantsWithTeeth(t *testing.T) {
	contract := "RESULT test-card sha=112233445566 — test card title"
	red := "red: TestWidget: crash on nil"
	green := "green: TestWidget PASS: handles nil"
	resultLines := []string{
		contract,
		"DONE",
		"BRANCH emma/test-card",
		"REPO mas-bandwidth/nova-tools",
		red,
		green,
		"prior: #100 @ abcdef",
	}
	report := "CLAIM: widget fixed\nCHANGED: widget.go\nDECISIONS: none"
	prov := Provenance{
		Model: "gemini-2.5-pro",
		Route: "google/gemini",
		Bench: "studio",
		Cost:  "$0.0120",
	}

	full := constructPRBody(resultLines, report, prov)
	bounded := boundPRBody(full, 4096)

	// Baseline check: the correctly constructed PR body must satisfy all invariants.
	if err := checkPRBodyInvariants(bounded, full, 4096, contract, red, green); err != nil {
		t.Fatalf("baseline check failed: %v", err)
	}

	// Teeth: assert that every single mutation is rejected by checkPRBodyInvariants.
	mutations := []struct {
		name       string
		mutate     func(body string) string
		mutateFull func(full string) string
		maxBytes   int
		wantErr    string
	}{
		{
			name: "contract-line-missing-or-reordered",
			mutate: func(body string) string {
				lines := strings.Split(body, "\n")
				lines[0], lines[2] = lines[2], lines[0]
				return strings.Join(lines, "\n")
			},
			mutateFull: func(full string) string {
				lines := strings.Split(full, "\n")
				lines[0], lines[2] = lines[2], lines[0]
				return strings.Join(lines, "\n")
			},
			maxBytes: 4096,
			wantErr:  "line 1",
		},
		{
			name: "done-line-missing-or-reordered",
			mutate: func(body string) string {
				lines := strings.Split(body, "\n")
				lines[1] = "NOT_DONE"
				return strings.Join(lines, "\n")
			},
			mutateFull: func(full string) string {
				lines := strings.Split(full, "\n")
				lines[1] = "NOT_DONE"
				return strings.Join(lines, "\n")
			},
			maxBytes: 4096,
			wantErr:  "line 2",
		},
		{
			name: "verbatim-red-line-dropped",
			mutate: func(body string) string {
				return strings.Replace(body, red, "red: altered red line", 1)
			},
			mutateFull: func(full string) string {
				return strings.Replace(full, red, "red: altered red line", 1)
			},
			maxBytes: 4096,
			wantErr:  "verbatim red line",
		},
		{
			name: "verbatim-green-line-dropped",
			mutate: func(body string) string {
				return strings.Replace(body, green, "green: altered green line", 1)
			},
			mutateFull: func(full string) string {
				return strings.Replace(full, green, "green: altered green line", 1)
			},
			maxBytes: 4096,
			wantErr:  "verbatim green line",
		},
		{
			name: "provenance-table-dropped",
			mutate: func(body string) string {
				return strings.Replace(body, "| model | route | bench | cost |", "no-table-here", 1)
			},
			mutateFull: func(full string) string {
				return strings.Replace(full, "| model | route | bench | cost |", "no-table-here", 1)
			},
			maxBytes: 4096,
			wantErr:  "provenance table header missing",
		},
		{
			name: "post-scan-text-appended-breaks-prefix-invariant",
			mutate: func(body string) string {
				return body + "\n\nPOST-SCAN UNSCANNED TAIL\n"
			},
			maxBytes: 4096,
			wantErr:  "not a prefix of scanned text",
		},
		{
			name: "exceeds-max-body-bytes",
			mutate: func(body string) string {
				return body
			},
			maxBytes: 50,
			wantErr:  "exceeds MaxBodyBytes",
		},
	}

	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			targetFull := full
			if m.mutateFull != nil {
				targetFull = m.mutateFull(full)
			}
			targetBody := m.mutate(bounded)
			err := checkPRBodyInvariants(targetBody, targetFull, m.maxBytes, contract, red, green)
			if err == nil {
				t.Fatalf("mutation %q was NOT caught by invariants validator!", m.name)
			}
			if !strings.Contains(err.Error(), m.wantErr) {
				t.Fatalf("mutation %q error %q did not contain %q", m.name, err.Error(), m.wantErr)
			}
		})
	}
}
