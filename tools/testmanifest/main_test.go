package main

import (
	"fmt"
	"strings"
	"testing"
)

func testEvent(action, test string) string {
	return fmt.Sprintf("{\"Action\":%q,\"Package\":\"example.test/p\",\"Test\":%q}\n", action, test)
}

func passingStream(names ...string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString(testEvent("run", name))
		b.WriteString(testEvent("pass", name))
	}
	b.WriteString(testEvent("pass", ""))
	return b.String()
}

func TestCheckRequiresOnePassForEveryExactName(t *testing.T) {
	names := []string{"TestOne", "TestTwo"}
	if err := check(strings.NewReader(passingStream(names...)), names, true); err != nil {
		t.Fatalf("passing exact manifest: %v", err)
	}
}

func TestCheckRefusesMissingDuplicateNestedSkipFailAndRedCommand(t *testing.T) {
	names := []string{"TestOne", "TestTwo"}
	base := passingStream(names...)
	tests := []struct {
		name     string
		stream   string
		statusOK bool
		want     string
	}{
		{name: "missing", stream: passingStream("TestOne"), statusOK: true, want: "TestTwo run=0 pass=0"},
		{name: "duplicate pass", stream: base + testEvent("pass", "TestOne"), statusOK: true, want: "TestOne run=1 pass=2"},
		{name: "nested skip", stream: base + testEvent("skip", "TestOne/unsupported"), statusOK: true, want: "nested skip TestOne/unsupported"},
		{name: "nested fail", stream: base + testEvent("fail", "TestTwo/case"), statusOK: true, want: "nested fail TestTwo/case"},
		{name: "nonzero command", stream: base, statusOK: false, want: "go test command exited nonzero"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := check(strings.NewReader(tc.stream), names, tc.statusOK)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestManifestNamesAreUniqueTopLevelGoTests(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{names: []string{"TestOne", "TestOne"}, want: "duplicate manifested test"},
		{names: []string{"TestOne/subtest"}, want: "invalid top-level test name"},
	} {
		if err := validateNames(tc.names); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("validateNames(%q) = %v, want containing %q", tc.names, err, tc.want)
		}
	}
}
