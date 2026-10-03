package hygiene

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// today's classification, in file order. A change to a name, its order, or
// its third field fails this file: acceptance of TEST: none is this set.
var kindClassToday = []struct {
	name    string
	ungated bool
}{
	{"fix-red", false},
	{"transcript-test", false},
	{"rebase", false},
	{"sweep", false},
	{"mutation-kill", false},
	{"guard", false},
	{"read", true},
	{"probe", true},
	{"text", true},
	{"tone", true},
	{"report", true},
}

// TestTheKindFileIsTheOneClassification pins the embedded name set and the
// third field together. The ungated names are exactly the five. An unknown
// name is not declared and is not ungated, so it is not given an empty gate list.
func TestTheKindFileIsTheOneClassification(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("kinds.txt")
	require.NoError(t, err)

	var rows []struct {
		name  string
		third string
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		require.GreaterOrEqual(t, len(fields), 3, "row %q has no third field", line)
		third := strings.TrimSpace(fields[2])
		assert.True(t, third == "gated" || third == "ungated", "third field %q is not gated or ungated", third)
		rows = append(rows, struct {
			name  string
			third string
		}{strings.TrimSpace(fields[0]), third})
	}

	require.Len(t, rows, len(kindClassToday))
	var wantNames []string
	wantUngated := map[string]bool{}
	for i, want := range kindClassToday {
		wantNames = append(wantNames, want.name)
		assert.Equal(t, want.name, rows[i].name)
		if want.ungated {
			assert.Equal(t, "ungated", rows[i].third, "%s", want.name)
			wantUngated[want.name] = true
		} else {
			assert.Equal(t, "gated", rows[i].third, "%s", want.name)
		}
		assert.Equal(t, want.ungated, KindUngated(want.name), "KindUngated(%s)", want.name)
		assert.True(t, KindDeclared(want.name), "KindDeclared(%s)", want.name)
	}
	require.Equal(t, wantNames, Kinds())

	parsedNames, parsedUngated := parseKindRows(string(raw))
	require.Equal(t, wantNames, parsedNames)
	require.Len(t, parsedUngated, len(wantUngated))
	for name := range parsedUngated {
		assert.True(t, wantUngated[name], "unexpected ungated name %q", name)
	}
	require.False(t, KindDeclared("not-a-declared-kind"), "an unknown name was declared")
	require.False(t, KindUngated("not-a-declared-kind"), "an unknown name was ungated")
	assert.False(t, parsedUngated["not-a-declared-kind"], "an unknown name was given an empty gate list")
}

// TestABadThirdFieldIsGated is the fail-closed rule. The living file has no
// bad field; these rows are synthetic, so today's file is not rewritten.
func TestABadThirdFieldIsGated(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		data    string
		kind    string
		ungated bool
	}{
		{name: "missing field", data: "read\tread and report\n", kind: "read"},
		{name: "empty field", data: "read\tread and report\t\n", kind: "read"},
		{name: "other value", data: "read\tread and report\tother\n", kind: "read"},
		{name: "wrong case", data: "read\tread and report\tUngated\n", kind: "read"},
		{name: "the word gated", data: "probe\tread and report\tgated\n", kind: "probe"},
		{name: "blank field", data: "text\tread and report\t \n", kind: "text"},
		{name: "a later field does not promote", data: "tone\tread and report\tgated\tungated\n", kind: "tone"},
		{name: "exact ungated", data: "report\tread and report\tungated\n", kind: "report", ungated: true},
		{name: "exact ungated with a later field", data: "read\tread and report\tungated\textra\n", kind: "read", ungated: true},
		{name: "carriage return", data: "read\tread and report\tungated\r\n", kind: "read", ungated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			names, ungated := parseKindRows(tc.data)
			require.Equal(t, []string{tc.kind}, names, "a bad field still declares the name")
			assert.Equal(t, tc.ungated, ungated[tc.kind])
		})
	}
}

// TestARepeatedKindRowKeepsTheFirstClass pins first-wins, which is how the
// name set already treats a repeated name. A later row does not reclassify it.
func TestARepeatedKindRowKeepsTheFirstClass(t *testing.T) {
	t.Parallel()

	t.Run("gated first", func(t *testing.T) {
		t.Parallel()
		names, ungated := parseKindRows("read\tdesc\tgated\nread\tdesc\tungated\n")
		require.Equal(t, []string{"read"}, names)
		assert.False(t, ungated["read"])
	})
	t.Run("ungated first", func(t *testing.T) {
		t.Parallel()
		names, ungated := parseKindRows("read\tdesc\tungated\nread\tdesc\tgated\n")
		require.Equal(t, []string{"read"}, names)
		assert.True(t, ungated["read"])
	})
	t.Run("a blank name is not a row", func(t *testing.T) {
		t.Parallel()
		names, ungated := parseKindRows("# read\tdesc\tungated\n\n\tdesc\tungated\nfix-red\tdesc\tgated\n")
		require.Equal(t, []string{"fix-red"}, names)
		assert.False(t, ungated["read"])
		assert.False(t, ungated["fix-red"])
	})
}
