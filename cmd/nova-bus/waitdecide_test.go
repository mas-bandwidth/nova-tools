package main

import (
	"strings"
	"testing"
)

// TestWaitTakesDecideLikeInbox is #1141. Glenn's ADOPT EVERYTHING note named `--decide`
// on four surfaces and three landed; `wait` is the fourth and still missing at this head.
// This is not a new feature: `inboxOpts` already carries `decide`, `floor`, `keyEnv` and
// `baseURL` (the comment above them says so in as many words), and `inboxListing` -- which
// `wait` polls through on every tick -- already judges when they are set. The ONLY gap was
// the flag surface on `wait` and the two guards that go with it.
//
// No provider key is read and no provider is called: assertions 1 and 4 never reach a
// judgement (1 is refused for its bus, 4 never sets --decide), and 2 and 3 refuse before
// the first poll.
func TestWaitTakesDecideLikeInbox(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	base := []string{"wait", "--bus", checkout, "--as", "Ada",
		"--receipt-max-words", "40", "--timeout", "2s", "--interval", "1s",
		"--remote", "origin", "--branch", "main"}

	// 1. `wait` defines --decide. The bus is deliberately not one, so the run refuses
	// before any note reaches a provider; before the fix package flag refuses the flag
	// itself, which is the one refusal this flag exists to not be.
	missing := t.TempDir()
	invalid := []string{"wait", "--bus", missing, "--as", "Ada",
		"--receipt-max-words", "40", "--timeout", "2s", "--interval", "1s",
		"--remote", "origin", "--branch", "main", "--decide", "--allow-private"}
	r := invoke(t, "", invalid...)
	if strings.Contains(r.stderr, "flag provided but not defined: -decide") {
		t.Errorf("assertion 1: wait does not define --decide, so the documented invocation exits 2: %s", strings.TrimSpace(r.stderr))
	}

	// 2. The confidence floor is refused by name, and names the verb the caller typed.
	r = invoke(t, "", append(append([]string{}, base...), "--decide", "--floor", "1.5", "--allow-private")...)
	if r.code != 2 || !strings.Contains(r.stderr, "nova-bus wait: --floor is a confidence and stands between 0 and 1") {
		t.Errorf("assertion 2: --floor 1.5 was not refused by name (exit %d): %s", r.code, strings.TrimSpace(r.stderr))
	}

	// 3. A bus with no .public marker refuses --decide by name, and --allow-private is
	// the one explicit way to mean it anyway.
	r = invoke(t, "", append(append([]string{}, base...), "--decide")...)
	if r.code != 2 || !strings.Contains(r.stderr, "WAIT REFUSED:") || !strings.Contains(r.stderr, "pass --allow-private to mean it anyway") {
		t.Errorf("assertion 3: a private bus was not refused by name (exit %d): %s", r.code, strings.TrimSpace(r.stderr))
	}

	// 4. Without --decide, what everybody reads is byte-for-byte today's first line
	// (testdata/today/wait.txt pins the whole return).
	r = invoke(t, "", base...)
	first := strings.SplitN(r.stdout, "\n", 2)[0]
	if first != "WAIT as=Ada timeout=2s interval=1s cursor=-" {
		t.Errorf("assertion 4: wait without --decide changed its first line: %q", first)
	}
}
