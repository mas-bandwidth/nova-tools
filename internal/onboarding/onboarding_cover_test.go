package onboarding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the one function of onboarding.go the unit tier's
// per-function coverage table held at 0.0%: ExampleLines, the reader of a
// usage banner's `example:` block. It is what turns a banner's promise — lines
// a first run can type — into the commands a test executes, so a drift here
// would read the wrong lines or forgive a banner without any. Each test names
// what it pins.

// ExampleLines' main path: under the `example:` heading it returns the lines
// that begin with the tool's own name, whitespace collapsed so a banner may
// align its flags, and stops at the first line that is not one of this tool's
// commands.
func TestOnboardingCoverExampleLinesReturnsTheToolLinesUnderTheHeadingCollapsed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc  string
		usage string
		tool  string
		want  []string
	}{
		{
			desc: "aligned flags collapse to one space between fields",
			usage: "nova-alpha: reads the fleet.\n" +
				"\n" +
				"example:\n" +
				"  nova-alpha list --all\n" +
				"     nova-alpha   read   17 --wide\n" +
				"  nova-alpha help\n",
			tool: "nova-alpha",
			want: []string{"nova-alpha list --all", "nova-alpha read 17 --wide", "nova-alpha help"},
		},
		{
			desc: "the block ends at the first line that is not a command of this tool",
			usage: "nova-alpha: reads the fleet.\n" +
				"\n" +
				"example:\n" +
				"nova-alpha list\n" +
				"\n" +
				"nova-alpha help\n" +
				"prose that happens to name nova-alpha again\n",
			tool: "nova-alpha",
			want: []string{"nova-alpha list"},
		},
		{
			desc: "a line indented under the heading is still the tool's command",
			usage: "nova-alpha: reads the fleet.\n" +
				"\n" +
				"example:\n" +
				"\tnova-alpha list\n",
			tool: "nova-alpha",
			want: []string{"nova-alpha list"},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()

			lines, err := ExampleLines(c.usage, c.tool)
			require.NoError(t, err, "ExampleLines refused a banner whose `example:` block opens with the tool's commands")
			assert.Equal(t, c.want, lines, "ExampleLines returned the wrong commands")
		})
	}
}

// ExampleLines' refusals: a banner without the `example:` block is the failure
// the reader is told about rather than an empty answer, and so is a block that
// opens with something else's command.
func TestOnboardingCoverExampleLinesRefusesABannerWithoutARunnableBlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc    string
		usage   string
		tool    string
		wantErr string
	}{
		{
			desc: "a bare banner carries no `example:` block at all",
			usage: "nova-alpha: reads the fleet.\n" +
				"\n" +
				"usage:\n" +
				"  nova-alpha list\n",
			tool:    "nova-alpha",
			wantErr: "the usage banner has no `example:` block",
		},
		{
			desc: "the block opens with another tool's command",
			usage: "nova-alpha: reads the fleet.\n" +
				"\n" +
				"example:\n" +
				"nova-beta list\n",
			tool:    "nova-alpha",
			wantErr: "the `example:` block holds no nova-alpha command",
		},
		{
			desc: "the block holds only the bare tool name, which is no command",
			usage: "nova-alpha: reads the fleet.\n" +
				"\n" +
				"example:\n" +
				"nova-alpha\n",
			tool:    "nova-alpha",
			wantErr: "the `example:` block holds no nova-alpha command",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()

			lines, err := ExampleLines(c.usage, c.tool)
			assert.ErrorContains(t, err, c.wantErr, "ExampleLines accepted a banner a first run cannot type into")
			assert.Nil(t, lines, "a refusal returns no lines, not an empty slice a caller could print")
		})
	}
}
