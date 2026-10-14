package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The README's table hands a stranger the first command for each tool. For
// nova-friend that command is the `nova-friend install` line in the row's
// <code> block. docs/CLI.md is the reference for that tool, and its own
// `nova-friend install` example carries the flags the verb requires -- the
// opencode harness needs `--model <provider/model>`, so a first example without
// it is a call the tool refuses.
//
// The two documents are meant to say the same thing, so the README's first
// `nova-friend install` example must be one of the examples docs/CLI.md shows.
// A README example the reference does not carry has drifted from the reference
// and sends the reader to a call the reference does not make.
func TestReadmeFriendInstallExampleCarriesItsRequiredModel(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	readme := readFile(t, filepath.Join(root, "README.md"))
	cli := readFile(t, filepath.Join(root, "docs", "CLI.md"))

	readmeExamples := friendInstallExamples(readme)
	require.NotEmpty(t, readmeExamples, "README.md's nova-friend row carries no `nova-friend install` example")

	cliExamples := friendInstallExamples(cli)
	require.NotEmpty(t, cliExamples, "docs/CLI.md carries no `nova-friend install` example")

	first := readmeExamples[0]
	assert.Contains(t, cliExamples, first,
		"README.md's first `nova-friend install` example is not one the docs/CLI.md nova-friend reference shows; make the two identical (docs/CLI.md's example carries `--model <provider/model>`)")
}

// friendInstallExamples returns every `nova-friend install` command line in a
// document, in order, trimmed to one space and cut at the first `<` or backtick
// so an example stops where its prose, HTML wrapper or placeholder does.
func friendInstallExamples(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, "nova-friend install")
		if i < 0 {
			continue
		}
		cmd := line[i:]
		if j := strings.IndexAny(cmd, "<`"); j >= 0 {
			cmd = cmd[:j]
		}
		cmd = strings.Join(strings.Fields(cmd), " ")
		if cmd == "" {
			continue
		}
		out = append(out, cmd)
	}
	return out
}
