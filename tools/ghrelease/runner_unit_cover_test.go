package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

func TestGhreleaseRunnerCoverExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("something went wrong"), 127},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := exitCode(tt.err)
			if got != tt.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestGhreleaseRunnerCoverFinish(t *testing.T) {
	t.Parallel()
	missingPath := "/nonexistent/missing/path"

	// Build a Bounded with a cmd pointing to a missing path, never started
	bBounded := func(ctx context.Context, timeout time.Duration) subproc.Bounded {
		cmd := exec.CommandContext(ctx, missingPath)
		return subproc.Bounded{
			Cmd:    cmd,
			Ctx:    ctx,
			Budget: timeout,
			Cancel: func() {},
		}
	}

	// expiredCtx is a context with a fixed past deadline
	fixedPastTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	expiredCtx, expiredCancel := context.WithDeadline(context.Background(), fixedPastTime)
	expiredCancel()

	tests := []struct {
		name         string
		bounded      subproc.Bounded
		err          error
		wantRC       int
		wantMsg      string
		wantMsgEmpty bool
	}{
		{
			name:         "nil error returns 0 with empty message",
			bounded:      bBounded(context.Background(), 0),
			err:          nil,
			wantRC:       0,
			wantMsgEmpty: true,
		},
		{
			name:         "plain error returns 127 with non-empty message",
			bounded:      bBounded(context.Background(), 0),
			err:          errors.New("start failed"),
			wantRC:       127,
			wantMsgEmpty: false,
		},
		{
			name: "expired context returns 124 with did not finish message",
			bounded: subproc.Bounded{
				Cmd:    exec.CommandContext(expiredCtx, missingPath),
				Ctx:    expiredCtx,
				Cancel: func() {},
			},
			err:     errors.New("signal: killed"),
			wantRC:  124,
			wantMsg: "did not finish",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := tt.bounded
			gotRC, gotMsg := finish(b, tt.err)
			if gotRC != tt.wantRC {
				t.Fatalf("finish() rc = %d, want %d", gotRC, tt.wantRC)
			}
			if tt.wantMsgEmpty {
				if gotMsg != "" {
					t.Errorf("finish() msg = %q, want empty", gotMsg)
				}
			} else {
				if gotMsg == "" {
					t.Error("finish() msg is empty, want non-empty")
				}
				if tt.wantMsg != "" && !strings.Contains(gotMsg, tt.wantMsg) {
					t.Errorf("finish() msg = %q, want to contain %q", gotMsg, tt.wantMsg)
				}
			}
		})
	}
}

func TestGhreleaseRunnerCoverCmd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     []string
		dir     string
		wantEnv bool
		wantDir bool
	}{
		{"no env", nil, "", false, false},
		{"with env", []string{"KEY=VALUE"}, "/some/dir", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := command{dir: tt.dir, env: tt.env, name: "test", args: []string{"arg1"}}
			b := osRunner{}.cmd(c)
			if tt.wantDir && b.Cmd.Dir != tt.dir {
				t.Errorf("cmd() Dir = %q, want %q", b.Cmd.Dir, tt.dir)
			}
			if !tt.wantEnv {
				if b.Cmd.Env != nil {
					t.Error("cmd() Env is non-nil when env is empty")
				}
			} else {
				if b.Cmd.Env == nil {
					t.Fatal("cmd() Env = nil, want non-nil")
				}
				lastIdx := len(b.Cmd.Env) - 1
				if b.Cmd.Env[lastIdx] != "KEY=VALUE" {
					t.Errorf("cmd() Env last element = %q, want %q", b.Cmd.Env[lastIdx], "KEY=VALUE")
				}
			}
			cancelCalled := false
			b.Cancel = func() { cancelCalled = true }
			b.Cancel()
			if !cancelCalled {
				t.Error("cmd() Cancel was not called")
			}
		})
	}
}

func TestGhreleaseRunnerCoverOutput(t *testing.T) {
	t.Parallel()
	missingPath := filepath.Join(t.TempDir(), "missing")
	out, rc := osRunner{}.Output(command{name: missingPath, args: nil})
	if rc != 127 {
		t.Errorf("Output() rc = %d, want 127", rc)
	}
	if out == "" || !strings.Contains(out, "no such file") {
		t.Errorf("Output() out = %q, want reason with 'no such file'", out)
	}
}

func TestGhreleaseRunnerCoverStream(t *testing.T) {
	t.Parallel()
	missingPath := filepath.Join(t.TempDir(), "missing")
	var stdout, stderr bytes.Buffer
	rc := osRunner{}.Stream(command{name: missingPath, args: nil}, &stdout, &stderr)
	if rc != 127 {
		t.Errorf("Stream() rc = %d, want 127", rc)
	}
	if stdout.Len() != 0 {
		t.Errorf("Stream() stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() == 0 || !strings.Contains(stderr.String(), "no such file") {
		t.Errorf("Stream() stderr = %q, want reason with 'no such file'", stderr.String())
	}
}
