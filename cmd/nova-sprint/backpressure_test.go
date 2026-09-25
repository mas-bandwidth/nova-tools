package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// TestBackpressureCheckReceipt is #3276 at the verb: one receipt line with
// round_trips=1, exit 0 when only the sprint's own key exists, exit 1 naming
// the legacy key, exit 2 on usage.
func TestBackpressureCheckReceipt(t *testing.T) {
	m := miniredis.RunT(t)
	t.Setenv("NOVA_SPRINT_REDIS_USER", "") // a throwaway server: no ACL user
	const sprint = "control-00003276"
	m.HSet("s:"+sprint+":backpressure", "state", "OFF")
	for i := 0; i < 100; i++ {
		m.Set(fmt.Sprintf("task:unrelated-%03d", i), "x")
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"backpressure", "check", "--sprint", sprint, "--redis", m.Addr()}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", code, out.String(), errOut.String())
	}
	if got, want := out.String(), "BACKPRESSURE CHECK OK sprint="+sprint+" own=1 beat=0 legacy=0 round_trips=1\n"; got != want {
		t.Fatalf("receipt %q, want %q", got, want)
	}

	m.HSet("backpressure", "state", "ON")
	out.Reset()
	errOut.Reset()
	if code := run([]string{"backpressure", "check", "--sprint", sprint, "--redis", m.Addr()}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	if !strings.HasPrefix(out.String(), "BACKPRESSURE CHECK REFUSED sprint="+sprint+" own=1 beat=0 legacy=backpressure round_trips=1 remedy=") {
		t.Fatalf("receipt %q, want REFUSED naming the legacy key with the remedy", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"backpressure", "check"}, &out, &errOut); code != 2 || out.Len() != 0 {
		t.Fatalf("no --sprint: exit %d stdout %q, want 2 and nothing on stdout", code, out.String())
	}
	if code := run([]string{"backpressure"}, &out, &errOut); code != 2 {
		t.Fatalf("no subverb: exit %d, want 2", code)
	}
}
