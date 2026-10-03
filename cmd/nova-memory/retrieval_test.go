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
	code, stdout, stderr := runCLI(t, "", "search", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	for _, missing := range []string{"--root", "--k", "--channels", "no query words"} {
		assert.Contains(t, stderr, missing)
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
