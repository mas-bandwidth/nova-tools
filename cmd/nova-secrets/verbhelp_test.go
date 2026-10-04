package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them opens the store, the key or a helper program (the CLI style's
// rule (b)). run is the whole tool on its arguments and streams, so this drives
// it in process: no binary is built and no process started, and nothing here
// waits on a clock.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	inProcess := func(args []string, stdout, stderr io.Writer) int {
		return run(args, strings.NewReader(""), stdout, stderr)
	}
	store := []string{"--store", "{dir}/store", "--as", "seat"}
	testverbhelp.Check(t, inProcess, []testverbhelp.Case{
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
	testverbhelp.HelpVerb(t, inProcess, "nova-secrets", "names", "seat add", "version")
}
