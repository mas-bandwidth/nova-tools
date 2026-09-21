package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusPass_ConjunctionAllSixChecks(t *testing.T) {
	// Baseline: all 6 checks passing
	in := StatusPassInput{
		Repo: "mas-bandwidth/nova-tools",
		Head: "abcdef123456",
		Base: "dev",
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
				return "abcdef123456\n", nil
			}
			return "", nil
		},
		CheckRedFirst: func(in StatusPassInput) CheckResult {
			return CheckResult{Check: CheckRedFirst, Status: "PASS", Detail: "reproduced red on baseline, green with fix"}
		},
		CheckHermetic: func(in StatusPassInput) CheckResult {
			return CheckResult{Check: CheckHermetic, Status: "PASS", Detail: "sandbox wall clean: isolated HOME, net-deny"}
		},
		CheckLint: func(in StatusPassInput) CheckResult {
			return CheckResult{Check: CheckLint, Status: "PASS", Detail: "gofmt clean, syntax valid"}
		},
		CheckCleanTree: func(in StatusPassInput) CheckResult {
			return CheckResult{Check: CheckCleanTree, Status: "PASS", Detail: "tree clean: no binaries, no scratch files"}
		},
		CheckRevDeps: func(in StatusPassInput) CheckResult {
			return CheckResult{Check: CheckReverseDependents, Status: "PASS", Detail: "all reverse dependents pass"}
		},
		CheckDocParity: func(in StatusPassInput) CheckResult {
			return CheckResult{Check: CheckDocParity, Status: "PASS", Detail: "all markdown links and spec anchors resolve"}
		},
	}

	var out bytes.Buffer
	in.Stdout = &out

	res := StatusPass(in)
	if !res.OK {
		t.Fatalf("expected status pass OK, got failure: check=%s reason=%s", res.FailedCheck, res.FailureReason)
	}
	if res.PassedCount != 6 || res.TotalCount != 6 {
		t.Fatalf("passed %d/%d, want 6/6", res.PassedCount, res.TotalCount)
	}
	output := out.String()
	for _, chk := range AllStatusChecks {
		if !strings.Contains(output, fmt.Sprintf("STATUS CHECK check=%s status=PASS", chk)) {
			t.Errorf("missing pass line for check %s in output:\n%s", chk, output)
		}
	}
	if !strings.Contains(output, "STATUS PASS OK repo=mas-bandwidth/nova-tools head=abcdef123456 checks=6/6") {
		t.Errorf("missing final STATUS PASS OK line:\n%s", output)
	}
}

func TestStatusPass_AnyCheckFailureFailsConjunction(t *testing.T) {
	for _, failingCheck := range AllStatusChecks {
		t.Run(failingCheck, func(t *testing.T) {
			in := StatusPassInput{
				Repo: "mas-bandwidth/nova-tools",
				Head: "abcdef123456",
				Base: "dev",
				CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
					if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
						return "abcdef123456\n", nil
					}
					return "", nil
				},
				CheckRedFirst: func(in StatusPassInput) CheckResult {
					if failingCheck == CheckRedFirst {
						return CheckResult{Check: CheckRedFirst, Status: "FAIL", Reason: "no test in diff"}
					}
					return CheckResult{Check: CheckRedFirst, Status: "PASS"}
				},
				CheckHermetic: func(in StatusPassInput) CheckResult {
					if failingCheck == CheckHermetic {
						return CheckResult{Check: CheckHermetic, Status: "FAIL", Reason: "secret leaked into sandbox"}
					}
					return CheckResult{Check: CheckHermetic, Status: "PASS"}
				},
				CheckLint: func(in StatusPassInput) CheckResult {
					if failingCheck == CheckLint {
						return CheckResult{Check: CheckLint, Status: "FAIL", Reason: "gofmt dirty"}
					}
					return CheckResult{Check: CheckLint, Status: "PASS"}
				},
				CheckCleanTree: func(in StatusPassInput) CheckResult {
					if failingCheck == CheckCleanTree {
						return CheckResult{Check: CheckCleanTree, Status: "FAIL", Reason: "committed binary"}
					}
					return CheckResult{Check: CheckCleanTree, Status: "PASS"}
				},
				CheckRevDeps: func(in StatusPassInput) CheckResult {
					if failingCheck == CheckReverseDependents {
						return CheckResult{Check: CheckReverseDependents, Status: "FAIL", Reason: "reverse dependent failed"}
					}
					return CheckResult{Check: CheckReverseDependents, Status: "PASS"}
				},
				CheckDocParity: func(in StatusPassInput) CheckResult {
					if failingCheck == CheckDocParity {
						return CheckResult{Check: CheckDocParity, Status: "FAIL", Reason: "broken doc link"}
					}
					return CheckResult{Check: CheckDocParity, Status: "PASS"}
				},
			}
			var out bytes.Buffer
			in.Stdout = &out

			res := StatusPass(in)
			if res.OK {
				t.Fatalf("expected conjunction failure when %s fails, but got OK=true", failingCheck)
			}
			if res.FailedCheck != failingCheck {
				t.Errorf("FailedCheck = %q, want %q", res.FailedCheck, failingCheck)
			}
			if res.PassedCount != 5 {
				t.Errorf("PassedCount = %d, want 5", res.PassedCount)
			}
			if !strings.Contains(out.String(), fmt.Sprintf("STATUS PASS FAIL check=%s", failingCheck)) {
				t.Errorf("missing STATUS PASS FAIL line in output:\n%s", out.String())
			}
		})
	}
}

func TestCheckRedFirst_Logic(t *testing.T) {
	// 1. Diff carries no test
	inNoTest := StatusPassInput{
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "diff" {
				return "pkg/widget.go\n", nil
			}
			return "", nil
		},
	}
	res := runCheckRedFirst(inNoTest)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "no test in diff") {
		t.Fatalf("expected FAIL on no test in diff, got: %+v", res)
	}

	// 2. Test passes without fix (not red first)
	inPassesWithoutFix := StatusPassInput{
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "diff" {
				return "pkg/widget.go\npkg/widget_test.go\n", nil
			}
			if name == "go" && len(args) > 0 && args[0] == "test" {
				// baseline test passes unexpectedly
				return "PASS\nok  pkg 0.01s", nil
			}
			return "", nil
		},
	}
	res = runCheckRedFirst(inPassesWithoutFix)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "passes on baseline without fix") {
		t.Fatalf("expected FAIL when test passes on baseline, got: %+v", res)
	}

	// 3. Proper red first: fails on baseline, passes with fix
	inRedFirst := StatusPassInput{
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "diff" {
				return "pkg/widget.go\npkg/widget_test.go\n", nil
			}
			if name == "go" && len(args) > 0 && args[0] == "test" {
				for _, a := range args {
					if strings.Contains(a, "baseline_test") {
						return "--- FAIL: TestWidgetCrash (0.00s)\n    widget_test.go:42: nil pointer dereference\nFAIL", fmt.Errorf("exit 1")
					}
				}
				return "PASS\nok  pkg 0.02s", nil
			}
			return "", nil
		},
	}
	res = runCheckRedFirst(inRedFirst)
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for proper red-first, got: %+v", res)
	}
}

func TestCheckHermetic_Logic(t *testing.T) {
	// 1. Secrets stripped and HOME isolated
	in := StatusPassInput{
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			hasIsolatedHome := false
			for _, e := range env {
				if strings.HasPrefix(e, "HOME=") && !strings.Contains(e, os.Getenv("HOME")) {
					hasIsolatedHome = true
				}
				if isSecretEnvVar(e) {
					return "", fmt.Errorf("secret leaked: %s", e)
				}
			}
			if !hasIsolatedHome {
				return "", fmt.Errorf("host HOME was not isolated")
			}
			return "ok", nil
		},
	}
	res := runCheckHermetic(in)
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for hermetic sandbox, got: %+v", res)
	}
}

func TestCheckLint_FormattingAndSyntax(t *testing.T) {
	dir := t.TempDir()

	// Clean formatted Go file
	cleanCode := `package foo

func Bar() int {
	return 42
}
`
	if err := os.WriteFile(filepath.Join(dir, "clean.go"), []byte(cleanCode), 0o644); err != nil {
		t.Fatal(err)
	}

	inClean := StatusPassInput{Dir: dir}
	res := runCheckLint(inClean)
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for clean go code, got: %+v", res)
	}

	// Unformatted Go file (gofmt dirty)
	dirtyCode := `package foo

func Bar() int {
return 42
}
`
	if err := os.WriteFile(filepath.Join(dir, "dirty.go"), []byte(dirtyCode), 0o644); err != nil {
		t.Fatal(err)
	}

	inDirty := StatusPassInput{Dir: dir}
	res = runCheckLint(inDirty)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "gofmt dirty") {
		t.Fatalf("expected FAIL on gofmt dirty, got: %+v", res)
	}
	_ = os.Remove(filepath.Join(dir, "dirty.go"))

	// Syntax error in Go file
	syntaxErrCode := `package foo
func Bar( {
`
	if err := os.WriteFile(filepath.Join(dir, "syntax.go"), []byte(syntaxErrCode), 0o644); err != nil {
		t.Fatal(err)
	}

	inSyntax := StatusPassInput{Dir: dir}
	res = runCheckLint(inSyntax)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "syntax error") {
		t.Fatalf("expected FAIL on syntax error, got: %+v", res)
	}
}

func TestCheckCleanTree_ScratchAndBinaries(t *testing.T) {
	dir := t.TempDir()

	// 1. Clean tree
	inClean := StatusPassInput{
		Dir: dir,
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			return "", nil // git status clean
		},
	}
	res := runCheckCleanTree(inClean)
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for clean tree, got: %+v", res)
	}

	// 2. Forbidden root RESULT.md detected
	if err := os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte("RESULT root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = runCheckCleanTree(inClean)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "RESULT.md") {
		t.Fatalf("expected FAIL on root RESULT.md, got: %+v", res)
	}
	_ = os.Remove(filepath.Join(dir, "RESULT.md"))

	// 3. Binary file detected
	binData := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	if err := os.WriteFile(filepath.Join(dir, "bad_tool"), binData, 0o755); err != nil {
		t.Fatal(err)
	}
	res = runCheckCleanTree(inClean)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "binary file") {
		t.Fatalf("expected FAIL on binary file, got: %+v", res)
	}
	_ = os.Remove(filepath.Join(dir, "bad_tool"))

	// 4. Dirty git working tree
	inDirty := StatusPassInput{
		Dir: dir,
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			return " M internal/pulse/wire.go\n?? scratch.txt\n", nil
		},
	}
	res = runCheckCleanTree(inDirty)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "working tree dirty") {
		t.Fatalf("expected FAIL on dirty tree, got: %+v", res)
	}
}

func TestCheckReverseDependents_Logic(t *testing.T) {
	// Reverse dependent passes
	inPass := StatusPassInput{
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			if name == "go" && len(args) > 0 && args[0] == "list" {
				return "github.com/mas/pkgA github.com/mas/pkgB\ngithub.com/mas/pkgB\n", nil
			}
			if len(args) > 0 && args[0] == "diff" {
				return "pkgB/b.go\n", nil
			}
			return "PASS", nil
		},
	}
	res := runCheckReverseDependents(inPass)
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for reverse dependents, got: %+v", res)
	}

	// Reverse dependent fails test
	inFail := StatusPassInput{
		CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
			if name == "go" && len(args) > 0 && args[0] == "list" {
				return "github.com/mas/pkgA github.com/mas/pkgB\ngithub.com/mas/pkgB\n", nil
			}
			if len(args) > 0 && args[0] == "diff" {
				return "pkgB/b.go\n", nil
			}
			if name == "go" && len(args) > 0 && args[0] == "test" {
				return "FAIL: TestPkgA", fmt.Errorf("exit 1")
			}
			return "", nil
		},
	}
	res = runCheckReverseDependents(inFail)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "reverse dependent") {
		t.Fatalf("expected FAIL on broken reverse dependent, got: %+v", res)
	}
}

func TestCheckDocParity_LinksAndAnchors(t *testing.T) {
	dir := t.TempDir()

	docTarget := `# Architecture Overview

Some intro text.

## Core Invariants

Details here.
`
	if err := os.WriteFile(filepath.Join(dir, "target.md"), []byte(docTarget), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Clean markdown with valid link and valid anchor
	cleanMD := `# Design Document

See [Core Invariants](target.md#core-invariants).
`
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(cleanMD), 0o644); err != nil {
		t.Fatal(err)
	}

	inClean := StatusPassInput{Dir: dir}
	res := runCheckDocParity(inClean)
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for valid links, got: %+v", res)
	}

	// 2. Broken link (target file missing)
	brokenLinkMD := `# Document
See [Missing](missing_file.md).
`
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(brokenLinkMD), 0o644); err != nil {
		t.Fatal(err)
	}
	res = runCheckDocParity(inClean)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "target not found") {
		t.Fatalf("expected FAIL on broken link, got: %+v", res)
	}

	// 3. Broken anchor (file exists, anchor missing)
	brokenAnchorMD := `# Document
See [Bad Anchor](target.md#non-existent-section).
`
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(brokenAnchorMD), 0o644); err != nil {
		t.Fatal(err)
	}
	res = runCheckDocParity(inClean)
	if res.Status != "FAIL" || !strings.Contains(res.Reason, "broken anchor") {
		t.Fatalf("expected FAIL on broken anchor, got: %+v", res)
	}
}

func TestScriptCard_AdmissionAndUnmeteredSpend(t *testing.T) {
	// 1. Valid script card admitted with JSON argv array and unmetered spend "-"
	validCard := `MODE: script
ARGV: ["go", "test", "./internal/pulse/..."]
RESULT: CARD-1 run status checks
`
	card, err := AdmitScriptCard(validCard)
	if err != nil {
		t.Fatalf("AdmitScriptCard failed on valid card: %v", err)
	}
	if card.Mode != "script" {
		t.Errorf("Mode = %q, want script", card.Mode)
	}
	if len(card.Argv) != 3 || card.Argv[0] != "go" || card.Argv[1] != "test" {
		t.Errorf("Argv = %v, want ['go', 'test', './internal/pulse/...']", card.Argv)
	}
	if card.Spend != "-" {
		t.Errorf("Spend = %q, want '-' for unmetered spend", card.Spend)
	}

	// 2. Refuses card missing MODE: script
	noMode := `ARGV: ["ls", "-la"]`
	if _, err := AdmitScriptCard(noMode); err == nil {
		t.Fatal("expected error on card missing MODE: script")
	}

	// 3. Refuses card missing ARGV array (never implicit shell)
	noArgv := `MODE: script
RUN: go test ./...
`
	if _, err := AdmitScriptCard(noArgv); err == nil || !strings.Contains(err.Error(), "missing ARGV") {
		t.Fatalf("expected missing ARGV error, got: %v", err)
	}

	// 4. Refuses login shells at admission
	loginShells := []string{
		`MODE: script` + "\n" + `ARGV: ["bash", "-l", "-c", "go test"]`,
		`MODE: script` + "\n" + `ARGV: ["/bin/bash", "--login", "script.sh"]`,
		`MODE: script` + "\n" + `ARGV: ["zsh", "-l", "script.sh"]`,
		`MODE: script` + "\n" + `ARGV: ["/usr/bin/zsh", "--login"]`,
		`MODE: script` + "\n" + `ARGV: ["sh", "-l"]`,
		`MODE: script` + "\n" + `ARGV: ["-bash"]`,
	}
	for _, lsh := range loginShells {
		_, err := AdmitScriptCard(lsh)
		if err == nil {
			t.Fatalf("login shell was admitted: %q", lsh)
		}
		if !strings.Contains(err.Error(), "login shell") {
			t.Fatalf("error %q does not name login shell", err.Error())
		}
	}
}

func TestStatusPass_RunsBeforeReadCardCut(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	// Case 1: Status pass fails -> CutKind refuses to cut read card
	var errb bytes.Buffer
	code := CutKind(CutKindInput{
		Kind:  "read",
		Repo:  "mas-bandwidth/nova-tools",
		PR:    812,
		Head:  "abc123def456",
		Title: "read card test",
		Out:   out,
		Queue: queue,
		StatusPassRunner: func(in CutKindInput) (bool, string) {
			return false, "red-first failed: no test in diff"
		},
		Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2 on status pass refusal", code)
	}
	if !strings.Contains(errb.String(), "CUT REFUSED: status pass failed") {
		t.Fatalf("stderr does not name status pass failure:\n%s", errb.String())
	}
	if files, _ := os.ReadDir(out); len(files) > 0 {
		t.Fatalf("read card was cut despite status pass failure: %v", files)
	}

	// Case 2: Status pass passes -> CutKind cuts the read card
	var outb bytes.Buffer
	errb.Reset()
	code = CutKind(CutKindInput{
		Kind:  "read",
		Repo:  "mas-bandwidth/nova-tools",
		PR:    812,
		Head:  "abc123def456",
		Title: "read card test",
		Out:   out,
		Queue: queue,
		StatusPassRunner: func(in CutKindInput) (bool, string) {
			return true, ""
		},
		Stdout: &outb,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 on green status pass: %s", code, errb.String())
	}
	if files, _ := os.ReadDir(out); len(files) != 1 {
		t.Fatalf("read card was not cut on green status pass")
	}
}

func TestRefillReads_StatusPassEnforced(t *testing.T) {
	// Verify that w.refillReads does not cut read cards when StatusPass returns false
	var logged []string
	w := &Wiring{
		in: WiringInput{
			Repo:  "mas-bandwidth/nova-tools",
			Queue: t.TempDir(),
			Work: &mockWorkSource{
				prs: []OpenPR{
					{Number: 100, Head: "head100", Title: "fix bug"},
					{Number: 200, Head: "head200", Title: "add feature"},
				},
			},
			StatusPass: func(repo string, pr int, head string) bool {
				// Only PR 200 has green status pass
				return pr == 200
			},
			Log: &logCapture{log: func(s string) { logged = append(logged, s) }},
		},
	}
	if err := os.MkdirAll(filepath.Join(w.in.Queue, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	workset := map[int]bool{100: true, 200: true}

	cutCount := w.refillReads(workset)
	if cutCount != 1 {
		t.Fatalf("cutCount = %d, want 1 (only PR 200 passed status pass)", cutCount)
	}

	foundFailureLog := false
	for _, l := range logged {
		if strings.Contains(l, "status pass failed for PR100") {
			foundFailureLog = true
		}
	}
	if !foundFailureLog {
		t.Errorf("missing log line for failed PR 100 status pass in logs:\n%v", logged)
	}
}

type mockWorkSource struct {
	prs    []OpenPR
	issues []OpenIssue
}

func (m *mockWorkSource) OpenPRs(repo string) ([]OpenPR, error)       { return m.prs, nil }
func (m *mockWorkSource) OpenIssues(repo string) ([]OpenIssue, error) { return m.issues, nil }

type logCapture struct {
	log func(string)
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.log(string(p))
	return len(p), nil
}

func TestMutation_StatusPassInvariantsWithTeeth(t *testing.T) {
	// Baseline: all 6 checks passing
	baselineInput := func() StatusPassInput {
		return StatusPassInput{
			Repo: "mas-bandwidth/nova-tools",
			Head: "112233445566",
			Base: "dev",
			CmdRunner: func(dir string, env []string, name string, args ...string) (string, error) {
				if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
					return "112233445566\n", nil
				}
				return "", nil
			},
			CheckRedFirst: func(in StatusPassInput) CheckResult {
				return CheckResult{Check: CheckRedFirst, Status: "PASS", Detail: "red-first pass"}
			},
			CheckHermetic: func(in StatusPassInput) CheckResult {
				return CheckResult{Check: CheckHermetic, Status: "PASS", Detail: "hermetic pass"}
			},
			CheckLint: func(in StatusPassInput) CheckResult {
				return CheckResult{Check: CheckLint, Status: "PASS", Detail: "lint pass"}
			},
			CheckCleanTree: func(in StatusPassInput) CheckResult {
				return CheckResult{Check: CheckCleanTree, Status: "PASS", Detail: "clean-tree pass"}
			},
			CheckRevDeps: func(in StatusPassInput) CheckResult {
				return CheckResult{Check: CheckReverseDependents, Status: "PASS", Detail: "rev-deps pass"}
			},
			CheckDocParity: func(in StatusPassInput) CheckResult {
				return CheckResult{Check: CheckDocParity, Status: "PASS", Detail: "doc-parity pass"}
			},
		}
	}

	// Verify baseline passes
	baseRes := StatusPass(baselineInput())
	if !baseRes.OK || baseRes.PassedCount != 6 {
		t.Fatalf("baseline check failed: %+v", baseRes)
	}

	// Mutations with teeth: each check failure must be caught and named by the status pass engine
	mutations := []struct {
		name      string
		mutate    func(*StatusPassInput)
		wantCheck string
		wantError string
	}{
		{
			name: "red-first-failure",
			mutate: func(in *StatusPassInput) {
				in.CheckRedFirst = func(in StatusPassInput) CheckResult {
					return CheckResult{Check: CheckRedFirst, Status: "FAIL", Reason: "reproducing test passes on baseline without fix"}
				}
			},
			wantCheck: CheckRedFirst,
			wantError: "reproducing test passes on baseline without fix",
		},
		{
			name: "hermetic-sandbox-leak-failure",
			mutate: func(in *StatusPassInput) {
				in.CheckHermetic = func(in StatusPassInput) CheckResult {
					return CheckResult{Check: CheckHermetic, Status: "FAIL", Reason: "secrets leaked into sandbox environment"}
				}
			},
			wantCheck: CheckHermetic,
			wantError: "secrets leaked into sandbox environment",
		},
		{
			name: "lint-gofmt-dirty-failure",
			mutate: func(in *StatusPassInput) {
				in.CheckLint = func(in StatusPassInput) CheckResult {
					return CheckResult{Check: CheckLint, Status: "FAIL", Reason: "gofmt dirty: internal/pulse/foo.go"}
				}
			},
			wantCheck: CheckLint,
			wantError: "gofmt dirty",
		},
		{
			name: "clean-tree-binary-file-failure",
			mutate: func(in *StatusPassInput) {
				in.CheckCleanTree = func(in StatusPassInput) CheckResult {
					return CheckResult{Check: CheckCleanTree, Status: "FAIL", Reason: "binary file committed: bin/daemon"}
				}
			},
			wantCheck: CheckCleanTree,
			wantError: "binary file committed",
		},
		{
			name: "reverse-dependents-breakage-failure",
			mutate: func(in *StatusPassInput) {
				in.CheckRevDeps = func(in StatusPassInput) CheckResult {
					return CheckResult{Check: CheckReverseDependents, Status: "FAIL", Reason: "reverse dependent pkg/caller failed to build"}
				}
			},
			wantCheck: CheckReverseDependents,
			wantError: "reverse dependent",
		},
		{
			name: "doc-parity-broken-anchor-failure",
			mutate: func(in *StatusPassInput) {
				in.CheckDocParity = func(in StatusPassInput) CheckResult {
					return CheckResult{Check: CheckDocParity, Status: "FAIL", Reason: "broken anchor in SPEC.md: #missing"}
				}
			},
			wantCheck: CheckDocParity,
			wantError: "broken anchor",
		},
	}

	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			input := baselineInput()
			m.mutate(&input)
			res := StatusPass(input)
			if res.OK {
				t.Fatalf("mutation %q was NOT caught: result OK=true", m.name)
			}
			if res.FailedCheck != m.wantCheck {
				t.Fatalf("mutation %q: FailedCheck = %q, want %q", m.name, res.FailedCheck, m.wantCheck)
			}
			if !strings.Contains(res.FailureReason, m.wantError) {
				t.Fatalf("mutation %q: FailureReason %q did not contain %q", m.name, res.FailureReason, m.wantError)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Tests with Teeth for the 6 Exact Review Findings
// -----------------------------------------------------------------------------

// Finding 1: Wall Truthfulness
func TestStatusPass_Finding1_HermeticWallTruthfulness(t *testing.T) {
	// Subtest A: Sandboxing policy requires valid nova-sandbox
	res := runCheckHermetic(StatusPassInput{
		Dir:     t.TempDir(),
		Sandbox: "/nonexistent/path/to/nova-sandbox",
	})
	if res.Status != "FAIL" {
		t.Fatalf("expected hermetic failure with nonexistent sandbox binary, got %+v", res)
	}

	// Subtest B: Missing sandbox binary in real run returns containment below model refusal
	resMissing := runCheckHermetic(StatusPassInput{
		Dir:       t.TempDir(),
		Sandbox:   "",
		CmdRunner: nil, // real execution
	})
	if resMissing.Status == "FAIL" {
		if !strings.Contains(resMissing.Reason, "nova-sandbox") && !strings.Contains(resMissing.Reason, "sandbox") {
			t.Errorf("expected failure reason mentioning sandbox, got %q", resMissing.Reason)
		}
	}

	// Subtest C: NetDeny flag verification in runner
	var capturedArgs []string
	fakeRunner := func(dir string, env []string, name string, args ...string) (string, error) {
		capturedArgs = args
		return "ok", nil
	}
	resRunner := runCheckHermetic(StatusPassInput{
		Dir:       t.TempDir(),
		Sandbox:   "/usr/local/bin/nova-sandbox",
		CmdRunner: fakeRunner,
	})
	if resRunner.Status != "PASS" {
		t.Fatalf("expected PASS with fake runner, got %+v", resRunner)
	}
	hasNetDeny := false
	for _, a := range capturedArgs {
		if a == "--net-deny" {
			hasNetDeny = true
			break
		}
	}
	if !hasNetDeny {
		t.Errorf("runCheckHermetic did not pass --net-deny to sandbox: %v", capturedArgs)
	}
}

// Finding 2: Baseline Tag & Assertion Verification
func TestStatusPass_Finding2_RedFirst_DistinguishesAssertionFromBuildError(t *testing.T) {
	// Subtest A: Baseline fails with syntax error -> must FAIL (not treated as reproduced red assertion)
	mockRunnerSyntaxErr := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "diff" {
			return "pkg/sample_test.go\npkg/sample.go\n", nil
		}
		if len(args) > 1 && args[1] == "-tags=baseline_test" {
			return "# pkg\npkg/sample.go:12:2: syntax error: unexpected semicolon", fmt.Errorf("exit status 2")
		}
		return "ok", nil
	}
	resSyntax := runCheckRedFirst(StatusPassInput{
		Dir:       t.TempDir(),
		CmdRunner: mockRunnerSyntaxErr,
	})
	if resSyntax.Status != "FAIL" {
		t.Fatalf("expected syntax error to FAIL red-first check, got %+v", resSyntax)
	}
	if !strings.Contains(resSyntax.Reason, "build/syntax error") {
		t.Errorf("expected reason to specify build/syntax error, got %q", resSyntax.Reason)
	}

	// Subtest B: Baseline fails with undefined identifier error -> must FAIL
	mockRunnerUndefinedErr := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "diff" {
			return "pkg/sample_test.go\npkg/sample.go\n", nil
		}
		if len(args) > 1 && args[1] == "-tags=baseline_test" {
			return "# pkg\npkg/sample_test.go:20:5: undefined: MyNewFeature", fmt.Errorf("exit status 2")
		}
		return "ok", nil
	}
	resUndefined := runCheckRedFirst(StatusPassInput{
		Dir:       t.TempDir(),
		CmdRunner: mockRunnerUndefinedErr,
	})
	if resUndefined.Status != "FAIL" {
		t.Fatalf("expected undefined identifier to FAIL red-first check, got %+v", resUndefined)
	}

	// Subtest C: Genuine test assertion failure (--- FAIL:) on baseline -> PASS with fix
	mockRunnerAssertionFail := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "diff" {
			return "pkg/sample_test.go\npkg/sample.go\n", nil
		}
		if len(args) > 1 && args[1] == "-tags=baseline_test" {
			return "--- FAIL: TestSample (0.00s)\n    sample_test.go:15: expected 42, got 0\nFAIL", fmt.Errorf("exit status 1")
		}
		return "ok  pkg 0.01s", nil
	}
	resAssertion := runCheckRedFirst(StatusPassInput{
		Dir:       t.TempDir(),
		CmdRunner: mockRunnerAssertionFail,
	})
	if resAssertion.Status != "PASS" {
		t.Fatalf("expected genuine test assertion to pass red-first check, got %+v", resAssertion)
	}

	// Subtest D: Additive test guard (only _test.go changed, passes green)
	mockRunnerAdditive := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "diff" {
			return "pkg/sample_test.go\n", nil
		}
		return "ok  pkg 0.01s", nil
	}
	resAdditive := runCheckRedFirst(StatusPassInput{
		Dir:       t.TempDir(),
		CmdRunner: mockRunnerAdditive,
	})
	if resAdditive.Status != "PASS" {
		t.Fatalf("expected additive test guard to PASS, got %+v", resAdditive)
	}
	if !strings.Contains(resAdditive.Detail, "additive test guard") {
		t.Errorf("expected detail to mention additive test guard, got %q", resAdditive.Detail)
	}
}

// Finding 3: Tested Identity Verification
func TestStatusPass_Finding3_TestedIdentity(t *testing.T) {
	// Subtest A: HEAD mismatch
	mockRunnerHeadMismatch := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
			return "1111111111111111111111111111111111111111\n", nil
		}
		return "", nil
	}
	var out bytes.Buffer
	resHeadMismatch := StatusPass(StatusPassInput{
		Dir:       t.TempDir(),
		Head:      "2222222222222222222222222222222222222222",
		Repo:      "mas-bandwidth/nova-tools",
		CmdRunner: mockRunnerHeadMismatch,
		Stdout:    &out,
	})
	if resHeadMismatch.OK {
		t.Fatalf("expected StatusPass to fail on HEAD mismatch")
	}
	if resHeadMismatch.FailedCheck != "identity" {
		t.Fatalf("FailedCheck = %q, want identity", resHeadMismatch.FailedCheck)
	}
	if !strings.Contains(resHeadMismatch.FailureReason, "does not match expected head") {
		t.Errorf("unexpected failure reason: %q", resHeadMismatch.FailureReason)
	}

	// Subtest B: Origin URL mismatch
	mockRunnerOriginMismatch := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
			return "3333333333333333333333333333333333333333\n", nil
		}
		if len(args) >= 3 && args[0] == "config" && args[2] == "remote.origin.url" {
			return "https://github.com/someone-else/other-repo.git\n", nil
		}
		return "", nil
	}
	out.Reset()
	resRepoMismatch := StatusPass(StatusPassInput{
		Dir:       t.TempDir(),
		Head:      "3333333333333333333333333333333333333333",
		Repo:      "mas-bandwidth/nova-tools",
		CmdRunner: mockRunnerOriginMismatch,
		Stdout:    &out,
	})
	if resRepoMismatch.OK {
		t.Fatalf("expected StatusPass to fail on Repo mismatch")
	}
	if resRepoMismatch.FailedCheck != "identity" {
		t.Fatalf("FailedCheck = %q, want identity", resRepoMismatch.FailedCheck)
	}
	if !strings.Contains(resRepoMismatch.FailureReason, "does not match expected repo") {
		t.Errorf("unexpected failure reason: %q", resRepoMismatch.FailureReason)
	}
}

// Finding 4: Production Gate Construction & Refusal on Absence
func TestStatusPass_Finding4_GateConstructionAndAbsenceRefusal(t *testing.T) {
	// Subtest A: NewWiring defaults StatusPass
	w := NewWiring(WiringInput{
		Queue: t.TempDir(),
	})
	if w.in.StatusPass == nil {
		t.Fatalf("NewWiring did not default in.StatusPass")
	}

	// Subtest B: refillReads refuses when StatusPass is explicitly nil (absence refuses)
	var logged []string
	wNil := &Wiring{
		in: WiringInput{
			Repo:  "mas-bandwidth/nova-tools",
			Queue: t.TempDir(),
			Work: &mockWorkSource{
				prs: []OpenPR{{Number: 1, Head: "head1", Title: "title1"}},
			},
			StatusPass: nil,
			Log:        &logCapture{log: func(s string) { logged = append(logged, s) }},
		},
	}
	if err := os.MkdirAll(filepath.Join(wNil.in.Queue, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	cut := wNil.refillReads(map[int]bool{1: true})
	if cut != 0 {
		t.Fatalf("cut = %d, want 0 when StatusPass is unavailable", cut)
	}
	foundLog := false
	for _, l := range logged {
		if strings.Contains(l, "status pass unavailable") {
			foundLog = true
			break
		}
	}
	if !foundLog {
		t.Fatalf("expected log about status pass unavailable, got %v", logged)
	}
}

// Finding 5: Script Lifecycle, RUN header parsing, and Output Bounding
func TestStatusPass_Finding5_ScriptLifecycle(t *testing.T) {
	// Subtest A: RUN header parsing
	cardText := "MODE: script\nRUN: [\"go\", \"test\", \"./...\"]\n\n# Body markdown\nRUN: [\"echo\", \"ignored\"]\nARGV: [\"echo\", \"ignored\"]\n"
	card, err := AdmitScriptCard(cardText)
	if err != nil {
		t.Fatalf("AdmitScriptCard failed on valid RUN header: %v", err)
	}
	if len(card.Argv) != 3 || card.Argv[0] != "go" || card.Argv[1] != "test" || card.Argv[2] != "./..." {
		t.Fatalf("card.Argv = %v, want [go test ./...]", card.Argv)
	}

	// Subtest B: Duplicate header lines rejected
	dupMode := "MODE: script\nMODE: worker\nRUN: [\"go\", \"test\"]\n"
	if _, err := AdmitScriptCard(dupMode); err == nil {
		t.Fatalf("expected error on duplicate MODE headers")
	}
	dupRun := "MODE: script\nRUN: [\"go\", \"test\"]\nRUN: [\"go\", \"vet\"]\n"
	if _, err := AdmitScriptCard(dupRun); err == nil {
		t.Fatalf("expected error on duplicate RUN headers")
	}

	// Subtest C: Bounded output buffer truncates and reports limit
	buf := &boundedOutputBuffer{limit: 20}
	n, _ := buf.Write([]byte("01234567890123456789EXTRA_BYTES"))
	if n != 31 {
		t.Errorf("Write n = %d, want 31", n)
	}
	if !buf.truncated {
		t.Errorf("expected buf.truncated = true")
	}
	if !strings.Contains(buf.String(), "OUTPUT TRUNCATED: exceeded 20 bytes hard bound") {
		t.Errorf("buffer output missing truncation notice:\n%s", buf.String())
	}

	// Subtest D: ScriptCard.Execute fails closed if NetDeny is true and sandbox missing
	cardNetDeny := &ScriptCard{
		Mode:    "script",
		Argv:    []string{"echo", "hi"},
		NetDeny: true,
		Sandbox: "/nonexistent/path/to/nova-sandbox",
	}
	code, _, err := cardNetDeny.Execute(t.TempDir(), nil)
	if code != 125 {
		t.Fatalf("exit code = %d, want 125 on missing sandbox with NetDeny", code)
	}
	if err == nil || !strings.Contains(err.Error(), "nova-sandbox") {
		t.Fatalf("expected error mentioning nova-sandbox, got: %v", err)
	}
}

// Finding 6: Reverse Dependency Discovery Failure Must FAIL
func TestStatusPass_Finding6_ReverseDependents_DiscoveryFailure(t *testing.T) {
	// Subtest A: go list error fails reverse dependents check
	mockGoListErr := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list" {
			return "go list: parsing /path/to/pkg: go.mod not found", fmt.Errorf("exit status 1")
		}
		return "", nil
	}
	resGoList := runCheckReverseDependents(StatusPassInput{
		Dir:       t.TempDir(),
		CmdRunner: mockGoListErr,
	})
	if resGoList.Status != "FAIL" {
		t.Fatalf("expected go list discovery error to FAIL, got %+v", resGoList)
	}
	if !strings.Contains(resGoList.Reason, "package dependency discovery failed") {
		t.Errorf("unexpected reason: %q", resGoList.Reason)
	}

	// Subtest B: git diff error fails reverse dependents check
	mockDiffErr := func(dir string, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list" {
			return "github.com/foo/bar", nil
		}
		if len(args) > 0 && args[0] == "diff" {
			return "fatal: ambiguous argument 'invalid-base...HEAD'", fmt.Errorf("exit status 128")
		}
		return "", nil
	}
	resDiff := runCheckReverseDependents(StatusPassInput{
		Dir:       t.TempDir(),
		Base:      "invalid-base",
		CmdRunner: mockDiffErr,
	})
	if resDiff.Status != "FAIL" {
		t.Fatalf("expected git diff error to FAIL, got %+v", resDiff)
	}
	if !strings.Contains(resDiff.Reason, "git diff failed against base") {
		t.Errorf("unexpected reason: %q", resDiff.Reason)
	}
}
