//go:build functional

package ci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// measureToolAnswers runs one built tool through the mistakes an AI makes
// (toolanswers_class_test.go): bare, an unknown verb, an unknown flag on a
// verb, each group's -h, and reads every verb's help for its effect and its
// dry run. helps is what everyVerbAnswersHelp read; bareErr the bare command's
// stderr. Each run is in a directory of its own with HOME there.
func measureToolAnswers(t *testing.T, a *toolAnswers, tool, bin, banner, bareErr string, helps map[string]string) {
	t.Helper()
	verbs := usageVerbs(tool, banner)
	a.short(tool, answerBare, tool, bareAnswers(bareErr))

	code, out, errs := runIn(t, bin, noSuchVerb)
	a.short(tool, answerVerb, tool+" "+noSuchVerb, unknownVerbAnswers(code, out+errs, verbs))

	// One verb per tool: every verb of a tool parses through one seam. The
	// first that lists a flag shows whether the answer names them.
	verb, flags := verbs[0], []string(nil)
	for _, v := range verbs {
		if f := helpFlags(helps[v]); len(f) > 0 {
			verb, flags = v, f
			break
		}
	}
	code, out, errs = runIn(t, bin, append(strings.Fields(verb), noSuchFlag)...)
	a.short(tool, answerFlag, tool+" "+verb+" "+noSuchFlag, unknownFlagAnswers(code, out+errs, flags))

	// The verb-help walk visits the groups too: each group's -h is help that
	// names the group's verbs.
	for _, g := range verbGroups(verbs) {
		code, out, errs = runIn(t, bin, g, "-h")
		a.short(tool, answerGroup, tool+" "+g+" -h", groupHelpAnswers(code, out, errs, groupMembers(g, verbs)))
	}
	for _, v := range verbs {
		problem := "its -h is not help (exit 0 on stdout)"
		if h, ok := helps[v]; ok {
			problem = dryRunAnswers(h)
		}
		a.short(tool, answerDry, tool+" "+v, problem)
	}
}

// runIn runs bin with args in a fresh directory, HOME and TMPDIR there, stdin
// empty, under a deadline.
func runIn(t *testing.T, bin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + dir}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		require.Fail(t, fmt.Sprintf("running %s %s: %v", bin, strings.Join(args, " "), err))
	}
	return code, out.String(), errb.String()
}
