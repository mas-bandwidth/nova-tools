package testverbhelp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// flagValue is the value after --name in args, "" when absent.
func flagValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--"+name {
			return args[i+1]
		}
	}
	return ""
}

// The check is only worth what it catches: one fake tool per way to break the
// rule, each named in what Problems returns, and a good one that passes.
func TestProblemsCatchesEveryWayToBreakTheRule(t *testing.T) {
	t.Parallel()
	good := func(args []string, stdout, stderr io.Writer) int {
		fmt.Fprintln(stdout, "usage: nova-demo send [flags]")
		return 0
	}
	for _, c := range []struct {
		name string
		run  Run
		want string
	}{
		{"exit 2", func(args []string, stdout, stderr io.Writer) int {
			fmt.Fprintln(stderr, "nova-demo send: flag: help requested; run: nova-demo help")
			return 2
		}, "exited 2"},
		{"silent", func(args []string, stdout, stderr io.Writer) int { return 0 }, "printed nothing on stdout"},
		{"stderr", func(args []string, stdout, stderr io.Writer) int {
			good(args, stdout, stderr)
			fmt.Fprintln(stderr, "note")
			return 0
		}, "wrote to stderr"},
		{"writes", func(args []string, stdout, stderr io.Writer) int {
			_ = os.WriteFile(filepath.Join(flagValue(args, "dir"), "state"), nil, 0o600)
			return good(args, stdout, stderr)
		}, "help writes nothing"},
		{"dials", func(args []string, stdout, stderr io.Writer) int {
			// A verb that dials before -h: the check hands it RefusedAddr, and a
			// real client's refusal is an exit and a line on stderr. Faked here,
			// so the unit check opens no socket.
			if flagValue(args, "addr") != RefusedAddr {
				return good(args, stdout, stderr)
			}
			fmt.Fprintf(stderr, "nova-demo send: dial %s: connection refused\n", RefusedAddr)
			return 1
		}, "exited 1"},
	} {
		problems := Problems(c.run, Case{Verb: "send", Flags: []string{"--dir", "{dir}", "--addr", "{addr}"}}, "-h", t.TempDir())
		assert.Contains(t, strings.Join(problems, "\n"), c.want, "%s: problems %q, want one saying %q", c.name, problems, c.want)
	}
	p := Problems(good, Case{Verb: "send", Flags: []string{"--dir", "{dir}"}}, "--help", t.TempDir())
	assert.Empty(t, p, "a tool that answers help: %q", p)
}
