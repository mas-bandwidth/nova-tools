package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetrievalJSONAndTextCarryTheSameSourceEvidence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("# Rocket\n\n\nThe Rocket Engine burns hot fuel daily.\n"), 0600))
	for _, verb := range []string{"search", "check"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			query := "Rocket Engine burns hot fuel daily."
			operand := query
			if verb == "check" {
				operand = "-"
			}
			args := []string{verb, "--root", root, "--channels", "bm25", "--k", "3"}
			code, text, stderr := runCLI(t, query, append(args, operand)...)
			require.Equal(t, 0, code, stderr)
			assert.Contains(t, text, `a.md:4 "The Rocket Engine burns hot fuel daily."`)
			code, raw, stderr := runCLI(t, query, append(args, "--json", operand)...)
			require.Equal(t, 0, code, stderr)
			var out struct {
				Result struct {
					Verb, Status string
					Exit         int
				}
				Facts map[string]any
				Items []struct {
					Kind   string
					Fields map[string]any
				}
			}
			require.NoError(t, json.Unmarshal([]byte(raw), &out))
			assert.Equal(t, verb, out.Result.Verb)
			assert.Equal(t, "ok", out.Result.Status)
			hits := 0
			for _, item := range out.Items {
				if item.Kind == "hit" {
					hits++
					assert.Equal(t, float64(4), item.Fields["line"])
					assert.Equal(t, "The Rocket Engine burns hot fuel daily.", item.Fields["snippet"])
				}
			}
			assert.Equal(t, strings.Count(text, " HIT "), hits)
		})
	}
}

func TestRetrievalJSONRefusalReportsAllMissingInputs(t *testing.T) {
	t.Parallel()
	code, raw, stderr := runCLI(t, "", "search", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	for _, missing := range []string{"--root", "--k", "--channels", "no query words"} {
		assert.Contains(t, raw, missing)
	}
}

func TestAbsentCalibrationIsNotAMeasuredZero(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("Quokka Narwhal Wombat.\n"), 0600))
	args := []string{"search", "--root", root, "--channels", "bm25", "--k", "1"}
	code, text, stderr := runCLI(t, "", append(args, "quokka")...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, text, "CAL score=- score-channel=-")
	code, raw, stderr := runCLI(t, "", append(args, "--json", "quokka")...)
	require.Equal(t, 0, code, stderr)
	var out struct {
		Items []struct {
			Kind   string
			Fields map[string]any
		}
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	calibration := 0
	for _, item := range out.Items {
		if item.Kind == "cal" {
			calibration++
			assert.Nil(t, item.Fields["score"])
			assert.Nil(t, item.Fields["score-channel"])
		}
	}
	assert.Equal(t, 1, calibration)
}

// The snippet is the paragraph's first 120 bytes, so the words a reader
// searched for can sit just past the cut and never print, in either rendering.
// --whole prints the whole paragraph of each hit instead, bounded by a byte cap
// that says how many bytes it dropped.
func TestWholePrintsThePassageTheSnippetCut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// "zebra" is the query term and sits past the 120-byte snippet cut.
	paragraph := strings.Repeat("alpha ", 30) + "zebra"
	require.NoError(t, os.WriteFile(filepath.Join(root, "long.md"), []byte(paragraph+"\n"), 0o600))

	for _, verb := range []string{"search", "check"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			token, operand, stdin := "SEARCH", "zebra", ""
			if verb == "check" {
				token, operand, stdin = "MEMORY", "-", "zebra alpha alpha"
			}
			base := []string{verb, "--root", root, "--channels", "bm25", "--k", "1"}

			code, text, stderr := runCLI(t, stdin, append(append([]string{}, base...), operand)...)
			require.Equalf(t, 0, code, "%s answered %d: %s", verb, code, stderr)
			cut := theLine(t, text, token+" HIT ")
			assert.Containsf(t, cut, "alpha alpha", "the snippet no longer starts the paragraph:\n%s", text)
			assert.NotContainsf(t, cut, "zebra", "the 120-byte snippet now holds the query term, so the cut this pins is gone:\n%s", text)

			code, text, stderr = runCLI(t, stdin, append(append([]string{}, base...), "--whole", operand)...)
			require.Equalf(t, 0, code, "%s --whole answered %d: %s", verb, code, stderr)
			assert.Containsf(t, theLine(t, text, token+" HIT "), "zebra",
				"--whole did not print the whole paragraph the snippet cut:\n%s", text)

			code, raw, stderr := runCLI(t, stdin, append(append([]string{}, base...), "--json", "--whole", operand)...)
			require.Equalf(t, 0, code, "%s --json --whole answered %d: %s", verb, code, stderr)
			var out struct {
				Items []struct {
					Kind   string
					Fields map[string]any
				}
			}
			require.NoError(t, json.Unmarshal([]byte(raw), &out))
			whole := ""
			for _, item := range out.Items {
				if item.Kind == "hit" {
					whole, _ = item.Fields["whole"].(string)
				}
			}
			assert.Containsf(t, whole, "zebra", "--json did not carry the whole paragraph under --whole:\n%s", raw)
		})
	}

	// A paragraph past the byte cap is bounded, and the cap says what it dropped.
	capRoot := t.TempDir()
	long := strings.Repeat("beta ", 2000) + "zebra"
	require.NoError(t, os.WriteFile(filepath.Join(capRoot, "huge.md"), []byte(long+"\n"), 0o600))
	code, text, stderr := runCLI(t, "", "search", "--root", capRoot, "--channels", "bm25", "--k", "1", "--whole", "zebra")
	require.Equalf(t, 0, code, "search --whole on a long paragraph answered %d: %s", code, stderr)
	assert.Regexpf(t, `\.\.\.\+\d+B"`, theLine(t, text, "SEARCH HIT "),
		"a paragraph past the byte cap did not carry the cap's note:\n%s", text)
}
