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

// TestHelpSaysHowAStoreIsMade pins the help's first-store block (STANDARD §2, ONBOARDING
// point 6: the banner answers how a first run goes, in commands, not nouns). The help
// says a store is a recovery.pub, a sops rule and a branch with an upstream, and that
// seal writes a seat's first value; so the same page must name the command that makes
// the key pair recovery.pub holds, show the .sops.yaml rule's body, show the git lines
// that give the branch its upstream, and show seal taking its value through --stdin,
// the way a program calls it.
func TestHelpSaysHowAStoreIsMade(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 0, code, "`nova-secrets help` exits %d: %s", code, stderr.String())
	help := stdout.String()
	for _, tc := range []struct{ name, want string }{
		{"the command that makes the key pair recovery.pub holds", "age-keygen -o"},
		{"the sops rule body", "creation_rules:"},
		{"the rule names the file it seals", "path_regex:"},
		{"the branch gets its upstream", "push -u origin main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, help, tc.want, "`nova-secrets help` says what a store holds but not how one is made; it must hold %q", tc.want)
		})
	}
	t.Run("seal with --stdin in its example", func(t *testing.T) {
		t.Parallel()
		_, block, ok := strings.Cut(help, "\nexample:\n")
		require.True(t, ok, "`nova-secrets help` ends in no example: block")
		var seals []string
		for _, line := range strings.Split(block, "\n") {
			if strings.Contains(line, "nova-secrets seal") {
				seals = append(seals, strings.TrimSpace(line))
			}
		}
		require.NotEmpty(t, seals, "the example block holds no seal line")
		withStdin := ""
		for _, seal := range seals {
			if strings.Contains(seal, "--stdin") {
				withStdin = seal
			}
		}
		assert.NotEmpty(t, withStdin, "every seal example reads the value from the terminal; a program takes --stdin; the seal examples are:\n  %s", strings.Join(seals, "\n  "))
	})
}
