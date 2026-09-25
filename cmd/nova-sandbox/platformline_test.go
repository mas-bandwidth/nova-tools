package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The one rule: a backticked span on the section's `Platform:` line of the bare
// form `<name>=` -- a field name with nothing after the `=` -- is a claim that
// the transcript has no such field, unless the same line also spells that field
// WITH a value somewhere (which is the line's way of saying "this field differs
// by platform"); every remaining claim must be true of what this bench actually
// prints.
func TestThePlatformLineNamesNoFieldThisBenchAlreadyPrints(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	var platform string
	inSection := false
	for _, l := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(l, "## ") {
			inSection = l == "## nova-sandbox"
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(l), "Platform:") {
			if platform != "" {
				t.Fatalf("the nova-sandbox section has more than one Platform: line: %q and %q", platform, l)
			}
			platform = l
		}
	}
	if platform == "" {
		t.Fatal("the nova-sandbox section has no Platform: line to read")
	}

	valued := map[string]bool{}
	var bare []string
	span := regexp.MustCompile("`([^`]*)`")
	valuedRe := regexp.MustCompile(`^[a-z_]+=.+$`)
	bareRe := regexp.MustCompile(`^[a-z_]+=$`)
	for _, m := range span.FindAllStringSubmatch(platform, -1) {
		s := m[1]
		switch {
		case valuedRe.MatchString(s):
			valued[s[:strings.Index(s, "=")]] = true
		case bareRe.MatchString(s):
			bare = append(bare, s[:strings.Index(s, "=")])
		}
	}

	var claims []string
	for _, name := range bare {
		if !valued[name] {
			claims = append(claims, name)
		}
	}
	if len(claims) == 0 {
		t.Fatal("the Platform: line makes no bare `name=` claim; this test would pass by checking nothing")
	}

	var out, errb bytes.Buffer
	if code := run([]string{"check"}, strings.NewReader(""), &out, &errb, os.Environ()); code != 0 {
		t.Fatalf("nova-sandbox check exited %d, want 0; stderr: %s", code, errb.String())
	}
	var printed string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "CHECK OK ") {
			printed = strings.TrimSpace(l)
		}
	}
	if printed == "" {
		t.Fatal("nova-sandbox check printed no CHECK OK line")
	}

	printedFields := map[string]bool{}
	for _, n := range checkFieldNames(printed) {
		printedFields[n] = true
	}
	for _, name := range claims {
		if printedFields[name] {
			t.Errorf("TESTS.md Platform: line calls `%s=` a field this transcript has no slot for, but this bench's check prints `%s=`; the transcript has the slot, so the Platform: line is wrong\n Platform: line: %q\n printed line:  %q", name, name, platform, printed)
		}
	}
}
