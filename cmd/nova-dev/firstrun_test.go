package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryCommandMeetsTheOnboardingStandard(t *testing.T) {
	t.Parallel()
	onboarding.Check(t, "nova-dev", run)
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	md, err := readTestMD()
	require.NoError(t, err)
	section, ok := onboarding.Section(md, "nova-dev")
	require.True(t, ok, "no ## nova-dev in TESTS")
	_, first, ok := strings.Cut(section, "```\n")
	require.True(t, ok)
	lines := strings.Split(strings.TrimSuffix(first, "\n```\n"), "\n")
	var got []onboarding.Result
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || !strings.HasPrefix(strings.TrimSpace(line), "nova-dev ") {
			continue
		}
		var out, errb bytes.Buffer
		args := strings.Fields(strings.TrimSpace(line))[1:]
		code := run(args, &out, &errb)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(lines, got, nil) {
		assert.Fail(t, "transcript mismatch", p)
	}
}

func readTestMD() (string, error) {
	// stub, the testdata not, but to avoid t.Setenv, use fixed
	return "", nil
}
