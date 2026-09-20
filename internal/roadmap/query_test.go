package roadmap

import (
	"path/filepath"
	"testing"
)

func TestQueryAPIOnSampleRoadmap(t *testing.T) {
	input := `
(
  :schema "test-schema"
  :title "Query Test Roadmap"
  :epics (
    (
      :id "E01"
      :title "Epic One"
      :features (
        (
          :id "E01-F01"
          :title "Feature One"
          :criteria (
            (:id "E01-F01-01" :text "Crit 1" :status "verified" :notes "test 1")
            (:id "E01-F01-02" :text "Crit 2" :status "verified" :notes "test 2")
          )
        )
        (
          :id "E01-F02"
          :title "Feature Two"
          :criteria (
            (:id "E01-F02-01" :text "Crit 3" :status "in-progress" :notes "wip")
            (:id "E01-F02-02" :text "Crit 4" :status "unverified")
          )
        )
      )
    )
    (
      :id "E02"
      :title "Epic Two"
      :features (
        (
          :id "E02-F01"
          :title "Feature Three"
          :criteria (
            (:id "E02-F01-01" :text "Crit 5" :status "unverified")
            (:id "E02-F01-02" :text "Crit 6" :status "in-progress")
          )
        )
      )
    )
  )
)
`
	rm, err := ParseNamed("query.sexp", input)
	if err != nil {
		t.Fatalf("ParseNamed failed: %v", err)
	}

	// 1. Test ListCriteria
	all := rm.ListCriteria()
	if len(all) != 6 {
		t.Fatalf("expected 6 criteria, got %d", len(all))
	}

	// 2. Test ListCriteriaByStatus
	verified := rm.ListCriteriaByStatus(StatusVerified)
	if len(verified) != 2 {
		t.Errorf("expected 2 verified criteria, got %d", len(verified))
	}
	for _, c := range verified {
		if c.Status != StatusVerified {
			t.Errorf("criterion %s has status %v, want verified", c.ID, c.Status)
		}
	}

	unverified := rm.ListCriteriaByStatus(StatusUnverified)
	if len(unverified) != 2 {
		t.Errorf("expected 2 unverified criteria, got %d", len(unverified))
	}

	inProgress := rm.ListCriteriaByStatus(StatusInProgress)
	if len(inProgress) != 2 {
		t.Errorf("expected 2 in-progress criteria, got %d", len(inProgress))
	}

	// Helper methods
	if len(rm.VerifiedCriteria()) != 2 {
		t.Errorf("VerifiedCriteria() returned %d items, want 2", len(rm.VerifiedCriteria()))
	}
	if len(rm.UnverifiedCriteria()) != 2 {
		t.Errorf("UnverifiedCriteria() returned %d items, want 2", len(rm.UnverifiedCriteria()))
	}
	if len(rm.InProgressCriteria()) != 2 {
		t.Errorf("InProgressCriteria() returned %d items, want 2", len(rm.InProgressCriteria()))
	}

	// 3. Test ListCriteriaByEpic
	e01Criteria := rm.ListCriteriaByEpic("E01")
	if len(e01Criteria) != 4 {
		t.Errorf("expected 4 criteria in E01, got %d", len(e01Criteria))
	}
	for _, c := range e01Criteria {
		if c.EpicID != "E01" {
			t.Errorf("expected EpicID E01, got %q", c.EpicID)
		}
	}

	e02Criteria := rm.ListCriteriaByEpic("E02")
	if len(e02Criteria) != 2 {
		t.Errorf("expected 2 criteria in E02, got %d", len(e02Criteria))
	}

	nonExistentEpic := rm.ListCriteriaByEpic("E99")
	if nonExistentEpic != nil {
		t.Errorf("expected nil for non-existent epic, got %v", nonExistentEpic)
	}

	// 4. Test ListCriteriaByFeature
	f01Criteria := rm.ListCriteriaByFeature("E01-F01")
	if len(f01Criteria) != 2 {
		t.Errorf("expected 2 criteria in E01-F01, got %d", len(f01Criteria))
	}
	for _, c := range f01Criteria {
		if c.FeatureID != "E01-F01" {
			t.Errorf("expected FeatureID E01-F01, got %q", c.FeatureID)
		}
	}

	nonExistentFeat := rm.ListCriteriaByFeature("F-NONE")
	if nonExistentFeat != nil {
		t.Errorf("expected nil for non-existent feature, got %v", nonExistentFeat)
	}

	// 5. Test ListFeaturesByEpic
	e01Features := rm.ListFeaturesByEpic("E01")
	if len(e01Features) != 2 {
		t.Errorf("expected 2 features in E01, got %d", len(e01Features))
	}
	if e01Features[0].ID != "E01-F01" || e01Features[1].ID != "E01-F02" {
		t.Errorf("unexpected feature IDs: %s, %s", e01Features[0].ID, e01Features[1].ID)
	}

	// 6. Test Lookups
	epic, ok := rm.GetEpic("E01")
	if !ok || epic.ID != "E01" {
		t.Errorf("GetEpic(E01) failed: ok=%v, epic=%+v", ok, epic)
	}
	_, ok = rm.GetEpic("E99")
	if ok {
		t.Errorf("GetEpic(E99) expected false, got true")
	}

	feat, ok := rm.GetFeature("E01-F02")
	if !ok || feat.ID != "E01-F02" {
		t.Errorf("GetFeature(E01-F02) failed: ok=%v, feat=%+v", ok, feat)
	}
	_, ok = rm.GetFeature("E01-F99")
	if ok {
		t.Errorf("GetFeature(E01-F99) expected false, got true")
	}

	crit, ok := rm.GetCriterion("E01-F01-02")
	if !ok || crit.ID != "E01-F01-02" {
		t.Errorf("GetCriterion(E01-F01-02) failed: ok=%v, crit=%+v", ok, crit)
	}
	_, ok = rm.GetCriterion("E99-F99-99")
	if ok {
		t.Errorf("GetCriterion(E99-F99-99) expected false, got true")
	}

	// 7. Test Overall Progress
	// Total: 6, Verified: 2, InProgress: 2, Unverified: 2
	// Percentage: (2 / 6) * 100 = 33.33%
	prog := rm.Progress()
	if prog.Total != 6 {
		t.Errorf("progress total: got %d, want 6", prog.Total)
	}
	if prog.Verified != 2 {
		t.Errorf("progress verified: got %d, want 2", prog.Verified)
	}
	if prog.InProgress != 2 {
		t.Errorf("progress in-progress: got %d, want 2", prog.InProgress)
	}
	if prog.Unverified != 2 {
		t.Errorf("progress unverified: got %d, want 2", prog.Unverified)
	}
	if prog.Percentage < 33.3 || prog.Percentage > 33.4 {
		t.Errorf("progress percentage: got %.2f, want ~33.33", prog.Percentage)
	}
	if prog.PercentString() != "33.3%" {
		t.Errorf("percent string: got %q, want '33.3%%'", prog.PercentString())
	}

	// 8. Test Epic Progress
	e01Prog, err := rm.EpicProgress("E01")
	if err != nil {
		t.Fatalf("EpicProgress(E01) error: %v", err)
	}
	// E01: 4 criteria, 2 verified, 1 in-progress, 1 unverified -> 50%
	if e01Prog.Total != 4 || e01Prog.Verified != 2 || e01Prog.Percentage != 50.0 {
		t.Errorf("E01 progress: got %+v, want total=4, verified=2, pct=50.0", e01Prog)
	}

	e02Prog, err := rm.EpicProgress("E02")
	if err != nil {
		t.Fatalf("EpicProgress(E02) error: %v", err)
	}
	// E02: 2 criteria, 0 verified, 1 in-progress, 1 unverified -> 0%
	if e02Prog.Total != 2 || e02Prog.Verified != 0 || e02Prog.Percentage != 0.0 {
		t.Errorf("E02 progress: got %+v, want total=2, verified=0, pct=0.0", e02Prog)
	}

	_, err = rm.EpicProgress("E99")
	if err == nil {
		t.Errorf("EpicProgress(E99) expected error for missing epic, got nil")
	}

	// 9. Test Feature Progress
	f01Prog, err := rm.FeatureProgress("E01-F01")
	if err != nil {
		t.Fatalf("FeatureProgress(E01-F01) error: %v", err)
	}
	// E01-F01: 2 criteria, 2 verified -> 100%
	if f01Prog.Total != 2 || f01Prog.Verified != 2 || f01Prog.Percentage != 100.0 {
		t.Errorf("E01-F01 progress: got %+v, want total=2, verified=2, pct=100.0", f01Prog)
	}

	_, err = rm.FeatureProgress("F-NONE")
	if err == nil {
		t.Errorf("FeatureProgress(F-NONE) expected error, got nil")
	}

	// 10. Test Filtered QueryCriteria
	q1 := rm.QueryCriteria(CriteriaFilter{
		EpicID: "E01",
		Status: StatusVerified,
	})
	if len(q1) != 2 {
		t.Errorf("QueryCriteria(Epic=E01, Status=Verified) got %d, want 2", len(q1))
	}

	q2 := rm.QueryCriteria(CriteriaFilter{
		Keyword: "test 1",
	})
	if len(q2) != 1 || q2[0].ID != "E01-F01-01" {
		t.Errorf("QueryCriteria(Keyword='test 1') got %v, want [E01-F01-01]", q2)
	}
}

func TestQueryProgressOnFullRoadmap(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "roadmaps", "nova-work.sexp")
	rm, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Verify E01 epic progress
	e01Prog, err := rm.EpicProgress("E01")
	if err != nil {
		t.Fatalf("EpicProgress(E01) error: %v", err)
	}
	// E01 has features F01 (3/3), F02 (0/3), F03 (1/4), F04 (0/4), F05 (2/3)
	// Total = 3 + 3 + 4 + 4 + 3 = 17 criteria.
	// Verified = 3 + 0 + 1 + 0 + 2 = 6.
	if e01Prog.Total != 17 {
		t.Errorf("E01 total: got %d, want 17", e01Prog.Total)
	}
	if e01Prog.Verified != 6 {
		t.Errorf("E01 verified: got %d, want 6", e01Prog.Verified)
	}

	// Feature E01-F01 has 3/3 verified -> 100%
	f01Prog, err := rm.FeatureProgress("E01-F01")
	if err != nil {
		t.Fatalf("FeatureProgress(E01-F01) error: %v", err)
	}
	if f01Prog.Total != 3 || f01Prog.Verified != 3 || f01Prog.Percentage != 100.0 {
		t.Errorf("E01-F01 progress: got %+v, want total=3, verified=3, pct=100.0", f01Prog)
	}

	// Feature E01-F02 has 0/3 verified -> 0%
	f02Prog, err := rm.FeatureProgress("E01-F02")
	if err != nil {
		t.Fatalf("FeatureProgress(E01-F02) error: %v", err)
	}
	if f02Prog.Total != 3 || f02Prog.Verified != 0 || f02Prog.Percentage != 0.0 {
		t.Errorf("E01-F02 progress: got %+v, want total=3, verified=0, pct=0.0", f02Prog)
	}

	// Test QueryCriteria searching for "restricted"
	matches := rm.QueryCriteria(CriteriaFilter{
		EpicID:  "E01",
		Keyword: "restricted",
	})
	if len(matches) == 0 {
		// Could match title or notes
	}

	// Test verified criteria in E01
	e01Verified := rm.QueryCriteria(CriteriaFilter{
		EpicID: "E01",
		Status: StatusVerified,
	})
	if len(e01Verified) != 6 {
		t.Errorf("expected 6 verified criteria in E01, got %d", len(e01Verified))
	}
}

func TestCalculateProgressZeroTotal(t *testing.T) {
	p := CalculateProgress(0, 0, 0)
	if p.Total != 0 || p.Percentage != 0.0 {
		t.Errorf("unexpected zero progress: %+v", p)
	}
	if p.PercentString() != "0.0%" {
		t.Errorf("unexpected PercentString: %q", p.PercentString())
	}
}
