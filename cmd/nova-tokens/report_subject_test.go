package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportSubjectIsOneQuotedValue pins the report line's subject: one
// quoted value, so the at= and build= inside it are not keys of the line
// (docs/STANDARD.md section 2: free text is oneline.Quote).
func TestReportSubjectIsOneQuotedValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "gemini", map[string]int{"input_tokens": 100}, "/x/schema/a.go")+"\n")

	r := invoke(t, "report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr)
	wantExit(t, r, 0)
	line := lineWith(r.stderr, "REPORT OK")
	require.NotEmpty(t, line, "stderr:\n%s", r.stderr)
	assertReportSubjectQuoted(t, line)
}

// assertReportSubjectQuoted checks one REPORT line: subject= is one quoted
// value, and at= and build= occur once each as keys of the line.
func assertReportSubjectQuoted(t *testing.T, line string) {
	t.Helper()
	i := strings.Index(line, "subject=")
	require.GreaterOrEqual(t, i, 0, "no subject= on %q", line)
	raw := line[i+len("subject="):]
	require.True(t, strings.HasPrefix(raw, `"`), "subject is not one quoted value: %s", line)
	n, err := quotedPrefixLen(raw)
	require.NoError(t, err, "subject quote does not close: %s", line)
	require.Equal(t, len(raw), n, "subject is not the last field, or text follows the quotes: %s", line)
	got, err := strconv.Unquote(raw)
	require.NoError(t, err, "subject does not unquote: %q", raw)
	assert.Contains(t, got, " at=", "quoted subject: %q", got)
	assert.Contains(t, got, " build=", "quoted subject: %q", got)

	counts := reportLineKeyCounts(t, line)
	assert.Equal(t, 1, counts["at"], "at= inside the subject is a key of the line: %s", line)
	assert.Equal(t, 1, counts["build"], "build= inside the subject is a key of the line: %s", line)
	assert.Equal(t, 1, counts["subject"], "subject= count on %s", line)
}

// quotedPrefixLen is the byte length of one Go double-quoted string at the
// start of s.
func quotedPrefixLen(s string) (int, error) {
	if len(s) < 2 || s[0] != '"' {
		return 0, strconv.ErrSyntax
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			if i >= len(s) {
				return 0, strconv.ErrSyntax
			}
			continue
		}
		if s[i] == '"' {
			return i + 1, nil
		}
	}
	return 0, strconv.ErrSyntax
}

// reportLineKeyCounts counts key= fields on one event line. A value that
// opens with a double quote is one field through the closing quote, so an
// at= or build= inside that value is not a key.
func reportLineKeyCounts(t *testing.T, line string) map[string]int {
	t.Helper()
	rest := line
	for range 2 {
		sp := strings.IndexByte(rest, ' ')
		require.GreaterOrEqual(t, sp, 0, "not a REPORT line: %q", line)
		rest = rest[sp+1:]
	}
	counts := map[string]int{}
	for rest != "" {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		sp := strings.IndexByte(rest, ' ')
		require.GreaterOrEqual(t, eq, 0, "not a key=value field in %q", line)
		require.False(t, sp >= 0 && sp < eq, "not a key=value field in %q", line)
		key := rest[:eq]
		val := rest[eq+1:]
		counts[key]++
		if strings.HasPrefix(val, `"`) {
			n, err := quotedPrefixLen(val)
			require.NoError(t, err, "unclosed quote in %q", line)
			rest = val[n:]
			continue
		}
		if sp = strings.IndexByte(val, ' '); sp >= 0 {
			rest = val[sp+1:]
			continue
		}
		rest = ""
	}
	return counts
}
