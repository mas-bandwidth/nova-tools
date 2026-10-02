package ci

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fieldsIndexSource wraps a function body in a compilable file so the rule can
// be read against one shape at a time.
func fieldsIndexSource(body string) []byte {
	return []byte("package p\n\nimport (\n\t\"bytes\"\n\t\"strings\"\n)\n\nvar _ = strings.Fields\nvar _ = bytes.Fields\n\n" + body + "\n")
}

func fieldsIndexFindings(t *testing.T, body string) []string {
	t.Helper()
	findings, err := uncheckedSplitIndexes("internal/x/x.go", fieldsIndexSource(body))
	require.NoError(t, err, "parsing the fixture: %v", err)
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.line)
	}
	return lines
}

// TestFieldsIndexRefusesThePreFixCursor is the shape of Emma's #1390: a
// commit-only cursor line splits into two fields and the walker reaches
// fields[2:]. Nothing in the function ever measured the slice.
func TestFieldsIndexRefusesThePreFixCursor(t *testing.T) {
	t.Parallel()

	got := fieldsIndexFindings(t, `
func readCursor(line string) []string {
	fields := strings.Fields(line)
	return fields[2:]
}`)
	require.Len(t, got, 1, "want 1 finding for the pre-fix cursor, got %d: %v", len(got), got)
	for _, want := range []string{"slice expression on fields", "strings.Fields", "len(fields)", "readCursor", fieldsIndexAllowlistPath} {
		assert.Contains(t, got[0], want, "the finding does not name %q:\n%s", want, got[0])
	}
}

// TestFieldsIndexAcceptsTheFixedCursor is the fix the rule asks for: a length
// check with a refusal line ahead of the index.
func TestFieldsIndexAcceptsTheFixedCursor(t *testing.T) {
	t.Parallel()

	got := fieldsIndexFindings(t, `
func readCursor(line string) ([]string, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return nil, errShort
	}
	return fields[2:], nil
}

var errShort error`)
	require.Empty(t, got, "the fixed cursor must be clean, got: %v", got)
}

// TestFieldsIndexAcceptsTheShapesThatCannotBeShort walks the narrowings one by
// one: each of these is in range for every input, and refusing it would make the
// rule noise and train a reader to allowlist.
func TestFieldsIndexAcceptsTheShapesThatCannotBeShort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"reverse walk over len", `
func lastLine(raw string) string {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			return lines[i]
		}
	}
	return ""
}`},
		{"last element by len", `
func tail(raw string) string {
	lines := strings.Split(raw, "\x00")
	return lines[len(lines)-1]
}`},
		{"switch on len", `
func kind(g string) string {
	parts := strings.Split(g, ":")
	switch len(parts) {
	case 3:
		return parts[2]
	}
	return ""
}`},
		{"range over the slice", `
func each(raw string) int {
	fields := strings.Fields(raw)
	n := 0
	for i := range fields {
		n += len(fields[i])
	}
	return n
}`},
		{"whole-slice slice expression", `
func all(raw string) []string {
	fields := strings.Fields(raw)
	return fields[:]
}`},
		{"index zero of a literal-separator Split", `
func head(line string) string {
	parts := strings.Split(line, "\t")
	return parts[0]
}`},
		{"tail of a literal-separator Split", `
func rest(raw string) []string {
	lines := strings.Split(raw, "\n")
	return lines[1:]
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Empty(t, fieldsIndexFindings(t, c.body), "%s must be clean, got: %v", c.name, fieldsIndexFindings(t, c.body))
		})
	}
}

// TestFieldsIndexRefusesTheShapesThatCanBeShort is the other direction: the
// narrowings are narrow. strings.Fields gives no first element, a computed
// separator may be empty, and index 1 is past the end of a one-element Split.
func TestFieldsIndexRefusesTheShapesThatCanBeShort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"first field of strings.Fields", `
func cmdName(raw string) string {
	fields := strings.Fields(raw)
	return fields[0]
}`, "index expression on fields"},
		{"second element of a Split", `
func second(line string) string {
	parts := strings.Split(line, "=")
	return parts[1]
}`, "index expression on parts"},
		{"Split on a computed separator", `
func head(line, sep string) string {
	parts := strings.Split(line, sep)
	return parts[0]
}`, "index expression on parts"},
		{"Split on an empty separator", `
func runeAt(line string) string {
	parts := strings.Split(line, "")
	return parts[0]
}`, "index expression on parts"},
		{"bytes.Fields", `
func firstWord(raw []byte) []byte {
	fields := bytes.Fields(raw)
	return fields[0]
}`, "index expression on fields"},
		{"a high bound past the end", `
func firstTwo(raw string) []string {
	lines := strings.Split(raw, "\n")
	return lines[:2]
}`, "slice expression on lines"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fieldsIndexFindings(t, c.body)
			require.Len(t, got, 1, "want 1 finding for %s, got %d: %v", c.name, len(got), got)
			require.Contains(t, got[0], c.want, "the finding does not name %q:\n%s", c.want, got[0])
		})
	}
}

// TestFieldsIndexKeyIsFileAndFunction proves the allowlist key: the row a reader
// would add, receiver-qualified when the offender is a method.
func TestFieldsIndexKeyIsFileAndFunction(t *testing.T) {
	t.Parallel()

	findings, err := uncheckedSplitIndexes("cmd/nova-wake/serve.go", fieldsIndexSource(`
type server struct{ onNote string }

func (s *server) spawn() string {
	fields := strings.Fields(s.onNote)
	return fields[0]
}`))
	require.NoError(t, err)
	require.Len(t, findings, 1, "want 1 finding, got %d", len(findings))
	want := "cmd/nova-wake/serve.go:server.spawn"
	require.Equal(t, want, findings[0].key, "key = %q, want %q", findings[0].key, want)
}

// TestFieldsIndexAllowlistIsShrinkOnly reads the shipped list: every row must be
// a `file:function` a reader can find, and the list is empty today because the
// one offender the rule found was fixed rather than listed.
func TestFieldsIndexAllowlistIsShrinkOnly(t *testing.T) {
	t.Parallel()

	for _, row := range loadAllowlist(t, fieldsIndexAllowlistPath, shrinkOnly).Rows() {
		key := row.Key
		assert.Contains(t, key, ":", "%s row %q is not a `file:function` key", fieldsIndexAllowlistPath, key)
		assert.False(t, strings.HasSuffix(key, ":"), "%s row %q is not a `file:function` key", fieldsIndexAllowlistPath, key)
		line := row.Text
		assert.Contains(t, line, " #", "%s row %q carries no reason; every exception says why the data cannot be short", fieldsIndexAllowlistPath, line)
	}
}
