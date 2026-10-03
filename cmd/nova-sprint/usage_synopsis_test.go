package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The verb-table synopsis is the one source of a verb's usage: a verb's -h
// leads with `usage: nova-sprint <verb> <synopsis>` (the synopsis empty for a
// verb with no words and no flags of its own), never the generic `[flags]`.
// Every verb's usage line is its synopsis, so a cold coordinator reads the
// same words from `nova-sprint help` and from `<verb> -h` (nova-tools#5154).
func TestEveryVerbUsageLineIsItsSynopsis(t *testing.T) {
	t.Parallel()
	for _, v := range verbs {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			code := newApp(func(string) string { return "" }).run(append(strings.Fields(v.name), "-h"), &out, &errb)
			require.Equal(t, 0, code, "%s -h: %d %q", v.name, code, errb.String())
			require.Empty(t, errb.String(), "%s -h wrote to stderr", v.name)
			first, _, _ := strings.Cut(out.String(), "\n")
			want := "usage: nova-sprint " + strings.TrimSpace(v.name+" "+v.syntax)
			assert.Equal(t, want, first, "%s -h", v.name)
		})
	}
}
