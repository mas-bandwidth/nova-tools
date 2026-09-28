package typedrec_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestUnknownKindRefusedOnEveryPath is #3497 Stella 4 item 1 (KIND): every
// reader of a card or RESULT KIND checks it against the declared six. An
// unknown KIND is refused by the parser (in the file and as the card's
// expectation) and by ValidateResultV2.
func TestUnknownKindRefusedOnEveryPath(t *testing.T) {
	t.Parallel()

	report := typedrec.Exemplar(typedrec.KindReport)
	bogus := strings.Replace(report, "KIND: report", "KIND: bogus", 1)
	if bogus == report {
		t.Fatal("report exemplar has no KIND: report line")
	}

	if res := typedrec.ParseResult([]byte(bogus)); res.Valid || res.Field != "KIND" || res.Defect != typedrec.DefectMalformed {
		t.Fatalf("ParseResult KIND: bogus: valid=%v field=%s defect=%s, want KIND malformed", res.Valid, res.Field, res.Defect)
	}
	if res := typedrec.ParseResult([]byte(report), typedrec.ParseOptions{ExpectedKind: "bogus"}); res.Valid || res.Field != "KIND" || res.Defect != typedrec.DefectMalformed {
		t.Fatalf("ParseResult expected kind bogus: valid=%v field=%s defect=%s, want KIND malformed", res.Valid, res.Field, res.Defect)
	}
	if _, err := typedrec.ValidateResultV2(bogus, ""); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateResultV2 KIND: bogus: err=%v, want a KIND refusal", err)
	}
	if _, err := typedrec.ValidateResultV2(report, "bogus"); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateResultV2 card kind bogus: err=%v, want a KIND refusal", err)
	}
	if _, err := typedrec.ValidateResultV2(typedrec.Exemplar(typedrec.KindFix), typedrec.KindReport); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateResultV2 fix record on a report card: err=%v, want a KIND contradiction", err)
	}
}
