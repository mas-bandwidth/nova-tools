package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// docs/TESTS.md promises that every transcript line is real output pasted whole, and its
// `### First run` block is the page a stranger meets first. Field VALUES are the recording
// machine's and differ by platform, so they are deliberately not compared here. The field
// NAMES come from one format string at cmd/nova-sandbox/main.go:437 with no platform branch
// above it, so they are the same on every bench -- which is what makes the drift real and
// this test portable. `hosts=` was the field that went missing from the documented line.
func TestTheCheckTranscriptNamesEveryFieldTheVerbPrints(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	var documented string
	inSection := false
	for _, l := range strings.Split(string(doc), "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(l, "## ") {
			inSection = l == "## nova-sandbox"
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(trimmed, "CHECK OK ") {
			if documented != "" {
				t.Fatalf("the nova-sandbox section has more than one CHECK OK line: %q and %q", documented, trimmed)
			}
			documented = trimmed
		}
	}
	if documented == "" {
		t.Fatal("TESTS.md has no CHECK OK transcript line to pin")
	}

	var out, errb bytes.Buffer
	if code := run([]string{"check"}, strings.NewReader(""), &out, &errb, os.Environ()); code != 0 {
		t.Fatalf("nova-sandbox check exited %d, want 0", code)
	}
	var printed string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "CHECK OK ") {
			if printed != "" {
				t.Fatalf("nova-sandbox check printed more than one CHECK OK line: %q and %q", printed, l)
			}
			printed = strings.TrimSpace(l)
		}
	}
	if printed == "" {
		t.Fatal("nova-sandbox check printed no CHECK OK line")
	}

	documentedFields := checkFieldNames(documented)
	printedFields := checkFieldNames(printed)
	if !reflect.DeepEqual(documentedFields, printedFields) {
		missing := diffFieldNames(printedFields, documentedFields)
		extra := diffFieldNames(documentedFields, printedFields)
		t.Errorf("TESTS.md CHECK OK line does not name every field the verb prints\n documented: %q\n printed:    %q\n documented fields: %v\n printed fields:    %v\n missing from document: %v\n in document but not printed: %v",
			documented, printed, documentedFields, printedFields, missing, extra)
	}
}

// checkFieldNames is the card's one rule: split the line on whitespace, and for each token
// matching ^[a-z_]+= take the text before the first '='. It STOPS after taking `note`,
// because note's value is a sentence with spaces in it, so everything after it is prose.
func checkFieldNames(line string) []string {
	field := regexp.MustCompile(`^[a-z_]+=`)
	var names []string
	for _, tok := range strings.Fields(line) {
		if !field.MatchString(tok) {
			continue
		}
		name := tok[:strings.Index(tok, "=")]
		names = append(names, name)
		if name == "note" {
			break
		}
	}
	return names
}

// diffFieldNames returns the names in want that are absent from have, in want's order.
func diffFieldNames(want, have []string) []string {
	seen := make(map[string]bool, len(have))
	for _, n := range have {
		seen[n] = true
	}
	var missing []string
	for _, n := range want {
		if !seen[n] {
			missing = append(missing, n)
		}
	}
	return missing
}
