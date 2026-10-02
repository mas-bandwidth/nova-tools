package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecDeadlineRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStderr string
	}{
		{
			name:       "no dir or file",
			args:       []string{"exec-deadline"},
			wantExit:   2,
			wantStderr: "give at least one of --dir or --file; refusing to guess",
		},
		{
			name:       "negative fail-max",
			args:       []string{"exec-deadline", "--dir", ".", "--fail-max", "-1"},
			wantExit:   2,
			wantStderr: "--fail-max must be a line ceiling of zero or more",
		},
		{
			name:       "unexpected positional arg",
			args:       []string{"exec-deadline", "--dir", ".", "extra"},
			wantExit:   2,
			wantStderr: "unexpected argument",
		},
		{
			name:       "non-existent dir",
			args:       []string{"exec-deadline", "--dir", "./non-existent-directory-xyz-123"},
			wantExit:   2,
			wantStderr: "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantExit {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, tt.wantExit, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestExecDeadlineHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"exec-deadline", "-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "exec-deadline") {
		t.Errorf("stdout = %q, want it to mention exec-deadline", out)
	}
	if !strings.Contains(out, "--dir") {
		t.Errorf("stdout = %q, want it to mention --dir", out)
	}
}

func TestExecDeadlineCleanDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := `package main
import (
	"context"
	"os/exec"
	"time"
)
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "echo", "hello")
	_ = cmd.Run()
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"exec-deadline", "--dir", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "EXEC-DEADLINE OK files=1 exec-calls=1 excluded=0") {
		t.Errorf("stdout = %q, want EXEC-DEADLINE OK files=1 exec-calls=1 excluded=0", stdout.String())
	}
}

func TestExecDeadlineViolationsAndCapping(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src1 := `package main
import (
	"context"
	"os/exec"
)
func f1() {
	_ = exec.CommandContext(context.Background(), "ls")
}
`
	src2 := `package main
import (
	"context"
	"os/exec"
)
func f2() {
	_ = exec.CommandContext(context.TODO(), "pwd")
}
`
	if err := os.WriteFile(filepath.Join(dir, "f1.go"), []byte(src1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f2.go"), []byte(src2), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Uncapped run
	var stdout, stderr bytes.Buffer
	code := run([]string{"exec-deadline", "--dir", dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr.String())
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "EXEC-DEADLINE FAIL f1.go:7:6:") {
		t.Errorf("stderr = %q, want EXEC-DEADLINE FAIL for f1.go:7:6", errOut)
	}
	if !strings.Contains(errOut, "EXEC-DEADLINE FAIL f2.go:7:6:") {
		t.Errorf("stderr = %q, want EXEC-DEADLINE FAIL for f2.go:7:6", errOut)
	}
	if !strings.Contains(errOut, "EXEC-DEADLINE FAIL files=2 exec-calls=2 violations=2 shown=2 excluded=0") {
		t.Errorf("stderr = %q, want summary line with violations=2 shown=2", errOut)
	}

	// 2. Capped run with --fail-max 1
	var stdout2, stderr2 bytes.Buffer
	code2 := run([]string{"exec-deadline", "--dir", dir, "--fail-max", "1"}, &stdout2, &stderr2)
	if code2 != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %s", code2, stderr2.String())
	}
	errOut2 := stderr2.String()
	if !strings.Contains(errOut2, "EXEC-DEADLINE MORE kind=violation shown=1 total=2") {
		t.Errorf("stderr = %q, want EXEC-DEADLINE MORE line", errOut2)
	}
	if !strings.Contains(errOut2, "EXEC-DEADLINE FAIL files=2 exec-calls=2 violations=2 shown=1 excluded=0") {
		t.Errorf("stderr = %q, want summary line with shown=1", errOut2)
	}
}

func TestExecDeadlineFileAndExclude(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	srcBad := `package main
import (
	"context"
	"os/exec"
)
func Bad() {
	_ = exec.CommandContext(context.Background(), "ls")
}
`
	srcClean := `package main
import (
	"context"
	"os/exec"
	"time"
)
func Clean() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "ls")
}
`
	badFile := filepath.Join(dir, "bad.go")
	cleanFile := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(badFile, []byte(srcBad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cleanFile, []byte(srcClean), 0o644); err != nil {
		t.Fatal(err)
	}

	// Check only clean file via --file
	var stdout, stderr bytes.Buffer
	code := run([]string{"exec-deadline", "--dir", dir, "--file", "clean.go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	// Check dir excluding bad.go
	var stdout2, stderr2 bytes.Buffer
	code2 := run([]string{"exec-deadline", "--dir", dir, "--exclude", "bad.go"}, &stdout2, &stderr2)
	if code2 != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code2, stderr2.String())
	}
	if !strings.Contains(stdout2.String(), "excluded=1") {
		t.Errorf("stdout = %q, want excluded=1", stdout2.String())
	}
}

func TestExecDeadlineStrictFlag(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := `package main
import (
	"context"
	"os/exec"
)
func Run(ctx context.Context) {
	_ = exec.CommandContext(ctx, "ls")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default (non-strict): parameter passes
	var stdout1, stderr1 bytes.Buffer
	code1 := run([]string{"exec-deadline", "--dir", dir}, &stdout1, &stderr1)
	if code1 != 0 {
		t.Fatalf("non-strict exit code = %d, want 0; stderr: %s", code1, stderr1.String())
	}

	// Strict: parameter is flagged
	var stdout2, stderr2 bytes.Buffer
	code2 := run([]string{"exec-deadline", "--dir", dir, "--strict"}, &stdout2, &stderr2)
	if code2 != 1 {
		t.Fatalf("strict exit code = %d, want 1; stderr: %s", code2, stderr2.String())
	}
	if !strings.Contains(stderr2.String(), "context parameter \"ctx\" has no verified deadline") {
		t.Errorf("stderr = %q, want mention of parameter ctx without verified deadline", stderr2.String())
	}
}
