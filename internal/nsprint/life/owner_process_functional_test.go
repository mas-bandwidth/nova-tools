//go:build functional && (linux || darwin)

package life_test

import (
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"os/exec"
	"testing"
)

func TestOwnerProcessKernelIdentityAndExit(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/cat")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = in.Close()
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	first := life.ProbeProcess(cmd.Process.Pid)
	second := life.ProbeProcess(cmd.Process.Pid)
	if first.Err != nil || first.Absent || first.Start == "" || first.Start == "-" || second.Start != first.Start {
		t.Fatalf("live identity: first=%+v second=%+v", first, second)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true
	if got := life.ProbeProcess(cmd.Process.Pid); got.Err != nil || !got.Absent {
		t.Fatalf("exited process: %+v", got)
	}
}
