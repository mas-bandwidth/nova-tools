package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestZZProbe(t *testing.T) {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	re := regexp.MustCompile(`(?m)^[A-Z][A-Z -]* FAIL\b.*$`)
	for _, l := range []string{
		"release s1-nope --reason x", "resolve s1-nope", "take --as m1 s1-nope.w1@1", "finish --as m1 s1-nope.w1@1 --report x",
		"ask s1-nope", "read --as reader-a --ok s1-nope", "accept s1-nope", "rework s1-nope --fix x", "return s1-nope --reason x",
		"drop s1-nope --reason x", "rank s1-nope --first", "merge --stream s9 --batch 1", "resume --stream s1 --did x",
		"fleet up m9", "fleet down m9", "ci s1-nope --red", "wait note-nope --for 1m", "ack note-nope --reason x",
		"add --stream s1 s1-1", "add --stream s1 --count 1 --needs nope", "check", "repair", "tick", "accept --group nope", "inbox --open nope",
	} {
		code, out, errs := ta.do(l)
		t.Logf("%-50s code=%d fail=%q", l, code, re.FindAllString(out+errs, -1))
	}
	_ = strings.TrimSpace
}
