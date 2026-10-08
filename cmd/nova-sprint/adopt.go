package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The verb `adopt` is the seat's adoption through the fleet play
// (adopt_play.go, cmdAdoptPlay); the hand adoption pipeline of aeb316dea is
// gone, replaced by that play.
func init() {
	// it builds over ssh, switches binaries on disk and pushes to the fleet:
	// it runs where it is typed or scheduled, never on the server
	notServed = append(notServed, "adopt")
}

// adoptRunner runs one command and returns its combined output: exec in
// production, a fake in a test.
type adoptRunner func(ctx context.Context, name string, args ...string) (string, error)

func execAdoptRunner(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, adoptLastLine(s))
	}
	return s, nil
}

func adoptLastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return oneline.Escape(s)
}
