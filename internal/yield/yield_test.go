package yield

import (
	"runtime"
	"testing"
)

// TestToCIStepsThisProcessDownToNice: after ToCI the process is at Nice
// (darwin and Linux), and a second call is a no-op that still succeeds.
// The test binary stays niced for the rest of its run, which is the
// behaviour under test and costs it nothing but priority.
func TestToCIStepsThisProcessDownToNice(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("no setpriority on %s", runtime.GOOS)
	}
	before, err := currentNice()
	if err != nil {
		t.Fatal(err)
	}
	if before > Nice {
		t.Skipf("already at nice %d, above %d; an unprivileged process cannot come back up", before, Nice)
	}
	if err := ToCI(); err != nil {
		t.Fatalf("ToCI: %v", err)
	}
	n, err := currentNice()
	if err != nil {
		t.Fatal(err)
	}
	if n != Nice {
		t.Fatalf("nice after ToCI = %d, want %d", n, Nice)
	}
	if err := ToCI(); err != nil {
		t.Fatalf("second ToCI: %v", err)
	}
}

// TestNiceIsFifteen pins the number the issue names: a change here is a
// change of policy, made on purpose with the class test that reads it.
func TestNiceIsFifteen(t *testing.T) {
	t.Parallel()
	if Nice != 15 {
		t.Fatalf("Nice = %d, want 15 (nova-tools#4293)", Nice)
	}
}
