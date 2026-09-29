package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

type verbRetireFake struct {
	retireOut string
	retireErr error
	mu        sync.Mutex
	calls     []string
}

func (f *verbRetireFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.mu.Unlock()
	if len(argv) > 0 && argv[0] == "ansible" {
		return f.retireOut, f.retireErr
	}
	return "", errors.New("unexpected child " + strings.Join(argv, " "))
}

func retireDeps(f *verbRetireFake) releaseDeps {
	return releaseDeps{
		Runner: f,
		Home:   func() (string, error) { return "/home/none", nil },
		Getenv: func(k string) string { return "" },
		Open:   openFleetStore,
	}
}

func TestFleetRetireVerb(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)

	// 1. Success case
	fakeOK := &verbRetireFake{
		retireOut: "alpha | CHANGED => { \"changed\": true }\n",
	}
	var out, errOut bytes.Buffer
	code := runFleetRetireWith(context.Background(), []string{"alpha", "stray.service", "--redis", mr.Addr()}, &out, &errOut, retireDeps(fakeOK))
	if code != 0 {
		t.Fatalf("runFleetRetireWith success: got exit %d stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "FLEET RETIRE bench=alpha unit=stray.service status=OK ms=") {
		t.Errorf("output missing expected line: %s", out.String())
	}

	// 2. Failure case
	fakeFail := &verbRetireFake{
		retireOut: "alpha | FAILED! => { \"msg\": \"unit not found\" }\n",
		retireErr: errors.New("exit status 2"),
	}
	out.Reset()
	errOut.Reset()
	code = runFleetRetireWith(context.Background(), []string{"alpha", "stray.service", "--redis", mr.Addr()}, &out, &errOut, retireDeps(fakeFail))
	if code != 1 {
		t.Fatalf("runFleetRetireWith failure: got exit %d, want 1", code)
	}
	if !strings.Contains(out.String(), "FLEET RETIRE bench=alpha unit=stray.service status=FAILED") {
		t.Errorf("output missing failure line: %s", out.String())
	}

	// 3. Dry run
	out.Reset()
	errOut.Reset()
	code = runFleetRetireWith(context.Background(), []string{"alpha", "stray.service", "--dry-run"}, &out, &errOut, retireDeps(fakeOK))
	if code != 0 {
		t.Fatalf("dry-run exit: got %d, want 0", code)
	}
	if !strings.Contains(out.String(), "check=yes") {
		t.Errorf("dry-run missing check=yes: %s", out.String())
	}
}

func TestFleetRetireUsage(t *testing.T) {
	t.Parallel()

	fake := &verbRetireFake{}

	// Not enough positional args
	var out, errOut bytes.Buffer
	code := runFleetRetireWith(context.Background(), []string{"alpha"}, &out, &errOut, retireDeps(fake))
	if code != 2 {
		t.Fatalf("runFleetRetireWith 1 arg: got exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "wants <bench> <unit>") {
		t.Errorf("stderr missing usage: %s", errOut.String())
	}

	// Extra positional args
	out.Reset()
	errOut.Reset()
	code = runFleetRetireWith(context.Background(), []string{"alpha", "unit1", "extra"}, &out, &errOut, retireDeps(fake))
	if code != 2 {
		t.Fatalf("runFleetRetireWith 3 args: got exit %d, want 2", code)
	}
}
