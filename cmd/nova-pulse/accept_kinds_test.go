package main

// T06b (#1651), SPEC-TOOLWORK §5 rule 3: `nova-pulse accept --kinds` prints the kinds
// table. It judges nothing, so it is the one shape of `accept` that wants no --job, no
// --bench and no certification record: a person asking what a kind's gate is should not
// have to have a certified bench to be told.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestAcceptKindsPrintsTheTableWithoutABench(t *testing.T) {
	var out, errs bytes.Buffer
	code := cmdAccept([]string{"--kinds"}, &out, &errs, time.Now())
	if code != 0 {
		t.Fatalf("`accept --kinds` exited %d, want 0\nstderr: %s", code, errs.String())
	}
	if errs.Len() != 0 {
		t.Errorf("`accept --kinds` refused something: %s", errs.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("the table is one line per kind, got:\n%s", out.String())
	}
	for _, want := range []string{
		"KIND name=fix-red gate=hygiene,shape,positive,mutate",
		"KIND name=read gate=none",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the printed table has no %q:\n%s", want, out.String())
		}
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "KIND name=") {
			t.Errorf("a printed row is not a KIND line: %q", l)
		}
	}
}
