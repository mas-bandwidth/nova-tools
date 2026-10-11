package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// runClaudeHook is the opt-in Claude PreToolUse protocol (docs/CLAUDE-ASYNC-BASH-CANDIDATE.md).
// It has no tool envelope: stdout is exactly one hook JSON object or empty.
func runClaudeHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--harness" || args[1] != "claude" {
		fmt.Fprintln(stderr, "nova-friend hook REFUSED: --harness wants claude and hook takes no words; run: nova-friend hook -h")
		return 2
	}
	const maxEvent = 1 << 20
	input, err := io.ReadAll(io.LimitReader(stdin, maxEvent+1))
	if err != nil {
		fmt.Fprintf(stderr, "nova-friend hook FAILED: PreToolUse input could not be read: %v\n", err)
		input = nil
	} else if len(input) > maxEvent {
		fmt.Fprintln(stderr, "nova-friend hook FAILED: PreToolUse input exceeds 1 MiB")
		input = nil
	}
	response := friend.ClaudeAsyncBashHook(input)
	if len(response) == 0 {
		return 0
	}
	if _, err := stdout.Write(append(response, '\n')); err != nil {
		fmt.Fprintf(stderr, "nova-friend hook FAILED: response could not be written: %v; run: nova-friend hook --harness claude\n", err)
		return 2
	}
	return 0
}
