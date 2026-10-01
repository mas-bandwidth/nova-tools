package textbody

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripQuotedAndCode(t *testing.T) {
	t.Parallel()

	input := `Hello
> quoted line 1
> quoted line 2
Real text here
` + "```" + `
code block
HOLD in code
` + "```" + `
After code`

	got := StripQuotedAndCode(input)
	assert.NotContains(t, got, "quoted line", "quoted lines were not stripped: %s", got)
	assert.NotContains(t, got, "HOLD in code", "code block was not stripped: %s", got)
	assert.Contains(t, got, "Real text here", "real text was lost: %s", got)
	assert.Contains(t, got, "After code", "real text was lost: %s", got)
}

func TestLineFilterEdges(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"line endings", "a\r\nb\r\n", "a\nb\n"},
		{"leading quote whitespace", " a \n\t > quoted\n\u2003> quoted too\nend", " a \nend"},
		{"quote and backtick inline", "a > b and `code`", "a > b and `code`"},
		{"language fence", "before\n  ```go\nhidden\n```\nafter", "before\nafter"},
		{"unclosed fence", "before\n```\nhidden", "before"},
		{"quote containing fence", "> ```\nvisible", "visible"},
		{"empty block", "a\n```\n```\nb", "a\nb"},
		{"blank lines stay", "a\n\n>gone\n\nb\n", "a\n\n\nb\n"},
		{"bytes stay", "a\xff\x00\tb\rc", "a\xff\x00\tb\rc"},
		{"tilde is ordinary", "~~~\nbody\n~~~", "~~~\nbody\n~~~"},
		{"one backtick prefix", "`x` flag\nvisible", "`x` flag\nvisible"},
		{"two backtick prefix", "``x`` flag\nvisible", "``x`` flag\nvisible"},
		{"four backtick fence", "````\nhidden\n````\nvisible", "visible"},
		{"closing fence trailing text", "```\nfoo\n``` bar\nkept\n```\nlost", "kept"},
		{"backtick in fence info", "``` a`b\nhidden\n```\nvisible", "visible"},
		{"indented fences", "    ```\nhidden\n\t```\nvisible", "visible"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := StripQuotedAndCode(c.in)
			require.Equal(t, c.want, got, "got %q want %q", got, c.want)
		})
	}
}

// Construct independent visible/hidden segments and compare exactly with the
// visible text used to build them. No filter predicate is shared with production.
func TestGeneratedBodiesKeepExactlyTheirVisibleLines(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(4467, 27))
	for n := 0; n < 3000; n++ {
		var input, want []string
		for j, limit := 0, r.IntN(24); j < limit; j++ {
			line := fmt.Sprintf("visible %d %d 日本語 %c", n, j, rune(0x100+r.IntN(500)))
			switch r.IntN(4) {
			case 0:
				input = append(input, "  > omitted "+line)
			case 1:
				input = append(input, "```text", "hidden "+line, "> hidden too", "```")
			default:
				input = append(input, line)
				want = append(want, line)
			}
		}
		if r.IntN(2) == 0 {
			input = append(input, "```", "unclosed hidden remainder")
		}
		body := strings.Join(input, "\n")
		if n%2 == 0 {
			body = strings.ReplaceAll(body, "\n", "\r\n")
		}
		got := StripQuotedAndCode(body)
		require.Equal(t, strings.Join(want, "\n"), got, "case %d: got %q want %q", n, got, strings.Join(want, "\n"))
	}
}
