package preflight

import (
	"errors"
	"strings"
	"testing"
)

// #3899: 7.26 reads one ps snapshot; a go test under the session preflight
// runs in is RED, one under another session or preflight's own caller is not.
func TestCheckLocalBatchTests(t *testing.T) {
	ps := ParseProcs(`    1     0 /sbin/launchd
  100     1 /Applications/Claude.app/Contents/MacOS/Claude
  110   100 /opt/claude-code/claude --resume
  120   110 /bin/zsh -c nova-sprint preflight
  130   120 nova-sprint preflight --redis space:6380
  140   110 /bin/bash -c cd /tmp/land && go test ./...
  150   140 /usr/local/go/bin/go test ./...
  151   150 /tmp/go-build1/b001/nova-sprint.test -test.v
  210   100 /opt/claude-code/claude
  220   210 go test ./internal/ci/...
  300     1 /bin/zsh -l
  310   300 go test -run TestX ./cmd/nova-sprint
  320   310 /tmp/go-build2/b001/nova-sprint.test
  330   320 nova-sprint preflight --redis x
garbage line
`)
	if len(ps) != 14 {
		t.Fatalf("parsed %d procs, want 14", len(ps))
	}
	l := CheckLocalBatchTests(ps, nil, 130)
	if !l.Red || l.N != "7.26" || !strings.Contains(l.Why, "go test pid=150 under session root 110") {
		t.Fatalf("want RED 7.26 naming pid 150 under the claude session 110: %s", l)
	}
	if strings.Contains(l.Why, "pid=220") || strings.Contains(l.Why, "pid=151") || !strings.Contains(l.Why, "remedy:") || !strings.Contains(l.Why, "nova-sprint ci request") {
		t.Fatalf("another session's go test, or a test binary, is a finding, or no remedy: %s", l)
	}
	// No claude ancestor: the root is the topmost process under pid 1, and a
	// go test that is preflight's own caller is not a finding.
	if l := CheckLocalBatchTests(ps, nil, 330); l.Red || !strings.HasPrefix(l.String(), "GREEN 7.26 local batch tests: no go test under session root 300") {
		t.Fatalf("preflight's own caller is a finding: %s", l)
	}
	if l := CheckLocalBatchTests(nil, errors.New("ps: exit 1"), 130); !l.Red || !strings.Contains(l.Why, "could not list processes") {
		t.Fatalf("a failed ps is not RED: %s", l)
	}
}
