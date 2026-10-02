package main

// The status grammar of this binary, pinned word and exit code together
// (docs/STANDARD.md §2): after the verb's token the first word of a status line
// is OK, REFUSED or FAILED, and the word and the exit code answer as one -- OK
// exits 0, FAILED (the verb ran and said no: the scan found something) exits 1,
// REFUSED exits 2. A result's token is the verb's line token (SELFTALK, SHAPES,
// EXAMPLE); a refusal's token is the tool name and the verb. Two completed runs
// carry no status word and so have no row here: `version` answers with the one
// version line (class rule version), and `help` prints the banner, which is
// never a refusal (docs/STANDARD.md §3).
//
// The two streams are read apart: a refusal leads stderr, and every other
// status line of this tool leads stdout -- its FAILED summary included, which
// is the exception docs/CLI-STYLE.md:39 names. A word read off both streams at
// once would read the scan's FAILED finding lines on stderr and miss a summary
// line on stdout that lost the word.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	clean := write(t, d, "clean.md", "The tree by the house has one lit window.\n")
	dirty := write(t, d, "drifted.md", "I cannot check my own work.\n")
	pages := filepath.Join(d, "pages")

	for _, tc := range []struct {
		name  string
		args  []string
		token string // the verb's token on the line its status word leads
		word  string // the first word after the token: OK, REFUSED or FAILED
		exit  int
	}{
		{"scan of a clean file", []string{clean}, "SELFTALK", "OK", 0},
		{"scan with a finding", []string{dirty}, "SELFTALK", "FAILED", 1},
		{"scan naming no files", nil, "nova-self-talk", "REFUSED", 2},
		{"shapes", []string{"shapes"}, "SHAPES", "OK", 0},
		{"shapes with an argument", []string{"shapes", "x"}, "nova-self-talk shapes", "REFUSED", 2},
		{"example", []string{"example", pages}, "EXAMPLE", "OK", 0},
		{"example with no directory", []string{"example"}, "nova-self-talk example", "REFUSED", 2},
		{"version with an argument", []string{"version", "--json"}, "nova-self-talk version", "REFUSED", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			got := run(tc.args, &stdout, &stderr)
			assert.Equal(t, tc.exit, got, "exit = %d, want %d\nstdout: %s\nstderr: %s",
				got, tc.exit, stdout.String(), stderr.String())
			stream := stdout.String()
			if tc.word == "REFUSED" {
				stream = stderr.String()
			}
			assert.Equal(t, tc.word, statusWord(stream, tc.token),
				"the first word after the verb's token\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
		})
	}
}

// statusWord is the first word after the verb's token on the first line of an
// output where a status word leads (docs/STANDARD.md §2); continuation lines --
// MORE, NOTE and this tool's item lines -- are walked past. "" when the output
// holds no status line of that token.
func statusWord(output, token string) string {
	for _, line := range strings.Split(output, "\n") {
		rest, ok := strings.CutPrefix(line, token+" ")
		if !ok {
			continue
		}
		word, _, _ := strings.Cut(rest, " ")
		word, _, _ = strings.Cut(word, ":")
		switch word {
		case "OK", "REFUSED", "FAILED":
			return word
		}
	}
	return ""
}
