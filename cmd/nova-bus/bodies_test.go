package main

import (
	"strings"
	"testing"
)

func TestBodiesFullUsesCountedFramesAndHonoursByteBudget(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "1000").mustCode(t, 0)
	if strings.Count(r.stdout, "INBOX BODY id=") != 1 || !strings.Contains(r.stdout, "bytes=44") {
		t.Fatalf("bounded body frame missing or malformed:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "INBOX BODIES printed=1 bytes=44 oversize=0 gaps=0 drained=false complete=false next=") {
		t.Fatalf("body receipt did not report bounded completion:\n%s", r.stdout)
	}
}
