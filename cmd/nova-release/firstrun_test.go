// The first run of nova-release, held by the one comparator: the bare command's
// refusal in docs/TESTS.md is what the tool prints, every command in order, in
// one sitting (docs/STANDARD.md onboarding point 5(c)).
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/release"

	"github.com/stretchr/testify/require"
)

// The `### First run` block of docs/TESTS.md is EXECUTED, every command in
// order, and its whole output compared with the block by the one comparator;
// nothing is normalised, because the refusal names only constants.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-release")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-release", lines)
	require.NoError(t, err)
	var results []onboarding.Result
	for _, s := range steps {
		var out, errs bytes.Buffer
		code := release.Main("nova-release", s.Args, "", &out, &errs)
		results = append(results, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errs.String()})
	}
	problems := onboarding.CompareTranscript(steps, results, nil)
	require.Empty(t, problems, "%v", problems)
}
