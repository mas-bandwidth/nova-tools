package main

import (
	"strings"
	"testing"
)

// A misspelled flag is one line, `unknown flag --x; run: nova-sprint help <verb>`, for
// every verb: the flag package's words are not glued to the verb's own ("takes no words
// flag provided but not defined").
func TestMisspelledFlagIsOneCleanLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, c := range []struct{ line, want string }{
		{"add --wrong", "nova-sprint add REFUSED: unknown flag --wrong; the flags of add are --actor, --after, --before, --brief, --brief-dir, --brief-file, --count, --epoch, --json, --max, --needs, --op, --redis, --rules, --score, --sentinel and 3 more; run: nova-sprint help add\n"},
		{"init --x", "nova-sprint init REFUSED: unknown flag --x; the flags of init are --actor, --coordinator, --epoch, --json, --max, --members, --op, --readers, --redis, --rules; run: nova-sprint help init\n"},
		{"where --zz", "nova-sprint where REFUSED: unknown flag --zz; the flags of where are --actor, --at-epoch, --epoch, --every, --json, --max, --op, --redis, --stale, --watch; run: nova-sprint help where\n"},
		{"inbox --zz", "nova-sprint inbox REFUSED: unknown flag --zz; the flags of inbox are --actor, --at-epoch, --deadline, --epoch, --json, --max, --op, --open, --read, --redis, --stale, --timeout, --wait; run: nova-sprint help inbox\n"},
		{"clear --zz", "nova-sprint clear REFUSED: unknown flag --zz; the flags of clear are --actor, --confirm, --epoch, --json, --max, --op, --redis; run: nova-sprint help clear\n"},
		{"teardown --zz", "nova-sprint teardown REFUSED: unknown flag --zz; the flags of teardown are --actor, --confirm, --epoch, --json, --max, --op, --redis; run: nova-sprint help teardown\n"},
		{"log --zz", "nova-sprint log REFUSED: unknown flag --zz; the flags of log are --actor, --at-epoch, --card, --epoch, --json, --max, --member, --op, --redis, --since, --stream; run: nova-sprint help log\n"},
		{"card --zz s1-1", "nova-sprint card REFUSED: unknown flag --zz; the flags of card are --actor, --at-epoch, --epoch, --fields, --json, --max, --op, --redis; run: nova-sprint help card\n"},
		{"add --stream s1 --brief", "nova-sprint add REFUSED: --brief wants a value; run: nova-sprint help add\n"},
	} {
		code, out, errs := ta.do(c.line)
		if code != 2 || out != "" || errs != c.want {
			t.Errorf("%s: exit %d, out %q, err %q; want exit 2 and exactly %q", c.line, code, out, errs, c.want)
		}
		if strings.Contains(errs, "not defined") || strings.Contains(errs, "takes no words") {
			t.Errorf("%s: the flag package's words are in the line: %q", c.line, errs)
		}
	}
}
