package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The verb's door: the five required flags and the identity, refused together and by
// name, and the help line a stranger pastes.
func TestAcceptVerbRefusesWithoutItsFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"accept"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--job", "--card", "--base", "--bench", "--cert", "--identity"} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("the refusal does not name %s:\n%s", want, errb.String())
		}
	}
	errb.Reset()
	if code := run([]string{"accept", "--job", "j", "--card", "c", "--base", "b", "--bench", "n", "--cert", "f", "--identity", "Rowan"}, &out, &errb, time.Now().UTC()); code != 2 || !strings.Contains(errb.String(), "Name <email>") {
		t.Fatalf("a malformed --identity was not refused by shape: exit %d\n%s", code, errb.String())
	}
}

func TestAcceptUsageLine(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "nova-pulse accept  --selftest --root <dir> --bench <name> --cert <path>") {
		t.Fatalf("help has no accept line:\n%s", out.String())
	}
}
