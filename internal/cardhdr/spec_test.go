package cardhdr_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

func TestSplitPathPointer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input    string
		wantPath string
		wantLine string
	}{
		{"file.go", "file.go", ""},
		{"file.go:42", "file.go", "42"},
		{"file.go:42-55", "file.go", "42-55"},
		{"file.go:42:10", "file.go", "42:10"},
		{"file.go:L42-L55", "file.go", "L42-L55"},
		{"internal/cardhdr/spec.go:100", "internal/cardhdr/spec.go", "100"},
		{"./internal/cardhdr/spec.go:100-120", "./internal/cardhdr/spec.go", "100-120"},
		{"internal/cardhdr/...", "internal/cardhdr/...", ""},
		{"repo:file.go", "repo:file.go", ""},
		{"repo:file.go:42", "repo:file.go", "42"},
	}

	for _, tc := range cases {
		gotPath, gotLine := cardhdr.SplitPathPointer(tc.input)
		if gotPath != tc.wantPath || gotLine != tc.wantLine {
			t.Errorf("SplitPathPointer(%q) = (%q, %q), want (%q, %q)",
				tc.input, gotPath, gotLine, tc.wantPath, tc.wantLine)
		}
	}
}

func TestParseEvidenceAndReceipts(t *testing.T) {
	t.Parallel()

	if _, err := cardhdr.ParseEvidence(""); err == nil {
		t.Error("ParseEvidence(\"\") want error, got nil")
	}
	if _, err := cardhdr.ParseEvidence("-"); err == nil {
		t.Error("ParseEvidence(\"-\") want error, got nil")
	}
	if v, err := cardhdr.ParseEvidence("bug repro on #4313"); err != nil || v != "bug repro on #4313" {
		t.Errorf("ParseEvidence valid = (%q, %v)", v, err)
	}

	if _, err := cardhdr.ParseReceipts(""); err == nil {
		t.Error("ParseReceipts(\"\") want error, got nil")
	}
	if _, err := cardhdr.ParseReceipts("-"); err == nil {
		t.Error("ParseReceipts(\"-\") want error, got nil")
	}
	if v, err := cardhdr.ParseReceipts("pass"); err != nil || v != "pass" {
		t.Errorf("ParseReceipts valid = (%q, %v)", v, err)
	}

	if rules, _ := cardhdr.ParseRules(""); rules != cardhdr.StandardRules {
		t.Errorf("ParseRules(\"\") = %q, want %q", rules, cardhdr.StandardRules)
	}
	if rules, _ := cardhdr.ParseRules("custom rule"); rules != "custom rule" {
		t.Errorf("ParseRules(\"custom rule\") = %q", rules)
	}
}
