package main

import (
	"bytes"
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

// TestHelpSaysHowAStoreIsMade pins the store paragraph (docs/STANDARD.md §3 point 6,
// docs/SPEC-SECRETS.md): `help` shows how a first store is made, so a cold reader can
// open one from this page alone. It names the command whose key pair yields
// recovery.pub, shows the .sops.yaml rule body as keygen prints it, shows the git
// road to a branch with an upstream, and shows seal with --stdin -- the path a
// program takes -- piping the value instead of waiting on the terminal. run is the
// whole tool in process: no key, store or helper program is opened, no value appears.
func TestHelpSaysHowAStoreIsMade(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 0, code, "`help` exits %d, stderr: %s", code, stderr.String())
	help := stdout.String()

	var makesRecoveryPub, sealsStdin bool
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "age-keygen -o") && strings.Contains(line, "recovery.pub") {
			makesRecoveryPub = true
		}
		if strings.Contains(line, "nova-secrets seal") && strings.Contains(line, "|") && strings.Contains(line, "--stdin") {
			sealsStdin = true
		}
	}
	assert.True(t, makesRecoveryPub,
		"`help` names no command that makes recovery.pub: no line carries both age-keygen -o "+
			"and recovery.pub\nhelp:\n%s", help)
	assert.True(t, sealsStdin,
		"`help` shows no seal example taking the value on a pipe with --stdin, so a program "+
			"reading this page still waits on the terminal for the value\nhelp:\n%s", help)

	// the sops rule file and its body, as keygen prints it and as the tool's own tests write it
	assert.Contains(t, help, "creation_rules:\n    - path_regex: ^<seat>\\.yaml$\n      age: <seat key>,<recovery key>",
		"`help` does not show the .sops.yaml rule body\nhelp:\n%s", help)

	// the git road to a branch with an upstream, as the store prerequisite's own remedy gives it
	assert.Contains(t, help, "git init --bare", "`help` does not show git init for the store\nhelp:\n%s", help)
	assert.Contains(t, help, "git push -u origin", "`help` does not show the push that gives the branch its upstream\nhelp:\n%s", help)
}
