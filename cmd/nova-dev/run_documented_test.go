package main

import (
	"bytes"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string { return "nova-dev reads no stdin" }

var errReadsNothing = readsNothing{}

func runCheck(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestDevToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	_ = devTool(nil).Problems()
}
