package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestFnHelpDescribesSubverbsAndReadWriteDistinction holds that `fn -h` and
// `fn --help` exit 0, print on stdout with nothing on stderr, list both load
// and check subverbs, and state their read/write distinction.
func TestFnHelpDescribesSubverbsAndReadWriteDistinction(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		var out, errb bytes.Buffer
		code := run([]string{"fn", flag}, &out, &errb, realDeps())
		if code != 0 {
			t.Errorf("fn %s: exit %d, want 0; stderr: %q", flag, code, errb.String())
		}
		if errb.Len() != 0 {
			t.Errorf("fn %s: stderr %q, want empty", flag, errb.String())
		}
		stdout := out.String()
		for _, want := range []string{
			"usage: nova-redis fn",
			"load",
			"check",
			"registers functions onto Redis",
			"evaluates registered functions read-only",
			"write",
			"read",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("fn %s: stdout missing %q; got:\n%s", flag, want, stdout)
			}
		}
	}
}

// TestMultiFlagValidationSpill holds that spill checks every flag before any
// dial and reports all missing or invalid flags in one refusal.
func TestMultiFlagValidationSpill(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	d := deps{
		now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		getenv: func(string) string { return "" },
	}

	// Case 1: Bad address hides zero TTL in single-check validation; both must be reported.
	{
		var out, errb bytes.Buffer
		code := run([]string{"spill", "--addr", "127.0.0.1", "--owner", "rowan", "--name", "note", "--ttl", "0s", "--value", "hi"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("spill with bad addr and zero ttl: exit %d, want 2; stderr: %q", code, errb.String())
		}
		stderr := errb.String()
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("spill: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--ttl is required and must be above zero; an unbounded key is a bug") {
			t.Errorf("spill: stderr missing zero ttl refusal; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("spill opened %d connections; want 0", mr.TotalConnectionCount())
		}
	}

	// Case 2: Missing flag alongside invalid flag: missing --owner and bad --addr.
	{
		var out, errb bytes.Buffer
		code := run([]string{"spill", "--addr", "bad:port:extra", "--name", "note", "--ttl", "10m", "--value", "hi"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("spill with missing owner and bad addr: exit %d, want 2", code)
		}
		stderr := errb.String()
		if !strings.Contains(stderr, "--owner is required; refusing to guess") {
			t.Errorf("spill: stderr missing owner required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--addr \"bad:port:extra\" is not <host:port>; refusing to guess") {
			t.Errorf("spill: stderr missing bad addr refusal; got:\n%s", stderr)
		}
	}

	// Case 3: Bad owner (whitespace or colon) and unparseable TTL.
	{
		var out, errb bytes.Buffer
		code := run([]string{"spill", "--addr", mr.Addr(), "--owner", "bad:owner", "--name", "note", "--ttl", "not-a-duration", "--value", "hi"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("spill with bad owner and bad ttl: exit %d, want 2", code)
		}
		stderr := errb.String()
		if !strings.Contains(stderr, "--owner is required and may not be empty or hold ':' or whitespace") {
			t.Errorf("spill: stderr missing bad owner refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--ttl \"not-a-duration\" is not a duration (try 10m)") {
			t.Errorf("spill: stderr missing bad duration refusal; got:\n%s", stderr)
		}
	}

	// Case 4: Bad user (whitespace) and zero TTL.
	{
		var out, errb bytes.Buffer
		code := run([]string{"spill", "--addr", mr.Addr(), "--owner", "rowan", "--name", "note", "--ttl", "0s", "--value", "hi", "--user", "bad user"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("spill with bad user and zero ttl: exit %d, want 2", code)
		}
		stderr := errb.String()
		if !strings.Contains(stderr, "--user \"bad user\" holds whitespace; give the ACL user's name") {
			t.Errorf("spill: stderr missing bad user refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--ttl is required and must be above zero; an unbounded key is a bug") {
			t.Errorf("spill: stderr missing zero ttl refusal; got:\n%s", stderr)
		}
	}
}

// TestMultiFlagValidationRecall holds that recall checks every flag before any
// dial and reports all missing or invalid flags in one refusal.
func TestMultiFlagValidationRecall(t *testing.T) {
	t.Parallel()

	d := deps{
		now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		getenv: func(string) string { return "" },
	}

	// Bad address, empty owner, and empty name.
	var out, errb bytes.Buffer
	code := run([]string{"recall", "--addr", "127.0.0.1", "--owner", "", "--name", ""}, &out, &errb, d)
	if code != 2 {
		t.Fatalf("recall with bad addr, empty owner and empty name: exit %d, want 2", code)
	}
	stderr := errb.String()
	if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
		t.Errorf("recall: stderr missing bad addr refusal; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--owner is required and may not be empty or hold ':' or whitespace") {
		t.Errorf("recall: stderr missing empty owner refusal; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--name is required and may not be empty or hold whitespace") {
		t.Errorf("recall: stderr missing empty name refusal; got:\n%s", stderr)
	}
}

// TestMultiFlagValidationFn holds that fn subverbs check all flags before dialing.
func TestMultiFlagValidationFn(t *testing.T) {
	t.Parallel()

	d := deps{
		now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		getenv: func(string) string { return "" },
	}

	// fn load with bad address, bad user, and bad password-env.
	{
		var out, errb bytes.Buffer
		code := run([]string{"fn", "load", "--addr", "127.0.0.1", "--user", "bad user", "--password-env", "1-bad"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("fn load with multiple bad flags: exit %d, want 2", code)
		}
		stderr := errb.String()
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("fn load: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--password-env \"1-bad\" is not a variable name") {
			t.Errorf("fn load: stderr missing bad password-env refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--user \"bad user\" holds whitespace; give the ACL user's name") {
			t.Errorf("fn load: stderr missing bad user refusal; got:\n%s", stderr)
		}
	}

	// fn check with missing addr and bad password-env.
	{
		var out, errb bytes.Buffer
		code := run([]string{"fn", "check", "--password-env", "1-bad"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("fn check with missing addr and bad password-env: exit %d, want 2", code)
		}
		stderr := errb.String()
		if !strings.Contains(stderr, "--addr is required; refusing to guess") {
			t.Errorf("fn check: stderr missing addr required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--password-env \"1-bad\" is not a variable name") {
			t.Errorf("fn check: stderr missing bad password-env refusal; got:\n%s", stderr)
		}
	}
}

// TestMultiFlagValidationServe holds that serve validates all flags before
// attempting to launch redis-server.
func TestMultiFlagValidationServe(t *testing.T) {
	t.Parallel()

	h := newServeHarness(t, "secret-pw")

	// Invalid bind, invalid port, and relative dir.
	var out, errb bytes.Buffer
	code := run([]string{"serve", "--bind", "8.8.8.8", "--port", "0", "--dir", "relative/path"}, &out, &errb, h.d)
	if code != 2 {
		t.Fatalf("serve with bad bind, port, and dir: exit %d, want 2", code)
	}
	if len(h.launches) != 0 {
		t.Errorf("serve launched %d times; want 0", len(h.launches))
	}
	stderr := errb.String()
	if !strings.Contains(stderr, "--bind \"8.8.8.8\" is neither loopback nor tailnet") {
		t.Errorf("serve: stderr missing public bind refusal; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--port \"0\" needs a port from 1 to 65535") {
		t.Errorf("serve: stderr missing port range refusal; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--dir \"relative/path\" must be an absolute path") {
		t.Errorf("serve: stderr missing relative dir refusal; got:\n%s", stderr)
	}
}

// TestRecallUnboundedRemedy holds that recall on an unbounded key outputs
// RECALL UNBOUNDED with the remedy to recreate the key with nova-redis spill,
// and never advises deleting store data.
func TestRecallUnboundedRemedy(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	// Write a raw hash without TTL or expires_ms field so recall identifies it as unbounded.
	mr.HSet("rowan:unbounded", fieldValue, "some-value")

	h := &harness{
		mr: mr,
		d: deps{
			now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
			getenv: func(string) string { return "" },
		},
	}

	code, stdout, stderr := h.run("recall", "--owner", "rowan", "--name", "unbounded")
	if code != 1 {
		t.Fatalf("recall unbounded key: exit %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "RECALL UNBOUNDED key=rowan:unbounded ") {
		t.Errorf("stdout missing RECALL UNBOUNDED prefix; got: %q", stdout)
	}
	wantRemedy := `remedy="recreate the key with nova-redis spill from its producer; an unbounded key is a bug and was not written by nova-redis spill"`
	if !strings.Contains(stdout, wantRemedy) {
		t.Errorf("stdout missing expected remedy %q; got: %q", wantRemedy, stdout)
	}
	if strings.Contains(stdout, "delete") || strings.Contains(stderr, "delete") {
		t.Errorf("output must never advise deleting store data; stdout=%q stderr=%q", stdout, stderr)
	}
}
