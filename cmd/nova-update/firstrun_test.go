package main

import (
	"bytes"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"os"
	"strings"
	"testing"
)

func TestExecutableFirstRun(t *testing.T) {
	t.Chdir("../..")
	var banner bytes.Buffer
	update.Main("nova-update", []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-update")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := update.Main("nova-update", strings.Fields(line)[1:], "", &out, &errs)
		if code == 2 {
			t.Fatalf("%s refused: %s", line, errs.String())
		}
	}
	doc, err := os.ReadFile("docs/TESTS.md")
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := onboarding.FirstRun(string(doc), "nova-update")
	if err != nil {
		t.Fatal(err)
	}
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			if c := update.Main("nova-update", strings.Fields(line)[2:], "", &out, &errs); c != 0 {
				t.Fatalf("first run: %d %s", c, errs.String())
			}
		} else if s := firstRunShape(line); s != "" {
			wanted = append(wanted, s)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if s := firstRunShape(line); s != "" {
			actual = append(actual, s)
		}
	}
	if strings.Join(wanted, "\n") != strings.Join(actual, "\n") {
		t.Fatalf("document shape %v differs from run %v", wanted, actual)
	}
}
func TestMissingIndependentFlagsAreNamedTogether(t *testing.T) {
	var out, errs bytes.Buffer
	c := update.Main("nova-update", []string{"report", "--send"}, "", &out, &errs)
	if c != 2 {
		t.Fatal(c)
	}
	for _, flag := range []string{"--file", "--as", "--to", "--bus", "--remote", "--branch"} {
		if !strings.Contains(errs.String(), flag) {
			t.Fatal("missing " + flag + ": " + errs.String())
		}
	}
	if strings.Count(errs.String(), "\n") > 2 {
		t.Fatal("refusal printed a banner")
	}
}

// REPORT has a dynamic timestamp as its second token, unlike two-word events.
// Remove only that value; keep the field and every other output shape check.
func firstRunShape(line string) string {
	if strings.HasPrefix(line, "REPORT at=") {
		fields := strings.Fields(line)
		fields[1] = "at="
		line = strings.Join(fields, " ")
	}
	return onboarding.Shape(line)
}
