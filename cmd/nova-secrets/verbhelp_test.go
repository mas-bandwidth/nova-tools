package main

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestHelpSaysHowAStoreIsMade pins the cold-reader page (SPEC-SECRETS: the
// recovery key is one age-keygen -o pair whose public line is recovery.pub;
// .sops.yaml is the creation rule the tests write; invariant 8 is a branch
// with an upstream; ONBOARDING point 6: how a first store is made). A program
// seals on --stdin, not at the terminal.
func TestHelpSaysHowAStoreIsMade(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	code := run([]string{"help"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 0, code, "help exits %d: %s", code, stderr.String())
	help := stdout.String()

	assert.Contains(t, help, "age-keygen -o", "help does not name the command that makes recovery.pub")
	assert.Contains(t, help, "creation_rules:\n  - path_regex: ^worker\\.yaml$\n    age: <seat public key>,<recovery key>", "help does not show the sops rule body")
	assert.Contains(t, help, "nova-secrets seal --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --name API_KEY --stdin", "help does not show seal with --stdin in its example")
	assert.Contains(t, help, "git -C <store> add recovery.pub .sops.yaml && git -C <store> commit", "help does not commit recovery.pub and .sops.yaml before the first push")
}
