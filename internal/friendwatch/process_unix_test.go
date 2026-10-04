//go:build darwin || linux

package friendwatch

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	if handled, code := OwnedHelper(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) == 3 && os.Args[1] == "--friendwatch-test-fixture" {
		os.Exit(groupFixture(os.Args[2]))
	}
	os.Exit(m.Run())
}

// groupFixture owns only fixture children, with stdin EOF as unconditional cleanup.
func groupFixture(mode string) int {
	if mode == "grandchild" {
		signal.Ignore(syscall.SIGTERM)
		fmt.Fprintln(os.Stdout, "grandchild ready")
		ready := os.NewFile(3, "fixture-ready")
		if ready != nil {
			_, _ = ready.Write([]byte{1})
			ready.Close()
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	if mode == "ordinary" {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM)
		fmt.Fprintln(os.Stdout, "ordinary ready")
		<-c
		fmt.Fprintln(os.Stdout, "ordinary term")
		return 0
	}
	child := exec.Command(os.Args[0], "--friendwatch-test-fixture", "grandchild")
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return 1
	}
	defer readyR.Close()
	child.ExtraFiles = []*os.File{readyW}
	if err := child.Start(); err != nil {
		readyW.Close()
		return 1
	}
	readyW.Close()
	var ready [1]byte
	if _, err := readyR.Read(ready[:]); err != nil {
		return 1
	}
	if mode == "complete" {
		return 0
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = child.Wait()
	return 0
}
