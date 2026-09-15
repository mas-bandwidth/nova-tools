package main

import (
	"bytes"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"strings"
	"testing"
)

func TestVersionStampAndUsage(t *testing.T) {
	for _, verb := range []string{"version", "--version"} {
		var out, err bytes.Buffer
		if code := update.Main("nova-version", []string{verb}, "v91.2.3", &out, &err); code != 0 || !strings.HasPrefix(out.String(), "nova-version v91.2.3 ") {
			t.Fatalf("%d %s %s", code, out.String(), err.String())
		}
	}
	var out, err bytes.Buffer
	if code := update.Main("nova-version", []string{"version", "extra"}, "v91.2.3", &out, &err); code != 2 {
		t.Fatal(code)
	}
	out.Reset()
	err.Reset()
	if code := update.Main("nova-version", []string{"help"}, "", &out, &err); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "version") {
		t.Fatal("help omitted version")
	}
}

// #406 item 2: the report usage line states what --file is and its shape, so
// an input does not read like an output and a reader learns the file is the
// rule-2 manifest (one tab-separated line per tool) before a run.
func TestVersionReportFileUsageStatesShape(t *testing.T) {
	var out, err bytes.Buffer
	if code := update.Main("nova-version", []string{"help"}, "", &out, &err); code != 0 {
		t.Fatalf("help exit %d: %s", code, err.String())
	}
	for _, want := range []string{"--file <manifest:", "tab-separated fields"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help does not state --file's shape (%q):\n%s", want, out.String())
		}
	}
}
