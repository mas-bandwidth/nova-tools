package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3399: Every sub-verb's --help and -h prints its usage at exit 0.
func TestEverySubVerbHelpPrintsUsageAtExit0(t *testing.T) {
	verbs := []string{"tune", "route", "help", "log", "classify", "review", "outcome"}
	for _, verb := range verbs {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(verb+"/"+flag, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				code := run([]string{verb, flag}, &stdout, &stderr)
				if code != 0 {
					t.Fatalf("nova-decide %s %s exit = %d, want 0; stderr: %q", verb, flag, code, stderr.String())
				}
				out := stdout.String()
				if !strings.Contains(out, "nova-decide "+verb) {
					t.Fatalf("nova-decide %s %s stdout missing usage heading; got:\n%s", verb, flag, out)
				}
				if stderr.Len() > 0 {
					t.Fatalf("nova-decide %s %s unexpected stderr: %q", verb, flag, stderr.String())
				}
			})
		}
	}
}

// #3399: The bare decide verb reads key: value lines, not JSON; a JSON state
// refuses as reason=bad-state-format rather than a missing field.
func TestJSONStateRefusesAsBadStateFormat(t *testing.T) {
	q := writeQuestions(t, map[string]any{
		"state_fields": []map[string]any{
			{"name": "security_shaped_package", "type": "bool"},
		},
		"questions": choiceQuestions(),
	})

	t.Run("state_file_json", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(statePath, []byte(`{"security_shaped_package": false}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"--questions", q, "--state", statePath}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("exit = %d, want 2; stderr: %q", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "reason=bad-state-format") {
			t.Fatalf("stderr missing reason=bad-state-format: %q", stderr.String())
		}
	})

	t.Run("state_file_malformed_json_brace", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(statePath, []byte("{\n  \"security_shaped_package\": false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"--questions", q, "--state", statePath}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("exit = %d, want 2; stderr: %q", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "reason=bad-state-format") {
			t.Fatalf("stderr missing reason=bad-state-format: %q", stderr.String())
		}
	})

	t.Run("state_inline_json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--questions", q, "--state", `{"security_shaped_package": false}`}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("exit = %d, want 2; stderr: %q", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "reason=bad-state-format") {
			t.Fatalf("stderr missing reason=bad-state-format: %q", stderr.String())
		}
	})

	t.Run("state_stdin_json", func(t *testing.T) {
		was := stdin
		stdin = strings.NewReader(`{"security_shaped_package": false}`)
		t.Cleanup(func() { stdin = was })

		var stdout, stderr bytes.Buffer
		code := run([]string{"--questions", q, "--state", "-"}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("exit = %d, want 2; stderr: %q", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "reason=bad-state-format") {
			t.Fatalf("stderr missing reason=bad-state-format: %q", stderr.String())
		}
	})
}

// #3399: route and review share one Redis flag spelling (--store, --user,
// --password-env), while keeping aliases (--store-user, --store-password-env).
func TestRedisFlagsUnification(t *testing.T) {
	for _, flag := range []string{"--user", "--store-user", "--password-env", "--store-password-env"} {
		t.Run("route/"+flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			// Pass the flag; expect it to parse without "flag provided but not defined"
			_ = run([]string{"route", flag, "test-val", "--no-jev"}, &stdout, &stderr)
			if strings.Contains(stderr.String(), "flag provided but not defined") {
				t.Fatalf("route does not recognize %s: %s", flag, stderr.String())
			}
		})
		t.Run("review/"+flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			_ = run([]string{"review", flag, "test-val"}, &stdout, &stderr)
			if strings.Contains(stderr.String(), "flag provided but not defined") {
				t.Fatalf("review does not recognize %s: %s", flag, stderr.String())
			}
		})
	}
}

