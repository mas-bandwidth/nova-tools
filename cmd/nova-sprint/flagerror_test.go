package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A misspelled flag is one line, `unknown flag --x; run: nova-sprint help <verb>`, for
// every verb: the flag package's words are not glued to the verb's own ("takes no words
// flag provided but not defined").
func TestMisspelledFlagIsOneCleanLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, c := range []struct{ line, want string }{
		{"add --wrong", "nova-sprint add: unknown flag --wrong; run: nova-sprint help add\n"},
		{"init --x", "nova-sprint init: unknown flag --x; run: nova-sprint help init\n"},
		{"where --zz", "nova-sprint where: unknown flag --zz; run: nova-sprint help where\n"},
		{"inbox --zz", "nova-sprint inbox: unknown flag --zz; run: nova-sprint help inbox\n"},
		{"clear --zz", "nova-sprint clear: unknown flag --zz; run: nova-sprint help clear\n"},
		{"teardown --zz", "nova-sprint teardown: unknown flag --zz; run: nova-sprint help teardown\n"},
		{"log --zz", "nova-sprint log: unknown flag --zz; run: nova-sprint help log\n"},
		{"card --zz s1-1", "nova-sprint card: unknown flag --zz; run: nova-sprint help card\n"},
		{"add --stream s1 --brief", "nova-sprint add: --brief wants a value; run: nova-sprint help add\n"},
	} {
		code, out, errs := ta.do(c.line)
		assert.Equal(t, 2, code, "%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		assert.Empty(t, out, "%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		assert.Equal(t, c.want, errs, "%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		assert.NotContains(t, errs, "not defined", "%s: the flag package's words are in the line", c.line)
		assert.NotContains(t, errs, "takes no words", "%s: the flag package's words are in the line", c.line)
	}
}
