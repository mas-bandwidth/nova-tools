package check

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditExecDeadlineDirectBackground(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	cmd := exec.CommandContext(context.Background(), "ls", "-la")
	_ = cmd.Run()
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("execCalls = %d, want 1", calls)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.Line != 7 {
		t.Errorf("finding line = %d, want 7", f.Line)
	}
	if f.Func != "Run" {
		t.Errorf("finding func = %q, want Run", f.Func)
	}
	if !containsSubstr(f.Detail, "context.Background()") {
		t.Errorf("finding detail = %q, want mention of context.Background()", f.Detail)
	}
}

func TestAuditExecDeadlineDirectTODO(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	_ = exec.CommandContext(context.TODO(), "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "context.TODO()") {
		t.Errorf("detail = %q, want mention of context.TODO()", findings[0].Detail)
	}
}

func TestAuditExecDeadlineDirectNil(t *testing.T) {
	t.Parallel()

	src := `package sample
import "os/exec"
func Run() {
	_ = exec.CommandContext(nil, "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "nil context") {
		t.Errorf("detail = %q, want mention of nil context", findings[0].Detail)
	}
}

func TestAuditExecDeadlineDirectWithCancel(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	ctx, _ := context.WithCancel(context.Background())
	_ = exec.CommandContext(ctx, "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "context.WithCancel") {
		t.Errorf("detail = %q, want mention of context.WithCancel", findings[0].Detail)
	}
}

func TestAuditExecDeadlineWithCancelInheritsDeadline(t *testing.T) {
	t.Parallel()

	// WithCancel wrapping a context with deadline MUST NOT be flagged.
	srcTimed := `package sample
import (
	"context"
	"os/exec"
	"time"
)
func Run() {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second) // wall-ok: ast test fixture duration
	ctxCancel, _ := context.WithCancel(ctx)
	_ = exec.CommandContext(ctxCancel, "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(srcTimed), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0 (ctxCancel inherits deadline from ctx): %v", len(findings), findings)
	}

	// WithCancel wrapping context.Background() MUST be flagged.
	srcUntimed := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	ctx, _ := context.WithCancel(context.Background())
	_ = exec.CommandContext(ctx, "echo", "hi")
}
`
	findings, calls, err = AuditExecDeadlineSource("sample.go", []byte(srcUntimed), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "context.WithCancel") {
		t.Errorf("detail = %q, want mention of context.WithCancel", findings[0].Detail)
	}
}

func TestAuditExecDeadlineDirectWithCancelInheritsDeadline(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
	"time"
)
func Run() {
	ctx, _ := context.WithTimeout(context.Background(), 30*time.Second)
	_ = exec.CommandContext(context.WithCancel(ctx), "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0: %v", len(findings), findings)
	}
}

func TestAuditExecDeadlineDirectWithTimeout(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
	"time"
)
func Run() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0 (valid timeout)", len(findings))
	}
}

func TestAuditExecDeadlineDirectWithDeadline(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
	"time"
)
func Run() {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	defer cancel()
	_ = exec.CommandContext(ctx, "echo", "hi")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 0 {
		t.Fatalf("calls = %d, findings = %d, want 1, 0", calls, len(findings))
	}
}

func TestAuditExecDeadlineVariableFromBackground(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	ctx := context.Background()
	_ = exec.CommandContext(ctx, "ps", "aux")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "context.Background") {
		t.Errorf("detail = %q, want mention of context.Background", findings[0].Detail)
	}
}

func TestAuditExecDeadlineVariableReassignedWithTimeout(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
	"time"
)
func Run() {
	ctx := context.Background()
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "ls")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 0 {
		t.Fatalf("calls = %d, findings = %d, want 1, 0", calls, len(findings))
	}
}

func TestAuditExecDeadlineWrongVariablePassed(t *testing.T) {
	t.Parallel()

	// Subtle bug: childCtx has timeout, but ctx (background) is passed to CommandContext
	src := `package sample
import (
	"context"
	"os/exec"
	"time"
)
func Run() {
	ctx := context.Background()
	childCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = childCtx
	_ = exec.CommandContext(ctx, "ls")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "ctx") {
		t.Errorf("detail = %q, want mention of variable ctx", findings[0].Detail)
	}
}

func TestAuditExecDeadlineAliasedVariable(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	base := context.Background()
	ctx := base
	_ = exec.CommandContext(ctx, "ls")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "base") && !containsSubstr(findings[0].Detail, "context.Background") {
		t.Errorf("detail = %q, want mention of base or context.Background", findings[0].Detail)
	}
}

func TestAuditExecDeadlineUninitializedVar(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	var ctx context.Context
	_ = exec.CommandContext(ctx, "ls")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
	if !containsSubstr(findings[0].Detail, "uninitialized") {
		t.Errorf("detail = %q, want mention of uninitialized", findings[0].Detail)
	}
}

func TestAuditExecDeadlineInClosure(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Run() {
	ctx := context.Background()
	fn := func() {
		_ = exec.CommandContext(ctx, "ls")
	}
	fn()
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 1 {
		t.Fatalf("calls = %d, findings = %d, want 1, 1", calls, len(findings))
	}
}

func TestAuditExecDeadlineParameterNonStrictAndStrict(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Helper(ctx context.Context) {
	_ = exec.CommandContext(ctx, "git", "status")
}
`
	// Non-strict: caller is trusted
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{Strict: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 0 {
		t.Fatalf("non-strict: calls = %d, findings = %d, want 1, 0", calls, len(findings))
	}

	// Strict: requires deadline in helper
	findingsStrict, callsStrict, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{Strict: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callsStrict != 1 || len(findingsStrict) != 1 {
		t.Fatalf("strict: calls = %d, findings = %d, want 1, 1", callsStrict, len(findingsStrict))
	}
}

func TestAuditExecDeadlineParameterWithDeadlineCheck(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
func Helper(ctx context.Context) {
	if _, ok := ctx.Deadline(); !ok {
		return
	}
	_ = exec.CommandContext(ctx, "git", "status")
}
`
	// Even in strict mode, Deadline() check satisfies the requirement
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{Strict: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 || len(findings) != 0 {
		t.Fatalf("calls = %d, findings = %d, want 1, 0", calls, len(findings))
	}
}

func TestAuditExecDeadlineIgnoreNonExec(t *testing.T) {
	t.Parallel()

	src := `package sample
import (
	"context"
	"os/exec"
)
type runner struct{}
func (r runner) CommandContext(ctx context.Context, name string) {}

func Run() {
	var r runner
	r.CommandContext(context.Background(), "something")
	_ = exec.Command("ls")
}
`
	findings, calls, err := AuditExecDeadlineSource("sample.go", []byte(src), ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 || len(findings) != 0 {
		t.Fatalf("calls = %d, findings = %d, want 0, 0", calls, len(findings))
	}
}

func TestCheckExecDeadlineDirAndFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	file1 := filepath.Join(dir, "clean.go")
	cleanSrc := `package testpkg
import (
	"context"
	"os/exec"
	"time"
)
func Clean() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "echo", "ok")
}
`
	if err := os.WriteFile(file1, []byte(cleanSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	file2 := filepath.Join(dir, "bad.go")
	badSrc := `package testpkg
import (
	"context"
	"os/exec"
)
func Bad() {
	_ = exec.CommandContext(context.Background(), "echo", "bad")
}
`
	if err := os.WriteFile(file2, []byte(badSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(dir, "example_test.go")
	testSrc := `package testpkg
import (
	"context"
	"os/exec"
	"testing"
)
func TestSomething(t *testing.T) {
	_ = exec.CommandContext(context.Background(), "echo", "test")
}
`
	if err := os.WriteFile(testFile, []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Scan dir without test files
	res, err := CheckExecDeadlineDir(dir, ExecDeadlineOptions{IncludeTests: false})
	if err != nil {
		t.Fatalf("CheckExecDeadlineDir error: %v", err)
	}
	if res.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2", res.FilesScanned)
	}
	if res.ExecCalls != 2 {
		t.Errorf("ExecCalls = %d, want 2", res.ExecCalls)
	}
	if len(res.Findings) != 1 {
		t.Errorf("Findings count = %d, want 1", len(res.Findings))
	}

	// 2. Scan dir including test files
	resTests, err := CheckExecDeadlineDir(dir, ExecDeadlineOptions{IncludeTests: true})
	if err != nil {
		t.Fatalf("CheckExecDeadlineDir error: %v", err)
	}
	if resTests.FilesScanned != 3 {
		t.Errorf("FilesScanned = %d, want 3", resTests.FilesScanned)
	}
	if len(resTests.Findings) != 2 {
		t.Errorf("Findings count = %d, want 2", len(resTests.Findings))
	}

	// 3. Scan specific files
	resFiles, err := CheckExecDeadlineFiles(dir, []string{"clean.go"}, ExecDeadlineOptions{})
	if err != nil {
		t.Fatalf("CheckExecDeadlineFiles error: %v", err)
	}
	if resFiles.FilesScanned != 1 || len(resFiles.Findings) != 0 {
		t.Errorf("resFiles: scanned=%d findings=%d, want 1, 0", resFiles.FilesScanned, len(resFiles.Findings))
	}

	// 4. Scan with exclude
	resExcl, err := CheckExecDeadlineDir(dir, ExecDeadlineOptions{Exclude: []string{"bad.go"}})
	if err != nil {
		t.Fatalf("CheckExecDeadlineDir with exclude error: %v", err)
	}
	if resExcl.Excluded != 1 {
		t.Errorf("Excluded = %d, want 1", resExcl.Excluded)
	}
	if len(resExcl.Findings) != 0 {
		t.Errorf("Findings count = %d, want 0 after excluding bad.go", len(resExcl.Findings))
	}
}

func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || filepath.ToSlash(s) != "" && (stringContains(s, substr)))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
