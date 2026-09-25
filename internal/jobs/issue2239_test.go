package jobs_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

// TestIssue2239 covers docs/SPEC-JOBS.md A4 for a plain unit grant: a unit
// whose lease is past expiry without a termination proof is uncertain, keeps
// its reservation, and is never re-granted until a fence is written.
func TestIssue2239(t *testing.T) {
	t.Parallel()

	// jobs-uncertain-keeps-its-resources: Release is the only thing that frees
	// capacity, and it refuses an uncertain grant. The control is a sibling
	// grant that is not uncertain: the same Release frees it.
	t.Run("jobs-uncertain-keeps-its-resources", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 4})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "unit:uncertain", Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatalf("grant unit:uncertain: %v", err)
		}
		if _, err := a.Grant(jobs.Request{ID: "unit:certain", Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatalf("grant unit:certain: %v", err)
		}
		if err := a.SetUncertain("unit:uncertain"); err != nil {
			t.Fatalf("SetUncertain: %v", err)
		}
		if !a.IsUncertain("unit:uncertain") {
			t.Fatal("unit:uncertain is not marked uncertain")
		}
		if a.IsUncertain("unit:certain") {
			t.Fatal("unit:certain is marked uncertain without being set")
		}

		// Control: the certain grant releases.
		if err := a.Release("unit:certain"); err != nil {
			t.Fatalf("release of a certain grant refused: %v", err)
		}

		// The uncertain grant does not.
		err := a.Release("unit:uncertain")
		if err == nil {
			t.Fatal("Release freed an uncertain grant without a termination proof or fence")
		}
		ref := mustRefusal(t, err)
		if ref.Holder != "unit:uncertain" || !strings.Contains(ref.Reason, "uncertain") {
			t.Errorf("refusal = %+v, want holder unit:uncertain naming the uncertainty", ref)
		}
		if _, ok := a.Held("unit:uncertain"); !ok {
			t.Fatal("refused Release still dropped the uncertain grant")
		}
		if !a.IsUncertain("unit:uncertain") {
			t.Fatal("refused Release lost the uncertain mark")
		}

		// Its 2 cpu still count: a competitor for 3 sees 2 free, where 4
		// would be free had the uncertain grant been released.
		_, err = a.Grant(jobs.Request{ID: "unit:competitor", Vector: jobs.Vector{"cpu": 3}})
		if err == nil {
			t.Fatal("competitor granted capacity an uncertain grant still holds")
		}
		if ref := mustRefusal(t, err); ref.Dim != "cpu" || ref.Free != 2 {
			t.Errorf("competitor refusal dim=%q free=%d, want cpu free=2", ref.Dim, ref.Free)
		}

		// A missing grant cannot be marked.
		if err := a.SetUncertain("unit:nobody"); err == nil {
			t.Fatal("SetUncertain accepted an id with no live grant")
		}
	})

	// jobs-an-uncertain-attempt-is-never-re-granted-on-expiry: the lease is past
	// expiry with no proof. Neither the expiry path (Release) nor a re-grant of
	// the same unit frees or re-issues the reservation; only a fence does, and
	// after it the unit is available again.
	t.Run("jobs-an-uncertain-attempt-is-never-re-granted-on-expiry", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 4})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "unit:tentative", Vector: jobs.Vector{"cpu": 4}}); err != nil {
			t.Fatalf("grant unit:tentative: %v", err)
		}
		if err := a.SetUncertain("unit:tentative"); err != nil {
			t.Fatalf("SetUncertain: %v", err)
		}

		// Before any proof or fence: every path that would re-grant is refused,
		// however many times it is tried.
		for i := 0; i < 3; i++ {
			if err := a.Release("unit:tentative"); err == nil {
				t.Fatalf("attempt %d: expiry Release freed the uncertain grant", i)
			}
			if _, err := a.Grant(jobs.Request{ID: "unit:tentative", Vector: jobs.Vector{"cpu": 4}}); err == nil {
				t.Fatalf("attempt %d: the uncertain unit was re-granted", i)
			}
			if _, err := a.Grant(jobs.Request{ID: "unit:next", Vector: jobs.Vector{"cpu": 1}}); err == nil {
				t.Fatalf("attempt %d: the next unit was granted the uncertain reservation", i)
			}
		}

		// An empty fence resolves nothing.
		if err := a.Fence("unit:tentative", " "); err == nil {
			t.Fatal("an empty fence was accepted")
		}
		if !a.IsUncertain("unit:tentative") {
			t.Fatal("an empty fence cleared the uncertain mark")
		}

		// The fence transition: now, and only now, it releases and re-grants.
		if err := a.Fence("unit:tentative", "fence:1"); err != nil {
			t.Fatalf("Fence: %v", err)
		}
		if a.IsUncertain("unit:tentative") {
			t.Fatal("fenced unit is still uncertain")
		}
		if err := a.Release("unit:tentative"); err != nil {
			t.Fatalf("release after fence: %v", err)
		}
		if _, err := a.Grant(jobs.Request{ID: "unit:tentative", Vector: jobs.Vector{"cpu": 4}}); err != nil {
			t.Fatalf("re-grant after fence and release: %v", err)
		}
	})
}
