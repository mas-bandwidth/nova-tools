package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func decodeCheckJSON(t *testing.T, stdout, stderr string) struct {
	Result struct {
		Status string
		Exit   int
		Why    []string
	}
	Facts map[string]json.RawMessage
} {
	t.Helper()
	require.Empty(t, stderr)
	var answer struct {
		Result struct {
			Status string
			Exit   int
			Why    []string
		}
		Facts map[string]json.RawMessage
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	require.NoError(t, decoder.Decode(&answer))
	var extra any
	require.ErrorIs(t, decoder.Decode(&extra), io.EOF)
	return answer
}

func TestCheckQuickstartEmitsCompositeJson(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		exit       int
		findings   int
	}{
		{"pass", "# Notes\n", 0, 0},
		{"both checks fail", "[missing](missing.md)\n[other](other.md)\n", 1, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte(tc.text), 0600))
			if tc.exit == 1 {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "code.go"), []byte("package fixture\n"), 0600))
			}
			textExit, textOut, textErr := runCheck(t, "quickstart", "--dir", dir)
			require.Equal(t, tc.exit, textExit, textErr)
			require.Contains(t, textOut, "QUICKSTART RUN")
			exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir, "--json", "--max", "1")
			answer := decodeCheckJSON(t, stdout, stderr)
			require.Equal(t, textExit, exit)
			require.Equal(t, exit, answer.Result.Exit)
			var checks []struct {
				Result struct {
					Verb string
					Exit int
				}
				Items []struct{ Fields struct{ Target string } }
				More  []struct {
					Shown int
					Total int
				}
			}
			require.NoError(t, json.Unmarshal(answer.Facts["checks"], &checks))
			require.Len(t, checks, 2)
			require.Equal(t, "links", checks[0].Result.Verb)
			require.Equal(t, "nocode", checks[1].Result.Verb)
			if tc.exit == 1 {
				require.Equal(t, "failed", answer.Result.Status)
				require.Equal(t, 1, checks[0].Result.Exit)
				require.Equal(t, 1, checks[1].Result.Exit)
				require.Equal(t, "missing.md", checks[0].Items[0].Fields.Target)
				require.Len(t, checks[0].More, 1)
				require.Equal(t, 2, checks[0].More[0].Total)
				require.Equal(t, 1, checks[0].More[0].Shown)
			} else {
				require.Equal(t, "ok", answer.Result.Status)
			}
			var findings int
			require.NoError(t, json.Unmarshal(answer.Facts["findings"], &findings))
			require.GreaterOrEqual(t, findings, tc.findings)
		})
	}
	for _, args := range [][]string{{"quickstart"}, {"quickstart", "--dir", filepath.Join(t.TempDir(), "missing")}} {
		textExit, _, _ := runCheck(t, args...)
		exit, stdout, stderr := runCheck(t, append(args, "--json")...)
		answer := decodeCheckJSON(t, stdout, stderr)
		require.Equal(t, textExit, exit)
		require.Equal(t, 2, exit)
		require.Equal(t, "refused", answer.Result.Status)
	}
}

func TestCheckDogfoodGateEmitsStructuredJson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "ok.json", map[string]any{"tool": "nova-example", "verb": "links", "by": "fixture-runner", "at": "2026-10-13T12:00:00Z", "ok": true, "notes": "Checked fixture links"})
	authors := writeAuthors(t, dir, "nova-example links = fixture-author\nnova-example quickstart = fixture-author\n")
	args := []string{"dogfood", "gate", "--cli", cli, "--receipts", receipts, "--authors", authors, "--require-all"}
	textExit, _, textErr := dogfoodRun(t, args...)
	require.Equal(t, 1, textExit, textErr)
	exit, stdout, stderr := dogfoodRun(t, append(args, "--json")...)
	answer := decodeCheckJSON(t, stdout, stderr)
	_, repeated, repeatedErr := dogfoodRun(t, append(args, "--json")...)
	require.Empty(t, repeatedErr)
	require.JSONEq(t, stdout, repeated)
	require.Equal(t, textExit, exit)
	require.Equal(t, exit, answer.Result.Exit)
	require.Equal(t, "failed", answer.Result.Status)
	var verdict bool
	require.NoError(t, json.Unmarshal(answer.Facts["verdict"], &verdict))
	require.False(t, verdict)
	var missing, uncovered []struct {
		Tool   string
		Verb   string
		Reason string
	}
	require.NoError(t, json.Unmarshal(answer.Facts["missing_authors"], &missing))
	require.Len(t, missing, 1)
	require.Equal(t, "nocode", missing[0].Verb)
	require.NoError(t, json.Unmarshal(answer.Facts["uncovered_verbs"], &uncovered))
	require.Len(t, uncovered, 2)
	require.Equal(t, "quickstart", uncovered[0].Verb)
	require.Equal(t, "nocode", uncovered[1].Verb)
	require.NotEmpty(t, uncovered[0].Reason)
	var verified []struct {
		File    string
		Receipt struct {
			By   string
			Verb string
		}
	}
	require.NoError(t, json.Unmarshal(answer.Facts["verified_receipts"], &verified))
	require.Len(t, verified, 1)
	require.Equal(t, "fixture-runner", verified[0].Receipt.By)
	require.Equal(t, filepath.Join(receipts, "ok.json")+":1", verified[0].File)
	// Caps bound each listing but preserve the verdict and true totals.
	_, capped, cappedErr := dogfoodRun(t, append(args, "--json", "--max", "1")...)
	cappedAnswer := decodeCheckJSON(t, capped, cappedErr)
	require.Equal(t, "failed", cappedAnswer.Result.Status)
	require.NoError(t, json.Unmarshal(cappedAnswer.Facts["uncovered_verbs"], &uncovered))
	require.Len(t, uncovered, 1)
	var envelope struct {
		More []struct {
			Kind  string
			Shown int
			Total int
		}
	}
	require.NoError(t, json.Unmarshal([]byte(capped), &envelope))
	totals := map[string]int{}
	for _, more := range envelope.More {
		require.Equal(t, 1, more.Shown)
		totals[more.Kind] = more.Total
	}
	require.Equal(t, 2, totals["uncovered_verbs"])
	require.Equal(t, 2, totals["findings"])
	require.Equal(t, 3, totals["rows"])
	// Without require-all, uncovered declarations do not change the existing verdict.
	exit, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--json")
	answer = decodeCheckJSON(t, stdout, stderr)
	require.Equal(t, 0, exit)
	require.Equal(t, "ok", answer.Result.Status)
	require.NoError(t, json.Unmarshal(answer.Facts["verdict"], &verdict))
	require.True(t, verdict)
	// Malformed receipts remain a failure; their actual path/reason stays available.
	require.NoError(t, os.WriteFile(filepath.Join(receipts, "bad.json"), []byte("{broken"), 0600))
	exit, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--json")
	answer = decodeCheckJSON(t, stdout, stderr)
	require.Equal(t, 1, exit)
	require.Equal(t, "failed", answer.Result.Status)
	require.Contains(t, stdout, "bad.json")
	require.Contains(t, stdout, "invalid-receipt")
	exit, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--json")
	answer = decodeCheckJSON(t, stdout, stderr)
	require.Equal(t, 2, exit)
	require.Equal(t, "refused", answer.Result.Status)
	require.NotEmpty(t, answer.Result.Why)
}
