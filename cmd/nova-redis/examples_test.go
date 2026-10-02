package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestHelpExamplesRunThroughTheComparator: the help's `example:` block is a
// first run (docs/ONBOARDING.md point 6), and each line runs, in order, against
// a miniredis fake standing at the address the line names, under the fixed
// clock of this package's harness, printing what is written here. The version
// word and the machine are the build's and are compared by shape.
func TestHelpExamplesRunThroughTheComparator(t *testing.T) {
	t.Parallel()

	sitting := []onboarding.Step{
		{Line: "$ nova-redis version", Want: []string{"nova-redis devel darwin/arm64 go1.27.1"}},
		{Line: "$ nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi", Want: []string{"SPILL OK dry-run=true key=ada:note ttl=10m0s expires=2026-09-23T12:10:00Z bytes=2 store=127.0.0.1:6379 written=0"}},
		{Line: "$ nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi", Want: []string{"SPILL OK key=ada:note ttl=10m0s expires=2026-09-23T12:10:00Z bytes=2"}},
		{Line: "$ nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note", Want: []string{"RECALL OK key=ada:note bytes=2 value=hi"}},
	}
	examples, err := onboarding.ExampleLines(usage, "nova-redis")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, s := range sitting {
		want = append(want, strings.TrimPrefix(s.Line, "$ "))
	}
	if strings.Join(examples, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the banner's example block is not the sitting this test runs\nbanner:\n  %s\nwant:\n  %s", strings.Join(examples, "\n  "), strings.Join(want, "\n  "))
	}
	h := newHarness(t)
	norms := []onboarding.Norm{onboarding.Version(), onboarding.GoBuild()}
	for _, s := range sitting {
		args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(s.Line, "$ nova-redis "), "127.0.0.1:6379", h.mr.Addr()))
		code, out, errs := h.runBare(args...)
		// The fake stands at the address the line names; the line prints it.
		out = strings.ReplaceAll(out, h.mr.Addr(), "127.0.0.1:6379")
		if strings.Contains(s.Line, "--dry-run") {
			assert.Zero(t, h.mr.TotalConnectionCount(), "the example %s dialled the store; a dry run needs none", s.Line)
		}
		if code != 0 {
			t.Errorf("the example %s exits %d; stderr: %s", s.Line, code, errs)
		}
		for _, p := range onboarding.Compare(s, onboarding.Result{Code: code, Stdout: out, Stderr: errs}, norms) {
			t.Error(p)
		}
	}
}
