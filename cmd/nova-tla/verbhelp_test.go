package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// Every verb answers -h and --help with its own help at exit 0 and runs nothing:
// the env fails every program lookup, TLC run and bench reach.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	e := testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran for help"); return 0 })
	e.lookPath = func(string) (string, error) { return "", errors.New("help looked up a program") }
	run := testkit.Main(func(args []string, in io.Reader, out, errb io.Writer) int {
		return newTool(e).RunContext(context.Background(), args, in, out, errb)
	})
	testverbhelp.Check(t, run.NoStdin(), []testverbhelp.Case{
		{Verb: "run", Flags: []string{"--dir", "{dir}/out"}},
		{Verb: "groups", Flags: []string{"--root", "{dir}"}},
		{Verb: "merge", Flags: []string{"--out", "{dir}/RUNS.tsv"}},
		{Verb: "inputs", Flags: []string{"--case", "MCA.cfg"}},
		{Verb: "table", Flags: []string{"--dir", "{dir}/t"}},
		{Verb: "member", Flags: []string{"--dir", "{dir}/m"}},
		{Verb: "replay", Flags: []string{"--source", "{dir}/table.lua", "--dir", "{dir}/r"}},
		{Verb: "witnesses", Flags: []string{"--timeout", "1m"}},
	})
	testverbhelp.HelpVerb(t, run.NoStdin(), "nova-tla", append(verbNames(), "version")...)
}
