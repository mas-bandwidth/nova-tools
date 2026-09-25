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
// matching ^[a-z0-9_-]+= take the text before the first '='. The class is wider
// than letters because the grammar holds hyphenated and digit names
// (read-noexec=, cwdb64=); a letters-only read drops exactly the mid-line
// insertion this pin exists to catch. It STOPS after taking `note`,
// because note's value is a sentence with spaces in it, so everything after it is prose.
func checkFieldNames(line string) []string {
	field := regexp.MustCompile(`^[a-z0-9_-]+=`)
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

// docs/TESTS.md's nova-sandbox transcript was recorded on a Mac, and the blocks
// underneath its Platform: line drifted from what that Mac prints: the PROBE OK
// block omits gpu=, and the SANDBOX OK block omits read-noexec=, ancestors= and
// gpu=. The CHECK OK pin above compares against a live run; these two lines
// cannot run portably here -- the wall they print is darwin-only -- so they are
// pinned against the format strings in main.go that print them, which have no
// platform branch above them. Field VALUES are the recording machine's and are
// not compared; field NAMES in order are.
func TestTheProbeAndSandboxTranscriptsNameEveryFieldTheVerbsPrint(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]string{}
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
		for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
			if strings.HasPrefix(trimmed, prefix) {
				if documented[prefix] != "" {
					t.Fatalf("the nova-sandbox section has more than one %q line: %q and %q", prefix, documented[prefix], trimmed)
				}
				documented[prefix] = trimmed
			}
		}
	}
	for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
		if documented[prefix] == "" {
			t.Fatalf("TESTS.md has no %q transcript line to pin", prefix)
		}
	}

	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	field := regexp.MustCompile(`^[a-z0-9_-]+=`)
	printed := map[string][]string{}
	for _, line := range strings.Split(string(src), "\n") {
		for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
			i := strings.Index(line, `"`+prefix)
			if i < 0 {
				continue
			}
			rest := line[i+1:]
			end := strings.Index(rest, `"`)
			if end < 0 {
				t.Fatalf("main.go's %q format string is not closed on its line: %q", prefix, line)
			}
			var names []string
			for _, tok := range strings.Fields(rest[:end]) {
				if !field.MatchString(tok) {
					continue
				}
				names = append(names, tok[:strings.Index(tok, "=")])
			}
			if len(printed[prefix]) != 0 {
				t.Fatalf("main.go prints more than one %q line; this test cannot say which one the transcript pins", prefix)
			}
			printed[prefix] = names
		}
	}
	for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
		if len(printed[prefix]) == 0 {
			t.Fatalf("main.go prints no %q line to pin the transcript to", prefix)
		}
		documentedFields := checkFieldNames(documented[prefix])
		if !reflect.DeepEqual(documentedFields, printed[prefix]) {
			missing := diffFieldNames(printed[prefix], documentedFields)
			extra := diffFieldNames(documentedFields, printed[prefix])
			t.Errorf("TESTS.md %q line does not name every field the verb prints\n documented: %q\n documented fields: %v\n printed fields:    %v\n missing from document: %v\n in document but not printed: %v",
				prefix, documented[prefix], documentedFields, printed[prefix], missing, extra)
		}
	}
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
