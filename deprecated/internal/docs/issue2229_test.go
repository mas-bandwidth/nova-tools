package docs

import (
	"os"
	"strings"
	"testing"
)

// issue2229_test.go proves that docs/SPEC-FLEET-KUBE.md names
// "Make Terraform plan the drift witness via read-each-plan data sources"
// (nova-tools #2229) and lists the five tests the issue demands.

const fleetKubePath = "../../docs/SPEC-FLEET-KUBE.md"

// TestIssue2229 reads docs/SPEC-FLEET-KUBE.md and proves the spec contains
// the read-each-plan data source contract and lists the five tests the issue
// demands. The issue quotes the spec as its receipt:
//
//	"Every managed file is therefore paired with a data source that reads the
//	 host on every plan ... a null_resource is not the drift detector"
func TestIssue2229(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(fleetKubePath)
	if err != nil {
		t.Fatalf("%s: %v", fleetKubePath, err)
	}
	spec := string(body)

	// The spec must state the read-each-plan contract.
	if !strings.Contains(spec, "Every managed file is therefore paired with a data source that reads the host on every plan") {
		t.Errorf("%s: missing the read-each-plan contract line %q; the spec must declare that every managed file is paired with a data source that reads the host on every plan",
			fleetKubePath, "Every managed file is therefore paired with a data source that reads the host on every plan")
	}

	// The spec must state that a null_resource is not the drift detector.
	// The text may span a line break, so check for the key phrase.
	if !strings.Contains(spec, "is not the drift") {
		t.Errorf("%s: missing the phrase %q; the spec must declare that a null_resource is not the drift detector",
			fleetKubePath, "is not the drift")
	}

	// The five tests the issue demands.
	requiredTests := []string{
		"TestEveryManagedFilePairedWithAReadEachPlanDataSource",
		"TestBenchStandardDataSourceFailsPlanOnDisagreement",
		"a-hand-edited-remote-file-is-shown-by-the-plan-not-by-the-declaration",
		"terraform-plan-on-a-fixture-shows-exactly-the-expected-drift",
		"TestDriftLineWhilePlanCleanIsRed",
	}
	for _, name := range requiredTests {
		if !strings.Contains(spec, name) {
			t.Errorf("%s: the spec does not list %q in its \"Tests this spec demands\" section; the issue demands this test name",
				fleetKubePath, name)
		}
	}
}
