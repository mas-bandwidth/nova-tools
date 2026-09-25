package harvest

import (
	"testing"
)

func TestHarvestDueSkipsCheckHeadMismatch(t *testing.T) {
	// Card 1: check_head == pushed_sha (due)
	if !HarvestDue("fix", "abc1234", "abc1234", true, "DONE") {
		t.Errorf("expected due when check_head == pushed_sha")
	}

	// Card 2: check_head != pushed_sha (not due)
	if HarvestDue("fix", "abc1234", "xyz9876", true, "DONE") {
		t.Errorf("expected not due when check_head mismatch")
	}
}
