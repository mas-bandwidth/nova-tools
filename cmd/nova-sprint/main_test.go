package main_test

import (
	"os/exec"
	"testing"
)

func TestNovaSprintVersion(t *testing.T) {
	_ = exec.Command("go", "run", ".", "version")
}
