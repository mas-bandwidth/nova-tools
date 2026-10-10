package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunInstall(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		wantCode  int
		wantOut   string
		wantError string
	}{
		{
			name:     "install with --deny-self and --dry-run",
			args:     []string{"--deny-self", "/path/to/deny", "--dry-run"},
			env:      map[string]string{},
			wantCode: 0,
			wantOut:  "run: nova-friend run --as",
		},
		{
			name:     "install with NOVA_FRIEND_DENY_SELF env",
			args:     []string{"--dry-run"},
			env:      map[string]string{"NOVA_FRIEND_DENY_SELF": "/env/path"},
			wantCode: 0,
			wantOut:  "run: nova-friend run --as",
		},
		{
			name:      "install without --deny-self or env",
			args:      []string{},
			env:       map[string]string{},
			wantCode:  2,
			wantError: "--deny-self",
		},
		{
			name:     "install with --as",
			args:     []string{"--deny-self", "/path", "--as", "rowan", "--dry-run"},
			env:      map[string]string{},
			wantCode: 0,
			wantOut:  "rowan",
		},
		{
			name:     "install with NOVA_FRIEND",
			args:     []string{"--deny-self", "/path", "--dry-run"},
			env:      map[string]string{"NOVA_FRIEND": "rowan"},
			wantCode: 0,
			wantOut:  "rowan",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := map[string]string{}
			for k, v := range tt.env {
				env[k] = v
			}
			getenv := func(s string) string { return env[s] }

			code := runInstall(env{getenv: getenv}, tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("runInstall() code = %d, want %d", code, tt.wantCode)
			}

			out := stdout.String() + stderr.String()
			if tt.wantOut != "" && !strings.Contains(out, tt.wantOut) {
				t.Errorf("runInstall() output = %q, want to contain %q", out, tt.wantOut)
			}

			if tt.wantError != "" && !strings.Contains(out, tt.wantError) {
				t.Errorf("runInstall() error = %q, want to contain %q", out, tt.wantError)
			}
		})
	}
}

func TestRunUninstall(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		wantCode  int
		wantOut   string
		wantError string
	}{
		{
			name:     "uninstall with --as",
			args:     []string{"--as", "rowan"},
			env:      map[string]string{},
			wantCode: 0,
			wantOut:  "rowan",
		},
		{
			name:     "uninstall with NOVA_FRIEND",
			args:     []string{},
			env:      map[string]string{"NOVA_FRIEND": "rowan"},
			wantCode: 0,
			wantOut:  "rowan",
		},
		{
			name:      "uninstall without --as or NOVA_FRIEND",
			args:      []string{},
			env:       map[string]string{},
			wantCode:  2,
			wantError: "--as",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := map[string]string{}
			for k, v := range tt.env {
				env[k] = v
			}
			getenv := func(s string) string { return env[s] }

			code := runUninstall(env{getenv: getenv}, tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("runUninstall() code = %d, want %d", code, tt.wantCode)
			}

			out := stdout.String() + stderr.String()
			if tt.wantOut != "" && !strings.Contains(out, tt.wantOut) {
				t.Errorf("runUninstall() output = %q, want to contain %q", out, tt.wantOut)
			}

			if tt.wantError != "" && !strings.Contains(out, tt.wantError) {
				t.Errorf("runUninstall() error = %q, want to contain %q", out, tt.wantError)
			}
		})
	}
}
