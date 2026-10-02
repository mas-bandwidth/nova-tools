package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONLinksKeepsTotalsAndProvenance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("[one](missing)\n[two](gone)\n"), 0600))
	exit, stdout, stderr := runCheck(t, "links", "--dir", dir, "--json", "--fail-max", "1")
	assert.Equal(t, 1, exit)
	assert.Empty(t, stderr)
	var out struct {
		Result struct {
			Status string
			Exit   int
		}
		Facts struct {
			Dir    string
			Broken int
		}
		Items []struct {
			Fields struct {
				File   string
				Line   int
				Target string
			}
		}
		More []struct {
			Shown int
			Total int
		}
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, "failed", out.Result.Status)
	assert.Equal(t, 1, out.Result.Exit)
	assert.Equal(t, dir, out.Facts.Dir)
	assert.Equal(t, 2, out.Facts.Broken)
	require.Len(t, out.Items, 1)
	assert.Equal(t, "a.md", out.Items[0].Fields.File)
	assert.Equal(t, 1, out.Items[0].Fields.Line)
	require.Len(t, out.More, 1)
	assert.Equal(t, 2, out.More[0].Total)
}

func TestJSONRefusalIsOneObjectWithEveryProblem(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runCheck(t, "kernel", "--json")
	assert.Equal(t, 2, exit)
	assert.Empty(t, stderr)
	var out struct {
		Result struct {
			Status string
			Why    []string
			Remedy string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, "refused", out.Result.Status)
	require.Len(t, out.Result.Why, 2)
	assert.Contains(t, strings.Join(out.Result.Why, " "), "--file")
	assert.Contains(t, strings.Join(out.Result.Why, " "), "--max-bytes")
	assert.Equal(t, "nova-check help", out.Result.Remedy)
}

func TestUnknownVerbNamesAvailableRemedies(t *testing.T) {
	t.Parallel()
	exit, _, stderr := runCheck(t, "bogus")
	assert.Equal(t, 2, exit)
	for _, want := range []string{"bogus", "the verbs are quickstart", "links", "run: nova-check help"} {
		assert.Contains(t, stderr, want)
	}
}

func TestHygieneHelpDescribesEveryFlag(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runCheck(t, "hygiene", "-h")
	assert.Equal(t, 0, exit)
	assert.Empty(t, stderr)
	for _, want := range []string{"git checkout to inspect", "base git ref", "head git ref", "allowed path globs", "allowed authors", "card kind", "finding lines", "positive seconds"} {
		assert.Contains(t, stdout, want)
	}
}

func TestSourceFreeExampleSetup(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	// Execute the printed setup in a directory containing no checkout fixtures.
	_, help, _ := runCheck(t, "help")
	start := strings.Index(help, "mkdir -p ")
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(help[start:], "\n\nexample:")
	require.GreaterOrEqual(t, end, 0)
	setup := exec.Command("sh", "-c", help[start:start+end])
	setup.Dir = scratch
	output, err := setup.CombinedOutput()
	require.NoError(t, err, string(output))
	for _, line := range examples(t) {
		args := strings.Fields(line)[1:]
		for i, arg := range args {
			if strings.HasPrefix(arg, "./self") {
				args[i] = filepath.Join(scratch, arg)
			}
		}
		exit, _, stderr := runCheck(t, args...)
		assert.Equal(t, 0, exit, stderr)
	}
}

func TestJSONCheckSuccessAndFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "record.md")
	require.NoError(t, os.WriteFile(file, []byte("# Kernel\n"), 0600))
	manifest := filepath.Join(dir, "MANIFEST")
	require.NoError(t, os.WriteFile(manifest, []byte("record.md\n"), 0600))
	for _, tc := range []struct {
		name string
		args []string
		exit int
	}{
		{"kernel pass", []string{"kernel", "--file", file, "--max-bytes", "4000"}, 0},
		{"kernel fail", []string{"kernel", "--file", file, "--max-bytes", "1"}, 1},
		{"attest", []string{"attest", "--home", dir, "--manifest", manifest}, 0},
		{"corpus", []string{"corpus", "--ledger", "testdata/example-self/corpus/anchors.md", "--root", exampleSelf, "--min-anchors", "2"}, 0},
		{"floors", []string{"floors", "--core", "../../internal/check/testdata/seed-core-floors.md", "--source", "../../internal/check/testdata/seed-floors.md"}, 0},
		{"nocode", []string{"nocode", "--dir", dir}, 0},
		{"deny lists", []string{"nocode", "--print-deny-list"}, 0},
		{"spelling", []string{"spelling", "--dir", dir}, 0},
		{"version", []string{"version"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := runCheck(t, append(tc.args, "--json")...)
			assert.Equal(t, tc.exit, exit, stderr)
			assert.Empty(t, stderr)
			var out struct {
				Result struct {
					Exit   int
					Status string
				}
			}
			require.NoError(t, json.Unmarshal([]byte(stdout), &out))
			assert.Equal(t, exit, out.Result.Exit)
			if exit == 0 {
				assert.Equal(t, "ok", out.Result.Status)
			} else {
				assert.Equal(t, "failed", out.Result.Status)
			}
		})
	}
}

func TestJSONSpellingWriteReflectsChangedFile(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "a.md")
	require.NoError(t, os.WriteFile(file, []byte("the teh word\n"), 0600))
	exit, stdout, stderr := runCheck(t, "spelling", "--file", file, "--write", "--json")
	assert.Equal(t, 0, exit, stderr)
	var out struct {
		Facts struct {
			Written      int
			Misspellings int
		}
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, 1, out.Facts.Written)
	assert.Equal(t, 1, out.Facts.Misspellings)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "the the word\n", string(raw))
}

func TestJSONUsesFlagParserValueBoundaries(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runCheck(t, "links", "--dir", "--json")
	assert.Equal(t, 2, exit)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, `dir "--json"`)
	assert.NotContains(t, stderr, "--dir is required")
	for _, value := range []string{"1", "True", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := runCheck(t, "links", "--dir", exampleSelf, "--json="+value)
			assert.Equal(t, 0, exit, stderr)
			assert.Empty(t, stderr)
			var out struct{ Result struct{ Status string } }
			require.NoError(t, json.Unmarshal([]byte(stdout), &out))
			assert.Equal(t, "ok", out.Result.Status)
		})
	}
}

func TestJSONDoesNotSwallowHelp(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"attest", "links", "kernel", "nocode", "floors", "corpus", "hygiene", "spelling", "version"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := runCheck(t, verb, "--json", "-h")
			assert.Equal(t, 0, exit)
			assert.Empty(t, stderr)
			assert.Contains(t, stdout, "nova-check "+verb)
			assert.Contains(t, stdout, "--json")
			if verb == "version" {
				assert.Contains(t, stdout, "print this build identity in a JSON envelope")
			}
		})
	}
}

func TestLinksBannerExampleMatchesOutput(t *testing.T) {
	t.Parallel()
	const command = "nova-check links --dir ./self"
	_, help, _ := runCheck(t, "help")
	require.Contains(t, help, command)
	scratch := t.TempDir()
	start := strings.Index(help, "mkdir -p ")
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(help[start:], "\n\nexample:")
	require.GreaterOrEqual(t, end, 0)
	setup := exec.Command("sh", "-c", help[start:start+end])
	setup.Dir = scratch
	output, err := setup.CombinedOutput()
	require.NoError(t, err, string(output))
	args := strings.Fields(command)[1:]
	args[2] = filepath.Join(scratch, args[2])
	exit, stdout, stderr := runCheck(t, args...)
	assert.Empty(t, stderr)
	step := onboarding.Step{Line: "$ " + command, Args: args, Want: []string{"LINKS OK files=1 links=0 excluded=0"}, StderrWhole: true}
	result := onboarding.Result{Code: exit, Stdout: stdout, Stderr: stderr}
	assert.Empty(t, onboarding.Compare(step, result, nil))
}
