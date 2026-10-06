package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStrangerRunFriendFakeHarnessIsRecorded holds the cold-run record
// docs/stranger/friend-fake-harness.md. It is red while one of the four
// section headings is missing, the transcript has no command line, or a
// stumble has no proposed card line.
func TestStrangerRunFriendFakeHarnessIsRecorded(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/stranger/friend-fake-harness.md")
	require.NoError(t, err, "docs/stranger/friend-fake-harness.md: %v", err)

	sections := h2Sections(string(raw))
	for _, name := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		_, ok := sections[name]
		require.Truef(t, ok, "missing section heading %s", name)
	}

	if !commandLine.MatchString(sections["Transcript"]) {
		t.Fatal("transcript has no command line")
	}

	for i, block := range h3Blocks(sections["Stumbles"]) {
		if !cardLine.MatchString(block) {
			t.Errorf("stumble %d has no line `card: <id>`", i+1)
		}
	}
}

// commandLine is a transcript line a stranger can see was typed: a dollar
// prompt and a command.
var commandLine = regexp.MustCompile(`(?m)^\$ \S`)

// cardLine is the proposed card id the record owes each stumble.
var cardLine = regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9.-]*[ \t]*$`)

// h2Sections splits a markdown file on level-2 headings whose text is a
// single word. The heading line itself is not part of the body.
func h2Sections(text string) map[string]string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	sections := map[string]string{}
	var name string
	var b strings.Builder
	flush := func() {
		if name == "" {
			return
		}
		sections[name] = b.String()
		b.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok && !strings.HasPrefix(rest, "#") && !strings.Contains(strings.TrimSpace(rest), " ") {
			flush()
			name = strings.TrimSpace(rest)
			continue
		}
		if name != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	flush()
	return sections
}

// h3Blocks splits a section on level-3 headings. The preamble before the
// first heading is not a stumble. Each returned block includes its heading.
func h3Blocks(section string) []string {
	var blocks []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		blocks = append(blocks, b.String())
		b.Reset()
	}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "### ") {
			flush()
		}
		if strings.HasPrefix(line, "### ") || b.Len() > 0 {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	flush()
	return blocks
}
