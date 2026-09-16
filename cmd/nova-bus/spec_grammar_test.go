package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The nova-bus section of docs/SPEC.md carries a verb list ("### The verbs")
// and an output grammar ("### Output grammar") that must name, byte for byte,
// every verb the binary ships and every token it prints. This test is what
// catches drift the moment a verb gains a case in main.go's dispatch with no
// line in the verb list, or a token is printed with no line in the grammar.

func novaBusSection(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod)")
		}
		dir = parent
	}
	data, err := os.ReadFile(filepath.Join(dir, "docs", "SPEC.md"))
	if err != nil {
		t.Fatalf("read SPEC.md: %v", err)
	}
	doc := string(data)
	start := strings.Index(doc, "## nova-bus")
	if start < 0 {
		t.Fatal("no nova-bus section in SPEC.md")
	}
	rest := doc[start:]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func fencedBlock(t *testing.T, section, heading string) string {
	t.Helper()
	start := strings.Index(section, heading)
	if start < 0 {
		t.Fatalf("no %q in the nova-bus section", heading)
	}
	rest := section[start:]
	open := strings.Index(rest, "```")
	if open < 0 {
		t.Fatalf("%q has no fenced block", heading)
	}
	rest = rest[open+3:]
	close := strings.Index(rest, "```")
	if close < 0 {
		t.Fatalf("%q block does not close", heading)
	}
	return rest[:close]
}

func TestVerbListNamesEveryShippedVerb(t *testing.T) {
	section := novaBusSection(t)
	block := fencedBlock(t, section, "### The verbs")
	verbs := []string{
		"draft", "prepare", "send", "inbox", "receipt", "close",
		"wait", "check", "names", "version", "help",
	}
	for _, v := range verbs {
		want := "nova-bus " + v
		if !strings.Contains(block, want) {
			t.Errorf("nova-bus %s ships, but the verb list has no line for it", v)
		}
	}
}

func TestOutputGrammarListsEveryPrintedToken(t *testing.T) {
	section := novaBusSection(t)
	block := fencedBlock(t, section, "### Output grammar")
	tokens := []string{
		"DRAFT REFUSED:", "DRAFT NOTE", "DRAFT OK",
		"PREPARE NOTE", "PREPARE FAIL",
		"SEND NOTE", "SEND OK", "SEND FAIL", "SEND REFUSED",
		"INBOX SCOPE", "INBOX LEGACY", "INBOX OPEN", "INBOX UNREADABLE",
		"INBOX UNADDRESSED", "INBOX SWITCH", "INBOX NOTE", "INBOX HEARD",
		"INBOX RECEIPT", "INBOX OK", "INBOX CURSOR", "INBOX FAIL", "INBOX REFUSED",
		"INBOX BODIES", "INBOX BODIES GAP", "INBOX BODY OVERSIZE",
		"INBOX BODY", "INBOX BODY END",
		"WAIT as=", "WAIT NOTE", "WAIT POLL", "WAIT OK", "WAIT TIMEOUT",
		"WAIT REFUSED", "WAIT DONE", "WAIT ADVANCED",
		"RECEIPT ALREADY", "RECEIPT OK", "RECEIPT FAIL", "RECEIPT REFUSED",
		"CLOSE OK", "CLOSE FAIL", "CLOSE REFUSED",
		"BUS SCOPE", "BUS INDEX", "BUS OK", "BUS WARN", "BUS FAIL", "BUS REFUSED",
		"NAMES NAME", "NAMES GROUP", "NAMES OK",
	}
	for _, tok := range tokens {
		if !strings.Contains(block, tok) {
			t.Errorf("the binary prints %q, but the output grammar has no line for it", tok)
		}
	}
}
