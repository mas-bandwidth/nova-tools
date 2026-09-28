package main

import (
	"errors"
	"io"
	"os/exec"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them opens the store, the key or a helper program
// (the CLI style's rule (b), #4505). main() exits the process, so this runs the
// built binary, as the rest of this package's tests do.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	// The first exec of a fresh binary is the platform's assessment, not help:
	// pay it once here, outside the budget.
	runNovaSecrets(bin, "version")
	run := func(args []string, stdout, stderr io.Writer) int {
		cmd := exec.Command(bin, args...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		var exitErr *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		} else if err != nil {
			return -1
		}
		return 0
	}
	store := []string{"--store", "{dir}/store", "--as", "seat"}
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "exec", Flags: store},
		{Verb: "names", Flags: store},
		{Verb: "check", Flags: store},
		{Verb: "gate", Flags: []string{"--store", "{dir}/store"}},
		{Verb: "keygen", Flags: []string{"--key", "{dir}/key.txt"}},
		{Verb: "place", Flags: []string{"--store", "{dir}/store"}},
		{Verb: "placed", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "seal", Flags: store},
		{Verb: "seat add", Flags: store},
		{Verb: "seat inject", Flags: store},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, run, "nova-secrets", "names", "seat add", "version")
}
