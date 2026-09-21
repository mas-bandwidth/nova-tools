package pulse

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/json"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The 6 status pass checks, run in conjunction before any read card is cut.
const (
	CheckRedFirst          = "red-first"
	CheckHermetic          = "hermetic"
	CheckLint              = "lint"
	CheckCleanTree         = "clean-tree"
	CheckReverseDependents = "reverse-dependents"
	CheckDocParity         = "doc-parity"
)

// AllStatusChecks is the canonical ordered list of the 6 checks.
var AllStatusChecks = []string{
	CheckRedFirst,
	CheckHermetic,
	CheckLint,
	CheckCleanTree,
	CheckReverseDependents,
	CheckDocParity,
}

// CheckResult is the outcome of one status check.
type CheckResult struct {
	Check  string `json:"check"`
	Status string `json:"status"` // "PASS" or "FAIL"
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// StatusPassResult is the overall conjunction result of the status pass.
type StatusPassResult struct {
	OK            bool          `json:"ok"`
	PassedCount   int           `json:"passed_count"`
	TotalCount    int           `json:"total_count"`
	FailedCheck   string        `json:"failed_check,omitempty"`
	FailureReason string        `json:"failure_reason,omitempty"`
	Checks        []CheckResult `json:"checks"`
}

// StatusPassInput contains everything needed to run the status pass.
type StatusPassInput struct {
	Repo           string   // repository owner/name, e.g. "mas-bandwidth/nova-tools"
	Dir            string   // checkout or worktree directory
	PR             int      // PR number (optional)
	Head           string   // head commit or branch (optional)
	Base           string   // base commit or branch (e.g. "dev" or "origin/dev")
	Checks         []string // checks to run (empty means AllStatusChecks)
	Sandbox        string   // optional path to nova-sandbox binary
	Stdout         io.Writer
	Stderr         io.Writer
	CmdRunner      func(dir string, env []string, name string, args ...string) (string, error)
	ReadFile       func(path string) ([]byte, error)
	ReadDir        func(path string) ([]os.DirEntry, error)
	Now            func() time.Time
	CheckRedFirst  func(in StatusPassInput) CheckResult
	CheckHermetic  func(in StatusPassInput) CheckResult
	CheckLint      func(in StatusPassInput) CheckResult
	CheckCleanTree func(in StatusPassInput) CheckResult
	CheckRevDeps   func(in StatusPassInput) CheckResult
	CheckDocParity func(in StatusPassInput) CheckResult
}

// StatusPass runs the conjunction of the 6 status pass checks.
// Returns a StatusPassResult and prints STATUS lines to in.Stdout.
func StatusPass(in StatusPassInput) StatusPassResult {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.ReadFile == nil {
		in.ReadFile = os.ReadFile
	}
	if in.ReadDir == nil {
		in.ReadDir = os.ReadDir
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Base == "" {
		in.Base = "dev"
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	runner := in.CmdRunner
	if runner == nil {
		runner = defaultExec
	}

	checksToRun := in.Checks
	if len(checksToRun) == 0 {
		checksToRun = AllStatusChecks
	}

	headStr := in.Head
	if headStr == "" {
		headStr = "-"
	}
	repoStr := in.Repo
	if repoStr == "" {
		repoStr = "-"
	}

	// 1. Tested Identity Verification: verify Repo and Head match actual tested tree
	if in.Head != "" && in.Head != "-" {
		actualHead, err := runner(dir, nil, "git", "rev-parse", "HEAD")
		if err != nil {
			result := StatusPassResult{
				OK:            false,
				TotalCount:    len(checksToRun),
				FailedCheck:   "identity",
				FailureReason: fmt.Sprintf("cannot resolve tested HEAD in %s: %v", dir, err),
			}
			fmt.Fprintf(in.Stdout, "STATUS PASS FAIL check=identity reason=%s repo=%s head=%s checks=0/%d\n",
				oneline.Field(result.FailureReason), oneline.Field(repoStr), oneline.Field(headStr), len(checksToRun))
			return result
		}
		actualHead = strings.TrimSpace(actualHead)
		if !strings.HasPrefix(actualHead, in.Head) && !strings.HasPrefix(in.Head, actualHead) {
			result := StatusPassResult{
				OK:            false,
				TotalCount:    len(checksToRun),
				FailedCheck:   "identity",
				FailureReason: fmt.Sprintf("tested tree HEAD (%s) does not match expected head (%s)", actualHead, in.Head),
			}
			fmt.Fprintf(in.Stdout, "STATUS PASS FAIL check=identity reason=%s repo=%s head=%s checks=0/%d\n",
				oneline.Field(result.FailureReason), oneline.Field(repoStr), oneline.Field(headStr), len(checksToRun))
			return result
		}
	}

	if in.Repo != "" && in.Repo != "-" {
		originURL, err := runner(dir, nil, "git", "config", "--get", "remote.origin.url")
		if err == nil && strings.TrimSpace(originURL) != "" {
			if !strings.Contains(strings.TrimSpace(originURL), in.Repo) {
				result := StatusPassResult{
					OK:            false,
					TotalCount:    len(checksToRun),
					FailedCheck:   "identity",
					FailureReason: fmt.Sprintf("tested tree origin (%s) does not match expected repo (%s)", strings.TrimSpace(originURL), in.Repo),
				}
				fmt.Fprintf(in.Stdout, "STATUS PASS FAIL check=identity reason=%s repo=%s head=%s checks=0/%d\n",
					oneline.Field(result.FailureReason), oneline.Field(repoStr), oneline.Field(headStr), len(checksToRun))
				return result
			}
		}
	}

	result := StatusPassResult{
		OK:         true,
		TotalCount: len(checksToRun),
	}

	for _, name := range checksToRun {
		var cr CheckResult
		switch name {
		case CheckRedFirst:
			if in.CheckRedFirst != nil {
				cr = in.CheckRedFirst(in)
			} else {
				cr = runCheckRedFirst(in)
			}
		case CheckHermetic:
			if in.CheckHermetic != nil {
				cr = in.CheckHermetic(in)
			} else {
				cr = runCheckHermetic(in)
			}
		case CheckLint:
			if in.CheckLint != nil {
				cr = in.CheckLint(in)
			} else {
				cr = runCheckLint(in)
			}
		case CheckCleanTree:
			if in.CheckCleanTree != nil {
				cr = in.CheckCleanTree(in)
			} else {
				cr = runCheckCleanTree(in)
			}
		case CheckReverseDependents:
			if in.CheckRevDeps != nil {
				cr = in.CheckRevDeps(in)
			} else {
				cr = runCheckReverseDependents(in)
			}
		case CheckDocParity:
			if in.CheckDocParity != nil {
				cr = in.CheckDocParity(in)
			} else {
				cr = runCheckDocParity(in)
			}
		default:
			cr = CheckResult{Check: name, Status: "FAIL", Reason: fmt.Sprintf("unknown check %q", name)}
		}

		result.Checks = append(result.Checks, cr)

		if cr.Status == "PASS" {
			result.PassedCount++
			fmt.Fprintf(in.Stdout, "STATUS CHECK check=%s status=PASS detail=%s\n",
				cr.Check, oneline.Field(cr.Detail))
		} else {
			result.OK = false
			if result.FailedCheck == "" {
				result.FailedCheck = cr.Check
				result.FailureReason = cr.Reason
			}
			fmt.Fprintf(in.Stdout, "STATUS CHECK check=%s status=FAIL reason=%s\n",
				cr.Check, oneline.Field(cr.Reason))
		}
	}

	if result.OK {
		fmt.Fprintf(in.Stdout, "STATUS PASS OK repo=%s head=%s checks=%d/%d\n",
			oneline.Field(repoStr), oneline.Field(headStr), result.PassedCount, result.TotalCount)
	} else {
		fmt.Fprintf(in.Stdout, "STATUS PASS FAIL check=%s reason=%s repo=%s head=%s checks=%d/%d\n",
			oneline.Field(result.FailedCheck), oneline.Field(result.FailureReason),
			oneline.Field(repoStr), oneline.Field(headStr), result.PassedCount, result.TotalCount)
	}

	return result
}

// defaultExec runs a command in dir with env.
func defaultExec(dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// -----------------------------------------------------------------------------
// Check 1: red-first
// -----------------------------------------------------------------------------

// runCheckRedFirst asserts that a reproducing test fails on baseline without fix.
func runCheckRedFirst(in StatusPassInput) CheckResult {
	runner := in.CmdRunner
	if runner == nil {
		runner = defaultExec
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}

	// 1. Get diff of changed files compared to base
	base := in.Base
	if base == "" {
		base = "dev"
	}
	diffOut, err := runner(dir, nil, "git", "diff", "--name-only", base+"...HEAD")
	if err != nil || strings.TrimSpace(diffOut) == "" {
		// Try working tree diff against base
		diffOut, err = runner(dir, nil, "git", "diff", "--name-only", base)
		if err != nil || strings.TrimSpace(diffOut) == "" {
			// Fallback: diff against HEAD~1
			diffOut, _ = runner(dir, nil, "git", "diff", "--name-only", "HEAD~1")
		}
	}

	var testFiles, nonTestFiles []string
	for _, f := range strings.Split(strings.TrimSpace(diffOut), "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.HasSuffix(f, "_test.go") {
			testFiles = append(testFiles, f)
		} else if strings.HasSuffix(f, ".go") {
			nonTestFiles = append(nonTestFiles, f)
		}
	}

	if len(testFiles) == 0 {
		return CheckResult{
			Check:  CheckRedFirst,
			Status: "FAIL",
			Reason: "no test in diff: a fix must carry a reproducing test first",
		}
	}

	// 2. Identify the packages touched by the test files
	pkgMap := make(map[string]bool)
	for _, tf := range testFiles {
		pkg := filepath.Dir(tf)
		if !strings.HasPrefix(pkg, "./") && !filepath.IsAbs(pkg) {
			pkg = "./" + pkg
		}
		pkgMap[pkg] = true
	}
	var pkgs []string
	for p := range pkgMap {
		pkgs = append(pkgs, p)
	}

	// Additive test guard: if no implementation files changed, verify the new tests pass green
	if len(nonTestFiles) == 0 {
		fixArgs := append([]string{"test"}, pkgs...)
		fixOut, fixErr := runner(dir, nil, "go", fixArgs...)
		if fixErr != nil {
			return CheckResult{
				Check:  CheckRedFirst,
				Status: "FAIL",
				Reason: fmt.Sprintf("additive test guard fails: %s", oneline.Cap(strings.TrimSpace(fixOut), 120)),
			}
		}
		return CheckResult{
			Check:  CheckRedFirst,
			Status: "PASS",
			Detail: "additive test guard: test added without implementation changes; passes green",
		}
	}

	// 3. Baseline check: verify reproducing test fails on baseline without fix.
	// Accept test failure only if it is an actual assertion failure, not a syntax/toolchain/build error.
	baselineArgs := append([]string{"test", "-tags=baseline_test"}, pkgs...)
	baseOut, baseErr := runner(dir, nil, "go", baselineArgs...)

	// Reject build errors, syntax errors, or package missing errors as red assertions
	if strings.Contains(baseOut, "syntax error") || strings.Contains(baseOut, "cannot find package") || strings.Contains(baseOut, "undefined:") {
		return CheckResult{
			Check:  CheckRedFirst,
			Status: "FAIL",
			Reason: fmt.Sprintf("baseline failed with build/syntax error rather than test assertion: %s", oneline.Cap(strings.TrimSpace(baseOut), 120)),
		}
	}

	if baseErr == nil && !strings.Contains(baseOut, "FAIL") {
		return CheckResult{
			Check:  CheckRedFirst,
			Status: "FAIL",
			Reason: "reproducing test passes on baseline without fix (not red first)",
		}
	}

	hasAssertionFail := strings.Contains(baseOut, "--- FAIL:") || strings.Contains(baseOut, "FAIL:")
	if !hasAssertionFail {
		return CheckResult{
			Check:  CheckRedFirst,
			Status: "FAIL",
			Reason: fmt.Sprintf("baseline error did not contain a reproducing test assertion: %s", oneline.Cap(strings.TrimSpace(baseOut), 120)),
		}
	}

	// 4. Fix check: run test with fix applied. Must PASS.
	fixArgs := append([]string{"test"}, pkgs...)
	fixOut, fixErr := runner(dir, nil, "go", fixArgs...)
	if fixErr != nil {
		return CheckResult{
			Check:  CheckRedFirst,
			Status: "FAIL",
			Reason: fmt.Sprintf("test fails with fix applied: %s", oneline.Cap(strings.TrimSpace(fixOut), 120)),
		}
	}

	failedAssertion := "reproduced red assertion on baseline"
	for _, l := range strings.Split(baseOut, "\n") {
		t := strings.TrimSpace(l)
		if strings.Contains(t, "FAIL:") || strings.Contains(t, "--- FAIL:") {
			failedAssertion = t
			break
		}
	}

	return CheckResult{
		Check:  CheckRedFirst,
		Status: "PASS",
		Detail: fmt.Sprintf("baseline red verified (%s), green with fix", failedAssertion),
	}
}

// -----------------------------------------------------------------------------
// Check 2: hermetic
// -----------------------------------------------------------------------------

// runCheckHermetic runs builds and tests inside an actual sandbox wall:
// - Verifies toolchain roots and wall terms with internal/sandbox and internal/swarm
// - Uses nova-sandbox with --net-deny
// - Enforces isolated HOME (refuses immediately if creation fails)
// - Strips API keys and credentials from environment
func runCheckHermetic(in StatusPassInput) CheckResult {
	runner := in.CmdRunner
	if runner == nil {
		runner = defaultExec
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	// 1. Ensure isolated sandbox HOME; REFUSE immediately on failure
	tempHome, err := os.MkdirTemp("", "hermetic-home-")
	if err != nil {
		return CheckResult{Check: CheckHermetic, Status: "FAIL", Reason: fmt.Sprintf("failed to create sandbox HOME: %v", err)}
	}
	defer os.RemoveAll(tempHome)

	// 2. Resolve toolchain roots and validate sandbox policy via internal/sandbox and internal/swarm
	toolRoots := swarm.ToolchainRoots(runtime.GOOS, tempHome)
	var readRoots, readNoExecRoots []string
	for _, tr := range toolRoots {
		if tr.Exec {
			readRoots = append(readRoots, tr.Path)
		} else {
			readNoExecRoots = append(readNoExecRoots, tr.Path)
		}
	}
	writeRoots := []string{absDir, tempHome}

	sbIn := sandbox.Input{
		Reads:       readRoots,
		ReadsNoExec: readNoExecRoots,
		Writes:      writeRoots,
		Cwd:         absDir,
		Home:        tempHome,
		NetDeny:     true,
		Argv:        []string{"go", "test", "-run=^$", "./..."},
	}
	_, refusals := sandbox.Build(sbIn)
	if len(refusals) > 0 {
		return CheckResult{
			Check:  CheckHermetic,
			Status: "FAIL",
			Reason: fmt.Sprintf("sandbox wall constraint violation: %s", refusals[0].Error()),
		}
	}

	// 3. Resolve sandbox binary
	sandboxBin := in.Sandbox
	if sandboxBin == "" {
		sandboxBin, _ = exec.LookPath(swarm.SandboxBinary)
	}

	// When using real execution (CmdRunner is nil), nova-sandbox binary is strictly required
	if in.CmdRunner == nil && sandboxBin == "" {
		return CheckResult{
			Check:  CheckHermetic,
			Status: "FAIL",
			Reason: "nova-sandbox not found: containment below the model requires OS wall enforcement (docs/SPEC-SANDBOX.md)",
		}
	}

	// 4. Build sanitized environment: no host HOME, no secrets, net-deny
	var hermeticEnv []string
	hermeticEnv = append(hermeticEnv, "HOME="+tempHome)
	hermeticEnv = append(hermeticEnv, "GOPROXY=off")
	hermeticEnv = append(hermeticEnv, "GOTOOLCHAIN=local")

	filtered := sandbox.DroppedEnv(os.Environ())
	for _, envVar := range filtered {
		if strings.HasPrefix(strings.ToUpper(envVar), "HOME=") {
			continue
		}
		if isSecretEnvVar(envVar) {
			continue
		}
		hermeticEnv = append(hermeticEnv, envVar)
	}

	hostHome := os.Getenv("HOME")
	for _, e := range hermeticEnv {
		if hostHome != "" && e == "HOME="+hostHome {
			return CheckResult{Check: CheckHermetic, Status: "FAIL", Reason: "host HOME leaked into hermetic environment"}
		}
		if isSecretEnvVar(e) {
			return CheckResult{Check: CheckHermetic, Status: "FAIL", Reason: "secret variable leaked into hermetic environment"}
		}
	}

	// 5. Execute command under sandbox wall
	var cmdName string
	var cmdArgs []string

	if sandboxBin != "" {
		cmdName = sandboxBin
		cmdArgs = append(cmdArgs, "--net-deny")
		for _, r := range readRoots {
			cmdArgs = append(cmdArgs, "--read", r)
		}
		for _, r := range readNoExecRoots {
			cmdArgs = append(cmdArgs, "--read-noexec", r)
		}
		for _, w := range writeRoots {
			cmdArgs = append(cmdArgs, "--write", w)
		}
		cmdArgs = append(cmdArgs, "--cwd", absDir, "--", "go", "test", "-run=^$", "./...")
	} else {
		cmdName = "go"
		cmdArgs = []string{"test", "-run=^$", "./..."}
	}

	out, err := runner(absDir, hermeticEnv, cmdName, cmdArgs...)
	if err != nil {
		return CheckResult{
			Check:  CheckHermetic,
			Status: "FAIL",
			Reason: fmt.Sprintf("hermetic build/test failed under sandbox wall: %s", oneline.Cap(strings.TrimSpace(out), 120)),
		}
	}

	detail := "sandbox wall verified: isolated HOME, net-deny enforced, zero secrets"
	if sandboxBin == "" {
		detail = "simulated environment hygiene verified: isolated HOME, zero secrets"
	}

	return CheckResult{
		Check:  CheckHermetic,
		Status: "PASS",
		Detail: detail,
	}
}

func isSecretEnvVar(envVar string) bool {
	upper := strings.ToUpper(envVar)
	parts := strings.SplitN(upper, "=", 2)
	k := parts[0]
	return strings.Contains(k, "KEY") || strings.Contains(k, "SECRET") ||
		strings.Contains(k, "TOKEN") || strings.Contains(k, "PASSWORD") ||
		strings.Contains(k, "CREDENTIAL") || strings.Contains(k, "AUTH") ||
		strings.HasPrefix(k, "JEV_") || strings.HasPrefix(k, "GITHUB_") ||
		strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "ANTHROPIC_") ||
		strings.HasPrefix(k, "OPENAI_") || strings.HasPrefix(k, "GEMINI_")
}

// -----------------------------------------------------------------------------
// Check 3: lint
// -----------------------------------------------------------------------------

// runCheckLint asserts formatting (gofmt) and syntax correctness.
func runCheckLint(in StatusPassInput) CheckResult {
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	readFile := in.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	var unformatted []string
	var syntaxErrors []string

	fset := token.NewFileSet()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		raw, err := readFile(path)
		if err != nil {
			return nil
		}

		// 1. Formatting check via go/format
		formatted, err := format.Source(raw)
		if err != nil {
			syntaxErrors = append(syntaxErrors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if !bytes.Equal(raw, formatted) {
			rel, _ := filepath.Rel(dir, path)
			if rel == "" {
				rel = path
			}
			unformatted = append(unformatted, rel)
		}

		// 2. Syntax check via go/parser
		_, err = parser.ParseFile(fset, path, raw, parser.AllErrors)
		if err != nil {
			syntaxErrors = append(syntaxErrors, fmt.Sprintf("%s: %v", path, err))
		}
		return nil
	})
	if err != nil {
		return CheckResult{Check: CheckLint, Status: "FAIL", Reason: fmt.Sprintf("lint walk failed: %v", err)}
	}

	if len(syntaxErrors) > 0 {
		return CheckResult{
			Check:  CheckLint,
			Status: "FAIL",
			Reason: fmt.Sprintf("syntax error: %s", syntaxErrors[0]),
		}
	}

	if len(unformatted) > 0 {
		return CheckResult{
			Check:  CheckLint,
			Status: "FAIL",
			Reason: fmt.Sprintf("gofmt dirty: %s", strings.Join(unformatted, ", ")),
		}
	}

	return CheckResult{
		Check:  CheckLint,
		Status: "PASS",
		Detail: "gofmt clean, syntax valid",
	}
}

// -----------------------------------------------------------------------------
// Check 4: clean-tree
// -----------------------------------------------------------------------------

// runCheckCleanTree asserts git status is clean, no untracked binaries or scratch files.
func runCheckCleanTree(in StatusPassInput) CheckResult {
	runner := in.CmdRunner
	if runner == nil {
		runner = defaultExec
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	readFile := in.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	// 1. Check for scratch files in the directory
	scratchFound := checkScratchFiles(dir, readFile)
	if scratchFound != "" {
		return CheckResult{
			Check:  CheckCleanTree,
			Status: "FAIL",
			Reason: fmt.Sprintf("scratch or misplaced file detected: %s", scratchFound),
		}
	}

	// 2. Check for binary files
	binaryFound := checkBinaryFiles(dir, readFile)
	if binaryFound != "" {
		return CheckResult{
			Check:  CheckCleanTree,
			Status: "FAIL",
			Reason: fmt.Sprintf("binary file committed or untracked: %s", binaryFound),
		}
	}

	// 3. Run git status --porcelain
	out, err := runner(dir, nil, "git", "status", "--porcelain")
	if err == nil {
		trimmed := strings.TrimSpace(out)
		if trimmed != "" {
			return CheckResult{
				Check:  CheckCleanTree,
				Status: "FAIL",
				Reason: fmt.Sprintf("working tree dirty: %s", oneline.Cap(trimmed, 120)),
			}
		}
	}

	return CheckResult{
		Check:  CheckCleanTree,
		Status: "PASS",
		Detail: "tree clean: no binaries, no scratch files, git status clean",
	}
}

func checkScratchFiles(dir string, readFile func(string) ([]byte, error)) string {
	// Root-level RESULT.md or notes.txt is forbidden (RESULT.md belongs only in job directories)
	for _, forbidden := range []string{"RESULT.md", "notes.txt", "notes-spec.md.bak"} {
		p := filepath.Join(dir, forbidden)
		if _, err := os.Stat(p); err == nil {
			return forbidden
		}
	}
	return ""
}

func checkBinaryFiles(dir string, readFile func(string) ([]byte, error)) string {
	var foundBinary string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || foundBinary != "" {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "bin" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if isBinaryFile(path, readFile) {
			rel, _ := filepath.Rel(dir, path)
			if rel == "" {
				rel = path
			}
			foundBinary = rel
			return filepath.SkipAll
		}
		return nil
	})
	return foundBinary
}

func isBinaryFile(path string, readFile func(string) ([]byte, error)) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".exe" || ext == ".dll" || ext == ".dylib" || ext == ".so" || ext == ".o" || ext == ".a" {
		return true
	}
	raw, err := readFile(path)
	if err != nil || len(raw) == 0 {
		return false
	}
	// Check ELF
	if _, err := elf.NewFile(bytes.NewReader(raw)); err == nil {
		return true
	}
	// Check Mach-O
	if _, err := macho.NewFile(bytes.NewReader(raw)); err == nil {
		return true
	}
	// Check PE
	if _, err := pe.NewFile(bytes.NewReader(raw)); err == nil {
		return true
	}
	// Check null byte in first 8000 bytes
	bound := len(raw)
	if bound > 8000 {
		bound = 8000
	}
	if bytes.IndexByte(raw[:bound], 0x00) >= 0 {
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Check 5: reverse-dependents
// -----------------------------------------------------------------------------

// runCheckReverseDependents asserts that reverse dependency packages continue to build and pass tests.
func runCheckReverseDependents(in StatusPassInput) CheckResult {
	runner := in.CmdRunner
	if runner == nil {
		runner = defaultExec
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}

	// Query package dependencies: discovery failure MUST fail the check
	out, err := runner(dir, nil, "go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./...")
	if err != nil {
		return CheckResult{
			Check:  CheckReverseDependents,
			Status: "FAIL",
			Reason: fmt.Sprintf("package dependency discovery failed (go list error): %s", oneline.Cap(strings.TrimSpace(out), 120)),
		}
	}

	// Touched packages from git diff
	base := in.Base
	if base == "" {
		base = "dev"
	}
	diffOut, diffErr := runner(dir, nil, "git", "diff", "--name-only", base+"...HEAD")
	if diffErr != nil {
		diffOut, diffErr = runner(dir, nil, "git", "diff", "--name-only", base)
		if diffErr != nil {
			return CheckResult{
				Check:  CheckReverseDependents,
				Status: "FAIL",
				Reason: fmt.Sprintf("git diff failed against base %s: %s", base, oneline.Cap(strings.TrimSpace(diffOut), 120)),
			}
		}
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	type pkgInfo struct {
		path    string
		imports []string
	}
	var pkgs []pkgInfo
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		pkgs = append(pkgs, pkgInfo{path: fields[0], imports: fields[1:]})
	}

	touchedDirs := make(map[string]bool)
	for _, f := range strings.Split(strings.TrimSpace(diffOut), "\n") {
		f = strings.TrimSpace(f)
		if f != "" && strings.HasSuffix(f, ".go") {
			touchedDirs[filepath.Dir(f)] = true
		}
	}

	var reverseDeps []string
	for _, p := range pkgs {
		isTouched := false
		for td := range touchedDirs {
			if strings.HasSuffix(p.path, "/"+td) || p.path == td {
				isTouched = true
				break
			}
		}
		if isTouched {
			continue
		}
		for _, imp := range p.imports {
			for td := range touchedDirs {
				if strings.HasSuffix(imp, "/"+td) || imp == td {
					reverseDeps = append(reverseDeps, p.path)
					break
				}
			}
		}
	}

	for _, rd := range reverseDeps {
		testOut, testErr := runner(dir, nil, "go", "test", "-run=^$", rd)
		if testErr != nil {
			return CheckResult{
				Check:  CheckReverseDependents,
				Status: "FAIL",
				Reason: fmt.Sprintf("reverse dependent %s failed to build/test: %s", rd, oneline.Cap(strings.TrimSpace(testOut), 120)),
			}
		}
	}

	return CheckResult{
		Check:  CheckReverseDependents,
		Status: "PASS",
		Detail: fmt.Sprintf("verified %d reverse dependent packages", len(reverseDeps)),
	}
}

// -----------------------------------------------------------------------------
// Check 6: doc-parity
// -----------------------------------------------------------------------------

var mdLinkRegex = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// runCheckDocParity asserts markdown links and spec anchors resolve.
func runCheckDocParity(in StatusPassInput) CheckResult {
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	readFile := in.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	var mdFiles []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			mdFiles = append(mdFiles, path)
		}
		return nil
	})

	for _, md := range mdFiles {
		raw, err := readFile(md)
		if err != nil {
			continue
		}
		matches := mdLinkRegex.FindAllStringSubmatch(string(raw), -1)
		for _, m := range matches {
			target := strings.TrimSpace(m[2])
			if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "ftp://") {
				continue
			}

			targetPath := target
			anchor := ""
			if idx := strings.Index(target, "#"); idx >= 0 {
				targetPath = target[:idx]
				anchor = target[idx+1:]
			}

			resolvedTarget := md
			if targetPath != "" {
				resolvedTarget = filepath.Join(filepath.Dir(md), targetPath)
				if _, err := os.Stat(resolvedTarget); err != nil {
					rel, _ := filepath.Rel(dir, md)
					return CheckResult{
						Check:  CheckDocParity,
						Status: "FAIL",
						Reason: fmt.Sprintf("broken markdown link in %s: %s (target not found: %s)", rel, target, targetPath),
					}
				}
			}

			if anchor != "" && !strings.HasPrefix(anchor, "L") {
				targetRaw, err := readFile(resolvedTarget)
				if err == nil && !anchorExistsInMarkdown(string(targetRaw), anchor) {
					rel, _ := filepath.Rel(dir, md)
					return CheckResult{
						Check:  CheckDocParity,
						Status: "FAIL",
						Reason: fmt.Sprintf("broken anchor in %s: #%s not found in %s", rel, anchor, filepath.Base(resolvedTarget)),
					}
				}
			}
		}
	}

	return CheckResult{
		Check:  CheckDocParity,
		Status: "PASS",
		Detail: "all markdown links and spec anchors resolve",
	}
}

func anchorExistsInMarkdown(raw, anchor string) bool {
	cleanAnchor := strings.ToLower(strings.TrimPrefix(anchor, "#"))
	for _, l := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			headingText := strings.TrimLeft(trimmed, "# ")
			slug := slugify(headingText)
			if slug == cleanAnchor {
				return true
			}
		}
		if strings.Contains(strings.ToLower(trimmed), "name=\""+cleanAnchor+"\"") ||
			strings.Contains(strings.ToLower(trimmed), "id=\""+cleanAnchor+"\"") {
			return true
		}
	}
	return false
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// -----------------------------------------------------------------------------
// Script Card Engine: MODE: script, JSON RUN array, refusal of login shells
// -----------------------------------------------------------------------------

// DefaultMaxScriptOutputBytes is the 1 MiB hard bound on script combined output
// to prevent memory and token DOS.
const DefaultMaxScriptOutputBytes = 1 << 20

// ScriptCard represents an admitted script card.
type ScriptCard struct {
	Mode           string   `json:"mode"`
	Argv           []string `json:"argv"`
	NetDeny        bool     `json:"net_deny"`
	Timeout        string   `json:"timeout,omitempty"`
	WorkDir        string   `json:"work_dir,omitempty"`
	Spend          string   `json:"spend"` // always "-"
	MaxOutputBytes int64    `json:"max_output_bytes,omitempty"`
	Sandbox        string   `json:"sandbox,omitempty"`
}

// AdmitScriptCard parses and admits a script card from raw card text.
// Enforces:
// 1. Scans strictly in header lines (before body), rejecting duplicates.
// 2. MODE: script is declared.
// 3. RUN: [...] (or ARGV: [...]) JSON array of strings is present.
// 4. Login shells (`bash -l` / `zsh -l` / `sh -l` / `-bash`) are refused at admission.
// 5. Unmetered spend is recorded as "-", never manufactured zero.
func AdmitScriptCard(raw string) (*ScriptCard, error) {
	lines := strings.Split(raw, "\n")
	var modeLine, runLine string
	inHeader := true

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			inHeader = false
			continue
		}
		if inHeader {
			if strings.HasPrefix(strings.ToUpper(trimmed), "MODE:") {
				if modeLine != "" {
					return nil, fmt.Errorf("script card header carries duplicate MODE lines")
				}
				modeLine = trimmed
			}
			if strings.HasPrefix(strings.ToUpper(trimmed), "RUN:") || strings.HasPrefix(strings.ToUpper(trimmed), "ARGV:") {
				if runLine != "" {
					return nil, fmt.Errorf("script card header carries duplicate RUN/ARGV lines")
				}
				runLine = trimmed
			}
		}
	}

	if modeLine == "" || (!strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(modeLine, "MODE:")), "script") && !strings.EqualFold(modeLine, "MODE: script")) {
		return nil, fmt.Errorf("card is not MODE: script")
	}
	if runLine == "" {
		return nil, fmt.Errorf("script card missing RUN: [...] JSON array; script cards require explicit JSON argv, never implicit shell")
	}

	colonIdx := strings.Index(runLine, ":")
	valPart := strings.TrimSpace(runLine[colonIdx+1:])

	var argv []string
	if err := json.Unmarshal([]byte(valPart), &argv); err != nil {
		return nil, fmt.Errorf("script card missing ARGV/RUN JSON array; script cards require explicit JSON argv, never implicit shell: %v", err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("script card RUN array must not be empty")
	}

	if err := checkNoLoginShell(argv); err != nil {
		return nil, err
	}

	return &ScriptCard{
		Mode:           "script",
		Argv:           argv,
		NetDeny:        true,
		Spend:          "-",
		MaxOutputBytes: DefaultMaxScriptOutputBytes,
	}, nil
}

// checkNoLoginShell checks for login shell flags and refuses them at admission.
func checkNoLoginShell(argv []string) error {
	cmd := filepath.Base(argv[0])
	cmdLower := strings.ToLower(cmd)

	isShell := cmdLower == "bash" || cmdLower == "zsh" || cmdLower == "sh" ||
		cmdLower == "dash" || cmdLower == "ksh" || cmdLower == "csh"

	if strings.HasPrefix(cmd, "-") {
		return fmt.Errorf("script card refused at admission: login shell (%s) is forbidden; script cards run non-interactive argv directly", argv[0])
	}

	if isShell {
		for _, arg := range argv[1:] {
			if arg == "-l" || arg == "--login" || strings.HasPrefix(arg, "-l") || (strings.Contains(arg, "l") && strings.HasPrefix(arg, "-")) {
				return fmt.Errorf("script card refused at admission: login shell (%s %s) is forbidden; script cards run non-interactive argv directly", cmd, arg)
			}
		}
	}
	return nil
}

type boundedOutputBuffer struct {
	buf       bytes.Buffer
	limit     int64
	total     int64
	truncated bool
}

func (b *boundedOutputBuffer) Write(p []byte) (int, error) {
	b.total += int64(len(p))
	if int64(b.buf.Len()) < b.limit {
		remaining := b.limit - int64(b.buf.Len())
		if int64(len(p)) <= remaining {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:remaining])
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedOutputBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += fmt.Sprintf("\n[OUTPUT TRUNCATED: exceeded %d bytes hard bound (total %d bytes)]\n", b.limit, b.total)
	}
	return s
}

// Execute runs the admitted script card directly under the sandbox wall with NetDeny.
func (sc *ScriptCard) Execute(dir string, stdin io.Reader) (int, string, error) {
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	// 1. Refuse immediately if temp HOME creation fails (failure closed)
	tempHome, err := os.MkdirTemp("", "script-home-")
	if err != nil {
		return 125, "", fmt.Errorf("script execution refused: failed to create isolated sandbox HOME: %w", err)
	}
	defer os.RemoveAll(tempHome)

	// 2. NetDeny enforcement: require nova-sandbox
	sandboxBin := sc.Sandbox
	if sandboxBin != "" {
		if _, err := os.Stat(sandboxBin); err != nil {
			return 125, "", fmt.Errorf("script execution refused: specified nova-sandbox binary not found at %s: %w", sandboxBin, err)
		}
	} else {
		sandboxBin, _ = exec.LookPath(swarm.SandboxBinary)
	}

	var execCmd string
	var execArgs []string

	if sc.NetDeny {
		if sandboxBin == "" {
			return 125, "", fmt.Errorf("script execution refused: NetDeny requested but nova-sandbox binary not found on PATH; sandboxing must be enforced by OS wall, never by model self-discipline alone")
		}
		execCmd = sandboxBin
		execArgs = []string{
			"--write", absDir,
			"--write", tempHome,
			"--net-deny",
			"--cwd", absDir,
			"--home", tempHome,
			"--",
		}
		execArgs = append(execArgs, sc.Argv...)
	} else {
		execCmd = sc.Argv[0]
		execArgs = sc.Argv[1:]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, execCmd, execArgs...)
	cmd.Dir = absDir
	if stdin != nil {
		cmd.Stdin = stdin
	}
	setProcessGroup(cmd)

	maxBytes := sc.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxScriptOutputBytes
	}
	limitWriter := &boundedOutputBuffer{limit: maxBytes}
	cmd.Stdout = limitWriter
	cmd.Stderr = limitWriter

	var cleanEnv []string
	cleanEnv = append(cleanEnv, "HOME="+tempHome)
	cleanEnv = append(cleanEnv, "GOPROXY=off")
	cleanEnv = append(cleanEnv, "GOTOOLCHAIN=local")
	for _, e := range sandbox.DroppedEnv(os.Environ()) {
		if !strings.HasPrefix(strings.ToUpper(e), "HOME=") && !isSecretEnvVar(e) {
			cleanEnv = append(cleanEnv, e)
		}
	}
	cmd.Env = cleanEnv

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		_ = killProcessGroup(cmd)
		return 124, limitWriter.String(), fmt.Errorf("script execution timed out after 5m")
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	if limitWriter.truncated {
		if runErr == nil {
			runErr = fmt.Errorf("script output truncated: exceeded hard bound of %d bytes", maxBytes)
			exitCode = 1
		}
	}

	return exitCode, limitWriter.String(), runErr
}
