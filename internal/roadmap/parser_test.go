package roadmap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNovaWorkSexp(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "roadmaps", "nova-work.sexp")
	rm, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if rm.Schema != "nova-work-roadmap-baseline-1" {
		t.Errorf("expected schema 'nova-work-roadmap-baseline-1', got %q", rm.Schema)
	}

	// Verify epics
	if len(rm.Epics) < 11 {
		t.Errorf("expected at least 11 epics, got %d", len(rm.Epics))
	}

	e01, ok := rm.GetEpic("E01")
	if !ok {
		t.Fatalf("epic E01 not found")
	}
	if e01.Title != "Canonical work data and restricted representation" {
		t.Errorf("unexpected E01 title: %q", e01.Title)
	}

	// Verify feature count
	features := 0
	for _, e := range rm.Epics {
		// Only count standard E01..E11 epics for product denominator
		if strings.HasPrefix(e.ID, "E") {
			features += len(e.Features)
		}
	}
	if features != 63 {
		t.Errorf("expected 63 product features, got %d", features)
	}

	// Check feature E01-F01
	f01, ok := rm.GetFeature("E01-F01")
	if !ok {
		t.Fatalf("feature E01-F01 not found")
	}
	if f01.Title != "Restricted Lisp reader and safe syntax" {
		t.Errorf("unexpected E01-F01 title: %q", f01.Title)
	}
	if len(f01.Criteria) != 3 {
		t.Fatalf("expected 3 criteria for E01-F01, got %d", len(f01.Criteria))
	}
	if f01.Criteria[0].ID != "E01-F01-01" {
		t.Errorf("expected criterion ID 'E01-F01-01', got %q", f01.Criteria[0].ID)
	}
	if f01.Criteria[0].Status != StatusVerified {
		t.Errorf("expected E01-F01-01 to be verified, got %v", f01.Criteria[0].Status)
	}
	if !strings.Contains(f01.VerificationNotes, "slice-01-reader.lisp") {
		t.Errorf("expected verification notes to cite slice-01-reader.lisp, got %q", f01.VerificationNotes)
	}

	// Verify criteria parity with tools/roadmap-parity.sh: 231 criteria, 155 verified, 76 unverified
	critList := rm.ListCriteria()
	// Filter to E01..E11 criteria (product denominator)
	var prodCriteria []*Criterion
	for _, c := range critList {
		if strings.HasPrefix(c.EpicID, "E") {
			prodCriteria = append(prodCriteria, c)
		}
	}

	if len(prodCriteria) != 231 {
		t.Errorf("expected 231 product criteria, got %d", len(prodCriteria))
	}

	verified := 0
	for _, c := range prodCriteria {
		if c.Status == StatusVerified {
			verified++
		}
	}
	if verified != 155 {
		t.Errorf("expected 155 verified criteria, got %d", verified)
	}

	unverified := len(prodCriteria) - verified
	if unverified != 76 {
		t.Errorf("expected 76 unverified criteria, got %d", unverified)
	}

	// Verify dependencies on E01-F02
	f02, ok := rm.GetFeature("E01-F02")
	if !ok {
		t.Fatalf("feature E01-F02 not found")
	}
	if len(f02.Dependencies) != 1 || f02.Dependencies[0] != "E01-F01" {
		t.Errorf("expected E01-F02 to depend on E01-F01, got %v", f02.Dependencies)
	}
}

func TestStrictSyntaxValidationAndErrorLocations(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErrSub string
		wantLine   int
		wantCol    int
	}{
		{
			name:       "unclosed list",
			input:      "(:schema \"v1\"\n :epics (\n   (:id \"E01\")\n",
			wantErrSub: "unclosed list starting at line 2, column 9",
			wantLine:   2,
			wantCol:    9,
		},
		{
			name:       "unexpected closing paren",
			input:      "(:schema \"v1\")\n )",
			wantErrSub: "unexpected closing parenthesis ')'",
			wantLine:   2,
			wantCol:    2,
		},
		{
			name:       "unterminated string",
			input:      "(:schema \"v1\"\n :title \"unterminated string\n :epics ())",
			wantErrSub: "unterminated string starting at line 2, column 9",
			wantLine:   2,
			wantCol:    9,
		},
		{
			name:       "dispatch macro forbidden",
			input:      "(:schema \"v1\"\n :tag #.dangerous)",
			wantErrSub: "dispatch macro '#' is forbidden",
			wantLine:   2,
			wantCol:    7,
		},
		{
			name:       "quote forbidden",
			input:      "(:schema \"v1\"\n 'eval-me)",
			wantErrSub: "reader macro or escape character ''' is forbidden",
			wantLine:   2,
			wantCol:    2,
		},
		{
			name:       "backquote forbidden",
			input:      "(:schema \"v1\"\n `eval-me)",
			wantErrSub: "reader macro or escape character '`' is forbidden",
			wantLine:   2,
			wantCol:    2,
		},
		{
			name:       "empty keyword",
			input:      "(:schema \"v1\"\n : )",
			wantErrSub: "empty keyword ':'",
			wantLine:   2,
			wantCol:    2,
		},
		{
			name:       "keyword with duplicate colon",
			input:      "(:schema \"v1\"\n :foo:bar \"val\")",
			wantErrSub: "invalid keyword with duplicate colon ':'",
			wantLine:   2,
			wantCol:    2,
		},
		{
			name:       "odd number of plist elements",
			input:      "(:schema \"v1\" :orphan-key)",
			wantErrSub: "keyword :orphan-key has no associated value",
			wantLine:   1,
			wantCol:    15,
		},
		{
			name:       "epic missing id",
			input:      "(:epics ((:title \"No ID\")))",
			wantErrSub: "epic missing required :id",
			wantLine:   1,
			wantCol:    10,
		},
		{
			name:       "feature missing id",
			input:      "(:epics ((:id \"E01\" :features ((:title \"No Feature ID\")))))",
			wantErrSub: "feature missing required :id",
			wantLine:   1,
			wantCol:    32,
		},
		{
			name:       "criterion missing id",
			input:      "(:epics ((:id \"E01\" :features ((:id \"E01-F01\" :criteria ((:text \"No Crit ID\")))))))",
			wantErrSub: "criterion missing required :id",
			wantLine:   1,
			wantCol:    58,
		},
		{
			name:       "empty input",
			input:      "; only comments\n   \n",
			wantErrSub: "empty roadmap document",
			wantLine:   1,
			wantCol:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNamed("test.sexp", tt.input)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrSub)
			}
			synErr, ok := err.(*SyntaxError)
			if !ok {
				t.Fatalf("expected *SyntaxError, got %T: %v", err, err)
			}

			if !strings.Contains(synErr.Message, tt.wantErrSub) {
				t.Errorf("error message %q does not contain %q", synErr.Message, tt.wantErrSub)
			}
			if synErr.Line != tt.wantLine {
				t.Errorf("line: got %d, want %d", synErr.Line, tt.wantLine)
			}
			if synErr.Column != tt.wantCol {
				t.Errorf("column: got %d, want %d", synErr.Column, tt.wantCol)
			}
			if !strings.Contains(synErr.Error(), "test.sexp:") {
				t.Errorf("Error() formatted string %q missing filename prefix", synErr.Error())
			}
		})
	}
}

func TestDirectCriteriaParsing(t *testing.T) {
	input := `
; Direct criteria specification in features
(
  :schema "test-criteria-v1"
  :title "Test Criteria Roadmap"
  :epics (
    (
      :id "EPIC-01"
      :title "Authentication & Access"
      :status "in-progress"
      :features (
        (
          :id "AUTH-01"
          :title "Token Validation"
          :status "in-progress"
          :depends-on ("CORE-01")
          :verification "auth_test.go: TestTokenValidation"
          :criteria (
            (
              :id "AUTH-01-01"
              :text "Validate signature against public key"
              :status "verified"
              :verification "auth_test.go:25"
              :depends-on ()
            )
            (
              :id "AUTH-01-02"
              :text "Reject expired tokens with 401 Unauthorized"
              :status "in-progress"
              :verification "auth_test.go:50"
              :depends-on ("AUTH-01-01")
            )
            (
              :id "AUTH-01-03"
              :text "Issue refresh token on valid renewal request"
              :status "unverified"
            )
          )
        )
      )
    )
  )
)
`
	rm, err := ParseNamed("auth.sexp", input)
	if err != nil {
		t.Fatalf("ParseNamed failed: %v", err)
	}

	if rm.Title != "Test Criteria Roadmap" {
		t.Errorf("expected title 'Test Criteria Roadmap', got %q", rm.Title)
	}

	epic, ok := rm.GetEpic("EPIC-01")
	if !ok {
		t.Fatalf("epic EPIC-01 not found")
	}
	if epic.Status != StatusInProgress {
		t.Errorf("expected epic status in-progress, got %v", epic.Status)
	}

	feat, ok := rm.GetFeature("AUTH-01")
	if !ok {
		t.Fatalf("feature AUTH-01 not found")
	}
	if len(feat.Dependencies) != 1 || feat.Dependencies[0] != "CORE-01" {
		t.Errorf("unexpected dependencies: %v", feat.Dependencies)
	}
	if len(feat.Criteria) != 3 {
		t.Fatalf("expected 3 criteria, got %d", len(feat.Criteria))
	}

	c1 := feat.Criteria[0]
	if c1.ID != "AUTH-01-01" || c1.Status != StatusVerified || c1.Title != "Validate signature against public key" {
		t.Errorf("unexpected c1: %+v", c1)
	}
	if c1.VerificationNotes != "auth_test.go:25" {
		t.Errorf("unexpected verification notes: %q", c1.VerificationNotes)
	}

	c2 := feat.Criteria[1]
	if c2.ID != "AUTH-01-02" || c2.Status != StatusInProgress {
		t.Errorf("unexpected c2: %+v", c2)
	}
	if len(c2.Dependencies) != 1 || c2.Dependencies[0] != "AUTH-01-01" {
		t.Errorf("unexpected c2 dependencies: %v", c2.Dependencies)
	}

	c3 := feat.Criteria[2]
	if c3.ID != "AUTH-01-03" || c3.Status != StatusUnverified {
		t.Errorf("unexpected c3: %+v", c3)
	}
}

func TestSubfeaturesFallbackToCriteria(t *testing.T) {
	input := `
(
  :epics (
    (
      :id "E01"
      :title "Base Epic"
      :features (
        (
          :id "E01-F01"
          :title "Base Feature"
          :state "verified"
          :subfeatures (
            "First acceptance criterion"
            "Second acceptance criterion"
          )
        )
      )
    )
  )
)
`
	rm, err := ParseNamed("subfeatures.sexp", input)
	if err != nil {
		t.Fatalf("ParseNamed failed: %v", err)
	}

	feat, ok := rm.GetFeature("E01-F01")
	if !ok {
		t.Fatalf("feature E01-F01 not found")
	}

	if len(feat.Criteria) != 2 {
		t.Fatalf("expected 2 criteria synthesized from subfeatures, got %d", len(feat.Criteria))
	}
	if feat.Criteria[0].ID != "E01-F01-01" || feat.Criteria[0].Title != "First acceptance criterion" {
		t.Errorf("unexpected criterion 0: %+v", feat.Criteria[0])
	}
	if feat.Criteria[0].Status != StatusVerified {
		t.Errorf("expected synthesized criterion to inherit feature verified status, got %v", feat.Criteria[0].Status)
	}
	if feat.Criteria[1].ID != "E01-F01-02" || feat.Criteria[1].Title != "Second acceptance criterion" {
		t.Errorf("unexpected criterion 1: %+v", feat.Criteria[1])
	}
}

func TestUnicodeAndCRLFPositionTracking(t *testing.T) {
	// Unicode characters take multiple bytes but count as 1 column each
	// "日本語" is 3 runes (9 bytes)
	input := "; 観測コメント\r\n(:schema \"v1\"\r\n :title \"日本語テスト\"\r\n :tag #.macro)"
	_, err := ParseNamed("unicode.sexp", input)
	if err == nil {
		t.Fatalf("expected error on dispatch macro, got nil")
	}
	synErr, ok := err.(*SyntaxError)
	if !ok {
		t.Fatalf("expected *SyntaxError, got %T", err)
	}
	if synErr.Line != 4 {
		t.Errorf("line: got %d, want 4", synErr.Line)
	}
	if synErr.Column != 7 {
		t.Errorf("column: got %d, want 7", synErr.Column)
	}
}

func TestParseBytesDirectly(t *testing.T) {
	data := []byte(`
(:schema "direct-bytes"
 :epics (
   (:id "EPIC-A"
    :title "Epic A"
    :features (
      (:id "FEAT-A1"
       :title "Feature A1"
       :criteria (
         (:id "CRIT-A1-1" :text "Crit A1" :status "verified")
       )
      )
    )
   )
 )
)`)
	rm, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if rm.Schema != "direct-bytes" {
		t.Errorf("schema: got %q, want 'direct-bytes'", rm.Schema)
	}
	crit, ok := rm.GetCriterion("CRIT-A1-1")
	if !ok || crit.Status != StatusVerified {
		t.Errorf("expected verified criterion CRIT-A1-1, got %+v", crit)
	}
}

func TestNonListEpicsSyntaxError(t *testing.T) {
	input := `(:schema "v1" :epics "not-a-list")`
	_, err := ParseNamed("test.sexp", input)
	if err == nil {
		t.Fatalf("expected error for non-list :epics, got nil")
	}
	if !strings.Contains(err.Error(), ":epics must be a list") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestUnterminatedEscapeInString(t *testing.T) {
	input := `(:schema "unterminated-esc\`
	_, err := ParseNamed("test.sexp", input)
	if err == nil {
		t.Fatalf("expected error for unterminated escape, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated escape sequence") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSymbolWithColonError(t *testing.T) {
	input := `(:schema "v1" :key sym:bad)`
	_, err := ParseNamed("test.sexp", input)
	if err == nil {
		t.Fatalf("expected error for symbol with colon, got nil")
	}
	if !strings.Contains(err.Error(), "symbol cannot contain colon") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNonKeywordInPlistError(t *testing.T) {
	input := `(:schema "v1" "not-a-keyword" "value")`
	_, err := ParseNamed("test.sexp", input)
	if err == nil {
		t.Fatalf("expected error for non-keyword plist key, got nil")
	}
	if !strings.Contains(err.Error(), "expected keyword for property key, got string") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStringEscapeSequences(t *testing.T) {
	input := `(:schema "v1\tline1\nline2\r\"quoted\"\\backslash")`
	rm, err := ParseNamed("escapes.sexp", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "v1\tline1\nline2\r\"quoted\"\\backslash"
	if rm.Schema != want {
		t.Errorf("got %q, want %q", rm.Schema, want)
	}
}

func TestParseStatusNormalizations(t *testing.T) {
	tests := []struct {
		raw  string
		want Status
	}{
		{"verified", StatusVerified},
		{"done", StatusVerified},
		{"closed", StatusVerified},
		{"landed", StatusVerified},
		{"merged", StatusVerified},
		{"green", StatusVerified},
		{"pass", StatusVerified},
		{"in-progress", StatusInProgress},
		{"in_progress", StatusInProgress},
		{"inprogress", StatusInProgress},
		{"doing", StatusInProgress},
		{"live", StatusInProgress},
		{"active", StatusInProgress},
		{"partial", StatusInProgress},
		{"wip", StatusInProgress},
		{"unverified", StatusUnverified},
		{"unmet", StatusUnverified},
		{"missing", StatusUnverified},
		{"todo", StatusUnverified},
		{"open", StatusUnverified},
		{"proposed", StatusUnverified},
		{"blocked", StatusUnverified},
		{"pending", StatusUnverified},
		{"deferred", StatusUnverified},
		{"unknown-custom", StatusUnverified},
	}

	for _, tt := range tests {
		got := ParseStatus(tt.raw)
		if got != tt.want {
			t.Errorf("ParseStatus(%q): got %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestParseFileErrors(t *testing.T) {
	_, err := ParseFile("non_existent_file_12345.sexp")
	if err == nil {
		t.Fatalf("expected error reading non-existent file, got nil")
	}
}

func TestSNodeHelpers(t *testing.T) {
	nodes, err := ParseSExpressions("test.sexp", "(:kw bare-symbol \"str\" 42)")
	if err != nil {
		t.Fatalf("ParseSExpressions failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Kind != NodeList {
		t.Fatalf("expected 1 list node, got %v", nodes)
	}
	list := nodes[0].List
	if !list[0].IsKeyword("kw") || list[0].IsKeyword("other") {
		t.Errorf("IsKeyword check failed")
	}
	if !list[1].IsSymbol("bare-symbol") || list[1].IsSymbol("other") {
		t.Errorf("IsSymbol check failed")
	}
	if list[2].Text() != "str" {
		t.Errorf("Text() got %q, want 'str'", list[2].Text())
	}
	var nilNode *SNode
	if nilNode.Text() != "" || nilNode.IsKeyword("x") || nilNode.IsSymbol("y") {
		t.Errorf("nilNode checks failed")
	}
}


