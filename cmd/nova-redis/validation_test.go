package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestMultiFlagValidationSpill holds that spill checks every flag before any
// dial and reports all missing or invalid flags in one single refusal.
func TestMultiFlagValidationSpill(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	d := deps{
		now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		getenv: func(string) string { return "" },
	}

	// Case 1: Bad address and zero TTL (ledger line 145: "a bad addr hides a zero TTL").
	{
		var out, errb bytes.Buffer
		code := run([]string{"spill", "--addr", "127.0.0.1", "--owner", "rowan", "--name", "note", "--ttl", "0s", "--value", "hi"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("spill with bad addr and zero ttl: exit %d, want 2", code)
		}
		stderr := errb.String()
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("spill: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("spill: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--ttl is required and must be above zero; an unbounded key is a bug") {
			t.Errorf("spill: stderr missing zero ttl refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "; run: nova-redis help") {
			t.Errorf("spill: stderr missing remedy; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("spill opened %d connections to Redis; want 0", mr.TotalConnectionCount())
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
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("spill: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--owner is required; refusing to guess") {
			t.Errorf("spill: stderr missing owner required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--addr \"bad:port:extra\" is not <host:port>; refusing to guess") {
			t.Errorf("spill: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("spill opened %d connections; want 0", mr.TotalConnectionCount())
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
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("spill: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--owner is required and may not be empty or hold ':' or whitespace") {
			t.Errorf("spill: stderr missing bad owner refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--ttl \"not-a-duration\" is not a duration (try 10m)") {
			t.Errorf("spill: stderr missing bad duration refusal; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("spill opened %d connections; want 0", mr.TotalConnectionCount())
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
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("spill: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--user \"bad user\" holds whitespace; give the ACL user's name") {
			t.Errorf("spill: stderr missing bad user refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--ttl is required and must be above zero; an unbounded key is a bug") {
			t.Errorf("spill: stderr missing zero ttl refusal; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("spill opened %d connections; want 0", mr.TotalConnectionCount())
		}
	}
}

// TestMultiFlagValidationRecall holds that recall checks every flag before any
// dial and reports all missing or invalid flags in one single refusal.
func TestMultiFlagValidationRecall(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	d := deps{
		now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		getenv: func(string) string { return "" },
	}

	// Case 1: Missing flags and invalid address.
	{
		var out, errb bytes.Buffer
		code := run([]string{"recall", "--addr", "127.0.0.1"}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("recall with missing flags and bad addr: exit %d, want 2", code)
		}
		stderr := errb.String()
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("recall: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--name is required; refusing to guess") {
			t.Errorf("recall: stderr missing name required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--owner is required; refusing to guess") {
			t.Errorf("recall: stderr missing owner required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("recall: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("recall opened %d connections; want 0", mr.TotalConnectionCount())
		}
	}

	// Case 2: Bad address, empty owner, and empty name.
	{
		var out, errb bytes.Buffer
		code := run([]string{"recall", "--addr", "127.0.0.1", "--owner", "", "--name", ""}, &out, &errb, d)
		if code != 2 {
			t.Fatalf("recall with bad addr, empty owner and empty name: exit %d, want 2", code)
		}
		stderr := errb.String()
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("recall: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("recall: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--owner is required and may not be empty or hold ':' or whitespace") {
			t.Errorf("recall: stderr missing empty owner refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--name is required and may not be empty or hold whitespace") {
			t.Errorf("recall: stderr missing empty name refusal; got:\n%s", stderr)
		}
		if mr.TotalConnectionCount() != 0 {
			t.Errorf("recall opened %d connections; want 0", mr.TotalConnectionCount())
		}
	}
}

// TestMultiFlagValidationFn holds that fn subverbs check all flags before dialing
// and report all problems in one refusal.
func TestMultiFlagValidationFn(t *testing.T) {
	t.Parallel()

	// fn load with bad address, bad user, and bad password-env.
	{
		h := newFnHarness(t)
		code, out, stderr := h.run("fn", "load", "--addr", "127.0.0.1", "--user", "bad user", "--password-env", "1-bad")
		if code != 2 || out != "" {
			t.Fatalf("fn load with multiple bad flags: exit %d out %q, want 2", code, out)
		}
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("fn load: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("fn load: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--password-env \"1-bad\" is not a variable name") {
			t.Errorf("fn load: stderr missing bad password-env refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--user \"bad user\" holds whitespace; give the ACL user's name") {
			t.Errorf("fn load: stderr missing bad user refusal; got:\n%s", stderr)
		}
		if h.dials != 0 {
			t.Errorf("fn load dialed %d times; want 0", h.dials)
		}
	}

	// fn check with bad address and bad password-env.
	{
		h := newFnHarness(t)
		code, out, stderr := h.run("fn", "check", "--addr", "127.0.0.1", "--password-env", "1-bad")
		if code != 2 || out != "" {
			t.Fatalf("fn check with bad addr and bad password-env: exit %d out %q, want 2", code, out)
		}
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("fn check: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--addr \"127.0.0.1\" is not <host:port>; refusing to guess") {
			t.Errorf("fn check: stderr missing bad addr refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--password-env \"1-bad\" is not a variable name") {
			t.Errorf("fn check: stderr missing bad password-env refusal; got:\n%s", stderr)
		}
		if h.dials != 0 {
			t.Errorf("fn check dialed %d times; want 0", h.dials)
		}
	}

	// fn check with missing addr and bad password-env.
	{
		h := newFnHarness(t)
		code, out, stderr := h.run("fn", "check", "--password-env", "1-bad")
		if code != 2 || out != "" {
			t.Fatalf("fn check with missing addr and bad password-env: exit %d out %q, want 2", code, out)
		}
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("fn check: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--addr is required; refusing to guess") {
			t.Errorf("fn check: stderr missing addr required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--password-env \"1-bad\" is not a variable name") {
			t.Errorf("fn check: stderr missing bad password-env refusal; got:\n%s", stderr)
		}
		if h.dials != 0 {
			t.Errorf("fn check dialed %d times; want 0", h.dials)
		}
	}
}

// TestMultiFlagValidationServe holds that serve validates all flags before
// attempting to launch redis-server and reports all problems in one refusal.
func TestMultiFlagValidationServe(t *testing.T) {
	t.Parallel()

	h := newServeHarness(t, "secret-pw")

	// Case 1: Invalid bind, invalid port, and relative dir.
	{
		var out, errb bytes.Buffer
		code := run([]string{"serve", "--bind", "8.8.8.8", "--port", "0", "--dir", "relative/path"}, &out, &errb, h.d)
		if code != 2 {
			t.Fatalf("serve with bad bind, port, and dir: exit %d, want 2", code)
		}
		if len(h.launches) != 0 {
			t.Errorf("serve launched %d times; want 0", len(h.launches))
		}
		stderr := errb.String()
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("serve: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--bind \"8.8.8.8\" is neither loopback nor tailnet") {
			t.Errorf("serve: stderr missing public bind refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--port \"0\" needs a port from 1 to 65535") {
			t.Errorf("serve: stderr missing port range refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--dir \"relative/path\" is not absolute; name the store directory in full") {
			t.Errorf("serve: stderr missing relative dir refusal; got:\n%s", stderr)
		}
	}

	// Case 2: Missing required flags.
	{
		var out, errb bytes.Buffer
		code := run([]string{"serve"}, &out, &errb, h.d)
		if code != 2 {
			t.Fatalf("serve with no flags: exit %d, want 2", code)
		}
		if len(h.launches) != 0 {
			t.Errorf("serve launched %d times; want 0", len(h.launches))
		}
		stderr := errb.String()
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("serve: expected 1 refusal line, got %d:\n%s", strings.Count(stderr, "\n"), stderr)
		}
		if !strings.Contains(stderr, "--bind is required; refusing to guess") {
			t.Errorf("serve: stderr missing bind required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--dir is required; refusing to guess") {
			t.Errorf("serve: stderr missing dir required refusal; got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--port is required; refusing to guess") {
			t.Errorf("serve: stderr missing port required refusal; got:\n%s", stderr)
		}
	}
}
