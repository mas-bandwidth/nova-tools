package main

import (
	"encoding/json"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCiFunctionalEmitsStructuredJson(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte("package sample\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "x_test.go"), []byte("//go:build functional\n\npackage sample\nimport \"testing\"\nfunc TestShared(t *testing.T){t.Parallel()}\nfunc Test"+strings.ToUpper(name[:1])+name[1:]+"(t *testing.T){t.Parallel()}\n"), 0o644))
	}
	args := []string{"functional", filepath.Join(root, "...")}
	code, plain, stderr := runCI(t, args, "")
	require.Equal(t, 0, code, stderr)
	lines := strings.Split(strings.TrimSpace(plain), "\n")
	require.Len(t, lines, 2)
	require.Equal(t, "^(TestAlpha|TestBeta|TestShared)$", lines[1])
	code, out, stderr := runCI(t, append([]string{"functional", "--json"}, args[1:]...), "")
	require.Equal(t, 0, code, stderr)
	require.Empty(t, stderr)
	var got struct {
		Packages []string `json:"packages"`
		Pattern  string   `json:"pattern"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	require.NoError(t, dec.Decode(&got))
	require.ErrorIs(t, dec.Decode(new(any)), io.EOF)
	assert.Equal(t, strings.Fields(lines[0]), got.Packages)
	assert.Equal(t, lines[1], got.Pattern)
	code, out, stderr = runCI(t, append([]string{"functional", "--json=false"}, args[1:]...), "")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, plain, out, "explicit false preserves the make-facing legacy output")
	t.Run("none selected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte("package sample\n"), 0o644))
		code, out, stderr := runCI(t, []string{"functional", "--json", dir}, "")
		require.Equal(t, 0, code, stderr)
		assert.JSONEq(t, `{"packages":[],"pattern":""}`, out)
		code, out, stderr = runCI(t, []string{"functional", dir}, "")
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs\n", out)
	})
}

func TestCiSlowtestsHighlightsUnenforcedBudgetBreach(t *testing.T) {
	t.Parallel()
	stdin := `{"Action":"pass","Package":"example.com/pkg/sample","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg/sample","Elapsed":75.3}
`
	// This quoted file path must survive the suggested shell command. The row
	// leaves the package over budget while allowing its slow top-level test.
	allow := filepath.Join(t.TempDir(), "allow list's.tsv")
	require.NoError(t, os.WriteFile(allow, []byte("pkg/sample\tTestA\t4.5\t3s@run1\n"), 0o644))
	for _, tc := range []struct {
		name   string
		flags  []string
		input  string
		code   int
		status string
	}{
		{"package", []string{"--budget", "60", "--test-budget", "1", "--allowlist", allow, "--max", "1"}, stdin, 0, "ok"},
		{"test only", []string{"--package-budget", "120", "--test-budget", "1"}, stdin, 0, "ok"},
		{"terminal flag marker", []string{"--budget", "60", "--"}, stdin, 0, "ok"},
		{"explicit false", []string{"--budget", "60", "--enforce=false"}, stdin, 0, "ok"},
		{"independent failure stays failed", []string{"--budget", "60"}, stdin + `{"Action":"start","Package":"example.com/truncated"}` + "\n", 1, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"slowtests", "--load", "1", "--cpus", "2"}, tc.flags...)
			code, plain, stderr := runCI(t, args, tc.input)
			require.Equal(t, tc.code, code, stderr)
			require.Contains(t, plain, "CI-SLOW WARN within_budget=false enforce=false")
			_, remedy, ok := strings.Cut(plain, "run: ")
			require.True(t, ok)
			remedy = strings.TrimSuffix(remedy, "\n")
			words, err := ratingsCommandWords(remedy)
			require.NoError(t, err)
			require.Equal(t, "nova-ci", words[0])
			require.Equal(t, "--enforce", words[len(words)-1])
			code, enforced, stderr := runCI(t, words[1:], tc.input)
			require.Equal(t, 1, code, stderr)
			assert.NotContains(t, enforced, "CI-SLOW WARN", "enforced behavior remains its normal failure")
			code, out, stderr := runCI(t, append([]string{"slowtests", "--json"}, args[1:]...), tc.input)
			require.Equal(t, tc.code, code, stderr)
			var got struct {
				Result struct {
					Status string `json:"status"`
					Exit   int    `json:"exit"`
					Remedy string `json:"remedy"`
				} `json:"result"`
				Facts map[string]any `json:"facts"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &got))
			assert.Equal(t, tc.status, got.Result.Status)
			assert.Equal(t, tc.code, got.Result.Exit)
			require.Equal(t, false, got.Facts["within_budget"])
			require.NotEmpty(t, got.Facts["warning"])
			words, err = ratingsCommandWords(got.Result.Remedy)
			require.NoError(t, err)
			code, out, stderr = runCI(t, words[1:], tc.input)
			require.Equal(t, 1, code, stderr)
			got.Facts = nil
			got.Result.Remedy = ""
			require.NoError(t, json.Unmarshal([]byte(out), &got))
			assert.Equal(t, "failed", got.Result.Status)
			assert.NotContains(t, got.Facts, "warning")
			assert.NotContains(t, got.Facts, "within_budget")
			assert.Empty(t, got.Result.Remedy)
		})
	}
	t.Run("under budget stays unchanged", func(t *testing.T) {
		t.Parallel()
		code, out, stderr := runCI(t, []string{"slowtests", "--budget", "120", "--load", "1", "--cpus", "2"}, stdin)
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "CI-SLOW OK packages=1 slowest=example.com/pkg/sample:75.3s\nCI-LOAD load=1.00 cpus=2 per-cpu=0.50: measured, not a verdict\n", out)
	})
}

// The onboarding parser intentionally does not accept shell escapes outside
// quotes. Convert POSIX's escaped apostrophe to its equivalent adjacent
// quoted literals, leaving every argument's value unchanged for the replay.
func ratingsCommandWords(command string) ([]string, error) {
	return onboarding.SplitShell(strings.ReplaceAll(command, `'\''`, `'"'"'`))
}
