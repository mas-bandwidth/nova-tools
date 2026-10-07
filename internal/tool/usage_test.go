package tool

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageContinuationKeepsItsIndent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, usage, want string
	}{
		{"one line", "record show --file <file>", "  nova-demo record show --file <file>\n"},
		{"continuation", "record show --file <file>\n    [--format <format>]\nrecord show --all", "  nova-demo record show --file <file>\n      [--format <format>]\n  nova-demo record show --all\n"},
		{"one space", "record show --file <file>\n [--format <format>]", "  nova-demo record show --file <file>\n   [--format <format>]\n"},
		{"tab and trailing space", "record show --file <file>  \r\n\t[--format <format>]\t\n \t", "  nova-demo record show --file <file>\n  \t[--format <format>]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := Tool{Name: "nova-demo", What: "Reads records.", ExitTable: "0 done, 1 refused, 2 usage", Verbs: []Verb{{Name: "record show", Usage: tc.usage, Effect: Inspection}}}
			banner := tool.Banner()
			_, usage, ok := strings.Cut(banner, "usage:\n")
			require.True(t, ok)
			usage, _, ok = strings.Cut(usage, "\n\n")
			require.True(t, ok)
			assert.Equal(t, tc.want+"  nova-demo version\n  nova-demo help [<verb>]", usage)
			var excerpt []string
			for _, line := range strings.Split(strings.TrimSuffix(tc.want, "\n"), "\n") {
				excerpt = append(excerpt, strings.TrimSpace(line))
			}
			assert.Equal(t, excerpt, verbflag.Excerpt(banner, tool.Name, "record show"))
			for _, line := range strings.Split(usage, "\n") {
				assert.LessOrEqual(t, utf8.RuneCountInString(line), 100)
			}
			var out, err bytes.Buffer
			require.Zero(t, tool.Run([]string{"record", "-h"}, strings.NewReader(""), &out, &err))
			assert.Empty(t, err.String())
			assert.Equal(t, "usage: nova-demo record <show> [flags]\n"+tc.want+"`nova-demo record <verb> -h` lists a verb's flags.\nexit codes: 0 done, 1 refused, 2 usage\n", out.String())
			for _, line := range strings.Split(out.String(), "\n") {
				assert.LessOrEqual(t, utf8.RuneCountInString(line), 100)
			}
		})
	}
}
