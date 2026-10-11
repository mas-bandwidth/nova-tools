package main

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and none of
// them asks an engine or writes the file it names (the CLI style's rule (b)): the
// transport here fails every request.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	w := world{do: func(*http.Request) (*http.Response, error) { return nil, errors.New("help asked an engine") },
		box: fakeBox(), lookup: fakeLookup, getenv: func(string) string { return "" }, adapters: Adapters()}
	run := testkit.Main(localTool(w).Run)
	testverbhelp.Check(t, run.NoStdin(), []testverbhelp.Case{
		{Verb: "status", Flags: []string{"--engine", "ollama"}},
		{Verb: "serve", Flags: []string{"--engine", "ollama", "--model", "m:7b", "--num-ctx", "4096"}},
		{Verb: "worker", Flags: []string{"--engine", "ollama", "--out", "{dir}/w.json"}},
	})
	testverbhelp.HelpVerb(t, run.NoStdin(), "nova-local", "status", "serve", "worker", "version")
}
