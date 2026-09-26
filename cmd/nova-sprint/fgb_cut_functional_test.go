//go:build functional

package main

import (
	"strings"
	"testing"
)

func TestFGBTaskCardVerbWithoutActorIsNamed(t *testing.T) {
	t.Parallel()
	code, out, errOut := runSprint("task", "move", "--id", "p", "--to-stream", "s1", "--redis", "127.0.0.1:1")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint task move: is a task card verb and needs --actor <a>") {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
}
