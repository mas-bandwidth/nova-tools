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
		{"add --wrong", "nova-sprint add REFUSED: unknown flag --wrong; the flags of add are --actor, --after, --allow-personal-base, --allow-shared-paths, --before, --brief, --brief-dir, --brief-file, --brief-op, --count, --decide-record, --epoch, --held, --json, --max, --needs and 10 more; run: nova-sprint help add\n"},
		{"init --x", "nova-sprint init REFUSED: unknown flag --x; the flags of init are --actor, --attempts, --coordinator, --epoch, --json, --max, --members, --op, --owner, --readers, --redis, --rules; run: nova-sprint help init\n"},
		{"where --zz", "nova-sprint where REFUSED: unknown flag --zz; the flags of where are --actor, --all, --archived, --at-epoch, --cards, --epoch, --every, --fresh, --json, --max, --op, --redis, --release, --rows, --stale, --watch; run: nova-sprint help where\n"},
		{"inbox --zz", "nova-sprint inbox REFUSED: unknown flag --zz; the flags of inbox are --actor, --at-epoch, --deadline, --epoch, --json, --max, --op, --open, --push, --read, --redis, --stale, --timeout, --wait; run: nova-sprint help inbox\n"},
		{"clear --zz", "nova-sprint clear REFUSED: unknown flag --zz; the flags of clear are --actor, --confirm, --epoch, --json, --max, --op, --redis; run: nova-sprint help clear\n"},
		{"teardown --zz", "nova-sprint teardown REFUSED: unknown flag --zz; the flags of teardown are --actor, --confirm, --epoch, --json, --max, --op, --redis; run: nova-sprint help teardown\n"},
		{"log --zz", "nova-sprint log REFUSED: unknown flag --zz; the flags of log are --actor, --at-epoch, --card, --epoch, --json, --max, --member, --op, --redis, --since, --stream; run: nova-sprint help log\n"},
		{"card --zz s1-1", "nova-sprint card REFUSED: unknown flag --zz; the flags of card are --actor, --all, --at-epoch, --brief, --epoch, --fields, --json, --max, --op, --redis, --stream; run: nova-sprint help card\n"},
		{"add --stream s1 --brief", "nova-sprint add REFUSED: --brief wants a value; run: nova-sprint help add\n"},
	} {
		code, out, errs := ta.do(c.line)
		assert.Equal(t, 2, code, "%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		assert.Empty(t, out, "%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		assert.Equal(t, c.want, errs, "%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		assert.NotContains(t, errs, "not defined", "%s: the flag package's words are in the line", c.line)
		assert.NotContains(t, errs, "takes no words", "%s: the flag package's words are in the line", c.line)
	}
}
