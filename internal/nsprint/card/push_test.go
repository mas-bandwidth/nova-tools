package card

import (
	"testing"
)

func TestPushRefusesCardWithoutCheckExpect(t *testing.T) {
	// (a) No KIND (so model), no CHECK line: code 2, stderr missing CHECK.
	cardA := "RESULT: ok\nSTEP 1: clone\n"
	if _, err := ExtractCardFields(cardA); err == nil || err.Error() != "missing CHECK" {
		t.Errorf("case (a) expected missing CHECK, got: %v", err)
	}

	// (b) EXPECT: (: EXPECT is not a regexp
	cardB := "RESULT: ok\nSTEP 1: clone\nCHECK: sh check.sh\nEXPECT: (\n"
	if _, err := ExtractCardFields(cardB); err == nil || err.Error() != "EXPECT is not a regexp" {
		t.Errorf("case (b) expected EXPECT is not a regexp, got: %v", err)
	}

	// (c) KIND: fix, CHECK and EXPECT: (?m)^PASS$ valid: code 0
	cardC := "RESULT: ok\nKIND: fix\nSTEP 1: clone\nCHECK: sh check.sh\nEXPECT: (?m)^PASS$\n"
	cf, err := ExtractCardFields(cardC)
	if err != nil {
		t.Errorf("case (c) expected success, got error: %v", err)
	}
	if cf.CheckSha256 == "" || cf.ExpectSha256 == "" {
		t.Errorf("case (c) expected sha256 hashes populated")
	}

	// (d) KIND: fix with CHECK and no EXPECT: missing EXPECT
	cardD := "RESULT: ok\nKIND: fix\nSTEP 1: clone\nCHECK: sh check.sh\n"
	if _, err := ExtractCardFields(cardD); err == nil || err.Error() != "missing EXPECT" {
		t.Errorf("case (d) expected missing EXPECT, got: %v", err)
	}

	// (e) KIND: report, PATHS: none, no CHECK or EXPECT: code 0, no check_sha256
	cardE := "RESULT: ok\nKIND: report\nPATHS: none\nSTEP 1: clone\n"
	cfE, err := ExtractCardFields(cardE)
	if err != nil {
		t.Errorf("case (e) expected success, got error: %v", err)
	}
	if cfE.CheckSha256 != "" {
		t.Errorf("case (e) expected no check_sha256 for exempt report card")
	}

	// (f) KIND: script with TEST: none, no CHECK or EXPECT: code 0, no check_sha256
	cardF := "RESULT: ok\nKIND: script\nTEST: none\nSTEP 1: clone\n"
	cfF, err := ExtractCardFields(cardF)
	if err != nil {
		t.Errorf("case (f) expected success, got error: %v", err)
	}
	if cfF.CheckSha256 != "" {
		t.Errorf("case (f) expected no check_sha256 for exempt script card")
	}
}
