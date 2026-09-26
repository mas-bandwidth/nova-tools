//go:build functional

package main

import (
	"bytes"
	"context"
	"testing"
)

// TestCensusSprintVerb: flag misuse of `census --sprint` exits 2 with nothing
// on stdout; every well-formed request is refused, one line on stdout, exit
// 1, whatever the s:<S>:card family holds (#4411), and reads nothing: the
// Redis named is a port nothing listens on. TestOneCountRetiredFamilyProbe
// runs the same refusal against a store whose family holds a landed card.
func TestCensusSprintVerb(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, args := range map[string][]string{
		"keys without sprint": {"--redis", "127.0.0.1:1", "--keys", "queued"},
		"sprint with fields":  {"--redis", "127.0.0.1:1", "--sprint", "s1", "--fields", "state"},
		"sprint with set":     {"--redis", "127.0.0.1:1", "--sprint", "s1", "--set", "benches"},
		"sprint with a glob":  {"--redis", "127.0.0.1:1", "--sprint", "s*"},
		"keys twice":          {"--redis", "127.0.0.1:1", "--sprint", "s1", "--keys", "landed,landed"},
	} {
		var out, errOut bytes.Buffer
		if code := runCensus(ctx, args, &out, &errOut); code != 2 || out.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q; want 2 and nothing", name, code, out.String())
		}
	}
	const want = `REFUSED census reads a retired key family; remedy="nova-sprint ws counts"` + "\n"
	for name, args := range map[string][]string{
		"sprint":             {"--redis", "127.0.0.1:1", "--sprint", "v1"},
		"sprint and keys":    {"--redis", "127.0.0.1:1", "--sprint", "v1", "--keys", "landed,queued"},
		"sprint, no --redis": {"--sprint", "v1"},
	} {
		var out, errOut bytes.Buffer
		if code := runCensus(ctx, args, &out, &errOut); code != 1 || out.String() != want || errOut.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 1 and %q", name, code, out.String(), errOut.String(), want)
		}
	}
}
