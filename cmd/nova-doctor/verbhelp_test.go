package main

import (
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// The one verb answers -h and --help with its own help at exit 0 and runs no check.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	run := testkit.Main(func(args []string, in io.Reader, out, errb io.Writer) int {
		return doctor.Main(doctor.NewRegistry(), nil, "", args, in, out, errb)
	})
	testverbhelp.Check(t, run.NoStdin(), []testverbhelp.Case{{Verb: "run", Flags: []string{"--local"}}})
	testverbhelp.HelpVerb(t, run.NoStdin(), "nova-doctor", "run", "version")
}
