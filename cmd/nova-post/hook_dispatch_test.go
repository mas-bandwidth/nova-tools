package main

// hook_dispatch_test.go is the control of Johnny's hold on #3034: `nova-post
// hook` reaches runHook through run's switch (it used to fall to bad-verb),
// and the webhook secret is read from the environment the unit passes.

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestHookVerbIsDispatched(t *testing.T) {
	t.Parallel()

	code, _, stderr := cli("hook")
	if code != 2 {
		t.Fatalf("`nova-post hook` exit=%d, want 2 (missing --addr): %s", code, stderr)
	}
	if strings.Contains(stderr, "bad-verb") {
		t.Fatalf("`nova-post hook` took the unknown-verb path: %s", stderr)
	}
	if !strings.Contains(stderr, "missing-flag") || !strings.Contains(stderr, "--addr") {
		t.Fatalf("`nova-post hook` did not reach runHook's flag check: %s", stderr)
	}
}

func TestHookRefusesAWildcardAddr(t *testing.T) {
	t.Parallel()

	code, _, stderr := cli("hook", "--addr", ":8080", "--redis", "127.0.0.1:6379")
	if code != 2 || !strings.Contains(stderr, "wildcard-addr") {
		t.Fatalf("exit=%d stderr=%s, want 2 wildcard-addr", code, stderr)
	}
}

func TestHookReadsTheSecretFromTheUnitEnv(t *testing.T) {
	mr := miniredis.RunT(t)
	t.Setenv(defaultHookSecretEnv, "")
	code, _, stderr := cli("hook", "--addr", "127.0.0.1:0", "--redis", mr.Addr())
	if code != 2 || !strings.Contains(stderr, "missing-secret") || !strings.Contains(stderr, "NOVA_GITHUB_WEBHOOK_SECRET") {
		t.Fatalf("exit=%d stderr=%s, want 2 missing-secret naming NOVA_GITHUB_WEBHOOK_SECRET", code, stderr)
	}
	t.Setenv("OTHER_HOOK_SECRET", "")
	code, _, stderr = cli("hook", "--addr", "127.0.0.1:0", "--redis", mr.Addr(), "--secret-env", "OTHER_HOOK_SECRET")
	if code != 2 || !strings.Contains(stderr, "OTHER_HOOK_SECRET") {
		t.Fatalf("exit=%d stderr=%s, want the refusal to name --secret-env's variable", code, stderr)
	}
}

func TestHelpNamesTheHookVerb(t *testing.T) {
	t.Parallel()

	_, out, _ := cli("help")
	if !strings.Contains(out, "nova-post hook") || !strings.Contains(out, "NOVA_GITHUB_WEBHOOK_SECRET") {
		t.Fatalf("help does not name the hook verb and its secret env:\n%s", out)
	}
}
